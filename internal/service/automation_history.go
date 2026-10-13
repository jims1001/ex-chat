package service

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

var ErrAutomationHistoryQuery = errors.New("invalid automation history query")

type AutomationExecutionPageOptions struct {
	Limit  int
	Cursor string
	Order  string
}
type AutomationExecutionPage struct {
	Items      []domain.AutomationRuleExecution
	NextCursor string
}

// The cursor is a read position, not an authorization token. Every query repeats
// tenant and filter predicates. Its high water excludes newer inserted rows, but
// it is not a frozen snapshot of mutable execution statuses.
type automationHistoryCursor struct {
	Version        int       `json:"v"`
	AccountID      uint      `json:"account"`
	RuleID         string    `json:"rule"`
	ConversationID string    `json:"conversation"`
	Status         string    `json:"status"`
	Order          string    `json:"order"`
	HighWater      uint      `json:"high_water"`
	TriggeredAt    time.Time `json:"at"`
	ID             uint      `json:"id"`
}

func automationHistoryID(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	for _, char := range raw {
		if char < '0' || char > '9' {
			return "", ErrAutomationHistoryQuery
		}
	}
	id, err := strconv.ParseUint(raw, 10, strconv.IntSize)
	if err != nil || id == 0 || id > math.MaxInt64 {
		return "", ErrAutomationHistoryQuery
	}
	return strconv.FormatUint(id, 10), nil
}
func automationHistoryStatuses(status string) []string {
	switch status {
	case "success", "succeeded":
		return []string{"success", "succeeded"}
	case "partial_failed", "partially_succeeded":
		return []string{"partial_failed", "partially_succeeded"}
	case "depth_exceeded", "loop_blocked":
		return []string{"depth_exceeded", "loop_blocked"}
	default:
		return []string{status}
	}
}

// ListExecutionsPage preserves the model response and adds bounded keyset reads.
// Only constant SQL order fragments are used; unknown states match literally.
func (s *AutomationService) ListExecutionsPage(accountID uint, ruleID, conversationID, status string, options AutomationExecutionPageOptions) (AutomationExecutionPage, error) {
	page := AutomationExecutionPage{Items: make([]domain.AutomationRuleExecution, 0)}
	var err error
	if ruleID, err = automationHistoryID(ruleID); err != nil {
		return page, err
	}
	if conversationID, err = automationHistoryID(conversationID); err != nil {
		return page, err
	}
	if len(status) > 50 || len(options.Cursor) > 2048 {
		return page, ErrAutomationHistoryQuery
	}
	if options.Limit == 0 {
		options.Limit = 100
	}
	if options.Limit < 1 || options.Limit > 100 {
		return page, ErrAutomationHistoryQuery
	}
	if options.Order == "" {
		options.Order = "desc"
	}
	if options.Order != "asc" && options.Order != "desc" {
		return page, ErrAutomationHistoryQuery
	}
	aliases := automationHistoryStatuses(status)
	position := automationHistoryCursor{Version: 1, AccountID: accountID, RuleID: ruleID, ConversationID: conversationID, Status: aliases[0], Order: options.Order}
	query := s.db.Model(&domain.AutomationRuleExecution{}).Where("account_id = ?", accountID)
	if ruleID != "" {
		query = query.Where("rule_id = ?", ruleID)
	}
	if conversationID != "" {
		query = query.Where("conversation_id = ?", conversationID)
	}
	if status != "" {
		query = query.Where("status IN ?", aliases)
	}
	if options.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(options.Cursor)
		if err != nil {
			return page, ErrAutomationHistoryQuery
		}
		var cursor automationHistoryCursor
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&cursor) != nil || decoder.Decode(new(any)) != io.EOF || cursor.Version != position.Version || cursor.AccountID != accountID || cursor.RuleID != ruleID || cursor.ConversationID != conversationID || cursor.Status != position.Status || cursor.Order != options.Order || cursor.ID == 0 || cursor.HighWater < cursor.ID || uint64(cursor.HighWater) > math.MaxInt64 || cursor.TriggeredAt.IsZero() {
			return page, ErrAutomationHistoryQuery
		}
		position = cursor
	} else {
		// Scope the horizon to this tenant, not to the current status. Status changes
		// while paging remain live; a refresh starts a new traversal.
		if err := s.db.Model(&domain.AutomationRuleExecution{}).Where("account_id = ?", accountID).Select("COALESCE(MAX(id), 0)").Scan(&position.HighWater).Error; err != nil {
			return page, err
		}
	}
	query = query.Where("id <= ?", position.HighWater)
	if options.Cursor != "" {
		if options.Order == "asc" {
			query = query.Where("(triggered_at > ? OR (triggered_at = ? AND id > ?))", position.TriggeredAt, position.TriggeredAt, position.ID)
		} else {
			query = query.Where("(triggered_at < ? OR (triggered_at = ? AND id < ?))", position.TriggeredAt, position.TriggeredAt, position.ID)
		}
	}
	if options.Order == "asc" {
		query = query.Order("triggered_at ASC, id ASC")
	} else {
		query = query.Order("triggered_at DESC, id DESC")
	}
	if err := query.Limit(options.Limit + 1).Find(&page.Items).Error; err != nil {
		return page, err
	}
	if len(page.Items) > options.Limit {
		page.Items = page.Items[:options.Limit]
		last := page.Items[len(page.Items)-1]
		position.TriggeredAt, position.ID = last.TriggeredAt.UTC(), last.ID
		raw, _ := json.Marshal(position)
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, nil
}

// Composition supplies a pure AUT projector without coupling this legacy
// service to runtime internals. Configuration occurs before serving requests.
func (s *AutomationService) SetAutomationHistoryProjector(projector func(domain.AutomationRuleExecution, *domain.AutomationRuleVersion) any) {
	s.historyProjector = projector
}

func (s *AutomationService) CompatibilityExecutionViews(executions []domain.AutomationRuleExecution) ([]any, error) {
	views := make([]any, 0, len(executions))
	if s.historyProjector == nil {
		return nil, errors.New("automation history compatibility view is not configured")
	}
	// Fetch exact immutable definitions only. Never read the mutable current rule
	// or cross-owner records, and never return action parameters to the caller.
	for _, execution := range executions {
		var version *domain.AutomationRuleVersion
		if execution.RuleVersion != 0 {
			var frozen domain.AutomationRuleVersion
			err := s.db.Where("account_id = ? AND rule_id = ? AND version = ?", execution.AccountID, execution.RuleID, execution.RuleVersion).First(&frozen).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, err
			}
			if err == nil {
				version = &frozen
			}
		}
		views = append(views, s.historyProjector(execution, version))
	}
	return views, nil
}
