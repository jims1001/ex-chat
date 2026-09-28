# E-ARCH-08-v59 CRMOpsAdvancedHandler 批量分配团队所有者边界

原 `CRMOpsAdvancedHandler` 的 `assign_team` 批量操作在 ORC 数据库中直接读取 TEN `Team` 和 CON `Conversation`。核对实际字段使用后，该分支只需要团队摘要和会话自身数据，不需要读取 IAM 团队成员或 CHN 收件箱。本轮改为 TEN `TeamRepository.FindSummaryByID` 校验团队账号归属，并由 CON `ConversationRepository.ListByIDs` 返回账号范围内会话。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `ab48423`；E-ARCH-08-v58 |
| 阶段分库检查 | 62 个子用例通过 |
| 专项回归 | TEN/CON 分库下当前账号团队和会话可用于分配，跨账号团队与会话均不返回；两库均无对方权威表；批量团队分配回归通过 |
| 依赖清单 | 登记 ORC→TEN `TeamRepository` 源码边；ORC 已声明 TEN/CON 同步依赖，依赖摘要保持 `20ea1e19a280eb3ea89b132e865ac82ed760b6c27ab843d90f71115b9332a54c` |
| 协作合同 | 新增 `TeamRepository.FindSummaryByID` 查询绑定并复用 `ConversationRepository.ListByIDs`；协作摘要 `56e4eadafed5e011189a5f23d8f2123525a5239d3ff1a50a23b53d75eee38902` |
| 完整回归 | `make test` 通过，批量团队分配及批量与单项一致性回归通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止规则阻止恢复 Team 直接查询 |

CRMOpsAdvancedHandler 批量分配团队的 TEN/CON 访问边界已经完成。下一项为 `CRMOpsAdvancedHandler` 批量添加/删除标签时的 CON/CUS 访问边界。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。
