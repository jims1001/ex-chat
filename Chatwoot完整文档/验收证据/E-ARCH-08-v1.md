# E-ARCH-08-v1 独立存储准备与模块运行验收

## 当前结论

**ARCH-08：REWORK，未完成独立运行验收。** 本轮完成 26 模块运行清单草稿与隔离建表能力；真正的 MSG 分库存储用例失败。不得把普通回归、隔离迁移、配置清单或健康检查当作独立业务运行、拆分切换及恢复通过。

| 字段 | 实际值 |
|---|---|
| evidence_id / version | E-ARCH-08 / v1 |
| evidence_type | E-CON、E-DATA、E-QA |
| task_id | ARCH-08；发现的问题联动重开 ARCH-02、ARCH-03、ARCH-04 |
| baseline_reference | foundation-v1；模块化架构 07；实施附件 29、31、32 |
| implementation baseline | Git `2e732474d3757de739654ff24c65325b23155347`，加本轮本地工作树修改 |
| environment | macOS arm64；Go 1.26.5；Docker Engine 28.2.2；SQLite 临时文件；隔离 Docker PostgreSQL/Redis |
| validation window | 2026-09-25 UTC（本机 Australia/Sydney 为 2026-09-26）；最终回归完成于 18:32 UTC 左右 |
| result | schema/regression pass；business isolation fail；ARCH-08 REWORK |
| scope | 本地开发与隔离测试；无生产数据、真实 Provider、容量档位测量或生产切换 |
| expires_at | 模型、所有者、依赖、迁移或仓储合同变化后重新执行 |

## 状态轨迹

1. `READY → RUNNING`：按 ARCH-08 读取基础规范、运行拓扑和存储边界。
2. 发现总体台账仍写 ARCH 完成 3 项；先与当时逐项台账的 7 项同步。
3. 完成模块运行清单、隔离迁移和专用迁移命令，验证 SQLite 与 PostgreSQL。
4. 真正执行分库 MSG 创建用例，返回 `no such table: conversations`。
5. 核对仓储源码，确认前置所有权、依赖、公开合同验收范围遗漏。
6. `RUNNING → REWORK`：ARCH-02/03/04 改为 REOPENED；下一轮优先修复 ARCH-02。不得解锁 ARCH-09。

本轮没有修改旧证据文件的历史记录。当前状态以 28 分册及 32 分册为准，旧 pass 不再覆盖本证据确认的未关闭差异。

## 实际实现

| 产物 | 行为及边界 |
|---|---|
| `architecture/module_runtime.json` | 26 模块的默认单元、能力索引、启动/能力/异步依赖、现有模型、停用与恢复策略；明确标记 draft 和独立运行未实现 |
| `internal/database/model_owners.go` | 106 个持久化业务模型的唯一所有者，与 ARCH-02 索引逐项核对；LocalChangeJournal 单独按 L0 对待 |
| `internal/database/module_migration.go` | 从组合迁移的同一模型集合选择 owner；只迁移该 owner 模型及其本地日志，关闭关系递归与外键自动迁移；使用独立配置副本 |
| `cmd/module-migrate` | 必须明确选择模块及 SQLite 路径或 MODULE_DB_DSN；非法输入在连接/DDL 之前拒绝；不输出连接凭证，不自动选择现有组合数据库 |
| `test/module_runtime_test.go` | 检查 26 模块、合同及依赖注册表、模型清单之间一致性 |
| `test/module_isolation_acceptance_test.go` | 独立验收入口，创建分离的 CON/MSG 数据库后执行生产 MSG 仓储；当前失败，普通回归中明确 skip 并提示运行命令 |
| 消息重试测试修正 | 外部域名改为测试域名；固定 150ms 等待改为有限期轮询已持久化终态；计数与事件记录改为并发安全；不改变生产投递逻辑 |

107 个组合迁移模型 = 106 个持久化业务模型 + 1 个 L0 日志模型。ARCH-02 的 112 个结构体索引还包含不独立建表的 DTO/嵌入内容，因此不是 112 张业务表。API、MED、ORC 当前没有独立持久化业务模型；其本地日志表不代表这些模块业务已实现。

## 验证结果

| 验证 | 实际结果 | 结论 |
|---|---|---|
| 首次原有回归 | 消息重试测试在固定等待后查不到投递记录；HTTP 客户端已替换，但 URL 安全预检仍可能进行 DNS 解析 | fail，已修正测试等待与域名 |
| 26 模块 SQLite 隔离迁移 | 每模块独立临时库，表集合与 owner 模型+本地日志精确相等 | pass |
| 26 模块 PostgreSQL 隔离迁移 | 每模块独立随机 schema，结束后仅清理本次 schema | pass |
| 重复迁移与既有数据 | 两种数据库每模块执行两次；本地日志历史行保留，未创建其他 owner 表 | pass |
| 配置隔离与输入拒绝 | 未改变调用方 GORM 迁移选项；非法模块没有建表；空 DB 明确拒绝 | pass |
| 运行清单与源注册表 | 26 项、无缺失/重复；模型、能力与依赖一致 | pass（仅清单一致性） |
| `make test` 最终回归 | exit 0；test 包 15.437s | pass；独立运行 gate 不在此通过结论内 |
| `make test-docker` 最终回归 | exit 0；database 包 6.757s，test 包 15.360s；PostgreSQL/Redis 健康，临时容器/网络/卷清理完成 | pass |
| 相关变更 race 验证 | module-migrate、database、test 专项全部通过 | pass |
| `go vet`、`git diff --check` | 无错误 | pass |
| `make test-module-isolation` | MSG 分库创建消息返回 `SQL logic error: no such table: conversations (1)`，非零退出 | **fail，不得关闭 ARCH-08** |

Race 命令：

```bash
go test -race ./cmd/module-migrate ./internal/database ./test \
  -run 'TestModuleMigration|TestModuleRuntime|TestMessageRetryActualDelivery|ExampleModuleModels' -count=1
```

普通回归成功只证明现有组合服务未出现所测回归及新 schema 准备能力正常。独立运行验收是单独的失败门槛，不能在完成率中忽略它。

## 差异及续跑顺序

| 编号 | 发现 | 证据位置 | 状态与修复要求 |
|---|---|---|---|
| D01 | 组合 AutoMigrate 沿关联自动引入其他 owner 表，无法直接用于独立建库 | `internal/database/database.go` 及模型 GORM 关联 | schema 准备已修复；组合模式保留原行为 |
| D02 | MSG Create 在本地事务内查询并更新 CON；CON Delete 同一事务内删除 MSG Message/Attachment，且直接操作媒体文件 | `internal/repository/message_repository.go` 的 Create；`internal/repository/conversation_repository.go` 的 Delete | 未关闭；重开 ARCH-02。由各 owner 执行自身写入及 Local Journal，以事件/ORC 协调，不得用跨库共享事务替代 |
| D03 | 仓储内部 MSG→CON、CON→MSG 读写形成实际存储依赖环；旧 ARCH-03 的源码枚举未覆盖这层 | 上述仓储；`architecture/dependency_sources.json` 的 scanScopes | 未关闭；重开 ARCH-03，先去除非法边，再扩展扫描范围，不能直接登记为合法循环 |
| D04 | MSG 只有本地 DB 构造参数，没有跨存储的 CON 查询/命令接线；AttachmentRepository 在依赖注册中为 MED，但对象索引明确消息附件引用归 MSG | MSG 构造器；`architecture/dependency_sources.json`；ARCH-02 对象索引第 18 项 | 未关闭；重开 ARCH-04，统一 owner、公开查询/命令、错误/幂等/超时，并完成真实接线和复验 |
| D05 | 单进程统一路由；26 模块独立启停、并发扩缩、升级回退、备份恢复、投影重建及三种拓扑仍无全套业务证据 | `cmd/server/main.go`、`internal/router/router.go`；实施附件 31 | 未完成，属于实现/验收工作，不能伪装为外部 BLOCKED，也不能用注册表代替 |

续跑先处理 ARCH-02/03/04，再返回 ARCH-08。不可通过在 MSG 库中复制 Conversation 表来修好失败测试；这会引入第二权威数据源。跨模块变更后同步分析 ARCH-05/06/07、日志与后续业务任务的影响，受影响者重开并回归。

## ARCH-08 完成条件对照

| 完成条件 | 本轮状态 |
|---|---|
| 26 模块运行合同 | 草稿清单已建立；生产运行配置、指标及接线未完成 |
| 必需与可选依赖明确 | 已区分启动、能力及异步依赖；能力级降级尚未全面实测 |
| 独立存储、启停、扩缩、升级与恢复通过 | 仅建表隔离通过；业务隔离失败，其余待实现/验收 |
| 可选模块故障不破坏核心客服 | 未完成全模块故障验收 |
| 核心故障保护和恢复 | 未完成 |
| 提取无双主写和重复事件 | 未执行真实切换 |
| 无直接跨模块内部数据访问 | 不通过，D02/D03/D04 |
| 拆分不改变对外业务合同 | 未完成三种拓扑的同场景对照 |

当前全局计数：DONE 4、REOPENED 3、REWORK 1、PENDING 474，总数 482。本轮没有新增 DONE，不声明全系统或 ARCH-08 完成。
