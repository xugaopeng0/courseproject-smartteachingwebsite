# deployment Specification

## Purpose
规定平台在本机 Docker Desktop 上的可复现部署：一条命令拉起前端、后端与数据服务，账号、班级、讲义与索引数据落在持久化卷中，重启不丢数据，初始管理员凭证缺失时明确失败而不是半启动。

## Requirements

### Requirement: Local Docker deployment
系统 SHALL 提供包含 React 前端、Go 后端、MySQL 8.4 关系库与 Qdrant 向量库的 Docker Compose 部署，数据库、向量与私有上传文件使用持久化卷。
#### Scenario: First startup
- **WHEN** 用户配置环境并执行 docker compose up --build -d
- **THEN** 健康服务通过 localhost:8080 提供平台，并创建配置的初始管理员
#### Scenario: Restart
- **WHEN** 容器重启且未删除数据卷
- **THEN** 账号、班级和讲义保持可用
#### Scenario: Missing administrator password
- **WHEN** 初始管理员凭证缺失或无效
- **THEN** 启动失败并提示如何修正配置
#### Scenario: Knowledge services
- **WHEN** 部署启动完成
- **THEN** 关系库以 ngram 全文索引所需参数运行，向量库存在与配置维度一致的集合，且两者只接在内部网络、不发布宿主端口
