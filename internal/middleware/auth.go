package middleware

import (
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

const (
	ContextUserID            = "user_id"
	ContextUser              = "user"
	ContextAccountID         = "account_id"
	ContextAccount           = "account"
	ContextAccountMembership = "account_membership"
)

func AuthMiddleware(secret string, userRepo *repository.UserRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			response.Unauthorized(c, "Authorization header is required")
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
			response.Unauthorized(c, "Authorization format must be Bearer <token>")
			c.Abort()
			return
		}

		tokenString := parts[1]
		claims, err := auth.ValidateToken(tokenString, secret)
		if err != nil {
			response.Unauthorized(c, "Invalid or expired token")
			c.Abort()
			return
		}

		user, err := userRepo.FindByID(claims.UserID)
		if err != nil || user == nil {
			response.Unauthorized(c, "User not found")
			c.Abort()
			return
		}

		c.Set(ContextUserID, user.ID)
		c.Set(ContextUser, user)
		c.Next()
	}
}

func TenantMiddleware(accountRepo *repository.AccountRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		accountIDParam := c.Param("account_id")
		if accountIDParam == "" {
			response.BadRequest(c, "account_id parameter is required")
			c.Abort()
			return
		}

		accountIDVal, err := strconv.ParseUint(accountIDParam, 10, 64)
		if err != nil {
			response.BadRequest(c, "invalid account_id format")
			c.Abort()
			return
		}
		accountID := uint(accountIDVal)

		account, err := accountRepo.FindByID(accountID)
		if err != nil || account == nil {
			response.NotFound(c, "Account not found")
			c.Abort()
			return
		}

		rawUserID, exists := c.Get(ContextUserID)
		if !exists {
			response.Unauthorized(c, "Authentication required")
			c.Abort()
			return
		}
		userID := rawUserID.(uint)

		membership, err := accountRepo.GetMembership(accountID, userID)
		if err != nil || membership == nil {
			response.Forbidden(c, "You do not have access to this account")
			c.Abort()
			return
		}

		c.Set(ContextAccountID, accountID)
		c.Set(ContextAccount, account)
		c.Set(ContextAccountMembership, membership)
		c.Next()
	}
}

func RequireRole(allowedRoles ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		rawMembership, exists := c.Get(ContextAccountMembership)
		if !exists {
			response.Forbidden(c, "Account membership required")
			c.Abort()
			return
		}

		membership, ok := rawMembership.(*domain.AccountUser)
		if !ok || membership == nil {
			response.Forbidden(c, "Invalid account membership")
			c.Abort()
			return
		}

		allowed := false
		for _, role := range allowedRoles {
			if membership.Role == role {
				allowed = true
				break
			}
		}

		if !allowed {
			response.Forbidden(c, "You do not have permission to perform this action")
			c.Abort()
			return
		}

		c.Next()
	}
}
