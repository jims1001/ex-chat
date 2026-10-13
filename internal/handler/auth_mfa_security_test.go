package handler_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/handler"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestMFALoginTokenPurposeSecurity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		DBDriver: "sqlite", DBPath: ":memory:",
		JWTSecret: "mfa-login-regression-test-secret", JWTExpirationHours: 24,
	}
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	userRepo := repository.NewUserRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	enterpriseRepo := repository.NewChannelAuthEnterpriseRepository(db)
	authHandler := handler.NewAuthHandler(cfg, userRepo, accountRepo)
	authHandler.SetEnterpriseRepo(enterpriseRepo)
	router := gin.New()
	router.POST("/auth/sign_in", authHandler.SignIn)
	router.POST("/auth/mfa/verify", authHandler.VerifyMFAForLogin)
	router.GET("/auth/validate_token", middleware.AuthMiddleware(cfg.JWTSecret, userRepo), authHandler.ValidateToken)
	router.GET("/ws", ws.ServeWS(nil, cfg, userRepo, accountRepo, nil, nil, nil))

	hash, err := auth.HashPassword("test-password")
	require.NoError(t, err)
	user := &domain.User{Name: "MFA Test", Email: "mfa@example.test", PasswordHash: hash, Role: domain.RoleAdministrator}
	require.NoError(t, userRepo.Create(user))
	login := map[string]string{"email": user.Email, "password": "test-password"}

	// Ordinary password login still returns a usable access token.
	ordinary := performMFARequest(t, router, http.MethodPost, "/auth/sign_in", login, "")
	require.Equal(t, http.StatusOK, ordinary.Code)
	ordinaryToken := decodeMFATestResponse(t, ordinary).Data.Token
	require.NotEmpty(t, ordinaryToken)
	_, err = auth.ValidateToken(ordinaryToken, cfg.JWTSecret)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, performMFARequest(t, router, http.MethodGet, "/auth/validate_token", nil, ordinaryToken).Code)

	profile := &domain.MFAProfile{
		UserID: user.ID, Secret: "JBSWY3DPEHPK3PXP", Enabled: true,
		BackupCodes: `["12345678"]`,
	}
	require.NoError(t, enterpriseRepo.SaveMFAProfile(profile))
	challengeResponse := performMFARequest(t, router, http.MethodPost, "/auth/sign_in", login, "")
	require.Equal(t, http.StatusPartialContent, challengeResponse.Code)
	challenge := decodeMFATestResponse(t, challengeResponse)
	require.True(t, challenge.MFARequired)
	require.NotEmpty(t, challenge.MFAToken)
	require.Equal(t, challenge.MFAToken, challenge.Data.MFAToken)
	require.Empty(t, challenge.Data.Token)
	claims, err := auth.ValidateMFAToken(challenge.MFAToken, cfg.JWTSecret)
	require.NoError(t, err)
	require.LessOrEqual(t, claims.ExpiresAt.Sub(claims.IssuedAt.Time), 5*time.Minute)

	t.Run("challenge cannot authenticate HTTP or agent WebSocket", func(t *testing.T) {
		require.Equal(t, http.StatusUnauthorized, performMFARequest(t, router, http.MethodGet, "/auth/validate_token", nil, challenge.MFAToken).Code)
		require.Equal(t, http.StatusUnauthorized, performMFARequest(t, router, http.MethodGet, "/ws?token="+challenge.MFAToken, nil, "").Code)
	})

	t.Run("access token cannot become an MFA challenge", func(t *testing.T) {
		code, err := auth.GenerateTOTPCode(profile.Secret, time.Now())
		require.NoError(t, err)
		result := performMFARequest(t, router, http.MethodPost, "/auth/mfa/verify", map[string]string{
			"mfa_token": ordinaryToken, "otp_code": code,
		}, "")
		require.Equal(t, http.StatusUnauthorized, result.Code)
		require.Empty(t, decodeMFATestResponse(t, result).Data.Token)
	})

	t.Run("challenge needs a valid second factor", func(t *testing.T) {
		for _, code := range []string{"", "invalid"} {
			result := performMFARequest(t, router, http.MethodPost, "/auth/mfa/verify", map[string]string{
				"mfa_token": challenge.MFAToken, "otp_code": code,
			}, "")
			require.Equal(t, http.StatusUnauthorized, result.Code)
			require.Empty(t, decodeMFATestResponse(t, result).Data.Token)
		}
	})

	var usedTOTP string
	t.Run("valid TOTP completes MFA and returns an access token", func(t *testing.T) {
		code, err := auth.GenerateTOTPCode(profile.Secret, time.Now())
		require.NoError(t, err)
		usedTOTP = code
		result := performMFARequest(t, router, http.MethodPost, "/auth/mfa/verify", map[string]string{
			"mfa_token": challenge.MFAToken, "otp_code": code,
		}, "")
		require.Equal(t, http.StatusOK, result.Code)
		token := decodeMFATestResponse(t, result).Data.Token
		_, err = auth.ValidateToken(token, cfg.JWTSecret)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, performMFARequest(t, router, http.MethodGet, "/auth/validate_token", nil, token).Code)
	})

	t.Run("inline login rejects already used TOTP", func(t *testing.T) {
		code := usedTOTP
		require.NotEmpty(t, code)
		result := performMFARequest(t, router, http.MethodPost, "/auth/sign_in", map[string]string{
			"email": user.Email, "password": "test-password", "mfa_otp": code,
		}, "")
		require.Equal(t, http.StatusUnauthorized, result.Code)
		require.Empty(t, decodeMFATestResponse(t, result).Data.Token)
	})

	newProtectedUser := func(email string) *domain.User {
		t.Helper()
		u := &domain.User{Name: "MFA Test", Email: email, PasswordHash: hash, Role: domain.RoleAgent}
		require.NoError(t, userRepo.Create(u))
		require.NoError(t, enterpriseRepo.SaveMFAProfile(&domain.MFAProfile{UserID: u.ID, Secret: profile.Secret, Enabled: true, BackupCodes: `["12345678","87654321"]`}))
		return u
	}

	t.Run("inline TOTP succeeds for an unused timestep", func(t *testing.T) {
		u := newProtectedUser("inline@example.test")
		code, err := auth.GenerateTOTPCode(profile.Secret, time.Now())
		require.NoError(t, err)
		result := performMFARequest(t, router, http.MethodPost, "/auth/sign_in", map[string]string{"email": u.Email, "password": "test-password", "mfa_otp": code}, "")
		require.Equal(t, http.StatusOK, result.Code)
		_, err = auth.ValidateToken(decodeMFATestResponse(t, result).Data.Token, cfg.JWTSecret)
		require.NoError(t, err)
	})

	t.Run("challenge and backup codes complete MFA only once", func(t *testing.T) {
		u := newProtectedUser("backup@example.test")
		challenge, err := auth.GenerateMFAToken(u, cfg.JWTSecret)
		require.NoError(t, err)
		body := map[string]string{"mfa_token": challenge, "backup_code": "12345678"}
		result := performMFARequest(t, router, http.MethodPost, "/auth/mfa/verify", body, "")
		require.Equal(t, http.StatusOK, result.Code)
		_, err = auth.ValidateToken(decodeMFATestResponse(t, result).Data.Token, cfg.JWTSecret)
		require.NoError(t, err)
		body["backup_code"] = "87654321"
		require.Equal(t, http.StatusUnauthorized, performMFARequest(t, router, http.MethodPost, "/auth/mfa/verify", body, "").Code)
		fresh, err := auth.GenerateMFAToken(u, cfg.JWTSecret)
		require.NoError(t, err)
		body["mfa_token"] = fresh
		body["backup_code"] = "12345678"
		require.Equal(t, http.StatusUnauthorized, performMFARequest(t, router, http.MethodPost, "/auth/mfa/verify", body, "").Code)
		body["backup_code"] = "87654321"
		require.Equal(t, http.StatusOK, performMFARequest(t, router, http.MethodPost, "/auth/mfa/verify", body, "").Code)
	})

	t.Run("MFA attempt limit spans new challenges and inline logins", func(t *testing.T) {
		u := newProtectedUser("limited@example.test")
		for i := 0; i < 5; i++ {
			challenge, err := auth.GenerateMFAToken(u, cfg.JWTSecret)
			require.NoError(t, err)
			result := performMFARequest(t, router, http.MethodPost, "/auth/mfa/verify", map[string]string{"mfa_token": challenge, "otp_code": "invalid"}, "")
			require.Equal(t, http.StatusUnauthorized, result.Code)
		}
		result := performMFARequest(t, router, http.MethodPost, "/auth/sign_in", map[string]string{"email": u.Email, "password": "test-password", "backup_code": "12345678"}, "")
		require.Equal(t, http.StatusTooManyRequests, result.Code)
		require.Equal(t, "300", result.Header().Get("Retry-After"))
		require.Empty(t, decodeMFATestResponse(t, result).Data.Token)
	})

	t.Run("MFA lookup failure cannot bypass the challenge", func(t *testing.T) {
		authHandler.SetEnterpriseRepo(failingMFAProfileRepository{ChannelIAMRepository: enterpriseRepo})
		defer authHandler.SetEnterpriseRepo(enterpriseRepo)
		result := performMFARequest(t, router, http.MethodPost, "/auth/sign_in", login, "")
		require.Equal(t, http.StatusInternalServerError, result.Code)
		require.Empty(t, decodeMFATestResponse(t, result).Data.Token)
	})

	t.Run("backup code persistence failure cannot create a session", func(t *testing.T) {
		authHandler.SetEnterpriseRepo(failingMFASaveRepository{ChannelIAMRepository: enterpriseRepo})
		defer authHandler.SetEnterpriseRepo(enterpriseRepo)
		for _, request := range []struct {
			path string
			body map[string]string
		}{
			{"/auth/sign_in", map[string]string{"email": user.Email, "password": "test-password", "backup_code": "87654321"}},
			{"/auth/mfa/verify", map[string]string{"mfa_token": challenge.MFAToken, "backup_code": "87654321"}},
		} {
			result := performMFARequest(t, router, http.MethodPost, request.path, request.body, "")
			require.Equal(t, http.StatusInternalServerError, result.Code)
			require.Empty(t, decodeMFATestResponse(t, result).Data.Token)
		}
	})
}

type failingMFAProfileRepository struct {
	repository.ChannelIAMRepository
}

func (failingMFAProfileRepository) GetMFAProfile(uint) (*domain.MFAProfile, error) {
	return nil, errors.New("MFA profile store unavailable")
}

type failingMFASaveRepository struct {
	repository.ChannelIAMRepository
}

func (failingMFASaveRepository) VerifyMFALogin(uint, string, string, time.Time) error {
	return errors.New("MFA profile store unavailable")
}

type mfaTestResponse struct {
	MFARequired bool   `json:"mfa_required"`
	MFAToken    string `json:"mfa_token"`
	Data        struct {
		Token    string `json:"token"`
		MFAToken string `json:"mfa_token"`
	} `json:"data"`
}

func decodeMFATestResponse(t *testing.T, result *httptest.ResponseRecorder) mfaTestResponse {
	t.Helper()
	var response mfaTestResponse
	require.NoError(t, json.Unmarshal(result.Body.Bytes(), &response))
	return response
}

func performMFARequest(t *testing.T, router http.Handler, method, path string, body any, token string) *httptest.ResponseRecorder {
	t.Helper()
	var payload bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&payload).Encode(body))
	}
	request := httptest.NewRequest(method, path, &payload)
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	result := httptest.NewRecorder()
	router.ServeHTTP(result, request)
	return result
}
