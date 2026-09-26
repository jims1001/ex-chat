# E-ARCH-08-v14 CHN AgentBot 绑定所有权

## 本次子项结论

已完成 CHN AgentBot 绑定所有权拆分。`AgentBot` 与 `AgentBotInbox` 均由 EXT 保存；CHN 仓储已删除机器人读取、绑定和解绑方法，不再创建或访问 EXT 权威表。

收件箱侧由 ORC 先通过 CHN 校验账号和收件箱，再调用 EXT 查询或替换绑定。机器人侧由 EXT 返回绑定的收件箱 ID，ORC 再通过 CHN 补齐收件箱详情；绑定前同样先校验 CHN 归属。删除机器人时，EXT 在自身事务内清理绑定。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `f62d491`；E-ARCH-08-v13 |
| 验证时间 | 2026-09-26T06:50:00+00:00 |
| 阶段分库竞争检查 | 22 个子用例通过 |
| 专项回归 | AgentBot 生命周期、收件箱增强和架构合同检查通过 |
| 完整回归 | `make test` 通过 |
| 容器回归 | `make test-docker` 通过 |
| 静态检查 | `go vet ./cmd/... ./internal/... ./pkg/... ./test/...`、`git diff --check` 通过 |

新增 CHN/EXT 分库用例覆盖收件箱侧读取、绑定、替换、解绑，机器人侧绑定、收件箱列表组合和解绑，以及跨账号机器人、跨账号收件箱、缺失 EXT 依赖和 EXT 停止。EXT 停止后，CHN WebsiteToken 查询仍可用。两库均未创建对方权威表。

## 合同与剩余边界

协作合同登记 ORC 对 CHN 的 `Exists`、`FindByIDs` 查询，以及对 EXT 的 `GetByInbox`、`GetByID`、`ReplaceInboxBinding`、`BindInbox`、`ListInboxIDs` 和 `UnbindInbox` 调用。合同摘要为 `aeee6da23cba9b86b9d1d8863a06058192725651610d1d94acb388c58f2238d7`。ARCH-04 保持 REOPENED。

本次只关闭 AgentBot 绑定所有权子项。下一项为 RTE 完整策略查询中的 fallback 用户和团队预加载边界；RoutingService 的跨所有者数据访问随后处理。ARCH-08 整体仍为 REWORK，完整运行验收见 [剩余验收清单](ARCH-08-剩余验收清单.md)。
