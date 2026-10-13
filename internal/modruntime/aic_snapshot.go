package modruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

func invalidAIUsageSnapshot() error {
	return errors.New("invalid AI usage snapshot checkpoint")
}

// snapshotAIUsageRows reads the quota's persisted values without struct-scan
// coercions: a stored NULL limit must not become a valid unlimited zero limit.
// Like the surrounding owner snapshot, this requires quiesced owner writes.
func snapshotAIUsageRows(db *gorm.DB) ([]byte, error) {
	column := "account_id"
	switch db.Dialector.Name() {
	case "sqlite":
		column = "CASE WHEN typeof(account_id) = 'integer' THEN account_id ELSE NULL END AS account_id"
	case "postgres":
		column = "CASE WHEN octet_length(CAST(account_id AS TEXT)) <= 20 THEN account_id ELSE NULL END AS account_id"
	default:
		return nil, invalidAIUsageSnapshot()
	}
	// Validate the complete projection even when a damaged source is empty.
	// Otherwise absent quota columns could be silently backed up as [].
	projection, err := db.Model(&domain.AIUsageQuota{}).Select(aiUsageFields[:]).Where("1 = 0").Rows()
	if err != nil {
		return nil, invalidAIUsageSnapshot()
	}
	if err := projection.Close(); err != nil || projection.Err() != nil {
		return nil, invalidAIUsageSnapshot()
	}
	rows, err := db.Model(&domain.AIUsageQuota{}).Select(column).Order("id ASC").Rows()
	if err != nil {
		return nil, invalidAIUsageSnapshot()
	}
	defer rows.Close()
	accounts := make([]uint, 0)
	seen := make(map[uint]bool)
	for rows.Next() {
		var raw any
		if err := rows.Scan(&raw); err != nil {
			return nil, invalidAIUsageSnapshot()
		}
		account, ok := raw.(int64)
		if !ok || account <= 0 || uint64(account) > uint64(^uint(0)) || seen[uint(account)] {
			return nil, invalidAIUsageSnapshot()
		}
		seen[uint(account)] = true
		accounts = append(accounts, uint(account))
	}
	if rows.Err() != nil || rows.Close() != nil {
		return nil, invalidAIUsageSnapshot()
	}
	views := make([]aiUsageView, 0, len(accounts))
	for _, account := range accounts {
		value, err := readPersistedAIUsage(db.Statement.Context, db, account)
		if err != nil {
			return nil, invalidAIUsageSnapshot()
		}
		view, ok := value.(aiUsageView)
		if !ok {
			return nil, invalidAIUsageSnapshot()
		}
		views = append(views, view)
	}
	payload, err := json.Marshal(views)
	if err != nil {
		return nil, invalidAIUsageSnapshot()
	}
	// Enforce the same identity, integer and timestamp representation on the
	// source as on restore, including duplicate IDs across distinct accounts.
	var validated []domain.AIUsageQuota
	if err := unmarshalAIUsageSnapshot(payload, &validated); err != nil {
		return nil, err
	}
	return payload, nil
}

// Every scalar is required, including explicit zero limits and zero times.
// The standard struct decoder alone accepts missing/null fields, duplicate
// keys, and case aliases; none prove a complete persisted quota checkpoint.
func unmarshalAIUsageSnapshot(raw []byte, target *[]domain.AIUsageQuota) error {
	var objects []json.RawMessage
	if target == nil || json.Unmarshal(raw, &objects) != nil || objects == nil {
		return invalidAIUsageSnapshot()
	}
	rows := make([]domain.AIUsageQuota, 0, len(objects))
	ids, accounts := make(map[uint]bool), make(map[uint]bool)
	for _, object := range objects {
		decoder := json.NewDecoder(bytes.NewReader(object))
		opening, err := decoder.Token()
		if err != nil || opening != json.Delim('{') {
			return invalidAIUsageSnapshot()
		}
		seen := make(map[string]bool, len(aiUsageFields))
		for decoder.More() {
			token, err := decoder.Token()
			field, ok := token.(string)
			if err != nil || !ok || seen[field] {
				return invalidAIUsageSnapshot()
			}
			index := -1
			for i, allowed := range aiUsageFields {
				if field == allowed {
					index = i
					break
				}
			}
			if index == -1 {
				return invalidAIUsageSnapshot()
			}
			seen[field] = true
			var value json.RawMessage
			if decoder.Decode(&value) != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
				return invalidAIUsageSnapshot()
			}
			if index >= 6 {
				var timestamp string
				if json.Unmarshal(value, &timestamp) != nil {
					return invalidAIUsageSnapshot()
				}
				if _, valid := parseAIUsageTime(timestamp); !valid {
					return invalidAIUsageSnapshot()
				}
			}
		}
		if closing, err := decoder.Token(); err != nil || closing != json.Delim('}') || len(seen) != len(aiUsageFields) {
			return invalidAIUsageSnapshot()
		}
		var row domain.AIUsageQuota
		if json.Unmarshal(object, &row) != nil || row.ID == 0 || row.AccountID == 0 || uint64(row.ID) > math.MaxInt64 || uint64(row.AccountID) > math.MaxInt64 || ids[row.ID] || accounts[row.AccountID] {
			return invalidAIUsageSnapshot()
		}
		ids[row.ID], accounts[row.AccountID] = true, true
		rows = append(rows, row)
	}
	*target = rows
	return nil
}

// Only AIC quota recovery bypasses model defaults and automatic timestamps.
// Normal quota creation and policy keep their existing behavior.
func restoreAIUsageSnapshotRows(tx *gorm.DB, rows any) (bool, error) {
	quotas, ok := rows.(*[]domain.AIUsageQuota)
	if !ok {
		return false, nil
	}
	for _, row := range *quotas {
		// PostgreSQL timestamp storage has microsecond precision. A checkpoint
		// from a nanosecond-capable source must not be rounded during recovery.
		if tx.Dialector.Name() == "postgres" && (row.ResetAt.Nanosecond()%1000 != 0 || row.CreatedAt.Nanosecond()%1000 != 0 || row.UpdatedAt.Nanosecond()%1000 != 0) {
			return true, invalidAIUsageSnapshot()
		}
		if err := tx.Model(&domain.AIUsageQuota{}).Create(map[string]any{
			"id": row.ID, "account_id": row.AccountID,
			"monthly_token_limit": row.MonthlyTokenLimit, "used_tokens": row.UsedTokens,
			"monthly_request_limit": row.MonthlyRequestLimit, "used_requests": row.UsedRequests,
			"reset_at": row.ResetAt, "created_at": row.CreatedAt, "updated_at": row.UpdatedAt,
		}).Error; err != nil {
			return true, err
		}
	}
	return true, nil
}
