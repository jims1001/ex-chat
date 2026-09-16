# 报表、SLA、帮助中心与搜索 API

> 导航：[全体功能与 API 总表](00-全体功能与API总表.md)｜[文档目录](README.md)

## 1. 功能范围

本模块提供运营统计、服务时效、满意度、帮助中心内容和统一搜索。报表结果受时间范围、时区、用户权限和数据更新延迟影响。

## 2. Report API V2

所有路径位于 /api/v2/accounts/{account_id} 下。

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /reports | 基础报表序列 |
| GET | /reports/summary | 汇总指标 |
| GET | /reports/bot_summary | Bot 汇总 |
| GET | /reports/agents | Agent 指标 |
| GET | /reports/inboxes | Inbox 指标 |
| GET | /reports/labels | Label 指标 |
| GET | /reports/teams | Team 指标 |
| GET | /reports/conversations | 会话明细 |
| GET | /reports/conversations_summary | 会话汇总 |
| GET | /reports/conversation_traffic | 会话流量 |
| GET | /reports/drilldown | 指标下钻 |
| GET | /reports/bot_metrics | Bot 指标 |
| GET | /reports/inbox_label_matrix | Inbox 与 Label 矩阵 |
| GET | /reports/first_response_time_distribution | 首次响应分布 |
| GET | /reports/outgoing_messages_count | Outgoing 数量 |

## 3. Summary Report API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v2/accounts/{account_id}/summary_reports/agent | Agent 汇总 |
| GET | /api/v2/accounts/{account_id}/summary_reports/team | Team 汇总 |
| GET | /api/v2/accounts/{account_id}/summary_reports/inbox | Inbox 汇总 |
| GET | /api/v2/accounts/{account_id}/summary_reports/label | Label 汇总 |
| GET | /api/v2/accounts/{account_id}/summary_reports/channel | Channel 汇总 |

## 4. Report Query 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| since | integer 或 date | 起始时间 |
| until | integer 或 date | 结束时间 |
| type | enum | account、agent、team、inbox、label 等 |
| id | integer | 对应维度 ID |
| business_hours | boolean | 是否只统计工作时间 |
| metric | string | 下钻指标 |
| page | integer | 明细页码 |
| timezone | string | 部分接口支持；否则使用 Account 报表时区 |

## 5. Report 指标字段

| 指标 | 类型 | 说明 |
|---|---|---|
| conversations_count | integer | 会话数量 |
| incoming_messages_count | integer | Incoming Message 数量 |
| outgoing_messages_count | integer | Outgoing Message 数量 |
| avg_first_response_time | number | 平均首次响应时间 |
| avg_response_time | number | 平均响应时间 |
| avg_resolution_time | number | 平均解决时间 |
| resolutions_count | integer | 解决数量 |
| bot_resolutions_count | integer | Bot 解决数量，按功能提供 |
| reply_time | number | 回复时长指标 |
| trend | number 或 object | 与上一周期比较 |

时间指标的单位必须按具体接口确认，不能仅根据字段名推断为秒或分钟。

## 6. Live Report API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v2/accounts/{account_id}/live_reports/conversation_metrics | 实时会话指标 |
| GET | /api/v2/accounts/{account_id}/live_reports/grouped_conversation_metrics | 按维度分组的实时指标 |

实时指标用于运营监控，不应替代结算或长期统计数据。

### 6.1 Year in Review API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v2/accounts/{account_id}/year_in_review | 获取当前成员的年度服务摘要 |

| 字段 | 类型 | 说明 |
|---|---|---|
| year | integer | Query 年份；省略时使用当前版本默认年份 |
| total_conversations | integer | 该成员年度负责的会话数 |
| busiest_day.date | string 或 null | 最繁忙日期 |
| busiest_day.count | integer 或 null | 当日会话数 |
| support_personality.avg_response_time_seconds | integer | 平均首次响应秒数 |

Year in Review 是当前成员视角，不等同于整个 Account 的年度报表。

## 7. SLA Policy API（Enterprise）

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/sla_policies | SLA 列表 |
| POST | /api/v1/accounts/{account_id}/sla_policies | 创建 SLA |
| GET | /api/v1/accounts/{account_id}/sla_policies/{id} | SLA 详情 |
| PATCH | /api/v1/accounts/{account_id}/sla_policies/{id} | 更新 SLA |
| DELETE | /api/v1/accounts/{account_id}/sla_policies/{id} | 删除 SLA |

### 7.1 SLA 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | SLA ID |
| name | string | 名称 |
| description | string 或 null | 说明 |
| first_response_time_threshold | integer | 首次响应阈值 |
| next_response_time_threshold | integer | 后续响应阈值 |
| resolution_time_threshold | integer | 解决阈值 |
| only_during_business_hours | boolean | 是否只计算工作时间 |
| conditions | array | 适用条件 |

阈值单位应以目标接口返回和界面设置为准。

## 8. Applied SLA API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/applied_slas | SLA 应用记录 |
| GET | /api/v1/accounts/{account_id}/applied_slas/metrics | SLA 指标 |
| GET | /api/v1/accounts/{account_id}/applied_slas/download | 下载 SLA 数据 |

常用 Query：since、until、inbox_id、team_id、agent_id、page。

## 9. CSAT API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/csat_survey_responses | 满意度响应列表 |
| GET | /api/v1/accounts/{account_id}/csat_survey_responses/metrics | 满意度指标 |
| GET | /api/v1/accounts/{account_id}/csat_survey_responses/download | 下载结果 |
| PATCH | /api/v1/accounts/{account_id}/csat_survey_responses/{id} | 更新可管理字段，Enterprise |
| GET | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/csat_template | Inbox CSAT 模板 |
| POST | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/csat_template | 创建或更新模板 |

### 9.1 CSAT 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Response ID |
| rating | integer | 评分 |
| feedback_message | string 或 null | 文字反馈 |
| conversation_id | integer | 会话 ID |
| contact | object | 客户摘要 |
| assigned_agent | object 或 null | Agent 摘要 |
| created_at | time | 提交时间 |

## 10. Search API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/search | 综合搜索 |
| GET | /api/v1/accounts/{account_id}/search/conversations | 搜索会话 |
| GET | /api/v1/accounts/{account_id}/search/messages | 搜索消息 |
| GET | /api/v1/accounts/{account_id}/search/contacts | 搜索联系人 |
| GET | /api/v1/accounts/{account_id}/search/articles | 搜索文章 |

| 字段 | 类型 | 说明 |
|---|---|---|
| q | string | 搜索关键词 |
| page | integer | 页码 |
| sort | string | 排序方式，按资源支持 |
| inbox_id | integer | Inbox 过滤 |
| status | string | 状态过滤 |

搜索结果仍受 Account、角色和 Inbox 权限限制。

## 11. Portal API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/portals | Portal 列表 |
| POST | /api/v1/accounts/{account_id}/portals | 创建 Portal |
| GET | /api/v1/accounts/{account_id}/portals/{id} | Portal 详情 |
| PATCH | /api/v1/accounts/{account_id}/portals/{id} | 更新 Portal |
| DELETE | /api/v1/accounts/{account_id}/portals/{id} | 删除 Portal |
| PATCH | /api/v1/accounts/{account_id}/portals/{id}/archive | 归档 Portal |
| DELETE | /api/v1/accounts/{account_id}/portals/{id}/logo | 删除 Logo |
| GET | /api/v1/accounts/{account_id}/portals/{id}/ssl_status | 自定义域名 SSL 状态 |
| POST | /api/v1/accounts/{account_id}/portals/{id}/send_instructions | 发送自定义域名设置说明 |

### 11.1 Portal 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Portal ID |
| name | string | 名称 |
| slug | string | 公开地址标识 |
| custom_domain | string 或 null | 自定义域名 |
| color | string | 主题颜色 |
| homepage_link | string 或 null | 主页链接 |
| page_title | string 或 null | 页面标题 |
| header_text | string 或 null | 页头文本 |
| archived | boolean | 是否归档 |
| locales | string array | 支持语言 |
| config.default_locale | string | 默认语言 |
| config.allowed_locales | string array | 可公开语言 |
| config.draft_locales | string array | 草稿语言 |
| config.layout | string 或 null | Portal 布局 |
| config.social_profiles | object | 社交平台链接 |
| config.locale_translations | object | 各语言的名称、标题和页头文本 |
| config.popular_content | object | 各语言的热门分类和文章 |

send_instructions 请求字段为 email。只有 Portal 已配置 custom_domain 时才能发送。

## 12. Category 与 Article API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET/POST | /api/v1/accounts/{account_id}/portals/{portal_id}/categories | 分类列表或创建 |
| GET/PATCH/DELETE | /api/v1/accounts/{account_id}/portals/{portal_id}/categories/{id} | 分类详情、更新或删除 |
| POST | /api/v1/accounts/{account_id}/portals/{portal_id}/categories/reorder | 分类排序 |
| GET/POST | /api/v1/accounts/{account_id}/portals/{portal_id}/articles | 文章列表或创建 |
| GET/PATCH/DELETE | /api/v1/accounts/{account_id}/portals/{portal_id}/articles/{id} | 文章详情、更新或删除 |
| POST | /api/v1/accounts/{account_id}/portals/{portal_id}/articles/reorder | 文章排序 |

### 12.1 Article 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Article ID |
| title | string | 标题 |
| description | string 或 null | 摘要 |
| content | string | 正文 |
| slug | string | 公开地址标识 |
| status | enum | draft、published、archived 等 |
| locale | string | 语言 |
| category_id | integer 或 null | 分类 |
| author | object | 作者摘要 |
| views | integer | 浏览量，按响应提供 |
| created_at | time | 创建时间 |
| updated_at | time | 更新时间 |

## 13. Public Help Center

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /hc/{slug} | Portal 首页 |
| GET | /hc/{slug}/sitemap.xml | Portal Sitemap |
| GET | /hc/{slug}/{locale}/search | 公开搜索 |
| GET | /hc/{slug}/{locale}/articles | 公开文章列表 |
| GET | /hc/{slug}/{locale}/categories | 公开分类列表 |
| GET | /hc/{slug}/{locale}/categories/{category_slug} | 分类详情 |
| GET | /hc/{slug}/{locale}/categories/{category_slug}/articles | 分类下文章列表 |
| GET | /hc/{slug}/articles/{article_slug} | 公开文章 |
| GET | /hc/{slug}/articles/{article_slug}.md | Markdown 文章内容 |
| GET | /hc/{slug}/articles/{article_slug}.png | 文章访问统计像素 |
| GET | /.well-known/cf-custom-hostname-challenge/{id} | 自定义域名所有权验证 |

公开接口只应返回已发布且属于对应 Portal 和语言的内容。

自定义域名验证使用当前请求域名查找 Portal，并要求路径 id 与 Portal 的验证标识一致；成功返回验证正文，域名或 id 不匹配返回 404。统计像素只用于记录文章访问，不返回文章正文。

## 14. 数据解释规则

- 报表时间范围按账号报表时区和接口参数解释；
- 当前期与上一期比较必须使用相同长度和同一筛选条件；
- 实时报表与历史报表可能存在短暂差异；
- 删除 Agent、Team 或 Label 后，历史指标仍可能保留原维度信息；
- SLA 只在适用条件满足时生效；
- CSAT 评分与文字反馈属于敏感客户数据；
- Draft 和 Archived Article 不能通过公开帮助中心访问。

指标公式、秒数口径、营业时间、分组维度、Bot 指标和空值规则详见：[报表指标口径、筛选与返回字典](20-报表指标口径筛选与返回字典.md)。Reporting Event、Audit Log 和帮助中心批量操作分别详见：[审计、企业设置、计费与平台迁移 API](16-审计企业设置计费与平台迁移API.md)和[分配策略、筛选、导入与批量操作 API](14-分配策略筛选导入与批量操作API.md)。
