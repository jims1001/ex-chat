package repository

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// ConversationCleanup routes a deletion command to each data owner. Each owner
// transaction is independently retryable; no transaction spans module stores.
type ConversationCleanup struct {
	stores map[string]*gorm.DB
}

var conversationCleanupOwners = []string{"MSG", "RPT", "QLT", "RTM", "TKT", "API", "AIC", "OPS", "BIL"}

func NewCombinedConversationCleanup(db *gorm.DB) *ConversationCleanup {
	stores := make(map[string]*gorm.DB, len(conversationCleanupOwners))
	for _, owner := range conversationCleanupOwners {
		stores[owner] = db
	}
	return &ConversationCleanup{stores: stores}
}

// NewConversationCleanup requires an explicit store for every affected owner.
func NewConversationCleanup(stores map[string]*gorm.DB) (*ConversationCleanup, error) {
	selected := make(map[string]*gorm.DB, len(conversationCleanupOwners))
	for _, owner := range conversationCleanupOwners {
		if stores[owner] == nil {
			return nil, fmt.Errorf("conversation cleanup owner %s has no store", owner)
		}
		selected[owner] = stores[owner]
	}
	return &ConversationCleanup{stores: selected}, nil
}

func (c *ConversationCleanup) CleanupConversation(accountID, conversationID uint) error {
	if c == nil || accountID == 0 || conversationID == 0 {
		return gorm.ErrInvalidData
	}
	for _, owner := range conversationCleanupOwners {
		if err := c.cleanupOwner(owner, accountID, conversationID); err != nil {
			return fmt.Errorf("cleanup %s conversation %d: %w", owner, conversationID, err)
		}
	}
	return nil
}

func (c *ConversationCleanup) cleanupOwner(owner string, accountID, conversationID uint) error {
	db := c.stores[owner]
	if db == nil {
		return gorm.ErrInvalidData
	}
	switch owner {
	case "MSG":
		var paths []string
		err := db.Transaction(func(tx *gorm.DB) error {
			var ids []uint
			if err := tx.Model(&domain.Message{}).Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Pluck("id", &ids).Error; err != nil {
				return err
			}
			if len(ids) != 0 {
				var attachments []domain.Attachment
				if err := tx.Where("account_id = ? AND message_id IN ?", accountID, ids).Find(&attachments).Error; err != nil {
					return err
				}
				for _, attachment := range attachments {
					paths = append(paths, attachment.DataURL)
				}
				if err := tx.Where("account_id = ? AND message_id IN ?", accountID, ids).Delete(&domain.Attachment{}).Error; err != nil {
					return err
				}
			}
			if err := tx.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Delete(&domain.Message{}).Error; err != nil {
				return err
			}
			if err := tx.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Delete(&domain.DraftMessage{}).Error; err != nil {
				return err
			}
			if err := tx.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Delete(&domain.EmailLog{}).Error; err != nil {
				return err
			}
			return tx.Model(&domain.LocalChangeJournal{}).
				Where("source_module = ? AND account_id = ? AND object_type = ? AND action = ? AND status = ? AND object_id IN ?", "MSG", accountID, "Message", "create", "pending", ids).
				Update("status", "superseded").Error
		})
		if err != nil {
			return err
		}
		for _, path := range paths {
			if strings.HasPrefix(path, "/uploads/") || strings.HasPrefix(path, "uploads/") {
				clean := filepath.Clean(strings.TrimPrefix(path, "/"))
				if clean != "uploads" && strings.HasPrefix(clean, "uploads"+string(filepath.Separator)) {
					_ = os.Remove(clean)
				}
			}
		}
		return os.RemoveAll(filepath.Join("uploads", fmt.Sprintf("account_%d", accountID), fmt.Sprintf("conv_%d", conversationID)))
	case "RPT":
		return db.Transaction(func(tx *gorm.DB) error {
			for _, model := range []any{&domain.AppliedSLA{}, &domain.SLAEvent{}, &domain.SLABreachLog{}, &domain.ReportingEvent{}} {
				if err := tx.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Delete(model).Error; err != nil {
					return err
				}
			}
			return nil
		})
	case "QLT":
		return db.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Delete(&domain.CSATSurvey{}).Error
	case "RTM":
		return db.Transaction(func(tx *gorm.DB) error {
			var ids []uint
			if err := tx.Model(&domain.Call{}).Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Pluck("id", &ids).Error; err != nil {
				return err
			}
			if len(ids) != 0 {
				if err := tx.Where("call_id IN ?", ids).Delete(&domain.CallICECandidate{}).Error; err != nil {
					return err
				}
				if err := tx.Where("account_id = ? AND id IN ?", accountID, ids).Delete(&domain.Call{}).Error; err != nil {
					return err
				}
			}
			return tx.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Delete(&domain.Conference{}).Error
		})
	case "TKT":
		return db.Model(&domain.Ticket{}).Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Update("conversation_id", nil).Error
	case "API":
		return db.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Delete(&domain.WidgetEvent{}).Error
	case "AIC":
		return db.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Delete(&domain.CopilotThread{}).Error
	case "OPS":
		return db.Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Delete(&domain.CampaignDelivery{}).Error
	case "BIL":
		return db.Model(&domain.Order{}).Where("account_id = ? AND conversation_id = ?", accountID, conversationID).Update("conversation_id", nil).Error
	default:
		return gorm.ErrInvalidData
	}
}
