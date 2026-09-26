# E-ARCH-08-v42 AutomationService CUS 联系人/公司条件边界

原 `AutomationService` 在 AUT 数据库中直接查询 Contact 和 Company，且查询只按 ID，未带账号范围。本轮将邮箱、电话、国家、城市和公司名称条件的数据读取统一交给 CUS `ContactRepository.FindByID(accountID, contactID)`，Company 由 CUS 在联系人视图中组合。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `dd52b08`；E-ARCH-08-v41 |
| 阶段分库检查 | 49 个子用例通过 |
| 专项回归 | AUT/CON/CUS 分库下，同账号联系人/公司条件命中，跨账号联系人不命中，AUT 不含 Contact/Company 表 |
| 依赖清单 | 补充 AUT→CUS `ContactRepository` 源码边；依赖摘要保持 `0d326344465397bbc651fbce06879fe19bf683bf15d51188d8aa8c5c311002b0` |
| 协作合同 | 复用 CUS 进程内所有者查询；摘要保持 `93b0298edced86759832801a5086083e885fd024d03ba571d28a624c291b89a0` |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；合同规则禁止恢复 AUT 对 Contact/Company 的直接查询 |

ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。下一项为 `AutomationService` 标签条件中的 CON/CUS `ConversationLabel` 访问边界。
