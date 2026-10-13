package router_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var auditTruthTables = []string{"audit_exports", "integrity_verifications", "access_logs", "audit_logs", "local_change_journals"}

func auditTruthSnapshot(t *testing.T, db *gorm.DB) map[string][]map[string]any {
	t.Helper()
	result := make(map[string][]map[string]any)
	for _, table := range auditTruthTables {
		var rows []map[string]any
		require.NoError(t, db.Table(table).Order("id").Find(&rows).Error)
		result[table] = rows
	}
	return result
}

// Catch attempted writes as well as committed rows, including swallowed failures.
func auditTruthNoWrites(t *testing.T, db *gorm.DB) {
	t.Helper()
	before := auditTruthSnapshot(t, db)
	attempts := 0
	observe := func(tx *gorm.DB) {
		for _, table := range auditTruthTables {
			if tx.Statement.Table == table {
				attempts++
			}
		}
	}
	name := "test:audit_truth_no_writes"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(name, observe))
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(name, observe))
	require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register(name, observe))
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Create().Remove(name))
		require.NoError(t, db.Callback().Update().Remove(name))
		require.NoError(t, db.Callback().Delete().Remove(name))
		require.Equal(t, 0, attempts, "audit endpoints must not attempt audit-table writes")
		require.Equal(t, before, auditTruthSnapshot(t, db), "historical rows must remain unchanged")
	})
}

func auditTruthMeta(t *testing.T, body map[string]any, export bool) {
	t.Helper()
	meta, ok := body["meta"].(map[string]any)
	require.True(t, ok, "stored claims need explicit response qualification: %v", body)
	require.Equal(t, "historical_unverified_claim", meta["record_semantics"])
	require.Equal(t, false, meta["execution_available"])
	require.Equal(t, false, meta["execution_proven"])
	if export {
		require.Equal(t, false, meta["artifact_available"])
	}
}

func auditExecutionTruthCases(t *testing.T, cfg *config.Config, db *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg.JWTSecret = "audit-truth-synthetic-test-secret"
	r := router.SetupRouter(cfg, db, ws.NewHub())
	accounts := []domain.Account{{Name: "Audit local"}, {Name: "Audit foreign"}}
	require.NoError(t, db.Create(&accounts).Error)
	own, foreign := accounts[0].ID, accounts[1].ID
	role := domain.CustomRole{AccountID: own, Name: "auditor", Permissions: `["audit_manage"]`}
	require.NoError(t, db.Create(&role).Error)
	principal := func(name, roleName string, custom *uint, accountID uint) string {
		user := domain.User{Name: name, Email: name + "@example.invalid", Role: domain.RoleAgent}
		require.NoError(t, db.Create(&user).Error)
		require.NoError(t, db.Create(&domain.AccountUser{AccountID: accountID, UserID: user.ID, Role: roleName, CustomRoleID: custom}).Error)
		token, err := auth.GenerateToken(&user, cfg.JWTSecret, 1)
		require.NoError(t, err)
		return token
	}
	admin := principal("audit-admin", domain.RoleAdministrator, nil, own)
	auditor := principal("audit-custom", domain.RoleAgent, &role.ID, own)
	agent := principal("audit-agent", domain.RoleAgent, nil, own)
	outsider := principal("audit-outsider", domain.RoleAdministrator, nil, foreign)
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		out := httptest.NewRecorder()
		r.ServeHTTP(out, req)
		return out
	}
	decode := func(out *httptest.ResponseRecorder) map[string]any {
		result := map[string]any{}
		require.NoError(t, json.Unmarshal(out.Body.Bytes(), &result))
		return result
	}
	base := fmt.Sprintf("/api/v1/accounts/%d", own)
	for _, endpoint := range []struct{ name, path, message string }{
		{"export", "/audit_exports", "Audit export generation is not implemented"},
		{"verification", "/audit_integrity/verifications", "Audit integrity verification is not implemented"},
	} {
		t.Run(endpoint.name+"_unavailable", func(t *testing.T) {
			auditTruthNoWrites(t, db)
			for _, token := range []string{admin, auditor} {
				for _, body := range []string{"", `{`, `null`, `[]`, `{"purpose":"private-marker","partition_id":"account-partition","checkpoint_id":"checkpoint","format":"json"}`, fmt.Sprintf(`{"account_id":%d,"status":"completed","verified":true,"record_count":10,"download_url":"private-marker"}`, foreign)} {
					out := request(http.MethodPost, base+endpoint.path, token, body)
					require.Equal(t, http.StatusNotImplemented, out.Code, out.Body.String())
					require.Equal(t, map[string]any{"success": false, "error": endpoint.message}, decode(out))
				}
			}
		})
	}
	t.Run("empty_reads_are_storage_only", func(t *testing.T) {
		auditTruthNoWrites(t, db)
		repo := repository.NewJournalRepository(db)
		var previous string
		for i := 0; i < 3; i++ {
			list, err := repo.ListIntegrityVerifications(own)
			require.NoError(t, err)
			require.NotNil(t, list)
			require.Empty(t, list)
			out := request(http.MethodGet, base+"/audit_integrity/verifications", admin, "")
			require.Equal(t, http.StatusOK, out.Code, out.Body.String())
			result := decode(out)
			require.Equal(t, []any{}, result["data"])
			require.Equal(t, []any{}, result["payload"])
			auditTruthMeta(t, result, false)
			require.Equal(t, float64(0), result["meta"].(map[string]any)["total"])
			if previous != "" {
				require.JSONEq(t, previous, out.Body.String())
			}
			previous = out.Body.String()
		}
	})
	t.Run("authorization_and_tenant_boundaries", func(t *testing.T) {
		auditTruthNoWrites(t, db)
		for _, route := range []struct{ method, path string }{
			{http.MethodPost, "/audit_exports"}, {http.MethodPost, "/audit_integrity/verifications"},
			{http.MethodGet, "/audit_exports/missing"}, {http.MethodGet, "/audit_integrity/verifications/missing"}, {http.MethodGet, "/audit_integrity/verifications"},
		} {
			for _, tc := range []struct {
				token  string
				status int
			}{{"", 401}, {"forged-token", 401}, {agent, 403}, {outsider, 403}} {
				out := request(route.method, base+route.path, tc.token, `{}`)
				require.Equal(t, tc.status, out.Code, out.Body.String())
				require.NotContains(t, out.Body.String(), "not implemented", "authorization must run before handler")
			}
			out := request(route.method, fmt.Sprintf("/api/v1/accounts/%d", foreign)+route.path, admin, `{}`)
			require.Equal(t, http.StatusForbidden, out.Code, out.Body.String())
		}
	})
	t.Run("historical_claims_are_not_execution_proof", func(t *testing.T) {
		now := time.Date(2026, 9, 12, 1, 2, 3, 0, time.UTC)
		exports := []domain.AuditExport{
			{ExportID: "legacy-export", AccountID: own, Status: "completed", RecordCount: 10, DownloadURL: base + "/audit_exports/legacy-export/download", ExpiresAt: now.Add(24 * time.Hour), CreatedAt: now},
			{ExportID: "foreign-export", AccountID: foreign, Status: "completed", RecordCount: 10, DownloadURL: "/foreign-artifact-claim", CreatedAt: now},
		}
		verifications := []domain.IntegrityVerification{
			{VerificationID: "VERIF-ARCH-01", AccountID: own, PartitionID: "platform-core-partition", CheckpointID: "CP-20260912-001", Verified: true, Status: "completed", Details: "All cryptographic hash chains and checkpoints verified successfully", CreatedAt: now},
			{VerificationID: "foreign-verification", AccountID: foreign, Verified: true, Status: "completed", CreatedAt: now},
		}
		require.NoError(t, db.Create(&exports).Error)
		require.NoError(t, db.Create(&verifications).Error)
		auditTruthNoWrites(t, db)
		for _, token := range []string{admin, auditor} {
			out := request(http.MethodGet, base+"/audit_exports/legacy-export", token, "")
			require.Equal(t, http.StatusOK, out.Code, out.Body.String())
			result := decode(out)
			require.Equal(t, true, result["success"])
			auditTruthMeta(t, result, true)
			stored := result["data"].(map[string]any)
			require.Equal(t, float64(exports[0].ID), stored["id"])
			require.Equal(t, "legacy-export", stored["export_id"])
			require.Equal(t, "completed", stored["status"])
			require.Equal(t, float64(10), stored["record_count"])
			require.Equal(t, exports[0].DownloadURL, stored["download_url"])
			require.Equal(t, http.StatusNotFound, request(http.MethodGet, exports[0].DownloadURL, token, "").Code)
			out = request(http.MethodGet, base+"/audit_integrity/verifications/VERIF-ARCH-01", token, "")
			require.Equal(t, http.StatusOK, out.Code, out.Body.String())
			result = decode(out)
			auditTruthMeta(t, result, false)
			stored = result["data"].(map[string]any)
			require.Equal(t, float64(verifications[0].ID), stored["id"])
			require.Equal(t, true, stored["verified"])
			require.Equal(t, "completed", stored["status"])
			require.Equal(t, verifications[0].Details, stored["details"])
			out = request(http.MethodGet, base+"/audit_integrity/verifications", token, "")
			require.Equal(t, http.StatusOK, out.Code, out.Body.String())
			result = decode(out)
			auditTruthMeta(t, result, false)
			require.Equal(t, float64(1), result["meta"].(map[string]any)["total"])
			require.Equal(t, []any{stored}, result["data"])
			require.Equal(t, result["data"], result["payload"])
		}
		repo := repository.NewJournalRepository(db)
		list, err := repo.ListIntegrityVerifications(own)
		require.NoError(t, err)
		require.Len(t, list, 1)
		require.Equal(t, verifications[0].ID, list[0].ID)
		for _, suffix := range []string{"/audit_exports/missing", "/audit_exports/foreign-export", "/audit_integrity/verifications/missing", "/audit_integrity/verifications/foreign-verification"} {
			out := request(http.MethodGet, base+suffix, admin, "")
			require.Equal(t, http.StatusNotFound, out.Code, out.Body.String())
			require.NotContains(t, out.Body.String(), "foreign-artifact")
		}
	})
	t.Run("storage_errors_are_static", func(t *testing.T) {
		auditTruthNoWrites(t, db)
		name := "test:audit_truth_query_failure"
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register(name, func(tx *gorm.DB) {
			if tx.Statement.Table == "audit_exports" || tx.Statement.Table == "integrity_verifications" {
				tx.AddError(errors.New("private-sql-marker"))
			}
		}))
		defer func() { require.NoError(t, db.Callback().Query().Remove(name)) }()
		require.NoError(t, db.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
			for _, table := range auditTruthTables {
				if tx.Statement.Table == table {
					tx.AddError(errors.New("private-sql-marker"))
				}
			}
		}))
		defer func() { require.NoError(t, db.Callback().Create().Remove(name)) }()
		for _, tc := range []struct{ path, message string }{
			{"/audit_exports", "Audit export generation is not implemented"},
			{"/audit_integrity/verifications", "Audit integrity verification is not implemented"},
		} {
			out := request(http.MethodPost, base+tc.path, admin, `{"purpose":"private-request-marker"}`)
			require.Equal(t, http.StatusNotImplemented, out.Code, out.Body.String())
			require.Equal(t, map[string]any{"success": false, "error": tc.message}, decode(out))
		}
		for _, tc := range []struct {
			path    string
			status  int
			message string
		}{
			{"/audit_exports/legacy-export", 404, "Audit export not found"},
			{"/audit_integrity/verifications/VERIF-ARCH-01", 404, "Integrity verification not found"},
			{"/audit_integrity/verifications", 500, "Failed to list integrity verifications"},
		} {
			out := request(http.MethodGet, base+tc.path, admin, "")
			require.Equal(t, tc.status, out.Code, out.Body.String())
			require.Equal(t, map[string]any{"success": false, "error": tc.message}, decode(out))
		}
		list, err := repository.NewJournalRepository(db).ListIntegrityVerifications(own)
		require.Error(t, err)
		require.Nil(t, list)
	})
}

func TestAuditExecutionTruthSQLite(t *testing.T) {
	cfg := &config.Config{Environment: "test", DBDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "audit.db")}
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	auditExecutionTruthCases(t, cfg, db)
}

func TestAuditExecutionTruthPostgres(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN required for isolated PostgreSQL audit execution regression")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	adminSQL, err := admin.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminSQL.Close()) })
	schema := fmt.Sprintf("audit_execution_truth_%d", time.Now().UnixNano())
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() { require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error) })
	cfg := &config.Config{Environment: "test", DBDriver: "postgres", DBDSN: dsn + " search_path=" + schema}
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	auditExecutionTruthCases(t, cfg, db)
}
