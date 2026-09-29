# E-ARCH-08-v130 CON 容量、版本回退、事件投递与恢复

| 验收项 | 结果 |
|---|---|
| PostgreSQL 容量 | 8 个 CON 实例并发执行同一优先级命令，只更新一次、版本只增一次，并仅产生一条收据和一个事件 |
| 混合版本 | v2 写入后，回退 v1 可重放收据并读取同一会话事实；v1 运行时拒绝 v2 请求 |
| 自动事件投递 | CON 删除事件由运行时工作器自动投递，源 outbox 转 delivered，并保存单一目标投递收据 |
| 所有者快照 | Conversation、ConversationParticipant、ConversationLabel 与本地日志完整恢复 |
| 恢复门禁 | 配置恢复代次的目标运行时在恢复前不就绪，匹配快照恢复后才就绪 |
| 隔离 | 容量与恢复目标均不包含 TEN/CHN/CUS/IAM/MSG 外部表 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。CON 行级独立运行验收关闭；下一项进入 MSG 公开合同。ARCH-08 保持 REWORK。
