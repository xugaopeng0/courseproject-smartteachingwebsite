# 首期验证记录

验证日期：2026-09-23。环境：Windows、Node 24、Go 1.26、真实 PostgreSQL 18、Chrome 无头浏览器。

## 已通过

- OpenSpec：`openspec validate --all --strict`，首期变更通过。
- 前端：TypeScript 类型检查及 Vite 生产构建通过。
- 后端：`go test ./...`、`go vet ./...`、Windows 编译及 Linux amd64 静态交叉编译通过。
- 集成：`node scripts/integration.mjs`，**59 项 HTTP 检查**通过。
- 浏览器：`node scripts/browser-test.mjs` 完整业务回归通过，无页面脚本错误。
- 布局：实际查看 1440px 桌面和 390px 手机截图，无手机横向溢出，主要操作可用。

## 集成检查覆盖

未登录拒绝访问、错误密码、管理员创建教师、重复账号、禁止注册提权、学生自助注册、角色授权、跨站请求拒绝、班级创建编辑、重复/无效邀请码、非成员隔离、隐藏学生侧邀请码、上传权限、扩展名和签名校验、50 MiB 大小限制、下载内容与预览响应头、讲义删除、班级归档与恢复、移除成员/主动退出后撤销访问、退出后旧 cookie 无效、数据库会话过期、密码哈希、后端进程重启后数据/会话/文件保持可用。

## 浏览器检查覆盖

管理员登录并创建教师 → 教师登录 → 创建三个明确标记为“示例班级”的演示班级 → 上传 PDF → 班级搜索 → 手机端学生注册 → 邀请码入班 → 下载 → 归档/恢复 → 退出班级 → 编辑班级 → 全部讲义列表。

演示数据仅在临时开发数据库中，Docker 首次启动不会自动添加。开发演示账号在 `.local/browser-demo.json`，截图位于 `.local/screenshots/`。

## Docker 实际部署验收（已完成）

2026-09-23，安装 Docker Desktop 后完成本机部署。Docker Engine 29.8.0，Compose v5.5.1，WSL 2 Linux 容器。

- 前端和后端镜像在 Docker 内成功完成生产构建。
- `db`、`api`、`web` 三个服务均为 healthy；网页仅绑定 `127.0.0.1:8080`。
- 执行 `node scripts/deployment-test.mjs`，通过真实 nginx 入口验证管理员登录、教师创建、学生注册、班级创建与加入、讲义上传和原内容下载、角色越权拒绝。
- 实际执行 `docker compose down`（未删除卷），再执行 `docker compose up -d --wait` 重建全部容器。
- 重建后确认用户、会话、班级、成员关系及文件内容完整保留，并再次验证管理员登录。
- Chrome 实际访问生产网页并验证登录、退出及静态资源/API 代理，无页面脚本错误；截图为 `.local/screenshots/docker-login.png`。
- 临时验收账号、班级、讲义与文件已清理。Docker 环境保留初始管理员，不包含开发演示数据。
- OpenSpec 任务 4.4 已勾选，首期 12/12 项全部完成，变更保持未归档。

首次构建遇到网络代理问题，最终使用现有系统代理和 Docker Desktop 构建代理完成；具体重建方法见 README。没有修改全局代理设置，也没有将代理凭证或管理员密码写入源码。

## TXT / MD 扩展验收（2026-09-23）

OpenSpec 变更 `add-text-handouts` 已完成 4/4 项，严格校验通过。新增后端 UTF-8 流式校验，覆盖中文、BOM、跨缓冲区字符、合法替换字符、空文件、仅 BOM、GBK/UTF-16、无效编码、NUL 和其他控制字符。

- `go test ./...`、`go vet ./...`、前端生产构建通过。
- 真实 PostgreSQL 集成测试扩展为 **73 项 HTTP 检查**，包含 TXT/MD 原始字节、MIME、附件行为、大小写扩展名、权限及无效文本拒绝，已有 PDF/Office 检查保持通过。
- Docker api/web 已重建并更新，保留原数据库与上传卷，三个服务 healthy。
- `node scripts/deployment-test.mjs --skip-recreate` 通过：Chrome 实际选择并上传中文 TXT 和 MD，下载字节一致，班级学生可访问，上传时声明通用二进制 MIME 不影响后端正确识别。
- 本次没有额外停止数据库或执行 down；上一节完整容器重建持久化验收仍为首期记录。此次临时验收数据已清理。

## 班级知识库实施中验证（2026-09-30）

`class-scoped-knowledge-search` 已进入实施阶段。当前代码层检查结果：Go 单元测试与 `go vet` 通过，Linux amd64 静态编译通过，前端 TypeScript 与 Vite 生产构建通过，OpenSpec 三个未归档变更严格校验通过；真实 PostgreSQL 的既有 73 项 HTTP 回归和 Chrome 既有业务回归均通过。

本节不代表知识库容器验收已经完成。当前 Docker Desktop 4.91.0 在启动时因 Windows 本地 `sailor-ingest.sock` 无法访问而退出，Docker API 不可用，因此 pgvector 迁移、七种格式容器解析、真实 Qwen3-Embedding 召回、备份恢复及新知识库浏览器流程仍待宿主机重启后执行。现有数据库卷、上传卷和模型卷没有被删除，也没有使用恢复出厂设置。

## MySQL + Qdrant 底座迁移验证（2026-10-01）

`mysql-qdrant-platform` 变更把关系库由 PostgreSQL 18 + pgvector 换成 MySQL 8.4、向量库换成 Qdrant v1.15.1，嵌入改由服务端课程网关调用，并新增切片策略与种子材料导入。本节只记录本次实际执行并通过的检查；环境为 Windows、Node 24、Go 1.26、Docker Desktop（Compose）。

- 代码检查：`go vet ./...`、`go test ./...` 通过，Linux amd64 静态交叉编译通过。共 29 项测试：无外部依赖时 17 项；补齐 `MYSQL_TEST_DSN` 与 `QDRANT_TEST_URL` 后 28 项；再提供 `MYSQL_TEST_DSN_NGRAM3` 时 29 项全部执行。覆盖任务领取、租约过期、归档跳过、退避与终止、发布与覆盖统计、归档暂停、源文件变化拒绝发布、中文全文命中、ngram 守卫、集合三分支、阈值与跨班过滤、嵌入失败不写向量、切片窗口与重叠、分隔符与标题分章、策略回退说明、网关错误与零向量校验。
- MySQL 实例：8.4.11，`@@ngram_token_size`=2，utf8mb4，`@@time_zone`=+00:00；启动参数按 Compose 固定，应用启动时的 ngram 与字符集校验生效。
- Compose 全栈：`db`、`qdrant`、`api`、`worker`、`web` 五个服务均为 healthy；只有 web 发布 `127.0.0.1:8080`，经 nginx 访问 `/api/health` 返回 ok。
- 数据迁移：`scripts/migrate-postgres-to-mysql.mjs` 先在数据卷副本（`<卷名>-drill`）上演练，再以 `--apply` 实际写入 MySQL：users 3/3、classrooms 1/1、memberships 1/1、handouts 0/0、sessions 3/3；重复执行行数不变，53 个上传文件 SHA-256 未变；同名账号按 username 建立 ID 映射后改写外键。
- 种子材料：`SEED_ENABLED=true` 首次启动导入 1 份材料并产生 1 个排队任务，索引代次记录 `window/800/80`；重复启动不重复导入；`SEED_ENABLED=false` 不导入；非成员下载讲义与读取索引状态均返回 403。
- 本地无 Docker：`npm run dev:local` 在 55432 启动便携 MySQL，并同时拉起 `.local/server.exe` 与 Vite（5173）；`npm run test:e2e` 在 55433 启动独立实例，**73 项真实 MySQL HTTP 检查**通过。
- 浏览器回归：`node scripts/browser-test.mjs` 通过（登录、创建教师、班级创建与检索、上传下载、学生注册/入班/退班、归档恢复、编辑、资源、手机布局），无页面脚本错误。
- 容器部署验收：`node scripts/deployment-test.mjs` 通过——容器内登录、角色、班级、入班、上传下载；重建容器后账号、会话、班级成员关系与上传字节完整保留；浏览器 TXT/MD 选择上传与学生访问；生产前端资源、登录退出与 API 代理；临时数据清理后五个服务仍为 healthy。
- 容器加固：worker 以非 root（`User=app`）运行，根文件系统只读（`ReadonlyRootfs=true`），`no-new-privileges` 生效，上传卷以只读方式挂载（`rw=false`）。
- 备份与恢复演练：`mysqldump`（3.5 秒，17 KB）与上传卷打包（0.8 秒）后恢复到临时 MySQL 实例（1.8 秒），11 张表的行数逐项一致（`3/1/1/0/4/1/0/0/0/0/1`），`schema_migrations` 与 ngram 全文索引在恢复库中同样存在。
- 回滚演练：旧 PostgreSQL 数据卷的副本可在 2.9 秒内启动，users=3、classrooms=1、memberships=1、handouts=0、sessions=3 与迁移前一致，说明回滚目标仍然可用；演练在副本上进行，原卷未被改动。
- 切片策略回退：策略不适用时（无 Markdown 标题、找不到所选分隔符）回退为默认窗口并在索引状态中给出中文说明；该行为由单元测试覆盖，索引状态接口新增 `strategy`、`requested_strategy` 与 `chunk_note` 字段（迁移 `002_chunk_note.sql`）。

尚未完成，不计入通过：

- 真实课程网关的中文检索验收：`npm run test:knowledge`（`scripts/knowledge-test.mjs`）已按新栈改造（读取网关、Qdrant 与 MySQL 配置），但尚未在可用的课程网关下执行，网关模型版本、维度与检索耗时待记录。
- 本地无 Docker 路径只启动便携 MySQL，不启动 Qdrant，因此 `npm run dev:local` 的知识库页面在未配置向量库与网关时不可用；知识库功能目前只在 Compose 环境验证。

### 七种格式的容器内解析与定位验收（2026-10-01）

在 `zhixu-teaching-api` 镜像内运行解析测试（镜像含 poppler 与 LibreOffice），样例文件由 python-docx／python-pptx 生成、再用容器内 LibreOffice 转出 DOC/PPT/PDF：

| 格式 | 解析单元 | 定位 | 转换标记 |
| --- | --- | --- | --- |
| PDF | 1 | 第 1 页 | 否 |
| DOCX | 2 | 提取正文第 1–2 段 | 否 |
| DOC | 2 | 提取正文第 1–2 段 | 是 |
| PPTX | 1 | 第 1 张幻灯片 | 否 |
| PPT | 1 | 第 1 张幻灯片 | 是 |
| TXT | 2 | 第 1–2 行 | 否 |
| MD | 4 | 第 1–7 行 | 否 |

每种格式都继续按默认窗口策略切片成功（`TestExtractionInContainer`，在容器内 5.7 秒通过）。同一批文件经真实上传接口上传后，七份讲义的索引状态均为 `failed` / `temporary_failure`——即**解析全部成功**，只因课程网关尚未配置而停在嵌入步骤；解析失败会得到 `unsupported` 或 `no_text`，与此可区分。

（该测试需要 poppler 与 LibreOffice，宿主机通常没有，因此通过 `FORMAT_CHECK_DIR` 在容器内执行；命令见 `docs/knowledge-base.md` 的验收一节。）

OpenSpec 变更 `mysql-qdrant-platform` 的隔离、容器与网关验收任务仍保持未勾选项，需在上述两项补齐后依据实际输出勾选；本文档不把未执行的检查计为通过。

## 三种检索模式实现验证（2026-10-01）

`knowledge-retrieval-modes` 变更在既有单路向量检索之上加入 `keyword` 与 `hybrid`，并把结果契约统一为 `hits`。本节记录已经实际执行并通过的部分。

- 模式实现：`keyword` 只查询 MySQL ngram 全文索引；`vector` 走 Qdrant 余弦并丢弃低于 0.35 的候选；`hybrid` 两路各自过滤后各取前 50 条，按 RRF（k = 60）融合，两路均命中的切片必然排在仅单路命中之前。融合只用于排序，不对外作为相关性阈值。
- 单元与数据库层测试（真实 MySQL + Qdrant，共 30 项）：关键字模式对「数据库 / 向量检索 / 切片」等中文词命中、对不存在的词零命中、并限定班级；`TestKeywordModeDoesNotCallEmbedding` 用计数用的网关替身断言关键字模式零请求，同时断言 `vector` 与 `hybrid` 在网关故障时明确报错而不是静默降级；`TestHybridFusionRanksDualPathFirst` 断言双路命中排前、单路同分时的稳定次序与 `top_k` 截断。
- HTTP 契约实测（Compose 环境，经 nginx）：空查询、未知 `mode`、`top_k=21` 均返回 400；`top_k` 缺省返回 200；请求体携带他班 `class_id` 时被丢弃而不是 400；班级无已发布索引时返回 200 `no_ready_documents` 且 `hits` 为空、`message` 为“尚无可检索的讲义”；未配置网关时 `keyword` 仍可返回 200，而 `vector`/`hybrid` 在有索引时返回 503。
- 前端：知识库页新增关键字/语义/混合模式选择、本轮模式标注，命中项展示切片序号与字符区间；结果字段改用 `hits`，无命中显示接口返回的「资料中未找到相关内容」。前端生产构建通过。

已由「真实模型下的端到端验收」补齐：三模式召回对比、跨班检索的完整 HTTP 回归、浏览器端模式切换回归（含 390px）。关键字下限的依据见下段。

关键字模式的召回测量已单独完成（切片直接入库，绕过嵌入服务）：在 20 条标注问题上，关键字模式 **20/20** 在前 5 条命中来源切片；5 条无关查询共产生 **0** 条命中。据此关键字路径的绝对过滤下限取「至少命中一个二字 token」（`keywordMinScore = 0.0`），当前语料下没有出现低相关切片进入前 5 条的情况。

## 解题助手（提示词、技能、会话与流式回答）验证（2026-10-13）

`solver-assistant` 变更交付提示词与技能、会话与 SSE 流式回答，以及复用会话表的不透明 Bearer 令牌。以下为实际执行并通过的部分。

后端（`npm run test:db`，便携 MySQL + 便携 Qdrant，实测 `ok 85.2s`）：

- 迁移 003：`prompts`（含 VIRTUAL 生成列 `scope_key` 与唯一键）、`chat_sessions`、`chat_messages` 建立成功；重放 DDL 与再跑一次迁移器都不报错且旧数据不变（`TestPromptAndSessionTablesShape`、`TestMigrationIsIdempotent`）。
- 内置内容：平台默认提示词与「解题引导」技能各写一条；管理员改过内容后重启不被覆盖；无班级行时提示词回退平台（`TestPlatformPromptsSeededIdempotently`）。
- 层级与级联：平台级与班级级同名技能共存、同层级同名触发 1062、班级行覆盖平台行、班级行 `enabled=0` 表示停用、删除班级后班级级提示词与会话消息被清理（`TestPromptScopeUniquenessAndCascade`；需先删讲义再删班级，因为 `handouts.class_id` 外键没有级联）。
- 令牌（`TestTokenIssuance`、`TestBearerTokenLifecycle`）：`POST /api/auth/token` 返回 `token`/`expires_at`/`user` 且**不写 Cookie**；数据库只存哈希、查不到原文；错误凭证与未知账号都返回 401 且消息一致；带令牌可读 `/auth/me`，伪造令牌 401，过期令牌 401；登出撤销后立即 401；学生被移出班级后令牌访问该班 403。
- 同源边界（`TestSameOriginBoundaryForTokens`）：仅令牌的跨站写请求放行；仅 Cookie 的跨站写请求 403「不允许跨站请求」；同时带令牌与 Cookie 时按 Cookie 规则 403；同源令牌请求正常。
- 流式客户端（`TestChatStreamEmitsDeltas`、`TestChatStreamRejectsTruncatedStream`、`TestChatStreamFailures`、`TestChatStreamStopsWhenEmitFails`、`TestChatStreamRequiresConfiguration`）：逐块回调并保留顺序；**流在没有 `[DONE]` 就结束会被判为截断并报错**（实现过程中发现并修掉的缺陷：原先会被当成完整回答）；非 200、网关忽略 `stream` 参数返回整段 JSON、只有结束标记、首字节超时四种情形都明确报错，不做“假流式”；回调失败立即停止读取上游。
- 会话与 SSE（`TestChatSessionFlowAndEvents`、`TestChatSessionList`、`TestChatNoMatchesSkipsChatGateway`、`TestChatInterruptionIsRecorded`、`TestChatCitationsAndOutOfRangeMarkers`、`TestChatIsolation`）：事件顺序为若干 `delta` → `citations` → `done`，响应头含 `text/event-stream`、`X-Accel-Buffering: no`、`Cache-Control: no-store`；用户消息与助手消息顺序号连续并落库，首条用户消息生成会话标题；**无命中时计数替身记录的对话调用次数为 0**，只返回固定文案与空出处；客户端取消后助手消息记为 `interrupted` 且不存在 `complete`；越界编号 `[7]` 被去掉方括号、范围内的 `[1]` 保留，出处可由来源接口逐字核对；非成员 403、跨班 404、归档只读、移除成员立即失效。

前端（本地栈：便携 MySQL + 便携 Qdrant + API + worker + Vite，嵌入与对话指向仓库自带替身；`.local/assistant-ui-check.mjs`，**11/11 通过**）：

- 讲义上传后索引就绪（`status=ready`）。
- **流式增量分多次渲染**：轮询助手气泡文本观察到 13 个不同长度，说明是逐块到达而不是一次性渲染。
- 回答含 `[1]`，出处卡片可打开「来源详情」且正文与材料一致。
- 无依据提问：显示「资料中未找到相关内容」，该气泡出处数量为 0。
- 「停止生成」：中断消息以 `.bubble.interrupted` 呈现，服务端日志同时记录「回答中断: 读取流式响应失败: context canceled」。
- 提示词与技能：保存班级提示词后状态变为「使用班级提示词」；内置「解题引导」可停用再启用；新增班级技能后「移除覆盖」可恢复平台状态。
- 390×844 视口横向溢出 0px；1440px 与 390px 均无页面脚本错误；截图见 `.local/screenshots/assistant-*.png`。
- 既有浏览器回归（`scripts/browser-test.mjs`）与 73 项 HTTP 集成在同一套新代码上继续通过。

本轮顺带修掉的开发体验问题：

- `npm run dev:local` 原先只起 API、没有任何 worker，上传的讲义永远不会被索引（知识库页只能显示“尚无可检索的讲义”）；现已同时启动 worker。
- 仓库自带的开发替身补齐 `/v1/embeddings`、`/v1/chat/completions` 与 **SSE 流式返回**（分片间隔可用 `STUB_CHUNK_DELAY_MS` 调整），因此没有 Docker 与大模型也能验证流式前端。
- Vite 增加 `watch.ignored`：编辑器保存时写入的临时目录会让 dev server 以 `EBUSY` 崩溃，进而拖垮整个本地栈。

尚未完成，不计入通过：无。经 web 容器（nginx）的容器级验证已在 2026-10-14 完成，见本文件「容器级验收」一节。

## 真实模型下的端到端验收（2026-10-01）

课程网关凭证未提供，因此用**兼容 OpenAI 的两个真实模型服务**完成等价验收：嵌入走本机 Ollama 的 `qwen3-embedding:0.6b`（1024 维，与集合一致），对话走 DeepSeek 的 `deepseek-flash`。两者都是真实模型推理，不是替身；服务端注入密钥、浏览器不接触密钥的边界不变。**课程网关到位后需用同一条命令复跑，以记录课程指定的模型版本。**

命令与接入方式（`.local/compose-localmodel.yaml` 只加在本地，仓库 Compose 仍保持 api/worker 仅接 internal 网络）：

```powershell
node scripts/ollama-runtime.mjs start          # 首次下载便携包并拉取嵌入模型
docker compose -f compose.yaml -f .local/compose-localmodel.yaml up -d api worker
npm run test:knowledge
$env:KNOWLEDGE_MODE_BASELINE = "1"; npm run test:knowledge   # 附带三模式基线
```

`node scripts/knowledge-test.mjs` 实测结果（**真实模型，非替身**：`deterministicStub=false`）：

- **PASS：20/20 前 5 条命中来源切片，5 条无关查询被拒，跨班泄露 0，共 151 项 API 检查。**
- 三模式召回基线（20 条标注问题，`top_k=5`）：**keyword 20/20（中位 18 ms）、vector 17/20（中位 60 ms）、hybrid 20/20（中位 61 ms）**；向量阈值 0.35，混合融合 RRF k=60。
- 索引与发布：上传讲义索引成功（`window/800/80`、状态 ready），不可提取文字的扫描件进入 `unsupported` 且不参与检索（`partial=true`、`ready_documents` 与 `total_documents` 正确）。
- 三种模式对外契约一致；携带他班 `class_id` 被丢弃；无关问题在三模式下都返回 `no_matches`。
- 问答（真实对话模型）：
  - 有依据提问 → 回答「课程考核由平时作业（40%）和期末项目（60%）组成。[1]」，1 条出处（序号 1、字符区间、`第 1–22 行` 定位、`download_url`），HTTP 200、**1.25 s**；服务端日志记录「材料 1 条，历史 1 条，提示 590 字，耗时 1110 ms，模型 deepseek-flash」。
  - 无依据提问 → `status=no_matches`、`answer=资料中未找到相关内容`、`citations=[]`，HTTP 200、**0.083 s**，且日志中**没有**问答遥测行，现场证明该路径未调用对话模型。
- 验收后数据库与向量库均无残留：users=3、classes=1、handouts=0、chunks=0、jobs=0、Qdrant 点 0。

本轮同时补上三处可观测性缺口（此前失败只能看到 `temporary_failure` 代码）：worker 在任务失败时记录 code 与原因、发布成功时记录切片数与策略；检索在模型/向量库调用失败时记录模式与原因；问答记录提示长度、历史条数、耗时与模型名。

### 问答与引用实现验证（单元与数据库层，2026-10-01）

`knowledge-answer-citations` 变更加入问答接口：先检索、仅在命中本班切片时才调用对话模型。本节记录已经实际执行并通过的部分。

- 对话模块：课程网关对话客户端与嵌入客户端相互独立，各自校验自身配置；`/chat/completions` 请求显式携带 `stream: false`（本期不做流式）。替身覆盖未配置、超时、非 200 与空回答四种失败，均返回明确错误而不重试或改发其他服务。
- 提示组装：只包含材料标题、切片序号、切片正文、用户最新一句与此前轮次；客户端构造的 `system` 消息与其他角色一律丢弃，历史最多 6 条、单条与总量均限长。单元测试断言提示中出现标题/序号/字符区间/正文/问题，且不含 `class_id`、`embedding`、`generation_id` 等内部字段。
- 引用校正：回答中的 `[n]` 只有落在命中范围内才作为引用呈现，越界编号去掉方括号后保留数字文本；`citations` 始终由服务端从命中切片生成。用「只命中 1 条却写出 `[7]`」的替身输出验证：回答中不再出现 `[7]`，出处列表仍只有真实命中。
- 接口行为（数据库层测试 + HTTP 冒烟）：无命中时返回 `status=no_matches`、`answer=资料中未找到相关内容`、`citations=[]`，并且对话网关调用次数为 0；有命中时调用一次对话网关并返回带 `index`／`ordinal`／字符区间／摘录／`download_url` 的出处列表；网关不可用返回 503 且不返回部分回答；空提问返回 400（校验先于网关可用性判断）；响应头带 `Cache-Control: no-store`。
- 前端：知识库页新增「生成回答」入口与简短回答区块，出处列表按编号列出材料标题、切片序号、字符区间与摘录，可打开来源或下载原文件；切换班级、切换模式或重新检索时清空上一轮回答。前端生产构建与 73 项集成检查通过。

已由「真实模型下的端到端验收」补齐：有依据提问、连续追问（浏览器）、5 条无关问题返回固定文案且不调用对话模型（日志中无遥测行）。

### 确定性替身下的端到端验收（2026-10-01）

课程网关暂不可用，因此另用一个**确定性替身**（`backend/cmd/gateway-stub`：以字符二元组特征哈希生成向量、对话接口返回确定性回答）把整条链路跑通。替身通过只存在于 `.local/`（已忽略提交）的 Compose 覆盖文件接入，仓库中的 Compose 定义不含它。**它是功能替身，不是语义模型**：以下结果证明的是权限、隔离、状态、契约、引用顺序与前端流程，不能作为召回质量证据。

`node scripts/knowledge-test.mjs`（`KNOWLEDGE_STUB=1`）：**PASS：20/20 前 5 条命中来源切片，5 条无关查询被拒，跨班泄露 0，共 92 项 API 检查**。覆盖：

- 索引与发布：两份讲义建成可检索索引，切片带 1 起始序号与字符区间。
- 三种模式：模式被回显；关键字模式命中直接出现在原文中的中文词；无关问题在三种模式下都返回 `no_matches` 与「资料中未找到相关内容」和空命中；请求体携带他班 `class_id` 时被丢弃，仍只返回本班结果。
- 引用与来源：批注脚本片段只作为文本显示（页面未执行脚本），「查看原文出处」打开来源详情；回答区块展示简短回答与编号一致的出处，出处摘录能在来源接口逐字核对。
- 状态区分：不可提取文字的扫描件进入不可索引状态、不参与检索，同时 `partial=true`、`ready_documents=1`、`total_documents=2`。
- 索引重建与删除：重建后旧引用返回 409、新引用与新切片一致；删除讲义后旧引用返回 404。
- 隔离：外班学生检索与提问都返回 403，响应中不含外班金丝雀文本；移除成员后检索与下载立即被拒。
- 浏览器：登录 → 班级 → 知识库 → 检索 → 出处 → 切换关键字/混合模式重新检索（结果区标注本轮模式）→ 生成回答 → 连续追问 → 无关问题不展示回答，无页面脚本错误。
- 故障路径：用 `KNOWLEDGE_OUTAGE_CMD` / `KNOWLEDGE_RESTORE_CMD` 停掉网关后，`vector`、`hybrid` 检索与问答都返回 503（不返回部分回答），而**关键字检索仍正常返回**、讲义下载内容不变；恢复网关后链路继续可用。命令在宿主机执行，正式验收时可指向真实的网关停机与恢复操作。
- 手机宽度（390×844）：同一流程（检索、生成回答、出处列表）在手机视口下 `scrollWidth - innerWidth <= 1`，无横向溢出、无页面脚本错误；桌面与手机截图分别写入 `.local/screenshots/knowledge-desktop.png` 与 `knowledge-mobile.png`（`*.png` 不在提交范围内，供人工查看，自动化检查只断言溢出与脚本错误，未做人工视觉确认）。

同一替身下 73 项既有 HTTP 回归、容器部署验收与前端生产构建全部通过。剩余待办只有一项：在真实课程网关上重跑同一脚本（`KNOWLEDGE_MIN_HITS` 保持默认 18）以获得召回质量与模型版本的正式记录。

### 登录被误判为跨站请求的修复（2026-10-01）

人工验收时反馈「登录提示不允许跨站请求」。原因是写请求的同源判定只接受与 `APP_ORIGIN` 字面相同的来源，而 `.env` 里写的是 `http://localhost:8080`，用 `http://127.0.0.1:8080` 打开时 `Origin` 不匹配即被拒（nginx 还用 `Host $host` 转发不带端口的 Host，使后端无法自行比对）。

修复：`sameOrigin` 改为「浏览器标注 `cross-site` 直接拒绝；没有 `Origin` 视为同源；`Origin` 主机与请求 `Host` 一致即通过（含去端口兜底）；否则必须命中 `APP_ORIGIN`（支持逗号分隔多个来源）」，nginx 改为 `proxy_set_header Host $http_host` 以保留端口。回归覆盖：`TestSameOrigin` 10 个子用例 + `TestSameOriginAllowsListedOrigins`。

实测（重建 api 与 web 后，真实 Chrome）：`http://localhost:8080` 与 `http://127.0.0.1:8080` 都能登录成功；`Origin: http://evil.example` 与 `Sec-Fetch-Site: cross-site` 仍返回 403。73 项 HTTP 集成、容器部署与浏览器验收、前端生产构建在改动后全部通过。

### 不依赖 Docker 的数据库层测试入口（2026-10-01）

新增 `npm run test:db`：自动启动便携 MySQL（55434，`mysqld --initialize-insecure` 首次约 12–15 秒）与便携 Qdrant（16334，首次自动下载约 28 MB 官方二进制到 `.tools/qdrant`，已忽略提交），然后运行后端全部 Go 测试，结束即关闭两个实例。实测结果：`ok teaching-platform 41.2s`，退出码 0，过程中没有使用 Docker。

这条入口覆盖了需要真实数据库才能验证的部分（方言与行锁、生成列唯一键、任务领取与租约、发布与回滚、ngram 中文全文命中、Qdrant 集合维度与阈值、跨存储残留点），使这些验证在只有 Windows 与 Node/Go 的机器上也能复现。Qdrant 下载在网络受限时会依次回退到 `curl.exe` 与 PowerShell，并把各次失败原因一起报出；两个实例的存储目录按端口隔离，因此可以和 `dev:local` 同时运行。

`npm run dev:local` 现在也启动便携 Qdrant（16333），本地预览不再需要 Docker 就能让 API 建好 `campusclaw_chunks` 集合；索引仍需要可用的课程网关。

### 本轮修复的回归与缺陷（2026-10-01）

- **上传在网关未配置时会整单失败**：`queueKnowledge` 在网关未配置（因此没有嵌入配置行）时仍写入 `knowledge_generations.profile_id`，触发外键失败，导致上传事务回滚——违反「上传不依赖模型通道」。新增 `queueKnowledgeForUpload`：网关未配置时跳过排队，上传照常成功、讲义显示为未建立索引，配置完成后由所有者建立索引。容器环境实测：上传返回 201，`knowledge_documents` 与 `knowledge_jobs` 均为 0 条，索引接口返回 503 并说明原因；`npm run test:db` 增加对应回归测试。
- **验收脚本清理不彻底**：`handouts.class_id` 外键没有级联，班级有讲义时直接删除班级会失败，失败运行的临时班级与讲义因此残留。清理语句改为先删讲义再删班级、成员与账号，失败时打印告警。实测：一次完整验收后数据库回到基线（users=3、classes=1、handouts=0、chunks=0、jobs=0），Qdrant 残留点为 0。
- 另有三个工具层缺陷：`test:db` 的 `finally` 里 `process.exit` 吞掉了真实错误（改为先打印再清理）；Node `fetch` 下载无超时导致卡死（加超时与多级回退）；便携运行时把进度写到 stderr 会让 PowerShell 误报非零退出码（改为收集后从 stdout 输出）。

### 种子导入在排队逻辑变更后的复核（2026-10-01）

上一处修复改了上传的排队条件，因此重新验证种子材料这条路径：

- 网关未配置时启动：种子材料被导入为普通讲义（示例班级 1 份），`knowledge_documents`、`knowledge_generations` 与 `knowledge_jobs` 均为 0——不再产生会破坏外键的记录，上传与导入都不依赖模型通道。
- 随后配置网关（替身）重启：同一份种子材料被重新排队并索引完成（`jobs=1`、`docs=1`、`chunks=1`，代次记录 `window/800/80`、状态 `ready`），说明「先部署、后配置网关」的补建索引路径可用。
- 检索与问答：关键字检索命中该切片（含 1 起始序号与字符区间），问答返回回答与 1 条出处。
- 复核后清理：经接口删除讲义（同时清理向量），删除示例班级，数据库回到基线（users=3、classes=1、handouts=0、chunks=0、jobs=0），向量库残留点为 0。

## 接口契约与文档页面验证（2026-10-13）

`openapi-swagger` 变更交付 OpenAPI 3.1 契约、自托管文档页面与路由一致性检查。

契约与提供方式：

- `docs/openapi.json`：45 KB，OpenAPI 3.1，涵盖 30 条路径 / 39 个操作；每个操作都有 `summary`、参数或请求体、成功响应与错误码，并按是否登录标注 `security`（`cookieAuth` 与 `bearerAuth`）。
- `GET /api/openapi.json` 返回的内容与仓库文件**字节一致**（`TestApiDocumentationEndpoints` 断言）；`GET /api/docs` 返回 1.4 KB 的文档页面，页面与资源（`swagger-ui.css` 182 KB、`swagger-ui-bundle.js` 1550 KB 等 7 个文件，Apache-2.0）全部由服务自托管。
- 路由注册已整理为可枚举的 `routeSpec` 表（`backend/routes.go`），注册与文档检查共用同一份数据；重构后 73 项 HTTP 集成与全部 Go 测试通过，说明对外行为未变。

一致性检查（`go test`，无需网络）：

| 测试 | 覆盖 |
| --- | --- |
| `TestOpenAPIMatchesRoutes` | 实现与契约的操作集合双向一致（缺失与多余都报错） |
| `TestOpenAPISecurityMatchesRoutes` | 受保护操作必须有 `security`、匿名操作必须没有，且每个操作有 `summary` |
| `TestOpenAPIPathParametersExist` | 契约声明的路径参数与实现中的 `{name}` 双向一致且 `required` |
| `TestOpenAPICopyIsCurrent` | `backend/openapi.json` 与 `docs/openapi.json` 字节一致（副本不过期） |
| `TestApiDocumentationEndpoints` | 契约与页面可读、页面不引用外部地址、资源存在、目录穿越与未知资源返回 404 |

反向验证（实施过程中真实发生，非构造）：`TestOpenAPIPathParametersExist` 抓出 `PUT /api/admin/skills/{name}` 漏声明路径参数 `name`；补上后通过。这正是该检查存在的意义。

文档页面（真实 Chrome，`node .local/api-docs-check.mjs`，**6/6 通过**）：

- 用账号密码换取令牌成功（`POST /api/auth/token`）。
- 页面渲染出 43 个操作块（含分组与说明）。
- 监听全部请求：**没有任何外部域名请求**，只访问本站 `/api`。
- 未填令牌时对 `GET /api/auth/me` 点 Execute：**网络响应 401**，页面显示「请先登录」。
- 在 Authorize 填入 Bearer 令牌后再次 Execute：**网络响应 200**，响应体含真实用户信息（`admin`）。
- 无页面脚本错误；截图 `.local/screenshots/api-docs-unauthorized.png` 与 `api-docs-authorized.png`。

契约错误码对照（逐条执行）：超长查询 400、未知 `mode` 400、未登录 401、空提问 400、带 Cookie 的跨站写请求 403 —— 与契约描述一致。

实现方式的两点偏差（已在 `tasks.md` 中记录）：

1. `go:embed` 不能读取包目录之外的路径，因此契约与 Swagger 资源由 `scripts/sync-openapi.mjs` 同步到 `backend/`（`openapi.json`、`swaggerui/`）；`TestOpenAPICopyIsCurrent` 与服务端 `--check` 双重拦截副本过期。
2. 因此文档页面与资源改由 API 在 `/api/docs` 提供，不再改动前端构建与 nginx：dev 与 Compose 两种形态走同一条路径，也不会受 SSE 缓冲配置影响。

尚未完成，不计入通过：无。经 web 容器访问 `/api/docs`、`/api/openapi.json` 与静态资源均已在 2026-10-14 实测通过，见本文件「容器级验收」一节。

## 接口测试套件验证（2026-10-14）

`api-test-suite` 变更交付 `apitest/`：一个接口一个脚本，目录层级就是 URL，一键运行并生成 HTML 报告。

运行（本地栈：便携 MySQL 55432 + 便携 Qdrant + API 8081 + 仓库自带网关替身）：

```
npm run test:api          # 等价于 apitest/.venv/Scripts/python apitest/run_all.py
```

首次完整运行结果：**8/8 用例通过，耗时 6.1 秒**，报告写入 `apitest/report/index.html`（自包含、无外链、不提交）。
报告头部记录目标服务、管理员口令来源、数据库核对方式（`direct` 或 `compose-exec`）、用例数与总耗时。

| 用例 | 核对内容（不只状态码） |
| --- | --- |
| `GET /api/health` | 200 且 `status=ok` |
| `POST /api/auth/login` | 200、角色 `admin`、**下发 Cookie**、`sessions` 新增行、库里查不到 Cookie 原文、库中角色与响应一致 |
| `POST /api/auth/token` | 200、返回 `token`/`expires_at`/`user`、**响应无 `Set-Cookie`**、库里只存哈希、错误口令与未知账号都是 401 且提示一致、令牌可读 `/auth/me` |
| `POST /api/classes/{id}/knowledge/search` | 混合检索 `ok` 且命中属于本班讲义、切片在 `knowledge_chunks` 中存在且属于该班、来源接口返回同一切片；关键字与向量各一次；无关问题 `no_matches` + 固定文案 + 空命中；未知模式 400 |
| `GET`／`PUT /api/classes/{id}/prompts` | 平台默认已初始化、班级覆盖回显并核对 `prompts` 行、超长 400 且原内容不变、新增/停用/移除班级技能并核对 `scope/enabled`、**学生写入 403** |
| `POST /api/classes/{id}/sessions` | 201、`chat_sessions` 行落库且班级一致、列表包含新会话、超长标题 400 |
| `POST .../{sid}/messages` | ≥2 个增量事件、结束前有出处、`done=complete`、出处 1–4 且编号从 1 开始、回答含 `[1]`；`chat_messages` 两行序号 1/2、助手状态 `complete`、落库内容与流式增量逐字一致、出处已落库；无依据时固定文案 + 空出处 + `no_matches`；空白消息 400 |

无依据提问**不调用对话模型**这项由外部观测确认：报告中的断言「无依据时不调用对话模型 — 期望 0，实际 0」
取自仓库自带网关替身的 `/stats` 计数（`APITEST_GATEWAY_STATS_URL` 可覆盖；真实网关环境下该断言改为注明由
后端测试 `TestChatNoMatchesSkipsChatGateway` 覆盖，不会伪装成通过）。

公共库自检：`apitest/.venv/Scripts/python apitest/selfcheck.py` → **16/16 通过**，覆盖 `Api` 的 200/401/403、
SSE 逐事件解析（含心跳与 UTF-8 解码）、`Database` 的查询与 `query_one`/`query_all`、
compose-exec 模式的 SQL 字面量转义与命令拼装、`report` 的单文件/转义/差异渲染。

清理与幂等（连续两次运行）：

| 表 | 基线 | 第 1 次后 | 第 2 次后 |
| --- | --- | --- | --- |
| users | 15 | 15 | 15 |
| classrooms | 16 | 16 | 16 |
| handouts | 9 | 9 | 9 |
| knowledge_documents | 9 | 9 | 9 |
| knowledge_chunks | 7 | 7 | 7 |
| chat_sessions | 8 | 8 | 8 |
| chat_messages | 36 | 36 | 36 |
| prompts | 7 | 7 | 7 |
| sessions | 60 | 64 | 68 |

两次运行都是 8/8；业务表零残留。`sessions` 每次 +4 属预期：用例本身要验证登录与令牌签发，这些会话行不是临时数据。

其余行为验证：

- **发现机制**：临时新增 `cases/api/tmp-verify-check/GET.py` 后无需改运行器即被执行，随后删除（用例数 1）。
- **失败处理**：把断言改为必失败后，退出码 1，报告出现「失败」标记与「期望 1，实际 2」差异；
  该用例的 `setup` 仍然创建了临时班级，运行后基线不变 —— teardown 在失败时照常执行。
- **前置缺失**：只运行会话消息用例时报告 `[FAIL] 前置未满足：session_id`，不是静默跳过，退出码 1。
- **依赖缺失**：用未安装依赖的系统解释器运行，输出安装命令提示并以退出码 2 结束（不再抛 `ImportError`）。
- **忽略提交**：`git check-ignore -v` 确认 `apitest/report/index.html` 与 `apitest/.venv` 都命中 `.gitignore`。

实施过程中发现并修掉的问题（都有回归保护）：

1. **产品缺陷**：班级自建技能无法只改启用状态。服务端原先只在「平台存在同名技能」时才允许省略正文，
   于是 `PUT /api/classes/{id}/skills/逐步提示` 只发 `{"enabled":false}` 会被 400 拒绝（前端的自建技能
   启用/停用按钮也会踩到）。改为：省略正文时先沿用本班已有正文，再回退平台同名技能；平台技能同理。
   回归测试 `TestSkillToggleKeepsClassBodyWithoutResending`（修复前该用例失败）。
2. **索引可见性竞态**：`knowledge_documents.status` 变为 `ready` 与实际可检索之间存在极短时间差，
   刚上传就检索会偶发 `no_matches`。测试侧增加关键字探针 `wait_searchable`，确认「查得到」再开始断言。
3. **SSE 中文乱码**：客户端 `iter_lines(decode_unicode=True)` 在响应头缺少 `charset` 时按 latin-1 解码，
   中文变乱码；改为显式 `response.encoding = "utf-8"`。自检脚本用本地假事件流覆盖这一点。
4. **依赖自检形同虚设**：`run_all.py` 在自检之前就 `import requests`，缺依赖时直接抛 `ImportError`；
   改为自检通过后再导入公共库。
5. **限流误报**：管理员口令探测遇到产品级登录限流（429）时会笼统提示「缺少口令」；
   现在区分 429 并给出「等待窗口或重启本地 API」的明确建议。
6. **清理用错身份**：teardown 用共享会话（管理员 Cookie）删讲义会被 403 拒绝；改为使用所属教师令牌。
7. **断言口径错误**：一开始要求「改写后的问法」在向量模式下也命中。仓库替身是字符二元组特征哈希、
   不是语义模型，改写问法达不到余弦阈值属正常；向量断言改用讲义原句（实测相似度 0.7075），并在用例注释中写明。

尚未完成，不计入通过：无。`direct` 与 `compose-exec` 两种模式都已实测通过（见下）。

## 容器级验收（2026-10-14）

Docker Desktop 启动后（引擎 29.8.0），用 `docker compose -f compose.yaml -f .local/compose-verification.yaml up -d --build api worker web`
重建并启动了新代码；api/worker 通过 `host.docker.internal` 接入宿主机上的开发替身（嵌入模型名沿用库里存量档案的
`qwen3-embedding:0.6b`——api 启动时会校验「当前配置与档案存储版本一致」，换名字会被正确拒绝，这是防串模型的保护）。

`node .local/container-verify.mjs`（经 nginx 的 web 容器，**11/11 通过**）：

| 检查 | 结果 |
| --- | --- |
| 经 web 容器打开 `/api/docs` | 200，返回文档页面 HTML |
| 经 web 容器读取 `/api/openapi.json` | 200，30 条路径 |
| 文档页面静态资源自托管 | `/api/docs/swagger-ui-bundle.js` 200 |
| 经 nginx 换取令牌、上传讲义 | 200 / 201 |
| 容器内 worker 完成索引 | `status=ready` |
| 流式响应头经 nginx 保持 | `text/event-stream; charset=utf-8`、`Cache-Control: no-store` |
| **经 nginx 收到多个增量事件** | **12 个 delta，分块读取 13 次** |
| **增量分批到达而不是一次性下发** | **首末分块间隔 3640 ms，最大间隔 325 ms** |
| 事件序列完整 | 以 `done: complete` 结束 |
| `api` 容器未发布宿主端口 | 只有 `8081/tcp`（只 expose） |

分批到达这一项是「nginx 没有缓冲 SSE」的直接证据：若被缓冲，所有增量会在结束时一次性到达。
`X-Accel-Buffering: no` 由 API 设置，但它是 nginx 自己消费的响应头，不会转发给客户端，因此不以此断言。

### 接口测试套件在 Compose 部署上运行（compose-exec 模式）

```
APITEST_BASE_URL=http://localhost:8080 npm run test:api
```

结果：**8/8 用例通过，15.6 秒**，报告头部记录「数据库核对：compose-exec（docker compose exec:-）」；
同一套用例在 `dev:local` 上以 `direct` 模式运行为 **8/8，6.6 秒**——两种核对方式结果一致。

这一轮验证发现并修掉一个真实缺陷：容器内 `mysql` 的批处理输出（`-N -B`）全部是文本、且把换行等字符转义输出，
于是 `COUNT(*)` 变成字符串 `'0'`、正文里的换行变成字面 `\n`，断言随之失败（首次运行 3/8）。
现在 compose-exec 解析会还原转义并把纯数字转回数值，两种模式的结果因此完全一致。

## MCP 工具服务验证（2026-10-27，第 6 课变更 `mcp-tool-server`）

自建工具服务 `mcp-server/`（Go 标准库）已实现并通过独立验收；本条目对应的是**服务本身**，宿主侧接入见后续变更 `tool-bridge`。

单元测试：`cd mcp-server && go vet ./... && go test ./...` → `ok mcp-server 3.3s`，覆盖协议错误码、令牌校验、
`initialize`/`tools/list` 结构、两个工具的执行、跨类换算的 `isError` 语义与 JSON Schema 子集校验器用例表。

容器与部署：

- `docker compose build mcp-demo` 成功；`docker compose ps mcp-demo` 的端口列只有 `8090/tcp`（只 expose，未发布宿主端口）。
- 从 `api` 容器访问 `http://mcp-demo:8090/health` 返回 `{"status":"ok","tools":2}`（同网互通）。
- 在构建后的镜像内检索令牌字面值为 **0 命中**（令牌只经环境变量注入）。

HTTP 级验收：容器里没有 curl、busybox 的 `wget` 又不支持自定义请求头，因此用 compose 内网的 Node 发请求
（`.local/mcp-check.mjs`，**17/17 通过**）：

| 检查 | 结果 |
| --- | --- |
| `GET /health` 无需令牌 | `{"status":"ok","tools":2}` |
| `initialize` | `protocolVersion=2025-06-18`、`capabilities.tools`、`serverInfo.name=campus-mcp-tools`，且不返回工具清单 |
| `tools/list` | 两个工具，均含名称、说明与输入结构；说明写明「不适用」的边界；顺序稳定（`formula_hint,unit_convert`） |
| `tools/call` `unit_convert` | `1.2 km = 1200 m（长度换算）` |
| `tools/call` `formula_hint` | 含公式、符号、下一步，且不含最终数值答案 |
| 跨类换算 | `isError=true`（业务失败），不是协议错误 |
| 未知方法 / 缺少必填参数 / 未知工具 / 非法 JSON | `-32601` / `-32602` / `-32602` / `-32700`，信息可读 |
| 无令牌 | HTTP 401 且响应体不含任何工具名 |
| 通知与非 POST | `notifications/initialized` → 202 空响应；`GET /mcp` → 405 并说明只接受 POST |
| 令牌不外泄 | 所有响应中检索不到令牌字面值 |

尚未完成，不计入通过：无。

## MCP 工具接入验证（2026-10-27，第 6 课变更 `tool-bridge`）

### 自动化

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| 后端全量测试（含工具链路用例） | `node scripts/test-db.mjs` | `ok teaching-platform 162.1s` |
| 接口测试套件（Compose 部署、compose-exec 模式） | `APITEST_BASE_URL=http://localhost:8080 npm run test:api` | **15/15 通过，34.0 秒** |
| 契约一致性（46 个操作，含 7 个新接口） | `go test -run TestOpenAPI` | 5 项全过 |
| 规约校验 | `npx openspec validate --all --strict` | 17/17 |

新增 Go 用例覆盖：登记/改名/删除与跨教师隔离、令牌只回 `has_token`、连接两步成功才替换清单、连接失败停在未连接且**不影响检索与流式对话主路径**、
连续连接不翻倍、断开幂等、技能工具项四种非法输入且**失败不改动既有内容**、候选集过滤与上限、参数校验后被拒、
结果净化（丢弃指令句/截断/**只作为 tool 消息回传、不进 system 提示词**）、轮次与调用上限降级、
记录五个要素与三种结果、服务器改名或删除时不写记录。

`apitest` 新增 6 个用例（登记、连接、断开、删除、记录、端到端流程）。端到端用例实跑：
**连接 `http://mcp-demo:8090` → 发现 `formula_hint`/`unit_convert` → 保存 `formula_hint@campus-tools` 技能 → 会话提问 →
流中出现 `tool_call` 事件且状态成功、`done.tools` 带工具 → 记录接口查到同一条 success（工具名、服务器名、会话一致）**。

### 界面（真实浏览器，`.local/tool-ui-check.mjs`，6/6 通过）

| 检查 | 结果 |
| --- | --- |
| 班级设置里登记并连接 | 显示「campus-tools 已连接 · 2 个工具 · 已配置令牌」与 `formula_hint、unit_convert` |
| 技能工具项保存 | 保存后回显 `formula_hint@campus-tools` |
| 会话中的工具步骤 | 气泡内出现 `formula_hint@campus-tools 成功`，回答非空 |
| 调用记录 | 表格出现「formula_hint@campus-tools 成功 1 工具界面教师」，可按结果筛选 |
| 断开后保存引用 | 就地报错（提示尚未连接），改动被拒绝 |
| 页面脚本错误 | 0 |

截图：`.local/screenshots/tool-servers.png`、`tool-skill-tools.png`、`tool-steps.png`、`tool-calls.png`、`tool-disconnect-reject.png`。

### 验收自查项

- **服务单独可验**：见上一节 17/17。
- **端到端可验**：见 apitest 端到端用例；记录含 `formula_hint@campus-tools`、服务器名与轮次。
- **断开后不可再调用**：断开后保存引用被拒（界面就地报错 + 接口 400「尚未连接」）；候选集为空时不再声明工具。
- **上限生效**：`CHAT_MAX_TOOL_CALLS=0` 时用例断言不声明工具、不产生调用，仍用已有信息作答并给出兜底摘要。
- **令牌不外泄**：宿主只回 `has_token`（用例断言无 `auth_token`/`token_cipher`）；镜像内检索令牌 0 命中；令牌不进前端。

### 同伴交叉验证（可复现步骤，证据归本人验证记录）

1. **服务与工具的隔离**：同伴用自己的账号登录 → 工具服务列表为空；用他人服务器标识请求连接/改名/删除均返回 404，响应与不存在的标识一致。
2. **令牌不回传**：`GET /api/tool-servers` 整份响应检索不到令牌字符串，只有 `has_token: true`。
3. **断开即失效**：断开后保存引用其工具的技能 → 400「尚未连接」；已引用该工具的会话提问时 `done.tools` 中状态为 `rejected`，记录多一条 `rejected`。
4. **记录只看自己范围**：同伴打开调用记录，只看到自己范围内的记录。

尚未完成，不计入通过：无。
