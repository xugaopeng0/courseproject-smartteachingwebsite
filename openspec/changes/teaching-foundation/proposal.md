## Why
建立智能教学平台的首期基础能力，让教师组织班级、共享讲义，让学生安全访问学习资料。使用白色为主、红色为辅的中文界面，以 OpenSpec 规约驱动开发。

## What Changes
- 实现账号密码登录、学生注册、管理员创建教师账号、退出登录。
- 实现教师创建和编辑班级、邀请码入班、成员管理、学生退出和班级归档。
- 实现 PDF、Word、PPT 讲义上传、班级内下载、PDF 预览和删除。
- 提供 React + TypeScript、Go、PostgreSQL 及 Docker Compose 本地部署。
- 本期不接入 AI；作业、大模型、智能体、知识库、技能与 MCP 保留后续扩展边界。

## Capabilities
### New Capabilities
- `authentication`: 用户身份、会话和角色控制。
- `classrooms`: 班级生命周期及成员权限。
- `handouts`: 班级讲义上传和授权访问。
- `deployment`: 可复现的本机容器部署。

### Modified Capabilities
无。

## Impact
新增前后端项目、数据库结构、私有文件存储、部署文档和自动化验证脚本。没有已有系统迁移要求。
