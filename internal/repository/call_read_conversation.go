package repository

import (
	"errors"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// FindCallReadReference preserves the stored internal-ID convention. Historical
// outbound documentation also mentioned display IDs, so a second relationship-
// consistent display-ID match makes that reference unprovable for ordinary
// readers. Never fall back to or migrate a display-ID-only match.
func (r *ConversationRepository) FindCallReadReference(accountID, conversationID, inboxID, contactID uint) (*domain.Conversation, error) {
	if r == nil || r.db == nil || r.db.Config == nil || r.db.Dialector == nil {
		return nil, errors.New("call access facts unavailable")
	}
	db := r.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	var conversations []domain.Conversation
	err := db.Select("id", "account_id", "inbox_id", "contact_id", "assignee_id", "team_id").
		Where("account_id = ? AND inbox_id = ? AND contact_id = ? AND (id = ? OR display_id = ?)", accountID, inboxID, contactID, conversationID, conversationID).
		Limit(2).Find(&conversations).Error
	if err != nil {
		return nil, err
	}
	if len(conversations) != 1 || conversations[0].ID != conversationID {
		return nil, nil
	}
	return &conversations[0], nil
}
