"""GET /api/health — 服务与数据库健康检查。"""

CASE = {"order": 5}


def run(ctx):
    status, body = ctx.api.request("GET", "/api/health", expect=200)
    ctx.equal("健康检查返回 200", status, 200)
    ctx.equal("状态字段为 ok", body.get("status"), "ok")
    return status

if __name__ == "__main__":  # 允许直接运行本脚本：python apitest/cases/api/health/GET.py
    import pathlib
    import sys

    sys.path.insert(0, str(next(p for p in pathlib.Path(__file__).resolve().parents if (p / "run_all.py").exists())))
    from lib.single import main

    main(__file__)
