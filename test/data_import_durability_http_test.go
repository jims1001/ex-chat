package test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDataImportDurabilityHTTP(t *testing.T) {
	cfg := importDurabilityHTTPConfig()
	cfg.DBDriver = "sqlite"
	cfg.DBPath = filepath.Join(t.TempDir(), "import-durability-http.db")
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	runDataImportDurabilityHTTP(t, cfg, db)
}
func importDurabilityHTTPConfig() *config.Config {
	return &config.Config{Environment: "test", JWTSecret: "data-import-durability-http-key-12345678", JWTExpirationHours: 24}
}

func runDataImportDurabilityHTTP(t *testing.T, cfg *config.Config, db *gorm.DB) {
	gin.SetMode(gin.TestMode)
	r := router.SetupRouter(cfg, db, ws.NewHub())
	for _, id := range []uint{91, 92} {
		require.NoError(t, db.Create(&domain.Account{ID: id, Name: "Import fixture"}).Error)
	}
	tokens := map[string]string{}
	for _, principal := range []string{"admin", "settings", "agent", "foreign"} {
		user := domain.User{Name: principal, Email: principal + "@import-http.example.test", Role: domain.RoleAgent}
		require.NoError(t, db.Create(&user).Error)
		membership := domain.AccountUser{AccountID: 91, UserID: user.ID, Role: domain.RoleAgent}
		if principal == "admin" {
			membership.Role = domain.RoleAdministrator
		}
		if principal == "foreign" {
			membership.AccountID = 92
			membership.Role = domain.RoleAdministrator
		}
		if principal == "settings" {
			role := domain.CustomRole{AccountID: 91, Name: "import settings", Permissions: `["settings_manage"]`}
			require.NoError(t, db.Create(&role).Error)
			membership.CustomRoleID = &role.ID
		}
		require.NoError(t, db.Create(&membership).Error)
		var err error
		tokens[principal], err = auth.GenerateToken(&user, cfg.JWTSecret, 24)
		require.NoError(t, err)
	}
	var createAt, updateAt, creates, updates, contacts int
	const marker = "import-http-storage-private-diagnostic"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register("import_http_create", func(tx *gorm.DB) {
		if tx.Statement.Table == "data_imports" {
			creates++
			if createAt > 0 && creates == createAt {
				_ = tx.AddError(errors.New(marker))
			}
		}
		if tx.Statement.Table == "contacts" {
			contacts++
		}
	}))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("import_http_update", func(tx *gorm.DB) {
		if tx.Statement.Table == "data_imports" {
			updates++
			if updateAt > 0 && updates == updateAt {
				_ = tx.AddError(errors.New(marker))
			}
		}
	}))
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Create().Remove("import_http_create"))
		require.NoError(t, db.Callback().Update().Remove("import_http_update"))
	})
	request := func(method, path, principal string, payload any, failCreate, failUpdate int) *httptest.ResponseRecorder {
		var body []byte
		var err error
		if payload != nil {
			body, err = json.Marshal(payload)
			require.NoError(t, err)
		}
		req := httptest.NewRequest(method, path, strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		if principal != "" {
			req.Header.Set("Authorization", "Bearer "+tokens[principal])
		}
		createAt, updateAt, creates, updates, contacts = failCreate, failUpdate, 0, 0, 0
		defer func() { createAt, updateAt = 0, 0 }()
		out := httptest.NewRecorder()
		r.ServeHTTP(out, req)
		return out
	}
	base := "/api/v1/accounts/91/data_imports"
	assertFailure := func(t *testing.T, out *httptest.ResponseRecorder, message string) {
		t.Helper()
		require.Equal(t, 500, out.Code, out.Body.String())
		require.JSONEq(t, fmt.Sprintf(`{"success":false,"error":%q}`, message), out.Body.String())
		require.NotContains(t, out.Body.String(), marker)
	}
	responseImport := func(t *testing.T, out *httptest.ResponseRecorder, status int) domain.DataImport {
		t.Helper()
		require.Equal(t, status, out.Code, out.Body.String())
		var envelope struct {
			Success bool              `json:"success"`
			Data    domain.DataImport `json:"data"`
		}
		require.NoError(t, json.Unmarshal(out.Body.Bytes(), &envelope))
		require.True(t, envelope.Success)
		require.NotZero(t, envelope.Data.ID)
		return envelope.Data
	}
	seed := func(t *testing.T, raw string) domain.DataImport {
		t.Helper()
		imp := domain.DataImport{AccountID: 91, ImportType: "contacts", SourceProvider: "csv", Status: "staged", RawData: raw}
		require.NoError(t, db.Create(&imp).Error)
		return imp
	}
	stored := func(t *testing.T, id uint) domain.DataImport {
		t.Helper()
		var imp domain.DataImport
		require.NoError(t, db.First(&imp, id).Error)
		return imp
	}
	t.Run("registered_v1_routes", func(t *testing.T) {
		found := map[string]bool{}
		for _, route := range r.Routes() {
			if strings.Contains(route.Path, "/data_imports") {
				require.True(t, strings.HasPrefix(route.Path, "/api/v1/accounts/:account_id/data_imports"), route.Path)
				found[route.Method+" "+route.Path] = true
			}
		}
		for _, suffix := range []string{"", "/prevalidate", "/:id/prevalidate", "/:id/start", "/:id/execute", "/:id/cancel", "/:id/discard"} {
			require.True(t, found["POST /api/v1/accounts/:account_id/data_imports"+suffix], suffix)
		}
		require.True(t, found["GET /api/v1/accounts/:account_id/data_imports/:id"])
	})
	t.Run("permission_and_tenant_guards", func(t *testing.T) {
		for _, principal := range []string{"admin", "settings", "agent", "foreign", ""} {
			out := request(http.MethodPost, base, principal, map[string]any{"total_records": 250}, 0, 0)
			if principal == "admin" || principal == "settings" {
				imp := responseImport(t, out, 201)
				require.Equal(t, "pending", imp.Status)
				require.Zero(t, imp.ProcessedRecords)
				require.Equal(t, 1, creates)
			} else {
				if principal == "" {
					require.Equal(t, 401, out.Code, out.Body.String())
				} else {
					require.Equal(t, 403, out.Code, out.Body.String())
				}
				require.Zero(t, creates)
			}
			require.Zero(t, contacts)
		}
		imp := seed(t, "name,email\nGuard,guard@import-http.example.test\n")
		for _, action := range []string{"prevalidate", "start", "execute", "cancel", "discard"} {
			out := request(http.MethodPost, fmt.Sprintf("%s/%d/%s", base, imp.ID, action), "agent", nil, 0, 0)
			require.Equal(t, 403, out.Code, out.Body.String())
			require.Zero(t, updates)
			require.Zero(t, contacts)
			out = request(http.MethodPost, fmt.Sprintf("/api/v1/accounts/92/data_imports/%d/%s", imp.ID, action), "foreign", nil, 0, 0)
			require.Equal(t, 404, out.Code, out.Body.String())
			require.Zero(t, updates)
			require.Zero(t, contacts)
		}
		require.Equal(t, "staged", stored(t, imp.ID).Status)
	})
	t.Run("create_admission_failure", func(t *testing.T) {
		out := request(http.MethodPost, base, "admin", map[string]any{"raw_data": "name,email\nAdmission,admit@import-http.example.test\n"}, 1, 0)
		assertFailure(t, out, "Failed to create import task")
		require.Equal(t, 1, creates)
		require.Zero(t, updates)
		require.Zero(t, contacts)
		var n int64
		require.NoError(t, db.Model(&domain.Contact{}).Where("email = ?", "admit@import-http.example.test").Count(&n).Error)
		require.Zero(t, n)
	})
	t.Run("execution_save_failures", func(t *testing.T) {
		for _, stage := range []string{"processing", "final", "handler_final"} {
			t.Run(stage, func(t *testing.T) {
				imp := seed(t, "name,email\nExecution,"+stage+"@import-http.example.test\n")
				at := 1
				message := "Failed to execute import"
				if stage == "final" {
					at = 2
				}
				if stage == "handler_final" {
					at = 3
					message = "Failed to update import task"
				}
				out := request(http.MethodPost, fmt.Sprintf("%s/%d/start", base, imp.ID), "admin", nil, 0, at)
				assertFailure(t, out, message)
				require.Equal(t, at, updates)
				status := "processing"
				writes := 1
				if stage == "processing" {
					status = "staged"
					writes = 0
				}
				if stage == "handler_final" {
					status = "completed"
				}
				require.Equal(t, writes, contacts)
				require.Equal(t, status, stored(t, imp.ID).Status)
				got := responseImport(t, request(http.MethodGet, fmt.Sprintf("%s/%d", base, imp.ID), "admin", nil, 0, 0), 200)
				require.Equal(t, status, got.Status)
				require.Zero(t, contacts)
			})
		}
	})
	t.Run("mutation_save_failures", func(t *testing.T) {
		for _, action := range []string{"prevalidate", "cancel", "discard"} {
			t.Run(action, func(t *testing.T) {
				imp := seed(t, "name,email\nMutation,"+action+"@import-http.example.test\n")
				before := stored(t, imp.ID)
				out := request(http.MethodPost, fmt.Sprintf("%s/%d/%s", base, imp.ID, action), "admin", nil, 0, 1)
				assertFailure(t, out, "Failed to update import task")
				require.Equal(t, 1, updates)
				require.Zero(t, contacts)
				require.Equal(t, before, stored(t, imp.ID))
			})
		}
	})
	t.Run("staged_execution_and_reports", func(t *testing.T) {
		raw := "name,email\nHealthy,healthy@import-http.example.test\nBad,not-an-email\nDuplicate,healthy@import-http.example.test\n"
		imp := responseImport(t, request(http.MethodPost, base, "settings", map[string]any{"raw_data": raw, "auto_start": false}, 0, 0), 201)
		require.Zero(t, imp.ProcessedRecords)
		require.Zero(t, contacts)
		imp = responseImport(t, request(http.MethodPost, fmt.Sprintf("%s/%d/execute", base, imp.ID), "settings", nil, 0, 0), 200)
		require.Equal(t, "completed", imp.Status)
		require.Equal(t, 3, imp.TotalRecords)
		require.Equal(t, 1, imp.ProcessedRecords)
		require.Equal(t, 1, imp.FailedRecords)
		require.Equal(t, 1, imp.SkippedRecords)
		require.Equal(t, 1, contacts)
		for _, suffix := range []string{"errors", "skipped", "errors/download", "skipped/download", "error_report", "skipped_records"} {
			out := request(http.MethodGet, fmt.Sprintf("%s/%d/%s", base, imp.ID, suffix), "admin", nil, 0, 0)
			require.Equal(t, 200, out.Code, out.Body.String())
			require.Zero(t, contacts)
			require.Zero(t, updates)
		}
		out := request(http.MethodPost, fmt.Sprintf("%s/%d/start", base, imp.ID), "admin", nil, 0, 0)
		require.Equal(t, 400, out.Code)
		require.Zero(t, contacts)
	})
	t.Run("count_only_readback", func(t *testing.T) {
		imp := responseImport(t, request(http.MethodPost, base, "admin", map[string]any{"total_records": 250}, 0, 0), 201)
		require.Equal(t, "pending", imp.Status)
		require.Equal(t, 250, imp.TotalRecords)
		require.Zero(t, imp.ProcessedRecords)
		require.Zero(t, contacts)
		require.Zero(t, updates)
		got := responseImport(t, request(http.MethodGet, fmt.Sprintf("%s/%d", base, imp.ID), "admin", nil, 0, 0), 200)
		require.Equal(t, "pending", got.Status)
		require.Zero(t, got.ProcessedRecords)
	})
}
