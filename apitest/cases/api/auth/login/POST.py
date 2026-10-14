"""POST /api/auth/login — 管理员登录、下发会话 Cookie，并核对会话行落库。

对应接口契约：docs/openapi.json 的 /api/auth/login。
"""

CASE = {"order": 10}


def run(ctx):
    status, body = ctx.api.request(
        "POST",
        "/api/auth/login",
        body={"username": ctx.fixtures.admin_username, "password": ctx.fixtures.admin_password},
        expect=200,
    )
    ctx.equal("登录返回 200", status, 200)
    ctx.equal("返回管理员角色", body.get("role"), "admin")
    ctx.check("返回账号与姓名", bool(body.get("username")) and bool(body.get("name")), f"{body.get('username')} / {body.get('name')}")

    cookie = ctx.api.session.cookies.get("teaching_session")
    ctx.check("下发会话 Cookie", bool(cookie), "teaching_session")

    user_id = body.get("id")
    rows = ctx.db.scalar("SELECT COUNT(*) FROM sessions WHERE user_id=%s", (user_id,))
    ctx.check("会话行已落库", (rows or 0) >= 1, f"sessions={rows}")

    hashed = ctx.db.scalar("SELECT COUNT(*) FROM sessions WHERE token_hash=%s", (cookie,))
    ctx.equal("数据库只存哈希、不存原始令牌", hashed, 0)

    role_row = ctx.db.scalar("SELECT role FROM users WHERE id=%s", (user_id,))
    ctx.equal("数据库中的角色与响应一致", role_row, "admin")
    return status

if __name__ == "__main__":  # 允许直接运行本脚本：python apitest/cases/api/auth/login/POST.py
    import pathlib
    import sys

    sys.path.insert(0, str(next(p for p in pathlib.Path(__file__).resolve().parents if (p / "run_all.py").exists())))
    from lib.single import main

    main(__file__)
