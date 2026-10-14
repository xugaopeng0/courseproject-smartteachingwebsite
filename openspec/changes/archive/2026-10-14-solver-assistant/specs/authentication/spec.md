# Spec Delta

## MODIFIED Requirements

### Requirement: Account and role authentication
系统 SHALL 支持管理员、教师和学生角色，使用密码哈希与服务端会话；此外 SHALL 支持用账号密码换取不透明 Bearer 令牌，供接口工具与文档页面调用需要登录的接口。

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
- **WHEN** 修改数据的请求携带非平台 Origin，且以 Cookie 身份发起
- **THEN** 系统拒绝请求

#### Scenario: Token issuance and use
- **WHEN** 客户端用正确账号密码请求签发令牌，并随后带 `Authorization: Bearer <token>` 调用需要登录的接口
- **THEN** 签发返回令牌与过期时间，后续请求通过鉴权；只带令牌的请求不做同源校验

#### Scenario: Token revocation
- **WHEN** 令牌已过期、已被登出撤销，或持令牌用户已失去对应班级的成员身份
- **THEN** 请求返回未授权，且不返回任何业务数据

#### Scenario: Token is not stored in the browser
- **WHEN** 网页使用浏览器会话访问接口
- **THEN** 令牌仅由调用方自行保存，服务端 MUST NOT 把令牌写入 Cookie、页面或前端产物
