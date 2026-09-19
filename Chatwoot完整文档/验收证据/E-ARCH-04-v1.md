# E-ARCH-04-v1 模块接口与事件合同证据

| 字段 | 实际值 |
|---|---|
| evidence_id / version | E-ARCH-04 / v1 |
| evidence_type | E-ARCH、E-CON、E-QA |
| task_id | ARCH-04 |
| baseline_reference | foundation-v1；module-dependencies-v1；模块化架构 03 |
| environment / topology | 本地 Go 运行时合同、机器可读模块合同注册表、Docker PostgreSQL/Redis |
| started_at / finished_at | 2026-09-16T16:45:00+04:00 / 2026-09-19T18:00:00+04:00 |
| result | pass；ARCH-04 DONE；第七轮独立复审 PASS |
| expires_at | 公共合同字段、模块集合、合同注册表或生产跨模块调用变化时立即失效并重开 |

## 状态轨迹

| 时间 | 原状态 | 新状态 | 原因 |
|---|---|---|---|
| 2026-09-16T16:40:00+04:00 | PENDING | READY | ARCH-01 至 ARCH-03 完成，依赖满足 |
| 2026-09-16T16:45:00+04:00 | READY | RUNNING | 开始冻结公共封装和 26 模块合同注册表 |
| 2026-09-16T16:52:00+04:00 | RUNNING | VERIFYING | 专项、本地全量、go vet、差异检查和 Docker 全量测试通过，进入独立复审 |
| 2026-09-16T16:58:00+04:00 | VERIFYING | REWORK | 独立复审发现公共封装与负向测试不完整，且源码依赖未逐边映射到具体合同 |
| 2026-09-16T17:04:00+04:00 | REWORK | RUNNING | 强制 actor ID；Capability 嵌套标准 Command/Query；约束错误策略；新增事实后缀白名单、全类型负向测试和源码边合同绑定 |
| 2026-09-16T17:08:00+04:00 | RUNNING | VERIFYING | 返工后专项、本地全量、go vet、差异检查及 Docker 全量验证通过，进入第二轮独立复审 |
| 2026-09-16T17:13:00+04:00 | VERIFYING | REWORK | 第二轮复审发现 provider 默认绑定不能证明 Label、CannedResponse 等对象的具体合同，且超时 Query 未校验注册真实性 |
| 2026-09-16T17:19:00+04:00 | REWORK | RUNNING | 新增 26 类实际源码组件的对象级合同绑定与 observed 全键解析；补对象合同和超时策略/Query 注册校验 |
| 2026-09-16T17:24:00+04:00 | RUNNING | VERIFYING | 对象级协作映射返工后的专项、本地全量、go vet、差异检查及 Docker 全量验证通过，进入第三轮独立复审 |
| 2026-09-16T17:30:00+04:00 | VERIFYING | REWORK | 第三轮复审确认 symbol 级绑定仍无法区分具体方法；需升级为 consumer/provider/file/symbol/method 独立键 |
| 2026-09-16T17:38:00+04:00 | REWORK | RUNNING | 自动提取实际字段方法调用并建立 292 条独立 edge binding；31 个组件按方法读写绑定具体合同与副本事实 |
| 2026-09-16T17:43:00+04:00 | RUNNING | VERIFYING | 方法级独立绑定返工后的专项、本地全量、go vet、差异检查及 Docker 全量验证通过，进入第四轮独立复审 |
| 2026-09-16T17:48:00+04:00 | VERIFYING | REWORK | 第四轮复审发现 GetDB 跨模块共享存储、FindOrCreate 写操作误分类及 Disable/Order/Draft 合同语义错配，禁止以登记替代代码整改 |
| 2026-09-19T17:45:00+04:00 | REWORK | VERIFYING | handler 的底层 DB 获取已全部收口为仓储/服务合同；新增禁止存储逃逸断言，并修正 FindOrCreate、Disable、Order、Draft 的方法级语义 |
| 2026-09-19T17:49:00+04:00 | VERIFYING | REWORK | 第五轮独立复审 BLOCK：发现错误 owner 仓储聚合跨域表、Label FindOrCreate 与消息属性更新语义漏标、可选依赖缺少保护 |
| 2026-09-19T17:54:00+04:00 | REWORK | VERIFYING | 按 owner 拆分 TEN/CHN/CUS/CON/MSG/QLT 查询，新增仓储边界与固定语义断言，并为可选 handler 依赖补配置错误保护 |
| 2026-09-19T18:00:00+04:00 | VERIFYING | DONE | 第七轮独立复审无 P0/P1；全量、静态、Docker、owner/合同/依赖验证全部通过 |

## 机器基线

| 检查项 | 实际值 | 结果 |
|---|---:|---|
| 模块 | 26/26 | pass |
| Command | 119 | pass |
| Query | 87 | pass |
| Event | 98 | pass |
| Command 多所有者 | 0 | pass |
| 缺少 Command/Query/Event 的模块 | 0 | pass |
| 无版本或错误前缀合同 | 0 | pass |
| 伪装动作请求的 Event | 0 | pass |
| 标准错误类型/重试策略 | 11/11 | pass |
| 合同注册表 SHA-256 | `a262a62fddc63fcd137a0f715e730f21da29472211812bd1e1159a8472a5ce51` | pass |
| 协作绑定提供模块 | 26/26 | pass |
| 已观察源码边缺少合同绑定 | 0 | pass |
| 对象级组件绑定 | 33/33 个实际跨模块组件 | pass |
| 方法级源码边绑定 | 307/307 | pass |
| 协作绑定 SHA-256 | `ef188ae3288e7c0f0d0272ccfb3908840d59208063f8d78985c5165618317863` | pass |

## 验证记录

| 时间 | 命令 | 结果 |
|---|---|---|
| 2026-09-16T16:49+04:00 | `go test ./test -run 'TestModuleContract|TestPublicContract' -count=1` | pass |
| 2026-09-16T16:50+04:00 | `go test ./... -count=1` | pass；`test` 14.966s |
| 2026-09-16T16:50+04:00 | `go vet ./...`、`git diff --check` | pass，无输出 |
| 2026-09-16T16:52+04:00 | `make test-docker` | pass；PostgreSQL/Redis healthy，`test` 16.802s；容器、网络和数据卷已清理 |
| 2026-09-16T17:07+04:00 | `go test ./... -count=1` | pass；`test` 15.116s |
| 2026-09-16T17:07+04:00 | `go vet ./...`、`git diff --check` | pass，无输出 |
| 2026-09-16T17:08+04:00 | `make test-docker` | pass；PostgreSQL/Redis healthy，`test` 14.777s；容器、网络和数据卷已清理 |
| 2026-09-16T17:23+04:00 | `go test ./... -count=1` | pass；`test` 17.762s |
| 2026-09-16T17:23+04:00 | `go vet ./...`、`git diff --check` | pass，无输出 |
| 2026-09-16T17:24+04:00 | `make test-docker` | pass；PostgreSQL/Redis healthy，`test` 15.468s；容器、网络和数据卷已清理 |
| 2026-09-16T17:42+04:00 | `go test ./... -count=1` | pass；`test` 15.366s |
| 2026-09-16T17:42+04:00 | `go vet ./...`、`git diff --check` | pass，无输出 |
| 2026-09-16T17:43+04:00 | `make test-docker` | pass；PostgreSQL/Redis healthy，`test` 15.637s；容器、网络和数据卷已清理 |
| 2026-09-19T17:45+04:00 | `go test ./test -run 'TestModuleContract\|TestPublicContract\|TestSourceCollaborations' -count=1` | pass；合同注册、公共封装、296 条方法级协作边及存储逃逸禁令全部通过 |
| 2026-09-19T17:46+04:00 | `go test ./... -count=1` | pass；`test` 15.195s |
| 2026-09-19T17:46+04:00 | `go vet ./...`、`git diff --check` | pass，无输出 |
| 2026-09-19T17:47+04:00 | `make test-docker` | pass；PostgreSQL/Redis healthy，`test` 14.890s；容器、网络和数据卷已清理 |
| 2026-09-19T17:55+04:00 | `go test ./test -run 'TestArchitectureDependencyGraphIsCompleteAndAcyclic\|TestModuleContract\|TestPublicContract\|TestSourceCollaborations\|TestOwnerRepositories' -count=1` | pass；owner 仓储、固定方法语义、307 条源码边和无环依赖均通过 |
| 2026-09-19T17:56+04:00 | `go test ./... -count=1` | pass；`test` 15.347s |
| 2026-09-19T17:56+04:00 | `go vet ./...`、`git diff --check` | pass，无输出 |
| 2026-09-19T17:57+04:00 | `make test-docker` | pass；PostgreSQL/Redis healthy，`test` 15.254s；容器、网络和数据卷已清理 |

## 合同闭环

- Command 公共封装强制 command_id、account/actor/target、idempotency_key、correlation/causation、timeout/status query。
- Query 公共封装强制租户与权限主体、过滤、稳定排序、分页位置、字段白名单、一致性和请求时间。
- Event 公共封装强制全局 ID、来源模块、聚合版本、事实/发布时间、因果链、变化字段、最小 payload 和敏感等级。
- Process Signal 只报告步骤事实；Capability Call 必须且只能嵌套一个通过完整校验的标准 Command 或 Query。
- 11 类 ContractError 的 retryable/retry_after 必须与冻结重试策略一致。
- 模块合同、协作绑定与依赖矩阵共享同一 26 模块集合；33 个实际跨模块组件的 307 条方法调用均解析到提供模块拥有的具体读、写和副本事实合同。
