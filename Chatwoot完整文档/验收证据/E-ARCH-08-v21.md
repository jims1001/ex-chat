# E-ARCH-08-v21 TEN AccountRepository 的 IAM 用户边界

## 本次子项结论

已补齐 `AccountRepository` 创建账号成员前的 IAM 用户校验。TEN 继续只保存 `AccountUser`、账号和自定义角色；用户身份详情通过 IAM `UserRepository` 查询和组合。

`AddMember` 和 `AddMemberWithRole` 在写入 TEN 前调用 IAM `FindByID`。用户不存在时返回未找到，IAM 不可用时传播依赖错误，不会留下孤立成员关系。成员详情、成员列表及容量策略成员继续通过 IAM `FindByIDs` 批量补齐，`UpdateMember` 显式排除用户关联写入。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `0de7e70`；E-ARCH-08-v20 |
| 验证时间 | 2026-09-26T14:08:00+00:00 |
| 阶段分库检查 | 29 个子用例通过 |
| 专项回归 | TEN/IAM 双库新增成员、身份组合、不存在用户拒绝、跨账号隔离及 IAM 故障通过 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过 |
| 静态检查 | `go vet ./cmd/... ./internal/... ./pkg/... ./test/...`、`git diff --check` 通过 |

新增用例确认 TEN 数据库未创建 `User` 权威表；不存在的 IAM 用户和 IAM 停机期间的新增请求均不会改变 `AccountUser` 数量。账号 2 的成员查询不会返回账号 1 的关系。

## 合同与剩余边界

依赖来源登记 `AccountRepository` 对 IAM `FindByID`、`FindByIDs` 的查询合同，协作合同摘要为 `597d0d11778be009410b21739c1d0ea53d38283067681af089252c0afc4e7f23`。ARCH-04 保持 REOPENED。

静态候选扫描仍会因成员返回 DTO 中的 `domain.User` 类型列出该文件，但禁止直接查询规则和 TEN/IAM 双库用例确认不存在 IAM 表访问。下一项为 CUS `CompanyExtensionRepository` 的 IAM 用户及 CON 会话访问边界。ARCH-08 整体仍为 REWORK，完整运行验收见 [剩余验收清单](ARCH-08-剩余验收清单.md)。
