package modruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type cannedReadFixture struct {
	runtime *Runtime
	ten     *gorm.DB
	fault   atomic.Int32
	calls   atomic.Int64
}

func newCannedReadFixture(t *testing.T, open func(*testing.T, string) *gorm.DB) *cannedReadFixture {
	t.Helper()
	f := &cannedReadFixture{ten: open(t, "TEN")}
	require.NoError(t, f.ten.Create(&domain.AccountUser{AccountID: 91, UserID: 7, Role: domain.RoleAgent}).Error)
	require.NoError(t, f.ten.Create(&domain.AccountUser{AccountID: 91, UserID: 8, Role: domain.RoleAdministrator}).Error)
	require.NoError(t, f.ten.Create(&domain.AccountUser{AccountID: 92, UserID: 7, Role: domain.RoleAgent}).Error)
	tenCfg := businessConfig("TEN")
	tenCfg.AllowedCallers["OPS"] = true
	// Membership reads need no IAM call or IAM storage.
	tenCfg.Dependencies["IAM"] = DependencyConfig{URL: "http://127.0.0.1:1", Token: "unused-test-token"}
	tenRuntime := &Runtime{Config: tenCfg, DB: f.ten, Contracts: NewContractRegistry("TEN")}
	require.NoError(t, tenRuntime.RegisterBusinessContracts())
	tenHandler := tenRuntime.Handler()
	ten := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		if r.URL.Path != "/contracts/queries/ten.permissions.check.v1" || r.Header.Get(headerCaller) != "OPS" {
			t.Errorf("unexpected TEN port: %s", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		switch f.fault.Load() {
		case 1:
			w.WriteHeader(503)
			return
		case 2:
			_ = json.NewEncoder(w).Encode(map[string]any{"data": membershipCheckView{AccountID: 92, UserID: 7, Allowed: true}})
			return
		case 3:
			_ = json.NewEncoder(w).Encode(map[string]any{"data": membershipCheckView{AccountID: 91, UserID: 9, Allowed: true}})
			return
		case 4:
			_, _ = w.Write([]byte(`{"data":`))
			return
		case 5:
			_ = json.NewEncoder(w).Encode(map[string]any{"data": membershipCheckView{AccountID: 9007199254740992, UserID: 7, Allowed: true}})
			return
		}
		tenHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(ten.Close)
	cfg := businessConfig("OPS")
	cfg.MaxTimeout = 3 * time.Second
	cfg.MaxRequestBytes = 16384
	cfg.Dependencies["TEN"] = DependencyConfig{URL: ten.URL, Token: tenCfg.AuthToken}
	f.runtime = &Runtime{Config: cfg, DB: open(t, "OPS"), Contracts: NewContractRegistry("OPS")}
	f.runtime.Contracts.UseCommandReceipts(f.runtime.DB)
	require.NoError(t, f.runtime.RegisterBusinessContracts())
	return f
}

func cannedReadQuery(mode string) foundation.QueryEnvelope {
	sort := "id"
	if mode == "list" {
		sort = "-id"
	}
	return foundation.QueryEnvelope{QueryType: cannedResponseGet, AccountID: 91, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, Filter: map[string]any{"mode": mode}, Sort: []string{sort}, Page: "1", Fields: append([]string(nil), cannedResponseFields[:]...), Consistency: "strong", RequestedAt: time.Now().UTC()}
}
func (f *cannedReadFixture) request(q foundation.QueryEnvelope, change func(*http.Request)) *httptest.ResponseRecorder {
	return performContractRequest(f.runtime.Handler(), "/contracts/queries/"+cannedResponseGet, q, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+f.runtime.Config.AuthToken)
		r.Header.Set(headerCaller, "API")
		if change != nil {
			change(r)
		}
	})
}
func (f *cannedReadFixture) query(t *testing.T, q foundation.QueryEnvelope, status int) []byte {
	t.Helper()
	out := f.request(q, nil)
	require.Equal(t, status, out.Code, out.Body.String())
	return out.Body.Bytes()
}
func cannedReadPage(t *testing.T, raw []byte) repository.CannedResponseReadPage {
	t.Helper()
	var out struct {
		Data repository.CannedResponseReadPage
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	return out.Data
}
func cannedReadItem(t *testing.T, raw []byte) repository.StoredCannedResponse {
	t.Helper()
	var out struct {
		Data repository.StoredCannedResponse
	}
	require.NoError(t, json.Unmarshal(raw, &out))
	return out.Data
}
func cannedSeed(t *testing.T, db *gorm.DB) []domain.CannedResponse {
	t.Helper()
	rows := []domain.CannedResponse{
		{AccountID: 91, ShortCode: "earliest", Content: "content says HELLO before shortcode"},
		{AccountID: 91, ShortCode: "HELLO", Content: "exact shortcode later"},
		{AccountID: 91, ShortCode: "HELLO", Content: "legacy duplicate"},
		{AccountID: 91, ShortCode: "こんにちは", Content: "你好 世界 café"},
		{AccountID: 91, ShortCode: "space", Content: "keep  spaces "},
		{AccountID: 92, ShortCode: "HELLO", Content: "foreign-only"},
	}
	require.NoError(t, db.Create(&rows).Error)
	return rows
}
func assertCannedReadOnly(t *testing.T, f *cannedReadFixture, before []domain.CannedResponse) {
	t.Helper()
	var after []domain.CannedResponse
	require.NoError(t, f.runtime.DB.Order("id").Find(&after).Error)
	require.Equal(t, before, after)
	var count int64
	require.NoError(t, f.runtime.DB.Model(&domain.LocalChangeJournal{}).Where("action <> ?", "recovery_complete").Count(&count).Error)
	require.Zero(t, count)
	for _, model := range []any{&domain.User{}, &domain.Account{}, &domain.AccountUser{}, &domain.Message{}, &domain.Conversation{}, &domain.Inbox{}} {
		require.False(t, f.runtime.DB.Migrator().HasTable(model), fmt.Sprintf("foreign OPS table %T", model))
	}
	require.False(t, f.ten.Migrator().HasTable(&domain.CannedResponse{}))
}

func cannedReadSemantics(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	f := newCannedReadFixture(t, open)
	rows := cannedSeed(t, f.runtime.DB)
	var before []domain.CannedResponse
	require.NoError(t, f.runtime.DB.Order("id").Find(&before).Error)
	repo := repository.NewCannedResponseRepository(f.runtime.DB)
	for _, search := range []string{"", "HeLLo", "こんにちは", "世界", "CAFÉ", "café", " ", "%", "_", "h_llo", "%你好%", "does-not-exist", " hello "} {
		q := cannedReadQuery("list")
		q.Filter["search"] = search
		got := cannedReadPage(t, f.query(t, q, 200))
		want, err := repo.List(91, search)
		require.NoError(t, err)
		require.Len(t, got.Items, len(want), "search %q", search)
		for i, row := range want {
			require.Equal(t, row.ID, got.Items[i].ID, "search %q", search)
			require.Equal(t, row.Content, got.Items[i].Content)
		}
		require.Empty(t, got.NextCursor)
	}
	for _, query := range []string{"", " \t\n", "\u2003", " HELLO ", "hello", "こんにちは", "世界", "café", "CAFÉ", "%", "_", "exact shortcode later", "none"} {
		q := cannedReadQuery("match")
		q.Filter["query"] = query
		want, err := repo.FindMatch(91, query)
		require.NoError(t, err)
		if want == nil {
			f.query(t, q, 404)
			continue
		}
		got := cannedReadItem(t, f.query(t, q, 200))
		require.Equal(t, want.ID, got.ID, "query %q", query)
	}
	q := cannedReadQuery("match")
	q.Filter["query"] = " hello "
	require.Equal(t, rows[0].ID, cannedReadItem(t, f.query(t, q, 200)).ID, "content lower ID beats exact shortcode later")
	for _, blank := range []string{"", " \t\n", "\u2003"} {
		q.Filter["query"] = blank
		calls := f.calls.Load()
		require.Equal(t, rows[0].ID, cannedReadItem(t, f.query(t, q, 200)).ID)
		require.Equal(t, calls+1, f.calls.Load())
	}
	q = cannedReadQuery("detail")
	q.Filter["id"] = rows[1].ID
	for _, actor := range []string{"7", "8"} {
		q.Actor.ID = actor
		got := cannedReadItem(t, f.query(t, q, 200))
		require.Equal(t, rows[1].ShortCode, got.ShortCode)
		require.Equal(t, rows[1].Content, got.Content)
	}
	q.Filter["id"] = rows[5].ID
	f.query(t, q, 404)
	q.Actor.ID = "7"
	q.AccountID = 92
	require.Equal(t, rows[5].ID, cannedReadItem(t, f.query(t, q, 200)).ID)
	q = cannedReadQuery("list")
	q.Filter["limit"] = 2
	ids := []uint{}
	for {
		page := cannedReadPage(t, f.query(t, q, 200))
		for _, row := range page.Items {
			ids = append(ids, row.ID)
		}
		if page.NextCursor == "" {
			break
		}
		q.Page = ""
		q.Cursor = page.NextCursor
	}
	require.Equal(t, []uint{rows[4].ID, rows[3].ID, rows[2].ID, rows[1].ID, rows[0].ID}, ids)
	assertCannedReadOnly(t, f, before)
	require.Len(t, f.runtime.Contracts.queries, 2)
	require.Contains(t, f.runtime.Contracts.queries, cannedResponseGet)
	require.Contains(t, f.runtime.Contracts.queries, cannedResponsePreview)
	require.Len(t, f.runtime.Contracts.commands, 1)
	require.Contains(t, f.runtime.Contracts.commands, cannedResponseManage)
	require.NotNil(t, f.runtime.Contracts.events["cus.contact.merged.v1"])
}
func TestOPSCannedResponseReadSemantics(t *testing.T) { cannedReadSemantics(t, newModuleTestDB) }
func TestPostgresOPSCannedResponseReadSemantics(t *testing.T) {
	cannedReadSemantics(t, postgresModuleDB)
}

func cannedReadAuthorization(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	f := newCannedReadFixture(t, open)
	rows := cannedSeed(t, f.runtime.DB)
	q := cannedReadQuery("detail")
	q.Filter["id"] = rows[0].ID
	f.query(t, q, 200)
	// TEN supports decimal string IDs; adjacent identities above 2^53 must not round.
	large := uint(9007199254740993)
	require.NoError(t, f.ten.Create(&domain.AccountUser{AccountID: 91, UserID: large, Role: domain.RoleAgent}).Error)
	require.NoError(t, f.ten.Create(&domain.AccountUser{AccountID: 91, UserID: large - 1, Role: domain.RoleAgent}).Error)
	exact := q
	exact.Actor.ID = strconv.FormatUint(uint64(large), 10)
	f.query(t, exact, 200)
	require.NoError(t, f.ten.Where("account_id = ? AND user_id = ?", 91, large).Delete(&domain.AccountUser{}).Error)
	f.query(t, exact, 404)
	largeAccount := uint(9007199254740993)
	require.NoError(t, f.ten.Create(&domain.AccountUser{AccountID: largeAccount, UserID: 7, Role: domain.RoleAgent}).Error)
	accountRow := domain.CannedResponse{AccountID: largeAccount, ShortCode: "exact-account", Content: "large account content"}
	neighborRow := domain.CannedResponse{AccountID: largeAccount - 1, ShortCode: "neighbor-account", Content: "must not leak"}
	require.NoError(t, f.runtime.DB.Create(&accountRow).Error)
	require.NoError(t, f.runtime.DB.Create(&neighborRow).Error)
	accountQuery := cannedReadQuery("detail")
	accountQuery.AccountID = largeAccount
	accountQuery.Filter["id"] = accountRow.ID
	require.Equal(t, largeAccount, cannedReadItem(t, f.query(t, accountQuery, 200)).AccountID)
	accountQuery.Filter["id"] = neighborRow.ID
	f.query(t, accountQuery, 404)
	accountQuery.Filter["id"] = accountRow.ID
	f.fault.Store(5)
	f.query(t, accountQuery, 404)
	f.fault.Store(0)
	for _, actor := range []foundation.ContractActor{{Type: "System", ID: "7", AuthSource: "module"}, {Type: "User", ID: "07", AuthSource: "module"}, {Type: "User", ID: "7", AuthSource: "jwt"}, {Type: "User", ID: "+7", AuthSource: "module"}, {Type: "User", ID: "0", AuthSource: "module"}, {Type: "User", ID: "9223372036854775808", AuthSource: "module"}} {
		bad := q
		bad.Actor = actor
		f.query(t, bad, 403)
	}
	for _, change := range []func(*http.Request){func(r *http.Request) { r.Header.Set("Authorization", "Bearer wrong") }, func(r *http.Request) { r.Header.Set(headerCaller, "UNKNOWN") }, func(r *http.Request) { r.Header.Del("Authorization") }} {
		require.Equal(t, 401, f.request(q, change).Code)
	}
	require.Equal(t, 400, f.request(q, func(r *http.Request) { r.Header.Set(headerContractVersion, "v2") }).Code)
	for fault, want := range map[int32]int{1: 503, 2: 404, 3: 404, 4: 503} {
		f.fault.Store(fault)
		raw := f.query(t, q, want)
		require.NotContains(t, string(raw), rows[0].Content)
	}
	f.fault.Store(0)
	bad := q
	bad.Actor.ID = "9"
	f.query(t, bad, 404)
	require.NoError(t, f.ten.Where("account_id = ? AND user_id = ?", 91, 7).Delete(&domain.AccountUser{}).Error)
	f.query(t, q, 404)
	q.Actor.ID = "8"
	f.query(t, q, 200)
	cfg := f.runtime.Config
	cfg.Dependencies = map[string]DependencyConfig{}
	absent := &Runtime{Config: cfg, DB: f.runtime.DB, Contracts: NewContractRegistry("OPS")}
	require.NoError(t, absent.RegisterBusinessContracts())
	require.NotNil(t, absent.Contracts.events["cus.contact.merged.v1"])
	_, err := absent.Contracts.queries[cannedResponseGet](context.Background(), func() foundation.QueryEnvelope {
		out := q
		out.Filter = map[string]any{"mode": "detail", "id": json.Number(strconv.Itoa(int(rows[0].ID)))}
		return out
	}())
	var failure *ContractFailure
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "dependency_unavailable", failure.Type)
}
func TestOPSCannedResponseReadAuthorization(t *testing.T) {
	cannedReadAuthorization(t, newModuleTestDB)
}
func TestPostgresOPSCannedResponseReadAuthorization(t *testing.T) {
	cannedReadAuthorization(t, postgresModuleDB)
}

func TestOPSCannedResponseReadStrictShapes(t *testing.T) {
	f := newCannedReadFixture(t, newModuleTestDB)
	cannedSeed(t, f.runtime.DB)
	cases := map[string]func(*foundation.QueryEnvelope){
		"missing_mode":        func(q *foundation.QueryEnvelope) { q.Filter = map[string]any{} },
		"unknown_mode":        func(q *foundation.QueryEnvelope) { q.Filter["mode"] = "get" },
		"unknown_filter":      func(q *foundation.QueryEnvelope) { q.Filter["user_id"] = 7 },
		"list_with_id":        func(q *foundation.QueryEnvelope) { q.Filter["id"] = 1 },
		"list_with_query":     func(q *foundation.QueryEnvelope) { q.Filter["query"] = "hello" },
		"bad_search":          func(q *foundation.QueryEnvelope) { q.Filter["search"] = nil },
		"oversize_search":     func(q *foundation.QueryEnvelope) { q.Filter["search"] = strings.Repeat("x", 4097) },
		"huge_limit":          func(q *foundation.QueryEnvelope) { q.Filter["limit"] = 101 },
		"zero_limit":          func(q *foundation.QueryEnvelope) { q.Filter["limit"] = 0 },
		"decimal_limit":       func(q *foundation.QueryEnvelope) { q.Filter["limit"] = 1.5 },
		"exponent_limit":      func(q *foundation.QueryEnvelope) { q.Filter["limit"] = json.Number("1e1") },
		"string_limit":        func(q *foundation.QueryEnvelope) { q.Filter["limit"] = "1" },
		"negative_limit":      func(q *foundation.QueryEnvelope) { q.Filter["limit"] = -1 },
		"wildcard_fields":     func(q *foundation.QueryEnvelope) { q.Fields = []string{"*"} },
		"extra_fields":        func(q *foundation.QueryEnvelope) { q.Fields = append(q.Fields, "user_id") },
		"reordered_fields":    func(q *foundation.QueryEnvelope) { q.Fields[0], q.Fields[1] = q.Fields[1], q.Fields[0] },
		"bad_sort":            func(q *foundation.QueryEnvelope) { q.Sort = []string{"id"} },
		"extra_sort":          func(q *foundation.QueryEnvelope) { q.Sort = append(q.Sort, "content") },
		"weak":                func(q *foundation.QueryEnvelope) { q.Consistency = "eventual" },
		"page2":               func(q *foundation.QueryEnvelope) { q.Page = "2" },
		"cursor_and_page":     func(q *foundation.QueryEnvelope) { q.Cursor = "2" },
		"zero_cursor":         func(q *foundation.QueryEnvelope) { q.Cursor = "0"; q.Page = "" },
		"noncanonical_cursor": func(q *foundation.QueryEnvelope) { q.Cursor = "02"; q.Page = "" },
		"overflow_cursor":     func(q *foundation.QueryEnvelope) { q.Cursor = "9223372036854775808"; q.Page = "" },
		"detail_missing_id":   func(q *foundation.QueryEnvelope) { *q = cannedReadQuery("detail") },
		"detail_string_id":    func(q *foundation.QueryEnvelope) { *q = cannedReadQuery("detail"); q.Filter["id"] = "1" },
		"detail_float_id":     func(q *foundation.QueryEnvelope) { *q = cannedReadQuery("detail"); q.Filter["id"] = json.Number("1.0") },
		"detail_overflow_id": func(q *foundation.QueryEnvelope) {
			*q = cannedReadQuery("detail")
			q.Filter["id"] = json.Number("9223372036854775808")
		},
		"detail_with_search": func(q *foundation.QueryEnvelope) {
			*q = cannedReadQuery("detail")
			q.Filter["id"] = 1
			q.Filter["search"] = "x"
		},
		"match_missing_query": func(q *foundation.QueryEnvelope) { *q = cannedReadQuery("match") },
		"match_with_id": func(q *foundation.QueryEnvelope) {
			*q = cannedReadQuery("match")
			q.Filter["query"] = "hello"
			q.Filter["id"] = 1
		},
		"match_with_cursor": func(q *foundation.QueryEnvelope) {
			*q = cannedReadQuery("match")
			q.Filter["query"] = "hello"
			q.Cursor = "2"
			q.Page = ""
		},
		"match_bad_query": func(q *foundation.QueryEnvelope) { *q = cannedReadQuery("match"); q.Filter["query"] = 7 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) { q := cannedReadQuery("list"); change(&q); f.query(t, q, 400) })
	}
	// Transport uses precise numbers: adjacent integers above float64 exact range stay distinct.
	row := domain.CannedResponse{ID: 9007199254740993, AccountID: 91, ShortCode: "large-id", Content: "precise"}
	require.NoError(t, f.runtime.DB.Create(&row).Error)
	q := cannedReadQuery("detail")
	q.Filter["id"] = json.Number("9007199254740993")
	require.Equal(t, row.ID, cannedReadItem(t, f.query(t, q, 200)).ID)
	q.Filter["id"] = json.Number("9007199254740992")
	f.query(t, q, 404)
	// Direct invocation must also reject invalid UTF-8, which JSON transport repairs.
	q = cannedReadQuery("list")
	q.Filter["search"] = string([]byte{0xff})
	_, err := f.runtime.Contracts.queries[cannedResponseGet](context.Background(), q)
	require.Error(t, err)
}

func cannedReadBounds(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	f := newCannedReadFixture(t, open)
	rows := make([]domain.CannedResponse, 105)
	for i := range rows {
		rows[i] = domain.CannedResponse{AccountID: 91, ShortCode: fmt.Sprintf("code-%d", i), Content: "bounded"}
	}
	require.NoError(t, f.runtime.DB.Create(&rows).Error)
	q := cannedReadQuery("list")
	page := cannedReadPage(t, f.query(t, q, 200))
	require.Len(t, page.Items, 50)
	require.NotEmpty(t, page.NextCursor)
	q.Filter["limit"] = 100
	page = cannedReadPage(t, f.query(t, q, 200))
	require.Len(t, page.Items, 100)
	require.NotEmpty(t, page.NextCursor)
	// Oversized source text is guarded in SQL; no partial record is exposed.
	require.NoError(t, f.runtime.DB.Model(&domain.CannedResponse{}).Where("id = ?", rows[104].ID).Update("content", strings.Repeat("x", repository.CannedResponseReadBytes+1)).Error)
	f.query(t, q, 503)
	detail := cannedReadQuery("detail")
	detail.Filter["id"] = rows[104].ID
	f.query(t, detail, 503)
	// Two individually bounded records can still exceed the encoded page budget.
	require.NoError(t, f.runtime.DB.Model(&domain.CannedResponse{}).Where("id IN ?", []uint{rows[103].ID, rows[104].ID}).Update("content", strings.Repeat("x", 600000)).Error)
	q.Filter["limit"] = 2
	f.query(t, q, 503)
	q.Filter["limit"] = 1
	page = cannedReadPage(t, f.query(t, q, 200))
	require.Len(t, page.Items, 1)
	require.NotEmpty(t, page.NextCursor)
	// JSON escaping is counted, not only stored text bytes.
	require.NoError(t, f.runtime.DB.Model(&domain.CannedResponse{}).Where("id = ?", rows[104].ID).Update("content", strings.Repeat("\x01", 200000)).Error)
	f.query(t, detail, 503)
	require.NoError(t, f.runtime.DB.Migrator().DropTable(&domain.CannedResponse{}))
	f.query(t, q, 503)
}
func TestOPSCannedResponseReadBounds(t *testing.T)         { cannedReadBounds(t, newModuleTestDB) }
func TestPostgresOPSCannedResponseReadBounds(t *testing.T) { cannedReadBounds(t, postgresModuleDB) }

func TestOPSCannedResponseReadCancellation(t *testing.T) {
	f := newCannedReadFixture(t, newModuleTestDB)
	cannedSeed(t, f.runtime.DB)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := f.runtime.Contracts.queries[cannedResponseGet](ctx, cannedReadQuery("list"))
	require.Error(t, err)
	_, err = repository.NewCannedResponseRepository(f.runtime.DB).ReadBounded(ctx, 91, "list", 0, "", 0, 50)
	require.Error(t, err)
}

func cannedReadSnapshot(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	f := newCannedReadFixture(t, open)
	rows := cannedSeed(t, f.runtime.DB)
	require.NoError(t, f.runtime.DB.Order("id").Find(&rows).Error) // Compare native persisted precision.
	campaign := domain.Campaign{AccountID: 91, InboxID: 1, Title: "unrelated campaign", Message: "retained"}
	require.NoError(t, f.runtime.DB.Create(&campaign).Error)
	path := filepath.Join(t.TempDir(), "ops.snapshot.json")
	require.NoError(t, CreateSnapshot(f.runtime.DB, "OPS", "canned-read", path))
	restored := open(t, "OPS")
	require.NoError(t, RestoreSnapshot(context.Background(), restored, "OPS", "canned-read", path, nil))
	f.runtime.DB = restored
	var actual []domain.CannedResponse
	require.NoError(t, restored.Order("id").Find(&actual).Error)
	for i, row := range rows {
		require.Equal(t, row.ID, actual[i].ID)
		require.Equal(t, row.ShortCode, actual[i].ShortCode)
		require.Equal(t, row.Content, actual[i].Content)
		require.True(t, row.CreatedAt.Equal(actual[i].CreatedAt))
		require.True(t, row.UpdatedAt.Equal(actual[i].UpdatedAt))
	}
	var afterCampaign domain.Campaign
	require.NoError(t, restored.First(&afterCampaign, campaign.ID).Error)
	require.Equal(t, campaign.Message, afterCampaign.Message)
	q := cannedReadQuery("match")
	q.Filter["query"] = "hello"
	require.Equal(t, rows[0].ID, cannedReadItem(t, f.query(t, q, 200)).ID)
	assertCannedReadOnly(t, f, actual)
	// The restored read projection has exactly six fields, with no derived identity/version.
	q = cannedReadQuery("detail")
	q.Filter["id"] = rows[0].ID
	var raw struct{ Data map[string]any }
	require.NoError(t, json.Unmarshal(f.query(t, q, 200), &raw))
	require.Len(t, raw.Data, 6)
	for _, field := range cannedResponseFields {
		require.Contains(t, raw.Data, field)
	}
}
func TestOPSCannedResponseReadSnapshot(t *testing.T)         { cannedReadSnapshot(t, newModuleTestDB) }
func TestPostgresOPSCannedResponseReadSnapshot(t *testing.T) { cannedReadSnapshot(t, postgresModuleDB) }

func TestPostgresOPSCannedResponseReadOnlyTransaction(t *testing.T) {
	f := newCannedReadFixture(t, postgresModuleDB)
	rows := cannedSeed(t, f.runtime.DB)
	err := f.runtime.DB.Transaction(func(tx *gorm.DB) error {
		require.NoError(t, tx.Exec("SET TRANSACTION READ ONLY").Error)
		for _, mode := range []string{"detail", "list", "match"} {
			page, err := repository.NewCannedResponseRepository(tx).ReadBounded(context.Background(), 91, mode, rows[0].ID, "hello", 0, 50)
			require.NoError(t, err)
			require.NotEmpty(t, page.Items)
		}
		return nil
	})
	require.NoError(t, err)
}
