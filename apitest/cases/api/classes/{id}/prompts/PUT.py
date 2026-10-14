"""PUT /api/classes/{id}/prompts — 保存班级提示词、超长被拒、技能可覆盖，并核对数据库。"""

NEW_PROMPT = "回答只用中文，先给结论，再给依据。"
SKILL_NAME = "逐步提示"


def setup(ctx):
    ctx.ensure_classroom()


def run(ctx):
    context = ctx.ensure_classroom()
    class_id = context["classroom"]["id"]
    token = context["teacher_token"]

    status, body = ctx.api.request(
        "PUT", f"/api/classes/{class_id}/prompts", body={"body": NEW_PROMPT}, token=token, expect=200
    )
    ctx.equal("保存班级提示词返回 200", status, 200)
    ctx.equal("来源变为班级", body.get("prompt_source"), "class")
    ctx.check("回显正文", NEW_PROMPT in str(body.get("prompt_body")), str(body.get("prompt_body"))[:30])

    stored = ctx.db.scalar("SELECT body FROM prompts WHERE class_id=%s AND kind='prompt'", (class_id,))
    ctx.check("数据库中的班级提示词与提交一致", stored == NEW_PROMPT, str(stored)[:40])

    too_long_status, _ = ctx.api.request(
        "PUT", f"/api/classes/{class_id}/prompts", body={"body": "字" * 2001}, token=token, expect=400
    )
    ctx.equal("超长提示词返回 400", too_long_status, 400)
    after = ctx.db.scalar("SELECT body FROM prompts WHERE class_id=%s AND kind='prompt'", (class_id,))
    ctx.equal("被拒后原内容不变", after, NEW_PROMPT)

    skill_status, skill_body = ctx.api.request(
        "PUT", f"/api/classes/{class_id}/skills/{SKILL_NAME}", body={"body": "每次只提示一个小步骤。", "enabled": True}, token=token, expect=200
    )
    ctx.equal("新增班级技能返回 200", skill_status, 200)
    names = [item.get("name") for item in skill_body.get("skills", [])]
    ctx.check("新技能出现在状态中", SKILL_NAME in names, str(names))
    skill_row = ctx.db.query("SELECT scope, enabled FROM prompts WHERE class_id=%s AND kind='skill' AND name=%s", (class_id, SKILL_NAME))
    ctx.check("技能按班级级落库", bool(skill_row) and skill_row[0]["scope"] == "class" and skill_row[0]["enabled"] == 1, str(skill_row[:1]))

    disable_status, disabled = ctx.api.request(
        "PUT", f"/api/classes/{class_id}/skills/{SKILL_NAME}", body={"enabled": False}, token=token, expect=200
    )
    ctx.equal("停用技能返回 200", disable_status, 200)
    entry = next((item for item in disabled.get("skills", []) if item["name"] == SKILL_NAME), {})
    ctx.check("停用后状态为未启用", entry.get("enabled") is False, str(entry))

    remove_status, removed = ctx.api.request("DELETE", f"/api/classes/{class_id}/skills/{SKILL_NAME}", token=token, expect=200)
    ctx.equal("移除班级技能返回 200", remove_status, 200)
    ctx.check("移除后不再出现", SKILL_NAME not in [item["name"] for item in removed.get("skills", [])])

    # 学生只读：注册一个学生并加入班级后，写提示词应被拒绝。
    student_name = "apits" + context["classroom"]["id"][:6]
    student_password = "student-pass-123456"
    ctx.api.request("POST", "/api/auth/register", body={"username": student_name, "name": "接口测试学生", "password": student_password}, expect=200)
    student_id = ctx.db.scalar("SELECT id FROM users WHERE username=%s", (student_name,))
    ctx.db.query("INSERT IGNORE INTO memberships(class_id,user_id) VALUES(%s,%s)", (class_id, student_id))
    ctx.fixtures.created_users.append(student_id)
    student_token = ctx.fixtures.token_for(student_name, student_password)
    read_status, read_body = ctx.api.request("GET", f"/api/classes/{class_id}/prompts", token=student_token, expect=200)
    ctx.equal("学生可读取", read_status, 200)
    ctx.check("学生不可编辑", read_body.get("can_edit") is False)
    deny_status, _ = ctx.api.request("PUT", f"/api/classes/{class_id}/prompts", body={"body": "学生不能改"}, token=student_token, expect=403)
    ctx.equal("学生写入返回 403", deny_status, 403)
    return status

if __name__ == "__main__":  # 允许直接运行本脚本：python apitest/cases/api/classes/{id}/prompts/PUT.py
    import pathlib
    import sys

    sys.path.insert(0, str(next(p for p in pathlib.Path(__file__).resolve().parents if (p / "run_all.py").exists())))
    from lib.single import main

    main(__file__)
