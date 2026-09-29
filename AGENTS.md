# 项目约定

- 技术栈：React + TypeScript + Go + PostgreSQL，Docker Compose 本机部署。
- 界面与面向用户的文档使用中文，视觉以白色为主、红色为辅。
- 遵循 SDD：功能变更先维护 OpenSpec proposal、specs、design、tasks，再实施和验证；不要在规约缺失时直接扩展产品范围。
- 当前首期变更 `teaching-foundation` 已完成 12/12 项，包含容器运行与持久化验收；保持未归档，验证证据见 `docs/verification.md`。
- 后端以服务端权限判断为准。新功能不得仅靠隐藏按钮限制角色；班级文件不得通过公共静态路径提供。
- 不提交 `.env`、`.local`、`.tools`、数据库、上传文件或凭证。
- 修改相关行为后运行有意义的检查：Go test/vet、前端构建、集成测试及必要的浏览器回归；不要声称未执行的 Docker 验收已通过。
- 后续 AI、作业等功能按独立 OpenSpec 变更开发，当前未实现的能力不在 UI 中伪装为可用入口。
