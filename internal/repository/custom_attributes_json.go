package repository

import (
	"encoding/json"
	"errors"
	"strings"
)

// unmarshalCustomAttributeObject retains stored JSON numbers when an unrelated
// key is merged or deleted. It preserves their JSON type and numeric text; no
// numeric-string conversion or numeric comparison is performed here.
func unmarshalCustomAttributeObject(raw string, value *map[string]any) error {
	// Unlike Decoder.Decode, Unmarshal rejected trailing data before touching
	// its destination. Preserve that behavior for existing persisted payloads.
	if !json.Valid([]byte(raw)) {
		return errors.New("invalid custom attributes JSON")
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(value)
}
