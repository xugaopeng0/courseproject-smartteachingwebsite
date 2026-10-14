# Design

## Context

- 现有测试：`scripts/integration.mjs`（Node，73 项真实 HTTP 检查，自建 MySQL 与后端进程）、`scripts/knowledge-test.mjs`（经课程网关的检索与问答验收）、`scripts/deployment-test.mjs`（容器与浏览器）。它们覆盖部署级行为，但不是「一接口一脚本」的形态。
- 本机 Python 3.13.1 与 pip 25.0 可用，`requests` 与 `PyMySQL` 未安装。
- `.env` 已包含 `DB_PASSWORD`、`ADMIN_USERNAME`、`ADMIN_PASSWORD`、`APP_ORIGIN`；Compose 部署把 web 发布在 `localhost:8080`。
- 会话与令牌：Cookie 名 `teaching_session`；`solver-assistant` 变更将提供 `POST /api/auth/token`。
- 流式接口：`POST /api/classes/{id}/sessions/{sid}/messages` 返回 `text/event-stream`。

## Goals / Non-Goals

**Goals:**

- 用例结构由文件路径自解释，新增接口只加文件。
- 每条用例都能独立运行，便于定位失败。
- 报告一页看清通过/失败与差异，且不进版本控制。
- 断言穿透到数据库，避免「接口返回 200 就算过」。

**Non-Goals:**

- 不替代既有 Node 集成测试与浏览器回归。
- 不做性能压测、并发压测与混沌测试。
- 不引入 pytest 等额外框架（保持「脚本 + 运行器」的最小形态，便于按课程要求逐个阅读）。

## Decisions

### 1. 用例发现规则

`cases/` 下的目录层级即 URL 路径，文件名即方法：`cases/api/classes/{id}/knowledge/search/POST.py` ↔ `POST /api/classes/{id}/knowledge/search`。运行器把 `{name}` 段保留为占位符，JSON 配置放在同目录的 `case.json`：

```json
{
  "path_params": { "id": "$class_a" },
  "requires": ["login"],
  "order": 30
}
```

上下文变量（`$class_a` 等）由前置用例或夹具写入，运行器在调用前解析；缺少变量即报告为「前置未满足」而不是静默跳过。备选「用装饰器注册」被否：那样新增接口要改公共代码，违背「一接口一脚本」的直觉。

### 2. 脚本协议

每个脚本导出 `run(ctx)` 与可选 `setup(ctx)` / `teardown(ctx)`，返回断言结果列表；运行器负责捕获异常、计时与报告。脚本内只使用 `lib/` 提供的客户端与数据库助手，不直接拼接连接串。

脚本既可被运行器导入执行，也可用 `python cases/api/login/POST.py` 直接运行（文件底部 `if __name__ == "__main__"` 调用运行器的单例模式）。

### 3. 公共库

- `lib/client.py`：`Api` 封装 `requests.Session`，自动带 Cookie、可切换 `Authorization: Bearer`、统一把非 2xx 转成带状态码与响应体的异常；另提供 `stream()` 返回逐事件的生成器。
- `lib/db.py`：用 `PyMySQL` 直连 MySQL，提供 `query_one` / `query_all` / `scalar`，连接参数来自 `.env`（`DB_PASSWORD`、容器端口 3306 经宿主映射不可用，因此默认连 `127.0.0.1:3306`；若部署未映射端口，则退回「用 `docker compose exec db` 执行 SQL」的适配器）。**注意**：当前 Compose 未把 `db` 端口发布到宿主，因此 `db.py` 默认实现走 `docker compose exec -T db mysql`，并在文档中说明；这样无需改动部署形态。
- `lib/fixtures.py`：创建与清理临时教师、学生、班级、讲义、提示词与会话；用 `randomBytes` 风格的后缀保证唯一。
- `lib/report.py`：把结果渲染成单文件 HTML（内联 CSS、无外链），写入 `apitest/report/index.html`。

### 4. 报告与忽略

报告目录 `apitest/report/` 与虚拟环境 `apitest/.venv/` 写入 `.gitignore`。报告含用例路径、方法、URL、状态码、耗时、断言明细与失败差异；不包含密码、令牌与讲义正文。

### 5. 依赖与运行

`apitest/requirements.txt` 固定 `requests` 与 `PyMySQL`。`run_all.py` 启动时检查依赖，缺失时打印：

```
python -m venv apitest/.venv
apitest/.venv/Scripts/pip install -r apitest/requirements.txt
```

并以非零退出码结束。`package.json` 增加 `test:api` → `python apitest/run_all.py`。

### 6. 与既有测试的分工

`test:e2e`（Node）继续作为部署级回归；`test:api`（Python）作为按接口拆分的契约与数据库一致性套件；`test:knowledge` 仍负责真实模型下的召回与问答验收。三者在 `README.md` 的验收一节并列说明，避免读者误以为互相替代。

### 7. 数据库核对的可行路径

Compose 中 `db` 未发布宿主端口（有意为之：数据服务只在内网）。因此数据库核对默认通过 `docker compose exec -T db mysql` 执行 SQL 并解析输出，而不是直连；若使用 `dev:local`（宿主 MySQL 在 55432），则直连。`db.py` 用一次探测决定模式，并把选择打印到报告头部，保证结果可解释。

## Risks / Trade-offs

- **依赖安装门槛**：需要 Python 环境与两个库；通过 requirements 与明确的缺失提示降低门槛，`run_all.py` 不自动联网安装。
- **通过 CLI 执行 SQL 的开销**：每个断言一次进程调用，比直连慢；用例数量有限（十余条），可接受；必要时改为一次查询批量断言。
- **流式断言的稳定性**：断言的是一组结构性事实（事件种类齐全、增量条数 ≥ 2、结束事件存在、落库一致），而不是逐字节比对，避免模型不同导致的抖动。
- **与 Node 套件重复**：两者覆盖有重叠，但分工与理由在文档中写明；重叠带来的维护成本通过「apitest 聚焦契约与数据库、Node 聚焦部署行为」控制。
