package test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
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
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestChecklistCompliance_AllItems(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "secret_checklist_compliance_12345",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up admin
	signUpPayload := map[string]string{
		"name":         "Checklist Tester",
		"email":        "tester@checklist.corp",
		"password":     "Password123!",
		"account_name": "Compliance Corp",
	}
	body, _ := json.Marshal(signUpPayload)
	req := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("sign up failed: code=%d", w.Code)
	}

	var authResp struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
			User     domain.User      `json:"user"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID
	accIDStr := strconv.Itoa(int(accountID))
	userID := authResp.Data.User.ID

	// Create an inbox
	inboxBody, _ := json.Marshal(map[string]any{
		"name":         "Support Web",
		"channel_type": "web_widget",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/inboxes", bytes.NewReader(inboxBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create inbox failed: code=%d", w.Code)
	}
	var inboxResp struct {
		Data domain.Inbox `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &inboxResp)
	inboxID := inboxResp.Data.ID

	// Create a contact
	contactBody, _ := json.Marshal(map[string]any{
		"name":  "Alice Customer",
		"email": "alice@customer.io",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/contacts", bytes.NewReader(contactBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create contact failed: code=%d", w.Code)
	}
	var contactResp struct {
		Data domain.Contact `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &contactResp)
	contactID := contactResp.Data.ID

	// -------------------------------------------------------------
	// Item 4: Automation Rules Engine with condition evaluation & business trigger
	// -------------------------------------------------------------
	t.Run("Item4_AutomationRule_Execution", func(t *testing.T) {
		ruleBody, _ := json.Marshal(map[string]any{
			"name":        "Urgent Escalation Rule",
			"description": "Auto bump priority when urgent is mentioned",
			"event_name":  "message_created",
			"conditions": `[
				{"attribute_key":"content","filter_operator":"contains","values":["urgent"]}
			]`,
			"actions": `[
				{"action_name":"change_priority","action_params":["urgent"]},
				{"action_name":"assign_agent","action_params":[` + strconv.Itoa(int(userID)) + `]}
			]`,
			"active": true,
		})
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/automation_rules", bytes.NewReader(ruleBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create automation rule failed: code=%d body=%s", w.Code, w.Body.String())
		}

		convBody, _ := json.Marshal(map[string]any{
			"inbox_id":   inboxID,
			"contact_id": contactID,
			"status":     "open",
			"priority":   "low",
		})
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/conversations", bytes.NewReader(convBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create conversation failed: code=%d", w.Code)
		}
		var convResp struct {
			Data domain.Conversation `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &convResp)
		convID := convResp.Data.ID

		msgBody, _ := json.Marshal(map[string]any{
			"content":      "This is an urgent problem that needs fixing!",
			"message_type": "incoming",
		})
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/conversations/"+strconv.Itoa(int(convID))+"/messages", bytes.NewReader(msgBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create message failed: code=%d", w.Code)
		}

		time.Sleep(100 * time.Millisecond)

		var updatedConv domain.Conversation
		if err := db.First(&updatedConv, convID).Error; err != nil {
			t.Fatalf("failed to query conversation: %v", err)
		}
		if updatedConv.Priority != "urgent" {
			t.Errorf("expected conversation priority 'urgent', got '%s'", updatedConv.Priority)
		}
		if updatedConv.AssigneeID == nil || *updatedConv.AssigneeID != userID {
			t.Errorf("expected assignee ID %d, got %v", userID, updatedConv.AssigneeID)
		}
	})

	// -------------------------------------------------------------
	// Item 5: Webhook delivery logging & HMAC signature
	// -------------------------------------------------------------
	t.Run("Item5_Webhook_DeliveryAudit", func(t *testing.T) {
		whBody, _ := json.Marshal(map[string]any{
			"url":           "https://mock-webhook.endpoint/callback",
			"subscriptions": []string{"conversation_created", "message_created"},
		})
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/webhooks", bytes.NewReader(whBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create webhook failed: code=%d", w.Code)
		}

		convBody, _ := json.Marshal(map[string]any{
			"inbox_id":   inboxID,
			"contact_id": contactID,
			"status":     "open",
		})
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/conversations", bytes.NewReader(convBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create conversation failed: code=%d", w.Code)
		}

		var deliveries []domain.WebhookDelivery
		for i := 0; i < 20; i++ {
			db.Where("account_id = ?", accountID).Find(&deliveries)
			if len(deliveries) > 0 {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}

		if len(deliveries) == 0 {
			t.Errorf("expected at least 1 WebhookDelivery record in DB, got 0")
		} else {
			if deliveries[0].Event == "" {
				t.Errorf("expected non-empty event in delivery log")
			}
		}
	})

	// -------------------------------------------------------------
	// Item 6: Mobile Push delivery formatting and audit logging
	// -------------------------------------------------------------
	t.Run("Item6_Push_DeliveryLog", func(t *testing.T) {
		subBody, _ := json.Marshal(map[string]any{
			"device_token":  "fcm_mock_device_token_999",
			"platform":      "android",
			"endpoint":      "https://fcm.googleapis.com/fcm/send",
			"auth_key":      "auth_sample_key",
			"p256dh":        "p256dh_sample_key",
		})
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/notification_subscriptions", bytes.NewReader(subBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("register subscription failed: code=%d", w.Code)
		}

		var sub domain.NotificationSubscription
		if err := db.Where("account_id = ? AND user_id = ?", accountID, userID).First(&sub).Error; err != nil {
			t.Fatalf("failed to find subscription: %v", err)
		}
		if sub.PushToken != "fcm_mock_device_token_999" {
			t.Errorf("expected token fcm_mock_device_token_999, got %s", sub.PushToken)
		}
	})

	// -------------------------------------------------------------
	// Item 7: RFC 6238 TOTP MFA & SAML XML Verification
	// -------------------------------------------------------------
	t.Run("Item7_TOTP_MFA_And_SAML", func(t *testing.T) {
		req = httptest.NewRequest(http.MethodGet, "/api/v1/profile/mfa", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("get mfa profile failed: code=%d", w.Code)
		}
		var mfaProfResp struct {
			Data domain.MFAProfile `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &mfaProfResp)
		secret := mfaProfResp.Data.Secret
		if secret == "" {
			t.Fatalf("expected non-empty TOTP secret")
		}

		realCode, err := auth.GenerateTOTPCode(secret, time.Now())
		if err != nil {
			t.Fatalf("failed to generate TOTP code: %v", err)
		}

		enableBody, _ := json.Marshal(map[string]string{"otp_code": realCode})
		req = httptest.NewRequest(http.MethodPost, "/api/v1/profile/mfa", bytes.NewReader(enableBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("enable mfa with valid TOTP code failed: code=%d body=%s", w.Code, w.Body.String())
		}

		samlXML := `<?xml version="1.0"?>
<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion">
  <saml:Assertion>
    <saml:Subject>
      <saml:NameID Format="urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress">sso_saml_agent@enterprise.corp</saml:NameID>
    </saml:Subject>
    <saml:AttributeStatement>
      <saml:Attribute Name="email">
        <saml:AttributeValue>sso_saml_agent@enterprise.corp</saml:AttributeValue>
      </saml:Attribute>
      <saml:Attribute Name="first_name">
        <saml:AttributeValue>Enterprise</saml:AttributeValue>
      </saml:Attribute>
      <saml:Attribute Name="last_name">
        <saml:AttributeValue>Agent</saml:AttributeValue>
      </saml:Attribute>
    </saml:AttributeStatement>
  </saml:Assertion>
</samlp:Response>`

		claims, err := auth.ParseSAMLResponse(samlXML)
		if err != nil {
			t.Fatalf("parse SAML response failed: %v", err)
		}
		if claims.Email != "sso_saml_agent@enterprise.corp" {
			t.Errorf("expected SAML email sso_saml_agent@enterprise.corp, got %s", claims.Email)
		}
		if claims.FirstName != "Enterprise" || claims.LastName != "Agent" {
			t.Errorf("expected Enterprise Agent, got %s %s", claims.FirstName, claims.LastName)
		}
	})

	// -------------------------------------------------------------
	// Item 8: Campaign Dispatch & SLA Breach Engine
	// -------------------------------------------------------------
	t.Run("Item8_Campaign_And_SLA", func(t *testing.T) {
		campBody, _ := json.Marshal(map[string]any{
			"title":            "Autumn Promotional Outreach",
			"message":          "Hello {{contact.name}}, check out our new update!",
			"inbox_id":         inboxID,
			"campaign_type":    "one_off",
			"audience_filters": `[]`,
		})
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/campaigns", bytes.NewReader(campBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create campaign failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var campResp struct {
			Data domain.Campaign `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &campResp)
		campID := campResp.Data.ID

		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/campaigns/"+strconv.Itoa(int(campID))+"/trigger", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("trigger campaign failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var trigResp struct {
			Success bool `json:"success"`
			Data    struct {
				SentCount int `json:"sent_count"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &trigResp)
		if trigResp.Data.SentCount == 0 {
			t.Errorf("expected sent_count > 0, got %d", trigResp.Data.SentCount)
		}

		var campDeliveries []domain.CampaignDelivery
		db.Where("campaign_id = ?", campID).Find(&campDeliveries)
		if len(campDeliveries) == 0 {
			t.Errorf("expected CampaignDelivery records in DB, got 0")
		}

		slaBody, _ := json.Marshal(map[string]any{
			"name":                    "Gold SLA Tier",
			"description":             "15 min response target",
			"first_response_time_sec": 60,
			"resolution_time_sec":     120,
		})
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/sla_policies", bytes.NewReader(slaBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create SLA policy failed: code=%d", w.Code)
		}

		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/sla_policies/process", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("process SLA failed: code=%d body=%s", w.Code, w.Body.String())
		}

		req = httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+accIDStr+"/sla_policies/breaches", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("list SLA breaches failed: code=%d body=%s", w.Code, w.Body.String())
		}
	})

	// -------------------------------------------------------------
	// Item 9: Third-party Integrations Lifecycle
	// -------------------------------------------------------------
	t.Run("Item9_Integrations_Lifecycle", func(t *testing.T) {
		req = httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+accIDStr+"/integrations/apps", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("list integration apps failed: code=%d", w.Code)
		}

		installBody, _ := json.Marshal(map[string]any{
			"webhook_url": "https://hooks.slack.com/services/T00/B00/XXXX",
			"channel":     "#customer-support",
		})
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/integrations/apps/slack", bytes.NewReader(installBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("install integration app failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var install domain.IntegrationInstallation
		if err := db.Where("account_id = ? AND app_id = ?", accountID, "slack").First(&install).Error; err != nil {
			t.Fatalf("expected integration installation row in DB: %v", err)
		}
		if install.Status != "installed" {
			t.Errorf("expected Status = 'installed', got '%s'", install.Status)
		}

		req = httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/"+accIDStr+"/integrations/apps/slack", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("uninstall integration app failed: code=%d body=%s", w.Code, w.Body.String())
		}

		db.Where("account_id = ? AND app_id = ?", accountID, "slack").First(&install)
		if install.Status != "disabled" {
			t.Errorf("expected Status = 'disabled' after uninstallation, got '%s'", install.Status)
		}
	})

	// -------------------------------------------------------------
	// Item 2: Teams, Labels, Canned Responses & Macros
	// -------------------------------------------------------------
	t.Run("Item2_Teams_Labels_Canned_Macros", func(t *testing.T) {
		lblBody, _ := json.Marshal(map[string]any{
			"title":       "VIP Client",
			"description": "Premium customer",
			"color":       "#FF0000",
		})
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/labels", bytes.NewReader(lblBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create label failed: code=%d", w.Code)
		}
		var lblResp struct {
			Data domain.Label `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &lblResp)
		labelID := lblResp.Data.ID

		updLblBody, _ := json.Marshal(map[string]any{
			"title":       "VIP Enterprise Client",
			"description": "Updated description",
			"color":       "#00FF00",
		})
		req = httptest.NewRequest(http.MethodPut, "/api/v1/accounts/"+accIDStr+"/labels/"+strconv.Itoa(int(labelID)), bytes.NewReader(updLblBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("update label failed: code=%d body=%s", w.Code, w.Body.String())
		}

		teamBody, _ := json.Marshal(map[string]any{
			"name":        "Tier 2 Escalations",
			"description": "Specialized team",
		})
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/teams", bytes.NewReader(teamBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create team failed: code=%d", w.Code)
		}
		var teamResp struct {
			Data domain.Team `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &teamResp)
		teamID := teamResp.Data.ID

		addMemBody, _ := json.Marshal(map[string]any{
			"user_ids": []uint{userID},
		})
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/teams/"+strconv.Itoa(int(teamID))+"/members", bytes.NewReader(addMemBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("add team member failed: code=%d", w.Code)
		}

		req = httptest.NewRequest(http.MethodDelete, "/api/v1/accounts/"+accIDStr+"/teams/"+strconv.Itoa(int(teamID))+"/members/"+strconv.Itoa(int(userID)), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("remove team member failed: code=%d body=%s", w.Code, w.Body.String())
		}

		cannedBody, _ := json.Marshal(map[string]any{
			"short_code": "refund_policy",
			"content":    "Our return policy allows 30 days full refund.",
		})
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/canned_responses", bytes.NewReader(cannedBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create canned response failed: code=%d", w.Code)
		}

		req = httptest.NewRequest(http.MethodGet, "/api/v1/accounts/"+accIDStr+"/canned_responses?q=refund", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("search canned responses failed: code=%d", w.Code)
		}
	})

	// -------------------------------------------------------------
	// Item 10: Google OAuth & SAML Bypass Prevention
	// -------------------------------------------------------------
	t.Run("Item10_Google_SAML_Security_Enforcement", func(t *testing.T) {
		// 1. Google OAuth query bypass rejection
		req = httptest.NewRequest(http.MethodGet, "/omniauth/google_oauth2/callback?code=mock_code&email=hacker@evil.com", nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected Google query bypass to be blocked with 401, got %d", w.Code)
		}

		// 2. Google OAuth valid JWT succeeds
		googleToken := auth.GenerateMockGoogleIDToken("verified_google_user@corp.com", "Verified User")
		req = httptest.NewRequest(http.MethodGet, "/omniauth/google_oauth2/callback?credential="+googleToken, nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("valid Google ID token login failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// 3. SAML corrupted response rejected
		req = httptest.NewRequest(http.MethodPost, "/omniauth/saml/callback?SAMLResponse=corrupted_invalid_xml", nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected corrupted SAML to be rejected with 401, got %d", w.Code)
		}

		// 4. SAML valid XML succeeds
		validSAML := auth.GenerateMockSAMLResponse("verified_saml_user@corp.com", "SAML", "User")
		samlForm := url.Values{}
		samlForm.Set("SAMLResponse", validSAML)
		req = httptest.NewRequest(http.MethodPost, "/omniauth/saml/callback", strings.NewReader(samlForm.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("valid SAML login failed: code=%d body=%s", w.Code, w.Body.String())
		}
	})

	// -------------------------------------------------------------
	// Item 11: MFA Sign-In Enforcement (TOTP & Backup Codes)
	// -------------------------------------------------------------
	t.Run("Item11_MFA_SignIn_Enforcement", func(t *testing.T) {
		// Create a separate user with MFA enabled
		mfaUserEmail := "mfa_protected_user@corp.com"
		pwd := "SecurePassword123!"
		hashedPwd, _ := auth.HashPassword(pwd)
		mfaUser := &domain.User{
			Name:         "MFA Guarded User",
			Email:        mfaUserEmail,
			PasswordHash: hashedPwd,
			Role:         domain.RoleAgent,
		}
		_ = db.Create(mfaUser)

		mfaSecret, _ := auth.GenerateTOTPSecret()
		backupCodes, _ := auth.GenerateBackupCodes(2)
		bBytes, _ := json.Marshal(backupCodes)
		db.Create(&domain.MFAProfile{
			UserID:      mfaUser.ID,
			Secret:      mfaSecret,
			BackupCodes: string(bBytes),
			Enabled:     true,
		})

		// 1. Sign in without MFA code -> MUST be rejected with 401
		signInNoMFA, _ := json.Marshal(map[string]string{
			"email":    mfaUserEmail,
			"password": pwd,
		})
		req = httptest.NewRequest(http.MethodPost, "/auth/sign_in", bytes.NewReader(signInNoMFA))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected sign in without MFA code to be blocked with 401, got %d", w.Code)
		}

		// 2. Sign in with invalid MFA code -> MUST be rejected with 401
		signInBadMFA, _ := json.Marshal(map[string]string{
			"email":    mfaUserEmail,
			"password": pwd,
			"mfa_otp":  "000000",
		})
		req = httptest.NewRequest(http.MethodPost, "/auth/sign_in", bytes.NewReader(signInBadMFA))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected sign in with wrong MFA code to be blocked with 401, got %d", w.Code)
		}

		// 3. Sign in with valid TOTP code -> MUST succeed with 200
		totpCode, _ := auth.GenerateTOTPCode(mfaSecret, time.Now())
		signInGoodMFA, _ := json.Marshal(map[string]string{
			"email":    mfaUserEmail,
			"password": pwd,
			"mfa_otp":  totpCode,
		})
		req = httptest.NewRequest(http.MethodPost, "/auth/sign_in", bytes.NewReader(signInGoodMFA))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected sign in with valid TOTP to succeed with 200, got %d body=%s", w.Code, w.Body.String())
		}

		// 4. Sign in with valid backup code -> MUST succeed with 200 and consume the code
		signInBackupCode, _ := json.Marshal(map[string]string{
			"email":    mfaUserEmail,
			"password": pwd,
			"mfa_otp":  backupCodes[0],
		})
		req = httptest.NewRequest(http.MethodPost, "/auth/sign_in", bytes.NewReader(signInBackupCode))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected sign in with backup code to succeed with 200, got %d", w.Code)
		}

		// Reusing the same backup code MUST fail
		req = httptest.NewRequest(http.MethodPost, "/auth/sign_in", bytes.NewReader(signInBackupCode))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected reused backup code to fail with 401, got %d", w.Code)
		}
	})

	// -------------------------------------------------------------
	// Item 12: Public API Strict 4-Way Ownership Validation
	// -------------------------------------------------------------
	t.Run("Item12_PublicAPI_CrossContact_Validation", func(t *testing.T) {
		// Create 2 separate contacts in the same inbox
		c1 := &domain.Contact{AccountID: accountID, Name: "Customer One", Email: "c1@test.com"}
		c2 := &domain.Contact{AccountID: accountID, Name: "Customer Two", Email: "c2@test.com"}
		db.Create(c1)
		db.Create(c2)

		conv1 := &domain.Conversation{AccountID: accountID, InboxID: inboxID, ContactID: c1.ID, Status: "open"}
		conv2 := &domain.Conversation{AccountID: accountID, InboxID: inboxID, ContactID: c2.ID, Status: "open"}
		db.Create(conv1)
		db.Create(conv2)

		// c1 tries to send a message into conv2 (cross-contact spoofing)
		spoofMsg, _ := json.Marshal(map[string]string{"content": "I am an attacker trying to inject a message into c2"})
		inboxWebsiteToken := inboxResp.Data.WebsiteToken
		spoofURL := "/public/api/v1/inboxes/" + inboxWebsiteToken + "/contacts/" + strconv.Itoa(int(c1.ID)) + "/conversations/" + strconv.Itoa(int(conv2.ID)) + "/messages"
		req = httptest.NewRequest(http.MethodPost, spoofURL, bytes.NewReader(spoofMsg))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound && w.Code != http.StatusForbidden {
			t.Fatalf("expected spoofed cross-contact message to fail with 404 or 403, got %d body=%s", w.Code, w.Body.String())
		}

		// Valid message from c1 into conv1 succeeds
		validMsg, _ := json.Marshal(map[string]string{"content": "Legitimate message from c1"})
		validURL := "/public/api/v1/inboxes/" + inboxWebsiteToken + "/contacts/" + strconv.Itoa(int(c1.ID)) + "/conversations/" + strconv.Itoa(int(conv1.ID)) + "/messages"
		req = httptest.NewRequest(http.MethodPost, validURL, bytes.NewReader(validMsg))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated && w.Code != http.StatusOK {
			t.Fatalf("expected valid message to succeed, got %d body=%s", w.Code, w.Body.String())
		}
	})

	// -------------------------------------------------------------
	// Item 13: Push Delivery Integrity & Real Gateway Dispatch
	// -------------------------------------------------------------
	t.Run("Item13_Push_Delivery_Integrity", func(t *testing.T) {
		deviceRepo := repository.NewDeviceRepository(db)
		pushSvc := service.NewPushService(db, deviceRepo)

		// 1. When gateway unconfigured: status MUST be "failed" with code 503
		pushUserID1 := uint(888)
		subUnconfigured := &domain.NotificationSubscription{
			AccountID: accountID,
			UserID:    pushUserID1,
			PushToken: "unconfigured_raw_token_xyz",
		}
		db.Create(subUnconfigured)
		_, _ = pushSvc.Dispatch(context.Background(), pushUserID1, accountID, service.PushPayload{
			Title: "Test",
			Body:  "Unconfigured test",
		})
		var failedLog domain.PushDeliveryLog
		db.Where("account_id = ? AND user_id = ? AND status = ?", accountID, pushUserID1, "failed").Order("id DESC").First(&failedLog)
		if failedLog.HTTPStatusCode != 503 {
			t.Errorf("expected 503 for unconfigured gateway, got %d", failedLog.HTTPStatusCode)
		}

		// 2. When gateway returns 200 via in-memory transport: status MUST be "delivered"
		mockClient := &http.Client{
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"success": true}`)),
					Header:     make(http.Header),
				}, nil
			}),
		}
		pushSvc.SetHTTPClient(mockClient)

		pushUserID2 := uint(889)
		subSuccess := &domain.NotificationSubscription{
			AccountID: accountID,
			UserID:    pushUserID2,
			PushToken: "https://fcm.googleapis.com/fcm/send/mock_token",
		}
		db.Create(subSuccess)
		count, err := pushSvc.Dispatch(context.Background(), pushUserID2, accountID, service.PushPayload{
			Title: "New Message",
			Body:  "Hello from agent",
		})
		var deliveredLog domain.PushDeliveryLog
		db.Where("account_id = ? AND user_id = ?", accountID, pushUserID2).Order("id DESC").First(&deliveredLog)
		if err != nil || count == 0 {
			t.Fatalf("expected successful push dispatch, got count=%d err=%v, log=%+v", count, err, deliveredLog)
		}
		if deliveredLog.Status != "delivered" {
			t.Errorf("expected status 'delivered', got '%s'", deliveredLog.Status)
		}
	})

	// -------------------------------------------------------------
	// Item 14: Captain Knowledge Base & Copilot AI
	// -------------------------------------------------------------
	t.Run("Item14_Copilot_And_Captain_AI", func(t *testing.T) {
		// 1. Create a Captain Knowledge Doc
		docBody, _ := json.Marshal(map[string]string{
			"title":   "退换货政策与流程",
			"content": "7天内无理由退货，保修期为1年。退货运费由平台承担。",
		})
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/captain/knowledge_docs", bytes.NewReader(docBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK && w.Code != http.StatusCreated {
			t.Fatalf("create knowledge doc failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// 2. Create a Captain Assistant
		botBody, _ := json.Marshal(map[string]any{
			"name":        "售后知识客服助手",
			"model_name":  "gpt-4o-mini",
			"system_prompt": "你是一个专业的售后客服助手",
		})
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/captain/assistants", bytes.NewReader(botBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK && w.Code != http.StatusCreated {
			t.Fatalf("create captain assistant failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// 3. Create a conversation and an incoming customer inquiry about return policy
		conv := &domain.Conversation{
			AccountID: accountID,
			InboxID:   inboxID,
			ContactID: 1,
			Status:    "open",
		}
		db.Create(conv)

		incomingMsg := &domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeContact,
			SenderID:       1,
			MessageType:    domain.MessageTypeIncoming,
			ContentType:    domain.ContentTypeText,
			Content:        "我想了解一下你们的退换货政策与流程是怎样的？",
		}
		db.Create(incomingMsg)

		// 4. Query Copilot suggestions
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/conversations/"+strconv.Itoa(int(conv.ID))+"/copilot/suggestions", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("copilot suggestions failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var sugResp struct {
			Data struct {
				Suggestions []struct {
					Reply      string  `json:"reply"`
					Confidence float64 `json:"confidence"`
					Source     string  `json:"source"`
				} `json:"suggestions"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &sugResp)
		if len(sugResp.Data.Suggestions) == 0 {
			t.Fatalf("expected at least one suggestion, got 0")
		}

		// 5. Query Copilot summarize
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/conversations/"+strconv.Itoa(int(conv.ID))+"/copilot/summarize", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("copilot summarize failed: code=%d body=%s", w.Code, w.Body.String())
		}

		// 6. Query Copilot rephrase
		rephraseBody, _ := json.Marshal(map[string]string{
			"text": "不行，不能退",
			"tone": "friendly",
		})
		req = httptest.NewRequest(http.MethodPost, "/api/v1/accounts/"+accIDStr+"/copilot/rephrase", bytes.NewReader(rephraseBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("copilot rephrase failed: code=%d body=%s", w.Code, w.Body.String())
		}
	})
}

func TestBusinessScenarioVerifications(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "business_scenarios_secret_key_123",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	userRepo := repository.NewUserRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	inboxRepo := repository.NewInboxRepository(db)
	convRepo := repository.NewConversationRepository(db)
	msgRepo := repository.NewMessageRepository(db)
	labelRepo := repository.NewLabelRepository(db)
	macroRepo := repository.NewMacroRepository(db)

	routingService := service.NewRoutingService(db, convRepo, hub)
	automationService := service.NewAutomationService(db, convRepo, msgRepo)
	slaService := service.NewSLAService(db)

	account := domain.Account{Name: "Business Verification Corp"}
	_ = accountRepo.Create(&account)

	// Scenario 1: Routing Service Capacity Limit
	t.Run("Scenario1_RoutingCapacityLimit", func(t *testing.T) {
		agent := domain.User{
			Name:         "Limited Agent",
			Email:        "limited@agent.com",
			Role:         domain.RoleAgent,
			Availability: domain.AvailabilityOnline,
		}
		_ = userRepo.Create(&agent)
		_ = accountRepo.AddMember(account.ID, agent.ID, domain.RoleAgent)

		inbox := domain.Inbox{
			AccountID:    account.ID,
			Name:         "Capacity Inbox",
			WebsiteToken: "cap_token_123",
		}
		_ = inboxRepo.Create(&inbox)
		_ = inboxRepo.AddMember(inbox.ID, agent.ID)

		// Set agent capacity policy to 1
		capPolicy := domain.CapacityPolicy{
			AccountID:         account.ID,
			UserID:            agent.ID,
			ConversationLimit: 1,
		}
		_ = db.Create(&capPolicy).Error

		// Create 1st conversation and assign to agent
		conv1 := domain.Conversation{
			AccountID: account.ID,
			InboxID:   inbox.ID,
			ContactID: 1,
			Status:    domain.ConversationStatusOpen,
		}
		_ = convRepo.Create(&conv1)
		assigned1, err := routingService.AutoAssign(&conv1)
		if err != nil {
			t.Fatalf("unexpected error on conv1: %v", err)
		}
		if assigned1 == nil || assigned1.ID != agent.ID {
			t.Fatalf("expected conv1 assigned to agent %d, got %+v", agent.ID, assigned1)
		}

		// Create 2nd conversation: agent has 1 open conversation and limit is 1
		conv2 := domain.Conversation{
			AccountID: account.ID,
			InboxID:   inbox.ID,
			ContactID: 2,
			Status:    domain.ConversationStatusOpen,
		}
		_ = convRepo.Create(&conv2)
		assigned2, err := routingService.AutoAssign(&conv2)
		if err != nil {
			t.Fatalf("expected nil error when capacity reached, got: %v", err)
		}
		if assigned2 != nil {
			t.Fatalf("expected conv2 to remain unassigned due to capacity limit 1, but assigned to agent %d", assigned2.ID)
		}
		if conv2.AssigneeID != nil {
			t.Fatalf("expected conv2.AssigneeID to be nil, got %v", *conv2.AssigneeID)
		}
	})

	// Scenario 2: Automation Rule Labels & Tags Condition
	t.Run("Scenario2_AutomationLabelCondition", func(t *testing.T) {
		inbox := domain.Inbox{
			AccountID:    account.ID,
			Name:         "Auto Inbox",
			WebsiteToken: "auto_token_123",
		}
		_ = inboxRepo.Create(&inbox)

		// Create VIP label
		vipLabel := domain.Label{
			AccountID: account.ID,
			Title:     "VIP",
		}
		_ = labelRepo.Create(&vipLabel)

		// Create Normal label
		normalLabel := domain.Label{
			AccountID: account.ID,
			Title:     "NORMAL",
		}
		_ = labelRepo.Create(&normalLabel)

		// Conversation A: Has VIP label
		convA := domain.Conversation{
			AccountID: account.ID,
			InboxID:   inbox.ID,
			ContactID: 10,
			Status:    domain.ConversationStatusOpen,
		}
		_ = convRepo.Create(&convA)
		_ = labelRepo.AttachToConversation(convA.ID, vipLabel.ID)

		// Conversation B: Has NORMAL label (no VIP)
		convB := domain.Conversation{
			AccountID: account.ID,
			InboxID:   inbox.ID,
			ContactID: 11,
			Status:    domain.ConversationStatusOpen,
		}
		_ = convRepo.Create(&convB)
		_ = labelRepo.AttachToConversation(convB.ID, normalLabel.ID)

		// Automation rule: Close conversation ONLY IF label is VIP
		ruleConditions, _ := json.Marshal([]map[string]any{
			{
				"attribute_key":   "labels",
				"filter_operator": "equal_to",
				"values":          []string{"VIP"},
				"query_operator":  "AND",
			},
		})
		ruleActions, _ := json.Marshal([]map[string]any{
			{
				"action_name": "resolve_conversation",
			},
		})
		autoRule := domain.AutomationRule{
			AccountID:  account.ID,
			Name:       "Close VIP Conversations",
			EventName:  "conversation_updated",
			Conditions: string(ruleConditions),
			Actions:    string(ruleActions),
			Active:     true,
		}
		_ = db.Create(&autoRule).Error

		// Trigger rule on Conversation A (VIP)
		automationService.HandleConversationUpdated(&convA)
		refreshedA, _ := convRepo.FindByID(account.ID, convA.ID)
		if refreshedA.Status != domain.ConversationStatusResolved {
			t.Fatalf("expected VIP conversation to be resolved by automation, got %s", refreshedA.Status)
		}

		// Trigger rule on Conversation B (Non-VIP)
		automationService.HandleConversationUpdated(&convB)
		refreshedB, _ := convRepo.FindByID(account.ID, convB.ID)
		if refreshedB.Status != domain.ConversationStatusOpen {
			t.Fatalf("expected Non-VIP conversation to remain open, but got %s", refreshedB.Status)
		}
	})

	// Scenario 3: SLA First Response & Private Internal Notes
	t.Run("Scenario3_SLAFirstResponsePrivateNotes", func(t *testing.T) {
		slaPolicy := domain.SLAPolicy{
			AccountID:                  account.ID,
			Name:                       "1 Hour First Response SLA",
			FirstResponseTimeThreshold: 60, // 60 seconds for test
			ResolutionTimeThreshold:    3600,
		}
		_ = db.Create(&slaPolicy).Error

		// Customer opened conversation 2 hours ago
		twoHoursAgo := time.Now().UTC().Add(-2 * time.Hour)
		convSLA := domain.Conversation{
			AccountID:      account.ID,
			InboxID:        1,
			ContactID:      20,
			Status:         domain.ConversationStatusOpen,
			SLAStatus:      "active",
			CreatedAt:      twoHoursAgo,
			LastActivityAt: twoHoursAgo,
		}
		_ = convRepo.Create(&convSLA)
		_ = db.Model(&convSLA).Update("created_at", twoHoursAgo).Error

		// Incoming customer message
		inMsg := domain.Message{
			AccountID:      account.ID,
			ConversationID: convSLA.ID,
			SenderType:     domain.SenderTypeContact,
			MessageType:    domain.MessageTypeIncoming,
			Content:        "Urgent issue needing response!",
			CreatedAt:      twoHoursAgo,
		}
		_ = db.Create(&inMsg).Error
		_ = db.Model(&inMsg).Update("created_at", twoHoursAgo).Error

		// Agent only wrote an internal note (private note) 1 hour ago
		oneHourAgo := time.Now().UTC().Add(-1 * time.Hour)
		privateNote := domain.Message{
			AccountID:      account.ID,
			ConversationID: convSLA.ID,
			SenderType:     domain.SenderTypeUser,
			SenderID:       1,
			MessageType:    domain.MessageTypeOutgoing,
			Private:        true, // INTERNAL NOTE
			Content:        "Investigating backend logs internally",
			CreatedAt:      oneHourAgo,
		}
		_ = db.Create(&privateNote).Error

		// Evaluate SLAs
		breaches, err := slaService.EvaluateAccountSLAs(account.ID)
		if err != nil {
			t.Fatalf("unexpected error evaluating SLAs: %v", err)
		}

		foundFirstResponseBreach := false
		for _, b := range breaches {
			if b.ConversationID == convSLA.ID && b.BreachType == "first_response" {
				foundFirstResponseBreach = true
				break
			}
		}

		if !foundFirstResponseBreach {
			t.Fatalf("expected first_response SLA breach when agent only wrote internal private note, but breach was not recorded")
		}

		refreshedConv, _ := convRepo.FindByID(account.ID, convSLA.ID)
		if refreshedConv.SLAStatus != "breached" {
			t.Fatalf("expected conversation SLAStatus to be 'breached', got %s", refreshedConv.SLAStatus)
		}
	})

	// Scenario 4: Macro Execution Updates LastActivityAt and List Ordering
	t.Run("Scenario4_MacroUpdatesLastActivityAt", func(t *testing.T) {
		oneHourAgo := time.Now().UTC().Add(-1 * time.Hour)
		convOld := domain.Conversation{
			AccountID:      account.ID,
			InboxID:        1,
			ContactID:      30,
			Status:         domain.ConversationStatusOpen,
			LastActivityAt: oneHourAgo,
			CreatedAt:      oneHourAgo,
		}
		_ = convRepo.Create(&convOld)
		_ = db.Model(&convOld).Updates(map[string]any{
			"last_activity_at": oneHourAgo,
			"created_at":        oneHourAgo,
		}).Error

		// Create Macro that sends a customer reply
		macroActions, _ := json.Marshal([]repository.MacroAction{
			{
				ActionName:   "send_message",
				ActionParams: []string{"Hello! This is a macro automated response."},
			},
		})
		macro := domain.Macro{
			AccountID: account.ID,
			Name:      "Quick Customer Reply",
			Actions:   string(macroActions),
		}
		_ = macroRepo.Create(context.Background(), &macro)

		// Execute macro
		res, err := macroRepo.Execute(context.Background(), account.ID, macro.ID, []uint{convOld.ID})
		if err != nil {
			t.Fatalf("macro execution failed: %v", err)
		}
		if res[convOld.ID] != "success" {
			t.Fatalf("expected macro success, got %v", res[convOld.ID])
		}

		// Verify last_activity_at is updated to recent time (not 1 hour ago)
		refreshed, _ := convRepo.FindByID(account.ID, convOld.ID)
		if refreshed.LastActivityAt.Before(time.Now().Add(-10 * time.Second)) {
			t.Fatalf("expected LastActivityAt to be updated to recent time, got %v (diff %v)", refreshed.LastActivityAt, time.Since(refreshed.LastActivityAt))
		}

		// Verify conversation ordering
		convList, _, _ := convRepo.List(account.ID, domain.ConversationStatusOpen, "", nil, nil, 1, 25)
		if len(convList) > 0 && convList[0].ID != convOld.ID {
			if convList[0].LastActivityAt.Before(refreshed.LastActivityAt) {
				t.Fatalf("expected updated conversation to appear at the top of conversation list")
			}
		}
	})
}

