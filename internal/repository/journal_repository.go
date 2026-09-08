package repository

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
)

type JournalRepository struct {
	db *gorm.DB
}

func NewJournalRepository(db *gorm.DB) *JournalRepository {
	return &JournalRepository{db: db}
}

type DataChangeFilter struct {
	ObjectType    string
	ObjectID      *uint
	Action        string
	ActorType     string
	ActorID       *uint
	CorrelationID string
	SourceModule  string
	Since         *time.Time
	Until         *time.Time
	Page          int
	PageSize      int
}

// RecordChange 在业务变更事务内原子写入 Local Change Journal（LOG-01 ~ LOG-08 规范）
func (r *JournalRepository) RecordChange(tx *gorm.DB, j *domain.LocalChangeJournal) error {
	if j.ChangeID == "" {
		j.ChangeID = foundation.GenerateUUID()
	}
	if j.SchemaVersion == "" {
		j.SchemaVersion = "1.0"
	}
	if j.SourceModule == "" {
		j.SourceModule = "CORE"
	}
	if j.ObjectType == "" && j.EntityType != "" {
		j.ObjectType = j.EntityType
	}
	if j.EntityType == "" && j.ObjectType != "" {
		j.EntityType = j.ObjectType
	}
	if j.ObjectID == 0 && j.EntityID != 0 {
		j.ObjectID = j.EntityID
	}
	if j.EntityID == 0 && j.ObjectID != 0 {
		j.EntityID = j.ObjectID
	}

	now := time.Now().UTC()
	if j.CreatedAt.IsZero() {
		j.CreatedAt = now
	}
	if j.OccurredAt.IsZero() {
		j.OccurredAt = j.CreatedAt
	}
	if j.RecordedAt.IsZero() {
		j.RecordedAt = j.CreatedAt
	}
	if j.IngestedAt.IsZero() {
		j.IngestedAt = j.CreatedAt
	}
	if j.Status == "" {
		j.Status = "published"
	}
	if j.Result == "" {
		j.Result = "applied"
	}
	if j.DataClassification == "" {
		j.DataClassification = domain.ClassificationInternal
	}
	if j.RetentionClass == "" {
		j.RetentionClass = domain.RetentionBusinessChange
	}

	// 计算完整性摘要链（LOG-04 防篡改哈希计算）
	rawPayload := fmt.Sprintf("%s:%d:%s:%d:%s:%s:%s",
		j.ChangeID, j.AccountID, j.ObjectType, j.ObjectID, j.Action, j.PreviousHash, j.Diff)
	j.IntegrityHash = fmt.Sprintf("%x", sha256.Sum256([]byte(rawPayload)))

	if err := tx.Create(j).Error; err != nil {
		return fmt.Errorf("failed to write local change journal: %w", err)
	}

	// 同步生成集中审计日志镜像（面向管理层查询）
	summary := fmt.Sprintf("%s %s #%d", j.Action, j.ObjectType, j.ObjectID)
	audit := domain.AuditLog{
		ChangeID:           j.ChangeID,
		AccountID:          j.AccountID,
		SourceModule:       j.SourceModule,
		EntityType:         j.ObjectType,
		EntityID:           j.ObjectID,
		Action:             j.Action,
		Summary:            summary,
		ActorType:          j.ActorType,
		ActorID:            j.ActorID,
		CorrelationID:      j.CorrelationID,
		DataClassification: j.DataClassification,
		CreatedAt:          j.CreatedAt,
	}

	return tx.Create(&audit).Error
}

func (r *JournalRepository) ListAuditLogs(accountID uint, entityType string, entityID *uint, page, pageSize int) ([]domain.AuditLog, int64, error) {
	var logs []domain.AuditLog
	var total int64

	query := r.db.Model(&domain.AuditLog{}).Where("account_id = ?", accountID)
	if entityType != "" {
		query = query.Where("entity_type = ?", entityType)
	}
	if entityID != nil {
		query = query.Where("entity_id = ?", *entityID)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Offset(offset).Limit(pageSize).Order("id DESC").Find(&logs).Error
	return logs, total, err
}

func (r *JournalRepository) GetAuditLog(accountID, id uint) (*domain.AuditLog, error) {
	var log domain.AuditLog
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&log).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &log, nil
}

// ListDataChanges 完整数据变更账本查询（LOG-05 /data_changes）
func (r *JournalRepository) ListDataChanges(accountID uint, filter DataChangeFilter) ([]domain.LocalChangeJournal, int64, error) {
	var records []domain.LocalChangeJournal
	var total int64

	query := r.db.Model(&domain.LocalChangeJournal{}).Where("account_id = ?", accountID)
	if filter.ObjectType != "" {
		query = query.Where("object_type = ? OR entity_type = ?", filter.ObjectType, filter.ObjectType)
	}
	if filter.ObjectID != nil {
		query = query.Where("object_id = ? OR entity_id = ?", *filter.ObjectID, *filter.ObjectID)
	}
	if filter.Action != "" {
		query = query.Where("action = ?", filter.Action)
	}
	if filter.ActorType != "" {
		query = query.Where("actor_type = ?", filter.ActorType)
	}
	if filter.ActorID != nil {
		query = query.Where("actor_id = ?", *filter.ActorID)
	}
	if filter.CorrelationID != "" {
		query = query.Where("correlation_id = ?", filter.CorrelationID)
	}
	if filter.SourceModule != "" {
		query = query.Where("source_module = ?", filter.SourceModule)
	}
	if filter.Since != nil {
		query = query.Where("occurred_at >= ?", *filter.Since)
	}
	if filter.Until != nil {
		query = query.Where("occurred_at <= ?", *filter.Until)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if filter.Page < 1 {
		filter.Page = 1
	}
	if filter.PageSize < 1 || filter.PageSize > 100 {
		filter.PageSize = 25
	}
	offset := (filter.Page - 1) * filter.PageSize

	err := query.Offset(offset).Limit(filter.PageSize).Order("id DESC").Find(&records).Error
	return records, total, err
}

func (r *JournalRepository) GetDataChange(accountID uint, changeID string) (*domain.LocalChangeJournal, error) {
	var j domain.LocalChangeJournal
	err := r.db.Where("account_id = ? AND change_id = ?", accountID, changeID).First(&j).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &j, nil
}

func (r *JournalRepository) GetChangeByChangeID(changeID string) (*domain.LocalChangeJournal, error) {
	var j domain.LocalChangeJournal
	err := r.db.Where("change_id = ?", changeID).First(&j).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &j, nil
}

// GetObjectTimeline 查询对象完整变更历史（LOG-05 /audit_objects/:object_type/:object_id/timeline）
func (r *JournalRepository) GetObjectTimeline(accountID uint, objectType string, objectID uint) ([]domain.LocalChangeJournal, error) {
	var records []domain.LocalChangeJournal
	err := r.db.Where("account_id = ? AND (object_type = ? OR entity_type = ?) AND (object_id = ? OR entity_id = ?)",
		accountID, objectType, objectType, objectID, objectID).
		Order("object_version_after ASC, id ASC").
		Find(&records).Error
	return records, err
}

// GetProcessTimeline 查询跨模块业务流程时间线（LOG-05 /audit_processes/:correlation_id/timeline）
func (r *JournalRepository) GetProcessTimeline(accountID uint, correlationID string) ([]domain.LocalChangeJournal, error) {
	var records []domain.LocalChangeJournal
	err := r.db.Where("account_id = ? AND correlation_id = ?", accountID, correlationID).
		Order("occurred_at ASC, id ASC").
		Find(&records).Error
	return records, err
}

// CreateCorrection 追加受控纠正（LOG-02, LOG-04, LOG-05：绝不修改原记录）
func (r *JournalRepository) CreateCorrection(corr *domain.DataChangeCorrection) error {
	if corr.CorrectionChangeID == "" {
		corr.CorrectionChangeID = foundation.GenerateUUID()
	}
	if corr.CreatedAt.IsZero() {
		corr.CreatedAt = time.Now().UTC()
	}
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(corr).Error; err != nil {
			return err
		}
		// 追加一条标准的 correct_log LocalChangeJournal 记录
		journal := &domain.LocalChangeJournal{
			ChangeID:            corr.CorrectionChangeID,
			AccountID:           corr.AccountID,
			SourceModule:        "AUD",
			ObjectType:          "DataChange",
			ObjectID:            corr.ID,
			ObjectDisplayID:     corr.OriginalChangeID,
			Action:              domain.ActionCorrectLog,
			Diff:                corr.CorrectedFields,
			Reason:              corr.Reason,
			ActorType:           "user",
			ActorID:             corr.ActorID,
			CreatedAt:           corr.CreatedAt,
			OccurredAt:          corr.CreatedAt,
			Result:              "corrected",
			DataClassification:  domain.ClassificationInternal,
			RetentionClass:      domain.RetentionSecurityCritical,
		}
		return r.RecordChange(tx, journal)
	})
}

// ListSecurityAuditLogs 查询安全审计日志（LOG-05 /security_audit_logs）
func (r *JournalRepository) ListSecurityAuditLogs(accountID uint, page, pageSize int) ([]domain.SecurityAuditLog, int64, error) {
	var logs []domain.SecurityAuditLog
	var total int64

	query := r.db.Model(&domain.SecurityAuditLog{}).Where("account_id = ?", accountID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Offset(offset).Limit(pageSize).Order("id DESC").Find(&logs).Error
	return logs, total, err
}

// RecordSecurityAudit 记录安全事件
func (r *JournalRepository) RecordSecurityAudit(log *domain.SecurityAuditLog) error {
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	}
	return r.db.Create(log).Error
}

// CreateAuditExport 创建异步导出任务（LOG-05 /audit_exports）
func (r *JournalRepository) CreateAuditExport(export *domain.AuditExport) error {
	if export.ExportID == "" {
		export.ExportID = foundation.GenerateUUID()
	}
	if export.CreatedAt.IsZero() {
		export.CreatedAt = time.Now().UTC()
	}
	if export.ExpiresAt.IsZero() {
		export.ExpiresAt = export.CreatedAt.Add(24 * time.Hour)
	}
	if export.DownloadURL == "" {
		export.DownloadURL = fmt.Sprintf("/api/v1/accounts/%d/audit_exports/%s/download", export.AccountID, export.ExportID)
	}
	return r.db.Create(export).Error
}

func (r *JournalRepository) GetAuditExport(accountID uint, exportID string) (*domain.AuditExport, error) {
	var export domain.AuditExport
	err := r.db.Where("account_id = ? AND export_id = ?", accountID, exportID).First(&export).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &export, nil
}

// CreateIntegrityVerification 创建完整性校验任务（LOG-04, LOG-05, LOG-07）
func (r *JournalRepository) CreateIntegrityVerification(v *domain.IntegrityVerification) error {
	if v.VerificationID == "" {
		v.VerificationID = foundation.GenerateUUID()
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	if v.Status == "" {
		v.Status = "completed"
	}
	return r.db.Create(v).Error
}

func (r *JournalRepository) GetIntegrityVerification(accountID uint, verificationID string) (*domain.IntegrityVerification, error) {
	var v domain.IntegrityVerification
	err := r.db.Where("account_id = ? AND verification_id = ?", accountID, verificationID).First(&v).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &v, nil
}

// RecordAccessLog 记录敏感日志访问（LOG-05 Section 9）
func (r *JournalRepository) RecordAccessLog(accountID, actorID uint, action, targetResource, purpose, ip string) error {
	log := domain.AccessLog{
		AccountID:      accountID,
		ActorID:        actorID,
		Action:         action,
		TargetResource: targetResource,
		Purpose:        purpose,
		IPAddress:      ip,
		CreatedAt:      time.Now().UTC(),
	}
	return r.db.Create(&log).Error
}
