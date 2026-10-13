package repository

import (
	"encoding/json"
	"errors"
	"unicode/utf8"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	persistedFeatureRows      = 512
	persistedFeatureKeyBytes  = 400
	persistedFeatureJSONBytes = 65536
)

// PersistedFeatureFlags returns only stored account flags, without defaults,
// edition/license resolution or effective-availability decisions. One bounded
// SELECT provides a statement snapshot; malformed data fails the entire result.
func (r *EnterpriseRepository) PersistedFeatureFlags(accountID uint) (map[string]bool, error) {
	unavailable := errors.New("persisted feature flags are unavailable")
	if accountID == 0 {
		return nil, unavailable
	}
	length, enabled := "", "enabled"
	dialect := r.db.Dialector.Name()
	switch dialect {
	case "sqlite":
		length = "length(CAST(feature_name AS BLOB))"
		// SQLite has no boolean storage class. Only persisted integer 0/1 is a
		// boolean; reject text, blobs, NULL and other integers before conversion.
		enabled = "CASE WHEN typeof(enabled) = 'integer' AND enabled IN (0, 1) THEN enabled ELSE NULL END"
	case "postgres":
		length = "octet_length(feature_name)"
	default:
		return nil, unavailable
	}
	rows, err := r.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).
		Model(&domain.AccountFeature{}).
		Select("CASE WHEN "+length+" <= ? THEN feature_name ELSE NULL END AS feature_name, "+enabled+" AS enabled", persistedFeatureKeyBytes).
		Where("account_id = ?", accountID).Order("feature_name ASC").Limit(persistedFeatureRows + 1).Rows()
	if err != nil {
		return nil, unavailable
	}
	defer rows.Close()
	flags := make(map[string]bool)
	for rows.Next() {
		if len(flags) == persistedFeatureRows {
			return nil, unavailable
		}
		var rawKey, rawEnabled any
		if err := rows.Scan(&rawKey, &rawEnabled); err != nil {
			return nil, unavailable
		}
		key, ok := rawKey.(string)
		if !ok || key == "" || len(key) > persistedFeatureKeyBytes || !utf8.ValidString(key) {
			return nil, unavailable
		}
		if _, duplicate := flags[key]; duplicate {
			return nil, unavailable
		}
		var value bool
		if dialect == "sqlite" {
			integer, ok := rawEnabled.(int64)
			if !ok || (integer != 0 && integer != 1) {
				return nil, unavailable
			}
			value = integer == 1
		} else {
			var ok bool
			value, ok = rawEnabled.(bool)
			if !ok {
				return nil, unavailable
			}
		}
		flags[key] = value
	}
	if rows.Err() != nil || rows.Close() != nil {
		return nil, unavailable
	}
	encoded, err := json.Marshal(flags)
	if err != nil || len(encoded) > persistedFeatureJSONBytes {
		return nil, unavailable
	}
	return flags, nil
}
