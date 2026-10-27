# mcp-tool-server Specification

## Purpose
规定自建 MCP 工具服务的对外契约：JSON-RPC 2.0 单入口、三个必备方法、令牌边界、工具清单与输入结构，以及可独立验收的健康检查与部署形态。

## Requirements

### Requirement: 协议与入口
工具服务 SHALL 以 JSON-RPC 2.0 报文提供 `initialize`、`tools/list`、`tools/call` 三个方法，并以 `POST /mcp` 作为唯一 RPC 入口；SHALL 另提供无需令牌的 `GET /health` 供健康检查。服务 SHALL 只接受 `POST` 调用 RPC 入口，其他方法 SHALL 返回可读错误而不是 500。

#### Scenario: 能力协商
- **WHEN** 客户端带有效令牌 POST `initialize`
- **THEN** 返回协议版本、`tools` 能力与服务器名称、版本，且不含任何工具清单

#### Scenario: 非 POST 访问 RPC 入口
- **WHEN** 客户端用 GET 访问 `/mcp`
- **THEN** 返回方法不被允许的可读错误，服务不中断

#### Scenario: 健康检查
- **WHEN** 客户端无令牌 GET `/health`
- **THEN** 返回 200 与 `{"status":"ok","tools":<工具数>}`

### Requirement: 令牌边界
服务 SHALL 从环境变量 `MCP_AUTH_TOKEN` 读取访问令牌；令牌 MUST NOT 出现在代码、镜像层、镜像内文件或任何响应中。缺失令牌或令牌错误时 SHALL 拒绝请求且不暴露工具清单；令牌比较 SHALL 为恒定时间比较。

#### Scenario: 缺少令牌被拒
- **WHEN** 不带 `Authorization` 头调用 `tools/list`
- **THEN** 返回未授权错误，响应体不含任何工具名

#### Scenario: 令牌错误被拒
- **WHEN** 带错误令牌调用 `tools/call`
- **THEN** 返回未授权错误，且未执行任何工具

#### Scenario: 令牌不落盘
- **WHEN** 检查镜像内的文件与任何接口响应
- **THEN** 都找不到 `MCP_AUTH_TOKEN` 的字面值

### Requirement: 工具清单与输入结构
服务 SHALL 至少暴露两个工具；每个工具 SHALL 同时提供名称、说明与输入结构（JSON Schema）。说明 SHALL 写明适用条件与能力边界，MUST NOT 写成宽泛的「万能助手」式描述。名称 SHALL 以小写字母开头、只含小写字母、数字与下划线。

#### Scenario: 发现工具
- **WHEN** 带有效令牌调用 `tools/list`
- **THEN** 每个工具条目都含 `name`、`description`、`inputSchema`，且 `description` 说明何时适用、何时不适用

#### Scenario: 说明与边界的可读性
- **WHEN** 阅读任一工具的说明
- **THEN** 能据此判断该工具能做什么、不能做什么，不需要额外的上下文

### Requirement: 工具执行
`tools/call` SHALL 按工具的输入结构校验参数：未知工具、缺少必填参数、类型或取值不合法 SHALL 返回可读错误且不执行；执行成功 SHALL 返回文本内容数组。工具返回的文本 SHALL 只含数据，MUST NOT 夹带对模型或宿主的指令式语句。

#### Scenario: 参数缺失
- **WHEN** 调用 `formula_hint` 时缺少必填参数
- **THEN** 返回参数错误并指出缺少的字段，未产生任何计算结果

#### Scenario: 未知工具
- **WHEN** 调用一个不存在的工具名
- **THEN** 返回可读错误，服务不崩溃

#### Scenario: 执行成功
- **WHEN** 以合法参数调用 `unit_convert`
- **THEN** 返回文本内容，内容为换算结果与单位，不含任何指令句

### Requirement: 协议错误语义
服务 SHALL 对非法 JSON、未知方法、非法参数分别返回 `-32700`、`-32601`、`-32602` 标准错误码与可读中文说明；错误响应 SHALL 仍为合法 JSON-RPC 报文。工具自身执行失败 SHALL 以 `isError` 标记的结果返回，而不是让整个 RPC 失败。

#### Scenario: 非法 JSON
- **WHEN** 请求体不是合法 JSON
- **THEN** 返回解析错误码与可读说明

#### Scenario: 未知方法
- **WHEN** 请求 `method` 不在支持列表内
- **THEN** 返回方法不存在错误码与可读说明

### Requirement: 部署形态
工具服务 SHALL 作为同一 Compose 工程的独立服务构建启动，与 `api` 处于同一内部网络，MUST NOT 向宿主机发布端口；服务地址 SHALL 可经内部 DNS 以 Base URL 形式访问（例如 `http://mcp-demo:8090`）。令牌 SHALL 只经环境变量注入。

#### Scenario: 同网访问
- **WHEN** 从 `api` 容器访问 `http://mcp-demo:8090/health`
- **THEN** 返回 200 且显示工具数量

#### Scenario: 不发布宿主端口
- **WHEN** 查看 `docker compose ps mcp-demo` 的端口列
- **THEN** 只有容器内端口，没有到宿主机的映射
