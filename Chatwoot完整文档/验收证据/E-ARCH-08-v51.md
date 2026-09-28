# E-ARCH-08-v51 CampaignService CON 会话查找边界

原 `CampaignService.TriggerCampaign` 在 OPS 数据库中直接查询 CON 所有的 `Conversation`，按账号、联系人和收件箱决定复用或创建会话；查询发生任何错误时都会进入创建分支，可能在 CON 故障时错误创建重复会话。本轮改为调用 CON 的账号级精简查询，只有明确未找到时才创建，所有者查询错误会直接返回。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `ce26dfa`；E-ARCH-08-v50 |
| 阶段分库检查 | 55 个子用例通过 |
| 专项回归 | OPS/CUS/CON/MSG 分库下，活动复用已解决及待处理会话，不因会话状态重复创建；跨账号同联系人/收件箱组合不泄漏；CampaignDelivery 保存正确 CON 会话 ID；OPS 不含 Conversation 表 |
| 依赖清单 | 复用已登记的 OPS→CON 同步依赖；依赖摘要保持 `628daf9c68e737c9672217156b4be5bf459d23648c7d2412491e4d0222a1ad8c` |
| 协作合同 | 新增 `ConversationRepository.FindByContactAndInbox` → `con.conversation.get.v1` 读取绑定；协作摘要 `dad3c35b2e1ba0c2ae80c39c1fc8512a3b503e1aceca19bbfcc25d6bf9e63a87` |
| 完整回归 | `make test` 通过，Campaign 生命周期及现有接口回归通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止规则覆盖 CampaignService 对 Conversation 的直接查找 |

CampaignService 的 CON 会话查找边界已经完成。下一项为 `SearchHandler` 的 CUS/CON/MSG 搜索读取边界。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。
