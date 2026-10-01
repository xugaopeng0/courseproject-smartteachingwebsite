# Design

## Context

动机见 [proposal.md](proposal.md)。实现基线来自已归档变更 `2026-10-01-class-scoped-knowledge-search`：PostgreSQL 18 + pgvector 单库、单路向量检索、无生成模块，其 22 项任务全部未勾选，容器验收因本机 Docker Desktop 无法启动而未完成。该变更的 13 条要求已进入 `openspec/specs/`。

约束：

- 班级授权集中在 `backend/classes.go` 的 `access()`（所有者或成员，管理员无隐式权限）；上传卷为私有卷；浏览器只访问本站 `/api`。
- 现有代码用 `pgx` 直连并提供 SQL，`backend/schema.sql` 幂等建表，`backend/migrations/*.sql` 以 `schema_migrations` 记录版本。
- 本地开发与集成测试使用 `embedded-postgres`（[scripts/postgres-runtime.mjs](scripts/postgres-runtime.mjs)），刻意不依赖 Docker；本机 Docker 守护进程当前不可用。
- 需保留的验收资产：`scripts/integration.mjs`（73 项 HTTP 检查）、`scripts/browser-test.mjs`、`scripts/deployment-test.mjs`、`scripts/knowledge-test.mjs`。

## Goals / Non-Goals

**Goals:**

- 把关系库、向量库与模型通道换成规约要求的形态，同时保持既有检索与资料管理行为不变。
- 切片策略可配置并随代次记录，使重建可复现、位置可回溯。
- 种子材料走普通讲义通道，继续受班级授权约束。
- 保留本地（便携运行时，不依赖 Docker）与容器（Compose，真实验收）两条验证路径。

**Non-Goals:**

- 不在本变更实现 keyword / hybrid 模式、`hits` 字段改名与问答引用；这些由后续变更承担。
- 不实现流式输出、交叉编码器重排序、OCR、跨班级知识库与智能体能力。
- 不改动登录、会话、班级、成员、上传与下载语义；上传仍仅限教师。

## Decisions

### 1. 关系库整体迁移到 MySQL 8，而不是只迁移切片表

理由：本课要求中文关键字检索落在 `FULLTEXT ... WITH PARSER ngram`，而切片正文、切片元数据、索引代次与班级授权需要同库事务；只把切片放进第二个关系库会引入跨库事务、双份授权查询与两套驱动维护成本；课程基线本身以 MySQL 承载关系数据。

替代方案：保留 PostgreSQL 用 `zhparser`/`pg_trgm` 做等价中文检索（不符规约，验收口径无法逐条对齐）；双关系库并存（一致性复杂）。pgvector 在 MySQL 上没有对应物，因此**向量检索必须与关系库迁移同时改到 Qdrant**，本变更不能只换一半。

方言差异必须逐项处理，不能依赖兼容层：

| PostgreSQL 现有用法 | MySQL 对应做法 |
| --- | --- |
| `$1` 占位符、`ANY($2)` 数组参数 | `?` 占位符，数组展开为 `IN (...)` |
| `timestamptz`、`boolean`、`jsonb` | `datetime(6)`（UTC）、`tinyint(1)`、`json` |
| `INSERT ... ON CONFLICT` | `INSERT ... ON DUPLICATE KEY UPDATE` |
| `WITH ... UPDATE ... RETURNING`（失败任务归档） | 同事务内先 `SELECT ... FOR UPDATE` 再 `UPDATE` |
| `FILTER (WHERE ...)` | `SUM(CASE WHEN ... THEN 1 ELSE 0 END)` |
| `FOR UPDATE OF j SKIP LOCKED`（多表加锁） | 单表行锁 + `SKIP LOCKED`，再按主键取上下文 |
| `pg_advisory_xact_lock(hashtext(...))` | `GET_LOCK(name, timeout)` / `RELEASE_LOCK` |
| `pgvector` 的 `<=>` 距离 | Qdrant 查询（见决策 2） |

字符集统一 `utf8mb4`。`ngram_token_size` 是服务端系统变量（默认 2），启动时校验其为 2，且必须在创建全文索引之前确定；改动该变量需要重建全文索引，写入运维文档。

### 2. 向量库使用 Qdrant，正文与授权仍以 MySQL 为权威

集合 `campusclaw_chunks`，余弦度量，维度取配置的嵌入维度。向量点主键等于切片标识；为使点标识合法，切片标识统一改为带连字符的 UUID 文本形式（当前 `randomID(16)` 的 32 位十六进制不满足 Qdrant 点标识要求）。payload 只放 `class_id`、`handout_id`、`generation_id`、`ordinal` 等过滤与校验字段。

客户端用 REST（`net/http`）实现，不引入官方 gRPC 客户端：接口面只有建集合、upsert、search、delete by filter，REST 便于用测试替身替换并断言请求体，避免把 gRPC/protobuf 依赖带进主进程。

检索链路固定为「先过滤、再取正文」：Qdrant 查询带 `class_id` 过滤 → 返回主键 → MySQL 以同班级条件回表取正文并校验代次为已发布代次；任一环节不符即丢弃。

### 3. 切片策略参数化并随代次记录

默认窗口 800 字、重叠 10%（约 80 字），窗口内优先在换行、空行或句号处断开；同时支持自定义分隔符与 Markdown 标题分章，超长章节回落到默认窗口。策略、窗口与重叠参数随索引代次记录，保证重建可复现；种子材料固定使用默认窗口。切片同时保留在提取文本中的字符区间与解析阶段的位置映射：前者服务结果契约，后者继续支撑 PDF 页码预览等既有定位能力。

### 4. 课程网关作为唯一模型通道

服务端提供 `embeddingClient`（兼容 OpenAI `/embeddings`）。配置项：`GATEWAY_BASE_URL`、`GATEWAY_API_KEY`、`EMBEDDING_MODEL`、`EMBEDDING_DIMENSIONS`、超时。启动时校验维度与集合一致；密钥只从环境读取。响应继续校验条数、维度、非有限值与零向量。失败返回明确错误，不降级、不改发其他服务。对话客户端由后续变更引入，本变更不预先实现。

### 5. 种子材料在启动时作为普通讲义导入

`SEED_ENABLED`（默认关闭，Compose 打开）、`SEED_DIR`（默认 `./seeds`）、`SEED_CLASS_NAME`。导入由 API 服务在启动阶段完成：确保示例班级存在 → 按内容哈希幂等写入讲义与上传卷 → 入队建立索引（默认窗口策略）。种子材料不走特殊通道，因此继续受班级授权、来源校验与删除一致性约束。种子目录缺失或为空时跳过并记录日志，不影响启动。

### 6. 本地与容器两条验证路径

本地：新增 `scripts/mysql-runtime.mjs`，用解压到 `.tools/mysql` 的 MySQL Community（忽略提交）以固定端口启动临时实例，替换 `embedded-postgres`；Qdrant 用 `.tools/qdrant` 单文件二进制。容器：Compose 增加 `db`（MySQL 8.4）与 `qdrant`，移除 `ollama` / `model-init`；`api` 与 `worker` 只经内网访问数据库与向量库；只有 `web` 暴露 `127.0.0.1:8080`。

### 7. 旧实现直接移除，不保留双实现

pgvector、Ollama 相关代码、配置、Compose 服务与文档随本变更删除。保留双实现会让「究竟走哪条路径」在验收时无法判定，也会让后续变更的改动面翻倍。

## Risks / Trade-offs

- [换库改动面覆盖全部 SQL，回归风险高] → 分三批推进（驱动与迁移、业务查询、知识库），每批用真实 MySQL 跑完既有 73 项 HTTP 检查；PostgreSQL 卷在验收通过前保持不动。
- [MySQL 与 Qdrant 双写不一致] → MySQL 为权威，回表校验班级与代次；删除、重建、归档后清理向量点，残留点因回表失败而不可见。
- [本机 Docker 不可用，容器验收无法执行] → 本地路径先用便携 MySQL + Qdrant 二进制覆盖功能验证；容器验收项在 Docker 恢复后补做，不预先声称通过。
- [切片标识改为 UUID 影响既有引用] → 库中尚无已发布的生产数据；重建索引后旧引用自然失效，符合既有「失效引用明确处理」要求。
- [迁移脚本一次性导入出错] → 数据库与上传卷按同一时间点配对备份，先在副本演练并核对行数、外键与文件哈希，再在正式环境执行。
- [种子材料与「Docker 首启不加演示数据」的既有约定冲突] → 以 `SEED_ENABLED` 区分：关闭时行为与首期一致；Compose 默认打开，并在验证文档中说明影响范围仅限示例班级。

## Migration Plan

1. 冻结写入并备份：PostgreSQL 数据库与上传卷按同一时间点配对导出。
2. 在副本上部署 MySQL 8 与迁移机制，运行一次性导出/导入脚本，核对用户、会话、班级、成员、讲义元数据行数及文件哈希。
3. 部署 Qdrant，按配置维度创建 `campusclaw_chunks`；对全部讲义与种子材料重建切片索引（默认窗口策略）。
4. 切换 API 与 worker 到 MySQL + Qdrant，PostgreSQL 卷转为只读保留；验证登录、班级、上传下载与既有检索契约不变。
5. 回滚：停止 worker 与新界面，部署旧 API 镜像并指回 PostgreSQL 卷；不执行 `docker compose down -v`，不以空卷冒充迁移成功。
6. 验收通过后更新 README、`docs/api.md`、`docs/architecture.md`、`docs/knowledge-base.md` 与 `docs/verification.md`。

## Open Questions

- 课程网关的实际地址、可用模型名与嵌入维度（维度决定集合大小；配置缺失时按不可用处理，不阻塞本变更其余验收项）。
- 课程提供的种子材料文件清单与期望的示例班级名称（缺失时导入跳过）。
