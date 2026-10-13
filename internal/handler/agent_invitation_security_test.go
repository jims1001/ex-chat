package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

type invitationHTTPFixture struct {
	router     *gin.Engine
	db         *gorm.DB
	users      *repository.UserRepository
	accounts   *repository.AccountRepository
	mail       *service.MockEmailSender
	emails     *service.EmailService
	admin      *domain.User
	account    *domain.Account
	adminToken string
	secret     string
}

func setupInvitationHTTP(t *testing.T) *invitationHTTPFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("SMTP_ADDRESS", "")
	t.Setenv("AGENT_INVITATIONS_ENABLED", "true")
	t.Setenv("FRONTEND_URL", "https://support.example.test")
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: gormlogger.Default.LogMode(gormlogger.Silent)})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&domain.User{}, &domain.Account{}, &domain.AccountUser{}, &domain.CustomRole{}, &domain.AgentInvitation{}, &domain.InvitationIdentity{}, &domain.MFAProfile{}, &domain.RevokedToken{}, &domain.EmailLog{}))
	f := &invitationHTTPFixture{db: db, users: repository.NewUserRepository(db), accounts: repository.NewAccountRepository(db), secret: "invitation-handler-test-secret"}
	f.admin = f.user(t, "admin@invite.test", "Original Admin")
	f.account = &domain.Account{Name: "Intended tenant"}
	require.NoError(t, f.accounts.Create(f.account))
	require.NoError(t, f.accounts.AddMember(f.account.ID, f.admin.ID, domain.RoleAdministrator))
	f.adminToken = f.token(t, f.admin)
	f.emails = service.NewEmailService(db)
	f.mail = service.NewMockEmailSender()
	f.emails.SetSender(f.mail)
	h := handler.NewAccountHandler(f.accounts, f.users)
	invitation := handler.NewAgentInvitationHandler(f.accounts, f.users, f.emails)
	cfg := &config.Config{JWTSecret: f.secret, JWTExpirationHours: 24}
	login := handler.NewAuthHandler(cfg, f.users, f.accounts)
	login.SetEnterpriseRepo(repository.NewChannelAuthEnterpriseRepository(db))
	r := gin.New()
	r.POST("/auth/invitations/activate", invitation.ActivateAgentInvitation)
	r.POST("/auth/invitations/preview", invitation.PreviewAgentInvitation)
	r.POST("/auth/sign_in", login.SignIn)
	authenticated := r.Group("/")
	authenticated.Use(middleware.AuthMiddleware(f.secret, f.users))
	authenticated.POST("/api/v1/agent_invitations/accept", invitation.AcceptAgentInvitation)
	tenant := authenticated.Group("/api/v1/accounts/:account_id")
	tenant.Use(middleware.TenantMiddleware(f.accounts), middleware.RequirePermission(f.accounts, domain.PermissionAgentManage))
	tenant.POST("/agents", invitation.AddAgent)
	tenant.PUT("/agents/:id", h.UpdateAgent)
	tenant.DELETE("/agents/:id", h.RemoveAgent)
	tenant.GET("/agent_invitations", invitation.ListAgentInvitations)
	tenant.DELETE("/agent_invitations/:id", invitation.RevokeAgentInvitation)
	f.router = r
	return f
}
func (f *invitationHTTPFixture) user(t *testing.T, email, name string) *domain.User {
	t.Helper()
	hash, err := auth.HashPassword("OriginalPassword123!")
	require.NoError(t, err)
	u := &domain.User{Name: name, Email: email, PasswordHash: hash, Role: domain.RoleAgent}
	require.NoError(t, f.users.Create(u))
	return u
}
func (f *invitationHTTPFixture) token(t *testing.T, user *domain.User) string {
	t.Helper()
	token, err := auth.GenerateToken(user, f.secret, 24)
	require.NoError(t, err)
	return token
}
func invitationTokenFromEmail(t *testing.T, email service.SentEmail) string {
	t.Helper()
	for _, word := range strings.Fields(email.TextBody) {
		u, err := url.Parse(word)
		if err == nil && u.Host == "support.example.test" {
			fragment, err := url.ParseQuery(u.Fragment)
			require.NoError(t, err)
			token := fragment.Get("token")
			if token != "" {
				require.Empty(t, u.RawQuery)
				return token
			}
		}
	}
	t.Fatal("missing invitation proof in local mock")
	return ""
}
func (f *invitationHTTPFixture) invite(t *testing.T, email, role string) (string, uint) {
	t.Helper()
	before := len(f.mail.GetSentEmails())
	res := performMFARequest(t, f.router, http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/agents", f.account.ID), map[string]string{"email": email, "role": role, "name": "Inviter cannot name global identity"}, f.adminToken)
	require.Equal(t, http.StatusAccepted, res.Code, res.Body.String())
	require.Contains(t, res.Body.String(), "captured_mock")
	sent := f.mail.GetSentEmails()
	require.Len(t, sent, before+1)
	raw := invitationTokenFromEmail(t, sent[len(sent)-1])
	require.NotContains(t, res.Body.String(), raw)
	require.NotContains(t, res.Body.String(), "token_digest")
	var envelope struct {
		Data domain.AgentInvitation `json:"data"`
	}
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &envelope))
	return raw, envelope.Data.ID
}
func activationPayload(raw string) map[string]string {
	return map[string]string{"token": raw, "name": "Recipient Chosen Name", "password": "ChosenPassword123!", "password_confirmation": "ChosenPassword123!"}
}
func TestAgentInvitationHTTPNewActivationNoWorkspaceOrJWT(t *testing.T) {
	f := setupInvitationHTTP(t)
	raw, id := f.invite(t, "new@invite.test", domain.RoleAgent)
	var count int64
	require.NoError(t, f.db.Model(&domain.User{}).Where("email = ?", "new@invite.test").Count(&count).Error)
	require.Zero(t, count)
	var row domain.AgentInvitation
	require.NoError(t, f.db.First(&row, id).Error)
	require.NotEqual(t, raw, row.TokenDigest)
	require.Len(t, row.TokenDigest, 64)
	response := performMFARequest(t, f.router, http.MethodPost, "/auth/invitations/activate", activationPayload(raw), "")
	require.Equal(t, 200, response.Code, response.Body.String())
	require.NotContains(t, response.Body.String(), "token")
	require.NotContains(t, response.Body.String(), `"password":`)
	u, err := f.users.FindByEmail("new@invite.test")
	require.NoError(t, err)
	require.NotNil(t, u)
	require.Equal(t, "Recipient Chosen Name", u.Name)
	require.Equal(t, domain.RoleAgent, u.Role)
	require.NotNil(t, u.ConfirmedAt)
	require.NoError(t, f.db.Model(&domain.Account{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	member, err := f.accounts.GetMembership(f.account.ID, u.ID)
	require.NoError(t, err)
	require.NotNil(t, member)
	retry := activationPayload(raw)
	retry["name"] = "Ignored retry name"
	retry["password"] = "IgnoredRetryPassword"
	retry["password_confirmation"] = retry["password"]
	require.Equal(t, 409, performMFARequest(t, f.router, http.MethodPost, "/auth/invitations/activate", retry, "").Code)
	require.Equal(t, 200, performMFARequest(t, f.router, http.MethodPost, "/auth/invitations/activate", activationPayload(raw), "").Code)
	current, err := f.users.FindByID(u.ID)
	require.NoError(t, err)
	require.Equal(t, u.PasswordHash, current.PasswordHash)
	require.Equal(t, u.Name, current.Name)
	require.Equal(t, 200, performMFARequest(t, f.router, http.MethodPost, "/auth/sign_in", map[string]string{"email": u.Email, "password": "ChosenPassword123!"}, "").Code)
	require.NoError(t, f.db.Model(&domain.AccountUser{}).Where("account_id = ? AND user_id = ?", f.account.ID, u.ID).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.Equal(t, 200, performMFARequest(t, f.router, http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/agents/%d", f.account.ID, u.ID), nil, f.adminToken).Code)
	require.Equal(t, 400, performMFARequest(t, f.router, http.MethodPost, "/auth/invitations/activate", activationPayload(raw), "").Code)
	member, err = f.accounts.GetMembership(f.account.ID, u.ID)
	require.NoError(t, err)
	require.Nil(t, member)
	require.NoError(t, f.db.Model(&domain.EmailLog{}).Count(&count).Error)
	require.Zero(t, count)
}
func TestAgentInvitationHTTPExistingUserRequiresIdentityAndMFA(t *testing.T) {
	f := setupInvitationHTTP(t)
	user := f.user(t, "existing@invite.test", "Existing Name")
	oldHash := user.PasswordHash
	require.NoError(t, f.db.Create(&domain.MFAProfile{UserID: user.ID, Secret: "JBSWY3DPEHPK3PXP", Enabled: true, BackupCodes: `["12345678","87654321"]`}).Error)
	raw, _ := f.invite(t, user.Email, domain.RoleAdministrator)
	require.Equal(t, 409, performMFARequest(t, f.router, http.MethodPost, "/auth/invitations/activate", activationPayload(raw), "").Code)
	body := map[string]string{"token": raw}
	require.Equal(t, 401, performMFARequest(t, f.router, http.MethodPost, "/api/v1/agent_invitations/accept", body, "").Code)
	require.Equal(t, 400, performMFARequest(t, f.router, http.MethodPost, "/api/v1/agent_invitations/accept", body, f.adminToken).Code)
	token := f.token(t, user) // Even a generic valid bearer cannot bypass current MFA.
	denied := performMFARequest(t, f.router, http.MethodPost, "/api/v1/agent_invitations/accept", body, token)
	require.Equal(t, 401, denied.Code, denied.Body.String())
	body["mfa_code"] = "12345678"
	accepted := performMFARequest(t, f.router, http.MethodPost, "/api/v1/agent_invitations/accept", body, token)
	require.Equal(t, 200, accepted.Code, accepted.Body.String())
	delete(body, "mfa_code")
	require.Equal(t, 200, performMFARequest(t, f.router, http.MethodPost, "/api/v1/agent_invitations/accept", body, token).Code)
	current, err := f.users.FindByID(user.ID)
	require.NoError(t, err)
	require.Equal(t, oldHash, current.PasswordHash)
	require.Equal(t, "Existing Name", current.Name)
	require.Nil(t, current.ConfirmedAt)
	require.Equal(t, domain.RoleAgent, current.Role)
	member, err := f.accounts.GetMembership(f.account.ID, user.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RoleAdministrator, member.Role)
	var profile domain.MFAProfile
	require.NoError(t, f.db.Where("user_id = ?", user.ID).First(&profile).Error)
	require.Equal(t, `["87654321"]`, profile.BackupCodes)
}
func TestAgentInvitationHTTPAccountRaceAndPartialSaga(t *testing.T) {
	t.Run("email appeared", func(t *testing.T) {
		f := setupInvitationHTTP(t)
		raw, _ := f.invite(t, "race@invite.test", domain.RoleAgent)
		original := f.user(t, "race@invite.test", "Real Owner")
		res := performMFARequest(t, f.router, http.MethodPost, "/auth/invitations/activate", activationPayload(raw), "")
		require.Equal(t, 409, res.Code)
		current, err := f.users.FindByID(original.ID)
		require.NoError(t, err)
		require.Equal(t, original.PasswordHash, current.PasswordHash)
		require.Equal(t, original.Name, current.Name)
		member, err := f.accounts.GetMembership(f.account.ID, current.ID)
		require.NoError(t, err)
		require.Nil(t, member)
	})
	t.Run("IAM committed TEN failed retry", func(t *testing.T) {
		f := setupInvitationHTTP(t)
		raw, _ := f.invite(t, "partial@invite.test", domain.RoleAgent)
		require.NoError(t, f.db.Exec("CREATE TRIGGER fail_invite_membership BEFORE INSERT ON account_users BEGIN SELECT RAISE(ABORT, 'local injected failure'); END").Error)
		failed := performMFARequest(t, f.router, http.MethodPost, "/auth/invitations/activate", activationPayload(raw), "")
		require.Equal(t, 409, failed.Code)
		require.Contains(t, failed.Body.String(), "identity exists")
		user, err := f.users.FindByEmail("partial@invite.test")
		require.NoError(t, err)
		require.NotNil(t, user)
		member, err := f.accounts.GetMembership(f.account.ID, user.ID)
		require.NoError(t, err)
		require.Nil(t, member)
		require.NoError(t, f.db.Exec("DROP TRIGGER fail_invite_membership").Error)
		wrongRetry := activationPayload(raw)
		wrongRetry["password"] = "DifferentRetryPassword"
		wrongRetry["password_confirmation"] = wrongRetry["password"]
		mismatch := performMFARequest(t, f.router, http.MethodPost, "/auth/invitations/activate", wrongRetry, "")
		require.Equal(t, 409, mismatch.Code)
		require.Contains(t, mismatch.Body.String(), "sign_in_required")
		member, err = f.accounts.GetMembership(f.account.ID, user.ID)
		require.NoError(t, err)
		require.Nil(t, member)
		require.Equal(t, 200, performMFARequest(t, f.router, http.MethodPost, "/auth/invitations/activate", activationPayload(raw), "").Code)
		current, err := f.users.FindByEmail(user.Email)
		require.NoError(t, err)
		require.Equal(t, user.ID, current.ID)
		require.Equal(t, user.PasswordHash, current.PasswordHash)
	})
}

type uncertainInvitationSender struct{ mail *service.MockEmailSender }

func (s uncertainInvitationSender) Send(ctx context.Context, from, to, subject, html, text string) error {
	_ = s.mail.Send(ctx, from, to, subject, html, text)
	return errors.New("provider may echo secret " + text)
}
func TestAgentInvitationHTTPUnknownMailReissueAndRevocation(t *testing.T) {
	f := setupInvitationHTTP(t)
	f.emails.SetSender(uncertainInvitationSender{f.mail})
	path := fmt.Sprintf("/api/v1/accounts/%d/agents", f.account.ID)
	failed := performMFARequest(t, f.router, http.MethodPost, path, map[string]string{"email": "unknown@invite.test"}, f.adminToken)
	require.Equal(t, 502, failed.Code)
	raw := invitationTokenFromEmail(t, f.mail.GetSentEmails()[0])
	require.NotContains(t, failed.Body.String(), raw)
	require.Equal(t, 400, performMFARequest(t, f.router, http.MethodPost, "/auth/invitations/activate", activationPayload(raw), "").Code)
	f.emails.SetSender(f.mail)
	fresh, id := f.invite(t, "unknown@invite.test", domain.RoleAgent)
	require.NotEqual(t, raw, fresh)
	require.Equal(t, 400, performMFARequest(t, f.router, http.MethodPost, "/auth/invitations/activate", activationPayload(raw), "").Code)
	revoked := performMFARequest(t, f.router, http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/agent_invitations/%d", f.account.ID, id), nil, f.adminToken)
	require.Equal(t, 200, revoked.Code)
	require.Equal(t, 400, performMFARequest(t, f.router, http.MethodPost, "/auth/invitations/activate", activationPayload(fresh), "").Code)
}
func TestAgentInvitationHTTPRejectsIdentityMutationAndAbsentMail(t *testing.T) {
	f := setupInvitationHTTP(t)
	path := fmt.Sprintf("/api/v1/accounts/%d/agents", f.account.ID)
	for _, password := range []any{"", nil, "Admin-chosen-secret"} {
		res := performMFARequest(t, f.router, http.MethodPost, path, map[string]any{"email": "never@invite.test", "password": password}, f.adminToken)
		require.Equal(t, 400, res.Code)
	}
	f.emails.SetSender(nil)
	res := performMFARequest(t, f.router, http.MethodPost, path, map[string]string{"email": "never@invite.test"}, f.adminToken)
	require.Equal(t, 503, res.Code)
	var count int64
	require.NoError(t, f.db.Model(&domain.AgentInvitation{}).Count(&count).Error)
	require.Zero(t, count)
	for _, field := range []string{"name", "password", "email"} {
		res := performMFARequest(t, f.router, http.MethodPut, path+fmt.Sprintf("/%d", f.admin.ID), map[string]any{field: nil, "role": "agent"}, f.adminToken)
		require.Equal(t, 400, res.Code)
	}
	member, err := f.accounts.GetMembership(f.account.ID, f.admin.ID)
	require.NoError(t, err)
	require.Equal(t, domain.RoleAdministrator, member.Role)
}
func TestAgentInvitationHTTPExpiryAndIdentityBeforeAcceptance(t *testing.T) {
	f := setupInvitationHTTP(t)
	raw, id := f.invite(t, "expiry@invite.test", domain.RoleAgent)
	require.NoError(t, f.db.Model(&domain.AgentInvitation{}).Where("id = ?", id).Update("expires_at", time.Now().Add(-time.Second)).Error)
	require.Equal(t, 400, performMFARequest(t, f.router, http.MethodPost, "/auth/invitations/activate", activationPayload(raw), "").Code)
	user, err := f.users.FindByEmail("expiry@invite.test")
	require.NoError(t, err)
	require.Nil(t, user)
}

func TestAgentInvitationHTTPPreviewBoundTenantAndDisabledFeature(t *testing.T) {
	f := setupInvitationHTTP(t)
	raw, _ := f.invite(t, "preview@invite.test", domain.RoleAdministrator)
	preview := performMFARequest(t, f.router, http.MethodPost, "/auth/invitations/preview", map[string]any{"token": raw, "account_id": 999, "role": "super_admin"}, "")
	require.Equal(t, 200, preview.Code)
	require.Equal(t, "no-store", preview.Header().Get("Cache-Control"))
	require.Contains(t, preview.Body.String(), `"account_name":"Intended tenant"`)
	require.Contains(t, preview.Body.String(), `"role":"administrator"`)
	require.NotContains(t, preview.Body.String(), raw)
	t.Setenv("AGENT_INVITATIONS_ENABLED", "false")
	for _, path := range []string{"/auth/invitations/preview", "/auth/invitations/activate"} {
		res := performMFARequest(t, f.router, http.MethodPost, path, activationPayload(raw), "")
		require.Equal(t, 503, res.Code)
	}
	require.Equal(t, 503, performMFARequest(t, f.router, http.MethodPost, "/api/v1/agent_invitations/accept", map[string]string{"token": raw}, f.adminToken).Code)
	var count int64
	require.NoError(t, f.db.Model(&domain.User{}).Where("email = ?", "preview@invite.test").Count(&count).Error)
	require.Zero(t, count)
}

func TestAgentInvitationHTTPLegacyCaseMailboxPinsExactIdentity(t *testing.T) {
	f := setupInvitationHTTP(t)
	user := f.user(t, "Legacy@Invite.Test", "Legacy Identity")
	raw, _ := f.invite(t, "legacy@invite.test", domain.RoleAgent)
	response := performMFARequest(t, f.router, http.MethodPost, "/api/v1/agent_invitations/accept", map[string]string{"token": raw}, f.token(t, user))
	require.Equal(t, 200, response.Code, response.Body.String())
	current, err := f.users.FindByID(user.ID)
	require.NoError(t, err)
	require.Equal(t, user.Email, current.Email)
	require.Equal(t, user.PasswordHash, current.PasswordHash)
}
