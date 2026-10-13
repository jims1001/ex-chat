package repository

import (
	"encoding/json"
	"errors"
	"sort"
	"unicode/utf8"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

const (
	publicBrandingValueBytes = 4096
	publicBrandingJSONBytes  = 16384
)

// PublicBrandingSetting intentionally excludes SystemConfig's administrative
// fields. The full owner model and its backup serialization remain unchanged.
type PublicBrandingSetting struct {
	ConfigKey string `json:"config_key"`
	Value     string `json:"value"`
}

// PublicBrandingSnapshot returns only persisted public branding strings. It
// never resolves defaults, environment values, boolean meanings or versions.
// The allowlist is fixed by section 8 of the installation configuration dictionary.
func (r *SystemConfigRepository) PublicBrandingSnapshot() ([]PublicBrandingSetting, error) {
	keys := []string{"INSTALLATION_NAME", "LOGO_THUMBNAIL", "LOGO", "LOGO_DARK", "BRAND_URL", "WIDGET_BRAND_URL", "BRAND_NAME", "TERMS_URL", "PRIVACY_URL", "DISPLAY_MANIFEST"}
	unavailable := errors.New("public configuration is unavailable")
	// Bound bytes before transfer from the store, including SQLite strings with
	// embedded NULs. CASE preserves the underlying value type; Scan into any
	// below must not coerce corrupt blobs/numbers/nulls into public strings.
	length := ""
	switch r.db.Dialector.Name() {
	case "sqlite":
		length = "length(CAST(value AS BLOB))"
	case "postgres":
		length = "octet_length(value)"
	default:
		return nil, unavailable
	}
	rows, err := r.db.Model(&domain.SystemConfig{}).
		Select("config_key, CASE WHEN "+length+" <= ? THEN value ELSE NULL END AS value", publicBrandingValueBytes).
		Where("config_key IN ?", keys).Order("config_key ASC").Limit(len(keys) + 1).Rows()
	if err != nil {
		return nil, unavailable
	}
	defer rows.Close()
	allowed := make(map[string]bool, len(keys))
	for _, key := range keys {
		allowed[key] = true
	}
	view := make([]PublicBrandingSetting, 0, len(keys))
	for rows.Next() {
		var rawKey, rawValue any
		if err := rows.Scan(&rawKey, &rawValue); err != nil {
			return nil, unavailable
		}
		key, keyOK := rawKey.(string)
		value, valueOK := rawValue.(string)
		if !keyOK || !valueOK || !allowed[key] || !utf8.ValidString(value) || len(value) > publicBrandingValueBytes {
			return nil, unavailable
		}
		delete(allowed, key) // Duplicate keys fail closed, even on a damaged schema.
		view = append(view, PublicBrandingSetting{ConfigKey: key, Value: value})
	}
	if rows.Err() != nil {
		return nil, unavailable
	}
	sort.Slice(view, func(i, j int) bool { return view[i].ConfigKey < view[j].ConfigKey })
	// Include JSON escaping and field names in the data limit, not merely raw
	// value lengths. Transport correlation metadata is outside this data limit.
	encoded, err := json.Marshal(view)
	if err != nil || len(encoded) > publicBrandingJSONBytes {
		return nil, unavailable
	}
	return view, nil
}
