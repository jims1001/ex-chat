# AI 模型、Provider 与 Feature 能力矩阵

> 导航：[全体功能与 API 总表](00-全体功能与API总表.md)｜[Captain、Copilot 与 AI API](09-Captain-Copilot与AI-API.md)

## 1. 功能范围

AI Preferences 为每个 Account 提供 Feature 开关和模型选择。Feature 表示业务能力，Model 表示执行该能力的模型，Provider 表示模型所属平台。三者分开配置，便于为高频、低成本和高质量任务选择不同模型。

## 2. Preferences API

| 方法 | 路径 | 功能 | 通常权限 |
|---|---|---|---|
| GET | /api/v1/accounts/{account_id}/captain/preferences | 获取 Provider、Model、Feature 和当前选择 | Account 成员 |
| PATCH | /api/v1/accounts/{account_id}/captain/preferences | 更新 Feature 开关或模型覆盖 | Administrator |

## 3. 更新字段

| 字段 | 类型 | 说明 |
|---|---|---|
| captain_models | object | Feature Key 到 Model ID 的映射 |
| captain_features | object | Feature Key 到 boolean 的映射 |

更新采用按 Key 合并方式，只改变已提交的 Feature。模型值为空时表示移除该 Feature 的 Account 覆盖并回到默认路由；Feature 开关和模型选择互相独立，选择模型不等于启用功能。

## 4. 响应字段

| 字段 | 类型 | 说明 |
|---|---|---|
| providers | object | Provider Key 到 display_name 的目录 |
| models | object | Model ID 到 Provider、展示名、计划状态和额度倍率的目录 |
| features | object | Feature Key 到功能配置和 Account 偏好的目录 |
| features.{key}.models | array | 该 Feature 可选择的模型列表 |
| features.{key}.default | string | 当前版本默认模型 |
| features.{key}.enabled | boolean | Account 是否启用该 Feature |
| features.{key}.model | string | 当前实际路由模型 |
| features.{key}.selected | string | 当前选中模型，与 model 一致 |
| features.{key}.provider | string | 当前模型 Provider |
| features.{key}.source | enum | default 或 account_override |

## 5. Provider 状态

| Provider Key | 展示名称 | 当前状态 | 凭证说明 |
|---|---|---|---|
| openai | OpenAI | 当前模型目录可用 | 使用安装范围的 OpenAI API Key，可选自定义 Endpoint |
| anthropic | Anthropic | 候选模型标记为即将支持 | 当前 Preferences 不提供 Account 级 Anthropic Key 字段 |
| gemini | Gemini | 候选模型标记为即将支持 | 当前 Preferences 不提供 Account 级 Gemini Key 字段 |

即将支持模型可以出现在目录中，但不应在正式业务中当作已经可调用。实际可用性还受 Account Feature、Cloud 计划、额度和安装配置影响。

## 6. 模型目录

| Model ID | Provider | 展示名称 | 额度倍率 | 当前说明 |
|---|---|---|---:|---|
| gpt-4.1 | openai | GPT-4.1 | 3 | 通用高质量模型 |
| gpt-4.1-mini | openai | GPT-4.1 Mini | 1 | 多数轻量能力默认模型 |
| gpt-4.1-nano | openai | GPT-4.1 Nano | 1 | 轻量翻译和标签候选 |
| gpt-5.1 | openai | GPT-5.1 | 2 | 通用高质量候选 |
| gpt-5-mini | openai | GPT-5 Mini | 1 | 成本与质量平衡候选 |
| gpt-5-nano | openai | GPT-5 Nano | 1 | 已在模型目录，当前 Feature 未选用 |
| gpt-5.2 | openai | GPT-5.2 | 3 | Assistant v2、FAQ 和文章生成重点模型 |
| claude-haiku-4.5 | anthropic | Claude Haiku 4.5 | 2 | 即将支持 |
| claude-sonnet-4.5 | anthropic | Claude Sonnet 4.5 | 3 | 即将支持 |
| gemini-3-flash | gemini | Gemini 3 Flash | 1 | 即将支持 |
| gemini-3-pro | gemini | Gemini 3 Pro | 3 | 即将支持 |
| whisper-1 | openai | Whisper | 1 | 音频转写 |
| gpt-4o-mini-transcribe | openai | GPT-4o Mini Transcribe | 1 | 音频转写默认模型 |
| text-embedding-3-small | openai | Text Embedding 3 Small | 1 | 帮助中心向量检索 |

额度倍率表示同类 AI 用量的相对消耗权重，不等于 Provider 对外公开价格。Cloud 实际扣减还应以账号计划和额度接口为准。

## 7. Feature 总表

| Feature Key | 业务功能 | 默认模型 | 默认启用 |
|---|---|---|---|
| editor | 改写、润色、语气和长度调整 | gpt-4.1-mini | 否 |
| assistant | 面向客户的 Captain Assistant 回答 | gpt-4.1；Assistant v2 为 gpt-5.2 | 否 |
| copilot | 面向 Account 成员的 Copilot 对话辅助 | gpt-4.1 | 否 |
| label_suggestion | 根据会话建议已有标签 | gpt-4.1-mini | 否 |
| document_faq_generation | 从网页或一般文档生成 FAQ | gpt-4.1-mini | 否 |
| conversation_faq_generation | 从已结束会话生成 FAQ 候选 | gpt-5.2 | 否 |
| pdf_faq_generation | 从 PDF 生成 FAQ | gpt-4.1-mini | 否 |
| help_center_article_generation | 生成或整理帮助中心文章 | gpt-5.2 | 否 |
| onboarding_content_generation | 账号引导阶段生成内容 | gpt-4.1 | 否 |
| help_center_query_translation | 翻译帮助中心检索问题 | gpt-4.1-nano | 否 |
| audio_transcription | 将语音附件转成文本 | gpt-4o-mini-transcribe | 否 |
| help_center_search | 帮助中心语义检索 | text-embedding-3-small | 否 |

所有 Feature 在 Account 没有显式开启时默认关闭。某些业务入口还可能由独立 Account Feature 或企业授权控制。

## 8. 文本生成 Feature 模型矩阵

| Feature | OpenAI 可选 | 即将支持候选 |
|---|---|---|
| editor | gpt-4.1-mini、gpt-4.1-nano、gpt-5-mini、gpt-4.1、gpt-5.1、gpt-5.2 | claude-haiku-4.5、gemini-3-flash、gemini-3-pro |
| assistant | gpt-4.1-mini、gpt-5-mini、gpt-4.1、gpt-5.1、gpt-5.2 | claude-haiku-4.5、claude-sonnet-4.5、gemini-3-flash、gemini-3-pro |
| copilot | gpt-4.1-mini、gpt-5-mini、gpt-4.1、gpt-5.1、gpt-5.2 | claude-haiku-4.5、claude-sonnet-4.5、gemini-3-flash、gemini-3-pro |
| label_suggestion | gpt-4.1-nano、gpt-4.1-mini、gpt-5-mini | gemini-3-flash、claude-haiku-4.5 |
| document_faq_generation | gpt-4.1-mini、gpt-5-mini、gpt-4.1、gpt-5.1、gpt-5.2 | claude-haiku-4.5、claude-sonnet-4.5、gemini-3-flash、gemini-3-pro |
| conversation_faq_generation | gpt-4.1-mini、gpt-5-mini、gpt-4.1、gpt-5.1、gpt-5.2 | claude-haiku-4.5、claude-sonnet-4.5、gemini-3-flash、gemini-3-pro |
| help_center_article_generation | gpt-4.1-mini、gpt-5-mini、gpt-4.1、gpt-5.1、gpt-5.2 | claude-haiku-4.5、claude-sonnet-4.5、gemini-3-flash、gemini-3-pro |

## 9. 专用 Feature 模型矩阵

| Feature | 可选模型 | 设计原因 |
|---|---|---|
| pdf_faq_generation | gpt-4.1-mini、gpt-5-mini、gpt-4.1、gpt-5.1、gpt-5.2 | 只允许当前已支持的 OpenAI 文档处理模型 |
| onboarding_content_generation | gpt-4.1、gpt-4.1-mini、gpt-5-mini、gpt-5.1、gpt-5.2 | 面向短期引导内容生成 |
| help_center_query_translation | gpt-4.1-nano、gpt-4.1-mini、gpt-5-mini | 低延迟短文本任务 |
| audio_transcription | gpt-4o-mini-transcribe、whisper-1 | 音频输入到文本输出 |
| help_center_search | text-embedding-3-small | 生成检索向量，不直接生成客户回复 |

不能把音频转写模型或 Embedding 模型配置给文本生成 Feature，也不能把通用文本模型配置给 help_center_search。

## 10. 模型选择顺序

实际模型按以下优先级确定：

1. Account 对该 Feature 的有效 captain_models 覆盖；
2. assistant 且启用 Captain Integration v2 时使用 gpt-5.2；
3. 该 Feature 的默认模型。

source 为 account_override 时表示使用 Account 覆盖；其余情况为 default。无效 Model ID 不能作为有效覆盖保存；已移除的覆盖会自动回到默认路由。

## 11. 凭证与 Endpoint

| 配置 | 功能 |
|---|---|
| OpenAI API Key | 验证 Captain AI 对 OpenAI 的请求 |
| OpenAI API Endpoint | 可选自定义兼容 Endpoint；默认使用 OpenAI 地址 |
| OpenAI Model | 旧的安装范围默认模型配置，Feature 路由以 Preferences 返回为准 |
| Embedding Model | 帮助中心检索使用的安装范围模型配置 |
| FireCrawl API Key | 抓取网页知识时使用，不属于语言模型凭证 |

Preferences API 不接收 API Key。模型凭证属于安装范围敏感配置，不能写入 captain_models、captain_features、Assistant config 或 Custom Tool 普通参数。

## 12. Feature 与业务入口关系

| Feature | 主要入口 | 结果使用方式 |
|---|---|---|
| editor | /captain/tasks/rewrite | 返回建议文本，由成员确认后使用 |
| label_suggestion | /captain/tasks/label_suggestion | 返回 Account 已有标签建议 |
| assistant | Assistant Playground、绑定 Inbox 的客户会话 | 自动回答、解决或转人工 |
| copilot | Copilot Thread 和 Message | Account 成员内部辅助，不直接对客户发送 |
| conversation_faq_generation | 会话结束后的 FAQ 候选 | 先审核，再批准进入知识范围 |
| document_faq_generation、pdf_faq_generation | Document 创建和同步 | 异步生成 FAQ，需检查 Document 状态 |
| help_center_article_generation | 帮助中心内容生成 | 人工审核后发布 |
| audio_transcription | 语音附件 | 转写文本作为会话上下文 |
| help_center_search | Assistant 知识检索 | 为回答提供相关知识片段 |

## 13. 失败与回退规则

- 没有 Account 覆盖时会回到 Feature 默认模型；
- 已选择模型调用失败时，不应假设系统会自动切换到另一个 Provider 或模型；
- 即将支持模型不能作为生产回退目标；
- API Key、Endpoint、额度、模型可用性和 Feature 开关任一不满足都可能导致失败；
- Task 返回 error 时不能自动作为客户消息发送；
- Document、PDF 和网页同步是异步流程，创建成功不代表 FAQ 已可用；
- Assistant 转人工后应停止继续自动回答，除非会话重新进入允许的 Bot 流程。

## 14. 成本与质量选择

| 任务类型 | 建议方向 | 需要观察 |
|---|---|---|
| 高频短文本 | Mini、Nano 或 Flash 类候选 | 延迟、标签准确率、额度 |
| 客户自动回答 | Assistant 允许的高质量模型 | 答案正确性、转人工率、引用 |
| 长文与 FAQ | GPT-5.2 或高质量候选 | 事实一致性、重复 FAQ、审核量 |
| 查询翻译 | Nano 或 Mini | 专有名词和语言识别 |
| 音频转写 | 专用 Transcribe 或 Whisper | 语言、噪声、时长、隐私 |
| 语义检索 | Embedding 模型 | 召回质量和知识更新时间 |

模型更换后应使用固定样本集重新验证，不应只比较单次输出。

## 15. AI Preferences 验收

- GET 返回 3 个 Provider、完整模型目录和 12 个 Feature；
- 每个 Feature 的 default、selected、provider、source 和 enabled 一致；
- Agent 可读取但不能更新 Preferences；
- 无效 Feature Key 不会覆盖其他设置；
- 不属于 Feature 模型列表的 Model ID 被拒绝；
- 清除 Account 覆盖后恢复到正确默认模型；
- Assistant v2 在无 Account 覆盖时使用 gpt-5.2；
- 关闭 Feature 后对应业务入口不再执行该能力；
- 额度和凭证不足时返回明确错误，不自动发送不完整结果；
- Anthropic 和 Gemini 的即将支持模型不被误判为当前可用。
