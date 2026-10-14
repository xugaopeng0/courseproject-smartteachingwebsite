# Design

## Context

- 现有认证：`sessions(token_hash, user_id, expires_at)` 表 + `teaching_session` HttpOnly Cookie；`currentUser` 只读 Cookie；写请求在 `route` 里先做同源校验（`sameOrigin`），浏览器不持有任何令牌。
- 现有问答：`POST /api/classes/{id}/knowledge/ask` 已实现「先检索后生成」——混合检索取前 4 条，无命中直接返回固定文案且不调用对话模型；`answer.go` 已有 `normalizeHistory`、`buildAnswerMessages`、`sanitizeAnswer` 可复用。
- 现有网关客户端：`gatewayClient.chat` 一次性非流式调用（`stream:false`），超时由 `GATEWAY_TIMEOUT` 控制。
- 现有部署：`web`（nginx）是唯一发布端口的服务，`/api/` 经 `resolver 127.0.0.11` + 变量 `proxy_pass` 转发到 `api:8081`，`proxy_read_timeout 150s`。
- MySQL 约定：生成列 + 唯一键代替部分索引；stored 生成列的基列不能使用 `ON DELETE CASCADE`（用 VIRTUAL 或不用级联）；语句由 `applyMigrations` 幂等应用。

## Goals / Non-Goals

**Goals:**

- 令牌与浏览器会话共用一套撤销路径，撤权立即生效。
- 回答真正逐块到达浏览器，而不是「等完整回答再前端打字」。
- 提示词、技能与会话数据都落在 MySQL，权限判断在服务端。
- 复用第 4 课的检索、引用与「无依据不生成」边界，不重复实现。

**Non-Goals:**

- 不引入 WebSocket、不引入独立的消息队列或缓存层。
- 不做多人共享会话、不做作业批改或成绩写入。
- 不改变单次问答接口的既有契约（保留并在文档中标注）。
- 不做令牌的刷新令牌与第三方 OAuth。

## Decisions

### 1. Bearer 令牌复用 `sessions` 表（不引入 JWT）

签发接口 `POST /api/auth/token` 用账号密码校验后写入一行会话记录，返回原始令牌与过期时间；服务端只保存 `token_hash`。`currentUser` 先查 `Authorization: Bearer`，再回退 Cookie。撤销沿用登出与到期清理（`DELETE FROM sessions WHERE expires_at<NOW(6)`）。

理由：撤权（登出、改密、移除成员后的重新鉴权）必须立即生效，JWT 需要额外黑名单才能做到。备选 JWT 被否，代价是每次请求多一次索引查询——现有会话本来就这样，量级可接受。

令牌有效期与会话一致（8 小时），由 `sessions.expires_at` 决定；令牌不做自动续期。

### 2. 同源校验只针对 Cookie 身份

`sameOrigin` 保留给带 Cookie 的写请求（浏览器会自动携带 Cookie，才有 CSRF 风险）；只带 `Authorization` 的请求跳过该校验。判定放在 `route` 中：存在 Bearer 头即视为令牌身份。这样 Swagger 页面与非浏览器客户端可直接调用，而浏览器流程的安全边界不变。

### 3. SSE 事件协议

`POST /api/classes/{id}/sessions/{sid}/messages` 在请求体为 `{"content": "..."}` 时返回 `Content-Type: text/event-stream`，事件为：

| 事件 | 载荷 | 说明 |
| --- | --- | --- |
| `delta` | `{"text": "..."}` | 增量文本，可多次 |
| `citations` | `{"citations": [...], "message_id": "..."}` | 回答结束前的出处列表与消息标识 |
| `done` | `{"status": "ok\|no_matches\|interrupted"}` | 结束标记 |
| `error` | `{"error": "..."}` | 失败原因，随后关闭 |

响应头带 `Cache-Control: no-store` 与 `X-Accel-Buffering: no`；每 15 秒发送一次 `: heartbeat` 注释行保活；`retry: 3000` 供客户端重连参考（服务端不重放历史）。

### 4. 网关流式读取

`gatewayClient` 增加 `chatStream(ctx, messages, emit func(string) error) error`：请求体 `stream:true`，逐行解析 `data:` 载荷，遇到 `[DONE]` 结束；非 200 与流内 `error` 字段都转成明确错误。首字节超时沿用 `GATEWAY_TIMEOUT`，整体用请求上下文控制（SSE 客户端断开即取消上游请求）。若网关不支持流式（返回非 SSE 内容），按错误返回并在 `error` 事件中说明，MUST NOT 静默降级为整段返回后假装流式。

### 5. 数据模型

```sql
prompts(id, scope ENUM('platform','class'), class_id NULL, kind ENUM('prompt','skill'),
        name, body, enabled, created_by, created_at, updated_at,
        scope_key VARCHAR(64) AS (COALESCE(class_id,'')) VIRTUAL,
        UNIQUE KEY prompts_scope (scope_key, kind, name))
chat_sessions(id, class_id, user_id, title, created_at, updated_at, last_message_at)
chat_messages(id, session_id, seq, role ENUM('user','assistant'), content MEDIUMTEXT,
              status ENUM('complete','interrupted','failed'), citations JSON NULL,
              model VARCHAR(64), prompt_chars INT, latency_ms INT, created_at,
              UNIQUE KEY chat_messages_seq (session_id, seq))
```

- `scope_key` 用 VIRTUAL 生成列做唯一键：MySQL 唯一键不约束 NULL，平台级行（`class_id IS NULL`）必须靠 `COALESCE` 才能去重。
- 会话与消息按 `class_id` / `session_id` 建索引；删除班级或成员时不做级联，由既有删除路径显式清理，避免 stored 生成列与级联的限制。
- 内置「解题引导」技能在启动时按 `(platform, skill, 解题引导)` 幂等写入，已存在则不覆盖用户改动，只补缺失行。

### 6. 提示组装

`system` 消息由四段拼成：固定约束（只依据材料、只引导不给最终答案、无依据时返回固定文案）、平台或班级提示词、启用中的技能正文、本次命中材料的标题与切片序号。其后是该会话最近 6 条消息与会话最新一句。长度按既有 `maxHistoryRunes` 与新增的提示词上限（2000 字）双重约束；超出即截断并在日志记录，MUST NOT 让整段内容直接进模型。

技能正文只影响模型行为，服务端仍以「无命中不生成」与「出处由服务端生成」为硬边界，避免技能被写成绕过规则的后门。

### 7. 无命中短路与中断语义

无命中时：持久化助手消息（`status=complete`、`citations=[]`、内容为固定文案），流中只发一个 `delta` 与 `done{status:"no_matches"}`，不调用对话模型——与单次问答一致。

中断时（客户端断开或上游错误）：把已收到的增量持久化为 `status=interrupted` 的消息并在 `done` 中标注，MUST NOT 记成完整回答；已持久化的用户消息保留。

### 8. 前端读取方式

会话页用 `fetch` + `ReadableStream` 手工解析 SSE，而不是 `EventSource`：需要 POST 请求体、需要带 Cookie 与中止控制（`AbortController`）。解析器按空行切分事件、按 `event:` / `data:` 分发，逐块追加到当前气泡；收到 `citations` 后渲染出处卡片（复用第 4 课的出处组件与「查看原文出处」交互）；「停止生成」按钮触发 `abort()`。

### 9. 部署适配

`frontend/nginx.conf` 的 `/api/` 段增加 `proxy_buffering off`、`proxy_cache off`，并把 `proxy_read_timeout` 提高到 300s；`api` 侧对 SSE 响应显式设置 `X-Accel-Buffering: no`，双保险避免整段缓冲。SSE 只在 `web` 之后可用，直连 `api` 端口不在部署形态内（`api` 不发布宿主端口）。

## Risks / Trade-offs

- **代理缓冲导致「假流式」**：靠 nginx 指令 + 响应头双重规避，并在验收里用「首个 delta 到达时间明显早于结束时间」与逐块到达计数确认。
- **模型侧不支持流式或流中断**：以明确错误返回，不降级伪装；已生成部分按中断记录，用户可重发。
- **令牌长期有效**：与浏览器会话同为 8 小时且不可续期；撤销路径与会话共用，登出即失效。
- **消息表增长**：会话列表与消息都分页读取；本期不做自动清理，文档标注容量假设。
- **技能约束只能约束模型不能强制**：服务端硬边界仍是「无命中不生成」与出处由服务端生成；「只引导不给答案」通过提示词与验收用例观察，不承诺形式化证明。
