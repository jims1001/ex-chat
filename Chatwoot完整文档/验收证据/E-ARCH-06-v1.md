# E-ARCH-06-v1 对外接口分层证据

| 字段 | 实际值 |
|---|---|
| evidence_id / version | E-ARCH-06 / v1 |
| evidence_type | E-ARCH、E-CON、E-SEC、E-QA |
| task_id | ARCH-06 |
| baseline_reference | foundation-v1；external-interfaces-v1；模块化架构 05 |
| environment / topology | 本地 Go 分层合同、机器可读接口注册表、生产路由源码、Docker PostgreSQL/Redis |
| started_at / finished_at | 2026-09-20T11:25:00+04:00 / 2026-09-20T11:54:02+04:00 |
| result | pass；ARCH-06 DONE |
| expires_at | 接口族、路由、动作映射、Callback、版本或错误规则变化时立即失效并重开 |

## 状态轨迹

| 时间 | 原状态 | 新状态 | 原因 |
|---|---|---|---|
| 2026-09-20T11:24:44+04:00 | PENDING | READY | ARCH-05 完成，依赖满足 |
| 2026-09-20T11:25:00+04:00 | READY | RUNNING | 开始冻结接口族、分层、动作、聚合和 Callback 合同 |
| 2026-09-20T11:40:00+04:00 | RUNNING | VERIFYING | 专项、本地全量、静态与 Docker 验证通过，进入独立复审 |
| 2026-09-20T11:54:02+04:00 | VERIFYING | DONE | 三轮独立复审完成，最终 P0/P1/P2 为 0，最终代码回归通过 |

## 机器基线

| 检查项 | 实际值 | 结果 |
|---|---:|---|
| 标准接口族 | 9/9 | pass |
| 外部处理层 | 7/7 | pass |
| 生产路由枚举 | 789（含报表注册器展开后的 V1/V2 路径） | pass |
| 写入口唯一动作映射 | 452/452 | pass |
| 有生产路由的接口族 | 7/7 | pass |
| 合同冻结但无独立 HTTP 入口 | 2（Plugin Capability、Webhook Delivery） | pass；未冒充上线 |
| 路由传输入口直接存储访问 | 0 | pass |
| 注册表 SHA-256 | `f200c859504763d94c3900a7046935d7738245842f3f39b556dca5de3007f3e9` | pass |

## 正负向验证

- 未知接口族、缺少幂等键的写动作、缺少投影新鲜度的聚合字段和未验签 Callback 均被拒绝。
- 聚合字段必须声明 owner module、稳定引用，以及投影更新时间或版本；API 注册表不拥有领域对象。
- Callback 合同要求连接、外部事件号、验签、时间窗、Receipt 去重、标准事实和补偿全部存在。
- 路由扫描展开复用的报表注册器；每个完整 `handler.Method` 生成唯一外部动作 ID并映射到 ARCH-04 已登记的 Command/ORC，直接在路由传输层出现 DB 调用或匿名写 handler 会失败。
- 邮件日志入口已从 Router 内联 DB 访问迁到 MSG owner 合同，精确重试和退信均按 `account_id + email_log_id` 限定；跨 Account 负向测试通过。
- 本证据冻结分层设计、入口映射和可复用运行时合同；不把 API-11 的逐路由幂等 receipt 接入，以及后续 API/OPEN/AUTH/INB/ENT/MOB 业务功能任务冒充为已经完成。

## 验证记录

| 时间 | 命令 | 结果 |
|---|---|---|
| 2026-09-20T11:35+04:00 | `go test ./test -run 'TestExternal\|TestEveryRouterEntrypoint' -count=1 -v` | pass；最终分类 789 个路由，452 个写入口 |
| 2026-09-20T11:40+04:00 | `go test ./... -count=1` | pass；`test` 14.703s |
| 2026-09-20T11:40+04:00 | `go vet ./...`、`git diff --check` | pass，无输出 |
| 2026-09-20T11:40+04:00 | `make test-docker` | pass；PostgreSQL/Redis healthy，`test` 14.513s；容器、网络和数据卷已清理 |
| 2026-09-20T11:42+04:00 | `go test ./... -count=1`、`go vet ./...`、`git diff --check` | pass；初审整改后的最终代码 `test` 18.053s，静态检查与差异检查无错误 |
| 2026-09-20T11:43+04:00 | `make test-docker` | pass；最终代码 PostgreSQL/Redis healthy，`test` 18.611s；容器、网络和数据卷已清理 |
| 2026-09-20T11:53+04:00 | `go test ./... -count=1`、`go vet ./...`、`git diff --check` | pass；最终代码 `test` 18.098s，静态检查和差异检查无错误 |
| 2026-09-20T11:54+04:00 | `make test-docker` | pass；最终代码 PostgreSQL/Redis healthy，`test` 16.207s；容器、网络和数据卷已清理 |

## 独立复审

- 初审发现的内联存储访问、路由展开、Provider 误分类、分层冻结和粗粒度动作映射问题均已关闭。
- 二审发现的动作语义、幂等声明边界和邮件重试跨租户风险均已关闭。
- 三审及最终确认结论：PASS；P0 0，P1 0，P2 0。
