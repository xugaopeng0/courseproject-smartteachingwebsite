# 知序 · 智能教学平台

平台实现登录、班级、讲义管理，以及限定班级范围、带原文出处的知识库检索。React + TypeScript / Go / MySQL 8.4 + Qdrant；嵌入由服务端调用课程网关（兼容 OpenAI 的服务端通道）。白色主界面、红色辅助视觉。遵循 OpenSpec 规约驱动开发。

## 本机 Docker 启动

前置条件：安装并启动 Docker Desktop，使用 Linux 容器。应用运行不依赖宿主机 Node、Go 或 MySQL。

在项目根目录的 PowerShell 中执行：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/setup.ps1
docker compose up --build -d
docker compose ps
```

Compose 启动 5 个服务：`db`（`mysql:8.4`，容器内 3306，仅内网）、`qdrant`（`qdrant/qdrant:v1.15.1`，6333，仅内网）、`api`、`worker`（与 api 同镜像 `zhixu-teaching-api`，`command: [worker]`，非 root、根文件系统只读、上传卷只读）和 `web`（nginx）。只有 `web` 发布宿主端口 `127.0.0.1:8080`；`db`、`qdrant`、`api`、`worker` 只接在 `internal: true` 的 `app` 网络上，没有宿主机端口（`web` 同时接入 `app` 与非 internal 的 `edge`，因为当前 Docker 不允许在 internal 网络上发布宿主端口）。

嵌入与对话都只由服务端调用课程网关，密钥不下发浏览器。请在项目根目录 `.env` 中填写 `GATEWAY_BASE_URL`、`GATEWAY_API_KEY`、`EMBEDDING_MODEL`，可选 `EMBEDDING_DIMENSIONS`（默认 1024）、`EMBEDDING_PROFILE_ID`、`GATEWAY_TIMEOUT`（默认 30s）与 `CHAT_MODEL`（问答使用；嵌入与对话是两个独立模块、两次调用）。网关未配置时索引任务会失败并记录 `temporary_failure`，语义检索与问答返回 503，但关键字检索、讲义浏览、上传和下载不受影响。

`SEED_ENABLED` 默认为 `true`：API 启动时会把宿主 `./seeds`（容器内 `/data/seeds`，只读）下的受支持格式种子材料导入内置示例班级（`SEED_CLASS_NAME`，默认“示例班级”，所有者是首个管理员账号），学生用邀请码加入。种子材料就是普通讲义，按文件名哈希推导稳定内部名、按内容哈希判断是否需要更新，因此重复启动幂等；worker 侧固定 `SEED_ENABLED=false`，不会重复导入。

打开 **http://localhost:8080**（用 `http://127.0.0.1:8080` 也可以：写请求的同源判定按「Origin 主机与请求 Host 一致」执行，不再要求与 `APP_ORIGIN` 字面相同）。初始管理员账号为 `admin`，密码见项目根目录 `.env` 中的 `ADMIN_PASSWORD`。初始化脚本生成随机密码，不覆盖已有 `.env`。反向代理或自定义域名下，把 `.env` 的 `APP_ORIGIN` 写成实际来源（支持逗号分隔多个）即可；注意管理员不能建班，班级只能由教师创建。

首次使用：

1. 管理员登录，在“教师管理”创建教师账号。
2. 退出，使用教师账号登录，创建班级。
3. 进入班级复制邀请码，并上传 PDF、Word、PPT、TXT 或 MD 讲义；新讲义会自动建立知识索引。
4. 学生从登录页自行注册，输入邀请码加入班级。
5. 学生查看和下载讲义，或在“知识库”中按问题检索原文并核对页码、幻灯片、段落或行号；PDF 可定位预览。

如果只想快速体验，可直接使用启动时自动导入的“示例班级”和其中的种子材料；它与手工创建的班级遵循同一套权限与检索规则。

首次引导管理员只会在账号不存在时创建。修改 `.env` 的管理员密码**不会重置已存在账号**，本期未实现密码重置功能。

```powershell
docker compose logs -f api worker # 查看 API 和索引任务日志
docker compose logs -f db qdrant  # 查看 MySQL 与向量库日志
docker compose ps                 # 5 个服务的健康状态
docker compose restart            # 重启，保留数据
docker compose down               # 停止，保留数据卷
```

业务数据、切片正文和向量分别存放在 `mysql-data`、`qdrant-data` 命名卷中，讲义文件存放在 `uploads` 卷中。迁移前的 PostgreSQL 数据保留在旧 `database` 卷，仅用于回滚，不要删除，也不要用它承载 MySQL 数据。不要使用 `docker compose down -v`，除非明确需要清空数据。仅网页端口绑定 `127.0.0.1:8080`，MySQL、Qdrant、API 和 worker 都没有宿主机公开端口。

构建时如果默认 Go 代理不可达，可配置 `GOPROXY=https://goproxy.cn,direct`。初始化脚本已为本地网络使用该设置。

## 本期能力

| 角色 | 功能 |
| --- | --- |
| 管理员 | 账号密码登录；创建、查看教师账号；维护平台默认提示词与平台技能；退出登录 |
| 教师 | 创建和编辑班级；复制邀请码；查看、移除学生；归档和恢复班级；上传、下载、预览、删除讲义；管理知识索引；班级内检索与溯源；设置本班提示词、启用／停用「解题引导」技能；在「解题助手」里连续提问并核对出处 |
| 学生 | 自助注册、登录；邀请码入班；查看成员；退出班级；下载讲义、预览 PDF；班级内检索与溯源；在「解题助手」里流式提问、逐字看回答并核对出处 |

上传支持 `.pdf`、`.doc`、`.docx`、`.ppt`、`.pptx`、`.txt`、`.md`（扩展名大小写不敏感），单文件最大 **50 MiB（界面简称 50 MB）**。后端检查扩展名、PDF/Office 文件签名及现代 Office ZIP 结构；TXT/MD 须为非空 **UTF-8** 文本（可带 BOM），拒绝无效编码、仅 BOM 和二进制控制字符，允许制表符、回车及换行。GBK/UTF-16 文件请另存为 UTF-8。PDF 使用浏览器原生预览；Office、TXT、MD 提供原文件下载，Markdown 不渲染为网页。班级归档后保留成员和讲义读取权限，禁止入班、编辑、上传、删除及教师移除成员；学生仍可主动退出。

知识库的切片正文、权限元数据与任务保存在 MySQL 8.4；向量保存在 Qdrant 集合 `campusclaw_chunks`（余弦度量，维度取 `EMBEDDING_DIMENSIONS`，默认 1024），向量点主键就是切片标识，正文一律回 MySQL 读取。嵌入与对话都由服务端调用课程网关，密钥不下发浏览器。MySQL 侧还为切片正文建立了 ngram 全文索引（`--ngram_token_size=2`，按二字切词）。上传成功后正文先按约 800 个 Unicode 字符的滑动窗口切片、相邻约 10% 重叠，也支持空行／换行／句号分隔符或 Markdown 标题分章；每个切片记录 1 起始序号与在提取文本中的字符区间。PDF 按页、PPT/PPTX 按幻灯片、TXT/MD 按行、DOC/DOCX 按提取正文段落定位；DOC/PPT 会先在隔离 worker 中转换。扫描件和图片文字本期不做 OCR。检索支持关键字、语义与混合三种模式，只返回原文片段和出处；问答则先检索、只有命中切片时才把材料标题、切片序号与正文交给对话模型，回答用 `[1]` 标注出处，没有依据时直接返回「资料中未找到相关内容」且不调用生成模型。删除讲义或撤销成员权限后，旧引用也无法继续读取。

「解题助手」把上面的问答做成会话：学生新建会话后连续提问，回答以 SSE 逐块下发、逐字显示，结束后给出可核对的出处列表；中途可以「停止生成」，已收到的部分按中断状态保存，不会记成完整回答。回答前使用的提示词与技能由教师按班级设置：平台提供默认提示词与内置「解题引导」技能（只给思路与追问，不直接给最终答案），班级可以覆盖或停用。接口层面除了浏览器 Cookie，还可以用 `POST /api/auth/token` 换取 Bearer 令牌调用（供接口工具使用），令牌只存哈希、与浏览器会话共用撤销路径。

本期不包含交叉编码器重排序、跨班级知识库、OCR、作业、找回密码、邮箱验证、学校统一认证、批量导入、多教师和讲义版本管理。

## 项目结构

```text
backend/        Go HTTP API、知识库 worker、数据库迁移、测试
frontend/       React 界面、nginx 代理、生产 Dockerfile
openspec/       需求规约、设计、实施与验收任务
.agents/skills/ OpenSpec 为 Codex 生成的技能
scripts/        初始化、集成测试、迁移与本地预览工具
seeds/          课程种子材料，只读挂载为 API 的 SEED_DIR
docs/           API、架构、知识库运维与验证记录
compose.yaml    本机容器编排
```

## SDD 工作流

OpenSpec CLI 以项目开发依赖锁定版本，并已初始化 Codex 工作流。首期变更在 `openspec/changes/teaching-foundation/`，包含先于代码建立的 proposal、design、四个能力规约和 tasks。

```powershell
npm ci
npx openspec list
npx openspec status --change teaching-foundation
npx openspec validate --all --strict
```

后续功能先创建变更，补齐需求及验收场景、设计与任务，再实施、验证、同步主规约和归档。首期已完成全部 12 项任务，包括真实 Docker 部署与持久化验收；当前变更保持未归档，等待后续规约同步和归档。

## 开发与验证

需要 Node 24 和 Go 1.26。当前机器额外下载的 Go 位于 `.tools/go`，它与 `.local` 都不应提交版本控制。

```powershell
npm ci
npm --prefix frontend ci
npm run test:db      # 无 Docker：自动启动便携 MySQL 与便携 Qdrant，跑后端全部 Go 测试
npm run build
npm run test:e2e     # 无 Docker：73 项真实 MySQL HTTP 检查
npm run test:knowledge   # 面向已启动的 Compose 部署，需要可用的课程网关，见下文
```

`test:db` 会自动下载便携 Qdrant（首次约 28 MB）到 `.tools/qdrant`，在 55434 与 16334 起两个独立实例，跑完即关闭；手动指向已有实例时设置 `MYSQL_TEST_DSN`、`QDRANT_TEST_URL` 即可（未设置时相关测试自动跳过），`MYSQL_TEST_DSN_NGRAM3` 用于验证 ngram 守卫（实例须以 `--ngram_token_size=3` 启动）。`test:e2e` 启动独立的真实 MySQL 和后端，跑 **73 项真实 MySQL HTTP 检查**（三角色权限、会话、班级、文件、进程重启持久化），使用端口 55433 / 8082。`test:knowledge` 经课程网关检查 20 条中文标注问题、无关问题、逐条出处、跨班级隔离与问答引用；它需要 `.env` 中有可用的网关配置，本次底座迁移后尚未在真实网关下执行。Linux 编译时二进制命名为 `.local/server`。

无 Docker 的 Windows 环境也可临时预览：

```powershell
npm run dev:local
```

首次运行会从 `https://dev.mysql.com/get/Downloads/MySQL-8.4/mysql-8.4.6-winx64.zip` 下载约 249 MB 的 MySQL 8.4 Windows ZIP，解压到 `.tools/mysql`（与 `.tools/go` 一样不提交），随后初始化临时数据目录并在 **55432** 端口启动 `mysqld`，同时拉起 `.local/server.exe` 与 Vite，实测约 1 分钟（下载直连官方 CDN，慢时可能更久）。镜像不可达或过慢时可用 `MYSQL_ZIP_URL` 指定其它地址（默认地址 `HEAD` 会返回 403，`GET` 正常，不必因此更换镜像）。由于 `mysqld` 在非 ASCII 路径下会静默退出，运行时会把二进制的 `bin` 与 `share` 复制到系统临时目录 `%TEMP%\zhixu-<项目路径哈希>\mysql` 作为 basedir；之后每次启动只需约 3 秒。

访问 **http://127.0.0.1:5173**。开发管理员账号为 `admin`，随机密码保存在 `.local/dev-credentials.json` 的 `admin` 字段中。这套开发数据和密码与 Docker 环境相互独立；开发 MySQL 在系统临时目录 `zhixu-<项目路径哈希>/development-db` 中持久保存，以绕过 Windows 中文路径问题。临时目录可能被系统清理，仅用于开发，被清理后重新初始化即可。讲义仍位于 `.local/uploads`。脚本同时会启动便携 Qdrant（16333）与知识库 worker，因此上传的讲义会被真正切片与索引；索引与语义检索、问答仍需要可用的嵌入与对话通道（见下文“接口文档”一节的替身做法），未配置时页面会明确提示。

## 接口文档与令牌

接口契约是 `docs/openapi.json`（OpenAPI 3.1），随二进制提供：

```powershell
# 浏览器直接打开文档页面（页面与资源都自托管，不依赖外网 CDN）
http://localhost:8080/api/docs          # Compose 部署
http://127.0.0.1:8081/api/docs          # 本地 dev:local

# 取一个 Bearer 令牌后，在页面右上角 Authorize 填入即可试调受保护接口
curl -s -X POST http://localhost:8080/api/auth/token `
  -H "Content-Type: application/json" `
  -d '{"username":"admin","password":"<ADMIN_PASSWORD>"}'
```

编辑契约后运行 `node scripts/sync-openapi.mjs` 同步到 `backend/openapi.json` 与 `backend/swaggerui/`；`go test` 会校验副本未过期，并双向比对契约与路由表，避免文档漂移。

浏览器回归测试：运行本地预览后执行 `node scripts/browser-test.mjs`。使用本机 Chrome 的无头模式，截图存入 `.local/screenshots`。

接口测试套件：`npm run test:api`。一个接口一个脚本（`apitest/cases/api/login/POST.py` ↔ `POST /api/login`），一键跑完并生成自包含 HTML 报告 `apitest/report/index.html`（不提交）。不只对状态码：登录后核对 `sessions` 行、检索后核对切片属于本班、提示词写入后核对 `prompts` 行、发消息后核对 `chat_messages` 的顺序号与状态。详见 `apitest/README.md`。

Docker 部署验收：`node scripts/deployment-test.mjs`。脚本读取本地 `.env`，在实际平台上创建临时教师、学生、班级及讲义，执行 `docker compose down` 和重新启动，验证数据保留与浏览器登录，最后清理本次创建的临时数据。**运行时会短暂停止本项目容器**，应在平台空闲时执行。

该脚本还验证浏览器 TXT/MD 上传和下载。只检查已部署功能、不重建容器时使用 `node scripts/deployment-test.mjs --skip-recreate`。

## 工具服务（MCP，第 6 课）

`mcp-server/` 是一个独立的 MCP 工具服务（Go 标准库，无第三方依赖），与主应用同一套 Compose 构建启动：

```powershell
docker compose up -d --build mcp-demo   # 只构建/启动工具服务
```

- 服务名 `mcp-demo`，容器内监听 `8090`，**只 expose 不发布宿主端口**；`api` 与 `worker` 经 `http://mcp-demo:8090` 访问。
- 协议：JSON-RPC 2.0 单入口 `POST /mcp`，方法 `initialize` / `tools/list` / `tools/call`；另有无需令牌的 `GET /health`。
- 令牌从环境变量 `MCP_AUTH_TOKEN` 读取（`.env` 注入），不写进代码、不进镜像、不下发前端；缺失或错误一律拒绝。
- 暴露两个工具：`formula_hint`（给公式、符号与思路，不给最终数值答案）、`unit_convert`（同类量纯单位换算）。

宿主侧接入在「班级设置 → 工具服务」里完成：教师登记名称与 Base URL（例如 `http://mcp-demo:8090`）→ 连接（握手 + 工具发现）→
在同一页把所有者的技能绑定工具（`tool@server`，例如 `formula_hint@campus-tools`）。连接成功后能看到发现的工具名；
会话回答里会先出现工具调用步骤，再给出正文；下方「调用记录」列出每次调用的工具、结果、第几轮与提问者。
令牌只在服务端，界面只显示「已配置令牌」。

单独验证（容器里没有 curl，`wget` 又不支持自定义请求头，因此用 compose 内网里的 Node 发请求）：

```powershell
$token = (Select-String -Path .env -Pattern '^MCP_AUTH_TOKEN=(.*)$').Matches[0].Groups[1].Value.Trim()
docker run --rm --network zhixu-teaching_app -e TOKEN=$token -e MCP_URL=http://mcp-demo:8090 `
  -v "$($PWD)/.local:/check:ro" node:24-alpine node /check/mcp-check.mjs
```

该脚本逐条断言健康检查、能力协商、工具发现、两个工具的执行、四类协议错误码与令牌边界（当前 17/17 通过）。

### 本机代理环境下的构建

本次部署遇到 Docker Hub 认证超时及构建容器 npm 下载缓慢，已通过现有代理完成构建。若重建时遇到相同问题，可在当前 PowerShell 中设置（端口需与自己的系统代理一致；本机为 7897）：

```powershell
$env:HTTP_PROXY = 'http://127.0.0.1:7897'
$env:HTTPS_PROXY = 'http://127.0.0.1:7897'
$env:NO_PROXY = 'localhost,127.0.0.1'
docker compose build --build-arg HTTP_PROXY=http://http.docker.internal:3128 --build-arg HTTPS_PROXY=http://http.docker.internal:3128
docker compose up -d --wait
```

此构建代理地址适用于本次 Docker Desktop 环境，来自 `docker info`；其他机器应使用其可用的代理地址。代理只用于当前构建过程，应用运行时不依赖这些代理配置。

## 后续扩展

参见 `docs/architecture.md`。班级和用户是后续作业权限的基础，讲义记录是知识库文档映射的入口；课程网关已提供服务端的密钥边界、嵌入通道与对话通道（`GATEWAY_*` 与 `CHAT_MODEL`，两者是独立模块）。三种检索模式与「先检索后生成」的问答引用已由 OpenSpec 变更 `knowledge-retrieval-modes` 与 `knowledge-answer-citations` 交付；尚未实现的能力不在页面中伪装为可用入口。
