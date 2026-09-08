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
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestEndToEndCustomerSupportWorkflow(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "e2e-super-secret-production-grade-key-2026",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()

	engine := router.SetupRouter(cfg, db, hub)

	// Step 1: Health check
	wHealth := httptest.NewRecorder()
	reqHealth, _ := http.NewRequest("GET", "/health", nil)
	engine.ServeHTTP(wHealth, reqHealth)
	if wHealth.Code != http.StatusOK {
		t.Fatalf("health check failed: %d", wHealth.Code)
	}

	// Step 2: Admin Sign Up
	signUpBody, _ := json.Marshal(map[string]string{
		"name":         "Operations Admin",
		"email":        "ops@enterprise.com",
		"password":     "supersecurepassword123",
		"account_name": "Enterprise Support",
	})
	wSignUp := httptest.NewRecorder()
	reqSignUp, _ := http.NewRequest("POST", "/auth/sign_up", bytes.NewBuffer(signUpBody))
	reqSignUp.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wSignUp, reqSignUp)

	if wSignUp.Code != http.StatusCreated {
		t.Fatalf("sign up failed: %d, body: %s", wSignUp.Code, wSignUp.Body.String())
	}

	var signUpRes struct {
		Data struct {
			Token string `json:"token"`
			User  struct {
				ID uint `json:"id"`
			} `json:"user"`
			Accounts []struct {
				ID uint `json:"id"`
			} `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wSignUp.Body.Bytes(), &signUpRes)
	token := signUpRes.Data.Token
	adminID := signUpRes.Data.User.ID
	accountID := signUpRes.Data.Accounts[0].ID

	// Step 3: Admin creates a Website Inbox
	inboxBody, _ := json.Marshal(map[string]any{
		"name":             "Website Live Support",
		"channel_type":     "Channel::WebWidget",
		"greeting_message": "Welcome! How can we assist you today?",
	})
	wInbox := httptest.NewRecorder()
	reqInbox, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/inboxes", accountID), bytes.NewBuffer(inboxBody))
	reqInbox.Header.Set("Authorization", "Bearer "+token)
	reqInbox.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wInbox, reqInbox)

	if wInbox.Code != http.StatusCreated {
		t.Fatalf("create inbox failed: %d, body: %s", wInbox.Code, wInbox.Body.String())
	}

	var inboxRes struct {
		Data struct {
			ID           uint   `json:"id"`
			WebsiteToken string `json:"website_token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wInbox.Body.Bytes(), &inboxRes)
	inboxID := inboxRes.Data.ID
	websiteToken := inboxRes.Data.WebsiteToken

	// Add Admin to Inbox members so they receive auto-assignments
	addMemberBody, _ := json.Marshal(map[string]any{
		"user_ids": []uint{adminID},
	})
	wMember := httptest.NewRecorder()
	reqMember, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/members", accountID, inboxID), bytes.NewBuffer(addMemberBody))
	reqMember.Header.Set("Authorization", "Bearer "+token)
	reqMember.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wMember, reqMember)
	if wMember.Code != http.StatusOK {
		t.Fatalf("add member failed: %d", wMember.Code)
	}

	// Step 4: Customer checks widget config
	wConfig := httptest.NewRecorder()
	reqConfig, _ := http.NewRequest("GET", "/api/v1/widget/config?website_token="+websiteToken, nil)
	engine.ServeHTTP(wConfig, reqConfig)
	if wConfig.Code != http.StatusOK {
		t.Fatalf("widget config failed: %d", wConfig.Code)
	}

	// Step 5: Customer starts a conversation via widget
	convBody, _ := json.Marshal(map[string]string{
		"source_id": "customer_session_42",
		"message":   "I would like to enquire about enterprise plans.",
	})
	wConv := httptest.NewRecorder()
	reqConv, _ := http.NewRequest("POST", "/api/v1/widget/conversations?website_token="+websiteToken, bytes.NewBuffer(convBody))
	reqConv.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wConv, reqConv)

	if wConv.Code != http.StatusCreated {
		t.Fatalf("widget start conv failed: %d, body: %s", wConv.Code, wConv.Body.String())
	}

	var convData struct {
		Data struct {
			Conversation struct {
				ID         uint  `json:"id"`
				AssigneeID *uint `json:"assignee_id"`
				Status     string
			} `json:"conversation"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wConv.Body.Bytes(), &convData)
	convID := convData.Data.Conversation.ID

	if convID == 0 {
		t.Fatalf("expected valid conversation ID, got 0")
	}

	// Verify auto-assignment to the online admin
	if convData.Data.Conversation.AssigneeID == nil || *convData.Data.Conversation.AssigneeID != adminID {
		t.Fatalf("expected conversation to be auto-assigned to admin %d, got %+v", adminID, convData.Data.Conversation.AssigneeID)
	}

	// Step 6: Agent views the conversation in their inbox
	wGetConv := httptest.NewRecorder()
	reqGetConv, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d", accountID, convID), nil)
	reqGetConv.Header.Set("Authorization", "Bearer "+token)
	engine.ServeHTTP(wGetConv, reqGetConv)
	if wGetConv.Code != http.StatusOK {
		t.Fatalf("get conversation failed: %d", wGetConv.Code)
	}

	// Step 7: Agent sends reply
	replyBody, _ := json.Marshal(map[string]any{
		"content": "Our enterprise plan includes dedicated SLA, custom onboarding, and 24/7 priority support.",
		"private": false,
	})
	wReply := httptest.NewRecorder()
	reqReply, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages", accountID, convID), bytes.NewBuffer(replyBody))
	reqReply.Header.Set("Authorization", "Bearer "+token)
	reqReply.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wReply, reqReply)
	if wReply.Code != http.StatusCreated {
		t.Fatalf("agent reply failed: %d", wReply.Code)
	}

	// Step 8: Agent creates and attaches a Label
	labelBody, _ := json.Marshal(map[string]string{
		"title": "enterprise-sales",
		"color": "#00aa55",
	})
	wLabel := httptest.NewRecorder()
	reqLabel, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/labels", accountID), bytes.NewBuffer(labelBody))
	reqLabel.Header.Set("Authorization", "Bearer "+token)
	reqLabel.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wLabel, reqLabel)

	var labelRes struct {
		Data struct {
			ID uint `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wLabel.Body.Bytes(), &labelRes)
	labelID := labelRes.Data.ID

	attachBody, _ := json.Marshal(map[string]any{
		"label_ids": []uint{labelID},
	})
	wAttach := httptest.NewRecorder()
	reqAttach, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/labels", accountID, convID), bytes.NewBuffer(attachBody))
	reqAttach.Header.Set("Authorization", "Bearer "+token)
	reqAttach.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wAttach, reqAttach)
	if wAttach.Code != http.StatusOK {
		t.Fatalf("attach label failed: %d, body: %s", wAttach.Code, wAttach.Body.String())
	}

	// Step 9: Agent resolves the conversation
	resolveBody, _ := json.Marshal(map[string]string{
		"status": "resolved",
	})
	wResolve := httptest.NewRecorder()
	reqResolve, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/toggle_status", accountID, convID), bytes.NewBuffer(resolveBody))
	reqResolve.Header.Set("Authorization", "Bearer "+token)
	reqResolve.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wResolve, reqResolve)
	if wResolve.Code != http.StatusOK {
		t.Fatalf("resolve conversation failed: %d", wResolve.Code)
	}

	// Step 10: Check Reports Summary
	wSummary := httptest.NewRecorder()
	reqSummary, _ := http.NewRequest("GET", fmt.Sprintf("/api/v2/accounts/%d/reports/summary", accountID), nil)
	reqSummary.Header.Set("Authorization", "Bearer "+token)
	engine.ServeHTTP(wSummary, reqSummary)
	if wSummary.Code != http.StatusOK {
		t.Fatalf("reports summary failed: %d", wSummary.Code)
	}

	var sumRes struct {
		Data service.AccountSummaryReport `json:"data"`
	}
	_ = json.Unmarshal(wSummary.Body.Bytes(), &sumRes)

	if sumRes.Data.TotalConversations != 1 {
		t.Errorf("expected 1 total conversation, got %d", sumRes.Data.TotalConversations)
	}
	if sumRes.Data.ResolvedConversations != 1 {
		t.Errorf("expected 1 resolved conversation, got %d", sumRes.Data.ResolvedConversations)
	}
	if sumRes.Data.TotalMessages != 2 {
		t.Errorf("expected 2 total messages, got %d", sumRes.Data.TotalMessages)
	}

	_ = inboxID
	_ = accountID
}
