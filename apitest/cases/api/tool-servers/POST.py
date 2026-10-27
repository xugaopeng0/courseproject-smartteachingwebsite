"""POST /api/tool-servers — 教师登记工具服务：201、重名 409、非法输入 400、学生 403。"""


def run(ctx):
    context = ctx.ensure_classroom()
    teacher = context["teacher_token"]
    ctx.set("teacher_token", teacher)
    # 名称前缀唯一，避免连续运行互相影响；清理时按前缀删除。
    name = "apit-tools-" + ctx.fixtures.suffix(4) if hasattr(ctx.fixtures, "suffix") else "apit-tools"
    import uuid

    name = "apit-tools-" + uuid.uuid4().hex[:6]
    ctx.set("tool_server_name", name)

    status, body = ctx.api.request(
        "POST",
        "/api/tool-servers",
        body={"name": name, "base_url": "http://mcp-demo:8090"},
        token=teacher,
        expect=201,
    )
    ctx.equal("登记返回 201", status, 201)
    ctx.equal("初始未连接", body.get("connected"), False)
    ctx.equal("工具列表为空", body.get("tools"), [])
    # 环境变量 MCP_AUTH_TOKEN 存在时 has_token 为 true 是正确的：只暴露布尔标记。
    has_token = body.get("has_token")
    ctx.check("has_token 是布尔标记", isinstance(has_token, bool), f"has_token={has_token}")
    ctx.check("响应不含令牌字段", "auth_token" not in body and "token_cipher" not in body)
    ctx.set("tool_server_id", body.get("id"))

    duplicate, _ = ctx.api.request(
        "POST", "/api/tool-servers", body={"name": name, "base_url": "http://mcp-demo:8090"}, token=teacher, expect=409
    )
    ctx.equal("重名返回 409", duplicate, 409)

    bad_name, _ = ctx.api.request(
        "POST", "/api/tool-servers", body={"name": "-bad", "base_url": "http://mcp-demo:8090"}, token=teacher, expect=400
    )
    ctx.equal("非法名称返回 400", bad_name, 400)
    bad_url, _ = ctx.api.request(
        "POST", "/api/tool-servers", body={"name": "ok-name", "base_url": "ftp://x"}, token=teacher, expect=400
    )
    ctx.equal("非法地址返回 400", bad_url, 400)

    student = ctx.get("student_token") or ctx.fixtures.create_student()
    ctx.set("student_token", student)
    denied, _ = ctx.api.request(
        "POST", "/api/tool-servers", body={"name": "s1", "base_url": "http://mcp-demo:8090"}, token=student, expect=403
    )
    ctx.equal("学生登记被拒 403", denied, 403)
    return status

if __name__ == "__main__":  # 允许直接运行本脚本：python apitest/cases/api/tool-servers/POST.py
    import pathlib
    import sys

    sys.path.insert(0, str(next(p for p in pathlib.Path(__file__).resolve().parents if (p / "run_all.py").exists())))
    from lib.single import main

    main(__file__)
