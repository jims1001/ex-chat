# E-ARCH-08-v136 MSG 邮件投递意图与事件

| 验收项 | 结果 |
|---|---|
| 邮件命令 | `msg.email.send.v1` 校验标准邮箱地址、主题长度、正文非空及 email_type 白名单 |
| 引用边界 | transcript 会话通过 CON 查询验证；可选 user_id 通过 TEN 成员查询验证；target 与账号/会话一致 |
| 所有者事实 | MSG 保存 EmailLog pending 投递意图，保留发送所需私密内容，供后续 Provider 适配器处理 |
| 安全响应 | 命令响应和 `msg.email_delivery.changed.v1` 只返回投递摘要，不含收发邮箱、主题和正文 |
| 原子与幂等 | EmailLog、命令收据和 pending 事件同事务；重放只有一条日志和一个事件 |
| 故障边界 | CON/TEN 校验失败或停止时不保存半成品 EmailLog |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 MSG 账号消息匿名化和完整声明面；ARCH-08 保持 REWORK。
