# E-ARCH-08-v112 MSG 联系人合并投影消费

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `54d07f1`；E-ARCH-08-v111 |
| 消费合同 | MSG 注册 `cus.contact.merged.v1` 消费处理器 |
| 更新范围 | 仅 account_id 匹配且 sender_type=Contact、sender_id=mergee 的消息 |
| 类型隔离 | 同 sender_id 的 User 消息保持不变 |
| 账号隔离 | 其他账号的 Contact 消息保持不变 |
| 去重存储 | 重复投递只产生一条 event_consumed；MSG 库不存在 Contact/Conversation 表 |
| 回归门禁 | 专项测试、84 项隔离、模块运行时、完整测试、Docker、`go vet ./...` 和差异检查通过 |

本证据仅关闭 MSG 合并投影消费。TKT/BIL/OPS 消费端和 MSG 完整运行面仍待完成；ARCH-08 保持 REWORK。
