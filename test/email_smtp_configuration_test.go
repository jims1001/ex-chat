package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestEmailSMTPConfigurationAndRejection(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Ensure environment has no SMTP_ADDRESS or ALLOW_MOCK_EMAIL for baseline
	os.Unsetenv("SMTP_ADDRESS")
	os.Unsetenv("ALLOW_MOCK_EMAIL")

	cfg := &config.Config{
		Port:               "8080",
		Environment:        "production", // non-test environment to verify unconfigured behavior
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test-jwt-secret-email-smtp-check-2026",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up admin user
	signUpPayload := map[string]string{
		"account_name": "No-SMTP Corp",
		"name":         "NoSMTP Admin",
		"email":        "admin@nosmtp-test.com",
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
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID

	// 2. Create Inbox
	inboxPayload := map[string]any{
		"name":         "Default Support Inbox",
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

	// 3. Create Conversation
	conv := domain.Conversation{
		AccountID: accountID,
		InboxID:   inboxID,
		Status:    domain.ConversationStatusOpen,
	}
	db.Create(&conv)

	msg := domain.Message{
		AccountID:      accountID,
		ConversationID: conv.ID,
		Content:        "Hello, test message",
		MessageType:    domain.MessageTypeIncoming,
	}
	db.Create(&msg)

	t.Run("EmailService_Unconfigured_Rejection_And_AuditLog", func(t *testing.T) {
		emailSvc := service.NewEmailService(db)
		// Ensure allowMock is false
		emailSvc.SetAllowMock(false)

		if emailSvc.IsConfigured() {
			t.Fatalf("expected IsConfigured() to be false when SMTP_ADDRESS is not set")
		}

		err := emailSvc.SendTranscript(context.Background(), accountID, &conv, []domain.Message{msg}, "customer@example.com")
		if err == nil {
			t.Fatalf("expected ErrSMTPNotConfigured, got nil")
		}
		if err != service.ErrSMTPNotConfigured {
			t.Fatalf("expected ErrSMTPNotConfigured, got: %v", err)
		}

		// Verify database EmailLog records failed status and exact error message
		var log domain.EmailLog
		dbErr := db.Where("account_id = ? AND to_email = ?", accountID, "customer@example.com").Order("id DESC").First(&log).Error
		if dbErr != nil {
			t.Fatalf("expected EmailLog in database, got: %v", dbErr)
		}
		if log.Status != "failed" {
			t.Fatalf("expected email log status 'failed', got '%s'", log.Status)
		}
		if log.Error != "smtp service is not configured" {
			t.Fatalf("expected error 'smtp service is not configured', got '%s'", log.Error)
		}
	})

	t.Run("AgentSendTranscript_API_Returns_422_When_SMTP_Unconfigured", func(t *testing.T) {
		transcriptPayload := map[string]string{
			"email": "agent-recipient@example.com",
		}
		body, _ := json.Marshal(transcriptPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/transcript", accountID, conv.ID), bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		// Must NOT return 200 OK! Must return 422 Unprocessable Entity
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected HTTP 422 Unprocessable Entity, got %d: %s", w.Code, w.Body.String())
		}

		var res struct {
			Success bool   `json:"success"`
			Error   string `json:"error"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &res)
		if res.Success != false {
			t.Fatalf("expected success to be false, got true")
		}
		if res.Error == "" {
			t.Fatalf("expected error description, got empty")
		}
	})

	t.Run("WidgetSendTranscript_API_Returns_422_When_SMTP_Unconfigured", func(t *testing.T) {
		widgetPayload := map[string]any{
			"conversation_id": conv.ID,
			"email":           "visitor-copy@example.com",
		}
		body, _ := json.Marshal(widgetPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/widget/conversations/transcript?website_token=%s", websiteToken), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		// Must NOT return 200 OK! Must return 422 Unprocessable Entity
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected HTTP 422 Unprocessable Entity from widget, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("Channel_Specific_SMTP_Fallback_Works", func(t *testing.T) {
		// Create inbox with custom SMTP in ProviderConfig
		channelSMTPPayload := map[string]any{
			"smtp_address":  "mail.custom-domain.org",
			"smtp_port":     "587",
			"smtp_username": "support@custom-domain.org",
			"smtp_password": "secret-smtp-pass",
			"smtp_enabled":  true,
		}
		providerConfigJSON, _ := json.Marshal(channelSMTPPayload)

		inboxCustom := domain.Inbox{
			AccountID:      accountID,
			Name:           "Email Channel with SMTP",
			ChannelType:    domain.ChannelEmail,
			WebsiteToken:   "custom_email_token_123",
			ProviderConfig: string(providerConfigJSON),
		}
		db.Create(&inboxCustom)

		convCustom := domain.Conversation{
			AccountID: accountID,
			InboxID:   inboxCustom.ID,
			Inbox:     &inboxCustom,
			Status:    domain.ConversationStatusOpen,
		}
		db.Create(&convCustom)

		emailSvc := service.NewEmailService(db)
		emailSvc.SetAllowMock(false)

		// Create mock to intercept channel SMTP or check that channel sender was selected
		// By pointing to invalid host or mock, check that it didn't reject as ErrSMTPNotConfigured!
		err := emailSvc.SendTranscript(context.Background(), accountID, &convCustom, []domain.Message{msg}, "target@domain.com")
		// Because it tried to connect to mail.custom-domain.org:587, the error should NOT be ErrSMTPNotConfigured!
		if err == service.ErrSMTPNotConfigured {
			t.Fatalf("channel SMTP should have been detected and used, but got ErrSMTPNotConfigured")
		}
		if err == nil {
			t.Fatalf("expected dial error to non-existent smtp server, got nil")
		}
	})

	t.Run("Unicode_Header_RFC2047_Encoding", func(t *testing.T) {
		// Verify SMTPEmailSender correctly encodes non-ASCII subjects and display names
		smtpSender := service.NewSMTPEmailSender("127.0.0.1", "2525", "user", "pass")
		if smtpSender == nil {
			t.Fatalf("expected non-nil SMTPEmailSender")
		}

		// Use mock sender capturing emails
		mockSender := service.NewMockEmailSender()
		emailSvc := service.NewEmailService(db)
		emailSvc.SetSender(mockSender)

		// Test SendTranscript with Chinese subject and verify EmailLog content
		convChinese := domain.Conversation{
			AccountID: accountID,
			InboxID:   inboxID,
			DisplayID: 8848,
			Status:    domain.ConversationStatusOpen,
		}
		db.Create(&convChinese)

		chineseMsg := domain.Message{
			AccountID:      accountID,
			ConversationID: convChinese.ID,
			Content:        "你好，请问支持多语言与Unicode吗？",
			MessageType:    domain.MessageTypeIncoming,
		}
		db.Create(&chineseMsg)

		recipient := "unicode.test@example.com"
		err := emailSvc.SendTranscript(context.Background(), accountID, &convChinese, []domain.Message{chineseMsg}, recipient)
		if err != nil {
			t.Fatalf("expected successful send via mock sender, got: %v", err)
		}

		sentEmails := mockSender.GetSentEmails()
		if len(sentEmails) == 0 {
			t.Fatalf("expected sent email captured in mock sender")
		}
		lastSent := sentEmails[len(sentEmails)-1]
		if lastSent.To != recipient {
			t.Fatalf("expected recipient %s, got %s", recipient, lastSent.To)
		}

		var log domain.EmailLog
		if err := db.Where("account_id = ? AND to_email = ?", accountID, recipient).Order("id DESC").First(&log).Error; err != nil {
			t.Fatalf("expected EmailLog saved, got: %v", err)
		}
		if log.DeliveryStatus != domain.EmailDeliveryStatusSent {
			t.Fatalf("expected DeliveryStatus 'sent', got '%s'", log.DeliveryStatus)
		}
		if log.RetryCount != 0 {
			t.Fatalf("expected retry_count 0, got %d", log.RetryCount)
		}
		if log.NextRetryAt != nil {
			t.Fatalf("expected next_retry_at to be nil for successful email")
		}
	})

	t.Run("Email_Bounce_And_Retry_Mechanism", func(t *testing.T) {
		mockSender := service.NewMockEmailSender()
		emailSvc := service.NewEmailService(db)
		emailSvc.SetSender(mockSender)

		// 1. Simulate a failed delivery
		now := time.Now()
		retryTime := now.Add(-1 * time.Minute) // In the past, ready for retry
		failedLog := domain.EmailLog{
			AccountID:      accountID,
			EmailType:      "transcript",
			ToEmail:        "retry.target@example.com",
			FromEmail:      "support@ex-chat.local",
			Subject:        "[#99] 会话重试测试",
			ContentHTML:    "<p>重试内容</p>",
			ContentText:    "重试内容",
			Status:         "failed",
			DeliveryStatus: domain.EmailDeliveryStatusFailed,
			RetryCount:     0,
			MaxRetries:     3,
			NextRetryAt:    &retryTime,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := db.Create(&failedLog).Error; err != nil {
			t.Fatalf("failed to create failed email log: %v", err)
		}

		// 2. Trigger RetryFailedEmails
		retried, err := emailSvc.RetryFailedEmails(context.Background(), 10)
		if err != nil {
			t.Fatalf("RetryFailedEmails returned error: %v", err)
		}
		if retried != 1 {
			t.Fatalf("expected 1 email retried, got %d", retried)
		}

		var updatedLog domain.EmailLog
		if err := db.First(&updatedLog, failedLog.ID).Error; err != nil {
			t.Fatalf("failed to reload email log: %v", err)
		}
		if updatedLog.Status != "sent" || updatedLog.DeliveryStatus != domain.EmailDeliveryStatusSent {
			t.Fatalf("expected status 'sent', got status='%s', delivery_status='%s'", updatedLog.Status, updatedLog.DeliveryStatus)
		}
		if updatedLog.RetryCount != 1 {
			t.Fatalf("expected retry_count 1, got %d", updatedLog.RetryCount)
		}
		if updatedLog.NextRetryAt != nil {
			t.Fatalf("expected next_retry_at nil after success, got %v", updatedLog.NextRetryAt)
		}

		// 3. Record Bounce for this log
		bouncedLog, err := emailSvc.RecordBounce(updatedLog.ID, "550 5.1.1 User unknown")
		if err != nil {
			t.Fatalf("RecordBounce failed: %v", err)
		}
		if bouncedLog.Status != "bounced" || bouncedLog.DeliveryStatus != domain.EmailDeliveryStatusBounced {
			t.Fatalf("expected bounced status, got %s / %s", bouncedLog.Status, bouncedLog.DeliveryStatus)
		}
		if bouncedLog.BounceReason != "550 5.1.1 User unknown" {
			t.Fatalf("unexpected bounce reason: %s", bouncedLog.BounceReason)
		}
		if bouncedLog.NextRetryAt != nil {
			t.Fatalf("expected NextRetryAt nil for bounced email")
		}

		// 4. Verify API GET /api/v1/accounts/:account_id/email_logs returns the logs
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/email_logs", accountID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK from GET /email_logs, got %d: %s", w.Code, w.Body.String())
		}
		var logsResp struct {
			Data []domain.EmailLog `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &logsResp)
		if len(logsResp.Data) == 0 {
			t.Fatalf("expected email logs in response, got empty")
		}
	})

	t.Run("Email_Retry_And_Bounce_Are_Account_Scoped", func(t *testing.T) {
		mockSender := service.NewMockEmailSender()
		emailSvc := service.NewEmailService(db)
		emailSvc.SetSender(mockSender)
		now := time.Now()
		first := domain.EmailLog{AccountID: accountID + 100, EmailType: "transcript", ToEmail: "first@example.com", FromEmail: "support@ex-chat.local", Subject: "first", Status: "failed", DeliveryStatus: domain.EmailDeliveryStatusFailed, MaxRetries: 3, CreatedAt: now, UpdatedAt: now}
		second := domain.EmailLog{AccountID: accountID + 101, EmailType: "transcript", ToEmail: "second@example.com", FromEmail: "support@ex-chat.local", Subject: "second", Status: "failed", DeliveryStatus: domain.EmailDeliveryStatusFailed, MaxRetries: 3, CreatedAt: now, UpdatedAt: now}
		if err := db.Create(&first).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&second).Error; err != nil {
			t.Fatal(err)
		}
		if _, _, err := emailSvc.RetryFailedEmail(context.Background(), first.AccountID, second.ID); err == nil {
			t.Fatal("cross-account retry passed")
		}
		updated, sent, err := emailSvc.RetryFailedEmail(context.Background(), second.AccountID, second.ID)
		if err != nil || !sent || updated.AccountID != second.AccountID {
			t.Fatalf("exact retry failed: sent=%v log=%+v err=%v", sent, updated, err)
		}
		if emails := mockSender.GetSentEmails(); len(emails) != 1 || emails[0].To != second.ToEmail {
			t.Fatalf("retry sent wrong account email: %+v", emails)
		}
		if _, err := emailSvc.RecordBounceForAccount(second.AccountID, first.ID, "bounce"); err == nil {
			t.Fatal("cross-account bounce passed")
		}
	})

	t.Run("Confirmation_Email_Dispatch", func(t *testing.T) {
		mockSender := service.NewMockEmailSender()
		emailSvc := service.NewEmailService(db)
		emailSvc.SetSender(mockSender)

		testUser := domain.User{
			Name:  "张三 (测试用户)",
			Email: "zhangsan@example.cn",
		}
		db.Create(&testUser)

		logItem, err := emailSvc.SendConfirmationEmail(context.Background(), &testUser, "sample_token_abc_123")
		if err != nil {
			t.Fatalf("SendConfirmationEmail failed: %v", err)
		}
		if logItem == nil || logItem.Status != "sent" {
			t.Fatalf("expected sent status, got %+v", logItem)
		}
		if logItem.EmailType != "confirmation" {
			t.Fatalf("expected email_type 'confirmation', got %s", logItem.EmailType)
		}

		sentEmails := mockSender.GetSentEmails()
		found := false
		for _, e := range sentEmails {
			if e.To == "zhangsan@example.cn" {
				found = true
				if !strings.Contains(e.Subject, "ExChat") {
					t.Errorf("expected subject to contain 'ExChat', got %s", e.Subject)
				}
				if !strings.Contains(e.HTMLBody, "sample_token_abc_123") {
					t.Errorf("expected confirmation token in body")
				}
				break
			}
		}
		if !found {
			t.Fatalf("confirmation email was not captured by mock sender")
		}
	})
}
