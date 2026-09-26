# E-ARCH-08-v13 CHN Campaign 查询边界

## 本次子项结论

已完成收件箱 Campaign 查询从 CHN 仓储移至 OPS 仓储。删除 InboxRepository.ListCampaigns，新增 CampaignRepository.ListByInbox，由 ORC 收件箱接口先通过 CHN 验证账号和收件箱归属，再向 OPS 查询活动。没有引入 CHN -> OPS 反向依赖。

查询保留 account_id 与 inbox_id 双条件、id DESC 排序，以及原有 success/payload/data 响应。归属校验使用 CHN Exists，不再加载成员与用户资料；查询请求的 Context 传给 OPS。缺少依赖或数据库故障返回 500，不冒充无数据或 404，不返回底层数据库错误详情。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git 2c8f9a4；E-ARCH-08-v12 |
| 验证时间 | 2026-09-26T06:36:50+00:00 |
| 阶段分库竞争检查 | 21 个子用例通过 |
| 专项回归 | TestInboxManagementEnhancements 通过 |
| 完整回归 | make test 通过 |
| 容器回归 | make test-docker 通过 |
| 静态与合同检查 | go vet、依赖图、协作合同绑定、git diff --check 通过 |

新增 CHN/OPS 分库用例验证：正确结果与排序、不同账号同一收件箱引用不泄漏、其他收件箱活动被排除、空结果、非法参数/不存在/跨账号收件箱、缺失 OPS 依赖、请求取消、CHN 与 OPS 故障错误区分、OPS 不可用时 CHN 渠道查询仍可用。用例故意保留 CHN 成员关系但不提供 TEN/IAM，证明活动查询不依赖成员预加载。两库不创建对方权威表。

## 合同与剩余边界

协作绑定改为 ORC -> CHN InboxRepository.Exists 和 ORC -> OPS CampaignRepository.ListByInbox（ops.campaign.get.v1），同步登记来源与合同校验摘要 aca0c490ac4eb5ba4c4285ecaeab2c34eb72b2cd4cb8baf5b3a7509b463e7718。ARCH-04 保持 REOPENED，未关闭全局合同验收。

本次仅完成收件箱 Campaign 查询子项。下一项为 CHN AgentBot 绑定所有权；其他业务边界、公开服务运行、启停、扩缩、恢复与提取验收仍见 [剩余验收清单](ARCH-08-剩余验收清单.md)。ARCH-08 整体仍为 REWORK。
