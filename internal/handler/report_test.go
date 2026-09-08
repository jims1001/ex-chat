package handler_test

import (
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
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/gin-gonic/gin"
)

func TestReport_SummaryAndAgentMetrics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "report-secret-key-1234567890123456",
		JWTExpirationHours: 24,
	}

	db, _ := database.InitDB(cfg)
	userRepo := repository.NewUserRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	convRepo := repository.NewConversationRepository(db)
	msgRepo := repository.NewMessageRepository(db)
	contactRepo := repository.NewContactRepository(db)

	reportService := service.NewReportService(db)
	reportHandler := handler.NewReportHandler(reportService)

	r := gin.New()
	authGroup := r.Group("/")
	authGroup.Use(middleware.AuthMiddleware(cfg.JWTSecret, userRepo))
	{
		tenantGroup := authGroup.Group("/api/v2/accounts/:account_id")
		tenantGroup.Use(middleware.TenantMiddleware(accountRepo))
		{
			tenantGroup.GET("/reports/summary", reportHandler.GetSummary)
			tenantGroup.GET("/reports/agents", reportHandler.GetAgentMetrics)
		}
	}

	// 1. Setup User and Account
	admin := domain.User{
		Name:         "Admin",
		Email:        "admin@report.com",
		Role:         domain.RoleAdministrator,
		Availability: domain.AvailabilityOnline,
	}
	_ = userRepo.Create(&admin)

	account := domain.Account{Name: "Report Account"}
	_ = accountRepo.Create(&account)
	_ = accountRepo.AddMember(account.ID, admin.ID, domain.RoleAdministrator)

	token, _ := auth.GenerateToken(&admin, cfg.JWTSecret, 24)

	// 2. Setup Contact
	contact := domain.Contact{
		AccountID: account.ID,
		Name:      "Customer 1",
		Email:     "c1@report.com",
	}
	_ = contactRepo.Create(&contact)

	// 3. Setup 2 Conversations: 1 resolved, 1 open
	conv1 := domain.Conversation{
		AccountID:  account.ID,
		InboxID:    1,
		ContactID:  contact.ID,
		AssigneeID: &admin.ID,
		Status:     domain.ConversationStatusResolved,
	}
	_ = convRepo.Create(&conv1)

	conv2 := domain.Conversation{
		AccountID:  account.ID,
		InboxID:    1,
		ContactID:  contact.ID,
		AssigneeID: &admin.ID,
		Status:     domain.ConversationStatusOpen,
	}
	_ = convRepo.Create(&conv2)

	// Setup 2 Messages
	_ = msgRepo.Create(&domain.Message{
		AccountID:      account.ID,
		ConversationID: conv1.ID,
		SenderType:     domain.SenderTypeContact,
		SenderID:       contact.ID,
		Content:        "Msg 1",
	})
	_ = msgRepo.Create(&domain.Message{
		AccountID:      account.ID,
		ConversationID: conv2.ID,
		SenderType:     domain.SenderTypeUser,
		SenderID:       admin.ID,
		Content:        "Msg 2",
	})

	// 4. Test Summary Report
	wSummary := httptest.NewRecorder()
	reqSummary, _ := http.NewRequest("GET", "/api/v2/accounts/1/reports/summary", nil)
	reqSummary.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(wSummary, reqSummary)

	if wSummary.Code != http.StatusOK {
		t.Fatalf("summary expected 200, got %d: %s", wSummary.Code, wSummary.Body.String())
	}

	var sumRes struct {
		Data service.AccountSummaryReport `json:"data"`
	}
	_ = json.Unmarshal(wSummary.Body.Bytes(), &sumRes)

	if sumRes.Data.TotalConversations != 2 ||
		sumRes.Data.OpenConversations != 1 ||
		sumRes.Data.ResolvedConversations != 1 ||
		sumRes.Data.TotalMessages != 2 ||
		sumRes.Data.TotalContacts != 1 {
		t.Fatalf("unexpected summary report: %+v", sumRes.Data)
	}

	// 5. Test Agent Metrics
	wAgents := httptest.NewRecorder()
	reqAgents, _ := http.NewRequest("GET", "/api/v2/accounts/1/reports/agents", nil)
	reqAgents.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(wAgents, reqAgents)

	if wAgents.Code != http.StatusOK {
		t.Fatalf("agent metrics expected 200, got %d", wAgents.Code)
	}

	var agentsRes struct {
		Data []service.AgentMetric `json:"data"`
	}
	_ = json.Unmarshal(wAgents.Body.Bytes(), &agentsRes)

	if len(agentsRes.Data) != 1 ||
		agentsRes.Data[0].AssignedConversations != 2 ||
		agentsRes.Data[0].ResolvedConversations != 1 {
		t.Fatalf("unexpected agent metrics: %+v", agentsRes.Data)
	}
}
