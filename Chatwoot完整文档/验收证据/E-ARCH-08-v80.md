# E-ARCH-08-v80 AICustomToolHandler BIL 订单查询边界

`AICustomToolHandler.executeInternalTool` 的订单查询、物流查询和地址更新原先从 AIC 数据库直接读取 BIL `Order`。本轮统一复用 BIL `OrderRepository.FindForTool`，按账号和非空订单号/物流号查询；地址更新仍由 BIL 所有者执行。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `55177bc`；E-ARCH-08-v79 |
| 阶段分库检查 | 82 个子用例通过；复用 v79 的 AIC/BIL 订单所有者隔离用例 |
| 专项回归 | 三处直接 Order 查询门禁通过；BIL 查询合同和跨账号隔离回归通过 |
| 协作合同 | 新增 AICustomToolHandler FindForTool 的 BIL 查询绑定；协作摘要 `166ba30f70139538c3036687c042874f82118023858b74fb8be9dbf89b504c66` |
| 完整回归 | `make test` 通过；82 项模块隔离验收通过 |
| 容器回归 | `make test-docker` 通过并清理全部临时资源 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过 |

AICCustomToolHandler 的 BIL 订单及物流查询边界已经完成。后续扫描确认下一项为 `CRMOpsAdvancedHandler` 残留数据库句柄清理及直接存储门禁。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
