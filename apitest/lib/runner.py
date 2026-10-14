"""用例发现与执行：目录即 URL、文件名即方法，新增用例不需要改这里。"""

from __future__ import annotations

import importlib.util
import json
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable

from .client import Api
from .db import Database
from .fixtures import Fixtures
from .report import Assertion, CaseResult

METHODS = {"GET", "POST", "PUT", "PATCH", "DELETE"}


@dataclass
class CaseSpec:
    path: Path
    method: str
    url_template: str  # 例如 /api/classes/{id}/knowledge/search
    order: int = 100
    requires: list[str] = field(default_factory=list)
    path_params: dict[str, str] = field(default_factory=dict)
    module: Any = None


class Context:
    """用例共享的上下文：客户端、数据库、临时数据与变量。"""

    def __init__(self, api: Api, db: Database, fixtures: Fixtures, variables: dict[str, Any]):
        self.api = api
        self.db = db
        self.fixtures = fixtures
        self.variables = variables
        self.assertions: list[Assertion] = []
        self._classroom: dict[str, Any] | None = None

    def check(self, name: str, ok: bool, detail: str = "") -> bool:
        self.assertions.append(Assertion(name=name, ok=bool(ok), detail=detail))
        return bool(ok)

    def equal(self, name: str, actual: Any, expected: Any) -> bool:
        return self.check(name, actual == expected, f"期望 {expected!r}，实际 {actual!r}")

    def set(self, key: str, value: Any) -> None:
        self.variables[key] = value

    def get(self, key: str, default: Any = None) -> Any:
        return self.variables.get(key, default)

    def require(self, key: str) -> Any:
        if key not in self.variables:
            raise RuntimeError(f"前置变量 {key} 未就绪")
        return self.variables[key]

    def ensure_classroom(self) -> dict[str, Any]:
        """按需创建「教师 + 班级 + 已索引讲义」，供所有班级范围的用例复用。"""
        if self._classroom is not None:
            return self._classroom
        admin_token = self.get("admin_token") or self.fixtures.admin_token()
        self.set("admin_token", admin_token)
        teacher = self.fixtures.create_teacher(admin_token)
        teacher_token = self.fixtures.token_for(teacher["username"], teacher["password"])
        self.fixtures.teacher_token = teacher_token
        classroom = self.fixtures.create_class(teacher_token)
        handout = self.fixtures.upload_handout(teacher_token, classroom["id"])
        status = self.fixtures.wait_indexed(teacher_token, classroom["id"], handout["id"])
        if status != "ready":
            raise RuntimeError(f"讲义索引未就绪：{status}")
        # 文档状态 ready 与实际可检索之间有极短的时间差，先探针确认再开查。
        if not self.fixtures.wait_searchable(teacher_token, classroom["id"], "考核方式"):
            raise RuntimeError("讲义已就绪但检索不到内容")
        self._classroom = {
            "teacher": teacher,
            "teacher_token": teacher_token,
            "classroom": classroom,
            "handout": handout,
        }
        self.set("class_id", classroom["id"])
        return self._classroom


def discover(root: Path) -> list[CaseSpec]:
    """遍历 cases/**/<METHOD>.py，把目录层级还原成 URL。"""
    cases: list[CaseSpec] = []
    for file in sorted((root / "cases").rglob("*.py")):
        if file.name.startswith("_") or file.name == "__init__.py":
            continue
        method = file.stem.upper()
        if method not in METHODS:
            raise RuntimeError(f"用例文件名必须是 HTTP 方法：{file}")
        relative = file.parent.relative_to(root / "cases")
        url = "/" + str(relative).replace("\\", "/")
        spec = CaseSpec(path=file, method=method, url_template=url)
        config_path = file.parent / "case.json"
        if config_path.exists():
            config = json.loads(config_path.read_text(encoding="utf-8"))
            spec.order = int(config.get("order", 100))
            spec.requires = list(config.get("requires", []))
            spec.path_params = dict(config.get("path_params", {}))
        spec.module = _load_module(file)
        cases.append(spec)
    cases.sort(key=lambda item: (item.order, item.url_template))
    return cases


def _load_module(path: Path) -> Any:
    module_name = "apitest_case_" + "_".join(path.parts[-4:]).replace(".py", "").replace("-", "_")
    spec = importlib.util.spec_from_file_location(module_name, path)
    if spec is None or spec.loader is None:
        raise RuntimeError(f"无法加载用例：{path}")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def resolve_url(spec: CaseSpec, ctx: Context) -> str:
    url = spec.url_template
    for name, reference in spec.path_params.items():
        key = reference[1:] if reference.startswith("$") else reference
        value = ctx.get(key)
        if value is None:
            raise RuntimeError(f"路径参数 {name} 依赖的变量 {key} 未就绪")
        url = url.replace("{" + name + "}", str(value))
    if "{" in url:
        raise RuntimeError(f"用例 {spec.path} 仍有未解析的占位符：{url}")
    return url


def run_case(spec: CaseSpec, ctx: Context) -> CaseResult:
    started = time.time()
    url = spec.url_template
    status: int | None = None
    error = ""
    try:
        # setup 先跑：班级范围的用例在这里按需准备「教师 + 班级 + 已索引讲义」，
        # 之后路径参数才解析得出来。
        setup = getattr(spec.module, "setup", None)
        if callable(setup):
            setup(ctx)
        # 先查前置再解析路径参数：前置缺失时报「前置未满足」，语义比「占位符无法解析」更清楚。
        for requirement in spec.requires:
            if requirement not in ctx.variables:
                raise RuntimeError(f"前置未满足：{requirement}")
        url = resolve_url(spec, ctx)
        result = spec.module.run(ctx)
        if isinstance(result, tuple):  # 用例可以顺带返回状态码
            status = result[0]
        elif isinstance(result, int):
            status = result
    except Exception as failure:  # noqa: BLE001 - 用例失败要记录，不中断整套运行
        error = f"{type(failure).__name__}: {failure}"
    duration = int((time.time() - started) * 1000)
    return CaseResult(
        path=str(spec.path.relative_to(spec.path.parents[3])),
        method=spec.method,
        url=url,
        status=status,
        duration_ms=duration,
        assertions=list(ctx.assertions),
        error=error,
    )


def run_all(specs: list[CaseSpec], make_context: Callable[[], Context], teardown: Callable[[Context], list[str]]) -> list[CaseResult]:
    results: list[CaseResult] = []
    ctx = make_context()
    for spec in specs:
        ctx.assertions = []
        result = run_case(spec, ctx)
        results.append(result)
        state = "PASS" if result.ok else "FAIL"
        print(f"  [{state}] {spec.method} {spec.url_template}（{result.duration_ms} ms）")
        if not result.ok:
            if result.error:
                print(f"        异常：{result.error}")
            for assertion in result.assertions:
                if not assertion.ok:
                    print(f"        断言失败：{assertion.name} {assertion.detail}")
    problems = teardown(ctx)
    for problem in problems:
        print(f"  [WARN] {problem}")
    return results
