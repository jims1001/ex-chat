# E-ARCH-08-v28 OPS Macro 所有权边界

原 `ops_repository.go` 同时放置 OPS Macro、NTF Notification 和 QLT CSAT 三个仓储；Macro 执行还在 OPS 数据库事务中直接读写 CON 会话、TEN 成员和团队、CUS 标签、MSG 消息及附件。本轮已将三个仓储拆为各自文件，并把 Macro 改为所有者合同编排。

Macro 模板仍由 OPS 保存；会话存在性及状态、优先级、分配、静音和休眠更新由 CON 执行；成员和团队由 TEN 校验；标签由 CUS 管理并通过 CON 保存会话标签关系；消息和附件由 MSG 创建。跨账号成员或团队不会写入会话。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `74677b1`；E-ARCH-08-v27 |
| 阶段分库检查 | 36 个子用例通过 |
| 专项回归 | OPS/CON/TEN/CUS/MSG 五库隔离下，优先级、成员、团队、标签、回复、附件及外部表禁用通过 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过 |
| 静态检查 | `go vet ./cmd/... ./internal/... ./pkg/... ./test/...`、`git diff --check` 通过 |

协作合同摘要为 `ec1441baf927a65843846eaddb7b75450a7d014312777a91f8757bfcd063357b`。跨库宏已不再依赖单库事务；当前进程内合同会把某一步失败明确写入该会话结果，完整分布式补偿和独立服务故障恢复仍属于 ARCH-08 后续验收。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。下一项为 `PlatformRepository` 的 TEN/IAM 跨所有者访问边界。
