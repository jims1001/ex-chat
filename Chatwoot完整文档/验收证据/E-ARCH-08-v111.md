# E-ARCH-08-v111 CON 联系人合并投影消费

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `14f906a`；E-ARCH-08-v110 |
| 消费合同 | CON 注册 `cus.contact.merged.v1` 消费处理器 |
| 投影更新 | 只按 event.account_id 将 Conversation.ContactID 从 merged_contact_id 改为 base_contact_id |
| 账号隔离 | 账号 91 两条会话更新；账号 92 同 contact id 会话保持不变 |
| 去重顺序 | 同一 EventID 重复投递只保留一条 event_consumed；新聚合从版本 2 开始被拒绝 |
| 存储边界 | CON 独立库没有 CUS Contact 表 |
| 回归门禁 | 专项测试、84 项隔离、模块运行时、完整测试、Docker、`go vet ./...` 和差异检查通过 |

本证据仅关闭 CON 对联系人合并事件的投影消费。MSG/TKT/BIL/OPS 消费端和 CON 完整独立运行面仍待完成；ARCH-08 保持 REWORK。
