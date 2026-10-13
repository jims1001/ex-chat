package modruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func entFeaturesQuery() foundation.QueryEnvelope {
	return foundation.QueryEnvelope{QueryType: persistedFeatureFlagsQuery, AccountID: 91,
		Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, Filter: map[string]any{},
		Sort: []string{"feature_name"}, Page: "1", Fields: []string{"feature_name", "enabled"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
}

func entFeaturesHandler(t *testing.T, db *gorm.DB) http.Handler {
	t.Helper()
	cfg := businessConfig("ENT")
	cfg.MaxTimeout = 5 * time.Second
	runtime := &Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("ENT")}
	require.NoError(t, runtime.RegisterBusinessContracts())
	require.Len(t, runtime.Contracts.queries, 2)
	require.Empty(t, runtime.Contracts.commands)
	require.Empty(t, runtime.Contracts.events)
	return runtime.Handler()
}

func entFeaturesRequest(handler http.Handler, q foundation.QueryEnvelope, mutate ...func(*http.Request)) *httptest.ResponseRecorder {
	return performContractRequest(handler, "/contracts/queries/"+persistedFeatureFlagsQuery, q, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer ent-secret")
		r.Header.Set(headerCaller, "API")
		for _, fn := range mutate {
			fn(r)
		}
	})
}

func entFeaturesData(t *testing.T, w *httptest.ResponseRecorder) map[string]bool {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var envelope struct {
		Data map[string]bool `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.NotNil(t, envelope.Data)
	return envelope.Data
}

func entFeaturesSeed(t *testing.T, db *gorm.DB, account uint, key string, enabled bool) {
	t.Helper()
	// Explicit map insertion preserves false against AccountFeature's true default.
	require.NoError(t, db.Model(&domain.AccountFeature{}).Create(map[string]any{"account_id": account, "feature_name": key, "enabled": enabled, "updated_at": time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)}).Error)
}

func entFeaturesState(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var flags []domain.AccountFeature
	var limits []domain.AccountLimit
	var layouts []domain.BrandedEmailLayout
	var journal []domain.LocalChangeJournal
	require.NoError(t, db.Order("id ASC").Find(&flags).Error)
	require.NoError(t, db.Order("id ASC").Find(&limits).Error)
	require.NoError(t, db.Order("id ASC").Find(&layouts).Error)
	require.NoError(t, db.Order("id ASC").Find(&journal).Error)
	encoded, err := json.Marshal([]any{flags, limits, layouts, journal})
	require.NoError(t, err)
	return string(encoded)
}

func entFeaturesOwnerOnly(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, foreign := range []any{&domain.User{}, &domain.Account{}, &domain.AccountUser{}, &domain.SystemConfig{}, &domain.Inbox{}, &domain.CustomFilter{}} {
		require.False(t, db.Migrator().HasTable(foreign), "foreign table %T", foreign)
	}
}

func entFeaturesReadCases(t *testing.T, db *gorm.DB) {
	t.Helper()
	handler := entFeaturesHandler(t, db)
	require.Empty(t, entFeaturesData(t, entFeaturesRequest(handler, entFeaturesQuery())))
	for key, enabled := range map[string]bool{"captain": true, "audit_logs": false, "unknown_future_extension": true, " Mixed_CASE key ": false} {
		entFeaturesSeed(t, db, 91, key, enabled)
	}
	entFeaturesSeed(t, db, 92, "other-account-private-marker", true)
	// Oversized unrelated rows must not fail this tenant's snapshot (within PG varchar100).
	for i := 0; i < 514; i++ {
		entFeaturesSeed(t, db, 92, fmt.Sprintf("other-%04d", i), true)
	}
	before := entFeaturesState(t, db)
	expected := map[string]bool{"captain": true, "audit_logs": false, "unknown_future_extension": true, " Mixed_CASE key ": false}
	var first []byte
	for _, actor := range []foundation.ContractActor{
		{Type: "User", ID: "7", AuthSource: "module"}, {Type: "System", ID: "api", AuthSource: "module"},
		{Type: "SuperAdmin", ID: "root", AuthSource: "jwt"}, {Type: "unverified", ID: "private-marker", AuthSource: "caller-asserted"},
	} {
		q := entFeaturesQuery()
		q.Actor = actor
		w := entFeaturesRequest(handler, q)
		require.Equal(t, expected, entFeaturesData(t, w))
		var raw map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
		if first == nil {
			first = raw["data"]
		} else {
			require.Equal(t, string(first), string(raw["data"]))
		}
		for _, hidden := range []string{"private-marker", `"id"`, `"updated_at"`, `"account_id"`, `"actor"`, `"license"`} {
			require.NotContains(t, w.Body.String(), hidden)
		}
	}
	q := entFeaturesQuery()
	q.AccountID = 93
	require.Empty(t, entFeaturesData(t, entFeaturesRequest(handler, q)))
	// Existing capability-check output and false behavior remain untouched.
	q = entFeaturesQuery()
	q.QueryType = "ent.capability.check.v1"
	q.Filter = map[string]any{"capability": "audit_logs"}
	w := performContractRequest(handler, "/contracts/queries/ent.capability.check.v1", q, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer ent-secret")
		r.Header.Set(headerCaller, "API")
	})
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"allowed":false`)
	require.Equal(t, before, entFeaturesState(t, db))
	entFeaturesOwnerOnly(t, db)
}

func TestENTPersistedFeatureFlagsOwnerOnly(t *testing.T) {
	entFeaturesReadCases(t, newModuleTestDB(t, "ENT"))
}

func TestENTPersistedFeatureFlagsQueryShape(t *testing.T) {
	db := newModuleTestDB(t, "ENT")
	handler := entFeaturesHandler(t, db)
	before := entFeaturesState(t, db)
	for name, mutate := range map[string]func(*foundation.QueryEnvelope){
		"account_filter":   func(q *foundation.QueryEnvelope) { q.Filter["account_id"] = 92 },
		"key_filter":       func(q *foundation.QueryEnvelope) { q.Filter["feature_name"] = "captain" },
		"effective":        func(q *foundation.QueryEnvelope) { q.Filter["effective"] = true },
		"arbitrary_filter": func(q *foundation.QueryEnvelope) { q.Filter["private-marker"] = "private-marker" },
		"sort":             func(q *foundation.QueryEnvelope) { q.Sort = []string{"-feature_name"} },
		"additional_sort":  func(q *foundation.QueryEnvelope) { q.Sort = append(q.Sort, "id") },
		"page":             func(q *foundation.QueryEnvelope) { q.Page = "2" },
		"cursor":           func(q *foundation.QueryEnvelope) { q.Cursor = "private-marker" },
		"fields":           func(q *foundation.QueryEnvelope) { q.Fields = []string{"enabled", "feature_name"} },
		"extra_field":      func(q *foundation.QueryEnvelope) { q.Fields = append(q.Fields, "updated_at") },
		"consistency":      func(q *foundation.QueryEnvelope) { q.Consistency = "eventual" },
	} {
		t.Run(name, func(t *testing.T) {
			q := entFeaturesQuery()
			mutate(&q)
			sysPublicFailure(t, entFeaturesRequest(handler, q), 400, "validation_failed", "persisted feature flags query shape is unsupported")
		})
	}
	require.Equal(t, before, entFeaturesState(t, db))
}

func TestENTPersistedFeatureFlagsTransportDenials(t *testing.T) {
	db := newModuleTestDB(t, "ENT")
	handler := entFeaturesHandler(t, db)
	before := entFeaturesState(t, db)
	for name, mutate := range map[string]func(*http.Request){
		"no_bearer":    func(r *http.Request) { r.Header.Del("Authorization") },
		"wrong_bearer": func(r *http.Request) { r.Header.Set("Authorization", "Bearer private-marker") },
		"no_caller":    func(r *http.Request) { r.Header.Del(headerCaller) },
		"wrong_caller": func(r *http.Request) { r.Header.Set(headerCaller, "unknown") },
	} {
		t.Run(name, func(t *testing.T) {
			sysPublicFailure(t, entFeaturesRequest(handler, entFeaturesQuery(), mutate), 401, "authentication_failed", "module caller authentication failed")
		})
	}
	for name, mutate := range map[string]func(*foundation.QueryEnvelope){
		"zero_account": func(q *foundation.QueryEnvelope) { q.AccountID = 0 },
		"no_actor":     func(q *foundation.QueryEnvelope) { q.Actor = foundation.ContractActor{} },
		"no_filter":    func(q *foundation.QueryEnvelope) { q.Filter = nil },
		"no_fields":    func(q *foundation.QueryEnvelope) { q.Fields = nil },
		"no_sort":      func(q *foundation.QueryEnvelope) { q.Sort = nil },
		"no_page":      func(q *foundation.QueryEnvelope) { q.Page = "" },
		"no_time":      func(q *foundation.QueryEnvelope) { q.RequestedAt = time.Time{} },
		"wrong_query":  func(q *foundation.QueryEnvelope) { q.QueryType = "ent.quota.get.v1" },
	} {
		t.Run(name, func(t *testing.T) {
			q := entFeaturesQuery()
			mutate(&q)
			sysPublicFailure(t, entFeaturesRequest(handler, q), 400, "validation_failed", "query envelope is invalid")
		})
	}
	encoded, err := json.Marshal(entFeaturesQuery())
	require.NoError(t, err)
	for name, body := range map[string][]byte{
		"unknown_field":    bytes.Replace(encoded, []byte(`"filter":{}`), []byte(`"filter":{},"admin":true`), 1),
		"second_value":     append(append([]byte{}, encoded...), []byte(` {}`)...),
		"trailing_garbage": append(append([]byte{}, encoded...), []byte(` private-marker`)...),
		"oversized_body":   bytes.Replace(encoded, []byte(`"filter":{}`), []byte(`"filter":{"private-marker":"`+strings.Repeat("x", 5000)+`"}`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/contracts/queries/"+persistedFeatureFlagsQuery, bytes.NewReader(body))
			r.Header.Set("Authorization", "Bearer ent-secret")
			r.Header.Set(headerCaller, "API")
			r.Header.Set(headerContractVersion, "v1")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			sysPublicFailure(t, w, 400, "validation_failed", "query envelope is invalid")
		})
	}
	sysPublicFailure(t, entFeaturesRequest(handler, entFeaturesQuery(), func(r *http.Request) { r.Header.Set(headerContractVersion, "v2") }), 400, "validation_failed", "contract version is unsupported")
	sysPublicFailure(t, entFeaturesRequest(handler, entFeaturesQuery(), func(r *http.Request) { r.Method = http.MethodGet }), 405, "validation_failed", "contract transport requires POST")
	for _, path := range []string{"queries/ent.quota.get.v1", "commands/ent.feature.set.v1"} {
		w := performContractRequest(handler, "/contracts/"+path, entFeaturesQuery(), func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer ent-secret")
			r.Header.Set(headerCaller, "API")
		})
		sysPublicFailure(t, w, 404, "resource_not_found", "")
	}
	require.Equal(t, before, entFeaturesState(t, db))
}

func entFeaturesBoundCases(t *testing.T, makeDB func(*testing.T) *gorm.DB) {
	t.Helper()
	t.Run("key_byte_boundary", func(t *testing.T) {
		db := makeDB(t)
		key := strings.Repeat("😀", 100)
		entFeaturesSeed(t, db, 91, key, false)
		require.Equal(t, map[string]bool{key: false}, entFeaturesData(t, entFeaturesRequest(entFeaturesHandler(t, db), entFeaturesQuery())))
	})
	for _, count := range []int{512, 513} {
		t.Run(fmt.Sprintf("row_bound_%d", count), func(t *testing.T) {
			db := makeDB(t)
			for i := 0; i < count; i++ {
				entFeaturesSeed(t, db, 91, fmt.Sprintf("flag-%04d", i), i%2 == 0)
			}
			before := entFeaturesState(t, db)
			w := entFeaturesRequest(entFeaturesHandler(t, db), entFeaturesQuery())
			if count == 512 {
				require.Len(t, entFeaturesData(t, w), 512)
			} else {
				sysPublicFailure(t, w, 503, "dependency_unavailable", "persisted feature flags are unavailable")
				require.NotContains(t, w.Body.String(), "flag-")
			}
			require.Equal(t, before, entFeaturesState(t, db))
		})
	}
	for _, extra := range []int{0, 1} {
		t.Run(fmt.Sprintf("encoded_bound_plus_%d", extra), func(t *testing.T) {
			db := makeDB(t)
			flags := map[string]bool{}
			remaining := 122
			for i := 0; i < 120; i++ {
				key := fmt.Sprintf("%04d", i) + strings.Repeat("<", 88)
				if i == 0 {
					key += strings.Repeat("x", 3+extra)
				}
				available := 100 - len(key)
				add := min(available, remaining)
				key += strings.Repeat("<", add)
				remaining -= add
				flags[key] = true
				entFeaturesSeed(t, db, 91, key, true)
			}
			require.Zero(t, remaining)
			encoded, err := json.Marshal(flags)
			require.NoError(t, err)
			require.Len(t, encoded, 65536+extra)
			w := entFeaturesRequest(entFeaturesHandler(t, db), entFeaturesQuery())
			if extra == 0 {
				require.Equal(t, flags, entFeaturesData(t, w))
			} else {
				sysPublicFailure(t, w, 503, "dependency_unavailable", "persisted feature flags are unavailable")
			}
		})
	}
}

func TestENTPersistedFeatureFlagsBounds(t *testing.T) {
	entFeaturesBoundCases(t, func(t *testing.T) *gorm.DB { return newModuleTestDB(t, "ENT") })
}

func TestENTPersistedFeatureFlagsMalformedRowsAndFailures(t *testing.T) {
	for name, statement := range map[string]string{
		"null_bool":         `UPDATE account_features SET enabled=NULL`,
		"text_bool":         `UPDATE account_features SET enabled='private-marker'`,
		"blob_bool":         `UPDATE account_features SET enabled=x'01'`,
		"invalid_integer":   `UPDATE account_features SET enabled=2`,
		"real_bool":         `UPDATE account_features SET enabled=0.5`,
		"blob_key":          `UPDATE account_features SET feature_name=x'666f6f'`,
		"invalid_utf8":      `UPDATE account_features SET feature_name=CAST(x'ff' AS TEXT)`,
		"empty_key":         `UPDATE account_features SET feature_name=''`,
		"oversized_key":     `UPDATE account_features SET feature_name='x'||replace(hex(zeroblob(200)),'0','a')`,
		"nul_oversized_key": `UPDATE account_features SET feature_name='x'||char(0)||replace(hex(zeroblob(200)),'0','a')`,
		"duplicate_key":     `DROP INDEX idx_acc_feature; INSERT INTO account_features(account_id,feature_name,enabled) VALUES (91,'private-marker',0)`,
	} {
		t.Run(name, func(t *testing.T) {
			db := newModuleTestDB(t, "ENT")
			entFeaturesSeed(t, db, 91, "private-marker", true)
			require.NoError(t, db.Exec(statement).Error)
			sysPublicFailure(t, entFeaturesRequest(entFeaturesHandler(t, db), entFeaturesQuery()), 503, "dependency_unavailable", "persisted feature flags are unavailable")
		})
	}
	t.Run("storage_failure", func(t *testing.T) {
		db := newModuleTestDB(t, "ENT")
		handler := entFeaturesHandler(t, db)
		require.NoError(t, db.Callback().Row().Before("gorm:row").Register("ent_store_failure", func(tx *gorm.DB) { tx.AddError(errors.New("private-marker storage detail")) }))
		sysPublicFailure(t, entFeaturesRequest(handler, entFeaturesQuery()), 503, "dependency_unavailable", "persisted feature flags are unavailable")
		require.NoError(t, db.Callback().Row().Remove("ent_store_failure"))
		require.Empty(t, entFeaturesData(t, entFeaturesRequest(handler, entFeaturesQuery())))
	})
}

func entFeaturesSnapshotCases(t *testing.T, source, target *gorm.DB) {
	t.Helper()
	entFeaturesSeed(t, source, 91, "captain", false)
	entFeaturesSeed(t, source, 91, "unknown_future", true)
	entFeaturesSeed(t, source, 92, "private-marker", false)
	var expectedRows []domain.AccountFeature
	require.NoError(t, source.Order("id ASC").Find(&expectedRows).Error)
	before := entFeaturesState(t, source)
	path := filepath.Join(t.TempDir(), "ent-snapshot.json")
	require.NoError(t, CreateSnapshot(source, "ENT", "ent-flags-test", path))
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(contents), "private-marker")
	require.Contains(t, string(contents), "updated_at")
	require.Equal(t, before, entFeaturesState(t, source))
	require.NoError(t, RestoreSnapshot(context.Background(), target, "ENT", "ent-flags-test", path, nil))
	var restoredRows []domain.AccountFeature
	require.NoError(t, target.Order("id ASC").Find(&restoredRows).Error)
	require.Equal(t, expectedRows, restoredRows, "restore must preserve flags and owner metadata")
	after := entFeaturesState(t, target)
	other := entFeaturesQuery()
	other.AccountID = 92
	require.Equal(t, map[string]bool{"private-marker": false}, entFeaturesData(t, entFeaturesRequest(entFeaturesHandler(t, target), other)))
	require.Equal(t, map[string]bool{"captain": false, "unknown_future": true}, entFeaturesData(t, entFeaturesRequest(entFeaturesHandler(t, target), entFeaturesQuery())))
	require.Equal(t, after, entFeaturesState(t, target))
	entFeaturesOwnerOnly(t, target)
	if target.Dialector.Name() == "postgres" {
		fresh := domain.AccountFeature{AccountID: 91, FeatureName: "after-restore", Enabled: true}
		require.NoError(t, target.Create(&fresh).Error)
		require.Greater(t, fresh.ID, restoredRows[len(restoredRows)-1].ID)
	}
}

func entFeaturesRollbackCase(t *testing.T, source, target *gorm.DB) {
	t.Helper()
	entFeaturesSeed(t, source, 91, "captain", false)
	entFeaturesSeed(t, source, 92, "other", true)
	entFeaturesSeed(t, target, 91, "retained", false)
	path := filepath.Join(t.TempDir(), "ent-rollback.json")
	require.NoError(t, CreateSnapshot(source, "ENT", "ent-rollback", path))
	before := entFeaturesState(t, target)
	failed := errors.New("synthetic rebuild failure")
	require.ErrorIs(t, RestoreSnapshot(context.Background(), target, "ENT", "ent-rollback", path, func(context.Context, *gorm.DB) error { return failed }), failed)
	require.Equal(t, before, entFeaturesState(t, target), "failed restore must roll back rows and recovery marker")
}

func TestENTPersistedFeatureFlagsRestoreRollback(t *testing.T) {
	entFeaturesRollbackCase(t, newModuleTestDB(t, "ENT"), newModuleTestDB(t, "ENT"))
}

func TestENTPersistedFeatureFlagsReadOnly(t *testing.T) {
	db := newModuleTestDB(t, "ENT")
	entFeaturesSeed(t, db, 91, "captain", false)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.Exec("PRAGMA query_only = ON").Error)
	t.Cleanup(func() { require.NoError(t, db.Exec("PRAGMA query_only = OFF").Error) })
	require.Equal(t, map[string]bool{"captain": false}, entFeaturesData(t, entFeaturesRequest(entFeaturesHandler(t, db), entFeaturesQuery())))
}

func TestENTPersistedFeatureFlagsSnapshot(t *testing.T) {
	entFeaturesSnapshotCases(t, newModuleTestDB(t, "ENT"), newModuleTestDB(t, "ENT"))
}

func TestENTPersistedFeatureFlagsConcurrentReads(t *testing.T) {
	db := newModuleTestDB(t, "ENT")
	entFeaturesSeed(t, db, 91, "captain", false)
	handler := entFeaturesHandler(t, db)
	before := entFeaturesState(t, db)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := entFeaturesRequest(handler, entFeaturesQuery())
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"captain":false`) {
				t.Errorf("concurrent persisted flags read failed: %d", w.Code)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, before, entFeaturesState(t, db))
}

func TestPostgresENTPersistedFeatureFlags(t *testing.T) {
	t.Run("owner_only", func(t *testing.T) { entFeaturesReadCases(t, postgresModuleDB(t, "ENT")) })
	t.Run("bounds", func(t *testing.T) {
		entFeaturesBoundCases(t, func(t *testing.T) *gorm.DB { return postgresModuleDB(t, "ENT") })
	})
	t.Run("snapshot", func(t *testing.T) {
		entFeaturesSnapshotCases(t, postgresModuleDB(t, "ENT"), postgresModuleDB(t, "ENT"))
	})
	t.Run("rollback", func(t *testing.T) {
		entFeaturesRollbackCase(t, postgresModuleDB(t, "ENT"), postgresModuleDB(t, "ENT"))
	})
	t.Run("null_boolean", func(t *testing.T) {
		db := postgresModuleDB(t, "ENT")
		entFeaturesSeed(t, db, 91, "captain", false)
		require.NoError(t, db.Model(&domain.AccountFeature{}).Where("account_id = ?", 91).Update("enabled", nil).Error)
		sysPublicFailure(t, entFeaturesRequest(entFeaturesHandler(t, db), entFeaturesQuery()), 503, "dependency_unavailable", "persisted feature flags are unavailable")
	})
	t.Run("read_only_transaction", func(t *testing.T) {
		db := postgresModuleDB(t, "ENT")
		entFeaturesSeed(t, db, 91, "captain", false)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SET TRANSACTION READ ONLY").Error; err != nil {
				return err
			}
			flags, err := repository.NewEnterpriseRepository(tx).PersistedFeatureFlags(91)
			require.Equal(t, map[string]bool{"captain": false}, flags)
			return err
		}))
	})
}
