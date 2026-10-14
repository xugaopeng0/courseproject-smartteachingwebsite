"""接口测试公共库：HTTP 客户端、数据库核对、临时数据与报告。

设计约定见 docs/verification.md 与 openspec/changes/api-test-suite/design.md：
- 用例脚本只依赖本目录的助手，不直接拼连接串；
- 数据库核对优先直连，连不上时退回 `docker compose exec`（Compose 不发布 db 端口）；
- 报告是自包含 HTML，写入 apitest/report/（忽略提交）。
"""

__all__ = ["client", "db", "fixtures", "report", "runner"]
