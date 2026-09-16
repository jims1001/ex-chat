# 联系人、公司与标签 API

> 导航：[全体功能与 API 总表](00-全体功能与API总表.md)｜[文档目录](README.md)

## 1. 功能范围

Contact 表示客户主体，ContactInbox 表示客户在某个渠道中的身份。Company 用于把多个 Contact 归入同一组织；Label 和 Custom Attribute 用于业务分类。

## 2. Contact API

| 方法 | 路径 | 功能 | 通常权限 |
|---|---|---|---|
| GET | /api/v1/accounts/{account_id}/contacts | 联系人列表 | Account 成员 |
| POST | /api/v1/accounts/{account_id}/contacts | 创建联系人 | 有联系人权限的成员 |
| GET | /api/v1/accounts/{account_id}/contacts/{contact_id} | 联系人详情 | 有联系人权限的成员 |
| PATCH | /api/v1/accounts/{account_id}/contacts/{contact_id} | 更新联系人 | 有联系人权限的成员 |
| DELETE | /api/v1/accounts/{account_id}/contacts/{contact_id} | 删除联系人 | 高权限角色 |
| GET | /api/v1/accounts/{account_id}/contacts/active | 活跃联系人 | Account 成员 |
| GET | /api/v1/accounts/{account_id}/contacts/search | 搜索联系人 | Account 成员 |
| POST | /api/v1/accounts/{account_id}/contacts/filter | 多条件筛选 | Account 成员 |
| POST | /api/v1/accounts/{account_id}/contacts/import | 导入联系人 | 允许导入的角色 |
| POST | /api/v1/accounts/{account_id}/contacts/export | 导出联系人 | 允许导出的角色 |

## 3. Contact 字段

| 字段 | 类型 | 可写 | 说明 |
|---|---|---:|---|
| id | integer | 否 | Contact ID |
| name | string | 是 | 客户姓名 |
| email | string 或 null | 是 | 邮箱 |
| phone_number | string 或 null | 是 | E.164 电话号码 |
| identifier | string 或 null | 是 | 外部业务唯一标识 |
| blocked | boolean | 是 | 是否阻止联系 |
| thumbnail | string 或 null | 否 | 头像地址 |
| avatar | file | 是 | 头像文件，按接口支持情况提交 |
| custom_attributes | object | 是 | 账号自定义字段值 |
| additional_attributes | object | 受限 | 城市、国家、语言等扩展信息 |
| contact_inboxes | array | 否 | 渠道身份列表 |
| companies | array | 否 | 所属公司，响应范围按接口而定 |
| labels | string array | 否 | 标签列表 |
| last_activity_at | time | 否 | 最后活动时间 |
| created_at | time | 否 | 创建时间 |

identifier 应在一个 Account 中保持稳定。email 和 phone_number 可能重复，不能在所有场景中作为唯一键。

## 4. Contact 子资源与命令

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/contacts/{contact_id}/conversations | 联系人会话 |
| POST | /api/v1/accounts/{account_id}/contacts/{contact_id}/contact_inboxes | 添加渠道身份 |
| GET/POST | /api/v1/accounts/{account_id}/contacts/{contact_id}/labels | 查看或设置标签 |
| GET | /api/v1/accounts/{account_id}/contacts/{contact_id}/notes | 联系人备注列表 |
| POST | /api/v1/accounts/{account_id}/contacts/{contact_id}/notes | 新增联系人备注 |
| GET | /api/v1/accounts/{account_id}/contacts/{contact_id}/notes/{note_id} | 联系人备注详情 |
| PATCH | /api/v1/accounts/{account_id}/contacts/{contact_id}/notes/{note_id} | 更新联系人备注 |
| DELETE | /api/v1/accounts/{account_id}/contacts/{contact_id}/notes/{note_id} | 删除联系人备注 |
| GET | /api/v1/accounts/{account_id}/contacts/{contact_id}/attachments | 联系人相关附件 |
| GET | /api/v1/accounts/{account_id}/contacts/{contact_id}/contactable_inboxes | 可联系 Inbox |
| POST | /api/v1/accounts/{account_id}/contact_inboxes/filter | 根据 Inbox 和 source_id 查找 Contact |
| POST | /api/v1/accounts/{account_id}/actions/contact_merge | 合并联系人；字段为 base_contact_id、mergee_contact_id |

### 4.1 ContactInbox 字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| id | integer | 响应 | ContactInbox ID |
| contact_id | integer | 是 | Contact ID |
| inbox_id | integer | 是 | Inbox ID |
| source_id | string | 是 | 客户在渠道中的身份 |
| pubsub_token | string | 响应 | 客户实时订阅令牌 |
| hmac_verified | boolean | 响应 | Widget 身份是否验证 |

同一个 Inbox 中 source_id 应能唯一定位一个客户身份。不同 Inbox 可以使用相同 source_id。

ContactInbox Filter 请求字段为 inbox_id 和 source_id；成功时返回匹配 Contact，未匹配时返回 404。

### 4.2 Contact Note 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 备注 ID |
| content | string | 备注正文 |
| user | object | 创建人 |
| created_at | time | 创建时间 |
| updated_at | time | 更新时间 |

备注属于内部客户信息，不应展示给 Contact。

## 5. Company API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/companies | 公司列表 |
| POST | /api/v1/accounts/{account_id}/companies | 创建公司 |
| GET | /api/v1/accounts/{account_id}/companies/{id} | 公司详情 |
| PATCH | /api/v1/accounts/{account_id}/companies/{id} | 更新公司 |
| DELETE | /api/v1/accounts/{account_id}/companies/{id} | 删除公司 |
| GET | /api/v1/accounts/{account_id}/companies/search | 搜索公司 |
| GET | /api/v1/accounts/{account_id}/companies/{id}/contacts | 查看公司联系人 |
| POST | /api/v1/accounts/{account_id}/companies/{id}/contacts | 添加公司联系人 |
| DELETE | /api/v1/accounts/{account_id}/companies/{id}/contacts/{contact_id} | 移除公司联系人 |
| GET | /api/v1/accounts/{account_id}/companies/{id}/conversations | 公司会话 |
| GET | /api/v1/accounts/{account_id}/companies/{id}/notes | 公司联系人备注汇总 |
| POST | /api/v1/accounts/{account_id}/companies/{id}/destroy_custom_attributes | 删除自定义字段值 |
| DELETE | /api/v1/accounts/{account_id}/companies/{id}/avatar | 删除头像 |

### 5.1 Company 字段

| 字段 | 类型 | 可写 | 说明 |
|---|---|---:|---|
| id | integer | 否 | Company ID |
| name | string | 是 | 公司名称 |
| description | string 或 null | 是 | 公司说明 |
| industry | string 或 null | 是 | 行业 |
| company_size | integer 或 string 或 null | 是 | 公司规模，按版本字段类型处理 |
| country | string 或 null | 是 | 国家 |
| website | string 或 null | 是 | 网站 |
| custom_attributes | object | 是 | 公司自定义字段 |
| avatar_url | string 或 null | 否 | 头像地址 |
| created_at | time | 否 | 创建时间 |

## 6. Label API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/labels | 标签列表 |
| POST | /api/v1/accounts/{account_id}/labels | 创建标签 |
| GET | /api/v1/accounts/{account_id}/labels/{id} | 标签详情 |
| PATCH | /api/v1/accounts/{account_id}/labels/{id} | 更新标签 |
| DELETE | /api/v1/accounts/{account_id}/labels/{id} | 删除标签 |

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Label ID |
| title | string | 标签名称 |
| description | string 或 null | 标签说明 |
| color | string | 颜色 |
| show_on_sidebar | boolean | 是否显示在侧栏 |

Contact 标签和 Conversation 标签使用同一标签目录，但绑定关系不同。

## 7. Custom Attribute Definition API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/custom_attribute_definitions | 字段定义列表 |
| POST | /api/v1/accounts/{account_id}/custom_attribute_definitions | 创建字段定义 |
| GET | /api/v1/accounts/{account_id}/custom_attribute_definitions/{id} | 字段定义详情 |
| PATCH | /api/v1/accounts/{account_id}/custom_attribute_definitions/{id} | 更新字段定义 |
| DELETE | /api/v1/accounts/{account_id}/custom_attribute_definitions/{id} | 删除字段定义 |

### 7.1 字段定义

| 字段 | 类型 | 说明 |
|---|---|---|
| attribute_display_name | string | 显示名称 |
| attribute_display_type | enum | text、number、currency、percent、link、date、list、checkbox 等 |
| attribute_key | string | 稳定字段键 |
| attribute_model | enum | contact_attribute、conversation_attribute 等 |
| attribute_description | string 或 null | 说明 |
| attribute_values | string array | list 类型的可选值 |
| attribute_regex | string 或 null | 格式规则，按版本支持 |

attribute_key 建立后应保持稳定。删除定义是否同时删除已有值，应在执行前确认接口说明和目标版本行为。

## 8. 查询与筛选字段

| 字段 | 类型 | 说明 |
|---|---|---|
| q | string | 搜索关键词 |
| page | integer | 页码 |
| sort | string | 排序方式 |
| include_contact_inboxes | boolean | 是否包含渠道身份，按接口支持 |
| filter_operator | enum | AND 或 OR |
| query_operator | enum | equal_to、not_equal_to、contains、is_present 等 |
| attribute_key | string | 过滤字段 |
| values | array | 比较值 |

## 9. 业务规则

- 所有 Contact、Company、Label 和字段定义必须属于当前 Account；
- 删除联系人前应确认会话、附件、渠道身份和外部系统影响；
- 合并联系人后使用目标 Contact ID，源 Contact 不应继续作为写入对象；
- 被 blocked 的 Contact 不应继续接收主动消息；
- phone_number 使用 E.164 格式；
- Contact 自定义字段和 Company 自定义字段不能混用；
- 导出包含客户信息，应限制权限和保留时间。

联系人批量操作和外部数据导入详见：[分配策略、筛选、导入与批量操作 API](14-分配策略筛选导入与批量操作API.md)。
