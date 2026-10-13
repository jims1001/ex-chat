package middleware

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/gin-gonic/gin"
)

// These adapters reuse the existing authority checks unchanged. Only the new
// count roots receive per-request clones of the original owner handles so early
// reads honor the deadline without logging SQL, bearer tokens or driver text.
func ReportCountAuthAdapter(secret string, users *repository.UserRepository, iam repository.ChannelIAMRepository, legacy gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isReportCountRoute(c) {
			legacy(c)
			return
		}
		scoped, err := repository.ReportCountIAMRead(c.Request.Context(), iam)
		if err != nil {
			AbortReportCountError(c, 503, "authentication_unavailable", "external", "Authentication is temporarily unavailable", nil, true, "authentication")
			return
		}
		AuthMiddleware(secret, users.ReportCountUserRead(c.Request.Context()), scoped)(c)
	}
}
func ReportCountTenantAdapter(accounts *repository.AccountRepository, legacy gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isReportCountRoute(c) {
			legacy(c)
			return
		}
		TenantMiddleware(accounts.ReportCountAccountRead(c.Request.Context()))(c)
	}
}
func ReportCountPermissionAdapter(accounts *repository.AccountRepository, legacy gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isReportCountRoute(c) {
			legacy(c)
			return
		}
		RequirePermission(accounts.ReportCountAccountRead(c.Request.Context()), domain.PermissionReportView)(c)
	}
}
