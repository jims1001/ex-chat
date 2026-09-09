package errors

import (
	"errors"
	"fmt"
	"net/http"
)

// Standard error codes
const (
	CodeBadRequest       = "BAD_REQUEST"
	CodeUnauthorized     = "UNAUTHORIZED"
	CodeForbidden        = "FORBIDDEN"
	CodeNotFound         = "NOT_FOUND"
	CodeConflict         = "CONFLICT"
	CodeValidationFailed = "VALIDATION_FAILED"
	CodeRateLimited      = "RATE_LIMITED"
	CodeInternal         = "INTERNAL_ERROR"
)

// AppError represents a structured application error
type AppError struct {
	Code       string         `json:"code"`
	Message    string         `json:"message"`
	HTTPStatus int            `json:"http_status"`
	Cause      error          `json:"-"`
	Details    map[string]any `json:"details,omitempty"`
}

func (e *AppError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("[%s] %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

func (e *AppError) Unwrap() error {
	return e.Cause
}

// WithDetails attaches metadata details to the error
func (e *AppError) WithDetails(details map[string]any) *AppError {
	e.Details = details
	return e
}

// New creates a new custom AppError
func New(code string, message string, httpStatus int) *AppError {
	return &AppError{
		Code:       code,
		Message:    message,
		HTTPStatus: httpStatus,
	}
}

// Wrap wraps an existing error into an AppError
func Wrap(cause error, code string, message string, httpStatus int) *AppError {
	return &AppError{
		Code:       code,
		Message:    message,
		HTTPStatus: httpStatus,
		Cause:      cause,
	}
}

// NewBadRequest creates a 400 Bad Request error
func NewBadRequest(message string, cause ...error) *AppError {
	var c error
	if len(cause) > 0 {
		c = cause[0]
	}
	return &AppError{
		Code:       CodeBadRequest,
		Message:    message,
		HTTPStatus: http.StatusBadRequest,
		Cause:      c,
	}
}

// NewUnauthorized creates a 401 Unauthorized error
func NewUnauthorized(message string, cause ...error) *AppError {
	var c error
	if len(cause) > 0 {
		c = cause[0]
	}
	return &AppError{
		Code:       CodeUnauthorized,
		Message:    message,
		HTTPStatus: http.StatusUnauthorized,
		Cause:      c,
	}
}

// NewForbidden creates a 403 Forbidden error
func NewForbidden(message string, cause ...error) *AppError {
	var c error
	if len(cause) > 0 {
		c = cause[0]
	}
	return &AppError{
		Code:       CodeForbidden,
		Message:    message,
		HTTPStatus: http.StatusForbidden,
		Cause:      c,
	}
}

// NewNotFound creates a 404 Not Found error
func NewNotFound(resource string, id any) *AppError {
	return &AppError{
		Code:       CodeNotFound,
		Message:    fmt.Sprintf("%s with identifier '%v' not found", resource, id),
		HTTPStatus: http.StatusNotFound,
	}
}

// NewConflict creates a 409 Conflict error
func NewConflict(message string, cause ...error) *AppError {
	var c error
	if len(cause) > 0 {
		c = cause[0]
	}
	return &AppError{
		Code:       CodeConflict,
		Message:    message,
		HTTPStatus: http.StatusConflict,
		Cause:      c,
	}
}

// NewValidationFailed creates a 422 Unprocessable Entity error
func NewValidationFailed(message string, details map[string]any) *AppError {
	return &AppError{
		Code:       CodeValidationFailed,
		Message:    message,
		HTTPStatus: http.StatusUnprocessableEntity,
		Details:    details,
	}
}

// NewRateLimited creates a 429 Too Many Requests error
func NewRateLimited(message string) *AppError {
	return &AppError{
		Code:       CodeRateLimited,
		Message:    message,
		HTTPStatus: http.StatusTooManyRequests,
	}
}

// NewInternal creates a 500 Internal Server Error
func NewInternal(message string, cause ...error) *AppError {
	var c error
	if len(cause) > 0 {
		c = cause[0]
	}
	return &AppError{
		Code:       CodeInternal,
		Message:    message,
		HTTPStatus: http.StatusInternalServerError,
		Cause:      c,
	}
}

// FromError converts a generic error to *AppError
func FromError(err error) *AppError {
	if err == nil {
		return nil
	}
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	return NewInternal(err.Error(), err)
}
