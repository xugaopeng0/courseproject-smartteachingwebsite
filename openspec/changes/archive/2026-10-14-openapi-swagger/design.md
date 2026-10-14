# Design

## Context

- 路由现在是 `main.go` 中逐条 `a.route(mux, "METHOD /api/...", auth, handler)` 调用，没有可枚举的表；`route` 内部统一处理 `Cache-Control`、同源校验与错误包装。
- 已有 22 个 `/api/*` 操作（健康检查、注册/登录/登出/当前用户、教师管理、班级与成员、讲义上传下载删除、知识库文档/索引/检索/问答/切片来源）。
- `docs/api.md` 是散文说明，`docs/openapi.json` 尚不存在。
- `web` 是唯一发布端口的服务，nginx 已把 `/api/` 反代到 `api:8081`；`api` 不发布宿主端口。
- 本机网络无法访问常见 CDN（本次会话中 HuggingFace、ollama.com 均不可达），因此文档页面必须自托管；npm 包源可用（`swagger-ui-dist` 5.33.2 可安装）。

## Goals / Non-Goals

**Goals:**

- 契约成为唯一事实来源，且无法悄悄漂移。
- 文档页面在离线环境可用，并能用 Bearer 令牌试调。
- 不改动任何接口的对外行为。

**Non-Goals:**

- 不引入注解生成器（swaggo 等）或独立文档服务。
- 不追求把契约生成客户端 SDK。
- 不替换 `docs/api.md`（保留为人读说明并补充指向契约的入口）。

## Decisions

### 1. 手写 OpenAPI 3.1 文件作为契约，随二进制嵌入

`docs/openapi.json` 由人工维护，用 `go:embed` 打包进 `api` 二进制，`GET /api/openapi.json` 直接返回。选 3.1 而不是 3.0：与 JSON Schema 对齐、`examples` 与 `nullable` 语义更清晰；Swagger UI 5.x 两者都支持。备选「用注解生成」被否：本仓库路由是手写表，引入生成器会带来注释负担和构建期依赖，且生成物难以评审。

### 2. 路由改为可枚举的表

把注册整理为 `routeSpec{Method, Pattern, Auth, Roles, Handler}` 切片，`routes()` 遍历注册。注册与一致性检查因此共用同一份数据，检查不需要反射或解析源码。这是本变更唯一的结构性重构，行为不变。

### 3. 一致性检查用离线单元测试

新增 `openapi_test.go`：解析嵌入的契约，取出 `(method, path)` 集合与 `(method, pattern)` 集合双向比对；再对每个受保护操作断言 `security` 非空、每个匿名操作断言 `security` 为空（`GET /api/health` 除外，按公开标注）。测试不启动服务、不访问网络，因此可进 `go test` 常规回归。

路径参数写法统一：实现用 `/api/classes/{id}`，契约用 `/api/classes/{id}`（OpenAPI 允许同名占位符），比对时只做字面比较，避免正则歧义。

### 4. 文档页面自托管

`swagger-ui-dist` 作为前端依赖；构建时把其静态资源复制到 `dist/swagger/`，nginx 增加：

- `location = /api/docs` → 返回一个最小 HTML，`SwaggerUIBundle({ url: "/api/openapi.json", ... })`；
- `location /swagger/` → 提供静态资源；
- `GET /api/openapi.json` 由 `api` 提供（走既有 `/api/` 反代）。

页面不引用任何外部域名，离线可渲染；用 `Authorization` 试调依赖契约里的 `securitySchemes.bearerAuth`（http bearer）。

### 5. 令牌获取方式写入契约

契约在 `POST /api/auth/token` 操作里说明用账号密码换令牌（由 `solver-assistant` 变更交付），并在 `info.description` 给出「先在文档页 Authorize 填令牌，再试调受保护接口」的步骤。若该接口尚未实现，契约仍保留条目——一致性检查以路由表为准，因此该变更需要在 `solver-assistant` 之后实施（任务中标注依赖顺序）。

## Risks / Trade-offs

- **文档漂移**：由双向一致性测试拦截；但字段级细节（例如某个响应少了一个字段）不在自动比对范围，仍靠评审与 `apitest` 交叉验证。
- **Swagger UI 体积**：约 1.5 MB 静态资源随 `web` 镜像发布，属可接受代价；若体积敏感可后续只发布压缩后的最小资源集。
- **契约与令牌变更的耦合**：契约里包含令牌签发操作，因此两个变更存在顺序依赖；若先实施本变更，一致性检查会因路由缺失而失败——任务里已把它列为前置条件。
- **OpenAPI 3.1 工具兼容性**：部分老工具只支持 3.0；本期只需 Swagger UI 与自建检查，已确认可用。
