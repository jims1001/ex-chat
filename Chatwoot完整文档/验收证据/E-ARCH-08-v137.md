# E-ARCH-08-v137 MSG 账号匿名化与完整声明面

| 验收项 | 结果 |
|---|---|
| 匿名化命令 | `msg.account_messages.anonymize.v1` 只处理 target 对应账号 |
| 消息事实 | 保留消息状态/时间线，清空 sender_id、正文、翻译、内容属性和 echo_id |
| 关联事实 | 删除目标账号 Attachment 与 DraftMessage；EmailLog 清空地址、主题、正文、退信和错误并解除用户/会话引用 |
| 账号隔离 | 其他账号 Message 与 EmailLog 保持完整 |
| 原子与幂等 | 所有匿名化、命令收据与最小批量变更事件同事务；重放不重复发布 |
| 声明面 | MSG 独立运行时注册冻结合同声明的 7 Command、5 Query，并覆盖 6 类 Event；库内无外部所有者表 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 MSG PostgreSQL 多实例、事件自动投递、混合版本回退及所有者备份恢复；ARCH-08 保持 REWORK。
