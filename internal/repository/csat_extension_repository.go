package repository

import (
	"errors"
	"math"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// CSATFilter specifies query criteria for CSAT surveys
type CSATFilter struct {
	Rating          *int
	ReviewStatus    string
	AssignedAgentID *uint
	InboxID         *uint
	Since           *time.Time
	Until           *time.Time
	Search          string
	Page            int
	PageSize        int
}

// CSATAgentMetric summarizes CSAT performance for an agent
type CSATAgentMetric struct {
	AgentID           uint    `json:"agent_id"`
	AgentName         string  `json:"agent_name"`
	TotalResponses    int64   `json:"total_responses"`
	ApprovedResponses int64   `json:"approved_responses"`
	AverageRating     float64 `json:"average_rating"`
	SatisfactionRate  float64 `json:"satisfaction_rate"`
}

// CSATMetrics provides complete statistical breakdown of CSAT results
type CSATMetrics struct {
	TotalResponses        int64             `json:"total_responses"`
	ApprovedResponses     int64             `json:"approved_responses"`
	PendingReview         int64             `json:"pending_review"`
	RejectedResponses     int64             `json:"rejected_responses"`
	AverageRating         float64           `json:"average_rating"`
	SatisfactionRate      float64           `json:"satisfaction_rate"` // (Rating 4+5) / Total * 100%
	RatingBreakdown       map[int]int64     `json:"rating_breakdown"`
	ReviewStatusBreakdown map[string]int64  `json:"review_status_breakdown"`
	AgentMetrics          []CSATAgentMetric `json:"agent_metrics,omitempty"`
}

// CSATExtensionRepository manages advanced queries and audit operations for CSAT surveys
type CSATExtensionRepository struct {
	db            *gorm.DB
	conversations csatConversationPort
	contacts      csatContactPort
	inboxes       csatInboxPort
	users         csatUserPort
}

type csatConversationPort interface {
	FindSummaryByID(accountID, conversationID uint) (*domain.Conversation, error)
	FindIDsByInbox(accountID, inboxID uint) ([]uint, error)
}

type csatContactPort interface {
	FindSummaryByID(accountID, id uint) (*domain.Contact, error)
}

type csatInboxPort interface {
	FindSummaryByID(accountID, id uint) (*domain.Inbox, error)
}

type csatUserPort interface {
	FindByID(id uint) (*domain.User, error)
}

// NewCSATExtensionRepository creates a new repository instance
func NewCSATExtensionRepository(db *gorm.DB) *CSATExtensionRepository {
	return NewCSATExtensionRepositoryWithOwners(db, NewConversationRepository(db), NewContactRepository(db), NewInboxRepository(db), NewUserRepository(db))
}

func NewCSATExtensionRepositoryWithOwners(db *gorm.DB, conversations csatConversationPort, contacts csatContactPort, inboxes csatInboxPort, users csatUserPort) *CSATExtensionRepository {
	return &CSATExtensionRepository{db: db, conversations: conversations, contacts: contacts, inboxes: inboxes, users: users}
}

func (r *CSATExtensionRepository) SavePublicSurvey(survey *domain.CSATSurvey) error {
	return r.db.Save(survey).Error
}

func (r *CSATExtensionRepository) FindGlobalSurvey(id uint) (*domain.CSATSurvey, error) {
	var survey domain.CSATSurvey
	if err := r.db.First(&survey, id).Error; err != nil {
		return nil, err
	}
	return &survey, nil
}

func (r *CSATExtensionRepository) FindSurveyByConversation(accountID, conversationID uint) (*domain.CSATSurvey, error) {
	var survey domain.CSATSurvey
	if err := r.db.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).First(&survey).Error; err != nil {
		return nil, err
	}
	return &survey, nil
}

// ListSurveys queries CSAT surveys with filtering and pagination
func (r *CSATExtensionRepository) ListSurveys(accountID uint, filter CSATFilter) ([]domain.CSATSurvey, int64, error) {
	var surveys []domain.CSATSurvey
	var total int64

	query := r.db.Model(&domain.CSATSurvey{}).Where("csat_surveys.account_id = ?", accountID)

	if filter.Rating != nil && *filter.Rating > 0 {
		query = query.Where("csat_surveys.rating = ?", *filter.Rating)
	}

	if filter.ReviewStatus != "" {
		query = query.Where("LOWER(csat_surveys.review_status) = ?", strings.ToLower(filter.ReviewStatus))
	}

	if filter.AssignedAgentID != nil && *filter.AssignedAgentID > 0 {
		query = query.Where("csat_surveys.assigned_agent_id = ?", *filter.AssignedAgentID)
	}

	if filter.InboxID != nil && *filter.InboxID > 0 {
		conversationIDs, err := r.conversations.FindIDsByInbox(accountID, *filter.InboxID)
		if err != nil {
			return nil, 0, err
		}
		query = query.Where("csat_surveys.conversation_id IN ?", conversationIDs)
	}

	if filter.Since != nil {
		query = query.Where("csat_surveys.created_at >= ?", *filter.Since)
	}

	if filter.Until != nil {
		query = query.Where("csat_surveys.created_at <= ?", *filter.Until)
	}

	if filter.Search != "" {
		s := "%" + strings.ToLower(filter.Search) + "%"
		query = query.Where("LOWER(csat_surveys.feedback_text) LIKE ?", s)
	}

	if err := query.Count(&total).Error; err != nil {
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

	err := query.Offset(offset).
		Limit(pageSize).
		Order("csat_surveys.id DESC").
		Find(&surveys).Error
	if err != nil {
		return nil, 0, err
	}
	for i := range surveys {
		if err := r.hydrateOwnerReferences(&surveys[i]); err != nil {
			return nil, 0, err
		}
	}
	return surveys, total, nil
}

// GetSurvey retrieves a single CSAT survey response by ID
func (r *CSATExtensionRepository) GetSurvey(accountID, id uint) (*domain.CSATSurvey, error) {
	var survey domain.CSATSurvey
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).
		First(&survey).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if err := r.hydrateOwnerReferences(&survey); err != nil {
		return nil, err
	}
	return &survey, nil
}

func (r *CSATExtensionRepository) hydrateOwnerReferences(survey *domain.CSATSurvey) error {
	if r.conversations == nil || r.contacts == nil || r.inboxes == nil || r.users == nil {
		return gorm.ErrInvalidData
	}
	conversation, err := r.conversations.FindSummaryByID(survey.AccountID, survey.ConversationID)
	if err != nil {
		return err
	}
	conversation.Contact, err = r.contacts.FindSummaryByID(survey.AccountID, conversation.ContactID)
	if err != nil {
		return err
	}
	conversation.Inbox, err = r.inboxes.FindSummaryByID(survey.AccountID, conversation.InboxID)
	if err != nil {
		return err
	}
	survey.Conversation = conversation
	if survey.AssignedAgentID != nil {
		survey.AssignedAgent, err = r.users.FindByID(*survey.AssignedAgentID)
		if err != nil {
			return err
		}
	}
	if survey.ReviewerID != nil {
		survey.Reviewer, err = r.users.FindByID(*survey.ReviewerID)
		if err != nil {
			return err
		}
	}
	return nil
}

// ReviewSurvey sets audit moderation status on a survey response
func (r *CSATExtensionRepository) ReviewSurvey(accountID, id, reviewerID uint, status, notes string) (*domain.CSATSurvey, error) {
	survey, err := r.GetSurvey(accountID, id)
	if err != nil || survey == nil {
		return nil, err
	}

	validStatus := strings.ToLower(strings.TrimSpace(status))
	if validStatus == "" {
		validStatus = "approved"
	}

	now := time.Now().UTC()
	survey.ReviewStatus = validStatus
	survey.ReviewNotes = notes
	survey.ReviewerID = &reviewerID
	survey.ReviewedAt = &now
	survey.UpdatedAt = now

	if err := r.db.Save(survey).Error; err != nil {
		return nil, err
	}

	return r.GetSurvey(accountID, id)
}

// BulkReviewSurveys reviews multiple CSAT surveys at once
func (r *CSATExtensionRepository) BulkReviewSurveys(accountID, reviewerID uint, ids []uint, status, notes string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	validStatus := strings.ToLower(strings.TrimSpace(status))
	if validStatus == "" {
		validStatus = "approved"
	}

	now := time.Now().UTC()
	updates := map[string]any{
		"review_status": validStatus,
		"review_notes":  notes,
		"reviewer_id":   reviewerID,
		"reviewed_at":   now,
		"updated_at":    now,
	}

	res := r.db.Model(&domain.CSATSurvey{}).
		Where("account_id = ? AND id IN ?", accountID, ids).
		Updates(updates)

	return res.RowsAffected, res.Error
}

// TriggerSurveyForConversation initiates or fetches a CSAT survey for a conversation
func (r *CSATExtensionRepository) TriggerSurveyForConversation(accountID, conversationID uint, assignedAgentID *uint) (*domain.CSATSurvey, error) {
	var existing domain.CSATSurvey
	err := r.db.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).First(&existing).Error
	if err == nil {
		return r.GetSurvey(accountID, existing.ID)
	}

	// Create pending survey record
	survey := domain.CSATSurvey{
		AccountID:       accountID,
		ConversationID:  conversationID,
		Rating:          0, // Pending customer submission
		AssignedAgentID: assignedAgentID,
		ReviewStatus:    "pending",
	}

	if err := r.db.Create(&survey).Error; err != nil {
		return nil, err
	}

	return r.GetSurvey(accountID, survey.ID)
}

// SubmitOrUpdateSurvey saves a customer response (from public or authenticated endpoints)
func (r *CSATExtensionRepository) SubmitOrUpdateSurvey(accountID, conversationID uint, assignedAgentID *uint, rating int, feedback string) (*domain.CSATSurvey, error) {
	var survey domain.CSATSurvey
	err := r.db.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).First(&survey).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	if errors.Is(err, gorm.ErrRecordNotFound) {
		survey = domain.CSATSurvey{
			AccountID:       accountID,
			ConversationID:  conversationID,
			Rating:          rating,
			FeedbackText:    feedback,
			AssignedAgentID: assignedAgentID,
			ReviewStatus:    "pending",
		}
		if err := r.db.Create(&survey).Error; err != nil {
			return nil, err
		}
	} else {
		survey.Rating = rating
		survey.FeedbackText = feedback
		if survey.AssignedAgentID == nil {
			survey.AssignedAgentID = assignedAgentID
		}
		survey.UpdatedAt = time.Now().UTC()
		if err := r.db.Save(&survey).Error; err != nil {
			return nil, err
		}
	}

	return r.GetSurvey(accountID, survey.ID)
}

// DeleteSurvey deletes a survey record
func (r *CSATExtensionRepository) DeleteSurvey(accountID, id uint) error {
	res := r.db.Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.CSATSurvey{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// GetAdvancedMetrics aggregates complete CSAT satisfaction scores, breakdowns and agent ranking
func (r *CSATExtensionRepository) GetAdvancedMetrics(accountID uint, inboxID, agentID *uint, since, until *time.Time) (*CSATMetrics, error) {
	var inboxConversationIDs []uint
	if inboxID != nil && *inboxID > 0 {
		var err error
		inboxConversationIDs, err = r.conversations.FindIDsByInbox(accountID, *inboxID)
		if err != nil {
			return nil, err
		}
	}
	baseQuery := func() *gorm.DB {
		q := r.db.Model(&domain.CSATSurvey{}).Where("csat_surveys.account_id = ? AND csat_surveys.rating > 0", accountID)
		if inboxID != nil && *inboxID > 0 {
			q = q.Where("csat_surveys.conversation_id IN ?", inboxConversationIDs)
		}
		if agentID != nil && *agentID > 0 {
			q = q.Where("csat_surveys.assigned_agent_id = ?", *agentID)
		}
		if since != nil {
			q = q.Where("csat_surveys.created_at >= ?", *since)
		}
		if until != nil {
			q = q.Where("csat_surveys.created_at <= ?", *until)
		}
		return q
	}

	metrics := &CSATMetrics{
		RatingBreakdown: map[int]int64{1: 0, 2: 0, 3: 0, 4: 0, 5: 0},
	}

	_ = baseQuery().Count(&metrics.TotalResponses).Error
	_ = baseQuery().Where("review_status = ?", "approved").Count(&metrics.ApprovedResponses).Error
	_ = baseQuery().Where("review_status = ?", "pending").Count(&metrics.PendingReview).Error
	_ = baseQuery().Where("review_status = ?", "rejected").Count(&metrics.RejectedResponses).Error

	metrics.ReviewStatusBreakdown = map[string]int64{
		"approved": metrics.ApprovedResponses,
		"pending":  metrics.PendingReview,
		"rejected": metrics.RejectedResponses,
	}

	if metrics.TotalResponses > 0 {
		var avg float64
		row := baseQuery().Select("AVG(rating)").Row()
		_ = row.Scan(&avg)
		metrics.AverageRating = math.Round(avg*100) / 100

		var positiveCount int64
		_ = baseQuery().Where("rating >= 4").Count(&positiveCount).Error
		metrics.SatisfactionRate = math.Round((float64(positiveCount)/float64(metrics.TotalResponses))*10000) / 100
	}

	type DistRow struct {
		Rating int   `gorm:"column:rating"`
		Count  int64 `gorm:"column:count"`
	}
	var distRows []DistRow
	_ = baseQuery().Select("rating, COUNT(*) as count").Group("rating").Scan(&distRows).Error
	for _, dr := range distRows {
		metrics.RatingBreakdown[dr.Rating] = dr.Count
	}

	// Agent metrics breakdown
	type AgentRow struct {
		AgentID   uint    `gorm:"column:assigned_agent_id"`
		Total     int64   `gorm:"column:total"`
		Approved  int64   `gorm:"column:approved"`
		AvgRating float64 `gorm:"column:avg_rating"`
		PosCount  int64   `gorm:"column:pos_count"`
	}
	var agentRows []AgentRow
	err := baseQuery().
		Select("csat_surveys.assigned_agent_id, COUNT(*) AS total, SUM(CASE WHEN csat_surveys.review_status = 'approved' THEN 1 ELSE 0 END) AS approved, AVG(csat_surveys.rating) AS avg_rating, SUM(CASE WHEN csat_surveys.rating >= 4 THEN 1 ELSE 0 END) AS pos_count").
		Where("csat_surveys.assigned_agent_id IS NOT NULL").
		Group("csat_surveys.assigned_agent_id").
		Scan(&agentRows).Error

	if err == nil {
		for _, ar := range agentRows {
			agent, ownerErr := r.users.FindByID(ar.AgentID)
			if ownerErr != nil {
				return nil, ownerErr
			}
			satRate := 0.0
			if ar.Total > 0 {
				satRate = math.Round((float64(ar.PosCount)/float64(ar.Total))*10000) / 100
			}
			metrics.AgentMetrics = append(metrics.AgentMetrics, CSATAgentMetric{
				AgentID:           ar.AgentID,
				AgentName:         agent.Name,
				TotalResponses:    ar.Total,
				ApprovedResponses: ar.Approved,
				AverageRating:     math.Round(ar.AvgRating*100) / 100,
				SatisfactionRate:  satRate,
			})
		}
	}

	return metrics, nil
}
