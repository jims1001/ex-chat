package modruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/stretchr/testify/require"
	cgosqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func aicUsageQuery() foundation.QueryEnvelope {
	return foundation.QueryEnvelope{QueryType: persistedAIUsageQuery, AccountID: 91,
		Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, Filter: map[string]any{},
		Sort: []string{"account_id"}, Page: "1", Fields: append([]string{}, aiUsageFields[:]...), Consistency: "strong", RequestedAt: time.Now().UTC()}
}

func aicUsageHandler(t *testing.T, db *gorm.DB) http.Handler {
	t.Helper()
	cfg := businessConfig("AIC")
	cfg.MaxTimeout = 5 * time.Second
	r := &Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("AIC")}
	require.NoError(t, r.RegisterBusinessContracts())
	require.Len(t, r.Contracts.queries, 2)
	require.Empty(t, r.Contracts.commands)
	require.Empty(t, r.Contracts.events)
	return r.Handler()
}

func aicUsageRequest(handler http.Handler, query foundation.QueryEnvelope) *httptest.ResponseRecorder {
	return performContractRequest(handler, "/contracts/queries/"+persistedAIUsageQuery, query, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer aic-secret")
		r.Header.Set(headerCaller, "API")
	})
}

func aicUsageData(t *testing.T, response *httptest.ResponseRecorder) aiUsageView {
	t.Helper()
	require.Equal(t, 200, response.Code, response.Body.String())
	var envelope struct {
		Data aiUsageView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	var shape struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &shape))
	require.Len(t, shape.Data, 9)
	for _, field := range aiUsageFields {
		require.Contains(t, shape.Data, field)
	}
	require.NotContains(t, response.Body.String(), "private-marker")
	return envelope.Data
}

func aicUsageFailure(t *testing.T, response *httptest.ResponseRecorder, status int, kind, message string) {
	t.Helper()
	sysPublicFailure(t, response, status, kind, message)
	require.NotContains(t, response.Body.String(), `"data"`)
}

func aicUsageSeed(t *testing.T, db *gorm.DB, id, account uint) aiUsageView {
	t.Helper()
	v := aiUsageView{ID: id, AccountID: account, MonthlyTokenLimit: 0, UsedTokens: 312,
		MonthlyRequestLimit: -7, UsedRequests: 12, ResetAt: time.Date(2020, 1, 2, 3, 4, 5, 123456000, time.UTC),
		CreatedAt: time.Date(2019, 2, 3, 4, 5, 6, 456789000, time.UTC), UpdatedAt: time.Date(2021, 3, 4, 5, 6, 7, 789123000, time.UTC)}
	require.NoError(t, db.Model(&domain.AIUsageQuota{}).Create(map[string]any{
		"id": v.ID, "account_id": v.AccountID, "monthly_token_limit": v.MonthlyTokenLimit, "used_tokens": v.UsedTokens,
		"monthly_request_limit": v.MonthlyRequestLimit, "used_requests": v.UsedRequests,
		"reset_at": v.ResetAt, "created_at": v.CreatedAt, "updated_at": v.UpdatedAt,
	}).Error)
	return v
}

func aicUsageOwnerState(t *testing.T, db *gorm.DB) map[string]json.RawMessage {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, CreateSnapshot(db, "AIC", "usage-read", path))
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var file snapshotFile
	require.NoError(t, json.Unmarshal(data, &file))
	return file.Snapshot.Tables
}

func aicUsageOwnerOnly(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, foreign := range []any{&domain.User{}, &domain.Account{}, &domain.AccountUser{}, &domain.AccountFeature{}, &domain.Inbox{}, &domain.SystemConfig{}, &domain.Order{}} {
		require.False(t, db.Migrator().HasTable(foreign), "foreign table %T", foreign)
	}
}

func aicUsageReadCases(t *testing.T, db *gorm.DB) {
	t.Helper()
	handler := aicUsageHandler(t, db)
	before := aicUsageOwnerState(t, db)
	aicUsageFailure(t, aicUsageRequest(handler, aicUsageQuery()), 404, "resource_not_found", "persisted AI usage was not found")
	require.Equal(t, before, aicUsageOwnerState(t, db), "missing quota was provisioned")
	want := aicUsageSeed(t, db, 17, 91)
	other := aicUsageSeed(t, db, 18, 92)
	require.NoError(t, db.Model(&domain.AIUsageQuota{}).Where("account_id = ?", 92).UpdateColumns(map[string]any{"used_tokens": 999, "monthly_request_limit": 0}).Error)
	other.UsedTokens, other.MonthlyRequestLimit = 999, 0
	// Owner configuration may contain prompts/secrets and must never be exposed.
	require.NoError(t, db.Create(&domain.AIScenario{AccountID: 91, Name: "private-marker", SystemPrompt: "private-marker"}).Error)
	before = aicUsageOwnerState(t, db)
	for _, actor := range []foundation.ContractActor{
		{Type: "User", ID: "7", AuthSource: "module"}, {Type: "System", ID: "API", AuthSource: "module"},
		{Type: "SuperAdmin", ID: "root", AuthSource: "jwt"}, {Type: "fabricated", ID: "private-marker", AuthSource: "unverified"},
	} {
		q := aicUsageQuery()
		q.Actor = actor
		require.Equal(t, want, aicUsageData(t, aicUsageRequest(handler, q)))
		q.AccountID = 92
		require.Equal(t, other, aicUsageData(t, aicUsageRequest(handler, q)))
		q.AccountID = 999
		aicUsageFailure(t, aicUsageRequest(handler, q), 404, "resource_not_found", "persisted AI usage was not found")
	}
	require.Equal(t, before, aicUsageOwnerState(t, db), "query changed quota/configuration/journal/receipt/outbox state")
	aicUsageOwnerOnly(t, db)
}

func TestAICUsageOwnerOnly(t *testing.T) { aicUsageReadCases(t, newModuleTestDB(t, "AIC")) }

func TestAICUsageRejectsExpandedShape(t *testing.T) {
	db := newModuleTestDB(t, "AIC")
	handler := aicUsageHandler(t, db)
	aicUsageSeed(t, db, 17, 91)
	before := aicUsageOwnerState(t, db)
	mutations := map[string]func(*foundation.QueryEnvelope){
		"account_override": func(q *foundation.QueryEnvelope) { q.Filter["account_id"] = 92 },
		"command":          func(q *foundation.QueryEnvelope) { q.Filter["command_id"] = "private-marker" },
		"operation":        func(q *foundation.QueryEnvelope) { q.Filter["operation_id"] = "private-marker" },
		"status":           func(q *foundation.QueryEnvelope) { q.Filter["status"] = "completed" },
		"provider_billing": func(q *foundation.QueryEnvelope) { q.Filter["provider"] = "private-marker" },
		"wildcard":         func(q *foundation.QueryEnvelope) { q.Fields = []string{"*"} },
		"partial":          func(q *foundation.QueryEnvelope) { q.Fields = q.Fields[:8] },
		"extra":            func(q *foundation.QueryEnvelope) { q.Fields = append(q.Fields, "remaining") },
		"duplicate":        func(q *foundation.QueryEnvelope) { q.Fields[0] = q.Fields[1] },
		"reordered":        func(q *foundation.QueryEnvelope) { q.Fields[0], q.Fields[1] = q.Fields[1], q.Fields[0] },
		"sort":             func(q *foundation.QueryEnvelope) { q.Sort = []string{"account_id DESC; private-marker"} },
		"page":             func(q *foundation.QueryEnvelope) { q.Page = "2" },
		"cursor":           func(q *foundation.QueryEnvelope) { q.Cursor = "private-marker" },
		"eventual":         func(q *foundation.QueryEnvelope) { q.Consistency = "eventual" },
		"account_overflow": func(q *foundation.QueryEnvelope) { q.AccountID = ^uint(0) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			q := aicUsageQuery()
			mutate(&q)
			aicUsageFailure(t, aicUsageRequest(handler, q), 400, "validation_failed", "persisted AI usage query shape is unsupported")
		})
	}
	require.Equal(t, before, aicUsageOwnerState(t, db))
}

func TestAICUsageTransportDenials(t *testing.T) {
	db := newModuleTestDB(t, "AIC")
	handler := aicUsageHandler(t, db)
	aicUsageSeed(t, db, 17, 91)
	before := aicUsageOwnerState(t, db)
	for name, mutate := range map[string]func(*http.Request){
		"missing_token": func(r *http.Request) { r.Header.Del("Authorization") }, "wrong_token": func(r *http.Request) { r.Header.Set("Authorization", "Bearer private-marker") },
		"unknown_caller": func(r *http.Request) { r.Header.Set(headerCaller, "UNKNOWN") }, "missing_caller": func(r *http.Request) { r.Header.Del(headerCaller) },
	} {
		t.Run(name, func(t *testing.T) {
			response := performContractRequest(handler, "/contracts/queries/"+persistedAIUsageQuery, aicUsageQuery(), func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer aic-secret")
				r.Header.Set(headerCaller, "API")
				mutate(r)
			})
			aicUsageFailure(t, response, 401, "authentication_failed", "module caller authentication failed")
		})
	}
	for name, mutate := range map[string]func(*foundation.QueryEnvelope){
		"zero_account": func(q *foundation.QueryEnvelope) { q.AccountID = 0 }, "empty_actor": func(q *foundation.QueryEnvelope) { q.Actor = foundation.ContractActor{} },
		"empty_source": func(q *foundation.QueryEnvelope) { q.Actor.AuthSource = "" }, "missing_filter": func(q *foundation.QueryEnvelope) { q.Filter = nil },
		"wrong_query": func(q *foundation.QueryEnvelope) { q.QueryType = "aic.operation.status.v1" }, "no_time": func(q *foundation.QueryEnvelope) { q.RequestedAt = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			q := aicUsageQuery()
			mutate(&q)
			aicUsageFailure(t, aicUsageRequest(handler, q), 400, "validation_failed", "query envelope is invalid")
		})
	}
	body, err := json.Marshal(aicUsageQuery())
	require.NoError(t, err)
	for name, data := range map[string][]byte{
		"unknown":    bytes.Replace(body, []byte(`"filter":{}`), []byte(`"filter":{},"admin":true`), 1),
		"extra_json": append(append([]byte{}, body...), []byte(` {}`)...), "garbage": append(append([]byte{}, body...), []byte(` private-marker`)...),
		"body_limit":               bytes.Replace(body, []byte(`"filter":{}`), []byte(`"filter":{"private-marker":"`+strings.Repeat("x", 5000)+`"}`), 1),
		"account_fraction":         bytes.Replace(body, []byte(`"account_id":91`), []byte(`"account_id":91.5`), 1),
		"account_numeric_overflow": bytes.Replace(body, []byte(`"account_id":91`), []byte(`"account_id":18446744073709551616`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/contracts/queries/"+persistedAIUsageQuery, bytes.NewReader(data))
			req.Header.Set("Authorization", "Bearer aic-secret")
			req.Header.Set(headerCaller, "API")
			req.Header.Set(headerContractVersion, "v1")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			aicUsageFailure(t, response, 400, "validation_failed", "query envelope is invalid")
		})
	}
	response := performContractRequest(handler, "/contracts/queries/"+persistedAIUsageQuery, aicUsageQuery(), func(r *http.Request) { r.Header.Set(headerContractVersion, "v2") })
	aicUsageFailure(t, response, 400, "validation_failed", "contract version is unsupported")
	for _, path := range []string{"queries/aic.model.get.v1", "queries/aic.thread.get.v1", "queries/aic.account_deletion_status.get.v1", "queries/aic.usage.status.v1", "queries/aic.operation.get.v1", "commands/aic.response.generate.v1"} {
		response := performContractRequest(handler, "/contracts/"+path, aicUsageQuery(), func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer aic-secret")
			r.Header.Set(headerCaller, "API")
		})
		aicUsageFailure(t, response, 404, "resource_not_found", "")
	}
	require.Equal(t, before, aicUsageOwnerState(t, db))
}

func aicUsageNumericCases(t *testing.T, db *gorm.DB) {
	t.Helper()
	want := aicUsageSeed(t, db, 17, 91)
	want.MonthlyTokenLimit = math.MinInt64
	want.UsedTokens = math.MaxInt64
	want.MonthlyRequestLimit = math.MaxInt64
	want.UsedRequests = math.MinInt64
	require.NoError(t, db.Model(&domain.AIUsageQuota{}).Where("id = ?", 17).UpdateColumns(map[string]any{"monthly_token_limit": want.MonthlyTokenLimit, "used_tokens": want.UsedTokens, "monthly_request_limit": want.MonthlyRequestLimit, "used_requests": want.UsedRequests}).Error)
	response := aicUsageRequest(aicUsageHandler(t, db), aicUsageQuery())
	require.Equal(t, want, aicUsageData(t, response))
	require.Contains(t, response.Body.String(), `"used_tokens":9223372036854775807`)
	require.Contains(t, response.Body.String(), `"used_requests":-9223372036854775808`)
	data, err := json.Marshal(want)
	require.NoError(t, err)
	require.LessOrEqual(t, len(data), aiUsageMaxEncodedBytes)
}
func TestAICUsageNumericBounds(t *testing.T) { aicUsageNumericCases(t, newModuleTestDB(t, "AIC")) }

func aicUsageTimeCases(t *testing.T, db *gorm.DB) {
	t.Helper()
	want := aicUsageSeed(t, db, 17, 91)
	want.ResetAt = time.Time{}
	want.CreatedAt = time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)
	want.UpdatedAt = time.Date(2024, 2, 29, 12, 34, 56, 123456000, time.FixedZone("stored offset", 5*60*60+30*60)).UTC()
	require.NoError(t, db.Model(&domain.AIUsageQuota{}).Where("id = ?", 17).UpdateColumns(map[string]any{
		"reset_at": want.ResetAt, "created_at": want.CreatedAt, "updated_at": want.UpdatedAt,
	}).Error)
	require.Equal(t, want, aicUsageData(t, aicUsageRequest(aicUsageHandler(t, db), aicUsageQuery())))
}

func TestAICUsageStoredTimeBounds(t *testing.T) { aicUsageTimeCases(t, newModuleTestDB(t, "AIC")) }

func TestAICUsageMalformedStorage(t *testing.T) {
	for _, field := range []string{"monthly_token_limit", "used_tokens", "monthly_request_limit", "used_requests", "reset_at", "created_at", "updated_at"} {
		for name, value := range map[string]any{"null": nil, "text": "private-marker", "blob": []byte("private-marker"), "real": 1.25, "overflow": strings.Repeat("9", 5000)} {
			t.Run(field+"/"+name, func(t *testing.T) {
				db := newModuleTestDB(t, "AIC")
				aicUsageSeed(t, db, 17, 91)
				require.NoError(t, db.Exec("UPDATE ai_usage_quota SET "+field+" = ?", value).Error)
				aicUsageFailure(t, aicUsageRequest(aicUsageHandler(t, db), aicUsageQuery()), 503, "dependency_unavailable", "persisted AI usage is unavailable")
			})
		}
	}
}

func TestAICUsageTimestampValidation(t *testing.T) {
	for _, value := range []string{"", "2024-02-30 12:00:00", "2024-01-01T24:00:00Z", "2024-01-01T00:00:00+24:00", "2024-01-01T00:00:00+00:60", "2024-01-01T00:00:00.1234567890Z", "0000-01-01T00:00:00Z", "0001-01-01T00:00:00+01:00", "9999-12-31T23:59:59-01:00", "infinity", "12345-01-01 00:00:00", "2024-01-01\x00private-marker"} {
		_, ok := parseAIUsageTime(value)
		require.False(t, ok, "accepted invalid time %q", value)
	}
	for _, value := range []string{"0001-01-01T00:00:00Z", "9999-12-31T23:59:59.999999999Z", "2024-02-29 12:34:56.123456", "2024-02-29T12:34:56.123456789+05:30", "2024-02-29 12:34:56+00", "2024-02-29T12:34:56+05", "2024-02-29"} {
		_, ok := parseAIUsageTime(value)
		require.True(t, ok, "rejected valid time %q", value)
	}
	// Establish the actual SQLite driver's coercion, then prove the bounded
	// text projection rejects that same corrupt persisted value.
	db := newModuleTestDB(t, "AIC")
	aicUsageSeed(t, db, 17, 91)
	require.NoError(t, db.Exec("UPDATE ai_usage_quota SET reset_at = ?", "private-marker-invalid-date").Error)
	var legacy domain.AIUsageQuota
	// The independent runtime uses glebarez, which returns a scan error. The
	// other existing SQLite driver silently coerces the same corrupt value.
	require.Error(t, db.First(&legacy).Error)
	aicUsageFailure(t, aicUsageRequest(aicUsageHandler(t, db), aicUsageQuery()), 503, "dependency_unavailable", "persisted AI usage is unavailable")
	cgoDB, err := gorm.Open(cgosqlite.Open(filepath.Join(t.TempDir(), "quota.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, cgoDB.AutoMigrate(&domain.AIUsageQuota{}))
	aicUsageSeed(t, cgoDB, 17, 91)
	require.NoError(t, cgoDB.Exec("UPDATE ai_usage_quota SET reset_at = ?", "private-marker-invalid-date").Error)
	legacy = domain.AIUsageQuota{}
	require.NoError(t, cgoDB.First(&legacy).Error)
	require.True(t, legacy.ResetAt.IsZero())
	aicUsageFailure(t, aicUsageRequest(aicUsageHandler(t, cgoDB), aicUsageQuery()), 503, "dependency_unavailable", "persisted AI usage is unavailable")
}

func TestAICUsageStoreFailuresAndDuplicates(t *testing.T) {
	for _, kind := range []string{"missing_table", "missing_column", "duplicate", "invalid_id"} {
		t.Run(kind, func(t *testing.T) {
			db := newModuleTestDB(t, "AIC")
			aicUsageSeed(t, db, 17, 91)
			switch kind {
			case "missing_table":
				require.NoError(t, db.Migrator().DropTable(&domain.AIUsageQuota{}))
			case "missing_column":
				require.NoError(t, db.Exec("ALTER TABLE ai_usage_quota DROP COLUMN reset_at").Error)
			case "duplicate":
				require.NoError(t, db.Exec("DROP INDEX idx_ai_usage_quota_account_id").Error)
				aicUsageSeed(t, db, 18, 91)
			case "invalid_id":
				require.NoError(t, db.Exec("UPDATE ai_usage_quota SET id = -1").Error)
			}
			aicUsageFailure(t, aicUsageRequest(aicUsageHandler(t, db), aicUsageQuery()), 503, "dependency_unavailable", "persisted AI usage is unavailable")
		})
	}
}

func aicUsageConcurrentCases(t *testing.T, db *gorm.DB) {
	t.Helper()
	want := aicUsageSeed(t, db, 17, 91)
	handler := aicUsageHandler(t, db)
	before := aicUsageOwnerState(t, db)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := aicUsageRequest(handler, aicUsageQuery())
			if response.Code != 200 {
				t.Errorf("concurrent read status %d", response.Code)
				return
			}
			var envelope struct {
				Data aiUsageView `json:"data"`
			}
			if json.Unmarshal(response.Body.Bytes(), &envelope) != nil || envelope.Data != want {
				t.Error("concurrent read differed")
			}
		}()
	}
	wg.Wait()
	require.Equal(t, before, aicUsageOwnerState(t, db))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	value, err := readPersistedAIUsage(ctx, db, 91)
	require.Nil(t, value)
	require.EqualError(t, err, "persisted AI usage is unavailable")
}
func TestAICUsageConcurrentAndCancelledReads(t *testing.T) {
	aicUsageConcurrentCases(t, newModuleTestDB(t, "AIC"))
}

func TestAICUsageSQLiteReadOnly(t *testing.T) {
	db := newModuleTestDB(t, "AIC")
	want := aicUsageSeed(t, db, 17, 91)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, tx.Exec("PRAGMA query_only = ON").Error)
		defer tx.Exec("PRAGMA query_only = OFF")
		view, err := readPersistedAIUsage(context.Background(), tx, 91)
		require.Equal(t, want, view)
		require.NoError(t, err)
		view, err = readPersistedAIUsage(context.Background(), tx, 999)
		require.Nil(t, view)
		require.EqualError(t, err, "persisted AI usage was not found")
		return nil
	}))
}

func TestPostgresAICUsage(t *testing.T) {
	t.Run("owner_only", func(t *testing.T) { aicUsageReadCases(t, postgresModuleDB(t, "AIC")) })
	t.Run("time_bounds", func(t *testing.T) { aicUsageTimeCases(t, postgresModuleDB(t, "AIC")) })
	for _, zone := range []string{"UTC", "Asia/Yerevan", "Asia/Shanghai"} {
		t.Run("session_timezone/"+zone, func(t *testing.T) {
			db := postgresModuleDB(t, "AIC")
			require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
				// SET LOCAL and all reads use this transaction's single session.
				require.NoError(t, tx.Exec("SELECT set_config('TimeZone', ?, true)", zone).Error)
				var actual string
				require.NoError(t, tx.Raw("SHOW TimeZone").Scan(&actual).Error)
				require.Equal(t, zone, actual)
				aicUsageTimeCases(t, tx)
				if zone == "Asia/Shanghai" {
					var rendered string
					require.NoError(t, tx.Raw("SELECT CAST(reset_at AS TEXT) FROM ai_usage_quota WHERE id = 17").Scan(&rendered).Error)
					require.Contains(t, rendered, "+08:05:43", "exercise the historical second-resolution offset")
				}
				// True UTC out-of-range instants must still fail even when their
				// session-local representation can fall inside years 1..9999.
				for _, invalid := range []string{"0001-12-31 23:59:59.999999+00 BC", "10000-01-01 00:00:00+00", "infinity", "-infinity"} {
					require.NoError(t, tx.Exec("UPDATE ai_usage_quota SET reset_at = CAST(? AS TIMESTAMPTZ) WHERE id = 17", invalid).Error)
					aicUsageFailure(t, aicUsageRequest(aicUsageHandler(t, tx), aicUsageQuery()), 503, "dependency_unavailable", "persisted AI usage is unavailable")
				}
				return nil
			}))
		})
	}
	t.Run("numeric_bounds", func(t *testing.T) { aicUsageNumericCases(t, postgresModuleDB(t, "AIC")) })
	t.Run("concurrent_and_cancelled", func(t *testing.T) { aicUsageConcurrentCases(t, postgresModuleDB(t, "AIC")) })
	t.Run("cancelled_database_wait", func(t *testing.T) {
		db := postgresModuleDB(t, "AIC")
		aicUsageSeed(t, db, 17, 91)
		blocker := db.Begin()
		require.NoError(t, blocker.Error)
		defer blocker.Rollback()
		require.NoError(t, blocker.Exec("LOCK TABLE ai_usage_quota IN ACCESS EXCLUSIVE MODE").Error)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		view, err := readPersistedAIUsage(ctx, db, 91)
		require.Nil(t, view)
		require.EqualError(t, err, "persisted AI usage is unavailable")
		require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	})
	t.Run("unsupported_date_style", func(t *testing.T) {
		db := postgresModuleDB(t, "AIC")
		aicUsageSeed(t, db, 17, 91)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			require.NoError(t, tx.Exec("SET LOCAL DateStyle TO SQL, DMY").Error)
			view, err := readPersistedAIUsage(context.Background(), tx, 91)
			require.Nil(t, view)
			require.EqualError(t, err, "persisted AI usage is unavailable")
			return nil
		}))
	})
	t.Run("read_only", func(t *testing.T) {
		db := postgresModuleDB(t, "AIC")
		want := aicUsageSeed(t, db, 17, 91)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			require.NoError(t, tx.Exec("SET TRANSACTION READ ONLY").Error)
			view, err := readPersistedAIUsage(context.Background(), tx, 91)
			require.Equal(t, want, view)
			require.NoError(t, err)
			view, err = readPersistedAIUsage(context.Background(), tx, 999)
			require.Nil(t, view)
			require.EqualError(t, err, "persisted AI usage was not found")
			return nil
		}))
	})
	for _, kind := range []string{"null", "infinity", "out_of_range_time", "duplicate", "missing_table"} {
		t.Run(kind, func(t *testing.T) {
			db := postgresModuleDB(t, "AIC")
			aicUsageSeed(t, db, 17, 91)
			switch kind {
			case "null":
				require.NoError(t, db.Exec("UPDATE ai_usage_quota SET reset_at = NULL").Error)
			case "infinity":
				require.NoError(t, db.Exec("UPDATE ai_usage_quota SET reset_at = 'infinity'").Error)
			case "out_of_range_time":
				require.NoError(t, db.Exec("UPDATE ai_usage_quota SET reset_at = '12345-01-01'").Error)
			case "duplicate":
				require.NoError(t, db.Exec("DROP INDEX idx_ai_usage_quota_account_id").Error)
				aicUsageSeed(t, db, 18, 91)
			case "missing_table":
				require.NoError(t, db.Migrator().DropTable(&domain.AIUsageQuota{}))
			}
			aicUsageFailure(t, aicUsageRequest(aicUsageHandler(t, db), aicUsageQuery()), 503, "dependency_unavailable", "persisted AI usage is unavailable")
		})
	}
}
