# Tasks

> 前置依赖：`solver-assistant`（提示词／技能、会话与流式消息、Bearer 令牌）与 `openapi-swagger`（契约）先落地，本套件才能覆盖提示词／技能、会话与流式的用例。

## 1. 骨架与依赖

- [x] 1.1 建立 `apitest/` 目录结构：`cases/`、`lib/`、`run_all.py`、`requirements.txt`、`README.md`。验证：目录与文件齐全，`README.md` 说明运行方式。
- [x] 1.2 写 `requirements.txt`（`requests`、`PyMySQL` 固定版本）并在 `run_all.py` 中做依赖自检，缺失时打印安装命令并非零退出。验证：用未安装依赖的解释器运行，输出安装提示且退出码为 2（实施中发现原先在自检前就导入公共库，会直接抛 `ImportError`，已改为自检通过后再导入）。
- [x] 1.3 `.gitignore` 增加 `apitest/report/` 与 `apitest/.venv/`；`package.json` 增加 `test:api`。验证：`git check-ignore -v` 确认报告与虚拟环境都被忽略，`git status` 不显示它们。

## 2. 运行器与报告

- [x] 2.1 实现用例发现：遍历 `cases/**/<METHOD>.py`，把目录层级还原为 URL（保留 `{name}` 占位符），读取同目录 `case.json` 的 `path_params`、`requires` 与 `order`。验证：新增临时脚本 `cases/api/tmp-verify-check/GET.py` 无需改运行器即被执行（用例数 1），验证后删除。
- [x] 2.2 实现执行与上下文：按 `order` 顺序执行，解析 `$变量` 引用，前置未满足时报告为失败而不是跳过；支持 `python cases/.../POST.py` 单例运行。验证：单脚本直接运行只跑该用例（1/1 通过）；缺前置时报告 `[FAIL] 前置未满足：session_id` 且退出码非 0。
- [x] 2.3 实现 HTML 报告：单文件、内联 CSS、无外链，含用例路径、方法、URL、状态码、耗时、断言明细与失败差异；写入 `apitest/report/index.html`。验证：报告含全部用例，`selfcheck.py` 断言无 `<link>`／`<script src>`（断网可渲染），并有 HTML 转义。
- [x] 2.4 退出码语义：全部通过为 0，任一失败为非 0。验证：故意失败的用例使退出码为 1，报告出现「失败」标记与「期望 1，实际 2」差异。

## 3. 公共库

- [x] 3.1 `lib/client.py`：`Api`（Cookie 会话、可切 Bearer、非 2xx 抛带状态码与响应体的异常）与 `stream()`（逐事件解析 SSE）。验证：`selfcheck.py` 覆盖 200、401、403 与流式读取（含心跳与 UTF-8 解码）。
- [x] 3.2 `lib/db.py`：探测数据库访问模式（Compose 未发布端口时走 `docker compose exec -T db mysql`，否则直连），提供 `query_one`/`query_all`/`scalar`，连接参数取自 `.env`。验证：两种模式各跑一次，返回结果一致——`direct`（dev:local，127.0.0.1:55432）8/8、6.6 秒；`compose-exec`（Compose 部署，localhost:8080）8/8、15.6 秒。**该验证发现并修掉了真实缺陷：容器内 `mysql` 的批处理输出是文本且转义换行，导致数字列变成字符串、正文比对失败；现在会还原转义并把纯数字转回数值。**
- [x] 3.3 `lib/fixtures.py`：创建／清理临时教师、学生、班级、讲义、提示词与会话，后缀唯一化。验证：连续运行两次结果一致（8/8），业务表零残留（users/classrooms/handouts/knowledge_documents/knowledge_chunks/chat_sessions/chat_messages/prompts 行数不变）。
- [x] 3.4 `lib/report.py`：结果渲染与差异格式化（期望/实际对照）。验证：`selfcheck.py` 用固定结果集渲染，断言单文件、转义与差异文本。

## 4. 用例

- [x] 4.1 `cases/api/auth/login/POST.py`：正确凭证登录成功、错误凭证 401；断言 Cookie 已下发，且数据库 `sessions` 新增一行（哈希非明文）。验证：单独运行与整体运行都通过。
- [x] 4.2 `cases/api/auth/token/POST.py`：换取令牌成功、错误凭证 401；断言响应不含 `Set-Cookie`、数据库中只存哈希。验证：同上（另断言错误口令与未知账号提示一致）。
- [x] 4.3 检索用例 `cases/api/classes/{id}/knowledge/search/POST.py`：三种模式各一次，断言返回结构、命中来源属于本班、无依据时返回固定文案与空命中。验证：断言含数据库侧核对（切片属于该班讲义且代次为已发布）。向量模式用讲义原句提问（替身不是语义模型，改写问法不保证过阈值），实测相似度 0.7075。
- [x] 4.4 提示词／技能用例 `cases/api/classes/{id}/prompts/GET.py` 与 `.../PUT.py`：读取生效内容、保存班级覆盖、启用与停用「解题引导」。验证：断言数据库 `prompts` 行的 `scope/kind/name/enabled` 与响应一致；学生身份调用返回 403。**该用例发现产品缺陷：班级自建技能只改启用状态会被 400 拒绝，已修复并补回归测试。**
- [x] 4.5 会话用例 `cases/api/classes/{id}/sessions/POST.py`：新建会话并断言数据库行；`.../{sid}/messages/POST.py`：发送一条消息并读取流，断言至少两个增量事件、结束事件、出处列表，并核对用户消息与助手消息已落库（顺序与状态正确）。验证：流式断言通过且数据库行数与内容一致（落库内容与流式增量逐字相同）。
- [x] 4.6 无依据提问用例：断言固定文案、空出处与助手消息落库，且不产生额外调用。验证：通过仓库自带替身的 `/stats` 计数从外部观测——报告中「无依据时不调用对话模型 — 期望 0，实际 0」；真实网关环境下该断言改为注明由后端测试覆盖。

## 5. 环境、清理与幂等

- [x] 5.1 用例全部使用临时数据并实现清理，运行后数据库回到基线。验证：连续运行两次，两次都是 8/8，业务表行数不变（`sessions` 每次 +4 属预期，来自登录与令牌签发本身）。
- [x] 5.2 失败时的清理：即使断言失败也要执行 `teardown`。验证：故意失败的用例运行后无残留账号与班级（基线行数不变）。
- [x] 5.3 报告头部记录运行环境（后端地址、数据库访问模式、用例数、耗时）。验证：报告中可见这些字段（另含管理员口令来源）。

## 6. 文档与验收

- [x] 6.1 `apitest/README.md` 与根 `README.md` 说明安装、运行、报告位置与与既有测试的分工。验证：按文档从零执行一次可复现（本轮即按文档创建虚拟环境、安装依赖并运行）。
- [x] 6.2 `docs/verification.md` 记录首次完整运行：用例数、通过数、报告路径与耗时。验证：条目对应一条可复现命令（`npm run test:api`，8/8，6.1 秒）。
- [x] 6.3 全量检查：`npm run test:api`、`npm run test:e2e`、`go vet`、`go test`、前端构建、`openspec validate --all --strict`。验证：逐条通过（8/8；73 项；vet 0；`ok 96.5s`；构建成功；14/14）。
