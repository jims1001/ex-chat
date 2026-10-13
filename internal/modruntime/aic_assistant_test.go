package modruntime

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func aicAssistantQuery() foundation.QueryEnvelope {
	return foundation.QueryEnvelope{QueryType: "aic.assistant.get.v1", AccountID: 91,
		Actor:  foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"},
		Filter: map[string]any{"inbox_id": json.Number("23")}, Sort: []string{"inbox_id"}, Page: "1",
		Fields: []string{"id", "name"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
}

func TestAICBoundAssistantDeclaredQuery(t *testing.T) {
	db := newModuleTestDB(t, "AIC")
	require.NoError(t, db.Create(&domain.CaptainAssistant{ID: 17, AccountID: 91, Name: "Support", Config: "private-marker"}).Error)
	require.NoError(t, db.Create(&domain.CaptainInbox{AccountID: 91, InboxID: 23, CaptainAssistantID: 17}).Error)
	r := &Runtime{Config: businessConfig("AIC"), DB: db, Contracts: NewContractRegistry("AIC")}
	require.NoError(t, r.RegisterBusinessContracts())
	response := performContractRequest(r.Handler(), "/contracts/queries/aic.assistant.get.v1", aicAssistantQuery(), func(request *http.Request) {
		request.Header.Set("Authorization", "Bearer aic-secret")
		request.Header.Set(headerCaller, "API")
	})
	require.Equal(t, 200, response.Code, response.Body.String())
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.JSONEq(t, `{"assistant":{"id":17,"name":"Support"}}`, string(envelope.Data))
}

func aicAssistantHandler(t *testing.T, db *gorm.DB) http.Handler {
	t.Helper()
	r := &Runtime{Config: businessConfig("AIC"), DB: db, Contracts: NewContractRegistry("AIC")}
	require.NoError(t, r.RegisterBusinessContracts())
	require.Len(t, r.Contracts.queries, 2)
	require.Empty(t, r.Contracts.commands)
	require.Empty(t, r.Contracts.events)
	return r.Handler()
}

func aicAssistantRequest(handler http.Handler, q foundation.QueryEnvelope) *httptest.ResponseRecorder {
	return performContractRequest(handler, "/contracts/queries/aic.assistant.get.v1", q, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer aic-secret")
		r.Header.Set(headerCaller, "API")
	})
}

func aicAssistantResult(t *testing.T, response *httptest.ResponseRecorder, want *repository.BoundAssistantSummary) {
	t.Helper()
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	encoded, err := json.Marshal(map[string]any{"assistant": want})
	require.NoError(t, err)
	require.JSONEq(t, string(encoded), string(envelope.Data))
	require.NotContains(t, response.Body.String(), "private-marker")
}

func aicAssistantSeed(t *testing.T, db *gorm.DB, id, account, inbox uint, name string) {
	t.Helper()
	require.NoError(t, db.Create(&domain.CaptainAssistant{ID: id, AccountID: account, Name: name,
		Description: "private-marker", SystemPrompt: "private-marker", Config: "private-marker", Model: "private-marker",
		Status: "inactive", ResponseGuidelines: "private-marker", Guardrails: "private-marker"}).Error)
	require.NoError(t, db.Create(&domain.CaptainInbox{AccountID: account, InboxID: inbox, CaptainAssistantID: id}).Error)
}

func aicAssistantOwnerCases(t *testing.T, db *gorm.DB) {
	t.Helper()
	h := aicAssistantHandler(t, db)
	before := aicUsageOwnerState(t, db)
	aicAssistantResult(t, aicAssistantRequest(h, aicAssistantQuery()), nil)
	require.Equal(t, before, aicUsageOwnerState(t, db), "missing binding created owner data")
	aicAssistantSeed(t, db, 17, 91, 23, "Support 中文")
	aicAssistantSeed(t, db, 18, 92, 23, "Other tenant")
	before = aicUsageOwnerState(t, db)
	for _, actor := range []foundation.ContractActor{
		{Type: "User", ID: "7", AuthSource: "module"}, {Type: "System", ID: "API", AuthSource: "module"},
		{Type: "SuperAdmin", ID: "root", AuthSource: "jwt"}, {Type: "fabricated", ID: "private-marker", AuthSource: "unverified"},
	} {
		q := aicAssistantQuery()
		q.Actor = actor
		aicAssistantResult(t, aicAssistantRequest(h, q), &repository.BoundAssistantSummary{ID: 17, Name: "Support 中文"})
		q.AccountID = 92
		aicAssistantResult(t, aicAssistantRequest(h, q), &repository.BoundAssistantSummary{ID: 18, Name: "Other tenant"})
		q.AccountID = 999
		aicAssistantResult(t, aicAssistantRequest(h, q), nil)
		q.AccountID = 91
		q.Filter["inbox_id"] = json.Number("24")
		aicAssistantResult(t, aicAssistantRequest(h, q), nil)
	}
	require.Equal(t, before, aicUsageOwnerState(t, db), "read changed owner configuration/journal/receipt/outbox state")
	aicUsageOwnerOnly(t, db)
}

func TestAICBoundAssistantOwnerOnly(t *testing.T) {
	aicAssistantOwnerCases(t, newModuleTestDB(t, "AIC"))
}

func TestAICBoundAssistantRejectsExpandedShape(t *testing.T) {
	db := newModuleTestDB(t, "AIC")
	aicAssistantSeed(t, db, 17, 91, 23, "Support")
	h := aicAssistantHandler(t, db)
	before := aicUsageOwnerState(t, db)
	mutations := map[string]func(*foundation.QueryEnvelope){
		"account_override": func(q *foundation.QueryEnvelope) { q.Filter["account_id"] = 92 },
		"assistant_lookup": func(q *foundation.QueryEnvelope) { q.Filter = map[string]any{"id": 17} },
		"missing_inbox":    func(q *foundation.QueryEnvelope) { q.Filter = map[string]any{} },
		"inbox_string":     func(q *foundation.QueryEnvelope) { q.Filter["inbox_id"] = "23" },
		"inbox_null":       func(q *foundation.QueryEnvelope) { q.Filter["inbox_id"] = nil },
		"inbox_bool":       func(q *foundation.QueryEnvelope) { q.Filter["inbox_id"] = true },
		"inbox_fraction":   func(q *foundation.QueryEnvelope) { q.Filter["inbox_id"] = json.Number("23.0") },
		"inbox_exponent":   func(q *foundation.QueryEnvelope) { q.Filter["inbox_id"] = json.Number("23e0") },
		"inbox_zero":       func(q *foundation.QueryEnvelope) { q.Filter["inbox_id"] = 0 },
		"inbox_negative":   func(q *foundation.QueryEnvelope) { q.Filter["inbox_id"] = -23 },
		"inbox_overflow":   func(q *foundation.QueryEnvelope) { q.Filter["inbox_id"] = json.Number("9223372036854775808") },
		"account_overflow": func(q *foundation.QueryEnvelope) { q.AccountID = ^uint(0) },
		"config":           func(q *foundation.QueryEnvelope) { q.Fields = append(q.Fields, "config") },
		"wildcard":         func(q *foundation.QueryEnvelope) { q.Fields = []string{"*"} },
		"partial":          func(q *foundation.QueryEnvelope) { q.Fields = []string{"id"} },
		"duplicate_field":  func(q *foundation.QueryEnvelope) { q.Fields = []string{"id", "id"} },
		"reordered_fields": func(q *foundation.QueryEnvelope) { q.Fields = []string{"name", "id"} },
		"sort":             func(q *foundation.QueryEnvelope) { q.Sort = []string{"name"} },
		"page":             func(q *foundation.QueryEnvelope) { q.Page = "2" },
		"cursor":           func(q *foundation.QueryEnvelope) { q.Cursor = "private-marker" },
		"eventual":         func(q *foundation.QueryEnvelope) { q.Consistency = "eventual" },
		"active":           func(q *foundation.QueryEnvelope) { q.Filter["status"] = "active" },
		"generation":       func(q *foundation.QueryEnvelope) { q.Filter["operation_id"] = "private-marker" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			q := aicAssistantQuery()
			mutate(&q)
			aicUsageFailure(t, aicAssistantRequest(h, q), 400, "validation_failed", "bound assistant query shape is unsupported")
		})
	}
	require.Equal(t, before, aicUsageOwnerState(t, db))
}

func TestAICBoundAssistantTransportDenials(t *testing.T) {
	db := newModuleTestDB(t, "AIC")
	aicAssistantSeed(t, db, 17, 91, 23, "Support")
	h := aicAssistantHandler(t, db)
	for name, mutate := range map[string]func(*http.Request){
		"missing_token":  func(r *http.Request) { r.Header.Del("Authorization") },
		"wrong_token":    func(r *http.Request) { r.Header.Set("Authorization", "Bearer private-marker") },
		"unknown_caller": func(r *http.Request) { r.Header.Set(headerCaller, "UNKNOWN") },
		"missing_caller": func(r *http.Request) { r.Header.Del(headerCaller) },
	} {
		t.Run(name, func(t *testing.T) {
			response := performContractRequest(h, "/contracts/queries/aic.assistant.get.v1", aicAssistantQuery(), func(r *http.Request) {
				r.Header.Set("Authorization", "Bearer aic-secret")
				r.Header.Set(headerCaller, "API")
				mutate(r)
			})
			aicUsageFailure(t, response, 401, "authentication_failed", "module caller authentication failed")
		})
	}
	nilFilter := aicAssistantQuery()
	nilFilter.Filter = nil
	aicUsageFailure(t, aicAssistantRequest(h, nilFilter), 400, "validation_failed", "query envelope is invalid")
	// The newly registered assistant query must not accept a quota envelope.
	aicUsageFailure(t, aicAssistantRequest(h, aicUsageQuery()), 400, "validation_failed", "query envelope is invalid")
	for _, suffix := range []string{"{}", "garbage"} {
		body, err := json.Marshal(aicAssistantQuery())
		require.NoError(t, err)
		request := httptest.NewRequest(http.MethodPost, "/contracts/queries/aic.assistant.get.v1", bytes.NewReader(append(body, suffix...)))
		request.Header.Set("Authorization", "Bearer aic-secret")
		request.Header.Set(headerCaller, "API")
		request.Header.Set(headerContractVersion, "v1")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		aicUsageFailure(t, response, 400, "validation_failed", "query envelope is invalid")
	}
}

func aicAssistantProjectionCase(t *testing.T, db *gorm.DB) {
	t.Helper()
	// Remove every other AIC table and the unneeded columns. A successful read
	// must not consult quotas, histories, configuration, CHN or a provider.
	models, err := database.ModuleModels("AIC")
	require.NoError(t, err)
	for _, model := range models {
		require.NoError(t, db.Migrator().DropTable(model))
	}
	require.NoError(t, db.Exec("CREATE TABLE captain_inboxes (id BIGINT, account_id BIGINT, inbox_id BIGINT, captain_assistant_id BIGINT)").Error)
	require.NoError(t, db.Exec("CREATE TABLE captain_assistants (id BIGINT, account_id BIGINT, name TEXT)").Error)
	require.NoError(t, db.Exec("INSERT INTO captain_inboxes VALUES (1, 91, 23, 17)").Error)
	require.NoError(t, db.Exec("INSERT INTO captain_assistants VALUES (17, 91, 'Support')").Error)
	queries := 0
	require.NoError(t, db.Callback().Row().Before("gorm:row").Register("assistant_projection", func(tx *gorm.DB) {
		queries++
		sql := tx.Statement.SQL.String()
		for _, forbidden := range []string{"*", "config", "prompt", "guardrails", "guidelines", "history", "quota", "copilot", "inboxes\""} {
			require.NotContains(t, sql, forbidden)
		}
		require.Contains(t, sql, "LEFT JOIN captain_assistants")
		require.Contains(t, sql, "LIMIT 2")
	}))
	defer db.Callback().Row().Remove("assistant_projection")
	aicAssistantResult(t, aicAssistantRequest(aicAssistantHandler(t, db), aicAssistantQuery()), &repository.BoundAssistantSummary{ID: 17, Name: "Support"})
	require.Equal(t, 1, queries, "binding/assistant must come from one SQL statement")
}

func TestAICBoundAssistantMinimalProjection(t *testing.T) {
	aicAssistantProjectionCase(t, newModuleTestDB(t, "AIC"))
}

func aicAssistantCorruptCase(t *testing.T, db *gorm.DB, kind string) {
	t.Helper()
	aicAssistantSeed(t, db, 17, 91, 23, "Support")
	switch kind {
	case "duplicate_binding":
		require.NoError(t, db.Create(&domain.CaptainInbox{AccountID: 91, InboxID: 23, CaptainAssistantID: 17}).Error)
	case "duplicate_assistant":
		require.NoError(t, db.Exec("ALTER TABLE captain_assistants RENAME TO old_assistants").Error)
		require.NoError(t, db.Exec("CREATE TABLE captain_assistants AS SELECT * FROM old_assistants").Error)
		require.NoError(t, db.Exec("INSERT INTO captain_assistants SELECT * FROM old_assistants").Error)
	case "dangling":
		require.NoError(t, db.Exec("UPDATE captain_inboxes SET captain_assistant_id = 999").Error)
	case "cross_account":
		require.NoError(t, db.Exec("UPDATE captain_assistants SET account_id = 92").Error)
	case "binding_zero":
		require.NoError(t, db.Exec("UPDATE captain_inboxes SET id = 0").Error)
	case "binding_negative":
		require.NoError(t, db.Exec("UPDATE captain_inboxes SET id = -1").Error)
	case "assistant_zero":
		require.NoError(t, db.Exec("UPDATE captain_inboxes SET captain_assistant_id = 0").Error)
	case "empty_name":
		require.NoError(t, db.Exec("UPDATE captain_assistants SET name = ''").Error)
	case "long_name":
		if db.Dialector.Name() == "postgres" {
			require.NoError(t, db.Exec("ALTER TABLE captain_assistants ALTER COLUMN name TYPE TEXT").Error)
		}
		require.NoError(t, db.Exec("UPDATE captain_assistants SET name = ?", strings.Repeat("界", 256)).Error)
	case "large_name":
		if db.Dialector.Name() == "postgres" {
			require.NoError(t, db.Exec("ALTER TABLE captain_assistants ALTER COLUMN name TYPE TEXT").Error)
		}
		require.NoError(t, db.Exec("UPDATE captain_assistants SET name = ?", strings.Repeat("x", 1<<20)).Error)
	case "null_name":
		if db.Dialector.Name() == "postgres" {
			require.NoError(t, db.Exec("ALTER TABLE captain_assistants ALTER COLUMN name DROP NOT NULL").Error)
		} else {
			require.NoError(t, db.Exec("ALTER TABLE captain_assistants RENAME TO old_assistants").Error)
			require.NoError(t, db.Exec("CREATE TABLE captain_assistants AS SELECT * FROM old_assistants").Error)
		}
		require.NoError(t, db.Exec("UPDATE captain_assistants SET name = NULL").Error)
	case "missing_binding_table":
		require.NoError(t, db.Migrator().DropTable(&domain.CaptainInbox{}))
	case "missing_assistant_table":
		require.NoError(t, db.Migrator().DropTable(&domain.CaptainAssistant{}))
	}
	aicUsageFailure(t, aicAssistantRequest(aicAssistantHandler(t, db), aicAssistantQuery()), 503, "dependency_unavailable", "bound assistant is unavailable")
}

var aicAssistantCorruptCases = []string{"duplicate_binding", "duplicate_assistant", "dangling", "cross_account", "binding_zero", "binding_negative", "assistant_zero", "empty_name", "long_name", "large_name", "null_name", "missing_binding_table", "missing_assistant_table"}

func TestAICBoundAssistantRejectsCorruption(t *testing.T) {
	for _, kind := range aicAssistantCorruptCases {
		t.Run(kind, func(t *testing.T) { aicAssistantCorruptCase(t, newModuleTestDB(t, "AIC"), kind) })
	}
}

func TestAICBoundAssistantSQLiteStoredTypes(t *testing.T) {
	for _, tc := range []struct{ name, column, value string }{
		{"binding_null", "binding", "NULL"}, {"binding_text", "binding", "'private-marker'"}, {"binding_real", "binding", "1.5"},
		{"binding_blob", "binding", "x'31'"}, {"reference_text", "reference", "'17'"}, {"reference_real", "reference", "17.0"},
		{"name_blob", "name", "x'537570706f7274'"}, {"name_number", "name", "7"}, {"name_invalid_utf8", "name", "CAST(x'ff' AS TEXT)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newModuleTestDB(t, "AIC")
			require.NoError(t, db.Migrator().DropTable(&domain.CaptainInbox{}, &domain.CaptainAssistant{}))
			require.NoError(t, db.Exec("CREATE TABLE captain_inboxes (id, account_id, inbox_id, captain_assistant_id)").Error)
			require.NoError(t, db.Exec("CREATE TABLE captain_assistants (id INTEGER, account_id INTEGER, name)").Error)
			require.NoError(t, db.Exec("INSERT INTO captain_inboxes VALUES (1, 91, 23, 17)").Error)
			require.NoError(t, db.Exec("INSERT INTO captain_assistants VALUES (17, 91, 'Support')").Error)
			switch tc.column {
			case "binding":
				require.NoError(t, db.Exec("UPDATE captain_inboxes SET id = "+tc.value).Error)
			case "reference":
				require.NoError(t, db.Exec("UPDATE captain_inboxes SET captain_assistant_id = "+tc.value).Error)
			case "name":
				require.NoError(t, db.Exec("UPDATE captain_assistants SET name = "+tc.value).Error)
			}
			aicUsageFailure(t, aicAssistantRequest(aicAssistantHandler(t, db), aicAssistantQuery()), 503, "dependency_unavailable", "bound assistant is unavailable")
		})
	}
}

func aicAssistantNumericCase(t *testing.T, db *gorm.DB) {
	t.Helper()
	for i, id := range []uint{9007199254740993, math.MaxInt64} {
		account, inbox := id, id-1
		name := strings.Repeat("𠮷", 255)
		aicAssistantSeed(t, db, id, account, inbox, name)
		q := aicAssistantQuery()
		q.AccountID = account
		q.Filter["inbox_id"] = json.Number(strconv.FormatUint(uint64(inbox), 10))
		aicAssistantResult(t, aicAssistantRequest(aicAssistantHandler(t, db), q), &repository.BoundAssistantSummary{ID: id, Name: name})
		if i == 0 {
			q.Filter["inbox_id"] = json.Number(strconv.FormatUint(uint64(inbox+1), 10))
			aicAssistantResult(t, aicAssistantRequest(aicAssistantHandler(t, db), q), nil)
		}
	}
}

func TestAICBoundAssistantNumericAndNameBounds(t *testing.T) {
	aicAssistantNumericCase(t, newModuleTestDB(t, "AIC"))
}

func aicAssistantSnapshotCase(t *testing.T, db *gorm.DB) {
	t.Helper()
	aicAssistantSeed(t, db, 17, 91, 23, "Support")
	aicAssistantSeed(t, db, 18, 92, 23, "Other tenant")
	path := filepath.Join(t.TempDir(), "assistant-snapshot.json")
	require.NoError(t, CreateSnapshot(db, "AIC", "assistant-generation", path))
	before := aicUsageOwnerState(t, db)
	require.NoError(t, db.Exec("UPDATE captain_inboxes SET captain_assistant_id = 18 WHERE account_id = 91").Error)
	aicUsageFailure(t, aicAssistantRequest(aicAssistantHandler(t, db), aicAssistantQuery()), 503, "dependency_unavailable", "bound assistant is unavailable")
	require.NoError(t, RestoreSnapshot(context.Background(), db, "AIC", "assistant-generation", path, nil))
	after := aicUsageOwnerState(t, db)
	// Recovery deliberately adds its existing local journal entry.
	require.Equal(t, before["captain_assistants"], after["captain_assistants"])
	require.Equal(t, before["captain_inboxes"], after["captain_inboxes"])
	q := aicAssistantQuery()
	aicAssistantResult(t, aicAssistantRequest(aicAssistantHandler(t, db), q), &repository.BoundAssistantSummary{ID: 17, Name: "Support"})
	q.AccountID = 92
	aicAssistantResult(t, aicAssistantRequest(aicAssistantHandler(t, db), q), &repository.BoundAssistantSummary{ID: 18, Name: "Other tenant"})
	q.AccountID = 999
	aicAssistantResult(t, aicAssistantRequest(aicAssistantHandler(t, db), q), nil)
	aicUsageOwnerOnly(t, db)
}

func TestAICBoundAssistantSnapshotRelationship(t *testing.T) {
	aicAssistantSnapshotCase(t, newModuleTestDB(t, "AIC"))
}

// Prepare independent SQLite connections before the timed concurrency obligation.
// Cold connection/read initialization is fixture setup, not the read under test.
func aicAssistantWarmSQLiteConnections(t *testing.T, db *gorm.DB) {
	t.Helper()
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	pool.SetMaxIdleConns(12)
	held := make([]*sql.Conn, 0, 12)
	defer func() {
		for _, conn := range held {
			_ = conn.Close()
		}
	}()
	for i := 0; i < 12; i++ {
		conn, err := pool.Conn(context.Background())
		require.NoError(t, err)
		held = append(held, conn)
		pinned := db.Session(&gorm.Session{NewDB: true}).WithContext(context.Background())
		pinned.Statement.ConnPool = conn
		value, err := repository.NewCaptainRepositoryWithOwnerPorts(pinned, nil, nil, nil).FindAssistantByInbox(context.Background(), 91, 23)
		require.NoError(t, err)
		require.Equal(t, &repository.BoundAssistantSummary{ID: 17, Name: "Support"}, value)
	}
	require.Equal(t, 12, pool.Stats().InUse)
	for _, conn := range held {
		require.NoError(t, conn.Close())
	}
	held = nil
	require.Equal(t, 12, pool.Stats().Idle)
}

func aicAssistantConcurrentCase(t *testing.T, db *gorm.DB) {
	t.Helper()
	aicAssistantSeed(t, db, 17, 91, 23, "Support")
	if db.Dialector.Name() == "sqlite" {
		aicAssistantWarmSQLiteConnections(t, db)
	}
	h := aicAssistantHandler(t, db)
	responses := make([]*httptest.ResponseRecorder, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			responses[i] = aicAssistantRequest(h, aicAssistantQuery())
		}(i)
	}
	wg.Wait()
	for _, response := range responses {
		aicAssistantResult(t, response, &repository.BoundAssistantSummary{ID: 17, Name: "Support"})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	value, err := repository.NewCaptainRepositoryWithOwnerPorts(db, nil, nil, nil).FindAssistantByInbox(ctx, 91, 23)
	require.Nil(t, value)
	require.EqualError(t, err, "bound assistant is unavailable")
}

func TestAICBoundAssistantConcurrentAndCancelled(t *testing.T) {
	aicAssistantConcurrentCase(t, newModuleTestDB(t, "AIC"))
}

func aicAssistantReadOnlyCase(t *testing.T, db *gorm.DB) {
	t.Helper()
	aicAssistantSeed(t, db, 17, 91, 23, "Support")
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			require.NoError(t, tx.Exec("SET TRANSACTION READ ONLY").Error)
		} else {
			require.NoError(t, tx.Exec("PRAGMA query_only = ON").Error)
			defer tx.Exec("PRAGMA query_only = OFF")
		}
		repo := repository.NewCaptainRepositoryWithOwnerPorts(tx, nil, nil, nil)
		value, err := repo.FindAssistantByInbox(context.Background(), 91, 23)
		require.NoError(t, err)
		require.Equal(t, &repository.BoundAssistantSummary{ID: 17, Name: "Support"}, value)
		value, err = repo.FindAssistantByInbox(context.Background(), 91, 24)
		require.NoError(t, err)
		require.Nil(t, value)
		return nil
	}))
}

func TestAICBoundAssistantReadOnly(t *testing.T) {
	aicAssistantReadOnlyCase(t, newModuleTestDB(t, "AIC"))
}

func TestPostgresAICBoundAssistant(t *testing.T) {
	t.Run("owner_only", func(t *testing.T) { aicAssistantOwnerCases(t, postgresModuleDB(t, "AIC")) })
	t.Run("minimal_projection", func(t *testing.T) { aicAssistantProjectionCase(t, postgresModuleDB(t, "AIC")) })
	t.Run("numeric_name_bounds", func(t *testing.T) { aicAssistantNumericCase(t, postgresModuleDB(t, "AIC")) })
	t.Run("snapshot_relationship", func(t *testing.T) { aicAssistantSnapshotCase(t, postgresModuleDB(t, "AIC")) })
	t.Run("concurrent_and_cancelled", func(t *testing.T) { aicAssistantConcurrentCase(t, postgresModuleDB(t, "AIC")) })
	t.Run("read_only", func(t *testing.T) { aicAssistantReadOnlyCase(t, postgresModuleDB(t, "AIC")) })
	for _, kind := range aicAssistantCorruptCases {
		t.Run("corruption/"+kind, func(t *testing.T) { aicAssistantCorruptCase(t, postgresModuleDB(t, "AIC"), kind) })
	}
	t.Run("cancelled_database_wait", func(t *testing.T) {
		db := postgresModuleDB(t, "AIC")
		aicAssistantSeed(t, db, 17, 91, 23, "Support")
		blocker := db.Begin()
		require.NoError(t, blocker.Error)
		defer blocker.Rollback()
		require.NoError(t, blocker.Exec("LOCK TABLE captain_inboxes IN ACCESS EXCLUSIVE MODE").Error)
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		value, err := repository.NewCaptainRepositoryWithOwnerPorts(db, nil, nil, nil).FindAssistantByInbox(ctx, 91, 23)
		require.Nil(t, value)
		require.EqualError(t, err, "bound assistant is unavailable")
		require.ErrorIs(t, ctx.Err(), context.DeadlineExceeded)
	})
}
