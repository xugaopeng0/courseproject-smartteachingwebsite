"""GET /api/tool-servers — 教师列出自己登记的工具服务；学生被拒。"""


def run(ctx):
    teacher = ctx.get("teacher_token")
    if not teacher:
        context = ctx.ensure_classroom()
        teacher = context["teacher_token"]
        ctx.set("teacher_token", teacher)
        ctx.set("class_id", context["classroom"]["id"])
    status, body = ctx.api.request("GET", "/api/tool-servers", token=teacher, expect=200)
    ctx.equal("列出工具服务返回 200", status, 200)
    servers = body.get("servers", [])
    ctx.check("返回服务器数组", isinstance(servers, list), f"共 {len(servers)} 台")
    for server in servers:
        ctx.check(
            f"{server.get('name')} 只有 has_token 标记",
            "auth_token" not in server and "token" not in server and isinstance(server.get("has_token"), bool),
            "响应不含令牌字段",
        )

    student = ctx.get("student_token")
    if not student:
        student = ctx.fixtures.create_student()
        ctx.set("student_token", student)
    denied, _ = ctx.api.request("GET", "/api/tool-servers", token=student, expect=403)
    ctx.equal("学生列表被拒 403", denied, 403)
    return status

if __name__ == "__main__":  # 允许直接运行本脚本：python apitest/cases/api/tool-servers/GET.py
    import pathlib
    import sys

    sys.path.insert(0, str(next(p for p in pathlib.Path(__file__).resolve().parents if (p / "run_all.py").exists())))
    from lib.single import main

    main(__file__)
