package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestConfirmationRequestQueriesAreAlwaysSilent(t *testing.T) {
	for _, path := range []string{"/auth/confirmation", "/auth/confirmation/resend", "/app/auth/confirmation", "/api/v1/auth/confirmation/"} {
		for _, query := range []string{
			"confirmation_token=bearer-secret&tracking=also-private", "CONFIRMATION_TOKEN=bearer-secret", "confirmation%5Ftoken=bearer-secret",
			"token=bearer-secret", "unknown_alias=bearer-secret", "confirmation_token=first-secret&confirmation_token=second-secret", "confirmation_token=bearer-secret%ZZ",
		} {
			require.Empty(t, safeRequestQuery(path, query), "%s query must never be logged", path)
		}
	}
	for _, query := range []string{"confirmation_token=bearer-secret&x=1", "CONFIRMATION_TOKEN=bearer-secret", "confirmation%5Ftoken=bearer-secret", "confirmation_token=bearer-secret%ZZ"} {
		require.NotContains(t, safeRequestQuery("/other", query), "bearer-secret")
	}
	require.Equal(t, "q=hello&sort=asc", safeRequestQuery("/api/search", "q=hello&sort=asc"))
}

func TestConfirmationRequestLoggerOmitsProofQueryAndBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var logs bytes.Buffer
	logger.SetOutput(&logs, slog.LevelDebug, "json")
	t.Cleanup(func() { logger.SetOutput(os.Stdout, slog.LevelInfo, "json") })
	r := gin.New()
	r.Use(RequestLogger())
	r.Any("/auth/confirmation", func(c *gin.Context) { c.Status(http.StatusUnprocessableEntity) })
	r.GET("/api/search", func(c *gin.Context) { c.Status(http.StatusOK) })
	for _, query := range []string{"confirmation_token=query-bearer", "alias=query-bearer", "CONFIRMATION_TOKEN=query-bearer", "confirmation_token=query-bearer%ZZ"} {
		req := httptest.NewRequest(http.MethodPost, "/auth/confirmation?"+query, strings.NewReader(`{"confirmation_token":"body-bearer"}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(httptest.NewRecorder(), req)
	}
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/search?q=hello", nil))
	require.NotContains(t, logs.String(), "query-bearer")
	require.NotContains(t, logs.String(), "body-bearer")
	require.NotContains(t, logs.String(), "confirmation_token")
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	require.Len(t, lines, 5)
	for i, line := range lines {
		var event map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &event))
		if i < 4 {
			require.Equal(t, "/auth/confirmation", event["path"])
		} else {
			require.Equal(t, "/api/search?q=hello", event["path"])
		}
	}
}
