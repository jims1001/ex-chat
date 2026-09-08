package domain

import "time"

// Standard 16 actions (LOG-02)
const (
	ActionCreate         = "create"
	ActionUpdate         = "update"
	ActionStatusChange   = "status_change"
	ActionAddRelation    = "add_relation"
	ActionRemoveRelation = "remove_relation"
	ActionReplaceSet     = "replace_set"
	ActionMerge          = "merge"
	ActionSplit          = "split"
	ActionArchive        = "archive"
	ActionRestore        = "restore"
	ActionSoftDelete     = "soft_delete"
	ActionHardDelete     = "hard_delete"
	ActionRedact         = "redact"
	ActionMigrate        = "migrate"
	ActionCompensate     = "compensate"
	ActionCorrectLog     = "correct_log"
	ActionAssignment     = "assignment"
	ActionDelete         = "delete"
)

// Data classifications (LOG-06)
const (
	ClassificationPublic       = "public"
	ClassificationInternal     = "internal"
	ClassificationConfidential = "confidential"
	ClassificationRestricted   = "restricted"
)

// Retention classes (LOG-04, LOG-06)
const (
	RetentionSecurityCritical  = "security_critical"
	RetentionBusinessChange    = "business_change"
	RetentionBillingLegal      = "billing_legal"
	RetentionPluginDelivery    = "plugin_delivery"
	RetentionAiGovernance      = "ai_governance"
	RetentionMigrationEvidence = "migration_evidence"
	RetentionOperational       = "operational"
	RetentionLegalHold         = "legal_hold"
)

// LocalChangeJournal 本地变更日志（LOG-01 ~ LOG-08 基础层规范）
// 与每次核心业务数据写入在同一个数据库事务内原子提交
type LocalChangeJournal struct {
	ID                  uint      `gorm:"primaryKey" json:"id"`
	ChangeID            string    `gorm:"size:64;uniqueIndex;not null" json:"change_id"`
	SchemaVersion       string    `gorm:"size:20;default:'1.0'" json:"schema_version"`
	SourceModule        string    `gorm:"size:64;index;default:'CORE'" json:"source_module"`
	AccountID           uint      `gorm:"index;not null" json:"account_id"`
	EntityType          string    `gorm:"size:64;index" json:"entity_type"`
	EntityID            uint      `gorm:"index" json:"entity_id"`
	ObjectType          string    `gorm:"size:64;index" json:"object_type"`
	ObjectID            uint      `gorm:"index" json:"object_id"`
	ObjectDisplayID     string    `gorm:"size:64" json:"object_display_id,omitempty"`
	ObjectVersionBefore int       `gorm:"default:0" json:"object_version_before"`
	ObjectVersionAfter  int       `gorm:"default:1" json:"object_version_after"`
	Action              string    `gorm:"size:64;not null" json:"action"`
	ChangedFields       string    `gorm:"type:text" json:"changed_fields,omitempty"`
	BeforeState         string    `gorm:"type:text" json:"before_state,omitempty"`
	AfterState          string    `gorm:"type:text" json:"after_state,omitempty"`
	Diff                string    `gorm:"type:text" json:"diff,omitempty"`
	ActorType           string    `gorm:"size:50;not null" json:"actor_type"`
	ActorID             uint      `gorm:"index;not null" json:"actor_id"`
	ActorAccountRole    string    `gorm:"size:50" json:"actor_account_role,omitempty"`
	SourceType          string    `gorm:"size:50;default:'account_api'" json:"source_type"`
	SourceID            string    `gorm:"size:64" json:"source_id,omitempty"`
	Reason              string    `gorm:"size:255" json:"reason,omitempty"`
	RequestID           string    `gorm:"size:64;index" json:"request_id,omitempty"`
	CommandID           string    `gorm:"size:64;index" json:"command_id,omitempty"`
	EventID             string    `gorm:"size:64;index" json:"event_id,omitempty"`
	CorrelationID       string    `gorm:"size:64;index" json:"correlation_id,omitempty"`
	CausationID         string    `gorm:"size:64;index" json:"causation_id,omitempty"`
	IdempotencyKeyHash  string    `gorm:"size:64" json:"idempotency_key_hash,omitempty"`
	OccurredAt          time.Time `gorm:"index" json:"occurred_at"`
	RecordedAt          time.Time `gorm:"index" json:"recorded_at"`
	IngestedAt          time.Time `gorm:"index" json:"ingested_at"`
	Result              string    `gorm:"size:50;default:'applied'" json:"result"`
	DataClassification  string    `gorm:"size:50;default:'internal'" json:"data_classification"`
	RetentionClass      string    `gorm:"size:50;default:'business_change'" json:"retention_class"`
	IntegrityHash       string    `gorm:"size:64" json:"integrity_hash,omitempty"`
	PreviousHash        string    `gorm:"size:64" json:"previous_hash,omitempty"`
	Status              string    `gorm:"size:50;default:'published'" json:"status"` // pending, delivered, published
	CreatedAt           time.Time `gorm:"index" json:"created_at"`
}

// AuditLog 面向管理员只读查询的集中审计记录（与 Chatwoot API 兼容）
type AuditLog struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	ChangeID           string    `gorm:"size:64;uniqueIndex" json:"change_id"`
	AccountID          uint      `gorm:"index" json:"account_id"`
	SourceModule       string    `gorm:"size:64;index;default:'CORE'" json:"source_module"`
	EntityType         string    `gorm:"size:64;index" json:"entity_type"`
	EntityID           uint      `gorm:"index" json:"entity_id"`
	Action             string    `gorm:"size:64" json:"action"`
	Summary            string    `gorm:"size:255" json:"summary"`
	ActorType          string    `gorm:"size:50" json:"actor_type"`
	ActorID            uint      `gorm:"index" json:"actor_id"`
	ActorName          string    `gorm:"size:255" json:"actor_name"`
	CorrelationID      string    `gorm:"size:64" json:"correlation_id"`
	DataClassification string    `gorm:"size:50;default:'internal'" json:"data_classification"`
	CreatedAt          time.Time `gorm:"index" json:"created_at"`
}

// SecurityAuditLog 安全审计日志（LOG-01, LOG-05：登录、MFA、Session、Token 撤销、越权拒绝）
type SecurityAuditLog struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	AccountID uint      `gorm:"index;not null" json:"account_id"`
	ActorType string    `gorm:"size:50;not null" json:"actor_type"` // user, platform_app, system
	ActorID   uint      `gorm:"index;not null" json:"actor_id"`
	Action    string    `gorm:"size:64;not null" json:"action"`     // login, logout, mfa_setup, mfa_disable, session_revoke, token_revoke, access_denied
	IPAddress string    `gorm:"size:50" json:"ip_address,omitempty"`
	UserAgent string    `gorm:"size:255" json:"user_agent,omitempty"`
	Details   string    `gorm:"type:text" json:"details,omitempty"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`
}

// AuditExport 异步日志合规导出任务（LOG-05）
type AuditExport struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	ExportID    string    `gorm:"size:64;uniqueIndex;not null" json:"export_id"`
	AccountID   uint      `gorm:"index;not null" json:"account_id"`
	Status      string    `gorm:"size:50;default:'completed'" json:"status"` // pending, processing, completed, failed
	Since       time.Time `json:"since"`
	Until       time.Time `json:"until"`
	ObjectTypes string    `gorm:"size:255" json:"object_types,omitempty"`
	Actions     string    `gorm:"size:255" json:"actions,omitempty"`
	Format      string    `gorm:"size:20;default:'json'" json:"format"`
	Purpose     string    `gorm:"size:255" json:"purpose"`
	RecordCount int       `gorm:"default:0" json:"record_count"`
	DownloadURL string    `gorm:"size:255" json:"download_url,omitempty"`
	ExpiresAt   time.Time `json:"expires_at"`
	CreatedAt   time.Time `gorm:"index" json:"created_at"`
}

// IntegrityVerification 账本防篡改完整性验证结果（LOG-04, LOG-05, LOG-07）
type IntegrityVerification struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	VerificationID string    `gorm:"size:64;uniqueIndex;not null" json:"verification_id"`
	AccountID      uint      `gorm:"index;not null" json:"account_id"`
	PartitionID    string    `gorm:"size:64" json:"partition_id"`
	CheckpointID   string    `gorm:"size:64" json:"checkpoint_id,omitempty"`
	Verified       bool      `gorm:"default:true" json:"verified"`
	GapCount       int       `gorm:"default:0" json:"gap_count"`
	ConflictCount  int       `gorm:"default:0" json:"conflict_count"`
	Status         string    `gorm:"size:50;default:'completed'" json:"status"`
	Details        string    `gorm:"type:text" json:"details,omitempty"`
	CreatedAt      time.Time `gorm:"index" json:"created_at"`
}

// DataChangeCorrection 针对已有日志的追加式纠正记录（LOG-02, LOG-04, LOG-05）
// 绝对不可修改或覆盖原记录，通过追加新 correction 记录链接
type DataChangeCorrection struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	CorrectionChangeID string    `gorm:"size:64;uniqueIndex;not null" json:"correction_change_id"`
	OriginalChangeID   string    `gorm:"size:64;index;not null" json:"original_change_id"`
	AccountID          uint      `gorm:"index;not null" json:"account_id"`
	CorrectedFields    string    `gorm:"type:text;not null" json:"corrected_fields"`
	Reason             string    `gorm:"size:255;not null" json:"reason"`
	EvidenceRef        string    `gorm:"size:255" json:"evidence_ref,omitempty"`
	ActorID            uint      `gorm:"index;not null" json:"actor_id"`
	CreatedAt          time.Time `gorm:"index" json:"created_at"`
}

// AccessLog 访问受控审计日志的审计记录（LOG-05 Section 9 "查看日志也要审计"）
type AccessLog struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	AccountID      uint      `gorm:"index;not null" json:"account_id"`
	ActorID        uint      `gorm:"index;not null" json:"actor_id"`
	Action         string    `gorm:"size:64;not null" json:"action"`
	TargetResource string    `gorm:"size:255;not null" json:"target_resource"`
	Purpose        string    `gorm:"size:255" json:"purpose,omitempty"`
	IPAddress      string    `gorm:"size:50" json:"ip_address,omitempty"`
	CreatedAt      time.Time `gorm:"index" json:"created_at"`
}
