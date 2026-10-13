package handler_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/handler"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

func setupPasswordReset(t *testing.T) (*gin.Engine, *gorm.DB, *domain.User, *service.EmailService, *service.MockEmailSender, *handler.AuthEnterpriseHandler) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("SMTP_ADDRESS", "")
	t.Setenv("FRONTEND_URL", "https://support.example.test")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&domain.User{}, &domain.PasswordResetToken{}, &domain.MFAProfile{}, &domain.UserSession{}, &domain.Account{}, &domain.AccountUser{}, &domain.EmailLog{}))
	cfg := &config.Config{Environment: "test", JWTSecret: "password-reset-test-secret", JWTExpirationHours: 24}
	users := repository.NewUserRepository(db)
	accounts := repository.NewAccountRepository(db)
	iam := repository.NewChannelAuthEnterpriseRepository(db)
	h := handler.NewAuthEnterpriseHandler(iam, users, accounts, nil, nil, nil, nil, cfg)
	emails := service.NewEmailService(db)
	mock := service.NewMockEmailSender()
	emails.SetSender(mock)
	h.SetEmailService(emails)
	login := handler.NewAuthHandler(cfg, users, accounts)
	login.SetEnterpriseRepo(iam)
	r := gin.New()
	r.POST("/auth/password", h.RequestPasswordReset)
	r.PUT("/auth/password", h.ResetPassword)
	r.POST("/auth/sign_in", login.SignIn)
	r.POST("/auth/mfa/verify", login.VerifyMFAForLogin)
	r.GET("/auth/validate_token", middleware.AuthMiddleware(cfg.JWTSecret, users), login.ValidateToken)
	hash, err := auth.HashPassword("old-password")
	require.NoError(t, err)
	user := &domain.User{Name: "Reset User", Email: "reset@example.test", PasswordHash: hash}
	require.NoError(t, users.Create(user))
	return r, db, user, emails, mock, h
}

func resetTokenFromEmail(t *testing.T, email service.SentEmail) string {
	t.Helper()
	for _, word := range strings.Fields(email.TextBody) {
		link, err := url.Parse(word)
		if err == nil && link.Scheme == "https" && link.Query().Get("reset_password_token") != "" {
			require.Equal(t, "support.example.test", link.Host)
			require.Equal(t, "/app/auth/password/edit", link.Path)
			return link.Query().Get("reset_password_token")
		}
	}
	t.Fatal("reset email did not contain a reset link")
	return ""
}

func TestPasswordResetEmailToNewPasswordBusinessLoop(t *testing.T) {
	r, db, user, _, mock, _ := setupPasswordReset(t)
	oldLogin := performMFARequest(t, r, http.MethodPost, "/auth/sign_in", map[string]string{"email": user.Email, "password": "old-password"}, "")
	require.Equal(t, 200, oldLogin.Code)
	oldAccess := decodeMFATestResponse(t, oldLogin).Data.Token
	require.NotEmpty(t, oldAccess)
	request := performMFARequest(t, r, http.MethodPost, "/auth/password", map[string]string{"email": user.Email}, "")
	unknown := performMFARequest(t, r, http.MethodPost, "/auth/password", map[string]string{"email": "absent@example.test"}, "")
	require.Equal(t, 200, request.Code)
	require.Equal(t, request.Body.String(), unknown.Body.String())
	require.NotContains(t, request.Body.String(), "reset_token")
	require.NotContains(t, request.Body.String(), "sent")
	mail := mock.GetSentEmails()
	require.Len(t, mail, 1)
	require.Equal(t, user.Email, mail[0].To)
	raw := resetTokenFromEmail(t, mail[0])
	require.Len(t, raw, 64)
	require.NotContains(t, request.Body.String(), raw)
	var record domain.PasswordResetToken
	require.NoError(t, db.First(&record).Error)
	digest, err := repository.PasswordResetTokenDigest(raw)
	require.NoError(t, err)
	require.Equal(t, digest, record.Token)
	require.NotEqual(t, raw, record.Token)
	require.NotNil(t, record.IssuedAt)
	require.WithinDuration(t, time.Now().Add(time.Hour), record.ExpiresAt, time.Minute)
	var auditCount int64
	require.NoError(t, db.Model(&domain.EmailLog{}).Count(&auditCount).Error)
	require.Zero(t, auditCount)
	body := map[string]string{"reset_password_token": raw, "password": "new-password", "password_confirmation": "new-password"}
	reset := performMFARequest(t, r, http.MethodPut, "/auth/password", body, "")
	require.Equal(t, 200, reset.Code, reset.Body.String())
	require.Equal(t, 400, performMFARequest(t, r, http.MethodPut, "/auth/password", body, "").Code)
	require.Equal(t, 401, performMFARequest(t, r, http.MethodPost, "/auth/sign_in", map[string]string{"email": user.Email, "password": "old-password"}, "").Code)
	newLogin := performMFARequest(t, r, http.MethodPost, "/auth/sign_in", map[string]string{"email": user.Email, "password": "new-password"}, "")
	require.Equal(t, 200, newLogin.Code)
	require.NotEmpty(t, decodeMFATestResponse(t, newLogin).Data.Token)
	// A completed reset revokes access tokens issued under the old password.
	require.Equal(t, 401, performMFARequest(t, r, http.MethodGet, "/auth/validate_token", nil, oldAccess).Code)
	require.Equal(t, 200, performMFARequest(t, r, http.MethodGet, "/auth/validate_token", nil, decodeMFATestResponse(t, newLogin).Data.Token).Code)
}

func TestPasswordResetKeepsMFARequired(t *testing.T) {
	r, db, user, _, mock, _ := setupPasswordReset(t)
	profile := &domain.MFAProfile{UserID: user.ID, Secret: "JBSWY3DPEHPK3PXP", Enabled: true, BackupCodes: `["12345678"]`}
	require.NoError(t, db.Create(profile).Error)
	require.Equal(t, 200, performMFARequest(t, r, http.MethodPost, "/auth/password", map[string]string{"email": user.Email}, "").Code)
	raw := resetTokenFromEmail(t, mock.GetSentEmails()[0])
	require.Equal(t, 200, performMFARequest(t, r, http.MethodPut, "/auth/password", map[string]string{"token": raw, "password": "new-password"}, "").Code)
	login := performMFARequest(t, r, http.MethodPost, "/auth/sign_in", map[string]string{"email": user.Email, "password": "new-password"}, "")
	require.Equal(t, http.StatusPartialContent, login.Code)
	challenge := decodeMFATestResponse(t, login)
	require.True(t, challenge.MFARequired)
	require.Empty(t, challenge.Data.Token)
	require.NotEmpty(t, challenge.MFAToken)
	require.NoError(t, db.First(profile, profile.ID).Error)
	require.True(t, profile.Enabled)
	require.Equal(t, `["12345678"]`, profile.BackupCodes)
	verified := performMFARequest(t, r, http.MethodPost, "/auth/mfa/verify", map[string]string{"mfa_token": challenge.MFAToken, "backup_code": "12345678"}, "")
	require.Equal(t, 200, verified.Code)
}

func TestPasswordResetGenericResponsesAndFailClosedDelivery(t *testing.T) {
	for _, scenario := range []string{"mail-failed", "activation-failed", "storage-failed", "passwordless", "missing-sender", "missing-frontend", "unsafe-frontend"} {
		t.Run(scenario, func(t *testing.T) {
			r, db, user, emails, mock, _ := setupPasswordReset(t)
			switch scenario {
			case "mail-failed":
				mock.SetFailError(errors.New("local simulated SMTP failure"))
			case "activation-failed":
				require.NoError(t, db.Exec("CREATE TRIGGER activation_fail BEFORE UPDATE OF issued_at ON password_reset_tokens BEGIN SELECT RAISE(ABORT, 'injected activation failure'); END").Error)
			case "storage-failed":
				require.NoError(t, db.Exec("CREATE TRIGGER creation_fail BEFORE INSERT ON password_reset_tokens BEGIN SELECT RAISE(ABORT, 'injected creation failure'); END").Error)
			case "passwordless":
				require.NoError(t, db.Model(user).Update("password_hash", "").Error)
			case "missing-sender":
				emails.SetSender(nil)
			case "missing-frontend":
				t.Setenv("FRONTEND_URL", "")
			case "unsafe-frontend":
				t.Setenv("FRONTEND_URL", "http://insecure.example.test")
			}
			known := performMFARequest(t, r, http.MethodPost, "/auth/password", map[string]string{"email": user.Email}, "")
			unknown := performMFARequest(t, r, http.MethodPost, "/auth/password", map[string]string{"email": "absent@example.test"}, "")
			require.Equal(t, known.Code, unknown.Code)
			require.Equal(t, known.Body.String(), unknown.Body.String())
			require.NotContains(t, known.Body.String(), "sent")
			if strings.HasPrefix(scenario, "missing-") || scenario == "unsafe-frontend" {
				require.Equal(t, 503, known.Code)
			} else {
				require.Equal(t, 200, known.Code)
			}
			if scenario == "activation-failed" {
				mail := mock.GetSentEmails()
				require.Len(t, mail, 1)
				raw := resetTokenFromEmail(t, mail[0])
				require.Equal(t, 400, performMFARequest(t, r, http.MethodPut, "/auth/password", map[string]string{"token": raw, "password": "new-password"}, "").Code)
			} else {
				require.Empty(t, mock.GetSentEmails())
			}
			var active int64
			require.NoError(t, db.Model(&domain.PasswordResetToken{}).Where("issued_at IS NOT NULL").Count(&active).Error)
			require.Zero(t, active)
		})
	}
}

type observingResetSender struct {
	check func(string) error
}

func (s *observingResetSender) Send(_ context.Context, _, _, _, _, text string) error {
	return s.check(text)
}

func TestPasswordResetTokenCannotBeUsedDuringFailedSend(t *testing.T) {
	r, db, user, emails, _, _ := setupPasswordReset(t)
	var raw string
	emails.SetSender(&observingResetSender{check: func(text string) error {
		raw = resetTokenFromEmail(t, service.SentEmail{TextBody: text})
		// Even a provider that sees the link before returning an error cannot
		// race the request and use a token before delivery is acknowledged.
		require.Equal(t, 400, performMFARequest(t, r, http.MethodPut, "/auth/password", map[string]string{"token": raw, "password": "new-password"}, "").Code)
		return errors.New("provider rejected email")
	}})
	require.Equal(t, 200, performMFARequest(t, r, http.MethodPost, "/auth/password", map[string]string{"email": user.Email}, "").Code)
	require.NotEmpty(t, raw)
	require.Equal(t, 400, performMFARequest(t, r, http.MethodPut, "/auth/password", map[string]string{"token": raw, "password": "new-password"}, "").Code)
	var got domain.User
	require.NoError(t, db.First(&got, user.ID).Error)
	require.Equal(t, user.PasswordHash, got.PasswordHash)
}

func TestPasswordResetPolicyAndDatabaseErrorResponses(t *testing.T) {
	r, db, user, _, mock, _ := setupPasswordReset(t)
	require.Equal(t, 200, performMFARequest(t, r, http.MethodPost, "/auth/password", map[string]string{"email": user.Email}, "").Code)
	raw := resetTokenFromEmail(t, mock.GetSentEmails()[0])
	for _, body := range []map[string]string{
		{"token": raw, "password": "short"},
		{"token": raw, "password": strings.Repeat("a", 73)},
		{"token": raw, "password": "new-password", "password_confirmation": "different"},
		{"token": raw, "reset_password_token": strings.Repeat("0", 64), "password": "new-password"},
		{"token": "invalid", "password": "new-password"},
	} {
		require.Equal(t, 400, performMFARequest(t, r, http.MethodPut, "/auth/password", body, "").Code)
	}
	require.NoError(t, db.Exec("CREATE TRIGGER password_fail BEFORE UPDATE OF password_hash ON users WHEN NEW.password_hash <> OLD.password_hash BEGIN SELECT RAISE(ABORT, 'injected password failure'); END").Error)
	body := map[string]string{"token": raw, "password": "new-password"}
	failed := performMFARequest(t, r, http.MethodPut, "/auth/password", body, "")
	require.Equal(t, 500, failed.Code)
	require.NotContains(t, failed.Body.String(), "injected")
	require.NotContains(t, failed.Body.String(), raw)
	require.NoError(t, db.Exec("DROP TRIGGER password_fail").Error)
	require.Equal(t, 200, performMFARequest(t, r, http.MethodPut, "/auth/password", body, "").Code)
}
