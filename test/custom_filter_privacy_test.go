package test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type privateFilterFixture struct {
	db       *gorm.DB
	router   http.Handler
	accounts [2]domain.Account
	users    [4]domain.User
	tokens   [4]string
}

func newPrivateFilterFixture(t *testing.T) *privateFilterFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{Environment: "test", DBDriver: "sqlite", DBPath: ":memory:", JWTSecret: "saved-filter-private-route-test-secret", JWTExpirationHours: 24}
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	f := &privateFilterFixture{db: db}
	for i := range f.accounts {
		f.accounts[i] = domain.Account{Name: fmt.Sprintf("Filter account %d", i)}
		require.NoError(t, db.Create(&f.accounts[i]).Error)
	}
	for i := range f.users {
		role := domain.RoleAgent
		if i == 2 {
			role = domain.RoleAdministrator
		}
		f.users[i] = domain.User{Name: fmt.Sprintf("Filter user %d", i), Email: fmt.Sprintf("filter-user-%d@example.invalid", i), Role: role}
		require.NoError(t, db.Create(&f.users[i]).Error)
		account := f.accounts[0].ID
		if i == 3 {
			account = f.accounts[1].ID
		}
		require.NoError(t, db.Create(&domain.AccountUser{AccountID: account, UserID: f.users[i].ID, Role: role}).Error)
		f.tokens[i], err = auth.GenerateToken(&f.users[i], cfg.JWTSecret, 24)
		require.NoError(t, err)
	}
	f.router = router.SetupRouter(cfg, db, ws.NewHub())
	return f
}

func (f *privateFilterFixture) request(method string, account, id uint, token, body, query string) *httptest.ResponseRecorder {
	path := fmt.Sprintf("/api/v1/accounts/%d/custom_filters", account)
	if id != 0 {
		path += fmt.Sprintf("/%d", id)
	}
	path += query
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	// These untrusted headers must never override the JWT actor.
	req.Header.Set("X-User-ID", fmt.Sprint(f.users[0].ID))
	req.Header.Set("X-Account-ID", fmt.Sprint(f.accounts[0].ID))
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

func privateFilterRow(t *testing.T, response *httptest.ResponseRecorder, status int) domain.CustomFilter {
	t.Helper()
	require.Equal(t, status, response.Code, response.Body.String())
	var decoded struct {
		Success bool                `json:"success"`
		Data    domain.CustomFilter `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &decoded))
	require.True(t, decoded.Success)
	require.Positive(t, decoded.Data.ID)
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
	require.Len(t, envelope, 2)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(envelope["data"], &fields))
	require.Len(t, fields, 9, "existing CustomFilter DTO shape")
	for _, key := range []string{"id", "account_id", "user_id", "name", "filter_type", "query", "object_version", "created_at", "updated_at"} {
		require.Contains(t, fields, key)
	}
	require.True(t, len(fields["query"]) > 0 && fields["query"][0] == '"', "REST query remains a string")
	return decoded.Data
}

func TestCustomFilterRESTPrivateOwnership(t *testing.T) {
	f := newPrivateFilterFixture(t)
	account := f.accounts[0].ID
	rows := make([]domain.CustomFilter, 4)
	for i := range rows {
		a := account
		if i == 3 {
			a = f.accounts[1].ID
		}
		body := fmt.Sprintf(`{"name":"PRIVATE-NAME-%d","filter_type":"conversation","query":{"integer":9007199254740993,"decimal":0.123456789012345678901234567890,"nested":[null,{"private":"PRIVATE-CRITERIA"}]},"user_id":%d,"account_id":%d,"id":999999,"object_version":900}`, i, f.users[(i+1)%4].ID, f.accounts[1].ID)
		rows[i] = privateFilterRow(t, f.request(http.MethodPost, a, 0, f.tokens[i], body, "?user_id=0"), http.StatusCreated)
		require.Equal(t, a, rows[i].AccountID)
		require.Equal(t, f.users[i].ID, rows[i].UserID)
		require.Zero(t, rows[i].ObjectVersion)
		require.NotEqual(t, uint(999999), rows[i].ID)
		require.Contains(t, rows[i].Query, "9007199254740993")
		require.Contains(t, rows[i].Query, "0.123456789012345678901234567890")
	}
	unowned := domain.CustomFilter{AccountID: account, UserID: 0, Name: "PRIVATE-UNOWNED", FilterType: "conversation", Query: "PRIVATE-UNOWNED-QUERY"}
	legacy := domain.CustomFilter{AccountID: account, UserID: f.users[0].ID, Name: "PRIVATE-LEGACY", FilterType: "contact", Query: `{"legacy":9007199254740993}`}
	runtime := domain.CustomFilter{AccountID: account, UserID: f.users[0].ID, Name: "PRIVATE-RUNTIME", FilterType: "report", Query: `{"runtime":true}`, ObjectVersion: 4}
	for _, row := range []*domain.CustomFilter{&unowned, &legacy, &runtime} {
		require.NoError(t, f.db.Create(row).Error)
	}

	t.Run("list and detail are private including admin", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			w := f.request(http.MethodGet, account, 0, f.tokens[i], "", fmt.Sprintf("?user_id=%d", f.users[0].ID))
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var listed struct {
				Data []domain.CustomFilter `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &listed))
			expected := 1
			if i == 0 {
				expected = 3
			}
			require.Len(t, listed.Data, expected)
			for j, row := range listed.Data {
				require.Equal(t, f.users[i].ID, row.UserID)
				if j > 0 {
					require.Greater(t, listed.Data[j-1].ID, row.ID)
				}
				privateFilterRow(t, f.request(http.MethodGet, account, row.ID, f.tokens[i], "", ""), http.StatusOK)
			}
		}
		w := f.request(http.MethodGet, account, 0, f.tokens[0], "", "?filter_type=contact")
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), legacy.Name)
		require.NotContains(t, w.Body.String(), rows[0].Name)
		w = f.request(http.MethodGet, account, 0, f.tokens[1], "", "?filter_type=report")
		require.JSONEq(t, `{"success":true,"data":[]}`, w.Body.String())
	})

	t.Run("other owners and unowned rows cannot be read changed or deleted", func(t *testing.T) {
		for _, actor := range []int{1, 2} {
			for _, row := range []domain.CustomFilter{rows[0], unowned, runtime, rows[3]} {
				for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodPatch, http.MethodDelete} {
					w := f.request(method, account, row.ID, f.tokens[actor], `{"name":"ATTACK","query":"ATTACK"}`, "")
					want := http.StatusNotFound
					if method == http.MethodDelete {
						want = http.StatusOK
						require.JSONEq(t, `{"success":true,"data":{"deleted":true}}`, w.Body.String())
					}
					require.Equal(t, want, w.Code, w.Body.String())
					require.NotContains(t, w.Body.String(), "PRIVATE-")
				}
				var unchanged domain.CustomFilter
				require.NoError(t, f.db.First(&unchanged, row.ID).Error)
				require.Equal(t, row.Name, unchanged.Name)
				require.Equal(t, row.Query, unchanged.Query)
				require.Equal(t, row.ObjectVersion, unchanged.ObjectVersion)
			}
		}
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
			id := rows[0].ID
			if method == http.MethodPost {
				id = 0
			}
			w := f.request(method, account, id, f.tokens[3], `{"name":"ATTACK","filter_type":"contact","query":{}}`, "")
			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
		}
	})

	t.Run("owned legacy CRUD and exact string query remain usable", func(t *testing.T) {
		body := `{"name":"  Rest saved  ","filter_type":"contact","query":"  opaque legacy query 9007199254740993  "}`
		created := privateFilterRow(t, f.request(http.MethodPost, account, 0, f.tokens[0], body, ""), http.StatusCreated)
		require.Equal(t, "opaque legacy query 9007199254740993", created.Query)
		updated := privateFilterRow(t, f.request(http.MethodPut, account, legacy.ID, f.tokens[0], `{"query":{"precise":123456789012345678901234567890.00001}}`, ""), http.StatusOK)
		require.Equal(t, legacy.Name, updated.Name)
		require.Equal(t, `{"precise":123456789012345678901234567890.00001}`, updated.Query)
		updated = privateFilterRow(t, f.request(http.MethodPatch, account, legacy.ID, f.tokens[0], `{"name":"Renamed","user_id":0,"object_version":99}`, ""), http.StatusOK)
		require.Equal(t, "Renamed", updated.Name)
		require.Equal(t, f.users[0].ID, updated.UserID)
		require.Zero(t, updated.ObjectVersion)
		for i := 0; i < 2; i++ {
			require.JSONEq(t, `{"success":true,"data":{"deleted":true}}`, f.request(http.MethodDelete, account, created.ID, f.tokens[0], "", "").Body.String())
		}
		require.Equal(t, http.StatusNotFound, f.request(http.MethodGet, account, created.ID, f.tokens[0], "", "").Code)
	})

	t.Run("versioned rows are readable but require SRH writes", func(t *testing.T) {
		privateFilterRow(t, f.request(http.MethodGet, account, runtime.ID, f.tokens[0], "", ""), http.StatusOK)
		for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
			w := f.request(method, account, runtime.ID, f.tokens[0], `{"name":"ATTACK"}`, "")
			require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
			require.NotContains(t, w.Body.String(), runtime.Name)
		}
		var unchanged domain.CustomFilter
		require.NoError(t, f.db.First(&unchanged, runtime.ID).Error)
		require.Equal(t, runtime.Name, unchanged.Name)
		require.Equal(t, runtime.Query, unchanged.Query)
		require.Equal(t, runtime.ObjectVersion, unchanged.ObjectVersion)
	})

	t.Run("real authentication and membership are required for every operation", func(t *testing.T) {
		for _, token := range []string{"", "forged.jwt.actor", f.tokens[0] + "forged"} {
			for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
				id := rows[0].ID
				if method == http.MethodPost {
					id = 0
				}
				require.Equal(t, http.StatusUnauthorized, f.request(method, account, id, token, `{}`, "?user_id=1").Code)
			}
			require.Equal(t, http.StatusUnauthorized, f.request(http.MethodGet, account, 0, token, "", "").Code)
		}
		require.NoError(t, f.db.Where("account_id = ? AND user_id = ?", account, f.users[1].ID).Delete(&domain.AccountUser{}).Error)
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			id := rows[1].ID
			if method == http.MethodPost {
				id = 0
			}
			require.Equal(t, http.StatusForbidden, f.request(method, account, id, f.tokens[1], `{}`, "").Code)
		}
		require.Equal(t, http.StatusForbidden, f.request(http.MethodGet, account, 0, f.tokens[1], "", "").Code)
	})
}

func TestCustomFilterRESTPrivateErrorsAndLogs(t *testing.T) {
	f := newPrivateFilterFixture(t)
	account, token := f.accounts[0].ID, f.tokens[0]
	row := privateFilterRow(t, f.request(http.MethodPost, account, 0, token, `{"name":"PRIVATE-SAVED-NAME","filter_type":"contact","query":{"secret":"PRIVATE-SAVED-QUERY"}}`, ""), http.StatusCreated)
	var logs bytes.Buffer
	logger.SetOutput(&logs, slog.LevelDebug, "json")
	t.Cleanup(func() { logger.SetOutput(os.Stderr, slog.LevelInfo, "json") })
	for _, method := range []string{http.MethodGet, http.MethodPatch} {
		w := f.request(method, account, row.ID, token, `{"name":"PRIVATE-NEW-NAME"}`, "?filter_type=PRIVATE-TYPE&query=PRIVATE-URL&user_id=0")
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	}
	for _, body := range []string{`{"name":123456789,"query":"PRIVATE-PAYLOAD"}`, `{"name":"x","filter_type":"contact","query":null}`, `{"name":"x","filter_type":"contact","query":["PRIVATE-PAYLOAD"]}`, `{"name":"x","filter_type":"contact","query":{}} {"private":"PRIVATE-PAYLOAD"}`} {
		w := f.request(http.MethodPost, account, 0, token, body, "")
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.NotContains(t, w.Body.String(), "PRIVATE-")
	}
	tests := []struct {
		name, method string
		id           uint
		install      func(func(*gorm.DB)) error
		remove       func() error
	}{
		{"create", http.MethodPost, 0, func(fn func(*gorm.DB)) error {
			return f.db.Callback().Create().After("gorm:create").Register("private_filter_failure", fn)
		}, func() error { return f.db.Callback().Create().Remove("private_filter_failure") }},
		{"list", http.MethodGet, 0, func(fn func(*gorm.DB)) error {
			return f.db.Callback().Query().After("gorm:query").Register("private_filter_failure", fn)
		}, func() error { return f.db.Callback().Query().Remove("private_filter_failure") }},
		{"get", http.MethodGet, row.ID, func(fn func(*gorm.DB)) error {
			return f.db.Callback().Query().After("gorm:query").Register("private_filter_failure", fn)
		}, func() error { return f.db.Callback().Query().Remove("private_filter_failure") }},
		{"update", http.MethodPatch, row.ID, func(fn func(*gorm.DB)) error {
			return f.db.Callback().Update().After("gorm:update").Register("private_filter_failure", fn)
		}, func() error { return f.db.Callback().Update().Remove("private_filter_failure") }},
		{"delete", http.MethodDelete, row.ID, func(fn func(*gorm.DB)) error {
			return f.db.Callback().Delete().After("gorm:delete").Register("private_filter_failure", fn)
		}, func() error { return f.db.Callback().Delete().Remove("private_filter_failure") }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, tc.install(func(tx *gorm.DB) {
				if tx.Statement.Table == "custom_filters" {
					tx.AddError(errors.New("PRIVATE-PROVIDER-ECHO contains PRIVATE-SAVED-QUERY"))
				}
			}))
			w := f.request(tc.method, account, tc.id, token, `{"name":"PRIVATE-FAILED-NAME","filter_type":"report","query":"PRIVATE-FAILED-QUERY"}`, "?filter_type=PRIVATE-URL-TYPE")
			require.NoError(t, tc.remove())
			require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
			require.JSONEq(t, `{"success":false,"error":"Custom filter storage unavailable"}`, w.Body.String())
			var existing domain.CustomFilter
			require.NoError(t, f.db.First(&existing, row.ID).Error)
			require.Equal(t, "PRIVATE-NEW-NAME", existing.Name)
			require.Equal(t, row.Query, existing.Query)
			var count int64
			require.NoError(t, f.db.Model(&domain.CustomFilter{}).Count(&count).Error)
			require.Equal(t, int64(1), count)
		})
	}
	require.Contains(t, logs.String(), "custom filter storage failed")
	require.NotContains(t, logs.String(), "PRIVATE-")
}
