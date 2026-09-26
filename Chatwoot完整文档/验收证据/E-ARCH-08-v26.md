# E-ARCH-08-v26 ChannelAuthEnterpriseRepository 七所有者边界

`ChannelAuthEnterpriseRepository` 已从单一共享数据库实现拆为 IAM、RTM、MIG、RPT、ENT、BIL、TEN 七个所有者存储适配器。对外兼容接口仍由组合层提供，但认证、通话、迁移、报表、企业配置、账单订阅及入门状态分别只访问自身权威库；handler、中间件和 WebSocket 接线也改为声明真实所有者接口。

原 `RecordBilling` 共享事务已拆为 BIL 账单写入和 ENT 额度投影。BIL 创建支持按已分配账单 ID 重试，ENT 使用 `billing-credit-{billingID}` 本地变更标记原子更新额度；阶段用例验证 ENT 首次不可用后重试不会重复账单或重复计费。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `72d3f4f`；E-ARCH-08-v25 |
| 阶段分库检查 | 34 个子用例通过 |
| 专项回归 | IAM/RTM/MIG/RPT/ENT/BIL/TEN 七库读写、禁用外表及 BIL→ENT 失败重试通过 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过 |
| 静态检查 | `go vet ./cmd/... ./internal/... ./pkg/... ./test/...`、`git diff --check` 通过 |

协作合同摘要为 `ff7520003cd23ba9acef4bb5d37221de12d40d5cf2ae31c21985c62e21d6cc67`。ARCH-03/04 保持 REOPENED；当前组合仍由同一进程装配，独立服务启停、恢复、扩缩、升级和备份验收尚未完成。下一项为 `CRMOpsAdvancedRepository` 的 MSG `Attachment` 访问边界及多所有者存储拆分。ARCH-08 整体仍为 REWORK。
