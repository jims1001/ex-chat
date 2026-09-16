package middleware

import (
	"net/http"

	"github.com/OracleBetX-Projects/ex-chat/pkg/ratelimit"
	"github.com/gin-gonic/gin"
)

// IPRateLimit returns a middleware that limits requests based on client IP
func IPRateLimit(limiter *ratelimit.Limiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		clientIP := c.ClientIP()
		if clientIP == "" {
			clientIP = "127.0.0.1"
		}

		if !limiter.Allow(clientIP) {
			c.Header("Retry-After", "5")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":   "rate limit exceeded",
				"message": "Too many requests, please slow down and try again shortly",
			})
			return
		}
		c.Next()
	}
}
