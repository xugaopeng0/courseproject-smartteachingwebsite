"""临时数据：创建教师、班级、讲义与会话，并在结束时清理干净。

用例只通过这里创建数据，因此「清理彻底」与「连续运行结果一致」由一处保证。
"""

from __future__ import annotations

import random
import string
from typing import Any

from .client import Api
from .db import Database

CLASS_RULE_TEXT = """# 课程规则

考核方式由平时作业百分之四十和期末项目百分之六十组成。
累计缺勤达到三次的学生不能参加期末答辩。
每周作业应在下周一晚上二十二点前提交。
"""


def suffix(length: int = 6) -> str:
    return "".join(random.choice(string.ascii_lowercase + string.digits) for _ in range(length))


class Fixtures:
    def __init__(self, api: Api, db: Database, admin_username: str, admin_password: str):
        self.api = api
        self.db = db
        self.admin_username = admin_username
        self.admin_password = admin_password
        self.created_users: list[str] = []
        self.created_classes: list[str] = []
        self.created_handouts: list[str] = []
        self.teacher_token: str | None = None

    def admin_token(self) -> str:
        status, body = self.api.request(
            "POST", "/api/auth/token", body={"username": self.admin_username, "password": self.admin_password}, expect=200
        )
        assert status == 200, body
        return body["token"]

    def create_teacher(self, admin_token: str) -> dict[str, str]:
        name = "apit" + suffix()
        password = "apitest-" + suffix(10)
        _, teacher = self.api.request(
            "POST",
            "/api/admin/teachers",
            body={"username": name, "name": "接口测试教师", "password": password},
            token=admin_token,
            expect=201,
        )
        self.created_users.append(teacher["id"])
        return {"id": teacher["id"], "username": teacher["username"], "password": password}

    def token_for(self, username: str, password: str) -> str:
        _, body = self.api.request("POST", "/api/auth/token", body={"username": username, "password": password}, expect=200)
        return body["token"]

    def create_class(self, teacher_token: str) -> dict[str, Any]:
        _, classroom = self.api.request(
            "POST",
            "/api/classes",
            body={"name": "接口测试班 " + suffix(), "description": "api-test-suite 临时班级"},
            token=teacher_token,
            expect=201,
        )
        self.created_classes.append(classroom["id"])
        return classroom

    def upload_handout(self, teacher_token: str, class_id: str, content: str = CLASS_RULE_TEXT) -> dict[str, Any]:
        _, handout = self.api.upload(
            f"/api/classes/{class_id}/handouts",
            filename="rules.md",
            content=content.encode("utf-8"),
            fields={"title": "课程规则（接口测试）"},
            token=teacher_token,
        )
        self.created_handouts.append(handout["id"])
        return handout

    def wait_indexed(self, teacher_token: str, class_id: str, handout_id: str, timeout: float = 60.0) -> str:
        """等讲义索引就绪，返回最终状态。"""
        import time

        deadline = time.time() + timeout
        status = "queued"
        while time.time() < deadline:
            _, payload = self.api.request(
                "GET", f"/api/classes/{class_id}/knowledge/documents", token=teacher_token, expect=200
            )
            for document in payload.get("documents", []):
                if document["handout_id"] == handout_id:
                    status = document["status"]
            if status in ("ready", "failed", "unsupported"):
                return status
            time.sleep(0.5)
        return status

    def wait_searchable(self, teacher_token: str, class_id: str, probe: str, timeout: float = 30.0) -> bool:
        """等讲义真的可被检索到。

        文档状态变为 ready 与实际可检索之间存在很短的时间差（发布事务提交与检索可见性），
        直接开查会偶发 no_matches，因此这里用一次关键字探针确认到「查得到」为止。
        """
        import time

        deadline = time.time() + timeout
        while time.time() < deadline:
            _, payload = self.api.request(
                "POST", f"/api/classes/{class_id}/knowledge/search", body={"query": probe, "mode": "keyword"}, token=teacher_token, expect=200
            )
            if payload.get("status") == "ok" and payload.get("hits"):
                return True
            time.sleep(0.5)
        return False

    def gateway_stats(self) -> dict[str, int] | None:
        """读取开发替身的调用计数；真实网关环境下返回 None（改用后端测试覆盖）。"""
        import json as json_module
        import os
        import urllib.error
        import urllib.request

        url = os.environ.get("APITEST_GATEWAY_STATS_URL", "http://127.0.0.1:11500/stats")
        try:
            with urllib.request.urlopen(url, timeout=3) as response:  # noqa: S310 - 只用于本地开发替身
                return json_module.loads(response.read().decode("utf-8"))
        except (urllib.error.URLError, OSError, ValueError):
            return None

    def cleanup(self) -> list[str]:
        """删除本次创建的数据；返回未能清理的条目说明。

        删除讲义必须用所属教师的令牌：共享的 api 会话此时带的是管理员 Cookie，
        管理员不是任课教师，会被 403 拒绝。
        """
        problems: list[str] = []
        token = self.teacher_token
        for handout_id in list(self.created_handouts):
            try:
                self.api.request("DELETE", f"/api/handouts/{handout_id}", token=token, expect=(200, 403, 404))
            except Exception as error:  # noqa: BLE001 - 清理失败要报告，不掩盖
                problems.append(f"删除讲义 {handout_id} 失败: {error}")
        for class_id in list(self.created_classes):
            try:
                self.db.query("DELETE FROM handouts WHERE class_id=%s", (class_id,))
                self.db.query("DELETE FROM classrooms WHERE id=%s", (class_id,))
            except Exception as error:  # noqa: BLE001
                problems.append(f"删除班级 {class_id} 失败: {error}")
        for user_id in list(self.created_users):
            try:
                self.db.query("DELETE FROM memberships WHERE user_id=%s", (user_id,))
                self.db.query("DELETE FROM users WHERE id=%s", (user_id,))
            except Exception as error:  # noqa: BLE001
                problems.append(f"删除账号 {user_id} 失败: {error}")
        return problems
