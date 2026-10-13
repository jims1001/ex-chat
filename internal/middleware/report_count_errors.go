package middleware

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/gin-gonic/gin"
)

const ReportCountReadTimeout = 5 * time.Second
const reportCountMaxResponseBytes = 64 * 1024
const reportCountErrorKey = "report_count_error"

type ReportCountFieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

type ReportCountAPIError struct {
	Code          string                  `json:"code"`
	Category      string                  `json:"category"`
	Message       string                  `json:"message"`
	FieldErrors   []ReportCountFieldError `json:"field_errors"`
	RequestID     string                  `json:"request_id"`
	CorrelationID string                  `json:"correlation_id"`
	Retryable     bool                    `json:"retryable"`
	Dependency    string                  `json:"dependency,omitempty"`
	RetryAfter    int                     `json:"retry_after,omitempty"`
}

// Exact two registered GET roots only; legacy endpoints retain their contracts.
func isReportCountRoute(c *gin.Context) bool {
	return c.Request.Method == http.MethodGet && (c.FullPath() == "/api/v1/accounts/:account_id/reports" || c.FullPath() == "/api/v2/accounts/:account_id/reports")
}

func isReportCountPath(path string) bool {
	p := strings.Split(path, "/")
	return len(p) == 6 && p[1] == "api" && (p[2] == "v1" || p[2] == "v2") && p[3] == "accounts" && p[4] != "" && p[5] == "reports"
}

func AbortReportCountError(c *gin.Context, status int, code, category, message string, fields []ReportCountFieldError, retryable bool, dependency string) {
	if fields == nil {
		fields = []ReportCountFieldError{}
	}
	c.Set(reportCountErrorKey, ReportCountAPIError{Code: code, Category: category, Message: message, FieldErrors: fields, Retryable: retryable, Dependency: dependency})
	if w, ok := c.Writer.(*reportCountWriter); ok {
		w.status = status
	}
	c.AbortWithStatus(status)
}

func defaultReportCountError(status int) ReportCountAPIError {
	e := ReportCountAPIError{Code: "internal_error", Category: "internal", Message: "Unable to complete report count request", FieldErrors: []ReportCountFieldError{}}
	switch status {
	case 400:
		e.Code, e.Category, e.Message = "invalid_request", "validation", "Invalid report count request"
	case 401:
		e.Code, e.Category, e.Message = "unauthenticated", "authentication", "Authentication is required"
	case 403:
		e.Code, e.Category, e.Message = "forbidden", "authorization", "Report access is not permitted"
	case 404:
		e.Code, e.Category, e.Message = "not_found", "authorization", "Account is unavailable"
	case 422:
		e.Code, e.Category, e.Message = "unsupported_report_option", "validation", "This report option is not supported"
	case 429:
		e.Code, e.Category, e.Message, e.Retryable = "rate_limited", "rate_limit", "Too many report requests", true
	case 502, 503:
		e.Code, e.Category, e.Message, e.Retryable, e.Dependency = "report_unavailable", "external", "Report counts are temporarily unavailable", true, "report_storage"
	case 504:
		e.Code, e.Category, e.Message, e.Retryable, e.Dependency = "report_timeout", "external", "Report count request timed out", true, "report_storage"
	}
	return e
}

// Buffer even WriteHeaderNow/Flush so auth aborts, recovery and late cancellation
// can replace the whole response. Only bounded count arrays use this writer.
func ReportCountErrorAdapter() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isReportCountRoute(c) {
			c.Next()
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), ReportCountReadTimeout)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		original := c.Writer
		buffered := &reportCountWriter{ResponseWriter: original, status: http.StatusOK, size: -1}
		c.Writer = buffered
		c.Next()
		status := buffered.Status()
		if buffered.overflow {
			status = http.StatusInternalServerError
			c.Set(reportCountErrorKey, defaultReportCountError(status))
		}
		if err := ctx.Err(); err != nil {
			status = http.StatusServiceUnavailable
			code, message := "request_cancelled", "Report count request was cancelled"
			retryable := false
			if errors.Is(err, context.DeadlineExceeded) {
				status, code, message, retryable = http.StatusGatewayTimeout, "report_timeout", "Report count request timed out", true
			}
			c.Set(reportCountErrorKey, ReportCountAPIError{Code: code, Category: "external", Message: message, FieldErrors: []ReportCountFieldError{}, Retryable: retryable, Dependency: "report_storage"})
		}
		c.Writer = original
		if status >= 400 {
			e := defaultReportCountError(status)
			if custom, ok := c.Get(reportCountErrorKey); ok {
				e = custom.(ReportCountAPIError)
			}
			u := foundation.GetUnifiedContext(c)
			e.RequestID, e.CorrelationID = u.RequestID, u.CorrelationID
			if e.Retryable {
				if seconds, err := strconv.Atoi(original.Header().Get("Retry-After")); err == nil && seconds > 0 {
					e.RetryAfter = seconds
				}
			}
			original.Header().Del("Content-Length")
			original.Header().Set("Content-Type", "application/json; charset=utf-8")
			original.WriteHeader(status)
			_ = json.NewEncoder(original).Encode(struct {
				Error ReportCountAPIError `json:"error"`
			}{e})
			return
		}
		original.WriteHeader(status)
		_, _ = original.Write(buffered.body.Bytes())
	}
}

// Installed inside legacy RecoveryLogger, so private panic values and stacks
// never reach its legacy diagnostics for these new roots.
func ReportCountRecovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isReportCountRoute(c) {
			c.Next()
			return
		}
		defer func() {
			if recover() != nil {
				c.Set(reportCountErrorKey, defaultReportCountError(http.StatusInternalServerError))
				if w, ok := c.Writer.(*reportCountWriter); ok {
					w.status = http.StatusInternalServerError
				}
				c.Abort()
			}
		}()
		c.Next()
	}
}

type reportCountWriter struct {
	gin.ResponseWriter
	body     bytes.Buffer
	status   int
	size     int
	overflow bool
}

func (w *reportCountWriter) WriteHeader(status int) {
	if w.size < 0 && status > 0 {
		w.status = status
	}
}
func (w *reportCountWriter) WriteHeaderNow() {
	if w.size < 0 {
		w.size = 0
	}
}
func (w *reportCountWriter) Write(b []byte) (int, error) {
	w.WriteHeaderNow()
	if w.overflow || len(b) > reportCountMaxResponseBytes-w.body.Len() {
		w.overflow = true
		return 0, errors.New("report count response exceeds limit")
	}
	n, e := w.body.Write(b)
	w.size += n
	return n, e
}
func (w *reportCountWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *reportCountWriter) Status() int                       { return w.status }
func (w *reportCountWriter) Size() int                         { return w.size }
func (w *reportCountWriter) Written() bool                     { return w.size >= 0 }
func (w *reportCountWriter) Flush()                            { w.WriteHeaderNow() }
func (w *reportCountWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("report count responses cannot be hijacked")
}
func (w *reportCountWriter) Pusher() http.Pusher { return nil }
