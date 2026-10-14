# Tasks

> 前置依赖：`solver-assistant` 变更交付 `POST /api/auth/token`；本变更的一致性检查以路由表为准，因此需在该变更之后再实施。

## 1. 路由表与契约骨架

- [x] 1.1 把 `main.go` 中的路由注册整理为可枚举的 `routeSpec` 切片（方法、模式、是否需要登录、处理器），`routes()` 遍历注册，行为保持不变。验证：既有 73 项 HTTP 集成与 Go 测试全部通过，说明重构未改变对外行为。
- [x] 1.2 新增 `docs/openapi.json`（OpenAPI 3.1）：`info`、`servers`、`components.securitySchemes`（`bearerAuth` 与 Cookie）与全部操作的骨架条目。验证：JSON 可解析；条目数与路由表一致（一致性检查断言当前为 39 个操作）。
- [x] 1.3 为每个操作补全参数、请求体字段与约束、成功响应结构与错误码说明。验证：逐条对照 `docs/api.md` 与处理器实现；一致性检查会拦截「实现有而文档缺」「文档有而实现无」与「路径参数未声明」三类差异。

## 2. 提供服务与文档页面

- [x] 2.1 `go:embed` 嵌入契约并实现 `GET /api/openapi.json`（匿名可读、`Cache-Control: no-store`）。验证：接口返回内容与仓库文件字节一致（`TestApiDocumentationEndpoints`）。
- [x] 2.2 引入 `swagger-ui-dist`（Apache-2.0）并把文档页面所需的 7 个资源随二进制自托管。**实现方式调整**：`go:embed` 无法读取 `backend/` 之外的目录，因此由 `scripts/sync-openapi.mjs` 把 `docs/openapi.json` 与 `frontend/node_modules/swagger-ui-dist` 的静态资源同步到 `backend/`（`openapi.json`、`swaggerui/`），页面与资源统一由 API 在 `/api/docs` 与 `/api/docs/{asset}` 提供，前端构建与 nginx 都不再需要额外步骤。验证：同步脚本 `--check` 与 `TestOpenAPICopyIsCurrent` 保证副本不过期；`TestApiDocumentationEndpoints` 断言页面与两个资源可读取。
- [x] 2.3 文档页面经 `/api/` 前缀提供，因此无需改动 nginx，也不会被 SSE 的缓冲配置影响。验证：容器内实测 `http://localhost:8080/api/docs` 200 且返回文档页面 HTML、`/api/openapi.json` 200（30 条路径）、`/api/docs/swagger-ui-bundle.js` 200；本地栈走同一条 `/api` 反代语义亦通过。
- [x] 2.4 页面配置为读取 `/api/openapi.json` 并启用 Authorize（Bearer）。验证：浏览器打开 `/api/docs` 能看到分组与操作，页面不引用任何外部域名（测试断言 + 浏览器请求监听，6/6 通过）。

## 3. 一致性检查

- [x] 3.1 新增 `openapi_test.go`：双向比对 `(method, path)` 集合与路由表，差异时给出缺失/多余清单。验证：`TestOpenAPIMatchesRoutes` 通过。
- [x] 3.2 鉴权标注一致性：受保护操作必须有 `security`，匿名操作必须没有。验证：`TestOpenAPISecurityMatchesRoutes` 覆盖全部 39 个操作。
- [x] 3.3 路径参数一致性：断言契约声明的路径参数与实现中的 `{name}` 占位符双向一致且 `required`。验证：`TestOpenAPIPathParametersExist` 通过（实施过程中它抓出 `/api/admin/skills/{name}` 漏声明参数，已修复）。

## 4. 试调可用性

- [x] 4.1 用 `POST /api/auth/token` 取令牌，在文档页面 Authorize 后试调一个受保护接口。验证：浏览器脚本填入令牌后执行 `GET /api/auth/me` 得到 200 与真实用户；不填令牌执行同一接口得到 401。
- [x] 4.2 契约中的错误码与实际一致：按文档构造非法请求（超长查询、未知模式、未登录、跨站写请求），断言返回状态码与错误字段一致。验证：五条错误用例逐条对照并记录（400/400/401/400/403）。

## 5. 文档与验收

- [x] 5.1 更新 `docs/api.md`（顶部指向契约与文档页面）、`README.md`（如何打开文档页面与获取令牌）、`AGENTS.md`（契约位置与一致性检查命令）。验证：文档中命令可直接复制执行。
- [x] 5.2 `docs/verification.md` 记录：契约条目数、页面与试调结果、一致性检查的正反两次结果。验证：每条结论对应一条可复现命令或测试名。
- [x] 5.3 全量检查：`go vet ./...`、`go test ./...`、前端构建、73 项 HTTP 集成、浏览器回归、`openspec validate --all --strict`。验证：逐条记录通过结果。
