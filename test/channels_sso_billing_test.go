package test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestChannels_SSO_Billing_And_Enterprise(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_channels_sso_key_1234567890",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Register Admin
	signUpPayload := map[string]string{
		"name":         "Enterprise Owner",
		"email":        "owner@enterprise.corp",
		"password":     "Secret123!",
		"account_name": "Global Support Hub",
	}
	body, _ := json.Marshal(signUpPayload)
	req := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("sign up failed: code=%d, body=%s", w.Code, w.Body.String())
	}

	var authResp struct {
		Success bool `json:"success"`
		Data    struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID
	accStr := strconv.FormatUint(uint64(accountID), 10)

	// 2. WhatsApp Webhook Verification & Message Ingestion
	// 2.1 Challenge verification
	req = httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+accStr+"/whatsapp/callback?hub.mode=subscribe&hub.challenge=test_challenge_123&hub.verify_token=valid_token", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "test_challenge_123" {
		t.Fatalf("whatsapp challenge failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// 2.2 Ingest incoming WhatsApp message
	waPayload := map[string]any{
		"object": "whatsapp_business_account",
		"entry": []map[string]any{
			{
				"id": "WHATSAPP_ID_1",
				"changes": []map[string]any{
					{
						"field": "messages",
						"value": map[string]any{
							"messaging_product": "whatsapp",
							"contacts": []map[string]any{
								{
									"profile": map[string]string{"name": "Alice WhatsApp"},
									"wa_id":   "+1234567890",
								},
							},
							"messages": []map[string]any{
								{
									"from":      "+1234567890",
									"id":        "wamid.HBgLMTIzNDU2Nzg5MA==",
									"timestamp": "1678900000",
									"type":      "text",
									"text":      map[string]string{"body": "Hello from WhatsApp customer!"},
								},
							},
						},
					},
				},
			},
		},
	}
	body, _ = json.Marshal(waPayload)
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accStr+"/whatsapp/callback", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("whatsapp message ingestion failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// 2.3 WhatsApp Authorization & CSAT Template
	authReqBody, _ := json.Marshal(map[string]string{
		"code":            "meta_oauth_auth_code",
		"waba_id":         "waba_101",
		"phone_number_id": "phone_101",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accStr+"/whatsapp/authorization", bytes.NewReader(authReqBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("whatsapp auth failed: code=%d", w.Code)
	}

	csatTmplBody, _ := json.Marshal(map[string]string{
		"message":     "How was your support experience?",
		"button_text": "Rate Us",
		"language":    "en",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accStr+"/inboxes/1/csat_template", bytes.NewReader(csatTmplBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("whatsapp csat template failed: code=%d", w.Code)
	}

	// 3. Twilio SMS Channel & Inbound Webhook
	twilioChannelBody, _ := json.Marshal(map[string]string{
		"inbox_name":   "Twilio SMS US",
		"account_sid":  "AC_mock_twilio_sid",
		"auth_token":   "auth_token_secret",
		"phone_number": "+18005551234",
		"medium":       "sms",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accStr+"/channels/twilio_channel", bytes.NewReader(twilioChannelBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("twilio channel creation failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// Twilio inbound SMS webhook
	formValues := url.Values{}
	formValues.Set("From", "+1987654321")
	formValues.Set("To", "+18005551234")
	formValues.Set("Body", "Inbound SMS message from client")
	formValues.Set("MessageSid", "SM_mock_message_sid_123")
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accStr+"/twilio/callback", strings.NewReader(formValues.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("twilio callback failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// 4. Calls & Conference Signaling
	callBody, _ := json.Marshal(map[string]any{
		"inbox_id":  1,
		"sdp_offer": "v=0\r\no=- 4611731400430051336 2 IN IP4 127.0.0.1...",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accStr+"/contacts/1/call", bytes.NewReader(callBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("initiate call failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// Conference room token
	confBody, _ := json.Marshal(map[string]string{
		"call_sid": "CA_room_sid_123",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accStr+"/inboxes/1/conference", bytes.NewReader(confBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("conference create failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// WhatsApp call SDP answer exchange
	sdpAnsBody, _ := json.Marshal(map[string]any{
		"call_id":    1,
		"sdp_answer": "v=0\r\no=whatsapp 2 2 IN IP4 127.0.0.1...",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accStr+"/whatsapp_calls", bytes.NewReader(sdpAnsBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("whatsapp call sdp failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// 5. Google OAuth & SAML SSO
	// 5a. Verify query email bypass is blocked (returns 401 Unauthorized)
	req = httptest.NewRequest(http.MethodGet, "/omniauth/google_oauth2/callback?code=mock_code&email=hacker@evil.com", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected query bypass to fail with 401, got %d", w.Code)
	}

	// 5b. Legitimate Google ID Token login succeeds
	validGoogleToken := auth.GenerateMockGoogleIDToken("google_agent@example.com", "Google Agent")
	req = httptest.NewRequest(http.MethodGet, "/omniauth/google_oauth2/callback?credential="+validGoogleToken, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("google oauth callback with valid credential failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// SAML configuration
	samlBody, _ := json.Marshal(map[string]any{
		"sso_url":       "https://idp.okta.com/app/chatwoot/sso/saml",
		"certificate":   "-----BEGIN CERTIFICATE-----\nMIIB...-----END CERTIFICATE-----",
		"role_mappings": `{"SupportTeam": "agent", "SupportAdmins": "administrator"}`,
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accStr+"/saml_settings", bytes.NewReader(samlBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save saml settings failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// SAML Login initiation
	samlLoginReq, _ := json.Marshal(map[string]string{"email": "saml_user@corp.com"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/saml_login", bytes.NewReader(samlLoginReq))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("initiate saml login failed: code=%d", w.Code)
	}

	// SAML ACS Callback: Corrupted SAML is rejected
	samlCallbackValues := url.Values{}
	samlCallbackValues.Set("SAMLResponse", "corrupted_saml_value")
	samlCallbackValues.Set("RelayState", "relay_token_123")
	req = httptest.NewRequest(http.MethodPost, "/omniauth/saml/callback", strings.NewReader(samlCallbackValues.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected corrupted saml callback to fail with 401, got %d", w.Code)
	}

	// SAML ACS Callback: Valid SAML succeeds
	validSAML := auth.GenerateMockSAMLResponse("saml_user@corp.com", "Saml", "User")
	samlCallbackValues.Set("SAMLResponse", validSAML)
	req = httptest.NewRequest(http.MethodPost, "/omniauth/saml/callback", strings.NewReader(samlCallbackValues.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("valid saml callback failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// 6. Profile MFA & Sessions & Password Reset
	// MFA profile check & setup
	req = httptest.NewRequest(http.MethodGet, "/api/v1/profile/mfa", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get mfa profile failed: code=%d", w.Code)
	}

	var mfaRes struct {
		Data struct {
			Secret string `json:"secret"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &mfaRes)
	if mfaRes.Data.Secret == "" {
		t.Fatalf("expected non-empty mfa secret")
	}

	// Incorrect TOTP code is rejected
	badMfaBody, _ := json.Marshal(map[string]string{"otp_code": "000000"})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/profile/mfa", bytes.NewReader(badMfaBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected invalid totp to fail with 400, got %d", w.Code)
	}

	// Valid RFC 6238 TOTP code enables MFA
	validCode, err := auth.GenerateTOTPCode(mfaRes.Data.Secret, time.Now())
	if err != nil {
		t.Fatalf("failed to calculate totp code: %v", err)
	}
	mfaEnableBody, _ := json.Marshal(map[string]string{"otp_code": validCode})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/profile/mfa", bytes.NewReader(mfaEnableBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("enable mfa failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// List Sessions
	req = httptest.NewRequest(http.MethodGet, "/api/v1/profile/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list sessions failed: code=%d", w.Code)
	}

	// Password reset request
	pwdReqBody, _ := json.Marshal(map[string]string{"email": "owner@enterprise.corp"})
	req = httptest.NewRequest(http.MethodPost, "/auth/password", bytes.NewReader(pwdReqBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("password reset request failed: code=%d", w.Code)
	}

	var pwdResetResp struct {
		Success bool `json:"success"`
		Data    struct {
			ResetToken string `json:"reset_token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &pwdResetResp)
	resetToken := pwdResetResp.Data.ResetToken

	// Perform reset
	resetBody, _ := json.Marshal(map[string]string{
		"token":    resetToken,
		"password": "NewSecretPassword999!",
	})
	req = httptest.NewRequest(http.MethodPut, "/auth/password", bytes.NewReader(resetBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("reset password failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// 7. Data Imports, Migration Jobs & Bulk Article Actions
	importBody, _ := json.Marshal(map[string]any{
		"source_provider": "csv",
		"import_type":     "contacts",
		"total_records":   250,
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accStr+"/data_imports", bytes.NewReader(importBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create data import failed: code=%d body=%s", w.Code, w.Body.String())
	}

	migJobBody, _ := json.Marshal(map[string]string{
		"job_type":      "webchat_legacy",
		"source_system": "legacy_v1_db",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accStr+"/migration_jobs", bytes.NewReader(migJobBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create migration job failed: code=%d body=%s", w.Code, w.Body.String())
	}

	bulkArticleBody, _ := json.Marshal(map[string]any{
		"ids":    []uint{1, 2, 3},
		"status": "published",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accStr+"/portals/1/articles/bulk_actions", bytes.NewReader(bulkArticleBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("bulk article action failed: code=%d", w.Code)
	}

	// 8. Reporting Events & Onboarding & Branded Email
	eventBody, _ := json.Marshal(map[string]any{
		"name":          "conversation_resolved_duration",
		"value":         180.5,
		"metadata_json": `{"channel":"whatsapp"}`,
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accStr+"/reporting_events", bytes.NewReader(eventBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create reporting event failed: code=%d body=%s", w.Code, w.Body.String())
	}

	onboardingBody, _ := json.Marshal(map[string]any{
		"step":      "invite_team",
		"industry":  "Ecommerce",
		"completed": true,
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accStr+"/onboarding", bytes.NewReader(onboardingBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save onboarding failed: code=%d", w.Code)
	}

	brandedBody, _ := json.Marshal(map[string]string{
		"layout_html":  "<html><body>{{content}}</body></html>",
		"header_color": "#0052cc",
		"logo_url":     "https://cdn.example.com/logo.png",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accStr+"/branded_email_layout", bytes.NewReader(brandedBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("save branded email failed: code=%d", w.Code)
	}

	// 9. Enterprise Limits & Billing
	limitsBody, _ := json.Marshal(map[string]int{
		"conversation_limit": 50000,
		"agent_limit":        100,
		"inbox_limit":        30,
	})
	req = httptest.NewRequest(http.MethodPost, "/enterprise/api/v1/accounts/"+accStr+"/limits", bytes.NewReader(limitsBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update enterprise limits failed: code=%d body=%s", w.Code, w.Body.String())
	}

	billingBody, _ := json.Marshal(map[string]any{
		"action_type": "deposit",
		"amount":      500.0,
		"currency":    "USD",
		"description": "Enterprise credits recharge via Stripe",
	})
	req = httptest.NewRequest(http.MethodPost, "/enterprise/api/v1/accounts/"+accStr+"/billing", bytes.NewReader(billingBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("record billing failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// 10. Platform App Email Channel Migration
	migApp := domain.PlatformApp{
		Name:  "Migration Controller",
		Token: "test_platform_token_secret_xyz",
	}
	_ = db.Create(&migApp).Error

	emailMigBody, _ := json.Marshal(map[string]string{
		"provider":        "sendgrid",
		"provider_config": `{"api_key": "SG.mock_key"}`,
	})
	req = httptest.NewRequest(http.MethodPost, "/platform/api/v1/accounts/"+accStr+"/email_channel_migrations", bytes.NewReader(emailMigBody))
	req.Header.Set("X-Platform-App-Token", "test_platform_token_secret_xyz")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("platform email migration failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// 11. Search & Well-Known
	req = httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+accStr+"/search?q=WhatsApp", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("global search failed: code=%d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/.well-known/assetlinks.json", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("assetlinks failed: code=%d", w.Code)
	}
}
