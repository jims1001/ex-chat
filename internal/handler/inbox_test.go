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

func TestInbox_LifecycleAndWidgetConfig(t *testing.T) {
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

	inboxHandler := handler.NewInboxHandler(inboxRepo, userRepo)

	r := gin.New()

	// Public widget config
	r.GET("/api/v1/widget/config", inboxHandler.WidgetConfig)

	// Auth & Tenant group
	authGroup := r.Group("/")
	authGroup.Use(middleware.AuthMiddleware(cfg.JWTSecret, userRepo))
	{
		tenantGroup := authGroup.Group("/api/v1/accounts/:account_id")
		tenantGroup.Use(middleware.TenantMiddleware(accountRepo))
		{
			tenantGroup.GET("/inboxes", inboxHandler.ListInboxes)
			tenantGroup.POST("/inboxes", inboxHandler.CreateInbox)
			tenantGroup.GET("/inboxes/:id", inboxHandler.GetInbox)
			tenantGroup.POST("/inboxes/:id/members", inboxHandler.AddInboxMembers)
		}
	}

	// 1. Create User and Account
	admin := domain.User{
		Name:         "Admin",
		Email:        "admin@example.com",
		PasswordHash: "dummy",
		Role:         domain.RoleAdministrator,
		Availability: domain.AvailabilityOnline,
	}
	_ = userRepo.Create(&admin)

	account := domain.Account{Name: "Workspace 1"}
	_ = accountRepo.Create(&account)
	_ = accountRepo.AddMember(account.ID, admin.ID, domain.RoleAdministrator)

	jwtToken, _ := auth.GenerateToken(&admin, cfg.JWTSecret, 24)

	// 2. Create Inbox
	createBody, _ := json.Marshal(map[string]any{
		"name":             "Website Support",
		"channel_type":     "Channel::WebWidget",
		"greeting_message": "Hello! How can we assist you?",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/v1/accounts/1/inboxes", bytes.NewBuffer(createBody))
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var inboxRes struct {
		Success bool `json:"success"`
		Data    struct {
			ID           uint   `json:"id"`
			Name         string `json:"name"`
			WebsiteToken string `json:"website_token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &inboxRes)

	if inboxRes.Data.WebsiteToken == "" {
		t.Fatalf("expected non-empty website_token")
	}

	websiteToken := inboxRes.Data.WebsiteToken
	inboxID := inboxRes.Data.ID

	// 3. Add Admin to Inbox
	addMemberBody, _ := json.Marshal(map[string]any{
		"user_ids": []uint{admin.ID},
	})
	wMember := httptest.NewRecorder()
	reqMember, _ := http.NewRequest("POST", "/api/v1/accounts/1/inboxes/1/members", bytes.NewBuffer(addMemberBody))
	reqMember.Header.Set("Authorization", "Bearer "+jwtToken)
	reqMember.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wMember, reqMember)

	if wMember.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", wMember.Code, wMember.Body.String())
	}

	// 4. Test Public Widget Config
	wWidget := httptest.NewRecorder()
	reqWidget, _ := http.NewRequest("GET", "/api/v1/widget/config?website_token="+websiteToken, nil)
	r.ServeHTTP(wWidget, reqWidget)

	if wWidget.Code != http.StatusOK {
		t.Fatalf("widget config expected 200, got %d: %s", wWidget.Code, wWidget.Body.String())
	}

	var widgetRes struct {
		Success bool `json:"success"`
		Data    struct {
			InboxID      uint   `json:"inbox_id"`
			Name         string `json:"name"`
			WebsiteToken string `json:"website_token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wWidget.Body.Bytes(), &widgetRes)

	if widgetRes.Data.InboxID != inboxID || widgetRes.Data.Name != "Website Support" {
		t.Fatalf("unexpected widget data: %+v", widgetRes.Data)
	}
}
