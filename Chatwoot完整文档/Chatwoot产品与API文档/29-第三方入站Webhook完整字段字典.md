# Chatwoot 第三方入站 Webhook 完整字段字典

> 导航：[渠道状态、错误、重新授权与回调字典](26-渠道状态错误重新授权与回调字典.md)｜[Webhook 与实时事件字段字典](19-Webhook与实时事件字段字典.md)

## 1. 文档范围

本分册说明第三方平台向 Chatwoot 投递事件时实际使用的路径、Header、路径参数、Payload 字段、支持事件、校验方式和处理结果。它与 Account Webhook 的方向相反：本分册是第三方向 Chatwoot 入站，19 分册是 Chatwoot 向外部系统出站。

## 2. 入站回调总表

| 平台 | 方法与路径 | 主要事件 |
|---|---|---|
| Facebook Messenger | GET、POST /bot | 验证、消息、附件、送达、已读和 Echo |
| Instagram | GET、POST /webhooks/instagram | 验证、消息、已读、Echo 和测试事件 |
| TikTok | POST /webhooks/tiktok | 收到消息、发出消息、会话已读 |
| X/Twitter | GET、POST /webhooks/twitter | CRC、私信和 Tweet |
| WhatsApp | GET、POST /webhooks/whatsapp/{phone_number} | 验证、消息、状态和 SMB Echo |
| LINE | POST /webhooks/line/{line_channel_id} | 消息和附件 |
| Telegram | POST /webhooks/telegram/{bot_token} | 私聊、Business Message、编辑、附件和按钮回调 |
| 通用 SMS | POST /webhooks/sms/{phone_number} | message-received、message-delivered、message-failed |
| Twilio Messaging | POST /twilio/callback | SMS、WhatsApp、媒体、位置和 Referral |
| Twilio Delivery | POST /twilio/delivery_status | 消息送达和失败 |
| Twilio Voice | POST /twilio/voice/*/{phone} | 呼叫、状态、会议和录音 |
| Slack | POST /api/v1/integrations/webhooks | URL 验证、Thread 消息、文件和 Link Shared |
| Shopify | POST /webhooks/shopify | shop/redact |
| Stripe | POST /enterprise/webhooks/stripe | 计费事件 |
| Firecrawl | POST /enterprise/webhooks/firecrawl | Captain crawl.page |

## 3. 通用接收规则

| 规则 | 说明 |
|---|---|
| 原始请求体 | HMAC 或签名校验必须使用第三方实际发送的原始内容 |
| 快速响应 | 通过基础校验后尽快返回 2xx，消息转换可以异步完成 |
| 幂等 | 使用第三方 Event ID、Message ID、Order ID 或状态对象 ID 去重 |
| 乱序 | 送达、已读、会议和录音事件可能晚到或乱序 |
| Account 边界 | 通过号码、Page、Bot、Channel 或签名定位 Inbox，不能由 Payload 任意指定 account_id |
| 未知事件 | 不创建业务对象；可以返回 2xx 防止第三方无效重试 |
| 敏感信息 | Header 签名、Bot Token、Verify Token、Access Token 和客户原始附件不得记录到普通业务日志 |

## 4. Meta 验证与签名

Instagram 和 WhatsApp 使用相同的 Meta 验证约定。

### 4.1 GET 验证字段

| Query 字段 | 必填 | 说明 |
|---|---:|---|
| hub.mode | 是 | 通常为 subscribe |
| hub.verify_token | 是 | 与安装或 Channel 配置的 Verify Token 比较 |
| hub.challenge | 是 | 验证成功后原样返回 |

Verify Token 不匹配时返回 401 和错误摘要。

### 4.2 POST 签名 Header

| Header | 格式 | 说明 |
|---|---|---|
| X-Hub-Signature-256 | sha256={hex_digest} | 使用 App Secret 对原始请求体计算 HMAC-SHA256 |

Instagram 可以使用直接 Instagram App Secret、Facebook App Secret 或对应 Channel 保存的 App Secret。WhatsApp Cloud 在 Embedded Signup 或 Channel 已保存 App Secret 时强制校验签名。

## 5. Facebook Messenger

Facebook Messenger 事件由 /bot 接收。主要 Payload 层次如下：

| 字段 | 说明 |
|---|---|
| object | 通常为 page |
| entry | Page 事件数组 |
| entry[].id | Page ID，用于定位 Facebook Page Channel |
| entry[].time | 批次时间 |
| entry[].messaging | 消息事件数组 |
| entry[].standby | Standby 事件数组 |

### 5.1 Messaging 字段

| 字段 | 说明 |
|---|---|
| sender.id | 客户或 Page ID |
| recipient.id | Page 或客户 ID |
| timestamp | 事件时间，毫秒 |
| message.mid | Provider Message ID，也是消息去重 ID |
| message.seq | Provider 序号，存在时返回 |
| message.text | 文本内容 |
| message.attachments | 附件数组 |
| message.quick_reply.payload | Quick Reply 值 |
| message.is_echo | 是否为 Page 发出消息的 Echo |
| message.app_id | 发送应用 ID，存在时用于识别来源 |
| message.reply_to.mid | 被回复的 Provider Message ID |
| delivery.watermark | 已送达时间水位 |
| read.watermark | 已读时间水位 |

附件应保留 type、payload.url 和第三方附加信息。相同 message.mid 只能创建一条 Chatwoot Message。

## 6. Instagram

### 6.1 顶层字段

| 字段 | 必填 | 说明 |
|---|---:|---|
| object | 是 | 必须为 instagram，否则返回 422 |
| entry | 是 | Instagram 账号事件数组 |
| entry[].id | 是 | Instagram 业务账号 ID |
| entry[].time | 否 | 事件时间 |
| entry[].messaging | 实际消息常用 | 消息数组 |
| entry[].standby | 特定 Page 模式 | Standby 消息数组 |
| entry[].changes | 测试事件常用 | 测试或变更数据 |

### 6.2 消息字段

| 字段 | 说明 |
|---|---|
| sender.id | 客户 ID；Echo 时也可用于识别业务账号 |
| recipient.id | Instagram 业务账号 ID；Echo 时为客户 ID |
| timestamp | 事件时间 |
| message.mid | Provider Message ID |
| message.text | 文本 |
| message.attachments | 图片、视频等附件 |
| message.is_echo | 是否为业务账号发出的消息 |
| message.reply_to.mid | 被回复的消息 ID，存在时使用 |
| read.watermark | 已读时间水位 |

当前支持 message 和 read。Echo 事件延迟处理，用于避免发送响应尚未完成时产生重复消息。changes[].value 形式主要用于第三方测试事件。

## 7. TikTok

### 7.1 签名 Header

| Header | 格式 | 说明 |
|---|---|---|
| Tiktok-Signature | t={unix_timestamp},s={hex_digest} | 使用 TIKTOK_APP_SECRET 对 timestamp + 点号 + 原始请求体计算 HMAC-SHA256 |

缺少 Header、Secret、timestamp 或 signature 返回 401。当前时间与签名 timestamp 相差超过 5 秒也返回 401。

### 7.2 顶层字段

| 字段 | 类型 | 说明 |
|---|---|---|
| event | enum | im_send_msg、im_receive_msg、im_mark_read_msg |
| user_openid | string | TikTok Business ID，用于定位 Channel |
| content | string | JSON 字符串，解析后得到消息或已读字段 |

### 7.3 content 消息字段

| 字段 | 说明 |
|---|---|
| type | text、image、share_post 或其他类型 |
| message_id | Provider Message ID |
| timestamp | 毫秒时间 |
| conversation_id | TikTok 会话 ID |
| from | 发送方展示标识 |
| from_user.id | 发送方 User ID |
| to | 接收方展示标识 |
| to_user.id | 接收方 User ID |
| text.body | 文本内容 |
| image.media_id | 图片媒体 ID |
| share_post.embed_url | 分享内容嵌入地址 |
| referenced_message_info.referenced_message_id | 被回复的消息 ID |

text、image 和 share_post 会转换为对应 Message；其他类型会保留消息并标记 is_unsupported。im_send_msg 按外发 Echo 处理。

### 7.4 content 已读字段

| 字段 | 说明 |
|---|---|
| conversation_id | TikTok 会话 ID |
| from_user.id | 发起已读事件的 User ID |
| read.last_read_timestamp | 最后已读时间，毫秒 |

业务账号自己产生的已读事件不反向更新客户已读状态。

## 8. X/Twitter

### 8.1 CRC 验证

| 方法 | Query | 响应 |
|---|---|---|
| GET /webhooks/twitter | crc_token | response_token，格式为 sha256={计算结果} |

### 8.2 事件字段

| 字段 | 说明 |
|---|---|
| for_user_id | 接收事件的账号 ID |
| direct_message_events | 私信事件数组 |
| tweet_create_events | Tweet 创建事件数组 |
| users | User ID 到用户资料的映射 |
| apps | App 资料映射，存在时使用 |

当前只处理 direct_message_events 和 tweet_create_events，其他键被忽略。单个事件的 ID 用于避免重复创建消息或 Tweet 事件。

## 9. WhatsApp Cloud

### 9.1 路径和顶层字段

| 字段 | 位置 | 说明 |
|---|---|---|
| phone_number | Path | Inbox 号码；Business Account Payload 也会从 metadata 重新定位 |
| object | Body | whatsapp_business_account |
| entry | Body | Business Account 事件数组 |
| entry[].id | Body | WABA ID |
| entry[].changes | Body | 变更数组 |
| changes[].field | Body | messages 或 smb_message_echoes |
| changes[].value | Body | 消息、状态、联系人和 metadata |

### 9.2 metadata

| 字段 | 说明 |
|---|---|
| display_phone_number | 展示号码；没有加号时会规范化 |
| phone_number_id | Provider 号码 ID，必须与 Channel 配置匹配 |

### 9.3 contacts

| 字段 | 说明 |
|---|---|
| wa_id | 联系人 WhatsApp ID 或电话号码 |
| user_id | Business-Scoped User ID，存在时作为候选身份 |
| parent_user_id | 父级 Business-Scoped User ID，优先用于稳定映射 |
| profile.name | 联系人名称 |
| profile.username | 用户名，存在时同步到客户附加信息 |

### 9.4 messages 通用字段

| 字段 | 说明 |
|---|---|
| id | Provider Message ID 和去重键 |
| from | 联系人号码 |
| from_user_id | Business-Scoped User ID |
| from_parent_user_id | 父级 Business-Scoped User ID |
| timestamp | Provider 时间 |
| type | 消息类型 |
| context.id | 被回复的消息 ID |
| referral | 广告或入口来源信息，原样保存在 content_attributes.referral |
| errors | 消息级错误数组 |

### 9.5 支持的消息类型

| type | 使用字段 | Chatwoot 结果 |
|---|---|---|
| text | text.body | 文本消息 |
| button | button.text | 按钮回复文本 |
| interactive | button_reply.title 或 list_reply.title | 交互回复文本 |
| image | image.id、caption、mime_type、filename | 图片附件和可选 caption |
| sticker | sticker.id | 图片附件 |
| audio、voice | id、mime_type | 音频附件 |
| video | id、caption、mime_type、filename | 视频附件 |
| document | id、caption、mime_type、filename | 文件附件 |
| location | latitude、longitude、name、address、url | Location 附件 |
| contacts | contacts[].name、phones | 一个或多个 Contact 附件 |
| unsupported | id、errors | 占位消息并设置 is_unsupported=true |

reaction、ephemeral 和 request_welcome 当前不创建消息。附件内容使用 Provider Media ID 再获取；Provider 返回未授权时会进入渠道授权错误处理。

### 9.6 contacts 消息对象

| 字段 | 说明 |
|---|---|
| contacts[].name.first_name | 名 |
| contacts[].name.last_name | 姓 |
| contacts[].name.formatted_name | 格式化名称 |
| contacts[].phones[].phone | 电话号码 |

一个 contacts 消息中有多个联系人时，会为每个联系人创建一条带 Contact Attachment 的消息，并共用父消息 id 作为来源标识。

### 9.7 statuses

| 字段 | 说明 |
|---|---|
| statuses[].id | 原始外发消息 ID |
| statuses[].status | sent、delivered、read 或 failed |
| statuses[].timestamp | 状态时间 |
| statuses[].recipient_id | 接收人 ID，存在时使用 |
| statuses[].recipient_user_id | Business-Scoped User ID |
| statuses[].recipient_parent_user_id | 父级 Business-Scoped User ID |
| statuses[].errors[].code | 失败错误码 |
| statuses[].errors[].title | 失败标题 |

failed 会把 code 和 title 写入消息 external_error。找不到对应 source_id 时不创建孤立状态消息。

### 9.8 SMB Message Echo

| 字段 | 说明 |
|---|---|
| changes[].field | smb_message_echoes |
| value.message_echoes | WhatsApp Business App 发出的消息数组 |
| message_echoes[].from | 业务号码 |
| message_echoes[].to | 联系人号码 |
| to_user_id、to_parent_user_id | 联系人 Business-Scoped User ID |

Echo 转换为 outgoing、delivered 消息，并设置 content_attributes.external_echo=true，避免再次发送到 Provider。

## 10. LINE

### 10.1 路径和 Header

| 项目 | 说明 |
|---|---|
| line_channel_id | Path；定位 LINE Channel |
| x-line-signature | 使用 Channel Secret 对原始请求体计算并 Base64 编码的 HMAC-SHA256 |

签名无效、Channel 不存在或 Account 不可用时不创建消息。

### 10.2 Payload 字段

| 字段 | 说明 |
|---|---|
| events | 事件数组 |
| events[].type | message 或 sticker |
| events[].source.userId | LINE User ID；用于获取资料和建立 ContactInbox |
| events[].timestamp | 事件时间 |
| events[].message.id | Provider Message ID |
| events[].message.type | text、sticker、image、video、audio 或 file |
| events[].message.text | 文本内容 |
| events[].message.stickerId | Sticker ID |
| events[].message.fileName | 文件名，文件消息存在时使用 |

text 创建文本消息；sticker 以可展示的 Sticker 地址保存；image、video、audio 和 file 使用 message.id 下载附件。当前要求 source.userId，群组或聊天室中无法取得该值的事件不会创建客户会话。

## 11. Telegram

### 11.1 路径和顶层字段

| 字段 | 说明 |
|---|---|
| bot_token | Path；定位 Telegram Channel |
| update_id | Telegram Update ID |
| message | 普通私聊消息 |
| business_message | Telegram Business 消息 |
| edited_message | 编辑后的普通消息 |
| edited_business_message | 编辑后的 Business 消息 |
| callback_query | 按钮回调 |

当前只处理 private Chat 和 callback_query，不处理群组对话。

### 11.2 User 和 Chat 字段

| 字段 | 说明 |
|---|---|
| message.from.id | 发送者 ID |
| message.from.first_name | 名 |
| message.from.last_name | 姓 |
| message.from.username | Telegram Username |
| message.from.language_code | 联系人语言 |
| message.chat.id | Chat ID |
| message.chat.type | 必须为 private，Business 模式除外 |
| business_connection_id | Telegram Business Connection ID |

### 11.3 消息字段

| 字段 | 说明 |
|---|---|
| message.message_id | Provider Message ID |
| message.text | 文本 |
| message.caption | 附件说明 |
| message.reply_to_message.message_id | 被回复的消息 ID |
| message.photo | 图片尺寸数组，使用最后一项 |
| message.sticker.thumb | Sticker 缩略图 |
| message.voice、message.audio | 音频对象 |
| message.video、message.video_note | 视频对象 |
| message.document | 文件对象，使用 mime_type 判断类型 |
| message.location.latitude、longitude | Location 坐标 |
| message.venue.title | Location 展示名 |
| message.contact.phone_number | Contact Attachment 电话 |
| message.contact.first_name、last_name | Contact Attachment 姓名 |

附件对象的 file_id 用于获取实际文件。Business Message 中发送者和客户方向根据 chat.id 与 from.id 判断，业务账号发出的消息保存为 outgoing。

### 11.4 编辑和按钮回调

| 事件 | 主要字段 | 行为 |
|---|---|---|
| edited_message | chat.id、message_id、text 或 caption | 更新已有消息内容 |
| edited_business_message | 同上 | 转换后更新已有 Business Message |
| callback_query | id、data、message.chat.id、business_connection_id | 将 data 作为消息内容处理 |

找不到原 ContactInbox、Conversation 或 source_id 时，不创建一条新的“编辑消息”。

## 12. 通用 SMS

通用 SMS 回调 Body 为事件数组，当前只处理第一项。

### 12.1 顶层字段

| 字段 | 说明 |
|---|---|
| type | message-received、message-delivered 或 message-failed |
| to | 目标号码，用于定位 Channel |
| message | 消息或状态对象 |

### 12.2 入站 message 字段

| 字段 | 说明 |
|---|---|
| id | Provider Message ID |
| from | 客户电话号码 |
| to | Inbox 电话号码 |
| text | SMS 文本 |
| media | 附件 URL 数组 |

.smil 和 .xml 媒体不保存为附件。其他媒体使用 Channel 凭证下载。

### 12.3 状态字段

| 字段 | 说明 |
|---|---|
| message.id | 原始外发消息 ID |
| message.type | message-delivered 或 message-failed |
| message.errorCode | 失败错误码 |
| message.description | 失败说明 |

message-delivered 映射为 delivered；message-failed 映射为 failed，并将 errorCode 与 description 写入 external_error。

## 13. Twilio Messaging 入站

### 13.1 通用字段

| 字段 | 说明 |
|---|---|
| ApiVersion | Twilio API 版本 |
| AccountSid | Twilio Account SID |
| SmsSid、MessageSid | Provider Message ID |
| MessagingServiceSid | Messaging Service ID |
| From、To | 发送和接收地址 |
| Body | 文本内容 |
| NumMedia | 附件数量 |
| FromCountry、FromState、FromCity、FromZip | 发送方位置摘要 |
| ToCountry、ToState、ToCity、ToZip | 接收方位置摘要 |

### 13.2 媒体字段

支持 MediaUrl0 至 MediaUrl9，以及对应的 MediaContentType0 至 MediaContentType9。没有 Body、MediaUrl0 且不是有效 Location 时，事件不创建消息。

### 13.3 WhatsApp Profile 和 Referral

| 字段 | 说明 |
|---|---|
| ProfileName | WhatsApp Profile 名称 |
| ExternalUserId、ParentExternalUserId | 外部用户标识 |
| ProfileUsername、Username | 用户名 |
| ReferralBody | Referral 文本 |
| ReferralHeadline | Referral 标题 |
| ReferralSourceId、ReferralSourceType、ReferralSourceUrl | 来源对象 |
| ReferralMediaId、ReferralMediaContentType、ReferralMediaUrl | Referral 媒体 |
| ReferralNumMedia | Referral 媒体数量 |
| ReferralCtwaClid | Click-to-WhatsApp 标识 |

### 13.4 Location

| 字段 | 使用条件 |
|---|---|
| MessageType | 必须为 location |
| Latitude | 必填 |
| Longitude | 必填 |

成功接收返回 204。

## 14. Twilio Delivery Status

| 字段 | 必填 | 说明 |
|---|---:|---|
| AccountSid | 否 | Twilio Account SID |
| From | 否 | 发送地址 |
| MessageSid | 是 | 原始外发消息 ID |
| MessagingServiceSid | 否 | Messaging Service ID |
| MessageStatus | 是 | Provider 状态 |
| ErrorCode | 失败时 | 错误码 |
| ErrorMessage | 失败时 | 错误说明 |

回调受理返回 204。状态只更新同一 Inbox 中匹配 MessageSid 的消息。

## 15. Twilio Voice

### 15.1 通用路径参数

| 字段 | 说明 |
|---|---|
| phone | 仅保留数字后加国际前缀，用于定位启用 Voice 的 Twilio Channel |

### 15.2 /voice/call/{phone}

| 字段 | 说明 |
|---|---|
| CallSid | 当前呼叫 ID |
| ParentCallSid | 父呼叫 ID，外呼 Dial 场景使用 |
| From、To | 呼叫双方 |
| Direction 或 CallDirection | inbound、outbound-api 或 outbound-dial |
| call_sid | Agent Leg 关联的原始 Call ID |

Inbox 关闭 inbound_calls_enabled 时，新的客户入站呼叫会被拒绝。Agent Leg 的 From 以 client: 开头。

### 15.3 /voice/status/{phone}

| 字段 | 说明 |
|---|---|
| CallSid | 呼叫 ID |
| CallStatus | Provider 呼叫状态 |
| Timestamp 等其他字段 | 作为 Provider 元信息保留 |

Provider 状态会规范化为 ringing、in_progress、completed、no_answer、failed 或 rejected。

### 15.4 /voice/conference_status/{phone}

| 字段 | 说明 |
|---|---|
| StatusCallbackEvent | conference-start、participant-join、participant-leave 或 conference-end |
| FriendlyName | Chatwoot 会议标识 |
| ConferenceSid | Twilio Conference ID |
| CallSid | 参与呼叫 ID |
| ParticipantLabel | Agent 或 contact 标签 |

未知 StatusCallbackEvent 返回 204 但不改变 Call。

### 15.5 /voice/recording_status/{phone}

主要使用 RecordingSid、RecordingUrl、RecordingStatus、RecordingDuration、ConferenceSid 和 CallSid。只有能定位到目标 Call 的录音结果才会关联会话。

## 16. Slack Events

### 16.1 URL Verification

| 字段 | 说明 |
|---|---|
| type | url_verification |
| challenge | 验证值；响应返回同名字段 |

### 16.2 Event Callback

| 字段 | 说明 |
|---|---|
| type | event_callback |
| event.type | message 或 link_shared |
| event.channel | Slack Channel ID，用于定位 Hook |
| event.user | Slack User ID；缺失时通常视为 Bot 或系统事件 |
| event.ts | Slack Message Timestamp 和去重标识 |
| event.thread_ts | 必须属于已映射 Chatwoot Conversation 的 Thread |
| event.text | 文本 |
| event.subtype | 仅空值或 file_share 被处理 |
| event.blocks | Rich Text Block 数组 |
| event.files | 文件数组 |
| event.links | Link Shared 列表 |
| event.unfurl_id | Link Unfurl ID |
| event.source | Link Unfurl 来源 |

Thread 消息只处理 rich_text 或有效文件。text 为 Attached File! 且仅作为文件占位时不会显示为普通消息。link_shared 进入异步 Unfurl 流程。

## 17. Shopify

### 17.1 Header

| Header | 说明 |
|---|---|
| X-Shopify-Topic | 事件类型，当前支持 shop/redact |
| X-Shopify-Hmac-SHA256 | 使用 SHOPIFY_CLIENT_SECRET 对原始请求体计算后 Base64 编码的 HMAC-SHA256 |

Secret 缺失、Header 缺失或签名不匹配返回 401。

### 17.2 shop/redact 字段

| 字段 | 说明 |
|---|---|
| shop_domain | 要删除连接的商店域名 |
| shop_id | Shopify Shop ID，存在时仅供事件参考 |

校验成功且 shop_domain 匹配时，删除该商店对应的 Shopify Hook。未知 Topic 返回 200 且不执行连接删除。

## 18. Stripe

| 项目 | 说明 |
|---|---|
| Header | Stripe-Signature |
| Body | Stripe Event 原始 JSON |
| 校验凭证 | 安装范围 Webhook Secret |
| 失败状态 | JSON 无效或签名不匹配返回 400 |
| 成功状态 | 处理完成返回 200 |

常用 Stripe Event 字段包括 id、type、created、livemode、data.object 和 request。业务处理以 event.type 和 data.object 为准；同一 event.id 必须保持幂等。

## 19. Firecrawl

| 字段 | 必填 | 说明 |
|---|---:|---|
| type | 是 | 当前处理 crawl.page |
| assistant_id | 是 | 目标 Captain Assistant ID |
| token | 是 | 与 Assistant 和 Account 绑定的回调 Token |
| success | 否 | 抓取是否成功 |
| id | 否 | Firecrawl 任务或页面 ID |
| metadata | 否 | 任务元信息 |
| format | 否 | 返回格式 |
| firecrawl | 否 | Provider 元信息 |
| data | 是 | 页面数组，当前读取第一项 |
| data[].markdown | 否 | 页面 Markdown 内容 |
| data[].metadata | 否 | 页面标题、URL、状态等元信息 |

Token 不匹配返回 401。type 不是 crawl.page 时返回 200，但不生成知识内容。

## 20. 回调状态与重试建议

| HTTP 状态 | 第三方应如何解释 |
|---|---|
| 200 或 204 | Chatwoot 已接收；业务对象可能仍在异步处理 |
| 400 | Payload 无法解析或签名格式错误，不应原样高频重试 |
| 401 | Verify Token、HMAC、签名、时间窗口或回调 Token 无效 |
| 404 | 路径资源、号码或自定义域不存在 |
| 422 | Payload 能解析但对象类型、号码状态或业务条件不允许 |
| 5xx | Chatwoot 暂时无法处理，第三方可按退避策略重试 |

## 21. 入站回调验收

- 每个渠道使用正确的生产回调路径和 Path 标识；
- 验证请求能返回第三方要求的 challenge；
- 修改一个字节后签名校验失败；
- 相同 Message ID 重复投递不会创建重复消息；
- 消息、附件、Location、Contact、Reply 和 Referral 能按支持范围保存；
- delivered、read 和 failed 不创建新消息，只更新原消息；
- Echo 不会再次发送回第三方；
- 未知事件不会写入错误 Account；
- Account suspended、Channel 不存在或需要重新授权时停止业务处理；
- 2xx 接收结果与最终 Message、Call、Document 或 Billing 状态分别检查。

各 Provider 在不同业务动作中使用的 API 版本、Scope、Token 和历史兼容规则见 [第三方 API 版本、能力漂移与历史兼容](41-第三方API版本能力漂移与历史兼容.md)。
