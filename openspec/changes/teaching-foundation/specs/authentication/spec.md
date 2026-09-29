## ADDED Requirements
### Requirement: Account and role authentication
系统 SHALL 支持管理员、教师和学生角色，使用密码哈希与服务端会话。
#### Scenario: Student registration
- **WHEN** 访客提交唯一账号、姓名和 8–72 字节的密码
- **THEN** 系统只能创建学生账号并建立会话
#### Scenario: Teacher provisioning
- **WHEN** 已登录管理员提交教师信息
- **THEN** 系统创建教师账号，非管理员请求被拒绝
#### Scenario: Login and logout
- **WHEN** 用户用正确凭证登录，之后退出登录
- **THEN** 系统签发 HttpOnly cookie，并在退出时撤销服务端会话
#### Scenario: Invalid authentication
- **WHEN** 凭证错误或会话过期
- **THEN** 系统拒绝访问，且不暴露密码哈希
#### Scenario: Cross-site mutation
- **WHEN** 修改数据的请求携带非平台 Origin
- **THEN** 系统拒绝请求
