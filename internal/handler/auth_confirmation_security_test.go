package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type confirmationHTTPFixture struct {
	router   *gin.Engine
	db       *gorm.DB
	users    *repository.UserRepository
	accounts *repository.AccountRepository
	iam      repository.ChannelIAMRepository
	h        *handler.AuthHandler
	emails   *service.EmailService
	mail     *service.MockEmailSender
	cfg      *config.Config
	logs     bytes.Buffer
}

func setupConfirmationHTTP(t *testing.T) *confirmationHTTPFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("SMTP_ADDRESS", "")
	t.Setenv("FRONTEND_URL", "https://support.example.test")
	t.Setenv("APP_URL", "")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&domain.User{}, &domain.Account{}, &domain.AccountUser{}, &domain.MFAProfile{}, &domain.UserSession{}, &domain.PasswordResetToken{}, &domain.EmailLog{}))
	f := &confirmationHTTPFixture{db: db, users: repository.NewUserRepository(db), accounts: repository.NewAccountRepository(db), cfg: &config.Config{Environment: "test", JWTSecret: "confirmation-http-test-secret", JWTExpirationHours: 24}}
	iam := repository.NewChannelAuthEnterpriseRepository(db)
	f.iam = iam
	f.h = handler.NewAuthHandler(f.cfg, f.users, f.accounts)
	f.h.SetEnterpriseRepo(iam)
	f.emails = service.NewEmailService(db)
	f.mail = service.NewMockEmailSender()
	f.emails.SetSender(f.mail)
	f.h.SetEmailService(f.emails)
	reset := handler.NewAuthEnterpriseHandler(iam, f.users, f.accounts, nil, nil, nil, nil, f.cfg)
	reset.SetEmailService(f.emails)
	logger.SetOutput(&f.logs, slog.LevelDebug, "json")
	t.Cleanup(func() { logger.SetOutput(os.Stdout, slog.LevelInfo, "json") })
	r := gin.New()
	r.Use(middleware.RequestLogger())
	r.POST("/auth", f.h.SignUp)
	r.GET("/auth/confirmation", f.h.ConfirmEmail)
	r.POST("/auth/confirmation", f.h.ConfirmEmail)
	r.POST("/auth/confirmation/resend", f.h.ResendConfirmation)
	r.POST("/auth/confirmation/resend-own", middleware.AuthMiddleware(f.cfg.JWTSecret, f.users), f.h.ResendConfirmation)
	r.POST("/auth/sign_in", f.h.SignIn)
	r.POST("/auth/mfa/verify", f.h.VerifyMFAForLogin)
	r.GET("/auth/validate_token", middleware.AuthMiddleware(f.cfg.JWTSecret, f.users), f.h.ValidateToken)
	r.POST("/auth/password", reset.RequestPasswordReset)
	r.PUT("/auth/password", reset.ResetPassword)
	f.router = r
	return f
}

func (f *confirmationHTTPFixture) user(t *testing.T, email string) *domain.User {
	t.Helper()
	hash, err := auth.HashPassword("OriginalPassword123!")
	require.NoError(t, err)
	u := &domain.User{Name: "Confirmation User", Email: email, PasswordHash: hash, Role: domain.RoleAgent}
	require.NoError(t, f.users.Create(u))
	return u
}

func confirmationTokenFromEmail(t *testing.T, mail service.SentEmail) string {
	t.Helper()
	for _, word := range strings.Fields(mail.TextBody) {
		u, err := url.Parse(word)
		if err == nil && u.Scheme == "https" && u.Query().Get("confirmation_token") != "" {
			require.Equal(t, "support.example.test", u.Host)
			require.Equal(t, "/auth/confirmation", u.Path)
			raw := u.Query().Get("confirmation_token")
			require.Len(t, raw, 64)
			return raw
		}
	}
	t.Fatal("confirmation email has no configured confirmation link")
	return ""
}

func (f *confirmationHTTPFixture) issue(t *testing.T, u *domain.User) string {
	t.Helper()
	before := len(f.mail.GetSentEmails())
	res := performMFARequest(t, f.router, http.MethodPost, "/auth/confirmation/resend", map[string]string{"email": u.Email}, "")
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	mail := f.mail.GetSentEmails()
	require.Len(t, mail, before+1)
	require.Equal(t, u.Email, mail[len(mail)-1].To)
	return confirmationTokenFromEmail(t, mail[len(mail)-1])
}

func (f *confirmationHTTPFixture) confirm(t *testing.T, raw string) *httptest.ResponseRecorder {
	t.Helper()
	return performMFARequest(t, f.router, http.MethodPost, "/auth/confirmation", map[string]string{"confirmation_token": raw}, "")
}

func confirmationStatus(t *testing.T, res *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope struct {
		Data struct {
			Status string `json:"confirmation_status"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &envelope))
	return envelope.Data.Status
}

func (f *confirmationHTTPFixture) assertNoBearerPersistence(t *testing.T, raw string) {
	t.Helper()
	require.NotContains(t, f.logs.String(), raw)
	for _, table := range []string{"users", "accounts", "account_users", "user_sessions", "email_logs", "password_reset_tokens"} {
		var rows []map[string]any
		require.NoError(t, f.db.Table(table).Find(&rows).Error)
		serialized, err := json.Marshal(rows)
		require.NoError(t, err)
		require.NotContains(t, string(serialized), raw, "bearer persisted in %s", table)
	}
	var count int64
	require.NoError(t, f.db.Model(&domain.EmailLog{}).Count(&count).Error)
	require.Zero(t, count, "confirmation mail must never enter the generic audit/retry queue")
}

func TestConfirmationHTTPSignupPreservesWorkspaceAndAccess(t *testing.T) {
	for _, scenario := range []string{"captured_mock", "no-email-service", "no-sender", "bad-frontend", "delivery-error", "no-iam"} {
		t.Run(scenario, func(t *testing.T) {
			f := setupConfirmationHTTP(t)
			status := "unavailable"
			switch scenario {
			case "captured_mock":
				status = "captured_mock"
			case "no-email-service":
				f.h.SetEmailService(nil)
			case "no-sender":
				f.emails.SetSender(nil)
			case "bad-frontend":
				t.Setenv("FRONTEND_URL", "http://unsafe.example.test")
			case "delivery-error":
				f.mail.SetFailError(errors.New("provider could have accepted message"))
				status = "delivery_unknown"
			case "no-iam":
				f.h.SetEnterpriseRepo(nil)
			}
			res := performMFARequest(t, f.router, http.MethodPost, "/auth", map[string]string{"name": "New Admin", "email": "New.Admin@example.test", "password": "OriginalPassword123!", "account_name": "My Workspace"}, "")
			require.Equal(t, http.StatusCreated, res.Code, res.Body.String())
			require.Equal(t, status, confirmationStatus(t, res))
			access := decodeMFATestResponse(t, res).Data.Token
			require.NotEmpty(t, access)
			claims, err := auth.ValidateToken(access, f.cfg.JWTSecret)
			require.NoError(t, err)
			require.Equal(t, domain.RoleAdministrator, claims.Role)
			require.Equal(t, "new.admin@example.test", claims.Email)
			require.Equal(t, 200, performMFARequest(t, f.router, http.MethodGet, "/auth/validate_token", nil, access).Code)
			u, err := f.users.FindByID(claims.UserID)
			require.NoError(t, err)
			require.Nil(t, u.ConfirmedAt)
			accounts, err := f.accounts.ListAccountsForUser(u.ID)
			require.NoError(t, err)
			require.Len(t, accounts, 1)
			require.Equal(t, "My Workspace", accounts[0].Name)
			member, err := f.accounts.GetMembership(accounts[0].ID, u.ID)
			require.NoError(t, err)
			require.Equal(t, domain.RoleAdministrator, member.Role)
			if scenario == "captured_mock" {
				mail := f.mail.GetSentEmails()
				require.Len(t, mail, 1)
				raw := confirmationTokenFromEmail(t, mail[0])
				digest, err := repository.ConfirmationTokenDigest(raw)
				require.NoError(t, err)
				require.Equal(t, digest, u.ConfirmationToken)
				require.Equal(t, u.Email, u.ConfirmationEmail)
				require.Equal(t, u.AuthVersion, u.ConfirmationAuthVersion)
				require.NotNil(t, u.ConfirmationIssuedAt)
				require.NotNil(t, u.ConfirmationExpiresAt)
				require.WithinDuration(t, time.Now().Add(24*time.Hour), *u.ConfirmationExpiresAt, time.Minute)
				require.NotContains(t, res.Body.String(), raw)
				require.NotContains(t, res.Body.String(), digest)
				require.NotContains(t, res.Body.String(), "confirmation_token")
				f.assertNoBearerPersistence(t, raw)
			} else {
				require.Empty(t, f.mail.GetSentEmails())
				require.Nil(t, u.ConfirmationIssuedAt)
			}
		})
	}
}

func TestConfirmationHTTPRoundTripAndReplay(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		t.Run(method, func(t *testing.T) {
			f := setupConfirmationHTTP(t)
			u := f.user(t, "confirmed@example.test")
			raw := f.issue(t, u)
			var res *httptest.ResponseRecorder
			if method == http.MethodGet {
				res = performMFARequest(t, f.router, method, "/auth/confirmation?confirmation_token="+raw, nil, "")
			} else {
				res = f.confirm(t, raw)
			}
			require.Equal(t, http.StatusOK, res.Code, res.Body.String())
			require.Equal(t, "no-store", res.Header().Get("Cache-Control"))
			require.Equal(t, "no-referrer", res.Header().Get("Referrer-Policy"))
			access := decodeMFATestResponse(t, res).Data.Token
			require.NotEmpty(t, access)
			require.Equal(t, 200, performMFARequest(t, f.router, http.MethodGet, "/auth/validate_token", nil, access).Code)
			current, err := f.users.FindByID(u.ID)
			require.NoError(t, err)
			require.NotNil(t, current.ConfirmedAt)
			require.Empty(t, current.ConfirmationToken)
			require.Nil(t, current.ConfirmationIssuedAt)
			require.Nil(t, current.ConfirmationExpiresAt)
			require.Equal(t, u.PasswordHash, current.PasswordHash)
			require.Equal(t, u.AuthVersion, current.AuthVersion)
			require.Equal(t, http.StatusUnprocessableEntity, f.confirm(t, raw).Code)
			require.NotContains(t, res.Body.String(), raw)
			f.assertNoBearerPersistence(t, raw)
		})
	}
}

func TestConfirmationHTTPAnonymousResendDoesNotEnumerate(t *testing.T) {
	f := setupConfirmationHTTP(t)
	u := f.user(t, "eligible@example.test")
	confirmed := f.user(t, "already@example.test")
	require.NoError(t, f.db.Model(confirmed).Update("confirmed_at", time.Now()).Error)
	known := performMFARequest(t, f.router, http.MethodPost, "/auth/confirmation/resend", map[string]string{"email": u.Email}, "")
	require.Equal(t, 200, known.Code)
	require.Contains(t, known.Body.String(), "Confirmation request processed")
	for _, email := range []string{"unknown@example.test", confirmed.Email, "invalid", ""} {
		res := performMFARequest(t, f.router, http.MethodPost, "/auth/confirmation/resend", map[string]string{"email": email}, "")
		require.Equal(t, known.Code, res.Code)
		require.Equal(t, known.Body.String(), res.Body.String())
	}
	require.Len(t, f.mail.GetSentEmails(), 1, "unknown, malformed, and already-confirmed accounts must not dispatch mail")
	f.mail.SetFailError(errors.New("provider error"))
	failed := performMFARequest(t, f.router, http.MethodPost, "/auth/confirmation/resend", map[string]string{"email": u.Email}, "")
	require.Equal(t, known.Body.String(), failed.Body.String())
	for _, forbidden := range []string{"sent", "delivered", "delivery", "confirmation_status", "token"} {
		require.NotContains(t, known.Body.String(), forbidden)
	}
	// Failure of the lookup itself must not distinguish an anonymous account.
	require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("test:lookup-error", func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			tx.AddError(errors.New("private database detail"))
		}
	}))
	t.Cleanup(func() { _ = f.db.Callback().Query().Remove("test:lookup-error") })
	unavailable := performMFARequest(t, f.router, http.MethodPost, "/auth/confirmation/resend", map[string]string{"email": u.Email}, "")
	require.Equal(t, known.Body.String(), unavailable.Body.String())
	require.NotContains(t, unavailable.Body.String(), "private database detail")
}

func TestConfirmationHTTPConfigurationFailsBeforeAccountLookup(t *testing.T) {
	for _, missing := range []string{"sender", "service", "iam", "frontend"} {
		t.Run(missing, func(t *testing.T) {
			f := setupConfirmationHTTP(t)
			switch missing {
			case "sender":
				f.emails.SetSender(nil)
			case "service":
				f.h.SetEmailService(nil)
			case "iam":
				f.h.SetEnterpriseRepo(nil)
			case "frontend":
				t.Setenv("FRONTEND_URL", "https://user:password@support.example.test")
			}
			lookups := 0
			require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("test:lookup-count", func(tx *gorm.DB) {
				if tx.Statement.Table == "users" {
					lookups++
				}
			}))
			known := performMFARequest(t, f.router, http.MethodPost, "/auth/confirmation/resend", map[string]string{"email": "known@example.test"}, "")
			unknown := performMFARequest(t, f.router, http.MethodPost, "/auth/confirmation/resend", map[string]string{"email": "unknown@example.test"}, "")
			require.Equal(t, http.StatusServiceUnavailable, known.Code)
			require.Equal(t, known.Code, unknown.Code)
			require.Equal(t, known.Body.String(), unknown.Body.String())
			require.Zero(t, lookups)
			require.Empty(t, f.mail.GetSentEmails())
		})
	}
}

func TestConfirmationHTTPAuthenticatedResendIsSelfOnly(t *testing.T) {
	f := setupConfirmationHTTP(t)
	self := f.user(t, "self@example.test")
	other := f.user(t, "other@example.test")
	access, err := auth.GenerateToken(self, f.cfg.JWTSecret, 24)
	require.NoError(t, err)
	res := performMFARequest(t, f.router, http.MethodPost, "/auth/confirmation/resend-own", map[string]string{"email": other.Email}, access)
	require.Equal(t, 200, res.Code)
	require.Equal(t, "captured_mock", confirmationStatus(t, res))
	mail := f.mail.GetSentEmails()
	require.Len(t, mail, 1)
	require.Equal(t, self.Email, mail[0].To)
	current, err := f.users.FindByID(other.ID)
	require.NoError(t, err)
	require.Empty(t, current.ConfirmationToken)
	require.Equal(t, 200, f.confirm(t, confirmationTokenFromEmail(t, mail[0])).Code)
	res = performMFARequest(t, f.router, http.MethodPost, "/auth/confirmation/resend-own", nil, access)
	require.Equal(t, "already_confirmed", confirmationStatus(t, res))
	require.Len(t, f.mail.GetSentEmails(), 1)
}

func TestConfirmationHTTPStaleExpiredAndMalformedProofsFailClosed(t *testing.T) {
	for _, scenario := range []string{"expired", "email-changed", "auth-version-changed", "reissued", "legacy", "uppercase", "malformed", "empty"} {
		t.Run(scenario, func(t *testing.T) {
			f := setupConfirmationHTTP(t)
			u := f.user(t, "stale@example.test")
			raw := f.issue(t, u)
			switch scenario {
			case "expired":
				require.NoError(t, f.db.Model(u).Update("confirmation_expires_at", time.Now().Add(-time.Second)).Error)
			case "email-changed":
				require.NoError(t, f.db.Model(u).Update("email", "new@example.test").Error)
			case "auth-version-changed":
				require.NoError(t, f.db.Model(u).Update("auth_version", gorm.Expr("auth_version + 1")).Error)
			case "reissued":
				newest := f.issue(t, u)
				require.NotEqual(t, raw, newest)
			case "legacy":
				raw = "legacy-plaintext-fixture"
				require.NoError(t, f.db.Model(u).Update("confirmation_token", raw).Error)
			case "uppercase":
				raw = strings.ToUpper(raw)
			case "malformed":
				raw = strings.Repeat("z", 64)
			case "empty":
				raw = ""
			}
			res := f.confirm(t, raw)
			require.Equal(t, http.StatusUnprocessableEntity, res.Code)
			require.Empty(t, decodeMFATestResponse(t, res).Data.Token)
			current, err := f.users.FindByID(u.ID)
			require.NoError(t, err)
			require.Nil(t, current.ConfirmedAt)
		})
	}
}

func TestConfirmationHTTPPasswordResetRevokesExistingProof(t *testing.T) {
	f := setupConfirmationHTTP(t)
	u := f.user(t, "reset-confirmation@example.test")
	raw := f.issue(t, u)
	require.Equal(t, 200, performMFARequest(t, f.router, http.MethodPost, "/auth/password", map[string]string{"email": u.Email}, "").Code)
	mail := f.mail.GetSentEmails()
	require.Len(t, mail, 2)
	resetRaw := resetTokenFromEmail(t, mail[1])
	require.Equal(t, 200, performMFARequest(t, f.router, http.MethodPut, "/auth/password", map[string]string{"token": resetRaw, "password": "ReplacementPassword123!"}, "").Code)
	require.Equal(t, http.StatusUnprocessableEntity, f.confirm(t, raw).Code)
	current, err := f.users.FindByID(u.ID)
	require.NoError(t, err)
	require.Nil(t, current.ConfirmedAt)
	require.Greater(t, current.AuthVersion, u.AuthVersion)
	fresh := f.issue(t, current)
	require.Equal(t, 200, f.confirm(t, fresh).Code)
}

type observingConfirmationSender struct {
	send func(context.Context, service.SentEmail) error
}

func (s *observingConfirmationSender) Send(ctx context.Context, from, to, subject, htmlBody, textBody string) error {
	return s.send(ctx, service.SentEmail{From: from, To: to, Subject: subject, HTMLBody: htmlBody, TextBody: textBody})
}

func TestConfirmationHTTPPendingAndCanceledDeliveryCannotAuthenticate(t *testing.T) {
	for _, outcome := range []string{"success", "provider-error", "canceled-before", "canceled-during"} {
		t.Run(outcome, func(t *testing.T) {
			f := setupConfirmationHTTP(t)
			u := f.user(t, "pending@example.test")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var raw string
			f.emails.SetSender(&observingConfirmationSender{send: func(ctx context.Context, mail service.SentEmail) error {
				raw = confirmationTokenFromEmail(t, mail)
				require.Equal(t, http.StatusUnprocessableEntity, f.confirm(t, raw).Code, "pending proof became usable while sender was still running")
				if outcome == "provider-error" {
					return fmt.Errorf("SMTP echoed private link: %s", mail.TextBody)
				}
				if outcome == "canceled-during" {
					cancel()
				}
				return nil
			}})
			if outcome == "canceled-before" {
				cancel()
			}
			body, err := json.Marshal(map[string]string{"email": u.Email})
			require.NoError(t, err)
			req := httptest.NewRequest(http.MethodPost, "/auth/confirmation/resend", bytes.NewReader(body)).WithContext(ctx)
			req.Header.Set("Content-Type", "application/json")
			res := httptest.NewRecorder()
			f.router.ServeHTTP(res, req)
			require.Equal(t, 200, res.Code)
			if outcome == "canceled-before" {
				require.Empty(t, raw)
				current, err := f.users.FindByID(u.ID)
				require.NoError(t, err)
				require.Empty(t, current.ConfirmationToken)
				return
			}
			require.NotEmpty(t, raw)
			require.NotContains(t, res.Body.String(), raw)
			f.assertNoBearerPersistence(t, raw)
			if outcome == "success" {
				require.Equal(t, 200, f.confirm(t, raw).Code)
			} else {
				require.Equal(t, http.StatusUnprocessableEntity, f.confirm(t, raw).Code)
			}
			calls := 0
			f.emails.SetSender(&observingConfirmationSender{send: func(context.Context, service.SentEmail) error { calls++; return nil }})
			retried, err := f.emails.RetryFailedEmails(context.Background(), 100)
			require.NoError(t, err)
			require.Zero(t, retried)
			require.Zero(t, calls)
		})
	}
}

func TestConfirmationHTTPDeliveryCannotActivateAfterIdentityDrift(t *testing.T) {
	for _, drift := range []string{"email", "credential-version"} {
		t.Run(drift, func(t *testing.T) {
			f := setupConfirmationHTTP(t)
			u := f.user(t, "bound@example.test")
			var raw string
			f.emails.SetSender(&observingConfirmationSender{send: func(_ context.Context, mail service.SentEmail) error {
				require.Equal(t, u.Email, mail.To)
				raw = confirmationTokenFromEmail(t, mail)
				if drift == "email" {
					require.NoError(t, f.db.Model(u).Update("email", "changed@example.test").Error)
				} else {
					require.NoError(t, f.db.Model(u).Update("auth_version", gorm.Expr("auth_version + 1")).Error)
				}
				return nil
			}})
			access, err := auth.GenerateToken(u, f.cfg.JWTSecret, 24)
			require.NoError(t, err)
			res := performMFARequest(t, f.router, http.MethodPost, "/auth/confirmation/resend-own", nil, access)
			require.Equal(t, 200, res.Code)
			require.Equal(t, "inactive", confirmationStatus(t, res))
			require.Equal(t, http.StatusUnprocessableEntity, f.confirm(t, raw).Code)
			current, err := f.users.FindByID(u.ID)
			require.NoError(t, err)
			require.Nil(t, current.ConfirmationIssuedAt)
			require.Nil(t, current.ConfirmedAt)
		})
	}
}

type confirmationGenerateObserver struct {
	repository.ChannelIAMRepository
	after func()
}

func (r confirmationGenerateObserver) GenerateConfirmationToken(id uint) (*domain.User, string, error) {
	u, raw, err := r.ChannelIAMRepository.GenerateConfirmationToken(id)
	if err == nil {
		r.after()
	}
	return u, raw, err
}

func TestConfirmationHTTPSenderUsesIssuedRecipientSnapshot(t *testing.T) {
	f := setupConfirmationHTTP(t)
	u := f.user(t, "original@example.test")
	f.h.SetEnterpriseRepo(confirmationGenerateObserver{ChannelIAMRepository: f.iam, after: func() { require.NoError(t, f.db.Model(u).Update("email", "replacement@example.test").Error) }})
	res := performMFARequest(t, f.router, http.MethodPost, "/auth/confirmation/resend", map[string]string{"email": u.Email}, "")
	require.Equal(t, 200, res.Code)
	mail := f.mail.GetSentEmails()
	require.Len(t, mail, 1)
	require.Equal(t, "original@example.test", mail[0].To, "mail destination must be the issued proof snapshot, never a later account reload")
	require.Equal(t, http.StatusUnprocessableEntity, f.confirm(t, confirmationTokenFromEmail(t, mail[0])).Code)
}

func TestConfirmationHTTPLateDeliveryCannotActivateSupersededAttempt(t *testing.T) {
	f := setupConfirmationHTTP(t)
	u := f.user(t, "resend@example.test")
	var proofs []string
	f.emails.SetSender(&observingConfirmationSender{send: func(_ context.Context, mail service.SentEmail) error {
		proofs = append(proofs, confirmationTokenFromEmail(t, mail))
		if len(proofs) == 1 {
			second := performMFARequest(t, f.router, http.MethodPost, "/auth/confirmation/resend", map[string]string{"email": u.Email}, "")
			require.Equal(t, 200, second.Code)
		}
		return nil
	}})
	access, err := auth.GenerateToken(u, f.cfg.JWTSecret, 24)
	require.NoError(t, err)
	res := performMFARequest(t, f.router, http.MethodPost, "/auth/confirmation/resend-own", nil, access)
	require.Equal(t, "inactive", confirmationStatus(t, res))
	require.Len(t, proofs, 2)
	require.NotEqual(t, proofs[0], proofs[1])
	require.Equal(t, http.StatusUnprocessableEntity, f.confirm(t, proofs[0]).Code)
	require.Equal(t, 200, f.confirm(t, proofs[1]).Code)
	require.Equal(t, http.StatusUnprocessableEntity, f.confirm(t, proofs[1]).Code)
}

func TestConfirmationHTTPStorageFailuresNeverGrantAccess(t *testing.T) {
	for _, phase := range []string{"generation", "activation", "consumption"} {
		t.Run(phase, func(t *testing.T) {
			f := setupConfirmationHTTP(t)
			u := f.user(t, "rollback@example.test")
			column := "confirmation_token"
			when := ""
			if phase == "activation" {
				column = "confirmation_issued_at"
				when = " WHEN NEW.confirmation_issued_at IS NOT NULL"
			}
			if phase == "consumption" {
				column = "confirmed_at"
			}
			if phase != "consumption" {
				require.NoError(t, f.db.Exec("CREATE TRIGGER confirmation_fail BEFORE UPDATE OF "+column+" ON users"+when+" BEGIN SELECT RAISE(ABORT, 'private storage error'); END").Error)
			}
			res := performMFARequest(t, f.router, http.MethodPost, "/auth/confirmation/resend", map[string]string{"email": u.Email}, "")
			require.Equal(t, 200, res.Code)
			require.NotContains(t, res.Body.String(), "private storage error")
			if phase == "generation" {
				require.Empty(t, f.mail.GetSentEmails())
				return
			}
			mail := f.mail.GetSentEmails()
			require.Len(t, mail, 1)
			raw := confirmationTokenFromEmail(t, mail[0])
			if phase == "consumption" {
				require.NoError(t, f.db.Exec("CREATE TRIGGER confirmation_fail BEFORE UPDATE OF confirmed_at ON users BEGIN SELECT RAISE(ABORT, 'private storage error'); END").Error)
			}
			failed := f.confirm(t, raw)
			require.Equal(t, http.StatusUnprocessableEntity, failed.Code)
			require.Empty(t, decodeMFATestResponse(t, failed).Data.Token)
			require.NotContains(t, failed.Body.String(), "private storage error")
			current, err := f.users.FindByID(u.ID)
			require.NoError(t, err)
			require.Nil(t, current.ConfirmedAt)
			if phase == "activation" {
				require.Nil(t, current.ConfirmationIssuedAt)
			} else {
				require.NotNil(t, current.ConfirmationIssuedAt)
			}
			require.NoError(t, f.db.Exec("DROP TRIGGER confirmation_fail").Error)
			if phase == "consumption" {
				require.Equal(t, 200, f.confirm(t, raw).Code)
			} else {
				require.Equal(t, http.StatusUnprocessableEntity, f.confirm(t, raw).Code)
			}
		})
	}
}

func TestConfirmationHTTPRequiresCurrentMFA(t *testing.T) {
	for _, factor := range []string{"otp", "backup"} {
		t.Run(factor, func(t *testing.T) {
			f := setupConfirmationHTTP(t)
			u := f.user(t, "mfa-confirmation@example.test")
			raw := f.issue(t, u)
			// Enable after issue to prove policy is checked at confirmation time.
			profile := &domain.MFAProfile{UserID: u.ID, Secret: "JBSWY3DPEHPK3PXP", Enabled: true, BackupCodes: `["12345678","87654321"]`}
			require.NoError(t, f.iam.SaveMFAProfile(profile))
			res := f.confirm(t, raw)
			require.Equal(t, http.StatusPartialContent, res.Code, res.Body.String())
			require.NotContains(t, res.Body.String(), `"token":`)
			challenge := decodeMFATestResponse(t, res)
			require.True(t, challenge.MFARequired)
			require.Empty(t, challenge.Data.Token)
			require.NotEmpty(t, challenge.MFAToken)
			require.Equal(t, challenge.MFAToken, challenge.Data.MFAToken)
			_, err := auth.ValidateMFAToken(challenge.MFAToken, f.cfg.JWTSecret)
			require.NoError(t, err)
			require.Equal(t, 401, performMFARequest(t, f.router, http.MethodGet, "/auth/validate_token", nil, challenge.MFAToken).Code)
			require.Equal(t, http.StatusUnprocessableEntity, f.confirm(t, raw).Code)
			invalid := performMFARequest(t, f.router, http.MethodPost, "/auth/mfa/verify", map[string]string{"mfa_token": challenge.MFAToken, "otp_code": "invalid"}, "")
			require.Equal(t, 401, invalid.Code)
			require.Empty(t, decodeMFATestResponse(t, invalid).Data.Token)
			body := map[string]string{"mfa_token": challenge.MFAToken}
			if factor == "otp" {
				code, err := auth.GenerateTOTPCode(profile.Secret, time.Now())
				require.NoError(t, err)
				body["otp_code"] = code
			} else {
				body["backup_code"] = "12345678"
			}
			verified := performMFARequest(t, f.router, http.MethodPost, "/auth/mfa/verify", body, "")
			require.Equal(t, 200, verified.Code, verified.Body.String())
			access := decodeMFATestResponse(t, verified).Data.Token
			require.NotEmpty(t, access)
			require.Equal(t, 200, performMFARequest(t, f.router, http.MethodGet, "/auth/validate_token", nil, access).Code)
			require.Equal(t, 401, performMFARequest(t, f.router, http.MethodPost, "/auth/mfa/verify", body, "").Code)
		})
	}
}

func TestConfirmationHTTPMFALookupFailureCannotGrantAccess(t *testing.T) {
	f := setupConfirmationHTTP(t)
	u := f.user(t, "mfa-unavailable@example.test")
	raw := f.issue(t, u)
	f.h.SetEnterpriseRepo(failingMFAProfileRepository{ChannelIAMRepository: f.iam})
	res := f.confirm(t, raw)
	require.Equal(t, http.StatusInternalServerError, res.Code)
	require.NotContains(t, res.Body.String(), `"token":`)
	require.Empty(t, decodeMFATestResponse(t, res).Data.Token)
	require.Empty(t, decodeMFATestResponse(t, res).MFAToken)
	require.NotContains(t, res.Body.String(), "store unavailable")
	current, err := f.users.FindByID(u.ID)
	require.NoError(t, err)
	require.NotNil(t, current.ConfirmedAt)
	require.Equal(t, http.StatusUnprocessableEntity, f.confirm(t, raw).Code)
}

type confirmationConsumeObserver struct {
	repository.ChannelIAMRepository
	after func()
}

func (r confirmationConsumeObserver) ConfirmUserByToken(raw string) (*domain.User, error) {
	u, err := r.ChannelIAMRepository.ConfirmUserByToken(raw)
	if err == nil {
		r.after()
	}
	return u, err
}

func TestConfirmationHTTPDoesNotUpgradeVerifiedCredentialSnapshot(t *testing.T) {
	for _, mfa := range []bool{false, true} {
		t.Run(fmt.Sprintf("mfa=%t", mfa), func(t *testing.T) {
			f := setupConfirmationHTTP(t)
			u := f.user(t, "credential-snapshot@example.test")
			raw := f.issue(t, u)
			if mfa {
				require.NoError(t, f.iam.SaveMFAProfile(&domain.MFAProfile{UserID: u.ID, Secret: "JBSWY3DPEHPK3PXP", Enabled: true, BackupCodes: `["12345678"]`}))
			}
			f.h.SetEnterpriseRepo(confirmationConsumeObserver{ChannelIAMRepository: f.iam, after: func() { require.NoError(t, f.db.Model(u).Update("auth_version", gorm.Expr("auth_version + 1")).Error) }})
			res := f.confirm(t, raw)
			if mfa {
				require.Equal(t, 206, res.Code)
				challenge := decodeMFATestResponse(t, res).MFAToken
				claims, err := auth.ValidateMFAToken(challenge, f.cfg.JWTSecret)
				require.NoError(t, err)
				require.Equal(t, u.AuthVersion, claims.AuthVersion)
				require.Equal(t, 401, performMFARequest(t, f.router, http.MethodPost, "/auth/mfa/verify", map[string]string{"mfa_token": challenge, "backup_code": "12345678"}, "").Code)
			} else {
				require.Equal(t, 200, res.Code)
				access := decodeMFATestResponse(t, res).Data.Token
				claims, err := auth.ValidateToken(access, f.cfg.JWTSecret)
				require.NoError(t, err)
				require.Equal(t, u.AuthVersion, claims.AuthVersion)
				require.Equal(t, 401, performMFARequest(t, f.router, http.MethodGet, "/auth/validate_token", nil, access).Code)
			}
		})
	}
}

func TestConfirmationHTTPKeepsInvitationProofIndependent(t *testing.T) {
	f := setupInvitationHTTP(t)
	iam := repository.NewChannelAuthEnterpriseRepository(f.db)
	h := handler.NewAuthHandler(&config.Config{JWTSecret: f.secret, JWTExpirationHours: 24}, f.users, f.accounts)
	h.SetEnterpriseRepo(iam)
	f.router.POST("/auth/confirmation", h.ConfirmEmail)
	invitationRaw, _ := f.invite(t, "independent@invite.test", domain.RoleAgent)
	_, confirmationRaw, err := iam.GenerateConfirmationToken(f.admin.ID)
	require.NoError(t, err)
	require.NoError(t, iam.ActivateConfirmationToken(f.admin.ID, confirmationRaw))
	require.Equal(t, 422, performMFARequest(t, f.router, http.MethodPost, "/auth/confirmation", map[string]string{"confirmation_token": invitationRaw}, "").Code)
	require.Equal(t, 400, performMFARequest(t, f.router, http.MethodPost, "/auth/invitations/activate", activationPayload(confirmationRaw), "").Code)
	require.Equal(t, 200, performMFARequest(t, f.router, http.MethodPost, "/auth/confirmation", map[string]string{"confirmation_token": confirmationRaw}, "").Code)
	activated := performMFARequest(t, f.router, http.MethodPost, "/auth/invitations/activate", activationPayload(invitationRaw), "")
	require.Equal(t, 200, activated.Code, activated.Body.String())
	require.NotContains(t, activated.Body.String(), "token")
	var workspaces int64
	require.NoError(t, f.db.Model(&domain.Account{}).Count(&workspaces).Error)
	require.EqualValues(t, 1, workspaces)
}
