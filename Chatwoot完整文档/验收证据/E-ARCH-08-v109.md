# E-ARCH-08-v109 CUS 账号联系人匿名化

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `92fdd6e`；E-ARCH-08-v108 |
| 新增合同 | `cus.account_contacts.anonymize.v1`、`cus.account_deletion_status.get.v1` |
| 匿名化 | 清空姓名以外的 PII 并改为 Deleted Contact；清空邮箱、电话、标识、头像、令牌、自定义属性和公司关联；blocked=true |
| 关联清理 | 删除账号联系人所属 ContactInbox、ContactLabel、ContactNote |
| 状态查询 | 返回 contact_count、identity_link_count、pii_contact_count 和 ready |
| 隔离 | 账号 91 匿名化不修改账号 92；业务、收据和事件同事务且重放不重复 |
| 回归门禁 | 专项测试、84 项隔离、模块运行时、完整测试、Docker、`go vet ./...` 和差异检查通过 |

本证据关闭 CUS 账号联系人匿名化与删除状态查询。联系人合并、单联系人删除及完整运行验收仍待完成；ARCH-08 保持 REWORK。
