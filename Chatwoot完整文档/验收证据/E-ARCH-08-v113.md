# E-ARCH-08-v113 TKT/BIL/OPS 联系人合并投影消费

| 消费模块 | 本地更新范围 | 账号隔离 | 重复事件 |
|---|---|---|---|
| TKT | Ticket.ContactID、TicketComment.ContactID | 其他账号工单不变 | 一条 event_consumed |
| BIL | Order.ContactID | 其他账号订单不变 | 一条 event_consumed |
| OPS | CampaignDelivery.ContactID | 其他账号投递不变 | 一条 event_consumed |

共同验证结果：三个独立库均不创建 CUS Contact 表；业务更新和消费收据同事务；事件认证使用 CUS 调用者；专项测试、84 项隔离、模块运行时、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过。

本证据关闭联系人合并的五个外部投影消费者，CUS 合并生产端到 CON/MSG/TKT/BIL/OPS 的事件闭环已形成。各模块其余独立运行能力仍待完成，ARCH-08 保持 REWORK。
