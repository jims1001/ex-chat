package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBillingHandlersUnconfiguredNeverInventState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AuthEnterpriseHandler{}
	router := gin.New()
	router.POST("/:account_id/subscription", h.CreateOrUpdateSubscription)
	router.POST("/:account_id/checkout", h.Checkout)
	for _, route := range []string{"subscription", "checkout"} {
		for _, body := range []string{"", `{}`} {
			req := httptest.NewRequest(http.MethodPost, "/91/"+route, strings.NewReader(body))
			out := httptest.NewRecorder()
			router.ServeHTTP(out, req)
			require.Equal(t, 503, out.Code)
			require.NotContains(t, out.Body.String(), "redirect_url")
			require.NotContains(t, out.Body.String(), "active")
			require.NotContains(t, out.Body.String(), "period")
		}
	}
}
