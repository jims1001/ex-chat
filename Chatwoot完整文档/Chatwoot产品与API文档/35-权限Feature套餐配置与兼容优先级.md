# 权限、Feature、套餐、配置与兼容优先级

> 适用版本：Chatwoot 4.16.0
> 本分册说明一个功能从“入口存在”到“当前请求可执行”之间的完整判定顺序，并固定不同版本形态、角色、资源归属、套餐、配置和历史数据共同作用时的结果。

## 1. 可用性不是单一权限判断

同一个 API 在不同环境或账号上可能出现不同结果，原因可能来自：

- 当前版本形态没有该入口；
- 入口存在，但当前 Account 未开启 Feature；
- Feature 已开启，但套餐额度不足；
- 当前用户属于账号，但角色无权执行该动作；
- 角色有权，但不属于目标 Inbox；
- 资源存在，但不属于当前 Account；
- Provider 尚未授权或凭据失效；
- 安装配置关闭了全局能力；
- Account 已暂停或进入计划删除状态；
- 对象当前状态不允许该动作；
- 触发了频率限制或会话数量限制；
- 历史数据缺少新版本字段，需要采用兼容默认值。

因此，完整一致性必须比较“判定顺序和最终错误”，而不是只比较角色名称。

## 2. 功能可用性的十二层判定顺序

| 优先级 | 判定层 | 主要问题 | 不通过时的典型结果 |
|---:|---|---|---|
| 1 | 入口与版本形态 | 当前版本形态是否提供此入口 | 入口不存在或不可用 |
| 2 | 身份认证 | Token、Cookie、签名或公开标识是否有效 | 401 或拒绝访问 |
| 3 | Account 状态 | Account 是否 active | 401、403 或账号不可用结果 |
| 4 | 主体关系 | 用户、Bot、Contact、Platform App 是否属于目标范围 | 401、403 或 404 |
| 5 | API Access Feature | API Token 是否允许访问账号 API | 403 |
| 6 | Feature 与安装配置 | 账号 Feature 和安装级配置是否都允许 | 403、404 或功能不可用 |
| 7 | 套餐与额度 | 席位、联系人、会话、AI、报表等额度是否足够 | 403、422 或额度错误 |
| 8 | 角色与 Custom Role | 当前主体是否拥有动作权限 | 403 |
| 9 | 对象范围 | 是否属于目标 Inbox、Team、Account 或公开范围 | 403 或 404 |
| 10 | 对象当前状态 | 当前状态是否允许更新、删除、发送或重试 | 409、422 或无变化 |
| 11 | Provider 能力 | 渠道是否支持该消息、模板、文件或回调 | 422、失败状态或渠道错误 |
| 12 | 频率和并发限制 | 当前请求是否超出频率、会话或互斥限制 | 429、409 或稍后处理 |

判定时应从上到下记录首个阻断原因。不得把所有失败统一返回为“无权限”。

## 3. 身份类型与认证边界

| 身份类型 | 主要凭据 | 可访问范围 | 不可越过的边界 |
|---|---|---|---|
| Account User | access-token 或有效登录会话 | 已加入的 Account 及其授权资源 | 不能访问未加入账号 |
| Administrator | Account User 凭据 | 账号管理、成员、渠道和大部分业务设置 | 仍受 Feature、套餐和资源归属限制 |
| Agent | Account User 凭据 | 会话、联系人、已授权 Inbox 和日常操作 | 默认不能执行账号级高权限动作 |
| Custom Role User | Account User 凭据 | 由 Custom Role 权限集合决定 | 权限名存在不代表 Feature 已可用 |
| Contact | Contact token | 自己的公开会话、消息和资料范围 | 不能读取其他 Contact 或账号管理内容 |
| Website Visitor | inbox_identifier 与 contact token | 对应网站聊天身份和会话 | 不能切换到其他 Inbox 身份 |
| Agent Bot | Bot access token | 关联 Inbox 或 Conversation 的自动处理范围 | 不能作为普通账号成员管理账号 |
| Platform App | api_access_token | 已授权的平台 Account 与 User 管理范围 | 不能访问未授权账号或绕过账号状态 |
| Super Admin | 安装级登录会话 | 安装级 Account、User 和全局设置 | 与普通 Account API 权限完全分离 |
| Provider Callback | 签名、密钥、验证 token 或来源规则 | 单一渠道或集成的回调入口 | 不能访问普通账号查询入口 |

## 4. Account 角色的功能边界

### 4.1 Administrator

通常可以执行：

- 管理 Account 设置；
- 邀请、更新和移除账号成员；
- 创建和管理 Inbox、Team、Label、Custom Attribute；
- 管理自动化、Macro、Campaign、Webhook、Integration；
- 管理 SLA、分配策略、容量策略和报表设置；
- 管理 Captain Assistant、知识来源、Custom Tool 和模型偏好；
- 查看审计、计费或企业功能，但仍受版本形态和 Feature 限制。

Administrator 不自动获得以下能力：

- 访问另一个 Account；
- 使用未启用的企业功能；
- 超过套餐或安装配置额度；
- 代表未授权 Contact 访问客户接口；
- 绕过 Provider 的文件、模板或消息规则；
- 使用安装级管理入口。

### 4.2 Agent

通常可以执行：

- 查看其可见范围内的会话；
- 回复消息、发送私密备注、添加附件；
- 变更会话状态、优先级、标签和分配；
- 查看和更新可见联系人资料；
- 使用 Macro、搜索、通知和个人设置；
- 使用已授权的 Captain Copilot 能力。

是否可见某个会话还取决于：

- 是否属于对应 Inbox；
- 是否为会话 assignee；
- 是否属于会话 Team；
- Account 对未分配会话的可见策略；
- Custom Role 是否进一步限制动作；
- Inbox 或对象是否仍处于有效状态。

### 4.3 Custom Role

Custom Role 采用明确权限集合。判断时必须区分：

- 菜单或入口可见；
- 列表可查看；
- 详情可查看；
- 创建、更新、删除分别是否允许；
- 批量动作是否单独允许；
- 导出和报表明细是否单独允许；
- 设置管理与业务操作是否分开授权。

不能用“能打开页面”推断所有相关 API 都可调用。

## 5. Inbox、Team 和对象范围

### 5.1 Inbox 范围

需要 Inbox 成员关系或等效授权的典型功能：

- 查看 Inbox 会话；
- 接收自动分配；
- 发送该渠道消息；
- 查看该 Inbox 的实时变化；
- 操作渠道模板和特定设置；
- 使用关联 Bot 或 Assistant。

Administrator 能否绕过 Inbox 成员限制，应按具体动作和当前版本行为核对，不能设为全局规则。

### 5.2 Team 范围

Team 主要用于：

- 分组管理账号成员；
- 将 Conversation 分配到 Team；
- 自动化动作指定 Team；
- 报表按 Team 汇总；
- 控制协作范围和工作分配。

Team 成员关系不自动等于 Inbox 成员关系，两种关系必须分别保存和校验。

### 5.3 对象级范围

| 对象 | 范围判断 |
|---|---|
| Contact | 必须属于路径中的 Account |
| Conversation | 必须属于 Account；显示编号只在账号范围内解释 |
| Message | 必须属于目标 Conversation |
| Inbox | 必须属于 Account，且渠道类型必须匹配动作 |
| Campaign | 必须属于 Account，并使用可用 Inbox |
| Integration Hook | 必须属于 Account 和对应 App |
| Captain Document | 必须属于目标 Assistant |
| Portal Article | 必须属于目标 Portal 和 Category 关系 |
| Platform Account User | 必须属于 Platform App 已授权范围 |
| Contact Conversation | 必须属于当前 Contact token 所代表身份 |

## 6. Feature、安装配置、版本形态和套餐的联合判定

### 6.1 四类控制项

| 控制项 | 控制范围 | 示例 | 修改后的生效特征 |
|---|---|---|---|
| Account Feature | 单个 Account | SLA、Custom Role、Captain、Audit | 通常只影响目标 Account |
| 安装配置 | 整个安装环境 | 注册、邮件、存储、Provider、限流 | 影响所有或一类账号 |
| 版本形态 | 整体能力边界 | 开源版、企业版、Cloud | 决定入口和授权体系 |
| 套餐与额度 | 账号消费边界 | 席位、AI 用量、联系人、会话 | 用尽后限制新增或继续使用 |

完整的 68 个 Account Feature 和 102 个安装配置见 [24-Feature开关版本与安装配置字典](24-Feature开关版本与安装配置字典.md)。

### 6.2 联合可用公式

一个受控功能只有在下列条件同时满足时可用：

1. 当前版本形态包含该功能；
2. 安装配置允许该功能；
3. Account Feature 已开启；
4. 企业授权或 Cloud 套餐有效；
5. 额度未耗尽；
6. 当前身份拥有操作权限；
7. 目标对象属于身份可见范围；
8. 必要 Provider 已配置且健康；
9. 对象状态允许当前动作；
10. 未触发频率或并发限制。

任何一项不满足，都不能仅通过修改前端显示状态恢复能力。

### 6.3 Feature 关闭后的结果

关闭 Feature 后，应逐项核对：

- 新入口是否隐藏；
- 相关 API 是 403、404 还是功能不可用；
- 已有对象是否只读、隐藏或仍可删除；
- 已经运行的异步任务是否继续完成；
- 已配置的自动化是否停止执行相关动作；
- 历史报表和审计数据是否继续可查看；
- 实时事件是否停止提供 Feature 专属字段；
- 重新开启后原配置是否恢复。

Feature 关闭不等于立即删除历史数据。

### 6.4 套餐到期或额度耗尽

必须分别定义：

- 是否阻止创建新对象；
- 是否允许读取历史对象；
- 是否允许删除或导出；
- 是否允许已经开始的任务完成；
- 是否进入宽限期；
- 是否在重新获得额度后自动恢复；
- 错误响应是否包含限制类型、当前值和上限。

## 7. 安装配置与 Account 设置的优先级

| 场景 | 优先级规则 |
|---|---|
| 全局能力未启用 | Account 不能通过自身设置强行启用 |
| 全局提供能力，Account Feature 关闭 | 目标 Account 不可使用 |
| 全局上限与 Account 上限并存 | 使用更严格的有效上限 |
| Account 时区与 Inbox 时区 | Inbox 业务时间优先用 Inbox 时区；账号级报表按报表约定 |
| 系统默认模型与 Account 模型偏好 | 合法的 Account 偏好优先；不可用时按明确回退规则 |
| 全局邮件设置与 Inbox 邮件渠道 | 全局发送能力是前提，Inbox 凭据决定具体渠道 |
| 全局频率限制与具体接口限制 | 任意一个先达到都可阻止请求 |
| 全局文件限制与渠道文件限制 | 使用更严格的有效限制 |

设置优先级必须在基线中固定，不得根据请求顺序随机改变。

## 8. 对象状态对动作的限制

| 对象状态 | 受限动作 | 预期处理 |
|---|---|---|
| Account suspended | 账号范围读写 | 拒绝普通账号访问 |
| Account pending deletion | 新增或高风险变更 | 按 Cloud 删除流程限制，并允许规定的恢复动作 |
| Conversation resolved | 客户继续发消息 | 依据 Inbox 设置允许并重开，或拒绝 |
| Conversation snoozed | 新消息到达 | 按规则恢复 open 或保持目标状态 |
| Message failed | 重试发送 | 仅允许具备必要内容和渠道条件的消息 |
| Message deleted | 编辑、重试或下载附件 | 不得恢复原文或原附件 |
| Campaign processing | 重复调度 | 只允许一个调度者取得处理权 |
| Campaign completed | 再次正常发送 | 不得生成第二轮目标，除非明确复制为新活动 |
| Inbox authorization error | 新消息发送 | 返回渠道错误或消息失败，提示重新授权 |
| Call terminal state | 后到非终态回调 | 不覆盖终态 |
| Data Import stale run | 旧任务继续写入 | 跳过旧运行结果 |
| Captain Document syncing | 同一文档再次同步 | 互斥、排队或返回当前状态 |

## 9. 错误结果的区分

| 状态 | 语义 | 常见情况 |
|---:|---|---|
| 400 | 请求结构或参数无法解释 | 缺少必需参数、格式无效 |
| 401 | 身份未建立、已失效或账号不可使用 | Token 无效、登录过期、账号暂停 |
| 403 | 身份已识别但无权执行 | Role、Feature、API Access、套餐限制 |
| 404 | 入口或当前范围内对象不存在 | 资源不属于账号、Feature 入口隐藏 |
| 409 | 当前状态或并发条件冲突 | 会话限制、重复状态领取、资源冲突 |
| 422 | 字段或业务规则校验失败 | 枚举错误、唯一冲突、状态不允许 |
| 429 | 请求过于频繁 | IP、用户、账号或 Token 频率限制 |
| 5xx | 服务或外部依赖暂时不可用 | 网络错误、Provider 故障、任务异常 |

出于安全隔离，同一“对象存在但不属于当前范围”的情况可能表现为 404，而不是 403。

## 10. API Access Feature 的独立作用

使用 access-token 调用 Account API 时，除普通成员权限外，还需满足 API 访问能力要求。必须核对：

- 登录会话可用但 API Token 是否仍被 Feature 限制；
- Administrator 是否也受 API Access Feature 约束；
- Profile、登录和公开入口是否属于同一限制范围；
- Feature 关闭前签发的 Token 是否立即失效于受控 API；
- 重新开启后原 Token 是否恢复；
- Platform 和 Contact Token 是否使用各自独立规则。

不能把所有 token 都当作相同权限模型。

## 11. Super Admin 与 Account 管理的隔离

Super Admin 入口用于安装级管理，与 Account Administrator 不互相继承：

- Account Administrator 不能因管理某个账号而进入安装级功能；
- Super Admin 登录成功不代表拥有普通账号会话；
- 安装级 Account 状态变更会影响普通账号访问；
- Super Admin 的登录、退出和频率限制单独计算；
- 安装级 Token、Platform App、全局 Agent Bot 与账号内对象使用不同范围；
- 审计时必须区分安装级操作主体和账号内操作主体。

对应入口见 [25-SuperAdmin与安装级管理功能](25-SuperAdmin与安装级管理功能.md)。

## 12. Public、Platform、Widget 和普通 Account API 的差异

| API 家族 | 资源定位 | 身份重点 | 典型用途 |
|---|---|---|---|
| Account API | account_id + 业务对象 ID | Account User access-token | 客服工作和账号管理 |
| Platform API | Platform App 授权范围 | api_access_token | 创建和管理平台账号、用户与关系 |
| Public API | inbox_identifier、contact_identifier 等公开标识 | Contact token 或公开凭据 | 客户侧会话和消息 |
| Widget API | website token、contact token、会话上下文 | 网站访客身份 | 网站聊天窗口 |
| Super Admin | 安装级资源 ID | 安装级登录会话 | 全局账号、用户和配置管理 |
| Callback API | Provider 路径和校验信息 | 签名、验证 token、来源规则 | 接收第三方事件 |

相同字段名在不同 API 家族中可能使用不同 ID 类型，不能互换。

## 13. 历史兼容规则

### 13.1 PATCH 与 PUT

部分历史入口可能同时接受 PATCH 和 PUT，或文档与实际调用习惯不同。保持一致时应：

- 以 [30-全API逐动作路径索引](30-全API逐动作路径索引.md) 列出的当前动作作为基线；
- 若两个方法都可用，记录响应和副作用是否完全相同；
- 不把接受旧方法解释为新方法不可用；
- 对不再支持的方法返回确定错误，不得产生部分更新。

### 13.2 display_id、id、identifier 与 source_id

| 标识 | 常见范围 | 兼容要求 |
|---|---|---|
| id | 对象主标识 | 按接口路径规定使用 |
| display_id | Account 内可读编号 | 必须先限定 account_id |
| identifier | 对外稳定公开标识 | 不能自动当成数字 ID |
| source_id | Provider 或 Inbox 内身份标识 | 只能在对应 Inbox 范围解释 |

历史数据可能只拥有其中一部分字段。允许的回退顺序必须按具体接口固定，不能全局猜测。

### 13.3 旧登录会话

- 早期创建且没有完整追踪信息的会话，应在达到会话上限时优先被移除；
- 当前会话不得因为清理旧会话而意外退出；
- 默认最多保留 25 个有效会话，除非安装配置明确调整；
- 登录设备列表缺失的旧字段按“未知”显示，不得伪造设备信息；
- Token 轮换后，旧 Token 的有效期与失效时机必须明确。

### 13.4 新增字段与旧数据

新增可选字段出现时：

- 旧对象允许字段缺失；
- 响应层可按约定补充默认值；
- 不能在读取旧对象时产生业务副作用；
- 列表与详情对默认值的表达应一致；
- 外部调用方必须容忍响应增加新字段；
- 枚举增加新值时不能按未知错误导致整个对象不可读取。

### 13.5 Provider 字段别名和状态映射

第三方平台可能在不同版本使用字段别名或新增状态。兼容规则应固定：

- 首选字段；
- 允许的历史字段；
- 两者同时存在时的优先级；
- 未知状态的保存与告警方式；
- 是否仍返回成功确认，防止第三方持续重试；
- 哪些未知事件必须拒绝。

## 14. 运行条件必须纳入基线

| 条件 | 对功能的影响 | 对照时必须固定 |
|---|---|---|
| 当前时间 | Token、MFA、SSO、Campaign、SLA、工作时间 | 统一时钟和允许误差 |
| 时区 | 工作时间、报表、活动计划 | Account、Inbox 和请求时区 |
| 邮件发送能力 | 邀请、重置密码、Email Inbox、Transcript | 发件配置和回执条件 |
| 文件服务 | 头像、附件、导入、知识文件 | 大小、类型、公开访问策略 |
| 搜索服务 | 全局搜索、消息检索、帮助中心 | 索引收敛时间 |
| 异步处理能力 | 删除、导入、活动、同步、Webhook | 可用性和最终状态查询 |
| Provider 网络 | 渠道发送、OAuth、模板和状态 | 测试账号、版本、回调地址 |
| AI Provider | Copilot、摘要、改写、知识问答 | 模型、凭据、温度和超时 |
| 计费状态 | Cloud 套餐和额度 | 方案、周期、已用量 |
| 密钥与签名 | Webhook、Widget、Provider 回调 | 同一密钥版本和轮换时间 |

不固定这些条件，两个相同请求可能得到不同结果，不能据此判为功能不一致。

## 15. 配置变更的传播规则

配置变更后需要分别检查：

1. 新请求是否立即采用新配置；
2. 已建立登录会话是否继续有效；
3. 已排队任务采用旧配置快照还是执行时新配置；
4. 已发送的外部请求是否可撤回；
5. 公开 Widget 是否需要刷新配置；
6. 搜索、报表和实时事件何时反映新值；
7. 回滚配置后是否恢复原行为；
8. 配置轮换期间是否允许新旧密钥短时并存。

以上结果必须按配置项记录，不能统一假定“保存后立即全部生效”。

## 16. 必测权限与兼容场景

| 编号 | 场景 | 预期核对结果 |
|---|---|---|
| PFC-01 | 有效 Agent Token 访问未加入 Account | 401、403 或 404 与基线一致，且不泄露对象 |
| PFC-02 | Administrator 调用关闭的企业 Feature | 不因角色高而绕过 Feature |
| PFC-03 | Feature 开启但套餐额度耗尽 | 返回明确额度限制，不创建半成品 |
| PFC-04 | Agent 有角色权限但不属于目标 Inbox | 按对象范围拒绝 |
| PFC-05 | Custom Role 可读但不可删除 | 详情成功，删除被拒绝 |
| PFC-06 | Account suspended 后使用旧 Token | 账号范围调用被阻止 |
| PFC-07 | Contact Token 读取另一 Contact 会话 | 按不可见处理 |
| PFC-08 | Platform App 操作未授权 Account | 拒绝且不改变目标账号 |
| PFC-09 | Feature 关闭后读取历史对象 | 按约定只读、隐藏或不可用，不删除历史数据 |
| PFC-10 | 旧对象缺少新增字段 | 仍可读取，并按兼容规则表达默认值 |
| PFC-11 | display_id 在两个 Account 重复 | 各自只定位本账号 Conversation |
| PFC-12 | Provider 授权过期后发送消息 | 消息进入明确失败状态并提供恢复方向 |
| PFC-13 | 配置变更时已有异步任务运行 | 按已声明的配置快照规则完成 |
| PFC-14 | 同时达到全局和接口频率限制 | 返回 429，限制维度可诊断 |
| PFC-15 | Super Admin 会话调用普通 Account API | 不自动获得账号身份 |

## 17. 一致性判定标准

本分册范围达到一致，必须同时满足：

- 十二层判定顺序一致；
- 同一身份、同一对象和同一配置下的允许与拒绝结果一致；
- Account、Inbox、Team、Contact、Bot 和 Platform 的范围隔离一致；
- Feature、安装配置、版本形态和套餐的联合结果一致；
- 状态码、错误类型和不泄露规则一致；
- 历史标识、旧会话、缺失字段和 Provider 别名的兼容结果一致；
- 配置变化对新请求、已有会话和运行中任务的传播方式一致；
- 第 16 节全部场景通过；
- 任何差异均已进入 [32-完整系统一致性还原目标与基线](32-完整系统一致性还原目标与基线.md) 的允许差异登记表。

只复现“Administrator 能做、Agent 不能做”的粗粒度规则，不足以还原完整权限行为。
