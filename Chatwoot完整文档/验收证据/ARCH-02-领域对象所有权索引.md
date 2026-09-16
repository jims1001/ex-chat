# ARCH-02 领域对象所有权索引

本索引以 `internal/domain` 中的 Go `struct` 为核对基线。业务对象只能登记一个权威模块；关系对象由负责该关系生命周期的模块拥有；DTO 不作为持久化权威对象。L0 原子日志是共享基础机制，不计入业务模块所有权分母，但必须登记其动态归属规则。

| # | 领域对象 | 定义位置 | 唯一所有者 | 分类 | 所有权说明 |
|---:|---|---|---|---|---|
| 1 | AICustomTool | models.go:1427 | AIC | 权威 | 自定义工具定义 |
| 2 | AIScenario | models.go:1323 | AIC | 权威 | 助手场景配置 |
| 3 | AIToolExecutionLog | models.go:1455 | AIC | 日志 | 工具执行审计记录 |
| 4 | AIUsageQuota | models.go:1335 | AIC | 权威 | 助手用量配额 |
| 5 | AccessLog | audit.go:170 | AUD | 日志 | 访问审计记录 |
| 6 | AccountBilling | models.go:1211 | BIL | 权威 | 租户计费资料 |
| 7 | AccountFeature | models.go:747 | ENT | 权威 | 租户功能权益 |
| 8 | AccountLimit | models.go:1198 | ENT | 权威 | 租户资源额度 |
| 9 | AccountSubscription | models.go:1650 | BIL | 权威 | 租户订阅状态 |
| 10 | AccountUser | models.go:203 | TEN | 关系 | 租户与用户成员关系 |
| 11 | Account | models.go:148 | TEN | 权威 | 租户主档 |
| 12 | AgentBotInbox | models.go:994 | EXT | 关系 | AgentBot 插件与 Inbox 授权范围 |
| 13 | AgentBot | models.go:979 | EXT | 权威 | AgentBot 扩展定义 |
| 14 | AgentCapacityPolicy | models.go:449 | RTE | 权威 | 坐席容量策略 |
| 15 | AppliedSLA | models.go:942 | RPT | 权威 | 会话 SLA 应用记录 |
| 16 | Article | models.go:592 | KB | 权威 | 知识文章原文 |
| 17 | AssignmentPolicy | models.go:477 | RTE | 权威 | 分配策略 |
| 18 | Attachment | models.go:1008 | MSG | 关系 | 消息附件引用；文件本体归 MED |
| 19 | AuditExport | audit.go:123 | AUD | 权威 | 审计导出任务 |
| 20 | AuditLog | audit.go:92 | AUD | 日志 | 业务审计记录 |
| 21 | AutomationRuleExecution | models.go:539 | AUT | 日志 | 自动化规则执行记录 |
| 22 | AutomationRule | models.go:525 | AUT | 权威 | 自动化规则定义 |
| 23 | BrandedEmailLayout | models.go:1234 | ENT | 权威 | 企业品牌邮件布局 |
| 24 | CSATSurvey | models.go:812 | QLT | 权威 | 客户满意度评价 |
| 25 | CallICECandidate | models.go:1627 | RTM | 关系 | 实时通话 ICE 候选 |
| 26 | Call | models.go:1054 | RTM | 权威 | 实时通话会话 |
| 27 | CampaignDelivery | models.go:710 | OPS | 日志 | Campaign 投递记录 |
| 28 | Campaign | models.go:892 | OPS | 权威 | 主动触达活动 |
| 29 | CannedResponse | models.go:408 | OPS | 权威 | 运营快捷回复 |
| 30 | CapacityPolicy | models.go:439 | RTE | 权威 | 路由容量策略 |
| 31 | CaptainAssistantResponse | models.go:1286 | AIC | 日志 | 助手响应记录 |
| 32 | CaptainAssistant | models.go:1258 | AIC | 权威 | 助手定义 |
| 33 | CaptainDocChunk | models.go:1312 | AIC | 投影 | 知识文档索引分块，可重建 |
| 34 | CaptainInbox | models.go:1275 | AIC | 关系 | 助手与 Inbox 使用策略 |
| 35 | CaptainKnowledgeDoc | models.go:1299 | AIC | 投影 | 知识文档索引元数据，可从 KB 重建 |
| 36 | Category | models.go:576 | KB | 权威 | 知识分类 |
| 37 | CompanyNote | models.go:876 | CUS | 权威 | 公司备注 |
| 38 | Company | models.go:860 | CUS | 权威 | 公司主档 |
| 39 | Conference | models.go:1076 | RTM | 权威 | 实时会议 |
| 40 | ContactFilterRequest | models.go:353 | API | 请求 DTO | Contact 查询条件，不持久化 |
| 41 | ContactInbox | models.go:279 | CUS | 关系 | 客户渠道身份关系 |
| 42 | ContactLabel | models.go:402 | CUS | 关系 | 客户标签使用关系 |
| 43 | ContactNote | models.go:512 | CUS | 权威 | 客户备注 |
| 44 | Contact | models.go:258 | CUS | 权威 | 客户主档 |
| 45 | ConversationFilterRequest | models.go:346 | API | 请求 DTO | Conversation 查询条件，不持久化 |
| 46 | ConversationLabel | models.go:396 | CON | 关系 | 会话标签使用关系 |
| 47 | ConversationParticipant | models.go:1044 | CON | 关系 | 会话参与者集合 |
| 48 | Conversation | models.go:292 | CON | 权威 | 会话状态机与时间线 |
| 49 | CopilotThreadMessage | models.go:1387 | AIC | 权威 | 辅助线程消息 |
| 50 | CopilotThread | models.go:1363 | AIC | 权威 | 辅助线程 |
| 51 | CustomAttributeDefinition | models.go:497 | CUS | 权威 | 客户自定义字段定义 |
| 52 | CustomFilter | models.go:1021 | SRH | 权威 | 保存的搜索过滤器 |
| 53 | CustomRole | models.go:1616 | TEN | 权威 | 租户自定义授权角色 |
| 54 | DashboardAppContentItem | models.go:624 | EXT | 投影 | Dashboard App 内容快照 |
| 55 | DashboardApp | models.go:631 | EXT | 权威 | Dashboard 扩展应用 |
| 56 | DataChangeCorrection | audit.go:157 | AUD | 权威 | 数据变更纠正记录 |
| 57 | DataImport | models.go:1149 | MIG | 权威 | 数据导入任务 |
| 58 | DraftMessage | models.go:1033 | MSG | 权威 | 消息草稿 |
| 59 | EmailChannelMigration | models.go:1245 | MIG | 权威 | 邮件渠道迁移任务 |
| 60 | EmailLog | email.go:15 | MSG | 日志 | 邮件消息投递记录 |
| 61 | FilterRule | models.go:338 | API | 请求 DTO | 查询过滤规则，不单独持久化 |
| 62 | InboxCapacityLimit | models.go:464 | RTE | 关系 | Inbox 路由容量限制 |
| 63 | InboxMember | models.go:250 | CHN | 关系 | Inbox 可见成员关系 |
| 64 | InboxMessageTemplate | models.go:1887 | CHN | 权威 | 渠道消息模板元数据 |
| 65 | Inbox | models.go:221 | CHN | 权威 | 渠道与 Inbox 主档 |
| 66 | IntegrationInstallation | models.go:687 | EXT | 权威 | 集成安装及授权范围 |
| 67 | IntegrityVerification | audit.go:141 | AUD | 日志 | 完整性验证记录 |
| 68 | Label | models.go:384 | CUS | 权威 | 标签定义 |
| 69 | LocalChangeJournal | audit.go:49 | N/A（L0 共享机制） | 基础机制 | 不属于 26 个业务模块；每条记录随 `source_module` 动态归属，不属于 AUD 永久账本 |
| 70 | MFAProfile | models.go:1127 | IAM | 权威 | 多因素认证配置 |
| 71 | Macro | models.go:758 | OPS | 权威 | 运营批量动作宏 |
| 72 | Message | models.go:360 | MSG | 权威 | 消息正文与发送状态 |
| 73 | MigrationJob | models.go:1168 | MIG | 权威 | 历史迁移任务 |
| 74 | NotificationSetting | models.go:794 | NTF | 权威 | 通知偏好设置 |
| 75 | NotificationSubscription | models.go:845 | NTF | 关系 | 推送订阅关系 |
| 76 | Notification | models.go:770 | NTF | 权威 | 通知及已读状态 |
| 77 | Onboarding | models.go:1222 | TEN | 权威 | 租户启用进度 |
| 78 | Order | models.go:1476 | BIL | 权威 | 订购与支付订单 |
| 79 | PasswordResetToken | models.go:1116 | IAM | 权威 | 密码重置凭证 |
| 80 | PlatformApp | models.go:834 | EXT | 权威 | 平台扩展应用 |
| 81 | Portal | models.go:561 | KB | 权威 | 知识门户配置 |
| 82 | PushDeliveryLog | models.go:722 | NTF | 日志 | 推送投递记录 |
| 83 | QAAppealActivity | models.go:1873 | QLT | 日志 | 质检申诉活动记录 |
| 84 | QAAppeal | models.go:1843 | QLT | 权威 | 质检申诉 |
| 85 | QACriterion | models.go:1760 | QLT | 权威 | 质检评分项 |
| 86 | QAEvaluationScore | models.go:1829 | QLT | 权威 | 质检单项评分 |
| 87 | QASamplingRule | models.go:1775 | QLT | 权威 | 质检抽样规则 |
| 88 | QAScorecard | models.go:1744 | QLT | 权威 | 质检评分卡 |
| 89 | QATask | models.go:1793 | QLT | 权威 | 质检任务与总评 |
| 90 | ReportingEvent | models.go:1183 | RPT | 投影 | 报表事实事件，可从业务事件重建 |
| 91 | RevokedToken | models.go:1138 | IAM | 权威 | 已撤销登录凭证 |
| 92 | SAMLSetting | models.go:1091 | IAM | 权威 | 单点登录设置 |
| 93 | SLABreachLog | models.go:698 | RPT | 日志 | SLA 违约事实记录 |
| 94 | SLAEvent | models.go:958 | RPT | 日志 | SLA 状态事件 |
| 95 | SLAPolicy | models.go:916 | RPT | 权威 | SLA 规则定义 |
| 96 | SecurityAuditLog | audit.go:110 | AUD | 日志 | 安全审计记录 |
| 97 | SubscriptionPlan | models.go:1637 | BIL | 权威 | 订阅套餐定义 |
| 98 | SystemConfig | models.go:739 | SYS | 权威 | 系统级配置 |
| 99 | TeamMember | models.go:431 | TEN | 关系 | 租户 Team 成员关系；RTE 只消费 team_id |
| 100 | Team | models.go:418 | TEN | 权威 | 租户 Team 主数据 |
| 101 | TicketActivity | models.go:1584 | TKT | 日志 | 工单活动时间线 |
| 102 | TicketAttachment | models.go:1556 | TKT | 关系 | 工单媒体引用；文件本体归 MED |
| 103 | TicketComment | models.go:1568 | TKT | 权威 | 工单评论 |
| 104 | TicketStatusHistory | models.go:1597 | TKT | 日志 | 工单状态历史 |
| 105 | TicketWatcher | models.go:1545 | TKT | 关系 | 工单关注人关系 |
| 106 | Ticket | models.go:1503 | TKT | 权威 | 工单主档与状态机 |
| 107 | UserSession | models.go:1106 | IAM | 权威 | 用户登录会话 |
| 108 | User | models.go:163 | IAM | 权威 | 用户身份主档 |
| 109 | WebhookDelivery | models.go:672 | EXT | 日志 | Webhook 投递记录 |
| 110 | Webhook | models.go:613 | EXT | 权威 | Webhook 订阅定义 |
| 111 | WidgetEvent | models.go:1716 | API | 日志 | Widget 接入事件 |
| 112 | WorkingHourConfig | models.go:1666 | CHN | 权威 | 渠道工作时间规则 |

## 完整性校验

- 源码基线：`rg '^type [A-Za-z0-9_]+ struct' internal/domain`。
- 源码对象数：112。
- 索引对象数：112；其中 111 个静态归属 26 个模块，1 个为明确排除的 L0 共享机制。
- 重复对象：0。
- 缺失对象：0。
- 多重静态所有者：0；`LocalChangeJournal` 不计入静态所有者分母，其记录按 `source_module` 动态归属。
- 投影重建规则：`CaptainDocChunk`、`CaptainKnowledgeDoc` 从 KB 原文重新切分和索引；`DashboardAppContentItem` 从扩展应用响应重新拉取；`ReportingEvent` 从业务事件或权威数据补投。
