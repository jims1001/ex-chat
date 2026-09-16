# AI 模型、助手、知识、Copilot、场景、工具与额度实施

> 批次：AI，共 25 项。基线来源：[09](../Chatwoot产品与API文档/09-Captain-Copilot与AI-API.md)、[22](../Chatwoot产品与API文档/22-AI模型Provider与Feature能力矩阵.md)、[31](../Chatwoot产品与API文档/31-异步任务进度终态与补偿规则.md)、[41](../Chatwoot产品与API文档/41-第三方API版本能力漂移与历史兼容.md)、[46](../Chatwoot产品与API文档/46-webchat-AI模型与插件体系补齐规范.md)和[30-第三方真实环境与最终接口冻结表](30-第三方真实环境与最终接口冻结表.md)。

## 1. 目标

在保留 webchat 传统知识机器人、多轮场景和外部机器人能力的基础上，建立可切换的 Provider 与 Model、Assistant、知识同步、FAQ、Scenario、Copilot、文本任务、自定义工具、用量额度、安全边界和失败回退。

## 2. Provider、Model 与偏好任务

| 编号 | 功能 | 逐步实施 | API 与字段 | 完成判定 |
|---|---|---|---|---|
| AI-01 | Provider 登记 | 记录名称、Endpoint、认证方式、状态、支持能力和区域 | provider、endpoint、auth_type、status | 凭证不在普通查询中返回 |
| AI-02 | Provider 校验 | 验证凭证、网络、模型目录和最小请求；区分可用与部分可用 | health、error_code | 错误原因可识别且不泄露凭证 |
| AI-03 | Model 目录 | 记录模型标识、Provider、上下文、输入类型、Feature 和状态 | model_id、capabilities、context_window | 仅展示当前可用模型 |
| AI-04 | Feature 矩阵 | 按文本改写、摘要、翻译、建议、知识回答、Copilot 等绑定模型 | captain_features、captain_models | 22 分册适用矩阵完整 |
| AI-05 | Preferences | 查询和更新 Account 的模型与功能偏好 | `/captain/preferences` | 无效组合在保存前被拒绝 |
| AI-06 | 选择与回退 | 按 Account 偏好、Feature、模型状态、额度和降级顺序选择 | fallback_models、feature | 回退结果可观察，不静默换模型 |

## 3. Assistant 与知识任务

| 编号 | 功能 | 逐步实施 | API 与字段 | 完成判定 |
|---|---|---|---|---|
| AI-07 | Assistant 生命周期 | 创建、列表、详情、更新、复制、停用和删除 | `/captain/assistants`；name、description、config、guardrails | 归属 Account，引用中删除受限 |
| AI-08 | Assistant 配置 | 设置模型、语气、语言、回答边界、转人工和拒答规则 | config、guardrails | 不允许越过安全边界 |
| AI-09 | Inbox 绑定 | 将 Assistant 绑定或解绑一个或多个 Inbox | assistant_id、inbox_id | 跨账号绑定被拒绝，冲突规则明确 |
| AI-10 | Document 创建 | 支持文本、外部链接、PDF 和帮助中心来源 | `/captain/documents`；name、external_link、pdf_file | 受理后进入可查询同步状态 |
| AI-11 | Document 同步 | 提取、分段、索引、统计、失败和重试 | status、sync_status、error | 最终状态和失败原因可确认 |
| AI-12 | Document 更新删除 | 内容变化触发重建；删除清除可检索内容和引用 | document_id | 旧内容不会继续回答 |
| AI-13 | FAQ 管理 | 创建、查询、更新、审核、启停和删除问答 | `/assistant_responses`；question、answer、status | 状态决定是否参与回答 |
| AI-14 | FAQ 候选与批量 | 生成候选、人工确认、批量启停或删除 | bulk action、status | 候选不自动成为正式答案 |
| AI-15 | Scenario | 定义标题、说明、触发、工具、顺序和启停 | `/scenarios`；instruction、tools、enabled | 冲突场景选择规则确定 |

## 4. Copilot、任务与工具

| 编号 | 功能 | 逐步实施 | API 与字段 | 完成判定 |
|---|---|---|---|---|
| AI-16 | 文本任务 | 实施改写、摘要、翻译、扩写、缩写等 operation | `/captain/tasks`；content、operation | 输入限制、模型选择和错误明确 |
| AI-17 | 会话任务 | 读取授权会话上下文进行摘要、建议或分类 | conversation_display_id | 私有和敏感内容按权限处理 |
| AI-18 | Copilot Thread | 创建、列表、查看和删除成员个人线程 | `/copilot_threads`；assistant_id、conversation_id | 用户之间线程隔离 |
| AI-19 | Copilot Message | 发送问题、接收回答、引用来源和反馈 | message、citations、status | 回答可追踪到模型与知识版本 |
| AI-20 | 建议采用 | 将建议插入草稿、修改后发送或拒绝，并记录采用结果 | suggestion_id、action | AI 不直接冒充成员发送，除非明确授权 |
| AI-21 | Custom Tool | 创建、查询、更新、测试、启停和删除工具 | `/custom_tools`；endpoint_url、http_method、auth_type、param_schema | Endpoint、参数和认证校验完整 |
| AI-22 | Tool 调用 | 生成参数、权限确认、调用、超时、解析、重试和结果回填 | invocation_id、status、response | 不可逆动作需要明确授权和幂等 |

## 5. 用量、安全与运行任务

| 编号 | 功能 | 逐步实施 | 字段重点 | 完成判定 |
|---|---|---|---|---|
| AI-23 | 用量与额度 | 记录 Account、Provider、Model、Feature、输入输出和成本；执行额度限制 | tokens、cost、quota、period | 用量可与 Provider 和套餐对账 |
| AI-24 | 安全与隐私 | 实施敏感信息处理、租户隔离、提示注入防护、内容限制和审计 | guardrails、redaction、policy_result | 跨账号知识和工具凭证不可访问 |
| AI-25 | 失败与漂移 | 处理限流、超时、模型下线、响应异常、未知状态和 Provider 变更 | error_code、retry_after、model_version | 失败可回退或转人工，不产生虚假成功 |

## 6. Assistant 回答流程

1. 确认 Inbox 已绑定启用的 Assistant；
2. 校验 Account Feature、额度和 Provider 状态；
3. 根据语言、场景和当前会话构造允许上下文；
4. 检索仅属于该 Assistant 和 Account 的有效知识；
5. 应用安全规则、转人工条件和工具许可；
6. 选择目标模型，记录模型和知识版本；
7. 必要时调用工具，并对不可逆动作取得明确授权；
8. 生成回答、引用和置信信息；
9. 再次执行内容与敏感信息检查；
10. 以建议、自动回复或转人工的目标模式处理；
11. 记录用量、反馈、事件、报表和审计；
12. 失败时按规则回退模型、转人工或给出明确错误。

## 7. 必测场景

- Provider 凭证错误、模型不存在、Endpoint 超时和限流；
- 首选模型不可用，回退模型成功或全部失败；
- Document 同步中断、重复同步、更新期间查询和删除竞争；
- FAQ 草稿、审核、启用和停用的回答差异；
- 两个 Assistant 绑定同一 Inbox 的冲突；
- 知识跨 Account、跨 Assistant 和无权会话访问；
- Copilot 线程跨成员访问、引用已删除知识；
- Tool 参数缺失、认证失效、超时、重复调用和不可逆动作；
- 额度刚好耗尽、并发请求超额和账期重置；
- Prompt 注入、敏感信息回显、未知模型版本和模型下线；
- 自动回答失败后正确转人工并保留上下文。

## 8. 完成条件

- [ ] AI-01 至 AI-25 全部关闭；
- [ ] Provider、Model、Feature、Preference 和回退形成闭环；
- [ ] Assistant、知识、FAQ、Scenario、Copilot 和 Tool 可完整使用；
- [ ] 用量、额度、隐私、安全、失败和模型漂移通过；
- [ ] 传统机器人、知识问答、多轮场景和外部机器人能力无回退；
- [ ] 真实模型请求和真实工具回执已进入证据包。
- [ ] 每个适用 AI Provider、模型、Tool 和回退合同已在 30 分册冻结并取得有效真实证据。
