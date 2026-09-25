# E-ARCH-08-v2 分库消息链路复验

## 结论

**ARCH-08 仍为 REWORK。** 在 v1 失败的 CON/MSG 双库场景中，消息创建、CON 活动更新、故障后补偿和重复重放检查已通过。这只是一个业务路径的阶段验收；26 模块独立运行的完成条件尚未满足。

| 字段 | 实际值 |
|---|---|
| evidence_id / version | E-ARCH-08 / v2 |
| baseline_reference | E-ARCH-08-v1、foundation-v1 |
| environment | 本地 macOS；分离 SQLite 库；普通 Go 回归 |
| validation window | 2026-09-25 18:46 UTC 左右 |
| result | MSG/CON 阶段隔离通过；ARCH-08 总体验收未通过 |
| scope | 本地源代码、单进程端口调用与隔离测试；未进行生产迁移或独立服务部署 |

## 本轮改动与验证

| 项目 | 已验证结果 | 边界 |
|---|---|---|
| MSG 创建 | CON 查询端口检查目标；MSG 消息与本地 Local Change Journal 同事务写入 | 当前端口为 Go 进程内调用，尚非跨进程公开合同 |
| CON 活动 | CON 自有仓储更新未读数、活动时间和对象版本，并写本地变更记录 | 仅覆盖创建消息引起的活动 |
| CON 暂时不可用 | MSG 保留 pending 记录，补偿恢复后标记 delivered；重复补偿不重复增加未读数；模拟 CON 已提交而 MSG 未标记时重启重放 | 当前由组合服务器每 5 秒尝试；没有独立模块运行监控或分布式调度 |
| 本地日志失败 | MSG 写入回滚，不留下没有日志的消息 | 仅覆盖创建消息 |
| 26 模块迁移 | v1 已通过 SQLite/PostgreSQL 模型表隔离 | 只证明建表边界，不证明业务可运行 |
| `make test-module-isolation` | 3 个子用例通过 | 仅为 CON/MSG 创建链路的阶段 gate |
| `make test`、`make test-docker` 与 `go vet` | 通过；Docker 临时 PostgreSQL/Redis 已清理 | 组合模式回归，不等于独立部署验收 |
| `go test -race` 阶段 gate | 通过 | 仅覆盖 MSG 创建分库链路 |

`scripts/audit-module-storage.py` 从仓储源码列出跨所有者模型引用候选；本轮输出 22 个文件、106 种文件/模型/所有者组合。候选不等于已确认的违规数，但其中 CON 删除跨所有者数据、CON 未读标记读取 MSG、MSG 发件人补全读取 IAM/CUS 均是待修复的真实直接访问。AttachmentRepository 的注册所有者与模型所有者仍不一致。

## 未关闭差异

1. ARCH-02：消息创建路径已修复，仓储其余跨所有者读写仍需逐项改为 owner 合同或业务编排；删除级联尤其不能沿用共享事务。
2. ARCH-03：依赖源码扫描仍未覆盖所有仓储内部访问，非法依赖环仍在。
3. ARCH-04：只有 CON/MSG 的进程内接口；独立服务的公开查询、命令、错误、超时和路由合同未完成，附件所有者冲突未关闭。
4. ARCH-08：26 模块独立启停、三种拓扑同场景、故障/恢复、扩缩、升级/回退、备份恢复、容量与灰度验收均未执行。

因此 ARCH-02/03/04 保持 REOPENED，ARCH-08 保持 REWORK；不解锁 ARCH-09 或后续依赖。E-ARCH-08-v1 保留当时失败现场，本文件记录修复后的新事实。
