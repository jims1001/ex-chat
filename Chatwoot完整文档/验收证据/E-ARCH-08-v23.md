# E-ARCH-08-v23 ORC ContactExtension 的 CHN/CON/MSG 边界

## 本次子项结论

已移除 `ContactExtensionRepository` 对 CHN 收件箱、CON 会话、MSG 消息及附件表的直接访问。CUS 继续保存联系人和 `ContactInbox` 渠道身份关系；ORC 组合层通过 CHN 查询收件箱及校验账号归属，通过 CON 查询会话范围、列表和统计，通过 MSG 查询消息与附件。

联系人渠道关系列表不再 `JOIN inboxes` 或 `Preload("Inbox")`。新增、删除关系前由 CHN 校验收件箱属于当前账号；列表只组合当前账号可见的收件箱。会话、消息和附件查询均按账号及联系人对应会话集合收口。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `195d911`；E-ARCH-08-v22 |
| 验证时间 | 2026-09-27T00:25:00+10:00 |
| 阶段分库检查 | 31 个子用例通过 |
| 专项回归 | CUS/CHN/CON/MSG 四库的渠道组合、跨账号拒绝、会话列表、统计及附件查询通过 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过 |
| 静态检查 | `go vet ./cmd/... ./internal/... ./pkg/... ./test/...`、`git diff --check` 通过 |

新增分库用例确认 CUS 数据库没有 `Inbox`、`Conversation`、`Message` 或 `Attachment` 权威表；账号 1 不能使用账号 2 的 CHN 收件箱创建联系人渠道关系。

## 合同与剩余边界

协作合同新增 ORC 对 CHN `FindByID/FindByIDs/ListByAccount/Exists`、CON `FindIDsByContactIDs/ListByContactIDs/GetStatsByContactIDs`、MSG `ListAttachmentsByConversationIDs/CountActivityByConversationIDs` 的查询绑定，合同摘要为 `e7df267f0e5ab0380144f5df1f88158692966e16a593eee6b752740a8742b617`。ARCH-04 保持 REOPENED。

静态候选扫描仍会因返回 DTO 中的 `Inbox` 和 `Conversation` 类型列出该文件；禁止查询规则和四库运行用例确认不存在跨库访问。下一项为 `ContactRepository` 的 CON/MSG/TKT/BIL/OPS/RTM/API 跨所有者访问边界。ARCH-08 整体仍为 REWORK，完整运行验收见 [剩余验收清单](ARCH-08-剩余验收清单.md)。
