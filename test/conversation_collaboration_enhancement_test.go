package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestConversationCollaborationEnhancement(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_key_1234567890123456",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up admin user
	signUpPayload := map[string]string{
		"name":         "Agent Alice",
		"email":        "alice@example.com",
		"password":     "Secret123!",
		"account_name": "Collaboration Test Corp",
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
		Success bool `json:"success"`
		Data    struct {
			Token    string           `json:"token"`
			User     domain.User      `json:"user"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID
	userID := authResp.Data.User.ID

	// 2. Create web widget inbox
	inboxPayload := map[string]any{
		"name":         "Support Web Widget",
		"channel_type": "Channel::WebWidget",
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
		Data domain.Inbox `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &inboxResp)
	inbox := inboxResp.Data
	websiteToken := inbox.WebsiteToken

	// 3. Identify contact via widget
	contactPayload := map[string]any{
		"website_token": websiteToken,
		"source_id":     "src_bob_12345",
		"identifier":    "cust_12345",
		"name":          "Bob Customer",
		"email":         "bob@customer.com",
	}
	body, _ = json.Marshal(contactPayload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/widget/contact?website_token=%s", websiteToken), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("widget identify failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var contactResp struct {
		Data domain.Contact `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &contactResp)
	contactID := contactResp.Data.ID
	sourceID := "src_bob_12345"

	// 4. Create conversation assigned to Alice
	convPayload := map[string]any{
		"inbox_id":    inbox.ID,
		"contact_id":  contactID,
		"assignee_id": userID,
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
		Data domain.Conversation `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &convResp)
	convID := convResp.Data.ID

	// 5. Customer sends 2 incoming messages
	for i := 1; i <= 2; i++ {
		msgPayload := map[string]any{
			"source_id":       sourceID,
			"conversation_id": convID,
			"content":         fmt.Sprintf("Hello from customer message #%d", i),
		}
		body, _ = json.Marshal(msgPayload)
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/widget/messages?website_token=%s", websiteToken), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create widget message #%d failed: code=%d body=%s", i, w.Code, w.Body.String())
		}
	}

	// Verify unread count is 2 in DB
	var dbConv domain.Conversation
	if err := db.Where("id = ?", convID).First(&dbConv).Error; err != nil {
		t.Fatalf("failed to find conversation: %v", err)
	}
	if dbConv.UnreadCount != 2 {
		t.Fatalf("expected unread_count=2 after 2 incoming messages, got %d", dbConv.UnreadCount)
	}

	// 6. Create unread notification for this conversation and user
	notificationRepo := repository.NewNotificationRepository(db)
	err = notificationRepo.Create(context.Background(), &domain.Notification{
		AccountID:        accountID,
		UserID:           userID,
		NotificationType: "conversation_creation",
		PrimaryActorType: "Conversation",
		PrimaryActorID:   convID,
	})
	if err != nil {
		t.Fatalf("failed to create notification: %v", err)
	}

	// Verify notification has read_at IS NULL
	var unreadNotif domain.Notification
	if err := db.Where("account_id = ? AND user_id = ? AND primary_actor_id = ?", accountID, userID, convID).First(&unreadNotif).Error; err != nil {
		t.Fatalf("failed to find notification: %v", err)
	}
	if unreadNotif.ReadAt != nil {
		t.Fatalf("expected notification read_at to be nil before opening conversation")
	}

	// 7. Agent opens conversation (GET /conversations/:id)
	// Must update agent_last_seen_at, assignee_last_seen_at, clear unread_count to 0, and clear notifications!
	req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d", accountID, convID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("get conversation failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var getConvResp struct {
		Data domain.Conversation `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &getConvResp)
	if getConvResp.Data.UnreadCount != 0 {
		t.Fatalf("expected unread_count=0 after opening conversation, got %d", getConvResp.Data.UnreadCount)
	}
	if getConvResp.Data.AgentLastSeenAt == nil {
		t.Fatalf("expected agent_last_seen_at to be populated")
	}
	if getConvResp.Data.AssigneeLastSeenAt == nil {
		t.Fatalf("expected assignee_last_seen_at to be populated since user is assignee")
	}

	// Verify notification is marked read in DB
	if err := db.Where("account_id = ? AND user_id = ? AND primary_actor_id = ?", accountID, userID, convID).First(&unreadNotif).Error; err != nil {
		t.Fatalf("failed to reload notification: %v", err)
	}
	if unreadNotif.ReadAt == nil {
		t.Fatalf("expected notification read_at to be set after opening conversation")
	}

	// 8. Agent manually marks conversation as unread (POST /conversations/:id/unread)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/unread", accountID, convID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("mark unread failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var markUnreadResp struct {
		Data domain.Conversation `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &markUnreadResp)
	if markUnreadResp.Data.UnreadCount < 1 {
		t.Fatalf("expected unread_count >= 1 after mark unread, got %d", markUnreadResp.Data.UnreadCount)
	}

	// 9. Agent explicitly calls update_last_seen (POST /conversations/:id/update_last_seen)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/update_last_seen", accountID, convID), bytes.NewReader([]byte("{}")))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update last seen failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var updateSeenResp struct {
		Data domain.Conversation `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &updateSeenResp)
	if updateSeenResp.Data.UnreadCount != 0 {
		t.Fatalf("expected unread_count=0 after explicit update_last_seen, got %d", updateSeenResp.Data.UnreadCount)
	}

	// 10. Mute and Unmute conversation
	// 10.1 Mute
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/mute", accountID, convID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("mute conversation failed: code=%d body=%s", w.Code, w.Body.String())
	}
	if err := db.Where("id = ?", convID).First(&dbConv).Error; err != nil || !dbConv.Muted {
		t.Fatalf("expected conversation to be muted in DB, got muted=%v err=%v", dbConv.Muted, err)
	}

	// 10.2 Unmute
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/unmute", accountID, convID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("unmute conversation failed: code=%d body=%s", w.Code, w.Body.String())
	}
	if err := db.Where("id = ?", convID).First(&dbConv).Error; err != nil || dbConv.Muted {
		t.Fatalf("expected conversation to be unmuted in DB, got muted=%v", dbConv.Muted)
	}

	// 11. Typing indicator
	// 11.1 Agent typing status
	typingPayload := map[string]string{
		"typing_status": "on",
	}
	body, _ = json.Marshal(typingPayload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/toggle_typing_status", accountID, convID), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("toggle agent typing status failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// 11.2 Widget visitor typing status
	widgetTypingPayload := map[string]any{
		"website_token":   websiteToken,
		"source_id":       sourceID,
		"conversation_id": convID,
		"typing_status":   "on",
	}
	body, _ = json.Marshal(widgetTypingPayload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/widget/conversations/toggle_typing?website_token=%s", websiteToken), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("toggle widget typing status failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// 12. Send transcript
	// 12.1 Agent send transcript
	transcriptPayload := map[string]string{
		"email": "manager@example.com",
	}
	body, _ = json.Marshal(transcriptPayload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/transcript", accountID, convID), bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("agent send transcript failed: code=%d body=%s", w.Code, w.Body.String())
	}
	var agentTranscriptResp struct {
		Data struct {
			Email         string `json:"email"`
			MessagesCount int    `json:"messages_count"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &agentTranscriptResp)
	if agentTranscriptResp.Data.Email != "manager@example.com" || agentTranscriptResp.Data.MessagesCount < 2 {
		t.Fatalf("unexpected agent transcript response: %+v", agentTranscriptResp)
	}

	// 12.2 Widget visitor send transcript
	widgetTranscriptPayload := map[string]any{
		"website_token":   websiteToken,
		"conversation_id": convID,
		"email":           "customer_copy@example.com",
	}
	body, _ = json.Marshal(widgetTranscriptPayload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/widget/conversations/transcript?website_token=%s", websiteToken), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("widget send transcript failed: code=%d body=%s", w.Code, w.Body.String())
	}

	// 13. Widget & Public last seen update
	// 13.1 Widget update last seen
	widgetSeenPayload := map[string]any{
		"website_token":   websiteToken,
		"source_id":       sourceID,
		"conversation_id": convID,
	}
	body, _ = json.Marshal(widgetSeenPayload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/widget/conversations/update_last_seen?website_token=%s", websiteToken), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("widget update last seen failed: code=%d body=%s", w.Code, w.Body.String())
	}
	if err := db.Where("id = ?", convID).First(&dbConv).Error; err != nil || dbConv.ContactLastSeenAt == nil {
		t.Fatalf("expected contact_last_seen_at to be set after widget update, got nil")
	}

	// 13.2 Public update last seen
	publicSeenURL := fmt.Sprintf("/public/api/v1/inboxes/%s/contacts/%d/conversations/%d/update_last_seen",
		websiteToken, contactID, convID)
	req = httptest.NewRequest(http.MethodPost, publicSeenURL, bytes.NewReader([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("public update last seen failed: code=%d body=%s", w.Code, w.Body.String())
	}

	t.Logf("Successfully verified conversation read/unread, notifications, mute/unmute, typing indicator, and transcripts!")
}
