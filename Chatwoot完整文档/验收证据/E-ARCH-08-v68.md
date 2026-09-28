# E-ARCH-08-v68 CRMOpsAdvancedHandler Shopify 会话联系人所有者边界

`ShopifyOrders` 原先由 ORC 直接查询 CON `Conversation` 并预加载 CUS `Contact`，用于从会话解析客户邮箱。本轮改为先通过 CON `ConversationRepository.FindSummaryByID` 读取账号范围内的会话摘要，再通过 CUS `ContactRepository.FindSummaryByID` 获取同账号联系人邮箱；ORC 不再查询或预加载两类权威表。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `11b415a`；E-ARCH-08-v67 |
| 阶段分库检查 | 71 个子用例通过 |
| 专项回归 | CON/CUS 独立库下会话到联系人邮箱组合通过；跨账号会话和联系人均不可见；两库不包含对方权威表；会话不存在时保持原有无邮箱筛选语义 |
| 依赖清单 | 复用 ORC→CON ConversationRepository 与 ORC→CUS ContactRepository 源码边；依赖摘要 `6b12a468832b7121784bd72ec16ed8e8c7566801f69fd9ce668b4d8c113e7c6a` |
| 协作合同 | 新增双方 `FindSummaryByID` 读取合同；协作摘要 `8f27961c9830a4506d34d5367ee078e4a04dc9b2b256690be7e68986a55e70bb` |
| 完整回归 | `make test` 通过；71 项模块隔离验收通过 |
| 容器回归 | `make test-docker` 通过并清理全部临时资源 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止恢复 ORC 对 Conversation 的 Contact 预加载 |

Shopify 会话联系人 CON/CUS 读取边界已经完成。下一项为 `CRMOpsAdvancedHandler.DialogflowProcess` 的 OPS CannedResponse 读取边界。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
