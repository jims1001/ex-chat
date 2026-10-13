package modruntime

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Legacy confirmation bodies and provider errors may contain bearer links.
// Owner export/import is a second boundary after startup cleanup: importing an
// older snapshot must never reintroduce those bodies or generic retry work.
// Copy before redaction so creating a snapshot never mutates live model values.
func confirmationSafeSnapshotLogs(rows []domain.EmailLog) []domain.EmailLog {
	if rows == nil {
		return nil
	}
	result := append(make([]domain.EmailLog, 0, len(rows)), rows...)
	for i := range result {
		if result[i].EmailType != "confirmation" {
			continue
		}
		result[i].ContentHTML = ""
		result[i].ContentText = ""
		result[i].Error = ""
		result[i].BounceReason = ""
		result[i].Subject = "Email confirmation (legacy content removed)"
		result[i].MaxRetries = 0
		result[i].NextRetryAt = nil
	}
	return result
}

// The generic GORM importer applies the model's max_retries=3 default to zero
// values. Disable confirmation retries explicitly in the same owner restore
// transaction, so a failure also rolls back table replacement and the marker.
func restoreConfirmationSnapshotLogs(tx *gorm.DB, target any) (bool, error) {
	rows, ok := target.(*[]domain.EmailLog)
	if !ok {
		return false, nil
	}
	if len(*rows) == 0 {
		return true, nil
	}
	safe := confirmationSafeSnapshotLogs(*rows)
	legacy, managed := make([]domain.EmailLog, 0, len(safe)), make([]domain.EmailLog, 0, len(safe))
	for _, row := range safe {
		if row.ManagedQueue == ordinaryEmailQueue {
			managed = append(managed, row)
		} else {
			legacy = append(legacy, row)
		}
	}
	if len(legacy) > 0 {
		if err := tx.Omit(clause.Associations).Create(&legacy).Error; err != nil {
			return true, err
		}
	}
	if len(managed) > 0 {
		if err := ordinaryEmailPrivateDB(tx).Omit(clause.Associations).Create(&managed).Error; err != nil {
			return true, errOrdinaryEmailStore
		}
	}
	return true, tx.Model(&domain.EmailLog{}).Where("email_type = ?", "confirmation").UpdateColumn("max_retries", 0).Error
}
