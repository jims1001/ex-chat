# E-ARCH-08-v142 RTE 容量策略管理

| 验收项 | 结果 |
|---|---|
| 成员边界 | `rte.capacity.manage.v1` 通过 TEN `ten.permissions.check.v1` 校验账号成员 |
| 所有者写入 | 仅写 RTE 自有 CapacityPolicy，不读取或修改 TEN/IAM 表 |
| 租户唯一性 | CapacityPolicy 改为 account_id+user_id 联合唯一，不同账号可使用相同用户编号 |
| 输入约束 | user_id 必填，conversation_limit 限定为 0 到 100000 |
| 原子性 | 命令收据、策略变更和 `rte.capacity.changed.v1` 在一个 RTE 事务内提交 |
| 读回 | `rte.capacity.get.v1` 可立即按同一账号与用户读回新容量 |

专项、模块运行时、84 项隔离、完整测试、Docker PostgreSQL/Redis、`go vet ./...` 和差异检查全部通过后提交。下一项为 RTE 完整声明面、可用性事件语义与独立运行验收；ARCH-08 保持 REWORK。
