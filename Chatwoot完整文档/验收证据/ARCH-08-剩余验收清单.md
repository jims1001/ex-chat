# ARCH-08 剩余验收清单

基线：E-ARCH-08-v30。整体状态：REWORK。此清单是下一轮实现与验收的入口，不能以阶段分库用例替代全模块业务验收。

## 必须完成的工作

| 项目 | 完成条件 | 当前缺口 |
|---|---|---|
| 存储边界 | 全部业务路径只访问所属权威库，无跨所有者联表、关联写入或事务 | 需审计仓储、handler/service 直接 SQL，以及 Preload/Save 的隐式关联访问 |
| 公开合同 | Command、Query、Event 的调用、认证、版本、超时、错误语义完整 | Command/Query 通用传输层已完成；真实业务处理器和 Event 投递尚未迁入独立进程 |
| 运行接线 | 每个模块有独立配置、数据库凭据、监听入口、健康与就绪状态 | 26 个模块的独立运行基础单元已完成；业务公开合同处理器尚未迁入 |
| 故障恢复 | 逐依赖超时、停止恢复、事件重复乱序重放与本地日志恢复通过 | 现有用例只覆盖部分 CON/MSG 故障场景 |
| 多实例与容量 | 幂等、并发版本、任务单一所有者及容量目标通过 | 全模块扩缩与负载验收未完成 |
| 升级回退 | 新旧版本并存、向前兼容迁移、灰度、回退后保留新事实 | 全模块版本切换验收未完成 |
| 备份恢复 | 只恢复所属数据，并重建投影和检查就绪状态 | 全模块恢复演练未完成 |
| 提取验收 | 全部调用走公开合同，切换无双主写、不重复发事件，外部语义不变 | 尚未执行完整模块提取 |

## 模块完成矩阵

所有行都必须关联真实执行证据后才可关闭。当前没有任何一行通过 ARCH-08 的全套验收。

| 模块 | 全业务分库 | 独立启停及故障 | 扩缩并发容量 | 升级回退 | 备份恢复及提取 |
|---|---|---|---|---|---|
| IAM | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| TEN | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| ENT | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| SYS | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| CHN | 声明合同运行面已验证 | 启停与依赖恢复通过 | PostgreSQL 8 实例同命令单写通过 | v1/v2 并存及回退事实保留通过 | 所有者备份恢复通过 |
| CUS | 7 Command、5 Query、6 Event 声明面已接入 | 合并事件自动扇出及停机恢复补投通过 | PostgreSQL 8 实例同命令单写通过 | v1/v2 并存及回退事实保留通过 | 所有者备份恢复与就绪门禁通过 |
| MED | 5 Command、4 Query、3 Event 声明面已接入 | 独立启停及事件投递通过 | PostgreSQL 8 实例同命令单写通过 | v1/v2 并存及回退事实保留通过 | 所有者备份恢复与就绪门禁通过 |
| CON | 7 Command、4 Query、4 Event 及合并事件消费完整接入 | 依赖停止失败且不产生半写；事件恢复投递通过 | 重启/8 实例幂等单写及事件去重通过 | v2→v1 回退通过 | 所有者快照恢复与门禁通过 |
| MSG | 7 Command、5 Query、6 Event 及联系人合并消费完整接入 | 依赖停止无半写；事件恢复投递通过 | 重启/8 实例幂等单写通过 | v2→v1 回退通过 | 所有者快照恢复与门禁通过 |
| RTE | 5 Command/4 Query 完整接入 | 4 类 Event 完整覆盖 | PostgreSQL 8 实例单写通过 | v2→v1 通过 | 备份恢复与 readiness 通过 |
| TKT | 5 Command/4 Query/3 Event 与合并消费完整接入 | 事件自动投递通过 | PostgreSQL 8 实例单写通过 | v2→v1 通过 | 六类所有者快照恢复与门禁通过 |
| QLT | 4 Command/3 Query/3 Event 完整接入 | 自动事件投递通过 | PostgreSQL 8 实例单写通过 | v2→v1 通过 | 八类所有者快照恢复与门禁通过 |
| KB | 6 Command/5 Query 已接入并完成账号边界验证 | 5 类事件均有生产路径且自动投递通过 | PostgreSQL 8 实例单写通过 | v2→v1 重放和读取通过 | Portal/Category/Article 与日志恢复、readiness 门禁通过 |
| BIL | 5 Command/3 Query 已接入并完成账号边界验证 | 5 类生产事件均有原子写入和自动投递路径，合并事件消费已接入 | PostgreSQL 8 实例单写通过 | v2→v1 重放和读取通过 | 四类所有者数据与日志恢复、readiness 门禁通过 |
| EXT | 7 Command/5 Query 已接入并完成账号边界验证 | 4 类事件均有生产和自动投递路径 | PostgreSQL 8 实例单写通过 | v2→v1 重放和读取通过 | 七类所有者数据与日志恢复、readiness 门禁通过 |
| NTF | 4 Query 已接入并完成账号/用户边界验证 | 待验收 | 待验收 | 待验收 | 待验收 |
| AUT | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| OPS | 合并事件消费已接入 | 待验收 | 事件去重通过 | 待验收 | 待验收 |
| AIC | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| MIG | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| RPT | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| SRH | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| ORC | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| API | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| RTM | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| AUD | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |

## 审计使用边界

`scripts/audit-module-storage.py` 仅列举外部模型类型引用，包含合法 DTO，也会漏掉字符串形式的表名和预加载关联。候选计数不能表示剩余违规数量，不能作为完成条件。需要结合代码审查、隔离库运行、禁止外部表的数据库权限和业务请求验证。

## 当前逐项队列

- 已完成并验证：CHN 收件箱成员及账号成员回退查询（E-ARCH-08-v11）。
- 已完成并验证：CHN 分配策略预加载与关联保存移至 ORC/RTE 边界（E-ARCH-08-v12）。
- 已完成并验证：收件箱 Campaign 查询由 ORC 调用 OPS，CHN 只提供归属校验（E-ARCH-08-v13）。
- 已完成并验证：CHN/EXT AgentBot 双向绑定、查询与解绑由 ORC 组合（E-ARCH-08-v14）。
- 已完成并验证：RTE 策略详情和列表的 fallback 用户/团队由 TEN/IAM 所有者端口组合，关联对象不写入 RTE（E-ARCH-08-v15）。
- 已完成并验证：RoutingService 自动分配和容量检查通过 CHN/TEN/IAM/CON/MSG 所有者查询，跨账号 fallback 与依赖故障明确失败（E-ARCH-08-v16）。
- 已完成并验证：RoutingService 休眠扫描和条件恢复由 CON 所有者执行，重复调度不重复返回（E-ARCH-08-v17）。
- 已完成并验证：AgentCapacityPolicyRepository 只访问 RTE 权威数据，TEN 成员与 CHN 收件箱由 handler 通过所有者合同组合（E-ARCH-08-v18）。
- 已完成并验证：AppliedSLARepository 只访问 RPT 权威数据，会话筛选、详情组合和 SLA 投影重置通过 CON 所有者合同完成（E-ARCH-08-v19）。
- 已完成并验证：CaptainRepository 只访问 AIC 权威数据，收件箱通过 CHN、会话统计与下钻通过 CON、助手消息统计通过 MSG 所有者合同完成（E-ARCH-08-v20）。
- 已完成并验证：AccountRepository 新增成员前通过 IAM 校验用户，成员详情通过 IAM 批量组合，TEN 不保存或更新用户身份数据（E-ARCH-08-v21）。
- 已完成并验证：CompanyExtensionRepository 在 ORC 组合 IAM 备注作者、CON 会话与统计、MSG 消息和附件，CUS 仅确定公司及联系人范围（E-ARCH-08-v22）。
- 已完成并验证：ContactExtensionRepository 在 ORC 组合 CHN 收件箱、CON 会话及 MSG 消息附件，CUS 只保存联系人和 ContactInbox 关系（E-ARCH-08-v23）。
- 已完成并验证：ContactRepository 的删除、活跃列表和合并通过 CON/MSG/TKT/BIL/OPS 所有者命令组合，RTM/API 通过联系人合并事件迁移引用（E-ARCH-08-v24）。
- 已完成并验证：LabelRepository 只访问 CUS 标签数据，ConversationLabel 的租户校验、关联写入、查询与删除由 CON 所有者合同完成（E-ARCH-08-v25）。
- 已完成并验证：ChannelAuthEnterpriseRepository 拆为 IAM/RTM/MIG/RPT/ENT/BIL/TEN 七个所有者存储，handler 按真实所有者合同接线；BIL→ENT 额度投影支持失败后幂等重试（E-ARCH-08-v26）。
- 已完成并验证：原 crm_ops_advanced_repository.go 中四个仓储已按 CUS/RPT/EXT/MSG 物理拆分；AttachmentRepository 修正为 MSG 所有者，并校验消息账号归属（E-ARCH-08-v27）。
- 已完成并验证：OpsRepository 已拆为 OPS/NTF/QLT 所有者文件；Macro 通过 CON/TEN/CUS/MSG 所有者命令完成会话、成员团队、标签、消息和附件操作（E-ARCH-08-v28）。
- 已完成并验证：PlatformRepository 通过 TEN/IAM 所有者合同处理账号、成员和用户生命周期，EXT 不再保存或查询外部权威表（E-ARCH-08-v29）。
- 已完成并验证：PublicRepository 通过 CHN/CUS/CON/MSG 所有者合同组合公开视图，并由 MSG 强制过滤私密消息（E-ARCH-08-v30）。
- 已完成并验证：QARepository 通过 TKT/CON 所有者候选查询生成质检任务，QLT 不访问工单和会话权威表（E-ARCH-08-v31）。
- 已完成并验证：WidgetRepository 通过 CHN/CUS/CON/OPS/MSG 所有者合同组合访客操作，API 只保存 WidgetEvent（E-ARCH-08-v32）。
- 已完成并验证：TicketRepository 通过 TEN/IAM/CUS/CON 所有者合同完成引用校验与详情组合，RPT SLA 标识保持不透明且 TKT 不访问 SLA 表（E-ARCH-08-v33）。
- 已完成并验证：ConversationRepository 的收件箱移动通过 CHN 所有者查询校验源和目标收件箱，CON 不再直接访问 Inbox 表（E-ARCH-08-v34）。
- 已完成并验证：ConversationCleanup 只协调 MSG/RPT/QLT/RTM/TKT/API/AIC/OPS/BIL 所有者命令，不再包含外部领域模型和数据库查询（E-ARCH-08-v35）。
- 已完成并验证：CopilotThreadRepository 通过 IAM/CON 所有者查询组合用户和会话，AIC 仅预加载自有助手数据（E-ARCH-08-v36）。
- 已完成并验证：CSATExtensionRepository 通过 CON/CUS/CHN/IAM 所有者合同组合调查详情、收件箱筛选和坐席指标，QLT 不再预加载或 JOIN 外部表（E-ARCH-08-v37）。
- 已完成并验证：QARepository 保留 QLT 自有关联预加载，质检人、二次复核人、坐席、申诉人、审核人和活动参与人均由 IAM 组合（E-ARCH-08-v38）。
- 已完成并验证：PortalRepository 保留 KB 门户、分类和文章关联，文章作者由 IAM 所有者查询组合（E-ARCH-08-v39）。
- 已完成并验证：InboxRepository 通过 CON 获取收件箱会话范围，由 MSG 统计投递失败和连续失败，CHN 不再联表访问会话和消息（E-ARCH-08-v40）。
- 已完成并验证：AutomationService 通过 TEN 账号成员及团队合同执行分配，TEN 通过 IAM 组合用户，AUT 不再访问三类外部权威表（E-ARCH-08-v41）。
- 已完成并验证：AutomationService 通过 CUS 所有者查询组合联系人和公司条件，并强制账号范围，AUT 不再访问 Contact/Company 表（E-ARCH-08-v42）。
- 已完成并验证：AutomationService 通过 CON 会话标签关系和 CUS 标签详情组合自动化条件，AUT 不再联表访问 Label/ConversationLabel（E-ARCH-08-v43）。
- 已完成并验证：SLAService 通过 CHN/CON/MSG 所有者合同读取收件箱、SLA 候选会话和消息时间线，RPT 不再访问三类外部权威表（E-ARCH-08-v44）。
- 已完成并验证：SLAService.ProcessAllBreaches 通过 TEN 最小账号 ID 查询驱动周期检查，RPT 不再直接读取 Account 表（E-ARCH-08-v45）。
- 已完成并验证：ReportService 的基础、维度和 v2 报表通过 CON/CHN/MSG/CUS 所有者查询获取事实，RPT 不再查询或联表四个所有者的权威表（E-ARCH-08-v46）。
- 已完成并验证：ReportService 通过 TEN 获取账号成员和团队、通过 IAM 补齐用户资料，RPT 不再读取 AccountUser/User/Team/TeamMember（E-ARCH-08-v47）。
- 已完成并验证：ReportService 通过 EXT 获取账号范围内的 AgentBotInbox 绑定，并与 CHN 收件箱归属交叉校验，RPT 不再读取 AgentBotInbox（E-ARCH-08-v48）。
- 已完成并验证：DataImportService 的 CSV/JSON 预校验和执行阶段通过 CUS 查询账号范围内的联系人邮箱并由 CUS 创建联系人，MIG 不再读取 Contact（E-ARCH-08-v49）。
- 已完成并验证：CampaignService 通过 CUS 解析账号范围内的标签 ID、通过 CON 查询会话标签对应联系人，OPS 不再跨 CON/CUS 联表筛选活动受众（E-ARCH-08-v50）。
- 已完成并验证：CampaignService 通过 CON 按账号、联系人和收件箱查找既有会话，只在明确未找到时创建，OPS 不再直接读取 Conversation（E-ARCH-08-v51）。
- 已完成并验证：SearchHandler 的全局及专项入口分别通过 CUS/CON/MSG 所有者执行联系人、会话和消息搜索，SRH 不再读取三类外部权威表（E-ARCH-08-v52）。
- 已完成并验证：SearchHandler 的全局及文章专项入口通过 KB 所有者执行账号范围内的文章搜索，SRH 不再读取 Article（E-ARCH-08-v53）。
- 已完成并验证：WidgetHandler 更新消息时通过 MSG 按账号读取消息，并由 CON 校验账号、收件箱及访客会话归属，API 不再联表读取 Message/Conversation（E-ARCH-08-v54）。
- 已完成并验证：WidgetHandler 通过 CHN 按账号和收件箱读取成员 ID，再由 IAM 批量读取公开用户资料，API 不再联表读取 InboxMember/User（E-ARCH-08-v55）。
- 已完成并验证：WidgetHandler 的事件及标签入口通过 CON 校验账号、收件箱和访客会话归属，且不再持有数据库句柄（E-ARCH-08-v56）。
- 已完成并验证：CRMOpsAdvancedHandler 批量分配坐席通过 TEN/IAM 校验并组合坐席，通过 CON/CHN 获取会话和收件箱，ORC 不再联表或预加载四个所有者数据（E-ARCH-08-v57）。
- 已完成并验证：CRMOpsAdvancedHandler 批量状态更新通过 CON/CHN/IAM 批量组合会话、收件箱与坐席，ORC 不再直接查询或预加载三方数据（E-ARCH-08-v58）。
- 已完成并验证：CRMOpsAdvancedHandler 批量分配团队通过 TEN 校验团队归属、通过 CON 获取账号范围内会话；核对确认该分支不需要 IAM/CHN 数据（E-ARCH-08-v59）。
- 已完成并验证：CRMOpsAdvancedHandler 批量添加/删除标签通过 CON 获取账号范围内会话、通过 CUS 创建或解析标签，并由 CON 维护会话标签关系；ORC 不再直接读取 Conversation/Label（E-ARCH-08-v60）。
- 已完成并验证：CRMOpsAdvancedHandler 公司联系人添加后的回读和解除前归属检查均通过 CUS CompanyRepository 完成，ORC 不再直接读取 Contact/Company/Labels（E-ARCH-08-v61）。
- 已完成并验证：CRMOpsAdvancedHandler.GetConversationSLA 通过 CON 按账号读取会话并由 CHN 组合收件箱，ORC 不再直接查询 Conversation 或预加载 Inbox（E-ARCH-08-v62）。
- 已完成并验证：CRMOpsAdvancedHandler.UploadAttachment 的会话存在性由 CON 校验，multipart/JSON 消息归属由 MSG 校验并显式注入 MSG 仓储，ORC 不再直接查询 Conversation/Message（E-ARCH-08-v63）。
- 已完成并验证：CRMOpsAdvancedHandler.GetDraft 通过 MSG 按账号、会话和用户精确读取草稿，ORC 不再直接查询 DraftMessage（E-ARCH-08-v64）。
- 已完成并验证：CRMOpsAdvancedHandler 联系人备注通过 CUS 执行归属及增删改查，并由 IAM 批量组合作者，ORC 不再直接查询 Contact/ContactNote 或预加载 User（E-ARCH-08-v65）。
- 已完成并验证：CRMOpsAdvancedHandler 旧版坐席容量策略通过 RTE AgentCapacityPolicyRepository 保存、更新和按账号列表，ORC 不再直接读写 CapacityPolicy（E-ARCH-08-v66）。
- 已完成并验证：CRMOpsAdvancedHandler 集成目录、Shopify 和 Dialogflow 配置通过 EXT IntegrationRepository 按账号读取，ORC 不再直接查询 IntegrationInstallation（E-ARCH-08-v67）。
- 已完成并验证：CRMOpsAdvancedHandler.ShopifyOrders 通过 CON 获取账号内会话摘要、通过 CUS 获取联系人邮箱，ORC 不再预加载 Conversation.Contact（E-ARCH-08-v68）。
- 已完成并验证：CRMOpsAdvancedHandler.DialogflowProcess 通过 OPS CannedResponseRepository 按账号匹配预设回复，ORC 不再直接查询 CannedResponse（E-ARCH-08-v69）。
- 已完成并验证：AdvancedHandler 自动化规则 CRUD、克隆与执行历史通过 AUT AutomationService 访问，ORC 不再直接读写 AutomationRule/AutomationRuleExecution（E-ARCH-08-v70）。
- 已完成并验证：conversation_participant_handler.go 通过 CON ConversationRepository 完成会话校验及参与者增删查同步，ORC 不再直接访问 ConversationParticipant（E-ARCH-08-v71）。
- 已完成并验证：custom_filter_handler.go 通过 SRH CustomFilterRepository 完成创建、个人及共享列表、详情、更新和删除，ORC 不再直接访问 CustomFilter（E-ARCH-08-v72）。
- 已完成并验证：PublicHandler 创建联系人后的属性回读复用 CUS 所有者结果，消息 echo_id 通过 MSG 按账号和会话查询，处理器不再持有数据库句柄（E-ARCH-08-v73）。
- 已完成并验证：PushService 通过 IAM UserRepository 获取用户时区，NTF 静默时段判断不再直接读取 User（E-ARCH-08-v74）。
- 已完成并验证：EmailService 通过 ENT ChannelENTRepository 读取 BrandedEmailLayout，MSG 不再直接访问品牌配置权威表（E-ARCH-08-v75）。
- 已完成并验证：MigrationService 导入会话时通过 CUS 按账号匹配联系人邮箱及标识，MIG 不再直接读取 Contact（E-ARCH-08-v76）。
- 已完成并验证：MigrationService.importContacts 通过 CUS 按账号执行邮箱、标识、电话及姓名查重，MIG 不再直接读取 Contact（E-ARCH-08-v77）。
- 已完成并验证：MigrationService.ensureDefaultInbox 通过 CHN 按账号复用或创建默认收件箱，MIG 不再直接读取 Inbox（E-ARCH-08-v78）。
- 已完成并验证：CopilotHandler 工具调用通过 BIL 按账号查询订单和物流并更新地址，AIC 不再直接读取 Order（E-ARCH-08-v79）。
- 已完成并验证：AICustomToolHandler 工具调用通过 BIL 按账号查询订单、物流并更新地址，AIC 不再直接读取 Order（E-ARCH-08-v80）。
- 已完成并验证：AdvancedHandler 和 ContactHandler 不再持有共享数据库句柄，CUS 合并事务保留本地日志（E-ARCH-08-v81、v82）。
- 已完成并验证：直接存储所有权审计门禁覆盖 repository/service/handler，当前违规为零（E-ARCH-08-v83）。
- 已完成并验证：26 个模块可使用独立 `MODULE_*` 配置、数据库和监听入口启动，健康及表级就绪检查通过（E-ARCH-08-v84）。
- 已完成并验证：独立模块 Command/Query 传输具备调用方认证、合同版本、相关 ID、请求上限、超时截断及稳定错误语义（E-ARCH-08-v85）。
- 已完成并验证：IAM 身份查询与 TEN 成员列表已在独立库和监听入口运行，TEN 经 HTTP 合同组合 IAM；依赖停止、同址恢复和超时均通过（E-ARCH-08-v86）。
- 已完成并验证：独立 Command 通过所有者本地日志持久化幂等收据，业务与收据原子提交；TEN 成员写入经 IAM 校验，支持重启回放、载荷冲突拒绝和失败回滚（E-ARCH-08-v87）。
- 已完成并验证：真实 PostgreSQL 下 8 个实例竞争同一命令只执行一次；TEN 多成员列表合并为一次 IAM 批量身份查询（E-ARCH-08-v88）。
- 已完成并验证：Event 使用生产端 outbox 与消费端 inbox 原子持久化，支持认证投递、重复去重、聚合版本缺口拒绝和停机恢复补投（E-ARCH-08-v89）。
- 已完成并验证：运行时发布版本与合同版本分离；IAM/TEN v1/v2 混合运行，回退后保留并可回放新版本事实（E-ARCH-08-v90）。
- 已完成并验证：TEN 所有者数据库快照仅包含本模块模型及本地日志，校验恢复后按持久化消费事件重建投影，并以恢复代际标记控制就绪（E-ARCH-08-v91）。
- 已完成并验证：CHN `chn.inbox.get.v1` 从独立 CHN 数据库返回安全收件箱摘要，强制账号范围且不创建 TEN/IAM/CON 外部表（E-ARCH-08-v92）。
- 已完成并验证：CHN `chn.inbox.create.v1` 经 TEN `ten.account.get.v1` 校验账号后，在 CHN 本地事务中原子写入业务数据和幂等收据；TEN 停机不产生残留写入（E-ARCH-08-v93）。
- 已完成并验证：CHN `chn.inbox.update.v1` 仅允许公开配置字段，强制账号归属并支持持久化幂等重放；密钥及供应商配置不能经该合同修改（E-ARCH-08-v94）。
- 已完成并验证：CHN `chn.member.bind.v1` 通过最小化 TEN `ten.permissions.check.v1` 校验账号成员，绑定/解绑与幂等收据原子提交，依赖失败不残留（E-ARCH-08-v95）。
- 已完成并验证：CHN `chn.channel.authorize.v1` 经 ENT `ent.capability.check.v1` 校验权益后在 CHN 本地保存密钥，响应及收据不泄露密钥，权益/依赖失败无残留（E-ARCH-08-v96）。
- 已完成并验证：CHN `chn.health.update.v1` 保留密钥并保存所有者健康观测；`chn.account_channels.revoke.v1` 原子撤销账号全部凭据和成员绑定且不影响其他账号（E-ARCH-08-v97）。
- 已完成并验证：CHN 能力、连接状态和账号删除状态查询只返回安全摘要；撤销后凭据与成员计数归零且 ready=true（E-ARCH-08-v98）。
- 已完成并验证：CHN 六条写命令与领域事件 outbox 同事务；收件箱/健康事件不含密钥，可认证投递、消费去重并在成功后标记 delivered（E-ARCH-08-v99）。
- 已完成并验证：CHN 模板管理/列表和 `chn.template.changed.v1` 接入；7 命令、5 查询、3 事件声明面及 CHN 专属备份恢复通过（E-ARCH-08-v100）。
- 已完成并验证：CHN 八实例并发同一模板命令仅产生一份模板、收据和事件；v2 写入后回退 v1 仍可重放收据并读取事实（E-ARCH-08-v101）。
- 已完成并验证：CUS `cus.contact.get.v1` 从所有者库返回账号范围内安全联系人摘要，不返回 PubsubToken 和未请求的自定义属性（E-ARCH-08-v102）。
- 已完成并验证：CUS `cus.contact.create.v1` 白名单创建联系人，业务、收据和 `cus.contact.created.v1` 事件同事务且重启重放不重复（E-ARCH-08-v103）。
- 已完成并验证：CUS `cus.contact.update.v1` 强制账号归属和字段白名单，对象版本递增且收据、`cus.contact.changed.v1` 事件原子提交（E-ARCH-08-v104）。
- 已完成并验证：CUS `cus.identity.resolve.v1` 在账号内按邮箱、标识、电话及姓名解析联系人，空条件和跨账号查询失败且不泄露令牌（E-ARCH-08-v105）。
- 已完成并验证：CUS `cus.company.get.v1` 按账号读取公司安全摘要与本地联系人计数，不返回自定义和内部附加属性（E-ARCH-08-v106）。
- 已完成并验证：CUS `cus.company.update.v1` 显式创建/更新公司，拒绝跨账号与非法属性，业务、收据和 `cus.company.changed.v1` 事件同事务（E-ARCH-08-v107）。
- 已完成并验证：CUS `cus.label.get.v1` 与 `cus.label.manage.v1` 支持账号范围查询、显式创建/更新和 `cus.label.changed.v1` 原子事件（E-ARCH-08-v108）。
- 已完成并验证：CUS `cus.account_contacts.anonymize.v1` 清除账号联系人 PII、身份映射、标签和备注；删除状态查询给出残留计数且不影响其他账号（E-ARCH-08-v109）。
- 已完成并验证：CUS `cus.contact.merge.v1` 只合并所有者本地事实并原子发布 `cus.contact.merged.v1`，不创建 CON/MSG/TKT/BIL/OPS 外部表（E-ARCH-08-v110）。
- 已完成并验证：CON 消费 `cus.contact.merged.v1` 后只重分配本账号 Conversation.ContactID，重复事件去重、版本缺口拒绝且不创建 Contact 表（E-ARCH-08-v111）。
- 已完成并验证：MSG 消费合并事件只重分配本账号 Contact 类型消息发送者，User 消息和其他账号保持不变，重复事件去重（E-ARCH-08-v112）。
- 已完成并验证：TKT/BIL/OPS 分别消费合并事件并只更新本账号 Ticket/TicketComment、Order、CampaignDelivery 联系人投影，重复事件去重且无 Contact 表（E-ARCH-08-v113）。
- 已完成并验证：CUS 合并事件按合同路由扇出至 CON/MSG/TKT/BIL/OPS，每目标持久化投递收据；部分目标停机时 outbox 保持 pending，恢复后只补投未完成目标（E-ARCH-08-v114）。
- 已完成并验证：CUS `cus.contact.delete.v1` 在 CON 已完成活跃会话检查与历史会话分离后，原子删除联系人及所有者关系数据，并发布 `cus.contact.deleted.v1`（E-ARCH-08-v115）。
- 已完成并验证：CUS 7 个 Command 和 5 个 Query 全部注册，6 个 Event 全部由业务命令发布；`module-server` 按 `MODULE_EVENT_TARGETS_JSON` 和轮询间隔自动投递 outbox（E-ARCH-08-v116）。
- 已完成并验证：CUS PostgreSQL 8 实例并发同命令单写、v2 写入后回退 v1 的收据重放与事实读取、所有者快照恢复和就绪门禁均通过；快照已支持复合主键表（E-ARCH-08-v117）。
- 已完成并验证：MED 新增 MediaAsset、UploadSession、SecurityScan 和 AccessGrant 权威表；`med.upload.create.v1` 原子创建资产与短期上传会话，`med.asset.get.v1` 只返回安全摘要（E-ARCH-08-v118）。
- 已完成并验证：MED `med.asset.confirm.v1` 使用恒定时间令牌校验、过期和校验和约束确认上传；`med.asset.scan.v1` 只允许 pending_scan 资产进入 ready/rejected，并原子发布安全事件（E-ARCH-08-v119）。
- 已完成并验证：MED 访问判定、安全状态和账号删除状态三个查询只读取账号内权威数据，并隐藏授权、存储、令牌和威胁明细（E-ARCH-08-v120）。
- 已完成并验证：MED 单资产删除撤销会话和授权，账号清理只清除本账号四类权威数据并原子发布最小化删除事件；完整声明面已注册（E-ARCH-08-v121）。
- 已完成并验证：MED PostgreSQL 8 实例同命令单写、v2 写入后回退 v1、删除事件自动投递、四类所有者数据快照恢复和就绪门禁均通过（E-ARCH-08-v122）。
- 已完成并验证：CON 会话详情和列表查询只读取本账号会话与 ConversationLabel，并以安全视图隐藏自定义属性及外部所有者实体（E-ARCH-08-v123）。
- 已完成并验证：CON 状态历史从本地事件日志生成最小摘要，账号删除状态通过本账号会话子查询统计标签和参与者关系；4 个 Query 已全部接入（E-ARCH-08-v124）。
- 已完成并验证：CON 创建前经 TEN/CHN/CUS 公开合同校验引用，会话、命令收据和创建事件原子提交；依赖停止不产生半写（E-ARCH-08-v125）。
- 已完成并验证：CON 通用更新只修改显式自有字段且通过 CHN 校验收件箱；分配变更通过 TEN 校验成员与团队，重放单写并原子发布变更事件（E-ARCH-08-v126）。
- 已完成并验证：CON 状态和优先级命令限制允许值、账号与删除中会话边界，休眠要求未来时间，并原子发布状态/变更事件（E-ARCH-08-v127）。
- 已完成并验证：CON 参与者增删同步先经 TEN 成员校验；账号匿名化只清除本账号会话的外部标识投影及关系，7 Command/4 Query 均已接入（E-ARCH-08-v128）。
- 已完成并验证：CON 显式删除操作只清理本账号所有者关系和会话，原子发布删除事件；7 Command、4 Query、4 Event 声明面完整（E-ARCH-08-v129）。
- 已完成并验证：CON PostgreSQL 8 实例单写、v2→v1 回退、删除事件自动投递及三类所有者数据和日志备份恢复均通过（E-ARCH-08-v130）。
- 已完成并验证：MSG 五个查询只读取本账号所有者表，默认排除私密消息，投递摘要不泄露正文或邮件敏感内容（E-ARCH-08-v131）。
- 已完成并验证：MSG 创建通过 CON/TEN 校验会话和发送者，命令键与 echo_id 双层幂等并原子发布最小创建事件；CON 幂等更新活动投影（E-ARCH-08-v132）。
- 已完成并验证：MSG edit/delete 按账号修改；删除清空敏感正文属性和自有附件，变更与删除事件最小化且同事务（E-ARCH-08-v133）。
- 已完成并验证：MSG Provider 状态只允许单调转换，重试只接受本账号 failed outgoing 消息；状态与最小事件同事务（E-ARCH-08-v134）。
- 已完成并验证：MSG 以本地事件日志保存会话/用户单调已读位置，校验 TEN 成员和消息归属，拒绝倒退且相同位置不重复发事件（E-ARCH-08-v135）。
- 已完成并验证：MSG 邮件命令经 CON/TEN 校验后保存 pending 投递事实，响应和邮件事件不泄露地址、主题或正文，依赖故障无半写（E-ARCH-08-v136）。
- 已完成并验证：MSG 账号匿名化保留时间线并清除消息/附件/草稿/邮件敏感事实，其他账号不变；7 Command/5 Query/6 Event 声明面完整（E-ARCH-08-v137）。
- 已完成并验证：MSG PostgreSQL 8 实例单写、v2→v1 回退、创建事件自动投递及四类所有者数据和日志备份恢复均通过（E-ARCH-08-v138）。
- 已完成并验证：RTE 四个查询通过 CON/CHN/TEN 所有者合同组合队列、分配、账号成员候选和活跃会话，仅本地读取 CapacityPolicy（E-ARCH-08-v139）。
- 已完成并验证：RTE 入队通过 CON 查询和分配变更命令清空分配，稳定下游幂等键与本地收据确保重放单写，原子发布队列事件（E-ARCH-08-v140）。
- 已完成并验证：RTE 领取、转移和释放均通过 CON 所有者命令写入，稳定下游幂等键保证重放单写，发布最小分配事件（E-ARCH-08-v141）。
- 已完成并验证：RTE 容量管理经 TEN 校验成员，只写 account_id+user_id 联合唯一的自有策略，原子发布容量事件（E-ARCH-08-v142）。
- 已完成并验证：RTE 冻结声明面的 5 Command/4 Query 全部注册，4 类 Event 均有明确生产路径，独立库不含外部所有者表（E-ARCH-08-v143）。
- 已完成并验证：RTE PostgreSQL 8 实例单写、v2→v1 回退、容量事件自动投递及四类所有者数据和日志备份恢复均通过（E-ARCH-08-v144）。
- RTE 行关闭；下一项：TKT 独立运行公开合同。

- 已完成并验证：TKT 四个查询按账号读取工单、工作流、状态/活动历史与六类删除计数，不预加载 IAM/CUS/CON，且不暴露自定义属性或评论正文（E-ARCH-08-v145）。
- 已完成并验证：TKT 创建命令经 TEN/CUS/CON 校验外部引用，工单、活动、初始历史、收据及最小创建事件同事务且重放单写（E-ARCH-08-v146）。
- 已完成并验证：TKT 状态变更执行允许转换和乐观版本校验，重新打开仅接受 resolved/closed；状态、版本、历史、活动、收据及事件同事务（E-ARCH-08-v147）。
- 已完成并验证：TKT 评论经 TEN 校验后只写自有评论/活动，响应和事件不含正文；账号匿名化清除六类事实中的 PII 并以 pii_count 判定 readiness（E-ARCH-08-v148）。
- 已完成并验证：TKT 完整声明面、PostgreSQL 8 实例单写、v2→v1、事件自动投递、六类所有者事实与日志恢复全部通过（E-ARCH-08-v149）。
- TKT 行关闭；下一项：QLT 独立运行公开合同。

- 已完成并验证：QLT 的 CSAT、质检任务和评分明细查询均先按账号确认所有者事实；无账号字段的评分表必须经账号内任务到达，且独立库不含 CON/TKT/IAM/CHN/CUS 外部表（E-ARCH-08-v150）。
- 下一项：QLT 调查创建与 CSAT 提交命令。

- 已完成并验证：QLT 调查创建和 CSAT 提交先通过 CON 合同校验账号内会话，账号+会话唯一约束阻止重复调查；提交事实、收据和最小 CSAT 事件同事务且重放单写（E-ARCH-08-v151）。
- 下一项：QLT 质检任务创建命令。

- 已完成并验证：QLT 质检任务创建仅接受本账号启用评分卡，经 TEN 校验质检员并经 CON/TKT 校验目标，坐席投影取自所有者响应；任务、收据和最小质检事件同事务（E-ARCH-08-v152）。
- 下一项：QLT 申诉审核命令。

- 已完成并验证：QLT 申诉审核按账号读取申诉和任务，经 TEN 校验复核人并执行原质检员回避；裁决同步任务、活动、收据和最小申诉事件，4 Command/3 Query/3 Event 声明面齐全（E-ARCH-08-v153）。
- 下一项：QLT PostgreSQL 多实例、版本回退、事件投递和备份恢复验收。

- 已完成并验证：QLT PostgreSQL 8 实例申诉裁决保持单写，v2→v1 回退保留裁决事实，质检事件自动投递，八类所有者数据与日志快照恢复及 readiness 门禁通过（E-ARCH-08-v154）。
- QLT 行关闭；下一项：KB 独立运行公开合同。

- 已完成并验证：KB 五个查询按账号读取 Portal/Category/Article，自有后台内容不预加载 IAM；公开视图只返回同账号已发布文章，版本摘要不泄露正文（E-ARCH-08-v155）。
- 下一项：KB 文章创建、发布与归档命令。

- 已完成并验证：KB 文章创建经 TEN 校验作者并限制本账号 Portal/Category，发布和归档执行显式状态边界；三类事实、收据与最小事件同事务且重放单写（E-ARCH-08-v156）。
- 下一项：KB 文档删除、门户管理与账号内容清理命令。

- 已完成并验证：KB 删除文章、创建/更新门户和账号内容清理均按 account_id 执行；外账号内容不可删除，三类命令与最小事件同事务且重放单写（E-ARCH-08-v157）。
- 下一项：KB PostgreSQL 多实例、版本回退、事件投递和备份恢复验收。

- 已完成并验证：KB PostgreSQL 8 实例门户创建保持单写，v2→v1 回退保留事实，知识事件自动投递，Portal/Category/Article 与日志快照恢复及 readiness 门禁通过（E-ARCH-08-v158）。
- KB 行关闭；下一项：BIL 独立运行公开合同。

- 已完成并验证：BIL 订单、付款流水和余额查询只读取本账号所有者表；订单响应不暴露客户姓名、邮箱、电话和地址（E-ARCH-08-v159）。
- 下一项：BIL 订单创建与更新命令。

- 已完成并验证：BIL checkout 创建本账号订单，订单更新只接受明确物流状态字段；事实、收据和最小订单事件同事务且重放单写（E-ARCH-08-v160）。
- 下一项：BIL 支付处理与退款命令。

- 已完成并验证：BIL 支付只接受本账号订单，成功扣款、失败流水和退款分别投递对应事件；累计退款不可超过成功支付且余额忽略失败流水（E-ARCH-08-v161）。
- 下一项：BIL 发票签发命令与完整声明面。

- 已完成并验证：BIL 发票按本账号订单签发，发票编号在账号内防重；5 Command/3 Query/5 Event 冻结声明面均有实际路径（E-ARCH-08-v162）。
- 下一项：BIL PostgreSQL 多实例、版本回退、事件投递和备份恢复验收。

- 已完成并验证：BIL PostgreSQL 8 实例 checkout 保持单写，v2→v1 回退保留订单事实，订单事件自动投递，四类所有者数据与日志快照恢复及 readiness 门禁通过（E-ARCH-08-v163）。
- BIL 行关闭；下一项：EXT 独立运行公开合同。

- 已完成并验证：EXT 目录、安装、机器人、投递和账号删除状态查询已接入；账号事实严格隔离，Token、设置、BotConfig、Webhook payload/response 均不出域（E-ARCH-08-v164）。
- 下一项：EXT 插件安装与禁用命令。

- 已完成并验证：EXT 插件安装只接受目录内应用和匹配账号目标，禁用只作用本账号 Installation；Settings 不进入响应或事件（E-ARCH-08-v165）。
- 下一项：EXT AgentBot 管理与 CHN 收件箱绑定命令。

- 已完成并验证：EXT AgentBot 创建更新删除均限制本账号，绑定前经 CHN 合同验证 Inbox，EXT 不读取 CHN 表且凭据不出域（E-ARCH-08-v166）。
- 下一项：EXT Webhook 管理、分发及投递记录命令。

- 已完成并验证：EXT Webhook 管理、分发、投递记录与账号清理按账号执行；Secret、Payload、ResponseBody 不出域，7 Command/5 Query/4 Event 声明面齐全（E-ARCH-08-v167）。
- 下一项：EXT PostgreSQL 多实例、版本回退、事件投递和备份恢复验收。

- 已完成并验证：EXT PostgreSQL 8 实例安装保持单写，v2→v1 回退保留安装事实，插件事件自动投递（E-ARCH-08-v168）。
- 下一项：EXT 七类所有者数据与日志备份恢复。

- 已完成并验证：EXT 七类所有者数据与日志完成快照恢复，恢复前后 readiness 代次门禁和外部表隔离通过（E-ARCH-08-v169）。
- EXT 行关闭；下一项：NTF 独立运行公开合同。

- 已完成并验证：NTF 通知列表、未读数、推送投递和账号删除状态查询已接入；用户和账号范围明确，设备 Token、标题、正文及错误详情不出域（E-ARCH-08-v170）。
- 下一项：NTF 通知创建、更新与已读命令。

- 已完成并验证：NTF 通知创建经 TEN 校验账号成员，通知延后更新和已读均按账号及自有通知定位；三类命令重放单写，跨账号访问和非成员写入被拒绝（E-ARCH-08-v171）。
- 下一项：NTF 通知偏好保存与设备注册命令。
