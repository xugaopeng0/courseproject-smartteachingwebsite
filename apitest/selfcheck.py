#!/usr/bin/env python
"""公共库自检：不依赖业务用例，直接验证 client / db / report 三个助手本身。

    apitest/.venv/Scripts/python apitest/selfcheck.py

覆盖：
- Api 的成功调用、401 与 403 异常（异常带状态码与响应体）、SSE 逐事件解析；
- Database 的访问模式（直连可达时用直连；否则打印 compose-exec 命令）与 compose 模式的 SQL 字面量转义；
- report 的渲染与 HTML 转义。
"""

from __future__ import annotations

import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent
sys.path.insert(0, str(ROOT))

from lib.client import Api  # noqa: E402
from lib.db import Database, DatabaseConfig, resolve_database_config  # noqa: E402
from lib.report import Assertion, CaseResult, render  # noqa: E402

PROJECT = ROOT.parent
results: list[tuple[str, bool, str]] = []


def check(name: str, ok: bool, detail: str = "") -> None:
    results.append((name, ok, detail))
    print(f"{'PASS' if ok else 'FAIL'}: {name}{' — ' + detail if detail else ''}")


def load_env() -> dict[str, str]:
    values: dict[str, str] = {}
    path = PROJECT / ".env"
    if path.exists():
        for line in path.read_text(encoding="utf-8").splitlines():
            if "=" in line and not line.strip().startswith("#"):
                key, value = line.split("=", 1)
                values[key.strip()] = value.strip()
    return values


def main() -> int:
    import json as json_module
    import os
    import threading
    from http.server import BaseHTTPRequestHandler, HTTPServer

    import run_all

    env = load_env()
    base_url = os.environ.get("APITEST_BASE_URL") or env.get("APP_ORIGIN") or "http://127.0.0.1:8081"
    api = Api(base_url)

    # 200：健康检查无需登录。
    status, body = api.request("GET", "/api/health", expect=200)
    check("Api 正常调用返回 200", status == 200 and body.get("status") == "ok")

    # 401：未登录访问受保护接口。
    try:
        api.request("GET", "/api/auth/me", expect=200)
        check("未登录应抛异常", False, "没有抛出")
    except Exception as error:  # noqa: BLE001
        check("未登录抛出带状态码的异常", getattr(error, "status", None) == 401, str(error)[:60])

    # 403：带 Cookie 的跨站写请求被同源校验拒绝（不需要登录也能验证这条路径）。
    response = api.raw_request("POST", "/api/auth/logout", headers={"Origin": "http://evil.example"})
    check("跨站写请求返回 403", response.status_code == 403, f"HTTP {response.status_code}")

    # SSE 解析：用本地假事件流单独验证解析器（不依赖业务数据）。
    class SseHandler(BaseHTTPRequestHandler):
        def do_POST(self):  # noqa: N802 - BaseHTTPRequestHandler 的约定
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream; charset=utf-8")
            self.end_headers()
            chunks = [
                'event: delta\ndata: {"text":"\u4f60\u597d"}\n\n',
                ": heartbeat\n\n",
                'event: done\ndata: {"status":"complete"}\n\n',
            ]
            for chunk in chunks:
                self.wfile.write(chunk.encode("utf-8"))
            self.wfile.flush()

        def log_message(self, *args):  # 静默
            return

    server = HTTPServer(("127.0.0.1", 0), SseHandler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    parsed = [(name, payload) for name, payload in Api(f"http://127.0.0.1:{server.server_port}").stream("POST", "/anything")]
    server.shutdown()
    names = [name for name, _ in parsed]
    check("SSE 逐事件解析（含心跳）", names == ["delta", "heartbeat", "done"], str(names))
    check("SSE 载荷解析为 JSON", parsed and parsed[0][1].get("text") == "你好", str(parsed[:1]))

    admin_password, source, probe_status = run_all.resolve_admin_password(env, base_url, "admin")
    check("可用管理员口令可解析", bool(admin_password), source or f"最后一次登录 HTTP {probe_status}")
    token = api.request("POST", "/api/auth/token", body={"username": "admin", "password": admin_password}, expect=200)[1]["token"]
    denied = api.request("POST", "/api/classes", body={"name": "自检班", "description": ""}, token=token, expect=403)
    check("管理员不能创建班级（角色边界）", denied[0] == 403, f"HTTP {denied[0]}")

    # 数据库：探测模式与 SQL 字面量转义。
    config = resolve_database_config({**env, **os.environ}, PROJECT, base_url)
    print(f"      数据库模式：{config.mode}（{config.host or 'docker compose exec'}:{config.port or '-'}）")
    database = Database(config, PROJECT)
    version = database.scalar("SELECT VERSION()")
    check("数据库可查询", bool(version), str(version))
    one = database.query_one("SELECT 1 AS one")
    check("query_one 返回字典", one == {"one": 1}, str(one))
    check("query_all 返回列表", isinstance(database.query_all("SELECT 1 AS one"), list))
    database.close()

    # compose 模式（Compose 不发布 db 端口时的退路）：只验证命令与字面量拼装，不需要 Docker。
    compose_db = Database(DatabaseConfig("", 0, "teaching", "", "teaching", "compose-exec"), PROJECT)
    import lib.db as db_module

    captured: dict[str, str] = {}
    original_run = db_module.subprocess.run

    def fake_run(command, **kwargs):
        captured["command"] = " ".join(command)

        class Result:
            returncode = 0
            stdout = "1\n"
            stderr = ""

        return Result()

    db_module.subprocess.run = fake_run
    try:
        compose_db.query("SELECT COUNT(*) AS c FROM users WHERE username=%s", ("o'brien",))
    finally:
        db_module.subprocess.run = original_run
    check("compose 模式转义单引号", "'o''brien'" in captured.get("command", ""), captured.get("command", "")[-40:])
    check("compose 模式调用容器内客户端", "docker compose exec -T" in captured.get("command", ""))

    # 报告渲染：转义与结构。
    target = ROOT / "report" / "selfcheck.html"
    rendered = render(
        [
            CaseResult(
                path="cases/demo/GET.py",
                method="GET",
                url="/api/demo?x=<script>",
                status=200,
                duration_ms=12,
                assertions=[Assertion("包含 <b>标记</b>", False, "期望 1，实际 2")],
            )
        ],
        {"目标服务": base_url},
        target,
    )
    text = rendered.read_text(encoding="utf-8")
    check("报告是单文件且内联样式", "<style>" in text and "<link" not in text and "<script src" not in text)
    check("报告转义 HTML", "&lt;script&gt;" in text and "<b>标记</b>" not in text)
    check("报告含断言差异", "期望 1，实际 2" in text)
    check("报告标注失败用例", "失败" in text and "demo/GET.py" in text)

    failed = [item for item in results if not item[1]]
    print(f"\n汇总：{len(results) - len(failed)}/{len(results)} 项自检通过")
    return 0 if not failed else 1


if __name__ == "__main__":
    raise SystemExit(main())
