"""HTTP 客户端：Cookie 会话、Bearer 令牌与 SSE 事件流。"""

from __future__ import annotations

import json
from typing import Any, Iterator

import requests


class ApiError(RuntimeError):
    """接口返回了非预期状态码。"""

    def __init__(self, method: str, path: str, status: int, body: str):
        super().__init__(f"{method} {path} → HTTP {status}: {body[:200]}")
        self.method = method
        self.path = path
        self.status = status
        self.body = body


class Api:
    """把 requests.Session 包装成用例友好的接口。"""

    def __init__(self, base_url: str):
        self.base_url = base_url.rstrip("/")
        self.session = requests.Session()

    def request(
        self,
        method: str,
        path: str,
        *,
        body: Any = None,
        token: str | None = None,
        headers: dict[str, str] | None = None,
        raw: bytes | None = None,
        content_type: str | None = "application/json",
        expect: int | tuple[int, ...] | None = None,
        timeout: float = 30.0,
    ) -> tuple[int, Any]:
        """发一次请求，返回 (状态码, 解析后的响应)。"""
        url = self.base_url + path
        request_headers = dict(headers or {})
        if token:
            request_headers["Authorization"] = f"Bearer {token}"
        data: bytes | None = raw
        if raw is None and body is not None:
            data = json.dumps(body, ensure_ascii=False).encode("utf-8")
        if data is not None and content_type:
            request_headers.setdefault("Content-Type", content_type)
        response = self.session.request(method, url, data=data, headers=request_headers, timeout=timeout)
        payload = self._decode(response)
        if expect is not None:
            allowed = expect if isinstance(expect, tuple) else (expect,)
            if response.status_code not in allowed:
                raise ApiError(method, path, response.status_code, response.text)
        return response.status_code, payload

    def raw_request(self, method: str, path: str, *, token: str | None = None, **kwargs: Any) -> requests.Response:
        """需要检查响应头（例如是否写 Cookie）时用，返回原始响应。"""
        headers = dict(kwargs.pop("headers", {}) or {})
        if token:
            headers["Authorization"] = f"Bearer {token}"
        return self.session.request(method, self.base_url + path, headers=headers, timeout=kwargs.pop("timeout", 30.0), **kwargs)

    def upload(
        self,
        path: str,
        *,
        filename: str,
        content: bytes,
        fields: dict[str, str] | None = None,
        token: str | None = None,
        expect: int | tuple[int, ...] | None = (201,),
    ) -> tuple[int, Any]:
        """multipart 上传。"""
        headers = {}
        if token:
            headers["Authorization"] = f"Bearer {token}"
        files = {"file": (filename, content, "application/octet-stream")}
        response = self.session.post(self.base_url + path, data=fields or {}, files=files, headers=headers, timeout=60.0)
        payload = self._decode(response)
        if expect is not None:
            allowed = expect if isinstance(expect, tuple) else (expect,)
            if response.status_code not in allowed:
                raise ApiError("POST", path, response.status_code, response.text)
        return response.status_code, payload

    def stream(
        self,
        method: str,
        path: str,
        *,
        body: Any = None,
        token: str | None = None,
        timeout: float = 120.0,
    ) -> Iterator[tuple[str, dict[str, Any]]]:
        """读取 SSE：逐个产出 (事件名, 载荷)。"""
        headers = {"Accept": "text/event-stream"}
        if token:
            headers["Authorization"] = f"Bearer {token}"
        data = json.dumps(body, ensure_ascii=False).encode("utf-8") if body is not None else None
        headers["Content-Type"] = "application/json"
        with self.session.request(
            method, self.base_url + path, data=data, headers=headers, stream=True, timeout=timeout
        ) as response:
            if response.status_code != 200:
                raise ApiError(method, path, response.status_code, response.text)
            # 事件流固定按 UTF-8 解码：响应头若缺少 charset，requests 会退回 latin-1，中文会变乱码。
            response.encoding = "utf-8"
            event = ""
            for raw_line in response.iter_lines(decode_unicode=True):
                line = (raw_line or "").strip()
                if not line:
                    event = ""
                    continue
                if line.startswith(":"):
                    yield "heartbeat", {}
                    continue
                if line.startswith("event:"):
                    event = line[6:].strip()
                    continue
                if line.startswith("data:"):
                    payload = line[5:].strip()
                    try:
                        decoded = json.loads(payload)
                    except json.JSONDecodeError:
                        decoded = {"raw": payload}
                    yield event or "message", decoded

    @staticmethod
    def _decode(response: requests.Response) -> Any:
        text = response.text
        if not text:
            return {}
        try:
            return response.json()
        except json.JSONDecodeError:
            return {"raw": text}
