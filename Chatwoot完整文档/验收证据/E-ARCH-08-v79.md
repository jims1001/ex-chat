# E-ARCH-08-v79 CopilotHandler BIL 订单查询边界

`CopilotHandler.ExecuteAITool` 的订单查询、物流查询和地址更新原先从 AIC 数据库直接读取 BIL `Order`。本轮新增 BIL `OrderRepository.FindForTool`，按账号和非空订单号/物流号查询；地址更新继续由 BIL 执行并显式处理失败。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `1898f94`；E-ARCH-08-v78 |
| 阶段分库检查 | 82 个子用例通过 |
| 专项回归 | AIC/BIL 独立库工具调用通过；覆盖订单、物流、地址更新、跨账号拒绝和外部权威表禁用 |
| 依赖清单 | AIC→BIL OrderRepository 依赖保持；依赖清单摘要 `2313cda54c4172b90e9753a16d3f80aae693caae351505d53762d3d1aada1361`；依赖清单合同摘要 `0ead677bd70ffdf2830af74964f01858962531956481727c96a4ff76d834c499` |
| 协作合同 | 新增 FindForTool 的 BIL 查询绑定；协作摘要 `aec9f71550038e4793c60d98e38d6eee612852b5faea7e964099c1844d93ed2a`；模块合同摘要保持 `76772e4a573bdafc1ac609469ac2fd774eb6505e24d3fa0fdafff99b0eb7699a` |
| 完整回归 | `make test` 通过；82 项模块隔离验收通过 |
| 容器回归 | `make test-docker` 通过并清理全部临时资源 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；CopilotHandler 已禁止恢复三处 Order 直接查询 |

CopilotHandler 的 BIL 订单及物流查询边界已经完成。后续扫描确认下一项为 `AICustomToolHandler` 工具调用中的 BIL 订单及物流查询边界。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
