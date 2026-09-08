package handler

import (
	"encoding/json"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

type AuthHandler struct {
	cfg            *config.Config
	userRepo       *repository.UserRepository
	accountRepo    *repository.AccountRepository
	enterpriseRepo repository.ChannelAuthEnterpriseRepository
}

func NewAuthHandler(cfg *config.Config, userRepo *repository.UserRepository, accountRepo *repository.AccountRepository) *AuthHandler {
	return &AuthHandler{
		cfg:         cfg,
		userRepo:    userRepo,
		accountRepo: accountRepo,
	}
}

func (h *AuthHandler) SetEnterpriseRepo(enterpriseRepo repository.ChannelAuthEnterpriseRepository) {
	h.enterpriseRepo = enterpriseRepo
}

type SignUpRequest struct {
	Name        string `json:"name" binding:"required"`
	Email       string `json:"email" binding:"required,email"`
	Password    string `json:"password" binding:"required,min=6"`
	AccountName string `json:"account_name"`
}

type SignInRequest struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
	MFAOTP   string `json:"mfa_otp"`
}

type AvailabilityRequest struct {
	Availability string `json:"availability" binding:"required"`
}

func (h *AuthHandler) SignUp(c *gin.Context) {
	var req SignUpRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	existingUser, err := h.userRepo.FindByEmail(req.Email)
	if err != nil {
		response.InternalError(c, "Failed to check existing email")
		return
	}
	if existingUser != nil {
		response.BadRequest(c, "Email already registered")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		response.InternalError(c, "Failed to process password")
		return
	}

	user := domain.User{
		Name:         req.Name,
		Email:        req.Email,
		PasswordHash: hash,
		Role:         domain.RoleAdministrator,
		Availability: domain.AvailabilityOnline,
	}

	if err := h.userRepo.Create(&user); err != nil {
		response.InternalError(c, "Failed to create user account")
		return
	}

	// Create initial workspace account for new admin
	accountName := req.AccountName
	if accountName == "" {
		accountName = user.Name + "'s Workspace"
	}
	account := domain.Account{
		Name:   accountName,
		Locale: "en",
	}
	if err := h.accountRepo.Create(&account); err == nil {
		_ = h.accountRepo.AddMember(account.ID, user.ID, domain.RoleAdministrator)
	}

	token, err := auth.GenerateToken(&user, h.cfg.JWTSecret, h.cfg.JWTExpirationHours)
	if err != nil {
		response.InternalError(c, "Failed to generate authentication token")
		return
	}

	accounts, _ := h.accountRepo.ListAccountsForUser(user.ID)

	response.Created(c, gin.H{
		"user":     user,
		"token":    token,
		"accounts": accounts,
	})
}

func (h *AuthHandler) SignIn(c *gin.Context) {
	var req SignInRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	user, err := h.userRepo.FindByEmail(req.Email)
	if err != nil || user == nil {
		response.Unauthorized(c, "Invalid email or password")
		return
	}

	if !auth.CheckPasswordHash(req.Password, user.PasswordHash) {
		response.Unauthorized(c, "Invalid email or password")
		return
	}

	// Check if user has MFA enabled
	if h.enterpriseRepo != nil {
		profile, err := h.enterpriseRepo.GetMFAProfile(user.ID)
		if err == nil && profile != nil && profile.Enabled {
			if req.MFAOTP == "" {
				// Block login: prompt client for MFA token
				response.Unauthorized(c, "Multi-factor authentication required")
				return
			}
			// Verify MFA TOTP code or backup code
			valid := auth.VerifyTOTPCode(profile.Secret, req.MFAOTP)
			if !valid {
				var backupCodes []string
				_ = json.Unmarshal([]byte(profile.BackupCodes), &backupCodes)
				for i, code := range backupCodes {
					if code == req.MFAOTP {
						valid = true
						backupCodes = append(backupCodes[:i], backupCodes[i+1:]...)
						bBytes, _ := json.Marshal(backupCodes)
						profile.BackupCodes = string(bBytes)
						_ = h.enterpriseRepo.SaveMFAProfile(profile)
						break
					}
				}
			}
			if !valid {
				response.Unauthorized(c, "Invalid MFA verification code")
				return
			}
		}
	}

	token, err := auth.GenerateToken(user, h.cfg.JWTSecret, h.cfg.JWTExpirationHours)
	if err != nil {
		response.InternalError(c, "Failed to generate authentication token")
		return
	}

	accounts, _ := h.accountRepo.ListAccountsForUser(user.ID)

	response.Success(c, gin.H{
		"user":     user,
		"token":    token,
		"accounts": accounts,
	})
}

func (h *AuthHandler) Profile(c *gin.Context) {
	rawUser, exists := c.Get(middleware.ContextUser)
	if !exists {
		response.Unauthorized(c, "User not found in context")
		return
	}

	user := rawUser.(*domain.User)
	accounts, _ := h.accountRepo.ListAccountsForUser(user.ID)

	response.Success(c, gin.H{
		"user":     user,
		"accounts": accounts,
	})
}

func (h *AuthHandler) UpdateAvailability(c *gin.Context) {
	rawUserID, exists := c.Get(middleware.ContextUserID)
	if !exists {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	userID := rawUserID.(uint)

	var req AvailabilityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid availability status")
		return
	}

	if req.Availability != domain.AvailabilityOnline &&
		req.Availability != domain.AvailabilityOffline &&
		req.Availability != domain.AvailabilityBusy {
		response.BadRequest(c, "Availability must be online, offline, or busy")
		return
	}

	if err := h.userRepo.UpdateAvailability(userID, req.Availability); err != nil {
		response.InternalError(c, "Failed to update availability")
		return
	}

	user, _ := h.userRepo.FindByID(userID)
	response.Success(c, user)
}

type UpdateProfileRequest struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	AvatarURL   string `json:"avatar_url"`
}

func (h *AuthHandler) UpdateProfile(c *gin.Context) {
	rawUserID, exists := c.Get(middleware.ContextUserID)
	if !exists {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	userID := rawUserID.(uint)

	var req UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid profile payload: "+err.Error())
		return
	}

	user, err := h.userRepo.FindByID(userID)
	if err != nil || user == nil {
		response.NotFound(c, "User not found")
		return
	}

	if strings.TrimSpace(req.Name) != "" {
		user.Name = strings.TrimSpace(req.Name)
	}
	if strings.TrimSpace(req.DisplayName) != "" {
		user.Name = strings.TrimSpace(req.DisplayName)
	}
	if req.AvatarURL != "" {
		user.AvatarURL = req.AvatarURL
	}

	if err := h.userRepo.Update(user); err != nil {
		response.InternalError(c, "Failed to update profile")
		return
	}

	accounts, _ := h.accountRepo.ListAccountsForUser(user.ID)
	response.Success(c, gin.H{
		"user":     user,
		"accounts": accounts,
	})
}
