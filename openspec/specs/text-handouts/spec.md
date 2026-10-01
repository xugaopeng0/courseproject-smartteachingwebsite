# text-handouts Specification

## Purpose
扩展班级私有讲义共享，使教师可上传纯文本和 Markdown 教学资料，学生可在授权范围内下载原始文件；同时通过编码和文本内容校验拒绝无效文件，保持已有文件类型、班级权限和大小限制的行为。

## Requirements

### Requirement: Text handout upload and download
系统 SHALL 在现有讲义流程中支持 .txt 和 .md（扩展名大小写不敏感），接受非空 UTF-8 文本及可选 BOM，保留原始字节，单文件最多 50 MiB。
#### Scenario: Valid text upload
- **WHEN** 所属教师向活动班级上传中文 UTF-8 TXT 或 MD 文件
- **THEN** 上传成功，班级成员可下载与原文件字节一致的讲义
#### Scenario: Invalid text content
- **WHEN** 文件为空、只有 BOM、UTF-8 无效，或包含除制表符、回车、换行以外的控制字符
- **THEN** 后端拒绝上传，不保留文件或讲义记录，并提示保存为非空 UTF-8 文本
#### Scenario: Private attachment download
- **WHEN** 成员请求 TXT 或 MD 文件，包括请求 preview=1
- **THEN** 仍以附件下载，使用正确文本 MIME 与 UTF-8 编码，不渲染其中的 HTML 或 Markdown
#### Scenario: Existing restrictions
- **WHEN** 学生上传、非成员下载、向归档班级上传或文件超过上限
- **THEN** 按现有权限和大小限制拒绝请求
#### Scenario: File selection
- **WHEN** 教师打开上传弹窗
- **THEN** 选择器允许 TXT、MD，格式提示明确标识 UTF-8 文本要求，同时保留 PDF、Word、PPT
