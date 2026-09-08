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

func TestOps_CannedResponsesAndLabels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "ops-secret-key-123456789012345678",
		JWTExpirationHours: 24,
	}

	db, _ := database.InitDB(cfg)
	userRepo := repository.NewUserRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	labelRepo := repository.NewLabelRepository(db)
	cannedRepo := repository.NewCannedResponseRepository(db)
	convRepo := repository.NewConversationRepository(db)

	opsHandler := handler.NewOpsHandler(labelRepo, cannedRepo, convRepo)

	r := gin.New()
	authGroup := r.Group("/")
	authGroup.Use(middleware.AuthMiddleware(cfg.JWTSecret, userRepo))
	{
		tenantGroup := authGroup.Group("/api/v1/accounts/:account_id")
		tenantGroup.Use(middleware.TenantMiddleware(accountRepo))
		{
			tenantGroup.GET("/canned_responses", opsHandler.ListCannedResponses)
			tenantGroup.POST("/canned_responses", opsHandler.CreateCannedResponse)
			tenantGroup.GET("/labels", opsHandler.ListLabels)
			tenantGroup.POST("/labels", opsHandler.CreateLabel)
			tenantGroup.POST("/conversations/:id/labels", opsHandler.AttachConversationLabels)
			tenantGroup.GET("/conversations/:id/labels", opsHandler.GetConversationLabels)
		}
	}

	// 1. Setup User and Account
	admin := domain.User{
		Name:         "Admin",
		Email:        "ops@test.com",
		Role:         domain.RoleAdministrator,
		Availability: domain.AvailabilityOnline,
	}
	_ = userRepo.Create(&admin)

	account := domain.Account{Name: "Ops Account"}
	_ = accountRepo.Create(&account)
	_ = accountRepo.AddMember(account.ID, admin.ID, domain.RoleAdministrator)

	token, _ := auth.GenerateToken(&admin, cfg.JWTSecret, 24)

	// 2. Create Canned Response
	cannedBody, _ := json.Marshal(map[string]string{
		"short_code": "!hello",
		"content":    "Hi there! Welcome to support, how may I help?",
	})
	wCanned := httptest.NewRecorder()
	reqCanned, _ := http.NewRequest("POST", "/api/v1/accounts/1/canned_responses", bytes.NewBuffer(cannedBody))
	reqCanned.Header.Set("Authorization", "Bearer "+token)
	reqCanned.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wCanned, reqCanned)

	if wCanned.Code != http.StatusCreated {
		t.Fatalf("create canned response expected 201, got %d", wCanned.Code)
	}

	// 3. Search Canned Response
	wSearch := httptest.NewRecorder()
	reqSearch, _ := http.NewRequest("GET", "/api/v1/accounts/1/canned_responses?q=hello", nil)
	reqSearch.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(wSearch, reqSearch)

	if wSearch.Code != http.StatusOK {
		t.Fatalf("search canned response expected 200, got %d", wSearch.Code)
	}

	var searchRes struct {
		Data []domain.CannedResponse `json:"data"`
	}
	_ = json.Unmarshal(wSearch.Body.Bytes(), &searchRes)
	if len(searchRes.Data) != 1 || searchRes.Data[0].ShortCode != "!hello" {
		t.Fatalf("expected 1 result with short code !hello, got %+v", searchRes.Data)
	}

	// 4. Create Label
	labelBody, _ := json.Marshal(map[string]string{
		"title": "billing-issue",
		"color": "#ff0000",
	})
	wLabel := httptest.NewRecorder()
	reqLabel, _ := http.NewRequest("POST", "/api/v1/accounts/1/labels", bytes.NewBuffer(labelBody))
	reqLabel.Header.Set("Authorization", "Bearer "+token)
	reqLabel.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wLabel, reqLabel)

	if wLabel.Code != http.StatusCreated {
		t.Fatalf("create label expected 201, got %d", wLabel.Code)
	}

	var labelRes struct {
		Data domain.Label `json:"data"`
	}
	_ = json.Unmarshal(wLabel.Body.Bytes(), &labelRes)
	labelID := labelRes.Data.ID

	// 5. Create conversation and attach label
	conv := domain.Conversation{
		AccountID: account.ID,
		InboxID:   1,
		ContactID: 1,
		Status:    domain.ConversationStatusOpen,
	}
	_ = convRepo.Create(&conv)

	attachBody, _ := json.Marshal(map[string]any{
		"label_ids": []uint{labelID},
	})
	wAttach := httptest.NewRecorder()
	reqAttach, _ := http.NewRequest("POST", "/api/v1/accounts/1/conversations/1/labels", bytes.NewBuffer(attachBody))
	reqAttach.Header.Set("Authorization", "Bearer "+token)
	reqAttach.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wAttach, reqAttach)

	if wAttach.Code != http.StatusOK {
		t.Fatalf("attach label expected 200, got %d", wAttach.Code)
	}

	// 6. Verify conversation has label
	wGetLabels := httptest.NewRecorder()
	reqGetLabels, _ := http.NewRequest("GET", "/api/v1/accounts/1/conversations/1/labels", nil)
	reqGetLabels.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(wGetLabels, reqGetLabels)

	var getLabelsRes struct {
		Data []domain.Label `json:"data"`
	}
	_ = json.Unmarshal(wGetLabels.Body.Bytes(), &getLabelsRes)
	if len(getLabelsRes.Data) != 1 || getLabelsRes.Data[0].Title != "billing-issue" {
		t.Fatalf("expected 1 label 'billing-issue', got: %+v", getLabelsRes.Data)
	}
}
