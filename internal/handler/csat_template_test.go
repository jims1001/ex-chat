package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type csatConfigOpener func(*testing.T) *gorm.DB

func csatConfigDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "csat.sqlite")), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	prepareCSATConfigDB(t, db)
	return db
}

func prepareCSATConfigDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	pool, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	require.NoError(t, database.AutoMigrateModule(db, "CHN"))
	providerCalls := 0
	previous := http.DefaultTransport
	http.DefaultTransport = csatNoNetworkTransport{&providerCalls}
	t.Cleanup(func() {
		http.DefaultTransport = previous
		require.Zero(t, providerCalls, "CSAT configuration must never call a provider")
	})
}

func csatConfigRows(t *testing.T, db *gorm.DB) []domain.Inbox {
	t.Helper()
	var rows []domain.Inbox
	require.NoError(t, db.Order("id").Find(&rows).Error)
	return rows
}

func csatConfigSeed(t *testing.T, db *gorm.DB) []domain.Inbox {
	t.Helper()
	rows := []domain.Inbox{
		{ID: 11, AccountID: 91, Name: "Local survey", ChannelType: "Channel::WebWidget", WebsiteToken: "local", CSATConfig: `{"question":"original","ratings":[1,2,3]}`},
		{ID: 12, AccountID: 92, Name: "Foreign survey", ChannelType: "Channel::WebWidget", WebsiteToken: "foreign", CSATConfig: `{"question":"foreign secret"}`},
		{ID: 13, AccountID: 91, Name: "WhatsApp survey", ChannelType: "Channel::Whatsapp", WebsiteToken: "whatsapp", CSATConfig: `{"message":"legacy config","status":"active"}`},
	}
	require.NoError(t, db.Create(&rows).Error)
	return rows
}

func csatConfigRequest(db *gorm.DB, method, id, body string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	h := NewInboxHandler(repository.NewInboxRepositoryWithOwnerPorts(db, nil, nil, nil), nil)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(middleware.ContextAccountID, uint(91)); c.Next() })
	r.GET("/inboxes/:id/csat_template", h.GetCSATTemplate)
	r.POST("/inboxes/:id/csat_template", h.SaveCSATTemplate)
	r.POST("/whatsapp/csat_template", (&ChannelDriverHandler{}).SetWhatsAppCSATTemplate)
	path := "/inboxes/" + id + "/csat_template"
	if id == "alias" {
		path = "/whatsapp/csat_template"
	}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	out := httptest.NewRecorder()
	r.ServeHTTP(out, req)
	return out
}

func csatConfigNoSuccess(t *testing.T, out *httptest.ResponseRecorder, code int) {
	t.Helper()
	require.Equal(t, code, out.Code, out.Body.String())
	var payload map[string]any
	require.NoError(t, json.Unmarshal(out.Body.Bytes(), &payload))
	require.Equal(t, false, payload["success"], out.Body.String())
	for _, key := range []string{"data", "template", "csat_template", "status"} {
		require.NotContains(t, payload, key)
	}
	require.NotContains(t, out.Body.String(), "private database marker")
}

func csatConfigWriteCounter(t *testing.T, db *gorm.DB) *int {
	t.Helper()
	writes := new(int)
	count := func(*gorm.DB) { *writes++ }
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("csat_writes", count))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("csat_writes", count))
	require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register("csat_writes", count))
	return writes
}

func testCSATConfigInvalidObjects(t *testing.T, open csatConfigOpener) {
	for _, tc := range []struct{ name, body string }{
		{"empty_body", ""}, {"malformed", `{"template":`}, {"null", `null`}, {"array", `[]`}, {"scalar", `true`},
		{"template_null", `{"template":null}`}, {"case_insensitive_template_null", `{"Template":null}`}, {"case_insensitive_csat_null", `{"CSAT_TEMPLATE":null}`}, {"invalid_unused_wrapper", `{"template":{"question":"valid"},"CSAT_TEMPLATE":null}`}, {"template_array", `{"template":[]}`}, {"template_scalar", `{"template":"wrong"}`},
		{"message_number", `{"message":12}`}, {"button_object", `{"button_text":{}}`}, {"csat_null", `{"csat_template":null}`}, {"csat_array", `{"csat_template":[]}`},
		{"trailing_object", `{"message":"new"}{}`}, {"trailing_garbage", `{"message":"new"}garbage`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := open(t)
			csatConfigSeed(t, db)
			before := csatConfigRows(t, db)
			writes := csatConfigWriteCounter(t, db)
			csatConfigNoSuccess(t, csatConfigRequest(db, http.MethodPost, "11", tc.body), http.StatusBadRequest)
			require.Zero(t, *writes)
			require.Equal(t, before, csatConfigRows(t, db))
		})
	}
}

func testCSATConfigScopedTargets(t *testing.T, open csatConfigOpener) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		for _, target := range []struct {
			id   string
			code int
		}{{"12", 404}, {"999", 404}, {"0", 400}, {"abc", 400}, {"-1", 400}, {"18446744073709551616", 400}} {
			t.Run(method+"_"+target.id, func(t *testing.T) {
				db := open(t)
				csatConfigSeed(t, db)
				before := csatConfigRows(t, db)
				writes := csatConfigWriteCounter(t, db)
				csatConfigNoSuccess(t, csatConfigRequest(db, method, target.id, `{"message":"new"}`), target.code)
				require.Zero(t, *writes)
				require.Equal(t, before, csatConfigRows(t, db))
			})
		}
	}
}

func testCSATConfigLocalRoundTrips(t *testing.T, open csatConfigOpener) {
	for _, tc := range []struct{ name, body, expected string }{
		{"template", `{"template":{"message":"Rate support","language":"zh","custom":{"nested":true}}}`, `{"message":"Rate support","language":"zh","custom":{"nested":true}}`},
		{"case_insensitive_template", `{"Template":{"question":"Valid case-insensitive object","ratings":[1,2]}}`, `{"question":"Valid case-insensitive object","ratings":[1,2]}`},
		{"csat_template", `{"csat_template":{"question":"How satisfied?","ratings":[1,2,3,4,5]}}`, `{"question":"How satisfied?","ratings":[1,2,3,4,5]}`},
		{"flat_defaults", `{"message":"Rate support"}`, `{"message":"Rate support","button_text":"","language":"","display_type":"","status":"active"}`},
		{"flat", `{"message":"How was support?","button_text":"Rate Us","language":"en","display_type":"emoji"}`, `{"message":"How was support?","button_text":"Rate Us","language":"en","display_type":"emoji","status":"active"}`},
		{"opaque_local_status", `{"template":{"question":"Local survey","status":"pending","custom":{"legacy":true}}}`, `{"question":"Local survey","status":"pending","custom":{"legacy":true}}`},
		{"precedence", `{"template":{"question":"primary"},"csat_template":{"question":"secondary"},"message":"flat"}`, `{"question":"primary"}`},
		{"empty_template_fallback", `{"template":{},"csat_template":{"question":"secondary"}}`, `{"question":"secondary"}`},
		{"empty_object", `{}`, `{"message":"","button_text":"","language":"","display_type":"","status":"active"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := open(t)
			csatConfigSeed(t, db)
			before := csatConfigRows(t, db)
			for _, method := range []string{http.MethodPost, http.MethodGet} {
				out := csatConfigRequest(db, method, "11", tc.body)
				require.Equal(t, http.StatusOK, out.Code, out.Body.String())
				var payload map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(out.Body.Bytes(), &payload))
				for _, alias := range []string{"template", "csat_template", "data"} {
					require.JSONEq(t, tc.expected, string(payload[alias]))
				}
				if method == http.MethodPost {
					require.JSONEq(t, `"active"`, string(payload["status"]))
				}
			}
			after := csatConfigRows(t, db)
			require.JSONEq(t, tc.expected, after[0].CSATConfig)
			require.Equal(t, before[1:], after[1:])
			require.Equal(t, before[0].CreatedAt, after[0].CreatedAt)
			require.Equal(t, before[0].ProviderConfig, after[0].ProviderConfig)
		})
	}
}

func testCSATConfigStorageErrors(t *testing.T, open csatConfigOpener) {
	for _, point := range []string{"lookup_get", "lookup_post", "write", "read"} {
		t.Run(point, func(t *testing.T) {
			db := open(t)
			csatConfigSeed(t, db)
			before := csatConfigRows(t, db)
			queryCount := 0
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register("csat_query_failure", func(tx *gorm.DB) {
				if tx.Statement.Table != "inboxes" {
					return
				}
				queryCount++
				if strings.HasPrefix(point, "lookup") || (point == "read" && queryCount == 3) {
					tx.AddError(errors.New("private database marker"))
				}
			}))
			if point == "write" {
				require.NoError(t, db.Callback().Update().Before("gorm:update").Register("csat_write_failure", func(tx *gorm.DB) { tx.AddError(errors.New("private database marker")) }))
			}
			method := http.MethodGet
			if point == "lookup_post" || point == "write" {
				method = http.MethodPost
			}
			out := csatConfigRequest(db, method, "11", `{"message":"new"}`)
			csatConfigNoSuccess(t, out, http.StatusInternalServerError)
			require.NoError(t, db.Callback().Query().Remove("csat_query_failure"))
			require.Equal(t, before, csatConfigRows(t, db))
		})
	}
}

func testCSATConfigDeletion(t *testing.T, open csatConfigOpener) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			db := open(t)
			csatConfigSeed(t, db)
			deleted := false
			require.NoError(t, db.Callback().Query().After("gorm:query").Register("csat_delete_after_lookup", func(tx *gorm.DB) {
				// FindByID has loaded the inbox and finished its final membership lookup.
				if !deleted && tx.Statement.Table == "inbox_members" && tx.Error == nil {
					deleted = true
					require.NoError(t, db.Where("id = ?", 11).Delete(&domain.Inbox{}).Error)
				}
			}))
			csatConfigNoSuccess(t, csatConfigRequest(db, method, "11", `{"message":"new"}`), http.StatusNotFound)
			require.True(t, deleted)
			require.Len(t, csatConfigRows(t, db), 2)
			err := repository.NewInboxRepositoryWithOwnerPorts(db, nil, nil, nil).SaveCSATTemplate(91, 11, map[string]any{"message": "late"})
			require.ErrorIs(t, err, gorm.ErrRecordNotFound)
			require.Len(t, csatConfigRows(t, db), 2)
		})
	}
}

func testCSATConfigStoredObjects(t *testing.T, open csatConfigOpener) {
	for _, tc := range []struct {
		name, value string
		code        int
	}{{"invalid", `{`, 500}, {"null", `null`, 200}, {"array", `[]`, 500}, {"scalar", `true`, 500}, {"empty_object", `{}`, 200}, {"absent", ``, 200}} {
		t.Run(tc.name, func(t *testing.T) {
			db := open(t)
			csatConfigSeed(t, db)
			require.NoError(t, db.Model(&domain.Inbox{}).Where("id = ?", 11).Update("csat_config", tc.value).Error)
			before := csatConfigRows(t, db)
			writes := csatConfigWriteCounter(t, db)
			out := csatConfigRequest(db, http.MethodGet, "11", "")
			if tc.code != 200 {
				csatConfigNoSuccess(t, out, tc.code)
			} else {
				require.Equal(t, 200, out.Code, out.Body.String())
				var payload map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(out.Body.Bytes(), &payload))
				require.Contains(t, string(payload["template"]), `"status":"active"`)
			}
			require.Zero(t, *writes)
			require.Equal(t, before, csatConfigRows(t, db))
		})
	}
}

type csatNoNetworkTransport struct{ calls *int }

func (t csatNoNetworkTransport) RoundTrip(*http.Request) (*http.Response, error) {
	*t.calls++
	return nil, errors.New("unexpected provider request")
}

func testCSATConfigWhatsAppUnavailable(t *testing.T, open csatConfigOpener) {
	for _, stored := range []struct{ name, config string }{{"absent", ""}, {"malformed", "{"}} {
		t.Run("GET_13_"+stored.name, func(t *testing.T) {
			db := open(t)
			csatConfigSeed(t, db)
			require.NoError(t, db.Model(&domain.Inbox{}).Where("id = ?", 13).Update("csat_config", stored.config).Error)
			before := csatConfigRows(t, db)
			writes := csatConfigWriteCounter(t, db)
			csatConfigNoSuccess(t, csatConfigRequest(db, http.MethodGet, "13", ""), http.StatusServiceUnavailable)
			require.Zero(t, *writes)
			require.Equal(t, before, csatConfigRows(t, db))
		})
	}

	for _, target := range []struct{ method, id string }{{http.MethodGet, "13"}, {http.MethodPost, "13"}, {http.MethodPost, "alias"}} {
		t.Run(target.method+"_"+target.id, func(t *testing.T) {
			db := open(t)
			csatConfigSeed(t, db)
			before := csatConfigRows(t, db)
			writes := csatConfigWriteCounter(t, db)
			calls := 0
			previous := http.DefaultTransport
			http.DefaultTransport = csatNoNetworkTransport{&calls}
			t.Cleanup(func() { http.DefaultTransport = previous })
			out := csatConfigRequest(db, target.method, target.id, `{"message":"new","language":"en","button_text":"Rate"}`)
			csatConfigNoSuccess(t, out, http.StatusServiceUnavailable)
			require.Contains(t, out.Body.String(), "currently unavailable")
			require.Zero(t, calls)
			require.Zero(t, *writes)
			require.Equal(t, before, csatConfigRows(t, db))
		})
	}
}

func runCSATConfigCorrections(t *testing.T, open csatConfigOpener) {
	for _, tc := range []struct {
		name string
		run  func(*testing.T, csatConfigOpener)
	}{
		{"invalid_objects", testCSATConfigInvalidObjects}, {"scoped_targets", testCSATConfigScopedTargets}, {"local_roundtrips", testCSATConfigLocalRoundTrips},
		{"storage_errors", testCSATConfigStorageErrors}, {"deletion", testCSATConfigDeletion}, {"stored_objects", testCSATConfigStoredObjects}, {"whatsapp_unavailable", testCSATConfigWhatsAppUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) { tc.run(t, open) })
	}
}

func TestCSATConfigCorrections(t *testing.T) { runCSATConfigCorrections(t, csatConfigDB) }
