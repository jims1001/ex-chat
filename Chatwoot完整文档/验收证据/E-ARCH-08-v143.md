# E-ARCH-08-v143 RTE 完整声明面与可用性事件

| 验收项 | 结果 |
|---|---|
| Command 声明面 | 5/5：入队、领取、转移、释放、容量管理全部注册 |
| Query 声明面 | 4/4：队列、分配、候选、容量全部注册 |
| Event 声明面 | 队列、分配、容量、可用性 4 类事件均有生产路径 |
| 可用性语义 | 容量变化发布 accepting_new_conversations，并携带 capacity_limit_changed 原因 |
| 独立存储 | RTE 数据库仅迁移 RTE 所有者模型和本地日志，不含 Account/Inbox/Conversation/Message/User/Team 表 |
| 依赖故障 | 启动可注册完整合同；只有实际调用相关合同才访问 CON/TEN/CHN |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 RTE PostgreSQL 多实例、事件自动投递、混合版本回退及所有者备份恢复；ARCH-08 保持 REWORK。
