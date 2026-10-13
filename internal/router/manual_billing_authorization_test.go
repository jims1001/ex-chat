package router_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestManualBillingRequiresLivePlatformAuthority(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{Environment: "test", DBDriver: "sqlite", DBPath: ":memory:", JWTSecret: "manual-billing-test-key", JWTExpirationHours: 24}
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	users := repository.NewUserRepository(db)
	accounts := repository.NewAccountRepository(db)
	account := domain.Account{Name: "Billing tenant"}
	require.NoError(t, accounts.Create(&account))
	tenant := domain.User{Name: "Tenant Admin", Email: "tenant@example.test", Role: domain.RoleAdministrator, Type: "User"}
	platform := domain.User{Name: "Platform Admin", Email: "platform@example.test", Role: domain.RoleAdministrator, Type: domain.UserTypeSuperAdmin}
	for _, u := range []*domain.User{&tenant, &platform} {
		require.NoError(t, users.Create(u))
		require.NoError(t, accounts.AddMember(account.ID, u.ID, domain.RoleAdministrator))
	}
	tenantToken, err := auth.GenerateToken(&tenant, cfg.JWTSecret, 24)
	require.NoError(t, err)
	platformToken, err := auth.GenerateToken(&platform, cfg.JWTSecret, 24)
	require.NoError(t, err)
	r := router.SetupRouter(cfg, db, ws.NewHub())
	request := func(method, path, token string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(`{"action_type":"deposit","amount":10,"currency":"USD","type":"SuperAdmin","role":"super_admin"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	paths := []string{fmt.Sprintf("/api/v1/accounts/%d/billing", account.ID), fmt.Sprintf("/enterprise/api/v1/accounts/%d/billing", account.ID)}
	for _, path := range paths {
		require.Equal(t, 403, request(http.MethodPost, path+"?type=SuperAdmin", tenantToken).Code)
		require.Equal(t, 200, request(http.MethodGet, path, tenantToken).Code)
	}
	var count int64
	require.NoError(t, db.Model(&domain.AccountBilling{}).Count(&count).Error)
	require.Zero(t, count)
	w := request(http.MethodPost, paths[0], platformToken)
	require.Equal(t, 201, w.Code, w.Body.String())
	require.NoError(t, db.Model(&domain.AccountBilling{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	// Old JWT role/type claims do not keep platform authority after DB demotion.
	require.NoError(t, db.Model(&domain.User{}).Where("id = ?", platform.ID).Update("type", "User").Error)
	require.Equal(t, 403, request(http.MethodPost, paths[1], platformToken).Code)
	require.NoError(t, db.Model(&domain.AccountBilling{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}
