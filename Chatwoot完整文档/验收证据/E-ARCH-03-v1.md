# E-ARCH-03-v1 无循环依赖验证证据

| 字段 | 实际值 |
|---|---|
| evidence_id / version | E-ARCH-03 / v1 |
| evidence_type | E-ARCH、E-CON、E-QA |
| task_id | ARCH-03 |
| baseline_reference | module-boundary-v1；ARCH-02 所有权索引；模块化架构 02 |
| environment / topology | 本地源码、机器可读依赖清单与组合部署拓扑 |
| started_at / finished_at | 2026-09-16T15:52:49+04:00 / 2026-09-16T16:40:00+04:00 |
| result | pass；ARCH-03 DONE |
| expires_at | 依赖清单、模块合同或源码跨模块调用变化时立即失效并重开 |

## 状态轨迹

| 时间 | 原状态 | 新状态 | 原因 |
|---|---|---|---|
| 2026-09-16T15:47:20+04:00 | PENDING | READY | ARCH-01、ARCH-02 完成，依赖满足 |
| 2026-09-16T15:52:49+04:00 | READY | RUNNING | 开始登记全部依赖类型并执行机器拓扑检查 |
| 2026-09-16T15:56:57+04:00 | RUNNING | VERIFYING | 机器拓扑、本地全量、go vet 和 Docker PostgreSQL/Redis 全量测试通过，进入独立复审 |
| 2026-09-16T16:02:00+04:00 | VERIFYING | REWORK | 独立复审发现四条真实同步依赖漏报、AIC→BIL 推翻预设顺序，且测试只能证明 manifest 自洽 |
| 2026-09-16T16:06:04+04:00 | REWORK | RUNNING | 补齐真实依赖并把 BIL 前移；新增独立来源索引、双向集合比较、冻结层级和三类拓扑/变异测试 |
| 2026-09-16T16:07:41+04:00 | RUNNING | VERIFYING | 返工后专项、本地全量、go vet、Docker PostgreSQL/Redis 全量验证通过，进入第二轮独立复审 |
| 2026-09-16T16:12:00+04:00 | VERIFYING | REWORK | 第二轮复审确认来源索引未反向扫描源码，并发现 RTE→MSG、OPS→CON/MSG 漏报 |
| 2026-09-16T16:14:46+04:00 | REWORK | RUNNING | 增加 13 个源码扫描范围、Repository 所有者映射和源码到证据/清单反向校验；补齐 RTE→MSG、OPS→CON/MSG、AIC→OPS |
| 2026-09-16T16:16:05+04:00 | RUNNING | VERIFYING | 第二轮返工后的专项、本地全量、go vet、差异检查及 Docker PostgreSQL/Redis 全量测试通过，进入第三轮独立复审 |
| 2026-09-16T16:18:00+04:00 | VERIFYING | REWORK | 第三轮复审发现扫描范围仍为人工白名单，遗漏 OPS handler 的 EXT、NTF、QLT 同步依赖 |
| 2026-09-16T16:22:00+04:00 | REWORK | RUNNING | 建立生产 handler/service 文件自动枚举规则与唯一 scope 校验，补齐对象所有者和 OPS 漏边 |
| 2026-09-16T16:27:00+04:00 | RUNNING | VERIFYING | 自动枚举专项、本地全量、go vet、差异检查及 Docker PostgreSQL/Redis 全量测试通过，进入第四轮独立复审 |
| 2026-09-16T16:30:00+04:00 | VERIFYING | REWORK | 第四轮复审发现源码扫描边未与 observedCode 双向相等，且 Service 扫描仅覆盖特例 |
| 2026-09-16T16:34:00+04:00 | REWORK | RUNNING | 扫描全部 `service.XService` 引用并登记 owner；增加扫描边与 observedCode 边集合反向校验 |
| 2026-09-16T16:38:00+04:00 | RUNNING | VERIFYING | Repository/Service 双向证据专项、本地全量、go vet、差异检查及 Docker 全量验证通过，进入第五轮独立复审 |
| 2026-09-16T16:40:00+04:00 | VERIFYING | DONE | 第五轮独立提交前复审 PASS；全部发现关闭，ARCH-03-D01 关闭 |

## 待验证项

- 26 个模块全部进入依赖图且各出现一次；
- 同步调用、事件来源、共享能力、启动依赖分别登记；
- 未登记引用、低层反向依赖、下层依赖 API/RTM/ORC 均为 0；
- 同步图、事件图和合并图拓扑排序均包含 26 个模块；
- 直接互相依赖、间接依赖环、事件触发环均为 0；
- 依赖清单变化能够通过自动测试使 ARCH-03 重新失败。

## 机器基线

| 检查项 | 实际值 | 当前结果 |
|---|---:|---|
| 模块数 | 26 | pass |
| 同步依赖边 | 123 | pass |
| 事件来源边 | 65 | pass |
| 共享能力边 | 6 | pass |
| 启动依赖边 | 5 | pass |
| 合并去重依赖边 | 184 | pass |
| 未知模块、自依赖、重复模块/顺序 | 0 | pass |
| 低层反向依赖 | 0 | pass |
| 下层依赖 API/RTM/ORC | 0 | pass |
| 直接、间接与事件环 | 0 | pass |
| 拓扑排序覆盖 | 26/26 | pass |
| 批准清单 SHA-256 | `3b62633653a389988b4fcea42eda1b2ea13ed7b24e7f3de2518ec887f8c856f9` | pass |

提供者优先顺序：

`IAM → TEN → ENT → SYS → CHN → CUS → MED → CON → MSG → RTE → TKT → QLT → KB → BIL → EXT → NTF → AUT → OPS → AIC → MIG → RPT → SRH → ORC → API → RTM → AUD`

## 可复核验证记录

| 时间 | 命令 | 结果 |
|---|---|---|
| 2026-09-16T15:54+04:00 | `go test ./test -run TestArchitectureDependencyGraphIsCompleteAndAcyclic -count=1 -v` | pass；26/26，循环 0 |
| 2026-09-16T15:55+04:00 | `go test ./... -count=1` | pass；`test` 15.286s，其余 package 全部通过 |
| 2026-09-16T15:55+04:00 | `go vet ./...` | pass，无输出 |
| 2026-09-16T15:56+04:00 | `make test-docker` | pass；PostgreSQL/Redis healthy，`test` 14.771s；容器、网络和数据卷已清理 |
| 2026-09-16T15:56+04:00 | `git diff --check` | pass，无输出 |
| 2026-09-16T16:15+04:00 | `go test ./test -run 'TestArchitectureDependency' -count=1` | pass；源码反向扫描、来源集合、三类拓扑和循环变异检查全部通过 |
| 2026-09-16T16:15+04:00 | `go test ./... -count=1` | pass；`test` 14.163s，其余 package 全部通过 |
| 2026-09-16T16:15+04:00 | `go vet ./...` | pass，无输出 |
| 2026-09-16T16:15+04:00 | `git diff --check` | pass，无输出 |
| 2026-09-16T16:16+04:00 | `make test-docker` | pass；PostgreSQL/Redis healthy，`test` 14.352s；容器、网络和数据卷已清理 |
| 2026-09-16T16:26+04:00 | `go test ./... -count=1` | pass；`test` 17.901s，其余 package 全部通过 |
| 2026-09-16T16:26+04:00 | `go vet ./...`、`git diff --check` | pass，无输出 |
| 2026-09-16T16:27+04:00 | `make test-docker` | pass；PostgreSQL/Redis healthy，`test` 17.721s；容器、网络和数据卷已清理 |
| 2026-09-16T16:37+04:00 | `go test ./... -count=1` | pass；`test` 16.368s，其余 package 全部通过 |
| 2026-09-16T16:37+04:00 | `go vet ./...`、`git diff --check` | pass，无输出 |
| 2026-09-16T16:38+04:00 | `make test-docker` | pass；PostgreSQL/Redis healthy，`test` 14.919s；容器、网络和数据卷已清理 |

## 独立复审返工关闭进度

| 发现 | 修复 | 当前验证 |
|---|---|---|
| 漏报 AUT→CUS、RPT→CON、AIC→TKT/BIL | 同步更新机器清单、冻结矩阵与来源索引 | synchronous 119；来源与 manifest 双向集合一致 |
| AIC→BIL 推翻原顺序 | BIL 从 order 19 前移至 order 14，AIC 调整为 order 19 | 新 provider-first 顺序 26/26 |
| manifest 只能自洽，无法证明来源 | 新增 `dependency_sources.json`；逐类双向比较，并核对已观察源码文件和 repository symbol | missing 0、extra 0、source symbol missing 0 |
| 层级和拓扑缺少独立基线及变异证明 | 测试内冻结 26 模块 layer/order；分别检查同步/事件/合并图；增加直接二环、间接三环、事件环变异用例 | 所有专项测试通过 |
| 来源索引未反向覆盖源码，漏报 RTE→MSG、OPS→CON/MSG | 对 13 个已归属服务/处理器扫描 Repository 引用，逐项校验 owner、observedCode 和同步边；补 AIC→OPS | 源码扫描范围 13；未登记 owner、证据和同步边均为 0；来源模块唯一且集合完整 |
| 扫描 scope 仍是人工白名单，遗漏 OPS→EXT/NTF/QLT | 自动枚举生产 handler/service；含依赖符号的文件必须唯一归属，否则测试失败；无符号文件按冻结规则排除 | 生产目录漏 scope 0、重复 scope 0、未知 owner 0；同步边 123 |
| observedCode 只校验单向子集，Service 仅扫描特例 | 全量扫描 `service.XService`；扫描边和 observedCode 边必须双向一致 | 未登记 Service owner 0、扫描边缺失证据 0、失效证据 0 |

最终验证：`go test ./... -count=1` pass（`test` 16.368s）；`go vet ./...` pass；`make test-docker` pass（`test` 14.919s，资源已清理）；`git diff --check` pass；第五轮独立复审 PASS。
