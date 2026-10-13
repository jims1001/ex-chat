package handler

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestChannelNoConfigAndFacebookFailClosed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, name := range []string{"CHANNEL_PUBLIC_BASE_URL", "CHANNEL_CREDENTIAL_KEY", "WHATSAPP_APP_ID", "WHATSAPP_APP_SECRET", "WHATSAPP_VERIFY_TOKEN", "WHATSAPP_API_VERSION"} {
		t.Setenv(name, "")
	}
	h := NewChannelDriverHandler(nil, nil, nil, nil, nil)
	r := gin.New()
	r.POST("/api/v1/accounts/:account_id/whatsapp/authorization", h.WhatsAppAuthorization)
	r.POST("/api/v1/accounts/:account_id/channels/twilio_channel", h.CreateTwilioChannel)
	r.POST("/api/v1/accounts/:account_id/whatsapp/callback", h.HandleWhatsAppWebhook)
	r.POST("/api/v1/accounts/:account_id/twilio/callback", h.HandleTwilioWebhook)
	for _, path := range []string{"/public/api/v1/channels/facebook/webhook", "/api/v1/accounts/:account_id/callbacks"} {
		r.GET(path, h.VerifyFacebookWebhook)
		r.POST(path, h.HandleFacebookWebhook)
	}
	for _, test := range []struct {
		method, path string
		want         int
	}{
		{"POST", "/api/v1/accounts/91/whatsapp/authorization", 503}, {"POST", "/api/v1/accounts/91/channels/twilio_channel", 503}, {"POST", "/api/v1/accounts/91/whatsapp/callback", 503}, {"POST", "/api/v1/accounts/91/twilio/callback", 503},
		{"POST", "/api/v1/accounts/0/whatsapp/authorization", 400}, {"POST", "/api/v1/accounts/not-an-id/twilio/callback", 400},
		{"GET", "/public/api/v1/channels/facebook/webhook?hub.mode=subscribe&hub.verify_token=forged&hub.challenge=123", 503}, {"POST", "/public/api/v1/channels/facebook/webhook", 503},
		{"GET", "/api/v1/accounts/91/callbacks?hub.mode=subscribe&hub.verify_token=forged&hub.challenge=123", 503}, {"POST", "/api/v1/accounts/91/callbacks", 503},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(test.method, test.path, strings.NewReader(`{"code":"LEAK-secret-code","auth_token":"LEAK-token","object":"page","entry":[{"messaging":[{"sender":{"id":"victim"},"message":{"text":"forged"}}]}]}`))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != test.want {
			t.Fatalf("%s %s: %d %s", test.method, test.path, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "LEAK") {
			t.Fatal("secret echoed")
		}
	}
}
