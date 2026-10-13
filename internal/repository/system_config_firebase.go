package repository

import (
	"context"
	"errors"
	"unicode/utf8"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

// FirebasePushConfiguration is a private, local SYS capability. It exposes only
// the persisted Firebase pair to the composed push sender, never public config,
// environment defaults, arbitrary keys, or an independent transport endpoint.
func (r *SystemConfigRepository) FirebasePushConfiguration(ctx context.Context) (string, string, error) {
	unavailable := errors.New("Firebase configuration is unavailable")
	length := ""
	switch r.db.Dialector.Name() {
	case "sqlite":
		length = "length(CAST(value AS BLOB))"
	case "postgres":
		length = "octet_length(value)"
	default:
		return "", "", unavailable
	}
	rows, err := r.db.WithContext(ctx).Model(&domain.SystemConfig{}).
		Select("config_key, CASE WHEN "+length+" <= ? THEN value ELSE NULL END AS value", 32768).
		Where("config_key IN ?", []string{"FIREBASE_PROJECT_ID", "FIREBASE_CREDENTIALS"}).Limit(3).Rows()
	if err != nil {
		return "", "", unavailable
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var rawKey, rawValue any
		if rows.Scan(&rawKey, &rawValue) != nil {
			return "", "", unavailable
		}
		key, keyOK := rawKey.(string)
		value, valueOK := rawValue.(string)
		_, duplicate := values[key]
		if !keyOK || !valueOK || duplicate || (key != "FIREBASE_PROJECT_ID" && key != "FIREBASE_CREDENTIALS") || !utf8.ValidString(value) || len(value) > 32768 {
			return "", "", unavailable
		}
		values[key] = value
	}
	if rows.Err() != nil {
		return "", "", unavailable
	}
	return values["FIREBASE_PROJECT_ID"], values["FIREBASE_CREDENTIALS"], nil
}
