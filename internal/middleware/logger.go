package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"
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
		raw := c.Request.URL.RawQuery

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
