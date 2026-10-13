package modruntime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
)

type srhFixture struct {
	runtime *Runtime
	allowed atomic.Bool
	outage  atomic.Bool
	calls   atomic.Int64
}

func newSRHFixture(t *testing.T, db *gorm.DB) *srhFixture {
	t.Helper()
	f := &srhFixture{}
	f.allowed.Store(true)
	ten := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		if r.URL.Path != "/contracts/queries/ten.permissions.check.v1" || r.Header.Get(headerCaller) != "SRH" || r.Header.Get("Authorization") != "Bearer ten-secret" {
			t.Errorf("invalid TEN public-port call")
			w.WriteHeader(500)
			return
		}
		var query foundation.QueryEnvelope
		if json.NewDecoder(r.Body).Decode(&query) != nil {
			w.WriteHeader(400)
			return
		}
		if f.outage.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		id, err := uintFilter(query.Filter, "user_id")
		if err != nil {
			w.WriteHeader(400)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": membershipCheckView{AccountID: query.AccountID, UserID: id, Allowed: f.allowed.Load()}})
	}))
	t.Cleanup(ten.Close)
	if db == nil {
		db = newModuleTestDB(t, "SRH")
	}
	cfg := businessConfig("SRH")
	cfg.MaxTimeout = 3 * time.Second
	cfg.Dependencies["TEN"] = DependencyConfig{URL: ten.URL, Token: "ten-secret"}
	f.runtime = &Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("SRH")}
	f.runtime.Contracts.UseCommandReceipts(db)
	if err := f.runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	return f
}
func srhCommand(key, operation string) foundation.CommandEnvelope {
	input := map[string]any{"operation": operation}
	if operation == "create" {
		input["name"] = "Private filter"
		input["filter_type"] = "conversation"
		input["query"] = json.RawMessage(`{"payload":[],"large":900719925474099312345,"decimal":1.234567890123456789}`)
	}
	return foundation.CommandEnvelope{CommandID: key, CommandType: savedFilterManage, AccountID: 91, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "91"}, Input: input, IdempotencyKey: key, CorrelationID: "srh-correlation", CausationID: "srh-test", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 3000, OnTimeout: "query", StatusQuery: savedFilterGet}}
}
func srhQuery() foundation.QueryEnvelope {
	return foundation.QueryEnvelope{QueryType: savedFilterGet, AccountID: 91, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, Filter: map[string]any{}, Sort: []string{"-id"}, Page: "1", Fields: []string{"*"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
}
func (f *srhFixture) request(path string, payload any) *httptest.ResponseRecorder {
	return performContractRequest(f.runtime.Handler(), path, payload, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+f.runtime.Config.AuthToken)
		r.Header.Set(headerCaller, "API")
		if command, ok := payload.(foundation.CommandEnvelope); ok {
			r.Header.Set(headerCorrelationID, command.CorrelationID)
		}
	})
}
func (f *srhFixture) command(t *testing.T, c foundation.CommandEnvelope, status int) string {
	t.Helper()
	response := f.request("/contracts/commands/"+savedFilterManage, c)
	if response.Code != status {
		t.Fatalf("command status=%d want=%d body=%s", response.Code, status, response.Body.String())
	}
	return response.Body.String()
}
func (f *srhFixture) query(t *testing.T, q foundation.QueryEnvelope, status int) string {
	t.Helper()
	response := f.request("/contracts/queries/"+savedFilterGet, q)
	if response.Code != status {
		t.Fatalf("query status=%d want=%d body=%s", response.Code, status, response.Body.String())
	}
	return response.Body.String()
}
func srhDecodeView(t *testing.T, body string) customFilterView {
	t.Helper()
	var envelope struct{ Data customFilterView }
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}
func srhCounts(t *testing.T, db *gorm.DB) (int64, int64, int64, int64) {
	t.Helper()
	var rows, changes, events, receipts int64
	for _, item := range []struct {
		model any
		where string
		count *int64
	}{{&domain.CustomFilter{}, "1=1", &rows}, {&domain.LocalChangeJournal{}, "action IN ('create','update','delete')", &changes}, {&domain.LocalChangeJournal{}, "action='event_outbox'", &events}, {&domain.LocalChangeJournal{}, "action='command_receipt'", &receipts}} {
		if err := db.Model(item.model).Where(item.where).Count(item.count).Error; err != nil {
			t.Fatal(err)
		}
	}
	return rows, changes, events, receipts
}

func TestSRHPrivateSavedFilterCRUD(t *testing.T) {
	f := newSRHFixture(t, nil)
	if body := f.query(t, srhQuery(), 200); !strings.Contains(body, `"data":[]`) {
		t.Fatal(body)
	}
	ids := []uint{}
	for _, kind := range []string{"conversation", "contact", "report"} {
		c := srhCommand("create-"+kind, "create")
		c.Input["filter_type"] = kind
		body := f.command(t, c, 200)
		created := srhDecodeView(t, body)
		if created.ID == 0 || created.AccountID != 91 || created.UserID != 7 || created.ObjectVersion != 1 || created.FilterType != kind || !strings.Contains(string(created.Query), "900719925474099312345") {
			t.Fatal(body)
		}
		ids = append(ids, created.ID)
		replay := f.command(t, c, 200)
		if srhDecodeView(t, replay).ID != created.ID || !strings.Contains(replay, "1.234567890123456789") {
			t.Fatal(replay)
		}
		q := srhQuery()
		q.Filter = map[string]any{"id": created.ID}
		if srhDecodeView(t, f.query(t, q, 200)).Name != created.Name {
			t.Fatal("get did not retain name")
		}
		update := srhCommand("update-"+kind, "update")
		update.Target = foundation.ContractTarget{Type: "custom_filter", ID: strconv.Itoa(int(created.ID)), ExpectedVersion: 1}
		update.Input["name"] = "Renamed"
		updated := srhDecodeView(t, f.command(t, update, 200))
		if updated.ObjectVersion != 2 || updated.Name != "Renamed" || !savedFilterJSONEqual(string(updated.Query), string(created.Query)) {
			t.Fatal("partial update lost fields")
		}
		unchanged := updated.UpdatedAt
		noOp := srhCommand("noop-"+kind, "update")
		noOp.Target = update.Target
		noOp.Target.ExpectedVersion = 2
		noOp.Input["query"] = json.RawMessage(`{"decimal":1.2345678901234567890,"large":900719925474099312345.00,"payload":[]}`)
		result := srhDecodeView(t, f.command(t, noOp, 200))
		if result.ObjectVersion != 2 || !result.UpdatedAt.Equal(unchanged) || result.ChangeID != "" {
			t.Fatal("no-op fabricated change")
		}
		update.IdempotencyKey += "-stale"
		update.CommandID += "-stale"
		f.command(t, update, 409)
		q = srhQuery()
		q.Filter = map[string]any{"filter_type": kind}
		body = f.query(t, q, 200)
		var list struct{ Data []customFilterView }
		if json.Unmarshal([]byte(body), &list) != nil || len(list.Data) != 1 || list.Data[0].ID != created.ID {
			t.Fatal(body)
		}
	}
	body := f.query(t, srhQuery(), 200)
	var list struct{ Data []customFilterView }
	if json.Unmarshal([]byte(body), &list) != nil || len(list.Data) != 3 {
		t.Fatal(body)
	}
	for i := range ids {
		if list.Data[i].ID != ids[len(ids)-1-i] {
			t.Fatal("unstable ordering")
		}
	}
	for _, id := range ids {
		d := srhCommand("delete-"+strconv.Itoa(int(id)), "delete")
		d.Target = foundation.ContractTarget{Type: "custom_filter", ID: strconv.Itoa(int(id)), ExpectedVersion: 2}
		body := f.command(t, d, 200)
		if !strings.Contains(body, `"object_version":3`) || !strings.Contains(body, `"deleted":true`) {
			t.Fatal(body)
		}
		f.command(t, d, 200)
		q := srhQuery()
		q.Filter["id"] = id
		f.query(t, q, 404)
	}
	rows, changes, events, receipts := srhCounts(t, f.runtime.DB)
	if rows != 0 || changes != 9 || events != 9 || receipts != 12 {
		t.Fatalf("counts %d %d %d %d", rows, changes, events, receipts)
	}
	var records []domain.LocalChangeJournal
	if err := f.runtime.DB.Where("action != ?", "command_receipt").Find(&records).Error; err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		raw := record.AfterState + record.BeforeState + record.Diff
		if strings.Contains(raw, "Private filter") || strings.Contains(raw, "Renamed") || strings.Contains(raw, "900719925474099312345") {
			t.Fatal("private criteria leaked to change/outbox")
		}
		if record.Action == "event_outbox" {
			var event foundation.EventEnvelope
			if json.Unmarshal([]byte(record.AfterState), &event) != nil || event.Validate() != nil || int(event.AggregateVersion) != record.ObjectVersionAfter || len(event.Payload) != 5 {
				t.Fatal("invalid minimal event")
			}
		}
	}
}

func TestSRHPrivacyActorAndReplayAuthorization(t *testing.T) {
	f := newSRHFixture(t, nil)
	create := srhCommand("private", "create")
	row := srhDecodeView(t, f.command(t, create, 200))
	for _, other := range []struct {
		account uint
		actor   string
	}{{91, "8"}, {92, "7"}} {
		q := srhQuery()
		q.AccountID = other.account
		q.Actor.ID = other.actor
		body := f.query(t, q, 200)
		if !strings.Contains(body, `"data":[]`) {
			t.Fatal(body)
		}
		q.Filter["id"] = row.ID
		f.query(t, q, 404)
		for _, op := range []string{"update", "delete"} {
			c := srhCommand("other-"+op+other.actor+strconv.Itoa(int(other.account)), op)
			c.AccountID = other.account
			c.Actor.ID = other.actor
			c.Target = foundation.ContractTarget{Type: "custom_filter", ID: strconv.Itoa(int(row.ID)), ExpectedVersion: 1}
			if op == "update" {
				c.Input["name"] = "stolen"
			}
			f.command(t, c, 404)
		}
	}
	actorChanged := create
	actorChanged.Actor.ID = "8"
	f.command(t, actorChanged, 409)
	changed := create
	changed.Input = map[string]any{"operation": "create", "name": "different", "filter_type": "contact", "query": map[string]any{}}
	f.command(t, changed, 409)
	for _, actor := range []foundation.ContractActor{{Type: "System", ID: "7", AuthSource: "module"}, {Type: "Contact", ID: "7", AuthSource: "module"}, {Type: "User", ID: "0", AuthSource: "module"}, {Type: "User", ID: "-1", AuthSource: "module"}, {Type: "User", ID: "7.5", AuthSource: "module"}, {Type: "User", ID: "07", AuthSource: "module"}, {Type: "User", ID: "18446744073709551616", AuthSource: "module"}, {Type: "User", ID: "7", AuthSource: "session"}} {
		c := create
		c.Actor = actor
		f.command(t, c, 403)
		q := srhQuery()
		q.Actor = actor
		f.query(t, q, 403)
	}
	f.allowed.Store(false)
	f.command(t, create, 404)
	f.query(t, srhQuery(), 404)
	f.allowed.Store(true)
	f.outage.Store(true)
	f.command(t, create, 503)
	f.query(t, srhQuery(), 503)
	f.outage.Store(false)
	f.command(t, create, 200)
	// Neither a shared legacy row nor an existing private unversioned row is adopted.
	for _, user := range []uint{0, 7} {
		if err := f.runtime.DB.Create(&domain.CustomFilter{AccountID: 91, UserID: user, Name: "legacy", FilterType: "conversation", Query: "{}"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	q := srhQuery()
	body := f.query(t, q, 200)
	var list struct{ Data []customFilterView }
	_ = json.Unmarshal([]byte(body), &list)
	if len(list.Data) != 1 {
		t.Fatal("legacy rows adopted")
	}
	for _, foreign := range []any{&domain.Account{}, &domain.AccountUser{}, &domain.User{}, &domain.AuditLog{}} {
		if f.runtime.DB.Migrator().HasTable(foreign) {
			t.Fatal("foreign owner table in SRH")
		}
	}
	if f.calls.Load() == 0 {
		t.Fatal("TEN port unused")
	}
}

func TestSRHSavedFilterValidationAndTransport(t *testing.T) {
	f := newSRHFixture(t, nil)
	for _, key := range []string{"account_id", "user_id", "id", "object_version", "actor", "created_at"} {
		c := srhCommand("forged-"+key, "create")
		c.Input[key] = 99
		f.command(t, c, 400)
		q := srhQuery()
		q.Filter[key] = 99
		if key != "id" {
			f.query(t, q, 400)
		}
	}
	for i, value := range []any{nil, "{}", []any{}, true, 12} {
		c := srhCommand("bad-query-"+strconv.Itoa(i), "create")
		c.Input["query"] = value
		f.command(t, c, 400)
	}
	for i, value := range []any{nil, 17, "", strings.Repeat("界", 256)} {
		c := srhCommand("bad-name-"+strconv.Itoa(i), "create")
		c.Input["name"] = value
		f.command(t, c, 400)
	}
	for i, value := range []any{nil, "tickets", "", 9} {
		c := srhCommand("bad-type-"+strconv.Itoa(i), "create")
		c.Input["filter_type"] = value
		f.command(t, c, 400)
	}
	for _, field := range []string{"name", "filter_type", "query"} {
		c := srhCommand("missing-"+field, "create")
		delete(c.Input, field)
		f.command(t, c, 400)
	}
	row := srhDecodeView(t, f.command(t, srhCommand("valid", "create"), 200))
	for _, op := range []string{"update", "delete"} {
		c := srhCommand("no-version-"+op, op)
		c.Target = foundation.ContractTarget{Type: "custom_filter", ID: strconv.Itoa(int(row.ID))}
		c.Input["name"] = "change"
		f.command(t, c, 400)
	}
	c := srhCommand("empty-update", "update")
	c.Target = foundation.ContractTarget{Type: "custom_filter", ID: strconv.Itoa(int(row.ID)), ExpectedVersion: 1}
	f.command(t, c, 400)
	for _, edit := range []func(*foundation.QueryEnvelope){func(q *foundation.QueryEnvelope) { q.Page = "2" }, func(q *foundation.QueryEnvelope) { q.Cursor = "next" }, func(q *foundation.QueryEnvelope) { q.Sort = []string{"id"} }, func(q *foundation.QueryEnvelope) { q.Consistency = "eventual" }, func(q *foundation.QueryEnvelope) { q.Fields = []string{"id"} }} {
		q := srhQuery()
		edit(&q)
		f.query(t, q, 400)
	}
	response := performContractRequest(f.runtime.Handler(), "/contracts/commands/"+savedFilterManage, srhCommand("untrusted", "create"), func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer wrong")
		r.Header.Set(headerCaller, "API")
	})
	if response.Code != 401 {
		t.Fatal(response.Body.String())
	}
	response = performContractRequest(f.runtime.Handler(), "/contracts/commands/"+savedFilterManage, srhCommand("untrusted-caller", "create"), func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+f.runtime.Config.AuthToken)
		r.Header.Set(headerCaller, "UNKNOWN")
	})
	if response.Code != 401 {
		t.Fatal(response.Body.String())
	}
}

func TestSRHAtomicJournalOutboxRollback(t *testing.T) {
	for _, failAction := range []string{"create", "event_outbox", "receipt_completion"} {
		t.Run(failAction, func(t *testing.T) {
			f := newSRHFixture(t, nil)
			db := f.runtime.DB
			inject := func(tx *gorm.DB) {
				if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "LocalChangeJournal" {
					if row, ok := tx.Statement.Dest.(*domain.LocalChangeJournal); ok && row.Action == failAction {
						tx.AddError(errors.New("injected local write failure"))
					}
				}
			}
			if failAction == "receipt_completion" {
				if err := db.Callback().Update().Before("gorm:update").Register("srh-fail", func(tx *gorm.DB) {
					if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "LocalChangeJournal" {
						tx.AddError(errors.New("injected receipt failure"))
					}
				}); err != nil {
					t.Fatal(err)
				}
			} else if err := db.Callback().Create().Before("gorm:create").Register("srh-fail", inject); err != nil {
				t.Fatal(err)
			}
			f.command(t, srhCommand("rollback", "create"), 503)
			rows, changes, events, receipts := srhCounts(t, db)
			if rows+changes+events+receipts != 0 {
				t.Fatalf("partial transaction %d %d %d %d", rows, changes, events, receipts)
			}
		})
	}
}

func TestSRHNumericSemanticNoopsAndLegacyReceiptDigest(t *testing.T) {
	for _, pair := range [][2]string{{`{"x":1}`, `{"x":1.0}`}, {`{"x":1e999999999999999999999}`, `{"x":10e999999999999999999998}`}, {`{"x":[-0,900719925474099312345]}`, `{"x":[0.0,900719925474099312345.00]}`}} {
		if !savedFilterJSONEqual(pair[0], pair[1]) {
			t.Fatalf("false no-op %v", pair)
		}
	}
	if savedFilterJSONEqual(`{"x":9007199254740992}`, `{"x":9007199254740993}`) {
		t.Fatal("number rounded")
	}
	c := srhCommand("digest", "create")
	first, _ := commandDigest(c)
	c.Actor.ID = "8"
	second, _ := commandDigest(c)
	if first == second {
		t.Fatal("SRH digest omitted actor")
	}
	c.CommandType = "iam.user.manage.v1"
	first, _ = commandDigest(c)
	c.Actor.ID = "7"
	second, _ = commandDigest(c)
	if first != second {
		t.Fatal("legacy digest behavior changed")
	}
	// Legacy preflight replay remains dependency-independent.
	store := NewCommandReceiptStore(newModuleTestDB(t, "SRH"), "SRH")
	c.CommandType = "legacy.command.manage.v1"
	expected := map[string]any{"ok": true}
	_, err := store.ExecutePrepared(context.Background(), c, nil, func(context.Context) (any, error) { return expected, nil })
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.ExecutePrepared(context.Background(), c, func(ctx context.Context, c foundation.CommandEnvelope) (context.Context, error) {
		return nil, errors.New("offline")
	}, func(context.Context) (any, error) { t.Fatal("replayed handler"); return nil, nil })
	if err != nil || !reflect.DeepEqual(result, expected) {
		t.Fatalf("legacy replay changed: %v %v", result, err)
	}
}

func TestSRHRealTENMembershipAndRevocation(t *testing.T) {
	tenDB := newModuleTestDB(t, "TEN")
	for _, member := range []domain.AccountUser{{AccountID: 91, UserID: 7, Role: domain.RoleAgent}, {AccountID: 91, UserID: 8, Role: domain.RoleAdministrator}, {AccountID: 92, UserID: 7, Role: domain.RoleAgent}} {
		if err := tenDB.Create(&member).Error; err != nil {
			t.Fatal(err)
		}
	}
	cfg := businessConfig("TEN")
	cfg.AllowedCallers["SRH"] = true
	cfg.Dependencies["IAM"] = DependencyConfig{URL: "http://127.0.0.1:1", Token: "unused-identity-port"}
	ten := &Runtime{Config: cfg, DB: tenDB, Contracts: NewContractRegistry("TEN")}
	if err := ten.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(ten.Handler())
	defer server.Close()
	f := newSRHFixture(t, nil)
	f.runtime.Config.Dependencies["TEN"] = DependencyConfig{URL: server.URL, Token: cfg.AuthToken}
	f.runtime.Contracts = NewContractRegistry("SRH")
	f.runtime.Contracts.UseCommandReceipts(f.runtime.DB)
	if err := f.runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	create := srhCommand("real-ten-create", "create")
	row := srhDecodeView(t, f.command(t, create, 200))
	q := srhQuery()
	q.Actor.ID = "8"
	q.Filter["id"] = row.ID
	f.query(t, q, 404)
	// An administrator has membership, but receives no exception for another user's filter.
	update := srhCommand("real-ten-admin", "update")
	update.Actor.ID = "8"
	update.Input["name"] = "admin overwrite"
	update.Target = foundation.ContractTarget{Type: "custom_filter", ID: strconv.Itoa(int(row.ID)), ExpectedVersion: 1}
	f.command(t, update, 404)
	if err := tenDB.Where("account_id = ? AND user_id = ?", 91, 7).Delete(&domain.AccountUser{}).Error; err != nil {
		t.Fatal(err)
	}
	for _, command := range []foundation.CommandEnvelope{create, srhCommand("real-ten-after-revoke", "create")} {
		body := f.command(t, command, 404)
		if strings.Contains(body, "Private filter") {
			t.Fatal("revoked receipt disclosed")
		}
	}
	f.query(t, srhQuery(), 404)
	if err := tenDB.Create(&domain.AccountUser{AccountID: 91, UserID: 7, Role: domain.RoleAgent}).Error; err != nil {
		t.Fatal(err)
	}
	f.command(t, create, 200)
	server.Close()
	f.command(t, create, 503)
	rows, changes, events, receipts := srhCounts(t, f.runtime.DB)
	if rows != 1 || changes != 1 || events != 1 || receipts != 1 {
		t.Fatal("denied requests changed facts")
	}
}

func TestSRHUpdateDeleteAtomicRollback(t *testing.T) {
	for _, operation := range []string{"update", "delete"} {
		for _, failure := range []string{"change", "outbox", "receipt"} {
			t.Run(operation+"/"+failure, func(t *testing.T) {
				f := newSRHFixture(t, nil)
				created := srhDecodeView(t, f.command(t, srhCommand("rollback-existing", "create"), 200))
				db := f.runtime.DB
				c := srhCommand("rollback-"+operation, operation)
				c.Target = foundation.ContractTarget{Type: "custom_filter", ID: strconv.Itoa(int(created.ID)), ExpectedVersion: 1}
				if operation == "update" {
					c.Input["name"] = "should roll back"
				}
				if failure == "receipt" {
					if err := db.Callback().Update().Before("gorm:update").Register("srh-rollback", func(tx *gorm.DB) {
						if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "LocalChangeJournal" {
							tx.AddError(errors.New("receipt completion failed"))
						}
					}); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := db.Callback().Create().Before("gorm:create").Register("srh-rollback", func(tx *gorm.DB) {
						if row, ok := tx.Statement.Dest.(*domain.LocalChangeJournal); ok && ((failure == "change" && row.Action == operation) || (failure == "outbox" && row.Action == "event_outbox")) {
							tx.AddError(errors.New("local capture failed"))
						}
					}); err != nil {
						t.Fatal(err)
					}
				}
				f.command(t, c, 503)
				rows, changes, events, receipts := srhCounts(t, db)
				if rows != 1 || changes != 1 || events != 1 || receipts != 1 {
					t.Fatalf("partial rollback %d %d %d %d", rows, changes, events, receipts)
				}
				var row domain.CustomFilter
				if err := db.First(&row, created.ID).Error; err != nil {
					t.Fatal(err)
				}
				if row.Name != created.Name || row.ObjectVersion != 1 || !row.UpdatedAt.Equal(created.UpdatedAt) {
					t.Fatal("mutation survived failed atomic commit")
				}
				if failure == "receipt" {
					_ = db.Callback().Update().Remove("srh-rollback")
				} else {
					_ = db.Callback().Create().Remove("srh-rollback")
				}
				f.command(t, c, 200)
			})
		}
	}
}

func TestSRHAdditiveSchemaKeepsLegacyRowsUnadopted(t *testing.T) {
	cfg := businessConfig("SRH")
	cfg.DBDriver, cfg.DBPath = "sqlite", t.TempDir()+"/legacy.db"
	db, err := OpenDatabase(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE custom_filters (id integer PRIMARY KEY AUTOINCREMENT, account_id integer NOT NULL, user_id integer NOT NULL, name text NOT NULL, filter_type text NOT NULL, query text NOT NULL, created_at datetime, updated_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO custom_filters (account_id,user_id,name,filter_type,query) VALUES (91,7,'legacy owner','conversation','{}'),(91,0,'legacy shared','report','{}')`).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrateModule(db, "SRH"); err != nil {
		t.Fatal(err)
	}
	var rows []domain.CustomFilter
	if err := db.Order("id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].UserID != 7 || rows[1].UserID != 0 || rows[0].ObjectVersion != 0 || rows[1].ObjectVersion != 0 {
		t.Fatal("migration adopted legacy rows")
	}
	f := newSRHFixture(t, db)
	if body := f.query(t, srhQuery(), 200); !strings.Contains(body, `"data":[]`) {
		t.Fatal(body)
	}
	q := srhQuery()
	q.Filter["id"] = rows[0].ID
	f.query(t, q, 404)
	c := srhCommand("legacy-not-adopted", "update")
	c.Target = foundation.ContractTarget{Type: "custom_filter", ID: strconv.Itoa(int(rows[0].ID)), ExpectedVersion: 1}
	c.Input["name"] = "adopt"
	f.command(t, c, 404)
	created := srhDecodeView(t, f.command(t, srhCommand("new-private", "create"), 200))
	if created.ObjectVersion != 1 || created.ID <= rows[1].ID {
		t.Fatal("new private row is invalid")
	}
}
