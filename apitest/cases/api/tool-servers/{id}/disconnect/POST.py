"""POST /api/tool-servers/{id}/disconnect — 断开清空清单，且此后引用被拒。"""


def run(ctx):
    teacher = ctx.get("teacher_token")
    server_id = ctx.get("tool_server_id")
    if not teacher or not server_id:
        context = ctx.ensure_classroom()
        teacher = context["teacher_token"]
        ctx.set("teacher_token", teacher)
        raise RuntimeError("前置未满足：tool_server_id")

    status, body = ctx.api.request("POST", f"/api/tool-servers/{server_id}/disconnect", token=teacher, expect=200)
    ctx.equal("断开返回 200", status, 200)
    ctx.equal("状态变为未连接", body.get("connected"), False)
    ctx.equal("清单被清空", body.get("tools"), [])
    again, _ = ctx.api.request("POST", f"/api/tool-servers/{server_id}/disconnect", token=teacher, expect=200)
    ctx.equal("重复断开幂等", again, 200)
    return status

if __name__ == "__main__":  # 允许直接运行本脚本：python apitest/cases/api/tool-servers/{id}/disconnect/POST.py
    import pathlib
    import sys

    sys.path.insert(0, str(next(p for p in pathlib.Path(__file__).resolve().parents if (p / "run_all.py").exists())))
    from lib.single import main

    main(__file__)
