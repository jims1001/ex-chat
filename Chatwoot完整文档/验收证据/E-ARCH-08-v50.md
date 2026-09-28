# E-ARCH-08-v50 CampaignService CON/CUS 会话标签查询边界

原 `CampaignService` 在 OPS 数据库中四次联表查询 CON 的 `Conversation`、`ConversationLabel` 与 CUS 的 `Label`，用于一次性活动和持续活动的受众筛选。本轮拆为所有者查询组合：CUS 按账号将精确或模糊标签标题解析为标签 ID，CON 再从自身会话标签关系返回账号范围内的联系人 ID，OPS 只负责活动受众编排。

| 项目 | 实际结果 |
|---|---|
| 基线 | Git `d0460c4`；E-ARCH-08-v49 |
| 阶段分库检查 | 54 个子用例通过 |
| 专项回归 | OPS/CUS/CON/MSG 分库下，JSON 精确标签数组和文本模糊标签均命中有对应会话标签的联系人，未匹配联系人不投递；各库不含其他模块权威表 |
| 依赖清单 | 登记 CampaignService 的 OPS→CUS `LabelRepository` 源码边；依赖摘要保持 `628daf9c68e737c9672217156b4be5bf459d23648c7d2412491e4d0222a1ad8c` |
| 协作合同 | 新增 CUS 标签 ID 查询及 CON 标签联系人查询绑定；协作摘要 `60e01818a507e29030aa61abb9e38ff853e783e466713a49ab3f8dd91296f047` |
| 完整回归 | `make test` 通过，Campaign 生命周期及现有接口回归通过 |
| 容器回归 | `make test-docker` 通过，PostgreSQL/Redis 容器及数据卷已清理 |
| 静态检查 | `go vet ./...`、`git diff --check` 通过；禁止规则覆盖 CampaignService 对 conversations、conversation_labels、labels 的直接联表 |

CampaignService 的 CON/CUS 会话标签查询边界已经完成。下一项为 `CampaignService` 的 CON 会话查找边界。ARCH-03/04 保持 REOPENED，ARCH-08 整体保持 REWORK。
