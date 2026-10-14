"""POST /api/classes/{id}/sessions — 新建会话并核对会话行落库。"""


def setup(ctx):
    ctx.ensure_classroom()


def run(ctx):
    context = ctx.ensure_classroom()
    class_id = context["classroom"]["id"]
    token = context["teacher_token"]

    status, body = ctx.api.request("POST", f"/api/classes/{class_id}/sessions", body={}, token=token, expect=201)
    ctx.equal("新建会话返回 201", status, 201)
    session_id = body.get("id")
    ctx.check("返回会话标识", bool(session_id))
    ctx.equal("会话归属该班", body.get("class_id"), class_id)
    ctx.check("标记为本人创建", body.get("mine") is True)
    ctx.set("session_id", session_id)

    row = ctx.db.query("SELECT class_id, user_id, title FROM chat_sessions WHERE id=%s", (session_id,))
    ctx.check("会话行已落库", len(row) == 1, f"rows={len(row)}")
    if row:
        ctx.equal("数据库中的班级一致", row[0]["class_id"], class_id)

    list_status, listed = ctx.api.request("GET", f"/api/classes/{class_id}/sessions", token=token, expect=200)
    ctx.equal("会话列表返回 200", list_status, 200)
    ids = [item["id"] for item in listed.get("sessions", [])]
    ctx.check("列表包含新会话", session_id in ids, f"共 {len(ids)} 个")

    bad_status, _ = ctx.api.request("POST", f"/api/classes/{class_id}/sessions", body={"title": "标" * 61}, token=token, expect=400)
    ctx.equal("超长标题返回 400", bad_status, 400)
    return status

if __name__ == "__main__":  # 允许直接运行本脚本：python apitest/cases/api/classes/{id}/sessions/POST.py
    import pathlib
    import sys

    sys.path.insert(0, str(next(p for p in pathlib.Path(__file__).resolve().parents if (p / "run_all.py").exists())))
    from lib.single import main

    main(__file__)
