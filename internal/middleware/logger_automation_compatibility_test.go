package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAutomationCompatibilityQueryNeverAppearsInLogs(t *testing.T) {
	for _, raw := range []string{"target=durable_local_v1", "unknown=PRIVATE-DEFINITION&cursor=PRIVATE-CURSOR", "target=PRIVATE-TARGET&target=OTHER-PRIVATE", "PRIVATE-KEY=PRIVATE-VALUE", "cursor=PRIVATE-CURSOR%ZZ"} {
		require.Empty(t, safeRequestQuery("/api/v1/accounts/91/automation_rules/compatibility", raw))
		require.Empty(t, safeRequestQuery("/api/v1/accounts/91/automation_rules/compatibility/", raw))
	}
	require.Equal(t, "limit=10", safeRequestQuery("/api/v1/accounts/91/automation_rules", "limit=10"))
	require.Equal(t, "q=hello", safeRequestQuery("/api/search", "q=hello"))
	gin.SetMode(gin.TestMode)
	var buf bytes.Buffer
	logger.SetOutput(&buf, slog.LevelDebug, "json")
	t.Cleanup(func() { logger.SetOutput(os.Stderr, slog.LevelInfo, "json") })
	router := gin.New()
	router.Use(RequestLogger())
	router.GET("/api/v1/accounts/:account_id/automation_rules/compatibility", func(c *gin.Context) { c.Status(http.StatusBadRequest) })
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/accounts/91/automation_rules/compatibility?unknown=PRIVATE-QUERY&cursor=PRIVATE-CURSOR", nil))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, buf.String(), "automation_rules/compatibility")
	require.NotContains(t, buf.String(), "PRIVATE-")
	require.NotContains(t, buf.String(), "unknown=")
	require.NotContains(t, buf.String(), "cursor=")
}
