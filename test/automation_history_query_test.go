package test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// The same bounded query contract runs through the ordinary authenticated API
// and directly against a disposable, native PostgreSQL AUT store. No runner,
// external provider, background worker, or deployment/default change is needed.
type automationHistoryQueryResult struct {
	items  []domain.AutomationRuleExecution
	cursor string
	code   int
}

type automationHistoryQueryFixture struct {
	db            *gorm.DB
	accounts      [2]uint
	rules         [2][2]uint
	conversations [2][2]uint
	query         func(*testing.T, uint, url.Values) automationHistoryQueryResult
}

func TestAutomationHistoryQueryAPI(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	h := &automationHistoryQueryFixture{db: f.db}
	tokens := map[uint]string{}
	for i, name := range []string{"history-query-a", "history-query-b"} {
		a := f.tenant(name)
		h.accounts[i], tokens[a.id] = a.id, a.token
		for j := 0; j < 2; j++ {
			h.rules[i][j] = f.rule(a, "conversation_created", []any{}, []any{durableAutomationAction("change_priority", "high")}).ID
			h.conversations[i][j] = f.conversation(a).ID
		}
	}
	h.query = func(t *testing.T, accountID uint, q url.Values) automationHistoryQueryResult {
		t.Helper()
		path := fmt.Sprintf("/api/v1/accounts/%d/automation_rules/executions", accountID)
		if encoded := q.Encode(); encoded != "" {
			path += "?" + encoded
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+tokens[accountID])
		resp := httptest.NewRecorder()
		f.router.ServeHTTP(resp, req)
		result := automationHistoryQueryResult{code: resp.Code, cursor: resp.Header().Get("X-Next-Cursor")}
		if result.cursor != "" && !strings.Contains(strings.ToLower(resp.Header().Get("Access-Control-Expose-Headers")), "x-next-cursor") {
			t.Fatal("continuation cursor is not exposed to browser clients")
		}
		if resp.Code != http.StatusOK {
			return result
		}
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode history response: %v; body=%s", err, resp.Body.String())
		}
		if data := bytes.TrimSpace(envelope.Data); len(data) == 0 || data[0] != '[' {
			t.Fatalf("history data must retain its bare-array shape: %s", resp.Body.String())
		}
		if err := json.Unmarshal(envelope.Data, &result.items); err != nil {
			t.Fatalf("decode history items: %v; body=%s", err, resp.Body.String())
		}
		return result
	}
	runAutomationHistoryQueryContract(t, h)

	t.Run("invalid HTTP pagination values", func(t *testing.T) {
		for _, value := range []string{"", "0", "-1", "101", "1.5", "abc", "18446744073709551616", "+1", " 1"} {
			t.Run("limit="+value, func(t *testing.T) {
				if got := h.query(t, h.accounts[0], url.Values{"limit": {value}}); got.code != http.StatusBadRequest {
					t.Fatalf("limit %q returned %d, want 400", value, got.code)
				}
			})
		}
	})
}

func TestAutomationHistoryQueryPostgres(t *testing.T) {
	dsn := os.Getenv("AUTOMATION_HISTORY_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AUTOMATION_HISTORY_TEST_POSTGRES_DSN is required for native PostgreSQL history regression")
	}
	base, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	baseSQL, err := base.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = baseSQL.Close() })
	schema := "automation_history_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := base.Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := base.Exec("DROP SCHEMA " + schema + " CASCADE").Error; err != nil {
			t.Error(err)
		}
	})
	// Support both libpq keyword DSNs and PostgreSQL URLs without weakening
	// isolation or inheriting an unrelated search_path from the test service.
	scopedDSN := dsn + " search_path=" + schema
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		scopedDSN = u.String()
	}
	db, err := gorm.Open(postgres.Open(scopedDSN), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.AutoMigrateModule(db, "AUT"); err != nil {
		t.Fatal(err)
	}
	svc := service.NewAutomationService(db, nil, nil)
	h := &automationHistoryQueryFixture{db: db, accounts: [2]uint{301, 302}, rules: [2][2]uint{{501, 502}, {503, 504}}, conversations: [2][2]uint{{701, 702}, {703, 704}}}
	h.query = func(t *testing.T, accountID uint, q url.Values) automationHistoryQueryResult {
		t.Helper()
		limit := 0
		if q.Get("limit") != "" {
			var err error
			limit, err = strconv.Atoi(q.Get("limit"))
			if err != nil {
				t.Fatal(err)
			}
		}
		page, err := svc.ListExecutionsPage(accountID, q.Get("rule_id"), q.Get("conversation_id"), q.Get("status"), service.AutomationExecutionPageOptions{Limit: limit, Order: q.Get("order"), Cursor: q.Get("cursor")})
		if err != nil {
			if !errors.Is(err, service.ErrAutomationHistoryQuery) {
				t.Fatalf("history query failed internally: %v", err)
			}
			return automationHistoryQueryResult{code: http.StatusBadRequest}
		}
		return automationHistoryQueryResult{code: http.StatusOK, items: page.Items, cursor: page.NextCursor}
	}
	runAutomationHistoryQueryContract(t, h)
	t.Run("legacy service defaults retain bounded deterministic order", func(t *testing.T) {
		expected := h.query(t, h.accounts[0], nil)
		items, err := svc.ListExecutions(h.accounts[0], "", "", "")
		if err != nil {
			t.Fatal(err)
		}
		assertAutomationHistoryIDs(t, items, automationHistoryIDs(expected.items))
	})
	t.Run("service bounds reject invalid limits", func(t *testing.T) {
		for _, limit := range []int{-1, 101} {
			if _, err := svc.ListExecutionsPage(h.accounts[0], "", "", "", service.AutomationExecutionPageOptions{Limit: limit}); err == nil {
				t.Fatalf("limit %d unexpectedly accepted", limit)
			}
		}
	})
}

func runAutomationHistoryQueryContract(t *testing.T, h *automationHistoryQueryFixture) {
	t.Helper()
	stamp := time.Date(2026, 10, 6, 12, 0, 0, 123456000, time.UTC)
	clear := func(t *testing.T) {
		t.Helper()
		if err := h.db.Where("id > ?", 0).Delete(&domain.AutomationRuleExecution{}).Error; err != nil {
			t.Fatal(err)
		}
	}
	seed := func(t *testing.T, tenant, rule, conversation int, status string, durable bool, at time.Time) domain.AutomationRuleExecution {
		t.Helper()
		row := domain.AutomationRuleExecution{AccountID: h.accounts[tenant], RuleID: h.rules[tenant][rule], ConversationID: h.conversations[tenant][conversation], EventName: "conversation_created", Status: status, ActionResults: "[]", TriggeredAt: at}
		if durable {
			key := uuid.NewString()
			row.ExecutionKey, row.RuleVersion, row.EventID, row.TargetVersion = &key, 1, "event-"+key, 1
		}
		if err := h.db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
		return row
	}
	query := func(t *testing.T, tenant int, q url.Values) automationHistoryQueryResult {
		t.Helper()
		got := h.query(t, h.accounts[tenant], q)
		if got.code != http.StatusOK {
			t.Fatalf("query %v returned %d, want 200", q, got.code)
		}
		return got
	}

	t.Run("mixed legacy durable aliases and exact unknown status", func(t *testing.T) {
		clear(t)
		for _, pair := range [][2]string{{"success", "succeeded"}, {"partial_failed", "partially_succeeded"}, {"depth_exceeded", "loop_blocked"}} {
			legacy := seed(t, 0, 0, 0, pair[0], false, stamp)
			durable := seed(t, 0, 0, 0, pair[1], true, stamp)
			seed(t, 1, 0, 0, pair[0], false, stamp.Add(time.Minute))
			seed(t, 1, 0, 0, pair[1], true, stamp.Add(time.Minute))
			for _, status := range pair {
				got := query(t, 0, url.Values{"status": {status}})
				assertAutomationHistoryIDs(t, got.items, []uint{durable.ID, legacy.ID})
				if got.items[0].Status != pair[1] || got.items[1].Status != pair[0] {
					t.Fatalf("filter rewrote stored statuses: %+v", got.items)
				}
			}
			first := query(t, 0, url.Values{"status": {pair[0]}, "limit": {"1"}})
			if first.cursor == "" {
				t.Fatal("alias-filtered page omitted continuation")
			}
			second := query(t, 0, url.Values{"status": {pair[1]}, "limit": {"1"}, "cursor": {first.cursor}})
			assertAutomationHistoryIDs(t, second.items, []uint{legacy.ID})
		}
		for _, status := range []string{"running", "waiting", "failed", "cancelled", "custom_unknown", "successfully", "success' OR '1'='1"} {
			row := seed(t, 0, 0, 0, status, true, stamp)
			seed(t, 1, 0, 0, status, true, stamp.Add(time.Minute))
			got := query(t, 0, url.Values{"status": {status}})
			assertAutomationHistoryIDs(t, got.items, []uint{row.ID})
		}
		got := query(t, 0, url.Values{"status": {"never_recorded"}})
		if len(got.items) != 0 || got.cursor != "" {
			t.Fatalf("empty status result: %+v", got)
		}
	})

	t.Run("rule conversation and account filters intersect", func(t *testing.T) {
		clear(t)
		a := seed(t, 0, 0, 0, "success", false, stamp)
		b := seed(t, 0, 0, 1, "succeeded", true, stamp)
		c := seed(t, 0, 1, 0, "failed", true, stamp)
		d := seed(t, 1, 0, 0, "succeeded", true, stamp.Add(time.Minute))
		cases := []struct {
			query url.Values
			want  []uint
		}{
			{nil, []uint{c.ID, b.ID, a.ID}},
			{url.Values{"rule_id": {fmt.Sprint(a.RuleID)}}, []uint{b.ID, a.ID}},
			{url.Values{"conversation_id": {fmt.Sprint(a.ConversationID)}}, []uint{c.ID, a.ID}},
			{url.Values{"rule_id": {fmt.Sprint(a.RuleID)}, "conversation_id": {fmt.Sprint(a.ConversationID)}, "status": {"succeeded"}}, []uint{a.ID}},
			{url.Values{"rule_id": {fmt.Sprint(d.RuleID)}}, []uint{}},
			{url.Values{"conversation_id": {fmt.Sprint(d.ConversationID)}}, []uint{}},
		}
		for _, tc := range cases {
			assertAutomationHistoryIDs(t, query(t, 0, tc.query).items, tc.want)
		}
		assertAutomationHistoryIDs(t, query(t, 1, nil).items, []uint{d.ID})
	})

	t.Run("default cap and stable timestamp ID ordering", func(t *testing.T) {
		clear(t)
		var rows []domain.AutomationRuleExecution
		for i := 0; i < 105; i++ {
			rows = append(rows, seed(t, 0, i%2, i%2, "succeeded", i%2 == 0, stamp))
		}
		// Trigger time is the primary sort key; IDs only break ties.
		older := seed(t, 0, 0, 0, "success", false, stamp.Add(-time.Second))
		newer := seed(t, 0, 0, 0, "succeeded", true, stamp.Add(time.Second))
		var desc []uint
		desc = append(desc, newer.ID)
		for i := len(rows) - 1; i >= 0; i-- {
			desc = append(desc, rows[i].ID)
		}
		desc = append(desc, older.ID)
		got := query(t, 0, nil)
		assertAutomationHistoryIDs(t, got.items, desc[:100])
		if got.cursor == "" {
			t.Fatal("bounded default page omitted its continuation cursor")
		}
		assertAutomationHistoryIDs(t, query(t, 0, url.Values{"limit": {"100"}, "order": {"desc"}}).items, desc[:100])
		var asc []uint
		for i := len(desc) - 1; i >= 0; i-- {
			asc = append(asc, desc[i])
		}
		assertAutomationHistoryIDs(t, query(t, 0, url.Values{"limit": {"3"}, "order": {"asc"}}).items, asc[:3])
	})

	for _, order := range []string{"asc", "desc"} {
		t.Run("pagination excludes later backdated inserts "+order, func(t *testing.T) {
			clear(t)
			var rows []domain.AutomationRuleExecution
			for i := 0; i < 7; i++ {
				rows = append(rows, seed(t, 0, 0, 0, "succeeded", i%2 == 0, stamp.Add(time.Duration(i/3)*time.Second)))
			}
			want := automationHistoryIDs(rows)
			if order == "desc" {
				for i, j := 0, len(want)-1; i < j; i, j = i+1, j-1 {
					want[i], want[j] = want[j], want[i]
				}
			}
			q := url.Values{"limit": {"2"}, "order": {order}, "rule_id": {fmt.Sprint(h.rules[0][0])}, "conversation_id": {fmt.Sprint(h.conversations[0][0])}, "status": {"success"}}
			first := query(t, 0, q)
			if len(first.items) != 2 || first.cursor == "" {
				t.Fatalf("first page is not bounded/continuable: %+v", first)
			}
			// A timestamp-only cursor would let at least one of these later rows
			// enter the next page. The captured high-water ID must exclude all.
			seed(t, 0, 0, 0, "succeeded", true, stamp.Add(-time.Hour))
			seed(t, 0, 0, 0, "success", false, stamp.Add(time.Second))
			seed(t, 0, 0, 0, "succeeded", true, stamp.Add(time.Hour))
			seen := automationHistoryIDs(first.items)
			cursor := first.cursor
			for page := 0; cursor != ""; page++ {
				if page >= 7 {
					t.Fatal("pagination failed to terminate")
				}
				q.Set("cursor", cursor)
				// Either vocabulary names the same filter, including cursor binding.
				q.Set("status", "succeeded")
				next := query(t, 0, q)
				if len(next.items) > 2 {
					t.Fatalf("page exceeded requested limit: %d", len(next.items))
				}
				seen = append(seen, automationHistoryIDs(next.items)...)
				cursor = next.cursor
			}
			if !reflect.DeepEqual(seen, want) {
				t.Fatalf("paged IDs=%v, want original snapshot=%v", seen, want)
			}
			// Repeating a page is deterministic; a fresh traversal includes all inserts.
			q.Set("cursor", first.cursor)
			assertAutomationHistoryIDs(t, query(t, 0, q).items, want[2:4])
			fresh := query(t, 0, url.Values{"limit": {"100"}, "order": {order}})
			if len(fresh.items) != 10 || fresh.cursor != "" {
				t.Fatalf("fresh page did not expose current rows: %+v", fresh)
			}
		})
	}

	t.Run("cursor scope and malformed query rejection", func(t *testing.T) {
		clear(t)
		for i := 0; i < 3; i++ {
			seed(t, 0, 0, 0, "succeeded", true, stamp)
		}
		q := url.Values{"limit": {"1"}, "order": {"desc"}, "rule_id": {fmt.Sprint(h.rules[0][0])}, "conversation_id": {fmt.Sprint(h.conversations[0][0])}, "status": {"success"}}
		first := query(t, 0, q)
		if first.cursor == "" {
			t.Fatal("missing test cursor")
		}
		q.Set("cursor", first.cursor)
		for key, values := range map[string][]string{
			"rule_id":         {fmt.Sprint(h.rules[0][1]), ""},
			"conversation_id": {fmt.Sprint(h.conversations[0][1]), ""},
			"status":          {"failed", ""}, "order": {"asc"},
		} {
			for _, value := range values {
				changed := cloneAutomationHistoryQuery(q)
				changed.Set(key, value)
				if got := h.query(t, h.accounts[0], changed); got.code != http.StatusBadRequest {
					t.Fatalf("cursor accepted changed %s=%q: status=%d", key, value, got.code)
				}
			}
		}
		if got := h.query(t, h.accounts[1], q); got.code != http.StatusBadRequest {
			t.Fatalf("cross-account cursor accepted: %d", got.code)
		}
		for _, key := range []string{"rule_id", "conversation_id"} {
			for _, value := range []string{"-1", "0", "abc", "1.5", "18446744073709551616", "1 OR 1=1", "+1", " 1"} {
				if got := h.query(t, h.accounts[0], url.Values{key: {value}}); got.code != http.StatusBadRequest {
					t.Fatalf("invalid %s=%q returned %d", key, value, got.code)
				}
			}
		}
		for _, value := range []string{"sideways", "desc, id", "desc; DROP TABLE automation_rule_executions"} {
			if got := h.query(t, h.accounts[0], url.Values{"order": {value}}); got.code != http.StatusBadRequest {
				t.Fatalf("invalid order %q returned %d", value, got.code)
			}
		}
		for _, value := range []string{"not-a-cursor", "e30", "W10", strings.Repeat("x", 8192)} {
			if got := h.query(t, h.accounts[0], url.Values{"cursor": {value}}); got.code != http.StatusBadRequest {
				t.Fatalf("malformed cursor returned %d", got.code)
			}
		}
		// A cursor is not an authorization credential, but malformed positions
		// must be rejected before values reach database-specific integer codecs.
		raw, err := base64.RawURLEncoding.DecodeString(first.cursor)
		if err != nil {
			t.Fatal(err)
		}
		var cursorFields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &cursorFields); err != nil {
			t.Fatal(err)
		}
		for _, mutation := range []struct{ key, value string }{
			{"v", "99"}, {"id", "0"}, {"high_water", "0"},
			{"id", "18446744073709551615"}, {"high_water", "18446744073709551615"},
			{"at", `"not-a-time"`}, {"at", `"0001-01-01T00:00:00Z"`},
			{"unknown_field", "true"},
		} {
			fields := make(map[string]json.RawMessage, len(cursorFields)+1)
			for key, value := range cursorFields {
				fields[key] = value
			}
			fields[mutation.key] = json.RawMessage(mutation.value)
			changedRaw, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			changed := cloneAutomationHistoryQuery(q)
			changed.Set("cursor", base64.RawURLEncoding.EncodeToString(changedRaw))
			if got := h.query(t, h.accounts[0], changed); got.code != http.StatusBadRequest {
				t.Fatalf("invalid cursor %s=%s returned %d", mutation.key, mutation.value, got.code)
			}
		}
		trailing := cloneAutomationHistoryQuery(q)
		trailing.Set("cursor", base64.RawURLEncoding.EncodeToString(append(raw, []byte(` {}`)...)))
		if got := h.query(t, h.accounts[0], trailing); got.code != http.StatusBadRequest {
			t.Fatalf("cursor with trailing JSON returned %d", got.code)
		}
		// Exactly-full terminal pages need no empty extra request.
		full := query(t, 0, url.Values{"limit": {"3"}})
		if len(full.items) != 3 || full.cursor != "" {
			t.Fatalf("exactly-full terminal page incorrectly continues: %+v", full)
		}
	})
}

func automationHistoryIDs(items []domain.AutomationRuleExecution) []uint {
	ids := make([]uint, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func assertAutomationHistoryIDs(t *testing.T, items []domain.AutomationRuleExecution, want []uint) {
	t.Helper()
	if got := automationHistoryIDs(items); !reflect.DeepEqual(got, want) {
		t.Fatalf("history IDs=%v, want=%v", got, want)
	}
}

func cloneAutomationHistoryQuery(q url.Values) url.Values {
	copy := make(url.Values, len(q))
	for key, values := range q {
		copy[key] = append([]string(nil), values...)
	}
	return copy
}
