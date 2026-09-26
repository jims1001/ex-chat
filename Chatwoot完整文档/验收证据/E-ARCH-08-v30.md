# E-ARCH-08-v30 PublicRepository CHN/CUS/CON/MSG 所有权边界

原 `PublicRepository` 属于 API，却直接查询 CHN 收件箱、CUS 联系人及 ContactInbox、CON 会话和 MSG 消息，并通过 GORM 预加载跨所有者关联。本轮保留公开 API 方法签名，由 API 仓储通过四个所有者合同组合公开视图。

网站令牌和数字收件箱查询由 CHN 执行；公开联系人解析、ContactInbox 关系和联系人局部更新由 CUS 执行；会话归属、列表、状态及自定义属性由 CON 执行；公开消息分页由 MSG 执行，并始终排除私密备注。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `60411cd`；E-ARCH-08-v29 |
| 阶段分库检查 | 38 个子用例通过 |
| 专项回归 | CHN/CUS/CON/MSG 四库隔离下，收件箱、联系人、会话、公开消息、私密过滤及外部表禁用通过 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过 |
| 静态检查 | `go vet ./cmd/... ./internal/... ./pkg/... ./test/...`、`git diff --check` 通过 |

协作合同摘要为 `b802a5080d6831c55a9a347208d84b31789731498b258427fead750e7004cccd`。当前合同仍为进程内所有者适配器，独立服务故障恢复和完整公开入口运行验收仍属于 ARCH-08 后续工作。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。下一项为 `QARepository` 的 TKT/CON 跨所有者访问边界。
