package modruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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
)

func cannedPreviewQuery(id uint) foundation.QueryEnvelope {
	q := cannedReadQuery("detail")
	q.QueryType = cannedResponsePreview
	q.Filter = map[string]any{"id": json.Number(strconv.FormatUint(uint64(id), 10)), "variables": map[string]any{}}
	q.Fields = append([]string(nil), cannedPreviewFields[:]...)
	return q
}
func (f *cannedReadFixture) preview(q foundation.QueryEnvelope, change func(*http.Request)) *httptest.ResponseRecorder {
	return performContractRequest(f.runtime.Handler(), "/contracts/queries/"+cannedResponsePreview, q, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+f.runtime.Config.AuthToken)
		r.Header.Set(headerCaller, "API")
		if change != nil {
			change(r)
		}
	})
}
func previewResult(t *testing.T, out *httptest.ResponseRecorder, status int) cannedPreviewView {
	t.Helper()
	require.Equal(t, status, out.Code, out.Body.String())
	var body struct{ Data cannedPreviewView }
	require.NoError(t, json.Unmarshal(out.Body.Bytes(), &body))
	if status != 200 {
		var raw map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(out.Body.Bytes(), &raw))
		require.NotContains(t, raw, "data")
		require.NotContains(t, out.Body.String(), "resolved_content")
	}
	return body.Data
}

type cannedPreviewSQLLog struct {
	logger.Interface
	mu      sync.Mutex
	queries []string
}

func (l *cannedPreviewSQLLog) Trace(ctx context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, _ := fc()
	l.mu.Lock()
	l.queries = append(l.queries, sql)
	l.mu.Unlock()
}
func (l *cannedPreviewSQLLog) take() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	result := append([]string(nil), l.queries...)
	l.queries = nil
	return result
}
func assertPreviewSQL(t *testing.T, queries []string, count int) {
	t.Helper()
	require.Len(t, queries, count)
	for _, sql := range queries {
		require.True(t, strings.HasPrefix(sql, "SELECT "), sql)
		require.Contains(t, strings.ReplaceAll(sql, "`", `"`), `FROM "canned_responses"`)
		require.Contains(t, sql, "object_version")
		require.Contains(t, sql, "account_id")
		require.NotContains(t, sql, " JOIN ")
	}
}

func cannedPreviewSemantics(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	f := newCannedReadFixture(t, open)
	rows := []domain.CannedResponse{
		{AccountID: 91, ShortCode: "legacy", Content: "你好 {{name}} / {{name}}", ObjectVersion: 0},
		{AccountID: 91, ShortCode: "versioned", Content: "{{value}}", ObjectVersion: 9007199254740993},
		{AccountID: 92, ShortCode: "foreign", Content: "private"},
		{AccountID: 91, ShortCode: "malformed", Content: "legacy prose {{ unfinished"},
		{AccountID: 91, ShortCode: "missing", Content: "secret {{missing}}"},
		{ID: 9007199254740993, AccountID: 91, ShortCode: "precise", Content: "exact", ObjectVersion: 9007199254740995},
	}
	for i := range rows {
		require.NoError(t, f.runtime.DB.Create(&rows[i]).Error)
	}
	var before []domain.CannedResponse
	require.NoError(t, f.runtime.DB.Order("id").Find(&before).Error)
	log := &cannedPreviewSQLLog{Interface: logger.Default.LogMode(logger.Silent)}
	f.runtime.DB = f.runtime.DB.Session(&gorm.Session{Logger: log})
	t.Run("legacy_unicode_repeated_and_fresh_member_check", func(t *testing.T) {
		q := cannedPreviewQuery(rows[0].ID)
		q.Filter["variables"] = map[string]any{"name": "世界", "extra": "unused"}
		calls := f.calls.Load()
		for i := 0; i < 2; i++ {
			view := previewResult(t, f.preview(q, nil), 200)
			require.Equal(t, "你好 世界 / 世界", view.ResolvedContent)
			require.Equal(t, int64(0), view.ObjectVersion)
			require.Equal(t, rows[0].ID, view.ID)
			require.Equal(t, uint(91), view.AccountID)
			require.Equal(t, "legacy", view.ShortCode)
		}
		require.Equal(t, int64(2), f.calls.Load()-calls)
		assertPreviewSQL(t, log.take(), 2)
	})
	t.Run("literal_empty_and_admin", func(t *testing.T) {
		q := cannedPreviewQuery(rows[1].ID)
		q.Actor.ID = "8"
		q.Filter["variables"] = map[string]any{"value": "{{missing}} <b>&\"</b>"}
		out := f.preview(q, nil)
		view := previewResult(t, out, 200)
		require.Equal(t, "{{missing}} <b>&\"</b>", view.ResolvedContent)
		require.Equal(t, int64(9007199254740993), view.ObjectVersion)
		var body struct{ Data map[string]json.RawMessage }
		require.NoError(t, json.Unmarshal(out.Body.Bytes(), &body))
		require.Len(t, body.Data, 5)
		for _, key := range cannedPreviewFields {
			require.Contains(t, body.Data, key)
		}
		q.Filter["variables"] = map[string]any{"value": ""}
		require.Empty(t, previewResult(t, f.preview(q, nil), 200).ResolvedContent)
		assertPreviewSQL(t, log.take(), 2)
	})
	t.Run("exact_wire_identity_and_version", func(t *testing.T) {
		out := f.preview(cannedPreviewQuery(rows[5].ID), nil)
		view := previewResult(t, out, 200)
		require.Equal(t, rows[5].ID, view.ID)
		require.Equal(t, rows[5].ObjectVersion, view.ObjectVersion)
		require.Contains(t, out.Body.String(), `"id":9007199254740993`)
		require.Contains(t, out.Body.String(), `"object_version":9007199254740995`)
		previewResult(t, f.preview(cannedPreviewQuery(rows[5].ID-1), nil), 404)
		assertPreviewSQL(t, log.take(), 2)
	})
	t.Run("foreign_and_missing", func(t *testing.T) {
		for _, id := range []uint{rows[2].ID, 9007199254740980} {
			previewResult(t, f.preview(cannedPreviewQuery(id), nil), 404)
		}
		assertPreviewSQL(t, log.take(), 2)
	})
	t.Run("visible_errors_preserve_legacy_reads", func(t *testing.T) {
		for _, i := range []int{3, 4} {
			out := f.preview(cannedPreviewQuery(rows[i].ID), nil)
			previewResult(t, out, 400)
			require.Contains(t, out.Body.String(), "source byte")
			require.NotContains(t, out.Body.String(), "secret")
			assertPreviewSQL(t, log.take(), 1)
			for _, mode := range []string{"detail", "detail_versioned"} {
				q := cannedReadQuery(mode)
				q.Filter["id"] = rows[i].ID
				if mode == "detail_versioned" {
					q.Fields = append(q.Fields, "object_version")
				}
				raw := f.query(t, q, 200)
				require.Equal(t, rows[i].Content, cannedReadItem(t, raw).Content)
			}
			log.take()
		}
	})
	t.Run("zero_effects", func(t *testing.T) { assertCannedReadOnly(t, f, before); cannedCounts(t, f, 6, 0, 0); log.take() })
	t.Run("revoked_membership", func(t *testing.T) {
		require.NoError(t, f.ten.Where("account_id = ? AND user_id = ?", 91, 7).Delete(&domain.AccountUser{}).Error)
		previewResult(t, f.preview(cannedPreviewQuery(rows[5].ID), nil), 404)
		require.Empty(t, log.take())
	})
}
func TestOPSCannedResponsePreviewSemantics(t *testing.T) { cannedPreviewSemantics(t, newModuleTestDB) }
func TestPostgresOPSCannedResponsePreviewSemantics(t *testing.T) {
	cannedPreviewSemantics(t, postgresModuleDB)
}

func cannedPreviewValidation(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	f := newCannedReadFixture(t, open)
	row := domain.CannedResponse{AccountID: 91, ShortCode: "x", Content: "{{name}}"}
	require.NoError(t, f.runtime.DB.Create(&row).Error)
	cases := map[string]func(*foundation.QueryEnvelope){
		"unknown_filter":    func(q *foundation.QueryEnvelope) { q.Filter["contact_id"] = 1 },
		"missing_variables": func(q *foundation.QueryEnvelope) { delete(q.Filter, "variables") },
		"null_variables":    func(q *foundation.QueryEnvelope) { q.Filter["variables"] = nil },
		"array_variables":   func(q *foundation.QueryEnvelope) { q.Filter["variables"] = []any{} },
		"string_variables":  func(q *foundation.QueryEnvelope) { q.Filter["variables"] = "x" },
		"null_value":        func(q *foundation.QueryEnvelope) { q.Filter["variables"] = map[string]any{"name": nil} },
		"number_value":      func(q *foundation.QueryEnvelope) { q.Filter["variables"] = map[string]any{"name": 12} },
		"bool_value":        func(q *foundation.QueryEnvelope) { q.Filter["variables"] = map[string]any{"name": true} },
		"object_value":      func(q *foundation.QueryEnvelope) { q.Filter["variables"] = map[string]any{"name": map[string]any{}} },
		"empty_name":        func(q *foundation.QueryEnvelope) { q.Filter["variables"] = map[string]any{"": "x"} },
		"dotted_name":       func(q *foundation.QueryEnvelope) { q.Filter["variables"] = map[string]any{"contact.name": "x"} },
		"space_name":        func(q *foundation.QueryEnvelope) { q.Filter["variables"] = map[string]any{" name": "x"} },
		"unicode_name":      func(q *foundation.QueryEnvelope) { q.Filter["variables"] = map[string]any{"姓名": "x"} },
		"missing_id":        func(q *foundation.QueryEnvelope) { delete(q.Filter, "id") },
		"null_id":           func(q *foundation.QueryEnvelope) { q.Filter["id"] = nil },
		"string_id":         func(q *foundation.QueryEnvelope) { q.Filter["id"] = "1" },
		"float_id":          func(q *foundation.QueryEnvelope) { q.Filter["id"] = json.Number("1.0") },
		"exponent_id":       func(q *foundation.QueryEnvelope) { q.Filter["id"] = json.Number("1e0") },
		"negative_id":       func(q *foundation.QueryEnvelope) { q.Filter["id"] = -1 },
		"zero_id":           func(q *foundation.QueryEnvelope) { q.Filter["id"] = 0 },
		"overflow_id":       func(q *foundation.QueryEnvelope) { q.Filter["id"] = json.Number("9223372036854775808") },
		"zero_account":      func(q *foundation.QueryEnvelope) { q.AccountID = 0 },
		"overflow_account":  func(q *foundation.QueryEnvelope) { q.AccountID = 9223372036854775808 },
		"fields_extra":      func(q *foundation.QueryEnvelope) { q.Fields = append(q.Fields, "content") },
		"fields_reordered":  func(q *foundation.QueryEnvelope) { q.Fields[0], q.Fields[1] = q.Fields[1], q.Fields[0] },
		"fields_missing":    func(q *foundation.QueryEnvelope) { q.Fields = nil },
		"sort_extra":        func(q *foundation.QueryEnvelope) { q.Sort = append(q.Sort, "content") },
		"sort_descending":   func(q *foundation.QueryEnvelope) { q.Sort = []string{"-id"} },
		"page":              func(q *foundation.QueryEnvelope) { q.Page = "2" },
		"cursor":            func(q *foundation.QueryEnvelope) { q.Cursor = "1" },
		"consistency":       func(q *foundation.QueryEnvelope) { q.Consistency = "eventual" },
		"query_type":        func(q *foundation.QueryEnvelope) { q.QueryType = cannedResponseGet },
	}
	log := &cannedPreviewSQLLog{Interface: logger.Default.LogMode(logger.Silent)}
	f.runtime.DB = f.runtime.DB.Session(&gorm.Session{Logger: log})
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			q := cannedPreviewQuery(row.ID)
			change(&q)
			calls := f.calls.Load()
			previewResult(t, f.preview(q, nil), 400)
			require.Equal(t, calls, f.calls.Load())
			require.Empty(t, log.take())
		})
	}
	for name, change := range map[string]func(*foundation.QueryEnvelope){"actor_type": func(q *foundation.QueryEnvelope) { q.Actor.Type = "System" }, "actor_source": func(q *foundation.QueryEnvelope) { q.Actor.AuthSource = "rest" }, "actor_id": func(q *foundation.QueryEnvelope) { q.Actor.ID = "07" }} {
		t.Run(name, func(t *testing.T) {
			q := cannedPreviewQuery(row.ID)
			change(&q)
			previewResult(t, f.preview(q, nil), 403)
			require.Empty(t, log.take())
		})
	}
	t.Run("authentication", func(t *testing.T) {
		previewResult(t, f.preview(cannedPreviewQuery(row.ID), func(r *http.Request) { r.Header.Del("Authorization") }), 401)
		previewResult(t, f.preview(cannedPreviewQuery(row.ID), func(r *http.Request) { r.Header.Set(headerCaller, "UNTRUSTED") }), 401)
		require.Empty(t, log.take())
	})
	t.Run("TEN_faults", func(t *testing.T) {
		for fault, status := range map[int32]int{1: 503, 2: 404, 3: 404, 4: 503, 5: 404} {
			f.fault.Store(fault)
			previewResult(t, f.preview(cannedPreviewQuery(row.ID), nil), status)
			require.Empty(t, log.take())
		}
		f.fault.Store(0)
	})
	t.Run("missing_TEN", func(t *testing.T) {
		dep := f.runtime.Config.Dependencies["TEN"]
		delete(f.runtime.Config.Dependencies, "TEN")
		previewResult(t, f.preview(cannedPreviewQuery(row.ID), nil), 503)
		f.runtime.Config.Dependencies["TEN"] = dep
		require.Empty(t, log.take())
	})
	t.Run("direct_types_and_cancellation", func(t *testing.T) {
		q := cannedPreviewQuery(row.ID)
		q.Filter["id"] = float64(row.ID)
		_, err := f.runtime.Contracts.queries[cannedResponsePreview](context.Background(), q)
		require.Error(t, err)
		q = cannedPreviewQuery(row.ID)
		q.Filter["variables"] = map[string]any{"name": string([]byte{255})}
		_, err = f.runtime.Contracts.queries[cannedResponsePreview](context.Background(), q)
		require.Error(t, err)
		// The existing JSON codec repairs invalid UTF-8 before string validation.
		require.Equal(t, "�", previewResult(t, f.preview(q, nil), 200).ResolvedContent)
		assertPreviewSQL(t, log.take(), 1)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = f.runtime.Contracts.queries[cannedResponsePreview](ctx, cannedPreviewQuery(row.ID))
		require.ErrorIs(t, err, context.Canceled)
		require.Empty(t, log.take())
	})
	t.Run("no_receipts_or_events", func(t *testing.T) { cannedCounts(t, f, 1, 0, 0) })
}
func TestOPSCannedResponsePreviewValidation(t *testing.T) {
	cannedPreviewValidation(t, newModuleTestDB)
}
func TestPostgresOPSCannedResponsePreviewValidation(t *testing.T) {
	cannedPreviewValidation(t, postgresModuleDB)
}

func cannedPreviewWire(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	f := newCannedReadFixture(t, open)
	row := domain.CannedResponse{AccountID: 91, ShortCode: "wire", Content: "literal"}
	require.NoError(t, f.runtime.DB.Create(&row).Error)
	original, err := json.Marshal(cannedPreviewQuery(row.ID))
	require.NoError(t, err)
	cases := map[string]func(string) string{
		"unknown_envelope": func(s string) string { return strings.TrimSuffix(s, "}") + `,"unknown":true}` },
		"unknown_actor": func(s string) string {
			return strings.Replace(s, `"auth_source":"module"`, `"auth_source":"module","unexpected":1`, 1)
		},
		"trailing_json":    func(s string) string { return s + ` {}` },
		"trailing_garbage": func(s string) string { return s + ` x` },
		"leading_zero_id": func(s string) string {
			return strings.Replace(s, `"id":`+strconv.FormatUint(uint64(row.ID), 10), `"id":01`, 1)
		},
		"fractional_account": func(s string) string { return strings.Replace(s, `"account_id":91`, `"account_id":91.0`, 1) },
		"exponent_account":   func(s string) string { return strings.Replace(s, `"account_id":91`, `"account_id":91e0`, 1) },
	}
	cases["valid_control"] = func(s string) string { return s }
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/contracts/queries/"+cannedResponsePreview, bytes.NewBufferString(change(string(original))))
			req.Header.Set("Authorization", "Bearer "+f.runtime.Config.AuthToken)
			req.Header.Set(headerCaller, "API")
			req.Header.Set(headerContractVersion, f.runtime.Config.ContractVersion)
			out := httptest.NewRecorder()
			f.runtime.Handler().ServeHTTP(out, req)
			if name == "valid_control" {
				require.Equal(t, "literal", previewResult(t, out, 200).ResolvedContent)
			} else {
				previewResult(t, out, 400)
				require.Contains(t, out.Body.String(), "query envelope is invalid")
			}
		})
	}
	t.Run("configured_request_limit", func(t *testing.T) {
		q := cannedPreviewQuery(row.ID)
		q.Filter["variables"] = map[string]any{"name": strings.Repeat("x", int(f.runtime.Config.MaxRequestBytes))}
		calls := f.calls.Load()
		previewResult(t, f.preview(q, nil), 400)
		require.Equal(t, calls, f.calls.Load())
	})
	cannedCounts(t, f, 1, 0, 0)
}
func TestOPSCannedResponsePreviewWire(t *testing.T)         { cannedPreviewWire(t, newModuleTestDB) }
func TestPostgresOPSCannedResponsePreviewWire(t *testing.T) { cannedPreviewWire(t, postgresModuleDB) }

func cannedPreviewBounds(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	f := newCannedReadFixture(t, open)
	f.runtime.Config.MaxRequestBytes = 8 << 20
	row := domain.CannedResponse{AccountID: 91, ShortCode: "bounded", Content: "{{x}}"}
	require.NoError(t, f.runtime.DB.Create(&row).Error)
	t.Run("variable_input_budget", func(t *testing.T) {
		q := cannedPreviewQuery(row.ID)
		q.Filter["variables"] = map[string]any{"x": strings.Repeat("a", repository.CannedResponseReadBytes)}
		calls := f.calls.Load()
		previewResult(t, f.preview(q, nil), 400)
		require.Equal(t, calls, f.calls.Load())
	})
	t.Run("escaped_response_budget", func(t *testing.T) {
		q := cannedPreviewQuery(row.ID)
		q.Filter["variables"] = map[string]any{"x": strings.Repeat("\x01", 200000)}
		previewResult(t, f.preview(q, nil), 400)
	})
	t.Run("expanded_response_budget", func(t *testing.T) {
		require.NoError(t, f.runtime.DB.Model(&row).Update("content", "{{x}}{{x}}").Error)
		q := cannedPreviewQuery(row.ID)
		q.Filter["variables"] = map[string]any{"x": strings.Repeat("a", 600000)}
		previewResult(t, f.preview(q, nil), 400)
	})
	t.Run("oversize_source", func(t *testing.T) {
		require.NoError(t, f.runtime.DB.Model(&row).Update("content", strings.Repeat("a", repository.CannedResponseReadBytes+1)).Error)
		previewResult(t, f.preview(cannedPreviewQuery(row.ID), nil), 503)
	})
	t.Run("negative_stored_version", func(t *testing.T) {
		require.NoError(t, f.runtime.DB.Model(&row).Updates(map[string]any{"content": "literal", "object_version": -1}).Error)
		previewResult(t, f.preview(cannedPreviewQuery(row.ID), nil), 503)
	})
	t.Run("deleted_source", func(t *testing.T) {
		require.NoError(t, f.runtime.DB.Delete(&row).Error)
		previewResult(t, f.preview(cannedPreviewQuery(row.ID), nil), 404)
	})
	t.Run("failed_store", func(t *testing.T) {
		require.NoError(t, f.runtime.DB.Migrator().DropTable(&domain.CannedResponse{}))
		previewResult(t, f.preview(cannedPreviewQuery(row.ID), nil), 503)
	})
}
func TestOPSCannedResponsePreviewBounds(t *testing.T) { cannedPreviewBounds(t, newModuleTestDB) }
func TestPostgresOPSCannedResponsePreviewBounds(t *testing.T) {
	cannedPreviewBounds(t, postgresModuleDB)
}

func cannedPreviewConcurrency(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	f := newCannedReadFixture(t, open)
	c := cannedCommand("preview-source", "create")
	c.Input["content"] = "1 {{name}}"
	var row cannedVersionView
	require.NoError(t, json.Unmarshal(cannedWrite(t, f, c, 200), &row))
	q := cannedPreviewQuery(row.ID)
	q.Filter["variables"] = map[string]any{"name": "value"}
	statuses := cannedConcurrent(t, f, []func(*cannedReadFixture) int{
		func(child *cannedReadFixture) int {
			for version := 1; version <= 12; version++ {
				c := cannedCommand(fmt.Sprint("preview-update-", version), "update")
				cannedTarget(&c, row.ID, uint64(version))
				c.Input["content"] = fmt.Sprint(version+1) + " {{name}}"
				if out := child.write(c); out.Code != 200 {
					return out.Code
				}
			}
			return 200
		},
		func(child *cannedReadFixture) int {
			for i := 0; i < 32; i++ {
				out := child.preview(q, nil)
				if out.Code != 200 {
					return out.Code
				}
				var body struct{ Data cannedPreviewView }
				if json.Unmarshal(out.Body.Bytes(), &body) != nil {
					return 598
				}
				if body.Data.ID != row.ID || body.Data.AccountID != 91 || body.Data.ObjectVersion < 1 || body.Data.ObjectVersion > 13 || body.Data.ResolvedContent != fmt.Sprint(body.Data.ObjectVersion)+" value" {
					return 599
				}
			}
			return 200
		},
	})
	require.Equal(t, []int{200, 200}, statuses)
	view := previewResult(t, f.preview(q, nil), 200)
	require.Equal(t, int64(13), view.ObjectVersion)
	require.Equal(t, "13 value", view.ResolvedContent)
	cannedCounts(t, f, 1, 13, 13)
}
func TestOPSCannedResponsePreviewConcurrency(t *testing.T) {
	cannedPreviewConcurrency(t, newModuleTestDB)
}
func TestPostgresOPSCannedResponsePreviewConcurrency(t *testing.T) {
	cannedPreviewConcurrency(t, postgresModuleDB)
}

func cannedPreviewCancellation(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	f := newCannedReadFixture(t, open)
	row := domain.CannedResponse{AccountID: 91, ShortCode: "cancel", Content: "literal"}
	require.NoError(t, f.runtime.DB.Create(&row).Error)
	entered := make(chan struct{})
	exited := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Drain the POST body so net/http can observe a peer disconnect.
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Errorf("TEN request body: %v", err)
			return
		}
		close(entered)
		select {
		case <-r.Context().Done():
			close(exited)
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	f.runtime.Config.Dependencies["TEN"] = DependencyConfig{URL: server.URL, Token: "test"}
	log := &cannedPreviewSQLLog{Interface: logger.Default.LogMode(logger.Silent)}
	f.runtime.DB = f.runtime.DB.Session(&gorm.Session{Logger: log})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	raw, err := json.Marshal(cannedPreviewQuery(row.ID))
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, "/contracts/queries/"+cannedResponsePreview, bytes.NewReader(raw)).WithContext(ctx)
	req.Header.Set("Authorization", "Bearer "+f.runtime.Config.AuthToken)
	req.Header.Set(headerCaller, "API")
	req.Header.Set(headerContractVersion, f.runtime.Config.ContractVersion)
	out := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); f.runtime.Handler().ServeHTTP(out, req) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("TEN query did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("canceled preview did not return")
	}
	// Cancellation may win in the common transport or return through TEN first.
	require.Contains(t, []int{503, 504}, out.Code, out.Body.String())
	require.NotContains(t, out.Body.String(), "resolved_content")
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("TEN request did not cancel")
	}
	require.Empty(t, log.take())
	cannedCounts(t, f, 1, 0, 0)
}
func TestOPSCannedResponsePreviewCancellation(t *testing.T) {
	cannedPreviewCancellation(t, newModuleTestDB)
}
func TestPostgresOPSCannedResponsePreviewCancellation(t *testing.T) {
	cannedPreviewCancellation(t, postgresModuleDB)
}

func TestPostgresOPSCannedResponsePreviewReadOnlyTransaction(t *testing.T) {
	f := newCannedReadFixture(t, postgresModuleDB)
	row := domain.CannedResponse{AccountID: 91, ShortCode: "readonly", Content: "hello {{name}}"}
	require.NoError(t, f.runtime.DB.Create(&row).Error)
	original := f.runtime.DB
	require.NoError(t, original.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, tx.Exec("SET TRANSACTION READ ONLY").Error)
		f.runtime.DB = tx
		defer func() { f.runtime.DB = original }()
		q := cannedPreviewQuery(row.ID)
		q.Filter["variables"] = map[string]any{"name": "world"}
		require.Equal(t, "hello world", previewResult(t, f.preview(q, nil), 200).ResolvedContent)
		return nil
	}))
	cannedCounts(t, f, 1, 0, 0)
}
