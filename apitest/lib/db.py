"""数据库核对：优先直连 MySQL，连不上时退回 `docker compose exec db mysql`。

Compose 有意不把 db 端口发布到宿主，因此两种模式都要支持；实际使用哪一种会写进报告头部，
方便解释结果来源。
"""

from __future__ import annotations

import re
import shlex
import subprocess
from dataclasses import dataclass
from pathlib import Path
from typing import Any


@dataclass
class DatabaseConfig:
    host: str
    port: int
    user: str
    password: str
    name: str
    mode: str  # direct | compose-exec


class Database:
    def __init__(self, config: DatabaseConfig, project_root: Path):
        self.config = config
        self.project_root = project_root
        self.connection = None
        if config.mode == "direct":
            import pymysql

            self.connection = pymysql.connect(
                host=config.host,
                port=config.port,
                user=config.user,
                password=config.password,
                database=config.name,
                charset="utf8mb4",
                autocommit=True,
                connect_timeout=5,
                cursorclass=pymysql.cursors.DictCursor,
            )

    def query(self, sql: str, args: tuple = ()) -> list[dict[str, Any]]:
        if self.connection is not None:
            with self.connection.cursor() as cursor:
                cursor.execute(sql, args)
                return list(cursor.fetchall())
        return self._query_via_compose(sql, args)

    # 语义化别名：query_all 与 query 等价，query_one 只取第一行。
    def query_all(self, sql: str, args: tuple = ()) -> list[dict[str, Any]]:
        return self.query(sql, args)

    def query_one(self, sql: str, args: tuple = ()) -> dict[str, Any] | None:
        rows = self.query(sql, args)
        return rows[0] if rows else None

    def scalar(self, sql: str, args: tuple = ()) -> Any:
        rows = self.query(sql, args)
        if not rows:
            return None
        return next(iter(rows[0].values()))

    def close(self) -> None:
        if self.connection is not None:
            self.connection.close()

    # `docker compose exec` 走的是容器里的 mysql 客户端，SQL 里的字面量必须自己转义。
    def _query_via_compose(self, sql: str, args: tuple) -> list[dict[str, Any]]:
        statement = sql
        for value in args:
            literal = "NULL" if value is None else "'" + str(value).replace("\\", "\\\\").replace("'", "''") + "'"
            statement = statement.replace("%s", literal, 1)
        columns, _ = self._split_statement(statement)
        command = [
            "docker",
            "compose",
            "exec",
            "-T",
            "-e",
            f"MYSQL_PWD={self.config.password}",
            "db",
            "mysql",
            "-h",
            "127.0.0.1",
            "-u",
            self.config.user,
            self.config.name,
            "--default-character-set=utf8mb4",
            "-N",
            "-B",
            "-e",
            statement,
        ]
        result = subprocess.run(command, cwd=self.project_root, capture_output=True, text=True, timeout=60)
        if result.returncode != 0:
            raise RuntimeError(f"数据库命令失败: {result.stderr.strip() or result.stdout.strip()}")
        parsed: list[dict[str, Any]] = []
        for line in result.stdout.splitlines():
            if not line.strip():
                continue
            values = [self._convert(self._unescape(item)) for item in line.split("\t")]
            parsed.append({columns[index] if index < len(columns) else f"col{index}": value for index, value in enumerate(values)})
        return parsed

    # mysql 批处理模式（-B）以制表符分隔并把换行等字符转义输出，且所有值都是文本。
    # 这里还原转义并把纯数字转回数值，否则 compose-exec 与 direct 两种模式结果不一致。
    _ESCAPES = {"n": "\n", "t": "\t", "r": "\r", "0": "\0", "\\": "\\"}

    @classmethod
    def _unescape(cls, value: str) -> str:
        if "\\" not in value:
            return value
        out: list[str] = []
        escaped = False
        for char in value:
            if escaped:
                out.append(cls._ESCAPES.get(char, char))
                escaped = False
            elif char == "\\":
                escaped = True
            else:
                out.append(char)
        if escaped:
            out.append("\\")
        return "".join(out)

    @staticmethod
    def _convert(value: str) -> Any:
        if value == "NULL":
            return None
        if re.fullmatch(r"-?\d+", value):
            return int(value)
        if re.fullmatch(r"-?\d+\.\d+", value):
            return float(value)
        return value

    @staticmethod
    def _split_statement(statement: str) -> tuple[list[str], list[str]]:
        """从 SELECT 列表推断列名，供 compose 模式还原成字典。"""
        head = statement.strip()
        if not head.lower().startswith("select"):
            return [], []
        body = head[6:]
        lowered = body.lower()
        for keyword in (" from ", " where ", " order ", " limit "):
            index = lowered.find(keyword)
            if index >= 0:
                body = body[:index]
                lowered = body.lower()
        columns: list[str] = []
        for item in body.split(","):
            name = item.strip()
            if " as " in name.lower():
                name = name.lower().split(" as ")[-1].strip()
            elif "(" in name:
                name = name.split("(")[-1].split(")")[0].strip()
            name = name.strip("`").split(".")[-1]
            columns.append(name)
        return columns, []


def resolve_database_config(env: dict[str, str], project_root: Path, base_url: str = "") -> DatabaseConfig:
    """决定数据库核对方式：显式配置 → 被测服务形态 → 端口探测。

    关键点：核对方式必须跟着**被测服务**走。如果被测的是 Compose 部署（web 发布的端口），
    它的数据库在容器里、端口没有发布，就必须走 `docker compose exec`；如果被测的是
    `dev:local`（127.0.0.1:8081），数据库是宿主机 55432 上的便携 MySQL，直连即可。
    """
    if env.get("APITEST_DB_HOST"):
        return DatabaseConfig(
            host=env["APITEST_DB_HOST"],
            port=int(env.get("APITEST_DB_PORT", "3306")),
            user=env.get("APITEST_DB_USER", "teaching"),
            password=env.get("APITEST_DB_PASSWORD", env.get("DB_PASSWORD", "")),
            name=env.get("APITEST_DB_NAME", "teaching"),
            mode="direct",
        )

    mode = env.get("APITEST_DB_MODE", "").strip().lower()
    compose_db = compose_service_running(project_root, "db")
    compose_port = compose_published_port(project_root, "web", "80")
    target_port = _port_of(base_url)

    if mode == "compose-exec":
        return _compose_config(env)
    if mode == "direct":
        return _direct_config(env)
    if compose_db and compose_port and target_port == compose_port:
        return _compose_config(env)
    direct = _first_direct(env)
    if direct is not None:
        return direct
    if compose_db:
        return _compose_config(env)
    return DatabaseConfig("", 0, "teaching", env.get("DB_PASSWORD", ""), "teaching", "compose-exec")


def _compose_config(env: dict[str, str]) -> DatabaseConfig:
    return DatabaseConfig(
        "", 0, env.get("APITEST_DB_USER", "teaching"), env.get("APITEST_DB_PASSWORD", env.get("DB_PASSWORD", "")), env.get("APITEST_DB_NAME", "teaching"), "compose-exec"
    )


def _direct_config(env: dict[str, str]) -> DatabaseConfig:
    found = _first_direct(env)
    if found is None:
        return DatabaseConfig("127.0.0.1", 3306, env.get("APITEST_DB_USER", "teaching"), env.get("DB_PASSWORD", ""), "teaching", "direct")
    return found


def _first_direct(env: dict[str, str]) -> DatabaseConfig | None:
    candidates = [
        DatabaseConfig("127.0.0.1", 3306, env.get("APITEST_DB_USER", "teaching"), env.get("DB_PASSWORD", ""), "teaching", "direct"),
        DatabaseConfig("127.0.0.1", 55432, "root", "", "teaching", "direct"),
    ]
    for candidate in candidates:
        if _reachable(candidate):
            return candidate
    return None


def _port_of(base_url: str) -> int | None:
    import urllib.parse

    parsed = urllib.parse.urlparse(base_url)
    if parsed.port:
        return parsed.port
    return 443 if parsed.scheme == "https" else 80 if parsed.scheme == "http" else None


def compose_service_running(project_root: Path, service: str) -> bool:
    try:
        result = subprocess.run(
            ["docker", "compose", "ps", "-q", service], cwd=project_root, capture_output=True, text=True, timeout=25
        )
    except (OSError, subprocess.SubprocessError):
        return False
    return result.returncode == 0 and bool(result.stdout.strip())


def compose_published_port(project_root: Path, service: str, container_port: str) -> int | None:
    try:
        result = subprocess.run(
            ["docker", "compose", "port", service, container_port],
            cwd=project_root,
            capture_output=True,
            text=True,
            timeout=25,
        )
    except (OSError, subprocess.SubprocessError):
        return None
    if result.returncode != 0 or not result.stdout.strip():
        return None
    first = result.stdout.strip().splitlines()[0]
    tail = first.rsplit(":", 1)[-1]
    return int(tail) if tail.isdigit() else None


def _reachable(config: DatabaseConfig) -> bool:
    import socket

    try:
        with socket.create_connection((config.host, config.port), timeout=1.5):
            return True
    except OSError:
        return False


def compose_exec_command(config: DatabaseConfig, sql: str) -> list[str]:
    """给报告用的命令预览。"""
    return ["docker", "compose", "exec", "db", "mysql", "-e", shlex.quote(sql)]
