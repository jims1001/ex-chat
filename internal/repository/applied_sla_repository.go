package repository

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// AppliedSLAFilter defines search and filter parameters for Applied SLAs
type AppliedSLAFilter struct {
	Since           *time.Time
	Until           *time.Time
	InboxID         *uint
	TeamID          *uint
	SLAPolicyID     *uint
	AssignedAgentID *uint
	Label           string
	SLAStatus       string // active, hit, missed, active_with_misses
	OnlyMissed      bool   // when true, filters to missed & active_with_misses
	Page            int
	PageSize        int
}

// AppliedSLAMetrics provides hit-rate and breach counts
type AppliedSLAMetrics struct {
	TotalAppliedSLAs  int64  `json:"total_applied_slas"`
	NumberOfSLAMisses int64  `json:"number_of_sla_misses"`
	HitRate           string `json:"hit_rate"`
}

// AppliedSLARepository manages persistent storage for Applied SLAs and SLA Events
type AppliedSLARepository struct {
	db *gorm.DB
}

// NewAppliedSLARepository creates a new AppliedSLARepository
func NewAppliedSLARepository(db *gorm.DB) *AppliedSLARepository {
	return &AppliedSLARepository{db: db}
}

// EnsureAppliedSLA retrieves or creates an AppliedSLA record for a conversation and SLA policy
func (r *AppliedSLARepository) EnsureAppliedSLA(accountID, conversationID, slaPolicyID uint) (*domain.AppliedSLA, error) {
	if accountID == 0 || conversationID == 0 || slaPolicyID == 0 {
		return nil, errors.New("invalid account_id, conversation_id or sla_policy_id")
	}

	var existing domain.AppliedSLA
	err := r.db.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).
		Preload("SLAPolicy").
		Preload("SLAEvents").
		First(&existing).Error

	if err == nil {
		if existing.SLAPolicyID != slaPolicyID {
			existing.SLAPolicyID = slaPolicyID
			existing.UpdatedAt = time.Now().UTC()
			if err := r.db.Save(&existing).Error; err != nil {
				return nil, err
			}
			return r.GetAppliedSLAByConversation(accountID, conversationID)
		}
		return &existing, nil
	}

	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	now := time.Now().UTC()
	applied := domain.AppliedSLA{
		AccountID:      accountID,
		ConversationID: conversationID,
		SLAPolicyID:    slaPolicyID,
		SLAStatus:      "active",
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if err := r.db.Create(&applied).Error; err != nil {
		return nil, err
	}

	return r.GetAppliedSLAByConversation(accountID, conversationID)
}

// GetAppliedSLAByConversation fetches the applied SLA for a conversation with all associations
func (r *AppliedSLARepository) GetAppliedSLAByConversation(accountID, conversationID uint) (*domain.AppliedSLA, error) {
	var applied domain.AppliedSLA
	err := r.db.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).
		Preload("SLAPolicy").
		Preload("SLAEvents", func(db *gorm.DB) *gorm.DB {
			return db.Order("created_at ASC")
		}).
		Preload("Conversation").
		Preload("Conversation.Contact").
		Preload("Conversation.Assignee").
		Preload("Conversation.Team").
		Preload("Conversation.Inbox").
		First(&applied).Error

	if err != nil {
		return nil, err
	}
	return &applied, nil
}

// GetAppliedSLAByID fetches an applied SLA by its ID
func (r *AppliedSLARepository) GetAppliedSLAByID(accountID, id uint) (*domain.AppliedSLA, error) {
	var applied domain.AppliedSLA
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).
		Preload("SLAPolicy").
		Preload("SLAEvents", func(db *gorm.DB) *gorm.DB {
			return db.Order("created_at ASC")
		}).
		Preload("Conversation").
		Preload("Conversation.Contact").
		Preload("Conversation.Assignee").
		Preload("Conversation.Team").
		Preload("Conversation.Inbox").
		First(&applied).Error

	if err != nil {
		return nil, err
	}
	return &applied, nil
}

// RecordSLAEvent idempotently records an SLA breach event
func (r *AppliedSLARepository) RecordSLAEvent(event *domain.SLAEvent) error {
	if event == nil || event.AppliedSLAID == 0 {
		return errors.New("invalid SLA event payload")
	}

	// Idempotency check: avoid duplicate event if same type and meta already exists
	var count int64
	q := r.db.Model(&domain.SLAEvent{}).
		Where("applied_sla_id = ? AND event_type = ?", event.AppliedSLAID, event.EventType)
	if event.Meta != "" {
		q = q.Where("meta = ?", event.Meta)
	}
	_ = q.Count(&count).Error
	if count > 0 {
		return nil
	}

	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	event.UpdatedAt = event.CreatedAt

	return r.db.Create(event).Error
}

// UpdateSLAStatus updates the status on AppliedSLA and synchronizes Conversation.SLAStatus
func (r *AppliedSLARepository) UpdateSLAStatus(accountID, appliedSLAID uint, newStatus string) error {
	if appliedSLAID == 0 || newStatus == "" {
		return errors.New("invalid applied_sla_id or status")
	}

	var applied domain.AppliedSLA
	if err := r.db.Where("account_id = ? AND id = ?", accountID, appliedSLAID).First(&applied).Error; err != nil {
		return err
	}

	applied.SLAStatus = newStatus
	applied.UpdatedAt = time.Now().UTC()
	if err := r.db.Save(&applied).Error; err != nil {
		return err
	}

	// Synchronize Conversation SLAStatus
	_ = r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, applied.ConversationID).
		Update("sla_status", newStatus).Error

	return nil
}

// DeleteByConversation removes an AppliedSLA record and its associated events
func (r *AppliedSLARepository) DeleteByConversation(accountID, conversationID uint) error {
	var applied domain.AppliedSLA
	if err := r.db.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).First(&applied).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	// Remove child SLA events first
	_ = r.db.Where("applied_sla_id = ?", applied.ID).Delete(&domain.SLAEvent{}).Error
	// Delete applied SLA
	if err := r.db.Delete(&applied).Error; err != nil {
		return err
	}

	// Reset Conversation SLA fields
	_ = r.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND id = ?", accountID, conversationID).
		Updates(map[string]any{
			"sla_policy_id":         nil,
			"sla_status":            "active",
			"first_response_due_at": nil,
			"next_response_due_at":  nil,
			"resolution_due_at":     nil,
		}).Error

	return nil
}

// buildFilterQuery constructs the filtered database query for Applied SLAs
func (r *AppliedSLARepository) buildFilterQuery(accountID uint, filter AppliedSLAFilter) *gorm.DB {
	q := r.db.Model(&domain.AppliedSLA{}).Where("applied_slas.account_id = ?", accountID)

	needsConvJoin := filter.InboxID != nil || filter.TeamID != nil || filter.AssignedAgentID != nil || filter.Label != ""
	if needsConvJoin {
		q = q.Joins("JOIN conversations ON conversations.id = applied_slas.conversation_id")
		if filter.InboxID != nil && *filter.InboxID > 0 {
			q = q.Where("conversations.inbox_id = ?", *filter.InboxID)
		}
		if filter.TeamID != nil && *filter.TeamID > 0 {
			q = q.Where("conversations.team_id = ?", *filter.TeamID)
		}
		if filter.AssignedAgentID != nil && *filter.AssignedAgentID > 0 {
			q = q.Where("conversations.assignee_id = ?", *filter.AssignedAgentID)
		}
		if filter.Label != "" {
			q = q.Joins("JOIN conversation_labels ON conversation_labels.conversation_id = conversations.id").
				Joins("JOIN labels ON labels.id = conversation_labels.label_id").
				Where("labels.name = ?", filter.Label)
		}
	}

	if filter.SLAPolicyID != nil && *filter.SLAPolicyID > 0 {
		q = q.Where("applied_slas.sla_policy_id = ?", *filter.SLAPolicyID)
	}

	if filter.SLAStatus != "" {
		q = q.Where("applied_slas.sla_status = ?", filter.SLAStatus)
	} else if filter.OnlyMissed {
		q = q.Where("applied_slas.sla_status IN ('missed', 'active_with_misses')")
	}

	if filter.Since != nil {
		q = q.Where("applied_slas.created_at >= ?", *filter.Since)
	}
	if filter.Until != nil {
		q = q.Where("applied_slas.created_at <= ?", *filter.Until)
	}

	return q
}

// ListAppliedSLAs queries applied SLAs with filtering, pagination and relation preloading
func (r *AppliedSLARepository) ListAppliedSLAs(accountID uint, filter AppliedSLAFilter) ([]domain.AppliedSLA, int64, error) {
	var total int64
	countQ := r.buildFilterQuery(accountID, filter)
	if err := countQ.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page := filter.Page
	if page < 1 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize < 1 || pageSize > 100 {
		pageSize = 25
	}
	offset := (page - 1) * pageSize

	var list []domain.AppliedSLA
	err := r.buildFilterQuery(accountID, filter).
		Preload("SLAPolicy").
		Preload("SLAEvents", func(db *gorm.DB) *gorm.DB {
			return db.Order("created_at ASC")
		}).
		Preload("Conversation").
		Preload("Conversation.Contact").
		Preload("Conversation.Assignee").
		Preload("Conversation.Team").
		Preload("Conversation.Inbox").
		Order("applied_slas.created_at DESC, applied_slas.id DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&list).Error

	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// GetMetrics computes total applied SLAs, missed count and hit rate
func (r *AppliedSLARepository) GetMetrics(accountID uint, filter AppliedSLAFilter) (*AppliedSLAMetrics, error) {
	var total int64
	baseQ := r.buildFilterQuery(accountID, AppliedSLAFilter{
		Since:           filter.Since,
		Until:           filter.Until,
		InboxID:         filter.InboxID,
		TeamID:          filter.TeamID,
		SLAPolicyID:     filter.SLAPolicyID,
		AssignedAgentID: filter.AssignedAgentID,
		Label:           filter.Label,
	})

	if err := baseQ.Count(&total).Error; err != nil {
		return nil, err
	}

	var misses int64
	missesQ := r.buildFilterQuery(accountID, AppliedSLAFilter{
		Since:           filter.Since,
		Until:           filter.Until,
		InboxID:         filter.InboxID,
		TeamID:          filter.TeamID,
		SLAPolicyID:     filter.SLAPolicyID,
		AssignedAgentID: filter.AssignedAgentID,
		Label:           filter.Label,
		OnlyMissed:      true,
	})
	if err := missesQ.Count(&misses).Error; err != nil {
		return nil, err
	}

	hitRate := "100%"
	if total > 0 {
		if misses > 0 {
			rate := math.Round((float64(total-misses)/float64(total))*10000) / 100
			hitRate = fmt.Sprintf("%.2f%%", rate)
			if strings.HasSuffix(hitRate, ".00%") {
				hitRate = strings.TrimSuffix(hitRate, ".00%") + "%"
			}
		}
	} else {
		hitRate = "100%"
	}

	return &AppliedSLAMetrics{
		TotalAppliedSLAs:  total,
		NumberOfSLAMisses: misses,
		HitRate:           hitRate,
	}, nil
}

// GetMissedAppliedSLAs returns all missed SLAs for CSV streaming/download
func (r *AppliedSLARepository) GetMissedAppliedSLAs(accountID uint, filter AppliedSLAFilter) ([]domain.AppliedSLA, error) {
	filter.OnlyMissed = true
	var list []domain.AppliedSLA
	err := r.buildFilterQuery(accountID, filter).
		Preload("SLAPolicy").
		Preload("SLAEvents", func(db *gorm.DB) *gorm.DB {
			return db.Order("created_at ASC")
		}).
		Preload("Conversation").
		Preload("Conversation.Contact").
		Preload("Conversation.Assignee").
		Preload("Conversation.Team").
		Preload("Conversation.Inbox").
		Order("applied_slas.created_at DESC").
		Find(&list).Error

	return list, err
}
