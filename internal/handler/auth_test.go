package handler_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/handler"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/gin-gonic/gin"
)

func setupTestRouter(t *testing.T) (*gin.Engine, *config.Config, *repository.UserRepository, *repository.AccountRepository) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("AGENT_INVITATIONS_ENABLED", "true")
	t.Setenv("FRONTEND_URL", "https://app.example.test")
	t.Setenv("MAILER_SENDER_EMAIL", "support@example.test")
	cfg := &config.Config{
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test-jwt-secret-key-must-be-long-enough",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("init test database: %v", err)
	}
	userRepo := repository.NewUserRepository(db)
	accountRepo := repository.NewAccountRepository(db)

	authHandler := handler.NewAuthHandler(cfg, userRepo, accountRepo)
	accountHandler := handler.NewAccountHandler(accountRepo, userRepo)
	emails := service.NewEmailService(db)
	emails.SetSender(service.NewMockEmailSender())
	invitationHandler := handler.NewAgentInvitationHandler(accountRepo, userRepo, emails)

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
			tenantGroup.POST("/agents", invitationHandler.AddAgent)
		}
	}

	return r, cfg, userRepo, accountRepo
}

func TestAuth_SignUpAndSignIn(t *testing.T) {
	r, _, users, accounts := setupTestRouter(t)

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
	reqAccount, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d", accountID), nil)
	reqAccount.Header.Set("Authorization", "Bearer "+token)
	r.ServeHTTP(wAccount, reqAccount)

	if wAccount.Code != http.StatusOK {
		t.Fatalf("get account expected 200, got %d: %s", wAccount.Code, wAccount.Body.String())
	}

	// 4. Invite an agent; recipient identity and membership wait for proof.
	addAgentBody, _ := json.Marshal(map[string]string{
		"name":  "Bob Agent",
		"email": "bob@example.com",
		"role":  "agent",
	})
	wAgent := httptest.NewRecorder()
	reqAgent, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/agents", accountID), bytes.NewBuffer(addAgentBody))
	reqAgent.Header.Set("Authorization", "Bearer "+token)
	reqAgent.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(wAgent, reqAgent)

	if wAgent.Code != http.StatusAccepted {
		t.Fatalf("invite agent expected 202, got %d: %s", wAgent.Code, wAgent.Body.String())
	}
	invited, err := users.FindByEmail("bob@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if invited != nil {
		t.Fatal("invitation created a user before identity proof")
	}
	members, err := accounts.ListMembers(accountID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].UserID != signUpRes.Data.User.ID {
		t.Fatalf("invitation changed account membership: %+v", members)
	}
	if strings.Contains(wAgent.Body.String(), `"token"`) || strings.Contains(wAgent.Body.String(), `"password"`) {
		t.Fatal("invitation response exposed credential fields")
	}
}
