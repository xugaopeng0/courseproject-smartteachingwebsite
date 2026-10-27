# Design

## Context

- 现有 Compose：`api`／`worker`（context `./backend`）、`web`（发布 `127.0.0.1:8080`）、`db`、`qdrant`；网络 `app`（internal）与 `edge`，只有 `web` 同时接入两者。
- 现有后端为单 BinGo 模块，无第三方框架；`api` 与 `worker` 由同一 Dockerfile 构建不同入口。
- 课程网关是唯一向外暴露的模型通道；工具服务是**反过来**由宿主调用的服务，不经过课程网关。
- 约束：`web` 是唯一发布端口，新增服务不发布端口；令牌只经环境变量注入。

## Goals / Non-Goals

**Goals:**

- 工具服务可脱离主应用单独验证（`/health`、`tools/list`、`tools/call` 三条命令即可）。
- 协议错误可读，客户端不会因为一个坏请求看到「空响应」或 500 堆栈。
- 工具说明写清边界，让模型能在候选集内自行判断该不该用。
- 工具返回纯数据，宿主后续做指令句过滤时以「数据」为前提。

**Non-Goals:**

- 不实现 MCP 的 `resources`、`prompts`、`sampling`、通知流（SSE/streamable HTTP 的服务端推送）等其余能力。
- 不做工具级限流、计费或审计（调用记录由宿主侧统一记录）。
- 不引入 MCP 官方 SDK 或任何第三方依赖：只依赖 Go 标准库，保证离线可构建。

## Decisions

### 1. 语言与依赖：独立 Go 模块、仅标准库

`mcp-server/` 是独立 Go 模块（`module mcp-server`），只用 `net/http`、`encoding/json`、`crypto/subtle` 等标准库。镜像沿用现有 `golang:1.26-alpine` 构建阶段 + `alpine` 运行阶段，与 `api` 的构建方式一致。

理由：不引入 SDK 依赖可避免离线构建与版本漂移问题，协议面很小（三个方法 + 错误语义）。备选：使用官方 Go SDK，被否——会引入网络依赖且隐藏报文细节，不利于课程演示与排错。

### 2. 传输：单个 HTTP 端点 + JSON-RPC 2.0

```
POST /mcp        JSON-RPC 单入口（initialize / tools/list / tools/call / notifications/initialized）
GET  /health     200 {"status":"ok","tools":N}（无令牌）
GET  /mcp        405 + 可读说明
```

请求体：`{"jsonrpc":"2.0","id":<number|string>,"method":"...","params":{...}}`。
成功响应：`{"jsonrpc":"2.0","id":<id>,"result":{...}}`。
错误响应：`{"jsonrpc":"2.0","id":<id|null>,"error":{"code":<int>,"message":"<中文说明>"}}`。

通知（`id` 缺省）返回 HTTP 202 与空响应体，符合 JSON-RPC 对通知不回包的约定。

### 3. 令牌校验

`Authorization: Bearer <MCP_AUTH_TOKEN>`；缺失或不匹配返回 **HTTP 401** 且响应体为 JSON-RPC 错误（`code: -32001`，`message: "未授权：缺少或错误的访问令牌"`）。使用 `subtle.ConstantTimeCompare` 比较。令牌只从 `os.Getenv("MCP_AUTH_TOKEN")` 读取；未配置令牌时服务拒绝启动（避免「空令牌即放行」的常见漏洞），并在日志中说明原因但不打印令牌值。

`/health` 不校验令牌，因为它不返回工具信息，只回计数，供 Compose 健康检查与编排探活用。

### 4. 方法语义

| 方法 | params | result |
| --- | --- | --- |
| `initialize` | `{protocolVersion?, clientInfo?}` | `{protocolVersion:"2025-06-18", capabilities:{tools:{listChanged:false}}, serverInfo:{name:"campus-mcp-tools", version:"1.0.0"}}` |
| `tools/list` | 无 | `{tools:[{name, description, inputSchema}, ...]}` |
| `tools/call` | `{name, arguments}` | `{content:[{type:"text", text}], isError:false}`；工具失败时 `isError:true` 并给出说明文本 |
| `notifications/initialized` | 无 | 202 空响应 |

`tools/list` 的顺序稳定（按名称排序），便于宿主列表整体替换时对比。

### 5. 工具与输入结构

两个工具，均为「数据型」能力，说明中写明**何时适用、何时不适用**：

1. `formula_hint`
   - 说明：当学生询问某个数学/统计/物理公式的**构成、符号含义或解题思路**时使用；只给出公式与符号解释、以及可自行尝试的下一步，**不做数值代入、不给最终数值答案**；不适用于查课程政策、资料检索或非计算类问题。
   - 输入：`{ "topic": string(1..200, required), "level": "beginner"|"intermediate"|"advanced"(optional) }`
   - 输出文本：公式、符号说明、下一步提示（纯文本，不含指令句）。
2. `unit_convert`
   - 说明：当用户**明确要求把一个带单位的值换算成另一单位**时使用；只做同类量的纯单位换算，**不做货币汇率、不做自定义单位**，也不对数值的业务含义做判断。
   - 输入：`{ "value": number(required), "from": enum(required), "to": enum(required) }`，支持长度（m/cm/km）、质量（kg/g）、时间（s/min/h）、容量（L/mL）。
   - 输出文本：`12.5 km = 12500 m`（纯结果）。

### 6. 参数校验（JSON Schema 子集）

服务端与宿主各自实现同一份最小校验逻辑（`type`、`required`、`enum`、`minimum`/`maximum`、`minLength`/`maxLength`、`items`），约 100 行；两处实现互为镜像并在各自测试中用同一组用例表覆盖。之所以不引入 JSON Schema 库：离线构建约束 + 只需一个子集；之所以不共享包：`api` 与 `mcp-server` 是两个构建上下文（`./backend` 与 `./mcp-server`），共享需要改动既有构建上下文，收益不抵风险。

### 7. 工具执行失败与协议错误的区别

工具「执行失败」（例如换算不支持的同类量）属于业务结果，用 `result.isError=true` 返回，客户端仍拿到可读说明；只有协议层问题（非法 JSON、未知方法、非法参数、未知工具）才用 `error` 对象。这样宿主可以把「工具说这么做不行」与「协议坏了」分开处理。

### 8. 部署

```yaml
mcp-demo:
  build: ./mcp-server
  environment:
    MCP_AUTH_TOKEN: ${MCP_AUTH_TOKEN:-}
  expose: ["8090"]          # 不发布宿主端口
  networks: [app]
  healthcheck: 用 wget 访问 /health
```

`api` 与 `worker` 可通过 `http://mcp-demo:8090` 访问；宿主（浏览器）永远不直连该服务，令牌也不会出现在前端。

## Risks / Trade-offs

- **只实现三个方法**：客户端若要求 SSE 推送或 `resources`，会得到「方法不存在」。课程演示与宿主侧只用到这三个方法，按期交付不扩大协议面；文档中标注为有界实现。
- **令牌轮换需要重启**：令牌只在启动时读取，轮换需重启容器。演示环境可接受，文档中标注。
- **工具集固定**：本期工具写死在服务内，不支持运行时注册。若要扩展需重新构建，避免把工具定义做成第二套权限体系。
- **重复的校验器**：两份约 100 行实现有漂移风险，用同一组测试用例表减少分叉；代码注释里互相指出对方位置。
