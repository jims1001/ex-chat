package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCampaignTriggerUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &AdvancedHandler{}
	r.POST("/accounts/:account_id/campaigns/:id/trigger", h.TriggerCampaign)
	out := httptest.NewRecorder()
	r.ServeHTTP(out, httptest.NewRequest(http.MethodPost, "/accounts/1/campaigns/1/trigger", nil))
	require.Equal(t, http.StatusInternalServerError, out.Code)
	require.JSONEq(t, `{"success":false,"error":"Campaign service unavailable"}`, out.Body.String())
	require.NotContains(t, out.Body.String(), "completed")
}
