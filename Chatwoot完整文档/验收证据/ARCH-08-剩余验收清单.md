# ARCH-08 剩余验收清单

基线：E-ARCH-08-v30。整体状态：REWORK。此清单是下一轮实现与验收的入口，不能以阶段分库用例替代全模块业务验收。

## 必须完成的工作

| 项目 | 完成条件 | 当前缺口 |
|---|---|---|
| 存储边界 | 全部业务路径只访问所属权威库，无跨所有者联表、关联写入或事务 | 需审计仓储、handler/service 直接 SQL，以及 Preload/Save 的隐式关联访问 |
| 公开合同 | Command、Query、Event 的调用、认证、版本、超时、错误语义完整 | 目前新增端口主要仍是进程内适配器 |
| 运行接线 | 每个模块有独立配置、数据库凭据、监听入口、健康与就绪状态 | 当前业务入口仍为 cmd/server 组合进程；cmd/module-migrate 仅准备架构 |
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
| CHN | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| CUS | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| MED | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| CON | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| MSG | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| RTE | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| TKT | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| QLT | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| KB | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| BIL | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| EXT | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| NTF | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| AUT | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
| OPS | 待完整验收 | 待验收 | 待验收 | 待验收 | 待验收 |
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
- 下一项：SLAService 的 CHN/MSG 收件箱与消息访问边界。
