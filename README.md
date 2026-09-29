# 知序 · 智能教学平台

首期实现登录、班级和讲义管理。React + TypeScript / Go / PostgreSQL；白色主界面、红色辅助视觉。遵循 OpenSpec 规约驱动开发。

## 本机 Docker 启动

前置条件：安装并启动 Docker Desktop，使用 Linux 容器。应用运行不依赖宿主机 Node、Go 或 PostgreSQL。

在项目根目录的 PowerShell 中执行：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/setup.ps1
docker compose up --build -d
docker compose ps
```

打开 **http://localhost:8080**。初始管理员账号为 `admin`，密码见项目根目录 `.env` 中的 `ADMIN_PASSWORD`。初始化脚本生成随机密码，不覆盖已有 `.env`。请用 `localhost` 访问；如改为其他域名或端口，需同步更新 `.env` 的 `APP_ORIGIN` 和 Compose 端口映射。

首次使用：

1. 管理员登录，在“教师管理”创建教师账号。
2. 退出，使用教师账号登录，创建班级。
3. 进入班级复制邀请码，并上传 PDF、Word 或 PPT 讲义。
4. 学生从登录页自行注册，输入邀请码加入班级。
5. 学生查看和下载讲义；PDF 可在新标签页预览。

首次引导管理员只会在账号不存在时创建。修改 `.env` 的管理员密码**不会重置已存在账号**，本期未实现密码重置功能。

```powershell
docker compose logs -f api       # 查看后端日志
docker compose restart          # 重启，保留数据
docker compose down             # 停止，保留数据卷
```

数据库和讲义分别存放在 `database`、`uploads` 命名卷中。不要使用 `docker compose down -v`，除非明确需要清空数据。仅网页端口绑定 `127.0.0.1:8080`，数据库、后端没有宿主机公开端口。

构建时如果默认 Go 代理不可达，可配置 `GOPROXY=https://goproxy.cn,direct`。初始化脚本已为本地网络使用该设置。

## 本期能力

| 角色 | 功能 |
| --- | --- |
| 管理员 | 账号密码登录；创建、查看教师账号；退出登录 |
| 教师 | 创建和编辑班级；复制邀请码；查看、移除学生；归档和恢复班级；上传、下载、预览、删除讲义 |
| 学生 | 自助注册、登录；邀请码入班；查看成员；退出班级；下载讲义、预览 PDF |

上传支持 `.pdf`、`.doc`、`.docx`、`.ppt`、`.pptx`、`.txt`、`.md`（扩展名大小写不敏感），单文件最大 **50 MiB（界面简称 50 MB）**。后端检查扩展名、PDF/Office 文件签名及现代 Office ZIP 结构；TXT/MD 须为非空 **UTF-8** 文本（可带 BOM），拒绝无效编码、仅 BOM 和二进制控制字符，允许制表符、回车及换行。GBK/UTF-16 文件请另存为 UTF-8。PDF 使用浏览器原生预览；Office、TXT、MD 提供原文件下载，Markdown 不渲染为网页。班级归档后保留成员和讲义读取权限，禁止入班、编辑、上传、删除及教师移除成员；学生仍可主动退出。

本期不包含 AI 调用、讲义解析入库、作业、找回密码、邮箱验证、学校统一认证、批量导入、多教师和讲义版本管理。

## 项目结构

```text
backend/        Go HTTP API、PostgreSQL schema、测试
frontend/       React 界面、nginx 代理、生产 Dockerfile
openspec/       需求规约、设计、实施与验收任务
.agents/skills/ OpenSpec 为 Codex 生成的技能
scripts/        初始化、集成测试和本地预览工具
docs/           API、架构与验证记录
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
cd backend
go test ./...
go vet ./...
go build -o ../.local/server.exe .
cd ..
npm run build
npm run test:e2e
```

`test:e2e` 启动独立的真实 PostgreSQL 和后端，测试三角色权限、会话、班级、文件和进程重启持久化；使用端口 55433 / 8082。Linux 编译时二进制命名为 `.local/server`。测试用 PostgreSQL 原生依赖由 npm 安装。

无 Docker 的 Windows 环境也可临时预览：

```powershell
node scripts/local-dev.mjs
```

访问 **http://127.0.0.1:5173**。开发管理员账号为 `admin`，随机密码保存在 `.local/dev-credentials.json` 的 `admin` 字段中。这套开发数据和密码与 Docker 环境相互独立；开发 PostgreSQL 在系统临时目录 `zhixu-<项目路径哈希>/development-db` 中持久保存，以绕过 Windows 中文路径问题。临时目录可能被系统清理，仅用于开发。讲义仍位于 `.local/uploads`。

浏览器回归测试：运行本地预览后执行 `node scripts/browser-test.mjs`。使用本机 Chrome 的无头模式，截图存入 `.local/screenshots`。

Docker 部署验收：`node scripts/deployment-test.mjs`。脚本读取本地 `.env`，在实际平台上创建临时教师、学生、班级及讲义，执行 `docker compose down` 和重新启动，验证数据保留与浏览器登录，最后清理本次创建的临时数据。**运行时会短暂停止本项目容器**，应在平台空闲时执行。

该脚本还验证浏览器 TXT/MD 上传和下载。只检查已部署功能、不重建容器时使用 `node scripts/deployment-test.mjs --skip-recreate`。

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

参见 `docs/architecture.md`。班级和用户是后续作业权限的基础，讲义记录是知识库文档映射的入口；大模型、智能体、技能与 MCP 以独立服务接口接入，当前页面不展示未实现的交互入口。
