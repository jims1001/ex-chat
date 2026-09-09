package handler

import (
	"net/http"
	"strconv"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

type PlatformHandler struct {
	platformRepo *repository.PlatformRepository
	userRepo     *repository.UserRepository
}

func NewPlatformHandler(p *repository.PlatformRepository, u *repository.UserRepository) *PlatformHandler {
	return &PlatformHandler{
		platformRepo: p,
		userRepo:     u,
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

type PlatformCreateAccountReq struct {
	Name   string `json:"name" binding:"required"`
	Locale string `json:"locale"`
	Domain string `json:"domain"`
}

func (h *PlatformHandler) CreateAccount(c *gin.Context) {
	var req PlatformCreateAccountReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	acc := domain.Account{
		Name:   req.Name,
		Locale: req.Locale,
		Domain: req.Domain,
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

func (h *PlatformHandler) GetAccount(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	acc, err := h.platformRepo.GetAccount(c.Request.Context(), uint(id))
	if err != nil {
		response.NotFound(c, "account not found")
		return
	}
	response.Success(c, acc)
}

type PlatformCreateUserReq struct {
	Name     string `json:"name" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required,min=6"`
	Role     string `json:"role"`
}

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
		Name:         req.Name,
		Email:        req.Email,
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

type PlatformAddAccountUserReq struct {
	UserID uint   `json:"user_id" binding:"required"`
	Role   string `json:"role" binding:"required"`
}

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
