# E-ARCH-08-v25 LabelRepository 会话标签关系边界

`LabelRepository` 已不再直接创建、删除或联表查询 CON 权威数据 `ConversationLabel`。CUS 只校验和读写标签、联系人标签；会话标签的关联、解除、ID 查询及标签删除时的关系清理由 `ConversationRepository` 在 CON 库执行，并统一携带 `accountID` 校验会话和标签租户。

组合仓储登记为 ORC→CON 同步边界。读取先由 CON 返回会话范围内的标签 ID，再由 CUS 按账号加载标签；删除先由 CON 幂等清理该账号会话下的关系，再删除 CUS 标签。OPS 接口也改为传递当前账号，避免仅凭会话 ID 访问关系。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `c271fa1`；E-ARCH-08-v24 |
| 阶段分库检查 | 33 个子用例通过 |
| 专项回归 | CUS/CON 双库关联、查询、解除、删除及跨账号拒绝通过 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过 |
| 静态检查 | `go vet ./cmd/... ./internal/... ./pkg/... ./test/...`、`git diff --check` 通过 |

协作合同摘要为 `a4b44b7afd3e9f101899607c0f66ea984e87ba0f63c9e2a7f3b90d0464a016bf`。ARCH-03/04 保持 REOPENED；当前适配器仍为进程内调用，独立服务启停、恢复、扩缩、升级和备份验收尚未完成。下一项为 `ChannelAuthEnterpriseRepository` 的 IAM/RTM/MIG/RPT/ENT/BIL/TEN 跨所有者访问边界。ARCH-08 整体仍为 REWORK。
