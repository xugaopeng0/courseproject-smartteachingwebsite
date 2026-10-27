"""POST /api/tool-servers/{id}/connect — 握手 + 工具发现；不可达时停在未连接。"""

import os


def run(ctx):
    teacher = ctx.get("teacher_token")
    if not teacher:
        context = ctx.ensure_classroom()
        teacher = context["teacher_token"]
        ctx.set("teacher_token", teacher)
    server_id = ctx.require("tool_server_id")

    status, body = ctx.api.request("POST", f"/api/tool-servers/{server_id}/connect", token=teacher, expect=200)
    ctx.equal("连接返回 200", status, 200)
    ctx.equal("标记为已连接", body.get("connected"), True)
    tools = body.get("tools", [])
    names = sorted(item.get("name") for item in tools)
    ctx.equal("发现两个工具", names, ["formula_hint", "unit_convert"])
    ctx.check(
        "工具带说明与输入结构",
        all(item.get("description") and isinstance(item.get("input_schema"), dict) for item in tools),
    )
    # 连续连接两次：清单不翻倍。
    again, second = ctx.api.request("POST", f"/api/tool-servers/{server_id}/connect", token=teacher, expect=200)
    ctx.equal("重复连接仍 200", again, 200)
    ctx.equal("清单不翻倍", len(second.get("tools", [])), len(tools))

    # 失败路径：登记一台不可达的服务器并连接，应停在未连接且给出原因。
    broken = ctx.fixtures.register_tool_server(teacher, "apit-broken-" + os.urandom(3).hex(), "http://127.0.0.1:1")
    failed, payload = ctx.api.request("POST", f"/api/tool-servers/{broken['id']}/connect", token=teacher, expect=502)
    ctx.equal("不可达时返回 502", failed, 502)
    ctx.check("失败原因可读", bool(payload.get("error")), str(payload.get("error"))[:60])
    listed = ctx.api.request("GET", "/api/tool-servers", token=teacher, expect=200)[1].get("servers", [])
    target = next((item for item in listed if item["id"] == broken["id"]), {})
    ctx.equal("失败后仍为未连接", target.get("connected"), False)
    ctx.check("记录了失败原因", bool(target.get("last_error")), str(target.get("last_error"))[:60])
    ctx.equal("失败后清单为空", target.get("tools"), [])
    return status

if __name__ == "__main__":  # 允许直接运行本脚本：python apitest/cases/api/tool-servers/{id}/connect/POST.py
    import pathlib
    import sys

    sys.path.insert(0, str(next(p for p in pathlib.Path(__file__).resolve().parents if (p / "run_all.py").exists())))
    from lib.single import main

    main(__file__)
