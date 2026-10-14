# Proposal

## Why

第 5 课要求把接口测试沉淀到仓库里：一个接口一个脚本、按 URL 组织目录、`run_all.py` 一键跑完并生成不提交的 HTML 报告，而且断言不能只看状态码，还要核对返回内容与数据库是否一致。现有 `scripts/integration.mjs`（Node，73 项真实 HTTP 检查）覆盖面够但不满足这套交付形态：它不是按接口拆分的脚本，没有 HTML 报告，也不直接核对数据库行。

## What Changes

- **新增 `apitest/`**：`cases/` 下按 URL 组织，一个 HTTP 操作一个 Python 脚本（`cases/api/login/POST.py` 对应 `POST /api/login`）；`lib/` 放公共客户端、数据库核对与报告生成；`run_all.py` 一键运行。
- **一键运行与报告**：`run_all.py` 自动发现用例、顺序执行、生成自包含 HTML 报告到 `apitest/report/`（忽略提交），并以退出码反映结果。
- **断言深度**：每个用例同时断言状态码、响应内容与数据库状态（会话行、讲义行、消息行等），不一致即失败。
- **覆盖范围**：至少覆盖登录、知识库检索、提示词／技能读写、创建会话并发送一条消息（含流式回答与出处落库）。
- **环境与清理**：复用 `.env` 的连接与管理员配置，用独立临时数据并在结束后清理，连续运行结果一致。
- 更新 `README.md`、`docs/verification.md` 与 `.gitignore`。

## Capabilities

### New Capabilities
- `api-test-suite`: 接口测试用例的组织方式、运行入口与报告产出、断言深度要求、最低覆盖范围，以及环境与清理约束。

### Modified Capabilities
（无：本变更不改变任何对外行为。）

## Impact

- **仓库**：新增 `apitest/` 目录（用例、公共库、依赖清单）；`.gitignore` 增加报告目录与虚拟环境。
- **依赖**：Python 3 + `requests` + `PyMySQL`（本机已装 Python 3.13，两个库需通过 `apitest/requirements.txt` 安装到虚拟环境）。
- **脚本**：`package.json` 增加 `test:api` 入口，便于与既有 `test:e2e` 并列执行。
- **文档**：`README.md` 说明安装与运行方式；`docs/verification.md` 记录首次运行的用例数与报告路径。
- **与既有测试的分工**：`scripts/integration.mjs` 继续承担部署级回归（角色、会话、上传、重启持久化）；`apitest` 承担按接口拆分的契约与数据库一致性检查，两者都保留。
