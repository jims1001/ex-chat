package modruntime

import (
	"context"
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const persistedAIUsageQuery = "aic.usage.get.v1"
const aiUsageMaxEncodedBytes = 4096

var aiUsageFields = [...]string{"id", "account_id", "monthly_token_limit", "used_tokens", "monthly_request_limit", "used_requests", "reset_at", "created_at", "updated_at"}

// aiUsageView is only persisted application accounting. It makes no provider
// billing, availability, entitlement or generation-completion assertion.
type aiUsageView struct {
	ID                  uint      `json:"id"`
	AccountID           uint      `json:"account_id"`
	MonthlyTokenLimit   int64     `json:"monthly_token_limit"`
	UsedTokens          int64     `json:"used_tokens"`
	MonthlyRequestLimit int64     `json:"monthly_request_limit"`
	UsedRequests        int64     `json:"used_requests"`
	ResetAt             time.Time `json:"reset_at"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

func (r *Runtime) registerPersistedAIUsage() error {
	return r.Contracts.RegisterQuery(persistedAIUsageQuery, func(ctx context.Context, query foundation.QueryEnvelope) (any, error) {
		// The trusted module asserts account scope. Actor claims never grant
		// global authority; the gateway must authenticate current tenant access.
		valid := query.Validate() == nil && query.QueryType == persistedAIUsageQuery && uint64(query.AccountID) <= math.MaxInt64 &&
			len(query.Filter) == 0 && len(query.Sort) == 1 && query.Sort[0] == "account_id" && query.Page == "1" && query.Cursor == "" &&
			len(query.Fields) == len(aiUsageFields) && query.Consistency == "strong"
		if valid {
			for i, field := range aiUsageFields {
				if query.Fields[i] != field {
					valid = false
					break
				}
			}
		}
		if !valid {
			return nil, &ContractFailure{Type: "validation_failed", Message: "persisted AI usage query shape is unsupported"}
		}
		return readPersistedAIUsage(ctx, r.DB, query.AccountID)
	})
}

func aiUsageUnavailable() error {
	return &ContractFailure{Type: "dependency_unavailable", Message: "persisted AI usage is unavailable"}
}

func readPersistedAIUsage(ctx context.Context, db *gorm.DB, accountID uint) (any, error) {
	if db == nil || accountID == 0 || uint64(accountID) > math.MaxInt64 || ctx.Err() != nil {
		return nil, aiUsageUnavailable()
	}
	// Raw numeric scans reject NULL, real, text and blob values. SQL guards
	// bound even corrupt storage before transfer. SQLite timestamp columns
	// must be cast to text: some SQLite drivers silently return time.Time{}
	// for malformed strings declared as datetime.
	columns := make([]string, 0, len(aiUsageFields))
	for i, field := range aiUsageFields {
		switch db.Dialector.Name() {
		case "sqlite":
			if i < 6 {
				columns = append(columns, "CASE WHEN typeof("+field+") = 'integer' THEN "+field+" ELSE NULL END AS "+field)
			} else {
				columns = append(columns, "CASE WHEN typeof("+field+") = 'text' AND length(CAST("+field+" AS BLOB)) <= 64 THEN CAST("+field+" AS TEXT) ELSE NULL END AS "+field)
			}
		case "postgres":
			if i < 6 {
				columns = append(columns, "CASE WHEN octet_length(CAST("+field+" AS TEXT)) <= 20 THEN "+field+" ELSE NULL END AS "+field)
			} else {
				// These timestamptz columns represent instants. Normalize before
				// text conversion so session offsets cannot move UTC bounds into
				// another year or introduce historical second-resolution offsets.
				// Keep the bounded text scan and strict date/range validation.
				text := "CAST((" + field + " AT TIME ZONE 'UTC') AS TEXT)"
				columns = append(columns, "CASE WHEN octet_length("+text+") <= 64 THEN "+text+" ELSE NULL END AS "+field)
			}
		default:
			return nil, aiUsageUnavailable()
		}
	}
	rows, err := db.WithContext(ctx).Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).
		Model(&domain.AIUsageQuota{}).Select(strings.Join(columns, ", ")).Where("account_id = ?", accountID).Order("account_id ASC").Limit(2).Rows()
	if err != nil {
		return nil, aiUsageUnavailable()
	}
	defer rows.Close()
	var view aiUsageView
	count := 0
	for rows.Next() {
		count++
		if count > 1 {
			return nil, aiUsageUnavailable()
		}
		var raw [9]any
		if err := rows.Scan(&raw[0], &raw[1], &raw[2], &raw[3], &raw[4], &raw[5], &raw[6], &raw[7], &raw[8]); err != nil {
			return nil, aiUsageUnavailable()
		}
		var numbers [6]int64
		for i := range numbers {
			value, ok := raw[i].(int64)
			if !ok {
				return nil, aiUsageUnavailable()
			}
			numbers[i] = value
		}
		if numbers[0] <= 0 || numbers[1] <= 0 || uint64(numbers[0]) > uint64(^uint(0)) || uint64(numbers[1]) != uint64(accountID) {
			return nil, aiUsageUnavailable()
		}
		var timestamps [3]time.Time
		for i := range timestamps {
			value, ok := raw[i+6].(string)
			if !ok {
				return nil, aiUsageUnavailable()
			}
			parsed, ok := parseAIUsageTime(value)
			if !ok {
				return nil, aiUsageUnavailable()
			}
			timestamps[i] = parsed
		}
		view = aiUsageView{ID: uint(numbers[0]), AccountID: accountID, MonthlyTokenLimit: numbers[2], UsedTokens: numbers[3],
			MonthlyRequestLimit: numbers[4], UsedRequests: numbers[5], ResetAt: timestamps[0], CreatedAt: timestamps[1], UpdatedAt: timestamps[2]}
	}
	if rows.Err() != nil || rows.Close() != nil || ctx.Err() != nil {
		return nil, aiUsageUnavailable()
	}
	if count == 0 {
		return nil, &ContractFailure{Type: "resource_not_found", Message: "persisted AI usage was not found"}
	}
	encoded, err := json.Marshal(view)
	if err != nil || len(encoded) > aiUsageMaxEncodedBytes {
		return nil, aiUsageUnavailable()
	}
	return view, nil
}

var aiUsageTimeText = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}(?:[ T][0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,9})?(?:Z|[+-](?:[01][0-9]|2[0-3])(?::[0-5][0-9])?)?)?$`)

func parseAIUsageTime(value string) (time.Time, bool) {
	if len(value) == 0 || len(value) > 64 || !aiUsageTimeText.MatchString(value) {
		return time.Time{}, false
	}
	// SQLite's supported text forms and PostgreSQL's ISO timestamp output.
	// No epoch, locale, infinity, BC or normalized malformed-date fallback.
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999-07", "2006-01-02T15:04:05.999999999-07", "2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999", "2006-01-02"} {
		parsed, err := time.Parse(layout, value)
		if err == nil && parsed.Year() >= 1 && parsed.Year() <= 9999 {
			_, offset := parsed.Zone()
			if offset > -24*60*60 && offset < 24*60*60 && parsed.UTC().Year() >= 1 && parsed.UTC().Year() <= 9999 {
				return parsed.UTC(), true
			}
		}
	}
	return time.Time{}, false
}
