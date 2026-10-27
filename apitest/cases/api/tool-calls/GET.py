"""GET /api/tool-calls — 记录列表：默认上限、结果筛选、学生被拒。"""


def run(ctx):
    teacher = ctx.get("teacher_token")
    if not teacher:
        context = ctx.ensure_classroom()
        teacher = context["teacher_token"]
        ctx.set("teacher_token", teacher)

    status, body = ctx.api.request("GET", "/api/tool-calls", token=teacher, expect=200)
    ctx.equal("记录列表返回 200", status, 200)
    ctx.check("返回记录数组", isinstance(body.get("calls"), list), f"{len(body.get('calls', []))} 条")
    ctx.equal("默认上限为 50", body.get("limit"), 50)
    ctx.check("带 has_more 标记", isinstance(body.get("has_more"), bool))

    filtered = ctx.api.request("GET", "/api/tool-calls?outcome=success", token=teacher, expect=200)[1]
    ctx.check(
        "按结果筛选有效",
        all(item.get("outcome") == "success" for item in filtered.get("calls", [])),
        f"{len(filtered.get('calls', []))} 条成功记录",
    )
    bad = ctx.api.request("GET", "/api/tool-calls?outcome=nope", token=teacher, expect=400)
    ctx.equal("非法 outcome 返回 400", bad[0], 400)

    student = ctx.get("student_token") or ctx.fixtures.create_student()
    ctx.set("student_token", student)
    denied = ctx.api.request("GET", "/api/tool-calls", token=student, expect=403)
    ctx.equal("学生查看记录被拒 403", denied[0], 403)
    return status

if __name__ == "__main__":  # 允许直接运行本脚本：python apitest/cases/api/tool-calls/GET.py
    import pathlib
    import sys

    sys.path.insert(0, str(next(p for p in pathlib.Path(__file__).resolve().parents if (p / "run_all.py").exists())))
    from lib.single import main

    main(__file__)
