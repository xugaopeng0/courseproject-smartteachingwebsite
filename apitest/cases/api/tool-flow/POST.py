"""端到端：连接自建工具服务 → 保存引用 formula_hint 的技能 → 会话提问 → 记录出现一条成功。

这不是单个接口的用例，而是第 6 课的跨接口流程；目录名 `tool-flow` 只用于展示，
请求地址由用例内部决定。要求环境里有一台可达的 MCP 工具服务（Compose 部署的 mcp-demo）。
"""

QUESTION = "请给出总评公式的构成"


def run(ctx):
    context = ctx.ensure_classroom()
    class_id = context["classroom"]["id"]
    teacher = context["teacher_token"]
    server_id = ctx.require("tool_server_id")
    server_name = ctx.require("tool_server_name")

    # 1) 连接（前一个用例已连接；重复连接同样成功）
    connect = ctx.api.request("POST", f"/api/tool-servers/{server_id}/connect", token=teacher, expect=200)[1]
    ctx.equal("工具服务已连接", connect.get("connected"), True)

    # 2) 保存引用该工具的技能
    skill_name = "带工具技能"
    ref = f"formula_hint@{server_name}"
    view = ctx.api.request(
        "PUT",
        f"/api/classes/{class_id}/skills/{skill_name}",
        body={"body": "需要公式时调用工具。", "enabled": True, "tools": [ref]},
        token=teacher,
        expect=200,
    )[1]
    skill = next((item for item in view.get("skills", []) if item["name"] == skill_name), {})
    ctx.equal("技能保存了工具引用", skill.get("tools"), [ref])

    # 3) 新建会话并发送一条会触发工具的消息
    session = ctx.api.request("POST", f"/api/classes/{class_id}/sessions", body={}, token=teacher, expect=201)[1]
    events = []
    for name, payload in ctx.api.stream(
        "POST",
        f"/api/classes/{class_id}/sessions/{session['id']}/messages",
        body={"content": QUESTION},
        token=teacher,
    ):
        events.append((name, payload))
    names = [name for name, _ in events]
    tool_events = [payload for name, payload in events if name == "tool_call"]
    done = [payload for name, payload in events if name == "done"]
    ctx.check("流中出现工具调用事件", len(tool_events) >= 1, str(names[:6]))
    if "tool_call" in names and "delta" in names:
        ctx.check("工具事件先于正文", names.index("tool_call") < names.index("delta"), str(names[:6]))
    ctx.check(
        "工具调用成功",
        any(item.get("status") == "ok" for item in tool_events),
        str([item.get("status") for item in tool_events]),
    )
    ctx.check("结束元数据带工具", bool(done and done[-1].get("tools")), str(done[-1:]))

    # 4) 记录里出现一条成功，且与流中的一致
    calls = ctx.api.request("GET", f"/api/tool-calls?session_id={session['id']}", token=teacher, expect=200)[1]
    records = calls.get("calls", [])
    ctx.check("记录中出现调用", len(records) >= 1, f"{len(records)} 条")
    record = next((item for item in records if item.get("outcome") == "success"), {})
    ctx.equal("记录的工具名一致", record.get("tool"), "formula_hint")
    ctx.equal("记录的服务器名一致", record.get("server_name"), server_name)
    ctx.equal("记录归属本次会话", record.get("session_id"), session["id"])
    ctx.check(
        "记录带轮次与提问者",
        record.get("turn", 0) >= 1 and bool(record.get("trigger_user")),
        f"turn={record.get('turn')} 提问者={record.get('trigger_user')}",
    )

    # 5) 回答基于工具结果（开发替身把工具结果写进正文）
    answer = "".join(payload.get("text", "") for name, payload in events if name == "delta")
    ctx.check("最终回答非空", bool(answer.strip()), answer[:40])

    # 清理：移除技能工具项，避免影响后续用例
    ctx.api.request("DELETE", f"/api/classes/{class_id}/skills/{skill_name}", token=teacher, expect=200)
    return 200

if __name__ == "__main__":  # 允许直接运行本脚本：python apitest/cases/api/tool-flow/POST.py
    import pathlib
    import sys

    sys.path.insert(0, str(next(p for p in pathlib.Path(__file__).resolve().parents if (p / "run_all.py").exists())))
    from lib.single import main

    main(__file__)
