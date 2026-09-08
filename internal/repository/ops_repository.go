package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
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
		for _, key := range []string{"message", "content", "text", "label", "priority", "status", "user_id", "agent_id"} {
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
				case "assign_agent", "assign_team":
					if len(params) > 0 {
						var agentID uint
						fmt.Sscanf(params[0], "%d", &agentID)
						if agentID > 0 {
							conv.AssigneeID = &agentID
						}
					}
				case "remove_assigned_agent":
					conv.AssigneeID = nil
				case "change_priority":
					if len(params) > 0 {
						conv.Priority = params[0]
					}
				case "add_label":
					if len(params) > 0 {
						var label domain.Label
						if err := tx.Where("account_id = ? AND title = ?", accountID, params[0]).FirstOrCreate(&label, domain.Label{AccountID: accountID, Title: params[0]}).Error; err == nil {
							var cl domain.ConversationLabel
							tx.Where("conversation_id = ? AND label_id = ?", conv.ID, label.ID).FirstOrCreate(&cl, domain.ConversationLabel{ConversationID: conv.ID, LabelID: label.ID})
						}
					}
				case "remove_label":
					if len(params) > 0 {
						var label domain.Label
						if err := tx.Where("account_id = ? AND title = ?", accountID, params[0]).First(&label).Error; err == nil {
							_ = tx.Where("conversation_id = ? AND label_id = ?", conv.ID, label.ID).Delete(&domain.ConversationLabel{}).Error
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
						_ = tx.Create(&msg)
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

func (r *NotificationRepository) List(ctx context.Context, accountID, userID uint) ([]domain.Notification, error) {
	var list []domain.Notification
	err := r.db.WithContext(ctx).
		Where("account_id = ? AND user_id = ?", accountID, userID).
		Order("created_at desc").
		Find(&list).Error
	return list, err
}

func (r *NotificationRepository) MarkAllRead(ctx context.Context, accountID, userID uint) error {
	now := time.Now()
	return r.db.WithContext(ctx).
		Model(&domain.Notification{}).
		Where("account_id = ? AND user_id = ? AND read_at IS NULL", accountID, userID).
		Update("read_at", now).Error
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
