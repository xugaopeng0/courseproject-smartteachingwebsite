# Proposal

## Why

第 5 课要求提供机器可读的接口描述，并能在浏览器打开文档页面、对照文档试调主要接口。当前只有 `docs/api.md` 这份散文式说明：没有可被工具消费的契约，也没有试调入口，接口一旦改动只能靠人工比对，无法自动发现文档与实现的漂移。

## What Changes

- **单一契约来源**：在 `docs/openapi.json` 维护 OpenAPI 3.1 描述，覆盖全部 `/api/*` 操作：方法、路径与查询参数、请求体字段与约束、成功与错误响应、错误码含义、是否需要登录以及所需角色。
- **对应用提供**：`GET /api/openapi.json` 返回该描述（随二进制嵌入，不依赖构建期挂载），`GET /api/docs` 提供可直接打开的接口文档页面。
- **文档页面自托管**：页面静态资源随 `web` 镜像发布，不使用外网 CDN；页面支持填入 Bearer 令牌后调用受保护接口。
- **一致性检查**：把路由注册整理成可枚举的表，新增自动化检查，双向比对「实现有而文档缺」与「文档有而未实现」，并校验受保护操作是否标注了鉴权；检查纳入常规验收命令。
- 更新 `docs/api.md`、`README.md`、`AGENTS.md`，并在 `docs/verification.md` 记录实测。

## Capabilities

### New Capabilities
- `api-documentation`: 接口契约的内容与覆盖要求、文档的获取方式与试调能力、以及文档与实现一致性的自动化保障。

### Modified Capabilities
（无：本变更不改变任何对外行为，只增加契约描述与文档入口。）

## Impact

- **后端**：新增嵌入静态契约与两个只读端点；路由注册改为遍历可枚举的路由表（注册与一致性检查共用）。
- **前端**：`swagger-ui-dist` 作为依赖，构建时随 `dist` 发布到 `/api/docs`；`nginx.conf` 增加对应 location。
- **仓库**：新增 `docs/openapi.json`；`.gitignore` 无需改动（无生成物）。
- **测试**：新增一致性单元测试；`apitest` 可用文档里的示例请求交叉验证。
- **文档**：`docs/api.md` 与 `README.md` 说明如何打开文档页面与获取令牌。
