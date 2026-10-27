# Tasks

> 前置：无（可独立于主应用实施与验收）。后续变更 `tool-bridge` 依赖本变更交付的服务与令牌约定。
> 验收记录见 `docs/verification.md` 的「MCP 工具服务验证」一节；HTTP 级检查脚本为 `.local/mcp-check.mjs`（17/17）。

## 1. 服务骨架与协议

- [x] 1.1 新增 `mcp-server/` 独立 Go 模块（仅标准库）：`main.go` 启动 HTTP 服务、`POST /mcp` 单入口与 `GET /health`；`MCP_AUTH_TOKEN` 缺失时拒绝启动并说明原因（不打印令牌值）。验证：`go vet`、`go test` 通过；未设置令牌启动时进程非零退出并给出可读日志（`TestRunRefusesWithoutToken`）。
- [x] 1.2 实现 JSON-RPC 2.0 编解码与错误语义：`-32700` 解析错误、`-32601` 未知方法、`-32602` 非法参数、`-32001` 未授权；通知（无 `id`）返回 202 空响应。验证：`TestProtocolErrors` 逐条覆盖四种错误码与非法信封；`TestTransportRules` 覆盖通知与非 POST。
- [x] 1.3 实现 `initialize`（返回协议版本、`tools` 能力、服务器名与版本）与 `tools/list`（名称排序、含 `name`/`description`/`inputSchema`）。验证：`TestInitialize` 断言字段齐全且不返回工具清单；`TestToolsList` 断言顺序稳定、两次调用一致。

## 2. 令牌与安全边界

- [x] 2.1 令牌校验：`Authorization: Bearer` + 恒定时间比较；缺失或错误返回 401 且响应体不含工具名。验证：`TestAuthorization` 覆盖「无令牌」「错令牌」「正确令牌」，并断言响应体不含令牌与工具名。
- [x] 2.2 令牌不落盘：确认代码、Dockerfile、镜像内文件与任何响应都不含令牌字面值。验证：在构建后的镜像内 `grep` 令牌字符串为 **0 命中**；`TestAuthorization` 与 `.local/mcp-check.mjs` 断言响应不含令牌。

## 3. 工具实现

- [x] 3.1 实现 `formula_hint`：说明写清适用条件与边界（不做数值代入、不给最终数值答案、不查政策）；输入结构 `topic`（1–200 字，必填）与可选 `level` 枚举；返回公式、符号说明与下一步提示的纯文本。验证：`TestFormulaHint`（含边界措辞与「不给最终答案」断言）、`TestFormulaHintMissingTopic`。
- [x] 3.2 实现 `unit_convert`：说明写清适用条件与边界（不做汇率、不做自定义单位）；输入 `value`/`from`/`to`（长度、质量、时间、容量枚举）；返回纯结果文本。验证：`TestUnitConvertCategories` 四类各一例，`TestUnitConvertCrossCategory`、`TestUnitConvertInvalidInput`。
- [x] 3.3 工具执行与协议错误分层：未知工具与非法参数走 `error`；工具内部失败走 `result.isError=true`。验证：`TestUnitConvertCrossCategory` 与 `TestProtocolErrors` 各覆盖一条，断言报文结构不同。

## 4. 部署与独立验收

- [x] 4.1 新增 `mcp-server/Dockerfile`（`golang:1.26-alpine` 构建、`alpine` 运行、非 root、`HEALTHCHECK` 调 `/health`），`compose.yaml` 增加 `mcp-demo` 服务：`build: ./mcp-server`、`expose: ["8090"]`、`networks: [app]`、`MCP_AUTH_TOKEN: ${MCP_AUTH_TOKEN:-}`；`.env.example` 增加 `MCP_AUTH_TOKEN` 与 `MCP_BASE_URL`。验证：`docker compose build mcp-demo` 成功且容器 healthy；`docker compose ps mcp-demo` 端口列只有 `8090/tcp`；`docker compose config --quiet` 通过。
- [x] 4.2 独立验收三条命令：`GET /health` 返回 200 与工具数；带令牌 `tools/list` 返回两个工具名；带令牌 `tools/call` 的 `unit_convert` 返回换算文本。验证：`.local/mcp-check.mjs` 17/17 通过，含无令牌被拒一次。
- [x] 4.3 同网互通：从 `api` 容器访问 `http://mcp-demo:8090/health` 返回 200。验证：`docker compose exec -T api wget -qO- http://mcp-demo:8090/health` 输出 `{"status":"ok","tools":2}`。

## 5. 文档

- [x] 5.1 `README.md` 增加「工具服务（MCP）」小节：单独构建启动、用令牌验证的方式（容器内无 curl、`wget` 不支持自定义头，改用 Node 发请求）、Base URL 约定；`AGENTS.md` 记录令牌只经环境变量的约定。验证：按文档从零执行一次可复现（含 17/17 脚本）。
- [x] 5.2 `docs/verification.md` 记录本变更的实测证据（健康检查、工具发现、工具调用、无令牌被拒、镜像内无令牌字面值、同网互通、端口隔离）。验证：每条结论对应一条可复现命令或脚本。
