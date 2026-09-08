package handler

import (
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

type AccountHandler struct {
	accountRepo *repository.AccountRepository
	userRepo    *repository.UserRepository
}

func NewAccountHandler(accountRepo *repository.AccountRepository, userRepo *repository.UserRepository) *AccountHandler {
	return &AccountHandler{
		accountRepo: accountRepo,
		userRepo:    userRepo,
	}
}

type CreateAccountRequest struct {
	Name   string `json:"name" binding:"required"`
	Locale string `json:"locale"`
	Domain string `json:"domain"`
}

type AddAgentRequest struct {
	Name     string `json:"name" binding:"required"`
	Email    string `json:"email" binding:"required,email"`
	Role     string `json:"role"`
	Password string `json:"password"`
}

func (h *AccountHandler) CreateAccount(c *gin.Context) {
	rawUserID, _ := c.Get(middleware.ContextUserID)
	userID := rawUserID.(uint)

	var req CreateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	locale := req.Locale
	if locale == "" {
		locale = "en"
	}

	account := domain.Account{
		Name:   req.Name,
		Locale: locale,
		Domain: req.Domain,
	}

	if err := h.accountRepo.Create(&account); err != nil {
		response.InternalError(c, "Failed to create account")
		return
	}

	if err := h.accountRepo.AddMember(account.ID, userID, domain.RoleAdministrator); err != nil {
		response.InternalError(c, "Failed to link account administrator")
		return
	}

	response.Created(c, account)
}

func (h *AccountHandler) GetAccount(c *gin.Context) {
	rawAccount, exists := c.Get(middleware.ContextAccount)
	if !exists {
		response.NotFound(c, "Account not found")
		return
	}
	response.Success(c, rawAccount)
}

func (h *AccountHandler) UpdateAccount(c *gin.Context) {
	rawAccount, exists := c.Get(middleware.ContextAccount)
	if !exists {
		response.NotFound(c, "Account not found")
		return
	}
	account := rawAccount.(*domain.Account)

	var req CreateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	if req.Name != "" {
		account.Name = req.Name
	}
	if req.Locale != "" {
		account.Locale = req.Locale
	}
	if req.Domain != "" {
		account.Domain = req.Domain
	}

	if err := h.accountRepo.Update(account); err != nil {
		response.InternalError(c, "Failed to update account")
		return
	}

	response.Success(c, account)
}

func (h *AccountHandler) ListAgents(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	members, err := h.accountRepo.ListMembers(accountID)
	if err != nil {
		response.InternalError(c, "Failed to list agents")
		return
	}

	response.Success(c, members)
}

func (h *AccountHandler) AddAgent(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	var req AddAgentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	role := req.Role
	if role == "" {
		role = domain.RoleAgent
	}

	req.Email = strings.ToLower(strings.TrimSpace(req.Email))

	user, err := h.userRepo.FindByEmail(req.Email)
	if err != nil {
		response.InternalError(c, "Failed to check user")
		return
	}

	if user == nil {
		pwd := req.Password
		if pwd == "" {
			pwd = "password123"
		}
		hash, err := auth.HashPassword(pwd)
		if err != nil {
			response.InternalError(c, "Failed to process password")
			return
		}
		newUser := domain.User{
			Name:         req.Name,
			Email:        req.Email,
			PasswordHash: hash,
			Role:         role,
			Availability: domain.AvailabilityOnline,
		}
		if err := h.userRepo.Create(&newUser); err != nil {
			response.InternalError(c, "Failed to create user for agent")
			return
		}
		user = &newUser
	}

	// Check if already member
	existingMembership, _ := h.accountRepo.GetMembership(accountID, user.ID)
	if existingMembership != nil {
		response.BadRequest(c, "Agent already exists in this account")
		return
	}

	if err := h.accountRepo.AddMember(accountID, user.ID, role); err != nil {
		response.InternalError(c, "Failed to add agent to account")
		return
	}

	membership, _ := h.accountRepo.GetMembership(accountID, user.ID)
	membership.User = user
	response.Created(c, membership)
}
