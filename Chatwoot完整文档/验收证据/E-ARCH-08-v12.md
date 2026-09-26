# E-ARCH-08-v12 CHN 收件箱策略访问边界

## 本次子项结论

CHN 收件箱仓储不再预加载 RTE AssignmentPolicy；详情、列表与 WebsiteToken 查询只读取渠道数据。创建和保存收件箱显式排除 AssignmentPolicy 关联，防止 ORM 写回策略。

收件箱接口所属 ORC 编排层分别查询 CHN 和 RTE，组合原有策略详情响应；没有引入 CHN -> RTE 的反向依赖。RTE 新增账号范围的策略摘要查询，只读取自身策略字段。创建、修改、绑定时拒绝其他账号的策略，查询故障返回服务器错误而非不存在或空策略。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git 3a6b6a7；E-ARCH-08-v11 |
| 阶段分库竞争检查 | 20 个子用例通过 |
| 针对性业务检查 | 分配策略增强测试通过；API 保留策略详情，仓储只保留 ID |
| 完整回归 | make test 通过 |
| 容器回归 | make test-docker 通过 |
| 静态检查 | go vet 通过；git diff --check 通过 |

新增 CHN/RTE 分库用例覆盖：附带策略对象的创建与保存不触碰 RTE 表；详情和列表的编排组合；创建、修改和绑定跨账号拒绝；RTE 停止后组合查询返回 500，CHN WebsiteToken 查询继续可用。RTE 摘要查询在缺少 IAM 用户表且策略带 fallback_assignee_id 时仍可使用。

## 合同登记与边界

协作合同新增 ORC InboxHandler -> RTE AssignmentPolicyRepository.FindSummaryByID 绑定。合同文件摘要同步更新为 75cb28d7f4ee3ab718f715cd6c65a5ee5aa218629779e3c765befc97acc8cec2；方法绑定、所有权和无环检查保留。ARCH-04 仍为 REOPENED，本次不是全局合同通过声明。

本次完成的是 CHN 仓储策略预加载及关联写入子项。RTE 的完整策略查询仍有 fallback 用户/团队预加载，RoutingService 仍直接访问其他所有者数据；这些属于后续 RTE 内部存储边界任务。CHN Campaign 查询和 AgentBot 绑定继续排队。ARCH-08 整体保持 REWORK，完整运行验收见 [剩余验收清单](ARCH-08-剩余验收清单.md)。
