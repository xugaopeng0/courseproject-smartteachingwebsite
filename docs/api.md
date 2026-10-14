# API 摘要

所有路由以 `/api` 开头。JSON 错误格式为 `{ "error": "可读的中文错误" }`。浏览器使用同源 HttpOnly cookie；接口工具与文档页面可以改用 Bearer 令牌。两种凭证共用同一张会话表，因此登出、过期与失去班级成员身份都会立即失效。

> **机器可读契约**：`docs/openapi.json`（OpenAPI 3.1）是接口的权威描述，覆盖全部操作的方法、参数、请求体、响应与错误码以及鉴权要求。
> - 浏览器直接打开 **`/api/docs`** 即可查看并试调（页面与资源都随服务自托管，不依赖外网 CDN）；先用 `POST /api/auth/token` 换令牌，再点右上角 **Authorize** 填入。
> - 契约原始内容由 `GET /api/openapi.json` 提供。
> - `go test` 中的 `openapi_test.go` 会双向比对契约与路由表、校验鉴权标注与路径参数，并检查副本是否过期（编辑契约后运行 `node scripts/sync-openapi.mjs` 同步到 `backend/openapi.json`）。

| 方法 | 路径 | 用途 / 权限 |
| --- | --- | --- |
| GET | `/health` | 数据库连接健康检查 |
| POST | `/auth/register` | 学生注册：username、name、password |
| POST | `/auth/login` | 登录：username、password；下发 HttpOnly cookie |
| POST | `/auth/token` | 用账号密码换取 Bearer 令牌：返回 `token`、`expires_at`、`user`，**不写 Cookie** |
| GET | `/auth/me` | 当前用户 |
| POST | `/auth/logout` | 撤销本次请求使用的凭证（cookie 或令牌） |
| GET / POST | `/admin/teachers` | 管理员列出/创建教师 |
| GET | `/admin/prompts` | 管理员读取平台默认提示词与平台技能 |
| PUT | `/admin/prompts` | 管理员修改平台默认提示词：`body` |
| PUT / DELETE | `/admin/skills/{name}` | 管理员维护平台技能；内置「解题引导」不允许删除，只能停用 |
| GET / POST | `/classes` | 当前用户班级列表 / 教师创建班级：name、description |
| POST | `/classes/join` | 学生邀请码入班：code |
| GET / PATCH | `/classes/{id}` | 成员读取详情 / 所属教师编辑 name、description |
| POST | `/classes/{id}/archive` | 所属教师归档/恢复：archived |
| DELETE | `/classes/{id}/members/{userID}` | 所属教师移除成员 / 学生退出自己 |
| POST | `/classes/{id}/handouts` | 所属教师上传：multipart file、title（可选） |
| GET | `/handouts/{id}/file` | 所属教师/成员下载；`?preview=1` 预览 PDF |
| DELETE | `/handouts/{id}` | 所属教师删除活动班级内讲义 |
| GET | `/classes/{id}/knowledge/documents` | 所有者/成员查看讲义索引状态与覆盖情况 |
| POST | `/classes/{id}/knowledge/documents/{handoutID}/index` | 未归档班级所有者建立、重试或重建索引 |
| POST | `/classes/{id}/knowledge/search` | 所有者/成员在单一班级内检索讲义原文 |
| GET | `/classes/{id}/knowledge/chunks/{chunkID}/source` | 所有者/成员重新授权后读取引用原文和定位 |
| POST | `/classes/{id}/knowledge/ask` | 所有者/成员单次问答（一次性返回，保留第 4 课契约） |
| GET | `/classes/{id}/prompts` | 成员读取本班生效的提示词与技能状态（学生只读） |
| PUT / DELETE | `/classes/{id}/prompts` | 所属教师保存/移除班级提示词（覆盖平台默认） |
| PUT / DELETE | `/classes/{id}/skills/{name}` | 所属教师覆盖、停用或移除某个技能 |
| GET / POST | `/classes/{id}/sessions` | 成员列出本班会话 / 新建会话 |
| GET | `/classes/{id}/sessions/{sid}` | 会话详情与消息（按 `seq` 升序，最多 50 条） |
| POST | `/classes/{id}/sessions/{sid}/messages` | 发送一条消息，以 `text/event-stream` 流式返回回答 |

鉴权说明：浏览器用 `teaching_session` cookie；接口工具用 `Authorization: Bearer <token>`。**只带令牌**的写请求不做同源校验（令牌不会被浏览器自动携带）；同时带 Cookie 的请求仍按同源规则校验，防止 CSRF。`/auth/token` 的令牌与会话同为 8 小时且不自动续期。

账号为 3–32 位字母、数字或下划线（统一小写），姓名为 1–40 字，密码为 8–72 字节。班级名最多 80 字，简介最多 1000 字，讲义标题最多 120 字。

讲义格式：PDF、DOC、DOCX、PPT、PPTX、TXT、MD，大小上限 50 MiB。TXT/MD 只接受非空 UTF-8 文本，可带 BOM；不接受无效编码、仅 BOM 或二进制控制字符（TAB、CR、LF 除外）。后端决定响应 MIME，不信任上传声明的 Content-Type；TXT/MD 即使请求 `preview=1` 也保持附件下载，不直接渲染 HTML/Markdown。

主要状态码：400 输入无效；401 未登录或会话过期；403 无权访问；404 资源不存在；409 重复/状态冲突；413 文件过大；429 登录或注册频率超限。

知识检索请求为 `{ "query": "...", "mode": "hybrid", "top_k": 5, "handout_ids": [] }`。`query` 去除首尾空白后为 1–1000 个 Unicode 字符，`top_k` 为 1–20，默认 5；`handout_ids` 最多 100 个，且必须都属于该班级，否则按不存在处理（404）。`mode` 取 `keyword`、`vector` 或 `hybrid`，默认 `hybrid`；未知字段（例如调用方自行携带的 `class_id`）在解析后被丢弃，不返回 400。

三种模式的差别：

| mode | 候选来源 | 绝对过滤 | 排序 |
| --- | --- | --- | --- |
| `keyword` | MySQL `MATCH ... AGAINST`（ngram 全文索引） | 至少命中一个二字 token | 全文相关度降序 |
| `vector` | Qdrant 余弦检索（带班级过滤） | 余弦相似度 ≥ 0.35 | 余弦降序 |
| `hybrid` | 两条路径各自执行，各取前 50 条 | 两路各自通过上述过滤 | RRF（k = 60）名次分降序，两路均命中的切片更靠前 |

`keyword` 不调用嵌入服务，因此课程网关不可用时仍可返回结果；`vector` 与 `hybrid` 在网关未配置或故障时返回 503，而不是伪装成没有命中。名次分只用于排序，不是相关性阈值。

检索响应用 `status` 说明本次结果属于哪种情形：

| status | 含义 | 命中列表 |
| --- | --- | --- |
| `ok` | 有权检索且至少有一条达到阈值的结果 | `hits` 非空 |
| `no_ready_documents` | 班级内没有任何已发布的活动索引 | `hits` 为空数组，`message` 为“尚无可检索的讲义” |
| `no_matches` | 有已发布索引，但本次没有命中（或向量库命中都因代次、班级或讲义筛选失效被丢弃） | `hits` 为空数组，`message` 为“资料中未找到相关内容” |

响应同时给出本次使用的 `mode`（与请求一致，缺省为 `hybrid`）与可选 `message`，以及已就绪/总讲义数 `ready_documents` / `total_documents`，部分就绪时 `partial=true`。`hits` 每条包含 `chunk_id`（切片标识，重建后不可复用）、`handout_id`、`title`、`filename`、`content`（原文摘录，取自 MySQL 切片正文）、`content_version`（内容 SHA-256）、`locator`（`type` / `start` / `end` / `label`，DOC/PPT 另带 `converted`）、`ordinal`（讲义内 1 起始切片序号）、`char_start` / `char_end`（在提取文本中的 0 起始半开字符区间）。只有 `vector` 模式额外返回 `similarity`（余弦相似度），它不是事实正确概率；`keyword` 与 `hybrid` 不返回相似度，也不把名次分当作阈值。

读取来源 `GET /classes/{id}/knowledge/chunks/{chunkID}/source` 返回同样的字段（不含 `similarity`）并附 `download_url`；切片所属代次不再是已发布代次时返回 409，讲义已删除或不属于该班时返回 404。

索引接口 `POST /classes/{id}/knowledge/documents/{handoutID}/index` 接受可选请求体 `{ "strategy": "window|separator|heading", "separator": "blank|newline|period", "size": 800, "overlap": 80 }`，成功返回 202 `{"queued": true}`：

- 不带请求体时使用默认窗口策略，`size` 默认 800、`overlap` 默认 80（窗口的 10%）。
- `size` 允许 100–4000；`overlap` 不得超过窗口的一半，省略时取窗口的 10%。
- `separator` 只在 `strategy=separator` 时生效，省略时按空行切分；`strategy=heading` 按 Markdown 标题分章，文档里没有标题时回退默认窗口，不建立空索引。
- 请求体只接受上述四个字段，未知字段、非法取值组合返回 400；班级已归档返回 409，非所有者返回 403。

切片策略、窗口与重叠随索引代次记录在数据库中，接口不返回这些参数；`GET /classes/{id}/knowledge/documents` 只返回每份讲义的标识、标题、文件名、`status`、`error_code`、`has_ready_index` 与更新时间。

知识库接口均发送 `Cache-Control: no-store`。班级授权在服务端执行；跨班讲义/切片标识按不存在处理。引用、下载和 PDF 预览每次重新鉴权，不能使用公共静态文件地址。课程网关不可用、向量检索不可用或向量配置不匹配返回 503，不影响讲义浏览和下载。

问答接口 `POST /classes/{id}/knowledge/ask` 接受 `{ "query": "...", "history": [{ "role": "user|assistant", "content": "..." }] }`，`query` 为 1–1000 字，`history` 最多保留最近 6 条、角色只允许 `user` 与 `assistant`（客户端构造的 `system` 消息会被丢弃）。处理顺序固定为「先检索、后生成」：

1. 以本班混合检索取排序前 4 条；
2. 没有任何命中时直接返回 `{"status":"no_matches","answer":"资料中未找到相关内容","citations":[]}`，**不访问对话网关**；班级没有任何已发布索引时返回 `no_ready_documents`，同样不生成内容；
3. 有命中时才把材料标题、切片序号与切片正文连同用户最新一句交给对话模型，回答为一次性非流式返回；
4. 返回的 `citations` 由服务端从命中切片生成，含 `index`（与回答中的 `[n]` 对应）、`chunk_id`、`handout_id`、`title`、`filename`、`ordinal`、`char_start` / `char_end`、`content`、`locator` 与 `download_url`，顺序与编号一致；回答中超出命中范围的编号会被去方括号处理，不再作为出处呈现。

对话模型未配置时返回 503；网关超时或返回错误时同样返回 503，且不返回部分回答。

## 提示词与技能

平台维护一份默认提示词与内置「解题引导」技能；班级可以覆盖提示词，也可以按名覆盖或停用某个技能。生效规则：

- **提示词**：班级行优先，其次平台行，都没有时只使用固定约束（只依据材料、无依据时固定文案、保留 `[n]` 编号）。
- **技能**：同名技能以班级行为准；班级行 `body` 为空表示沿用平台正文，`enabled=false` 表示本班停用。内置「解题引导」要求模型只给思路与追问、不直接给最终答案，且只能停用不能删除。

`GET /classes/{id}/prompts` 返回 `{ prompt_body, prompt_source: "class|platform|none", skills: [{name, body, enabled, scope}], can_edit, archived }`；`PUT` 接受 `{ "body": "..." }`（去除首尾空白后 1–2000 字），`DELETE` 回到平台默认。技能接口 `PUT /classes/{id}/skills/{name}` 接受 `{ "body"?: "...", "enabled"?: true }`，新建技能必须给正文。写操作只允许所属教师，归档班级返回 403，学生只能读取。

## 会话与流式回答

`POST /classes/{id}/sessions` 新建会话（`{"title": "..."}` 可选，最多 60 字）；`GET /classes/{id}/sessions` 返回本班会话列表（含 `message_count` 与 `mine`）；`GET /classes/{id}/sessions/{sid}` 返回 `{ session, messages }`。消息 `content` 为 1–1000 字，用户消息在流开始前就已落库。

`POST /classes/{id}/sessions/{sid}/messages` 返回 `text/event-stream`，顺序固定为「先检索、后生成」：先混合检索（前 4 条）；没有命中时只返回固定文案且**不调用对话模型**；有命中时才把生效提示词、技能、材料与最近 6 条历史交给模型并逐块转发。

| 事件 | 载荷 | 说明 |
| --- | --- | --- |
| `delta` | `{"text": "..."}` | 增量文本，可多次；无命中时只出现一次固定文案 |
| `citations` | `{"message_id": "...", "citations": [...]}` | 结束前的出处列表，字段与单次问答一致 |
| `done` | `{"status": "complete\|no_matches\|no_ready_documents\|interrupted", "message_id": "..."}` | 结束标记 |
| `error` | `{"error": "..."}` | 中途失败；已生成部分按 `interrupted` 记录 |

响应头带 `Cache-Control: no-store` 与 `X-Accel-Buffering: no`，每 15 秒发送一次 `: heartbeat` 注释行保活。客户端断开时服务端立即停止向网关索取内容，已收到的增量按 `status=interrupted` 保存，绝不记成完整回答；网关不支持流式（返回整段 JSON）时同样按错误处理，不做“攒完再一次性下发”的伪装。检索阶段失败（网关未配置、向量库不可用）在任何事件发出之前以普通 JSON 503 返回，避免流已开始还改状态码。

会话与消息按班级做服务端权限判断：非本班教师与学生一律 403，跨班路径按不存在返回 404；归档班级禁止新建会话与发送消息但保留读取；成员被移除后立即失去访问。
