# E-ARCH-08-v22 ORC CompanyExtension 的 IAM/CON/MSG 边界

## 本次子项结论

已移除 `CompanyExtensionRepository` 对 IAM 用户、CON 会话以及 MSG 消息和附件表的直接访问。公司与联系人范围仍由 CUS 确定；ORC 组合层分别调用 IAM 补齐备注作者、调用 CON 查询公司会话与统计、调用 MSG 统计消息和列出附件。

公司附件的联系人过滤新增归属校验，不属于目标公司的联系人不会进入 CON/MSG 查询。依赖来源把组合调用登记在 ORC，避免形成 CUS 与 CON 的同步循环依赖。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `8e43526`；E-ARCH-08-v21 |
| 验证时间 | 2026-09-27T00:17:00+10:00 |
| 阶段分库检查 | 30 个子用例通过 |
| 专项回归 | CUS/IAM/CHN/CON/MSG 五库的备注作者、会话组合、统计、附件列表及越界联系人拒绝通过 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过 |
| 静态检查 | `go vet ./cmd/... ./internal/... ./pkg/... ./test/...`、`git diff --check` 通过 |

新增分库用例确认 CUS 数据库没有 `User`、`Conversation`、`Message` 或 `Attachment` 权威表。CON 查询按账号和 CUS 给出的联系人集合收口，MSG 查询按账号和 CON 给出的会话集合收口。

## 合同与剩余边界

协作合同新增 ORC 对 IAM `FindByID/FindByIDs`、CON `ListByContactIDs/GetStatsByContactIDs/FindIDsByContactIDs`、MSG `CountActivityByConversationIDs/ListAttachmentsByConversationIDs` 的查询绑定，合同摘要为 `337084ef50ba120bdcff812ea182e444b4a3bd7fe7112cb1a116b59f5115bb52`。ARCH-04 保持 REOPENED。

静态候选扫描仍可能因返回 DTO 中的外部领域类型列出文件；禁止查询规则和五库运行用例用于判定真实存储访问。下一项为 `ContactExtensionRepository` 的 CHN/CON/MSG 访问边界。ARCH-08 整体仍为 REWORK，完整运行验收见 [剩余验收清单](ARCH-08-剩余验收清单.md)。
