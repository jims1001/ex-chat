package repository

import (
	"fmt"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type channelAuthRPTRepository struct{ db *gorm.DB }

func (r *channelAuthRPTRepository) CreateReportingEvent(evt *domain.ReportingEvent) error {
	return r.db.Create(evt).Error
}

func (r *channelAuthRPTRepository) ListReportingEvents(accountID uint, name string) ([]domain.ReportingEvent, error) {
	var events []domain.ReportingEvent
	query := r.db.Where("account_id = ?", accountID)
	if name != "" {
		query = query.Where("name = ?", name)
	}
	err := query.Order("id DESC").Find(&events).Error
	return events, err
}

func (r *channelAuthRPTRepository) ListReportingEventsByConversation(accountID, conversationID uint) ([]domain.ReportingEvent, error) {
	var events []domain.ReportingEvent
	err := r.db.Where("account_id = ? AND (conversation_id = ? OR metadata_json LIKE ?)",
		accountID, conversationID, fmt.Sprintf(`%%"conversation_id":%d%%`, conversationID)).
		Order("id DESC").Find(&events).Error
	return events, err
}
