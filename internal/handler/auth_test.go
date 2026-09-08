package handler_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/handler"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/gin-gonic/gin"
)

func setupTestRouter() (*gin.Engine, *config.Config, *repository.UserRepository, *repository.AccountRepository) {
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

	authHandler := handler.NewAuthHandler(cfg, userRepo, accountRepo)
	accountHandler := handler.NewAccountHandler(accountRepo, userRepo)

	r := gin.New()

	r.POST("/auth/sign_up", authHandler.SignUp)
	r.POST("/auth/sign_in", authHandler.SignIn)

	authGroup := r.Group("/")
	authGroup.Use(middleware.AuthMiddleware(cfg.JWTSecret, userRepo))
	{
		authGroup.GET("/api/v1/profile", authHandler.Profile)
		authGroup.POST("/api/v1/profile/availability", authHandler.UpdateAvailability)
		authGroup.POST("/api/v1/accounts", accountHandler.CreateAccount)

		tenantGroup := authGroup.Group("/api/v1/accounts/:account_id")
		tenantGroup.Use(middleware.TenantMiddleware(accountRepo))
		{
			tenantGroup.GET("", accountHandler.GetAccount)
			tenantGroup.PUT("", accountHandler.UpdateAccount)
			tenantGroup.GET("/agents", accountHandler.ListAgents)
			tenantGroup.POST("/agents", accountHandler.AddAgent)
		}
	}

	return r, cfg, userRepo, accountRepo
}

func TestAuth_SignUpAndSignIn(t *testing.T) {
	r, _, _, _ := setupTestRouter()

	// 1. Sign Up
	signUpBody, _ := json.Marshal(map[string]string{
		"name":         "Alice Admin",
		"email":        "alice@example.com",
		"password":     "secret123",
		"account_name": "Acme Support",
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/auth/sign_up", bytes.NewBuffer(signUpBody))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var signUpRes struct {
		Success bool `json:"success"`
		Data    struct {
			Token string `json:"token"`
			User  struct {
				ID    uint   `json:"id"`
				Email string `json:"email"`
			} `json:"user"`
			Accounts []struct {
				ID   uint   `json:"id"`
				Name string `json:"name"`
			} `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &signUpRes)

	if !signUpRes.Success || signUpRes.Data.Token == "" {
		t.Fatalf("expected success with token, got: %s", w.Body.String())
	}
	if len(signUpRes.Data.Accounts) == 0 {
		t.Fatalf("expected accounts created, got 0")
	}

	token := signUpRes.Data.Token
	accountID := signUpRes.Data.Accounts[0].ID

	// 2. Profile with Token
	wProfile := httptest.NewRecorder()
	reqProfile, _ := http.NewRequest("GET", "/api/v1/profile", nil)
	reqProfile.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(wProfile, reqProfile)

	if wProfile.Code != http.StatusOK {
		t.Fatalf("profile expected 200, got %d: %s", wProfile.Code, wProfile.Body.String())
	}

	// 3. Get Account
	wAccount := httptest.NewRecorder()
	reqAccount, _ := http.NewRequest("GET", "/api/v1/accounts/1", nil)
	reqAccount.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(wAccount, reqAccount)

	if wAccount.Code != http.StatusOK {
		t.Fatalf("get account expected 200, got %d: %s", wAccount.Code, wAccount.Body.String())
	}

	// 4. Add Agent
	addAgentBody, _ := json.Marshal(map[string]string{
		"name":  "Bob Agent",
		"email": "bob@example.com",
		"role":  "agent",
	})
	wAgent := httptest.NewRecorder()
	reqAgent, _ := http.NewRequest("POST", "/api/v1/accounts/1/agents", bytes.NewBuffer(addAgentBody))
	reqAgent.Header.Set("Authorization", "Bearer "+token)
	reqAgent.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wAgent, reqAgent)

	if wAgent.Code != http.StatusCreated {
		t.Fatalf("add agent expected 201, got %d: %s", wAgent.Code, wAgent.Body.String())
	}

	_ = accountID
}
