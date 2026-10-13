# RPT Inbox 依赖失败传播（phase55）

## 最小范围

`ReportService.loadInboxesMap` 原先忽略 CHN owner 的 `ListByAccount`
错误。失败时所得空 map 会被当作缺失配置，继续使用周一至周五 UTC
09:00–18:00 的会话筛选、自然秒时长及 WebWidget 渠道分类；部分行附带
错误时也会形成不完整 map。本批在 owner 返回错误时丢弃全部行并传播
原始错误，不返回成功指标。共享会话加载器和 trends 的直接加载点立即
中止；成功路径的所有查询、字段和筛选保持不变。

覆盖 account summary、agent/team/inbox/label 四维度、首响分布、会话
CSV 导出和 trends。`reports/conversations_summary`、三种首响分布别名
以及 V2 `summary_reports/agent|team|inbox|label` 复用相同路径。

## 现有 HTTP 合同

无需新增错误类、状态码或处理器分支。既有处理器收到依赖错误后使用
HTTP 500，JSON 为 `{"success":false,"error":"<该接口原有固定文案>"}`：

- account summary：`Failed to generate report summary`
- conversations summary（JSON/CSV）：`Failed to generate conversations summary`
- 四维度：`Failed to generate agent|team|inbox|label metrics`
- 首响分布及别名：`Failed to calculate first response distribution`
- 会话导出：`Failed to export conversations`
- trends：`Failed to generate conversation trends`

失败响应不包含 data/payload、内部依赖错误字符串或 CSV 附件头。服务层
保留错误身份供调用方检查。处理器既有日志行为不在本次错误传播修复
范围；本批不宣称完成所有内部错误日志脱敏或全局统一错误合同。

## 兼容边界

- 仅实际返回的错误属于依赖失败；健康 nil/空列表与不存在的 Inbox ID
  继续走既有成功语义和既有回退规则，不发明新营业时间策略。
- `business_hours=false` 也不吞掉已经发起的 CHN 读取错误；本批不减少
  或重排成功路径的查询，也不去掉 inbox 维度现有的第二次列表读取。
- 显式空/反向 trends 区间继续在读取 owner 前返回空趋势。
- Account 范围、半开 `[since, until)`、普通/营业时间数值、字段类型和
  JSON/CSV 结构不变。无新写入、联表、数据所有权或权限规则。
- 不改变 count-series 结构化错误、机器人报表、SLA、missing-Inbox
  业务定义或 legacy 营业时间计数冲突，亦不宣称独立 RPT 完整验收。

## 回归与验收状态

新增服务层回归分别验证首次失败、部分行+错误、下游不被调用、健康空
map、无效趋势窗口；SQLite/PostgreSQL 独立 owner-store 场景验证恢复、
健康空 Account 和真实 missing Inbox 的旧回退。真实路由覆盖 V1/V2、
维度别名、首响别名、JSON/CSV、通用失败包络、权限先行和恢复后的完整
成功结果不变。原生清单新增 87 个精确结果、2 个根。

这些是待执行的验收义务；代码准备或清单登记不能代表测试已通过。
最终状态和门禁证据须以同一最终提交上的独立执行记录为准。
