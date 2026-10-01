# classrooms Specification

## Purpose
规定班级的归属与成员关系：教师拥有班级并通过唯一邀请码招生，学生凭邀请码入班或主动退出，归档后保留读取但停止写入；所有班级与成员信息都按所属教师与在班学生做服务端权限判断。

## Requirements

### Requirement: Classroom lifecycle and membership
系统 SHALL 对班级执行所属教师与已入班学生的权限检查。
#### Scenario: Create and edit
- **WHEN** 教师创建或编辑自己的班级
- **THEN** 名称与简介被保存，所属教师可获取唯一邀请码
#### Scenario: Join
- **WHEN** 学生提交活动班级的邀请码
- **THEN** 学生加入班级，重复加入被拒绝
#### Scenario: Members and leaving
- **WHEN** 所属教师移除学生，或学生主动退出班级
- **THEN** 成员关系被删除，后续班级文件访问被拒绝
#### Scenario: Archive
- **WHEN** 所属教师归档班级
- **THEN** 禁止加入、编辑、上传、删除讲义与教师移除成员；现有成员保留读取权限且可主动退出；所属教师可以恢复班级
#### Scenario: Unauthorized access
- **WHEN** 其他教师或未入班学生请求班级信息
- **THEN** 班级和成员信息访问被拒绝
