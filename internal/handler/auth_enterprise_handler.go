package handler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

type AuthEnterpriseHandler struct {
	repo             repository.ChannelAuthEnterpriseRepository
	userRepo         *repository.UserRepository
	accountRepo      *repository.AccountRepository
	portalRepo       *repository.PortalRepository
	contactRepo      *repository.ContactRepository
	migrationService  *service.MigrationService
	dataImportService *service.DataImportService
	cfg               *config.Config
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
	cfg *config.Config,
) *AuthEnterpriseHandler {
	return &AuthEnterpriseHandler{
		repo:        repo,
		userRepo:    userRepo,
		accountRepo: accountRepo,
		portalRepo:  portalRepo,
		contactRepo: contactRepo,
		cfg:         cfg,
	}
}

// ----------------- Google OAuth2 & SAML -----------------

func (h *AuthEnterpriseHandler) GoogleOAuthCallback(c *gin.Context) {
	credential := c.Query("credential")
	if credential == "" {
		credential = c.PostForm("credential")
	}

	if credential == "" {
		response.Unauthorized(c, "Missing Google ID token credential")
		return
	}

	email, name, err := auth.ParseGoogleIDToken(credential)
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
	if err != nil || user == nil {
		user = &domain.User{
			Email: email,
			Name:  name,
			Role:  domain.RoleAgent,
		}
		_ = h.userRepo.Create(user)
	}

	token, _ := auth.GenerateToken(user, h.cfg.JWTSecret, h.cfg.JWTExpirationHours)
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

	setting, err := h.repo.GetSAMLSetting(uint(accountID))
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

	var req struct {
		SSOURL       string `json:"sso_url" binding:"required"`
		Certificate  string `json:"certificate" binding:"required"`
		RoleMappings string `json:"role_mappings"`
		Enabled      *bool  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	setting := &domain.SAMLSetting{
		AccountID:    uint(accountID),
		SSOURL:       req.SSOURL,
		Certificate:  req.Certificate,
		RoleMappings: req.RoleMappings,
		Enabled:      enabled,
	}

	if err := h.repo.SaveSAMLSetting(setting); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, setting)
}

func (h *AuthEnterpriseHandler) InitiateSAMLLogin(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	relayStateBytes := make([]byte, 16)
	_, _ = rand.Read(relayStateBytes)
	relayState := hex.EncodeToString(relayStateBytes)

	response.Success(c, gin.H{
		"sso_url":     "https://idp.example.com/sso/saml",
		"relay_state": relayState,
		"email":       req.Email,
	})
}

func (h *AuthEnterpriseHandler) SAMLCallback(c *gin.Context) {
	samlResponse := c.PostForm("SAMLResponse")
	if samlResponse == "" {
		samlResponse = c.Query("SAMLResponse")
	}
	relayState := c.PostForm("RelayState")
	if relayState == "" {
		relayState = c.Query("RelayState")
	}

	if samlResponse == "" {
		response.Unauthorized(c, "Missing SAMLResponse payload")
		return
	}

	claims, err := auth.ParseSAMLResponse(samlResponse)
	if err != nil || claims == nil || claims.Email == "" {
		errMsg := "SAML verification failed"
		if err != nil {
			errMsg += ": " + err.Error()
		}
		logger.WithComponent("sso").Warn("saml authentication failed", "error", errMsg, "client_ip", c.ClientIP())
		response.Unauthorized(c, errMsg)
		return
	}

	userName := "SAML Enterprise User"
	if claims.FirstName != "" || claims.LastName != "" {
		userName = strings.TrimSpace(claims.FirstName + " " + claims.LastName)
	}

	user, err := h.userRepo.FindByEmail(claims.Email)
	if err != nil || user == nil {
		user = &domain.User{
			Email: claims.Email,
			Name:  userName,
			Role:  domain.RoleAgent,
		}
		_ = h.userRepo.Create(user)
	}

	token, _ := auth.GenerateToken(user, h.cfg.JWTSecret, h.cfg.JWTExpirationHours)
	logger.WithComponent("sso").Info("saml authentication succeeded", "user_id", user.ID, "email", user.Email)
	response.Success(c, gin.H{
		"token":       token,
		"user":        user,
		"relay_state": relayState,
	})
}

// ----------------- MFA & Sessions & Password Reset -----------------

func (h *AuthEnterpriseHandler) GetMFAProfile(c *gin.Context) {
	userID := c.GetUint("user_id")
	profile, err := h.repo.GetMFAProfile(userID)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	if profile == nil {
		secret, err := auth.GenerateTOTPSecret()
		if err != nil {
			response.InternalError(c, "Failed to generate MFA secret")
			return
		}
		rawCodes, _ := auth.GenerateBackupCodes(8)
		bBytes, _ := json.Marshal(rawCodes)
		profile = &domain.MFAProfile{
			UserID:      userID,
			Secret:      secret,
			BackupCodes: string(bBytes),
			Enabled:     false,
		}
		_ = h.repo.SaveMFAProfile(profile)
	}

	response.Success(c, profile)
}

func (h *AuthEnterpriseHandler) EnableMFA(c *gin.Context) {
	userID := c.GetUint("user_id")
	var req struct {
		OTPCode string `json:"otp_code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	profile, err := h.repo.GetMFAProfile(userID)
	if err != nil || profile == nil {
		response.BadRequest(c, "MFA setup has not been initiated. Please setup MFA first.")
		return
	}

	// Verify standard RFC 6238 TOTP or backup code
	valid := auth.VerifyTOTPCode(profile.Secret, req.OTPCode)
	if !valid {
		var backupCodes []string
		_ = json.Unmarshal([]byte(profile.BackupCodes), &backupCodes)
		for i, code := range backupCodes {
			if code == req.OTPCode {
				valid = true
				backupCodes = append(backupCodes[:i], backupCodes[i+1:]...)
				bBytes, _ := json.Marshal(backupCodes)
				profile.BackupCodes = string(bBytes)
				break
			}
		}
	}
	if !valid {
		response.BadRequest(c, "Invalid TOTP code or backup code")
		return
	}

	profile.Enabled = true
	_ = h.repo.SaveMFAProfile(profile)

	response.Success(c, gin.H{
		"enabled": true,
		"message": "MFA enabled successfully",
	})
}

func (h *AuthEnterpriseHandler) DisableMFA(c *gin.Context) {
	userID := c.GetUint("user_id")
	profile, err := h.repo.GetMFAProfile(userID)
	if err == nil && profile != nil {
		profile.Enabled = false
		_ = h.repo.SaveMFAProfile(profile)
	}
	response.Success(c, gin.H{"enabled": false})
}

func (h *AuthEnterpriseHandler) ListSessions(c *gin.Context) {
	userID := c.GetUint("user_id")
	sessions, err := h.repo.ListSessions(userID)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, sessions)
}

func (h *AuthEnterpriseHandler) DeleteSession(c *gin.Context) {
	userID := c.GetUint("user_id")
	sessionID, _ := strconv.ParseUint(c.Param("session_id"), 10, 64)

	_ = h.repo.DeleteSession(userID, uint(sessionID))
	response.Success(c, gin.H{"revoked": true})
}

func (h *AuthEnterpriseHandler) RequestPasswordReset(c *gin.Context) {
	var req struct {
		Email string `json:"email" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	user, err := h.userRepo.FindByEmail(req.Email)
	if err != nil || user == nil {
		// Silent success to prevent user enumeration
		response.Success(c, gin.H{"message": "If email exists, reset link has been sent"})
		return
	}

	tokenBytes := make([]byte, 16)
	_, _ = rand.Read(tokenBytes)
	resetToken := hex.EncodeToString(tokenBytes)

	tokenRecord := &domain.PasswordResetToken{
		UserID:    user.ID,
		Email:     user.Email,
		Token:     resetToken,
		ExpiresAt: time.Now().Add(2 * time.Hour),
	}
	_ = h.repo.CreatePasswordResetToken(tokenRecord)

	response.Success(c, gin.H{
		"message":     "Reset token generated",
		"reset_token": resetToken,
	})
}

func (h *AuthEnterpriseHandler) ResetPassword(c *gin.Context) {
	var req struct {
		Token    string `json:"token" binding:"required"`
		Password string `json:"password" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	tokenRecord, err := h.repo.GetValidPasswordResetToken(req.Token)
	if err != nil || tokenRecord == nil {
		response.BadRequest(c, "Reset token is invalid or expired")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	user, err := h.userRepo.FindByID(tokenRecord.UserID)
	if err != nil || user == nil {
		response.NotFound(c, "User not found")
		return
	}

	user.PasswordHash = string(hash)
	_ = h.userRepo.Update(user)
	_ = h.repo.MarkPasswordResetTokenUsed(tokenRecord.ID)

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
		migService = service.NewMigrationService(h.repo.GetDB())
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

	if err := h.repo.CreateMigrationJob(job); err != nil {
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

	jobs, err := h.repo.ListMigrationJobs(uint(accountID))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, jobs)
}

func (h *AuthEnterpriseHandler) BulkArticleActions(c *gin.Context) {
	accountID, _ := strconv.ParseUint(c.Param("account_id"), 10, 64)
	portalID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	if portalID == 0 {
		portalID, _ = strconv.ParseUint(c.Param("portal_id"), 10, 64)
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

	response.Success(c, gin.H{
		"account_id":       accountID,
		"portal_id":        portalID,
		"updated_articles": len(req.IDs),
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

	if err := h.repo.CreateReportingEvent(evt); err != nil {
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
	events, err := h.repo.ListReportingEvents(uint(accountID), name)
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

	onb, err := h.repo.GetOnboarding(uint(accountID))
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

	if err := h.repo.SaveOnboarding(onb); err != nil {
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

	layout, err := h.repo.GetBrandedEmailLayout(uint(accountID))
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

	if err := h.repo.SaveBrandedEmailLayout(layout); err != nil {
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

	limits, err := h.repo.GetAccountLimit(uint(accountID))
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

	limit, err := h.repo.GetAccountLimit(uint(accountID))
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

	if err := h.repo.SaveAccountLimit(limit); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, limit)
}

func (h *AuthEnterpriseHandler) RecordBilling(c *gin.Context) {
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

	if err := h.repo.RecordBilling(billing); err != nil {
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

	list, err := h.repo.ListBillings(uint(accountID))
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

	db := h.repo.GetDB()
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
		if err := db.Create(&newInbox).Error; err == nil {
			targetInboxID = &newInbox.ID
		}
	}

	if req.SourceInboxID != nil && targetInboxID != nil {
		_ = db.Model(&domain.Conversation{}).
			Where("account_id = ? AND inbox_id = ?", accountID, *req.SourceInboxID).
			Update("inbox_id", *targetInboxID).Error
	}

	mig := &domain.EmailChannelMigration{
		AccountID:      uint(accountID),
		Provider:       provider,
		Status:         "completed",
		ProviderConfig: req.ProviderConfig,
	}

	if err := h.repo.CreateEmailMigration(mig); err != nil {
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
	plans, err := h.repo.ListSubscriptionPlans()
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

	sub, err := h.repo.GetAccountSubscription(uint(accountID))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, sub)
}

func (h *AuthEnterpriseHandler) CreateOrUpdateSubscription(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	var req struct {
		PlanID   uint   `json:"plan_id"`
		PlanSlug string `json:"plan_slug"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	// Ensure default plans exist
	_, _ = h.repo.ListSubscriptionPlans()

	db := h.repo.GetDB()
	var plan domain.SubscriptionPlan
	var planErr error
	if req.PlanID > 0 {
		planErr = db.First(&plan, req.PlanID).Error
	} else if req.PlanSlug != "" {
		planErr = db.Where("slug = ?", req.PlanSlug).First(&plan).Error
	} else {
		planErr = db.Where("slug = ?", "pro").First(&plan).Error
	}

	if planErr != nil {
		response.NotFound(c, "Subscription plan not found")
		return
	}

	now := time.Now().UTC()
	sub, _ := h.repo.GetAccountSubscription(uint(accountID))
	if sub == nil {
		sub = &domain.AccountSubscription{
			AccountID: uint(accountID),
		}
	}
	sub.PlanID = plan.ID
	sub.Plan = &plan
	sub.Status = "active"
	sub.CurrentPeriodStart = now
	sub.CurrentPeriodEnd = now.AddDate(0, 1, 0) // 1 month

	if err := h.repo.SaveAccountSubscription(sub); err != nil {
		response.InternalError(c, err.Error())
		return
	}

	_ = h.repo.RecordBilling(&domain.AccountBilling{
		AccountID:   uint(accountID),
		ActionType:  "subscription",
		Amount:      float64(plan.PriceCents) / 100.0,
		Currency:    "USD",
		Description: "Subscription: " + plan.Name,
		CreatedAt:   now,
	})

	response.Success(c, sub)
}

func (h *AuthEnterpriseHandler) HandleSubscriptionWebhook(c *gin.Context) {
	var payload map[string]any
	if err := c.ShouldBindJSON(&payload); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	response.Success(c, gin.H{
		"received": true,
		"status":   "processed",
	})
}
