package modruntime

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// restoreENTFeatureRows preserves the persisted false value that GORM's struct
// Create would otherwise replace with AccountFeature's default:true. Only this
// ENT owner model uses explicit columns; the snapshot format/defaults stay intact.
func restoreENTFeatureRows(tx *gorm.DB, rows any) (bool, error) {
	features, ok := rows.(*[]domain.AccountFeature)
	if !ok {
		return false, nil
	}
	if len(*features) == 0 {
		return true, nil
	}
	for _, row := range *features {
		err := tx.Model(&domain.AccountFeature{}).Create(map[string]any{
			"id": row.ID, "account_id": row.AccountID, "feature_name": row.FeatureName,
			"enabled": row.Enabled, "updated_at": row.UpdatedAt,
		}).Error
		if err != nil {
			return true, err
		}
	}
	return true, nil
}
