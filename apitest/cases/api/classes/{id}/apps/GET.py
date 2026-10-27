"""GET /api/classes/{id}/apps — 内置应用清单（知识库问答 / 出题练习）。"""


def run(ctx):
    context = ctx.ensure_classroom()
    class_id = context["classroom"]["id"]
    token = context["teacher_token"]
    status, body = ctx.api.request("GET", f"/api/classes/{class_id}/apps", token=token, expect=200)
    ctx.equal("应用清单返回 200", status, 200)
    apps = body.get("apps", [])
    ids = [item.get("id") for item in apps]
    ctx.check("包含知识库问答与出题练习", ids == ["qa", "quiz"], str(ids))
    ctx.check(
        "每个应用都有名称与说明",
        all(item.get("name") and item.get("description") for item in apps),
        str([item.get("name") for item in apps]),
    )
    return status
