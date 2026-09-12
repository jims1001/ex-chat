package middleware

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
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

func AuthMiddleware(secret string, userRepo *repository.UserRepository, enterpriseRepos ...repository.ChannelAuthEnterpriseRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			response.Unauthorized(c, "Authorization header is required")
			c.Abort()
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			response.Unauthorized(c, "Invalid authorization header format")
			c.Abort()
			return
		}

		tokenStr := parts[1]
		if len(enterpriseRepos) > 0 && enterpriseRepos[0] != nil {
			revoked, err := enterpriseRepos[0].IsTokenRevoked(tokenStr)
			if err == nil && revoked {
				response.Unauthorized(c, "Token has been revoked")
				c.Abort()
				return
			}
		}

		claims, err := auth.ValidateToken(tokenStr, secret)
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

		c.Set(ContextUserID, claims.UserID)
		c.Set(ContextUser, user)
		c.Set("token_string", tokenStr)
		c.Next()
	}
}

func TenantMiddleware(accountRepo *repository.AccountRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		accountIDStr := c.Param("account_id")
		if accountIDStr == "" {
			response.BadRequest(c, "Account ID is required")
			c.Abort()
			return
		}

		accountID64, err := strconv.ParseUint(accountIDStr, 10, 64)
		if err != nil {
			response.BadRequest(c, "Invalid account ID")
			c.Abort()
			return
		}
		accountID := uint(accountID64)

		rawUserID, exists := c.Get(ContextUserID)
		if !exists {
			response.Unauthorized(c, "User context required")
			c.Abort()
			return
		}
		userID := rawUserID.(uint)

		account, err := accountRepo.FindByID(accountID)
		if err != nil || account == nil {
			response.NotFound(c, "Account not found")
			c.Abort()
			return
		}

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

// RequireRole checks if the authenticated user has one of the allowed roles.
// Users with RoleAdministrator ("administrator") are automatically granted access.
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

		// Super admin always has access
		if membership.Role == domain.RoleAdministrator {
			c.Next()
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

// RequirePermission checks if the authenticated user in the account has the specified permission.
// - Users with RoleAdministrator ("administrator") automatically have all permissions.
// - If the user's role matches any fallbackRoles, access is granted.
// - If the user has a CustomRole (either via CustomRoleID or CustomRole name matching),
//   its Permissions JSON array is checked for the specified permission or "administrator".
// - Otherwise, access is denied with 403 Forbidden.
func RequirePermission(accountRepo *repository.AccountRepository, permission string, fallbackRoles ...string) gin.HandlerFunc {
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

		// 1. Super admin always allowed
		if membership.Role == domain.RoleAdministrator {
			c.Next()
			return
		}

		// 2. Check fallback roles
		for _, r := range fallbackRoles {
			if membership.Role == r {
				c.Next()
				return
			}
		}

		// 3. Check custom role permissions
		if accountRepo != nil {
			hasPerm, err := accountRepo.HasPermission(membership.AccountID, membership.UserID, permission)
			if err == nil && hasPerm {
				c.Next()
				return
			}
		} else if membership.CustomRole != nil {
			var perms []string
			if err := json.Unmarshal([]byte(membership.CustomRole.Permissions), &perms); err == nil {
				for _, p := range perms {
					if p == domain.PermissionAdministrator || p == permission {
						c.Next()
						return
					}
				}
			}
		}

		response.Forbidden(c, "You do not have permission to perform this action")
		c.Abort()
	}
}

// RequireSuperAdmin verifies that the authenticated user possesses super administrator privileges.
// Super administrator access is granted if:
// 1. user.IsSuperAdmin() is true (user.Type == "SuperAdmin" or user.Role == "super_admin")
// 2. OR user's email matches one of the configured super admin emails (cfg.IsSuperAdminEmail)
// If unauthorized, it returns 403 Forbidden with {"success": false, "error": "Super administrator access required"}.
func RequireSuperAdmin(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		rawUser, exists := c.Get(ContextUser)
		if !exists {
			response.Forbidden(c, "Super administrator access required")
			c.Abort()
			return
		}

		user, ok := rawUser.(*domain.User)
		if !ok || user == nil {
			response.Forbidden(c, "Super administrator access required")
			c.Abort()
			return
		}

		if user.IsSuperAdmin() || (cfg != nil && cfg.IsSuperAdminEmail(user.Email)) {
			c.Next()
			return
		}

		response.Forbidden(c, "Super administrator access required")
		c.Abort()
	}
}
