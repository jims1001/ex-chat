package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

// PlatformHandler manages administrative platform API endpoints
type PlatformHandler struct {
	platformRepo *repository.PlatformRepository
	userRepo     *repository.UserRepository
	jwtSecret    string
}

// NewPlatformHandler creates a new PlatformHandler instance
func NewPlatformHandler(p *repository.PlatformRepository, u *repository.UserRepository) *PlatformHandler {
	return &PlatformHandler{
		platformRepo: p,
		userRepo:     u,
		jwtSecret:    "platform_login_secret_default_key",
	}
}

// SetJWTSecret configures the token signing secret for platform login links
func (h *PlatformHandler) SetJWTSecret(secret string) {
	if secret != "" {
		h.jwtSecret = secret
	}
}

// PlatformAuthMiddleware validates platform api token
func (h *PlatformHandler) PlatformAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("api_access_token")
		if token == "" {
			token = c.GetHeader("X-Platform-App-Token")
		}
		if token == "" {
			token = c.Query("api_access_token")
		}
		if token == "" {
			logger.WithComponent("platform").Warn("missing platform api access token",
				"client_ip", c.ClientIP(),
				"path", c.Request.URL.Path,
			)
			response.Unauthorized(c, "missing platform api access token")
			c.Abort()
			return
		}

		app, err := h.platformRepo.VerifyPlatformToken(c.Request.Context(), token)
		if err != nil {
			logger.WithComponent("platform").Warn("invalid platform api access token",
				"client_ip", c.ClientIP(),
				"error", err.Error(),
			)
			response.Unauthorized(c, "invalid platform api access token")
			c.Abort()
			return
		}

		c.Set("platform_app_id", app.ID)
		c.Next()
	}
}

// ----------------- Account Lifecycle Management -----------------

// PlatformCreateAccountReq represents account provisioning payload
type PlatformCreateAccountReq struct {
	Name                string `json:"name" binding:"required"`
	Locale              string `json:"locale"`
	Domain              string `json:"domain"`
	SupportEmail        string `json:"support_email"`
	AutoResolveDuration int    `json:"auto_resolve_duration"`
}

// CreateAccount provisions a new tenant account
func (h *PlatformHandler) CreateAccount(c *gin.Context) {
	var req PlatformCreateAccountReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	acc := domain.Account{
		Name:                strings.TrimSpace(req.Name),
		Locale:              req.Locale,
		Domain:              strings.TrimSpace(req.Domain),
		SupportEmail:        strings.TrimSpace(req.SupportEmail),
		AutoResolveDuration: req.AutoResolveDuration,
	}
	if acc.Locale == "" {
		acc.Locale = "en"
	}

	if err := h.platformRepo.CreateAccount(c.Request.Context(), &acc); err != nil {
		logger.WithComponent("platform").Error("failed to create platform account",
			"name", req.Name,
			"error", err.Error(),
		)
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	logger.WithComponent("platform").Info("platform account created successfully",
		"account_id", acc.ID,
		"name", acc.Name,
	)

	response.Created(c, acc)
}

// ListAccounts returns paginated accounts matching optional search
func (h *PlatformHandler) ListAccounts(c *gin.Context) {
	search := c.Query("search")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "25"))

	accounts, total, err := h.platformRepo.ListAccounts(c.Request.Context(), search, page, pageSize)
	if err != nil {
		logger.WithComponent("platform").Error("failed to list platform accounts", "error", err.Error())
		response.InternalError(c, "Failed to list accounts")
		return
	}

	response.Success(c, gin.H{
		"accounts":  accounts,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// GetAccount retrieves account details by ID
func (h *PlatformHandler) GetAccount(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	acc, err := h.platformRepo.GetAccount(c.Request.Context(), uint(id))
	if err != nil || acc == nil {
		response.NotFound(c, "account not found")
		return
	}
	response.Success(c, acc)
}

// PlatformUpdateAccountReq represents account modification payload
type PlatformUpdateAccountReq struct {
	Name                string `json:"name"`
	Locale              string `json:"locale"`
	Domain              string `json:"domain"`
	SupportEmail        string `json:"support_email"`
	AutoResolveDuration *int   `json:"auto_resolve_duration"`
}

// UpdateAccount modifies an existing account
func (h *PlatformHandler) UpdateAccount(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	var req PlatformUpdateAccountReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	updates := make(map[string]any)
	if strings.TrimSpace(req.Name) != "" {
		updates["name"] = strings.TrimSpace(req.Name)
	}
	if strings.TrimSpace(req.Locale) != "" {
		updates["locale"] = strings.TrimSpace(req.Locale)
	}
	if strings.TrimSpace(req.Domain) != "" {
		updates["domain"] = strings.TrimSpace(req.Domain)
	}
	if strings.TrimSpace(req.SupportEmail) != "" {
		updates["support_email"] = strings.TrimSpace(req.SupportEmail)
	}
	if req.AutoResolveDuration != nil {
		updates["auto_resolve_duration"] = *req.AutoResolveDuration
	}

	updated, err := h.platformRepo.UpdateAccount(c.Request.Context(), uint(id), updates)
	if err != nil {
		logger.WithComponent("platform").Error("failed to update account", "account_id", id, "error", err.Error())
		response.NotFound(c, "Account not found or update failed")
		return
	}

	logger.WithComponent("platform").Info("platform account updated", "account_id", id)
	response.Success(c, updated)
}

// DeleteAccount deletes an account and cleans up memberships
func (h *PlatformHandler) DeleteAccount(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	if err := h.platformRepo.DeleteAccount(c.Request.Context(), uint(id)); err != nil {
		logger.WithComponent("platform").Error("failed to delete account", "account_id", id, "error", err.Error())
		response.NotFound(c, "Account not found")
		return
	}

	logger.WithComponent("platform").Info("platform account deleted", "account_id", id)
	response.Success(c, gin.H{"message": "account deleted successfully"})
}

// GetAccountStatus checks operational status of an account
func (h *PlatformHandler) GetAccountStatus(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	acc, err := h.platformRepo.GetAccount(c.Request.Context(), uint(id))
	if err != nil || acc == nil {
		response.NotFound(c, "account not found")
		return
	}

	response.Success(c, gin.H{
		"id":     acc.ID,
		"name":   acc.Name,
		"status": "active",
	})
}

// ----------------- User Lifecycle Management -----------------

// PlatformCreateUserReq represents user creation payload
type PlatformCreateUserReq struct {
	Name     string `json:"name" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
	Role     string `json:"role"`
}

// CreateUser provisions a new user
func (h *PlatformHandler) CreateUser(c *gin.Context) {
	var req PlatformCreateUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		logger.WithComponent("platform").Error("failed to hash password for platform user",
			"email", req.Email,
			"error", err.Error(),
		)
		response.Error(c, http.StatusInternalServerError, "failed to hash password")
		return
	}

	user := domain.User{
		Name:         strings.TrimSpace(req.Name),
		Email:        strings.ToLower(strings.TrimSpace(req.Email)),
		PasswordHash: string(hash),
		Role:         req.Role,
		Availability: domain.AvailabilityOnline,
	}
	if user.Role == "" {
		user.Role = domain.RoleAgent
	}

	if err := h.userRepo.Create(&user); err != nil {
		logger.WithComponent("platform").Warn("platform create user email conflict or failure",
			"email", req.Email,
			"error", err.Error(),
		)
		response.Error(c, http.StatusConflict, "email already in use")
		return
	}

	logger.WithComponent("platform").Info("platform user created successfully",
		"user_id", user.ID,
		"email", user.Email,
		"role", user.Role,
	)

	response.Created(c, user)
}

// ListUsers queries platform users with search and pagination
func (h *PlatformHandler) ListUsers(c *gin.Context) {
	search := c.Query("search")
	role := c.Query("role")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "25"))

	users, total, err := h.platformRepo.ListUsers(c.Request.Context(), search, role, page, pageSize)
	if err != nil {
		logger.WithComponent("platform").Error("failed to list users", "error", err.Error())
		response.InternalError(c, "Failed to list users")
		return
	}

	response.Success(c, gin.H{
		"users":     users,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// GetUser retrieves user details including tenant memberships
func (h *PlatformHandler) GetUser(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	user, memberships, err := h.platformRepo.GetUser(c.Request.Context(), uint(id))
	if err != nil || user == nil {
		response.NotFound(c, "user not found")
		return
	}

	response.Success(c, gin.H{
		"user":          user,
		"account_users": memberships,
	})
}

// PlatformUpdateUserReq represents user update payload
type PlatformUpdateUserReq struct {
	Name         string `json:"name"`
	Email        string `json:"email"`
	Password     string `json:"password"`
	Role         string `json:"role"`
	Availability string `json:"availability"`
	AvatarURL    string `json:"avatar_url"`
}

// UpdateUser updates user profile and credentials
func (h *PlatformHandler) UpdateUser(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	var req PlatformUpdateUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	updates := make(map[string]any)
	if strings.TrimSpace(req.Name) != "" {
		updates["name"] = strings.TrimSpace(req.Name)
	}
	if strings.TrimSpace(req.Email) != "" {
		updates["email"] = strings.ToLower(strings.TrimSpace(req.Email))
	}
	if strings.TrimSpace(req.Password) != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil {
			response.InternalError(c, "Failed to hash password")
			return
		}
		updates["password_hash"] = string(hash)
	}
	if strings.TrimSpace(req.Role) != "" {
		updates["role"] = strings.TrimSpace(req.Role)
	}
	if strings.TrimSpace(req.Availability) != "" {
		updates["availability"] = strings.TrimSpace(req.Availability)
	}
	if strings.TrimSpace(req.AvatarURL) != "" {
		updates["avatar_url"] = strings.TrimSpace(req.AvatarURL)
	}

	updated, err := h.platformRepo.UpdateUser(c.Request.Context(), uint(id), updates)
	if err != nil {
		logger.WithComponent("platform").Error("failed to update user", "user_id", id, "error", err.Error())
		response.NotFound(c, "User not found or update failed")
		return
	}

	logger.WithComponent("platform").Info("platform user updated", "user_id", id)
	response.Success(c, updated)
}

// DeleteUser deletes a user and cleans up memberships
func (h *PlatformHandler) DeleteUser(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	if err := h.platformRepo.DeleteUser(c.Request.Context(), uint(id)); err != nil {
		logger.WithComponent("platform").Error("failed to delete user", "user_id", id, "error", err.Error())
		response.NotFound(c, "User not found")
		return
	}

	logger.WithComponent("platform").Info("platform user deleted", "user_id", id)
	response.Success(c, gin.H{"message": "user deleted successfully"})
}

// GetUserLoginToken generates single-sign-on token for the user
func (h *PlatformHandler) GetUserLoginToken(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(c, "Invalid user ID")
		return
	}

	user, _, err := h.platformRepo.GetUser(c.Request.Context(), uint(id))
	if err != nil || user == nil {
		response.NotFound(c, "User not found")
		return
	}

	token, err := auth.GenerateToken(user, h.jwtSecret, 24)
	if err != nil {
		logger.WithComponent("platform").Error("failed to generate sso login token", "user_id", id, "error", err.Error())
		response.InternalError(c, "Failed to generate login token")
		return
	}

	response.Success(c, gin.H{
		"user":      user,
		"token":     token,
		"login_url": fmt.Sprintf("/app/login?token=%s", token),
	})
}

// ----------------- AccountUser Membership Management -----------------

// PlatformAddAccountUserReq represents adding member payload
type PlatformAddAccountUserReq struct {
	UserID uint   `json:"user_id" binding:"required"`
	Role   string `json:"role" binding:"required"`
}

// AddAccountUser associates a user with an account
func (h *PlatformHandler) AddAccountUser(c *gin.Context) {
	accountIDStr := c.Param("id")
	if accountIDStr == "" {
		accountIDStr = c.Param("account_id")
	}
	accountID, _ := strconv.ParseUint(accountIDStr, 10, 32)
	var req PlatformAddAccountUserReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if err := h.platformRepo.AddUserToAccount(c.Request.Context(), uint(accountID), req.UserID, req.Role); err != nil {
		logger.WithComponent("platform").Error("failed to add user to account via platform api",
			"account_id", uint(accountID),
			"user_id", req.UserID,
			"role", req.Role,
			"error", err.Error(),
		)
		response.Error(c, http.StatusConflict, err.Error())
		return
	}

	logger.WithComponent("platform").Info("added user to account via platform api",
		"account_id", uint(accountID),
		"user_id", req.UserID,
		"role", req.Role,
	)

	response.Created(c, gin.H{"status": "ok"})
}

// ListAccountUsers lists member users within a tenant account
func (h *PlatformHandler) ListAccountUsers(c *gin.Context) {
	accountIDStr := c.Param("id")
	if accountIDStr == "" {
		accountIDStr = c.Param("account_id")
	}
	accountID, err := strconv.ParseUint(accountIDStr, 10, 64)
	if err != nil || accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "25"))

	members, total, err := h.platformRepo.ListAccountUsers(c.Request.Context(), uint(accountID), page, pageSize)
	if err != nil {
		logger.WithComponent("platform").Error("failed to list account users", "account_id", accountID, "error", err.Error())
		response.InternalError(c, "Failed to list account users")
		return
	}

	response.Success(c, gin.H{
		"account_users": members,
		"total":         total,
		"page":          page,
		"page_size":     pageSize,
	})
}

// PlatformDeleteAccountUserReq represents member removal payload
type PlatformDeleteAccountUserReq struct {
	UserID uint `json:"user_id"`
}

// DeleteAccountUser removes a user from an account (supports body, query, or path param)
func (h *PlatformHandler) DeleteAccountUser(c *gin.Context) {
	accountIDStr := c.Param("id")
	if accountIDStr == "" {
		accountIDStr = c.Param("account_id")
	}
	accountID, err := strconv.ParseUint(accountIDStr, 10, 64)
	if err != nil || accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	var userID uint
	if uidStr := c.Param("user_id"); uidStr != "" {
		if parsed, err := strconv.ParseUint(uidStr, 10, 64); err == nil {
			userID = uint(parsed)
		}
	}
	if userID == 0 {
		if uidStr := c.Query("user_id"); uidStr != "" {
			if parsed, err := strconv.ParseUint(uidStr, 10, 64); err == nil {
				userID = uint(parsed)
			}
		}
	}
	if userID == 0 {
		var req PlatformDeleteAccountUserReq
		_ = c.ShouldBindJSON(&req)
		userID = req.UserID
	}

	if userID == 0 {
		response.BadRequest(c, "user_id is required")
		return
	}

	if err := h.platformRepo.RemoveUserFromAccount(c.Request.Context(), uint(accountID), userID); err != nil {
		logger.WithComponent("platform").Error("failed to remove user from account",
			"account_id", accountID, "user_id", userID, "error", err.Error())
		response.NotFound(c, "Account user membership not found")
		return
	}

	logger.WithComponent("platform").Info("removed user from account", "account_id", accountID, "user_id", userID)
	response.Success(c, gin.H{"message": "account user removed successfully"})
}

// ----------------- AgentBot (Platform & Account) -----------------

// PlatformCreateAgentBotReq represents agent bot creation payload
type PlatformCreateAgentBotReq struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	OutgoingURL string `json:"outgoing_url" binding:"required"`
	BotType     string `json:"bot_type"`
	AccountID   uint   `json:"account_id"`
}

// CreateAgentBot creates a platform-level or account-level agent bot
func (h *PlatformHandler) CreateAgentBot(c *gin.Context) {
	var req PlatformCreateAgentBotReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	botType := strings.TrimSpace(req.BotType)
	if botType == "" {
		botType = "webhook"
	}

	bot := domain.AgentBot{
		Name:        strings.TrimSpace(req.Name),
		Description: req.Description,
		OutgoingURL: strings.TrimSpace(req.OutgoingURL),
		BotType:     botType,
		AccountID:   req.AccountID,
	}

	if err := h.platformRepo.CreateAgentBot(c.Request.Context(), &bot); err != nil {
		logger.WithComponent("platform").Error("failed to create agent bot", "name", req.Name, "error", err.Error())
		response.InternalError(c, "Failed to create agent bot")
		return
	}

	logger.WithComponent("platform").Info("agent bot created via platform api",
		"bot_id", bot.ID, "account_id", bot.AccountID, "name", bot.Name)

	response.Created(c, bot)
}

// ListAgentBots lists platform-level or filtered agent bots
func (h *PlatformHandler) ListAgentBots(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "25"))

	var accountID *uint
	if accStr := c.Query("account_id"); accStr != "" {
		if parsed, err := strconv.ParseUint(accStr, 10, 64); err == nil {
			u := uint(parsed)
			accountID = &u
		}
	}

	bots, total, err := h.platformRepo.ListAgentBots(c.Request.Context(), accountID, page, pageSize)
	if err != nil {
		logger.WithComponent("platform").Error("failed to list agent bots", "error", err.Error())
		response.InternalError(c, "Failed to list agent bots")
		return
	}

	response.Success(c, gin.H{
		"agent_bots": bots,
		"total":      total,
		"page":       page,
		"page_size":  pageSize,
	})
}

// GetAgentBot retrieves agent bot details
func (h *PlatformHandler) GetAgentBot(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(c, "Invalid agent bot ID")
		return
	}

	bot, err := h.platformRepo.GetAgentBot(c.Request.Context(), uint(id))
	if err != nil || bot == nil {
		response.NotFound(c, "agent bot not found")
		return
	}

	response.Success(c, bot)
}

// PlatformUpdateAgentBotReq represents agent bot update payload
type PlatformUpdateAgentBotReq struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	OutgoingURL string `json:"outgoing_url"`
	BotType     string `json:"bot_type"`
}

// UpdateAgentBot updates an agent bot's parameters
func (h *PlatformHandler) UpdateAgentBot(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(c, "Invalid agent bot ID")
		return
	}

	var req PlatformUpdateAgentBotReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	updates := make(map[string]any)
	if strings.TrimSpace(req.Name) != "" {
		updates["name"] = strings.TrimSpace(req.Name)
	}
	if req.Description != "" {
		updates["description"] = req.Description
	}
	if strings.TrimSpace(req.OutgoingURL) != "" {
		updates["outgoing_url"] = strings.TrimSpace(req.OutgoingURL)
	}
	if strings.TrimSpace(req.BotType) != "" {
		updates["bot_type"] = strings.TrimSpace(req.BotType)
	}

	updated, err := h.platformRepo.UpdateAgentBot(c.Request.Context(), uint(id), updates)
	if err != nil {
		logger.WithComponent("platform").Error("failed to update agent bot", "bot_id", id, "error", err.Error())
		response.NotFound(c, "Agent bot not found")
		return
	}

	logger.WithComponent("platform").Info("agent bot updated", "bot_id", id)
	response.Success(c, updated)
}

// DeleteAgentBot deletes an agent bot
func (h *PlatformHandler) DeleteAgentBot(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		response.BadRequest(c, "Invalid agent bot ID")
		return
	}

	if err := h.platformRepo.DeleteAgentBot(c.Request.Context(), uint(id)); err != nil {
		logger.WithComponent("platform").Error("failed to delete agent bot", "bot_id", id, "error", err.Error())
		response.NotFound(c, "Agent bot not found")
		return
	}

	logger.WithComponent("platform").Info("agent bot deleted", "bot_id", id)
	response.Success(c, gin.H{"message": "agent bot deleted successfully"})
}

// ListAccountAgentBots returns agent bots applicable to a specific account
func (h *PlatformHandler) ListAccountAgentBots(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	bots, err := h.platformRepo.ListAccountAgentBots(c.Request.Context(), uint(accountID))
	if err != nil {
		logger.WithComponent("platform").Error("failed to list account agent bots", "account_id", accountID, "error", err.Error())
		response.InternalError(c, "Failed to list account agent bots")
		return
	}

	response.Success(c, gin.H{
		"agent_bots": bots,
	})
}

// CreateAccountAgentBot creates an agent bot scoped specifically to an account
func (h *PlatformHandler) CreateAccountAgentBot(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || accountID == 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}

	var req PlatformCreateAgentBotReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	botType := strings.TrimSpace(req.BotType)
	if botType == "" {
		botType = "webhook"
	}

	bot := domain.AgentBot{
		AccountID:   uint(accountID),
		Name:        strings.TrimSpace(req.Name),
		Description: req.Description,
		OutgoingURL: strings.TrimSpace(req.OutgoingURL),
		BotType:     botType,
	}

	if err := h.platformRepo.CreateAgentBot(c.Request.Context(), &bot); err != nil {
		logger.WithComponent("platform").Error("failed to create account agent bot", "account_id", accountID, "error", err.Error())
		response.InternalError(c, "Failed to create agent bot")
		return
	}

	logger.WithComponent("platform").Info("account agent bot created", "bot_id", bot.ID, "account_id", accountID)
	response.Created(c, bot)
}

// DeleteAccountAgentBot removes an agent bot associated with an account
func (h *PlatformHandler) DeleteAccountAgentBot(c *gin.Context) {
	botID, err := strconv.ParseUint(c.Param("agent_bot_id"), 10, 64)
	if err != nil || botID == 0 {
		response.BadRequest(c, "Invalid agent bot ID")
		return
	}

	if err := h.platformRepo.DeleteAgentBot(c.Request.Context(), uint(botID)); err != nil {
		response.NotFound(c, "Agent bot not found")
		return
	}

	response.Success(c, gin.H{"message": "account agent bot removed successfully"})
}
