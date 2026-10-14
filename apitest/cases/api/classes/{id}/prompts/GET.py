"""GET /api/classes/{id}/prompts — 读取本班生效的提示词与技能状态。"""


def setup(ctx):
    ctx.ensure_classroom()


def run(ctx):
    context = ctx.ensure_classroom()
    class_id = context["classroom"]["id"]
    status, body = ctx.api.request("GET", f"/api/classes/{class_id}/prompts", token=context["teacher_token"], expect=200)
    ctx.equal("读取提示词返回 200", status, 200)
    ctx.check("给出提示词来源", body.get("prompt_source") in ("platform", "class", "none"), str(body.get("prompt_source")))
    ctx.check("所属教师可编辑", body.get("can_edit") is True)
    skills = body.get("skills", [])
    names = [item.get("name") for item in skills]
    ctx.check("内置「解题引导」技能存在", "解题引导" in names, str(names))
    guidance = next((item for item in skills if item.get("name") == "解题引导"), {})
    ctx.check("内置技能默认启用", guidance.get("enabled") is True)
    ctx.check("技能带正文", bool(guidance.get("body")), str(guidance.get("body", ""))[:24])

    platform_rows = ctx.db.scalar("SELECT COUNT(*) FROM prompts WHERE scope='platform' AND kind='prompt'")
    ctx.check("平台默认提示词已初始化", (platform_rows or 0) >= 1, f"rows={platform_rows}")
    return status

if __name__ == "__main__":  # 允许直接运行本脚本：python apitest/cases/api/classes/{id}/prompts/GET.py
    import pathlib
    import sys

    sys.path.insert(0, str(next(p for p in pathlib.Path(__file__).resolve().parents if (p / "run_all.py").exists())))
    from lib.single import main

    main(__file__)
