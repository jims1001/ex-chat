package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

type AuthHandler struct {
	cfg            *config.Config
	userRepo       *repository.UserRepository
	accountRepo    *repository.AccountRepository
	enterpriseRepo repository.ChannelIAMRepository
	emailService   *service.EmailService
}

func NewAuthHandler(cfg *config.Config, userRepo *repository.UserRepository, accountRepo *repository.AccountRepository) *AuthHandler {
	return &AuthHandler{
		cfg:         cfg,
		userRepo:    userRepo,
		accountRepo: accountRepo,
	}
}

func (h *AuthHandler) SetEnterpriseRepo(enterpriseRepo repository.ChannelIAMRepository) {
	h.enterpriseRepo = enterpriseRepo
}

func (h *AuthHandler) SetEmailService(emailService *service.EmailService) {
	h.emailService = emailService
}

type SignUpRequest struct {
	Name        string `json:"name" binding:"required"`
	Email       string `json:"email" binding:"required,email"`
	Password    string `json:"password" binding:"required,min=6"`
	AccountName string `json:"account_name"`
}

type SignInRequest struct {
	Email      string `json:"email" binding:"required,email"`
	Password   string `json:"password" binding:"required"`
	MFAOTP     string `json:"mfa_otp"`
	OTPCode    string `json:"otp_code"`
	BackupCode string `json:"backup_code"`
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

	logger.WithComponent("auth").Info("new user registered",
		"user_id", user.ID,
		"email", user.Email,
		"account_id", account.ID,
		"client_ip", c.ClientIP(),
	)

	confirmationStatus := h.dispatchConfirmation(c.Request.Context(), user.ID)

	response.Created(c, gin.H{
		"user":                user,
		"token":               token,
		"accounts":            accounts,
		"confirmation_status": confirmationStatus,
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
		logger.WithComponent("auth").Warn("sign-in failed: user not found", "email", req.Email, "client_ip", c.ClientIP())
		response.Unauthorized(c, "Invalid email or password")
		return
	}

	if !auth.CheckPasswordHash(req.Password, user.PasswordHash) {
		logger.WithComponent("auth").Warn("sign-in failed: invalid password", "user_id", user.ID, "email", req.Email, "client_ip", c.ClientIP())
		response.Unauthorized(c, "Invalid email or password")
		return
	}

	// Check if user has MFA enabled
	if h.enterpriseRepo != nil {
		profile, err := h.enterpriseRepo.GetMFAProfile(user.ID)
		if err != nil {
			response.InternalError(c, "Failed to check MFA configuration")
			return
		}
		if profile != nil && profile.Enabled {
			otp := req.MFAOTP
			if otp == "" {
				otp = req.OTPCode
			}
			if otp == "" {
				otp = req.BackupCode
			}

			if otp == "" {
				// Block login: prompt client for MFA token
				mfaToken, err := auth.GenerateMFAToken(user, h.cfg.JWTSecret)
				if err != nil {
					response.InternalError(c, "Failed to generate MFA session token")
					return
				}
				logger.WithComponent("auth").Info("sign-in mfa challenge required", "user_id", user.ID, "email", req.Email)
				c.JSON(http.StatusPartialContent, gin.H{
					"success":      false,
					"mfa_required": true,
					"mfa_token":    mfaToken,
					"message":      "Multi-factor authentication required",
					"data": gin.H{
						"mfa_required": true,
						"mfa_token":    mfaToken,
					},
				})
				return
			}

			// Password was verified above; all second-factor state is persisted
			// atomically so other instances cannot bypass limits or reuse a code.
			if err := h.enterpriseRepo.VerifyMFALogin(user.ID, otp, "", time.Time{}); err != nil {
				respondMFAVerificationError(c, err)
				return
			}
		}
	}

	token, err := auth.GenerateToken(user, h.cfg.JWTSecret, h.cfg.JWTExpirationHours)
	if err != nil {
		logger.WithComponent("auth").Error("failed to generate auth token", "user_id", user.ID, "error", err.Error())
		response.InternalError(c, "Failed to generate authentication token")
		return
	}

	accounts, _ := h.accountRepo.ListAccountsForUser(user.ID)

	logger.WithComponent("auth").Info("user signed in successfully",
		"user_id", user.ID,
		"email", user.Email,
		"client_ip", c.ClientIP(),
	)

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
		user.DisplayName = strings.TrimSpace(req.DisplayName)
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

// SignOut invalidates the user's current token and clears the session
func (h *AuthHandler) SignOut(c *gin.Context) {
	tokenStr := c.GetString("token_string")
	if tokenStr == "" {
		authHeader := c.GetHeader("Authorization")
		parts := strings.Split(authHeader, " ")
		if len(parts) == 2 && parts[0] == "Bearer" {
			tokenStr = parts[1]
		}
	}

	rawUserID, exists := c.Get(middleware.ContextUserID)
	var userID uint
	if exists {
		userID = rawUserID.(uint)
	}

	if tokenStr != "" && h.enterpriseRepo != nil {
		expiresAt := time.Now().Add(time.Duration(h.cfg.JWTExpirationHours) * time.Hour)
		claims, err := auth.ValidateToken(tokenStr, h.cfg.JWTSecret)
		if err == nil && claims != nil {
			if claims.UserID != 0 {
				userID = claims.UserID
			}
			if claims.ExpiresAt != nil {
				expiresAt = claims.ExpiresAt.Time
			}
		}
		_ = h.enterpriseRepo.RevokeToken(tokenStr, userID, expiresAt)
	}

	response.Success(c, gin.H{
		"success": true,
		"message": "signed out successfully",
	})
}

// ValidateToken returns the currently authenticated user's information and session status
func (h *AuthHandler) ValidateToken(c *gin.Context) {
	rawUser, exists := c.Get(middleware.ContextUser)
	if !exists {
		response.Unauthorized(c, "User not found in context")
		return
	}

	user := rawUser.(*domain.User)
	accounts, _ := h.accountRepo.ListAccountsForUser(user.ID)

	response.Success(c, gin.H{
		"success":  true,
		"user":     user,
		"data":     user,
		"accounts": accounts,
	})
}

type ConfirmEmailRequest struct {
	ConfirmationToken string `json:"confirmation_token"`
}

// ConfirmEmail verifies the user's email address using a confirmation token
func (h *AuthHandler) ConfirmEmail(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	token := c.Query("confirmation_token")
	if token == "" {
		var req ConfirmEmailRequest
		_ = c.ShouldBindJSON(&req)
		token = req.ConfirmationToken
	}

	if token == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"message": "Invalid token",
		})
		return
	}

	if h.enterpriseRepo == nil {
		response.InternalError(c, "Enterprise auth not configured")
		return
	}

	user, err := h.enterpriseRepo.ConfirmUserByToken(token)
	if err != nil || user == nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"message": "Invalid token or already confirmed",
		})
		return
	}

	// Mailbox proof does not bypass the user's current second-factor policy.
	profile, err := h.enterpriseRepo.GetMFAProfile(user.ID)
	if err != nil {
		response.InternalError(c, "Email confirmed; sign in to continue")
		return
	}
	if profile != nil && profile.Enabled {
		challenge, err := auth.GenerateMFAToken(user, h.cfg.JWTSecret)
		if err != nil {
			response.InternalError(c, "Email confirmed; sign in to continue")
			return
		}
		c.JSON(http.StatusPartialContent, gin.H{
			"success": false, "mfa_required": true, "mfa_token": challenge,
			"message": "Email confirmed; multi-factor authentication required",
			"data":    gin.H{"mfa_required": true, "mfa_token": challenge},
		})
		return
	}
	tokenStr, err := auth.GenerateToken(user, h.cfg.JWTSecret, h.cfg.JWTExpirationHours)
	if err != nil {
		response.InternalError(c, "Email confirmed; sign in to continue")
		return
	}
	accounts, _ := h.accountRepo.ListAccountsForUser(user.ID)

	response.Success(c, gin.H{
		"message":  "Email confirmed successfully",
		"user":     user,
		"token":    tokenStr,
		"accounts": accounts,
	})
}

type ResendConfirmationRequest struct {
	Email string `json:"email"`
}

// ResendConfirmation processes anonymous requests without claiming delivery or
// revealing eligibility. Authenticated callers may see their own attempt status.
func (h *AuthHandler) ResendConfirmation(c *gin.Context) {
	if h.enterpriseRepo == nil || h.emailService == nil || h.emailService.CheckConfirmationConfiguration() != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"message": "Email confirmation is unavailable"})
		return
	}
	var user *domain.User
	authenticated := false
	if rawUserID, exists := c.Get(middleware.ContextUserID); exists {
		authenticated = true
		user, _ = h.userRepo.FindByID(rawUserID.(uint))
	} else {
		var req ResendConfirmationRequest
		_ = c.ShouldBindJSON(&req)
		if req.Email != "" {
			user, _ = h.userRepo.FindByEmail(strings.ToLower(strings.TrimSpace(req.Email)))
		}
	}
	status := "unavailable"
	if user != nil {
		if user.IsConfirmed() {
			status = "already_confirmed"
		} else {
			status = h.dispatchConfirmation(c.Request.Context(), user.ID)
		}
	}
	data := gin.H{"message": "Confirmation request processed"}
	if authenticated {
		data["confirmation_status"] = status
	}
	response.Success(c, data)
}

// Synchronous, bounded submission keeps completion tied to this exact proof.
// No detached goroutine or generic email retry can later activate an old link.
// Signup/account creation remains independent of optional email delivery.
func (h *AuthHandler) dispatchConfirmation(ctx context.Context, userID uint) string {
	if h.enterpriseRepo == nil || h.emailService == nil || h.emailService.CheckConfirmationConfiguration() != nil {
		return "unavailable"
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return "unavailable"
	}
	proof, raw, err := h.enterpriseRepo.GenerateConfirmationToken(userID)
	if err != nil || proof == nil || proof.ConfirmationExpiresAt == nil {
		return "unavailable"
	}
	state, err := h.emailService.SendConfirmationEmail(ctx, proof, raw, *proof.ConfirmationExpiresAt)
	if err != nil || (state != "submitted" && state != "captured_mock") {
		if state == "delivery_unknown" {
			return state
		}
		return "unavailable"
	}
	// An expired request after provider acceptance is ambiguous. Never activate
	// it based on a late sender response after the request has been canceled.
	if ctx.Err() != nil {
		return "delivery_unknown"
	}
	if err := h.enterpriseRepo.ActivateConfirmationToken(proof.ID, raw); err != nil {
		return "inactive"
	}
	return state
}

// UploadAvatar updates the authenticated user's avatar via file upload or URL
func (h *AuthHandler) UploadAvatar(c *gin.Context) {
	rawUserID, exists := c.Get(middleware.ContextUserID)
	if !exists {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	userID := rawUserID.(uint)

	user, err := h.userRepo.FindByID(userID)
	if err != nil || user == nil {
		response.NotFound(c, "User not found")
		return
	}

	var avatarURL string
	// Multipart file upload
	file, err := c.FormFile("avatar")
	if err == nil && file != nil {
		uploadDir := "public/uploads/avatars"
		_ = os.MkdirAll(uploadDir, 0755)
		filename := fmt.Sprintf("avatar_%d_%d%s", userID, time.Now().UnixNano(), filepath.Ext(file.Filename))
		targetPath := filepath.Join(uploadDir, filename)
		if err := c.SaveUploadedFile(file, targetPath); err == nil {
			avatarURL = "/" + targetPath
		}
	}

	if avatarURL == "" {
		if formURL := c.PostForm("avatar_url"); formURL != "" {
			avatarURL = formURL
		}
	}

	if avatarURL == "" {
		var req struct {
			AvatarURL string `json:"avatar_url"`
		}
		_ = c.ShouldBindJSON(&req)
		if req.AvatarURL != "" {
			avatarURL = req.AvatarURL
		}
	}

	if avatarURL != "" {
		user.AvatarURL = avatarURL
		_ = h.userRepo.Update(user)
	}

	response.Success(c, user)
}

// DeleteAvatar clears the authenticated user's avatar
func (h *AuthHandler) DeleteAvatar(c *gin.Context) {
	rawUserID, exists := c.Get(middleware.ContextUserID)
	if !exists {
		response.Unauthorized(c, "User not authenticated")
		return
	}
	userID := rawUserID.(uint)

	user, err := h.userRepo.FindByID(userID)
	if err != nil || user == nil {
		response.NotFound(c, "User not found")
		return
	}

	user.AvatarURL = ""
	if err := h.userRepo.Update(user); err != nil {
		response.InternalError(c, "Failed to remove avatar")
		return
	}

	response.Success(c, user)
}

type VerifyMFALoginRequest struct {
	MFAToken   string `json:"mfa_token" binding:"required"`
	OTPCode    string `json:"otp_code"`
	BackupCode string `json:"backup_code"`
}

// VerifyMFAForLogin authenticates with a secondary MFA OTP code or backup code
func (h *AuthHandler) VerifyMFAForLogin(c *gin.Context) {
	var req VerifyMFALoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	claims, err := auth.ValidateMFAToken(req.MFAToken, h.cfg.JWTSecret)
	if err != nil {
		response.Unauthorized(c, "Invalid or expired MFA session token")
		return
	}

	user, err := h.userRepo.FindByID(claims.UserID)
	if err != nil || !auth.ClaimsMatchUser(claims, user) {
		response.Unauthorized(c, "Invalid or revoked MFA session")
		return
	}

	if h.enterpriseRepo == nil {
		response.InternalError(c, "Enterprise auth not configured")
		return
	}

	code := req.OTPCode
	if code == "" {
		code = req.BackupCode
	}
	if err := h.enterpriseRepo.VerifyMFALogin(user.ID, code, claims.ID, claims.ExpiresAt.Time); err != nil {
		respondMFAVerificationError(c, err)
		return
	}

	token, err := auth.GenerateToken(user, h.cfg.JWTSecret, h.cfg.JWTExpirationHours)
	if err != nil {
		response.InternalError(c, "Failed to generate token")
		return
	}

	accounts, _ := h.accountRepo.ListAccountsForUser(user.ID)
	response.Success(c, gin.H{
		"user":     user,
		"token":    token,
		"accounts": accounts,
	})
}

// Authentication errors intentionally do not reveal which factor was accepted.
func respondMFAVerificationError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, repository.ErrMFARateLimited):
		c.Header("Retry-After", "300")
		response.Error(c, http.StatusTooManyRequests, "Too many MFA verification attempts; try again later")
	case errors.Is(err, repository.ErrMFAInvalidCode), errors.Is(err, repository.ErrMFAReplay), errors.Is(err, repository.ErrMFANotEnabled):
		response.Unauthorized(c, "Invalid or already used MFA verification")
	default:
		response.InternalError(c, "Unable to verify MFA securely")
	}
}
