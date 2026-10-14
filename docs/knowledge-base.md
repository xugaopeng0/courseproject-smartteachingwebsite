# 班级知识库运维说明

## 组件与固定版本

| 组件 | 固定版本 / 配置 | 用途 | 许可证参考 |
| --- | --- | --- | --- |
| MySQL | `mysql:8.4`（实测 8.4.11），`utf8mb4` + `--ngram_token_size=2` | 账号、班级、讲义元数据、索引任务、切片正文与中文全文索引 | [GPLv2](https://www.mysql.com/about/legal/licensing/) |
| Qdrant | `qdrant/qdrant:v1.15.1` | 切片向量；集合 `campusclaw_chunks`，余弦度量，维度取 `EMBEDDING_DIMENSIONS` | [Apache 2.0](https://github.com/qdrant/qdrant/blob/v1.15.1/LICENSE) |
| 课程网关 | 部署方提供的兼容 OpenAI 服务；`EMBEDDING_MODEL`、`EMBEDDING_DIMENSIONS`（默认 1024）、`GATEWAY_TIMEOUT`（默认 30s） | 仅由服务端调用的嵌入接口（后续对话复用同一通道） | 由课程提供方决定 |
| LibreOffice | Alpine 3.23 仓库固定快照中的 writer / impress | 在 worker 临时目录中将 DOC/PPT 转为现代格式 | [MPL 2.0](https://www.libreoffice.org/about-us/licenses/) |
| Poppler | Alpine 3.23 仓库固定快照中的 poppler-utils | PDF 页数和逐页文本提取 | [项目说明](https://poppler.freedesktop.org/) |

MySQL 的启动参数在 Compose 中固定为 `--character-set-server=utf8mb4 --collation-server=utf8mb4_0900_ai_ci --ngram_token_size=2 --default-time-zone=+00:00`。应用启动时会校验 `ngram_token_size=2` 与 `character_set_server=utf8mb4`，不满足就拒绝写入索引；`ngram_token_size` 是只读启动参数，改动后必须重建全文索引。嵌入不再依赖本地模型服务：没有网关配置时索引不会完成（见下文故障处理）。

## 网络、端口与数据卷

`db`（3306）、`qdrant`（6333）、`api` 与 `worker` 只接入 `internal: true` 的 `app` 网络，都没有宿主机端口；只有 `web`（nginx）发布 `127.0.0.1:8080`，并同时接入 `app` 与非 internal 的 `edge` 网络——当前 Docker 不允许在 internal 网络上发布宿主端口。

数据卷：`mysql-data`（MySQL 数据目录）、`qdrant-data`（Qdrant 存储）、`uploads`（讲义文件，api 可写、worker 只读）、迁移前的 PostgreSQL `database` 卷（仅用于回滚，不要删除，也不要用它承载 MySQL 数据）。

## 存储分工与中文检索

- 切片正文保存在 MySQL 的 `knowledge_chunks.content`，并建立 `FULLTEXT KEY knowledge_chunks_content_ft (content) WITH PARSER ngram`；中文按二字 token 切词。
- 向量保存在 Qdrant 集合 `campusclaw_chunks`：余弦度量，维度取 `EMBEDDING_DIMENSIONS`（默认 1024）。向量点主键就是 `knowledge_chunks.id`（带连字符的 UUID 文本），payload 只存 `class_id` / `handout_id` / `generation_id` / `ordinal`，不存正文。
- 集合缺失时按配置的维度与余弦自动创建；集合已存在但维度或度量不匹配时启动即报错，不覆盖已有集合；写入前逐条校验向量维度。
- 检索先用 `class_id` 过滤向量库，再用切片主键回 MySQL 取正文，并校验该切片所属代次仍是讲义的已发布代次；代次失效、讲义已删除或不属于本班的命中直接丢弃。切片正文与授权始终以 MySQL 为权威，不用向量库的 payload 做判断。
- 删除讲义或重建代次时会清理对应的向量点；清理失败只记日志，残留点仍会因代次校验而无法被检索到。

## 切片策略与默认参数

| 策略 | 取值 | 说明 |
| --- | --- | --- |
| 默认窗口 `window` | `size` 800、`overlap` 80 | 约 800 个 Unicode 字符的滑动窗口，相邻切片保留约 10% 重叠；窗口内优先在换行、空行或句号处断开 |
| 自定义分隔符 `separator` | `blank` / `newline` / `period` | 分别按空行、换行、句号分段；超出窗口长度的段落继续按默认窗口切分 |
| Markdown 标题分章 `heading` | — | 以标题行为边界分章；超出窗口长度的章节在章节内回落默认窗口；文档没有标题时整篇回落默认窗口，不建立空索引 |

索引接口 `POST /api/classes/{id}/knowledge/documents/{handoutID}/index` 的可选请求体为 `{"strategy":"window|separator|heading","separator":"blank|newline|period","size":800,"overlap":80}`：`size` 允许 100–4000，`overlap` 不得超过窗口的一半，省略 `overlap` 时取窗口的 10%；不带请求体时使用默认窗口。当前界面不传请求体，即一律使用默认窗口策略。

每个切片记录讲义内 1 起始序号 `ordinal`、在提取文本中的字符区间 `char_start` / `char_end`（0 起始半开区间），并保留解析阶段的位置映射（PDF 页码、幻灯片号、原文件行号或提取正文段号；跨位置时给出范围）。策略、窗口与重叠随代次记录在 `knowledge_generations.chunk_strategy` / `chunk_size` / `chunk_overlap`，因此同一内容与同一策略重复索引可以得到一致的切片边界。种子材料固定使用默认窗口策略。

## 种子材料导入

`SEED_ENABLED=true` 时（Compose 对 api 默认打开，worker 固定为 `false`），API 启动会把 `SEED_DIR`（Compose 中为 `/data/seeds`，绑定宿主 `./seeds` 只读）下的种子材料导入内置示例班级：

- 跳过目录、点号开头的文件与 `README` 开头的说明文件；其余按扩展名支持的格式导入，与手工上传的格式集合一致。
- 班级名取 `SEED_CLASS_NAME`（默认“示例班级”），所有者是首个管理员账号，学生用邀请码加入；示例班级已存在时直接复用。
- 内部存储名按文件名哈希推导（`seed-<哈希><扩展名>`），是否需要更新按内容哈希判断：内容未变不重复导入，内容变化则更新标题、大小与类型并重新排队索引。
- 种子材料就是普通讲义：受同样的班级授权、检索与删除一致性约束，没有专用读取通道。重复启动不会产生重复文件或重复任务。

## 索引状态与处理范围

新上传讲义在上传数据库事务内写入持久化任务，但上传请求不等待解析或模型。worker 使用租约领取任务，状态依次为排队、解析、向量化和可检索。任务最多尝试三次，重试按 5 秒起的指数退避；完整代次写入后才切换活动索引，重建失败时旧索引继续可用。租约 10 分钟，服务重启后过期租约可以重新领取。

同一份讲义同时只允许一个未完成任务（`queued` / `running` / `paused`）：索引接口在同一事务内用行锁协调，已有活动任务时不会重复排队。班级归档期间不会领取新任务，进行中的任务会暂停为 `paused`，恢复班级后可再次领取。文件内容与代次记录不一致时拒绝发布，避免把旧正文挂到新索引上。

切换 `EMBEDDING_PROFILE_ID`（或模型、维度、预处理方式变化）时，worker 会先为既有讲义建立新代次，全部成功后才原子切换活动配置；任一文档失败时保留旧配置与旧索引。同一 `EMBEDDING_PROFILE_ID` 必须始终对应同一模型与维度，否则启动时会报配置不一致。

解析上限：单文件 50 MiB、展开内容 200 MiB、最多 10000 个切片、单 worker 串行处理、每文件最多 120 秒、单次网关请求按 `GATEWAY_TIMEOUT`（默认 30 秒）。worker 以非 root 用户运行，根文件系统只读，上传卷只读，临时文件放在 512 MiB 的 tmpfs；LibreOffice 禁用宏交互，也不向转换进程传入网关凭证。

支持范围：

- TXT/MD：非空 UTF-8，保留 1 起始原文件行号。
- PDF：按物理页提取文字；纯扫描件显示“无可提取文本，本期不支持 OCR”。
- DOCX：正文和表格中的段落，显示“提取正文第 N 段”，不虚构页码。
- PPTX：按幻灯片提取文字。
- DOC/PPT：先经 LibreOffice 转换，再使用对应现代格式的定位，并标明“经格式转换提取”。

图片、批注、演讲者备注、嵌入附件和复杂图表不保证覆盖。来源文本在 React 中作为普通文本显示，不渲染文档 HTML 或脚本。

## 提示词、技能与解题助手

平台在启动时幂等写入一份默认提示词与内置「解题引导」技能（`prompts` 表：`scope=platform`、`kind=prompt|skill`）；班级可以覆盖提示词，也可以按名覆盖或停用某个技能，生效顺序与接口见 `docs/api.md`。约定：平台级内容只有管理员能改，班级级只有所属教师能改，学生只读，归档班级只读。会话与消息存在 `chat_sessions` / `chat_messages`：用户消息在流开始前落库，助手消息按 `complete` / `interrupted` 记录状态。

「解题助手」（班级详情页的独立标签）复用第 4 课的检索、材料组装与出处生成：每次提问先做本班混合检索取前 4 条，命中才调用对话模型，回答以 SSE 逐块返回，结束前给出出处列表；没有命中时只返回「资料中未找到相关内容」且不调用对话模型；客户端中途断开时已收增量按 `interrupted` 保存。内置「解题引导」技能要求模型只给思路与追问、不直接给最终答案——它是提示词层面的约束，服务端的硬边界仍是「无命中不生成」与「出处由服务端生成」。

无 Docker 时也能验证整条链路：`npm run dev:local` 会同时启动便携 MySQL、便携 Qdrant、API、worker 与 Vite；把嵌入与对话指向仓库自带的开发替身即可（替身支持 `/v1` 前缀与 SSE 流式）：

```powershell
cd backend; go build -o ..\.local\gateway-stub\stub.exe .\cmd\gateway-stub; cd ..
$env:PORT = "11500"; $env:STUB_CHUNK_DELAY_MS = "300"; .local\gateway-stub\stub.exe   # 另开终端
$env:EMBEDDING_BASE_URL = "http://127.0.0.1:11500/v1"; $env:EMBEDDING_MODEL = "stub-lexical-1024"
$env:GATEWAY_BASE_URL  = "http://127.0.0.1:11500/v1"; $env:CHAT_MODEL = "stub-chat"
npm run dev:local
```

替身是确定性开发工具，不是语义模型：可以验证权限、状态、流式协议、引用顺序与前端流程，不能作为召回质量证据。

## 配置与故障

`.env` 可设置：

```dotenv
DB_PASSWORD=
DB_ROOT_PASSWORD=
GATEWAY_BASE_URL=
GATEWAY_API_KEY=
EMBEDDING_MODEL=
EMBEDDING_DIMENSIONS=1024
EMBEDDING_PROFILE_ID=gateway-embedding-v1
CHAT_MODEL=
GATEWAY_TIMEOUT=30s
QDRANT_COLLECTION=campusclaw_chunks
SEED_ENABLED=true
SEED_CLASS_NAME=示例班级
```

`DATABASE_URL`（`teaching:<密码>@tcp(db:3306)/teaching?parseTime=true&loc=UTC&charset=utf8mb4`）、`UPLOAD_DIR`、`KNOWLEDGE_ENABLED`、`QDRANT_URL`、`SEED_DIR` 由 Compose 固定，其余由 `.env` 提供。`scripts/setup.ps1` 只生成数据库与管理员随机凭证，网关变量需要自行填写。

- 课程网关未配置（缺少 `GATEWAY_BASE_URL` 或 `EMBEDDING_MODEL`）：索引任务以 `temporary_failure` 失败并按退避重试，检索返回 503；讲义浏览、上传和下载不受影响。
- 网关返回非 200、结果条数不符、维度错误、含非有限值或零向量时，该批次不写入向量库，任务按可重试故障处理；永久性错误（无文本、不支持、加密等）直接标记为失败或不支持，不再重试。
- 若曾以空网关配置启动过，数据库里已注册的 profile 会记住空模型名；之后补上网关配置并沿用同一个 `EMBEDDING_PROFILE_ID` 会因配置不一致而启动失败，此时应改用新的 profile id。
- MySQL 实例 `ngram_token_size` 不为 2、或服务端字符集不是 utf8mb4 时，应用（`KNOWLEDGE_ENABLED=true`）启动即拒绝写入索引；需改回启动参数并重建全文索引。
- Qdrant 集合维度与 `EMBEDDING_DIMENSIONS` 不一致时启动报错，不覆盖已有集合；更换嵌入模型或维度必须同时启用新的 `EMBEDDING_PROFILE_ID`。

查看状态：

```powershell
docker compose ps
docker compose logs -f api worker db qdrant
$dbPassword = (Get-Content .env | Where-Object { $_ -like 'DB_PASSWORD=*' })[0].Split('=')[1]
docker compose exec -T -e MYSQL_PWD=$dbPassword db mysql -h 127.0.0.1 -u teaching teaching --default-character-set=utf8mb4 `
  -e "SELECT status,count(*) FROM knowledge_jobs GROUP BY status; SELECT status,count(*) FROM knowledge_documents GROUP BY status;"
```

班级“知识库”页会直接显示每份讲义的索引状态与错误码，`GET /api/classes/{id}/knowledge/documents` 返回同一份数据。

## 升级前备份与恢复演练

MySQL 与上传卷必须按同一时间点配对备份；Qdrant 中的向量可以从切片正文重建，但重建需要可用网关。以下命令不会删除现有卷：

```powershell
New-Item -ItemType Directory -Force .local/backup | Out-Null
$dbPassword = (Get-Content .env | Where-Object { $_ -like 'DB_PASSWORD=*' })[0].Split('=')[1]
docker compose exec -T -e MYSQL_PWD=$dbPassword db mysqldump -h 127.0.0.1 -u teaching --single-transaction --default-character-set=utf8mb4 teaching > .local/backup/teaching.sql
docker run --rm -v zhixu-teaching_uploads:/source:ro -v "${PWD}/.local/backup:/backup" alpine:3.23 tar -C /source -czf /backup/uploads.tgz .
```

恢复演练应使用单独的临时 MySQL、临时 Qdrant 与临时卷，确认导入行数、讲义 SHA-256、索引重建和下载均可用后再升级。只恢复向量时，可先清空或新建集合，再由教师在班级“知识库”页对讲义点击“重建”（`POST .../index` 不带请求体即可），或换用新的 `EMBEDDING_PROFILE_ID` 触发全量代次重建。正常回滚时先停止 worker 和新界面，部署旧 API/前端并保留新增表与旧 `database` 卷；不得运行 `docker compose down -v`，不得用空卷冒充迁移成功。

PostgreSQL → MySQL 的一次性迁移脚本为 `node scripts/migrate-postgres-to-mysql.mjs`：

- 默认把数据卷复制为 `<卷名>-drill` 副本并在副本上导出（原卷只读），加 `--apply` 才写入 MySQL，`--keep-drill` 保留副本，`--volume <名称>` 指定其它源卷。
- 只迁移 users / classrooms / memberships / handouts / sessions；知识库切片在迁移后由 worker 重新建立索引。
- 目标库已有同名账号（例如 admin 在目标库已有不同主键）时，按 username 建立 ID 映射后改写外键。
- 写入使用 `INSERT ... ON DUPLICATE KEY UPDATE`，重复执行不产生重复行；迁移前后对上传卷每个文件做 SHA-256 比对。

## 验收

```powershell
npm run test:e2e         # 无 Docker：55433 上的独立 MySQL，73 项真实 HTTP 检查
npm run test:db          # 无 Docker：便携 MySQL（55434）+ 便携 Qdrant（16334），后端全部 Go 测试
npm run test:knowledge   # 已启动的 Compose 部署：经课程网关的中文检索与问答验收
node scripts/browser-test.mjs
node scripts/deployment-test.mjs
```

`test:db` 会自动下载并解压便携 Qdrant 到 `.tools/qdrant`（首次约 28 MB），跑完即关闭两个实例；它覆盖方言、行锁、生成列、发布与回滚、ngram 全文命中、跨存储一致性等需要真实数据库的用例，并可直接设置 `MYSQL_TEST_DSN_NGRAM3` 之外的端口。

数据库层测试也可以手动指向已有实例（未设置时自动跳过）：

```powershell
$env:MYSQL_TEST_DSN = "root:密码@tcp(127.0.0.1:13306)/teaching?parseTime=true&loc=UTC&charset=utf8mb4"
$env:QDRANT_TEST_URL = "http://127.0.0.1:16333"
$env:MYSQL_TEST_DSN_NGRAM3 = "root:密码@tcp(127.0.0.1:13307)/teaching?parseTime=true&loc=UTC&charset=utf8mb4"  # 可选：验证 ngram 守卫
cd backend; go test ./...
```

七种格式的解析与定位验收需要 poppler 与 LibreOffice，因此在后端镜像内执行（样例目录见 `.local/format-check`）：

```powershell
cd backend
$env:CGO_ENABLED = "0"; $env:GOOS = "linux"; $env:GOARCH = "amd64"
go test -c -o ..\.local\format-check\backend.test .
docker run --rm --entrypoint sh -e HOME=/tmp -e FORMAT_CHECK_DIR=/samples `
  -v "${PWD}\..\.local\format-check:/samples:ro" zhixu-teaching-api:latest `
  -c "/samples/backend.test -test.run TestExtractionInContainer -test.v"
```

`test:knowledge` 为运行中的 Compose 部署创建临时班级和讲义，检查 20 条中文标注问题至少 18 条在前 5 条命中正确来源、5 条无关问题不返回猜测内容、跨班级泄露为 0，并逐条读取引用验证定位；结果写入忽略提交的 `.local/knowledge-acceptance.json`，临时讲义、班级和账号随后清理。附加 `KNOWLEDGE_MODE_BASELINE=1` 会额外把同一批问题在 keyword／vector／hybrid 三种模式下各测一遍，记录命中数与中位耗时。

真实模型下的实测结果（嵌入 `qwen3-embedding:0.6b`、对话 `deepseek-flash`）：hybrid 20/20、keyword 20/20、vector 17/20，中位耗时 61／18／60 ms，详见 `docs/verification.md`。

嵌入与对话可以指向不同的 OpenAI 兼容服务：`EMBEDDING_BASE_URL`／`EMBEDDING_API_KEY` 与 `CHAT_BASE_URL`／`CHAT_API_KEY` 未填写时回退到 `GATEWAY_BASE_URL`／`GATEWAY_API_KEY`。本机没有课程网关时，可用本地 Ollama 提供真实嵌入模型、用云端服务提供对话：

```powershell
node scripts/ollama-runtime.mjs start     # 便携包 + qwen3-embedding:0.6b（首次约 1.4 GB + 640 MB）
$env:EMBEDDING_BASE_URL = "http://host.docker.internal:11434/v1"
$env:EMBEDDING_MODEL = "qwen3-embedding:0.6b"
docker compose -f compose.yaml -f .local/compose-localmodel.yaml up -d api worker
npm run test:knowledge
```

`.local/compose-localmodel.yaml` 只存在于本机（已忽略提交）：它把 api 与 worker 额外接入 `edge` 网络并声明 `host.docker.internal`，仓库中的 Compose 定义仍保持 api 与 worker 仅接 internal 网络。用完执行 `docker compose up -d api worker` 即可恢复。

完全没有真实模型时，还可以用确定性替身先把链路跑通（**仅用于功能验证，不能作为召回质量证据**）：

```powershell
cd backend
$env:CGO_ENABLED = "0"; $env:GOOS = "linux"; $env:GOARCH = "amd64"
go build -o ..\.local\gateway-stub\stub .\cmd\gateway-stub
docker run -d --name gateway-stub --network zhixu-teaching_app `
  -v "${PWD}\..\.local\gateway-stub:/stub:ro" alpine:3.23 /stub/stub
# 用只放在 .local/（已忽略提交）的覆盖文件把 api/worker 指向替身
docker compose -f compose.yaml -f ..\.local\compose-stub.yaml up -d api worker
$env:GATEWAY_BASE_URL = "http://gateway-stub:8080"; $env:EMBEDDING_MODEL = "stub-lexical-1024"
$env:KNOWLEDGE_STUB = "1"; $env:KNOWLEDGE_MIN_HITS = "14"
node ..\scripts\knowledge-test.mjs
```

验证结束后用 `docker compose up -d api worker` 恢复到常规配置，并删除 `gateway-stub` 容器。
