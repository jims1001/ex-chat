# E-ARCH-08-v117 CUS 容量、版本回退与备份恢复

| 验收项 | 结果 |
|---|---|
| PostgreSQL 多实例 | 8 个 CUS 实例并发处理同一 `cus.contact.create.v1`，八个请求返回同一结果 |
| 单写不变式 | 仅一个 Contact、一条 command_receipt 和一条 `cus.contact.created.v1` outbox |
| 混合版本 | v2 在接受 v1/v2 的窗口写入，v1 回退后仍可重放原收据并读取联系人 |
| 回退边界 | 回退后的 v1 运行时拒绝 v2 合同版本，不删除 v2 期间已提交事实 |
| 所有者快照 | 快照包含 CUS 所有表和 LocalChangeJournal，不包含其他模块表 |
| 恢复就绪 | 恢复前 recovery generation 门禁拒绝就绪；原子恢复并写入 recovery_complete 后就绪 |
| 复合主键 | 快照按 GORM 模型真实主键排序，`contact_labels(contact_id,label_id)` 可创建和恢复快照 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。CUS 声明合同、独立运行、事件投递、容量、版本回退和备份恢复的本轮验收项已闭合。下一项为 MED 公开合同与独立运行能力；ARCH-08 保持 REWORK。
