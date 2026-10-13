package handler

import (
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebhookRetryUnavailableAndInvalid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewWebhookHandler(nil)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(middleware.ContextAccountID, uint(91)) })
	r.POST("/deliveries/:id/retry", h.RetryDelivery)
	r.POST("/deliveries/retry", h.RetryDelivery)
	for _, tc := range []struct {
		name, path string
		status     int
	}{
		{"positive_id", "/deliveries/11/retry", 503}, {"no_id", "/deliveries/retry", 503},
		{"zero", "/deliveries/0/retry", 400}, {"negative", "/deliveries/-1/retry", 400},
		{"text", "/deliveries/garbage/retry", 400}, {"overflow", "/deliveries/18446744073709551616/retry", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := httptest.NewRecorder()
			r.ServeHTTP(out, httptest.NewRequest(http.MethodPost, tc.path, nil))
			require.Equal(t, tc.status, out.Code, out.Body.String())
			require.NotContains(t, out.Body.String(), `"success":true`)
			require.NotContains(t, out.Body.String(), `"status":"delivered"`)
		})
	}
}
