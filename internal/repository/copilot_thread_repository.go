package repository

import (
	"errors"
	"fmt"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"gorm.io/gorm"
)

// CopilotThreadFilter defines criteria for querying Copilot threads
type CopilotThreadFilter struct {
	ConversationID *uint
	AssistantID    *uint
	UserID         *uint
	Status         string
	Query          string
	Page           int
	PageSize       int
}

// CopilotThreadMetrics provides aggregated metrics on Copilot thread usage
type CopilotThreadMetrics struct {
	TotalThreads    int64   `json:"total_threads"`
	ActiveThreads   int64   `json:"active_threads"`
	ArchivedThreads int64   `json:"archived_threads"`
	TotalMessages   int64   `json:"total_messages"`
	AverageTurns    float64 `json:"average_turns"`
	TotalTokens     int64   `json:"total_tokens"`
	ThumbsUpCount   int64   `json:"thumbs_up_count"`
	ThumbsDownCount int64   `json:"thumbs_down_count"`
	PositiveRate    string  `json:"positive_rate"`
}

// CopilotThreadRepository manages database persistence for Copilot threads and messages
type CopilotThreadRepository struct {
	db *gorm.DB
}

// NewCopilotThreadRepository creates a new instance of CopilotThreadRepository
func NewCopilotThreadRepository(db *gorm.DB) *CopilotThreadRepository {
	return &CopilotThreadRepository{db: db}
}

// CreateThread stores a new CopilotThread record
func (r *CopilotThreadRepository) CreateThread(thread *domain.CopilotThread) error {
	if thread.AccountID == 0 {
		return errors.New("account_id is required")
	}
	if thread.Status == "" {
		thread.Status = domain.CopilotThreadStatusActive
	}
	now := time.Now().UTC()
	thread.CreatedAt = now
	thread.UpdatedAt = now
	return r.db.Create(thread).Error
}

// GetThread retrieves a single CopilotThread with associations
func (r *CopilotThreadRepository) GetThread(accountID, threadID uint) (*domain.CopilotThread, error) {
	var thread domain.CopilotThread
	err := r.db.Where("account_id = ? AND id = ?", accountID, threadID).
		Preload("User").
		Preload("Conversation").
		Preload("Assistant").
		First(&thread).Error
	if err != nil {
		return nil, err
	}
	return &thread, nil
}

// UpdateThread updates fields of a CopilotThread
func (r *CopilotThreadRepository) UpdateThread(thread *domain.CopilotThread) error {
	thread.UpdatedAt = time.Now().UTC()
	return r.db.Model(&domain.CopilotThread{}).
		Where("account_id = ? AND id = ?", thread.AccountID, thread.ID).
		Updates(map[string]any{
			"title":      thread.Title,
			"status":     thread.Status,
			"context":    thread.Context,
			"metadata":   thread.Metadata,
			"updated_at": thread.UpdatedAt,
		}).Error
}

// DeleteThread cascades deletions for a thread and its messages
func (r *CopilotThreadRepository) DeleteThread(accountID, threadID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Verify thread exists
		var thread domain.CopilotThread
		if err := tx.Where("account_id = ? AND id = ?", accountID, threadID).First(&thread).Error; err != nil {
			return err
		}

		// Delete messages
		if err := tx.Where("account_id = ? AND thread_id = ?", accountID, threadID).
			Delete(&domain.CopilotThreadMessage{}).Error; err != nil {
			return err
		}

		// Delete thread
		return tx.Delete(&thread).Error
	})
}

// ListThreads returns a paginated list of threads matching the given filter
func (r *CopilotThreadRepository) ListThreads(accountID uint, filter CopilotThreadFilter) ([]domain.CopilotThread, int64, error) {
	var threads []domain.CopilotThread
	var total int64

	q := r.db.Model(&domain.CopilotThread{}).Where("account_id = ?", accountID)

	if filter.ConversationID != nil {
		q = q.Where("conversation_id = ?", *filter.ConversationID)
	}
	if filter.AssistantID != nil {
		q = q.Where("assistant_id = ?", *filter.AssistantID)
	}
	if filter.UserID != nil {
		q = q.Where("user_id = ?", *filter.UserID)
	}
	if filter.Status != "" {
		q = q.Where("status = ?", filter.Status)
	}
	if filter.Query != "" {
		like := "%" + filter.Query + "%"
		q = q.Where("title LIKE ? OR context LIKE ?", like, like)
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page := filter.Page
	if page < 1 {
		page = 1
	}
	pageSize := filter.PageSize
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	err := q.Preload("User").
		Preload("Assistant").
		Preload("Conversation").
		Order("COALESCE(last_message_at, updated_at) DESC").
		Limit(pageSize).
		Offset(offset).
		Find(&threads).Error

	return threads, total, err
}

// AddMessage appends a message to a thread and updates thread aggregate stats
func (r *CopilotThreadRepository) AddMessage(msg *domain.CopilotThreadMessage) error {
	if msg.AccountID == 0 || msg.ThreadID == 0 {
		return errors.New("account_id and thread_id are required")
	}

	now := time.Now().UTC()
	msg.CreatedAt = now
	msg.UpdatedAt = now

	return r.db.Transaction(func(tx *gorm.DB) error {
		// 1. Insert message
		if err := tx.Create(msg).Error; err != nil {
			return err
		}

		// 2. Update thread stats
		updates := map[string]any{
			"message_count":   gorm.Expr("message_count + ?", 1),
			"total_tokens":    gorm.Expr("total_tokens + ?", msg.TokenCount),
			"last_message_at": now,
			"updated_at":      now,
		}

		return tx.Model(&domain.CopilotThread{}).
			Where("account_id = ? AND id = ?", msg.AccountID, msg.ThreadID).
			Updates(updates).Error
	})
}

// GetMessage retrieves a single message by ID
func (r *CopilotThreadRepository) GetMessage(accountID, threadID, messageID uint) (*domain.CopilotThreadMessage, error) {
	var msg domain.CopilotThreadMessage
	err := r.db.Where("account_id = ? AND thread_id = ? AND id = ?", accountID, threadID, messageID).First(&msg).Error
	if err != nil {
		return nil, err
	}
	return &msg, nil
}

// ListMessages retrieves messages for a thread with pagination and sorting
func (r *CopilotThreadRepository) ListMessages(accountID, threadID uint, page, pageSize int, orderAsc bool) ([]domain.CopilotThreadMessage, int64, error) {
	var messages []domain.CopilotThreadMessage
	var total int64

	q := r.db.Model(&domain.CopilotThreadMessage{}).
		Where("account_id = ? AND thread_id = ?", accountID, threadID)

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 30
	}
	offset := (page - 1) * pageSize

	orderStr := "id DESC"
	if orderAsc {
		orderStr = "id ASC"
	}

	err := q.Order(orderStr).
		Limit(pageSize).
		Offset(offset).
		Find(&messages).Error

	return messages, total, err
}

// GetRecentThreadContext gets the last N messages of a thread in chronological order
func (r *CopilotThreadRepository) GetRecentThreadContext(accountID, threadID uint, limit int) ([]domain.CopilotThreadMessage, error) {
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	var revMessages []domain.CopilotThreadMessage
	err := r.db.Where("account_id = ? AND thread_id = ?", accountID, threadID).
		Order("id DESC").
		Limit(limit).
		Find(&revMessages).Error
	if err != nil {
		return nil, err
	}

	// Reverse to chronological order (oldest first)
	messages := make([]domain.CopilotThreadMessage, len(revMessages))
	for i, m := range revMessages {
		messages[len(revMessages)-1-i] = m
	}
	return messages, nil
}

// DeleteMessage deletes a single message and decrements the thread's message count
func (r *CopilotThreadRepository) DeleteMessage(accountID, threadID, messageID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var msg domain.CopilotThreadMessage
		if err := tx.Where("account_id = ? AND thread_id = ? AND id = ?", accountID, threadID, messageID).First(&msg).Error; err != nil {
			return err
		}

		if err := tx.Delete(&msg).Error; err != nil {
			return err
		}

		return tx.Model(&domain.CopilotThread{}).
			Where("account_id = ? AND id = ? AND message_count > 0", accountID, threadID).
			Update("message_count", gorm.Expr("message_count - 1")).Error
	})
}

// ClearMessages deletes all messages for a thread and resets message stats
func (r *CopilotThreadRepository) ClearMessages(accountID, threadID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("account_id = ? AND thread_id = ?", accountID, threadID).
			Delete(&domain.CopilotThreadMessage{}).Error; err != nil {
			return err
		}

		return tx.Model(&domain.CopilotThread{}).
			Where("account_id = ? AND id = ?", accountID, threadID).
			Updates(map[string]any{
				"message_count":   0,
				"total_tokens":    0,
				"last_message_at": nil,
				"updated_at":      time.Now().UTC(),
			}).Error
	})
}

// UpdateFeedback updates thumbs up/down feedback and notes on a message
func (r *CopilotThreadRepository) UpdateFeedback(accountID, threadID, messageID uint, feedback, notes string) error {
	return r.db.Model(&domain.CopilotThreadMessage{}).
		Where("account_id = ? AND thread_id = ? AND id = ?", accountID, threadID, messageID).
		Updates(map[string]any{
			"feedback":       feedback,
			"feedback_notes": notes,
			"updated_at":     time.Now().UTC(),
		}).Error
}

// GetThreadMetrics computes aggregate statistics across all Copilot threads in the account
func (r *CopilotThreadRepository) GetThreadMetrics(accountID uint) (*CopilotThreadMetrics, error) {
	metrics := &CopilotThreadMetrics{
		PositiveRate: "100%",
	}

	// 1. Thread count stats
	var totalThreads int64
	_ = r.db.Model(&domain.CopilotThread{}).Where("account_id = ?", accountID).Count(&totalThreads)
	metrics.TotalThreads = totalThreads

	var activeThreads int64
	_ = r.db.Model(&domain.CopilotThread{}).Where("account_id = ? AND status = ?", accountID, domain.CopilotThreadStatusActive).Count(&activeThreads)
	metrics.ActiveThreads = activeThreads

	var archivedThreads int64
	_ = r.db.Model(&domain.CopilotThread{}).Where("account_id = ? AND status = ?", accountID, domain.CopilotThreadStatusArchived).Count(&archivedThreads)
	metrics.ArchivedThreads = archivedThreads

	// 2. Token and message count stats
	type sumResult struct {
		TotalTokens   int64
		TotalMessages int64
	}
	var res sumResult
	_ = r.db.Model(&domain.CopilotThread{}).
		Where("account_id = ?", accountID).
		Select("COALESCE(SUM(total_tokens), 0) as total_tokens, COALESCE(SUM(message_count), 0) as total_messages").
		Scan(&res)

	metrics.TotalTokens = res.TotalTokens
	metrics.TotalMessages = res.TotalMessages

	if metrics.TotalThreads > 0 {
		metrics.AverageTurns = float64(metrics.TotalMessages) / float64(metrics.TotalThreads)
	}

	// 3. Feedback counts
	var thumbsUp, thumbsDown int64
	_ = r.db.Model(&domain.CopilotThreadMessage{}).
		Where("account_id = ? AND feedback = ?", accountID, domain.CopilotFeedbackThumbsUp).
		Count(&thumbsUp)
	_ = r.db.Model(&domain.CopilotThreadMessage{}).
		Where("account_id = ? AND feedback = ?", accountID, domain.CopilotFeedbackThumbsDown).
		Count(&thumbsDown)

	metrics.ThumbsUpCount = thumbsUp
	metrics.ThumbsDownCount = thumbsDown

	totalFeedbacks := thumbsUp + thumbsDown
	if totalFeedbacks > 0 {
		rate := float64(thumbsUp) / float64(totalFeedbacks) * 100.0
		metrics.PositiveRate = fmt.Sprintf("%.1f%%", rate)
	}

	logger.WithComponent("copilot").Info("copilot thread metrics calculated",
		"account_id", accountID,
		"total_threads", metrics.TotalThreads,
		"total_messages", metrics.TotalMessages,
		"positive_rate", metrics.PositiveRate,
	)

	return metrics, nil
}
