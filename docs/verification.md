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
