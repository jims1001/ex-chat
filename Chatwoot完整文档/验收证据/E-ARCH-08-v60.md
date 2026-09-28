# E-ARCH-08-v60 CRMOpsAdvancedHandler 批量标签所有者边界

原 `CRMOpsAdvancedHandler` 的 `add_labels` 和 `remove_labels` 批量操作在 ORC 数据库中直接查询 CON `Conversation`；删除标签时还直接查询 CUS `Label`。本轮改为 CON `ConversationRepository.ListByIDs` 筛选账号范围内会话，由 CUS `LabelRepository.FindIDsByTitleOrID` 解析账号范围内标签，再通过 CON 的标签关系命令添加或删除；原有按数字标签 ID 删除的兼容回退保持不变。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `b2e91c6`；E-ARCH-08-v59 |
| 阶段分库检查 | 63 个子用例通过 |
| 专项回归 | CON/CUS 分库下当前账号会话可添加和删除 CUS 标签，跨账号会话被 CON 排除，跨账号标签被 CUS 拒绝；两库均无对方权威表 |
| 依赖清单 | 登记 ORC→CUS `LabelRepository` 源码边；ORC 已声明 CON/CUS 同步依赖，依赖摘要保持 `20ea1e19a280eb3ea89b132e865ac82ed760b6c27ab843d90f71115b9332a54c` |
| 协作合同 | 新增 `LabelRepository.FindIDsByTitleOrID` 读取合同并复用 `ConversationRepository.ListByIDs` 及标签关系命令；协作摘要 `da8c96083e6aaacfcc1855d383f962f42a2d8737182b6cc3953549f3fbf08dcd` |
| 完整回归 | `make test` 通过，批量标签生命周期、批量与单项一致性及自动化删除标签回归通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器、网络及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止规则阻止恢复 Conversation 和 Label 直接查询 |

CRMOpsAdvancedHandler 批量添加/删除标签的 CON/CUS 访问边界已经完成。下一项为 `CRMOpsAdvancedHandler` 公司联系人添加回读及解除关联时的 CUS 访问边界。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。
