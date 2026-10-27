# 项目约定

- 技术栈：React + TypeScript + Go + MySQL 8.4 + Qdrant，Docker Compose 本机部署。
- 嵌入与对话只由服务端调用课程网关（兼容 OpenAI 接口），网关地址、模型名与密钥不下发浏览器；浏览器只访问本站 `/api`。
- 界面与面向用户的文档使用中文，视觉以白色为主、红色为辅。
- 遵循 SDD：功能变更先维护 OpenSpec proposal、specs、design、tasks，再实施和验证；不要在规约缺失时直接扩展产品范围。
- 变更已全部归档在 `openspec/changes/archive/`（第 1–5 课共 9 个变更），主规约位于 `openspec/specs/`：15 份能力规约、`openspec validate --all --strict` 全通过；验证证据见 `docs/verification.md`。
- 第 5 课的三个变更都已实施、验证并归档；主规约已同步。容器级验收（经 web 容器验证 SSE 不缓冲、文档页面可访问、apitest 的 compose-exec 模式）于 2026-10-14 在 Docker 上实测通过，证据见 `docs/verification.md`。
- 接口契约以 `docs/openapi.json`（OpenAPI 3.1）为准，`node scripts/sync-openapi.mjs` 把它与 `swagger-ui-dist` 资源同步到 `backend/`（`openapi.json`、`swaggerui/`）供 `go:embed` 使用；`openapi_test.go` 会双向比对契约与路由表、校验鉴权标注与路径参数，并拦截副本过期。新增接口时必须同时补契约，否则 `go test` 失败。浏览器可直接打开 `/api/docs` 试调。
- 会话回答走 SSE：`proxy_buffering off` 已配在 nginx，新增接口时不要打开缓冲；网关客户端区分一次性 `chat` 与 `chatStream`，不允许把整段返回伪装成流式。
- 自建 MCP 工具服务在 `mcp-server/`（Go 标准库），Compose 服务名 `mcp-demo`，只 expose `8090` 不发布宿主端口。访问令牌只经环境变量 `MCP_AUTH_TOKEN` 注入，不写进代码、镜像与前端；接口与日志都不回传令牌。工具说明必须写明适用与不适用的边界，工具返回值只能是数据，不能夹带指令式语句。
- 后端以服务端权限判断为准。新功能不得仅靠隐藏按钮限制角色；班级文件不得通过公共静态路径提供。
- 切片正文、权限与索引元数据以 MySQL 为权威，向量只存 Qdrant 且仅保存标识类 payload；检索回表时必须重新校验班级与已发布代次。
- 不提交 `.env`、`.local`、`.tools`、数据库、上传文件或凭证；课程提供的种子材料放在 `seeds/`，由 API 启动时幂等导入示例班级。
- 修改相关行为后运行有意义的检查：Go test/vet、前端构建、集成测试及必要的浏览器回归；不要声称未执行的 Docker 验收已通过。
- 后续 AI、作业等功能按独立 OpenSpec 变更开发，当前未实现的能力不在 UI 中伪装为可用入口。
