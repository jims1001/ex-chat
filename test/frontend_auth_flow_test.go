package test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestFrontendAuthFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_jwt_secret_frontend_auth_flow_32b!",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	makeReq := func(token, method, path string, body any) *httptest.ResponseRecorder {
		var reqBody *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reqBody = bytes.NewReader(b)
		} else {
			reqBody = bytes.NewReader([]byte{})
		}
		rq := httptest.NewRequest(method, path, reqBody)
		if token != "" {
			rq.Header.Set("Authorization", "Bearer "+token)
		}
		rq.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, rq)
		return rec
	}

	t.Run("1. Initial state without token returns 401 on protected profile route", func(t *testing.T) {
		res := makeReq("", http.MethodGet, "/api/v1/profile", nil)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 unauthorized without token, got %d: %s", res.Code, res.Body.String())
		}
	})

	t.Run("2. Invalid token returns 401 triggering frontend auth expiry", func(t *testing.T) {
		res := makeReq("invalid.expired.jwt.token", http.MethodGet, "/api/v1/profile", nil)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 unauthorized with bad token, got %d: %s", res.Code, res.Body.String())
		}
	})

	var authToken string
	var registeredEmail = "alice.support@example.com"
	var registeredPassword = "Password123!"

	t.Run("3. User Sign Up initializes account and returns valid token", func(t *testing.T) {
		signUpBody := map[string]string{
			"email":        registeredEmail,
			"password":     registeredPassword,
			"name":         "爱丽丝",
			"account_name": "星河互娱工作区",
		}
		res := makeReq("", http.MethodPost, "/auth/sign_up", signUpBody)
		if res.Code != http.StatusCreated && res.Code != http.StatusOK {
			t.Fatalf("sign up failed with status %d: %s", res.Code, res.Body.String())
		}

		var signUpResp struct {
			Data struct {
				Token string      `json:"token"`
				User  domain.User `json:"user"`
			} `json:"data"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &signUpResp); err != nil {
			t.Fatalf("failed to decode signup json: %v", err)
		}

		if signUpResp.Data.Token == "" {
			t.Fatalf("expected non-empty token upon sign up")
		}
		authToken = signUpResp.Data.Token

		if signUpResp.Data.User.Name != "爱丽丝" {
			t.Errorf("expected user name '爱丽丝', got '%s'", signUpResp.Data.User.Name)
		}
	})

	t.Run("4. Profile loading retrieves user details and workspaces for initialization", func(t *testing.T) {
		res := makeReq(authToken, http.MethodGet, "/api/v1/profile", nil)
		if res.Code != http.StatusOK {
			t.Fatalf("failed to get profile, status %d: %s", res.Code, res.Body.String())
		}

		var profResp struct {
			Data struct {
				User     domain.User      `json:"user"`
				Accounts []domain.Account `json:"accounts"`
			} `json:"data"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &profResp); err != nil {
			t.Fatalf("failed to parse profile response: %v", err)
		}

		if profResp.Data.User.Name != "爱丽丝" {
			t.Errorf("expected user name '爱丽丝', got '%s'", profResp.Data.User.Name)
		}
		if len(profResp.Data.Accounts) == 0 {
			t.Errorf("expected at least 1 account associated with user, got 0")
		}
	})

	t.Run("5. User Sign In succeeds with valid credentials", func(t *testing.T) {
		signInBody := map[string]string{
			"email":    registeredEmail,
			"password": registeredPassword,
		}
		res := makeReq("", http.MethodPost, "/auth/sign_in", signInBody)
		if res.Code != http.StatusOK {
			t.Fatalf("sign in failed with status %d: %s", res.Code, res.Body.String())
		}

		var signInResp struct {
			Data struct {
				Token string      `json:"token"`
				User  domain.User `json:"user"`
			} `json:"data"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &signInResp); err != nil {
			t.Fatalf("failed to parse signin response: %v", err)
		}
		if signInResp.Data.Token == "" {
			t.Fatalf("expected non-empty token on sign in")
		}
	})

	t.Run("6. User Sign In fails with invalid password", func(t *testing.T) {
		signInBody := map[string]string{
			"email":    registeredEmail,
			"password": "WrongPassword999!",
		}
		res := makeReq("", http.MethodPost, "/auth/sign_in", signInBody)
		if res.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 for wrong credentials, got %d: %s", res.Code, res.Body.String())
		}
	})

	t.Run("7. Password reset request succeeds", func(t *testing.T) {
		resetBody := map[string]string{
			"email": registeredEmail,
		}
		res := makeReq("", http.MethodPost, "/auth/password", resetBody)
		if res.Code != http.StatusOK {
			t.Fatalf("expected 200 for reset password request, got %d: %s", res.Code, res.Body.String())
		}
	})

	t.Run("8. User Sign Out cleanly ends session", func(t *testing.T) {
		res := makeReq(authToken, http.MethodPost, "/auth/sign_out", nil)
		if res.Code != http.StatusOK {
			t.Fatalf("expected 200 for sign out, got %d: %s", res.Code, res.Body.String())
		}
	})
}
