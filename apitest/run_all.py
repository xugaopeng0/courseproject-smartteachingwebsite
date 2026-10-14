#!/usr/bin/env python
"""一键运行 apitest 下的全部接口用例，并生成自包含 HTML 报告。

用法：
    python apitest/run_all.py                      # 全部用例
    python apitest/run_all.py --filter sessions    # 只跑路径包含 sessions 的用例
    python apitest/run_all.py --base-url http://127.0.0.1:8081
    python apitest/run_all.py --no-report

环境变量：
    APITEST_BASE_URL    被测服务地址（默认取仓库 .env 的 APP_ORIGIN，再退回 http://127.0.0.1:8081）
    APITEST_DB_HOST/PORT/USER/PASSWORD/NAME   显式指定数据库；不填则自动探测（直连或 docker compose exec）
"""

from __future__ import annotations

import argparse
import os
import sys
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parent
PROJECT = ROOT.parent
sys.path.insert(0, str(ROOT))

REPORT_PATH = ROOT / "report" / "index.html"

INSTALL_HINT = """请先安装依赖后重试：
  python -m venv apitest/.venv
  apitest/.venv/Scripts/pip install -r apitest/requirements.txt   # Windows
  apitest/.venv/bin/pip install -r apitest/requirements.txt       # Linux / macOS"""


def load_env() -> dict[str, str]:
    values: dict[str, str] = {}
    path = PROJECT / ".env"
    if not path.exists():
        return values
    for line in path.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        values[key.strip()] = value.strip()
    return values


def missing_dependencies() -> list[str]:
    missing = []
    for module in ("requests", "pymysql"):
        try:
            __import__("requests" if module == "requests" else "pymysql")
        except ImportError:
            missing.append(module)
    return missing


def resolve_admin_password(env: dict[str, str], base_url: str, username: str) -> tuple[str, str, int]:
    """探测可用的管理员口令：显式环境变量 → .env → 本地 dev:local 凭据。

    三种来源都可能存在（Compose 用 .env、dev:local 用 .local/dev-credentials.json），
    因此用一次登录请求确认哪一个当前有效，并把来源与最后一次状态码一起返回，
    以便区分「口令都不对」与「触发登录限流 429」。
    """
    import json as json_module

    import requests as requests_module

    candidates: list[tuple[str, str]] = []
    if os.environ.get("APITEST_ADMIN_PASSWORD"):
        candidates.append((os.environ["APITEST_ADMIN_PASSWORD"], "APITEST_ADMIN_PASSWORD"))
    if env.get("ADMIN_PASSWORD"):
        candidates.append((env["ADMIN_PASSWORD"], ".env 的 ADMIN_PASSWORD"))
    dev_credentials = PROJECT / ".local" / "dev-credentials.json"
    if dev_credentials.exists():
        try:
            value = json_module.loads(dev_credentials.read_text(encoding="utf-8")).get("admin", "")
        except json_module.JSONDecodeError:
            value = ""
        if value:
            candidates.append((value, ".local/dev-credentials.json"))
    last_status = 0
    for password, source in candidates:
        try:
            response = requests_module.post(
                f"{base_url.rstrip('/')}/api/auth/login",
                json={"username": username, "password": password},
                timeout=15,
            )
        except requests_module.RequestException:
            last_status = -1
            continue
        if response.status_code == 200:
            return password, source, 200
        last_status = response.status_code
        if last_status == 429:
            break
    return "", "", last_status


def main() -> int:
    parser = argparse.ArgumentParser(description="接口测试套件")
    parser.add_argument("--base-url", help="被测服务地址，例如 http://127.0.0.1:8081")
    parser.add_argument("--filter", default="", help="只运行 URL 或文件路径包含该字符串的用例")
    parser.add_argument("--no-report", action="store_true", help="不生成 HTML 报告")
    args = parser.parse_args()

    missing = missing_dependencies()
    if missing:
        print("缺少依赖：" + "、".join(missing))
        print(INSTALL_HINT)
        return 2

    # 依赖自检通过后再导入公共库：否则缺少依赖时会直接抛 ImportError，而不是给出安装提示。
    from lib.client import Api  # noqa: PLC0415
    from lib.db import Database, resolve_database_config  # noqa: PLC0415
    from lib.fixtures import Fixtures  # noqa: PLC0415
    from lib.report import render  # noqa: PLC0415
    from lib.runner import Context, discover, run_all  # noqa: PLC0415

    env = load_env()
    base_url = (
        (args.base_url or "").strip()
        or os.environ.get("APITEST_BASE_URL", "").strip()
        or env.get("APP_ORIGIN", "").strip()
        or "http://127.0.0.1:8081"
    )
    admin_username = os.environ.get("APITEST_ADMIN_USERNAME") or env.get("ADMIN_USERNAME", "admin")
    print(f"目标服务：{base_url}")
    print(f"管理员账号：{admin_username}（按 APITEST_ADMIN_PASSWORD → .env → .local/dev-credentials.json 依次探测口令）")
    admin_password, password_source, probe_status = resolve_admin_password(env, base_url, admin_username)
    if not admin_password:
        if probe_status == 429:
            print("登录被限流（HTTP 429：同一 IP 10 分钟内最多 40 次登录/注册/签发）。")
            print("请等待限流窗口结束后重试；本地 dev:local 重启 API 即可清零计数（计数在进程内存中）。")
        else:
            print("缺少可用的管理员口令（最后一次登录尝试 HTTP %d）。" % probe_status)
            print("请在仓库 .env 中设置 ADMIN_PASSWORD（scripts/setup.ps1 会生成），")
            print("或显式提供 APITEST_ADMIN_PASSWORD；本地 dev:local 的口令在 .local/dev-credentials.json。")
        return 2

    config = resolve_database_config({**env, **os.environ}, PROJECT, base_url)
    database = Database(config, PROJECT)
    api = Api(base_url)
    fixtures = Fixtures(api, database, admin_username, admin_password)

    specs = discover(ROOT)
    if args.filter:
        needle = args.filter.replace("\\", "/")
        specs = [
            item
            for item in specs
            if needle in item.url_template or needle in str(item.path).replace("\\", "/")
        ]
    if not specs:
        print("没有匹配的用例。")
        return 2

    print(f"管理员口令来源：{password_source}")
    print(f"数据库核对：{config.mode}（{config.host or 'docker compose exec'}:{config.port or '-'}）")
    print(f"用例数量：{len(specs)}")

    started = time.time()
    results = run_all(specs, lambda: Context(api, database, fixtures, {}), lambda ctx: fixtures.cleanup())
    duration = time.time() - started
    passed = sum(1 for item in results if item.ok)
    failed = len(results) - passed

    environment = {
        "目标服务": base_url,
        "管理员口令来源": password_source,
        "数据库核对方式": f"{config.mode}（{config.host or 'docker compose exec db'}）",
        "用例数量": f"{len(results)}（通过 {passed}，失败 {failed}）",
        "总耗时": f"{duration:.1f} 秒",
        "Python": sys.version.split()[0],
    }
    if not args.no_report:
        path = render(results, environment, REPORT_PATH)
        print(f"报告：{path.relative_to(PROJECT)}")
    print(f"\n汇总：{passed}/{len(results)} 用例通过，耗时 {duration:.1f} 秒")
    database.close()
    return 0 if failed == 0 else 1


if __name__ == "__main__":
    raise SystemExit(main())
