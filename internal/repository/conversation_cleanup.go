package repository

import (
	"fmt"

	"gorm.io/gorm"
)

// ConversationCleanup coordinates independently retryable commands owned by
// the modules that reference a conversation. It does not query owner tables.
type ConversationCleanup struct {
	commands []conversationCleanupCommand
}

type conversationCleanupCommand interface {
	Owner() string
	CleanupConversation(accountID, conversationID uint) error
}

var conversationCleanupOwners = []string{"MSG", "RPT", "QLT", "RTM", "TKT", "API", "AIC", "OPS", "BIL"}

func NewCombinedConversationCleanup(db *gorm.DB) *ConversationCleanup {
	cleanup, _ := NewConversationCleanup(map[string]*gorm.DB{
		"MSG": db, "RPT": db, "QLT": db, "RTM": db, "TKT": db,
		"API": db, "AIC": db, "OPS": db, "BIL": db,
	})
	return cleanup
}

// NewConversationCleanup requires an explicit store for every affected owner
// and adapts each store to that owner's deletion command.
func NewConversationCleanup(stores map[string]*gorm.DB) (*ConversationCleanup, error) {
	for _, owner := range conversationCleanupOwners {
		if stores[owner] == nil {
			return nil, fmt.Errorf("conversation cleanup owner %s has no store", owner)
		}
	}
	return &ConversationCleanup{commands: []conversationCleanupCommand{
		newMSGConversationCleanup(stores["MSG"]), newRPTConversationCleanup(stores["RPT"]),
		newQLTConversationCleanup(stores["QLT"]), newRTMConversationCleanup(stores["RTM"]),
		newTKTConversationCleanup(stores["TKT"]), newAPIConversationCleanup(stores["API"]),
		newAICConversationCleanup(stores["AIC"]), newOPSConversationCleanup(stores["OPS"]),
		newBILConversationCleanup(stores["BIL"]),
	}}, nil
}

func (c *ConversationCleanup) CleanupConversation(accountID, conversationID uint) error {
	if c == nil || accountID == 0 || conversationID == 0 {
		return gorm.ErrInvalidData
	}
	for _, command := range c.commands {
		if command == nil {
			return gorm.ErrInvalidData
		}
		if err := command.CleanupConversation(accountID, conversationID); err != nil {
			return fmt.Errorf("cleanup %s conversation %d: %w", command.Owner(), conversationID, err)
		}
	}
	return nil
}
