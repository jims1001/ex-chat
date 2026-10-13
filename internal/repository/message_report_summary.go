package repository

import (
	"errors"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var errReportMessageSummaryUnavailable = errors.New("report message summary is unavailable")

// CountReportSummary reads one MSG-owned snapshot by the message's own timestamp.
// Optional bounds and SQLite's existing stored-text comparison are deliberately
// retained. In particular this legacy endpoint has no daily-series range budget
// or storage-representation guard. No content, conversation join or writes occur.
func (r *MessageRepository) CountReportSummary(accountID uint, since, until *time.Time) (domain.ReportMessageSummary, error) {
	var counts domain.ReportMessageSummary
	if r == nil || r.db == nil || r.db.Config == nil || r.db.Dialector == nil || r.db.Statement == nil {
		return counts, errReportMessageSummaryUnavailable
	}
	query := r.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).
		Model(&domain.Message{}).Where("account_id = ?", accountID)
	if since != nil {
		query = query.Where("created_at >= ?", ReportTimeBoundary(query, *since))
	}
	if until != nil {
		query = query.Where("created_at < ?", ReportTimeBoundary(query, *until))
	}
	rows, err := query.Select("COUNT(*) AS total, COUNT(CASE WHEN message_type = ? THEN 1 END) AS incoming, COUNT(CASE WHEN message_type = ? THEN 1 END) AS outgoing", domain.MessageTypeIncoming, domain.MessageTypeOutgoing).Rows()
	if err != nil {
		return counts, errReportMessageSummaryUnavailable
	}
	defer rows.Close()
	if !rows.Next() || rows.Scan(&counts.Total, &counts.Incoming, &counts.Outgoing) != nil || rows.Next() || rows.Err() != nil || rows.Close() != nil {
		return domain.ReportMessageSummary{}, errReportMessageSummaryUnavailable
	}
	return counts, nil
}
