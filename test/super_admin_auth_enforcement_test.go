package test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestSuperAdminAuthEnforcement(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "super_admin_jwt_secret_key_32bytes!",
		JWTExpirationHours: 72,
		SuperAdminEmails:   []string{"super_whitelist@oraclebetx.com"},
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// Helper for HTTP requests
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

	// 1. Setup Tenant Admin (Signs up normally, Role: administrator in tenant, Type: User)
	tenantAdminSignUp, _ := json.Marshal(map[string]string{
		"name":         "Tenant Admin",
		"email":        "tenant_admin@example.com",
		"password":     "Password123!",
		"account_name": "Tenant One",
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(tenantAdminSignUp))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("tenant admin sign up failed: code=%d, body=%s", w.Code, w.Body.String())
	}
	var tenantAdminResp struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &tenantAdminResp)
	tenantAdminToken := tenantAdminResp.Data.Token
	accID := tenantAdminResp.Data.Accounts[0].ID
	accStr := strconv.FormatUint(uint64(accID), 10)

	// 2. Setup Plain Agent (Role: agent, Type: User)
	recAgentAdd := makeReq(tenantAdminToken, http.MethodPost, "/api/v1/accounts/"+accStr+"/agents", map[string]string{
		"name":     "Plain Agent",
		"email":    "plain_agent@example.com",
		"password": "Password123!",
		"role":     "agent",
	})
	if recAgentAdd.Code != http.StatusCreated {
		t.Fatalf("failed to add agent: %d", recAgentAdd.Code)
	}

	agentSignIn, _ := json.Marshal(map[string]string{
		"email":    "plain_agent@example.com",
		"password": "Password123!",
	})
	req = httptest.NewRequest(http.MethodPost, "/auth/sign_in", bytes.NewReader(agentSignIn))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("agent sign in failed: %d", w.Code)
	}
	var agentAuthResp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &agentAuthResp)
	plainAgentToken := agentAuthResp.Data.Token

	// 3. Setup Super Admin User via Type = "SuperAdmin"
	hash, _ := auth.HashPassword("SuperSecret123!")
	superAdminTypeUser := domain.User{
		Name:         "Root Super Admin",
		Email:        "root_superadmin@oraclebetx.com",
		PasswordHash: hash,
		Role:         domain.RoleAgent,
		Type:         domain.UserTypeSuperAdmin,
		Availability: domain.AvailabilityOnline,
	}
	db.Create(&superAdminTypeUser)
	superAdminTypeToken, err := auth.GenerateToken(&superAdminTypeUser, cfg.JWTSecret, cfg.JWTExpirationHours)
	if err != nil {
		t.Fatalf("failed to generate token for super admin type: %v", err)
	}

	// 4. Setup Super Admin User via Role = "super_admin"
	superAdminRoleUser := domain.User{
		Name:         "Role Super Admin",
		Email:        "role_superadmin@oraclebetx.com",
		PasswordHash: hash,
		Role:         domain.RoleSuperAdmin,
		Type:         domain.UserTypeUser,
		Availability: domain.AvailabilityOnline,
	}
	db.Create(&superAdminRoleUser)
	superAdminRoleToken, err := auth.GenerateToken(&superAdminRoleUser, cfg.JWTSecret, cfg.JWTExpirationHours)
	if err != nil {
		t.Fatalf("failed to generate token for super admin role: %v", err)
	}

	// 5. Setup Whitelisted Email User (Role: agent, Type: User, but in cfg.SuperAdminEmails)
	whitelistUser := domain.User{
		Name:         "Whitelisted Super Admin",
		Email:        "super_whitelist@oraclebetx.com",
		PasswordHash: hash,
		Role:         domain.RoleAgent,
		Type:         domain.UserTypeUser,
		Availability: domain.AvailabilityOnline,
	}
	db.Create(&whitelistUser)
	whitelistToken, err := auth.GenerateToken(&whitelistUser, cfg.JWTSecret, cfg.JWTExpirationHours)
	if err != nil {
		t.Fatalf("failed to generate token for whitelist user: %v", err)
	}

	// -------------------------------------------------------------
	// Scenario 1: Plain Agent MUST be 403 Forbidden
	// -------------------------------------------------------------
	t.Run("Scenario 1: Plain Agent is 403 Forbidden", func(t *testing.T) {
		recGet := makeReq(plainAgentToken, http.MethodGet, "/api/v1/super_admin/configs", nil)
		if recGet.Code != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden on GET /api/v1/super_admin/configs for plain agent, got %d (body: %s)", recGet.Code, recGet.Body.String())
		}

		recPost := makeReq(plainAgentToken, http.MethodPost, "/api/v1/super_admin/configs", map[string]string{
			"config_key": "INSTALLATION_NAME",
			"value":      "Hacked Instance Name",
		})
		if recPost.Code != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden on POST /api/v1/super_admin/configs for plain agent, got %d (body: %s)", recPost.Code, recPost.Body.String())
		}
	})

	// -------------------------------------------------------------
	// Scenario 2: Tenant Administrator (Not SuperAdmin) MUST be 403 Forbidden
	// -------------------------------------------------------------
	t.Run("Scenario 2: Tenant Administrator is 403 Forbidden", func(t *testing.T) {
		recGet := makeReq(tenantAdminToken, http.MethodGet, "/api/v1/super_admin/configs", nil)
		if recGet.Code != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden on GET /api/v1/super_admin/configs for tenant admin, got %d (body: %s)", recGet.Code, recGet.Body.String())
		}

		recPost := makeReq(tenantAdminToken, http.MethodPost, "/api/v1/super_admin/configs", map[string]string{
			"config_key": "INSTALLATION_NAME",
			"value":      "Tenant Hacked Instance",
		})
		if recPost.Code != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden on POST /api/v1/super_admin/configs for tenant admin, got %d (body: %s)", recPost.Code, recPost.Body.String())
		}
	})

	// -------------------------------------------------------------
	// Scenario 3: Anonymous / Unauthenticated is 401 Unauthorized
	// -------------------------------------------------------------
	t.Run("Scenario 3: Anonymous Request is 401 Unauthorized", func(t *testing.T) {
		recGet := makeReq("", http.MethodGet, "/api/v1/super_admin/configs", nil)
		if recGet.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 Unauthorized on GET /api/v1/super_admin/configs, got %d", recGet.Code)
		}

		recPost := makeReq("", http.MethodPost, "/api/v1/super_admin/configs", map[string]string{
			"config_key": "INSTALLATION_NAME",
			"value":      "Anon Value",
		})
		if recPost.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 Unauthorized on POST /api/v1/super_admin/configs, got %d", recPost.Code)
		}
	})

	// -------------------------------------------------------------
	// Scenario 4: Super Admin by User.Type = "SuperAdmin" is 200 OK
	// -------------------------------------------------------------
	t.Run("Scenario 4: Super Admin by User.Type Allowed", func(t *testing.T) {
		recPost := makeReq(superAdminTypeToken, http.MethodPost, "/api/v1/super_admin/configs", map[string]string{
			"config_key": "ENABLE_GLOBAL_AI",
			"value":      "true",
		})
		if recPost.Code != http.StatusOK {
			t.Fatalf("expected 200 OK on POST /api/v1/super_admin/configs for SuperAdmin type, got %d (body: %s)", recPost.Code, recPost.Body.String())
		}

		recGet := makeReq(superAdminTypeToken, http.MethodGet, "/api/v1/super_admin/configs", nil)
		if recGet.Code != http.StatusOK {
			t.Fatalf("expected 200 OK on GET /api/v1/super_admin/configs for SuperAdmin type, got %d", recGet.Code)
		}

		var configsResp struct {
			Data []domain.SystemConfig `json:"data"`
		}
		_ = json.Unmarshal(recGet.Body.Bytes(), &configsResp)
		found := false
		for _, cfgItem := range configsResp.Data {
			if cfgItem.ConfigKey == "ENABLE_GLOBAL_AI" && cfgItem.Value == "true" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected to find ENABLE_GLOBAL_AI in system configs list")
		}
	})

	// -------------------------------------------------------------
	// Scenario 5: Super Admin by User.Role = "super_admin" is 200 OK
	// -------------------------------------------------------------
	t.Run("Scenario 5: Super Admin by User.Role Allowed", func(t *testing.T) {
		recPost := makeReq(superAdminRoleToken, http.MethodPost, "/api/v1/super_admin/configs", map[string]string{
			"config_key": "STORAGE_MAX_UPLOAD_MB",
			"value":      "100",
		})
		if recPost.Code != http.StatusOK {
			t.Fatalf("expected 200 OK on POST /api/v1/super_admin/configs for super_admin role, got %d (body: %s)", recPost.Code, recPost.Body.String())
		}

		recGet := makeReq(superAdminRoleToken, http.MethodGet, "/api/v1/super_admin/configs", nil)
		if recGet.Code != http.StatusOK {
			t.Fatalf("expected 200 OK on GET /api/v1/super_admin/configs for super_admin role, got %d", recGet.Code)
		}
	})

	// -------------------------------------------------------------
	// Scenario 6: Super Admin by Config Email Whitelist is 200 OK
	// -------------------------------------------------------------
	t.Run("Scenario 6: Super Admin by Email Whitelist Allowed", func(t *testing.T) {
		recGet := makeReq(whitelistToken, http.MethodGet, "/api/v1/super_admin/configs", nil)
		if recGet.Code != http.StatusOK {
			t.Fatalf("expected 200 OK on GET /api/v1/super_admin/configs for whitelisted email, got %d (body: %s)", recGet.Code, recGet.Body.String())
		}

		recPost := makeReq(whitelistToken, http.MethodPost, "/api/v1/super_admin/configs", map[string]string{
			"config_key": "INSTANCE_TAG",
			"value":      "production-cluster-01",
		})
		if recPost.Code != http.StatusOK {
			t.Fatalf("expected 200 OK on POST /api/v1/super_admin/configs for whitelisted email, got %d (body: %s)", recPost.Code, recPost.Body.String())
		}
	})
}
