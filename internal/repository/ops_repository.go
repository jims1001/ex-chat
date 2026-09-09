package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"gorm.io/gorm"
)

// MacroAction defines a single action inside a macro
type MacroAction struct {
	ActionName   string `json:"action_name"`
	ActionParams any    `json:"action_params"`
	Name         string `json:"name"`
	Params       any    `json:"params"`
}

func parseMacroActions(rawActions string) []MacroAction {
	raw := strings.TrimSpace(rawActions)
	if raw == "" || raw == "[]" || raw == "{}" {
		return nil
	}

	var actions []MacroAction
	if err := json.Unmarshal([]byte(raw), &actions); err == nil && len(actions) > 0 {
		return actions
	}

	var wrapper struct {
		Actions []MacroAction `json:"actions"`
		Values  []MacroAction `json:"values"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapper); err == nil {
		if len(wrapper.Actions) > 0 {
			return wrapper.Actions
		}
		if len(wrapper.Values) > 0 {
			return wrapper.Values
		}
	}

	var single MacroAction
	if err := json.Unmarshal([]byte(raw), &single); err == nil && (single.ActionName != "" || single.Name != "") {
		return []MacroAction{single}
	}

	return nil
}

func getMacroParamStrings(params any) []string {
	if params == nil {
		return nil
	}
	switch p := params.(type) {
	case []string:
		return p
	case []any:
		var res []string
		for _, item := range p {
			res = append(res, fmt.Sprintf("%v", item))
		}
		return res
	case string:
		return []string{p}
	case map[string]any:
		for _, key := range []string{"message", "content", "text", "label", "priority", "status", "user_id", "agent_id", "team_id", "team_ids", "id"} {
			if v, ok := p[key]; ok {
				return []string{fmt.Sprintf("%v", v)}
			}
		}
		for _, v := range p {
			return []string{fmt.Sprintf("%v", v)}
		}
	}
	return nil
}

// MacroRepository manages Macro templates
type MacroRepository struct {
	db *gorm.DB
}

func NewMacroRepository(db *gorm.DB) *MacroRepository {
	return &MacroRepository{db: db}
}

func (r *MacroRepository) Create(ctx context.Context, macro *domain.Macro) error {
	return r.db.WithContext(ctx).Create(macro).Error
}

func (r *MacroRepository) List(ctx context.Context, accountID uint) ([]domain.Macro, error) {
	var macros []domain.Macro
	err := r.db.WithContext(ctx).Where("account_id = ?", accountID).Find(&macros).Error
	return macros, err
}

func (r *MacroRepository) GetByID(ctx context.Context, accountID, macroID uint) (*domain.Macro, error) {
	var macro domain.Macro
	err := r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, macroID).First(&macro).Error
	if err != nil {
		return nil, err
	}
	return &macro, nil
}

func (r *MacroRepository) Update(ctx context.Context, macro *domain.Macro) error {
	return r.db.WithContext(ctx).Save(macro).Error
}

func (r *MacroRepository) Delete(ctx context.Context, accountID, macroID uint) error {
	return r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, macroID).Delete(&domain.Macro{}).Error
}

// Execute applies a macro's actions sequentially to target conversations
func (r *MacroRepository) Execute(ctx context.Context, accountID, macroID uint, convIDs []uint) (map[uint]string, error) {
	macro, err := r.GetByID(ctx, accountID, macroID)
	if err != nil {
		return nil, fmt.Errorf("macro not found: %w", err)
	}

	actions := parseMacroActions(macro.Actions)
	if len(actions) == 0 {
		return nil, fmt.Errorf("invalid macro actions: no executable actions found")
	}

	results := make(map[uint]string)

	err = r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, convID := range convIDs {
			var conv domain.Conversation
			if err := tx.Where("account_id = ? AND id = ?", accountID, convID).First(&conv).Error; err != nil {
				results[convID] = "conversation not found"
				continue
			}

			// Apply actions
			for _, act := range actions {
				actName := act.ActionName
				if actName == "" {
					actName = act.Name
				}
				params := getMacroParamStrings(act.ActionParams)
				if len(params) == 0 {
					params = getMacroParamStrings(act.Params)
				}

				switch actName {
				case "resolve_conversation", "close_conversation", "close", "resolve":
					conv.Status = domain.ConversationStatusResolved
				case "open_conversation", "open":
					conv.Status = domain.ConversationStatusOpen
				case "snooze_conversation", "snooze":
					conv.Status = domain.ConversationStatusSnoozed
				case "change_status":
					if len(params) > 0 {
						if params[0] == "closed" {
							conv.Status = domain.ConversationStatusResolved
						} else {
							conv.Status = params[0]
						}
					}
				case "assign_agent":
					if len(params) > 0 {
						var agentID uint
						fmt.Sscanf(params[0], "%d", &agentID)
						if agentID > 0 {
							conv.AssigneeID = &agentID
						}
					}
				case "remove_assigned_agent":
					conv.AssigneeID = nil
				case "assign_team":
					if len(params) > 0 {
						var teamID uint
						fmt.Sscanf(params[0], "%d", &teamID)
						if teamID > 0 {
							conv.TeamID = &teamID
						}
					}
				case "remove_assigned_team":
					conv.TeamID = nil
				case "change_priority":
					if len(params) > 0 {
						conv.Priority = params[0]
					}
				case "add_label", "add_labels":
					for _, p := range params {
						p = strings.TrimSpace(p)
						if p == "" {
							continue
						}
						var label domain.Label
						if err := tx.Where("account_id = ? AND title = ?", accountID, p).FirstOrCreate(&label, domain.Label{AccountID: accountID, Title: p}).Error; err == nil {
							var cl domain.ConversationLabel
							tx.Where("conversation_id = ? AND label_id = ?", conv.ID, label.ID).FirstOrCreate(&cl, domain.ConversationLabel{ConversationID: conv.ID, LabelID: label.ID})
						}
					}
				case "remove_label", "remove_labels":
					for _, p := range params {
						p = strings.TrimSpace(p)
						if p == "" {
							continue
						}
						var idNum uint
						_, _ = fmt.Sscanf(p, "%d", &idNum)
						var labels []domain.Label
						if idNum > 0 {
							_ = tx.Where("account_id = ? AND (title = ? OR id = ?)", accountID, p, idNum).Find(&labels).Error
						} else {
							_ = tx.Where("account_id = ? AND title = ?", accountID, p).Find(&labels).Error
						}
						for _, lbl := range labels {
							_ = tx.Where("conversation_id = ? AND label_id = ?", conv.ID, lbl.ID).Delete(&domain.ConversationLabel{}).Error
						}
						if len(labels) == 0 && idNum > 0 {
							_ = tx.Where("conversation_id = ? AND label_id = ?", conv.ID, idNum).Delete(&domain.ConversationLabel{}).Error
						}
					}
				case "send_message", "send_reply", "reply", "add_private_note", "private_note":
					if len(params) > 0 && params[0] != "" {
						isPrivate := actName == "add_private_note" || actName == "private_note"
						msgType := domain.MessageTypeOutgoing
						if isPrivate {
							msgType = domain.MessageTypeActivity
						}
						msg := domain.Message{
							AccountID:      accountID,
							ConversationID: conv.ID,
							SenderType:     domain.SenderTypeUser,
							SenderID:       0,
							MessageType:    msgType,
							ContentType:    domain.ContentTypeText,
							Content:        params[0],
							Status:         domain.MessageStatusSent,
							Private:        isPrivate,
							CreatedAt:      time.Now().UTC(),
							UpdatedAt:      time.Now().UTC(),
						}
						if err := tx.Create(&msg).Error; err == nil {
							logger.WithComponent("macro").Info("macro sent message",
								"account_id", accountID,
								"conversation_id", conv.ID,
								"message_id", msg.ID,
								"private", isPrivate,
							)
						}
					}
				}
			}

			now := time.Now().UTC()
			conv.LastActivityAt = now
			conv.UpdatedAt = now
			conv.ObjectVersion++
			if err := tx.Save(&conv).Error; err != nil {
				results[convID] = fmt.Sprintf("failed: %v", err)
			} else {
				_ = tx.Model(&domain.Conversation{}).Where("id = ?", conv.ID).Updates(map[string]any{
					"last_activity_at": now,
					"updated_at":       now,
					"status":           conv.Status,
					"priority":         conv.Priority,
					"assignee_id":      conv.AssigneeID,
					"team_id":          conv.TeamID,
				}).Error
				results[convID] = "success"
			}
		}
		return nil
	})

	return results, err
}

// NotificationRepository manages agent notifications
type NotificationRepository struct {
	db *gorm.DB
}

func NewNotificationRepository(db *gorm.DB) *NotificationRepository {
	return &NotificationRepository{db: db}
}

func (r *NotificationRepository) Create(ctx context.Context, n *domain.Notification) error {
	return r.db.WithContext(ctx).Create(n).Error
}

func (r *NotificationRepository) FindByID(ctx context.Context, accountID, userID, id uint) (*domain.Notification, error) {
	var n domain.Notification
	err := r.db.WithContext(ctx).
		Where("account_id = ? AND user_id = ? AND id = ?", accountID, userID, id).
		First(&n).Error
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func (r *NotificationRepository) Update(ctx context.Context, n *domain.Notification) error {
	return r.db.WithContext(ctx).Save(n).Error
}

func (r *NotificationRepository) List(ctx context.Context, accountID, userID uint) ([]domain.Notification, error) {
	return r.ListWithFilter(ctx, accountID, userID, "", false)
}

func (r *NotificationRepository) ListWithFilter(ctx context.Context, accountID, userID uint, status string, includeSnoozed bool) ([]domain.Notification, error) {
	var list []domain.Notification
	query := r.db.WithContext(ctx).Where("account_id = ? AND user_id = ?", accountID, userID)
	now := time.Now().UTC()

	switch status {
	case "unread":
		query = query.Where("read_at IS NULL")
		if !includeSnoozed {
			query = query.Where("snoozed_until IS NULL OR snoozed_until <= ?", now)
		}
	case "read":
		query = query.Where("read_at IS NOT NULL")
		if !includeSnoozed {
			query = query.Where("snoozed_until IS NULL OR snoozed_until <= ?", now)
		}
	case "snoozed":
		query = query.Where("snoozed_until IS NOT NULL AND snoozed_until > ?", now)
	default:
		if !includeSnoozed {
			query = query.Where("snoozed_until IS NULL OR snoozed_until <= ?", now)
		}
	}

	err := query.Order("created_at desc").Find(&list).Error
	return list, err
}

func (r *NotificationRepository) MarkAllRead(ctx context.Context, accountID, userID uint) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).
		Model(&domain.Notification{}).
		Where("account_id = ? AND user_id = ? AND read_at IS NULL", accountID, userID).
		Update("read_at", now).Error
}

func (r *NotificationRepository) MarkConversationNotificationsRead(ctx context.Context, accountID, userID, conversationID uint) error {
	now := time.Now().UTC()
	return r.db.WithContext(ctx).
		Model(&domain.Notification{}).
		Where("account_id = ? AND user_id = ? AND primary_actor_type = ? AND primary_actor_id = ? AND read_at IS NULL",
			accountID, userID, "Conversation", conversationID).
		Update("read_at", now).Error
}

func (r *NotificationRepository) MarkRead(ctx context.Context, accountID, userID, id uint) error {
	now := time.Now().UTC()
	res := r.db.WithContext(ctx).
		Model(&domain.Notification{}).
		Where("account_id = ? AND user_id = ? AND id = ?", accountID, userID, id).
		Update("read_at", now)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *NotificationRepository) MarkUnread(ctx context.Context, accountID, userID, id uint) error {
	res := r.db.WithContext(ctx).
		Model(&domain.Notification{}).
		Where("account_id = ? AND user_id = ? AND id = ?", accountID, userID, id).
		Update("read_at", nil)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *NotificationRepository) Snooze(ctx context.Context, accountID, userID, id uint, snoozedUntil *time.Time) error {
	res := r.db.WithContext(ctx).
		Model(&domain.Notification{}).
		Where("account_id = ? AND user_id = ? AND id = ?", accountID, userID, id).
		Update("snoozed_until", snoozedUntil)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *NotificationRepository) Unsnooze(ctx context.Context, accountID, userID, id uint) error {
	res := r.db.WithContext(ctx).
		Model(&domain.Notification{}).
		Where("account_id = ? AND user_id = ? AND id = ?", accountID, userID, id).
		Update("snoozed_until", nil)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *NotificationRepository) Delete(ctx context.Context, accountID, userID, id uint) error {
	res := r.db.WithContext(ctx).
		Where("account_id = ? AND user_id = ? AND id = ?", accountID, userID, id).
		Delete(&domain.Notification{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *NotificationRepository) BatchDelete(ctx context.Context, accountID, userID uint, ids []uint, onlyRead bool) (int64, error) {
	query := r.db.WithContext(ctx).Model(&domain.Notification{}).
		Where("account_id = ? AND user_id = ?", accountID, userID)

	if len(ids) > 0 {
		query = query.Where("id IN (?)", ids)
	}
	if onlyRead {
		query = query.Where("read_at IS NOT NULL")
	}

	res := query.Delete(&domain.Notification{})
	return res.RowsAffected, res.Error
}

func (r *NotificationRepository) GetUnreadCount(ctx context.Context, accountID, userID uint) (unreadCount, totalCount, snoozedCount int64, err error) {
	now := time.Now().UTC()
	err = r.db.WithContext(ctx).Model(&domain.Notification{}).
		Where("account_id = ? AND user_id = ? AND read_at IS NULL AND (snoozed_until IS NULL OR snoozed_until <= ?)", accountID, userID, now).
		Count(&unreadCount).Error
	if err != nil {
		return 0, 0, 0, err
	}

	err = r.db.WithContext(ctx).Model(&domain.Notification{}).
		Where("account_id = ? AND user_id = ? AND snoozed_until IS NOT NULL AND snoozed_until > ?", accountID, userID, now).
		Count(&snoozedCount).Error
	if err != nil {
		return 0, 0, 0, err
	}

	err = r.db.WithContext(ctx).Model(&domain.Notification{}).
		Where("account_id = ? AND user_id = ?", accountID, userID).
		Count(&totalCount).Error
	return unreadCount, totalCount, snoozedCount, err
}

func (r *NotificationRepository) GetNotificationSetting(ctx context.Context, accountID, userID uint) (*domain.NotificationSetting, error) {
	var setting domain.NotificationSetting
	err := r.db.WithContext(ctx).
		Where("account_id = ? AND user_id = ?", accountID, userID).
		First(&setting).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Return default settings
			return &domain.NotificationSetting{
				AccountID:          accountID,
				UserID:             userID,
				SelectedEmailFlags: `["conversation_assignment","conversation_mention"]`,
				SelectedPushFlags:  `["conversation_assignment","conversation_mention","sla_breach"]`,
				SelectedInAppFlags: `["conversation_assignment","conversation_mention","conversation_creation","sla_breach","system_alert"]`,
				Muted:              false,
				QuietHoursEnabled:  false,
				QuietHoursStart:    "22:00",
				QuietHoursEnd:      "08:00",
			}, nil
		}
		return nil, err
	}
	return &setting, nil
}

func (r *NotificationRepository) UpsertNotificationSetting(ctx context.Context, setting *domain.NotificationSetting) error {
	var existing domain.NotificationSetting
	err := r.db.WithContext(ctx).
		Where("account_id = ? AND user_id = ?", setting.AccountID, setting.UserID).
		First(&existing).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.WithContext(ctx).Create(setting).Error
		}
		return err
	}

	existing.SelectedEmailFlags = setting.SelectedEmailFlags
	existing.SelectedPushFlags = setting.SelectedPushFlags
	existing.SelectedInAppFlags = setting.SelectedInAppFlags
	existing.Muted = setting.Muted
	existing.QuietHoursEnabled = setting.QuietHoursEnabled
	existing.QuietHoursStart = setting.QuietHoursStart
	existing.QuietHoursEnd = setting.QuietHoursEnd
	existing.UpdatedAt = time.Now().UTC()

	return r.db.WithContext(ctx).Save(&existing).Error
}

// CSATRepository manages CSAT surveys and satisfaction metrics
type CSATRepository struct {
	db *gorm.DB
}

func NewCSATRepository(db *gorm.DB) *CSATRepository {
	return &CSATRepository{db: db}
}

func (r *CSATRepository) Create(ctx context.Context, survey *domain.CSATSurvey) error {
	return r.db.WithContext(ctx).Create(survey).Error
}

func (r *CSATRepository) GetMetrics(ctx context.Context, accountID uint) (map[string]any, error) {
	var count int64
	var avgRating float64

	r.db.WithContext(ctx).Model(&domain.CSATSurvey{}).Where("account_id = ?", accountID).Count(&count)
	row := r.db.WithContext(ctx).Model(&domain.CSATSurvey{}).Where("account_id = ?", accountID).Select("AVG(rating)").Row()
	_ = row.Scan(&avgRating)

	type RatingDist struct {
		Rating int   `json:"rating"`
		Count  int64 `json:"count"`
	}
	var dist []RatingDist
	r.db.WithContext(ctx).Model(&domain.CSATSurvey{}).
		Select("rating, count(*) as count").
		Where("account_id = ?", accountID).
		Group("rating").
		Scan(&dist)

	return map[string]any{
		"total_responses": count,
		"average_rating":  avgRating,
		"distribution":    dist,
	}, nil
}
