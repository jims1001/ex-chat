# 报表人工回复时长的已知机器人排除（phase54）

## 已实现的有限范围

产品与 API 文档 20 第 4 节将首响及回复时长的终点定义为人工回复。本批仅修正
`CalculateConversationFRT` 和 `CalculateConversationReplyTimes` 的终点资格：

- 仍须是准确的 `message_type=outgoing` 且 `private=false`。
- 精确排除 `SenderType` 为 `AgentBot`、`CaptainAssistant`、`Captain::Assistant`
  的记录。多个机器人消息均不结束或重置当前客户等待周期。
- 没有合格回复时不产生时长样本，而非加入零秒样本；机器人之后的合格回复从原有
  客户等待起点计时。机器人之后新到达的 incoming 也不替换当前等待起点。
- 历史空值和未知 SenderType 继续按原规则参与；不归一化大小写或空白，不把实现
  扩展成 `SenderType=User` 白名单。`Status`、`DeliveryFailed` 和其他历史字段不新增
  筛选条件，因此时长依然不证明发送成功或送达。

共用 helper 的现有 Account Summary JSON、conversations_summary JSON/CSV、
agent/team/inbox/label 汇总和三个首响分布别名继承该修正。Team/label 仍只暴露原有
时长字段，本批不增加 avg_reply_time，也不添加新的通用 reports 根指标。

## 保留的边界与未完成能力

- 继续选择会话创建时间范围内的会话，并将其 MSG 记录限定于原有半开时间窗口。
  不把窗口外的 incoming 取回，也不扩展跨窗口会话样本。
- FRT 仍以已加载消息中的第一个 incoming 为起点；不实现文档“最近一次非人工活动”
  所可能要求的另一种起点算法，不改变排序和同时间戳规则。
- Reply time 仍从当前连续 incoming 等待序列的首条开始，下一个合格公开回复结束
  该周期；private outgoing、activity 等记录保持原语义。
- 营业时间继续调用现有 Inbox 算法和会话筛选。业务时间为 true 时的旧 Summary
  计数/cohort 冲突仍待单独处理，不能据此声称整个报表合同已经完成。
- 计数、FCR、机器人解决/转人工、解决时长、SLA 和独立 Captain/AIC 分析均未修改。
  特别是 FCR 保留“一个公开 outgoing”的既有公式，机器人记录仍可影响 FCR。
- Owner 读取接口、SQL、Auth/Tenant/Permission 链、JSON 包络/字段和 CSV 格式未变。
  CHN 读取失败传播是另一项工作，不包含在本批。

## 回归与验证方法

- `TestReportHumanResponseDurations` / `TestReportHumanResponseBusinessHours`：三个
  精确 bot 类型、纯 bot、多 bot、两轮等待、private/非 outgoing、历史 SenderType、
  queued/failed 等状态、原 incoming 起点及跨日营业时间。
- `TestReportHumanDurationOwnerStores` / `TestPostgresReportHumanDurationOwnerStores`：
  RPT/CON/MSG/CUS/CHN/TEN/IAM 独立 store，Summary/四维度/分布、无 bot-only
  样本、租户消息隔离、原计数/FCR/解决时长、cohort 和半开端点。
- `TestReportHumanDurationHTTP` / `TestPostgresReportHumanDurationHTTP`：实际路由的
  V1/V2、JSON/CSV、四维度、分布别名及权限隔离。确切 native 子项须加入 reviewed
  coverage manifest，数量断言随之更新。

回归必须先在未修改 helper 的冻结 phase53 源码上证实失败，再在本批源码上通过。
Focused、race、完整 `make ci` 和完整 disposable `make ci-postgres` 是不同阶段；
未运行的检查不得表述为通过。所有 DB/HTTP 夹具只使用本地合成数据，不联系外部服务。

回滚只需恢复本批 helper 和相应回归/合同文档；没有数据迁移或配置开关。
