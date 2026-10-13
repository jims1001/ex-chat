package modruntime

import (
	"bytes"
	"context"
	"encoding/json"
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

var sysPublicTestKeys = []string{"BRAND_NAME", "BRAND_URL", "DISPLAY_MANIFEST", "INSTALLATION_NAME", "LOGO", "LOGO_DARK", "LOGO_THUMBNAIL", "PRIVACY_URL", "TERMS_URL", "WIDGET_BRAND_URL"}

func sysPublicQuery() foundation.QueryEnvelope {
	return foundation.QueryEnvelope{QueryType: publicSystemConfigurationQuery, AccountID: 91,
		Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, Filter: map[string]any{},
		Sort: []string{"config_key"}, Page: "1", Fields: []string{"config_key", "value"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
}

func sysPublicHandler(t *testing.T, db *gorm.DB) http.Handler {
	t.Helper()
	cfg := businessConfig("SYS")
	cfg.MaxTimeout = 5 * time.Second
	runtime := &Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("SYS")}
	require.NoError(t, runtime.RegisterBusinessContracts())
	require.Len(t, runtime.Contracts.queries, 1)
	require.Empty(t, runtime.Contracts.commands)
	require.Empty(t, runtime.Contracts.events)
	return runtime.Handler()
}

func sysPublicRequest(handler http.Handler, query foundation.QueryEnvelope) *httptest.ResponseRecorder {
	return performContractRequest(handler, "/contracts/queries/"+publicSystemConfigurationQuery, query, func(request *http.Request) {
		request.Header.Set("Authorization", "Bearer sys-secret")
		request.Header.Set(headerCaller, "API")
	})
}

func sysPublicData(t *testing.T, response *httptest.ResponseRecorder) []repository.PublicBrandingSetting {
	t.Helper()
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var envelope struct {
		Data []repository.PublicBrandingSetting `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.NotNil(t, envelope.Data)
	return envelope.Data
}

func sysPublicFailure(t *testing.T, response *httptest.ResponseRecorder, status int, kind, message string) {
	t.Helper()
	require.Equal(t, status, response.Code, response.Body.String())
	var failure foundation.ContractError
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &failure))
	require.NoError(t, failure.Validate())
	require.Equal(t, kind, failure.Type)
	if message != "" {
		require.Equal(t, message, failure.Message)
	}
	require.Empty(t, failure.Details)
	require.NotContains(t, response.Body.String(), "private-marker")
}

func sysPublicOwnerState(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var configs []domain.SystemConfig
	var journal []domain.LocalChangeJournal
	require.NoError(t, db.Order("id ASC").Find(&configs).Error)
	require.NoError(t, db.Order("id ASC").Find(&journal).Error)
	encoded, err := json.Marshal([]any{configs, journal})
	require.NoError(t, err)
	return string(encoded)
}

func sysPublicAssertOwnerOnly(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, foreign := range []any{&domain.User{}, &domain.Account{}, &domain.AccountUser{}, &domain.AccountFeature{}, &domain.Inbox{}, &domain.CustomFilter{}} {
		require.False(t, db.Migrator().HasTable(foreign), "foreign table %T", foreign)
	}
}

func sysPublicReadCases(t *testing.T, db *gorm.DB) {
	t.Helper()
	handler := sysPublicHandler(t, db)
	store := repository.NewSystemConfigRepository(db)
	t.Setenv("INSTALLATION_NAME", "environment-private-marker")
	require.Empty(t, sysPublicData(t, sysPublicRequest(handler, sysPublicQuery())))
	require.NoError(t, store.Set("SMTP_PASSWORD", "private-marker"))
	require.NoError(t, store.Set("HCAPTCHA_SERVER_KEY", "private-marker"))
	require.NoError(t, store.Set("INSTALLATION_EVENTS_WEBHOOK_URL", "private-marker"))
	require.NoError(t, store.Set("UNKNOWN_PUBLIC_KEY", "private-marker"))
	require.NoError(t, store.Set("brand_name", "private-marker"))
	require.NoError(t, store.Set("BRAND_NAME_SUFFIX", "private-marker"))
	require.NoError(t, store.Set("ENABLE_ACCOUNT_SIGNUP", "private-marker"))
	// Even oversized private settings must neither enter the public result nor
	// fail the public snapshot. Only whitelisted rows are fetched and validated.
	require.NoError(t, store.Set("PRIVATE_LARGE", strings.Repeat("private-marker", 10000)))
	require.Empty(t, sysPublicData(t, sysPublicRequest(handler, sysPublicQuery())))
	expected := []repository.PublicBrandingSetting{}
	for i := len(sysPublicTestKeys) - 1; i >= 0; i-- {
		require.NoError(t, store.Set(sysPublicTestKeys[i], "persisted "+sysPublicTestKeys[i]))
	}
	// Seed an actual empty persisted value; the existing admin Set behavior is
	// outside this read-only slice and its struct update may omit zero values.
	require.NoError(t, db.Model(&domain.SystemConfig{}).Where("config_key = ?", "BRAND_NAME").Update("value", "").Error)
	require.NoError(t, store.Set("DISPLAY_MANIFEST", "false"))
	for _, key := range sysPublicTestKeys {
		value := "persisted " + key
		if key == "BRAND_NAME" {
			value = ""
		}
		if key == "DISPLAY_MANIFEST" {
			value = "false"
		}
		expected = append(expected, repository.PublicBrandingSetting{ConfigKey: key, Value: value})
	}
	before := sysPublicOwnerState(t, db)
	for _, actor := range []foundation.ContractActor{
		{Type: "User", ID: "7", AuthSource: "module"},
		{Type: "SuperAdmin", ID: "root", AuthSource: "jwt"},
		{Type: "System", ID: "api", AuthSource: "module"},
		{Type: "fabricated", ID: "private-marker", AuthSource: "unverified"},
	} {
		for _, account := range []uint{1, 91, 999999} {
			query := sysPublicQuery()
			query.Actor, query.AccountID = actor, account
			response := sysPublicRequest(handler, query)
			require.Equal(t, expected, sysPublicData(t, response))
			for _, hidden := range []string{"private-marker", "\"id\"", "updated_at", "account_id", "actor", "version", "activated"} {
				require.NotContains(t, response.Body.String(), hidden)
			}
		}
	}
	require.Equal(t, before, sysPublicOwnerState(t, db), "public reads changed owner rows/journal/receipts/outbox")
	sysPublicAssertOwnerOnly(t, db)
	// Reads observe the currently persisted string, not a fabricated cache or
	// an environment/default/version activation mechanism.
	require.NoError(t, store.Set("DISPLAY_MANIFEST", `{"uninterpreted":true}`))
	got := sysPublicData(t, sysPublicRequest(handler, sysPublicQuery()))
	require.Equal(t, `{"uninterpreted":true}`, got[2].Value)
}

func TestSYSPublicConfigurationOwnerOnly(t *testing.T) {
	sysPublicReadCases(t, newModuleTestDB(t, "SYS"))
}

func TestSYSPublicConfigurationRejectsExpandedQueryShape(t *testing.T) {
	db := newModuleTestDB(t, "SYS")
	handler := sysPublicHandler(t, db)
	require.NoError(t, repository.NewSystemConfigRepository(db).Set("SMTP_PASSWORD", "private-marker"))
	before := sysPublicOwnerState(t, db)
	mutations := map[string]func(*foundation.QueryEnvelope){
		"named_secret":     func(q *foundation.QueryEnvelope) { q.Filter["config_key"] = "SMTP_PASSWORD" },
		"key_list":         func(q *foundation.QueryEnvelope) { q.Filter["keys"] = []string{"BRAND_NAME", "SMTP_PASSWORD"} },
		"admin":            func(q *foundation.QueryEnvelope) { q.Filter["admin"] = true },
		"account_filter":   func(q *foundation.QueryEnvelope) { q.Filter["account_id"] = 91 },
		"public_variant":   func(q *foundation.QueryEnvelope) { q.Filter["public"] = false },
		"wildcard":         func(q *foundation.QueryEnvelope) { q.Fields = []string{"*"} },
		"metadata":         func(q *foundation.QueryEnvelope) { q.Fields = append(q.Fields, "updated_at", "id") },
		"duplicate_fields": func(q *foundation.QueryEnvelope) { q.Fields = []string{"value", "value"} },
		"reordered_fields": func(q *foundation.QueryEnvelope) { q.Fields = []string{"value", "config_key"} },
		"sort":             func(q *foundation.QueryEnvelope) { q.Sort = []string{"value DESC; private-marker"} },
		"page":             func(q *foundation.QueryEnvelope) { q.Page = "2" },
		"cursor":           func(q *foundation.QueryEnvelope) { q.Cursor = "private-marker" },
		"consistency":      func(q *foundation.QueryEnvelope) { q.Consistency = "eventual" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			query := sysPublicQuery()
			mutate(&query)
			sysPublicFailure(t, sysPublicRequest(handler, query), 400, "validation_failed", "public configuration query shape is unsupported")
		})
	}
	require.Equal(t, before, sysPublicOwnerState(t, db))
}

func TestSYSPublicConfigurationTransportDenials(t *testing.T) {
	db := newModuleTestDB(t, "SYS")
	handler := sysPublicHandler(t, db)
	require.NoError(t, repository.NewSystemConfigRepository(db).Set("BRAND_NAME", "safe-public-value"))
	before := sysPublicOwnerState(t, db)
	for name, mutate := range map[string]func(*http.Request){
		"missing_token":  func(r *http.Request) { r.Header.Del("Authorization") },
		"wrong_token":    func(r *http.Request) { r.Header.Set("Authorization", "Bearer admin") },
		"unknown_caller": func(r *http.Request) { r.Header.Set(headerCaller, "UNKNOWN") },
		"missing_caller": func(r *http.Request) { r.Header.Del(headerCaller) },
	} {
		t.Run(name, func(t *testing.T) {
			response := performContractRequest(handler, "/contracts/queries/"+publicSystemConfigurationQuery, sysPublicQuery(), func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer sys-secret")
				r.Header.Set(headerCaller, "API")
				mutate(r)
			})
			sysPublicFailure(t, response, 401, "authentication_failed", "module caller authentication failed")
		})
	}
	for name, mutate := range map[string]func(*foundation.QueryEnvelope){
		"zero_account":       func(q *foundation.QueryEnvelope) { q.AccountID = 0 },
		"empty_actor":        func(q *foundation.QueryEnvelope) { q.Actor = foundation.ContractActor{} },
		"empty_actor_source": func(q *foundation.QueryEnvelope) { q.Actor.AuthSource = "" },
		"missing_filter":     func(q *foundation.QueryEnvelope) { q.Filter = nil },
		"wrong_query":        func(q *foundation.QueryEnvelope) { q.QueryType = "sys.instance_status.get.v1" },
		"no_time":            func(q *foundation.QueryEnvelope) { q.RequestedAt = time.Time{} },
	} {
		t.Run(name, func(t *testing.T) {
			q := sysPublicQuery()
			mutate(&q)
			sysPublicFailure(t, sysPublicRequest(handler, q), 400, "validation_failed", "query envelope is invalid")
		})
	}
	encoded, err := json.Marshal(sysPublicQuery())
	require.NoError(t, err)
	for name, body := range map[string][]byte{
		"unknown_field":    bytes.Replace(encoded, []byte(`"filter":{}`), []byte(`"filter":{},"admin":true`), 1),
		"second_value":     append(append([]byte{}, encoded...), []byte(` {}`)...),
		"trailing_garbage": append(append([]byte{}, encoded...), []byte(` private-marker`)...),
		"body_limit":       bytes.Replace(encoded, []byte(`"filter":{}`), []byte(`"filter":{"private-marker":"`+strings.Repeat("x", 5000)+`"}`), 1),
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/contracts/queries/"+publicSystemConfigurationQuery, bytes.NewReader(body))
			r.Header.Set("Authorization", "Bearer sys-secret")
			r.Header.Set(headerCaller, "API")
			r.Header.Set(headerContractVersion, "v1")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, r)
			sysPublicFailure(t, response, 400, "validation_failed", "query envelope is invalid")
		})
	}
	response := performContractRequest(handler, "/contracts/queries/"+publicSystemConfigurationQuery, sysPublicQuery(), func(r *http.Request) {
		r.Header.Set(headerContractVersion, "v2")
	})
	sysPublicFailure(t, response, 400, "validation_failed", "contract version is unsupported")
	for _, path := range []string{"queries/sys.configuration.admin.get.v1", "queries/sys.instance_status.get.v1", "queries/sys.jobs.get.v1", "queries/sys.instance.status.v1", "queries/sys.jobs.observe.v1", "commands/sys.configuration.update.v1"} {
		response := performContractRequest(handler, "/contracts/"+path, sysPublicQuery(), func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer sys-secret")
			r.Header.Set(headerCaller, "API")
		})
		sysPublicFailure(t, response, 404, "resource_not_found", "")
	}
	require.Equal(t, before, sysPublicOwnerState(t, db))
}

func sysPublicBoundCases(t *testing.T, makeDB func(*testing.T) *gorm.DB) {
	t.Helper()
	for name, value := range map[string]string{
		"empty": "", "plain": "  unchanged  ", "byte_boundary": strings.Repeat("x", 4096),
		"unicode_boundary": strings.Repeat("界", 1365) + "x", "false_string": "false", "json_string": `{"unchanged":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			db := makeDB(t)
			// Seed an owner row directly: empty admin writes now preserve absence.
			require.NoError(t, db.Create(&domain.SystemConfig{ConfigKey: "BRAND_NAME", Value: value}).Error)
			before := sysPublicOwnerState(t, db)
			data := sysPublicData(t, sysPublicRequest(sysPublicHandler(t, db), sysPublicQuery()))
			require.Equal(t, []repository.PublicBrandingSetting{{ConfigKey: "BRAND_NAME", Value: value}}, data)
			require.Equal(t, before, sysPublicOwnerState(t, db))
		})
	}
	for name, value := range map[string]string{
		"byte_overflow": strings.Repeat("x", 4097), "unicode_overflow": strings.Repeat("界", 1366),
		"escaped_json_overflow": strings.Repeat("<", 3000), "large": strings.Repeat("private-marker", 10000),
	} {
		t.Run(name, func(t *testing.T) {
			db := makeDB(t)
			require.NoError(t, repository.NewSystemConfigRepository(db).Set("BRAND_NAME", value))
			before := sysPublicOwnerState(t, db)
			sysPublicFailure(t, sysPublicRequest(sysPublicHandler(t, db), sysPublicQuery()), 503, "dependency_unavailable", "public configuration is unavailable")
			require.Equal(t, before, sysPublicOwnerState(t, db))
		})
	}
	for _, size := range []int{16384, 16385} {
		t.Run("total_"+strings.Repeat("over", size-16384), func(t *testing.T) {
			db := makeDB(t)
			entries := []repository.PublicBrandingSetting{}
			for _, key := range sysPublicTestKeys[:4] {
				entries = append(entries, repository.PublicBrandingSetting{ConfigKey: key})
			}
			empty, err := json.Marshal(entries)
			require.NoError(t, err)
			remaining := size - len(empty)
			for i := range entries {
				n := min(remaining, 4096)
				entries[i].Value = strings.Repeat("x", n)
				remaining -= n
				require.NoError(t, repository.NewSystemConfigRepository(db).Set(entries[i].ConfigKey, entries[i].Value))
			}
			require.Zero(t, remaining)
			response := sysPublicRequest(sysPublicHandler(t, db), sysPublicQuery())
			if size == 16384 {
				require.Equal(t, entries, sysPublicData(t, response))
			} else {
				sysPublicFailure(t, response, 503, "dependency_unavailable", "public configuration is unavailable")
			}
		})
	}
}

func TestSYSPublicConfigurationBounds(t *testing.T) {
	sysPublicBoundCases(t, func(t *testing.T) *gorm.DB { return newModuleTestDB(t, "SYS") })
}

func TestSYSPublicConfigurationMalformedRowsAndStoreFailure(t *testing.T) {
	for name, value := range map[string]any{"null": nil, "blob": []byte("private-marker"), "invalid_utf8": string([]byte{0xff, 0xfe}), "nul_overflow": "\x00" + strings.Repeat("x", 5000)} {
		t.Run(name, func(t *testing.T) {
			db := newModuleTestDB(t, "SYS")
			require.NoError(t, db.Exec("INSERT INTO system_configs (config_key, value) VALUES (?, ?)", "BRAND_NAME", value).Error)
			sysPublicFailure(t, sysPublicRequest(sysPublicHandler(t, db), sysPublicQuery()), 503, "dependency_unavailable", "public configuration is unavailable")
		})
	}
	for name, setup := range map[string]string{
		"missing_table":  "",
		"numeric_type":   "CREATE TABLE system_configs (config_key, value); INSERT INTO system_configs VALUES ('BRAND_NAME', 123)",
		"duplicate_rows": "CREATE TABLE system_configs (config_key, value); INSERT INTO system_configs VALUES ('BRAND_NAME', 'one'), ('BRAND_NAME', 'two')",
	} {
		t.Run(name, func(t *testing.T) {
			db := newModuleTestDB(t, "SYS")
			require.NoError(t, db.Migrator().DropTable(&domain.SystemConfig{}))
			if setup != "" {
				require.NoError(t, db.Exec(setup).Error)
			}
			sysPublicFailure(t, sysPublicRequest(sysPublicHandler(t, db), sysPublicQuery()), 503, "dependency_unavailable", "public configuration is unavailable")
		})
	}
}

func sysPublicSnapshotCases(t *testing.T, source, target *gorm.DB) {
	t.Helper()
	store := repository.NewSystemConfigRepository(source)
	require.NoError(t, store.Set("INSTALLATION_NAME", "Restored public brand"))
	require.NoError(t, store.Set("SMTP_PASSWORD", "private-marker-retained-owner-data"))
	before := sysPublicOwnerState(t, source)
	path := filepath.Join(t.TempDir(), "sys-snapshot.json")
	require.NoError(t, CreateSnapshot(source, "SYS", "sys-public-test", path))
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(contents), "private-marker-retained-owner-data")
	require.Contains(t, string(contents), "updated_at")
	require.Equal(t, before, sysPublicOwnerState(t, source))
	require.NoError(t, RestoreSnapshot(context.Background(), target, "SYS", "sys-public-test", path, nil))
	private, err := repository.NewSystemConfigRepository(target).Get("SMTP_PASSWORD")
	require.NoError(t, err)
	require.Equal(t, "private-marker-retained-owner-data", private)
	targetBefore := sysPublicOwnerState(t, target)
	response := sysPublicRequest(sysPublicHandler(t, target), sysPublicQuery())
	require.Equal(t, []repository.PublicBrandingSetting{{ConfigKey: "INSTALLATION_NAME", Value: "Restored public brand"}}, sysPublicData(t, response))
	require.NotContains(t, response.Body.String(), "private-marker")
	require.Equal(t, targetBefore, sysPublicOwnerState(t, target))
	sysPublicAssertOwnerOnly(t, target)
}

func TestSYSPublicConfigurationPreservesPrivateOwnerSnapshot(t *testing.T) {
	sysPublicSnapshotCases(t, newModuleTestDB(t, "SYS"), newModuleTestDB(t, "SYS"))
}

func TestSYSPublicConfigurationConcurrentReads(t *testing.T) {
	db := newModuleTestDB(t, "SYS")
	require.NoError(t, repository.NewSystemConfigRepository(db).Set("BRAND_NAME", "Shared public brand"))
	handler := sysPublicHandler(t, db)
	before := sysPublicOwnerState(t, db)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			response := sysPublicRequest(handler, sysPublicQuery())
			if response.Code != 200 || !strings.Contains(response.Body.String(), "Shared public brand") {
				t.Errorf("concurrent public read failed: %d", response.Code)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, before, sysPublicOwnerState(t, db))
}

func TestPostgresSYSPublicConfiguration(t *testing.T) {
	t.Run("owner_only", func(t *testing.T) { sysPublicReadCases(t, postgresModuleDB(t, "SYS")) })
	t.Run("bounds", func(t *testing.T) {
		sysPublicBoundCases(t, func(t *testing.T) *gorm.DB { return postgresModuleDB(t, "SYS") })
	})
	t.Run("snapshot", func(t *testing.T) { sysPublicSnapshotCases(t, postgresModuleDB(t, "SYS"), postgresModuleDB(t, "SYS")) })
	t.Run("read_only_transaction", func(t *testing.T) {
		db := postgresModuleDB(t, "SYS")
		require.NoError(t, repository.NewSystemConfigRepository(db).Set("BRAND_NAME", "Read-only brand"))
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SET TRANSACTION READ ONLY").Error; err != nil {
				return err
			}
			view, err := repository.NewSystemConfigRepository(tx).PublicBrandingSnapshot()
			require.Equal(t, []repository.PublicBrandingSetting{{ConfigKey: "BRAND_NAME", Value: "Read-only brand"}}, view)
			return err
		}))
	})
}
