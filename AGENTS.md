# 项目约定

- 技术栈：React + TypeScript + Go + MySQL 8.4 + Qdrant，Docker Compose 本机部署。
- 嵌入与对话只由服务端调用课程网关（兼容 OpenAI 接口），网关地址、模型名与密钥不下发浏览器；浏览器只访问本站 `/api`。
- 界面与面向用户的文档使用中文，视觉以白色为主、红色为辅。
- 遵循 SDD：功能变更先维护 OpenSpec proposal、specs、design、tasks，再实施和验证；不要在规约缺失时直接扩展产品范围。
- 变更已全部归档在 `openspec/changes/archive/`（含 `mysql-qdrant-platform`、`knowledge-retrieval-modes`、`knowledge-answer-citations`），主规约位于 `openspec/specs/`：11 份能力规约、`openspec validate --all --strict` 全通过；验证证据见 `docs/verification.md`。
- 后端以服务端权限判断为准。新功能不得仅靠隐藏按钮限制角色；班级文件不得通过公共静态路径提供。
- 切片正文、权限与索引元数据以 MySQL 为权威，向量只存 Qdrant 且仅保存标识类 payload；检索回表时必须重新校验班级与已发布代次。
- 不提交 `.env`、`.local`、`.tools`、数据库、上传文件或凭证；课程提供的种子材料放在 `seeds/`，由 API 启动时幂等导入示例班级。
- 修改相关行为后运行有意义的检查：Go test/vet、前端构建、集成测试及必要的浏览器回归；不要声称未执行的 Docker 验收已通过。
- 后续 AI、作业等功能按独立 OpenSpec 变更开发，当前未实现的能力不在 UI 中伪装为可用入口。
