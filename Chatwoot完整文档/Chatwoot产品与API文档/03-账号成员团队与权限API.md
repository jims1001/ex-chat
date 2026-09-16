# 账号、成员、团队与权限 API

> 导航：[全体功能与 API 总表](00-全体功能与API总表.md)｜[文档目录](README.md)

## 1. 功能范围

本模块用于管理租户账号、客服成员、团队、Inbox 成员、角色和可分配对象。所有账号级接口都以 account_id 作为租户范围。

## 2. Account API

| 方法 | 路径 | 功能 | 通常权限 |
|---|---|---|---|
| POST | /api/v1/accounts | 创建账号 | 已登录用户或安装策略允许 |
| GET | /api/v1/accounts/{account_id} | 获取账号详情 | Account 成员 |
| PATCH | /api/v1/accounts/{account_id} | 更新账号 | Administrator |
| POST | /api/v1/accounts/{account_id}/update_active_at | 更新当前活动时间 | Account 成员 |
| GET | /api/v1/accounts/{account_id}/cache_keys | 获取配置版本标识 | Account 成员 |

### 2.1 Account 字段

| 字段 | 类型 | 可写 | 说明 |
|---|---|---:|---|
| id | integer | 否 | Account ID |
| name | string | 是 | 账号名称 |
| locale | string | 是 | 默认语言 |
| domain | string 或 null | 受限 | 入站邮件相关域名 |
| support_email | string 或 null | 是 | 支持邮箱 |
| status | string | 受限 | 账号状态 |
| role | string | 否 | 当前用户在账号中的角色 |
| feature_flags | integer 或 object | 否 | 当前账号功能能力 |
| settings | object | 是 | 账号业务设置 |
| custom_attributes | object | 是 | 账号自定义信息 |
| created_at | time | 否 | 创建时间 |

### 2.2 Account settings 常用字段

| 字段 | 类型 | 说明 |
|---|---|---|
| reporting_timezone | string | 报表使用的 IANA 时区 |
| auto_resolve_duration | integer 或 null | 自动解决等待时长，是否生效取决于功能配置 |
| captain_auto_resolve_mode | string 或 null | Captain 自动解决模式 |
| audio_transcriptions | boolean | 是否允许音频转写 |

未知 settings 字段应保持原值，不应在局部更新时整体清空。

## 3. Agent API

| 方法 | 路径 | 功能 | 通常权限 |
|---|---|---|---|
| GET | /api/v1/accounts/{account_id}/agents | 成员列表 | Account 成员 |
| POST | /api/v1/accounts/{account_id}/agents | 添加成员 | Administrator |
| POST | /api/v1/accounts/{account_id}/agents/bulk_create | 批量添加成员 | Administrator |
| PATCH | /api/v1/accounts/{account_id}/agents/{id} | 更新成员 | Administrator |
| DELETE | /api/v1/accounts/{account_id}/agents/{id} | 移除成员 | Administrator |

### 3.1 Agent 请求字段

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| name | string | 创建时是 | 姓名 |
| email | string | 创建时是 | 登录邮箱 |
| role | enum | 创建时是 | agent 或 administrator |
| availability | enum | 否 | 可用状态配置 |
| auto_offline | boolean | 否 | 离开后是否自动离线 |
| custom_role_id | integer 或 null | 否 | Enterprise 自定义角色 |

### 3.2 Agent 响应字段

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | User ID |
| account_id | integer | Account ID |
| name | string | 姓名 |
| email | string | 邮箱 |
| role | enum | agent 或 administrator |
| availability | enum | 配置的可用状态 |
| availability_status | enum | 当前计算状态：online、busy、offline |
| auto_offline | boolean | 自动离线设置 |
| confirmed | boolean | 邮箱是否确认 |
| custom_role_id | integer 或 null | 自定义角色 |
| thumbnail | string 或 null | 头像地址 |

availability_status 是展示结果，不应作为成员配置写入。

## 4. Assignable Agent API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/assignable_agents | 获取账号范围可分配客服 |
| GET | /api/v1/accounts/{account_id}/inboxes/{inbox_id}/assignable_agents | 获取指定 Inbox 可分配客服 |

常用响应字段：

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | Agent ID |
| name | string | 姓名 |
| availability_status | enum | 当前可用状态 |
| thumbnail | string 或 null | 头像 |
| confirmed | boolean | 是否已确认 |

Inbox 范围结果可能受到成员关系、容量、角色和可用状态影响。

## 5. Team API

| 方法 | 路径 | 功能 | 通常权限 |
|---|---|---|---|
| GET | /api/v1/accounts/{account_id}/teams | 团队列表 | Account 成员 |
| POST | /api/v1/accounts/{account_id}/teams | 创建团队 | Administrator |
| GET | /api/v1/accounts/{account_id}/teams/{team_id} | 团队详情 | Account 成员 |
| PATCH | /api/v1/accounts/{account_id}/teams/{team_id} | 更新团队 | Administrator |
| DELETE | /api/v1/accounts/{account_id}/teams/{team_id} | 删除团队 | Administrator |
| GET | /api/v1/accounts/{account_id}/teams/{team_id}/team_members | 团队成员 | Account 成员 |
| POST | /api/v1/accounts/{account_id}/teams/{team_id}/team_members | 添加团队成员 | Administrator |
| PATCH | /api/v1/accounts/{account_id}/teams/{team_id}/team_members | 更新团队成员 | Administrator |
| DELETE | /api/v1/accounts/{account_id}/teams/{team_id}/team_members | 移除团队成员 | Administrator |

### 5.1 Team 字段

| 字段 | 类型 | 可写 | 说明 |
|---|---|---:|---|
| id | integer | 否 | Team ID |
| name | string | 是 | 团队名称 |
| description | string 或 null | 是 | 团队说明 |
| allow_auto_assign | boolean | 是 | 是否允许自动分配 |
| account_id | integer | 否 | 所属 Account |
| members | array | 否 | 团队成员，详情接口可能返回 |

### 5.2 Team Member 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| user_ids | integer array | 批量添加或移除的 User ID |
| team_id | integer | Team ID，通常由路径提供 |

## 6. Inbox Member API

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/inbox_members/{inbox_id} | 获取 Inbox 成员 |
| POST | /api/v1/accounts/{account_id}/inbox_members | 设置或添加 Inbox 成员 |
| PATCH | /api/v1/accounts/{account_id}/inbox_members | 更新 Inbox 成员 |
| DELETE | /api/v1/accounts/{account_id}/inbox_members | 移除 Inbox 成员 |

| 字段 | 类型 | 必填 | 说明 |
|---|---|---:|---|
| inbox_id | integer | 是 | Inbox ID |
| user_ids | integer array | 是 | Agent ID 列表 |

成员必须属于同一 Account。移除 Inbox 成员后，其历史会话记录仍保留，但后续可见范围和可分配范围会发生变化。

## 7. Custom Role API（Enterprise）

| 方法 | 路径 | 功能 |
|---|---|---|
| GET | /api/v1/accounts/{account_id}/custom_roles | 角色列表 |
| POST | /api/v1/accounts/{account_id}/custom_roles | 创建角色 |
| GET | /api/v1/accounts/{account_id}/custom_roles/{id} | 角色详情 |
| PATCH | /api/v1/accounts/{account_id}/custom_roles/{id} | 更新角色 |
| DELETE | /api/v1/accounts/{account_id}/custom_roles/{id} | 删除角色 |

| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer | 角色 ID |
| name | string | 角色名称 |
| description | string 或 null | 角色说明 |
| permissions | string array | 权限标识列表 |
| account_id | integer | 所属账号 |

删除仍被成员使用的角色前，应先确认成员的替代权限。

## 8. Agent Capacity Policy API（Enterprise）

| 方法 | 路径 | 功能 |
|---|---|---|
| GET/POST | /api/v1/accounts/{account_id}/agent_capacity_policies | 列表或创建策略 |
| GET/PATCH/DELETE | /api/v1/accounts/{account_id}/agent_capacity_policies/{id} | 查看、更新或删除 |
| GET | /api/v1/accounts/{account_id}/agent_capacity_policies/{id}/users | 查看适用成员 |
| POST | /api/v1/accounts/{account_id}/agent_capacity_policies/{id}/users | 添加适用成员 |
| DELETE | /api/v1/accounts/{account_id}/agent_capacity_policies/{id}/users/{user_id} | 移除适用成员 |
| POST | /api/v1/accounts/{account_id}/agent_capacity_policies/{id}/inbox_limits | 新增 Inbox 容量 |
| PATCH | /api/v1/accounts/{account_id}/agent_capacity_policies/{id}/inbox_limits/{limit_id} | 更新 Inbox 容量 |
| DELETE | /api/v1/accounts/{account_id}/agent_capacity_policies/{id}/inbox_limits/{limit_id} | 删除 Inbox 容量 |

### 8.1 Capacity 字段

| 字段 | 类型 | 说明 |
|---|---|---|
| name | string | 策略名称 |
| description | string 或 null | 策略说明 |
| user_ids | integer array | 适用 Agent |
| inbox_id | integer | 指定 Inbox |
| conversation_limit | integer | 最大并行会话数 |

## 9. 权限规则

- Administrator 管理账号、成员、角色和成员关系；
- Agent 只能读取其角色和 Inbox 范围允许的数据；
- Custom Role 不能获得超出 Account Feature 的能力；
- Account A 的 Token 不得操作 Account B 的成员、团队或策略；
- 成员删除、角色切换和团队调整应重新获取可分配列表；
- 容量达到上限时，自动分配和人工分配的结果可能不同，应以接口返回为准。

基础角色、6 种 Custom Role 权限和各接口组的可用范围详见：[角色权限与接口可用性矩阵](21-角色权限与接口可用性矩阵.md)。Inbox 分配顺序、公平分配和批量操作详见：[分配策略、筛选、导入与批量操作 API](14-分配策略筛选导入与批量操作API.md)。
