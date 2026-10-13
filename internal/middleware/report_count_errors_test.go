package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestReportCountErrorAdapter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, version := range []string{"v1", "v2"} {
		t.Run(version, func(t *testing.T) {
			for _, status := range []int{400, 401, 403, 404, 422, 429, 500, 502, 503, 504} {
				t.Run(http.StatusText(status), func(t *testing.T) {
					r := gin.New()
					r.Use(foundation.UnifiedContextMiddleware(), ReportCountErrorAdapter(), RecoveryLogger(), ReportCountRecovery())
					r.GET("/api/"+version+"/accounts/:account_id/reports", func(c *gin.Context) {
						c.Header("Retry-After", "3")
						c.Header("X-Test", "retained")
						c.Writer.WriteHeader(status)
						c.Writer.WriteHeaderNow()
						c.Writer.Flush()
						_, _ = c.Writer.WriteString("private SQL error")
						c.Abort()
					})
					w := httptest.NewRecorder()
					req := httptest.NewRequest("GET", "/api/"+version+"/accounts/91/reports", nil)
					req.Header.Set("X-Request-Id", "request44")
					req.Header.Set("X-Correlation-Id", "correlation44")
					r.ServeHTTP(w, req)
					require.Equal(t, status, w.Code)
					require.NotContains(t, w.Body.String(), "private")
					require.Equal(t, "retained", w.Header().Get("X-Test"))
					var got struct {
						Error ReportCountAPIError `json:"error"`
					}
					require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
					require.NotEmpty(t, got.Error.Code)
					require.NotEmpty(t, got.Error.Category)
					require.Equal(t, "request44", got.Error.RequestID)
					require.Equal(t, "correlation44", got.Error.CorrelationID)
					require.NotNil(t, got.Error.FieldErrors)
					if got.Error.Retryable {
						require.Equal(t, 3, got.Error.RetryAfter)
					}
				})
			}
		})
	}
	t.Run("panic_after_partial_success_is_sanitized", func(t *testing.T) {
		r := gin.New()
		r.Use(foundation.UnifiedContextMiddleware(), ReportCountErrorAdapter(), RecoveryLogger(), ReportCountRecovery())
		r.GET("/api/v1/accounts/:account_id/reports", func(c *gin.Context) {
			c.Writer.WriteHeaderNow()
			_, _ = c.Writer.WriteString("partial private")
			panic("private bearer-token SQL driver")
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/accounts/91/reports", nil))
		require.Equal(t, 500, w.Code)
		require.NotContains(t, w.Body.String(), "private")
		require.Contains(t, w.Body.String(), "internal_error")
	})
	t.Run("bounded_buffer_discards_all", func(t *testing.T) {
		r := gin.New()
		r.Use(foundation.UnifiedContextMiddleware(), ReportCountErrorAdapter(), RecoveryLogger(), ReportCountRecovery())
		r.GET("/api/v1/accounts/:account_id/reports", func(c *gin.Context) {
			_, err := c.Writer.Write([]byte(strings.Repeat("x", reportCountMaxResponseBytes+1)))
			require.Error(t, err)
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/accounts/1/reports", nil))
		require.Equal(t, 500, w.Code)
		require.Contains(t, w.Body.String(), "internal_error")
	})
	t.Run("late_cancel_and_deadline_discard_success", func(t *testing.T) {
		for _, deadline := range []bool{false, true} {
			r := gin.New()
			r.Use(foundation.UnifiedContextMiddleware(), ReportCountErrorAdapter(), RecoveryLogger(), ReportCountRecovery())
			ctx, cancel := context.WithCancel(context.Background())
			want := 503
			if deadline {
				var end context.CancelFunc
				ctx, end = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer end()
				want = 504
			}
			defer cancel()
			r.GET("/api/v1/accounts/:account_id/reports", func(c *gin.Context) {
				if !deadline {
					d, ok := c.Request.Context().Deadline()
					require.True(t, ok)
					require.InDelta(t, 5, time.Until(d).Seconds(), 0.3)
				}
				c.JSON(200, []int{1})
				cancel()
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/accounts/1/reports", nil).WithContext(ctx))
			require.Equal(t, want, w.Code)
			require.NotEqual(t, "[1]", w.Body.String())
		}
	})
	t.Run("legacy_paths_and_methods_keep_exact_bytes", func(t *testing.T) {
		for _, path := range []string{"/api/v1/accounts/91/reports/summary", "/api/v2/accounts/91/reports/summary", "/other/reports", "/api/v1/accounts/91/reports"} {
			method := "GET"
			if strings.HasSuffix(path, "91/reports") {
				method = "POST"
			}
			r := gin.New()
			r.Use(foundation.UnifiedContextMiddleware(), ReportCountErrorAdapter(), RecoveryLogger(), ReportCountRecovery())
			r.Handle(method, path, func(c *gin.Context) {
				c.Writer.WriteHeader(403)
				c.Writer.WriteHeaderNow()
				_, _ = c.Writer.WriteString(`{"success":false,"error":"legacy forbidden"}`)
				c.Abort()
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(method, path, nil))
			require.Equal(t, 403, w.Code)
			require.Equal(t, `{"success":false,"error":"legacy forbidden"}`, w.Body.String())
		}
	})
}

func TestReportCountLogsOmitPrivateInputAndPanic(t *testing.T) {
	var output bytes.Buffer
	logger.SetOutput(&output, slog.LevelDebug, "json")
	defer logger.SetOutput(os.Stdout, slog.LevelInfo, "json")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(foundation.UnifiedContextMiddleware(), RequestLogger(), ReportCountErrorAdapter(), RecoveryLogger(), ReportCountRecovery())
	r.GET("/api/v1/accounts/:account_id/reports", func(c *gin.Context) { panic("secret-panic-SQL-driver") })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/accounts/91/reports?private=secret-query", nil))
	require.Equal(t, 500, w.Code)
	require.Contains(t, output.String(), "request_id")
	require.NotContains(t, output.String(), "secret-query")
	require.NotContains(t, output.String(), "secret-panic")
	require.Empty(t, safeRequestQuery("/api/v2/accounts/1/reports", "private=secret-query"))
	require.Equal(t, "days=7", safeRequestQuery("/api/v2/accounts/1/reports/trends", "days=7"))
}
