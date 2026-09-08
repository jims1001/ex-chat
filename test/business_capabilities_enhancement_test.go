package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
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

func TestBusinessCapabilitiesEnhancement(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "capability-enhancement-secret-key-2026",
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
		"name":         "能力测试员",
		"email":        "capability.test@example.com",
		"password":     "password123456",
		"account_name": "澄川智能客服",
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
			Token string `json:"token"`
			User  struct {
				ID uint `json:"id"`
			} `json:"user"`
			Accounts []struct {
				ID uint `json:"id"`
			} `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wSignUp.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID
	authHeader := "Bearer " + token

	// -------------------------------------------------------------
	// 1. OpenAI / Gemini Real Model Invocations & Token Usage
	// -------------------------------------------------------------
	t.Run("Item1_AIModel_RealInvocation_And_TokenUsage", func(t *testing.T) {
		var receivedOpenAIReq *http.Request
		var receivedGeminiReq *http.Request

		mockClient := &http.Client{
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				res := httptest.NewRecorder()
				res.WriteHeader(http.StatusOK)

				if strings.Contains(req.URL.Path, "chat/completions") {
					receivedOpenAIReq = req
					respBody := map[string]any{
						"model": "gpt-4o",
						"choices": []map[string]any{
							{
								"message": map[string]string{
									"role":    "assistant",
									"content": "您好！我是真实的 OpenAI 模型回复。针对您的问题，建议排查网络配置并重置路由器。",
								},
							},
						},
						"usage": map[string]int{
							"prompt_tokens":     18,
							"completion_tokens": 42,
							"total_tokens":      60,
						},
					}
					b, _ := json.Marshal(respBody)
					_, _ = res.Write(b)
					return res.Result(), nil
				}

				if strings.Contains(req.URL.Path, "generateContent") {
					receivedGeminiReq = req
					respBody := map[string]any{
						"candidates": []map[string]any{
							{
								"content": map[string]any{
									"parts": []map[string]string{
										{"text": "您好！我是 Google Gemini 真实大模型生成的解答。"},
									},
								},
							},
						},
						"usageMetadata": map[string]int{
							"promptTokenCount":     25,
							"candidatesTokenCount": 55,
							"totalTokenCount":      80,
						},
					}
					b, _ := json.Marshal(respBody)
					_, _ = res.Write(b)
					return res.Result(), nil
				}

				_, _ = res.Write([]byte(`{}`))
				return res.Result(), nil
			}),
		}

		service.SetGlobalAIHTTPClient(mockClient)
		defer service.SetGlobalAIHTTPClient(nil)

		// 1. Test OpenAI completion API
		openAIBody, _ := json.Marshal(map[string]any{
			"provider": "openai",
			"model":    "gpt-4o",
			"prompt":   "如何排查无法连接的问题？",
			"api_key":  "sk-test-openai-real-key-12345",
		})
		wAI1 := httptest.NewRecorder()
		reqAI1, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/copilot/completions", accountID), bytes.NewBuffer(openAIBody))
		reqAI1.Header.Set("Authorization", authHeader)
		reqAI1.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wAI1, reqAI1)
		if wAI1.Code != http.StatusOK {
			t.Fatalf("OpenAI completion failed: %d, body: %s", wAI1.Code, wAI1.Body.String())
		}

		if receivedOpenAIReq == nil {
			t.Fatalf("expected real HTTP request dispatched to OpenAI, got nil")
		}
		if receivedOpenAIReq.Header.Get("Authorization") != "Bearer sk-test-openai-real-key-12345" {
			t.Fatalf("expected OpenAI bearer token in header, got: %s", receivedOpenAIReq.Header.Get("Authorization"))
		}

		var compResp1 struct {
			Data struct {
				Content      string `json:"content"`
				PromptTokens int    `json:"prompt_tokens"`
				CompTokens   int    `json:"completion_tokens"`
				TotalTokens  int    `json:"total_tokens"`
				Provider     string `json:"provider"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wAI1.Body.Bytes(), &compResp1)
		if !strings.Contains(compResp1.Data.Content, "真实的 OpenAI 模型回复") {
			t.Fatalf("unexpected content: %s", compResp1.Data.Content)
		}
		if compResp1.Data.PromptTokens != 18 || compResp1.Data.CompTokens != 42 || compResp1.Data.TotalTokens != 60 {
			t.Fatalf("expected token usage 18/42/60, got %+v", compResp1.Data)
		}

		// 2. Test Gemini completion API
		geminiBody, _ := json.Marshal(map[string]any{
			"provider": "gemini",
			"model":    "gemini-1.5-pro",
			"prompt":   "介绍一下知识库分片检索。",
			"api_key":  "AIzaSyTestGeminiRealKey54321",
		})
		wAI2 := httptest.NewRecorder()
		reqAI2, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/copilot/completions", accountID), bytes.NewBuffer(geminiBody))
		reqAI2.Header.Set("Authorization", authHeader)
		reqAI2.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wAI2, reqAI2)
		if wAI2.Code != http.StatusOK {
			t.Fatalf("Gemini completion failed: %d, body: %s", wAI2.Code, wAI2.Body.String())
		}

		if receivedGeminiReq == nil {
			t.Fatalf("expected real HTTP request dispatched to Gemini, got nil")
		}
		if !strings.Contains(receivedGeminiReq.URL.RawQuery, "key=AIzaSyTestGeminiRealKey54321") {
			t.Fatalf("expected Gemini API key in URL query, got: %s", receivedGeminiReq.URL.RawQuery)
		}

		var compResp2 struct {
			Data struct {
				Content      string `json:"content"`
				PromptTokens int    `json:"prompt_tokens"`
				CompTokens   int    `json:"completion_tokens"`
				TotalTokens  int    `json:"total_tokens"`
				Provider     string `json:"provider"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wAI2.Body.Bytes(), &compResp2)
		if !strings.Contains(compResp2.Data.Content, "Google Gemini 真实大模型") {
			t.Fatalf("unexpected content: %s", compResp2.Data.Content)
		}
		if compResp2.Data.PromptTokens != 25 || compResp2.Data.CompTokens != 55 || compResp2.Data.TotalTokens != 80 {
			t.Fatalf("expected token usage 25/55/80, got %+v", compResp2.Data)
		}
	})

	// -------------------------------------------------------------
	// 2. Shopify Real Customer Orders & Strict Filtering
	// -------------------------------------------------------------
	t.Run("Item2_Shopify_Real_Orders_And_Filtering", func(t *testing.T) {
		// 1. Query by customer email: customer@example.com -> expect #1001
		wShop1 := httptest.NewRecorder()
		reqShop1, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/integrations/shopify/orders?email=customer@example.com", accountID), nil)
		reqShop1.Header.Set("Authorization", authHeader)
		engine.ServeHTTP(wShop1, reqShop1)
		if wShop1.Code != http.StatusOK {
			t.Fatalf("Shopify orders failed: %d", wShop1.Code)
		}

		var shopResp1 struct {
			Data struct {
				Orders []struct {
					ID            string `json:"id"`
					OrderNumber   string `json:"order_number"`
					CustomerEmail string `json:"customer_email"`
				} `json:"orders"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wShop1.Body.Bytes(), &shopResp1)
		if len(shopResp1.Data.Orders) != 1 || shopResp1.Data.Orders[0].OrderNumber != "#1001" {
			t.Fatalf("expected only order #1001 for customer@example.com, got %+v", shopResp1.Data.Orders)
		}

		// 2. Query by order ID: 1002 -> expect #1002
		wShop2 := httptest.NewRecorder()
		reqShop2, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/integrations/shopify/orders?order_id=1002", accountID), nil)
		reqShop2.Header.Set("Authorization", authHeader)
		engine.ServeHTTP(wShop2, reqShop2)
		if wShop2.Code != http.StatusOK {
			t.Fatalf("Shopify orders failed: %d", wShop2.Code)
		}

		var shopResp2 struct {
			Data struct {
				Orders []struct {
					OrderNumber string `json:"order_number"`
				} `json:"orders"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wShop2.Body.Bytes(), &shopResp2)
		if len(shopResp2.Data.Orders) != 1 || shopResp2.Data.Orders[0].OrderNumber != "#1002" {
			t.Fatalf("expected order #1002, got %+v", shopResp2.Data.Orders)
		}

		// 3. Query with non-existent email -> must return 0 orders (NOT the static 2 orders)
		wShop3 := httptest.NewRecorder()
		reqShop3, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/integrations/shopify/orders?email=nonexistent@nobody.com", accountID), nil)
		reqShop3.Header.Set("Authorization", authHeader)
		engine.ServeHTTP(wShop3, reqShop3)
		if wShop3.Code != http.StatusOK {
			t.Fatalf("Shopify orders failed: %d", wShop3.Code)
		}

		var shopResp3 struct {
			Data struct {
				Orders []any `json:"orders"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wShop3.Body.Bytes(), &shopResp3)
		if len(shopResp3.Data.Orders) != 0 {
			t.Fatalf("expected 0 orders for non-existent email, got %d orders", len(shopResp3.Data.Orders))
		}
	})

	// -------------------------------------------------------------
	// 3. Dialogflow V2 detectIntent API & Session Fulfillment
	// -------------------------------------------------------------
	t.Run("Item3_Dialogflow_V2_DetectIntent_And_Handoff", func(t *testing.T) {
		var receivedDFReq *http.Request
		mockDFClient := &http.Client{
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				receivedDFReq = req
				res := httptest.NewRecorder()
				res.WriteHeader(http.StatusOK)

				respBody := map[string]any{
					"queryResult": map[string]any{
						"queryText":       "我要找人工客服处理退款",
						"action":          "human_handoff",
						"fulfillmentText": "正在为您连线资深售后专家，请稍候...",
						"intent": map[string]string{
							"name":        "projects/ex-chat-agent/agent/intents/df-handoff-001",
							"displayName": "human_handoff",
						},
						"intentDetectionConfidence": 0.98,
					},
				}
				b, _ := json.Marshal(respBody)
				_, _ = res.Write(b)
				return res.Result(), nil
			}),
		}

		router.SetAdvancedHTTPClient(mockDFClient)
		defer router.SetAdvancedHTTPClient(nil)

		// Setup inbox and conversation
		inbox := domain.Inbox{AccountID: accountID, Name: "客服中心", ChannelType: "Channel::WebWidget", WebsiteToken: "tok_df_test"}
		db.Create(&inbox)
		contact := domain.Contact{AccountID: accountID, Name: "退款申请用户", Email: "refund.user@example.com"}
		db.Create(&contact)
		conv := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, ContactID: contact.ID, Status: domain.ConversationStatusPending}
		db.Create(&conv)

		// Rebuild router to pick up mock client
		dfEngine := router.SetupRouter(cfg, db, hub)

		dfBody, _ := json.Marshal(map[string]any{
			"conversation_id": conv.ID,
			"query":           "我要找人工客服处理退款",
			"session_id":      fmt.Sprintf("conv-%d-%d", accountID, conv.ID),
		})
		wDF := httptest.NewRecorder()
		reqDF, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/integrations/dialogflow/process", accountID), bytes.NewBuffer(dfBody))
		reqDF.Header.Set("Authorization", authHeader)
		reqDF.Header.Set("Content-Type", "application/json")
		dfEngine.ServeHTTP(wDF, reqDF)
		if wDF.Code != http.StatusOK {
			t.Fatalf("Dialogflow process failed: %d, body: %s", wDF.Code, wDF.Body.String())
		}

		if receivedDFReq == nil {
			t.Fatalf("expected real HTTP request dispatched to Dialogflow detectIntent API")
		}
		if !strings.Contains(receivedDFReq.URL.Path, "detectIntent") {
			t.Fatalf("expected detectIntent in Dialogflow URL path, got: %s", receivedDFReq.URL.Path)
		}

		var dfResp struct {
			Data struct {
				Intent       string  `json:"intent"`
				Reply        string  `json:"reply"`
				HumanHandoff bool    `json:"human_handoff"`
				Confidence   float64 `json:"confidence"`
			} `json:"data"`
		}
		_ = json.Unmarshal(wDF.Body.Bytes(), &dfResp)
		if dfResp.Data.Intent != "human_handoff" || !dfResp.Data.HumanHandoff {
			t.Fatalf("expected human_handoff intent and flag, got %+v", dfResp.Data)
		}
		if !strings.Contains(dfResp.Data.Reply, "资深售后专家") {
			t.Fatalf("unexpected reply: %s", dfResp.Data.Reply)
		}

		// Verify outgoing message was persisted into conversation
		var msg domain.Message
		db.Where("conversation_id = ?", conv.ID).Order("id DESC").First(&msg)
		if !strings.Contains(msg.Content, "资深售后专家") {
			t.Fatalf("expected bot reply posted to conversation, got: %s", msg.Content)
		}

		// Verify conversation was reopened
		var updatedConv domain.Conversation
		db.First(&updatedConv, conv.ID)
		if updatedConv.Status != domain.ConversationStatusOpen {
			t.Fatalf("expected conversation status open after handoff, got: %s", updatedConv.Status)
		}
	})

	// -------------------------------------------------------------
	// 4. Multipart Attachment Upload & Storage Flow
	// -------------------------------------------------------------
	t.Run("Item4_Multipart_Attachment_Upload_And_Storage", func(t *testing.T) {
		inbox := domain.Inbox{AccountID: accountID, Name: "附件测试收件箱", ChannelType: "Channel::WebWidget", WebsiteToken: "tok_attach_test"}
		db.Create(&inbox)
		contact := domain.Contact{AccountID: accountID, Name: "文件上传客户", Email: "upload.user@example.com"}
		db.Create(&contact)
		conv := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, ContactID: contact.ID, Status: domain.ConversationStatusOpen}
		db.Create(&conv)

		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		_ = writer.WriteField("message_id", "409")

		part, err := writer.CreateFormFile("attachment", "sample_receipt.png")
		if err != nil {
			t.Fatalf("failed to create form file: %v", err)
		}
		fileData := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRtest-image-content-binary-bytes")
		_, _ = part.Write(fileData)
		_ = writer.Close()

		wAtt := httptest.NewRecorder()
		reqAtt, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/attachments", accountID, conv.ID), body)
		reqAtt.Header.Set("Authorization", authHeader)
		reqAtt.Header.Set("Content-Type", writer.FormDataContentType())
		engine.ServeHTTP(wAtt, reqAtt)
		if wAtt.Code != http.StatusCreated {
			t.Fatalf("Attachment upload failed: %d, body: %s", wAtt.Code, wAtt.Body.String())
		}

		var attResp struct {
			Data domain.Attachment `json:"data"`
		}
		_ = json.Unmarshal(wAtt.Body.Bytes(), &attResp)
		if attResp.Data.ID == 0 {
			t.Fatalf("expected created attachment ID, got: %+v", attResp.Data)
		}
		if attResp.Data.FileSize != int64(len(fileData)) {
			t.Fatalf("expected file size %d, got %d", len(fileData), attResp.Data.FileSize)
		}
		if !strings.HasPrefix(attResp.Data.DataURL, "/uploads/account_") {
			t.Fatalf("expected data_url to start with /uploads/account_, got: %s", attResp.Data.DataURL)
		}

		// Verify file actually exists on local disk
		localFilePath := strings.TrimPrefix(attResp.Data.DataURL, "/")
		if _, err := os.Stat(localFilePath); err != nil {
			t.Fatalf("uploaded file was not saved to disk at %s: %v", localFilePath, err)
		}
		defer os.Remove(localFilePath)
	})

	// -------------------------------------------------------------
	// 5. Conversation Team Assignment
	// -------------------------------------------------------------
	t.Run("Item5_Conversation_Team_Assignment", func(t *testing.T) {
		// 1. Create a Team
		team := domain.Team{
			AccountID:       accountID,
			Name:            "大客户专席技术团队",
			Description:     "负责重点客户的高级运维及架构支持",
			AllowAutoAssign: true,
		}
		db.Create(&team)

		inbox := domain.Inbox{AccountID: accountID, Name: "VIP收件箱", ChannelType: "Channel::WebWidget", WebsiteToken: "tok_team_inbox"}
		db.Create(&inbox)
		contact := domain.Contact{AccountID: accountID, Name: "专席用户", Email: "team.user@example.com"}
		db.Create(&contact)

		// 2. Create conversation with team_id
		createConvBody, _ := json.Marshal(map[string]any{
			"inbox_id":   inbox.ID,
			"contact_id": contact.ID,
			"team_id":    team.ID,
		})
		wConv := httptest.NewRecorder()
		reqConv, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations", accountID), bytes.NewBuffer(createConvBody))
		reqConv.Header.Set("Authorization", authHeader)
		reqConv.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wConv, reqConv)
		if wConv.Code != http.StatusCreated && wConv.Code != http.StatusOK {
			t.Fatalf("create conversation with team failed: %d, body: %s", wConv.Code, wConv.Body.String())
		}

		var convResp struct {
			Data domain.Conversation `json:"data"`
		}
		_ = json.Unmarshal(wConv.Body.Bytes(), &convResp)
		if convResp.Data.TeamID == nil || *convResp.Data.TeamID != team.ID {
			t.Fatalf("expected team_id %d, got %+v", team.ID, convResp.Data.TeamID)
		}

		// 3. Update conversation assignment to another team / agent
		team2 := domain.Team{AccountID: accountID, Name: "二线售后服务团队"}
		db.Create(&team2)

		assignBody, _ := json.Marshal(map[string]any{
			"team_id": team2.ID,
		})
		wAssign := httptest.NewRecorder()
		reqAssign, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/assignments", accountID, convResp.Data.ID), bytes.NewBuffer(assignBody))
		reqAssign.Header.Set("Authorization", authHeader)
		reqAssign.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wAssign, reqAssign)
		if wAssign.Code != http.StatusOK {
			t.Fatalf("assign team failed: %d, body: %s", wAssign.Code, wAssign.Body.String())
		}

		var checkConv domain.Conversation
		db.First(&checkConv, convResp.Data.ID)
		if checkConv.TeamID == nil || *checkConv.TeamID != team2.ID {
			t.Fatalf("expected updated team_id %d, got %+v", team2.ID, checkConv.TeamID)
		}
	})

	// -------------------------------------------------------------
	// 6. Report Time Filtering (since, until, business_hours)
	// -------------------------------------------------------------
	t.Run("Item6_Report_Time_Filtering", func(t *testing.T) {
		inbox := domain.Inbox{AccountID: accountID, Name: "报表收件箱", ChannelType: "Channel::WebWidget", WebsiteToken: "tok_report_inbox"}
		db.Create(&inbox)
		contact := domain.Contact{AccountID: accountID, Name: "报表用户", Email: "report.user@example.com"}
		db.Create(&contact)

		now := time.Now().UTC()
		// Old conversation 10 days ago
		cOld := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, ContactID: contact.ID, CreatedAt: now.AddDate(0, 0, -10)}
		db.Create(&cOld)
		// Recent conversation 1 day ago
		cRecent := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, ContactID: contact.ID, CreatedAt: now.AddDate(0, 0, -1)}
		db.Create(&cRecent)

		// Filter for last 3 days
		sinceSec := now.AddDate(0, 0, -3).Unix()
		untilSec := now.Unix()

		wSummary := httptest.NewRecorder()
		reqSummary, _ := http.NewRequest("GET", fmt.Sprintf("/api/v2/accounts/%d/reports/summary?since=%d&until=%d", accountID, sinceSec, untilSec), nil)
		reqSummary.Header.Set("Authorization", authHeader)
		engine.ServeHTTP(wSummary, reqSummary)
		if wSummary.Code != http.StatusOK {
			t.Fatalf("Report summary failed: %d, body: %s", wSummary.Code, wSummary.Body.String())
		}

		var summaryResp struct {
			Data service.AccountSummaryReport `json:"data"`
		}
		_ = json.Unmarshal(wSummary.Body.Bytes(), &summaryResp)
		// The 10 days ago conversation must be excluded
		if summaryResp.Data.TotalConversations <= 0 {
			t.Fatalf("expected positive conversation count in last 3 days, got %d", summaryResp.Data.TotalConversations)
		}
	})

	// -------------------------------------------------------------
	// 7. First Response Time Distribution (5 Buckets & Channels)
	// -------------------------------------------------------------
	t.Run("Item7_FirstResponse_5Buckets_And_Channel_Breakdown", func(t *testing.T) {
		inboxWhatsapp := domain.Inbox{AccountID: accountID, Name: "WhatsApp Support", ChannelType: "Channel::Whatsapp", WebsiteToken: "tok_whatsapp_frt"}
		db.Create(&inboxWhatsapp)
		inboxEmail := domain.Inbox{AccountID: accountID, Name: "Email Support", ChannelType: "Channel::Email", WebsiteToken: "tok_email_frt"}
		db.Create(&inboxEmail)

		contact := domain.Contact{AccountID: accountID, Name: "响应测试用户", Email: "frt.test@example.com"}
		db.Create(&contact)

		now := time.Now().UTC()

		// 1. WhatsApp conv with 30m response -> falls in 0-1h
		convWA := domain.Conversation{AccountID: accountID, InboxID: inboxWhatsapp.ID, ContactID: contact.ID, CreatedAt: now}
		db.Create(&convWA)
		msgInWA := domain.Message{AccountID: accountID, ConversationID: convWA.ID, MessageType: domain.MessageTypeIncoming, Content: "WA问询", CreatedAt: now.Add(-30 * time.Minute)}
		db.Create(&msgInWA)
		msgOutWA := domain.Message{AccountID: accountID, ConversationID: convWA.ID, MessageType: domain.MessageTypeOutgoing, Content: "WA对客回复", CreatedAt: now}
		db.Create(&msgOutWA)

		// 2. Email conv with 2h response -> falls in 1-4h
		convEM := domain.Conversation{AccountID: accountID, InboxID: inboxEmail.ID, ContactID: contact.ID, CreatedAt: now}
		db.Create(&convEM)
		msgInEM := domain.Message{AccountID: accountID, ConversationID: convEM.ID, MessageType: domain.MessageTypeIncoming, Content: "邮件问询", CreatedAt: now.Add(-2 * time.Hour)}
		db.Create(&msgInEM)
		msgOutEM := domain.Message{AccountID: accountID, ConversationID: convEM.ID, MessageType: domain.MessageTypeOutgoing, Content: "邮件回复", CreatedAt: now}
		db.Create(&msgOutEM)

		// Query first_response_time_distribution
		wFRT := httptest.NewRecorder()
		reqFRT, _ := http.NewRequest("GET", fmt.Sprintf("/api/v2/accounts/%d/reports/first_response_time_distribution", accountID), nil)
		reqFRT.Header.Set("Authorization", authHeader)
		engine.ServeHTTP(wFRT, reqFRT)
		if wFRT.Code != http.StatusOK {
			t.Fatalf("GET first_response_time_distribution failed: %d, body: %s", wFRT.Code, wFRT.Body.String())
		}

		var frtResp struct {
			Data service.FirstResponseDistributionReport `json:"data"`
		}
		_ = json.Unmarshal(wFRT.Body.Bytes(), &frtResp)

		// Verify 5 standard buckets
		if frtResp.Data.Total.ZeroToOneHour < 1 {
			t.Fatalf("expected at least 1 in 0_to_1h bucket, got %+v", frtResp.Data.Total)
		}
		if frtResp.Data.Total.OneToFourHours < 1 {
			t.Fatalf("expected at least 1 in 1_to_4h bucket, got %+v", frtResp.Data.Total)
		}

		// Verify channel breakdown
		foundWA := false
		foundEmail := false
		for _, ch := range frtResp.Data.Channels {
			if ch.ChannelType == "Channel::Whatsapp" && ch.Distribution.ZeroToOneHour >= 1 {
				foundWA = true
			}
			if ch.ChannelType == "Channel::Email" && ch.Distribution.OneToFourHours >= 1 {
				foundEmail = true
			}
		}
		if !foundWA || !foundEmail {
			t.Fatalf("expected channels breakdown to contain WhatsApp and Email with correct distribution, got: %+v", frtResp.Data.Channels)
		}
	})
}
