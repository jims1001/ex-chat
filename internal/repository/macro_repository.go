package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"gorm.io/gorm"
)

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
		res := make([]string, 0, len(p))
		for _, item := range p {
			res = append(res, fmt.Sprintf("%v", item))
		}
		return res
	case string:
		return []string{p}
	case map[string]any:
		for _, key := range []string{"message", "content", "text", "label", "priority", "status", "user_id", "agent_id", "team_id", "team_ids", "id", "file_url"} {
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

type MacroRepository struct {
	db            *gorm.DB
	conversations *ConversationRepository
	accounts      *AccountRepository
	teams         *TeamRepository
	labels        *LabelRepository
	messages      *MessageRepository
}

func NewMacroRepository(db *gorm.DB) *MacroRepository {
	conversations := NewConversationRepository(db)
	return NewMacroRepositoryWithOwnerPorts(db, conversations, NewAccountRepository(db), NewTeamRepository(db), NewLabelRepositoryWithConversationPort(db, conversations), NewMessageRepositoryWithConversationPort(db, conversations))
}

func NewMacroRepositoryWithOwnerPorts(db *gorm.DB, conversations *ConversationRepository, accounts *AccountRepository, teams *TeamRepository, labels *LabelRepository, messages *MessageRepository) *MacroRepository {
	return &MacroRepository{db: db, conversations: conversations, accounts: accounts, teams: teams, labels: labels, messages: messages}
}

func (r *MacroRepository) Create(ctx context.Context, macro *domain.Macro) error {
	return r.db.WithContext(ctx).Create(macro).Error
}
func (r *MacroRepository) List(ctx context.Context, accountID uint) ([]domain.Macro, error) {
	return r.ListForUser(ctx, accountID, 0, true)
}
func (r *MacroRepository) ListForUser(ctx context.Context, accountID, userID uint, isAdmin bool) ([]domain.Macro, error) {
	var macros []domain.Macro
	query := r.db.WithContext(ctx).Where("account_id = ?", accountID)
	if !isAdmin && userID > 0 {
		query = query.Where("visibility = 'global' OR (visibility = 'personal' AND created_by = ?)", userID)
	}
	err := query.Order("id ASC").Find(&macros).Error
	return macros, err
}
func (r *MacroRepository) GetByID(ctx context.Context, accountID, macroID uint) (*domain.Macro, error) {
	var macro domain.Macro
	if err := r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, macroID).First(&macro).Error; err != nil {
		return nil, err
	}
	return &macro, nil
}
func (r *MacroRepository) GetByIDForUser(ctx context.Context, accountID, macroID, userID uint, isAdmin bool) (*domain.Macro, error) {
	macro, err := r.GetByID(ctx, accountID, macroID)
	if err != nil {
		return nil, err
	}
	if !isAdmin && macro.Visibility == "personal" && macro.CreatedBy != userID {
		return nil, gorm.ErrRecordNotFound
	}
	return macro, nil
}
func (r *MacroRepository) Update(ctx context.Context, macro *domain.Macro) error {
	return r.db.WithContext(ctx).Save(macro).Error
}
func (r *MacroRepository) Delete(ctx context.Context, accountID, macroID uint) error {
	return r.db.WithContext(ctx).Where("account_id = ? AND id = ?", accountID, macroID).Delete(&domain.Macro{}).Error
}
func (r *MacroRepository) Execute(ctx context.Context, accountID, macroID uint, convIDs []uint) (map[uint]string, error) {
	return r.ExecuteForUser(ctx, accountID, macroID, 0, true, convIDs)
}

func (r *MacroRepository) ExecuteForUser(ctx context.Context, accountID, macroID, userID uint, isAdmin bool, convIDs []uint) (map[uint]string, error) {
	macro, err := r.GetByIDForUser(ctx, accountID, macroID, userID, isAdmin)
	if err != nil {
		return nil, fmt.Errorf("macro not found or not accessible: %w", err)
	}
	actions := parseMacroActions(macro.Actions)
	if len(actions) == 0 {
		return nil, fmt.Errorf("invalid macro actions: no executable actions found")
	}
	if err := validateMacroActionPlan(actions); err != nil {
		return nil, err
	}
	if r.conversations == nil || r.accounts == nil || r.teams == nil || r.labels == nil || r.messages == nil {
		return nil, gorm.ErrInvalidData
	}
	results := make(map[uint]string, len(convIDs))
	for _, convID := range convIDs {
		exists, checkErr := r.conversations.MessageTargetExists(accountID, convID)
		if checkErr != nil || !exists {
			results[convID] = "conversation not found"
			continue
		}
		if applyErr := r.applyActions(ctx, accountID, convID, userID, actions); applyErr != nil {
			results[convID] = fmt.Sprintf("failed: %v", applyErr)
		} else {
			results[convID] = "success"
		}
	}
	return results, nil
}

// Validate the entire parsed plan before any owner reads or effects. External
// action names remain configurable but have no implemented execution path.
func validateMacroActionPlan(actions []MacroAction) error {
	for _, action := range actions {
		name := action.ActionName
		if name == "" {
			name = action.Name
		}
		switch name {
		case "resolve_conversation", "close_conversation", "close", "resolve",
			"open_conversation", "open", "snooze_conversation", "snooze",
			"mute_conversation", "change_status", "assign_agent", "remove_assigned_agent",
			"assign_team", "remove_assigned_team", "change_priority", "add_label", "add_labels",
			"remove_label", "remove_labels", "send_message", "send_reply", "reply",
			"add_private_note", "private_note", "send_attachment":
		default:
			return fmt.Errorf("unsupported macro action %q", name)
		}
	}
	return nil
}

func (r *MacroRepository) applyActions(ctx context.Context, accountID, convID, userID uint, actions []MacroAction) error {
	mutation := MacroConversationMutation{}
	for _, act := range actions {
		name := act.ActionName
		if name == "" {
			name = act.Name
		}
		params := getMacroParamStrings(act.ActionParams)
		if len(params) == 0 {
			params = getMacroParamStrings(act.Params)
		}
		switch name {
		case "resolve_conversation", "close_conversation", "close", "resolve":
			mutation.Status = stringPtr(domain.ConversationStatusResolved)
		case "open_conversation", "open":
			mutation.Status = stringPtr(domain.ConversationStatusOpen)
			mutation.ClearSnooze = true
		case "snooze_conversation", "snooze":
			d := 24 * time.Hour
			if len(params) > 0 {
				if parsed, e := time.ParseDuration(params[0]); e == nil && parsed > 0 {
					d = parsed
				}
			}
			until := time.Now().UTC().Add(d)
			mutation.Status = stringPtr(domain.ConversationStatusSnoozed)
			mutation.SnoozedUntil = &until
		case "mute_conversation":
			mutation.Muted = boolPtr(true)
			mutation.Status = stringPtr(domain.ConversationStatusResolved)
		case "change_status":
			if len(params) > 0 {
				status := params[0]
				if status == "closed" {
					status = domain.ConversationStatusResolved
				}
				mutation.Status = &status
			}
		case "assign_agent":
			id := parseMacroID(params)
			if id > 0 {
				member, e := r.accounts.GetMembership(accountID, id)
				if e != nil {
					return e
				}
				if member != nil {
					mutation.AssigneeID = &id
					mutation.SetAssignee = true
				} else {
					logger.WithComponent("macro").Warn("blocked cross-tenant agent assignment in macro", "account_id", accountID, "target_agent_id", id, "conversation_id", convID)
				}
			}
		case "remove_assigned_agent":
			mutation.SetAssignee = true
		case "assign_team":
			id := parseMacroID(params)
			if id > 0 {
				team, e := r.teams.FindSummaryByID(accountID, id)
				if e != nil {
					return e
				}
				if team != nil {
					mutation.TeamID = &id
					mutation.SetTeam = true
				}
			}
		case "remove_assigned_team":
			mutation.SetTeam = true
		case "change_priority":
			if len(params) > 0 {
				mutation.Priority = &params[0]
			}
		case "add_label", "add_labels":
			for _, p := range params {
				title := strings.TrimSpace(p)
				if title == "" {
					continue
				}
				label, e := r.labels.FindOrCreateByTitle(accountID, title)
				if e != nil {
					return e
				}
				if e = r.labels.AttachToConversation(accountID, convID, label.ID); e != nil {
					return e
				}
			}
		case "remove_label", "remove_labels":
			for _, p := range params {
				if e := r.detachMacroLabel(accountID, convID, strings.TrimSpace(p)); e != nil {
					return e
				}
			}
		case "send_message", "send_reply", "reply", "add_private_note", "private_note":
			if len(params) > 0 && params[0] != "" {
				if _, e := r.messages.CreateMacroMessage(accountID, convID, userID, params[0], name == "add_private_note" || name == "private_note", nil); e != nil {
					return e
				}
			}
		case "send_attachment":
			if len(params) > 0 && params[0] != "" {
				spec := MacroAttachmentSpec{FileURL: params[0], FileType: "application/octet-stream"}
				if m, ok := act.ActionParams.(map[string]any); ok {
					if v, ok := m["file_url"].(string); ok && v != "" {
						spec.FileURL = v
					}
					if v, ok := m["file_type"].(string); ok && v != "" {
						spec.FileType = v
					}
					if v, ok := m["file_size"].(float64); ok {
						spec.FileSize = int64(v)
					}
				}
				if _, e := r.messages.CreateMacroMessage(accountID, convID, userID, spec.FileURL, false, &spec); e != nil {
					return e
				}
			}
		}
	}
	return r.conversations.ApplyMacroMutation(accountID, convID, mutation)
}

func parseMacroID(params []string) uint {
	if len(params) == 0 {
		return 0
	}
	var id uint
	_, _ = fmt.Sscanf(params[0], "%d", &id)
	return id
}
func stringPtr(v string) *string { return &v }
func boolPtr(v bool) *bool       { return &v }
func (r *MacroRepository) detachMacroLabel(accountID, convID uint, value string) error {
	if value == "" {
		return nil
	}
	var label *domain.Label
	id := parseMacroID([]string{value})
	var err error
	if id > 0 {
		label, err = r.labels.FindByID(accountID, id)
	} else {
		label, err = r.labels.FindByTitle(accountID, value)
	}
	if err != nil {
		return err
	}
	if label == nil {
		return nil
	}
	return r.labels.DetachFromConversation(accountID, convID, label.ID)
}
