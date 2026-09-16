# Inbox、Widget、工作时间、成员与渠道状态实施

> 批次：INB，共 17 项。基线来源：[04](../Chatwoot产品与API文档/04-Inbox渠道与Widget-API.md)、[17](../Chatwoot产品与API文档/17-各渠道创建更新字段字典.md)、[26](../Chatwoot产品与API文档/26-渠道状态错误重新授权与回调字典.md)、[44](../Chatwoot产品与API文档/44-webchat统一渠道会话与API补齐规范.md)。

## 1. 目标

把网站入口和所有外部渠道统一为 Inbox，完成 Inbox 生命周期、成员、工作时间、Widget 配置、访客身份、渠道健康和重新授权状态。

## 2. 任务清单

| 编号 | 功能 | 逐步实施 | API 与字段 | 完成判定 |
|---|---|---|---|---|
| INB-01 | Inbox 列表与详情 | 按 Account 和权限返回渠道摘要、类型、状态和成员信息 | `/inboxes`；id、name、channel_type、status | 不可见 Inbox 不进入结果 |
| INB-02 | Inbox 创建 | 先选择渠道类型；校验对应字段；建立渠道配置和公开标识 | name、channel.type、provider_config | 每类渠道只接受自身字段 |
| INB-03 | Inbox 更新 | 区分公共字段、渠道字段和只读健康字段 | name、greeting、working_hours、settings | 未提供字段不被清空，敏感值不回显 |
| INB-04 | Inbox 删除 | 检查会话、活动、Assistant 和外部订阅；执行停用或删除 | inbox_id | 历史会话按生命周期保留 |
| INB-05 | Inbox 成员 | 绑定、移除和列出成员，联动可见性和分配 | user_ids | 成员变化实时影响候选和权限 |
| INB-06 | 工作时间 | 设置星期、开始、结束、全天、时区和启停 | working_hours、timezone | 跨午夜、时区和空配置结果明确 |
| INB-07 | 节假日与离线 | 表达非营业时间、离线提示和次次可用时间 | out_of_office_message | Widget、SLA 和自动化使用同一结果 |
| INB-08 | Widget 配置读取 | 用 website_token 读取公开外观、语言、表单和业务状态 | `/api/v1/widget/config` | 不返回管理或敏感配置 |
| INB-09 | Widget 身份建立 | 创建或恢复访客身份，稳定保存 identifier 和会话令牌 | `/widget/contact`；identifier、email、name | 同一身份不重复生成客户 |
| INB-10 | 访客资料更新 | 更新可写资料和自定义属性，校验字段定义 | custom_attributes | 不可写字段和跨访客修改被拒绝 |
| INB-11 | Widget 会话 | 创建、列出、查看、更新已读和自定义属性 | `/widget/conversations` | 只能访问当前 Contact 的会话 |
| INB-12 | Widget 消息 | 发送、列出、附件和 echo 去重 | `/widget/messages`；content、content_type、attachments、echo_id | 重复点击不重复生成消息 |
| INB-13 | Widget 成员展示 | 返回可公开客服信息和在线状态 | inbox members | 隐私字段不公开 |
| INB-14 | 外观与体验 | 保留欢迎语、颜色、启动按钮、预聊天表单和离线留言 | widget settings | webchat 既有网站体验无回退 |
| INB-15 | 渠道健康 | 统一 connected、warning、disconnected、reauthorization_required | status、health、error_code | 列表和详情状态一致 |
| INB-16 | 重新授权通知 | 状态异常时产生管理通知，成功恢复后关闭 | provider、inbox_id、reason | 同一故障不产生无限重复通知 |
| INB-17 | 高级命令 | 管理 Agent Bot、模板、密钥、健康检查和入站通话开关 | secret、template、agent_bot、inbound_calls | 命令受渠道类型、权限和 Feature 限制 |

## 3. Widget 完整流程

1. 读取公开配置；
2. 判断营业状态和预聊天要求；
3. 建立或恢复访客身份；
4. 创建或恢复会话；
5. 发送消息并使用 echo_id 去重；
6. 通过实时事件接收消息和状态；
7. 更新已读位置；
8. 结束、重开或创建新会话；
9. 离线时接受留言并触发后续通知；
10. 清理或恢复浏览器身份时遵守客户身份规则。

## 4. 必测场景

- 营业时间内外、跨午夜、Account 与 Inbox 时区不同；
- 有预聊天表单和无表单；
- 匿名访客转为已识别客户；
- 同一 identifier 重复初始化；
- Website token 错误、禁用 Inbox、删除 Inbox；
- Widget 身份尝试读取 Account 管理接口；
- 渠道断开、重新授权要求、恢复连接；
- Inbox 成员移除后会话和实时订阅权限变化。

## 5. 完成条件

- [ ] INB-01 至 INB-17 全部关闭；
- [ ] 网站聊天从初始化到消息、已读、结束形成闭环；
- [ ] 工作时间结果被 Widget、SLA 和自动化共同使用；
- [ ] 渠道健康、通知和重新授权状态闭环；
- [ ] 现有 webchat 外观、表单、离线留言和移动 WebView 能力无回退。
