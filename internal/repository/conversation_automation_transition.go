package repository

import (
	"encoding/json"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// AutomationMessageActivityTransition returns only the transition committed with
// the stable activity receipt. The current conversation is deliberately not read:
// a later manual mutation cannot rewrite the meaning of a pending message fact.
// Missing markers on old receipts mean no alias, never an inferred reopen.
func (r *ConversationRepository) AutomationMessageActivityTransition(accountID, conversationID uint, messageChangeID string) (string, string, error) {
	version, err := r.AutomationMessageActivityVersion(accountID, conversationID, messageChangeID)
	if err != nil {
		return "", "", err
	}
	var record domain.LocalChangeJournal
	if err := r.db.Where("change_id = ? AND source_module = ? AND account_id = ? AND object_type = ? AND object_id = ? AND action = ? AND source_type = ? AND command_id = ?",
		conversationAutomationMessageActivityID(messageChangeID), "CON", accountID, "Conversation", conversationID, "update", "msg.message.created.v1", messageChangeID).First(&record).Error; err != nil {
		return "", "", err
	}
	if record.AfterState == "" {
		return "", "", nil
	}
	var receipt map[string]any
	if json.Unmarshal([]byte(record.AfterState), &receipt) != nil {
		return "", "", gorm.ErrInvalidData
	}
	before, hasBefore := receipt["status_before"]
	after, hasAfter := receipt["status_after"]
	if !hasBefore && !hasAfter {
		return "", "", nil
	}
	if !hasBefore || !hasAfter || before != domain.ConversationStatusResolved || after != domain.ConversationStatusOpen || record.Reason != "message_reopened" || version != record.ObjectVersionAfter || receipt["reopen"] != true {
		return "", "", gorm.ErrInvalidData
	}
	for _, field := range strings.Split(record.ChangedFields, ",") {
		if field == "status" {
			return domain.ConversationStatusResolved, domain.ConversationStatusOpen, nil
		}
	}
	return "", "", gorm.ErrInvalidData
}
