package repository

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// TicketFilter contains filtering criteria for querying tickets
type TicketFilter struct {
	Status         string `form:"status"`
	Priority       string `form:"priority"`
	AssignedGroup  string `form:"assigned_group"`
	AssigneeID     *uint  `form:"assignee_id"`
	ContactID      *uint  `form:"contact_id"`
	ConversationID *uint  `form:"conversation_id"`
	SLAStatus      string `form:"sla_status"`
	Query          string `form:"q"`
	Page           int    `form:"page"`
	Limit          int    `form:"limit"`
}

// TicketRepository handles database operations for tickets, comments and activities
type TicketRepository struct {
	db *gorm.DB
}

func (r *TicketRepository) ValidateReferences(accountID uint, assigneeID, contactID, conversationID, slaPolicyID *uint) error {
	checks := []struct {
		id    *uint
		model any
		where string
	}{
		{assigneeID, &domain.AccountUser{}, "account_id = ? AND user_id = ?"},
		{contactID, &domain.Contact{}, "account_id = ? AND id = ?"},
		{conversationID, &domain.Conversation{}, "account_id = ? AND id = ?"},
		{slaPolicyID, &domain.SLAPolicy{}, "account_id = ? AND id = ?"},
	}
	for _, check := range checks {
		if check.id == nil || *check.id == 0 {
			continue
		}
		var count int64
		if err := r.db.Model(check.model).Where(check.where, accountID, *check.id).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("referenced resource %d does not belong to account", *check.id)
		}
	}
	return nil
}

// NewTicketRepository creates a new instance of TicketRepository
func NewTicketRepository(db *gorm.DB) *TicketRepository {
	return &TicketRepository{db: db}
}

// List queries tickets matching the filter criteria with pagination and SLA status refresh
func (r *TicketRepository) List(accountID uint, filter TicketFilter) ([]domain.Ticket, int64, error) {
	query := r.db.Model(&domain.Ticket{}).Where("account_id = ?", accountID)

	if filter.Status != "" {
		parts := strings.Split(filter.Status, ",")
		if len(parts) == 1 {
			query = query.Where("status = ?", strings.TrimSpace(parts[0]))
		} else {
			var cleaned []string
			for _, p := range parts {
				if tr := strings.TrimSpace(p); tr != "" {
					cleaned = append(cleaned, tr)
				}
			}
			if len(cleaned) > 0 {
				query = query.Where("status IN (?)", cleaned)
			}
		}
	}

	if filter.Priority != "" {
		query = query.Where("priority = ?", filter.Priority)
	}

	if filter.AssignedGroup != "" {
		query = query.Where("assigned_group = ?", filter.AssignedGroup)
	}

	if filter.AssigneeID != nil {
		query = query.Where("assignee_id = ?", *filter.AssigneeID)
	}

	if filter.ContactID != nil {
		query = query.Where("contact_id = ?", *filter.ContactID)
	}

	if filter.ConversationID != nil {
		query = query.Where("conversation_id = ?", *filter.ConversationID)
	}

	if filter.SLAStatus != "" {
		parts := strings.Split(filter.SLAStatus, ",")
		if len(parts) == 1 {
			query = query.Where("sla_status = ?", strings.TrimSpace(parts[0]))
		} else {
			var cleaned []string
			for _, p := range parts {
				if tr := strings.TrimSpace(p); tr != "" {
					cleaned = append(cleaned, tr)
				}
			}
			if len(cleaned) > 0 {
				query = query.Where("sla_status IN (?)", cleaned)
			}
		}
	}

	if filter.Query != "" {
		q := "%" + filter.Query + "%"
		query = query.Where("ticket_number LIKE ? OR title LIKE ? OR description LIKE ? OR custom_attributes LIKE ?", q, q, q, q)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page := filter.Page
	if page < 1 {
		page = 1
	}
	limit := filter.Limit
	if limit < 1 {
		limit = 25
	} else if limit > 100 {
		limit = 100
	}
	offset := (page - 1) * limit

	var tickets []domain.Ticket
	if err := query.Preload("Assignee").
		Preload("Contact").
		Preload("Creator").
		Order("created_at DESC").
		Offset(offset).
		Limit(limit).
		Find(&tickets).Error; err != nil {
		return nil, 0, err
	}

	// Update SLA dynamic breach status for unclosed tickets
	now := time.Now()
	for i := range tickets {
		if (tickets[i].Status == "open" || tickets[i].Status == "pending") && tickets[i].DueAt != nil {
			if now.After(*tickets[i].DueAt) {
				if tickets[i].SLAStatus != "breached" {
					tickets[i].SLAStatus = "breached"
					r.db.Model(&domain.Ticket{}).Where("id = ?", tickets[i].ID).Update("sla_status", "breached")
				}
			} else if tickets[i].DueAt.Sub(now) <= 30*time.Minute {
				if tickets[i].SLAStatus != "warning" && tickets[i].SLAStatus != "breached" {
					tickets[i].SLAStatus = "warning"
					r.db.Model(&domain.Ticket{}).Where("id = ?", tickets[i].ID).Update("sla_status", "warning")
				}
			}
		}
	}

	return tickets, total, nil
}

// FindByID retrieves a single ticket by its numeric ID
func (r *TicketRepository) FindByID(accountID, id uint) (*domain.Ticket, error) {
	var ticket domain.Ticket
	if err := r.db.Where("account_id = ? AND id = ?", accountID, id).
		Preload("Assignee").
		Preload("Contact").
		Preload("Creator").
		Preload("Conversation").
		Preload("Comments", func(db *gorm.DB) *gorm.DB {
			return db.Preload("User").Preload("Contact").Order("created_at ASC")
		}).
		Preload("Activities", func(db *gorm.DB) *gorm.DB {
			return db.Preload("User").Order("created_at ASC")
		}).
		Preload("StatusHistories", func(db *gorm.DB) *gorm.DB {
			return db.Preload("User").Order("created_at ASC")
		}).
		Preload("Attachments", func(db *gorm.DB) *gorm.DB {
			return db.Order("created_at ASC")
		}).
		First(&ticket).Error; err != nil {
		return nil, err
	}
	return &ticket, nil
}

// FindByTicketNumber retrieves a single ticket by its ticket number
func (r *TicketRepository) FindByTicketNumber(accountID uint, ticketNumber string) (*domain.Ticket, error) {
	var ticket domain.Ticket
	if err := r.db.Where("account_id = ? AND ticket_number = ?", accountID, ticketNumber).
		Preload("Assignee").
		Preload("Contact").
		Preload("Creator").
		Preload("Conversation").
		Preload("Comments", func(db *gorm.DB) *gorm.DB {
			return db.Preload("User").Preload("Contact").Order("created_at ASC")
		}).
		Preload("Activities", func(db *gorm.DB) *gorm.DB {
			return db.Preload("User").Order("created_at ASC")
		}).
		Preload("StatusHistories", func(db *gorm.DB) *gorm.DB {
			return db.Preload("User").Order("created_at ASC")
		}).
		Preload("Attachments", func(db *gorm.DB) *gorm.DB {
			return db.Order("created_at ASC")
		}).
		First(&ticket).Error; err != nil {
		return nil, err
	}
	return &ticket, nil
}

// FindByIDOrNumber searches by numeric ID first, then by TicketNumber
func (r *TicketRepository) FindByIDOrNumber(accountID uint, idOrNumber string) (*domain.Ticket, error) {
	if num, err := strconv.ParseUint(idOrNumber, 10, 32); err == nil {
		if ticket, err := r.FindByID(accountID, uint(num)); err == nil && ticket != nil {
			return ticket, nil
		}
	}
	return r.FindByTicketNumber(accountID, idOrNumber)
}

// Create inserts a new ticket, generating a ticket number and default SLA deadline if omitted
func (r *TicketRepository) Create(ticket *domain.Ticket) error {
	if ticket.AccountID == 0 {
		return errors.New("account_id is required")
	}
	if strings.TrimSpace(ticket.Title) == "" {
		return errors.New("title is required")
	}
	if ticket.Status == "" {
		ticket.Status = "open"
	}
	if ticket.Priority == "" {
		ticket.Priority = "high"
	}
	now := time.Now()
	if ticket.CreatedAt.IsZero() {
		ticket.CreatedAt = now
	}
	ticket.UpdatedAt = now

	if ticket.DueAt == nil {
		var deadline time.Time
		switch strings.ToLower(ticket.Priority) {
		case "urgent":
			deadline = now.Add(4 * time.Hour)
		case "high":
			deadline = now.Add(12 * time.Hour)
		case "low":
			deadline = now.Add(48 * time.Hour)
		default:
			deadline = now.Add(24 * time.Hour)
		}
		ticket.DueAt = &deadline
	}

	if ticket.SLAStatus == "" {
		ticket.SLAStatus = "normal"
	}

	providedNumber := ticket.TicketNumber
	const maxRetries = 5

	for attempt := 0; attempt < maxRetries; attempt++ {
		err := r.db.Transaction(func(tx *gorm.DB) error {
			if providedNumber == "" || attempt > 0 {
				todayStr := now.Format("20060102")
				prefix := fmt.Sprintf("TK-%s-", todayStr)
				var lastTicket domain.Ticket
				if err := tx.Where("account_id = ? AND ticket_number LIKE ?", ticket.AccountID, prefix+"%").
					Order("ticket_number DESC").
					Limit(1).
					Find(&lastTicket).Error; err != nil {
					return err
				}
				nextSeq := 1
				if lastTicket.TicketNumber != "" {
					suffix := strings.TrimPrefix(lastTicket.TicketNumber, prefix)
					if num, err := strconv.Atoi(suffix); err == nil {
						nextSeq = num + 1 + attempt
					}
				} else {
					nextSeq = 1 + attempt
				}
				ticket.TicketNumber = fmt.Sprintf("TK-%s-%04d", todayStr, nextSeq)
			}

			if err := tx.Create(ticket).Error; err != nil {
				return err
			}

			// Record initial creation activity
			detailsJSON, _ := json.Marshal(map[string]interface{}{
				"title":          ticket.Title,
				"priority":       ticket.Priority,
				"assigned_group": ticket.AssignedGroup,
				"ticket_number":  ticket.TicketNumber,
			})
			activity := &domain.TicketActivity{
				AccountID: ticket.AccountID,
				TicketID:  ticket.ID,
				UserID:    ticket.CreatorID,
				Action:    "created",
				Details:   string(detailsJSON),
				CreatedAt: now,
			}
			if err := tx.Create(activity).Error; err != nil {
				return err
			}

			// Record initial status history
			statusHistory := &domain.TicketStatusHistory{
				AccountID:     ticket.AccountID,
				TicketID:      ticket.ID,
				FromStatus:    "",
				ToStatus:      ticket.Status,
				Reason:        "工单创建",
				WaitingReason: ticket.WaitingReason,
				ResumeAt:      ticket.ResumeAt,
				AutoCloseAt:   ticket.AutoCloseAt,
				UserID:        ticket.CreatorID,
				CreatedAt:     now,
			}
			return tx.Create(statusHistory).Error
		})

		if err == nil {
			return nil
		}

		if isUniqueConstraintViolation(err) && providedNumber == "" && attempt < maxRetries-1 {
			ticket.ID = 0
			ticket.TicketNumber = ""
			continue
		}
		return err
	}
	return errors.New("failed to generate unique ticket number after retries")
}

// Update saves changes to the ticket with diff activity audit
func (r *TicketRepository) Update(ticket *domain.Ticket) error {
	return r.UpdateWithActor(ticket, nil)
}

// UpdateWithActor saves changes to the ticket and records field diff activity in transaction
func (r *TicketRepository) UpdateWithActor(ticket *domain.Ticket, actorID *uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var oldTicket domain.Ticket
		if err := tx.Where("account_id = ? AND id = ?", ticket.AccountID, ticket.ID).First(&oldTicket).Error; err != nil {
			return err
		}

		diffs := make(map[string]map[string]interface{})
		if oldTicket.Title != ticket.Title {
			diffs["title"] = map[string]interface{}{"old": oldTicket.Title, "new": ticket.Title}
		}
		if oldTicket.Priority != ticket.Priority {
			diffs["priority"] = map[string]interface{}{"old": oldTicket.Priority, "new": ticket.Priority}
		}
		if oldTicket.Status != ticket.Status {
			diffs["status"] = map[string]interface{}{"old": oldTicket.Status, "new": ticket.Status}
		}
		if oldTicket.AssignedGroup != ticket.AssignedGroup {
			diffs["assigned_group"] = map[string]interface{}{"old": oldTicket.AssignedGroup, "new": ticket.AssignedGroup}
		}
		oldAssignee := uint(0)
		if oldTicket.AssigneeID != nil {
			oldAssignee = *oldTicket.AssigneeID
		}
		newAssignee := uint(0)
		if ticket.AssigneeID != nil {
			newAssignee = *ticket.AssigneeID
		}
		if oldAssignee != newAssignee {
			diffs["assignee_id"] = map[string]interface{}{"old": oldTicket.AssigneeID, "new": ticket.AssigneeID}
		}
		if oldTicket.DueAt != nil && ticket.DueAt != nil {
			if !oldTicket.DueAt.Equal(*ticket.DueAt) {
				diffs["due_at"] = map[string]interface{}{"old": oldTicket.DueAt, "new": ticket.DueAt}
			}
		} else if (oldTicket.DueAt == nil) != (ticket.DueAt == nil) {
			diffs["due_at"] = map[string]interface{}{"old": oldTicket.DueAt, "new": ticket.DueAt}
		}
		if oldTicket.Description != ticket.Description {
			diffs["description"] = map[string]interface{}{"old": oldTicket.Description, "new": ticket.Description}
		}
		if oldTicket.WaitingReason != ticket.WaitingReason {
			diffs["waiting_reason"] = map[string]interface{}{"old": oldTicket.WaitingReason, "new": ticket.WaitingReason}
		}

		ticket.UpdatedAt = time.Now()
		if err := tx.Save(ticket).Error; err != nil {
			return err
		}

		now := time.Now()
		if len(diffs) > 0 {
			detailsJSON, _ := json.Marshal(diffs)
			activity := &domain.TicketActivity{
				AccountID: ticket.AccountID,
				TicketID:  ticket.ID,
				UserID:    actorID,
				Action:    "updated",
				Details:   string(detailsJSON),
				CreatedAt: now,
			}
			if err := tx.Create(activity).Error; err != nil {
				return err
			}

			actorUID := uint(0)
			if actorID != nil {
				actorUID = *actorID
			}
			_ = NewJournalRepository(tx).RecordChange(tx, &domain.LocalChangeJournal{
				AccountID:     ticket.AccountID,
				EntityType:    "Ticket",
				EntityID:      ticket.ID,
				ObjectType:    "Ticket",
				ObjectID:      ticket.ID,
				Action:        "ticket_update",
				ActorType:     "User",
				ActorID:       actorUID,
				ChangedFields: string(detailsJSON),
				Result:        "applied",
				OccurredAt:    now,
			})
		}

		// If status changed, record status history as well
		if oldTicket.Status != ticket.Status {
			statusHistory := &domain.TicketStatusHistory{
				AccountID:     ticket.AccountID,
				TicketID:      ticket.ID,
				FromStatus:    oldTicket.Status,
				ToStatus:      ticket.Status,
				Reason:        "工单更新",
				WaitingReason: ticket.WaitingReason,
				ResumeAt:      ticket.ResumeAt,
				AutoCloseAt:   ticket.AutoCloseAt,
				UserID:        actorID,
				CreatedAt:     now,
			}
			if err := tx.Create(statusHistory).Error; err != nil {
				return err
			}
		}

		return nil
	})
}

// Delete removes a ticket and its associated attachments, comments, activities & status histories
func (r *TicketRepository) Delete(accountID, id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("account_id = ? AND ticket_id = ?", accountID, id).Delete(&domain.TicketAttachment{}).Error; err != nil {
			return err
		}
		if err := tx.Where("account_id = ? AND ticket_id = ?", accountID, id).Delete(&domain.TicketComment{}).Error; err != nil {
			return err
		}
		if err := tx.Where("account_id = ? AND ticket_id = ?", accountID, id).Delete(&domain.TicketActivity{}).Error; err != nil {
			return err
		}
		if err := tx.Where("account_id = ? AND ticket_id = ?", accountID, id).Delete(&domain.TicketStatusHistory{}).Error; err != nil {
			return err
		}
		return tx.Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.Ticket{}).Error
	})
}

// AddComment adds a comment/note to a ticket and creates an activity entry
func (r *TicketRepository) AddComment(comment *domain.TicketComment) error {
	if comment.AccountID == 0 || comment.TicketID == 0 {
		return errors.New("account_id and ticket_id are required")
	}
	if strings.TrimSpace(comment.Content) == "" {
		return errors.New("comment content cannot be empty")
	}
	now := time.Now()
	if comment.CreatedAt.IsZero() {
		comment.CreatedAt = now
	}
	comment.UpdatedAt = now

	if err := r.db.Create(comment).Error; err != nil {
		return err
	}

	detailsJSON, _ := json.Marshal(map[string]interface{}{
		"comment_id": comment.ID,
		"is_private": comment.IsPrivate,
		"preview":    comment.Content,
	})
	activity := &domain.TicketActivity{
		AccountID: comment.AccountID,
		TicketID:  comment.TicketID,
		UserID:    comment.UserID,
		Action:    "commented",
		Details:   string(detailsJSON),
		CreatedAt: now,
	}
	_ = r.db.Create(activity)

	return nil
}

// ListComments retrieves all comments for a ticket in chronological order
func (r *TicketRepository) ListComments(accountID, ticketID uint) ([]domain.TicketComment, error) {
	var comments []domain.TicketComment
	if err := r.db.Where("account_id = ? AND ticket_id = ?", accountID, ticketID).
		Preload("User").
		Preload("Contact").
		Order("created_at ASC").
		Find(&comments).Error; err != nil {
		return nil, err
	}
	return comments, nil
}

// AddActivity appends an event to the ticket's activity timeline
func (r *TicketRepository) AddActivity(activity *domain.TicketActivity) error {
	if activity.AccountID == 0 || activity.TicketID == 0 {
		return errors.New("account_id and ticket_id are required")
	}
	if activity.CreatedAt.IsZero() {
		activity.CreatedAt = time.Now()
	}
	return r.db.Create(activity).Error
}

// ListActivities retrieves all timeline activities for a ticket
func (r *TicketRepository) ListActivities(accountID, ticketID uint) ([]domain.TicketActivity, error) {
	var activities []domain.TicketActivity
	if err := r.db.Where("account_id = ? AND ticket_id = ?", accountID, ticketID).
		Preload("User").
		Order("created_at ASC").
		Find(&activities).Error; err != nil {
		return nil, err
	}
	return activities, nil
}

// GetStats calculates summary metrics for tickets in the given account
func (r *TicketRepository) GetStats(accountID uint) (map[string]int64, error) {
	stats := map[string]int64{
		"open":     0,
		"pending":  0,
		"urgent":   0,
		"resolved": 0,
		"closed":   0,
		"total":    0,
	}

	var rows []struct {
		Status string
		Count  int64
	}
	if err := r.db.Model(&domain.Ticket{}).
		Select("status, count(*) as count").
		Where("account_id = ?", accountID).
		Group("status").
		Scan(&rows).Error; err != nil {
		return nil, err
	}

	for _, row := range rows {
		stats[row.Status] = row.Count
		stats["total"] += row.Count
	}

	// Calculate breached / warning as urgent
	var urgentCount int64
	r.db.Model(&domain.Ticket{}).
		Where("account_id = ? AND status IN ('open', 'pending') AND (sla_status IN ('warning', 'breached') OR priority = 'urgent')", accountID).
		Count(&urgentCount)
	stats["urgent"] = urgentCount

	return stats, nil
}

// TransitionParams defines inputs for a ticket status transition
type TransitionParams struct {
	ToStatus      string
	Reason        string
	WaitingReason string
	ResumeAt      *time.Time
	AutoCloseAt   *time.Time
	UserID        *uint
	Comment       string
}

// TransitionStatus performs a validated status transition and creates audit records
func (r *TicketRepository) TransitionStatus(accountID uint, ticket *domain.Ticket, params TransitionParams) error {
	now := time.Now()
	fromStatus := ticket.Status
	toStatus := strings.ToLower(strings.TrimSpace(params.ToStatus))
	if toStatus == "" {
		toStatus = fromStatus
	}

	ticket.Status = toStatus
	if params.Reason != "" {
		ticket.StatusReason = params.Reason
	}
	if params.WaitingReason != "" {
		ticket.WaitingReason = params.WaitingReason
	}
	if params.ResumeAt != nil {
		ticket.ResumeAt = params.ResumeAt
	}
	if params.AutoCloseAt != nil {
		ticket.AutoCloseAt = params.AutoCloseAt
	}

	if toStatus == "resolved" {
		ticket.ResolvedAt = &now
		if ticket.DueAt != nil {
			if now.Before(*ticket.DueAt) || now.Equal(*ticket.DueAt) {
				ticket.SLAStatus = "achieved"
			} else {
				ticket.SLAStatus = "breached"
			}
		}
	} else if toStatus == "closed" {
		ticket.ClosedAt = &now
	}

	ticket.UpdatedAt = now

	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(ticket).Error; err != nil {
			return err
		}

		// Record formal status history
		history := &domain.TicketStatusHistory{
			AccountID:     accountID,
			TicketID:      ticket.ID,
			FromStatus:    fromStatus,
			ToStatus:      toStatus,
			Reason:        params.Reason,
			WaitingReason: ticket.WaitingReason,
			ResumeAt:      ticket.ResumeAt,
			AutoCloseAt:   ticket.AutoCloseAt,
			UserID:        params.UserID,
			CreatedAt:     now,
		}
		if err := tx.Create(history).Error; err != nil {
			return err
		}

		// Record activity timeline
		detailsMap := map[string]interface{}{
			"from_status":    fromStatus,
			"to_status":      toStatus,
			"reason":         params.Reason,
			"waiting_reason": ticket.WaitingReason,
		}
		if ticket.ResumeAt != nil {
			detailsMap["resume_at"] = ticket.ResumeAt
		}
		if ticket.AutoCloseAt != nil {
			detailsMap["auto_close_at"] = ticket.AutoCloseAt
		}
		detailsJSON, _ := json.Marshal(detailsMap)

		action := "status_changed"
		if toStatus == "pending" {
			action = "waiting"
		} else if fromStatus == "pending" && toStatus == "open" {
			action = "resumed"
		}

		activity := &domain.TicketActivity{
			AccountID: accountID,
			TicketID:  ticket.ID,
			UserID:    params.UserID,
			Action:    action,
			Details:   string(detailsJSON),
			CreatedAt: now,
		}
		if err := tx.Create(activity).Error; err != nil {
			return err
		}

		if strings.TrimSpace(params.Comment) != "" {
			comment := &domain.TicketComment{
				AccountID: accountID,
				TicketID:  ticket.ID,
				UserID:    params.UserID,
				Content:   strings.TrimSpace(params.Comment),
				IsPrivate: true,
				CreatedAt: now,
				UpdatedAt: now,
			}
			if err := tx.Create(comment).Error; err != nil {
				return err
			}
		}

		return nil
	})
}

// Wait puts a ticket into waiting/pending status with waiting reason, optional resume_at & auto_close_at
func (r *TicketRepository) Wait(accountID, ticketID uint, userID *uint, waitingReason string, resumeAt, autoCloseAt *time.Time, reason, comment string) (*domain.Ticket, error) {
	ticket, err := r.FindByID(accountID, ticketID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(waitingReason) == "" {
		waitingReason = "等待客户回复"
	}
	if strings.TrimSpace(reason) == "" {
		reason = waitingReason
	}
	err = r.TransitionStatus(accountID, ticket, TransitionParams{
		ToStatus:      "pending",
		Reason:        reason,
		WaitingReason: waitingReason,
		ResumeAt:      resumeAt,
		AutoCloseAt:   autoCloseAt,
		UserID:        userID,
		Comment:       comment,
	})
	if err != nil {
		return nil, err
	}
	return ticket, nil
}

// Resume wakes up a pending ticket, moving it back to open status
func (r *TicketRepository) Resume(accountID, ticketID uint, userID *uint, reason, comment string) (*domain.Ticket, error) {
	ticket, err := r.FindByID(accountID, ticketID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(reason) == "" {
		reason = "客户已回复或客服唤醒，恢复工单处理"
	}
	err = r.TransitionStatus(accountID, ticket, TransitionParams{
		ToStatus: "open",
		Reason:   reason,
		UserID:   userID,
		Comment:  comment,
	})
	if err != nil {
		return nil, err
	}
	return ticket, nil
}

// Remind sends a reminder regarding the ticket, incrementing reminder count and recording last_reminded_at
func (r *TicketRepository) Remind(accountID, ticketID uint, userID *uint, message string) (*domain.Ticket, error) {
	ticket, err := r.FindByID(accountID, ticketID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	ticket.ReminderCount++
	ticket.LastRemindedAt = &now
	ticket.UpdatedAt = now

	err = r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.Ticket{}).Where("account_id = ? AND id = ?", accountID, ticket.ID).
			Updates(map[string]interface{}{
				"reminder_count":   ticket.ReminderCount,
				"last_reminded_at": ticket.LastRemindedAt,
				"updated_at":       now,
			}).Error; err != nil {
			return err
		}

		commentContent := message
		if strings.TrimSpace(commentContent) == "" {
			commentContent = fmt.Sprintf("【系统催办提醒】已向客户发送催办提醒（第 %d 次）。", ticket.ReminderCount)
		}

		comment := &domain.TicketComment{
			AccountID: accountID,
			TicketID:  ticket.ID,
			UserID:    userID,
			Content:   commentContent,
			IsPrivate: true,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := tx.Create(comment).Error; err != nil {
			return err
		}

		detailsJSON, _ := json.Marshal(map[string]interface{}{
			"reminder_count": ticket.ReminderCount,
			"reminded_at":    now,
			"message":        commentContent,
		})
		activity := &domain.TicketActivity{
			AccountID: accountID,
			TicketID:  ticket.ID,
			UserID:    userID,
			Action:    "reminded",
			Details:   string(detailsJSON),
			CreatedAt: now,
		}
		return tx.Create(activity).Error
	})
	if err != nil {
		return nil, err
	}
	return ticket, nil
}

// BatchRemind sends reminders to specified pending tickets or all pending tickets in the account
func (r *TicketRepository) BatchRemind(accountID uint, ticketIDs []uint, userID *uint, message string) ([]uint, error) {
	var ids []uint
	if len(ticketIDs) > 0 {
		ids = ticketIDs
	} else {
		var tickets []domain.Ticket
		if err := r.db.Where("account_id = ? AND status = ?", accountID, "pending").Find(&tickets).Error; err != nil {
			return nil, err
		}
		for _, t := range tickets {
			ids = append(ids, t.ID)
		}
	}

	var successful []uint
	for _, id := range ids {
		if _, err := r.Remind(accountID, id, userID, message); err == nil {
			successful = append(successful, id)
		}
	}
	return successful, nil
}

// ListStatusHistory retrieves the formal status audit history for a ticket
func (r *TicketRepository) ListStatusHistory(accountID, ticketID uint) ([]domain.TicketStatusHistory, error) {
	var histories []domain.TicketStatusHistory
	if err := r.db.Where("account_id = ? AND ticket_id = ?", accountID, ticketID).
		Preload("User").
		Order("created_at ASC").
		Find(&histories).Error; err != nil {
		return nil, err
	}
	return histories, nil
}

const (
	MaxAttachmentFileSize = 20 * 1024 * 1024 // 20 MB
	MaxDataURLLength      = 30 * 1024 * 1024 // ~30 MB Base64
)

var dangerousAttachmentExtensions = map[string]bool{
	".exe": true, ".bat": true, ".sh": true, ".cmd": true,
	".msi": true, ".vbs": true, ".ps1": true, ".scr": true,
	".com": true, ".pif": true, ".application": true, ".gadget": true,
}

func isUniqueConstraintViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate") || strings.Contains(msg, "1062") || strings.Contains(msg, "23505")
}

// CreateAttachment saves an attachment for a ticket and writes an activity record
func (r *TicketRepository) CreateAttachment(accountID, ticketID uint, fileName, fileType string, fileSize int64, dataURL string, userID *uint) (*domain.TicketAttachment, error) {
	trimmedFileName := strings.TrimSpace(fileName)
	if trimmedFileName == "" {
		return nil, errors.New("file_name is required")
	}

	ext := strings.ToLower(filepath.Ext(trimmedFileName))
	if dangerousAttachmentExtensions[ext] {
		return nil, fmt.Errorf("file extension '%s' is not allowed for security reasons", ext)
	}

	if len(dataURL) > MaxDataURLLength {
		return nil, errors.New("attachment payload exceeds maximum permitted length (30MB)")
	}

	if fileSize > MaxAttachmentFileSize {
		return nil, errors.New("attachment file size exceeds 20MB limit")
	}

	if fileSize <= 0 && dataURL != "" {
		fileSize = int64(len(dataURL) * 3 / 4) // estimate decoded base64 size
		if fileSize > MaxAttachmentFileSize {
			return nil, errors.New("attachment file size exceeds 20MB limit")
		}
	}

	var ticket domain.Ticket
	if err := r.db.Where("account_id = ? AND id = ?", accountID, ticketID).First(&ticket).Error; err != nil {
		return nil, err
	}

	attachment := &domain.TicketAttachment{
		AccountID: accountID,
		TicketID:  ticketID,
		FileName:  trimmedFileName,
		FileType:  fileType,
		FileSize:  fileSize,
		DataURL:   dataURL,
		CreatedAt: time.Now(),
	}

	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(attachment).Error; err != nil {
			return err
		}
		detailsJSON, _ := json.Marshal(map[string]interface{}{
			"attachment_id": attachment.ID,
			"file_name":     attachment.FileName,
			"file_size":     attachment.FileSize,
		})
		activity := &domain.TicketActivity{
			AccountID: accountID,
			TicketID:  ticketID,
			UserID:    userID,
			Action:    "attachment_added",
			Details:   string(detailsJSON),
			CreatedAt: time.Now(),
		}
		return tx.Create(activity).Error
	})
	if err != nil {
		return nil, err
	}
	return attachment, nil
}

// ListAttachments returns all attachments for a ticket
func (r *TicketRepository) ListAttachments(accountID, ticketID uint) ([]domain.TicketAttachment, error) {
	var attachments []domain.TicketAttachment
	if err := r.db.Where("account_id = ? AND ticket_id = ?", accountID, ticketID).
		Order("created_at ASC").
		Find(&attachments).Error; err != nil {
		return nil, err
	}
	return attachments, nil
}

// DeleteAttachment removes an attachment from a ticket
func (r *TicketRepository) DeleteAttachment(accountID, ticketID, attachmentID uint, userID *uint) error {
	var attachment domain.TicketAttachment
	if err := r.db.Where("account_id = ? AND ticket_id = ? AND id = ?", accountID, ticketID, attachmentID).First(&attachment).Error; err != nil {
		return err
	}

	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Delete(&attachment).Error; err != nil {
			return err
		}
		detailsJSON, _ := json.Marshal(map[string]interface{}{
			"attachment_id": attachment.ID,
			"file_name":     attachment.FileName,
		})
		activity := &domain.TicketActivity{
			AccountID: accountID,
			TicketID:  ticketID,
			UserID:    userID,
			Action:    "attachment_removed",
			Details:   string(detailsJSON),
			CreatedAt: time.Now(),
		}
		return tx.Create(activity).Error
	})
}

// ListByConversation retrieves all tickets linked to a conversation
func (r *TicketRepository) ListByConversation(accountID, conversationID uint) ([]domain.Ticket, error) {
	var tickets []domain.Ticket
	if err := r.db.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).
		Preload("Assignee").
		Preload("Contact").
		Preload("Creator").
		Preload("Attachments").
		Order("created_at DESC").
		Find(&tickets).Error; err != nil {
		return nil, err
	}
	return tickets, nil
}

// ListByContact retrieves all tickets linked to a contact
func (r *TicketRepository) ListByContact(accountID, contactID uint) ([]domain.Ticket, error) {
	var tickets []domain.Ticket
	if err := r.db.Where("account_id = ? AND contact_id = ?", accountID, contactID).
		Preload("Assignee").
		Preload("Contact").
		Preload("Creator").
		Preload("Conversation").
		Preload("Attachments").
		Order("created_at DESC").
		Find(&tickets).Error; err != nil {
		return nil, err
	}
	return tickets, nil
}

// Search queries tickets by text across ticket_number, title, description, and contact info
func (r *TicketRepository) Search(accountID uint, queryText string, limit int) ([]domain.Ticket, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	q := "%" + strings.TrimSpace(queryText) + "%"
	var tickets []domain.Ticket
	if err := r.db.Where("account_id = ?", accountID).
		Where("ticket_number LIKE ? OR title LIKE ? OR description LIKE ? OR custom_attributes LIKE ?", q, q, q, q).
		Preload("Assignee").
		Preload("Contact").
		Preload("Creator").
		Preload("Conversation").
		Preload("Attachments").
		Order("created_at DESC").
		Limit(limit).
		Find(&tickets).Error; err != nil {
		return nil, err
	}
	return tickets, nil
}

var ErrOptimisticLockConflict = errors.New("optimistic lock conflict: ticket has been modified by another user")

// UpdateWithVersion saves changes with optimistic concurrency version check
func (r *TicketRepository) UpdateWithVersion(ticket *domain.Ticket, currentVersion int, actorID *uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		var oldTicket domain.Ticket
		if err := tx.Where("account_id = ? AND id = ?", ticket.AccountID, ticket.ID).First(&oldTicket).Error; err != nil {
			return err
		}

		if currentVersion > 0 && oldTicket.Version != currentVersion {
			return ErrOptimisticLockConflict
		}

		ticket.Version = oldTicket.Version + 1
		ticket.UpdatedAt = time.Now()

		res := tx.Model(&domain.Ticket{}).
			Where("id = ? AND account_id = ? AND version = ?", ticket.ID, ticket.AccountID, oldTicket.Version).
			Updates(map[string]interface{}{
				"title":             ticket.Title,
				"description":       ticket.Description,
				"priority":          ticket.Priority,
				"status":            ticket.Status,
				"assigned_group":    ticket.AssignedGroup,
				"assignee_id":       ticket.AssigneeID,
				"contact_id":        ticket.ContactID,
				"conversation_id":   ticket.ConversationID,
				"sla_policy_id":     ticket.SLAPolicyID,
				"due_at":            ticket.DueAt,
				"waiting_reason":    ticket.WaitingReason,
				"resume_at":         ticket.ResumeAt,
				"auto_close_at":     ticket.AutoCloseAt,
				"custom_attributes": ticket.CustomAttributes,
				"version":           ticket.Version,
				"updated_at":        ticket.UpdatedAt,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrOptimisticLockConflict
		}

		// Record activity diff
		now := time.Now()
		detailsJSON, _ := json.Marshal(map[string]interface{}{
			"version_from": oldTicket.Version,
			"version_to":   ticket.Version,
			"status":       ticket.Status,
			"priority":     ticket.Priority,
		})
		activity := &domain.TicketActivity{
			AccountID: ticket.AccountID,
			TicketID:  ticket.ID,
			UserID:    actorID,
			Action:    "updated_versioned",
			Details:   string(detailsJSON),
			CreatedAt: now,
		}
		return tx.Create(activity).Error
	})
}

// Watcher management
func (r *TicketRepository) AddWatcher(accountID, ticketID, userID uint) error {
	if accountID == 0 || ticketID == 0 || userID == 0 {
		return errors.New("account_id, ticket_id and user_id are required")
	}
	var ticketCount, memberCount int64
	if err := r.db.Model(&domain.Ticket{}).Where("account_id = ? AND id = ?", accountID, ticketID).Count(&ticketCount).Error; err != nil {
		return err
	}
	if ticketCount == 0 {
		return gorm.ErrRecordNotFound
	}
	if err := r.db.Model(&domain.AccountUser{}).Where("account_id = ? AND user_id = ?", accountID, userID).Count(&memberCount).Error; err != nil {
		return err
	}
	if memberCount == 0 {
		return errors.New("watcher does not belong to account")
	}
	var watcher domain.TicketWatcher
	return r.db.Where(domain.TicketWatcher{AccountID: accountID, TicketID: ticketID, UserID: userID}).
		FirstOrCreate(&watcher, domain.TicketWatcher{
			AccountID: accountID,
			TicketID:  ticketID,
			UserID:    userID,
			CreatedAt: time.Now().UTC(),
		}).Error
}

func (r *TicketRepository) RemoveWatcher(accountID, ticketID, userID uint) error {
	return r.db.Where("account_id = ? AND ticket_id = ? AND user_id = ?", accountID, ticketID, userID).
		Delete(&domain.TicketWatcher{}).Error
}

func (r *TicketRepository) ListWatchers(accountID, ticketID uint) ([]domain.TicketWatcher, error) {
	var watchers []domain.TicketWatcher
	err := r.db.Where("account_id = ? AND ticket_id = ?", accountID, ticketID).
		Preload("User").
		Find(&watchers).Error
	return watchers, err
}

// BulkUpdate updates multiple tickets in a transaction
func (r *TicketRepository) BulkUpdate(accountID uint, ids []uint, updates map[string]interface{}, actorID *uint) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	updates["updated_at"] = time.Now().UTC()

	var count int64
	err := r.db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&domain.Ticket{}).Where("account_id = ? AND id IN (?)", accountID, ids).Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		count = res.RowsAffected

		now := time.Now().UTC()
		detailsJSON, _ := json.Marshal(updates)
		for _, id := range ids {
			act := domain.TicketActivity{
				AccountID: accountID,
				TicketID:  id,
				UserID:    actorID,
				Action:    "bulk_updated",
				Details:   string(detailsJSON),
				CreatedAt: now,
			}
			_ = tx.Create(&act).Error
		}
		return nil
	})
	return count, err
}

// ProcessAutoCloseAndResume scans and applies auto-close and resume-from-waiting rules
func (r *TicketRepository) ProcessAutoCloseAndResume(now time.Time) (closedCount int64, resumedCount int64, err error) {
	// 1. Auto-close: tickets where status = resolved and auto_close_at <= now
	var autoCloseTickets []domain.Ticket
	if err := r.db.Where("status = 'resolved' AND auto_close_at IS NOT NULL AND auto_close_at <= ?", now).Find(&autoCloseTickets).Error; err == nil {
		for _, t := range autoCloseTickets {
			closeTime := now
			t.Status = "closed"
			t.ClosedAt = &closeTime
			t.StatusReason = "超时自动关闭"
			t.UpdatedAt = now
			t.Version++
			if r.db.Save(&t).Error == nil {
				closedCount++
				actJSON, _ := json.Marshal(map[string]interface{}{"status": "closed", "reason": "auto_close_timeout"})
				_ = r.db.Create(&domain.TicketActivity{
					AccountID: t.AccountID,
					TicketID:  t.ID,
					Action:    "auto_closed",
					Details:   string(actJSON),
					CreatedAt: now,
				}).Error
			}
		}
	}

	// 2. Resume waiting tickets: status = waiting/pending and resume_at <= now
	var resumeTickets []domain.Ticket
	if err := r.db.Where("status IN ('pending', 'waiting') AND resume_at IS NOT NULL AND resume_at <= ?", now).Find(&resumeTickets).Error; err == nil {
		for _, t := range resumeTickets {
			t.Status = "open"
			t.StatusReason = "等待期满自动恢复处理"
			t.UpdatedAt = now
			t.Version++
			if r.db.Save(&t).Error == nil {
				resumedCount++
				actJSON, _ := json.Marshal(map[string]interface{}{"status": "open", "reason": "resume_timeout_reached"})
				_ = r.db.Create(&domain.TicketActivity{
					AccountID: t.AccountID,
					TicketID:  t.ID,
					Action:    "auto_resumed",
					Details:   string(actJSON),
					CreatedAt: now,
				}).Error
			}
		}
	}

	return closedCount, resumedCount, nil
}
