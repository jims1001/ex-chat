# E-ARCH-08-v114 CUS 联系人合并事件扇出

`cus.contact.merged.v1` 现在按事件合同路由扇出至 CON、MSG、TKT、BIL 和 OPS。每个目标在 CUS 所有者日志中保留独立投递收据；只有所有已配置目标都成功后，源 outbox 才标记为 delivered。

| 验收项 | 结果 |
|---|---|
| 五目标扇出 | CON Conversation、MSG Contact 发送者、TKT Ticket/TicketComment、BIL Order、OPS CampaignDelivery 全部更新为基础联系人 |
| 分目标进度 | 五个 `event_delivery` 收据按 event_id 与目标模块唯一 |
| 部分失败 | MSG 停机时 CON 成功收据保留，源 outbox 仍为 pending |
| 恢复补投 | MSG 同地址恢复后只投递 MSG，CON 不重复调用；完成后 outbox 转 delivered |
| 消费幂等 | 五个所有者各只应用一次，重试不重复更新 |
| 数据边界 | CUS 只保存 outbox 和投递收据，投影只在各所有者独立库更新 |

专项用例覆盖五模块真实 HTTP 消费及停机恢复。模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查均通过后提交。

v113 已验证五个消费者能力；v114 补齐了单个源事件的真实多目标投递及恢复语义。守护进程或模块服务的自动调度接线尚未完成，不由本证据代表。下一项为 CUS 单联系人删除命令及 `cus.contact.deleted.v1` 事件；ARCH-08 保持 REWORK。
