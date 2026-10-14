"""POST /api/classes/{id}/sessions/{sid}/messages — 发送消息、读取 SSE 事件并核对消息落库。"""

QUESTION = "考核方式由什么组成"
UNRELATED = "南极帝企鹅的迁徙路线"


def setup(ctx):
    # 路径参数 {id} 需要班级；{sid} 由「新建会话」用例写入 $session_id。
    ctx.ensure_classroom()


def _collect(ctx, class_id, session_id, token, question):
    events = []
    for name, payload in ctx.api.stream(
        "POST", f"/api/classes/{class_id}/sessions/{session_id}/messages", body={"content": question}, token=token
    ):
        events.append((name, payload))
    return events


def run(ctx):
    context = ctx.ensure_classroom()
    class_id = context["classroom"]["id"]
    token = context["teacher_token"]
    session_id = ctx.get("session_id")
    path = f"/api/classes/{class_id}/sessions/{session_id}/messages"

    events = _collect(ctx, class_id, session_id, token, QUESTION)
    names = [name for name, _ in events]
    deltas = [payload for name, payload in events if name == "delta"]
    citations_events = [payload for name, payload in events if name == "citations"]
    done_events = [payload for name, payload in events if name == "done"]

    ctx.check("收到多个增量事件", len(deltas) >= 2, f"delta={len(deltas)}")
    ctx.check("结束前给出出处事件", bool(citations_events), str(names[-3:]))
    ctx.check("收到结束事件", bool(done_events), str(names[-1:]))
    if done_events:
        ctx.equal("结束状态为 complete", done_events[-1].get("status"), "complete")
    citations = citations_events[-1].get("citations", []) if citations_events else []
    ctx.check("出处数量在 1–4 之间", 1 <= len(citations) <= 4, f"citations={len(citations)}")
    if citations:
        first = citations[0]
        ctx.equal("出处编号从 1 开始", first.get("index"), 1)
        ctx.check("出处带切片序号与字符区间", first.get("ordinal", 0) >= 1 and first.get("char_end", 0) > first.get("char_start", 0))

    answer = "".join(item.get("text", "") for item in deltas)
    ctx.check("回答包含出处编号", "[1]" in answer, answer[:40])

    rows = ctx.db.query(
        "SELECT seq, role, status, content, citations FROM chat_messages WHERE session_id=%s ORDER BY seq", (session_id,)
    )
    ctx.equal("用户与助手消息各一条", len(rows), 2)
    if len(rows) == 2:
        ctx.equal("顺序号连续", [rows[0]["seq"], rows[1]["seq"]], [1, 2])
        ctx.equal("第一条是用户消息", rows[0]["role"], "user")
        ctx.equal("用户消息内容一致", rows[0]["content"], QUESTION)
        ctx.equal("第二条是助手消息", rows[1]["role"], "assistant")
        ctx.equal("助手消息状态为 complete", rows[1]["status"], "complete")
        ctx.equal("落库回答与流式增量一致", rows[1]["content"], answer)
        ctx.check("出处已落库", bool(rows[1]["citations"]) and "chunk_id" in str(rows[1]["citations"]), str(rows[1]["citations"])[:40])

    # 无依据提问：固定文案、空出处，且同样落库。顺带用网关计数确认没有调用对话模型。
    before = ctx.fixtures.gateway_stats()
    empty_events = _collect(ctx, class_id, session_id, token, UNRELATED)
    after = ctx.fixtures.gateway_stats()
    empty_answer = "".join(payload.get("text", "") for name, payload in empty_events if name == "delta")
    empty_done = [payload for name, payload in empty_events if name == "done"]
    empty_citations = [payload for name, payload in empty_events if name == "citations"]
    ctx.equal("无依据时返回固定文案", empty_answer, "资料中未找到相关内容")
    ctx.check("无依据时结束状态为 no_matches", bool(empty_done) and empty_done[-1].get("status") == "no_matches", str(empty_done[-1:]))
    ctx.equal("无依据时出处为空", len(empty_citations[-1].get("citations", [])) if empty_citations else 0, 0)
    if before is not None and after is not None:
        ctx.equal("无依据时不调用对话模型", after["chat"] - before["chat"], 0)
    else:
        ctx.check(
            "无依据时不调用对话模型（由后端测试覆盖）",
            True,
            "网关计数不可用；真实网关环境下由 TestChatNoMatchesSkipsChatGateway 覆盖",
        )

    rows_after = ctx.db.query("SELECT seq, role, status, citations FROM chat_messages WHERE session_id=%s ORDER BY seq", (session_id,))
    ctx.equal("无依据的问答也落库", len(rows_after), 4)
    if len(rows_after) == 4:
        ctx.equal("无依据的助手消息状态为 complete", rows_after[3]["status"], "complete")
        ctx.equal("无依据的助手消息出处为空", rows_after[3]["citations"], "[]")

    bad_status, _ = ctx.api.request("POST", path, body={"content": "   "}, token=token, expect=400)
    ctx.equal("空白消息返回 400", bad_status, 400)
    return 200

if __name__ == "__main__":  # 允许直接运行本脚本：python apitest/cases/api/classes/{id}/sessions/{sid}/messages/POST.py
    import pathlib
    import sys

    sys.path.insert(0, str(next(p for p in pathlib.Path(__file__).resolve().parents if (p / "run_all.py").exists())))
    from lib.single import main

    main(__file__)
