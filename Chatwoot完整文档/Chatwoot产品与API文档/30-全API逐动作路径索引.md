# Chatwoot 全 API 逐动作路径索引

> 导航：[全接口功能与文档覆盖矩阵](23-全接口功能与文档覆盖矩阵.md)｜[公共字段、枚举、错误与验收](12-公共字段枚举错误与验收.md)

## 1. 使用说明

本分册按 HTTP 方法和完整路径列出 Chatwoot 4.16.0 的业务动作。字段、权限、状态和设计规则通过“分册”列定位，不在本索引重复展开。

GET/POST 表示同一路径分别支持两个方法；PATCH/DELETE 同理。{id} 始终先受当前身份、Account 和上级资源归属校验。

## 2. 身份认证

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| POST | /auth/sign_in | 用户登录 | 02、13 |
| DELETE | /auth/sign_out | 当前会话退出 | 13 |
| GET | /auth/validate_token | 验证认证头 | 02、13 |
| POST | /auth | 用户注册 | 13 |
| POST | /auth/password | 发送密码重置 | 13 |
| PUT | /auth/password | 提交新密码 | 13 |
| GET | /auth/confirmation | 确认邮箱 | 13 |
| POST | /auth/confirmation | 重发确认邮件 | 13 |
| POST | /resend_confirmation | 重发确认邮件的兼容入口 | 13 |
| GET | /omniauth/google_oauth2/callback | Google OAuth 登录回调 | 13 |
| POST | /api/v1/auth/saml_login | 发起 SAML 登录 | 13、16 |
| GET/POST | /auth/saml | 进入 SAML Provider 认证 | 13、16 |
| GET | /omniauth/saml/callback | SAML 登录回调 | 13、16 |

认证组件可能同时接受兼容的 GET 或 POST OAuth 回调；业务端应使用 13 分册定义的正式登录流程。

## 3. Account

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| POST | /api/v1/accounts | 创建 Account | 03 |
| GET | /api/v1/accounts/{account_id} | 获取 Account | 03 |
| PATCH/PUT | /api/v1/accounts/{account_id} | 更新 Account | 03 |
| POST | /api/v1/accounts/{account_id}/update_active_at | 更新当前成员活动时间 | 03、13 |
| GET | /api/v1/accounts/{account_id}/cache_keys | 获取客户端缓存版本 | 02、12 |
| POST | /api/v1/accounts/{account_id}/actions/contact_merge | 合并联系人 | 05、14 |
| POST | /api/v1/accounts/{account_id}/bulk_actions | 批量操作会话或联系人 | 14 |
| PATCH/PUT | /api/v1/accounts/{account_id}/onboarding | 更新引导状态 | 03 |
| GET | /api/v1/accounts/{account_id}/onboarding/help_center_generation | 查询引导内容生成进度 | 09、31 |

## 4. Agent、Team 与权限

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET/POST | /api/v1/accounts/{account_id}/agents | 成员列表或邀请 | 03、21 |
| PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/agents/{id} | 更新或删除成员 | 03、21 |
| POST | /api/v1/accounts/{account_id}/agents/bulk_create | 批量添加成员 | 03 |
| GET/POST | /api/v1/accounts/{account_id}/teams | 团队列表或创建 | 03、21 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/teams/{id} | 团队详情、更新或删除 | 03、21 |
| GET/POST/PATCH/DELETE | /api/v1/accounts/{account_id}/teams/{team_id}/team_members | 团队成员列表、添加、批量更新或移除 | 03 |
| POST/PATCH/DELETE | /api/v1/accounts/{account_id}/inbox_members | 设置、批量更新或移除 Inbox 成员 | 03、04 |
| GET | /api/v1/accounts/{account_id}/inbox_members/{inbox_id} | 查询 Inbox 成员 | 03、04 |
| GET | /api/v1/accounts/{account_id}/assignable_agents | 查询可分配成员 | 03、06 |
| GET/POST | /api/v1/accounts/{account_id}/custom_roles | 自定义角色列表或创建 | 03、21 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/custom_roles/{id} | 角色详情、更新或删除 | 03、21 |

## 5. Agent Capacity Policy

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET/POST | /api/v1/accounts/{account_id}/agent_capacity_policies | 容量策略列表或创建 | 03、14 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/agent_capacity_policies/{id} | 容量策略详情、更新或删除 | 03、14 |
| GET/POST | /api/v1/accounts/{account_id}/agent_capacity_policies/{policy_id}/users | 策略成员列表或添加 | 03、14 |
| DELETE | /api/v1/accounts/{account_id}/agent_capacity_policies/{policy_id}/users/{id} | 从策略移除成员 | 03、14 |
| POST | /api/v1/accounts/{account_id}/agent_capacity_policies/{policy_id}/inbox_limits | 创建 Inbox 容量限制 | 03、14 |
| PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/agent_capacity_policies/{policy_id}/inbox_limits/{id} | 更新或删除 Inbox 容量限制 | 03、14 |

## 6. Assignment Policy

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET/POST | /api/v1/accounts/{account_id}/assignment_policies | 分配策略列表或创建 | 14 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/assignment_policies/{id} | 策略详情、更新或删除 | 14 |
| GET/POST | /api/v1/accounts/{account_id}/assignment_policies/{policy_id}/inboxes | 查询或绑定 Inbox | 14 |
| DELETE | /api/v1/accounts/{account_id}/assignment_policies/{policy_id}/inboxes/{id} | 解除策略 Inbox 绑定 | 14 |
| GET/POST/DELETE | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/assignment_policy | 查询、绑定或解除 Inbox 策略 | 14 |

## 7. Captain Preferences 与 Task

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET | /api/v1/accounts/{account_id}/captain/preferences | 获取 Provider、模型与 Feature | 09、22 |
| PATCH/PUT | /api/v1/accounts/{account_id}/captain/preferences | 更新 AI 偏好 | 09、22 |
| POST | /api/v1/accounts/{account_id}/captain/tasks/rewrite | 改写文本 | 09、22 |
| POST | /api/v1/accounts/{account_id}/captain/tasks/summarize | 总结文本或会话 | 09、22 |
| POST | /api/v1/accounts/{account_id}/captain/tasks/reply_suggestion | 生成回复建议 | 09、22 |
| POST | /api/v1/accounts/{account_id}/captain/tasks/label_suggestion | 生成标签建议 | 09、22 |
| POST | /api/v1/accounts/{account_id}/captain/tasks/follow_up | 生成跟进建议 | 09、22 |

## 8. Captain Assistant、Scenario 和 FAQ

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET/POST | /api/v1/accounts/{account_id}/captain/assistants | Assistant 列表或创建 | 09 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/captain/assistants/{id} | Assistant 详情、更新或删除 | 09 |
| POST | /api/v1/accounts/{account_id}/captain/assistants/{id}/playground | 测试 Assistant 回答 | 09 |
| GET | /api/v1/accounts/{account_id}/captain/assistants/{id}/stats | Assistant 统计 | 09、20 |
| GET | /api/v1/accounts/{account_id}/captain/assistants/{id}/summary | Assistant 摘要 | 09、20 |
| GET | /api/v1/accounts/{account_id}/captain/assistants/{id}/drilldown | Assistant 指标下钻 | 09、20 |
| GET | /api/v1/accounts/{account_id}/captain/assistants/tools | 可用工具目录 | 09 |
| GET/POST | /api/v1/accounts/{account_id}/captain/assistants/{assistant_id}/inboxes | 查询或绑定 Inbox | 09 |
| DELETE | /api/v1/accounts/{account_id}/captain/assistants/{assistant_id}/inboxes/{inbox_id} | 解除 Inbox 绑定 | 09 |
| GET/POST | /api/v1/accounts/{account_id}/captain/assistants/{assistant_id}/scenarios | Scenario 列表或创建 | 09 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/captain/assistants/{assistant_id}/scenarios/{id} | Scenario 详情、更新或删除 | 09 |
| GET/POST | /api/v1/accounts/{account_id}/captain/assistant_responses | FAQ 列表或创建 | 09 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/captain/assistant_responses/{id} | FAQ 详情、更新或删除 | 09 |
| POST | /api/v1/accounts/{account_id}/captain/bulk_actions | 批量管理 Captain 资源 | 09 |
| POST | /api/v1/accounts/{account_id}/captain/message_reports | 反馈 AI 消息 | 09、20 |

## 9. Captain Document、Copilot 和 Custom Tool

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET/POST | /api/v1/accounts/{account_id}/captain/documents | 文档列表或创建 | 09、31 |
| GET/DELETE | /api/v1/accounts/{account_id}/captain/documents/{id} | 文档详情或删除 | 09、31 |
| POST | /api/v1/accounts/{account_id}/captain/documents/{id}/sync | 重新同步文档 | 09、31 |
| GET/POST | /api/v1/accounts/{account_id}/captain/copilot_threads | Copilot Thread 列表或创建 | 09 |
| GET/POST | /api/v1/accounts/{account_id}/captain/copilot_threads/{thread_id}/copilot_messages | Copilot 消息列表或创建 | 09 |
| GET/POST | /api/v1/accounts/{account_id}/captain/custom_tools | Custom Tool 列表或创建 | 09、22 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/captain/custom_tools/{id} | Tool 详情、更新或删除 | 09、22 |
| POST | /api/v1/accounts/{account_id}/captain/custom_tools/test | 测试 Tool | 09、22 |

## 10. SAML、Agent Bot 与 Audit

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET/POST | /api/v1/accounts/{account_id}/saml_settings | 获取或创建 SAML 设置 | 13、16 |
| PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/saml_settings | 更新或删除 SAML 设置 | 13、16 |
| GET/POST | /api/v1/accounts/{account_id}/agent_bots | Agent Bot 列表或创建 | 10 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/agent_bots/{id} | Bot 详情、更新或删除 | 10 |
| DELETE | /api/v1/accounts/{account_id}/agent_bots/{id}/avatar | 删除 Bot 头像 | 10 |
| POST | /api/v1/accounts/{account_id}/agent_bots/{id}/reset_access_token | 重置 Bot Access Token | 10 |
| POST | /api/v1/accounts/{account_id}/agent_bots/{id}/reset_secret | 重置 Bot Secret | 10 |
| GET | /api/v1/accounts/{account_id}/audit_logs | 查询审计日志 | 16 |

## 11. Facebook Page Callback

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET/POST | /api/v1/accounts/{account_id}/callbacks/register_facebook_page | 登记 Facebook Page | 15、26 |
| POST | /api/v1/accounts/{account_id}/callbacks/facebook_pages | 查询可管理 Page | 15、26 |
| POST | /api/v1/accounts/{account_id}/callbacks/reauthorize_page | 重新授权 Page | 15、26 |

## 12. Canned Response、Automation、Macro、SLA 与 Campaign

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET/POST | /api/v1/accounts/{account_id}/canned_responses | 快捷回复列表或创建 | 07 |
| PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/canned_responses/{id} | 更新或删除快捷回复 | 07 |
| GET/POST | /api/v1/accounts/{account_id}/automation_rules | 自动化规则列表或创建 | 07、18 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/automation_rules/{id} | 规则详情、更新或删除 | 07、18 |
| POST | /api/v1/accounts/{account_id}/automation_rules/{id}/clone | 克隆规则 | 07、18 |
| GET/POST | /api/v1/accounts/{account_id}/macros | Macro 列表或创建 | 07、18 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/macros/{id} | Macro 详情、更新或删除 | 07、18 |
| POST | /api/v1/accounts/{account_id}/macros/{id}/execute | 执行 Macro | 07、18 |
| GET/POST | /api/v1/accounts/{account_id}/sla_policies | SLA 策略列表或创建 | 08 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/sla_policies/{id} | SLA 策略详情、更新或删除 | 08 |
| GET/POST | /api/v1/accounts/{account_id}/campaigns | Campaign 列表或创建 | 07、31 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/campaigns/{id} | Campaign 详情、更新或删除 | 07、31 |
| GET/POST | /api/v1/accounts/{account_id}/dashboard_apps | Dashboard App 列表或创建 | 10 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/dashboard_apps/{id} | App 详情、更新或删除 | 10 |
| POST | /api/v1/accounts/{account_id}/channels/twilio_channel | 创建 Twilio Messaging Inbox | 15、17 |

## 13. Conversation 列表与命令

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET/POST | /api/v1/accounts/{account_id}/conversations | 会话列表或创建 | 06 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/conversations/{id} | 会话详情、更新或删除 | 06 |
| GET | /api/v1/accounts/{account_id}/conversations/meta | 会话列表元信息 | 06、20 |
| GET | /api/v1/accounts/{account_id}/conversations/search | 搜索会话 | 06、08 |
| GET | /api/v1/accounts/{account_id}/conversations/unread_counts | 未读会话统计 | 06、20 |
| POST | /api/v1/accounts/{account_id}/conversations/filter | 条件筛选会话 | 14 |
| POST | /api/v1/accounts/{account_id}/conversations/{id}/mute | 静音会话 | 06 |
| POST | /api/v1/accounts/{account_id}/conversations/{id}/unmute | 取消静音 | 06 |
| POST | /api/v1/accounts/{account_id}/conversations/{id}/transcript | 发送会话记录 | 06 |
| POST | /api/v1/accounts/{account_id}/conversations/{id}/toggle_status | 切换状态 | 06、12 |
| POST | /api/v1/accounts/{account_id}/conversations/{id}/toggle_priority | 切换优先级 | 06、12 |
| POST | /api/v1/accounts/{account_id}/conversations/{id}/toggle_typing_status | 更新输入状态 | 06、19 |
| POST | /api/v1/accounts/{account_id}/conversations/{id}/update_last_seen | 更新已读位置 | 06 |
| POST | /api/v1/accounts/{account_id}/conversations/{id}/unread | 标记未读 | 06 |
| POST | /api/v1/accounts/{account_id}/conversations/{id}/custom_attributes | 更新会话自定义字段 | 05、06 |
| GET | /api/v1/accounts/{account_id}/conversations/{id}/attachments | 查询全部会话附件 | 06 |
| GET | /api/v1/accounts/{account_id}/conversations/{id}/inbox_assistant | 查询 Inbox Assistant | 09 |
| GET | /api/v1/accounts/{account_id}/conversations/{id}/reporting_events | 查询会话指标事件 | 16、20 |

## 14. Conversation 子资源

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET/POST | /api/v1/accounts/{account_id}/conversations/{conversation_id}/messages | 消息列表或发送 | 06 |
| PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/conversations/{conversation_id}/messages/{id} | 更新或删除消息 | 06 |
| POST | /api/v1/accounts/{account_id}/conversations/{conversation_id}/messages/{id}/translate | 翻译消息 | 06、27 |
| POST | /api/v1/accounts/{account_id}/conversations/{conversation_id}/messages/{id}/retry | 重试失败消息 | 06、26 |
| POST | /api/v1/accounts/{account_id}/conversations/{conversation_id}/assignments | 分配成员或团队 | 06、14 |
| GET/POST | /api/v1/accounts/{account_id}/conversations/{conversation_id}/labels | 查询或设置标签 | 05、06 |
| GET/POST/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/conversations/{conversation_id}/participants | 管理参与者 | 06 |
| POST | /api/v1/accounts/{account_id}/conversations/{conversation_id}/direct_uploads | 创建直传附件 | 06、14 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/conversations/{conversation_id}/draft_messages | 获取、保存或删除草稿 | 06、14 |

## 15. Search

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET | /api/v1/accounts/{account_id}/search | 全局搜索 | 08 |
| GET | /api/v1/accounts/{account_id}/search/conversations | 搜索会话 | 08 |
| GET | /api/v1/accounts/{account_id}/search/messages | 搜索消息 | 08 |
| GET | /api/v1/accounts/{account_id}/search/contacts | 搜索联系人 | 08 |
| GET | /api/v1/accounts/{account_id}/search/articles | 搜索帮助文章 | 08 |

## 16. Company

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET/POST | /api/v1/accounts/{account_id}/companies | 公司列表或创建 | 05 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/companies/{id} | 公司详情、更新或删除 | 05 |
| GET | /api/v1/accounts/{account_id}/companies/search | 搜索公司 | 05、08 |
| POST | /api/v1/accounts/{account_id}/companies/{id}/destroy_custom_attributes | 删除公司自定义字段值 | 05 |
| DELETE | /api/v1/accounts/{account_id}/companies/{id}/avatar | 删除公司头像 | 05 |
| GET/POST | /api/v1/accounts/{account_id}/companies/{company_id}/contacts | 公司联系人列表或关联 | 05 |
| DELETE | /api/v1/accounts/{account_id}/companies/{company_id}/contacts/{id} | 解除公司联系人关系 | 05 |
| GET | /api/v1/accounts/{account_id}/companies/{company_id}/contacts/search | 搜索可关联联系人 | 05 |
| GET | /api/v1/accounts/{account_id}/companies/{company_id}/conversations | 公司会话 | 05、06 |
| GET | /api/v1/accounts/{account_id}/companies/{company_id}/notes | 公司备注 | 05 |

## 17. Contact

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET/POST | /api/v1/accounts/{account_id}/contacts | 联系人列表或创建 | 05 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/contacts/{id} | 联系人详情、更新或删除 | 05 |
| GET | /api/v1/accounts/{account_id}/contacts/active | 活跃联系人 | 05 |
| GET | /api/v1/accounts/{account_id}/contacts/search | 搜索联系人 | 05、08 |
| POST | /api/v1/accounts/{account_id}/contacts/filter | 筛选联系人 | 05、14 |
| POST | /api/v1/accounts/{account_id}/contacts/import | 导入联系人 | 05、14 |
| POST | /api/v1/accounts/{account_id}/contacts/export | 导出联系人 | 05 |
| GET | /api/v1/accounts/{account_id}/contacts/{id}/contactable_inboxes | 查询可联系 Inbox | 05 |
| POST | /api/v1/accounts/{account_id}/contacts/{id}/destroy_custom_attributes | 删除自定义字段值 | 05 |
| DELETE | /api/v1/accounts/{account_id}/contacts/{id}/avatar | 删除联系人头像 | 05 |
| GET | /api/v1/accounts/{account_id}/contacts/{contact_id}/conversations | 联系人会话 | 05、06 |
| POST | /api/v1/accounts/{account_id}/contacts/{contact_id}/contact_inboxes | 创建渠道身份 | 05、17 |
| GET/POST | /api/v1/accounts/{account_id}/contacts/{contact_id}/labels | 查询或设置联系人标签 | 05 |
| GET/POST | /api/v1/accounts/{account_id}/contacts/{contact_id}/notes | 备注列表或创建 | 05 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/contacts/{contact_id}/notes/{id} | 备注详情、更新或删除 | 05 |
| GET | /api/v1/accounts/{account_id}/contacts/{contact_id}/attachments | 联系人附件 | 05 |
| POST | /api/v1/accounts/{account_id}/contacts/{contact_id}/call | 向联系人外呼 | 15 |
| POST | /api/v1/accounts/{account_id}/contact_inboxes/filter | 按 Inbox 和 source_id 查找渠道身份 | 05 |

## 18. Data Import

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET/POST | /api/v1/accounts/{account_id}/data_imports | 导入列表或创建 | 14、31 |
| GET | /api/v1/accounts/{account_id}/data_imports/{id} | 导入详情和进度 | 14、31 |
| POST | /api/v1/accounts/{account_id}/data_imports/validate_source | 校验导入源 | 14 |
| POST | /api/v1/accounts/{account_id}/data_imports/{id}/start | 启动导入 | 14、31 |
| POST | /api/v1/accounts/{account_id}/data_imports/{id}/abandon | 放弃导入 | 14、31 |
| GET | /api/v1/accounts/{account_id}/data_imports/{id}/error_logs | 下载错误记录 | 14 |
| GET | /api/v1/accounts/{account_id}/data_imports/{id}/skip_logs | 下载跳过记录 | 14 |

## 19. CSAT、SLA Result 与 Reporting Event

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET | /api/v1/accounts/{account_id}/csat_survey_responses | CSAT 列表 | 08、20 |
| GET | /api/v1/accounts/{account_id}/csat_survey_responses/metrics | CSAT 指标 | 08、20 |
| GET | /api/v1/accounts/{account_id}/csat_survey_responses/download | 下载 CSAT | 08、20 |
| PATCH | /api/v1/accounts/{account_id}/csat_survey_responses/{id} | 更新 CSAT 审核信息 | 08、20 |
| GET | /api/v1/accounts/{account_id}/applied_slas | 已应用 SLA 列表 | 08、20 |
| GET | /api/v1/accounts/{account_id}/applied_slas/metrics | SLA 指标 | 08、20 |
| GET | /api/v1/accounts/{account_id}/applied_slas/download | 下载 SLA 数据 | 08、20 |
| GET | /api/v1/accounts/{account_id}/reporting_events | Account 指标事件 | 16、20 |

## 20. Call 与 WhatsApp Call

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET | /api/v1/accounts/{account_id}/calls | 通话列表 | 15 |
| GET | /api/v1/accounts/{account_id}/whatsapp_calls/{id} | WhatsApp Call 详情 | 15、26 |
| POST | /api/v1/accounts/{account_id}/whatsapp_calls/{id}/accept | 接听 | 15、26 |
| POST | /api/v1/accounts/{account_id}/whatsapp_calls/{id}/reject | 拒绝 | 15、26 |
| POST | /api/v1/accounts/{account_id}/whatsapp_calls/{id}/terminate | 结束 | 15、26 |
| POST | /api/v1/accounts/{account_id}/whatsapp_calls/{id}/upload_recording | 上传录音 | 15 |
| POST | /api/v1/accounts/{account_id}/whatsapp_calls/initiate | 发起 WhatsApp Call | 15、26 |

## 21. Custom Definition、Filter 与品牌邮件

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET/POST | /api/v1/accounts/{account_id}/custom_attribute_definitions | 自定义字段定义列表或创建 | 05 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/custom_attribute_definitions/{id} | 定义详情、更新或删除 | 05 |
| GET/POST | /api/v1/accounts/{account_id}/custom_filters | 保存筛选列表或创建 | 14 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/custom_filters/{id} | 筛选详情、更新或删除 | 14 |
| GET | /api/v1/accounts/{account_id}/branded_email_layout | 获取品牌邮件布局 | 16 |
| PATCH/PUT | /api/v1/accounts/{account_id}/branded_email_layout | 更新品牌邮件布局 | 16 |

## 22. Inbox

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET/POST | /api/v1/accounts/{account_id}/inboxes | Inbox 列表或创建 | 04、17 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/inboxes/{id} | Inbox 详情、更新或删除 | 04、17 |
| GET | /api/v1/accounts/{account_id}/inboxes/{id}/assignable_agents | Inbox 可分配成员 | 03、04 |
| GET | /api/v1/accounts/{account_id}/inboxes/{id}/campaigns | Inbox Campaign | 07 |
| GET | /api/v1/accounts/{account_id}/inboxes/{id}/agent_bot | 当前 Bot | 10 |
| POST | /api/v1/accounts/{account_id}/inboxes/{id}/set_agent_bot | 设置 Bot | 10 |
| DELETE | /api/v1/accounts/{account_id}/inboxes/{id}/avatar | 删除头像 | 04 |
| POST | /api/v1/accounts/{account_id}/inboxes/{id}/sync_templates | 同步模板 | 15、26、31 |
| GET | /api/v1/accounts/{account_id}/inboxes/{id}/health | WhatsApp Cloud 健康检查 | 15、26 |
| POST | /api/v1/accounts/{account_id}/inboxes/{id}/register_webhook | 重新登记 WhatsApp Webhook | 15、26 |
| POST | /api/v1/accounts/{account_id}/inboxes/{id}/reset_secret | 重置 API Inbox Secret | 04、15 |
| POST/DELETE | /api/v1/accounts/{account_id}/inboxes/{id}/conference | 创建、加入或结束会议 | 15 |
| GET | /api/v1/accounts/{account_id}/inboxes/{id}/conference/token | 获取会议 Token | 15 |
| POST | /api/v1/accounts/{account_id}/inboxes/{id}/enable_whatsapp_calling | 启用 WhatsApp Calling | 15 |
| POST | /api/v1/accounts/{account_id}/inboxes/{id}/disable_whatsapp_calling | 停用 WhatsApp Calling | 15 |
| POST | /api/v1/accounts/{account_id}/inboxes/{id}/set_inbound_calls | 设置入站通话 | 15 |
| GET/POST | /api/v1/accounts/{account_id}/inboxes/{id}/csat_template | 查询或创建 CSAT 模板 | 15、26、31 |
| POST | /api/v1/accounts/{account_id}/inboxes/{id}/csat_template/analyze | 分析 CSAT 模板 | 15、26 |

## 23. Label 与 Notification

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET/POST | /api/v1/accounts/{account_id}/labels | 标签列表或创建 | 05 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/labels/{id} | 标签详情、更新或删除 | 05 |
| GET | /api/v1/accounts/{account_id}/notifications | 通知列表 | 07 |
| PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/notifications/{id} | 更新或删除通知 | 07 |
| POST | /api/v1/accounts/{account_id}/notifications/read_all | 全部标为已读 | 07 |
| GET | /api/v1/accounts/{account_id}/notifications/unread_count | 未读数量 | 07 |
| POST | /api/v1/accounts/{account_id}/notifications/destroy_all | 删除全部通知 | 07 |
| POST | /api/v1/accounts/{account_id}/notifications/{id}/snooze | 暂停通知 | 07 |
| POST | /api/v1/accounts/{account_id}/notifications/{id}/unread | 标记未读 | 07 |
| GET/PATCH/PUT | /api/v1/accounts/{account_id}/notification_settings | 获取或更新通知偏好 | 07、28 |

## 24. 渠道授权

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| POST | /api/v1/accounts/{account_id}/twitter/authorization | X/Twitter 授权 | 15、26 |
| POST | /api/v1/accounts/{account_id}/microsoft/authorization | Microsoft Email 授权 | 15、26 |
| POST | /api/v1/accounts/{account_id}/google/authorization | Google Email 授权 | 15、26 |
| POST | /api/v1/accounts/{account_id}/instagram/authorization | Instagram 授权 | 15、26 |
| POST | /api/v1/accounts/{account_id}/tiktok/authorization | TikTok 授权 | 15、26 |
| POST | /api/v1/accounts/{account_id}/notion/authorization | Notion 授权 | 27 |
| POST | /api/v1/accounts/{account_id}/whatsapp/authorization | WhatsApp Embedded Signup | 15、26 |

## 25. Account Webhook 与 Integration

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET/POST | /api/v1/accounts/{account_id}/webhooks | Webhook 列表或创建 | 10、19 |
| PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/webhooks/{id} | 更新或删除 Webhook | 10、19 |
| GET | /api/v1/accounts/{account_id}/integrations/apps | 集成目录 | 10、27 |
| GET | /api/v1/accounts/{account_id}/integrations/apps/{id} | 集成详情 | 10、27 |
| POST | /api/v1/accounts/{account_id}/integrations/hooks | 创建 Hook | 10、27 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/integrations/hooks/{id} | Hook 详情、更新或删除 | 10、27 |
| POST | /api/v1/accounts/{account_id}/integrations/hooks/{id}/process_event | 请求 Hook 处理事件 | 10、27 |
| POST/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/integrations/slack | 创建、更新或删除 Slack 连接 | 10、27 |
| GET | /api/v1/accounts/{account_id}/integrations/slack/list_all_channels | Slack Channel 列表 | 27 |
| POST | /api/v1/accounts/{account_id}/integrations/dyte/create_a_meeting | 创建 RealtimeKit 会议 | 10、27 |
| POST | /api/v1/accounts/{account_id}/integrations/dyte/add_participant_to_meeting | 添加会议参与者 | 10、27 |
| POST | /api/v1/accounts/{account_id}/integrations/shopify/auth | 获取 Shopify 授权 URL | 27 |
| GET | /api/v1/accounts/{account_id}/integrations/shopify/orders | 获取联系人订单 | 27 |
| DELETE | /api/v1/accounts/{account_id}/integrations/shopify | 删除 Shopify 连接 | 27 |
| DELETE | /api/v1/accounts/{account_id}/integrations/linear | 删除 Linear 连接 | 27 |
| GET | /api/v1/accounts/{account_id}/integrations/linear/teams | Linear Team | 27 |
| GET | /api/v1/accounts/{account_id}/integrations/linear/team_entities | Linear Team 实体 | 27 |
| POST | /api/v1/accounts/{account_id}/integrations/linear/create_issue | 创建 Issue | 27 |
| POST | /api/v1/accounts/{account_id}/integrations/linear/link_issue | 关联 Issue | 27 |
| POST | /api/v1/accounts/{account_id}/integrations/linear/unlink_issue | 解除 Issue 关联 | 27 |
| GET | /api/v1/accounts/{account_id}/integrations/linear/search_issue | 搜索 Issue | 27 |
| GET | /api/v1/accounts/{account_id}/integrations/linear/linked_issues | 已关联 Issue | 27 |
| DELETE | /api/v1/accounts/{account_id}/integrations/notion | 删除 Notion 连接 | 27 |
| POST | /api/v1/integrations/webhooks | Slack 入站事件 | 27、29 |

## 26. Portal、Category、Article 与 Upload

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET/POST | /api/v1/accounts/{account_id}/portals | Portal 列表或创建 | 08 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/portals/{id} | Portal 详情、更新或删除 | 08 |
| PATCH | /api/v1/accounts/{account_id}/portals/{id}/archive | 归档 Portal | 08 |
| DELETE | /api/v1/accounts/{account_id}/portals/{id}/logo | 删除 Logo | 08 |
| POST | /api/v1/accounts/{account_id}/portals/{id}/send_instructions | 发送域名配置说明 | 08 |
| GET | /api/v1/accounts/{account_id}/portals/{id}/ssl_status | 自定义域名 SSL 状态 | 08、31 |
| GET/POST | /api/v1/accounts/{account_id}/portals/{portal_id}/categories | 分类列表或创建 | 08 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/portals/{portal_id}/categories/{id} | 分类详情、更新或删除 | 08 |
| POST | /api/v1/accounts/{account_id}/portals/{portal_id}/categories/reorder | 分类排序 | 08 |
| GET/POST | /api/v1/accounts/{account_id}/portals/{portal_id}/articles | 文章列表或创建 | 08 |
| GET/PATCH/PUT/DELETE | /api/v1/accounts/{account_id}/portals/{portal_id}/articles/{id} | 文章详情、更新或删除 | 08 |
| POST | /api/v1/accounts/{account_id}/portals/{portal_id}/articles/reorder | 文章排序 | 08 |
| POST | /api/v1/accounts/{account_id}/portals/{portal_id}/articles/bulk_actions/translate | 批量翻译 | 14 |
| PATCH | /api/v1/accounts/{account_id}/portals/{portal_id}/articles/bulk_actions/update_status | 批量更新状态 | 14 |
| PATCH | /api/v1/accounts/{account_id}/portals/{portal_id}/articles/bulk_actions/update_category | 批量移动分类 | 14 |
| DELETE | /api/v1/accounts/{account_id}/portals/{portal_id}/articles/bulk_actions/delete_articles | 批量删除文章 | 14 |
| POST | /api/v1/accounts/{account_id}/upload | Account 文件上传 | 08、14 |

## 27. Profile、MFA、Session 与设备订阅

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET/PATCH/PUT | /api/v1/profile | 个人资料读取或更新 | 13 |
| DELETE | /api/v1/profile/avatar | 删除头像 | 13 |
| POST | /api/v1/profile/availability | 更新可用状态 | 13 |
| POST | /api/v1/profile/auto_offline | 自动离线 | 13 |
| PUT | /api/v1/profile/set_active_account | 设置当前 Account | 13 |
| POST | /api/v1/profile/resend_confirmation | 重发确认邮件 | 13 |
| POST | /api/v1/profile/reset_access_token | 重置个人 Access Token | 13 |
| GET/POST/DELETE | /api/v1/profile/mfa | 获取、启用或停用 MFA | 13 |
| POST | /api/v1/profile/mfa/verify | 验证 OTP | 13 |
| POST | /api/v1/profile/mfa/backup_codes | 生成恢复码 | 13 |
| GET | /api/v1/profile/sessions | 登录设备列表 | 13 |
| DELETE | /api/v1/profile/sessions/{id} | 退出指定设备 | 13 |
| POST | /api/v1/notification_subscriptions | 注册 Browser Push 或 FCM | 28 |
| DELETE | /api/v1/notification_subscriptions | 删除当前 User 的 FCM 订阅 | 28 |

## 28. Widget API

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| POST | /api/v1/widget/config | 初始化 Widget 配置 | 04、11 |
| POST | /api/v1/widget/direct_uploads | Widget 直传附件 | 04 |
| GET | /api/v1/widget/campaigns | 可展示 Campaign | 04、07 |
| POST | /api/v1/widget/events | 上报 Widget 事件 | 04 |
| GET/POST | /api/v1/widget/messages | 消息列表或创建 | 04、11 |
| PATCH/PUT | /api/v1/widget/messages/{id} | 更新 Widget 消息 | 04 |
| GET/POST | /api/v1/widget/conversations | 会话列表或创建 | 04、11 |
| POST | /api/v1/widget/conversations/destroy_custom_attributes | 删除当前 Widget 会话自定义字段 | 04 |
| POST | /api/v1/widget/conversations/set_custom_attributes | 设置当前 Widget 会话自定义字段 | 04 |
| POST | /api/v1/widget/conversations/update_last_seen | 更新当前 Widget 会话客户已读位置 | 04 |
| POST | /api/v1/widget/conversations/toggle_typing | 当前 Widget 会话客户输入状态 | 04、19 |
| POST | /api/v1/widget/conversations/transcript | 发送当前 Widget 会话记录 | 04 |
| GET | /api/v1/widget/conversations/toggle_status | 切换当前 Widget 会话状态 | 04 |
| GET/PATCH/PUT | /api/v1/widget/contact | 获取或更新 Widget Contact | 04、11 |
| POST | /api/v1/widget/contact/destroy_custom_attributes | 删除客户自定义字段 | 04 |
| PATCH | /api/v1/widget/contact/set_user | 设置受验证客户身份 | 04、13 |
| GET | /api/v1/widget/inbox_members | Widget 成员列表 | 04 |
| POST | /api/v1/widget/labels | 设置 Widget 标签 | 04、05 |
| DELETE | /api/v1/widget/labels/{id} | 移除 Widget 标签 | 04、05 |
| POST | /api/v1/widget/integrations/dyte/add_participant_to_meeting | 客户加入会议 | 15、27 |

## 29. V2 Reports

所有路径以 /api/v2/accounts/{account_id} 开头。

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| POST | /api/v2/accounts | 使用公开注册流程创建 User 和 Account | 03、13 |

| 方法 | 相对路径 | 功能 | 分册 |
|---|---|---|---|
| GET | /summary_reports/agent | Agent 汇总 | 08、20 |
| GET | /summary_reports/team | Team 汇总 | 08、20 |
| GET | /summary_reports/inbox | Inbox 汇总 | 08、20 |
| GET | /summary_reports/label | Label 汇总 | 08、20 |
| GET | /summary_reports/channel | Channel 汇总 | 08、20 |
| GET | /reports | 报表时间序列 | 08、20 |
| GET | /reports/summary | 报表摘要 | 08、20 |
| GET | /reports/bot_summary | Bot 摘要 | 08、20 |
| GET | /reports/agents | Agent 报表 | 08、20 |
| GET | /reports/inboxes | Inbox 报表 | 08、20 |
| GET | /reports/labels | Label 报表 | 08、20 |
| GET | /reports/teams | Team 报表 | 08、20 |
| GET | /reports/conversations | Conversation 报表 | 08、20 |
| GET | /reports/conversations_summary | 会话汇总 | 08、20 |
| GET | /reports/conversation_traffic | 会话流量 | 08、20 |
| GET | /reports/drilldown | 指标下钻 | 08、20 |
| GET | /reports/bot_metrics | Bot 指标 | 08、20 |
| GET | /reports/inbox_label_matrix | Inbox 与 Label 矩阵 | 08、20 |
| GET | /reports/first_response_time_distribution | 首次响应时间分布 | 08、20 |
| GET | /reports/outgoing_messages_count | 外发消息数量 | 08、20 |
| GET | /year_in_review | 年度服务摘要 | 08、20 |
| GET | /live_reports/conversation_metrics | 实时会话指标 | 08、20 |
| GET | /live_reports/grouped_conversation_metrics | 分组实时指标 | 08、20 |

## 30. Enterprise Billing

所有路径以 /enterprise/api/v1/accounts/{account_id} 开头，仅在相应版本和环境可用。

| 方法 | 相对路径 | 功能 | 分册 |
|---|---|---|---|
| POST | /checkout | 获取计费管理地址 | 16 |
| POST | /subscription | 创建或确认订阅 | 16 |
| POST | /select_billing_currency | 选择计费币种 | 16 |
| GET | /limits | 获取额度和用量 | 16 |
| POST | /toggle_deletion | 标记或取消 Account 删除 | 16、31 |
| POST | /topup_checkout | 创建 AI 额度充值结算 | 16 |
| GET | /topup_options | 获取充值选项 | 16 |

## 31. Platform API

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| POST | /platform/api/v1/users | 创建 Platform User | 11、16 |
| GET/PATCH/PUT/DELETE | /platform/api/v1/users/{id} | User 详情、更新或删除 | 11、16 |
| GET | /platform/api/v1/users/{id}/login | 获取登录入口 | 11、16 |
| POST | /platform/api/v1/users/{id}/token | 生成 User Token | 11、16 |
| GET/POST | /platform/api/v1/agent_bots | Platform Bot 列表或创建 | 11 |
| GET/PATCH/PUT/DELETE | /platform/api/v1/agent_bots/{id} | Bot 详情、更新或删除 | 11 |
| DELETE | /platform/api/v1/agent_bots/{id}/avatar | 删除 Bot 头像 | 11 |
| GET/POST | /platform/api/v1/accounts | Platform Account 列表或创建 | 11、16 |
| GET/PATCH/PUT/DELETE | /platform/api/v1/accounts/{id} | Account 详情、更新或删除 | 11、16 |
| GET/POST/DELETE | /platform/api/v1/accounts/{account_id}/account_users | 成员关系列表、创建或删除 | 11、16 |
| POST | /platform/api/v1/accounts/{account_id}/email_channel_migrations | 批量迁移邮箱渠道 | 16 |

## 32. Public API

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET | /public/api/v1/inboxes/{inbox_identifier} | 获取 Public API Inbox 信息 | 11 |
| POST | /public/api/v1/inboxes/{inbox_identifier}/contacts | 创建 Public Contact | 11 |
| GET/PATCH/PUT | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier} | Contact 详情或更新 | 11 |
| GET/POST | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier}/conversations | 会话列表或创建 | 11 |
| GET | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier}/conversations/{id} | 会话详情 | 11 |
| POST | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier}/conversations/{id}/toggle_status | 切换状态 | 11 |
| POST | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier}/conversations/{id}/toggle_typing | 客户输入状态 | 11、19 |
| POST | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier}/conversations/{id}/update_last_seen | 更新客户已读位置 | 11 |
| GET/POST | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier}/conversations/{conversation_id}/messages | 消息列表或创建 | 11 |
| PATCH/PUT | /public/api/v1/inboxes/{inbox_identifier}/contacts/{contact_identifier}/conversations/{conversation_id}/messages/{id} | 更新 Public Message | 11 |
| GET/PATCH/PUT | /public/api/v1/csat_survey/{id} | 获取或提交 CSAT | 08、11 |

## 33. Help Center 公开入口

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET | /hc/{slug} | Portal 首页 | 08 |
| GET | /hc/{slug}/sitemap.xml | Sitemap | 08 |
| GET | /hc/{slug}/{locale} | 指定语言 Portal | 08 |
| GET | /hc/{slug}/{locale}/search | 公开搜索 | 08 |
| GET | /hc/{slug}/{locale}/articles | 文章列表 | 08 |
| GET | /hc/{slug}/{locale}/categories | 分类列表 | 08 |
| GET | /hc/{slug}/{locale}/categories/{category_slug} | 分类详情 | 08 |
| GET | /hc/{slug}/{locale}/categories/{category_slug}/articles | 分类文章 | 08 |
| GET | /hc/{slug}/articles/{article_slug} | 文章页面 | 08 |
| GET | /hc/{slug}/articles/{article_slug}.md | Markdown 文章 | 08 |
| GET | /hc/{slug}/articles/{article_slug}.png | 访问统计像素 | 08 |

## 34. Provider 回调

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET/POST | /bot | Facebook Messenger | 26、29 |
| GET/POST | /webhooks/twitter | X/Twitter CRC 和事件 | 26、29 |
| POST | /webhooks/line/{line_channel_id} | LINE 事件 | 29 |
| POST | /webhooks/telegram/{bot_token} | Telegram 事件 | 29 |
| POST | /webhooks/sms/{phone_number} | 通用 SMS 事件 | 29 |
| GET/POST | /webhooks/whatsapp/{phone_number} | WhatsApp 验证和事件 | 26、29 |
| GET/POST | /webhooks/instagram | Instagram 验证和事件 | 26、29 |
| POST | /webhooks/tiktok | TikTok 事件 | 26、29 |
| POST | /webhooks/shopify | Shopify 隐私事件 | 27、29 |
| GET | /twitter/callback | X/Twitter 授权回调 | 15、26 |
| GET | /linear/callback | Linear 授权回调 | 27 |
| GET | /shopify/callback | Shopify 授权回调 | 27 |
| POST | /twilio/callback | Twilio 入站消息 | 15、29 |
| POST | /twilio/delivery_status | Twilio 消息状态 | 15、29 |
| POST | /twilio/voice/call/{phone} | Twilio Voice 指令 | 15、29 |
| POST | /twilio/voice/status/{phone} | Twilio Voice 状态 | 15、29 |
| POST | /twilio/voice/conference_status/{phone} | Twilio Conference 状态 | 15、29 |
| POST | /twilio/voice/recording_status/{phone} | Twilio Recording 状态 | 15、29 |
| GET | /microsoft/callback | Microsoft 授权回调 | 15、26 |
| GET | /google/callback | Google 授权回调 | 15、26 |
| GET | /instagram/callback | Instagram 授权回调 | 15、26 |
| GET | /tiktok/callback | TikTok 授权回调 | 15、26 |
| GET | /notion/callback | Notion 授权回调 | 27 |
| POST | /enterprise/webhooks/stripe | Stripe 计费事件 | 16、29 |
| POST | /enterprise/webhooks/firecrawl | Firecrawl 页面结果 | 16、29 |

## 35. Well-known、健康与页面边界

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET | /health | 实例健康探测 | 25 |
| GET | /api | API 信息入口 | 02 |
| GET | /.well-known/assetlinks.json | Android App Link | 28 |
| GET | /.well-known/apple-app-site-association | iOS Universal Link | 28 |
| GET | /.well-known/microsoft-identity-association.json | Microsoft 域名关联 | 15、28 |
| GET | /.well-known/cf-custom-hostname-challenge/{id} | 自定义域名验证 | 08 |
| GET | /app、/app/* | Dashboard 页面入口 | 01 |
| GET | /widget | Widget 页面入口 | 04 |
| GET | /survey/responses/{id} | CSAT 页面 | 08 |
| GET | /slack_uploads | Slack 附件跳转 | 27 |
| GET | /swagger、/swagger/* | 交互式接口说明页面 | 02 |

## 36. Super Admin

| 方法 | 路径 | 功能 | 分册 |
|---|---|---|---|
| GET/POST | /super_admin/sign_in | 打开或提交 Super Admin 登录 | 25 |
| DELETE | /super_admin/sign_out | 标准退出 | 25 |
| GET | /super_admin/logout | 页面兼容退出 | 25 |
| GET | /super_admin | 安装总览 | 25 |
| GET/POST | /super_admin/app_config | 应用配置查看或保存 | 24、25 |
| GET/POST | /super_admin/push_diagnostics | 推送诊断或测试 | 25、28 |
| POST | /super_admin/push_diagnostics/destroy_subscriptions | 删除选中订阅 | 25、28 |
| GET/POST | /super_admin/accounts | Account 列表或创建 | 24、25 |
| GET/PATCH/PUT/DELETE | /super_admin/accounts/{id} | Account 详情、更新或删除 | 24、25 |
| POST | /super_admin/accounts/{id}/seed | 初始化 Account 数据 | 25、31 |
| POST | /super_admin/accounts/{id}/reset_cache | 刷新 Account 缓存 | 25 |
| GET/POST | /super_admin/users | User 列表或创建 | 25 |
| GET/PATCH/PUT/DELETE | /super_admin/users/{id} | User 详情、更新或删除 | 25 |
| DELETE | /super_admin/users/{id}/avatar | 删除 User 头像 | 25 |
| GET | /super_admin/access_tokens | Token 列表 | 25 |
| GET | /super_admin/access_tokens/{id} | Token 详情 | 25 |
| GET/POST | /super_admin/installation_configs | 安装配置列表或创建 | 24、25 |
| GET/PATCH/PUT | /super_admin/installation_configs/{id} | 配置详情或更新 | 24、25 |
| GET/POST | /super_admin/agent_bots | 全局 Bot 列表或创建 | 25 |
| GET/PATCH/PUT/DELETE | /super_admin/agent_bots/{id} | Bot 详情、更新或删除 | 25 |
| DELETE | /super_admin/agent_bots/{id}/avatar | 删除 Bot 头像 | 25 |
| GET/POST | /super_admin/platform_apps | Platform App 列表或创建 | 25 |
| GET/PATCH/PUT/DELETE | /super_admin/platform_apps/{id} | App 详情、更新或删除 | 25 |
| GET/POST | /super_admin/platform_banners | Banner 列表或创建 | 25 |
| GET/PATCH/PUT/DELETE | /super_admin/platform_banners/{id} | Banner 详情、更新或删除 | 25 |
| GET | /super_admin/instance_status | 实例状态 | 25 |
| GET | /super_admin/settings | 版本设置 | 25 |
| GET | /super_admin/settings/refresh | 刷新版本状态 | 25 |
| POST | /super_admin/account_users | 添加 AccountUser | 25 |
| GET/DELETE | /super_admin/account_users/{id} | 关系详情或删除 | 25 |
| GET/POST | /installation/onboarding | 首次安装引导 | 24、25 |
| GET | /monitoring | 后台任务监控入口 | 25、31 |

## 37. 索引边界

- 同一个资源的 PATCH 和 PUT 表示兼容的更新方法，实际请求优先使用对应专题分册列出的推荐方法；
- 企业接口、语音、SAML、公司、高级搜索和 Captain 等入口受 Feature 与版本限制；
- 页面入口、Well-known、Provider Webhook 和 Super Admin 页面不使用 Account API 认证方式；
- 第三方回调的完整 Payload 字段见 29 分册；
- 创建成功但需要后台处理的动作，最终状态见 31 分册；
- 路径存在不代表当前身份可用，最终以 21 分册的权限和 24 分册的 Feature 为准。
