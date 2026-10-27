# Tasks

> 前置依赖：`mcp-tool-server`（自建工具服务与 `MCP_AUTH_TOKEN` 约定）。本变更依赖它提供的 `POST /mcp` 三方法与 `/health`。
> 验收记录见 `docs/verification.md` 的「MCP 工具接入验证」一节。

## 1. 数据模型与配置

- [x] 1.1 新增迁移 `004_tool_bridge.sql`：`tool_servers`（唯一键 `(owner_id,name)`、`token_cipher`、`connected`、`last_error`）、`tool_server_tools`（复合主键 + 级联删除）、`tool_calls`（只增不改，按所有者与会话建索引）。验证：对已有库重复执行两次不报错；`go test` 覆盖表结构与幂等（`TestMigrationIsIdempotent` 等）。
- [x] 1.2 令牌加密与配置：`TOOL_SECRET_KEY`（32 字节派生 AES-256-GCM 密钥）、`CHAT_MAX_TOOL_ROUNDS`、`CHAT_MAX_TOOL_CALLS`；未配置密钥时写入服务器级令牌返回 400。验证：`TestToolServerRegistry`（无密钥时 400）与令牌往返用例。
- [x] 1.3 `.env.example` 增加上述配置项与说明。验证：补全后 `docker compose config --quiet` 通过。

## 2. 工具服务客户端

- [x] 2.1 `tools_client.go`：JSON-RPC 调用封装（`initialize`、`tools/list`、`tools/call`），带超时、Bearer 令牌与可读错误分类。验证：`TestToolServerConnectFailure`（不可达/握手失败）与连接成功用例。
- [x] 2.2 连接状态机：两步都成功才替换清单并标记已连接；任一步失败停在未连接、清空清单、写 `last_error`。验证：`TestToolServerConnectDisconnect`、`TestToolServerConnectFailure`。
- [x] 2.3 令牌解析顺序（服务器密文 → 环境变量）与 `has_token` 计算。验证：`TestToolServerRegistry` 断言响应只有布尔标记、不含令牌；apitest 同名断言。

## 3. 登记、连接与断开接口

- [x] 3.1 `GET/POST /api/tool-servers`：登记、列表（连接状态、工具数、`has_token`）；名称唯一；学生 403。验证：`TestToolServerRegistry` + apitest `tool-servers/GET.py`、`POST.py`。
- [x] 3.2 `PATCH/DELETE /api/tool-servers/{id}`：改名与删除；他人标识 404。验证：`TestToolServerRenameAndIsolation`（改名成功、改名后仍可连接、重名 409、他人改名/删除 404）+ apitest `DELETE.py`。
- [x] 3.3 `connect` / `disconnect`：连接失败可读原因且保持未连接；断开清空清单；重复断开幂等；连续连接不翻倍。验证：`TestToolServerConnectDisconnect` + apitest `connect/POST.py`、`disconnect/POST.py`。
- [x] 3.4 连接失败不影响主路径。验证：`TestConnectFailureKeepsChatPath`（连接 502 后提问仍 200 且有完整流式事件）。

## 4. 技能工具项

- [x] 4.1 技能读写支持 `tools`（内置 `knowledge_search` 与 `工具@服务器`），平台技能只允许内置，最多 8 条。验证：`TestSkillToolRefValidation` 覆盖合法与四种非法。
- [x] 4.2 保存期校验顺序（格式 → 归属 → 已连接 → 在清单内），失败不改动既有内容。验证：`TestSkillToolSaveKeepsPreviousContent`（400 后数据库正文与工具项不变）。
- [x] 4.3 前端技能编辑器增加工具项，保存报错就地显示。验证：`.local/tool-ui-check.mjs`（保存成功回显 + 断开后保存报错）。

## 5. 编排循环

- [x] 5.1 候选集计算：生效且启用的技能 → 去内置检索 → 只保留已连接且存在的工具 → 去重 → 上限裁剪。验证：`TestCandidateToolsFilteringAndCap`。
- [x] 5.2 网关客户端支持工具调用：请求带 `tools`，流式解析 `delta.tool_calls`（按索引累积）。验证：`TestOrchestrationCallsToolAndRecords`、`TestOrchestrationHonoursLimits`。
- [x] 5.3 宿主执行：校验参数 → 带令牌 `tools/call` → 净化 → 追加 assistant/tool 消息 → 下一轮。验证：`TestOrchestrationCallsToolAndRecords`。
- [x] 5.4 上限与降级：轮次与累计调用上限、超限不再声明工具并用已有信息收尾。验证：`TestOrchestrationHonoursLimits`（`CHAT_MAX_TOOL_CALLS=0` 时不声明工具、仍作答）。

## 6. 结果净化

- [x] 6.1 句子级过滤与超长截断。验证：`TestSanitizeToolText`、`TestSanitizeKeepsDataDropsInstructions`。
- [x] 6.2 净化结果只作为工具消息回传，不进入 system 提示词。验证：`TestToolResultStaysOutOfSystemPrompt`。

## 7. 流式事件与元数据

- [x] 7.1 新增 `tool_call` 事件（调用中/结束）并先于正文推送。验证：`TestOrchestrationCallsToolAndRecords`（事件序为 tool 先于 delta）+ apitest 端到端用例 + 界面用例。
- [x] 7.2 `done` 事件增加 `tools` 数组。验证：端到端用例断言 `done.tools` 含成功状态；界面显示工具步骤。

## 8. 调用记录

- [x] 8.1 每次调用写一条记录；写前重读服务器，缺失或改名则跳过。验证：`TestOrchestrationCallsToolAndRecords`（字段完整）、`TestOrchestrationRejectsUnknownTool`（被拒也写）、`TestToolCallRecordSkippedWhenServerRenamed`。
- [x] 8.2 `GET /api/tool-calls`：按所有者范围、默认 `limit=50`、上限 200、支持结果与会话筛选、学生 403。验证：apitest `tool-calls/GET.py`。
- [x] 8.3 前端调用记录视图（三种结果可见、可筛选）。验证：`.local/tool-ui-check.mjs`（成功条目 + 筛选）；被拒/出错由接口用例与单元测试覆盖。

## 9. 文档与契约

- [x] 9.1 更新 `docs/openapi.json`（7 个新接口与工具 schema）并同步；一致性检查通过。验证：`TestOpenAPIMatchesRoutes`（46 个操作）、`TestOpenAPISecurityMatchesRoutes`、`TestOpenAPIPathParametersExist`、`TestOpenAPICopyIsCurrent`。
- [x] 9.2 更新 `docs/api.md`（工具服务、技能工具项、流式工具事件、调用记录）、`README.md`（宿主侧接入说明）、`apitest/README.md`（新增用例与运行前提）。验证：命令与字段可直接对照实现。

## 10. 接口测试

- [x] 10.1 `apitest/` 新增用例：登记（201/409/400/403）、连接（成功/失败/重复不翻倍）、断开（幂等）、删除（404）、记录（默认上限/筛选/403）。验证：`npm run test:api` 15/15（Compose、compose-exec 模式）。
- [x] 10.2 端到端用例：连接 → 保存引用 `formula_hint@…` 的技能 → 会话提问 → 记录查到同一条成功（工具名、服务器名、会话一致）。验证：`tool-flow/POST.py` 通过。

## 11. 验收证据

- [x] 11.1 端到端实测：连接 → 发现 → 绑定技能 → 对话中触发 → 记录含工具引用与服务器名。验证：`.local/tool-ui-check.mjs` 6/6 + 截图；`docs/verification.md` 记录实测数字。
- [x] 11.2 断开后不可调用、上限生效、令牌不外泄三项各实测一次。验证：断开后保存被拒（界面 + 400）、`CHAT_MAX_TOOL_CALLS=0` 用例、`has_token` 与镜像内令牌 0 命中。
- [x] 11.3 同伴交叉验证四项的步骤与证据位就绪。验证：`docs/verification.md`「同伴交叉验证」给出四条可复现步骤（隔离、令牌不回传、断开即失效、记录范围）。
