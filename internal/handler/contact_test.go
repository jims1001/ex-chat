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

func TestContact_LifecycleAndMerge(t *testing.T) {
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

	contactHandler := handler.NewContactHandler(contactRepo, inboxRepo)

	r := gin.New()

	// Widget identification
	r.POST("/api/v1/widget/contact", contactHandler.WidgetIdentify)

	// Auth & Tenant group
	authGroup := r.Group("/")
	authGroup.Use(middleware.AuthMiddleware(cfg.JWTSecret, userRepo))
	{
		tenantGroup := authGroup.Group("/api/v1/accounts/:account_id")
		tenantGroup.Use(middleware.TenantMiddleware(accountRepo))
		{
			tenantGroup.GET("/contacts", contactHandler.ListContacts)
			tenantGroup.POST("/contacts", contactHandler.CreateContact)
			tenantGroup.GET("/contacts/:id", contactHandler.GetContact)
			tenantGroup.PUT("/contacts/:id", contactHandler.UpdateContact)
			tenantGroup.POST("/actions/contact_merge", contactHandler.MergeContact)
		}
	}

	// 1. Setup Admin & Account & Inbox
	admin := domain.User{
		Name:         "Admin",
		Email:        "admin@crm.com",
		PasswordHash: "dummy",
		Role:         domain.RoleAdministrator,
		Availability: domain.AvailabilityOnline,
	}
	_ = userRepo.Create(&admin)

	account := domain.Account{Name: "CRM Workspace"}
	_ = accountRepo.Create(&account)
	_ = accountRepo.AddMember(account.ID, admin.ID, domain.RoleAdministrator)

	jwtToken, _ := auth.GenerateToken(&admin, cfg.JWTSecret, 24)

	inbox := domain.Inbox{
		AccountID:    account.ID,
		Name:         "Web Inbox",
		WebsiteToken: "web_token_12345",
	}
	_ = inboxRepo.Create(&inbox)

	// 2. Create Contact 1
	body1, _ := json.Marshal(map[string]string{
		"name":  "John Doe",
		"email": "john@example.com",
		"phone": "+1234567890",
	})
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/api/v1/accounts/1/contacts", bytes.NewBuffer(body1))
	req1.Header.Set("Authorization", "Bearer "+jwtToken)
	req1.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w1, req1)

	if w1.Code != http.StatusCreated {
		t.Fatalf("create contact 1 expected 201, got %d: %s", w1.Code, w1.Body.String())
	}

	// 3. Create Contact 2
	body2, _ := json.Marshal(map[string]string{
		"name":  "John Alternate",
		"email": "john.alt@example.com",
	})
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/api/v1/accounts/1/contacts", bytes.NewBuffer(body2))
	req2.Header.Set("Authorization", "Bearer "+jwtToken)
	req2.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w2, req2)

	if w2.Code != http.StatusCreated {
		t.Fatalf("create contact 2 expected 201, got %d: %s", w2.Code, w2.Body.String())
	}

	// 4. Test Widget Identify (auto creates visitor contact linked to source_id)
	widgetBody, _ := json.Marshal(map[string]string{
		"source_id": "session_uuid_999",
		"name":      "Anonymous Visitor",
	})
	wWidget := httptest.NewRecorder()
	reqWidget, _ := http.NewRequest("POST", "/api/v1/widget/contact?website_token=web_token_12345", bytes.NewBuffer(widgetBody))
	reqWidget.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wWidget, reqWidget)

	if wWidget.Code != http.StatusOK {
		t.Fatalf("widget identify expected 200, got %d: %s", wWidget.Code, wWidget.Body.String())
	}

	// 5. Test Merge Contact 2 into Contact 1
	mergeBody, _ := json.Marshal(map[string]uint{
		"base_contact_id":   1,
		"mergee_contact_id": 2,
	})
	wMerge := httptest.NewRecorder()
	reqMerge, _ := http.NewRequest("POST", "/api/v1/accounts/1/actions/contact_merge", bytes.NewBuffer(mergeBody))
	reqMerge.Header.Set("Authorization", "Bearer "+jwtToken)
	reqMerge.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wMerge, reqMerge)

	if wMerge.Code != http.StatusOK {
		t.Fatalf("merge contacts expected 200, got %d: %s", wMerge.Code, wMerge.Body.String())
	}

	// Verify Contact 2 is gone
	wGet2 := httptest.NewRecorder()
	reqGet2, _ := http.NewRequest("GET", "/api/v1/accounts/1/contacts/2", nil)
	reqGet2.Header.Set("Authorization", "Bearer "+jwtToken)
	r.ServeHTTP(wGet2, reqGet2)

	if wGet2.Code != http.StatusNotFound {
		t.Fatalf("expected contact 2 to be not found (404), got %d", wGet2.Code)
	}
}
