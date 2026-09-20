# E-ARCH-05-v1 插件扩展点证据

| 字段 | 实际值 |
|---|---|
| evidence_id / version | E-ARCH-05 / v1 |
| evidence_type | E-ARCH、E-CON、E-SEC、E-QA |
| task_id | ARCH-05 |
| baseline_reference | foundation-v1；plugin-extensions-v1；模块化架构 04 |
| environment / topology | 本地 Go 运行时合同、机器可读扩展点注册表、Docker PostgreSQL/Redis |
| started_at / finished_at | 2026-09-20T10:00:00+04:00 / 2026-09-20T11:24:44+04:00 |
| result | pass；ARCH-05 DONE |
| expires_at | 扩展点、公开入口、清单字段、授权或隔离规则变化时立即失效并重开 |

## 状态轨迹

| 时间 | 原状态 | 新状态 | 原因 |
|---|---|---|---|
| 2026-09-19T18:00:00+04:00 | PENDING | READY | ARCH-04 完成，依赖满足 |
| 2026-09-20T10:00:00+04:00 | READY | RUNNING | 开始冻结扩展点、清单、授权、生命周期与隔离规则 |
| 2026-09-20T11:15:00+04:00 | RUNNING | VERIFYING | 专项、本地全量、静态和 Docker 验证完成，进入独立复审 |
| 2026-09-20T11:24:44+04:00 | VERIFYING | DONE | 四轮独立复审完成，最终 P0/P1 为 0，证据与最终代码验证一致 |

## 机器基线

| 检查项 | 实际值 | 结果 |
|---|---:|---|
| 标准扩展点 | 12/12 | pass |
| 已登记现有入口 | 12/12 | pass |
| 入口 owner 匹配 | 12/12 | pass |
| 必填清单字段 | 39/39 | pass |
| 生命周期状态 | 7/7 | pass |
| 调用结果状态 | 7/7 | pass |
| 设计安全/隔离规则 | 8/8 | pass |
| 已登记入口缺少真实源码 | 0 | pass |
| 注册表 SHA-256 | `df64a669c2a92666bef5b2e7c886893e4d4f1f05949cecbda4dff8f37313b5fa` | pass |

## 正负向验证

- 完整插件清单、有效联合授权、有效调用和签名事件投递均通过。
- 通配网络域名、非 HTTPS、缺少补偿的高风险能力、跨 Account、未确认高风险动作、过期调用和未签名事件均被拒绝。
- 入口的 `storageOwner` 必须与 ARCH-02/03 的组件 owner 索引一致，且每个登记入口必须解析到真实生产源码；底层 DB 逃逸由 ARCH-04 的生产扫描继续禁止。
- 失败模式逐扩展点冻结为后续实现任务的强制合同。本证据不把安装中心、调用执行器、投递队列或隔离运行时冒充为已经上线；它们由 EXT、CHAN、AI、MIG、NTF 后续任务逐项接入和验收。

## 验证记录

| 时间 | 命令 | 结果 |
|---|---|---|
| 2026-09-20T11:05+04:00 | `go test ./test -run 'TestPluginExtension\|TestPluginContracts' -count=1` | pass |
| 2026-09-20T11:09+04:00 | `go test ./... -count=1` | fail；既有 `Gap7_WebSocket_Realtime_Connection` 广播读取 2 秒超时 |
| 2026-09-20T11:10+04:00 | `go test ./... -count=1` | pass；`test` 14.381s，确认前次为非确定性时序波动 |
| 2026-09-20T11:10+04:00 | `go vet ./...`、`git diff --check` | pass，无输出 |
| 2026-09-20T11:15+04:00 | `make test-docker` | pass；PostgreSQL/Redis healthy，`test` 14.640s；容器、网络和数据卷已清理 |
| 2026-09-20T11:24+04:00 | `go test ./... -count=1`、`go vet ./...`、`git diff --check` | pass；最终代码 `test` 15.472s，静态检查和差异检查无错误 |
| 2026-09-20T11:24+04:00 | `make test-docker` | pass；最终代码 PostgreSQL/Redis healthy，`test` 16.241s；容器、网络和数据卷已清理 |

## 独立复审

- 第一轮、第二轮和第三轮发现的清单约束、网络边界、高风险降级、入口源码证明、运行时映射与反向集合冻结问题均已关闭。
- 第四轮复审结论：PASS；P0 0，P1 0。
