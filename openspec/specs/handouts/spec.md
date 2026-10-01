# handouts Specification

## Purpose
规定班级讲义的私有存放与访问边界：只有所属教师能上传与删除，格式、大小与签名都要校验，文件以随机名保存且不通过公共静态路径提供，下载与预览每次都要重新确认班级成员身份。

## Requirements

### Requirement: Private classroom handouts
系统 SHALL 允许所属教师向活动班级上传不超过 50 MiB 的 PDF、Word 和 PowerPoint 文件。
#### Scenario: Valid upload
- **WHEN** 所属教师上传非空、扩展名受支持且格式签名匹配的文档（现代 Office 还检查 ZIP 内部类型）
- **THEN** 随机命名的私有文件与元数据被持久化，并展示在班级中
#### Scenario: Invalid upload
- **WHEN** 文件超限、格式不受支持或签名无效
- **THEN** 拒绝上传，不新增讲义记录
#### Scenario: Download and preview
- **WHEN** 所属教师或已入班学生请求讲义
- **THEN** 下载使用原始文件名，PDF 支持内联预览
#### Scenario: Unauthorized access
- **WHEN** 非成员访问讲义，或学生尝试上传与删除讲义
- **THEN** 请求被拒绝
#### Scenario: Delete
- **WHEN** 所属教师删除活动班级的讲义
- **THEN** 元数据被移除，文件不再可访问
