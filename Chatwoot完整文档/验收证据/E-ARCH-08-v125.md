# E-ARCH-08-v125 CON 会话创建与创建事件

| 验收项 | 结果 |
|---|---|
| 外部引用 | 创建前分别调用 TEN `ten.account.get.v1`、CHN `chn.inbox.get.v1`、CUS `cus.contact.get.v1` |
| 所有者写入 | CON 只保存账号、收件箱、联系人标识及自身会话状态，不创建 TEN/CHN/CUS 表 |
| 输入边界 | 账号 target 必须匹配；收件箱和联系人必填；优先级只允许 low/medium/high/urgent |
| 原子性 | Conversation、命令收据和 `con.conversation.created.v1` outbox 在同一事务提交 |
| 幂等 | 同一命令重放只产生 1 条会话、1 条收据和 1 个创建事件 |
| 故障边界 | CUS 停止时返回依赖不可用，CON 不写入半成品会话或收据 |
| 数据最小化 | 创建响应和事件不包含联系人令牌、收件箱密钥或外部实体内容 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 CON 会话通用更新、分配变更及变更事件；ARCH-08 保持 REWORK。
