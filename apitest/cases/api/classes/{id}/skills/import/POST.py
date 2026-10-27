"""POST /api/classes/{id}/skills/import — 上传技能文件（.md / .json），核对入库与校验拒绝。"""

MARKDOWN = """---
name: 上传技能甲
enabled: true
tools: knowledge_search
---
先用一句话给结论，再分点给出依据。
"""

JSON_SKILL = '{"name":"上传技能乙","body":"用要点作答。","enabled":false}'


def run(ctx):
    context = ctx.ensure_classroom()
    class_id = context["classroom"]["id"]
    token = context["teacher_token"]
    path = f"/api/classes/{class_id}/skills/import"

    status, body = ctx.api.upload(
        path,
        filename="skill.md",
        content=MARKDOWN.encode("utf-8"),
        token=token,
        expect=(201,),
    )
    ctx.equal("上传 .md 返回 201", status, 201)
    ctx.equal("解析出的技能名正确", body.get("skill"), "上传技能甲")
    view = body.get("view") or {}
    skills = {item["name"]: item for item in view.get("skills", [])}
    entry = skills.get("上传技能甲", {})
    ctx.check("技能出现在状态中", bool(entry), str(list(skills)[:4]))
    ctx.equal("正文被完整解析", entry.get("body"), "先用一句话给结论，再分点给出依据。")
    ctx.equal("工具项被解析", entry.get("tools"), ["knowledge_search"])

    row = ctx.db.query("SELECT body, enabled, tools FROM prompts WHERE class_id=%s AND kind='skill' AND name=%s", (class_id, "上传技能甲"))
    ctx.equal("落库一条班级技能", len(row), 1)
    ctx.check("已启用的技能在库中为启用", bool(row) and row[0]["enabled"] == 1, str(row[:1]))

    status, body = ctx.api.upload(path, filename="skill.json", content=JSON_SKILL.encode("utf-8"), token=token, expect=(201,))
    ctx.equal("上传 .json 返回 201", status, 201)
    ctx.equal("json 技能名正确", body.get("skill"), "上传技能乙")

    bad = ctx.api.request(
        "POST",
        path,
        body=None,
        raw=b"not multipart",
        content_type="text/plain",
        token=token,
        expect=(400, 413),
    )
    ctx.check("非 multipart 请求被拒", bad[0] in (400, 413), f"HTTP {bad[0]}")

    # 清理：删除本次上传的技能，避免影响后续用例
    for name in ("上传技能甲", "上传技能乙"):
        ctx.api.request("DELETE", f"/api/classes/{class_id}/skills/{name}", token=token, expect=(200, 404))
    return status
