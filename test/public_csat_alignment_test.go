package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestPublicCSATAlignment(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "public-csat-secret-2026",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()

	engine := router.SetupRouter(cfg, db, hub)

	// Admin registration to get account
	signUpBody, _ := json.Marshal(map[string]string{
		"name":         "CSAT测试员",
		"email":        "csat.tester@example.com",
		"password":     "password123456",
		"account_name": "澄川CSAT工作区",
	})
	wSignUp := httptest.NewRecorder()
	reqSignUp, _ := http.NewRequest("POST", "/auth/sign_up", bytes.NewBuffer(signUpBody))
	reqSignUp.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wSignUp, reqSignUp)
	if wSignUp.Code != http.StatusCreated {
		t.Fatalf("sign up failed: %d, body: %s", wSignUp.Code, wSignUp.Body.String())
	}

	var authResp struct {
		Data struct {
			Accounts []struct {
				ID uint `json:"id"`
			} `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wSignUp.Body.Bytes(), &authResp)
	accountID := authResp.Data.Accounts[0].ID

	// Create inbox
	inbox := domain.Inbox{
		AccountID:   accountID,
		Name:        "网站在线客服",
		ChannelType: "Channel::WebWidget",
	}
	if err := db.Create(&inbox).Error; err != nil {
		t.Fatalf("failed to create inbox: %v", err)
	}

	// -------------------------------------------------------------------------
	// Scenario 1: Open conversation without CSAT survey returns 404 on GET
	// -------------------------------------------------------------------------
	t.Run("GET_Open_Conversation_Without_CSAT_Returns_404", func(t *testing.T) {
		openConv := domain.Conversation{
			AccountID: accountID,
			InboxID:   inbox.ID,
			Status:    "open",
			Priority:  "medium",
			DisplayID: 1,
		}
		if err := db.Create(&openConv).Error; err != nil {
			t.Fatalf("failed to create open conversation: %v", err)
		}

		// Query by UUID
		w1 := httptest.NewRecorder()
		req1, _ := http.NewRequest("GET", "/public/api/v1/csat_survey/"+openConv.UUID, nil)
		engine.ServeHTTP(w1, req1)
		if w1.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for conversation without CSAT survey, got %d, body=%s", w1.Code, w1.Body.String())
		}

		// Query by ID without token returns 403 Forbidden
		wUnauth := httptest.NewRecorder()
		reqUnauth, _ := http.NewRequest("GET", fmt.Sprintf("/public/api/v1/csat_survey/%d", openConv.ID), nil)
		engine.ServeHTTP(wUnauth, reqUnauth)
		if wUnauth.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for conversation by numeric ID without token, got %d", wUnauth.Code)
		}

		// Query by ID with valid token returns 404 Not Found (no CSAT on this conversation)
		w2 := httptest.NewRecorder()
		req2, _ := http.NewRequest("GET", fmt.Sprintf("/public/api/v1/csat_survey/%d?token=%s", openConv.ID, openConv.UUID), nil)
		engine.ServeHTTP(w2, req2)
		if w2.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for conversation without CSAT survey by ID with token, got %d", w2.Code)
		}
	})

	// -------------------------------------------------------------------------
	// Scenario 2: Conversation with CSAT survey details reading via GET
	// -------------------------------------------------------------------------
	t.Run("GET_CSAT_Survey_Details_By_UUID_And_ID", func(t *testing.T) {
		conv := domain.Conversation{
			AccountID: accountID,
			InboxID:   inbox.ID,
			Status:    "resolved",
			Priority:  "medium",
			DisplayID: 2,
		}
		if err := db.Create(&conv).Error; err != nil {
			t.Fatalf("failed to create conv: %v", err)
		}

		// Create input_csat message
		csatMsg := domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     "User",
			SenderID:       1,
			ContentType:    "input_csat",
			Content:        "您对本次服务满意吗？",
		}
		if err := db.Create(&csatMsg).Error; err != nil {
			t.Fatalf("failed to create input_csat message: %v", err)
		}

		// Create CSAT survey response record
		survey := domain.CSATSurvey{
			AccountID:      accountID,
			ConversationID: conv.ID,
			Rating:         4,
			FeedbackText:   "客服响应很迅速，解答专业！",
			ReviewStatus:   "pending",
		}
		if err := db.Create(&survey).Error; err != nil {
			t.Fatalf("failed to create survey: %v", err)
		}

		// 1. Query by UUID
		wUUID := httptest.NewRecorder()
		reqUUID, _ := http.NewRequest("GET", "/public/api/v1/csat_survey/"+conv.UUID, nil)
		engine.ServeHTTP(wUUID, reqUUID)
		if wUUID.Code != http.StatusOK {
			t.Fatalf("expected 200 OK by UUID, got %d, body=%s", wUUID.Code, wUUID.Body.String())
		}

		var respUUID struct {
			ConversationID     uint `json:"conversation_id"`
			CSATSurveyResponse struct {
				ConversationID  uint   `json:"conversation_id"`
				Rating          int    `json:"rating"`
				FeedbackMessage string `json:"feedback_message"`
			} `json:"csat_survey_response"`
			InboxName string `json:"inbox_name"`
		}
		_ = json.Unmarshal(wUUID.Body.Bytes(), &respUUID)
		if respUUID.ConversationID != conv.ID {
			t.Fatalf("expected conversation_id %d, got %d", conv.ID, respUUID.ConversationID)
		}
		if respUUID.CSATSurveyResponse.Rating != 4 {
			t.Fatalf("expected rating 4, got %d", respUUID.CSATSurveyResponse.Rating)
		}
		if respUUID.CSATSurveyResponse.FeedbackMessage != "客服响应很迅速，解答专业！" {
			t.Fatalf("expected feedback message, got: %s", respUUID.CSATSurveyResponse.FeedbackMessage)
		}
		if respUUID.InboxName != "网站在线客服" {
			t.Fatalf("expected inbox name, got: %s", respUUID.InboxName)
		}

		// 2. Query by numeric ID without credentials: must return 403 Forbidden!
		wUnauth := httptest.NewRecorder()
		reqUnauth, _ := http.NewRequest("GET", fmt.Sprintf("/public/api/v1/csat_survey/%d", conv.ID), nil)
		engine.ServeHTTP(wUnauth, reqUnauth)
		if wUnauth.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for unauthenticated numeric ID, got %d, body=%s", wUnauth.Code, wUnauth.Body.String())
		}

		// 3. Query by numeric ID with visitor token matching conv.UUID: returns 200 OK!
		wID := httptest.NewRecorder()
		reqID, _ := http.NewRequest("GET", fmt.Sprintf("/public/api/v1/csat_survey/%d?token=%s", conv.ID, conv.UUID), nil)
		engine.ServeHTTP(wID, reqID)
		if wID.Code != http.StatusOK {
			t.Fatalf("expected 200 OK by ID with token, got %d, body=%s", wID.Code, wID.Body.String())
		}
	})

	// -------------------------------------------------------------------------
	// Scenario 3: Update CSAT survey response via PATCH & PUT
	// -------------------------------------------------------------------------
	t.Run("PATCH_And_PUT_CSAT_Survey_Updates_Rating_And_Feedback", func(t *testing.T) {
		conv := domain.Conversation{
			AccountID: accountID,
			InboxID:   inbox.ID,
			Status:    "resolved",
			DisplayID: 3,
		}
		_ = db.Create(&conv)

		csatMsg := domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     "User",
			SenderID:       1,
			ContentType:    "input_csat",
			Content:        "请为本次服务打分",
		}
		_ = db.Create(&csatMsg)

		// 1. Update using Chatwoot standard nested format via PATCH
		patchPayload := map[string]any{
			"message": map[string]any{
				"submitted_values": map[string]any{
					"csat_survey_response": map[string]any{
						"rating":           5,
						"feedback_message": "非常满意的服务体验！",
					},
				},
			},
		}
		bodyPatch, _ := json.Marshal(patchPayload)
		wPatch := httptest.NewRecorder()
		reqPatch, _ := http.NewRequest("PATCH", "/public/api/v1/csat_survey/"+conv.UUID, bytes.NewBuffer(bodyPatch))
		reqPatch.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wPatch, reqPatch)

		if wPatch.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for PATCH csat_survey, got %d, body=%s", wPatch.Code, wPatch.Body.String())
		}

		var patchResp struct {
			ConversationID     uint `json:"conversation_id"`
			CSATSurveyResponse struct {
				Rating          int    `json:"rating"`
				FeedbackMessage string `json:"feedback_message"`
			} `json:"csat_survey_response"`
		}
		_ = json.Unmarshal(wPatch.Body.Bytes(), &patchResp)
		if patchResp.ConversationID != conv.ID || patchResp.CSATSurveyResponse.Rating != 5 {
			t.Fatalf("unexpected patch response: %+v", patchResp)
		}

		// Verify database persistence
		var savedSurvey domain.CSATSurvey
		if err := db.Where("account_id = ? AND conversation_id = ?", accountID, conv.ID).First(&savedSurvey).Error; err != nil {
			t.Fatalf("failed to query saved survey: %v", err)
		}
		if savedSurvey.Rating != 5 || savedSurvey.FeedbackText != "非常满意的服务体验！" {
			t.Fatalf("expected rating 5 and text in DB, got: %+v", savedSurvey)
		}

		// Verify message content_attributes was updated
		var updatedMsg domain.Message
		_ = db.Where("id = ?", csatMsg.ID).First(&updatedMsg)
		if !strings.Contains(updatedMsg.ContentAttributes, "非常满意的服务体验！") {
			t.Fatalf("expected message content_attributes to contain feedback, got: %s", updatedMsg.ContentAttributes)
		}

		// 2. Update via PUT with flat format
		putPayload := map[string]any{
			"rating":           4,
			"feedback_message": "改评为4星，补充说明一下物流稍微有点慢",
		}
		bodyPut, _ := json.Marshal(putPayload)
		wPut := httptest.NewRecorder()
		reqPut, _ := http.NewRequest("PUT", fmt.Sprintf("/public/api/v1/csat_survey/%d?token=%s", conv.ID, conv.UUID), bytes.NewBuffer(bodyPut))
		reqPut.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wPut, reqPut)

		if wPut.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for PUT csat_survey, got %d, body=%s", wPut.Code, wPut.Body.String())
		}

		_ = db.Where("account_id = ? AND conversation_id = ?", accountID, conv.ID).First(&savedSurvey)
		if savedSurvey.Rating != 4 || !strings.Contains(savedSurvey.FeedbackText, "物流稍微有点慢") {
			t.Fatalf("expected rating 4 and updated text, got: %+v", savedSurvey)
		}
	})

	// -------------------------------------------------------------------------
	// Scenario 4: Validation & 14-day lock
	// -------------------------------------------------------------------------
	t.Run("Rating_Validation_And_14_Days_Lock", func(t *testing.T) {
		conv := domain.Conversation{
			AccountID: accountID,
			InboxID:   inbox.ID,
			Status:    "resolved",
			DisplayID: 4,
		}
		_ = db.Create(&conv)

		// 1. Invalid rating (> 5)
		badPayload, _ := json.Marshal(map[string]any{"rating": 6})
		wBad := httptest.NewRecorder()
		reqBad, _ := http.NewRequest("PATCH", fmt.Sprintf("/public/api/v1/csat_survey/%d?token=%s", conv.ID, conv.UUID), bytes.NewBuffer(badPayload))
		reqBad.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wBad, reqBad)
		if wBad.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for rating > 5, got %d", wBad.Code)
		}

		// 2. CSAT created more than 14 days ago cannot be updated (422)
		oldTime := time.Now().Add(-15 * 24 * time.Hour)
		oldSurvey := domain.CSATSurvey{
			AccountID:      accountID,
			ConversationID: conv.ID,
			Rating:         3,
			FeedbackText:   "Initial survey 15 days ago",
			CreatedAt:      oldTime,
		}
		_ = db.Create(&oldSurvey)

		updatePayload, _ := json.Marshal(map[string]any{"rating": 5, "feedback_message": "Trying to update after 15 days"})
		wLock := httptest.NewRecorder()
		reqLock, _ := http.NewRequest("PATCH", fmt.Sprintf("/public/api/v1/csat_survey/%d?token=%s", conv.ID, conv.UUID), bytes.NewBuffer(updatePayload))
		reqLock.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wLock, reqLock)

		if wLock.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 for CSAT locked after 14 days, got %d, body=%s", wLock.Code, wLock.Body.String())
		}
		if !strings.Contains(wLock.Body.String(), "cannot update the CSAT survey after 14 days") {
			t.Fatalf("expected error message for 14-day lock, got: %s", wLock.Body.String())
		}
	})

	// -------------------------------------------------------------------------
	// Scenario 5: POST backward compatibility returns 201 Created
	// -------------------------------------------------------------------------
	t.Run("POST_Backward_Compatibility_Returns_201", func(t *testing.T) {
		conv := domain.Conversation{
			AccountID: accountID,
			InboxID:   inbox.ID,
			Status:    "resolved",
			DisplayID: 5,
		}
		_ = db.Create(&conv)

		postPayload := map[string]any{
			"account_id":    accountID,
			"rating":        5,
			"feedback_text": "Legacy POST format test",
		}
		bodyPost, _ := json.Marshal(postPayload)
		wPost := httptest.NewRecorder()
		reqPost, _ := http.NewRequest("POST", "/public/api/v1/csat_survey/"+conv.UUID, bytes.NewBuffer(bodyPost))
		reqPost.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wPost, reqPost)

		if wPost.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created for POST csat_survey, got %d, body=%s", wPost.Code, wPost.Body.String())
		}
	})
}
