package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"gorm.io/gorm"
)

type AutomationService struct {
	db       *gorm.DB
	convRepo *repository.ConversationRepository
	msgRepo  *repository.MessageRepository
}

func NewAutomationService(db *gorm.DB, convRepo *repository.ConversationRepository, msgRepo *repository.MessageRepository) *AutomationService {
	return &AutomationService{
		db:       db,
		convRepo: convRepo,
		msgRepo:  msgRepo,
	}
}

type RuleCondition struct {
	AttributeKey   string `json:"attribute_key"`
	FilterOperator string `json:"filter_operator"`
	Values         []any  `json:"values"`
	Value          any    `json:"value"`
	Key            string `json:"key"`
	Operator       string `json:"operator"`
	QueryOperator  string `json:"query_operator"` // AND, OR
}

type ActionInstruction struct {
	ActionName   string `json:"action_name"`
	ActionParams any    `json:"action_params"`
	Name         string `json:"name"`
	Params       any    `json:"params"`
}

// HandleConversationCreated triggers automation on conversation creation
func (s *AutomationService) HandleConversationCreated(conv *domain.Conversation) {
	s.evaluateAndExecute(conv, nil, "conversation_created")
}

// HandleConversationUpdated triggers automation on status/assignment updates
func (s *AutomationService) HandleConversationUpdated(conv *domain.Conversation) {
	s.evaluateAndExecute(conv, nil, "conversation_updated")
}

// HandleMessageCreated triggers automation on new message
func (s *AutomationService) HandleMessageCreated(conv *domain.Conversation, msg *domain.Message) {
	s.evaluateAndExecute(conv, msg, "message_created")
}

func (s *AutomationService) evaluateAndExecute(conv *domain.Conversation, msg *domain.Message, eventName string) {
	var rules []domain.AutomationRule
	err := s.db.Where("account_id = ? AND event_name = ? AND active = ?", conv.AccountID, eventName, true).
		Order("id ASC").
		Find(&rules).Error
	if err != nil || len(rules) == 0 {
		return
	}

	for _, rule := range rules {
		if !s.matchConditions(rule.Conditions, conv, msg) {
			continue
		}

		actions := s.parseActions(rule.Actions)
		if len(actions) == 0 {
			continue
		}

		for _, action := range actions {
			s.executeAction(conv, msg, action)
		}
	}
}

func (s *AutomationService) parseConditions(rawConditions string) []RuleCondition {
	raw := strings.TrimSpace(rawConditions)
	if raw == "" || raw == "[]" || raw == "{}" {
		return nil
	}

	var conditions []RuleCondition
	if err := json.Unmarshal([]byte(raw), &conditions); err == nil && len(conditions) > 0 {
		return s.normalizeConditions(conditions)
	}

	var wrapper struct {
		Conditions []RuleCondition `json:"conditions"`
		Rules      []RuleCondition `json:"rules"`
		Values     []RuleCondition `json:"values"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapper); err == nil {
		if len(wrapper.Conditions) > 0 {
			return s.normalizeConditions(wrapper.Conditions)
		}
		if len(wrapper.Rules) > 0 {
			return s.normalizeConditions(wrapper.Rules)
		}
		if len(wrapper.Values) > 0 {
			return s.normalizeConditions(wrapper.Values)
		}
	}

	var single RuleCondition
	if err := json.Unmarshal([]byte(raw), &single); err == nil && (single.AttributeKey != "" || single.Key != "") {
		return s.normalizeConditions([]RuleCondition{single})
	}

	return nil
}

func (s *AutomationService) normalizeConditions(conditions []RuleCondition) []RuleCondition {
	for i := range conditions {
		cond := &conditions[i]
		if cond.AttributeKey == "" && cond.Key != "" {
			cond.AttributeKey = cond.Key
		}
		if cond.FilterOperator == "" && cond.Operator != "" {
			cond.FilterOperator = cond.Operator
		}
		if len(cond.Values) == 0 && cond.Value != nil {
			cond.Values = []any{cond.Value}
		}
	}
	return conditions
}

func (s *AutomationService) parseActions(rawActions string) []ActionInstruction {
	raw := strings.TrimSpace(rawActions)
	if raw == "" || raw == "[]" || raw == "{}" {
		return nil
	}

	var actions []ActionInstruction
	if err := json.Unmarshal([]byte(raw), &actions); err == nil && len(actions) > 0 {
		return s.normalizeActions(actions)
	}

	var wrapper struct {
		Actions []ActionInstruction `json:"actions"`
		Rules   []ActionInstruction `json:"rules"`
		Values  []ActionInstruction `json:"values"`
	}
	if err := json.Unmarshal([]byte(raw), &wrapper); err == nil {
		if len(wrapper.Actions) > 0 {
			return s.normalizeActions(wrapper.Actions)
		}
		if len(wrapper.Rules) > 0 {
			return s.normalizeActions(wrapper.Rules)
		}
		if len(wrapper.Values) > 0 {
			return s.normalizeActions(wrapper.Values)
		}
	}

	var single ActionInstruction
	if err := json.Unmarshal([]byte(raw), &single); err == nil && (single.ActionName != "" || single.Name != "") {
		return s.normalizeActions([]ActionInstruction{single})
	}

	return nil
}

func (s *AutomationService) normalizeActions(actions []ActionInstruction) []ActionInstruction {
	for i := range actions {
		act := &actions[i]
		if act.ActionName == "" && act.Name != "" {
			act.ActionName = act.Name
		}
		if act.ActionParams == nil && act.Params != nil {
			act.ActionParams = act.Params
		}
	}
	return actions
}

func (s *AutomationService) matchConditions(rawConditions string, conv *domain.Conversation, msg *domain.Message) bool {
	raw := strings.TrimSpace(rawConditions)
	if raw == "" || raw == "[]" || raw == "{}" {
		return true // Explicitly empty rule matches unconditionally
	}

	conditions := s.parseConditions(raw)
	if len(conditions) == 0 {
		// Conditions were specified but invalid / unparseable; do not match
		return false
	}

	result := true
	for i, cond := range conditions {
		matched := s.matchSingleCondition(cond, conv, msg)
		if i == 0 {
			result = matched
			continue
		}

		prevQueryOp := strings.ToUpper(conditions[i-1].QueryOperator)
		if prevQueryOp == "OR" {
			result = result || matched
		} else { // Default AND
			result = result && matched
		}
	}
	return result
}

func (s *AutomationService) matchSingleCondition(cond RuleCondition, conv *domain.Conversation, msg *domain.Message) bool {
	var targetVal any

	switch cond.AttributeKey {
	case "status":
		targetVal = conv.Status
	case "priority":
		targetVal = conv.Priority
	case "inbox_id":
		targetVal = int(conv.InboxID)
	case "assignee_id":
		if conv.AssigneeID != nil {
			targetVal = int(*conv.AssigneeID)
		}
	case "content":
		if msg != nil {
			targetVal = msg.Content
		}
	case "message_type":
		if msg != nil {
			targetVal = msg.MessageType
		}
	case "private_note":
		if msg != nil {
			targetVal = msg.Private
		}
	case "contact_id":
		targetVal = int(conv.ContactID)
	case "labels", "label", "tag", "tags", "label_ids", "conversation_labels":
		var labels []domain.Label
		if len(conv.Labels) > 0 {
			labels = conv.Labels
		}
		if len(labels) == 0 && s.db != nil && conv.ID > 0 {
			_ = s.db.Joins("JOIN conversation_labels ON conversation_labels.label_id = labels.id").
				Where("conversation_labels.conversation_id = ?", conv.ID).
				Find(&labels).Error
		}

		switch cond.FilterOperator {
		case "is_present":
			return len(labels) > 0
		case "is_not_present":
			return len(labels) == 0
		case "equal_to", "is":
			if len(labels) == 0 {
				return false
			}
			for _, lbl := range labels {
				for _, v := range cond.Values {
					vStr := strings.TrimSpace(fmt.Sprintf("%v", v))
					if vStr == "" {
						continue
					}
					if strings.EqualFold(lbl.Title, vStr) || fmt.Sprintf("%d", lbl.ID) == vStr {
						return true
					}
				}
			}
			return false
		case "contains", "includes":
			if len(labels) == 0 {
				return false
			}
			for _, lbl := range labels {
				for _, v := range cond.Values {
					vStr := strings.TrimSpace(fmt.Sprintf("%v", v))
					if vStr == "" {
						continue
					}
					if strings.EqualFold(lbl.Title, vStr) || fmt.Sprintf("%d", lbl.ID) == vStr || strings.Contains(strings.ToLower(lbl.Title), strings.ToLower(vStr)) {
						return true
					}
				}
			}
			return false
		case "not_equal_to", "is_not":
			if len(labels) == 0 {
				return true
			}
			for _, lbl := range labels {
				for _, v := range cond.Values {
					vStr := strings.TrimSpace(fmt.Sprintf("%v", v))
					if vStr == "" {
						continue
					}
					if strings.EqualFold(lbl.Title, vStr) || fmt.Sprintf("%d", lbl.ID) == vStr {
						return false
					}
				}
			}
			return true
		case "does_not_contain":
			if len(labels) == 0 {
				return true
			}
			for _, lbl := range labels {
				for _, v := range cond.Values {
					vStr := strings.TrimSpace(fmt.Sprintf("%v", v))
					if vStr == "" {
						continue
					}
					if strings.Contains(strings.ToLower(lbl.Title), strings.ToLower(vStr)) {
						return false
					}
				}
			}
			return true
		default:
			return false
		}
	default:
		// Check custom attributes
		if conv.CustomAttributes != "" {
			var custMap map[string]any
			if err := json.Unmarshal([]byte(conv.CustomAttributes), &custMap); err == nil {
				if val, ok := custMap[cond.AttributeKey]; ok {
					targetVal = val
					break
				}
			}
		}
		return false
	}

	switch cond.FilterOperator {
	case "is_present":
		return targetVal != nil && fmt.Sprintf("%v", targetVal) != ""
	case "is_not_present":
		return targetVal == nil || fmt.Sprintf("%v", targetVal) == ""
	case "equal_to", "is":
		for _, v := range cond.Values {
			if fmt.Sprintf("%v", targetVal) == fmt.Sprintf("%v", v) {
				return true
			}
		}
		return false
	case "not_equal_to", "is_not":
		for _, v := range cond.Values {
			if fmt.Sprintf("%v", targetVal) == fmt.Sprintf("%v", v) {
				return false
			}
		}
		return true
	case "contains", "includes":
		strVal := strings.ToLower(fmt.Sprintf("%v", targetVal))
		for _, v := range cond.Values {
			if strings.Contains(strVal, strings.ToLower(fmt.Sprintf("%v", v))) {
				return true
			}
		}
		return false
	case "does_not_contain":
		strVal := strings.ToLower(fmt.Sprintf("%v", targetVal))
		for _, v := range cond.Values {
			if strings.Contains(strVal, strings.ToLower(fmt.Sprintf("%v", v))) {
				return false
			}
		}
		return true
	case "starts_with":
		strVal := strings.ToLower(fmt.Sprintf("%v", targetVal))
		for _, v := range cond.Values {
			if strings.HasPrefix(strVal, strings.ToLower(fmt.Sprintf("%v", v))) {
				return true
			}
		}
		return false
	}
	return false
}

func (s *AutomationService) executeAction(conv *domain.Conversation, msg *domain.Message, action ActionInstruction) {
	getParamString := func(key string) string {
		if m, ok := action.ActionParams.(map[string]any); ok {
			if v, ok := m[key].(string); ok {
				return v
			}
			for _, v := range m {
				if s, ok := v.(string); ok {
					return s
				}
			}
		} else if arr, ok := action.ActionParams.([]any); ok && len(arr) > 0 {
			return fmt.Sprintf("%v", arr[0])
		} else if arrStr, ok := action.ActionParams.([]string); ok && len(arrStr) > 0 {
			return arrStr[0]
		} else if str, ok := action.ActionParams.(string); ok {
			return str
		}
		return ""
	}

	getParamUint := func(key string) uint {
		if m, ok := action.ActionParams.(map[string]any); ok {
			if f, ok := m[key].(float64); ok {
				return uint(f)
			}
		} else if arr, ok := action.ActionParams.([]any); ok && len(arr) > 0 {
			if f, ok := arr[0].(float64); ok {
				return uint(f)
			}
		} else if f, ok := action.ActionParams.(float64); ok {
			return uint(f)
		}
		return 0
	}

	switch action.ActionName {
	case "assign_agent", "assign_team":
		uid := getParamUint("user_id")
		if uid == 0 {
			uid = getParamUint("agent_id")
		}
		if uid > 0 {
			_ = s.convRepo.Assign(conv.AccountID, conv.ID, &uid)
			conv.AssigneeID = &uid
		}
	case "remove_assigned_agent":
		_ = s.convRepo.Assign(conv.AccountID, conv.ID, nil)
		conv.AssigneeID = nil
	case "change_status", "resolve_conversation", "close_conversation", "close", "resolve", "open_conversation":
		newStatus := "open"
		if action.ActionName == "resolve_conversation" || action.ActionName == "close_conversation" || action.ActionName == "close" || action.ActionName == "resolve" {
			newStatus = "resolved"
		} else if st := getParamString("status"); st != "" {
			if st == "closed" {
				newStatus = "resolved"
			} else {
				newStatus = st
			}
		}
		_ = s.convRepo.UpdateStatus(conv.AccountID, conv.ID, newStatus, nil)
		conv.Status = newStatus
	case "change_priority":
		if p := getParamString("priority"); p != "" {
			_ = s.convRepo.UpdatePriority(conv.AccountID, conv.ID, p)
			conv.Priority = p
		}
	case "send_message", "send_reply", "reply", "add_private_note", "private_note":
		content := getParamString("message")
		if content == "" {
			content = getParamString("content")
		}
		if content != "" {
			isPrivate := action.ActionName == "add_private_note" || action.ActionName == "private_note"
			msgType := domain.MessageTypeOutgoing
			if isPrivate {
				msgType = domain.MessageTypeActivity
			}
			reply := domain.Message{
				AccountID:      conv.AccountID,
				ConversationID: conv.ID,
				SenderType:     domain.SenderTypeUser,
				SenderID:       0,
				MessageType:    msgType,
				ContentType:    domain.ContentTypeText,
				Content:        content,
				Status:         domain.MessageStatusSent,
				Private:        isPrivate,
				CreatedAt:      time.Now().UTC(),
				UpdatedAt:      time.Now().UTC(),
			}
			_ = s.msgRepo.Create(&reply)
		}
	case "add_label", "add_labels":
		var labelsToAdd []string
		if m, ok := action.ActionParams.(map[string]any); ok {
			if val, ok := m["labels"].([]any); ok {
				for _, it := range val {
					labelsToAdd = append(labelsToAdd, fmt.Sprintf("%v", it))
				}
			} else if val, ok := m["label"].(string); ok {
				labelsToAdd = append(labelsToAdd, val)
			}
		} else if arr, ok := action.ActionParams.([]any); ok {
			for _, it := range arr {
				labelsToAdd = append(labelsToAdd, fmt.Sprintf("%v", it))
			}
		} else if arrStr, ok := action.ActionParams.([]string); ok {
			labelsToAdd = append(labelsToAdd, arrStr...)
		} else if str, ok := action.ActionParams.(string); ok {
			labelsToAdd = append(labelsToAdd, str)
		}
		for _, lblTitle := range labelsToAdd {
			lblTitle = strings.TrimSpace(lblTitle)
			if lblTitle != "" {
				var label domain.Label
				if err := s.db.Where("account_id = ? AND title = ?", conv.AccountID, lblTitle).FirstOrCreate(&label, domain.Label{AccountID: conv.AccountID, Title: lblTitle}).Error; err == nil {
					cl := domain.ConversationLabel{ConversationID: conv.ID, LabelID: label.ID}
					_ = s.db.Where(cl).FirstOrCreate(&cl).Error
				}
			}
		}
	}

	now := time.Now().UTC()
	conv.LastActivityAt = now
	conv.UpdatedAt = now
	_ = s.db.Model(&domain.Conversation{}).Where("id = ?", conv.ID).Updates(map[string]any{
		"last_activity_at": now,
		"updated_at":       now,
		"status":           conv.Status,
		"priority":         conv.Priority,
		"assignee_id":      conv.AssigneeID,
	}).Error
}
