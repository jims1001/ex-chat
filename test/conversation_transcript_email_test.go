package test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
)

func TestConversationTranscriptEmailDeliveryAndAudit(t *testing.T) {
	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test-jwt-secret-transcript-test-2026",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up admin user
	signUpPayload := map[string]string{
		"account_name": "Transcript Test Corp",
		"name":         "Transcript Admin",
		"email":        "admin@transcript-test.com",
		"password":     "Password123!",
	}
	body, _ := json.Marshal(signUpPayload)
	req := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("sign up failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var authResp struct {
		Data struct {
			Token    string           `json:"token"`
			User     domain.User      `json:"user"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID

	// 2. Create Inbox (Channel::WebWidget)
	inboxPayload := map[string]any{
		"name":         "Transcript Support Inbox",
		"channel_type": domain.ChannelWebWidget,
	}
	body, _ = json.Marshal(inboxPayload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes", accountID), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create inbox failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var inboxResp struct {
		Data struct {
			ID           uint   `json:"id"`
			WebsiteToken string `json:"website_token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &inboxResp)
	inboxID := inboxResp.Data.ID
	websiteToken := inboxResp.Data.WebsiteToken

	// 3. Create Contact via Widget
	contactPayload := map[string]any{
		"source_id": "src_alice_12345",
		"name":      "Alice Customer",
		"email":     "alice.customer@example.org",
	}
	body, _ = json.Marshal(contactPayload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/widget/contact?website_token=%s", websiteToken), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create widget contact failed: code=%d body=%s", w.Code, w.Body.String())
	}
	var contactResp struct {
		Data struct {
			ID       uint   `json:"id"`
			SourceID string `json:"source_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &contactResp)
	contactID := contactResp.Data.ID

	// 4. Create Conversation
	convPayload := map[string]any{
		"inbox_id":   inboxID,
		"contact_id": contactID,
		"priority":   domain.PriorityMedium,
	}
	body, _ = json.Marshal(convPayload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations", accountID), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create conversation failed: code=%d body=%s", w.Code, w.Body.String())
	}
	var convResp struct {
		Data struct {
			ID        uint `json:"id"`
			DisplayID uint `json:"display_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &convResp)
	convID := convResp.Data.ID

	// 5. Create Messages: incoming message, outgoing agent message, and private note
	// 5.1 Incoming message
	msg1Payload := map[string]any{
		"source_id":       "src_alice_12345",
		"conversation_id": convID,
		"content":         "Hello, I need help with my invoice #INV-9021!",
	}
	body, _ = json.Marshal(msg1Payload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/widget/messages?website_token=%s", websiteToken), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("send incoming message failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// 5.2 Outgoing agent reply
	msg2Payload := map[string]any{
		"content": "Hi Alice, let me check the invoice status for you right away.",
		"private": false,
	}
	body, _ = json.Marshal(msg2Payload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages", accountID, convID), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("send agent message failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// 5.3 Internal Private Note (should NOT appear in widget customer transcript)
	msg3Payload := map[string]any{
		"content": "Internal note: Customer account has pending credit adjustment.",
		"private": true,
	}
	body, _ = json.Marshal(msg3Payload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages", accountID, convID), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("send internal note failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// ----------------------------------------------------
	// Test Scenario 1: Agent sends full transcript to supervisor
	// ----------------------------------------------------
	t.Run("AgentSendTranscript_RealDeliveryAndLog", func(t *testing.T) {
		supervisorEmail := "supervisor@company.org"
		transcriptPayload := map[string]string{
			"email": supervisorEmail,
		}
		body, _ := json.Marshal(transcriptPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/transcript", accountID, convID), bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}

		var resp struct {
			Data struct {
				Message       string `json:"message"`
				Email         string `json:"email"`
				MessagesCount int    `json:"messages_count"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Data.Email != supervisorEmail || resp.Data.MessagesCount < 3 {
			t.Fatalf("unexpected response: %+v", resp)
		}

		// Verify database EmailLog record
		var log domain.EmailLog
		err := db.Where("account_id = ? AND to_email = ?", accountID, supervisorEmail).Order("id DESC").First(&log).Error
		if err != nil {
			t.Fatalf("expected EmailLog in database, got error: %v", err)
		}

		if log.Status != "sent" {
			t.Fatalf("expected log status 'sent', got '%s'", log.Status)
		}
		if *log.ConversationID != convID {
			t.Fatalf("expected conversation_id %d, got %v", convID, *log.ConversationID)
		}
		if !strings.Contains(log.Subject, "会话记录") {
			t.Fatalf("expected subject to contain '会话记录', got '%s'", log.Subject)
		}
		if !strings.Contains(log.ContentHTML, "#INV-9021") {
			t.Fatalf("expected HTML transcript to include invoice message, got %s", log.ContentHTML)
		}
		if !strings.Contains(log.ContentText, "Internal note:") {
			t.Fatalf("expected agent transcript text to include internal note, got %s", log.ContentText)
		}
	})

	// ----------------------------------------------------
	// Test Scenario 2: Widget visitor sends transcript copy
	// ----------------------------------------------------
	t.Run("WidgetVisitorSendTranscript_ExcludesPrivateNotes", func(t *testing.T) {
		visitorEmail := "alice.copy@example.org"
		widgetPayload := map[string]any{
			"website_token":   websiteToken,
			"conversation_id": convID,
			"email":           visitorEmail,
		}
		body, _ := json.Marshal(widgetPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/widget/conversations/transcript?website_token=%s", websiteToken), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK from widget transcript, got %d: %s", w.Code, w.Body.String())
		}

		// Verify database EmailLog for visitor
		var log domain.EmailLog
		err := db.Where("account_id = ? AND to_email = ?", accountID, visitorEmail).Order("id DESC").First(&log).Error
		if err != nil {
			t.Fatalf("expected EmailLog for widget visitor, got: %v", err)
		}

		if log.Status != "sent" {
			t.Fatalf("expected status 'sent', got '%s'", log.Status)
		}
		// Private note MUST NOT be included for widget visitors!
		if strings.Contains(log.ContentHTML, "Internal note: Customer account has pending credit adjustment.") {
			t.Fatalf("security violation: private note leaked to widget visitor transcript!")
		}
		if strings.Contains(log.ContentText, "Internal note: Customer account has pending credit adjustment.") {
			t.Fatalf("security violation: private note leaked to widget visitor transcript text!")
		}
		if !strings.Contains(log.ContentHTML, "#INV-9021") {
			t.Fatalf("expected customer message to be in transcript")
		}
	})

	// ----------------------------------------------------
	// Test Scenario 3: Email fallback to contact email
	// ----------------------------------------------------
	t.Run("SendTranscript_FallbackToContactEmail", func(t *testing.T) {
		// When no email is provided in payload, fallback to contact's email: alice.customer@example.org
		emptyEmailPayload := map[string]string{}
		body, _ := json.Marshal(emptyEmailPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/transcript", accountID, convID), bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK on fallback email, got %d: %s", w.Code, w.Body.String())
		}

		var resp struct {
			Data struct {
				Email string `json:"email"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Data.Email != "alice.customer@example.org" {
			t.Fatalf("expected fallback email to 'alice.customer@example.org', got '%s'", resp.Data.Email)
		}
	})

	// ----------------------------------------------------
	// Test Scenario 4: Error handling and failed status logging
	// ----------------------------------------------------
	t.Run("EmailSendingFailure_AuditLogStatusFailed", func(t *testing.T) {
		// Create an EmailService with a failing sender
		mockSender := service.NewMockEmailSender()
		mockSender.SetFailError(errors.New("connection to smtp.relay.internal:587 timed out"))

		failingEmailService := service.NewEmailService(db)
		failingEmailService.SetSender(mockSender)

		// Create conversation object
		var conv domain.Conversation
		db.Where("id = ?", convID).First(&conv)
		var msgs []domain.Message
		db.Where("conversation_id = ?", convID).Find(&msgs)

		err := failingEmailService.SendTranscript(req.Context(), accountID, &conv, msgs, "fail-target@example.com")
		if err == nil {
			t.Fatalf("expected error from failing email service, got nil")
		}

		// Verify database EmailLog recorded the failure
		var failedLog domain.EmailLog
		err = db.Where("account_id = ? AND to_email = ?", accountID, "fail-target@example.com").First(&failedLog).Error
		if err != nil {
			t.Fatalf("expected failed EmailLog in DB, got: %v", err)
		}

		if failedLog.Status != "failed" {
			t.Fatalf("expected status 'failed', got '%s'", failedLog.Status)
		}
		if !strings.Contains(failedLog.Error, "timed out") {
			t.Fatalf("expected error message to contain 'timed out', got '%s'", failedLog.Error)
		}
	})

	// ----------------------------------------------------
	// Test Scenario 5: Branded layout customization
	// ----------------------------------------------------
	t.Run("BrandedEmailLayout_CustomStyling", func(t *testing.T) {
		branded := domain.BrandedEmailLayout{
			AccountID:   accountID,
			HeaderColor: "#e11d48", // Custom Rose color
			LogoURL:     "https://cdn.example.com/brand-logo.png",
			LayoutHTML:  "<div>Custom Layout</div>",
		}
		db.Save(&branded)

		emailSvc := service.NewEmailService(db)
		var conv domain.Conversation
		db.Where("id = ?", convID).First(&conv)
		var msgs []domain.Message
		db.Where("conversation_id = ?", convID).Find(&msgs)

		htmlContent := emailSvc.RenderTranscriptHTML(&conv, msgs)
		if !strings.Contains(htmlContent, "#e11d48") {
			t.Fatalf("expected HTML to use branded header color '#e11d48', got: %s", htmlContent)
		}
		if !strings.Contains(htmlContent, "https://cdn.example.com/brand-logo.png") {
			t.Fatalf("expected HTML to include branded logo URL, got: %s", htmlContent)
		}
	})
}
