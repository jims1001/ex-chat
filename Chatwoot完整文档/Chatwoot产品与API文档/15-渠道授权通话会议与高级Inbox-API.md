# 渠道授权、通话、会议与高级 Inbox API

> 导航：[全体功能与 API 总表](00-全体功能与API总表.md)｜[文档目录](README.md)

## 1. 功能范围

本模块补充第三方渠道授权、Facebook Page 接入、Twilio Inbox、WhatsApp Embedded Signup、WhatsApp 模板、语音通话和会议能力。渠道接口通常需要 Administrator 权限，并依赖对应第三方应用配置和账号权限。

## 2. OAuth 授权入口

| 方法 | 路径 | 渠道 |
|---|---|---|
| POST | /api/v1/accounts/{account_id}/twitter/authorization | X/Twitter |
| POST | /api/v1/accounts/{account_id}/microsoft/authorization | Microsoft Email |
| POST | /api/v1/accounts/{account_id}/google/authorization | Google Email |
| POST | /api/v1/accounts/{account_id}/instagram/authorization | Instagram |
| POST | /api/v1/accounts/{account_id}/tiktok/authorization | TikTok |
| POST | /api/v1/accounts/{account_id}/notion/authorization | Notion |

### 2.1 授权请求和响应字段

| 字段 | 类型 | 说明 |
|---|---|---|
| return_to | string 或 null | 授权完成后的目标场景，部分渠道支持 |
| success | boolean | 是否成功生成授权入口 |
| url | string | 第三方授权地址 |

调用授权接口只表示开始授权。只有第三方回调完成、凭证保存且 Inbox 或 Integration 状态正常，渠道才可正式使用。

## 3. Facebook Page API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/callbacks/register_facebook_page | Facebook 授权入口兼容路径 |
| POST | /api/v1/accounts/{account_id}/callbacks/register_facebook_page | 注册 Facebook Page Inbox |
| POST | /api/v1/accounts/{account_id}/callbacks/facebook_pages | 读取当前授权用户的 Page |
| POST | /api/v1/accounts/{account_id}/callbacks/reauthorize_page | 重新授权已有 Page Inbox |

### 3.1 注册 Page 字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| user_access_token | string | 是 | Facebook 用户授权 Token |
| page_access_token | string | 是 | Page Access Token |
| page_id | string | 是 | Facebook Page ID |
| inbox_name | string | 是 | 新 Inbox 名称 |

### 3.2 Page 查询与重新授权字段

| 字段 | 类型 | 说明 |
|---|---|---|
| omniauth_token | string | Facebook 临时授权 Token |
| inbox_id | integer | 需要重新授权的 Inbox |
| id | string | Page ID |
| name | string | Page 名称 |
| access_token | string | Page Token；如返回，应按敏感信息处理 |
| exists | boolean | 当前 Account 是否已经接入该 Page |

同一个 Page 不应重复创建多个 Inbox。重新授权仅适用于当前 Account 中对应的 Facebook Inbox。

## 4. WhatsApp Embedded Signup API

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /api/v1/accounts/{account_id}/whatsapp/authorization | 创建或重新授权 WhatsApp Cloud Inbox |

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| code | string | 是 | Embedded Signup 授权码 |
| business_id | string | 是 | Meta Business ID |
| waba_id | string | 是 | WhatsApp Business Account ID |
| phone_number_id | string | 按授权结果 | WhatsApp Phone Number ID |
| inbox_id | integer | 重新授权时 | 已有 Inbox ID |

### 4.1 成功响应字段

| 字段 | 类型 | 说明 |
|---|---|---|
| success | boolean | 是否成功 |
| id | integer | Inbox ID |
| name | string | Inbox 名称 |
| channel_type | string | whatsapp |
| message | string 或 null | 重新授权结果说明 |

业务账号、WABA 和电话号码必须是同一次授权流程中允许访问的资源。重新授权是否允许还受 Inbox 当前状态和 Feature 控制。

## 5. Twilio Channel API

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /api/v1/accounts/{account_id}/channels/twilio_channel | 创建 Twilio SMS 或 WhatsApp Inbox |

### 5.1 Twilio 创建字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| twilio_channel.name | string | 是 | Inbox 名称 |
| twilio_channel.account_sid | string | 是 | Twilio Account SID |
| twilio_channel.auth_token | string | 是 | Twilio Auth Token |
| twilio_channel.api_key_sid | string 或 null | 否 | 使用 API Key 时提供 |
| twilio_channel.messaging_service_sid | string 或 null | 按配置 | Messaging Service SID |
| twilio_channel.phone_number | string 或 null | 按渠道 | 发送或接听号码 |
| twilio_channel.medium | enum | 是 | sms 或 whatsapp |

凭证会先用于验证 Twilio 账号。创建 SMS 类型时还需要第三方回调配置成功。Auth Token 不应在响应和普通日志中展示。

## 6. Voice Inbox 创建 API（Enterprise）

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /api/v1/accounts/{account_id}/inboxes | 创建 Twilio Voice Inbox |

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| name | string | 是 | Inbox 名称 |
| channel.type | string | 是 | voice |
| channel.phone_number | string | 是 | Twilio Voice 号码 |
| channel.provider | string | 是 | Voice Provider |
| channel.provider_config.account_sid | string | 是 | Twilio Account SID |
| channel.provider_config.auth_token | string | 是 | Twilio Auth Token |
| channel.provider_config.api_key_sid | string | 是 | Twilio API Key SID |
| channel.provider_config.api_key_secret | string | 是 | Twilio API Key Secret |

Voice Inbox 需要 channel_voice Feature。创建完成后仍需确认号码的 Voice 能力和入站回调配置。

## 7. Inbox 高级命令

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/sync_templates | 同步渠道消息模板 |
| POST | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/register_webhook | 重新注册渠道回调 |
| POST | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/reset_secret | 重置 API Channel Secret |
| GET | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/health | 获取渠道健康状态 |
| POST | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/enable_whatsapp_calling | 启用 WhatsApp Calling，Enterprise |
| POST | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/disable_whatsapp_calling | 关闭 WhatsApp Calling，Enterprise |
| POST | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/set_inbound_calls | 设置是否接收入站通话，Enterprise |

### 7.1 Inbound Calls 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| inbound_calls_enabled | boolean | 是否允许该 Voice Inbox 接收入站通话 |

调用开关接口成功只表示 Chatwoot 侧配置完成。第三方账号、号码能力和 Webhook 仍必须有效。

## 8. WhatsApp CSAT Template API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/csat_template | 获取模板状态 |
| POST | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/csat_template | 创建 CSAT 模板 |
| POST | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/csat_template/analyze | 分析模板内容，需要 Captain |

### 8.1 Template 请求字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| template.message | string | 是 | 满意度邀请内容 |
| template.button_text | string | 否 | 按钮文字 |
| template.language | string | 否 | 模板语言，默认值按 Provider 处理 |

### 8.2 Template 响应字段

| 字段 | 类型 | 说明 |
|---|---|---|
| name 或 friendly_name | string | Provider 中的模板名称 |
| template_id 或 content_sid | string | Provider 模板 ID |
| status | string | pending、PENDING 或 Provider 返回状态 |
| language | string | 模板语言 |
| error | string 或 null | 失败说明 |
| details | object 或 null | Provider 错误代码等补充信息 |

模板创建成功通常表示已提交第三方审核，不表示已经可以发送。只有状态进入 Provider 允许的可用状态后才能正式使用。

## 9. Calls 查询 API（Enterprise）

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/calls | 通话列表和筛选 |

### 9.1 Calls Query 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| status | enum | ringing、in-progress、completed、no-answer、failed、rejected |
| direction | enum | inbound、outbound |
| inbox_id | integer | 按 Inbox 筛选 |
| agent_id | integer | 按接听成员筛选 |
| since | integer | 起始 Unix 时间 |
| until | integer | 结束 Unix 时间 |
| page | integer | 页码，每页固定数量按版本提供 |

### 9.2 Call 响应字段

以下 id 至 contact 字段属于 payload 中的单条 Call。

| 字段 | 类型 | 说明 |
|---|---|---|
| meta.count | integer | 筛选结果总数 |
| meta.current_page | integer | 当前页 |
| meta.total_pages | integer | 总页数 |
| payload | array | Call 列表 |
| id | integer | Call ID |
| call_id | string | Provider Call ID |
| provider | enum | twilio、whatsapp |
| status | enum | ringing、in-progress、completed、no-answer、failed、rejected |
| direction | enum | inbound、outbound |
| duration_seconds | integer 或 null | 通话时长 |
| end_reason | string 或 null | 结束原因 |
| started_at | integer 或 null | 开始时间 |
| created_at | integer | 创建时间 |
| message_id | integer 或 null | 对应的通话消息 |
| recording_url | string 或 null | 录音地址 |
| transcript | string 或 null | 转写文本 |
| conversation | object | 会话 ID 和 display_id |
| inbox | object | Inbox 摘要 |
| agent | object 或 null | 接听成员摘要 |
| contact | object | 联系人摘要 |

普通成员只能读取自己处理且仍有权限访问的会话通话；Administrator 或具备报表权限的角色可以读取账号范围结果。

## 10. 联系人外呼与 Conference API（Enterprise）

| 方法 | 路径 | 功能 |
|---|---|---|
| POST | /api/v1/accounts/{account_id}/contacts/{contact_id}/call | 使用 Voice Inbox 发起外呼 |
| GET | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/conference/token | 获取当前成员会议 Token |
| POST | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/conference | 加入或创建通话会议 |
| DELETE | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/conference | 结束或离开会议 |

### 10.1 外呼字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| inbox_id | integer | 是 | 已启用 Voice 的 Inbox |
| conversation_id | integer | 否 | 可以复用的 open Conversation display_id |

### 10.2 外呼响应字段

| 字段 | 类型 | 说明 |
|---|---|---|
| conversation_id | integer | 新建或复用的会话 display_id |
| inbox_id | integer | Voice Inbox ID |
| call_sid | string | Provider Call SID |
| conference_sid | string | Conference SID |

### 10.3 Conference 字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| call_sid | string | POST、DELETE | Provider Call SID |
| conversation_id | integer | POST、DELETE | Conversation display_id |
| status | string | 响应 | success |
| id | integer | 响应 | Conversation display_id |
| conference_sid | string | POST 响应 | Conference SID |
| using_webrtc | boolean | POST 响应 | 是否使用 WebRTC |

只有属于当前 Voice Inbox 和 Conversation 的 Call 才能加入或结束会议。

## 11. WhatsApp Calling API（Enterprise）

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/whatsapp_calls/{id} | Call 详情 |
| POST | /api/v1/accounts/{account_id}/whatsapp_calls/initiate | 发起 WhatsApp Call |
| POST | /api/v1/accounts/{account_id}/whatsapp_calls/{id}/accept | 接听 |
| POST | /api/v1/accounts/{account_id}/whatsapp_calls/{id}/reject | 拒绝 |
| POST | /api/v1/accounts/{account_id}/whatsapp_calls/{id}/terminate | 结束 |
| POST | /api/v1/accounts/{account_id}/whatsapp_calls/{id}/upload_recording | 上传录音 |

### 11.1 WhatsApp Call 请求字段

| 字段 | 使用接口 | 说明 |
|---|---|---|
| conversation_id | initiate | Conversation display_id |
| sdp_offer | initiate | 发起端会话描述 |
| sdp_answer | accept | 接听端会话描述 |
| recording | upload_recording | 录音文件 |

### 11.2 WhatsApp Call 响应字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Call ID |
| call_id | string | Provider Call ID |
| provider | string | whatsapp |
| status | string | calling、ringing、in-progress 或终止状态 |
| direction | string | inbound 或 outbound |
| conversation_id | integer | 内部 Conversation ID，按响应提供 |
| inbox_id | integer | Inbox ID |
| message_id | integer 或 null | 通话消息 ID |
| accepted_by_agent_id | integer 或 null | 接听成员 |
| elapsed_seconds | integer | 已持续秒数 |
| caller | object | 联系人名称、电话和头像 |
| ice_servers | array | 媒体连接信息 |
| status | enum | 录音上传结果为 uploaded 或 already_uploaded，按接口场景解释 |

发起 WhatsApp Call 要求 Inbox 已启用 Calling、Conversation 可访问且 Contact 有电话号码。联系人未授权通话时，接口可能先触发权限请求，而不是立即建立通话。

## 12. 渠道与通话规则

- 授权地址、Access Token、Provider Secret、SDP 和录音都属于敏感数据；
- OAuth 入口生成成功不等于渠道创建成功；
- 模板提交成功不等于第三方审核通过；
- 通话状态应以服务端 Call 详情和实时事件为准；
- 同一个 Call 的 accept、reject、terminate 和录音上传需要处理重复请求；
- 通话结束后，录音和转写可能延迟出现；
- Inbox 类型、Provider 能力、企业授权和 Feature 必须同时满足。

各渠道的普通创建、更新、凭证和响应字段详见：[各渠道创建与更新字段字典](17-各渠道创建更新字段字典.md)。
