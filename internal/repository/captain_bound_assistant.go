package repository

import (
	"context"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// BoundAssistantSummary describes only the stored inbox binding, not whether
// the assistant is active, entitled, available or able to generate a response.
type BoundAssistantSummary struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

var errBoundAssistantUnavailable = errors.New("bound assistant is unavailable")

func (r *CaptainRepository) readBoundAssistant(ctx context.Context, accountID, inboxID uint) (*BoundAssistantSummary, error) {
	if r == nil || r.db == nil || ctx.Err() != nil || accountID == 0 || inboxID == 0 || uint64(accountID) > math.MaxInt64 || uint64(inboxID) > math.MaxInt64 {
		return nil, errBoundAssistantUnavailable
	}
	// One statement observes the binding and referenced assistant together. The
	// second row is only a duplicate sentinel; neither full models nor foreign
	// CHN inboxes, configuration, prompts or provider data are loaded.
	fields := []string{"binding.id", "binding.account_id", "binding.inbox_id", "binding.captain_assistant_id", "assistant.id", "assistant.account_id"}
	columns := make([]string, 0, len(fields)+1)
	for _, field := range fields {
		switch r.db.Dialector.Name() {
		case "sqlite":
			columns = append(columns, "CASE WHEN typeof("+field+") = 'integer' THEN "+field+" ELSE NULL END")
		case "postgres":
			columns = append(columns, "CASE WHEN octet_length(CAST("+field+" AS TEXT)) <= 20 THEN "+field+" ELSE NULL END")
		default:
			return nil, errBoundAssistantUnavailable
		}
	}
	if r.db.Dialector.Name() == "sqlite" {
		columns = append(columns, "CASE WHEN typeof(assistant.name) = 'text' AND length(CAST(assistant.name AS BLOB)) <= 1020 THEN assistant.name ELSE NULL END")
	} else {
		columns = append(columns, "CASE WHEN octet_length(assistant.name) <= 1020 THEN assistant.name ELSE NULL END")
	}
	rows, err := r.db.WithContext(ctx).Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).
		Raw("SELECT "+strings.Join(columns, ", ")+" FROM captain_inboxes AS binding LEFT JOIN captain_assistants AS assistant ON assistant.id = binding.captain_assistant_id AND assistant.account_id = binding.account_id WHERE binding.account_id = ? AND binding.inbox_id = ? LIMIT 2", accountID, inboxID).Rows()
	if err != nil {
		return nil, errBoundAssistantUnavailable
	}
	defer rows.Close()
	var result *BoundAssistantSummary
	for rows.Next() {
		if result != nil {
			return nil, errBoundAssistantUnavailable
		}
		var raw [7]any
		if err := rows.Scan(&raw[0], &raw[1], &raw[2], &raw[3], &raw[4], &raw[5], &raw[6]); err != nil {
			return nil, errBoundAssistantUnavailable
		}
		var ids [6]uint
		for i := range ids {
			value, ok := raw[i].(int64)
			if !ok || value <= 0 || uint64(value) > uint64(^uint(0)) {
				return nil, errBoundAssistantUnavailable
			}
			ids[i] = uint(value)
		}
		name, ok := raw[6].(string)
		if !ok || name == "" || !utf8.ValidString(name) || len(name) > 1020 || utf8.RuneCountInString(name) > 255 ||
			ids[1] != accountID || ids[2] != inboxID || ids[3] != ids[4] || ids[5] != accountID {
			return nil, errBoundAssistantUnavailable
		}
		result = &BoundAssistantSummary{ID: ids[4], Name: name}
	}
	if rows.Err() != nil || rows.Close() != nil || ctx.Err() != nil {
		return nil, errBoundAssistantUnavailable
	}
	return result, nil
}
