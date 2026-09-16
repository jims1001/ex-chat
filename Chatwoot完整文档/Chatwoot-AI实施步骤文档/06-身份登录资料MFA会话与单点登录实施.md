# 身份、登录、资料、MFA、会话与单点登录实施

> 批次：AUTH，共 16 项。基线来源：[13](../Chatwoot产品与API文档/13-身份资料安全与登录会话API.md)、[21](../Chatwoot产品与API文档/21-角色权限与接口可用性矩阵.md)、[47](../Chatwoot产品与API文档/47-webchat开放平台安全审计与企业能力补齐规范.md)。

## 1. 目标

统一登录主体、认证信息、个人资料、安全验证、设备会话、OAuth 和 SAML，使身份验证与 Account 权限分离，又能在每次业务请求中正确组合。

## 2. 任务清单

| 编号 | 功能 | 逐步实施 | API 与字段 | 完成判定 |
|---|---|---|---|---|
| AUTH-01 | 普通登录 | 校验邮箱密码、账号状态和锁定策略；签发认证信息；记录设备 | `/auth/sign_in`；email、password | 成功、错误、锁定和未确认状态明确 |
| AUTH-02 | Token 验证 | 验证认证头组合、到期、撤销和 User 状态 | `/auth/validate_token` | 无效 Token 不能访问任何受保护资源 |
| AUTH-03 | 刷新与轮换 | 定义 Token 轮换、并发请求和旧 Token 失效窗口 | access-token、client、uid | 不产生无限有效旧 Token |
| AUTH-04 | 退出 | 撤销当前会话并清理当前设备订阅关联 | `/auth/sign_out` | 退出后当前 Token 立即失效 |
| AUTH-05 | 密码重置 | 请求重置、验证令牌、设置新密码并撤销旧会话 | `/auth/password`；email、reset_password_token、password | 令牌一次性、过期和重复使用可识别 |
| AUTH-06 | 邮箱确认 | 完成确认请求、令牌校验和已确认状态处理 | confirmation_token | 重复确认保持幂等 |
| AUTH-07 | Profile | 查询和更新姓名、显示名、头像、语言和界面设置 | `/api/v1/profile`；name、display_name、ui_settings | 只允许修改本人资料 |
| AUTH-08 | 在线状态 | 按 Account 设置 available、busy、offline 等状态 | `/profile/availability`；account_id、availability | 分配候选和界面实时同步 |
| AUTH-09 | MFA 建立 | 生成验证配置、确认一次性验证码、生成恢复码 | `/profile/mfa`；otp_code、enabled | 未确认前不启用，敏感信息只展示一次 |
| AUTH-10 | MFA 登录 | 首次凭证通过后要求验证码或恢复码 | otp_code、backup_code | 错误次数、锁定和恢复码消耗正确 |
| AUTH-11 | MFA 关闭与恢复 | 再次验证身份；关闭 MFA；轮换恢复码 | password、otp_code | 旧恢复码失效并产生审计 |
| AUTH-12 | 登录设备列表 | 返回设备名、IP、最近活动和当前会话标志 | `/profile/sessions` | 用户只能查看本人会话 |
| AUTH-13 | 撤销设备会话 | 撤销指定或其他全部会话并处理推送订阅 | session_id | 被撤销设备后续请求失败 |
| AUTH-14 | Google OAuth | 发起授权、处理回调、匹配或建立身份、完成登录 | `/omniauth/google_oauth2/callback`；code、sso_auth_token | state、邮箱和重复账号规则通过 |
| AUTH-15 | SAML | 配置 SSO、发起登录、处理断言、角色映射和回跳 | `/api/v1/auth/saml_login`；account_id、RelayState | 签名、受众、时间和角色映射正确 |
| AUTH-16 | 安全策略 | 联合密码周期、失败锁定、IP 白名单、登录审批和敏感操作再验证 | security settings | webchat 既有安全能力无回退 |

## 3. 登录处理顺序

1. 确认 User 状态、账号全局限制和请求来源；
2. 验证第一凭证；
3. 若启用 MFA，进入第二验证阶段；
4. 生成会话与认证头；
5. 记录设备、IP、时间和登录方式；
6. 返回可访问 Account 摘要；
7. 后续每次请求再执行 Account 和角色判断；
8. 退出、密码变化、MFA 安全变化或设备撤销时按范围失效会话。

## 4. 必测安全场景

- 正确密码、错误密码、锁定用户、暂停账号；
- MFA 正确码、错误码、过期码、已使用恢复码；
- 同一用户多设备登录，撤销单台和撤销其他全部；
- 密码重置后旧会话是否按基线失效；
- OAuth state 不匹配、邮箱已存在、授权取消；
- SAML 断言过期、签名错误、Account 不匹配、角色映射变化；
- Profile 更新他人资料和跨账号 availability；
- 登录错误、审计和通知中不泄露凭证。

## 5. 完成条件

- [ ] AUTH-01 至 AUTH-16 全部关闭；
- [ ] 普通登录、MFA、OAuth 和 SAML 均有成功与失败证据；
- [ ] Token 到期、轮换、退出、密码变化和设备撤销结果一致；
- [ ] webchat 的 IP、审批、密码周期和锁定策略已保留；
- [ ] 身份验证不会绕过 Account、角色和 Inbox 权限。
