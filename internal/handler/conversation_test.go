package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/handler"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/gin-gonic/gin"
)

func TestConversation_FullLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test-jwt-secret-key-must-be-long-enough",
		JWTExpirationHours: 24,
	}

	db, _ := database.InitDB(cfg)
	userRepo := repository.NewUserRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	inboxRepo := repository.NewInboxRepository(db)
	contactRepo := repository.NewContactRepository(db)
	convRepo := repository.NewConversationRepository(db)
	msgRepo := repository.NewMessageRepository(db)

	convHandler := handler.NewConversationHandler(convRepo, msgRepo, inboxRepo, contactRepo)

	r := gin.New()

	// Public Widget Endpoints
	r.POST("/api/v1/widget/conversations", convHandler.WidgetCreateConversation)
	r.GET("/api/v1/widget/messages", convHandler.WidgetListMessages)
	r.POST("/api/v1/widget/messages", convHandler.WidgetCreateMessage)

	// Authenticated Agent Endpoints
	authGroup := r.Group("/")
	authGroup.Use(middleware.AuthMiddleware(cfg.JWTSecret, userRepo))
	{
		tenantGroup := authGroup.Group("/api/v1/accounts/:account_id")
		tenantGroup.Use(middleware.TenantMiddleware(accountRepo))
		{
			tenantGroup.GET("/conversations", convHandler.ListConversations)
			tenantGroup.GET("/conversations/:id", convHandler.GetConversation)
			tenantGroup.POST("/conversations/:id/toggle_status", convHandler.ToggleStatus)
			tenantGroup.POST("/conversations/:id/assignments", convHandler.Assign)
			tenantGroup.GET("/conversations/:id/messages", convHandler.ListMessages)
			tenantGroup.POST("/conversations/:id/messages", convHandler.CreateMessage)
		}
	}

	// 1. Setup Admin, Account, and Website Inbox
	agent := domain.User{
		Name:         "Support Agent",
		Email:        "agent@chat.com",
		PasswordHash: "dummy",
		Role:         domain.RoleAgent,
		Availability: domain.AvailabilityOnline,
	}
	_ = userRepo.Create(&agent)

	account := domain.Account{Name: "Acme Support"}
	_ = accountRepo.Create(&account)
	_ = accountRepo.AddMember(account.ID, agent.ID, domain.RoleAdministrator)

	token, _ := auth.GenerateToken(&agent, cfg.JWTSecret, 24)

	inbox := domain.Inbox{
		AccountID:    account.ID,
		Name:         "Live Widget",
		WebsiteToken: "tok_widget_abc",
	}
	_ = inboxRepo.Create(&inbox)

	// 2. Visitor creates conversation via Widget
	visitorConvBody, _ := json.Marshal(map[string]string{
		"source_id": "visitor_session_1",
		"message":   "Hello! I need help with billing.",
	})
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/api/v1/widget/conversations?website_token=tok_widget_abc", bytes.NewBuffer(visitorConvBody))
	req1.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w1, req1)

	if w1.Code != http.StatusCreated {
		t.Fatalf("visitor start conv expected 201, got %d: %s", w1.Code, w1.Body.String())
	}

	var convRes struct {
		Success bool `json:"success"`
		Data    struct {
			Conversation struct {
				ID        uint   `json:"id"`
				DisplayID uint   `json:"display_id"`
				Status    string `json:"status"`
			} `json:"conversation"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w1.Body.Bytes(), &convRes)
	convID := convRes.Data.Conversation.ID

	if convID == 0 || convRes.Data.Conversation.Status != "open" {
		t.Fatalf("expected open conversation, got: %+v", convRes.Data)
	}

	// 3. Agent lists conversations
	wList := httptest.NewRecorder()
	reqList, _ := http.NewRequest("GET", "/api/v1/accounts/1/conversations", nil)
	reqList.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(wList, reqList)

	if wList.Code != http.StatusOK {
		t.Fatalf("agent list conversations expected 200, got %d", wList.Code)
	}

	// 4. Agent sends reply message
	agentReplyBody, _ := json.Marshal(map[string]any{
		"content": "Hi there! I would be glad to help with billing.",
		"private": false,
	})
	wReply := httptest.NewRecorder()
	reqReply, _ := http.NewRequest("POST", "/api/v1/accounts/1/conversations/1/messages", bytes.NewBuffer(agentReplyBody))
	reqReply.Header.Set("Authorization", "Bearer "+token)
	reqReply.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wReply, reqReply)

	if wReply.Code != http.StatusCreated {
		t.Fatalf("agent reply expected 201, got %d: %s", wReply.Code, wReply.Body.String())
	}

	// 5. Agent sends PRIVATE note
	privateNoteBody, _ := json.Marshal(map[string]any{
		"content": "Internal note: customer needs refund check",
		"private": true,
	})
	wNote := httptest.NewRecorder()
	reqNote, _ := http.NewRequest("POST", "/api/v1/accounts/1/conversations/1/messages", bytes.NewBuffer(privateNoteBody))
	reqNote.Header.Set("Authorization", "Bearer "+token)
	reqNote.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wNote, reqNote)

	if wNote.Code != http.StatusCreated {
		t.Fatalf("agent private note expected 201, got %d: %s", wNote.Code, wNote.Body.String())
	}

	// 6. Agent views messages (should have 3 messages: visitor msg, agent reply, private note)
	wAgentMsgs := httptest.NewRecorder()
	reqAgentMsgs, _ := http.NewRequest("GET", "/api/v1/accounts/1/conversations/1/messages", nil)
	reqAgentMsgs.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(wAgentMsgs, reqAgentMsgs)

	var agentMsgsRes struct {
		Data struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wAgentMsgs.Body.Bytes(), &agentMsgsRes)
	if agentMsgsRes.Data.Total != 3 {
		t.Fatalf("agent expected 3 messages, got %d", agentMsgsRes.Data.Total)
	}

	// 7. Visitor views messages (should only have 2 messages, private note MUST be hidden)
	wVisMsgs := httptest.NewRecorder()
	reqVisMsgs, _ := http.NewRequest("GET", "/api/v1/widget/messages?website_token=tok_widget_abc&conversation_id=1", nil)
	r.ServeHTTP(wVisMsgs, reqVisMsgs)

	var visMsgsRes struct {
		Data struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wVisMsgs.Body.Bytes(), &visMsgsRes)
	if visMsgsRes.Data.Total != 2 {
		t.Fatalf("visitor expected 2 messages (private note hidden), got %d", visMsgsRes.Data.Total)
	}

	// 8. Agent resolves conversation
	resolveBody, _ := json.Marshal(map[string]string{
		"status": "resolved",
	})
	wResolve := httptest.NewRecorder()
	reqResolve, _ := http.NewRequest("POST", "/api/v1/accounts/1/conversations/1/toggle_status", bytes.NewBuffer(resolveBody))
	reqResolve.Header.Set("Authorization", "Bearer "+token)
	reqResolve.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wResolve, reqResolve)

	if wResolve.Code != http.StatusOK {
		t.Fatalf("resolve conversation expected 200, got %d", wResolve.Code)
	}

	// 9. Visitor sends new message -> Conversation auto-reopens
	visitorMsgBody, _ := json.Marshal(map[string]any{
		"source_id":       "visitor_session_1",
		"conversation_id": 1,
		"content":         "Wait, one more question!",
	})
	wReopen := httptest.NewRecorder()
	reqReopen, _ := http.NewRequest("POST", "/api/v1/widget/messages?website_token=tok_widget_abc", bytes.NewBuffer(visitorMsgBody))
	reqReopen.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wReopen, reqReopen)

	if wReopen.Code != http.StatusCreated {
		t.Fatalf("visitor follow-up message expected 201, got %d", wReopen.Code)
	}

	// Verify conversation status is now open again
	wConvCheck := httptest.NewRecorder()
	reqConvCheck, _ := http.NewRequest("GET", "/api/v1/accounts/1/conversations/1", nil)
	reqConvCheck.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(wConvCheck, reqConvCheck)

	var checkRes struct {
		Data struct {
			Status string `json:"status"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wConvCheck.Body.Bytes(), &checkRes)
	if checkRes.Data.Status != "open" {
		t.Fatalf("expected conversation to auto-reopen, got: %s", checkRes.Data.Status)
	}
}
