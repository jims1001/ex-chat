# E-ARCH-08-v72 AdvancedHandler 自定义筛选 SRH 所有者边界

`custom_filter_handler.go` 原先由 ORC 直接读写 SRH `CustomFilter`。本轮新增 `CustomFilterRepository`，将创建、个人及共享列表、账号内详情、更新和删除全部移入 SRH 所有者仓储；更新和删除均再次限定账号与记录 ID，处理器不再持有该模型的直接存储访问。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `9d2553f`；E-ARCH-08-v71 |
| 阶段分库检查 | 75 个子用例通过 |
| 专项回归 | SRH 独立库下创建、个人及共享列表、更新、删除和账号隔离通过；跨账号读取与删除被拒绝；SRH 库无 CON Conversation 或 CUS Contact 权威表 |
| 依赖清单 | 登记 CustomFilterRepository 为 SRH 所有者及 ORC→SRH 源码边；依赖摘要 `1940ba09da68fc23906f884f993de1e7f6b80d110671e1adc6d90279d577237d` |
| 协作合同 | 新增 SRH 自定义筛选 Command、Query、Event 及组件绑定；协作摘要 `d0d7eba3a3e6308c5764e556965885a41c817a51102c3049d064191bd193d56c`；模块合同摘要 `76772e4a573bdafc1ac609469ac2fd774eb6505e24d3fa0fdafff99b0eb7699a` |
| 完整回归 | `make test` 通过；75 项模块隔离验收通过 |
| 容器回归 | `make test-docker` 通过并清理全部临时资源 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；custom filter handler 已增加禁止直接使用 `h.db` 的门禁 |

自定义筛选的 SRH 边界已经完成。后续扫描确认下一项为 `PublicHandler` 创建联系人后的 CUS 回读及消息 `echo_id` 的 MSG 幂等查询边界。ARCH-03/04 保持 REOPENED，ARCH-08 保持 REWORK。
