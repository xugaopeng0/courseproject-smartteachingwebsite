## ADDED Requirements
### Requirement: Local Docker deployment
系统 SHALL 提供包含 React 前端、Go 后端与 PostgreSQL 的 Docker Compose 部署，数据库与私有上传文件使用持久化卷。
#### Scenario: First startup
- **WHEN** 用户配置环境并执行 docker compose up --build -d
- **THEN** 健康服务通过 localhost:8080 提供平台，并创建配置的初始管理员
#### Scenario: Restart
- **WHEN** 容器重启且未删除数据卷
- **THEN** 账号、班级和讲义保持可用
#### Scenario: Missing administrator password
- **WHEN** 初始管理员凭证缺失或无效
- **THEN** 启动失败并提示如何修正配置
