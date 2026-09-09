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

func TestMessageLifecycleManagement(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_message_lifecycle_123456",
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
		"name":         "Agent Bob",
		"email":        "bob@example.com",
		"password":     "Secret123!",
		"account_name": "Message Test Corp",
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
	json.Unmarshal(w.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID

	// 2. Create an Inbox
	inboxPayload := map[string]interface{}{
		"name":         "Support Web",
		"channel_type": "Channel::WebWidget",
	}
	body, _ = json.Marshal(inboxPayload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes", accountID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create inbox failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var inboxResp struct {
		Data domain.Inbox `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &inboxResp)
	inboxID := inboxResp.Data.ID

	// 3. Create a Contact
	contactPayload := map[string]interface{}{
		"name":  "Alice Customer",
		"email": "alice.cust@example.com",
	}
	body, _ = json.Marshal(contactPayload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/contacts", accountID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create contact failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var contactResp struct {
		Data domain.Contact `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &contactResp)
	contactID := contactResp.Data.ID

	// 4. Create a Conversation
	convPayload := map[string]interface{}{
		"inbox_id":   inboxID,
		"contact_id": contactID,
		"status":     "open",
	}
	body, _ = json.Marshal(convPayload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations", accountID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create conversation failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var convResp struct {
		Data domain.Conversation `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &convResp)
	convID := convResp.Data.ID

	// 5. Send initial message
	msgPayload := map[string]interface{}{
		"content": "Original message content for testing",
		"private": false,
	}
	body, _ = json.Marshal(msgPayload)
	req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages", accountID, convID), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create message failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var msgResp struct {
		Data domain.Message `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &msgResp)
	msg1ID := msgResp.Data.ID

	if msgResp.Data.Content != "Original message content for testing" {
		t.Fatalf("unexpected message content: %s", msgResp.Data.Content)
	}
	if msgResp.Data.Deleted {
		t.Fatalf("new message should not be marked deleted")
	}
	if msgResp.Data.EditedAt != nil {
		t.Fatalf("new message edited_at should be nil")
	}

	// 6. Test Message Editing (PUT /conversations/:id/messages/:message_id)
	t.Run("Edit Message - Empty Content Validation", func(t *testing.T) {
		editPayload := map[string]interface{}{"content": "   "}
		body, _ := json.Marshal(editPayload)
		req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d", accountID, convID, msg1ID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for empty content, got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("Edit Message - Non-existent Message", func(t *testing.T) {
		editPayload := map[string]interface{}{"content": "Valid update"}
		body, _ := json.Marshal(editPayload)
		req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/999999", accountID, convID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for non-existent message, got %d body=%s", w.Code, w.Body.String())
		}
	})

	t.Run("Edit Message - Success", func(t *testing.T) {
		editPayload := map[string]interface{}{"content": "Updated message content successfully!"}
		body, _ := json.Marshal(editPayload)
		req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d", accountID, convID, msg1ID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for message update, got %d body=%s", w.Code, w.Body.String())
		}

		var updatedResp struct {
			Data domain.Message `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &updatedResp)
		if updatedResp.Data.Content != "Updated message content successfully!" {
			t.Fatalf("message content not updated: %s", updatedResp.Data.Content)
		}
		if updatedResp.Data.EditedAt == nil {
			t.Fatalf("edited_at should be set after edit")
		}

		// Verify in DB directly
		var dbMsg domain.Message
		db.First(&dbMsg, msg1ID)
		if dbMsg.Content != "Updated message content successfully!" || dbMsg.EditedAt == nil {
			t.Fatalf("DB message not updated properly: content=%s, edited_at=%v", dbMsg.Content, dbMsg.EditedAt)
		}
	})

	// 7. Test Message Soft-Delete (DELETE /conversations/:id/messages/:message_id)
	t.Run("Delete Message - Non-existent Message", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/999999", accountID, convID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for deleting non-existent message, got %d", w.Code)
		}
	})

	t.Run("Delete Message - Success and Retraction", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d", accountID, convID, msg1ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 for deleting message, got %d body=%s", w.Code, w.Body.String())
		}

		var delResp struct {
			Success bool           `json:"success"`
			Data    domain.Message `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &delResp)
		if !delResp.Data.Deleted {
			t.Fatalf("expected deleted=true in response")
		}
		if delResp.Data.DeletedAt == nil {
			t.Fatalf("expected deleted_at to be populated")
		}

		// Verify in DB
		var dbMsg domain.Message
		db.First(&dbMsg, msg1ID)
		if !dbMsg.Deleted || dbMsg.DeletedAt == nil {
			t.Fatalf("DB message not marked deleted: deleted=%v, deleted_at=%v", dbMsg.Deleted, dbMsg.DeletedAt)
		}
		if dbMsg.Content != "[此消息已被撤回/删除]" {
			t.Fatalf("DB message content should be masked to retracted notice, got: %s", dbMsg.Content)
		}
	})

	t.Run("Delete Message - Cannot Delete Twice", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d", accountID, convID, msg1ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 when deleting already deleted message, got %d", w.Code)
		}
	})

	t.Run("Edit Message - Rejected on Deleted Message", func(t *testing.T) {
		editPayload := map[string]interface{}{"content": "Trying to edit deleted message"}
		body, _ := json.Marshal(editPayload)
		req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d", accountID, convID, msg1ID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
			t.Fatalf("expected 400 or 404 when editing deleted message, got %d body=%s", w.Code, w.Body.String())
		}
	})

	// 8. Test Message Retry (POST /conversations/:id/messages/:message_id/retry)
	t.Run("Retry Message - Rejected on Non-failed Message", func(t *testing.T) {
		// Create fresh sent message
		msgPayload := map[string]interface{}{"content": "Message 2 for retry test"}
		body, _ := json.Marshal(msgPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages", accountID, convID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create message 2 failed: %d", w.Code)
		}

		var msg2Resp struct {
			Data domain.Message `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &msg2Resp)
		msg2ID := msg2Resp.Data.ID

		// Attempt retry when status == "sent"
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d/retry", accountID, convID, msg2ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 when retrying non-failed message, got %d body=%s", w.Code, w.Body.String())
		}

		// Simulate message delivery failure in DB
		db.Model(&domain.Message{}).Where("id = ?", msg2ID).Update("status", "failed")

		// Now retry should succeed
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d/retry", accountID, convID, msg2ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 when retrying failed message, got %d body=%s", w.Code, w.Body.String())
		}

		var retriedResp struct {
			Data domain.Message `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &retriedResp)
		if retriedResp.Data.Status != "sent" {
			t.Fatalf("expected status to be reset to 'sent', got: %s", retriedResp.Data.Status)
		}

		// Verify in DB directly
		var dbMsg2 domain.Message
		db.First(&dbMsg2, msg2ID)
		if dbMsg2.Status != "sent" {
			t.Fatalf("DB message status not 'sent': %s", dbMsg2.Status)
		}

		// Subsequent retry should fail since status is now 'sent'
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d/retry", accountID, convID, msg2ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 when retrying already sent message, got %d", w.Code)
		}
	})

	t.Run("Retry Message - Non-existent Message", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/999999/retry", accountID, convID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for retrying non-existent message, got %d", w.Code)
		}
	})
}
