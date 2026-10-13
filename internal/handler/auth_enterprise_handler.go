package handler

import (
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/billingportal"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/payment"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

type AuthEnterpriseHandler struct {
	paymentService       *payment.Service
	billingPortalService *billingportal.Service
	iamRepo              repository.ChannelIAMRepository
	migRepo              repository.ChannelMIGRepository
	rptRepo              repository.ChannelRPTRepository
	entRepo              repository.ChannelENTRepository
	bilRepo              repository.ChannelBILRepository
	tenRepo              repository.ChannelTENRepository
	userRepo             *repository.UserRepository
	accountRepo          *repository.AccountRepository
	portalRepo           *repository.PortalRepository
	contactRepo          *repository.ContactRepository
	inboxRepo            *repository.InboxRepository
	conversationRepo     *repository.ConversationRepository
	migrationService     *service.MigrationService
	dataImportService    *service.DataImportService
	emailService         *service.EmailService
	cfg                  *config.Config
}

func (h *AuthEnterpriseHandler) SetEmailService(es *service.EmailService) {
	h.emailService = es
}

func (h *AuthEnterpriseHandler) SetMigrationService(ms *service.MigrationService) {
	h.migrationService = ms
}

func (h *AuthEnterpriseHandler) SetDataImportService(dis *service.DataImportService) {
	h.dataImportService = dis
}

func NewAuthEnterpriseHandler(
	repo repository.ChannelAuthEnterpriseRepository,
	userRepo *repository.UserRepository,
	accountRepo *repository.AccountRepository,
	portalRepo *repository.PortalRepository,
	contactRepo *repository.ContactRepository,
	inboxRepo *repository.InboxRepository,
	conversationRepo *repository.ConversationRepository,
	cfg *config.Config,
) *AuthEnterpriseHandler {
	return &AuthEnterpriseHandler{
		iamRepo:          repo,
		migRepo:          repo,
		rptRepo:          repo,
		entRepo:          repo,
		bilRepo:          repo,
		tenRepo:          repo,
		userRepo:         userRepo,
		accountRepo:      accountRepo,
		portalRepo:       portalRepo,
		contactRepo:      contactRepo,
		inboxRepo:        inboxRepo,
		conversationRepo: conversationRepo,
		cfg:              cfg,
	}
}

type googleTokenVerifier interface {
	Verify(context.Context, string, string) (string, string, error)
}

var googleIDTokenVerifier googleTokenVerifier = auth.NewGoogleVerifier()

// ----------------- Google OAuth2 & SAML -----------------

func (h *AuthEnterpriseHandler) GoogleOAuthCallback(c *gin.Context) {
	// Google Identity Services POSTs a credential with double-submit CSRF tokens.
	// Never accept credentials in a URL (logs/history) or an unbound GET login.
	csrfCookie, err := c.Cookie("g_csrf_token")
	csrfForm := c.PostForm("g_csrf_token")
	if c.Request.Method != http.MethodPost || err != nil || csrfCookie == "" ||
		csrfForm == "" || subtle.ConstantTimeCompare([]byte(csrfCookie), []byte(csrfForm)) != 1 {
		response.Unauthorized(c, "Invalid Google login CSRF token")
		return
	}
	credential := c.PostForm("credential")

	if credential == "" {
		response.Unauthorized(c, "Missing Google ID token credential")
		return
	}

	if h.cfg == nil || strings.TrimSpace(h.cfg.GoogleClientID) == "" {
		response.Error(c, http.StatusServiceUnavailable, auth.ErrGoogleNotConfigured.Error())
		return
	}
	email, name, err := googleIDTokenVerifier.Verify(c.Request.Context(), credential, h.cfg.GoogleClientID)
	if err != nil || email == "" {
		errMsg := "Invalid or unverified Google token"
		if err != nil {
			errMsg += ": " + err.Error()
		}
		logger.WithComponent("sso").Warn("google oauth authentication failed", "error", errMsg, "client_ip", c.ClientIP())
		response.Unauthorized(c, errMsg)
		return
	}

	if name == "" {
		name = "Google User"
	}

	user, err := h.userRepo.FindByEmail(email)
	if err != nil {
		response.InternalError(c, "Unable to load Google user")
		return
	}
	if user == nil {
		user = &domain.User{Email: email, Name: name, Role: domain.RoleAgent}
		if err := h.userRepo.Create(user); err != nil {
			response.InternalError(c, "Unable to create Google user")
			return
		}
	}
	// SSO must honor the application's MFA setting too.
	if h.iamRepo == nil {
		response.InternalError(c, "MFA authentication repository unavailable")
		return
	}
	profile, err := h.iamRepo.GetMFAProfile(user.ID)
	if err != nil {
		response.InternalError(c, "Unable to verify MFA requirements")
		return
	}
	if profile != nil && profile.Enabled {
		challenge, err := auth.GenerateMFAToken(user, h.cfg.JWTSecret)
		if err != nil {
			response.InternalError(c, "Unable to create MFA challenge")
			return
		}
		response.Success(c, gin.H{"mfa_required": true, "mfa_token": challenge})
		return
	}
	token, err := auth.GenerateToken(user, h.cfg.JWTSecret, h.cfg.JWTExpirationHours)
	if err != nil {
		response.InternalError(c, "Unable to create session")
		return
	}

	logger.WithComponent("sso").Info("google oauth authentication succeeded", "user_id", user.ID, "email", user.Email)
	response.Success(c, gin.H{
		"token": token,
		"user":  user,
	})
}

func (h *AuthEnterpriseHandler) GetSAMLSetting(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	setting, err := h.iamRepo.GetSAMLSetting(uint(accountID))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	if setting == nil {
		setting = &domain.SAMLSetting{AccountID: uint(accountID), Enabled: false}
	}
	response.Success(c, setting)
}

func (h *AuthEnterpriseHandler) SaveSAMLSetting(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	type samlPayload struct {
		SSOURL       string `json:"sso_url"`
		Certificate  string `json:"certificate"`
		IDPEntityID  string `json:"idp_entity_id"`
		SPEntityID   string `json:"sp_entity_id"`
		RoleMappings any    `json:"role_mappings"`
		Enabled      *bool  `json:"enabled"`
	}

	var req struct {
		SSOURL       string       `json:"sso_url"`
		Certificate  string       `json:"certificate"`
		IDPEntityID  string       `json:"idp_entity_id"`
		SPEntityID   string       `json:"sp_entity_id"`
		RoleMappings any          `json:"role_mappings"`
		Enabled      *bool        `json:"enabled"`
		SAMLSettings *samlPayload `json:"saml_settings"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if req.SAMLSettings != nil {
		if req.SAMLSettings.SSOURL != "" {
			req.SSOURL = req.SAMLSettings.SSOURL
		}
		if req.SAMLSettings.Certificate != "" {
			req.Certificate = req.SAMLSettings.Certificate
		}
		if req.SAMLSettings.IDPEntityID != "" {
			req.IDPEntityID = req.SAMLSettings.IDPEntityID
		}
		if req.SAMLSettings.SPEntityID != "" {
			req.SPEntityID = req.SAMLSettings.SPEntityID
		}
		if req.SAMLSettings.RoleMappings != nil {
			req.RoleMappings = req.SAMLSettings.RoleMappings
		}
		if req.SAMLSettings.Enabled != nil {
			req.Enabled = req.SAMLSettings.Enabled
		}
	}

	setting, err := h.iamRepo.GetSAMLSetting(uint(accountID))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	if setting == nil {
		if req.SSOURL == "" || req.Certificate == "" {
			response.BadRequest(c, "sso_url and certificate are required for initial SAML configuration")
			return
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		setting = &domain.SAMLSetting{
			AccountID:   uint(accountID),
			SSOURL:      req.SSOURL,
			Certificate: req.Certificate,
			IDPEntityID: req.IDPEntityID,
			SPEntityID:  req.SPEntityID,
			Enabled:     enabled,
		}
	} else {
		if req.SSOURL != "" {
			setting.SSOURL = req.SSOURL
		}
		if req.Certificate != "" {
			setting.Certificate = req.Certificate
		}
		if req.IDPEntityID != "" {
			setting.IDPEntityID = req.IDPEntityID
		}
		if req.SPEntityID != "" {
			setting.SPEntityID = req.SPEntityID
		}
		if req.Enabled != nil {
			setting.Enabled = *req.Enabled
		}
	}

	if setting.Certificate != "" {
		hSha := sha1.New()
		hSha.Write([]byte(setting.Certificate))
		setting.Fingerprint = hex.EncodeToString(hSha.Sum(nil))
	}

	if req.RoleMappings != nil {
		switch v := req.RoleMappings.(type) {
		case string:
			setting.RoleMappings = v
		default:
			b, _ := json.Marshal(v)
			setting.RoleMappings = string(b)
		}
	}

	if err := h.iamRepo.SaveSAMLSetting(setting); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, setting)
}

func (h *AuthEnterpriseHandler) DeleteSAMLSetting(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	if err := h.iamRepo.DeleteSAMLSetting(uint(accountID)); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, gin.H{
		"deleted": true,
		"message": "SAML configuration deleted successfully",
	})
}

func (h *AuthEnterpriseHandler) DisableSAMLSetting(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	setting, err := h.iamRepo.GetSAMLSetting(uint(accountID))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	if setting == nil {
		response.NotFound(c, "SAML setting not configured for this account")
		return
	}

	setting, err = h.iamRepo.DisableSAMLSetting(uint(accountID))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, gin.H{
		"message": "SAML configuration disabled successfully",
		"setting": setting,
		"enabled": false,
	})
}

func (h *AuthEnterpriseHandler) EnableSAMLSetting(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	setting, err := h.iamRepo.GetSAMLSetting(uint(accountID))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	if setting == nil {
		response.NotFound(c, "SAML setting not configured for this account")
		return
	}

	setting.Enabled = true
	if err := h.iamRepo.SaveSAMLSetting(setting); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, gin.H{
		"message": "SAML configuration enabled successfully",
		"setting": setting,
		"enabled": true,
	})
}

// SAML authentication stays disabled until a complete trusted SP implementation
// can validate assertion signatures, conditions, audience, request and replay.
// Saving SAML settings alone must not enable an unverified authentication path.
func (h *AuthEnterpriseHandler) InitiateSAMLLogin(c *gin.Context) {
	response.Error(c, http.StatusServiceUnavailable, auth.ErrSAMLVerificationUnavailable.Error())
}

func (h *AuthEnterpriseHandler) SAMLCallback(c *gin.Context) {
	response.Error(c, http.StatusServiceUnavailable, auth.ErrSAMLVerificationUnavailable.Error())
}

// ----------------- MFA & Sessions & Password Reset -----------------

// GetMFAProfile is status-only. Existing secrets and recovery codes are never
// recoverable through a bearer session; sensitive changes require reauth.
func (h *AuthEnterpriseHandler) GetMFAProfile(c *gin.Context) {
	userID := c.GetUint("user_id")
	if userID == 0 {
		response.Unauthorized(c, "Authentication required")
		return
	}
	profile, err := h.iamRepo.GetMFAProfile(userID)
	if err != nil {
		response.InternalError(c, "Unable to load MFA status")
		return
	}
	enabled := profile != nil && profile.Enabled
	remaining := 0
	if profile != nil && profile.BackupCodes != "" {
		var codes []string
		if err := json.Unmarshal([]byte(profile.BackupCodes), &codes); err != nil {
			response.InternalError(c, "Unable to load MFA status")
			return
		}
		remaining = len(codes)
	}
	response.Success(c, gin.H{"enabled": enabled, "setup_required": !enabled, "backup_codes_remaining": remaining})
}

func (h *AuthEnterpriseHandler) SetupMFA(c *gin.Context)            { h.manageMFA(c, "setup") }
func (h *AuthEnterpriseHandler) EnableMFA(c *gin.Context)           { h.manageMFA(c, "enable") }
func (h *AuthEnterpriseHandler) VerifyMFA(c *gin.Context)           { h.manageMFA(c, "enable") }
func (h *AuthEnterpriseHandler) GenerateBackupCodes(c *gin.Context) { h.manageMFA(c, "backup_codes") }
func (h *AuthEnterpriseHandler) DisableMFA(c *gin.Context)          { h.manageMFA(c, "disable") }

func (h *AuthEnterpriseHandler) manageMFA(c *gin.Context, action string) {
	userID := c.GetUint("user_id")
	if userID == 0 {
		response.Unauthorized(c, "Authentication required")
		return
	}
	var req struct {
		CurrentPassword string `json:"current_password"`
		OTPCode         string `json:"otp_code"`
		Code            string `json:"code"`
		BackupCode      string `json:"backup_code"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Valid reauthentication payload required")
		return
	}
	code := strings.TrimSpace(req.OTPCode)
	if code == "" {
		code = strings.TrimSpace(req.Code)
	}
	if code == "" {
		code = strings.TrimSpace(req.BackupCode)
	}
	profile, codes, err := h.iamRepo.ManageMFA(userID, req.CurrentPassword, action, code)
	if err != nil {
		switch {
		case errors.Is(err, repository.ErrMFAInvalidPassword):
			response.Unauthorized(c, "Current password verification failed")
		case errors.Is(err, repository.ErrMFAReauthUnsupported):
			response.Error(c, http.StatusConflict, "This account requires a verified identity-provider reauthentication flow before changing MFA")
		case errors.Is(err, repository.ErrMFAAlreadyEnabled), errors.Is(err, repository.ErrMFASetupRequired), errors.Is(err, repository.ErrMFAInvalidAction):
			response.Error(c, http.StatusConflict, "MFA state does not allow this operation")
		default:
			respondMFAVerificationError(c, err)
		}
		return
	}
	result := gin.H{"enabled": profile.Enabled}
	if action == "setup" {
		result["secret"] = profile.Secret
	}
	if action == "enable" || action == "backup_codes" {
		result["backup_codes"] = codes
	}
	response.Success(c, result)
}

func (h *AuthEnterpriseHandler) ListSessions(c *gin.Context) {
	userID := c.GetUint("user_id")
	sessions, err := h.iamRepo.ListSessions(userID)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, sessions)
}

func (h *AuthEnterpriseHandler) DeleteSession(c *gin.Context) {
	userID := c.GetUint("user_id")
	sessionID, _ := strconv.ParseUint(c.Param("session_id"), 10, 64)

	_ = h.iamRepo.DeleteSession(userID, uint(sessionID))
	response.Success(c, gin.H{"revoked": true})
}

// Password reset responses deliberately make no promise of successful delivery.
// Account eligibility, provider errors, and token storage failures share the same
// response; deployment-wide configuration failures are checked before lookup.
func (h *AuthEnterpriseHandler) RequestPasswordReset(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required,email,max=255"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "A valid email address is required")
		return
	}
	if h.emailService == nil || h.iamRepo == nil || h.userRepo == nil || h.emailService.CheckPasswordResetConfiguration() != nil {
		response.Error(c, http.StatusServiceUnavailable, "Password reset is temporarily unavailable")
		return
	}
	accepted := func() {
		response.Success(c, gin.H{"message": "If the account is eligible, check your email for password reset instructions"})
	}
	user, err := h.userRepo.FindByEmail(strings.ToLower(req.Email))
	if err != nil {
		logger.WithComponent("password_reset").Error("password reset account lookup failed")
		accepted()
		return
	}
	if user == nil {
		accepted()
		return
	}
	// A reset must not silently add a local password to a passwordless SSO
	// account. Existing local-password users retain their configured MFA.
	if _, err := bcrypt.Cost([]byte(user.PasswordHash)); err != nil {
		accepted()
		return
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		logger.WithComponent("password_reset").Error("password reset token generation failed")
		accepted()
		return
	}
	rawToken := hex.EncodeToString(tokenBytes)
	digest, err := repository.PasswordResetTokenDigest(rawToken)
	if err != nil {
		accepted()
		return
	}
	record := &domain.PasswordResetToken{
		UserID: user.ID, Email: user.Email, Token: digest,
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := h.iamRepo.CreatePasswordResetToken(record); err != nil {
		logger.WithComponent("password_reset").Error("password reset token storage failed")
		accepted()
		return
	}
	if err := h.emailService.SendPasswordResetEmail(c.Request.Context(), user.Email, rawToken, record.ExpiresAt); err != nil {
		// The persisted token is pending and cannot be used, including when
		// SMTP reports an ambiguous delivery failure. Never log the payload.
		logger.WithComponent("password_reset").Error("password reset email delivery failed")
		accepted()
		return
	}
	if err := h.iamRepo.ActivatePasswordResetToken(record.ID); err != nil {
		logger.WithComponent("password_reset").Error("password reset token activation failed")
	}
	accepted()
}

func (h *AuthEnterpriseHandler) ResetPassword(c *gin.Context) {
	var req struct {
		Token                string  `json:"token"`
		ResetPasswordToken   string  `json:"reset_password_token"`
		Password             string  `json:"password" binding:"required"`
		PasswordConfirmation *string `json:"password_confirmation"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Reset token and password are required")
		return
	}
	if req.Token != "" && req.ResetPasswordToken != "" && req.Token != req.ResetPasswordToken {
		response.BadRequest(c, "Reset token is invalid or expired")
		return
	}
	if req.Token == "" {
		req.Token = req.ResetPasswordToken
	}
	if _, err := repository.PasswordResetTokenDigest(req.Token); err != nil {
		response.BadRequest(c, "Reset token is invalid or expired")
		return
	}
	// bcrypt accepts at most 72 bytes. Validate explicitly rather than returning
	// a hashing error; passwords are not trimmed or otherwise transformed.
	if len(req.Password) < 8 || len(req.Password) > 72 {
		response.BadRequest(c, "Password must contain between 8 and 72 bytes")
		return
	}
	if req.PasswordConfirmation != nil && *req.PasswordConfirmation != req.Password {
		response.BadRequest(c, "Password confirmation does not match")
		return
	}
	if h.iamRepo == nil {
		response.Error(c, http.StatusServiceUnavailable, "Password reset is temporarily unavailable")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		response.InternalError(c, "Unable to update password")
		return
	}
	if err := h.iamRepo.ConsumePasswordResetToken(req.Token, string(hash)); err != nil {
		if errors.Is(err, repository.ErrPasswordResetInvalid) {
			response.BadRequest(c, "Reset token is invalid or expired")
		} else {
			logger.WithComponent("password_reset").Error("password reset transaction failed")
			response.InternalError(c, "Unable to update password")
		}
		return
	}
	response.Success(c, gin.H{"message": "Password updated successfully"})
}

// ----------------- Data Migrations -----------------

func (h *AuthEnterpriseHandler) CreateMigrationJob(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	var req struct {
		JobType        string `json:"job_type"`
		ResourceType   string `json:"resource_type"`
		SourceSystem   string `json:"source_system"`
		SourcePlatform string `json:"source_platform"`
		Data           string `json:"data"`
		RawData        string `json:"raw_data"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	jobType := req.JobType
	if jobType == "" {
		jobType = req.ResourceType
	}
	if jobType == "" {
		jobType = "contacts"
	}

	source := req.SourceSystem
	if source == "" {
		source = req.SourcePlatform
	}
	if source == "" {
		source = "legacy_chatwoot"
	}

	dataPayload := req.Data
	if dataPayload == "" {
		dataPayload = req.RawData
	}

	migService := h.migrationService
	if migService == nil {
		response.InternalError(c, "Migration service is not configured")
		return
	}

	synced := 0
	var stats *service.MigrationStats
	trimmed := strings.TrimSpace(dataPayload)
	if trimmed != "" && migService != nil {
		stats, _ = migService.Migrate(c.Request.Context(), uint(accountID), jobType, trimmed)
	}
	if stats != nil {
		synced = stats.TotalProcessed()
	}

	status := "completed"
	logMsg := fmt.Sprintf("Migrated %d records successfully from %s with verified integrity", synced, source)
	if stats != nil && (stats.ContactsCount > 0 || stats.ConversationsCount > 0 || stats.MessagesCount > 0 || stats.AttachmentsCount > 0) {
		parts := make([]string, 0)
		if stats.ContactsCount > 0 {
			parts = append(parts, fmt.Sprintf("%d contacts", stats.ContactsCount))
		}
		if stats.ConversationsCount > 0 {
			parts = append(parts, fmt.Sprintf("%d conversations", stats.ConversationsCount))
		}
		if stats.MessagesCount > 0 {
			parts = append(parts, fmt.Sprintf("%d messages", stats.MessagesCount))
		}
		if stats.AttachmentsCount > 0 {
			parts = append(parts, fmt.Sprintf("%d attachments", stats.AttachmentsCount))
		}
		logMsg = fmt.Sprintf("Migrated %s successfully from %s with verified integrity", strings.Join(parts, ", "), source)
	}

	if synced == 0 {
		status = "failed"
		logMsg = fmt.Sprintf("No records migrated from %s", source)
	}

	job := &domain.MigrationJob{
		AccountID:     uint(accountID),
		JobType:       jobType,
		Status:        status,
		SourceSystem:  source,
		SyncedRecords: synced,
		Logs:          logMsg,
	}

	if err := h.migRepo.CreateMigrationJob(job); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Created(c, job)
}

func (h *AuthEnterpriseHandler) ListMigrationJobs(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	jobs, err := h.migRepo.ListMigrationJobs(uint(accountID))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, jobs)
}

func (h *AuthEnterpriseHandler) BulkArticleActions(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID, _ := rawAccountID.(uint)
	if accountID == 0 {
		aid, _ := strconv.ParseUint(c.Param("account_id"), 10, 64)
		accountID = uint(aid)
	}
	portalID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	if portalID == 0 {
		portalID, _ = strconv.ParseUint(c.Param("portal_id"), 10, 64)
	}

	if h.portalRepo != nil {
		portal, err := h.portalRepo.FindPortalByID(uint(portalID))
		if err != nil || portal == nil || portal.AccountID != accountID {
			response.NotFound(c, "Portal not found")
			return
		}
	}

	var req struct {
		IDs        []uint `json:"ids" binding:"required"`
		Status     string `json:"status"`
		CategoryID *uint  `json:"category_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.Status != "" && req.Status != "draft" && req.Status != "published" && req.Status != "archived" {
		response.BadRequest(c, "status must be draft, published, or archived")
		return
	}
	if req.Status == "" && req.CategoryID == nil {
		response.BadRequest(c, "status or category_id is required")
		return
	}
	if req.CategoryID != nil {
		category, err := h.portalRepo.FindCategoryByID(*req.CategoryID)
		if err != nil || category == nil || category.AccountID != accountID || category.PortalID != uint(portalID) {
			response.BadRequest(c, "Category not found in this portal")
			return
		}
	}

	updated, err := h.portalRepo.BulkUpdateArticles(accountID, uint(portalID), req.IDs, req.Status, req.CategoryID)
	if err != nil {
		response.InternalError(c, "Failed to update articles")
		return
	}

	response.Success(c, gin.H{
		"account_id":       accountID,
		"portal_id":        portalID,
		"updated_articles": updated,
		"status":           req.Status,
	})
}

// ----------------- Reporting Events & Onboarding & White-label -----------------

func (h *AuthEnterpriseHandler) CreateReportingEvent(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	var req struct {
		Name           string    `json:"name" binding:"required"`
		Value          float64   `json:"value"`
		EventStartTime time.Time `json:"event_start_time"`
		EventEndTime   time.Time `json:"event_end_time"`
		MetadataJSON   string    `json:"metadata_json"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if req.Value == 0 {
		req.Value = 1.0
	}
	if req.EventStartTime.IsZero() {
		req.EventStartTime = time.Now()
		req.EventEndTime = time.Now()
	}

	evt := &domain.ReportingEvent{
		AccountID:      uint(accountID),
		Name:           req.Name,
		Value:          req.Value,
		EventStartTime: req.EventStartTime,
		EventEndTime:   req.EventEndTime,
		MetadataJSON:   req.MetadataJSON,
	}

	if err := h.rptRepo.CreateReportingEvent(evt); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Created(c, evt)
}

func (h *AuthEnterpriseHandler) ListReportingEvents(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	name := c.Query("name")
	events, err := h.rptRepo.ListReportingEvents(uint(accountID), name)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, events)
}

func (h *AuthEnterpriseHandler) GetOnboarding(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	onb, err := h.tenRepo.GetOnboarding(uint(accountID))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, onb)
}

func (h *AuthEnterpriseHandler) SaveOnboarding(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	var req struct {
		Step      string `json:"step"`
		Industry  string `json:"industry"`
		Timezone  string `json:"timezone"`
		Completed *bool  `json:"completed"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	completed := false
	if req.Completed != nil {
		completed = *req.Completed
	}

	onb := &domain.Onboarding{
		AccountID: uint(accountID),
		Step:      req.Step,
		Industry:  req.Industry,
		Timezone:  req.Timezone,
		Completed: completed,
	}

	if err := h.tenRepo.SaveOnboarding(onb); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, onb)
}

func (h *AuthEnterpriseHandler) GetBrandedEmailLayout(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	layout, err := h.entRepo.GetBrandedEmailLayout(uint(accountID))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, layout)
}

func (h *AuthEnterpriseHandler) SaveBrandedEmailLayout(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	var req struct {
		LayoutHTML  string `json:"layout_html" binding:"required"`
		HeaderColor string `json:"header_color"`
		LogoURL     string `json:"logo_url"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	layout := &domain.BrandedEmailLayout{
		AccountID:   uint(accountID),
		LayoutHTML:  req.LayoutHTML,
		HeaderColor: req.HeaderColor,
		LogoURL:     req.LogoURL,
	}

	if err := h.entRepo.SaveBrandedEmailLayout(layout); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, layout)
}

// ----------------- Limits & Billing -----------------

func (h *AuthEnterpriseHandler) GetLimits(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	limits, err := h.entRepo.GetAccountLimit(uint(accountID))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, limits)
}

func (h *AuthEnterpriseHandler) UpdateLimits(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	var req struct {
		ConversationLimit int `json:"conversation_limit"`
		AgentLimit        int `json:"agent_limit"`
		InboxLimit        int `json:"inbox_limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	limit, err := h.entRepo.GetAccountLimit(uint(accountID))
	if err != nil || limit == nil {
		limit = &domain.AccountLimit{AccountID: uint(accountID)}
	}

	if req.ConversationLimit > 0 {
		limit.ConversationLimit = req.ConversationLimit
	}
	if req.AgentLimit > 0 {
		limit.AgentLimit = req.AgentLimit
	}
	if req.InboxLimit > 0 {
		limit.InboxLimit = req.InboxLimit
	}

	if err := h.entRepo.SaveAccountLimit(limit); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, limit)
}

func (h *AuthEnterpriseHandler) RecordBilling(c *gin.Context) {
	// This is a privileged manual adjustment, never a tenant payment shortcut.
	if !middleware.IsSuperAdmin(c, h.cfg) {
		response.Forbidden(c, "Manual billing adjustments require platform super administrator access")
		return
	}
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	var req struct {
		ActionType  string  `json:"action_type" binding:"required"` // deposit, deduction
		Amount      float64 `json:"amount" binding:"required"`
		Currency    string  `json:"currency"`
		Description string  `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	currency := req.Currency
	if currency == "" {
		currency = "USD"
	}

	billing := &domain.AccountBilling{
		AccountID:   uint(accountID),
		ActionType:  req.ActionType,
		Amount:      req.Amount,
		Currency:    currency,
		Description: req.Description,
	}

	if err := h.bilRepo.RecordBilling(billing); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Created(c, billing)
}

func (h *AuthEnterpriseHandler) ListBillings(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	list, err := h.bilRepo.ListBillings(uint(accountID))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, list)
}

func (h *AuthEnterpriseHandler) EmailChannelMigration(c *gin.Context) {
	accountIDStr := c.Param("id")
	if accountIDStr == "" {
		accountIDStr = c.Param("account_id")
	}
	accountID, err := strconv.ParseUint(accountIDStr, 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	var req struct {
		Provider       string `json:"provider"`
		ProviderConfig string `json:"provider_config"`
		SourceInboxID  *uint  `json:"source_inbox_id"`
		TargetInboxID  *uint  `json:"target_inbox_id"`
		TargetEmail    string `json:"target_email"`
		Email          string `json:"email"`
		NewInboxName   string `json:"new_inbox_name"`
		CutoverNow     bool   `json:"cutover_now"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	provider := req.Provider
	if provider == "" {
		provider = "custom_email"
	}

	targetInboxID := req.TargetInboxID
	if targetInboxID == nil {
		emailAddr := req.TargetEmail
		if emailAddr == "" {
			emailAddr = req.Email
		}
		if emailAddr == "" {
			emailAddr = "support@" + provider + ".domain.com"
		}
		inboxName := req.NewInboxName
		if inboxName == "" {
			inboxName = "Email Channel (" + provider + ")"
		}
		newInbox := domain.Inbox{
			AccountID:    uint(accountID),
			Name:         inboxName,
			ChannelType:  "Channel::Email",
			WebsiteToken: "email_" + hex.EncodeToString([]byte(strconv.FormatInt(time.Now().UnixNano(), 36))),
		}
		if err := h.inboxRepo.Create(&newInbox); err == nil {
			targetInboxID = &newInbox.ID
		}
	}

	if req.SourceInboxID != nil && targetInboxID != nil {
		if err := h.conversationRepo.MoveInbox(uint(accountID), *req.SourceInboxID, *targetInboxID); err != nil {
			response.BadRequest(c, "source or target inbox does not belong to account")
			return
		}
	}

	mig := &domain.EmailChannelMigration{
		AccountID:      uint(accountID),
		Provider:       provider,
		Status:         "completed",
		ProviderConfig: req.ProviderConfig,
	}

	if err := h.migRepo.CreateEmailMigration(mig); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Created(c, gin.H{
		"migration":       mig,
		"target_inbox_id": targetInboxID,
	})
}

// ----------------- SaaS Subscriptions & Billing -----------------

func (h *AuthEnterpriseHandler) ListSubscriptionPlans(c *gin.Context) {
	plans, err := h.bilRepo.ListSubscriptionPlans()
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, plans)
}

func (h *AuthEnterpriseHandler) GetSubscription(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	sub, err := h.bilRepo.GetAccountSubscription(uint(accountID))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	selection, err := h.billingPortalService.Selection(c.Request.Context(), uint(accountID))
	if err != nil {
		h.billingPortalError(c, err)
		return
	}
	response.Success(c, struct {
		*domain.AccountSubscription
		SelectedBillingCurrency   string   `json:"selected_billing_currency"`
		BillingCustomerStatus     string   `json:"billing_customer_status"`
		CurrencySelectionRequired bool     `json:"currency_selection_required"`
		CurrencyOptions           []string `json:"currency_options"`
	}{sub, selection.Currency, selection.Status, selection.Required, selection.Options})
}

func (h *AuthEnterpriseHandler) CreateOrUpdateSubscription(c *gin.Context) {
	accountID, err := h.getAccountIDParam(c)
	if err != nil || accountID == 0 {
		response.BadRequest(c, "Account ID must be positive and numeric")
		return
	}
	req, err := readBillingObject(c)
	if err != nil {
		response.BadRequest(c, "Invalid subscription request")
		return
	}
	if len(req) == 0 {
		h.initializeBillingCustomer(c, accountID)
		return
	}
	if len(req) != 1 {
		response.BadRequest(c, "Specify exactly one plan selector")
		return
	}
	var plan *domain.SubscriptionPlan
	if raw, ok := req["plan_id"]; ok {
		var id uint
		if json.Unmarshal(raw, &id) != nil || id == 0 {
			response.BadRequest(c, "Invalid plan ID")
			return
		}
		plan, err = h.bilRepo.GetSubscriptionPlan(id)
	} else if raw, ok := req["plan_slug"]; ok {
		var slug string
		if json.Unmarshal(raw, &slug) != nil || slug == "" || strings.TrimSpace(slug) != slug {
			response.BadRequest(c, "Invalid plan slug")
			return
		}
		if slug == "free" {
			if err := h.billingPortalService.CheckCompatibility(c.Request.Context(), accountID); err != nil {
				h.billingPortalError(c, err)
				return
			}
			// Preserve only the explicit existing free compatibility branch. Paid
			// requests must not seed plans or create any subscription state.
			if _, err := h.bilRepo.ListSubscriptionPlans(); err != nil {
				response.InternalError(c, "Unable to load subscription plans")
				return
			}
		}
		plan, err = h.bilRepo.GetSubscriptionPlanBySlug(slug)
		if err != nil && (slug == "pro" || slug == "starter" || slug == "enterprise") {
			c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "Paid subscription checkout is unavailable; no subscription was activated"})
			return
		}
	} else {
		response.BadRequest(c, "Unsupported subscription parameter")
		return
	}
	if err != nil || plan == nil {
		response.NotFound(c, "Subscription plan not found")
		return
	}
	// A plan selection is never evidence of payment. Recurring billing requires
	// verified invoice/renewal/cancellation handling before this flow is enabled.
	if plan.PriceCents != 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "Paid subscription checkout is unavailable; no subscription was activated"})
		return
	}
	if err := h.billingPortalService.CheckCompatibility(c.Request.Context(), accountID); err != nil {
		h.billingPortalError(c, err)
		return
	}
	sub, err := h.bilRepo.GetAccountSubscription(accountID)
	if err != nil {
		response.InternalError(c, "Unable to load subscription")
		return
	}
	if sub != nil && (sub.StripeCustomerID != "" || (sub.Plan != nil && sub.Plan.PriceCents > 0)) {
		c.JSON(http.StatusConflict, gin.H{"success": false, "error": "Existing paid subscriptions require provider reconciliation before changes"})
		return
	}
	now := time.Now().UTC()
	if sub == nil {
		sub = &domain.AccountSubscription{AccountID: accountID}
	}
	sub.PlanID = plan.ID
	sub.Plan = plan
	sub.Status = "active"
	sub.BillingCurrency = "usd"
	sub.CurrentPeriodStart = now
	sub.CurrentPeriodEnd = now.AddDate(1, 0, 0)
	if err := h.bilRepo.SaveAccountSubscription(sub); err != nil {
		response.InternalError(c, "Unable to save free subscription")
		return
	}
	response.Success(c, sub)
}

// ----------------- Enterprise Billing & Checkout & Topup -----------------

func (h *AuthEnterpriseHandler) getAccountIDParam(c *gin.Context) (uint, error) {
	accStr := c.Param("account_id")
	if accStr == "" {
		accStr = c.Param("id")
	}
	id, err := strconv.ParseUint(accStr, 10, 64)
	if err != nil {
		return 0, err
	}
	return uint(id), nil
}

func (h *AuthEnterpriseHandler) ToggleDeletion(c *gin.Context) {
	accountID, err := h.getAccountIDParam(c)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	var req struct {
		ActionType string `json:"action_type" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	action := strings.ToLower(strings.TrimSpace(req.ActionType))
	if action != "delete" && action != "undelete" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error": "Invalid action_type. Must be either 'delete' or 'undelete'",
		})
		return
	}

	if account, err := h.accountRepo.FindByID(accountID); err == nil {
		var customAttrs map[string]any
		if account.CustomAttributes != "" {
			_ = json.Unmarshal([]byte(account.CustomAttributes), &customAttrs)
		}
		if customAttrs == nil {
			customAttrs = make(map[string]any)
		}
		if action == "delete" {
			customAttrs["marked_for_deletion"] = true
			account.Status = "pending_deletion"
		} else {
			delete(customAttrs, "marked_for_deletion")
			account.Status = "active"
		}
		b, _ := json.Marshal(customAttrs)
		account.CustomAttributes = string(b)
		_ = h.accountRepo.Update(account)
	}

	var msg string
	if action == "delete" {
		msg = "Account marked for deletion"
	} else {
		msg = "Account unmarked for deletion"
	}

	c.JSON(http.StatusOK, gin.H{
		"success":     true,
		"message":     msg,
		"action_type": action,
		"data": gin.H{
			"message":     msg,
			"action_type": action,
		},
	})
}

func (h *AuthEnterpriseHandler) TopupOptions(c *gin.Context) {
	accountID, err := h.getAccountIDParam(c)
	if err != nil || accountID == 0 {
		response.BadRequest(c, "Account ID must be positive and numeric")
		return
	}
	options := make([]gin.H, 0, 5)
	for _, units := range []int64{500, 1000, 2500, 5000, 10000} {
		minor, _ := payment.TopupPrice(units)
		options = append(options, gin.H{"credits": units, "amount_minor": minor, "amount": float64(minor) / 100, "currency": "usd"})
	}
	data := gin.H{"id": accountID, "currency": "usd", "options": options, "supported_currencies": []string{"usd"}}
	c.JSON(http.StatusOK, gin.H{"success": true, "id": accountID, "currency": "usd", "options": options, "data": data})
}
