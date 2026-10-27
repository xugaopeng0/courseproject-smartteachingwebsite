# 接口测试套件（apitest）

一个接口一个脚本，目录层级就是 URL，文件名就是 HTTP 方法：`cases/api/login/POST.py` 对应 `POST /api/login`。
运行器自动发现用例，因此**新增接口只需新增文件**，不必改公共代码。

## 安装与运行

```powershell
# 1) 依赖（只装一次）
python -m venv apitest/.venv
apitest/.venv/Scripts/pip install -r apitest/requirements.txt      # Windows
# apitest/.venv/bin/pip install -r apitest/requirements.txt        # Linux / macOS

# 2) 运行全部用例并生成 HTML 报告
apitest/.venv/Scripts/python apitest/run_all.py
npm run test:api                                                   # 等价入口

# 常用参数
apitest/.venv/Scripts/python apitest/run_all.py --filter sessions   # 只跑路径含 sessions 的用例
apitest/.venv/Scripts/python apitest/run_all.py --base-url http://127.0.0.1:8081
apitest/.venv/Scripts/python apitest/run_all.py --no-report
```

报告写在 `apitest/report/index.html`（自包含 HTML，不依赖外网；目录已忽略提交）。

被测地址按 `--base-url` → `APITEST_BASE_URL` → 仓库 `.env` 的 `APP_ORIGIN` → `http://127.0.0.1:8081` 依次解析；
管理员口令按 `APITEST_ADMIN_PASSWORD` → `.env` 的 `ADMIN_PASSWORD` → `.local/dev-credentials.json` 依次**探测**
（探测用的登录请求若被限流，会明确提示 429 而不是笼统地说口令缺失）。

> 登录、注册与签发令牌合计有「同一 IP 10 分钟内 40 次」的产品级限流。一套用例约消耗 6 次，
> 因此连续跑七八次后会返回 429；等待窗口结束，或重启本地 API（计数在进程内存里）即可继续。

## 公共库自检

```powershell
apitest/.venv/Scripts/python apitest/selfcheck.py
```

不依赖业务用例，单独验证三个助手本身：`Api` 的 200/401/403 与 SSE 逐事件解析（含心跳与 UTF-8 解码）、
`Database` 的查询与 compose-exec 模式的 SQL 字面量转义、`report` 的渲染与 HTML 转义。

## 数据库核对

用例不只看状态码：登录后核对 `sessions` 行、检索后核对切片属于本班、提示词写入后核对 `prompts` 行、
发消息后核对 `chat_messages` 的顺序号与状态。核对方式**跟着被测服务走**，并在报告头部注明：

- 被测服务是 `dev:local`（`127.0.0.1:8081`）→ **direct**：直连宿主机 55432 上的便携 MySQL；
- 被测服务是 Compose 部署（`localhost:8080`，web 发布的端口）→ **compose-exec**：Compose 有意不发布 db 端口，
  因此改用容器内的 `mysql` 客户端执行同一条 SQL。

判定顺序：`APITEST_DB_HOST` → `APITEST_DB_MODE`（`direct` / `compose-exec`）→ 自动（比较被测端口与
`docker compose port web 80`，再按 3306／55432 探测，最后回退 compose-exec）。
显式参数还包括 `APITEST_DB_PORT`、`APITEST_DB_USER`、`APITEST_DB_PASSWORD`、`APITEST_DB_NAME`。

## 用例写法

```python
"""POST /api/auth/login — 说明这个用例核对什么。"""

def run(ctx):
    status, body = ctx.api.request("POST", "/api/auth/login", body={...}, expect=200)
    ctx.check("登录返回 200", status == 200)
    ctx.equal("角色正确", body["role"], "admin")
    rows = ctx.db.scalar("SELECT COUNT(*) FROM sessions WHERE user_id=%s", (body["id"],))
    ctx.check("会话已落库", (rows or 0) >= 1)
    return status
```

- `ctx.api`：HTTP 客户端（Cookie 会话、`token=` 走 Bearer、`stream()` 读 SSE、`upload()` 上传）。
- `ctx.db`：数据库核对（`query` / `query_all` / `query_one` / `scalar`，占位符用 `%s`）。
- `ctx.fixtures`：临时教师／班级／讲义／学生，运行结束统一清理；`gateway_stats()` 读取开发替身的调用计数。
- `ctx.ensure_classroom()`：按需准备「教师 + 班级 + 已索引讲义」并缓存，供班级范围的用例复用。
- 脚本也可单独运行：`apitest/.venv/Scripts/python apitest/cases/api/auth/login/POST.py`
  （与整体运行完全等价；用例文件末尾的 `__main__` 块负责补好导入路径）。

同目录的 `case.json` 声明顺序与路径参数：

```json
{ "order": 40, "path_params": { "id": "$class_id" }, "requires": ["class_id"] }
```

前置未满足时该用例记为失败（不会静默跳过）。

## 与其它测试的分工

| 命令 | 覆盖 |
| --- | --- |
| `npm run test:api` | 按接口拆分的契约与数据库一致性（本目录） |
| `npm run test:e2e` | 部署级回归：角色、会话、上传、重启持久化（73 项，Node） |
| `npm run test:knowledge` | 经课程网关的中文召回与问答验收（需真实网关） |
| `npm run test:db` | 后端数据库层单元测试（便携 MySQL + Qdrant） |

## 第 6 课新增用例

`cases/api/tool-servers/`（登记、连接、断开、删除）、`cases/api/tool-calls/`（记录列表、默认上限、结果筛选、学生 403）、
`cases/api/tool-flow/`（端到端：连接 → 绑定 `formula_hint@…` 技能 → 会话提问 → 记录出现成功）。

端到端用例要求环境里有一台可达的 MCP 工具服务：Compose 部署下即 `http://mcp-demo:8090`（用例登记的就是这个地址）。
`dev:local` 下 API 在宿主机运行、访问不到容器内网的 `mcp-demo`，因此这组用例请对着 Compose 部署运行：

    $env:APITEST_BASE_URL = "http://localhost:8080"; npm run test:api
