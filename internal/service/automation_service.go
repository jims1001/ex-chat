package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"gorm.io/gorm"
)

type automationContextKey struct{}

const MaxAutomationDepth = 3

// WithAutomationDepth returns a context with incremented automation depth
func WithAutomationDepth(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	depth := GetAutomationDepth(ctx)
	return context.WithValue(ctx, automationContextKey{}, depth+1)
}

// GetAutomationDepth returns current automation recursion depth
func GetAutomationDepth(ctx context.Context) int {
	if ctx == nil {
		return 0
	}
	if v, ok := ctx.Value(automationContextKey{}).(int); ok {
		return v
	}
	return 0
}

type AutomationService struct {
	db       *gorm.DB
	convRepo *repository.ConversationRepository
	msgRepo  *repository.MessageRepository

	// Idempotency cache to prevent same-second duplicated execution for same conversation+event+rule
	recentExecMu sync.Mutex
	recentExec   map[string]time.Time
}

func NewAutomationService(db *gorm.DB, convRepo *repository.ConversationRepository, msgRepo *repository.MessageRepository) *AutomationService {
	return &AutomationService{
		db:         db,
		convRepo:   convRepo,
		msgRepo:    msgRepo,
		recentExec: make(map[string]time.Time),
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

// ActionResult records individual action status and error
type ActionResult struct {
	ActionName string `json:"action_name"`
	Status     string `json:"status"` // success, failed
	Error      string `json:"error,omitempty"`
}

// HandleConversationCreated triggers automation on conversation creation
func (s *AutomationService) HandleConversationCreated(conv *domain.Conversation) {
	s.HandleConversationCreatedWithContext(context.Background(), conv)
}

// HandleConversationCreatedWithContext triggers automation on conversation creation with recursion context
func (s *AutomationService) HandleConversationCreatedWithContext(ctx context.Context, conv *domain.Conversation) {
	s.evaluateAndExecute(ctx, conv, nil, "conversation_created")
}

// HandleConversationUpdated triggers automation on status/assignment updates
func (s *AutomationService) HandleConversationUpdated(conv *domain.Conversation) {
	s.HandleConversationUpdatedWithContext(context.Background(), conv)
}

// HandleConversationUpdatedWithContext triggers automation on status/assignment updates with recursion context
func (s *AutomationService) HandleConversationUpdatedWithContext(ctx context.Context, conv *domain.Conversation) {
	s.evaluateAndExecute(ctx, conv, nil, "conversation_updated")
}

// HandleMessageCreated triggers automation on new message
func (s *AutomationService) HandleMessageCreated(conv *domain.Conversation, msg *domain.Message) {
	s.HandleMessageCreatedWithContext(context.Background(), conv, msg)
}

// HandleMessageCreatedWithContext triggers automation on new message with recursion context
func (s *AutomationService) HandleMessageCreatedWithContext(ctx context.Context, conv *domain.Conversation, msg *domain.Message) {
	s.evaluateAndExecute(ctx, conv, msg, "message_created")
}

func (s *AutomationService) evaluateAndExecute(ctx context.Context, conv *domain.Conversation, msg *domain.Message, eventName string) {
	if conv == nil {
		return
	}

	depth := GetAutomationDepth(ctx)
	if depth >= MaxAutomationDepth {
		logger.WithComponent("automation").Warn("recursion loop prevented: automation execution depth exceeded threshold",
			"depth", depth,
			"conversation_id", conv.ID,
			"account_id", conv.AccountID,
			"event", eventName,
		)
		// Record depth exceeded execution in DB
		exec := domain.AutomationRuleExecution{
			AccountID:      conv.AccountID,
			RuleID:         0,
			ConversationID: conv.ID,
			EventName:      eventName,
			Status:         "depth_exceeded",
			ActionResults:  "[]",
			DurationMs:     0,
			TriggeredAt:    time.Now().UTC(),
			Error:          fmt.Sprintf("recursion depth %d exceeded maximum allowed (%d)", depth, MaxAutomationDepth),
		}
		if msg != nil && msg.ID > 0 {
			exec.MessageID = &msg.ID
		}
		_ = s.db.Create(&exec).Error
		return
	}

	nextCtx := WithAutomationDepth(ctx)

	var rules []domain.AutomationRule
	err := s.db.Where("account_id = ? AND event_name = ? AND active = ?", conv.AccountID, eventName, true).
		Order("id ASC").
		Find(&rules).Error
	if err != nil || len(rules) == 0 {
		return
	}

	for _, rule := range rules {
		// Idempotency check: prevent same rule triggering on same conversation within 2 seconds
		idempKey := fmt.Sprintf("%d:%d:%d:%s", conv.AccountID, conv.ID, rule.ID, eventName)
		s.recentExecMu.Lock()
		now := time.Now()
		if lastRun, exists := s.recentExec[idempKey]; exists && now.Sub(lastRun) < 2*time.Second {
			s.recentExecMu.Unlock()
			continue
		}
		s.recentExec[idempKey] = now
		// Clean up old entries if map gets large
		if len(s.recentExec) > 1000 {
			for k, t := range s.recentExec {
				if now.Sub(t) > 10*time.Second {
					delete(s.recentExec, k)
				}
			}
		}
		s.recentExecMu.Unlock()

		if !s.matchConditions(rule.Conditions, conv, msg) {
			continue
		}

		logger.WithComponent("automation").Info("automation rule matched and triggered",
			"rule_id", rule.ID,
			"rule_name", rule.Name,
			"event", eventName,
			"conversation_id", conv.ID,
			"account_id", conv.AccountID,
			"depth", depth,
		)

		actions := s.parseActions(rule.Actions)
		if len(actions) == 0 {
			continue
		}

		startTime := time.Now()
		var actionResults []ActionResult
		hasFailure := false
		hasSuccess := false

		for _, action := range actions {
			actErr := s.executeAction(nextCtx, conv, msg, action)
			if actErr != nil {
				hasFailure = true
				actionResults = append(actionResults, ActionResult{
					ActionName: action.ActionName,
					Status:     "failed",
					Error:      actErr.Error(),
				})
			} else {
				hasSuccess = true
				actionResults = append(actionResults, ActionResult{
					ActionName: action.ActionName,
					Status:     "success",
				})
			}
		}

		duration := time.Since(startTime).Milliseconds()
		status := "success"
		if hasFailure && hasSuccess {
			status = "partial_failed"
		} else if hasFailure && !hasSuccess {
			status = "failed"
		}

		resBytes, _ := json.Marshal(actionResults)
		executionRecord := domain.AutomationRuleExecution{
			AccountID:      conv.AccountID,
			RuleID:         rule.ID,
			ConversationID: conv.ID,
			EventName:      eventName,
			Status:         status,
			ActionResults:  string(resBytes),
			DurationMs:     duration,
			TriggeredAt:    startTime.UTC(),
		}
		if msg != nil && msg.ID > 0 {
			executionRecord.MessageID = &msg.ID
		}
		if hasFailure {
			var errMsgs []string
			for _, ar := range actionResults {
				if ar.Error != "" {
					errMsgs = append(errMsgs, fmt.Sprintf("%s: %s", ar.ActionName, ar.Error))
				}
			}
			executionRecord.Error = strings.Join(errMsgs, "; ")
		}

		_ = s.db.Create(&executionRecord).Error
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
	case "team_id":
		if conv.TeamID != nil {
			targetVal = int(*conv.TeamID)
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
	case "email":
		if conv.Contact != nil && conv.Contact.Email != "" {
			targetVal = conv.Contact.Email
		} else if s.db != nil && conv.ContactID > 0 {
			var contact domain.Contact
			if err := s.db.Where("id = ?", conv.ContactID).First(&contact).Error; err == nil {
				conv.Contact = &contact
				targetVal = contact.Email
			}
		}
	case "phone_number":
		if conv.Contact != nil && conv.Contact.PhoneNumber != "" {
			targetVal = conv.Contact.PhoneNumber
		} else if s.db != nil && conv.ContactID > 0 {
			var contact domain.Contact
			if err := s.db.Where("id = ?", conv.ContactID).First(&contact).Error; err == nil {
				conv.Contact = &contact
				targetVal = contact.PhoneNumber
			}
		}
	case "country_code", "city", "company_name":
		if conv.Contact == nil && s.db != nil && conv.ContactID > 0 {
			var contact domain.Contact
			if err := s.db.Where("id = ?", conv.ContactID).First(&contact).Error; err == nil {
				conv.Contact = &contact
			}
		}
		if conv.Contact != nil {
			if cond.AttributeKey == "company_name" && conv.Contact.CompanyID != nil && s.db != nil {
				var company domain.Company
				if err := s.db.Where("id = ?", *conv.Contact.CompanyID).First(&company).Error; err == nil {
					targetVal = company.Name
				}
			}
			if targetVal == nil && conv.Contact.CustomAttributes != "" {
				var custMap map[string]any
				if err := json.Unmarshal([]byte(conv.Contact.CustomAttributes), &custMap); err == nil {
					if val, ok := custMap[cond.AttributeKey]; ok {
						targetVal = val
					}
				}
			}
		}
	case "browser_language", "conversation_language", "referer", "mail_subject":
		if conv.CustomAttributes != "" {
			var custMap map[string]any
			if err := json.Unmarshal([]byte(conv.CustomAttributes), &custMap); err == nil {
				if val, ok := custMap[cond.AttributeKey]; ok {
					targetVal = val
				}
			}
		}
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

func (s *AutomationService) executeAction(ctx context.Context, conv *domain.Conversation, msg *domain.Message, action ActionInstruction) error {
	logger.WithComponent("automation").Info("executing automation action",
		"action_name", action.ActionName,
		"conversation_id", conv.ID,
		"account_id", conv.AccountID,
		"depth", GetAutomationDepth(ctx),
	)
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

	var parseUintVal func(val any) uint
	parseUintVal = func(val any) uint {
		if val == nil {
			return 0
		}
		switch v := val.(type) {
		case float64:
			if v > 0 {
				return uint(v)
			}
		case float32:
			if v > 0 {
				return uint(v)
			}
		case int:
			if v > 0 {
				return uint(v)
			}
		case int64:
			if v > 0 {
				return uint(v)
			}
		case uint:
			return v
		case string:
			var u uint
			if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &u); err == nil && u > 0 {
				return u
			}
		case []any:
			if len(v) > 0 {
				return parseUintVal(v[0])
			}
		case []string:
			if len(v) > 0 {
				return parseUintVal(v[0])
			}
		}
		return 0
	}

	getParamUint := func(keys ...string) uint {
		if m, ok := action.ActionParams.(map[string]any); ok {
			for _, k := range keys {
				if v, exists := m[k]; exists {
					if u := parseUintVal(v); u > 0 {
						return u
					}
				}
			}
			return 0
		} else if arr, ok := action.ActionParams.([]any); ok && len(arr) > 0 {
			return parseUintVal(arr[0])
		} else if arrStr, ok := action.ActionParams.([]string); ok && len(arrStr) > 0 {
			return parseUintVal(arrStr[0])
		}
		return parseUintVal(action.ActionParams)
	}

	switch action.ActionName {
	case "assign_agent":
		uid := getParamUint("user_id", "agent_id")
		if uid > 0 {
			// Verify agent belongs to this tenant account if account_users table exists
			hasTable := s.db.Migrator().HasTable(&domain.AccountUser{})
			allowed := true
			if hasTable {
				var count int64
				_ = s.db.Model(&domain.AccountUser{}).Where("account_id = ? AND user_id = ?", conv.AccountID, uid).Count(&count).Error
				if count == 0 {
					allowed = false
				}
			}
			if !allowed {
				logger.WithComponent("automation").Warn("blocked cross-tenant agent assignment in automation rule",
					"account_id", conv.AccountID,
					"target_agent_id", uid,
					"conversation_id", conv.ID,
				)
			} else {
				_ = s.convRepo.Assign(conv.AccountID, conv.ID, &uid)
				conv.AssigneeID = &uid
				var agent domain.User
				if err := s.db.Where("id = ?", uid).First(&agent).Error; err == nil {
					conv.Assignee = &agent
				}
			}
		}
	case "remove_assigned_agent":
		_ = s.convRepo.Assign(conv.AccountID, conv.ID, nil)
		conv.AssigneeID = nil
		conv.Assignee = nil
	case "assign_team":
		tid := getParamUint("team_id", "team_ids", "id")
		if tid > 0 {
			var team domain.Team
			if err := s.db.Where("account_id = ? AND id = ?", conv.AccountID, tid).First(&team).Error; err == nil {
				_ = s.convRepo.AssignTeam(conv.AccountID, conv.ID, &tid)
				conv.TeamID = &tid
				conv.Team = &team
			}
		}
	case "remove_assigned_team":
		_ = s.convRepo.AssignTeam(conv.AccountID, conv.ID, nil)
		conv.TeamID = nil
		conv.Team = nil
	case "mute_conversation":
		conv.Muted = true
		newStatus := "resolved"
		_ = s.convRepo.UpdateStatus(conv.AccountID, conv.ID, newStatus, nil)
		conv.Status = newStatus
		_ = s.db.Model(&domain.Conversation{}).Where("id = ?", conv.ID).Update("muted", true).Error
	case "snooze_conversation", "snooze":
		newStatus := domain.ConversationStatusSnoozed
		snoozeDuration := 24 * time.Hour
		if st := getParamString("duration"); st != "" {
			if d, parseErr := time.ParseDuration(st); parseErr == nil && d > 0 {
				snoozeDuration = d
			}
		}
		snoozedUntil := time.Now().UTC().Add(snoozeDuration)
		conv.SnoozedUntil = &snoozedUntil
		_ = s.convRepo.UpdateStatus(conv.AccountID, conv.ID, newStatus, &snoozedUntil)
		conv.Status = newStatus
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
			// Chatwoot specification: private note is outgoing message with private=true
			msgType := domain.MessageTypeOutgoing
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
			if err := s.msgRepo.Create(&reply); err != nil {
				logger.WithComponent("automation").Error("failed to send automation reply message",
					"conversation_id", conv.ID,
					"account_id", conv.AccountID,
					"error", err.Error(),
				)
			} else {
				logger.WithComponent("automation").Info("automation rule sent reply message",
					"conversation_id", conv.ID,
					"account_id", conv.AccountID,
					"message_id", reply.ID,
					"private", isPrivate,
				)
			}
		}
	case "add_label", "add_labels":
		var labelsToAdd []string
		if m, ok := action.ActionParams.(map[string]any); ok {
			if val, ok := m["labels"].([]any); ok {
				for _, it := range val {
					labelsToAdd = append(labelsToAdd, fmt.Sprintf("%v", it))
				}
			} else if val, ok := m["labels"].([]string); ok {
				labelsToAdd = append(labelsToAdd, val...)
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

					found := false
					for _, existing := range conv.Labels {
						if existing.ID == label.ID {
							found = true
							break
						}
					}
					if !found {
						conv.Labels = append(conv.Labels, label)
					}
				}
			}
		}

	case "remove_label", "remove_labels":
		var labelsToRemove []string
		if m, ok := action.ActionParams.(map[string]any); ok {
			if val, ok := m["labels"].([]any); ok {
				for _, it := range val {
					labelsToRemove = append(labelsToRemove, fmt.Sprintf("%v", it))
				}
			} else if val, ok := m["labels"].([]string); ok {
				labelsToRemove = append(labelsToRemove, val...)
			} else if val, ok := m["label"].(string); ok {
				labelsToRemove = append(labelsToRemove, val)
			} else if val, ok := m["remove"].([]any); ok {
				for _, it := range val {
					labelsToRemove = append(labelsToRemove, fmt.Sprintf("%v", it))
				}
			} else if val, ok := m["remove"].([]string); ok {
				labelsToRemove = append(labelsToRemove, val...)
			}
		} else if arr, ok := action.ActionParams.([]any); ok {
			for _, it := range arr {
				labelsToRemove = append(labelsToRemove, fmt.Sprintf("%v", it))
			}
		} else if arrStr, ok := action.ActionParams.([]string); ok {
			labelsToRemove = append(labelsToRemove, arrStr...)
		} else if str, ok := action.ActionParams.(string); ok {
			labelsToRemove = append(labelsToRemove, str)
		}
		for _, lblTarget := range labelsToRemove {
			lblTarget = strings.TrimSpace(lblTarget)
			if lblTarget == "" {
				continue
			}
			var idNum uint
			_, _ = fmt.Sscanf(lblTarget, "%d", &idNum)

			var labels []domain.Label
			if idNum > 0 {
				_ = s.db.Where("account_id = ? AND (title = ? OR id = ?)", conv.AccountID, lblTarget, idNum).Find(&labels).Error
			} else {
				_ = s.db.Where("account_id = ? AND title = ?", conv.AccountID, lblTarget).Find(&labels).Error
			}

			for _, l := range labels {
				_ = s.db.Where("conversation_id = ? AND label_id = ?", conv.ID, l.ID).Delete(&domain.ConversationLabel{}).Error
			}
			if len(labels) == 0 && idNum > 0 {
				_ = s.db.Where("conversation_id = ? AND label_id = ?", conv.ID, idNum).Delete(&domain.ConversationLabel{}).Error
			}
		}

		if len(conv.Labels) > 0 {
			var kept []domain.Label
			for _, existing := range conv.Labels {
				shouldRemove := false
				for _, rem := range labelsToRemove {
					rem = strings.TrimSpace(rem)
					if strings.EqualFold(existing.Title, rem) || fmt.Sprintf("%d", existing.ID) == rem {
						shouldRemove = true
						break
					}
				}
				if !shouldRemove {
					kept = append(kept, existing)
				}
			}
			conv.Labels = kept
		}
	}

	now := time.Now().UTC()
	conv.LastActivityAt = now
	conv.UpdatedAt = now
	updateFields := map[string]any{
		"last_activity_at": now,
		"updated_at":       now,
		"status":           conv.Status,
		"priority":         conv.Priority,
	}
	if conv.AssigneeID != nil {
		updateFields["assignee_id"] = conv.AssigneeID
	} else if action.ActionName == "remove_assigned_agent" {
		updateFields["assignee_id"] = nil
	}
	if conv.TeamID != nil {
		updateFields["team_id"] = conv.TeamID
	} else if action.ActionName == "remove_assigned_team" {
		updateFields["team_id"] = nil
	}
	return s.db.Model(&domain.Conversation{}).Where("id = ?", conv.ID).Updates(updateFields).Error
}
