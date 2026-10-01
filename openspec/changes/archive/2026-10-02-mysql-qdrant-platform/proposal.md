# Proposal

## Why

本课要求中文关键字检索落在 MySQL 全文索引、向量检索落在 Qdrant，嵌入只经服务端课程网关。当前实现是 PostgreSQL 18 + pgvector 单库与本地 Ollama 嵌入，既无法提供 `FULLTEXT ... WITH PARSER ngram` 的中文切词能力，也不符合向量库与模型通道选型。这些差异都属于底座：不先换掉，后续的三种检索模式与问答没有可落地且可验收的地基。

## What Changes

- **BREAKING** 关系库由 PostgreSQL 18 + pgvector 迁移到 MySQL 8，切片正文建立 `FULLTEXT ... WITH PARSER ngram`（token 长度 2）全文索引；数据库驱动、全部 SQL、`schema.sql` 与迁移机制随之改写，既有数据经一次性迁移脚本导入。
- **BREAKING** 向量检索由 pgvector 迁移到 Qdrant 集合 `campusclaw_chunks`（余弦度量）；向量点主键等于切片标识，切片正文只保存在关系库。
- **BREAKING** 嵌入改由服务端调用课程网关（兼容 OpenAI 嵌入接口），移除对本地 Ollama 的依赖；网关地址、模型名与密钥只存在于服务端配置。
- 上传成功后正文先切分为切片再嵌入：默认约 800 字窗口、相邻约 10% 重叠，另支持自定义分隔符与 Markdown 标题分章；切片记录序号与字符区间，策略参数随索引代次记录。
- 启动时按配置把课程提供的种子材料作为内置示例班级的普通讲义导入，并沿用默认窗口策略建立索引。
- 本变更**不改变既有检索接口的对外契约**：仍是单路向量检索、0.35 阈值、按班级授权、结果含出处。三种检索模式与问答由后续变更交付。
- 本地开发与集成测试改用便携 MySQL 运行时，继续保持不依赖 Docker。

## Capabilities

### New Capabilities

- `knowledge-chunking`: 切片策略（默认窗口、自定义分隔符、Markdown 标题分章）、切片序号与字符区间、位置映射保留，以及种子材料沿用默认策略。
- `knowledge-data-platform`: 正文与向量的存储分工、全文索引与向量集合的配置一致性、课程网关的服务端调用与密钥边界、跨存储一致性。

### Modified Capabilities

- `knowledge-ingestion`: 新增种子材料启动导入；嵌入与对话改为仅经服务端课程网关，移除「默认本地嵌入服务」的表述。

## Impact

- 后端：数据库驱动与全部 SQL、迁移机制、索引 worker、向量检索通路与模型客户端；检索接口的响应结构保持不变。
- 依赖与部署：新增 MySQL 8 与 Qdrant 服务及课程网关配置；退役 pgvector、本地 Ollama 与 `embedded-postgres`；新增便携 MySQL 本地运行时。
- 数据：现有 PostgreSQL 数据与上传文件须按同一时间点配对备份，并在副本上演练迁移；回滚保留原卷，不以清空卷代替迁移。
- 范围边界：三种检索模式、`hits` 契约与问答引用由 `knowledge-retrieval-modes` 与 `knowledge-answer-citations` 两个后续变更承担，本变更不提前实现。
