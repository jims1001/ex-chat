# CSAT 必需读取失败传播（phase57）

## 既有合同与最小修复

`Chatwoot完整文档/Chatwoot-AI实施步骤文档/13-报表搜索SLA-CSAT与指标口径实施.md`
的 RPT-20 要求报表与原始评价逐项可对账；
`Chatwoot完整文档/Chatwoot产品与API文档/02-API通用约定与认证.md`
第 8 节区分查询成功与服务异常。依赖失败不满足 HTTP 200 的成功语义。

此前 legacy `CSATRepository.GetMetrics` 忽略总数、AVG Row/Scan、评分分布
共三次必需 SQL 读取的错误。advanced `GetAdvancedMetrics` 忽略总数、
approved/pending/rejected 三类数量、AVG Row/Scan、正向数量、评分分布、
客服聚合共八次必需读取的错误，可能返回零值或部分数据。

本批逐次检查这 11 个读取结果，一旦失败立即返回原始错误和 nil，丢弃
已累积指标；不继续后续 SQL 或 IAM 客服资料读取。部分行伴随流错误也
不能构成成功结果。已有 CON 筛选和 IAM 资料读取错误传播保持原样。

AVG 使用 sql.NullFloat64，并检查查询构建/回调和 Row.Scan 两类错误。
SQL 的正常 NULL 聚合仍返回零平均值；不把健康空库转换成 500。
本批不引入多查询快照语义，读取间数据变化依然不构成一致性快照。

## HTTP 合同

- V1/V2 `reports/csat` 的失败返回固定 HTTP 500 文本
  `Failed to get csat report`，不再暴露依赖错误原文。
- V1 `csat_surveys/metrics` 继续沿用 HTTP 500
  `Failed to get csat metrics`。
- 错误包络保留 `success: false` 与 `error`，无成功 data、内部哨兵、
  评价原文或部分指标。成功仍是 `success: true` 与原有 data 对象。
- 不新增路由。reports 路径保持 report_view 权限规则；advanced 路径
  保持现有 account 成员可读规则，不顺带收紧为 report 权限。
- 不改变现有日志处理，不宣称全局日志脱敏或新的统一错误分类。

## 成功语义与所有权

- legacy 仍统计 account 的所有现有评分，包括零或历史异常评分；
  平均值不新增四舍五入。健康空 distribution 仍是 null。
- advanced 仍只选择 rating > 0，正向仍为 rating >= 4；不增加评分上限
  或新的审核状态过滤。均值及满意率保留原先两位舍入。
- rating_breakdown 的 1..5 零值、review_status_breakdown 的三项键及
  空 AgentMetrics 的 omitempty 保持不变；不新增排序承诺。
- since/until 继续是 CSAT 自身的闭区间 `>=` / `<=`，保留可选边界、
  inbox/agent 过滤、账号隔离与原有查询顺序。不套用其他报表半开区间。
- QLT 只读自有 csat_surveys；inbox 会话 ID 从 CON 获取，客服名称由
  IAM 获取。无跨 owner 表复制、共享成员审计变更或新指标策略。

## 源码准备与待执行回归

数据库驱动故障用例逐一覆盖 11 个读取失败，AVG Next/转换 Scan 错误、
分布/客服部分行后报错、原始错误身份、立即停止、丢弃结果与同实例恢复。
独立 QLT/CON/IAM SQLite/PostgreSQL 用例覆盖所有查询回调失败、实际表
不可用和恢复、健康空值、范围/租户/owner 隔离、闭区间、评分资格和舍入。
现有 CON 失败先于 QLT，后续 IAM 失败丢弃前一客服结果也保持覆盖。

HTTP 用例覆盖三条真实路由的全部 SQL 失败点、通用 500、成功包络、
空形状、真实表不可用/恢复、认证和当前成员/权限的先行验证。
清单新增两个原生根、54 个精确结果，仅登记待执行义务，不代表已通过。

第五十六批完整门禁接受且父任务明确释放执行资源前，本批只进行源码、
静态检查和测试准备；不运行 Go、构建、服务或测试。旧实现失败证明、
focused ordinary/race/native、最终完整 core/native 与独立接受必须
以同一冻结源码的实际证据为准。恢复检查点标记 INCOMPLETE / UNVERIFIED，
不能代替验收或发布。不混入 DataImport 生命周期耐久性，不推送、部署、
调用外部 provider、读取真实凭据或操作用户电脑。
