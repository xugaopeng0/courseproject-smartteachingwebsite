# Proposal

## Why

第 5 课的「解题助手」只能依据本班讲义作答；一旦学生问的是需要在课堂材料之外做一次计算或换算的问题，模型只能凭提示词硬猜，无法调用确定性工具。第 6 课要求接入 MCP 工具：先有一个**独立可验**的工具服务，再由宿主把它接进对话。

当前仓库没有任何对外暴露工具能力的服务，Compose 里也没有可与 `api` 同网互通的工具容器；工具调用的令牌边界、协议报文与工具描述都还不存在。

## What Changes

- **新增 `mcp-server/` 子目录**：与主应用同一套工程，由现有 `docker-compose.yml` 一并构建启动（服务名 `mcp-demo`，内部监听 `8090`，不发布宿主端口）。
- **实现 MCP 协议三方法**：`initialize`（能力协商与协议版本）、`tools/list`（工具发现）、`tools/call`（执行工具）；报文为 JSON-RPC 2.0，单一入口 `POST /mcp`，另提供 `GET /health`。
- **访问令牌从环境变量读取**：`MCP_AUTH_TOKEN` 由 Compose 从 `.env` 注入，不写进代码、不进镜像层、不下发前端；缺失或错误即拒绝，非法请求返回可读的 JSON-RPC 错误。
- **两个工具**（名称、说明、输入结构三项齐全）：`formula_hint`（给出公式结构、符号含义与解题思路，不做数值代入、不给最终数值答案）与 `unit_convert`（带单位数值的纯单位换算）。说明写清适用条件与边界，供模型判断是否选用；输入结构用 JSON Schema 表达，供宿主在调用前校验参数。
- **工具返回文本数据**：只回可供模型组织回答的纯文本，不含任何指令式语句。

## Capabilities

### New Capabilities
- `mcp-tool-server`: 自建工具服务的协议契约（JSON-RPC 方法与错误语义）、工具清单与输入结构、令牌边界、部署形态与可独立验收的健康检查。

## Impact

- **新服务**：`mcp-server/`（Go，仅标准库）；`compose.yaml` 增加 `mcp-demo` 服务并入 `app` 网络。
- **配置**：`.env` 与 `.env.example` 增加 `MCP_AUTH_TOKEN`、`MCP_BASE_URL`。
- **后端**：本变更不改主应用代码；宿主侧接入由后续变更 `tool-bridge` 完成。
- **文档**：`README.md` 增加工具服务的构建与单独验证方式；`docs/verification.md` 记录本变更的实测证据。
