# API 请求与响应遗漏字段补充字典

> 适用版本：Chatwoot 4.16.0
> 本分册补齐公开接口定义中此前未被单独解释的字段，并固定字段的读写方向、空值、归属和业务含义。

## 1. 覆盖范围

Chatwoot 4.16.0 公开接口定义包含：

- 123 类请求、响应和公共对象；
- 766 个对象字段出现位置；
- 273 个去重后的字段名称；
- 此前各专题分册已经解释 234 个唯一字段；
- 本分册补充剩余 39 个唯一字段，使公开接口字段名称达到 273 / 273 可定位。

同名字段在多个对象中出现时，只统计一次名称，但仍需按对象语境解释。例如 available_name 同时出现在 User、Agent 和 Public Message Sender 中，含义接近，但可见范围不同。

## 2. 字段方向说明

| 方向 | 含义 | 调用规则 |
|---|---|---|
| 创建可写 | 创建对象时允许提交 | 未提交时采用默认值或保持空值 |
| 更新可写 | 更新对象时允许提交 | 缺失字段通常保持原值 |
| 响应只读 | 由业务状态、关系或统计产生 | 请求提交同名字段不应被当作有效写入 |
| 条件响应 | 只在特定 Feature、渠道、权限或对象状态下返回 | 缺失不等于 false 或空值 |
| 可空 | 字段可返回 null | 必须与字段缺失、空文本区分 |

## 3. Account 自动解决字段

| 字段 | 类型 | 方向 | 空值和默认 | 说明 |
|---|---|---|---|---|
| auto_resolve_after | integer 或 null | 更新可写 | null 表示未按该值启用 | 会话连续无活动达到指定分钟数后进入自动解决流程；允许范围为 10 至 1,439,856 分钟 |
| auto_resolve_ignore_waiting | boolean 或 null | 更新可写 | 未提交时保持当前设置 | 是否忽略处于等待客户或等待状态的会话；不能用字段缺失推断为 false |
| auto_resolve_message | string 或 null | 更新可写 | null 表示不发送自定义自动解决消息 | 自动解决前或过程中发送给客户的说明文本，是否实际发送还受 Inbox 和渠道能力限制 |

自动解决还要求 Account 对应 Feature 和定时处理能力可用。达到时间只是候选条件，不代表会话在该分钟精确同步改变状态。

## 4. User、Agent 和 Team 补充字段

### 4.1 User 与 Agent

| 字段 | 类型 | 方向 | 说明 |
|---|---|---|---|
| available_name | string 或 null | 响应只读 | 面向客户或公开消息显示的名称；可与内部 name 不同 |
| hmac_identifier | string 或 null | 条件响应 | 用于可验证身份场景的公开校验标识，不等同于 Secret 或 Contact Token |
| inviter_id | number 或 null | 响应只读 | 邀请该用户进入系统的 User ID；直接注册或历史数据可能为空 |

available_name 的显示优先级必须保持一致：存在公开名称时优先使用；不存在时再按对象类型回退到普通显示名称。不得把邮箱当作默认公开名称。

### 4.2 Team

| 字段 | 类型 | 方向 | 说明 |
|---|---|---|---|
| is_member | boolean | 响应只读 | 当前请求用户是否属于该 Team；它是相对于当前身份计算的值，不是 Team 的全局固定属性 |

同一个 Team 对不同用户返回的 is_member 可以不同，因此该字段不能在跨用户缓存中直接复用。

## 5. Agent Bot 补充字段

| 字段 | 类型 | 方向 | 空值和安全要求 | 说明 |
|---|---|---|---|---|
| bot_config | object | 创建和更新可写、响应可读 | 未配置时为空对象；不得放入需明文回显的 Secret | Bot 类型和行为所需的配置集合 |
| system_bot | boolean | 响应只读 | 普通 Account Bot 默认为非系统 Bot | 标识是否由安装范围提供；影响可编辑、删除和可见范围 |

system_bot 为 true 不代表该 Bot 自动绑定所有 Inbox。实际处理范围仍由 Inbox 绑定、Conversation assignee 和 Bot 状态决定。

## 6. Portal、Category 和 Article 补充字段

### 6.1 Portal 统计字段

| 字段 | 类型 | 方向 | 说明 |
|---|---|---|---|
| all_articles_count | integer | 响应只读 | 当前 Portal 范围内全部文章数量 |
| archived_articles_count | integer 或 null | 响应只读 | 已归档文章数量；旧数据或不提供该统计的入口可为空 |
| draft_articles_count | integer 或 null | 响应只读 | 草稿文章数量 |
| published_count | integer 或 null | 响应只读 | 已发布文章数量 |

这些计数必须使用同一 Portal、语言和权限范围解释。all_articles_count 不一定等于三个状态计数的简单相加，因为当前版本可能存在其他状态或筛选范围。

### 6.2 Portal Logo

| 字段 | 类型 | 方向 | 说明 |
|---|---|---|---|
| file_url | string | 响应只读 | Logo 文件访问地址；访问权限、有效期和删除后的结果按文件规则处理 |

file_url 是可访问入口，不是永久保存标识。更新 Logo 后旧地址不保证继续有效。

### 6.3 Category

| 字段 | 类型 | 方向 | 空值和约束 | 说明 |
|---|---|---|---|---|
| associated_category_id | integer 或 null | 创建、更新可写 | 无跨语言关联时为空 | 指向同一内容在其他语言或关联分类中的对应 Category |
| parent_category_id | integer 或 null | 创建、更新可写 | 顶级分类为空 | 建立分类层级；目标必须属于允许的 Portal 和 Account 范围 |
| position | integer 或 null | 创建、更新可写，响应可读 | 未提交时由排序规则决定 | Category 在同级列表中的位置 |

associated_category_id 表示内容关联，不等于父子关系。parent_category_id 表示层级，不用于跨语言映射。

### 6.4 Article

| 字段 | 类型 | 方向 | 空值和约束 | 说明 |
|---|---|---|---|---|
| associated_article_id | integer 或 null | 创建、更新可写 | 无关联文章时为空 | 指向同一主题的关联或其他语言 Article |
| author_id | integer 或 null | 创建、更新可写，响应可读 | 历史文章可能为空 | Article 作者的 User ID，必须属于允许的 Account 范围 |
| folder_id | integer 或 null | 条件响应 | 未使用文件夹能力时为空 | Article 所属 Folder；不能与 Category ID 混用 |
| position | integer 或 null | 创建、更新可写，响应可读 | 未明确排序时由列表规则决定 | Article 在 Category 或列表中的位置 |

删除关联 Article 不应静默删除当前 Article；应解除关联、返回空值或按帮助中心生命周期规则处理。

## 7. Automation 补充字段

| 字段 | 类型 | 方向 | 说明 |
|---|---|---|---|
| created_on | integer | 响应只读 | Automation Rule 的创建时间，使用 Unix 时间表达 |

created_on 与常见 created_at 表达同一类时间语义，但格式可能不同。比较时应统一时区和秒级单位，不得把字段名不同误判为两个创建时间。

## 8. Conversation 补充字段

| 字段 | 类型 | 方向 | 空值和状态 | 说明 |
|---|---|---|---|---|
| contact_last_seen_at | number 或 null | 响应只读 | Contact 尚未查看时可为空 | Contact 最近查看该 Conversation 的时间 |
| last_non_activity_message | object 或 null | 响应只读 | 尚无聊天消息时为空 | 最近一条非 activity Message 摘要 |
| sla_events | array | 条件响应 | 未应用 SLA 或无事件时为空数组或缺失 | 当前 Conversation 的 SLA 目标、命中和超时事件摘要 |

### 8.1 contact_last_seen_at

该字段与 agent_last_seen_at、assignee_last_seen_at 分属不同阅读主体。必须分别使用，不能用一个时间覆盖全部已读状态。

### 8.2 last_non_activity_message

该字段用于快速显示最近一条客户或成员聊天内容：

- activity Message 不作为目标；
- 删除消息后不得继续泄露原正文；
- 列表和详情可能返回不同丰富程度的 Message 摘要；
- 纯附件 Message 允许正文为空；
- 异步状态更新可能改变其 status，但不应改变 Conversation 归属。

### 8.3 sla_events

数组元素应能解释：

- SLA 事件类型；
- 目标时间；
- 是否已命中或超时；
- 事件时间；
- 关联 SLA Policy 或 Applied SLA；
- 工作时间口径。

SLA Feature 关闭、无权限或接口使用精简响应时，该字段可能不出现。

## 9. Message 补充字段

| 字段 | 类型 | 方向 | 空值和条件 | 说明 |
|---|---|---|---|---|
| campaign_id | integer 或 null | 创建时条件可写、响应可读 | 非 Campaign Message 为空 | 产生该 Message 的 Campaign ID |
| processed_message_content | string 或 null | 响应只读 | 未处理、纯附件或删除后可为空 | 经格式处理后用于展示或后续处理的正文 |
| sender_type | string 或 null | 响应只读 | 无明确发送者时可为空 | sender 对象类型，如 Contact、User、AgentBot |
| sentiment | object 或 null | 条件响应 | 未启用或未完成分析时为空 | Message 情绪分析结果 |

### 9.1 campaign_id

- 普通人工消息不得伪造为 Campaign Message；
- 一次性活动产生的消息应可反查 Campaign；
- 删除 Campaign 不应改变已产生 Message 的历史归属；
- Campaign 重试或补发应使用新的明确批次规则，不能覆盖历史 campaign_id。

### 9.2 processed_message_content

- 不等同于原始 content；
- 最大长度与 Message 正文相同，为 150000 个字符；
- 内容处理不得改变原消息方向和发送者；
- Message 脱敏后不得继续返回原处理文本；
- 接口调用方不应仅保存此字段而丢弃原始 content 和 content_attributes。

### 9.3 sender_type

sender_type 必须与 sender 对象匹配：

| sender_type | sender 含义 |
|---|---|
| Contact | 客户联系人 |
| User | Account 成员 |
| AgentBot | 自动处理 Bot |
| null | 系统活动或无法提供公开发送者 |

不能仅根据 message_type 推断 sender_type。outgoing Message 既可能由 User，也可能由 AgentBot 产生。

### 9.4 sentiment

sentiment 是可选分析对象，最低需要区分：

- 是否已分析；
- 情绪类别或评分；
- 分析时间或模型信息，如当前入口提供；
- 分析失败或不可用状态。

该字段缺失不应解释为“中性”。

## 10. Custom Attribute 补充字段

| 字段 | 类型 | 方向 | 适用范围 | 说明 |
|---|---|---|---|---|
| default_value | string 或 null | 创建、更新可写，响应可读 | 支持默认值的字段类型 | 新对象或新值未提交时的建议默认值 |
| regex_pattern | string 或 null | 创建、更新可写，响应可读 | text | 文本值需满足的正则校验表达式 |
| regex_cue | string 或 null | 创建、更新可写，响应可读 | text | regex_pattern 不匹配时提供的提示文本 |

规则：

- regex_pattern 和 regex_cue 只对 text 类型有实际意义；
- 改变 regex_pattern 不应自动改写历史值；
- 历史值不符合新规则时，应允许读取，并在后续写入时明确处理；
- 删除字段定义与删除业务对象上的已有值是不同动作；
- default_value 不得在每次读取时强行覆盖显式空值；
- 正则校验失败应返回对应字段错误，不得只返回通用失败。

## 11. Public Contact 补充字段

| 字段 | 类型 | 方向 | 空值和条件 | 说明 |
|---|---|---|---|---|
| label_list | array | 响应只读 | 无标签时为空数组 | 当前 Public Contact 可公开返回的标签标题列表 |
| middle_name | string 或 null | 条件响应 | 未提供时为空 | Contact 中间名或扩展身份名称 |

label_list 的可见范围必须服从 Public API 字段策略。普通 Account 内部标签不应因为同名字段而自动全部暴露给 Contact。

## 12. Public Inbox 补充字段

| 字段 | 类型 | 方向 | 说明 |
|---|---|---|---|
| identity_validation_enabled | boolean | 响应只读 | 是否强制执行 Widget 用户身份校验 |

当 identity_validation_enabled 为 true：

- 客户身份需提供可验证的 identifier 和校验信息；
- 校验失败不得建立受信任身份；
- 不能用前一个 Contact 的校验结果复用到另一个 Contact；
- 关闭校验后，既有受信任 Contact 不应被错误合并为匿名 Contact；
- 开关变化需要更新 Widget 配置并重新验证新会话行为。

## 13. Public Message Attachment 补充字段

| 字段 | 类型 | 方向 | 适用附件 | 说明 |
|---|---|---|---|---|
| coordinates_lat | number 或 null | 响应只读 | location | 纬度，范围应为 -90 至 90 |
| coordinates_long | number 或 null | 响应只读 | location | 经度，范围应为 -180 至 180 |
| fallback_title | string 或 null | 响应只读 | location、fallback、contact 等 | 当前客户端无法完整呈现附件时使用的标题 |
| transcribed_text | string 或 null | 条件响应 | audio | 音频转写文本 |

### 13.1 位置附件

- 经纬度必须成对解释；
- 只有一个坐标时不得假定另一个为 0；
- 超出范围的坐标应拒绝或不作为有效位置；
- fallback_title 不能替代精确坐标；
- 删除 Message 后不应继续公开位置附件。

### 13.2 音频转写

- 音频文件大于 25 MB 时不进入正常转写范围；
- transcribed_text 缺失可能表示未启用、未完成、失败或不支持；
- 转写文本不等同于客户原始输入，不能替换音频附件；
- 删除音频附件后不得继续通过普通客户入口返回转写原文；
- AI 或转写 Provider 的差异应在基线中记录。

## 14. Public Message Sender 补充字段

Public Message Sender 中的 available_name 为 string 或 null，用于向 Contact 显示成员或 Bot 的公开名称。

必须满足：

- 不返回不必要的内部资料；
- 名称变更只影响后续显示或按接口规则更新历史摘要；
- 不因 available_name 为空而暴露邮箱；
- AgentBot 和 User 的 sender_type 保持可区分；
- 私密备注不应出现在 Public Message 中。

## 15. Public Message Update 补充字段

| 字段 | 类型 | 方向 | 说明 |
|---|---|---|---|
| submitted_values | object | 更新可写 | Contact 对 Bot 交互消息、表单或选择项提交的答案 |

submitted_values 的处理规则：

- 必须关联当前 Contact 可见的目标 Message；
- 键和值必须符合原 Message 的 content_type 和 content_attributes；
- 同一个不可重复提交的交互不能产生多次业务副作用；
- 重复提交时应返回已有结果、明确冲突或按消息定义更新，不能随机处理；
- 不允许通过 submitted_values 修改 Message 的 sender、conversation_id 或 message_type；
- 敏感表单字段不得进入不必要的 Webhook 或实时事件；
- 提交结果应在 Message 详情和相关事件中保持一致。

## 16. 读写和可空汇总

| 分类 | 字段 |
|---|---|
| 更新可写 | auto_resolve_after、auto_resolve_ignore_waiting、auto_resolve_message |
| 创建与更新可写 | bot_config、associated_category_id、parent_category_id、position、associated_article_id、author_id、default_value、regex_pattern、regex_cue |
| 创建时条件可写 | campaign_id |
| 更新时交互可写 | submitted_values |
| 响应只读 | available_name、hmac_identifier、inviter_id、is_member、system_bot、created_on、contact_last_seen_at、last_non_activity_message、sla_events、processed_message_content、sender_type、sentiment、label_list、middle_name、identity_validation_enabled、coordinates_lat、coordinates_long、fallback_title、transcribed_text、file_url、Portal 计数字段 |
| 条件或历史可空 | inviter_id、Portal 分状态计数、关联 ID、author_id、folder_id、contact_last_seen_at、last_non_activity_message、campaign_id、processed_message_content、sender_type、sentiment、middle_name、位置标题和转写文本 |

## 17. 跨入口一致性要求

| 对象 | 需要对照的入口 | 一致字段 |
|---|---|---|
| Agent/User | 成员列表、详情、Message sender、Public sender | id、available_name、sender_type 的对应关系 |
| Portal | Portal 详情、Article 列表、公开帮助中心 | 状态计数、公开文章集合、语言范围 |
| Conversation | 列表、详情、Webhook、实时事件 | contact_last_seen_at、last_non_activity_message、sla_events |
| Message | 列表、详情、Webhook、Public API | campaign_id、processed_message_content、sender_type、sentiment |
| Custom Attribute | 定义列表、对象更新、筛选 | default_value、regex_pattern、regex_cue |
| Public Inbox | Widget 配置、Contact 创建、Conversation 创建 | identity_validation_enabled 和身份校验结果 |
| Attachment | Message 详情、Public Message、下载入口 | 坐标、fallback_title、transcribed_text 和删除状态 |

## 18. 字段错误处理

| 错误 | 预期结果 |
|---|---|
| auto_resolve_after 小于 10 或大于 1,439,856 | 422，指出范围错误 |
| parent_category_id 指向其他 Portal | 404 或 422，且不改变层级 |
| author_id 不属于当前 Account | 404 或 422，且不泄露成员资料 |
| regex_pattern 无效 | 422，字段定义不保存 |
| 文本不匹配 regex_pattern | 422，并提供 regex_cue 或字段错误 |
| coordinates_lat 或 coordinates_long 越界 | 422 或拒绝成为有效 location |
| submitted_values 不符合原交互结构 | 422，且不产生后续副作用 |
| campaign_id 不属于当前 Account | 404 或 422，不创建 Message |
| system_bot 被普通 Account 删除 | 按安装范围权限拒绝 |

## 19. 字段完整性验收

完成本分册验收必须满足：

1. 39 个补充字段均能定位到对象、类型和方向；
2. 同名字段在不同对象中的可见范围得到区分；
3. 可空、缺失、空文本、空数组和空对象没有混用；
4. 请求只读字段不会改变业务对象；
5. 跨 Account、Portal、Category、Conversation 和 Campaign 的 ID 被正确限制；
6. Public 响应不泄露内部身份或敏感配置；
7. 列表、详情、Webhook、实时事件和 Public API 的公共值最终一致；
8. 字段规模、正则、坐标和交互结构错误均有确定结果；
9. 公开接口定义中的 273 个唯一字段名称全部在 00–38 分册中可查询；
10. 版本变化后重新核对新增、删除和改变语义的字段。
