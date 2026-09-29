## Context
现有后端先检查白名单，再检查 PDF/Office 签名与现代 Office ZIP 内容。前端 accept 与提示也只列出 PDF/Office。文件存储与授权下载可直接复用。

## Goals / Non-Goals
目标：支持 TXT、MD，后端真实校验，部署后可用，无数据库迁移。
非目标：Markdown 预览、GBK/UTF-16 转码、AI 解析、放开任意文件。

## Decisions
- .txt 使用 text/plain; charset=utf-8，.md 使用 text/markdown; charset=utf-8。MIME 由后端扩展名映射生成，不信任客户端声明。
- 使用缓冲读取 Unicode 字符，拒绝非法 UTF-8 和 unicode 控制字符（允许 TAB/CR/LF）；可接受文件开头 BOM，但单独 BOM 不算有效内容。不扫描 HTML/Markdown 语法，因为只按附件提供。
- 保持下载附件响应、nosniff 及沙盒策略；preview=1 仍仅对 PDF 生效。
- 保持原始字节，不做隐式转码，避免中文乱码和修改内容。对文本校验失败提供明确中文错误。
- 先完成规约，再实施；使用 Go 文本边界测试、真实数据库 HTTP 测试及 Docker 浏览器上传下载验收，保留既有格式回归。

## Risks / Trade-offs
旧 Windows 软件保存的 GBK 或 UTF-16 文本须先另存为 UTF-8。内容校验仅确认文本性质，无法证明 .md 包含特定 Markdown 语法；纯文本本身也是有效 Markdown。

## Migration Plan
无需表结构迁移。构建并替换 api/web 容器，保持数据库和上传卷；验证后更新任务记录。
