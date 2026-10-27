# Design

## Context

- 「解题助手」现有链路：`sendChatMessage` 先持久化用户消息 → 本班混合检索取前 4 条 → 无命中短路（不调模型）→ 有命中则组装 system 提示词（固定约束 + 生效提示词 + 启用的技能正文 + 材料）→ `gatewayClient.chatStream` 逐块转发 → 结束前发 `citations` 与 `done`。
- 技能已存在：平台级 + 班级级（`prompts` 表，`kind=skill`），有 `body` 与 `enabled`；启用的技能正文进入 system 提示词。
- 网关是 OpenAI 兼容接口，支持 `tools` 声明与 `tool_calls` 返回；当前客户端只处理 `delta.content`。
- 认证：Bearer 令牌与浏览器 Cookie 共用 `sessions` 表；写请求的同源校验只对 Cookie 身份生效。
- 既有约定：范围只取自会话所属班级；跨范围一律按「不存在」处理；列表接口默认带条数上限。

## Goals / Non-Goals

**Goals:**

- 工具是**增益**：连接失败、调用失败、超限都必须降级为「用已有信息作答」，不得让检索与流式对话主路径失败。
- 令牌只在服务端出现：登记接口可写入令牌，但任何响应都只回 `has_token`。
- 调用记录能回答五个问题，并且只增不改。
- 工具结果按「数据」处理：净化后再回传模型，永不进入 system 提示词。

**Non-Goals:**

- 不做工具调用的并发执行（一次一轮，串行）。
- 不做工具级配额、计量或跨用户共享服务器。
- 不做 SSE 之外的传输（不引入 WebSocket），也不做 MCP 的 `resources` / `sampling`。
- 不改变「无命中不生成」的既有硬边界。

## Decisions

### 1. 数据模型

```sql
tool_servers(id, owner_id, name, base_url, token_cipher VARCHAR(512) NULL,
             connected TINYINT(1), connected_at, last_error VARCHAR(500), created_at, updated_at,
             UNIQUE KEY tool_servers_owner_name (owner_id, name),
             KEY tool_servers_owner (owner_id))
tool_server_tools(server_id, name, description, input_schema JSON, discovered_at,
                  PRIMARY KEY (server_id, name),
                  CONSTRAINT ... FOREIGN KEY (server_id) REFERENCES tool_servers(id) ON DELETE CASCADE)
tool_calls(id, owner_id, class_id NULL, session_id NULL, turn INT, trigger_user_id,
           server_id NULL, server_name VARCHAR(64), tool_name VARCHAR(64),
           outcome ENUM('success','rejected','error'), detail VARCHAR(500), created_at,
           KEY tool_calls_owner (owner_id, created_at), KEY tool_calls_session (session_id))
```

- `tool_server_tools` 用复合主键做「清单整体替换」：连接成功时在同一事务里 `DELETE` + 批量 `INSERT`，因此连续连接两次条数不翻倍。
- `tool_calls` 不设外键到 `tool_servers`（服务器删除后历史记录仍需保留）；`server_id` 可为 NULL。只增不改：没有更新与删除接口。
- 令牌列存 AES-256-GCM 密文（随机 nonce 前置、base64），密钥来自 `TOOL_SECRET_KEY`（32 字节，SHA-256 派生）；未配置密钥时只允许使用环境变量令牌，写入服务器级令牌返回 400 并说明。

### 2. 令牌解析与 `has_token`

服务端按顺序解析：服务器记录里的密文 → 环境变量 `MCP_AUTH_TOKEN`。`has_token = 密文存在 或 环境变量非空`。响应体只含 `has_token: true|false`，MUST NOT 含令牌、密文或长度提示。日志同样不打印令牌。

### 3. 连接状态机

`POST /api/tool-servers/{id}/connect`：

1. 读服务器记录（校验归属）；2. 解析令牌（无令牌 → 400「未配置访问令牌」）；3. 调 `initialize`（超时 5s）；4. 调 `tools/list`（超时 5s）；5. 两步都成功 → 事务内替换清单、`connected=1`、`last_error=''`；任一步失败 → `connected=0`、清空清单、`last_error` 写可读原因、返回 400 或 502。

`POST .../disconnect`：`connected=0` + 清空清单，返回最新状态。已断开时再断开是幂等成功。

超时与错误分类：连接拒绝/超时 → 502「无法连接工具服务」；`initialize` 返回 JSON-RPC 错误 → 502 并带上对方的 `message`；`tools/list` 缺字段或为空 → 502「工具清单格式不合法」。

### 4. 技能的工具声明

技能的 `tools` 是字符串数组，元素两种形式：

- `knowledge_search`（内置检索，固定标识，不需要服务器）；
- `<tool>@<server>`，其中 `tool` 满足 `^[a-z][a-z0-9_]{0,63}$`，`server` 满足 `^[A-Za-z0-9][A-Za-z0-9-]{0,63}$`。

保存时逐条校验：格式 → 服务器存在且属于**本次请求者**（班级技能：该班所属教师；平台技能只允许内置检索）→ 服务器已连接 → 工具在该服务器的发现清单内。任一不过返回 400 并指出是哪一条、为什么。最多 8 条。

平台技能 MUST NOT 引用外部工具：管理员没有服务器归属，跨用户引用会破坏隔离；返回 400 并说明。

### 5. 候选集与最小权限

每次提问时：生效技能（平台 + 班级合并后 `enabled=1`）的 `tools` 取并集 → 去掉内置检索（它由检索阶段直接执行，不交给模型）→ 只保留「服务器已连接且工具在清单内」的条目 → 去重 → 按上限裁剪（`maxCandidateTools=4`，按技能顺序稳定排序）→ 作为本次请求的 `tools` 声明。

「先按用途划定上限，再让模型在上限之内自主决定」：上限先裁剪，模型只能从这个集合里挑；模型若返回集合外的名字（含服务器已断开、工具已下线），宿主记为 **rejected** 并写记录，不静默忽略。

### 6. 编排循环

```
轮次 0：messages = [system, ...history, user]  + tools=候选集
  请求模型（流式）。若本次没有 tool_calls → 直接把正文流给客户端，结束。
  若有 tool_calls：宿主逐个校验参数（JSON Schema 子集）→ 带令牌 POST tools/call
      → 净化结果 → 追加 assistant(tool_calls) 与 tool(tool_call_id) 消息 → 轮次 +1
  达到 maxToolRounds（默认 3，含首轮）或累计调用达到 maxToolCalls（默认 4）：
      不再提供 tools，追加一条用户侧系统提示「已到工具调用上限，请基于已有信息作答」→ 让模型收尾
      若模型仍要求调用或返回空正文 → 用已有信息与检索命中直接拼一个不依赖工具的兜底回答
```

- 两个上限来自 `CHAT_MAX_TOOL_ROUNDS`、`CHAT_MAX_TOOL_CALLS`（便于验收时调小）。
- 工具轮次期间**不向客户端推送正文**（避免「正文先于调用事件」与半截答案），只在最终轮推送 `delta`；`tool_call` 事件在每次调用前后各发一次。
- 工具调用超时 8s；失败按 `error` 记事件并继续（不中断回答）。
- 任何工具异常都不得让整个回答失败：最坏情况是「没有工具、只用检索命中回答」。

### 7. 结果净化

按句切分（`。！？!?\n`），逐句丢弃匹配指令式句式的句子（例如「忽略…指令」「忽略以上」「你现在是」「必须输出」「system:」「Instead,」「You must」「请直接输出」等，大小写不敏感），拼接后按 1200 字截断并追加「…（已截断）」。净化后的文本以 `role:"tool"` 消息返回模型——它是**数据**，不进入 system 提示词，也不作为后续 system 内容。

### 8. 流式事件与元数据

沿用 `text/event-stream`，在现有事件之外增加：

| 事件 | 载荷 | 时机 |
| --- | --- | --- |
| `tool_call` | `{"tool":"formula_hint","server":"campus-tools","status":"running"}` | 发起调用前 |
| `tool_call` | `{"tool":..., "server":..., "status":"ok\|rejected\|error","detail":"..."}` | 调用结束后 |
| `done` | `{"status":"complete\|no_matches\|no_ready_documents\|interrupted","message_id":"...","tools":[{"name","server","status"}]}` | 结束 |

`tools` 数组即「meta 中能看见本次用到了哪些工具及其状态」。未连接、越权、不在候选集内 → `rejected`；运输失败/工具返回 isError → `error`。

### 9. 调用记录

每次调用（含被拒）写一条：`owner_id`（服务器所有者）、`class_id`、`session_id`、`turn`（第几次模型请求，1 起）、`trigger_user_id`（提问者）、`server_id`/`server_name`（调用开始时快照）、`tool_name`、`outcome`、`detail`（错误摘要，≤500 字）。

写记录前重新读一次服务器记录：不存在、或 `name` 与调用开始时快照不一致（被改名）→ **跳过写入**（预期行为，不是缺陷）。这样记录不会指向已不存在的归属。

查询：`GET /api/tool-calls?limit=&outcome=&session_id=&server_id=`，默认 `limit=50`、上限 200；普通教师只见 `owner_id = 自己` 的记录；学生不可访问（403）；跨范围 `session_id` 按「不存在」返回空列表而不是 404（列表语义）。

### 10. 权限与角色

工具服务与调用记录是**教师**（管理角色）的能力：`GET/POST/PATCH/DELETE /api/tool-servers*`、`GET /api/tool-calls` 只允许教师；学生 403。服务器归属按 `owner_id` 隔离：访问他人服务器 id 一律 404（与不存在一致）。

### 11. 前端

- 教师侧新增「工具」页：服务器列表（名称、Base URL、连接状态、工具数、`has_token`）、登记表单（名称/Base URL/令牌，令牌只写不回显）、连接/断开/删除、展开看发现到的工具；下方「调用记录」表格（时间、工具@服务器、结果、会话、第几轮、提问者），带结果筛选与条数选择。
- 班级设置：技能编辑器增加「工具」输入（内置检索 + `tool@server` 选择器），保存报错就地显示。
- 解题助手：工具调用显示为有序的可见步骤（调用中 / 成功 / 被拒 / 出错），结束后保留。

### 12. 网关替身扩展（仅开发替身）

为在无真实模型时也可验证编排：当请求带 `tools` 且用户消息包含「公式」且候选集里有 `formula_hint` 时，返回一段 `tool_calls`（`finish_reason=tool_calls`）；收到 `role:"tool"` 消息后，把工具结果作为回答正文返回。这样端到端用例可离线断言「调用发生、记录出现」。真实网关下由模型自行决定。

## Risks / Trade-offs

- **模型选择工具的不确定性**：候选集与说明决定倾向，但无法保证每次都调用；验收用「可触发的问题」与替身做确定性覆盖，真实模型下的行为记为观察项。
- **工具轮次拖长响应**：设 3 轮 / 4 次上限与 8s 超时；工具轮次不推正文，避免半截答案。
- **令牌安全**：加密密钥不在仓库；未配置时退化为环境变量令牌（部署配置一致性由验收项检查）。任何响应与日志都不回传令牌。
- **记录只增不改**：长期增长；文档标注保留策略为「本课程不清理」，查询有默认上限。
- **平台技能不能引用外部工具**：牺牲了「平台级统一挂工具」的便利，换取跨用户隔离的确定性。
