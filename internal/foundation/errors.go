package foundation

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// API-10 规范错误代码
const (
	ErrCodeUnauthenticated   = "unauthenticated"
	ErrCodeUnauthorized      = "unauthorized"
	ErrCodeRecordNotFound    = "record_not_found"
	ErrCodeRecordInvalid     = "record_invalid"
	ErrCodeConflict          = "conflict"
	ErrCodeRateLimited       = "rate_limited"
	ErrCodeInternalError     = "internal_error"
	ErrCodeBadRequest        = "bad_request"
	ErrCodeIdempotencyLocked = "idempotency_locked"
)

type APIError struct {
	Code       string         `json:"code"`
	Message    string         `json:"message"`
	Details    map[string]any `json:"details,omitempty"`
	RetryAfter int            `json:"retry_after,omitempty"`
	RequestID  string         `json:"request_id,omitempty"`
}

type ErrorEnvelope struct {
	Error APIError `json:"error"`
}

func RespondError(c *gin.Context, statusCode int, code string, message string, details map[string]any) {
	uCtx := GetUnifiedContext(c)

	errResp := ErrorEnvelope{
		Error: APIError{
			Code:      code,
			Message:   message,
			Details:   details,
			RequestID: uCtx.RequestID,
		},
	}

	c.AbortWithStatusJSON(statusCode, errResp)
}

func RespondBadRequest(c *gin.Context, message string) {
	RespondError(c, http.StatusBadRequest, ErrCodeBadRequest, message, nil)
}

func RespondUnauthenticated(c *gin.Context, message string) {
	RespondError(c, http.StatusUnauthorized, ErrCodeUnauthenticated, message, nil)
}

func RespondForbidden(c *gin.Context, message string) {
	RespondError(c, http.StatusForbidden, ErrCodeUnauthorized, message, nil)
}

func RespondNotFound(c *gin.Context, message string) {
	RespondError(c, http.StatusNotFound, ErrCodeRecordNotFound, message, nil)
}

func RespondConflict(c *gin.Context, message string) {
	RespondError(c, http.StatusConflict, ErrCodeConflict, message, nil)
}

func RespondInternalError(c *gin.Context, message string) {
	RespondError(c, http.StatusInternalServerError, ErrCodeInternalError, message, nil)
}
