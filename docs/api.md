# API 摘要

所有路由以 `/api` 开头。JSON 错误格式为 `{ "error": "可读的中文错误" }`。认证使用同源 HttpOnly cookie，不使用 localStorage token。

| 方法 | 路径 | 用途 / 权限 |
| --- | --- | --- |
| GET | `/health` | 数据库连接健康检查 |
| POST | `/auth/register` | 学生注册：username、name、password |
| POST | `/auth/login` | 登录：username、password |
| GET | `/auth/me` | 当前用户 |
| POST | `/auth/logout` | 撤销会话 |
| GET / POST | `/admin/teachers` | 管理员列出/创建教师 |
| GET / POST | `/classes` | 当前用户班级列表 / 教师创建班级：name、description |
| POST | `/classes/join` | 学生邀请码入班：code |
| GET / PATCH | `/classes/{id}` | 成员读取详情 / 所属教师编辑 name、description |
| POST | `/classes/{id}/archive` | 所属教师归档/恢复：archived |
| DELETE | `/classes/{id}/members/{userID}` | 所属教师移除成员 / 学生退出自己 |
| POST | `/classes/{id}/handouts` | 所属教师上传：multipart file、title（可选） |
| GET | `/handouts/{id}/file` | 所属教师/成员下载；`?preview=1` 预览 PDF |
| DELETE | `/handouts/{id}` | 所属教师删除活动班级内讲义 |

账号为 3–32 位字母、数字或下划线（统一小写），姓名为 1–40 字，密码为 8–72 字节。班级名最多 80 字，简介最多 1000 字，讲义标题最多 120 字。

讲义格式：PDF、DOC、DOCX、PPT、PPTX、TXT、MD，大小上限 50 MiB。TXT/MD 只接受非空 UTF-8 文本，可带 BOM；不接受无效编码、仅 BOM 或二进制控制字符（TAB、CR、LF 除外）。后端决定响应 MIME，不信任上传声明的 Content-Type；TXT/MD 即使请求 `preview=1` 也保持附件下载，不直接渲染 HTML/Markdown。

主要状态码：400 输入无效；401 未登录或会话过期；403 无权访问；404 资源不存在；409 重复/状态冲突；413 文件过大；429 登录或注册频率超限。
