package router_test

import (
	"encoding/json"
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
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func instanceStatusRouteCases(t *testing.T, cfg *config.Config, db *gorm.DB, dialect string) {
	t.Helper()
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })
	cfg.JWTSecret = "instance-status-synthetic-test-secret"
	cfg.SuperAdminEmails = []string{"allowed@example.invalid"}
	r := router.SetupRouter(cfg, db, ws.NewHub())
	request := func(path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, strings.NewReader(`{"type":"SuperAdmin","role":"super_admin"}`))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("X-User-Type", "SuperAdmin")
		req.Header.Set("X-User-Role", "super_admin")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	principal := func(email, role, kind string) (domain.User, string) {
		user := domain.User{Name: "Synthetic diagnostic user", Email: email, Role: role, Type: kind}
		require.NoError(t, db.Create(&user).Error)
		token, err := auth.GenerateToken(&user, cfg.JWTSecret, 1)
		require.NoError(t, err)
		return user, token
	}
	_, agent := principal("agent@example.invalid", domain.RoleAgent, domain.UserTypeUser)
	tenantUser, tenantAdmin := principal("tenant@example.invalid", domain.RoleAdministrator, domain.UserTypeUser)
	account := domain.Account{Name: "Tenant diagnostics fixture"}
	require.NoError(t, db.Create(&account).Error)
	require.NoError(t, db.Create(&domain.AccountUser{AccountID: account.ID, UserID: tenantUser.ID, Role: domain.RoleAdministrator}).Error)
	typeUser, typeToken := principal("type@example.invalid", domain.RoleAgent, domain.UserTypeSuperAdmin)
	roleUser, roleToken := principal("role@example.invalid", domain.RoleSuperAdmin, domain.UserTypeUser)
	_, emailToken := principal("allowed@example.invalid", domain.RoleAgent, domain.UserTypeUser)
	forged, err := auth.GenerateToken(&typeUser, "wrong-synthetic-test-secret", 1)
	require.NoError(t, err)
	expired, err := auth.GenerateToken(&typeUser, cfg.JWTSecret, -1)
	require.NoError(t, err)
	revoked, err := auth.GenerateToken(&typeUser, cfg.JWTSecret, 1)
	require.NoError(t, err)
	require.NoError(t, repository.NewChannelAuthEnterpriseRepository(db).RevokeToken(revoked, typeUser.ID, time.Now().Add(time.Hour)))
	// A correctly signed stale privilege hint cannot replace the persisted user.
	stale := tenantUser
	stale.Role, stale.Type = domain.RoleSuperAdmin, domain.UserTypeSuperAdmin
	staleToken, err := auth.GenerateToken(&stale, cfg.JWTSecret, 1)
	require.NoError(t, err)
	denied := func(token string, status int) {
		w := request("/super_admin/instance_status?type=SuperAdmin&role=super_admin", token)
		require.Equal(t, status, w.Code, w.Body.String())
		for _, diagnostic := range []string{`"database"`, `"version"`, `"online"`, `4.16.0`} {
			require.NotContains(t, w.Body.String(), diagnostic)
		}
	}
	for _, tc := range []struct {
		name, token string
		status      int
	}{
		{"anonymous", "", 401}, {"forged", forged, 401}, {"expired", expired, 401}, {"revoked", revoked, 401},
		{"agent", agent, 403}, {"tenant_administrator", tenantAdmin, 403}, {"signed_privilege_hints", staleToken, 403},
	} {
		t.Run(tc.name, func(t *testing.T) { denied(tc.token, tc.status) })
	}
	for name, token := range map[string]string{"type": typeToken, "role": roleToken, "configured_email": emailToken} {
		t.Run(name, func(t *testing.T) {
			w := request("/super_admin/instance_status", token)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var data map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &data))
			require.Equal(t, map[string]any{"status": "online", "version": "4.16.0", "database": dialect}, data)
		})
	}
	// Reuse already issued credentials after both persisted privilege forms are removed.
	require.NoError(t, db.Model(&typeUser).Update("type", domain.UserTypeUser).Error)
	denied(typeToken, http.StatusForbidden)
	require.NoError(t, db.Model(&roleUser).Update("role", domain.RoleAgent).Error)
	denied(roleToken, http.StatusForbidden)
	cfg.SuperAdminEmails = nil
	denied(emailToken, http.StatusForbidden)
	for _, token := range []string{"", agent, forged, revoked} {
		w := request("/health", token)
		require.Equal(t, http.StatusOK, w.Code)
		var data map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &data))
		require.Equal(t, map[string]any{"status": "healthy", "service": "ex-chat", "foundation_version": foundation.CurrentFoundationVersion}, data)
	}
	require.Equal(t, http.StatusNotFound, request("/api/v1/super_admin/instance_status", typeToken).Code)
}

func TestInstanceStatusSuperAdminSQLite(t *testing.T) {
	for _, driver := range []string{"sqlite", "unknown-driver-falls-back-to-sqlite"} {
		t.Run(driver, func(t *testing.T) {
			cfg := &config.Config{Environment: "test", DBDriver: driver, DBPath: filepath.Join(t.TempDir(), "instance.db")}
			db, err := database.InitDB(cfg)
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
			instanceStatusRouteCases(t, cfg, db, "sqlite")
		})
	}
}

func TestInstanceStatusSuperAdminPostgres(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("DB_DSN required for isolated PostgreSQL diagnostics regression")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	adminSQL, err := admin.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, adminSQL.Close()) })
	schema := fmt.Sprintf("instance_status_%d", time.Now().UnixNano())
	require.NoError(t, admin.Exec("CREATE SCHEMA "+schema).Error)
	t.Cleanup(func() { require.NoError(t, admin.Exec("DROP SCHEMA "+schema+" CASCADE").Error) })
	cfg := &config.Config{Environment: "test", DBDriver: "postgres", DBDSN: dsn + " search_path=" + schema}
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	// Deliberately stale configuration must not override the opened database.
	cfg.DBDriver = "sqlite"
	instanceStatusRouteCases(t, cfg, db, "postgres")
}
