package modruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
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
	"gorm.io/gorm/logger"
	"log"
)

func rptEventsQuery() foundation.QueryEnvelope {
	return foundation.QueryEnvelope{QueryType: storedReportingEventsQuery, AccountID: 91, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, Filter: map[string]any{}, Sort: []string{"-id"}, Page: "1", Fields: append([]string{}, storedReportingEventsFields[:]...), Consistency: "strong", RequestedAt: time.Now().UTC()}
}
func rptEventsRuntime(t *testing.T, db *gorm.DB) *Runtime {
	t.Helper()
	cfg := businessConfig("RPT")
	cfg.MaxTimeout = 5 * time.Second
	runtime := &Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("RPT")}
	require.NoError(t, runtime.RegisterBusinessContracts())
	require.Len(t, runtime.Contracts.queries, 1)
	require.Empty(t, runtime.Contracts.commands)
	require.Empty(t, runtime.Contracts.events)
	return runtime
}
func rptEventsRequest(handler http.Handler, query foundation.QueryEnvelope, mutate ...func(*http.Request)) *httptest.ResponseRecorder {
	return performContractRequest(handler, "/contracts/queries/"+storedReportingEventsQuery, query, func(request *http.Request) {
		request.Header.Set("Authorization", "Bearer rpt-secret")
		request.Header.Set(headerCaller, "API")
		for _, fn := range mutate {
			fn(request)
		}
	})
}
func rptEventsPage(t *testing.T, w *httptest.ResponseRecorder) repository.StoredReportingEventPage {
	t.Helper()
	require.Equal(t, 200, w.Code, w.Body.String())
	var envelope struct {
		Data repository.StoredReportingEventPage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.NotNil(t, envelope.Data.Events)
	return envelope.Data
}
func rptEventsSeed(t *testing.T, db *gorm.DB, id, account uint, name, metadata string, conversation *uint) {
	t.Helper()
	stamp := time.Date(2025, 2, 3, 4, 5, 6, 123456000, time.UTC)
	require.NoError(t, db.Model(&domain.ReportingEvent{}).Create(map[string]any{"id": id, "account_id": account, "conversation_id": conversation, "user_id": nil, "inbox_id": nil, "name": name, "value": 0, "event_start_time": stamp, "event_end_time": stamp.Add(time.Minute), "metadata_json": metadata, "created_at": stamp}).Error)
}
func rptEventsRows(t *testing.T, db *gorm.DB) []domain.ReportingEvent {
	t.Helper()
	var rows []domain.ReportingEvent
	require.NoError(t, db.Order("id ASC").Find(&rows).Error)
	return rows
}
func rptEventsOwnerOnly(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, foreign := range []any{&domain.User{}, &domain.Account{}, &domain.AccountUser{}, &domain.Conversation{}, &domain.Inbox{}, &domain.AccountFeature{}, &domain.CustomFilter{}} {
		require.False(t, db.Migrator().HasTable(foreign), "foreign table %T", foreign)
	}
}
func rptEventsOwnerCases(t *testing.T, db *gorm.DB) {
	handler := rptEventsRuntime(t, db).Handler()
	empty := rptEventsPage(t, rptEventsRequest(handler, rptEventsQuery()))
	require.Empty(t, empty.Events)
	require.False(t, empty.HasMore)
	require.Empty(t, empty.NextCursor)
	large := uint(9007199254740993)
	rptEventsSeed(t, db, 1, 91, "exact", "malformed { opaque", nil)
	rptEventsSeed(t, db, large, 91, " Exact ", `{"conversation_id":9007199254740993}`, &large)
	rptEventsSeed(t, db, large+1, 92, "private-marker", "private-marker"+strings.Repeat("x", 17000), nil)
	before := rptEventsRows(t, db)
	for _, actor := range []foundation.ContractActor{{Type: "User", ID: "7", AuthSource: "module"}, {Type: "System", ID: "api", AuthSource: "module"}, {Type: "SuperAdmin", ID: "root", AuthSource: "jwt"}, {Type: "fabricated", ID: "private-marker", AuthSource: "unverified"}} {
		q := rptEventsQuery()
		q.Actor = actor
		w := rptEventsRequest(handler, q)
		page := rptEventsPage(t, w)
		require.Len(t, page.Events, 2)
		require.Equal(t, large, page.Events[0].ID)
		require.Equal(t, "malformed { opaque", page.Events[1].MetadataJSON)
		require.Zero(t, page.Events[0].Value)
		require.NotContains(t, w.Body.String(), "private-marker")
		var raw struct {
			Data struct {
				Events []map[string]any `json:"events"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
		require.Len(t, raw.Data.Events[0], 11)
		require.Nil(t, raw.Data.Events[1]["conversation_id"])
	}
	q := rptEventsQuery()
	q.Filter["name"] = " Exact "
	require.Len(t, rptEventsPage(t, rptEventsRequest(handler, q)).Events, 1)
	q.Filter["name"] = "Exact"
	require.Empty(t, rptEventsPage(t, rptEventsRequest(handler, q)).Events)
	q = rptEventsQuery()
	q.Filter["conversation_id"] = large
	require.Equal(t, large, rptEventsPage(t, rptEventsRequest(handler, q)).Events[0].ID)
	q.AccountID = 93
	require.Empty(t, rptEventsPage(t, rptEventsRequest(handler, q)).Events)
	require.Equal(t, before, rptEventsRows(t, db))
	rptEventsOwnerOnly(t, db)
}
func TestRPTStoredEventsOwnerOnly(t *testing.T) { rptEventsOwnerCases(t, newModuleTestDB(t, "RPT")) }

func rptEventsAssociationCases(t *testing.T, db *gorm.DB) {
	expected := uint(9007199254740993)
	other := expected + 1
	entries := []struct {
		metadata string
		direct   *uint
		match    bool
	}{
		{`{"conversation_id":9007199254740993}`, nil, true}, {` { "conversation_id" : 9007199254740993, "x":null } `, nil, true},
		{`{"conversation_id":9007199254740994}`, nil, false}, {`{"conversation_id":90071992547409930}`, nil, false},
		{`{"conversation_id":"9007199254740993"}`, nil, false}, {`{"nested":{"conversation_id":9007199254740993}}`, nil, false},
		{`{"conversation_id":9007199254740993.0}`, nil, false}, {`{"conversation_id":9.007199254740993e15}`, nil, false},
		{`{"conversation_id":09007199254740993}`, nil, false}, {`{"conversation_id":9007199254740993,"conversation_id":9007199254740994}`, nil, false},
		{`{"conversation_id":9007199254740993,"\u0063onversation_id":9007199254740993}`, nil, false},
		{`{"conversation_id":9007199254740993,"x":1,"x":2}`, nil, false}, {`{"conversation_id":-9007199254740993}`, nil, false},
		{`{"conversation_id":9007199254740993} trailing`, nil, false}, {`[{"conversation_id":9007199254740993}]`, nil, false},
		{`{"conversation_id":9007199254740993}`, &other, true}, {`{"conversation_id":9007199254740994}`, &expected, true}, {`invalid`, &expected, true},
	}
	expectedIDs := []uint{}
	for i, entry := range entries {
		id := uint(i + 1)
		rptEventsSeed(t, db, id, 91, "event", entry.metadata, entry.direct)
		if entry.match {
			expectedIDs = append([]uint{id}, expectedIDs...)
		}
	}
	q := rptEventsQuery()
	q.Filter["conversation_id"] = expected
	page := rptEventsPage(t, rptEventsRequest(rptEventsRuntime(t, db).Handler(), q))
	actual := []uint{}
	for _, event := range page.Events {
		actual = append(actual, event.ID)
	}
	require.Equal(t, expectedIDs, actual)
}
func TestRPTStoredEventsExactAssociation(t *testing.T) {
	rptEventsAssociationCases(t, newModuleTestDB(t, "RPT"))
}

func rptEventsPaginationCases(t *testing.T, db *gorm.DB) {
	for i := 1; i <= 235; i++ {
		metadata := `{}`
		if i <= 30 {
			metadata = `{"conversation_id":7}`
		}
		rptEventsSeed(t, db, uint(i), 91, "event", metadata, nil)
	}
	rptEventsSeed(t, db, 1000, 92, "event", `{"conversation_id":7}`, nil)
	rptEventsSeed(t, db, 1001, 91, "other", `{"conversation_id":7}`, nil)
	q := rptEventsQuery()
	q.Filter = map[string]any{"name": "event", "conversation_id": 7}
	handler := rptEventsRuntime(t, db).Handler()
	page := rptEventsPage(t, rptEventsRequest(handler, q))
	require.Empty(t, page.Events)
	require.True(t, page.HasMore)
	require.Equal(t, "136", page.NextCursor)
	q.Page = ""
	q.Cursor = page.NextCursor
	page = rptEventsPage(t, rptEventsRequest(handler, q))
	require.Empty(t, page.Events)
	require.True(t, page.HasMore)
	require.Equal(t, "36", page.NextCursor)
	q.Cursor = page.NextCursor
	page = rptEventsPage(t, rptEventsRequest(handler, q))
	require.Len(t, page.Events, 25)
	require.Equal(t, uint(30), page.Events[0].ID)
	require.Equal(t, uint(6), page.Events[24].ID)
	require.Equal(t, "6", page.NextCursor)
	// A fresh runtime can resume the same canonical cursor without process state.
	q.Cursor = page.NextCursor
	page = rptEventsPage(t, rptEventsRequest(rptEventsRuntime(t, db).Handler(), q))
	require.Len(t, page.Events, 5)
	require.Equal(t, uint(5), page.Events[0].ID)
	require.Equal(t, uint(1), page.Events[4].ID)
	require.False(t, page.HasMore)
	require.Empty(t, page.NextCursor)
	q = rptEventsQuery()
	q.Filter["name"] = "event"
	ids := []uint{}
	for {
		page = rptEventsPage(t, rptEventsRequest(handler, q))
		for _, event := range page.Events {
			ids = append(ids, event.ID)
		}
		if !page.HasMore {
			break
		}
		q.Page = ""
		q.Cursor = page.NextCursor
	}
	require.Len(t, ids, 235)
	for i, id := range ids {
		require.Equal(t, uint(235-i), id)
	}
}
func TestRPTStoredEventsSparsePagination(t *testing.T) {
	rptEventsPaginationCases(t, newModuleTestDB(t, "RPT"))
}

func TestRPTStoredEventsQueryShapeAndTransport(t *testing.T) {
	db := newModuleTestDB(t, "RPT")
	handler := rptEventsRuntime(t, db).Handler()
	for name, edit := range map[string]func(*foundation.QueryEnvelope){"account_filter": func(q *foundation.QueryEnvelope) { q.Filter["account_id"] = 92 }, "extra_filter": func(q *foundation.QueryEnvelope) { q.Filter["limit"] = 25 }, "projection": func(q *foundation.QueryEnvelope) { q.Fields = []string{"*"} }, "reordered": func(q *foundation.QueryEnvelope) { q.Fields[0], q.Fields[1] = q.Fields[1], q.Fields[0] }, "duplicate": func(q *foundation.QueryEnvelope) { q.Fields[0] = "name" }, "sort": func(q *foundation.QueryEnvelope) { q.Sort = []string{"id"} }, "extra_sort": func(q *foundation.QueryEnvelope) { q.Sort = append(q.Sort, "name") }, "consistency": func(q *foundation.QueryEnvelope) { q.Consistency = "eventual" }, "page": func(q *foundation.QueryEnvelope) { q.Page = "2" }, "both_page_cursor": func(q *foundation.QueryEnvelope) { q.Cursor = "1" }, "oversize_name": func(q *foundation.QueryEnvelope) { q.Filter["name"] = strings.Repeat("x", 1021) }, "nonstring_name": func(q *foundation.QueryEnvelope) { q.Filter["name"] = 1 }} {
		t.Run(name, func(t *testing.T) {
			q := rptEventsQuery()
			edit(&q)
			sysPublicFailure(t, rptEventsRequest(handler, q), 400, "validation_failed", "stored reporting events query shape is unsupported")
		})
	}
	for _, id := range []any{"7", 0, -1, 1.5, json.Number("1e2"), json.Number("1.0"), uint64(math.MaxUint64), nil, true, []int{7}} {
		q := rptEventsQuery()
		q.Filter["conversation_id"] = id
		sysPublicFailure(t, rptEventsRequest(handler, q), 400, "validation_failed", "stored reporting events query shape is unsupported")
	}
	for _, cursor := range []string{"0", "01", "+1", "-1", "1.0", "1e2", " 1", "9223372036854775808", "private-marker"} {
		q := rptEventsQuery()
		q.Page = ""
		q.Cursor = cursor
		sysPublicFailure(t, rptEventsRequest(handler, q), 400, "validation_failed", "stored reporting events query shape is unsupported")
	}
	for _, edit := range []func(*foundation.QueryEnvelope){func(q *foundation.QueryEnvelope) { q.AccountID = 0 }, func(q *foundation.QueryEnvelope) { q.Actor = foundation.ContractActor{} }, func(q *foundation.QueryEnvelope) { q.Filter = nil }, func(q *foundation.QueryEnvelope) { q.RequestedAt = time.Time{} }, func(q *foundation.QueryEnvelope) { q.Page = "" }} {
		q := rptEventsQuery()
		edit(&q)
		sysPublicFailure(t, rptEventsRequest(handler, q), 400, "validation_failed", "query envelope is invalid")
	}
	for _, edit := range []func(*http.Request){func(r *http.Request) { r.Header.Del("Authorization") }, func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }, func(r *http.Request) { r.Header.Del(headerCaller) }, func(r *http.Request) { r.Header.Set(headerCaller, "unknown") }} {
		sysPublicFailure(t, rptEventsRequest(handler, rptEventsQuery(), edit), 401, "authentication_failed", "module caller authentication failed")
	}
	encoded, err := json.Marshal(rptEventsQuery())
	require.NoError(t, err)
	for _, raw := range [][]byte{append(append([]byte{}, encoded...), []byte(` {}`)...), append(append([]byte{}, encoded...), []byte(` private-marker`)...), bytes.Replace(encoded, []byte(`"filter":{}`), []byte(`"filter":{},"unknown":"private-marker"`), 1), bytes.Replace(encoded, []byte(`"filter":{}`), []byte(`"filter":{"name":"`+strings.Repeat("x", 5000)+`"}`), 1)} {
		req := httptest.NewRequest(http.MethodPost, "/contracts/queries/"+storedReportingEventsQuery, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer rpt-secret")
		req.Header.Set(headerCaller, "API")
		req.Header.Set(headerContractVersion, "v1")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		sysPublicFailure(t, w, 400, "validation_failed", "query envelope is invalid")
	}
	for _, path := range []string{"queries/rpt.metrics.get.v1", "queries/rpt.sla.get.v1", "commands/rpt.report.generate.v1", "commands/rpt.export.create.v1"} {
		w := performContractRequest(handler, "/contracts/"+path, rptEventsQuery(), func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer rpt-secret")
			r.Header.Set(headerCaller, "API")
		})
		require.Equal(t, 404, w.Code)
	}
}

func TestRPTStoredEventsSnapshotPreservesZero(t *testing.T) {
	source, target := newModuleTestDB(t, "RPT"), newModuleTestDB(t, "RPT")
	rptEventsSnapshotCases(t, source, target)
}
func rptEventsSnapshotCases(t *testing.T, source, target *gorm.DB) {
	large := uint(9007199254740993)
	rptEventsSeed(t, source, large, 91, "event", "opaque {", &large)
	rptEventsSeed(t, source, large+1, 92, "other", "{}", nil)
	require.NoError(t, source.Model(&domain.ReportingEvent{}).Where("id = ?", large).Updates(map[string]any{"created_at": time.Time{}, "event_start_time": time.Time{}, "event_end_time": time.Time{}}).Error)
	expected := rptEventsRows(t, source)
	path := filepath.Join(t.TempDir(), "rpt.snapshot.json")
	require.NoError(t, CreateSnapshot(source, "RPT", "rpt-read", path))
	require.NoError(t, RestoreSnapshot(context.Background(), target, "RPT", "rpt-read", path, nil))
	require.Equal(t, expected, rptEventsRows(t, target), "stored zero and exact owner records must survive restore")
	page := rptEventsPage(t, rptEventsRequest(rptEventsRuntime(t, target).Handler(), rptEventsQuery()))
	require.Len(t, page.Events, 1)
	require.Zero(t, page.Events[0].Value)
	rptEventsOwnerOnly(t, target)
	if target.Dialector.Name() == "postgres" {
		row := domain.ReportingEvent{AccountID: 91, Name: "after-restore", MetadataJSON: "{}"}
		require.NoError(t, target.Create(&row).Error)
		require.Greater(t, row.ID, large+1)
	}
}

func TestRPTStoredEventsReadOnlyCancellationAndConcurrency(t *testing.T) {
	db := newModuleTestDB(t, "RPT")
	rptEventsSeed(t, db, 1, 91, "event", "{}", nil)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.Exec("PRAGMA query_only = ON").Error)
	t.Cleanup(func() { require.NoError(t, db.Exec("PRAGMA query_only = OFF").Error) })
	runtime := rptEventsRuntime(t, db)
	handler := runtime.Handler()
	rptEventsPage(t, rptEventsRequest(handler, rptEventsQuery()))
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := rptEventsRequest(handler, rptEventsQuery())
			if w.Code != 200 {
				t.Errorf("concurrent read: %d", w.Code)
			}
		}()
	}
	wg.Wait()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = runtime.Contracts.queries[storedReportingEventsQuery](ctx, rptEventsQuery())
	require.ErrorContains(t, err, "stored reporting events are unavailable")
}

func TestRPTStoredEventsStorageFailuresAreStatic(t *testing.T) {
	for label, statement := range map[string]string{"null_metadata": "UPDATE reporting_events SET metadata_json=NULL", "blob_metadata": "UPDATE reporting_events SET metadata_json=x'707269766174652d6d61726b6572'", "invalid_utf8": "UPDATE reporting_events SET metadata_json=CAST(x'ff' AS TEXT)", "huge_metadata": "UPDATE reporting_events SET metadata_json=?", "invalid_value": "UPDATE reporting_events SET value='private-marker'", "null_value": "UPDATE reporting_events SET value=NULL", "infinite_value": "UPDATE reporting_events SET value=1e999", "huge_numeric_text": "UPDATE reporting_events SET value=?", "bad_optional_id": "UPDATE reporting_events SET user_id='private-marker'", "bad_time": "UPDATE reporting_events SET event_start_time='private-marker'", "null_time": "UPDATE reporting_events SET created_at=NULL", "blob_name": "UPDATE reporting_events SET name=x'61'", "oversize_name": "UPDATE reporting_events SET name=?"} {
		t.Run(label, func(t *testing.T) {
			db := newModuleTestDB(t, "RPT")
			rptEventsSeed(t, db, 1, 91, "event", "{}", nil)
			args := []any{}
			if strings.Contains(statement, "?") {
				args = append(args, "private-marker"+strings.Repeat("x", 17000))
			}
			require.NoError(t, db.Exec(statement, args...).Error)
			var output bytes.Buffer
			db = db.Session(&gorm.Session{Logger: logger.New(log.New(&output, "", 0), logger.Config{LogLevel: logger.Info})})
			w := rptEventsRequest(rptEventsRuntime(t, db).Handler(), rptEventsQuery())
			sysPublicFailure(t, w, 503, "dependency_unavailable", "stored reporting events are unavailable")
			require.NotContains(t, output.String(), "private-marker")
			require.NotContains(t, output.String(), "SELECT")
		})
	}
	db := newModuleTestDB(t, "RPT")
	require.NoError(t, db.Callback().Row().Before("gorm:row").Register("rpt-read-failure", func(tx *gorm.DB) { tx.AddError(errors.New("private-marker")) }))
	sysPublicFailure(t, rptEventsRequest(rptEventsRuntime(t, db).Handler(), rptEventsQuery()), 503, "dependency_unavailable", "stored reporting events are unavailable")
}

func rptEventsBoundCases(t *testing.T, db *gorm.DB) {
	rptEventsSeed(t, db, 1, 91, strings.Repeat("😀", 255), strings.Repeat("x", repository.ReportingEventMetadataBytes), nil)
	page := rptEventsPage(t, rptEventsRequest(rptEventsRuntime(t, db).Handler(), rptEventsQuery()))
	require.Len(t, page.Events, 1)
	require.Len(t, page.Events[0].Name, 1020)
	require.Len(t, page.Events[0].MetadataJSON, 16384)
	// Escaping can multiply encoded bytes even though each stored field is bounded.
	require.NoError(t, db.Where("id = ?", 1).Delete(&domain.ReportingEvent{}).Error)
	for i := 1; i <= 25; i++ {
		rptEventsSeed(t, db, uint(i), 91, "event", strings.Repeat("<", 16384), nil)
	}
	sysPublicFailure(t, rptEventsRequest(rptEventsRuntime(t, db).Handler(), rptEventsQuery()), 503, "dependency_unavailable", "stored reporting events are unavailable")
}
func TestRPTStoredEventsBounds(t *testing.T) { rptEventsBoundCases(t, newModuleTestDB(t, "RPT")) }

func TestPostgresRPTStoredEvents(t *testing.T) {
	t.Run("snapshot_source_scalars", func(t *testing.T) {
		rptSnapshotSourceScalarCases(t, func(t *testing.T) *gorm.DB { return postgresModuleDB(t, "RPT") })
	})
	t.Run("snapshot_complete_metadata", func(t *testing.T) {
		rptSnapshotCompleteMetadataCase(t, postgresModuleDB(t, "RPT"), postgresModuleDB(t, "RPT"))
	})
	t.Run("snapshot_precision_rejection", func(t *testing.T) {
		source, target := newModuleTestDB(t, "RPT"), postgresModuleDB(t, "RPT")
		rptEventsSeed(t, source, 1, 91, "event", "{}", nil)
		rptEventsSeed(t, target, 2, 91, "retained", "{}", nil)
		before := rptEventsRows(t, target)
		stamp := time.Date(2025, 1, 2, 3, 4, 5, 987654321, time.UTC)
		require.NoError(t, source.Model(&domain.ReportingEvent{}).Where("id = ?", 1).Update("created_at", stamp).Error)
		path := filepath.Join(t.TempDir(), "nanos.json")
		require.NoError(t, CreateSnapshot(source, "RPT", "rpt-nanos", path))
		err := RestoreSnapshot(context.Background(), target, "RPT", "rpt-nanos", path, nil)
		require.Error(t, err)
		require.Equal(t, before, rptEventsRows(t, target))
	})
	t.Run("timestamp_session_settings", func(t *testing.T) {
		db := postgresModuleDB(t, "RPT")
		rptEventsSeed(t, db, 1, 91, "event", "{}", nil)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			require.NoError(t, tx.Exec("SET LOCAL TIME ZONE 'America/New_York'").Error)
			require.NoError(t, tx.Exec("SET LOCAL DateStyle = 'SQL, DMY'").Error)
			for _, stamp := range []time.Time{time.Time{}, time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(1850, 1, 1, 0, 0, 0, 123456000, time.UTC), time.Date(9999, 12, 31, 23, 59, 59, 999999000, time.UTC)} {
				require.NoError(t, tx.Model(&domain.ReportingEvent{}).Where("id = ?", 1).Updates(map[string]any{"created_at": stamp, "event_start_time": stamp, "event_end_time": stamp}).Error)
				page, err := repository.NewReportingEventReadRepository(tx).ListStored(context.Background(), 91, nil, 0, 0)
				require.NoError(t, err)
				require.Len(t, page.Events, 1)
				require.True(t, stamp.Equal(page.Events[0].CreatedAt))
				rows, err := repository.NewReportingEventReadRepository(tx).SnapshotRows(context.Background())
				require.NoError(t, err)
				require.True(t, stamp.Equal(rows[0].CreatedAt))
			}
			return nil
		}))
	})

	t.Run("owner_only", func(t *testing.T) { rptEventsOwnerCases(t, postgresModuleDB(t, "RPT")) })
	t.Run("exact_association", func(t *testing.T) { rptEventsAssociationCases(t, postgresModuleDB(t, "RPT")) })
	t.Run("sparse_pagination", func(t *testing.T) { rptEventsPaginationCases(t, postgresModuleDB(t, "RPT")) })
	t.Run("bounds", func(t *testing.T) { rptEventsBoundCases(t, postgresModuleDB(t, "RPT")) })
	t.Run("snapshot_validation", func(t *testing.T) {
		rptSnapshotValidationCases(t, postgresModuleDB(t, "RPT"), postgresModuleDB(t, "RPT"))
	})
	t.Run("encoded_boundary", func(t *testing.T) {
		for _, extra := range []int{0, 1} {
			rptEventsEncodedBoundary(t, postgresModuleDB(t, "RPT"), extra)
		}
	})
	t.Run("snapshot", func(t *testing.T) { rptEventsSnapshotCases(t, postgresModuleDB(t, "RPT"), postgresModuleDB(t, "RPT")) })
	for name, value := range map[string]string{"nan": "NaN", "positive_infinity": "Infinity", "negative_infinity": "-Infinity"} {
		t.Run(name, func(t *testing.T) {
			db := postgresModuleDB(t, "RPT")
			rptEventsSeed(t, db, 1, 91, "event", "{}", nil)
			require.NoError(t, db.Exec("UPDATE reporting_events SET value = ?", value).Error)
			sysPublicFailure(t, rptEventsRequest(rptEventsRuntime(t, db).Handler(), rptEventsQuery()), 503, "dependency_unavailable", "stored reporting events are unavailable")
		})
	}
	t.Run("infinite_time", func(t *testing.T) {
		db := postgresModuleDB(t, "RPT")
		rptEventsSeed(t, db, 1, 91, "event", "{}", nil)
		require.NoError(t, db.Exec("UPDATE reporting_events SET created_at='infinity'").Error)
		sysPublicFailure(t, rptEventsRequest(rptEventsRuntime(t, db).Handler(), rptEventsQuery()), 503, "dependency_unavailable", "stored reporting events are unavailable")
	})
	t.Run("numeric_text", func(t *testing.T) {
		db := postgresModuleDB(t, "RPT")
		rptEventsSeed(t, db, 1, 91, "event", "{}", nil)
		for _, value := range []float64{0, -1.25, math.MaxFloat64, math.SmallestNonzeroFloat64} {
			require.NoError(t, db.Exec("UPDATE reporting_events SET value = ?", value).Error)
			page := rptEventsPage(t, rptEventsRequest(rptEventsRuntime(t, db).Handler(), rptEventsQuery()))
			require.Equal(t, value, page.Events[0].Value)
		}
		for _, value := range []string{strings.Repeat("9", 400), "0." + strings.Repeat("0", 400) + "1", strings.Repeat("9", 2000), "0." + strings.Repeat("0", 2000) + "1"} {
			require.NoError(t, db.Exec("UPDATE reporting_events SET value = ?", value).Error)
			sysPublicFailure(t, rptEventsRequest(rptEventsRuntime(t, db).Handler(), rptEventsQuery()), 503, "dependency_unavailable", "stored reporting events are unavailable")
		}
	})
	t.Run("read_only", func(t *testing.T) {
		db := postgresModuleDB(t, "RPT")
		rptEventsSeed(t, db, 1, 91, "event", "{}", nil)
		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SET TRANSACTION READ ONLY").Error; err != nil {
				return err
			}
			page, err := repository.NewReportingEventReadRepository(tx).ListStored(context.Background(), 91, nil, 0, 0)
			require.Len(t, page.Events, 1)
			return err
		}))
	})
}
