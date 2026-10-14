"""POST /api/auth/token — 用账号密码换取不透明 Bearer 令牌。

核对点：响应字段完整、**不写 Cookie**、数据库只存哈希、错误凭证与未知账号表现一致。
"""

CASE = {"order": 20}


def run(ctx):
    response = ctx.api.raw_request(
        "POST",
        "/api/auth/token",
        json={"username": ctx.fixtures.admin_username, "password": ctx.fixtures.admin_password},
    )
    status = response.status_code
    body = response.json()
    ctx.equal("签发返回 200", status, 200)
    ctx.check("返回令牌与过期时间", bool(body.get("token")) and bool(body.get("expires_at")), f"expires_at={body.get('expires_at')}")
    ctx.check("响应带用户信息", body.get("user", {}).get("username") == ctx.fixtures.admin_username)
    ctx.check("签发过程不写 Cookie", "set-cookie" not in {key.lower() for key in response.headers}, str(dict(response.headers))[:80])

    token = body["token"]
    ctx.set("admin_token", token)

    stored = ctx.db.scalar("SELECT COUNT(*) FROM sessions WHERE token_hash=%s", (token,))
    ctx.equal("数据库不存令牌原文", stored, 0)
    hashed = ctx.db.scalar("SELECT COUNT(*) FROM sessions WHERE user_id=%s", (body["user"]["id"],))
    ctx.check("令牌写入会话表", (hashed or 0) >= 1, f"sessions={hashed}")

    wrong = ctx.api.raw_request("POST", "/api/auth/token", json={"username": ctx.fixtures.admin_username, "password": "wrong-password"})
    unknown = ctx.api.raw_request("POST", "/api/auth/token", json={"username": "nosuchuser", "password": "wrong-password"})
    ctx.equal("错误凭证返回 401", wrong.status_code, 401)
    ctx.equal("未知账号返回 401", unknown.status_code, 401)
    ctx.equal("两种失败提示一致", wrong.json().get("error"), unknown.json().get("error"))

    me_status, me = ctx.api.request("GET", "/api/auth/me", token=token, expect=200)
    ctx.equal("令牌可读取当前用户", me_status, 200)
    ctx.equal("当前用户与签发一致", me.get("username"), ctx.fixtures.admin_username)
    return status

if __name__ == "__main__":  # 允许直接运行本脚本：python apitest/cases/api/auth/token/POST.py
    import pathlib
    import sys

    sys.path.insert(0, str(next(p for p in pathlib.Path(__file__).resolve().parents if (p / "run_all.py").exists())))
    from lib.single import main

    main(__file__)
