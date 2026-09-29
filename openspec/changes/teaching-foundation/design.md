## Context
空项目，部署于本机 Docker。首期提供完整真实数据流程，不接入 AI。

## Goals / Non-Goals
Goals: 安全登录、三角色授权、班级管理、讲义共享、响应式中文界面、容器持久化。
Non-Goals: 找回密码、邮箱验证、统一认证、多教师、入班审核、作业实现、Office 在线转换、AI 入库、讲义版本管理。

## Decisions
- React + TypeScript + Vite，Go 标准库 HTTP 路由，pgx 连接 PostgreSQL。
- 服务端随机会话，数据库保存 token 的 SHA-256，HttpOnly/SameSite cookie；8 小时过期。bcrypt 密码哈希；同源校验阻断跨站写操作；登录速率限制。
- 学生自注册，角色不可由注册请求指定；管理员通过初始环境变量创建，管理员只能创建教师账号；班级操作由所属教师执行。
- 唯一随机邀请码，不向学生返回；成员关系唯一，重复入班返回冲突。归档后禁止入班和内容变更，保留下载权限，教师可恢复班级。
- 上传上限 50 MiB，接受 .pdf/.doc/.docx/.ppt/.pptx；检查格式签名、Office ZIP 类型，随机存储名，元数据入库；文件放私有 Docker volume，通过授权 API 提供。
- 本期文件存储采用后端私有卷，不额外运行对象存储服务；后续可在文件访问层替换 S3。
- nginx 同源提供 SPA 并代理 /api；仅前端端口绑定 127.0.0.1。数据库和文件持久化，容器健康检查，环境变量管理初始凭证。

## Risks / Trade-offs
本机 HTTP 使用非 Secure cookie；未来 HTTPS 部署设置 COOKIE_SECURE=true。单实例内存登录节流适合本期，横向扩展需共享限流。格式检查不能替代病毒扫描，未来开放公网时增加扫描。Office 文档仅下载，PDF 使用浏览器原生预览。

## Migration Plan
后端启动执行幂等建表。首次启动从环境变量创建管理员（必须配置密码）。docker compose down 保留卷；备份数据库和文件卷需配套。

## Validation
Go 编译/vet/测试；TypeScript 编译；真实 PostgreSQL 的 HTTP 集成测试覆盖三角色、越权、会话、班级状态、文件上传下载与大小/类型限制；浏览器检查主要页面及交互。Docker 未安装时明确记录未验证容器运行，不宣称已经部署。
