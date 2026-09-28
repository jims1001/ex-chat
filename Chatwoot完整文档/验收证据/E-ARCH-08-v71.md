# E-ARCH-08-v71 AdvancedHandler 会话参与者 CON 所有者边界

`conversation_participant_handler.go` 原先由 ORC 直接校验 `Conversation` 并读写 CON `ConversationParticipant`。本轮将会话账号校验、参与者添加、列表、删除和原子同步全部移入 `ConversationRepository`；每个仓储命令均在同一 CON 存储中重新验证账号与会话归属，避免只依赖 handler 的前置检查。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `df9938e`；E-ARCH-08-v70 |
| 阶段分库检查 | 74 个子用例通过 |
| 专项回归 | CON 独立库下参与者添加去重、列表、差异同步、批量删除和账号隔离通过；跨账号读写返回不存在；CON 库无 IAM User 或 TEN AccountUser 权威表 |
| 依赖清单 | 新增 participant handler 的 ORC→CON ConversationRepository 源码边；依赖摘要 `d113c694602b100682f9560fed4a4dd4f3d0a80be920fe862bfc6a511346919f` |
| 协作合同 | 复用 ConversationRepository 的 CON 组件绑定；协作摘要保持 `195f278a52063a3ec726f75f38c12ddadfc9e9476abfe2014620d9ad57f4988d` |
| 完整回归 | `make test` 通过；74 项模块隔离验收通过 |
| 容器回归 | `make test-docker` 通过并清理全部临时资源 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；participant handler 已增加禁止直接使用 `h.db` 的门禁 |

会话参与者的 CON 边界已经完成。下一项为 `custom_filter_handler.go` 的 SRH `CustomFilter` 访问边界。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
