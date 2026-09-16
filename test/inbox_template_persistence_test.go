package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestInboxTemplatePersistenceAndSync(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "inbox-template-test-secret-2026",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()

	engine := router.SetupRouter(cfg, db, hub)

	// Step 0: Sign up Admin User & Account
	signUpBody, _ := json.Marshal(map[string]string{
		"name":         "渠道管理员",
		"email":        "channel.admin@example.com",
		"password":     "password123456",
		"account_name": "多渠道消息中台",
	})
	wSignUp := httptest.NewRecorder()
	reqSignUp, _ := http.NewRequest("POST", "/auth/sign_up", bytes.NewBuffer(signUpBody))
	reqSignUp.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wSignUp, reqSignUp)
	if wSignUp.Code != http.StatusCreated {
		t.Fatalf("sign up failed: %d body: %s", wSignUp.Code, wSignUp.Body.String())
	}

	var authResp struct {
		Data struct {
			Token string `json:"token"`
			Accounts []struct {
				ID uint `json:"id"`
			} `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wSignUp.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID

	// Create WhatsApp Channel Inbox
	inbox := domain.Inbox{
		AccountID:    accountID,
		Name:         "WhatsApp Business Support",
		ChannelType:  "Channel::Whatsapp",
		WebsiteToken: "tok_whatsapp_template_sync",
	}
	db.Create(&inbox)

	t.Run("Scenario 1: Initial Sync With Empty Config Returns Empty Real Templates", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/sync_templates", accountID, inbox.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("sync templates failed: %d: %s", w.Code, w.Body.String())
		}

		var syncResp struct {
			Status    string           `json:"status"`
			Templates []map[string]any `json:"templates"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &syncResp)
		if syncResp.Status != "synced" {
			t.Fatalf("expected synced status, got %s", syncResp.Status)
		}
		// Without provider_config templates, sync should return 0 templates (no fake templates!)
		if len(syncResp.Templates) != 0 {
			t.Fatalf("expected 0 templates when unconfigured, got %d (should not fake templates)", len(syncResp.Templates))
		}

		// Now configure initial templates in provider_config and sync
		initialConfig := map[string]any{
			"templates": []map[string]any{
				{
					"name":     "sample_issue_resolution",
					"status":   "APPROVED",
					"category": "UTILITY",
					"language": "en_US",
					"components": []map[string]any{
						{"type": "BODY", "text": "Your issue {{1}} has been resolved."},
					},
				},
				{
					"name":     "customer_satisfaction_survey",
					"status":   "APPROVED",
					"category": "MARKETING",
					"language": "en_US",
					"components": []map[string]any{
						{"type": "BODY", "text": "Please rate your experience with us."},
					},
				},
			},
		}
		initialBytes, _ := json.Marshal(initialConfig)
		db.Model(&inbox).Update("provider_config", string(initialBytes))

		w2 := httptest.NewRecorder()
		req2, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/sync_templates", accountID, inbox.ID), nil)
		req2.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(w2, req2)

		if w2.Code != http.StatusOK {
			t.Fatalf("sync templates failed after config: %d", w2.Code)
		}

		var syncResp2 struct {
			Status    string           `json:"status"`
			Templates []map[string]any `json:"templates"`
		}
		_ = json.Unmarshal(w2.Body.Bytes(), &syncResp2)
		if len(syncResp2.Templates) != 2 {
			t.Fatalf("expected 2 synced templates from provider_config, got %d", len(syncResp2.Templates))
		}

		// Verify database table has persistent rows!
		var dbRows []domain.InboxMessageTemplate
		db.Where("account_id = ? AND inbox_id = ?", accountID, inbox.ID).Find(&dbRows)
		if len(dbRows) != 2 {
			t.Fatalf("expected 2 database persistent rows, got %d", len(dbRows))
		}
	})

	t.Run("Scenario 2: Create Custom Message Template and Retrieve by Filter", func(t *testing.T) {
		createBody, _ := json.Marshal(map[string]any{
			"name":     "order_shipping_tracking",
			"status":   "APPROVED",
			"category": "UTILITY",
			"language": "zh_CN",
			"components": []map[string]any{
				{"type": "HEADER", "text": "订单已发货通知"},
				{"type": "BODY", "text": "尊敬的客户，您的订单 {{1}} 已由 {{2}} 承运发出，运单号 {{3}}。"},
			},
		})
		wCreate := httptest.NewRecorder()
		reqCreate, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/message_templates", accountID, inbox.ID), bytes.NewBuffer(createBody))
		reqCreate.Header.Set("Authorization", "Bearer "+token)
		reqCreate.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wCreate, reqCreate)

		if wCreate.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created for custom template, got %d: %s", wCreate.Code, wCreate.Body.String())
		}

		// Query templates with name filter
		wGet := httptest.NewRecorder()
		reqGet, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/message_templates?name=shipping", accountID, inbox.ID), nil)
		reqGet.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wGet, reqGet)

		if wGet.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", wGet.Code, wGet.Body.String())
		}

		var getResp struct {
			Templates []map[string]any `json:"templates"`
		}
		_ = json.Unmarshal(wGet.Body.Bytes(), &getResp)
		if len(getResp.Templates) != 1 {
			t.Fatalf("expected 1 filtered template, got %d", len(getResp.Templates))
		}
		if getResp.Templates[0]["name"] != "order_shipping_tracking" {
			t.Fatalf("expected template name 'order_shipping_tracking', got %v", getResp.Templates[0]["name"])
		}
	})

	t.Run("Scenario 3: Sync from Inbox ProviderConfig Dynamically", func(t *testing.T) {
		// Update Inbox ProviderConfig with new external WhatsApp Cloud API templates
		customConfig := map[string]any{
			"templates": []map[string]any{
				{
					"name":     "payment_reminder_v2",
					"status":   "APPROVED",
					"category": "UTILITY",
					"language": "en_US",
					"components": []map[string]any{
						{"type": "BODY", "text": "Payment reminder for invoice {{1}}."},
					},
				},
				{
					"name":     "security_alert_login",
					"status":   "APPROVED",
					"category": "AUTHENTICATION",
					"language": "en_US",
					"components": []map[string]any{
						{"type": "BODY", "text": "Security code: {{1}}."},
					},
				},
			},
		}
		configBytes, _ := json.Marshal(customConfig)
		db.Model(&inbox).Update("provider_config", string(configBytes))

		// Trigger Sync
		wSync := httptest.NewRecorder()
		reqSync, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/sync_templates", accountID, inbox.ID), nil)
		reqSync.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wSync, reqSync)

		if wSync.Code != http.StatusOK {
			t.Fatalf("expected 200 OK on sync, got %d: %s", wSync.Code, wSync.Body.String())
		}

		// Verify payment_reminder_v2 and security_alert_login were persisted!
		var paymentTmpl domain.InboxMessageTemplate
		err := db.Where("account_id = ? AND inbox_id = ? AND name = ?", accountID, inbox.ID, "payment_reminder_v2").First(&paymentTmpl).Error
		if err != nil {
			t.Fatalf("expected payment_reminder_v2 to be persisted into DB: %v", err)
		}
		if paymentTmpl.Status != "APPROVED" {
			t.Fatalf("expected payment_reminder_v2 status APPROVED, got %s", paymentTmpl.Status)
		}
	})

	t.Run("Scenario 4: Delete Message Template and List Updated State", func(t *testing.T) {
		var target domain.InboxMessageTemplate
		db.Where("account_id = ? AND inbox_id = ? AND name = ?", accountID, inbox.ID, "sample_issue_resolution").First(&target)
		if target.ID == 0 {
			t.Fatalf("expected sample_issue_resolution in database")
		}

		wDel := httptest.NewRecorder()
		reqDel, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/message_templates/%d", accountID, inbox.ID, target.ID), nil)
		reqDel.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wDel, reqDel)

		if wDel.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for delete, got %d: %s", wDel.Code, wDel.Body.String())
		}

		// Verify deletion
		var count int64
		db.Model(&domain.InboxMessageTemplate{}).Where("account_id = ? AND inbox_id = ? AND id = ?", accountID, inbox.ID, target.ID).Count(&count)
		if count != 0 {
			t.Fatalf("expected template to be deleted from database, found count %d", count)
		}
	})
}
