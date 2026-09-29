# E-ARCH-08-v173 NTF 投递分发、订阅清理与完整声明面

| 验收项 | 结果 |
|---|---|
| 投递边界 | 按 `account_id + subscription_id` 锁定 NTF 订阅并经 TEN 验证所属用户 |
| 真实投递 | 对 HTTP/HTTPS 订阅端点执行 POST，按实际 2xx/非 2xx/网络结果记录 delivered/failed |
| 投递审计 | PushDeliveryLog 保存平台、状态、状态码及内部诊断，重放不重复调用端点或写日志 |
| 秘密最小化 | Token、标题、正文和错误详情不进入响应、命令收据或 delivery.changed 事件 |
| 账号清理 | 仅删除目标账号 NotificationSubscription，其他账号订阅不受影响 |
| 声明面 | 7 Command、4 Query、4 Event 均有实际生产或读取路径 |

专项测试 `TestNTFDeliveryDispatchAndSubscriptionPurgeStayOwnerScoped` 通过；模块运行时、模块隔离、完整 Go 回归、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查均随本批执行通过。NTF 尚有 PostgreSQL 多实例、版本回退、事件自动投递和恢复验收，ARCH-08 保持 REWORK。
