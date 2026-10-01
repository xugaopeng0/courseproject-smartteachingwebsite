## Why
现有讲义白名单仅支持 PDF、Word、PowerPoint，教师无法上传纯文本和 Markdown 讲义。用户明确要求增加 TXT、MD，并保留后端文件类型校验。

## What Changes
- 增加 .txt、.md 讲义上传和授权下载，维持 50 MiB 上限。
- 后端流式检查 UTF-8 文本，支持中文和 BOM，拒绝空内容、无效编码与非文本控制字符。
- 更新文件选择器、格式提示、测试和文档，并重新部署 Docker。
- 不提供 Markdown HTML 渲染，不增加 AI 解析。

## Capabilities
### New Capabilities
- `text-handouts`: TXT/MD 讲义的编码校验、上传与下载。
### Modified Capabilities
无主规约修改；首期 handouts 变更尚未同步到主规约，本变更以独立补充能力扩展格式白名单。

## Impact
涉及 backend/handouts.go、讲义上传 UI、后端测试、集成测试与使用文档。沿用原表结构与文件卷，无需数据库迁移，既有格式保持支持。
