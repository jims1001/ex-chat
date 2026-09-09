---
name: conversation-participants-lifecycle
description: >-
  Defines the architectural design, lifecycle workflows (add, remove, full update/sync, list),
  and mandatory structured logging standards for conversation participants management in Chatwoot.
  Use whenever creating, modifying, or troubleshooting conversation participant collaboration and auditing features.
---

# Conversation Participants Lifecycle & Logging Specification

本 Skill 规范了客服系统（Chatwoot）中**会话参与者（Conversation Participants / 协作坐席）**的生命周期流转机制、API 接口契约、跨租户安全隔离、原子差异更新流程以及**强制结构化日志审计规范**。

---

## 1. 业务概念与职责划分

在复杂的客服服务场景中，除主分配坐席（`Assignee`）外，通常需要多个领域的专家或二级技术支持作为**参与者（Participants）**协同处理工单：
- **主要分配坐席（Assignee）**：直接对会话负责并与客户沟通；
- **参与坐席（Participant）**：协同查阅会话记录、发送内部备注（Private Notes）、或受邀协同回复客户；
- **生命周期动作**：
  - **添加参与者（Add）**：单人或多名坐席被拉入会话；
  - **移除参与者（Remove）**：某位坐席退出协作或被管理员移除；
  - **完整更新/全量同步（Full Update / Sync）**：前端批量配置参与人列表时，执行全量差异同步（原子剔除未选中坐席，并新增增量坐席）；
  - **参与者列表（List）**：展示当前会话的所有参与坐席。

---

## 2. 领域模型与数据库契约

- **表名**：`conversation_participants`
- **实体定义**（`internal/domain/models.go`）：
  ```go
  type ConversationParticipant struct {
      ID             uint      `gorm:"primaryKey" json:"id"`
      ConversationID uint      `gorm:"uniqueIndex:idx_conv_part;not null" json:"conversation_id"`
      UserID         uint      `gorm:"uniqueIndex:idx_conv_part;not null" json:"user_id"`
      CreatedAt      time.Time `json:"created_at"`
  }
  ```
- **核心约束**：
  - 联合唯一索引 `idx_conv_part (conversation_id, user_id)`，严格保证同一会话不可重复加入同一参与者；
  - 操作需遵循幂等性设计。

---

## 3. 标准 API 接口契约

所有接口挂载在租户认证作用域 `/api/v1/accounts/:account_id` 下：

| HTTP 方法 | 请求路径 | 功能说明 | 核心请求体 / 参数 |
|---|---|---|---|
| `GET` | `/conversations/:id/participants` | 查询指定会话的参与者列表 | 无 |
| `POST` | `/conversations/:id/participants` | 增量添加参与者（支持单人/批量） | `{"user_ids": [101, 102]}` |
| `DELETE` | `/conversations/:id/participants/:user_id` | 按路径参数移除单个参与者 | `:user_id` (路径参数) |
| `DELETE` | `/conversations/:id/participants` | 按请求体批量移除参与者 | `{"user_ids": [101, 102]}` 或 `{"user_id": 101}` |
| `PUT` | `/conversations/:id/participants` | 完整更新/全量同步参与者列表 | `{"user_ids": [102, 103, 104]}` |
| `PATCH` | `/conversations/:id/participants` | 完整更新同步别名路由 | `{"user_ids": [102, 103, 104]}` |

---

## 4. 关键流程设计与实现规范

### 4.1 移除参与者流程（Removal Workflow）
1. **参数提取**：优先从 URL 路径参数 `:user_id` 解析；若未提供，兼容从 JSON 请求体解析 `user_id` 或 `user_ids` 列表；
2. **多租户安全边界防御**：
   - 检查会话是否存在且必须归属于当前 `account_id`；
   - 若会话属于其他租户，必须阻断并记录 `WARN` 告警日志，响应 `404 Not Found`；
3. **安全删除**：
   - `DELETE FROM conversation_participants WHERE conversation_id = ? AND user_id IN (?)`；
4. **日志审计**：
   - 记录执行操作人 `operator_id`、目标用户 `target_user_ids`、实际影响行数 `removed_count`；
   - 若记录不存在，输出 `WARN` 日志；若删除成功，输出 `INFO` 日志。

### 4.2 完整更新流程（Full Update / Replace Sync Workflow）
当界面采用多选选择器或标签输入框进行参与者更新时，采用**原子全量同步（Diff Sync）**：
1. **输入清洗**：去除无效 ID（0）并对目标列表去重；
2. **事务原子性**：开启数据库事务 `db.Transaction`：
   - **Step 1 (查验)**：查询当前数据库中已存在的参与者列表 `currentParts`；
   - **Step 2 (差量计算)**：
     - `toRemove = current - desired`（在库中但不在目标列表中的用户）；
     - `toAdd = desired - current`（在目标列表中但尚未入库的新用户）；
     - `toKeep = current ∩ desired`（保持不变的用户，不执行额外写入，保留原 `created_at`）；
   - **Step 3 (批处理移除)**：若 `len(toRemove) > 0`，批量删除废弃记录；
   - **Step 4 (批处理新增)**：针对 `toAdd` 中的用户，插入新记录；
   - **Step 5 (最终拉取)**：拉取更新后的最新列表 `finalParts` 返回客户端；
3. **结构化审计**：在事务成功后，详细记录本次更新被移除的用户列表、新增的用户列表及最终参与者总数。

---

## 5. 强制结构化日志审计规范 (Mandatory Logging Standards)

为满足企业合规审计与系统可观测性要求，所有涉及参与者变动的控制器和业务逻辑**必须接入统一结构化日志**：

### 5.1 统一组件命名
- **Component 名称**：`conversation_participant`
- **使用方法**：`logger.WithComponent("conversation_participant")`

### 5.2 必须包含的核心上下文键值
每个日志条目必须根据动作挂载以下字段：

| 键名 (Key) | 类型 | 说明 | 示例 |
|---|---|---|---|
| `account_id` | `uint` | 当前操作所属的租户/账户 ID | `1` |
| `conversation_id` | `uint` | 涉及的会话 ID | `10024` |
| `operator_id` | `uint` | 当前执行操作的用户/坐席 ID（来自 Token） | `15` |
| `action` | `string` | 动作分类标识 | `"add_participants"`, `"remove_participants"`, `"update_participants"` |
| `target_user_ids` / `user_ids` | `[]uint` | 本次动作的目标坐席列表 | `[101, 102]` |
| `added_count` / `removed_count` | `int64` | 实际变更的记录行数 | `2` |
| `added_user_ids` | `[]uint` | 差异同步中实际新增的 User ID 集合 | `[103]` |
| `removed_user_ids` | `[]uint` | 差异同步中实际剔除的 User ID 集合 | `[101]` |
| `final_count` | `int` | 变更后当前会话拥有的参与者总数 | `3` |
| `error` | `string` | 发生错误时的底层错误信息 | `"record not found"` |

### 5.3 日志级别落地规则

#### 1) `INFO` 级（正常业务审计）
- 参与者添加成功：
  ```go
  logger.WithComponent("conversation_participant").Info("conversation participants added",
      "account_id", accID,
      "conversation_id", convID,
      "operator_id", operatorID,
      "requested_user_ids", req.UserIDs,
      "added_count", addedCount,
      "added_user_ids", addedUserIDs,
  )
  ```
- 参与者移除成功：
  ```go
  logger.WithComponent("conversation_participant").Info("conversation participants removed successfully",
      "account_id", accID,
      "conversation_id", convID,
      "operator_id", operatorID,
      "target_user_ids", validUIDs,
      "removed_count", res.RowsAffected,
  )
  ```
- 完整更新同步成功：
  ```go
  logger.WithComponent("conversation_participant").Info("conversation participants fully updated",
      "account_id", accID,
      "conversation_id", convID,
      "operator_id", operatorID,
      "added_user_ids", toAdd,
      "removed_user_ids", toRemove,
      "final_count", len(finalParts),
  )
  ```
- 参与者列表查询：
  ```go
  logger.WithComponent("conversation_participant").Info("conversation participants listed",
      "account_id", accID,
      "conversation_id", convID,
      "operator_id", operatorID,
      "participant_count", len(parts),
  )
  ```

#### 2) `WARN` 级（客户端输入错误、未命中或安全拦截）
- 缺少参数或空列表：
  ```go
  logger.WithComponent("conversation_participant").Warn("remove participant failed: missing user_id",
      "account_id", accID,
      "conversation_id", convID,
      "operator_id", operatorID,
  )
  ```
- 跨租户越权访问或会话不存在：
  ```go
  logger.WithComponent("conversation_participant").Warn("conversation access denied (cross-tenant violation)",
      "action", action,
      "account_id", accID,
      "conversation_id", convID,
      "actual_account_id", conv.AccountID,
      "operator_id", operatorID,
  )
  ```
- 尝试移除不存在的参与者记录：
  ```go
  logger.WithComponent("conversation_participant").Warn("no participants were removed (records not found)",
      "account_id", accID,
      "conversation_id", convID,
      "operator_id", operatorID,
      "target_user_ids", validUIDs,
  )
  ```

#### 3) `ERROR` 级（系统级异常、数据库故障）
- 数据库事务失败或底层查询中断：
  ```go
  logger.WithComponent("conversation_participant").Error("database error during full update of conversation participants",
      "account_id", accID,
      "conversation_id", convID,
      "operator_id", operatorID,
      "error", err.Error(),
  )
  ```

---

## 6. 模块化与代码组织规范

为了避免单个文件膨胀（遵循“一个文件不能太大”原则）：
- 会话参与者专属控制器统一归入：[`internal/handler/conversation_participant_handler.go`](file:///Users/tong/Documents/GitHub/OracleBetX-Projects/ex-chat/internal/handler/conversation_participant_handler.go)；
- 严禁将超过 50 行的辅助判断逻辑直接杂糅至 `crm_ops_advanced_handler.go` 或 `conversation_handler.go`；
- 所有参与者生命周期路由统一在 [`internal/router/router.go`](file:///Users/tong/Documents/GitHub/OracleBetX-Projects/ex-chat/internal/router/router.go) 中的 `tenant` 分组声明。

---

## 7. 自动化测试与验证清单

在后续对会话参与者功能进行扩展或重构时，必须运行专项回归测试并确认所有断言通过：

```bash
# 运行参与者完整生命周期专项测试
GOTOOLCHAIN=local go test -v -run TestConversationParticipantsLifecycle ./test/

# 运行全系统测试
GOTOOLCHAIN=local go test -count=1 ./...
```

**测试必须覆盖的 5 大关键场景**：
1. `Add_Participants_And_List`：增量添加、幂等重试（避免 duplicate key 报错）、列表查询；
2. `Remove_Single_Participant_Via_Path_Param`：通过 REST 路径参数 `:user_id` 删除单个参与者；
3. `Remove_Batch_Participants_Via_Body`：通过 JSON 请求体批量剔除多个参与者；
4. `Full_Update_Workflow_Diff_Sync`：PUT / PATCH 全量差异同步，验证原有参与者剔除、新增参与者插入、交集参与者保持不变；
5. `Multi_Tenant_Isolation`：租户 B 尝试操作租户 A 会话参与者时，100% 拦截并返回 404，同时触发 WARN 安全日志。
