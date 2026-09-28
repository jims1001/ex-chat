# E-ARCH-08-v89 Event outbox、去重与版本恢复

本轮为独立模块运行时增加 Event 传输。生产端将完整 EventEnvelope 与业务事实写入同一所有者事务；调度器只投递 pending outbox；消费端在自己的事务中应用投影并保存 inbox 收据。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `7e5dfdd`；E-ARCH-08-v88 |
| 传输入口 | `POST /contracts/events/{event_type}`，沿用模块调用方、Bearer、合同版本和相关 ID 门禁 |
| 原子 outbox | 事件与生产端业务写入同事务；事务失败时两者均回滚 |
| 消费原子性 | 投影处理与消费收据同事务；处理失败时两者均回滚 |
| 重复事件 | 相同 Event ID 第二次投递返回 duplicate，处理器只执行一次 |
| 乱序事件 | 聚合版本必须逐一递增；版本 1 后直接投递版本 3 返回 409，并给出期望版本 2 |
| 缺口恢复 | 补投版本 2 后版本 3 可成功消费，处理次数与三个唯一版本一致 |
| 停止恢复 | 消费端停止时 outbox 保持 pending；同址恢复后调度器补投成功并标记 delivered |
| 阶段分库检查 | 84 个子用例全部通过 |
| 完整及容器回归 | `make test`、`make test-docker` 通过，Docker 临时资源已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过 |
| 运行清单摘要 | `7d234da5d3869b086dc089c9c88ea1772dabf3d8d745858d98ae9112feb4a0cd` |

当前以 IAM `identity.changed` → TEN 消费链路证明通用 Event 基础设施，尚未将 26 个模块的全部登记事件接入。下一项处理运行时版本升级、旧新版本并存和回退后事实保留。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
