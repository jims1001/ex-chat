package test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestAIMissingKeyDetection(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 1. Direct OpenAI Provider test without API Key
	t.Run("Direct_OpenAI_Provider_Missing_Key", func(t *testing.T) {
		prov := service.NewOpenAIProvider("", "", "gpt-4o")
		resp, err := prov.GenerateCompletion(context.Background(), service.AICompletionRequest{
			Model: "gpt-4o",
			Messages: []service.AIMessage{
				{Role: "user", Content: "你好，请帮我处理退款。"},
			},
		})
		if err == nil {
			t.Fatalf("expected error when API key is missing, got nil (content=%v)", resp)
		}
		if !errors.Is(err, service.ErrAPIKeyMissing) {
			t.Fatalf("expected ErrAPIKeyMissing, got: %v", err)
		}
		if resp != nil {
			t.Fatalf("expected nil response when API key is missing, got: %+v", resp)
		}
	})

	// 2. Direct Gemini Provider test without API Key
	t.Run("Direct_Gemini_Provider_Missing_Key", func(t *testing.T) {
		prov := service.NewGeminiProvider("", "gemini-1.5-pro")
		resp, err := prov.GenerateCompletion(context.Background(), service.AICompletionRequest{
			Model: "gemini-1.5-pro",
			Messages: []service.AIMessage{
				{Role: "user", Content: "你好，请帮我查询订单。"},
			},
		})
		if err == nil {
			t.Fatalf("expected error when Gemini API key is missing, got nil (content=%v)", resp)
		}
		if !errors.Is(err, service.ErrAPIKeyMissing) {
			t.Fatalf("expected ErrAPIKeyMissing, got: %v", err)
		}
		if resp != nil {
			t.Fatalf("expected nil response when Gemini API key is missing, got: %+v", resp)
		}
	})

	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "ai-missing-key-secret-key-2026",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()

	engine := router.SetupRouter(cfg, db, hub)

	// Admin registration
	signUpBody, _ := json.Marshal(map[string]string{
		"name":         "AI检测员",
		"email":        "ai.detection@example.com",
		"password":     "password123456",
		"account_name": "澄川AI测试工作区",
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
			Token    string `json:"token"`
			Accounts []struct {
				ID uint `json:"id"`
			} `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wSignUp.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID
	authHeader := "Bearer " + token
	accStr := fmt.Sprintf("%d", accountID)

	// 3. HTTP /copilot/completions endpoint missing key returns 422
	t.Run("HTTP_Completions_Missing_Key_Returns_422", func(t *testing.T) {
		// OpenAI without key
		openAIPayload, _ := json.Marshal(map[string]any{
			"provider": "openai",
			"model":    "gpt-4o",
			"prompt":   "帮我起草客户道歉信",
		})
		w1 := httptest.NewRecorder()
		req1, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%s/copilot/completions", accStr), bytes.NewBuffer(openAIPayload))
		req1.Header.Set("Authorization", authHeader)
		req1.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(w1, req1)

		if w1.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 UnprocessableEntity for missing OpenAI key, got %d, body=%s", w1.Code, w1.Body.String())
		}
		if !strings.Contains(w1.Body.String(), "API key is missing") && !strings.Contains(w1.Body.String(), "not configured") {
			t.Fatalf("expected error message mentioning missing API key, got: %s", w1.Body.String())
		}

		// Gemini without key
		geminiPayload, _ := json.Marshal(map[string]any{
			"provider": "gemini",
			"model":    "gemini-1.5-pro",
			"prompt":   "总结一下这通电话",
		})
		w2 := httptest.NewRecorder()
		req2, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%s/copilot/completions", accStr), bytes.NewBuffer(geminiPayload))
		req2.Header.Set("Authorization", authHeader)
		req2.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(w2, req2)

		if w2.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 UnprocessableEntity for missing Gemini key, got %d, body=%s", w2.Code, w2.Body.String())
		}

		// Local heuristic works without key
		localPayload, _ := json.Marshal(map[string]any{
			"provider": "local-heuristic",
			"prompt":   "系统状态检查",
		})
		w3 := httptest.NewRecorder()
		req3, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%s/copilot/completions", accStr), bytes.NewBuffer(localPayload))
		req3.Header.Set("Authorization", authHeader)
		req3.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(w3, req3)

		if w3.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for local-heuristic, got %d, body=%s", w3.Code, w3.Body.String())
		}
	})

	// 4. Captain Assistant Playground missing key
	t.Run("Captain_Playground_Missing_Key_Refuses_Fake_Answers", func(t *testing.T) {
		// Create an assistant with model gpt-4o and no API key configured
		asst := domain.CaptainAssistant{
			AccountID:    accountID,
			Name:         "GPT-4o 售前助手",
			Model:        "gpt-4o",
			Status:       "active",
			SystemPrompt: "你是一个专业的客服代表",
		}
		if err := db.Create(&asst).Error; err != nil {
			t.Fatalf("failed to create assistant: %v", err)
		}

		// Create an FAQ for the assistant
		faq := domain.CaptainAssistantResponse{
			AccountID:   accountID,
			AssistantID: asst.ID,
			Question:    "退货退款政策",
			Answer:      "自收货之日起7日内可无理由退换货。",
		}
		if err := db.Create(&faq).Error; err != nil {
			t.Fatalf("failed to create FAQ: %v", err)
		}

		// Init usage quota
		quota := domain.AIUsageQuota{
			AccountID:           accountID,
			MonthlyRequestLimit: 1000,
			MonthlyTokenLimit:   1000000,
			UsedRequests:        0,
			UsedTokens:          0,
			ResetAt:             time.Now().Add(30 * 24 * time.Hour),
		}
		_ = db.Create(&quota)

		// Test A: Prompt does NOT match FAQ -> model lacks API Key -> MUST return 422 and NOT fake success or charge quota
		missPayload, _ := json.Marshal(map[string]any{
			"message_content": "请写一首关于量子物理的诗歌",
		})
		wA := httptest.NewRecorder()
		reqA, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%s/captain/assistants/%d/playground", accStr, asst.ID), bytes.NewBuffer(missPayload))
		reqA.Header.Set("Authorization", authHeader)
		reqA.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wA, reqA)

		if wA.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 for unconfigured assistant model in playground, got %d, body=%s", wA.Code, wA.Body.String())
		}
		if strings.Contains(wA.Body.String(), "我正在为您查询相关解答") {
			t.Fatalf("expected NO fake polite greeting in playground, got fake text in: %s", wA.Body.String())
		}

		// Verify quota was NOT incremented
		var quotaCheck domain.AIUsageQuota
		_ = db.Where("account_id = ?", accountID).First(&quotaCheck)
		if quotaCheck.UsedRequests != 0 {
			t.Fatalf("expected UsedRequests to remain 0 after failed AI invocation, got %d", quotaCheck.UsedRequests)
		}

		// Test B: Prompt DOES match FAQ -> Returns FAQ answer legitimately (knowledge base hit)
		hitPayload, _ := json.Marshal(map[string]any{
			"message_content": "请问你们的退货退款政策是什么？",
		})
		wB := httptest.NewRecorder()
		reqB, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%s/captain/assistants/%d/playground", accStr, asst.ID), bytes.NewBuffer(hitPayload))
		reqB.Header.Set("Authorization", authHeader)
		reqB.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wB, reqB)

		if wB.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for matched FAQ in playground, got %d, body=%s", wB.Code, wB.Body.String())
		}
		if !strings.Contains(wB.Body.String(), "7日内可无理由退换货") {
			t.Fatalf("expected FAQ answer in response, got: %s", wB.Body.String())
		}
	})

	// 5. Summarize and Suggestions endpoints honestly report ai_configured: false
	t.Run("Summarize_And_Suggestions_Report_AI_Configured_Status", func(t *testing.T) {
		// Create inbox and conversation
		inbox := domain.Inbox{AccountID: accountID, Name: "Web Chat", ChannelType: "Channel::WebWidget"}
		_ = db.Create(&inbox)
		conv := domain.Conversation{
			AccountID: accountID,
			InboxID:   inbox.ID,
			Status:    "open",
			Priority:  "medium",
			DisplayID: 101,
		}
		_ = db.Create(&conv)
		msg := domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			Content:        "我的订单还没发货，请尽快处理，谢谢！",
			MessageType:    domain.MessageTypeIncoming,
		}
		_ = db.Create(&msg)

		// Summarize
		wSum := httptest.NewRecorder()
		reqSum, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%s/conversations/%d/copilot/summarize", accStr, conv.ID), nil)
		reqSum.Header.Set("Authorization", authHeader)
		engine.ServeHTTP(wSum, reqSum)

		if wSum.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for summarize, got %d, body=%s", wSum.Code, wSum.Body.String())
		}
		var sumResp struct {
			Data struct {
				Summary      string `json:"summary"`
				Source       string `json:"source"`
				AIConfigured bool   `json:"ai_configured"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wSum.Body.Bytes(), &sumResp)
		if sumResp.Data.AIConfigured {
			t.Fatalf("expected ai_configured to be false, got true")
		}
		if sumResp.Data.Source != "rule-heuristic" {
			t.Fatalf("expected source 'rule-heuristic', got: %s", sumResp.Data.Source)
		}

		// Suggestions
		wSug := httptest.NewRecorder()
		reqSug, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%s/conversations/%d/copilot/suggestions", accStr, conv.ID), nil)
		reqSug.Header.Set("Authorization", authHeader)
		engine.ServeHTTP(wSug, reqSug)

		if wSug.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for suggestions, got %d, body=%s", wSug.Code, wSug.Body.String())
		}
		var sugResp struct {
			Data struct {
				Suggestions []struct {
					Reply  string `json:"reply"`
					Source string `json:"source"`
				} `json:"suggestions"`
				AIConfigured bool `json:"ai_configured"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wSug.Body.Bytes(), &sugResp)
		if sugResp.Data.AIConfigured {
			t.Fatalf("expected ai_configured to be false for suggestions, got true")
		}
		for _, s := range sugResp.Data.Suggestions {
			if s.Source == "Heuristic Copilot" {
				t.Fatalf("source should not claim Heuristic Copilot when AI is unconfigured: %+v", s)
			}
		}
	})
}
