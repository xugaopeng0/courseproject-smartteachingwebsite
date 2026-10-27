"""DELETE /api/tool-servers/{id} — 删除后该服务器不可再访问。"""


def run(ctx):
    teacher = ctx.get("teacher_token")
    server_id = ctx.require("tool_server_id")
    status, body = ctx.api.request("DELETE", f"/api/tool-servers/{server_id}", token=teacher, expect=200)
    ctx.equal("删除返回 200", status, 200)
    ctx.equal("返回 ok", body.get("ok"), True)
    gone, _ = ctx.api.request("POST", f"/api/tool-servers/{server_id}/connect", token=teacher, expect=404)
    ctx.equal("删除后连接返回 404", gone, 404)
    listed = ctx.api.request("GET", "/api/tool-servers", token=teacher, expect=200)[1].get("servers", [])
    ctx.check("列表不再包含该服务器", all(item["id"] != server_id for item in listed))
    return status

if __name__ == "__main__":  # 允许直接运行本脚本：python apitest/cases/api/tool-servers/{id}/DELETE.py
    import pathlib
    import sys

    sys.path.insert(0, str(next(p for p in pathlib.Path(__file__).resolve().parents if (p / "run_all.py").exists())))
    from lib.single import main

    main(__file__)
