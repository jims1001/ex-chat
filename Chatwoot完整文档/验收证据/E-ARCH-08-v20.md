# E-ARCH-08-v20 AIC Captain 的 CHN/CON/MSG 边界

## 本次子项结论

已关闭 `CaptainRepository` 对 CHN 收件箱、CON 会话和 MSG 消息的直接存储访问。AIC 仓储现在只保存 Captain 助手、收件箱绑定、FAQ 和知识库等 AIC 权威数据。

助手详情和列表先读取 AIC 的 `CaptainInbox` 绑定，再通过 CHN `InboxRepository` 补齐收件箱；绑定前也由 CHN 校验账号归属。统计由 MSG 返回助手消息命中的会话 ID 和回复数，由 CON 计算处理量、解决量、转人工量及下钻分页。原先被忽略的 CON/MSG 查询错误现在会向调用方返回。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `f19eba2`；E-ARCH-08-v19 |
| 验证时间 | 2026-09-26T08:25:00+00:00 |
| 阶段分库检查 | 28 个子用例通过 |
| 专项回归 | AIC/CHN/CON/MSG 分库绑定、详情组合、统计、下钻、跨账号拒绝及 MSG 故障通过；Captain 完整管理回归通过 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过 |
| 静态检查 | `go vet ./cmd/... ./internal/... ./pkg/... ./test/...`、`git diff --check` 通过 |

新增分库用例确认 AIC 数据库未创建 `Inbox`、`Conversation` 或 `Message` 权威表。账号 1 不能绑定账号 2 的收件箱，账号 2 的会话和消息不会进入账号 1 的 Captain 统计；MSG 停止时统计明确失败。

## 合同与剩余边界

AIC 的同步依赖清单新增 CHN，并登记 `FindByIDs`、`FindSummaryByID`、`GetCaptainStats`、`ListCaptainConversations`、`CountCaptainReplies` 和 `FindCaptainConversationIDs` 六个查询合同。依赖摘要为 `0d08905f732a7bb5c1d9c63213ca1995e8c79c15d108c2520874c6c4c04e947a`，协作合同摘要为 `233a5d57a5320b813dbb81aba3362f68990935e076da47b6b13e6eb2bc5206cb`。ARCH-03、ARCH-04 保持 REOPENED。

静态候选扫描仍会因 `CaptainRepository` 的接口返回 DTO 类型列出 `Inbox` 和 `Conversation`，但禁止直接查询规则及四库运行用例确认 AIC 已不访问对应表。下一项为 TEN `AccountRepository` 的 IAM 用户访问边界。ARCH-08 整体仍为 REWORK，完整运行验收见 [剩余验收清单](ARCH-08-剩余验收清单.md)。
