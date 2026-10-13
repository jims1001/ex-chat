package middleware

import (
	"fmt"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/gin-gonic/gin"
)

// RequestLogger intercepts incoming HTTP requests and outputs structured slog events
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		raw := safeRequestQuery(c.Request.URL.Path, c.Request.URL.RawQuery)

		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()
		clientIP := c.ClientIP()
		method := c.Request.Method
		bytesWritten := c.Writer.Size()

		if raw != "" {
			path = path + "?" + raw
		}

		uCtx := foundation.GetUnifiedContext(c)
		accID := c.GetUint("account_id")
		if accID == 0 && uCtx != nil {
			accID = uCtx.AccountID
		}
		userID := c.GetUint("user_id")
		if userID == 0 && uCtx != nil {
			userID = uCtx.ActorID
		}

		ctx := c.Request.Context()
		if uCtx != nil {
			ctx = logger.ContextWithRequestID(ctx, uCtx.RequestID)
			ctx = logger.ContextWithCorrelationID(ctx, uCtx.CorrelationID)
		}
		if accID > 0 {
			ctx = logger.ContextWithAccountID(ctx, accID)
		}
		if userID > 0 {
			ctx = logger.ContextWithUserID(ctx, userID)
		}

		log := logger.WithContext(ctx).With(
			"component", "http",
			"method", method,
			"path", path,
			"status", status,
			"latency_ms", float64(latency.Microseconds())/1000.0,
			"client_ip", clientIP,
			"bytes", bytesWritten,
		)

		msg := fmt.Sprintf("HTTP %s %s -> %d (%s)", method, c.Request.URL.Path, status, latency)

		switch {
		case status >= 500:
			log.Error(msg)
		case status >= 400:
			log.Warn(msg)
		default:
			log.Info(msg)
		}
	}
}

// Reset links carry a credential in their query, including on frontend routes
// handled by a reverse proxy. Never persist it in structured HTTP logs.
func safeRequestQuery(path, raw string) string {
	// Even rejected billing overrides may contain a short-lived portal credential.
	clean := strings.TrimSuffix(path, "/")
	if strings.HasPrefix(clean, "/api/v1/accounts/") || strings.HasPrefix(clean, "/enterprise/api/v1/accounts/") {
		for _, suffix := range []string{"/subscription", "/checkout", "/select_billing_currency"} {
			if strings.HasSuffix(clean, suffix) {
				return ""
			}
		}
	}

	// Rejected count-route parameters can contain private text; log no query.
	if isReportCountPath(strings.TrimSuffix(path, "/")) {
		return ""
	}
	// Config requests can accidentally carry credentials under any query name,
	// including on rejected requests and the router's trailing-slash redirect.
	if strings.TrimSuffix(path, "/") == "/api/v1/super_admin/configs" {
		return ""
	}
	// Invitation endpoints accept proof only in JSON. Even rejected query input
	// must not persist a mailbox bearer token in access logs.
	if strings.Contains(path, "/invitation") || strings.Contains(path, "/agent_invitations") || strings.Contains(path, "/confirmation") {
		return ""
	}
	// The compatibility inventory never logs query/cursor input, including
	// rejected unknown parameters that may contain raw definitions or secrets.
	if strings.HasPrefix(path, "/api/v1/accounts/") && strings.HasSuffix(strings.TrimSuffix(path, "/"), "/automation_rules/compatibility") {
		return ""
	}
	// Saved-filter URL parameters may contain private criteria, including
	// rejected or unknown keys. The handler logs only safe operation metadata.
	if strings.HasPrefix(path, "/api/v1/accounts/") && (strings.HasSuffix(strings.TrimSuffix(path, "/"), "/custom_filters") || strings.Contains(path, "/custom_filters/")) {
		return ""
	}
	if raw == "" {
		return ""
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return ""
	} // malformed encoding must not leak a partial credential
	for key := range values {
		if strings.EqualFold(key, "hub.verify_token") || strings.EqualFold(key, "access_token") || strings.EqualFold(key, "auth_token") || strings.EqualFold(key, "client_secret") ||
			strings.EqualFold(key, "reset_password_token") || strings.EqualFold(key, "confirmation_token") ||
			(strings.Contains(path, "/auth/password") && strings.EqualFold(key, "token")) {
			values.Set(key, "[REDACTED]")
		}
	}
	return values.Encode()
}

// RecoveryLogger catches unhandled panics, logs formatted stack trace and returns 500 JSON
func RecoveryLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				stack := string(debug.Stack())
				uCtx := foundation.GetUnifiedContext(c)
				reqID := ""
				if uCtx != nil {
					reqID = uCtx.RequestID
				}

				ctx := logger.ContextWithRequestID(c.Request.Context(), reqID)
				logger.WithContext(ctx).With(
					"component", "recovery",
					"panic", fmt.Sprint(r),
					"method", c.Request.Method,
					"path", c.Request.URL.Path,
					"client_ip", c.ClientIP(),
				).Error("recovered from panic in HTTP handler", "stack", stack)

				c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
					"error":      "Internal Server Error",
					"request_id": reqID,
				})
			}
		}()
		c.Next()
	}
}
