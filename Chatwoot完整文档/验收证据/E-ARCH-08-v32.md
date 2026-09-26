# E-ARCH-08-v32 WidgetRepository CHN/CUS/CON/OPS/MSG 所有权边界

原 `WidgetRepository` 属于 API，除自有的 `WidgetEvent` 外，还直接读写 CHN 收件箱、CUS 联系人及标签、CON 会话及会话标签、OPS 活动和 MSG 消息。本轮保留 Widget 对外方法，由 API 仓储通过五个领域所有者组合访客视图和写入。

网站令牌由 CHN 解析；访客身份、ContactInbox、联系人资料和联系人标签由 CUS 管理；会话归属、自定义属性和会话标签关系由 CON 管理；持续活动由 OPS 查询；公开消息和表单提交值由 MSG 管理。API 只保存、查询及迁移 `WidgetEvent`。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `579db03`；E-ARCH-08-v31 |
| 阶段分库检查 | 40 个子用例通过 |
| 专项回归 | API/CHN/CUS/CON/OPS/MSG 六库隔离下，收件箱、访客、会话消息、标签、活动、消息更新和外部表禁用通过 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过 |
| 静态检查 | `go vet ./cmd/... ./internal/... ./pkg/... ./test/...`、`git diff --check` 通过 |

协作合同摘要为 `728065c09c9f8243484b21fccc0f54c23f4babdc8c0b6d1ed0119c69c1027b6d`。跨库标签写入目前通过同步所有者命令顺序执行，独立服务故障恢复和完整 Widget 入口运行验收仍属于 ARCH-08 后续工作。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。下一项为 `TicketRepository` 的 TEN/CUS/CON/RPT 跨所有者访问边界。
