package repository

import (
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// UpdateCallRecording changes only recording metadata on an existing tenant-
// owned call. Save/upsert could resurrect a deleted call or overwrite live state.
func (r *channelAuthRTMRepository) UpdateCallRecording(accountID, callID uint, recordingURL, recordingSID string, duration *int) error {
	changes := map[string]interface{}{"recording_url": recordingURL, "updated_at": time.Now()}
	if recordingSID != "" {
		changes["RecordingSID"] = recordingSID
	}
	if duration != nil {
		changes["duration"] = *duration
	}
	result := r.db.Model(&domain.Call{}).Where("account_id = ? AND id = ?", accountID, callID).Updates(changes)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}
