package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func TestCaptainFullManagement(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_captain_123456",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()

	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up admin user
	signUpPayload := map[string]any{
		"account_name": "OracleBetX Captain Lab",
		"name":         "Captain Admin",
		"email":        "captain.admin@oraclebetx.com",
		"password":     "SecretPass123!",
	}
	body, _ := json.Marshal(signUpPayload)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("sign_up expected 201, got %d: %s", w.Code, w.Body.String())
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
	accID := authResp.Data.Accounts[0].ID
	accStr := strconv.Itoa(int(accID))

	authHeaders := map[string]string{
		"Authorization": "Bearer " + token,
		"Content-Type":  "application/json",
	}

	doReq := func(method, path string, payload any) *httptest.ResponseRecorder {
		var buf *bytes.Buffer
		if payload != nil {
			if s, ok := payload.(string); ok {
				buf = bytes.NewBufferString(s)
			} else {
				b, _ := json.Marshal(payload)
				buf = bytes.NewBuffer(b)
			}
		} else {
			buf = bytes.NewBuffer(nil)
		}
		recorder := httptest.NewRecorder()
		request, _ := http.NewRequest(method, path, buf)
		for k, v := range authHeaders {
			request.Header.Set(k, v)
		}
		r.ServeHTTP(recorder, request)
		return recorder
	}

	// -------------------------------------------------------------
	// Scenario 1: Captain 助手完整管理 (CRUD & Config & Guidelines)
	// -------------------------------------------------------------
	var createdAssistantID uint
	t.Run("Scenario1_CaptainAssistant_CRUD", func(t *testing.T) {
		createPayload := map[string]any{
			"name":          "电商核心售后助手",
			"description":   "负责7天无理由退换货、物流异常及售后保修咨询",
			"system_prompt": "你是一个严谨且充满亲和力的售后技术支持专家。",
			"model":         "local-heuristic",
			"config": map[string]any{
				"temperature":        0.5,
				"welcome_message":    "您好！我是售后服务助手，请问有什么可以协助您的？",
				"handoff_message":    "该问题需要专属工程师介入，已为您转接人工坐席，请稍候。",
				"resolution_message": "很高兴能为您解答，若无其他疑问本次服务将自动完结，祝您生活愉快！",
				"feature_faq":        true,
			},
			"response_guidelines": []string{
				"保持中文回复，语气温和有礼貌",
				"若涉及退货，必须提示客户保留商品原包装与配件",
			},
			"guardrails": []string{
				"严禁透露公司内部供应链成本信息",
				"严禁承诺超出国家三包范围的额外现金赔偿",
			},
		}

		res := doReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/captain/assistants", createPayload)
		if res.Code != http.StatusCreated {
			t.Fatalf("create assistant failed: code=%d, body=%s", res.Code, res.Body.String())
		}

		var respData struct {
			Data domain.CaptainAssistant `json:"data"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &respData)
		if respData.Data.ID == 0 || respData.Data.Name != "电商核心售后助手" {
			t.Fatalf("unexpected assistant data: %+v", respData.Data)
		}
		createdAssistantID = respData.Data.ID

		// Get Assistant Detail
		res = doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/captain/assistants/%d", accStr, createdAssistantID), nil)
		if res.Code != http.StatusOK {
			t.Fatalf("get assistant failed: code=%d, body=%s", res.Code, res.Body.String())
		}
		var getResp struct {
			Data domain.CaptainAssistant `json:"data"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &getResp)
		if getResp.Data.Name != "电商核心售后助手" || !strings.Contains(getResp.Data.Config, "welcome_message") {
			t.Errorf("assistant detail mismatch: %+v", getResp.Data)
		}

		// Update Assistant
		updatePayload := map[string]any{
			"name":        "电商核心售后助手（旗舰店升级版）",
			"description": "升级版：负责全国联保与急速售后换新",
			"config": map[string]any{
				"temperature":     0.3,
				"welcome_message": "尊敬的尊享客户，售后旗舰助手随时为您服务！",
			},
		}
		res = doReq(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%s/captain/assistants/%d", accStr, createdAssistantID), updatePayload)
		if res.Code != http.StatusOK {
			t.Fatalf("update assistant failed: code=%d, body=%s", res.Code, res.Body.String())
		}
		var updateResp struct {
			Data domain.CaptainAssistant `json:"data"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &updateResp)
		if updateResp.Data.Name != "电商核心售后助手（旗舰店升级版）" {
			t.Errorf("expected updated name, got: %s", updateResp.Data.Name)
		}

		// List Assistants
		res = doReq(http.MethodGet, "/api/v1/accounts/"+accStr+"/captain/assistants", nil)
		if res.Code != http.StatusOK {
			t.Fatalf("list assistants failed: code=%d", res.Code)
		}
		var listResp struct {
			Data []domain.CaptainAssistant `json:"data"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &listResp)
		if len(listResp.Data) == 0 {
			t.Fatalf("expected at least 1 assistant in list")
		}
	})

	// -------------------------------------------------------------
	// Scenario 2: 收件箱 Inbox 绑定与解绑 (Inbox Bindings)
	// -------------------------------------------------------------
	var inbox1, inbox2 domain.Inbox
	t.Run("Scenario2_Inbox_Binding_And_Unbinding", func(t *testing.T) {
		// Create 2 Inboxes in DB
		inbox1 = domain.Inbox{
			AccountID:    accID,
			Name:         "官方商城在线客服",
			ChannelType:  "Channel::WebWidget",
			WebsiteToken: "test_web_tok_1",
		}
		db.Create(&inbox1)

		inbox2 = domain.Inbox{
			AccountID:    accID,
			Name:         "WhatsApp 官方通道",
			ChannelType:  "Channel::Whatsapp",
			WebsiteToken: "test_wa_tok_2",
		}
		db.Create(&inbox2)

		// 1. Bind Inbox 1 to Assistant
		bindPayload1 := map[string]any{
			"inbox_id": inbox1.ID,
		}
		res := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%s/captain/assistants/%d/inboxes", accStr, createdAssistantID), bindPayload1)
		if res.Code != http.StatusCreated && res.Code != http.StatusOK {
			t.Fatalf("bind inbox1 failed: code=%d, body=%s", res.Code, res.Body.String())
		}

		// 2. Duplicate binding should be idempotent
		res = doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%s/captain/assistants/%d/inboxes", accStr, createdAssistantID), bindPayload1)
		if res.Code != http.StatusOK && res.Code != http.StatusCreated {
			t.Fatalf("duplicate bind inbox1 failed: code=%d", res.Code)
		}

		// 3. Bind Inbox 2
		bindPayload2 := map[string]any{
			"inbox": map[string]any{
				"inbox_id": inbox2.ID,
			},
		}
		res = doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%s/captain/assistants/%d/inboxes", accStr, createdAssistantID), bindPayload2)
		if res.Code != http.StatusCreated && res.Code != http.StatusOK {
			t.Fatalf("bind inbox2 failed: code=%d", res.Code)
		}

		// 4. List Bound Inboxes
		res = doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/captain/assistants/%d/inboxes", accStr, createdAssistantID), nil)
		if res.Code != http.StatusOK {
			t.Fatalf("list bound inboxes failed: code=%d", res.Code)
		}
		var boundResp struct {
			Data []domain.Inbox `json:"data"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &boundResp)
		if len(boundResp.Data) != 2 {
			t.Fatalf("expected 2 bound inboxes, got %d", len(boundResp.Data))
		}

		// 5. Unbind Inbox 2
		res = doReq(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%s/captain/assistants/%d/inboxes/%d", accStr, createdAssistantID, inbox2.ID), nil)
		if res.Code != http.StatusOK {
			t.Fatalf("unbind inbox2 failed: code=%d", res.Code)
		}

		// Verify list now has 1
		res = doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/captain/assistants/%d/inboxes", accStr, createdAssistantID), nil)
		_ = json.Unmarshal(res.Body.Bytes(), &boundResp)
		if len(boundResp.Data) != 1 || boundResp.Data[0].ID != inbox1.ID {
			t.Fatalf("expected 1 remaining bound inbox (inbox1), got %d", len(boundResp.Data))
		}
	})

	// -------------------------------------------------------------
	// Scenario 3: FAQ 常见问答管理 (Assistant Responses / FAQs)
	// -------------------------------------------------------------
	var faq1ID, faq2ID uint
	t.Run("Scenario3_FAQ_Management", func(t *testing.T) {
		// 1. Create FAQ 1 via /captain/assistant_responses
		faq1Payload := map[string]any{
			"assistant_id": createdAssistantID,
			"question":     "如何申请退货退款？",
			"answer":       "您可以在下单后7天内登录个人中心，找到对应订单点击‘申请售后’选择‘仅退款’或‘退货退款’。客服将于24小时内核实并提供顺丰上门取件条码，运费由商城全额垫付。",
			"status":       "active",
		}
		res := doReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/captain/assistant_responses", faq1Payload)
		if res.Code != http.StatusCreated {
			t.Fatalf("create FAQ 1 failed: code=%d, body=%s", res.Code, res.Body.String())
		}
		var faq1Resp struct {
			Data domain.CaptainAssistantResponse `json:"data"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &faq1Resp)
		faq1ID = faq1Resp.Data.ID

		// 2. Create FAQ 2 via alias /captain/faqs
		faq2Payload := map[string]any{
			"assistant_id": createdAssistantID,
			"question":     "商品保修期是多久？保修范围有哪些？",
			"answer":       "商城全品类电子硬件享受全国联保1年服务，核心部件享受延保至2年。非人为损坏性能故障免费更换原装配件。",
			"status":       "active",
		}
		res = doReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/captain/faqs", faq2Payload)
		if res.Code != http.StatusCreated {
			t.Fatalf("create FAQ 2 failed: code=%d", res.Code)
		}
		var faq2Resp struct {
			Data domain.CaptainAssistantResponse `json:"data"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &faq2Resp)
		faq2ID = faq2Resp.Data.ID

		// 3. Search and List FAQs
		res = doReq(http.MethodGet, "/api/v1/accounts/"+accStr+"/captain/assistant_responses?search=退货退款", nil)
		if res.Code != http.StatusOK {
			t.Fatalf("search FAQ failed: code=%d", res.Code)
		}
		var searchResp struct {
			Data struct {
				Items []domain.CaptainAssistantResponse `json:"items"`
				Total int64                             `json:"total"`
			} `json:"data"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &searchResp)
		if searchResp.Data.Total != 1 || !strings.Contains(searchResp.Data.Items[0].Question, "退货退款") {
			t.Fatalf("expected 1 search result matching 退货退款, got total=%d", searchResp.Data.Total)
		}

		// 4. Update FAQ 2
		updateFAQPayload := map[string]any{
			"answer": "商城全品类硬件享受全国联保2年金牌保障，核心部件提供3年免费质保！",
			"status": "active",
		}
		res = doReq(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%s/captain/faqs/%d", accStr, faq2ID), updateFAQPayload)
		if res.Code != http.StatusOK {
			t.Fatalf("update FAQ failed: code=%d", res.Code)
		}
		var getUpdatedFAQ struct {
			Data domain.CaptainAssistantResponse `json:"data"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &getUpdatedFAQ)
		if !strings.Contains(getUpdatedFAQ.Data.Answer, "金牌保障") {
			t.Errorf("expected updated answer, got %s", getUpdatedFAQ.Data.Answer)
		}

		// 5. Delete FAQ 2
		res = doReq(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%s/captain/faqs/%d", accStr, faq2ID), nil)
		if res.Code != http.StatusOK {
			t.Fatalf("delete FAQ failed: code=%d", res.Code)
		}

		// Verify 404 after deletion
		res = doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/captain/faqs/%d", accStr, faq2ID), nil)
		if res.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for deleted FAQ, got %d", res.Code)
		}
	})

	// -------------------------------------------------------------
	// Scenario 4: AI Playground 交互演练场 (Playground Simulation & RAG)
	// -------------------------------------------------------------
	t.Run("Scenario4_Playground_Simulation_And_RAG", func(t *testing.T) {
		// Ask question that matches FAQ 1
		playgroundPayload := map[string]any{
			"message_content": "你好，请问我要怎么退货退款呢？",
			"message_history": []map[string]string{
				{"role": "user", "content": "你好！"},
				{"role": "assistant", "content": "您好！请问有什么可以协助您的？"},
			},
		}

		res := doReq(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%s/captain/assistants/%d/playground", accStr, createdAssistantID), playgroundPayload)
		if res.Code != http.StatusOK {
			t.Fatalf("playground simulation failed: code=%d, body=%s", res.Code, res.Body.String())
		}

		var pgResp struct {
			Data struct {
				Role        string `json:"role"`
				Content     string `json:"content"`
				MatchedFAQs []struct {
					ID       uint   `json:"id"`
					Question string `json:"question"`
					Answer   string `json:"answer"`
				} `json:"matched_faqs"`
				Citations []map[string]string `json:"citations"`
				Usage     map[string]int      `json:"usage"`
				Model     string              `json:"model"`
			} `json:"data"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &pgResp)

		if pgResp.Data.Role != "assistant" {
			t.Errorf("expected role 'assistant', got: %s", pgResp.Data.Role)
		}
		if len(pgResp.Data.MatchedFAQs) == 0 {
			t.Errorf("expected playground to match FAQ 1, but matched_faqs is empty")
		} else if pgResp.Data.MatchedFAQs[0].ID != faq1ID {
			t.Errorf("expected matched FAQ ID %d, got %d", faq1ID, pgResp.Data.MatchedFAQs[0].ID)
		}
		if !strings.Contains(pgResp.Data.Content, "退货") && !strings.Contains(pgResp.Data.Content, "申请") {
			t.Errorf("expected answer to contain keywords, got: %s", pgResp.Data.Content)
		}
		if pgResp.Data.Usage["total_tokens"] <= 0 {
			t.Errorf("expected non-zero total_tokens in usage, got: %v", pgResp.Data.Usage)
		}
	})

	// -------------------------------------------------------------
	// Scenario 5: 统计大盘与指标下钻 (Stats, Summary & Drilldown)
	// -------------------------------------------------------------
	t.Run("Scenario5_Stats_Summary_And_Drilldown", func(t *testing.T) {
		// 1. Seed customer contact and conversations for this assistant's inbox
		contact := domain.Contact{
			AccountID: accID,
			Name:      "张晓敏",
			Email:     "xiaomin.zhang@example.com",
		}
		db.Create(&contact)

		// Resolved conversation (handled by assistant)
		convResolved := domain.Conversation{
			AccountID: accID,
			InboxID:   inbox1.ID,
			ContactID: contact.ID,
			Status:    "resolved",
			CreatedAt: time.Now().Add(-2 * time.Hour),
		}
		db.Create(&convResolved)

		// Message by assistant
		msg1 := domain.Message{
			AccountID:      accID,
			ConversationID: convResolved.ID,
			SenderType:     "CaptainAssistant",
			SenderID:       createdAssistantID,
			MessageType:    domain.MessageTypeOutgoing,
			ContentType:    domain.ContentTypeText,
			Content:        "您的问题已为您成功解答，如有疑问欢迎再次联系！",
			CreatedAt:      time.Now().Add(-2 * time.Hour),
		}
		db.Create(&msg1)

		// Handoff conversation (assigned to human agent 99)
		agentID := uint(99)
		convHandoff := domain.Conversation{
			AccountID:  accID,
			InboxID:    inbox1.ID,
			ContactID:  contact.ID,
			AssigneeID: &agentID,
			Status:     "open",
			CreatedAt:  time.Now().Add(-1 * time.Hour),
		}
		db.Create(&convHandoff)

		msg2 := domain.Message{
			AccountID:      accID,
			ConversationID: convHandoff.ID,
			SenderType:     "CaptainAssistant",
			SenderID:       createdAssistantID,
			MessageType:    domain.MessageTypeOutgoing,
			ContentType:    domain.ContentTypeText,
			Content:        "为您转接人工坐席中...",
			CreatedAt:      time.Now().Add(-1 * time.Hour),
		}
		db.Create(&msg2)

		// 2. Query Stats
		res := doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/captain/assistants/%d/stats?range=7", accStr, createdAssistantID), nil)
		if res.Code != http.StatusOK {
			t.Fatalf("get assistant stats failed: code=%d, body=%s", res.Code, res.Body.String())
		}
		var statsResp struct {
			Data struct {
				ConversationsHandled struct {
					Value any `json:"value"`
				} `json:"conversations_handled"`
				AutoResolutionRate struct {
					Value any `json:"value"`
				} `json:"auto_resolution_rate"`
				HandoffRate struct {
					Value any `json:"value"`
				} `json:"handoff_rate"`
				Knowledge map[string]int64 `json:"knowledge"`
			} `json:"data"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &statsResp)

		handledVal := fmt.Sprintf("%v", statsResp.Data.ConversationsHandled.Value)
		if handledVal == "0" {
			t.Errorf("expected conversations_handled > 0, got %s", handledVal)
		}
		if statsResp.Data.Knowledge["faqs_count"] < 1 {
			t.Errorf("expected at least 1 FAQ counted in knowledge stats")
		}

		// 3. Query Summary
		res = doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/captain/assistants/%d/summary?range=7", accStr, createdAssistantID), nil)
		if res.Code != http.StatusOK {
			t.Fatalf("get assistant summary failed: code=%d, body=%s", res.Code, res.Body.String())
		}
		var summaryResp struct {
			Data struct {
				Message string `json:"message"`
			} `json:"data"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &summaryResp)
		if !strings.Contains(summaryResp.Data.Message, "Captain助手") || !strings.Contains(summaryResp.Data.Message, "解决率") {
			t.Errorf("unexpected summary message: %s", summaryResp.Data.Message)
		}

		// 4. Query Drilldown: auto_resolution_rate
		res = doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/captain/assistants/%d/drilldown?metric=auto_resolution_rate&range=7", accStr, createdAssistantID), nil)
		if res.Code != http.StatusOK {
			t.Fatalf("drilldown auto_resolution_rate failed: code=%d, body=%s", res.Code, res.Body.String())
		}
		var drillResp struct {
			Data struct {
				Meta struct {
					Metric     string `json:"metric"`
					TotalCount int64  `json:"total_count"`
				} `json:"meta"`
				Payload []domain.Conversation `json:"payload"`
			} `json:"data"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &drillResp)
		if drillResp.Data.Meta.Metric != "auto_resolution_rate" {
			t.Errorf("expected metric 'auto_resolution_rate', got: %s", drillResp.Data.Meta.Metric)
		}
		if len(drillResp.Data.Payload) == 0 {
			t.Errorf("expected drilldown payload to include resolved conversation")
		} else {
			if drillResp.Data.Payload[0].Status != "resolved" {
				t.Errorf("expected payload status 'resolved', got: %s", drillResp.Data.Payload[0].Status)
			}
		}

		// 5. Query Drilldown: handoff_rate
		res = doReq(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%s/captain/assistants/%d/drilldown?metric=handoff_rate&range=7", accStr, createdAssistantID), nil)
		if res.Code != http.StatusOK {
			t.Fatalf("drilldown handoff_rate failed: code=%d", res.Code)
		}
		_ = json.Unmarshal(res.Body.Bytes(), &drillResp)
		if drillResp.Data.Meta.Metric != "handoff_rate" || len(drillResp.Data.Payload) == 0 {
			t.Errorf("expected drilldown handoff_rate payload to include handed-off conversation")
		}

		// Clean up assistant deletion
		res = doReq(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%s/captain/assistants/%d", accStr, createdAssistantID), nil)
		if res.Code != http.StatusOK {
			t.Fatalf("delete assistant failed: code=%d", res.Code)
		}
	})
}
