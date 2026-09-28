# E-ARCH-08-v66 CRMOpsAdvancedHandler 旧版坐席容量策略所有者边界

旧版坐席容量策略入口原先由 ORC 直接读写 RTE 的 `CapacityPolicy`。本轮将按账号保存、更新和列表查询移入 `AgentCapacityPolicyRepository`，并由路由层显式注入；ORC 只通过 RTE 所有者合同调用，不再直接访问容量策略表。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `2807b89`；E-ARCH-08-v65 |
| 阶段分库检查 | 69 个子用例通过 |
| 专项回归 | RTE 独立库下保存、更新和账号范围列表通过；其他账号记录不会进入结果；RTE 库无 IAM User 或 TEN Account 权威表 |
| 依赖清单 | 复用 ORC→RTE AgentCapacityPolicyRepository 源码边；依赖摘要保持 `20ea1e19a280eb3ea89b132e865ac82ed760b6c27ab843d90f71115b9332a54c` |
| 模块合同 | 新增 `rte.capacity.manage.v1` 写合同；模块合同摘要 `e477ec629d8ccc3712b386657b32966b74f3a93f0c2fd5068e60410da5c4993d` |
| 协作合同 | 新增旧版容量策略列表与保存方法边；协作摘要 `20170fcf2bc78f22d021a6e8e1aecca3ae74e84d2200a81f2b91c031f83cfd9a` |
| 完整回归 | `make test` 通过；69 项模块隔离验收通过 |
| 容器回归 | `make test-docker` 通过并清理全部临时资源 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止恢复 ORC 对 `CapacityPolicy` 的直接 Assign/查询 |

旧版容量策略的 RTE 边界已经完成。下一项为 `CRMOpsAdvancedHandler` 集成安装列表与 Shopify/Dialogflow 配置读取的 EXT 访问边界。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
