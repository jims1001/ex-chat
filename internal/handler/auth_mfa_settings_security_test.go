package handler_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/handler"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMFASettingsReauthenticationAndDisclosure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{DBDriver: "sqlite", DBPath: ":memory:", JWTSecret: "mfa-settings-test-secret", JWTExpirationHours: 24}
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	users := repository.NewUserRepository(db)
	accounts := repository.NewAccountRepository(db)
	repo := repository.NewChannelAuthEnterpriseRepository(db)
	h := handler.NewAuthEnterpriseHandler(repo, users, accounts, nil, nil, nil, nil, cfg)
	r := gin.New()
	g := r.Group("/mfa", middleware.AuthMiddleware(cfg.JWTSecret, users))
	g.GET("", h.GetMFAProfile)
	g.POST("/setup", h.SetupMFA)
	g.POST("/enable", h.EnableMFA)
	g.POST("/verify", h.VerifyMFA)
	g.POST("/backup_codes", h.GenerateBackupCodes)
	g.DELETE("", h.DisableMFA)
	hash, err := auth.HashPassword("current-password")
	require.NoError(t, err)
	newUser := func(email string, active bool, passwordless bool) (*domain.User, string) {
		u := &domain.User{Email: email, Name: "MFA settings", PasswordHash: hash, Role: domain.RoleAgent}
		if passwordless {
			u.PasswordHash = ""
		}
		require.NoError(t, users.Create(u))
		if active {
			require.NoError(t, repo.SaveMFAProfile(&domain.MFAProfile{UserID: u.ID, Secret: "JBSWY3DPEHPK3PXP", Enabled: true, BackupCodes: `["12345678","87654321"]`}))
		}
		token, err := auth.GenerateToken(u, cfg.JWTSecret, 24)
		require.NoError(t, err)
		return u, token
	}
	decode := func(raw []byte) map[string]any {
		t.Helper()
		var result struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(raw, &result))
		return result.Data
	}
	t.Run("GET is status only and does not create setup material", func(t *testing.T) {
		u, token := newUser("status-new@example.test", false, false)
		w := performMFARequest(t, r, http.MethodGet, "/mfa", nil, token)
		require.Equal(t, 200, w.Code)
		data := decode(w.Body.Bytes())
		require.Equal(t, false, data["enabled"])
		require.NotContains(t, data, "secret")
		require.NotContains(t, data, "backup_codes")
		profile, err := repo.GetMFAProfile(u.ID)
		require.NoError(t, err)
		require.Nil(t, profile)
		_, token = newUser("status-active@example.test", true, false)
		w = performMFARequest(t, r, http.MethodGet, "/mfa", nil, token)
		require.Equal(t, 200, w.Code)
		data = decode(w.Body.Bytes())
		require.Equal(t, true, data["enabled"])
		require.NotContains(t, data, "secret")
		require.NotContains(t, data, "backup_codes")
		require.NotContains(t, w.Body.String(), "JBSWY3DPEHPK3PXP")
		require.NotContains(t, w.Body.String(), "12345678")
	})
	t.Run("bearer token alone cannot change active MFA", func(t *testing.T) {
		u, token := newUser("missing-factor@example.test", true, false)
		for _, v := range []struct {
			method, path string
			body         map[string]string
		}{
			{http.MethodPost, "/mfa/backup_codes", map[string]string{}},
			{http.MethodDelete, "/mfa", map[string]string{"current_password": "wrong", "backup_code": "12345678"}},
			{http.MethodDelete, "/mfa", map[string]string{"current_password": "current-password"}},
		} {
			w := performMFARequest(t, r, v.method, v.path, v.body, token)
			require.Equal(t, 401, w.Code)
			require.NotContains(t, decode(w.Body.Bytes()), "backup_codes")
		}
		profile, err := repo.GetMFAProfile(u.ID)
		require.NoError(t, err)
		require.True(t, profile.Enabled)
		require.Contains(t, profile.BackupCodes, "12345678")
	})
	t.Run("setup enable regenerate disable require fresh reauthentication", func(t *testing.T) {
		u, token := newUser("manage-flow@example.test", false, false)
		w := performMFARequest(t, r, http.MethodPost, "/mfa/setup", map[string]string{"current_password": "current-password"}, token)
		require.Equal(t, 200, w.Code, w.Body.String())
		data := decode(w.Body.Bytes())
		secret, ok := data["secret"].(string)
		require.True(t, ok)
		require.NotEmpty(t, secret)
		require.NotContains(t, data, "backup_codes")
		code, err := auth.GenerateTOTPCode(secret, time.Now())
		require.NoError(t, err)
		w = performMFARequest(t, r, http.MethodPost, "/mfa/verify", map[string]string{"current_password": "current-password", "otp_code": code}, token)
		require.Equal(t, 200, w.Code, w.Body.String())
		data = decode(w.Body.Bytes())
		require.NotContains(t, data, "secret")
		codes := data["backup_codes"].([]any)
		require.Len(t, codes, 8)
		w = performMFARequest(t, r, http.MethodPost, "/mfa/backup_codes", map[string]string{"current_password": "current-password", "backup_code": codes[0].(string)}, token)
		require.Equal(t, 200, w.Code, w.Body.String())
		fresh := decode(w.Body.Bytes())["backup_codes"].([]any)
		require.Len(t, fresh, 8)
		w = performMFARequest(t, r, http.MethodDelete, "/mfa", map[string]string{"current_password": "current-password", "backup_code": fresh[0].(string)}, token)
		require.Equal(t, 200, w.Code, w.Body.String())
		require.Equal(t, false, decode(w.Body.Bytes())["enabled"])
		profile, err := repo.GetMFAProfile(u.ID)
		require.NoError(t, err)
		require.False(t, profile.Enabled)
		require.Empty(t, profile.Secret)
	})
	t.Run("setup TOTP cannot be replayed to disable", func(t *testing.T) {
		_, token := newUser("setup-replay@example.test", false, false)
		w := performMFARequest(t, r, http.MethodPost, "/mfa/setup", map[string]string{"current_password": "current-password"}, token)
		require.Equal(t, 200, w.Code)
		code, err := auth.GenerateTOTPCode(decode(w.Body.Bytes())["secret"].(string), time.Now())
		require.NoError(t, err)
		body := map[string]string{"current_password": "current-password", "otp_code": code}
		require.Equal(t, 200, performMFARequest(t, r, http.MethodPost, "/mfa/verify", body, token).Code)
		require.Equal(t, 401, performMFARequest(t, r, http.MethodDelete, "/mfa", body, token).Code)
	})
	t.Run("passwordless provider accounts fail closed", func(t *testing.T) {
		_, token := newUser("provider@example.test", true, true)
		w := performMFARequest(t, r, http.MethodDelete, "/mfa", map[string]string{"current_password": "anything", "backup_code": "12345678"}, token)
		require.Equal(t, 409, w.Code)
		require.NotContains(t, w.Body.String(), "12345678")
	})
	t.Run("persistence failure is not a successful setting change", func(t *testing.T) {
		_, token := newUser("db-failure@example.test", true, false)
		broken := handler.NewAuthEnterpriseHandler(failingMFAManagementRepository{ChannelAuthEnterpriseRepository: repo}, users, accounts, nil, nil, nil, nil, cfg)
		rr := gin.New()
		rr.DELETE("/mfa", middleware.AuthMiddleware(cfg.JWTSecret, users), broken.DisableMFA)
		w := performMFARequest(t, rr, http.MethodDelete, "/mfa", map[string]string{"current_password": "current-password", "backup_code": "12345678"}, token)
		require.Equal(t, 500, w.Code)
		require.NotContains(t, decode(w.Body.Bytes()), "enabled")
	})
}

type failingMFAManagementRepository struct {
	repository.ChannelAuthEnterpriseRepository
}

func (failingMFAManagementRepository) ManageMFA(uint, string, string, string) (*domain.MFAProfile, []string, error) {
	return nil, nil, errors.New("storage unavailable")
}
