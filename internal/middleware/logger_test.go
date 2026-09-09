package middleware_test

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/gin-gonic/gin"
)

func TestMiddlewares(t *testing.T) {
	gin.SetMode(gin.TestMode)
	buf := &bytes.Buffer{}
	logger.SetOutput(buf, slog.LevelDebug, "json")

	t.Run("RequestLogger outputs structured log with request id", func(t *testing.T) {
		buf.Reset()
		r := gin.New()
		r.Use(foundation.UnifiedContextMiddleware())
		r.Use(middleware.RequestLogger())
		r.GET("/test/ping", func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"pong": true})
		})

		req := httptest.NewRequest(http.MethodGet, "/test/ping", nil)
		req.Header.Set(foundation.HeaderRequestID, "test-req-999")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		out := buf.String()
		if !strings.Contains(out, "test-req-999") {
			t.Errorf("expected log to contain request_id test-req-999, got: %s", out)
		}
		if !strings.Contains(out, "\"component\":\"http\"") {
			t.Errorf("expected log to contain component:http, got: %s", out)
		}
	})

	t.Run("RecoveryLogger captures panic and logs stack trace", func(t *testing.T) {
		buf.Reset()
		r := gin.New()
		r.Use(foundation.UnifiedContextMiddleware())
		r.Use(middleware.RecoveryLogger())
		r.GET("/test/panic", func(c *gin.Context) {
			panic("simulated fatal crash")
		})

		req := httptest.NewRequest(http.MethodGet, "/test/panic", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500 on panic, got %d", w.Code)
		}

		out := buf.String()
		if !strings.Contains(out, "simulated fatal crash") {
			t.Errorf("expected log to contain panic reason, got: %s", out)
		}
		if !strings.Contains(out, "\"component\":\"recovery\"") {
			t.Errorf("expected log to contain component:recovery, got: %s", out)
		}
	})
}
