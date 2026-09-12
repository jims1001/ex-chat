package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestSAMLLifecycle_Full(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "saml_test_secret_32bytes_random!!",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// Create test user and account
	account := &domain.Account{
		Name: "Enterprise SAML Account",
	}
	if err := db.Create(account).Error; err != nil {
		t.Fatalf("failed to create account: %v", err)
	}

	user := &domain.User{
		Email: "admin@enterprise.com",
		Name:  "Enterprise Admin",
		Role:  domain.RoleAdministrator,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	// Link user to account as administrator
	accountUser := &domain.AccountUser{
		AccountID: account.ID,
		UserID:    user.ID,
		Role:      domain.RoleAdministrator,
	}
	if err := db.Create(accountUser).Error; err != nil {
		t.Fatalf("failed to link user: %v", err)
	}

	token, err := auth.GenerateToken(user, cfg.JWTSecret, cfg.JWTExpirationHours)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	makeReq := func(method, path string, body any) *httptest.ResponseRecorder {
		var reqBody *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reqBody = bytes.NewReader(b)
		} else {
			reqBody = bytes.NewReader([]byte{})
		}
		rq := httptest.NewRequest(method, path, reqBody)
		rq.Header.Set("Authorization", "Bearer "+token)
		rq.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, rq)
		return rec
	}

	basePath := fmt.Sprintf("/api/v1/accounts/%d/saml_settings", account.ID)

	// 1. Initially GET returns default disabled configuration
	t.Run("1_GetInitialSAMLSettings", func(t *testing.T) {
		res := makeReq(http.MethodGet, basePath, nil)
		if res.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
		}
		var data struct {
			Data domain.SAMLSetting `json:"data"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &data)
		if data.Data.Enabled {
			t.Errorf("expected initial setting to be disabled")
		}
	})

	// 2. Create SAML Settings via POST
	t.Run("2_CreateSAMLSettings", func(t *testing.T) {
		payload := map[string]any{
			"sso_url":       "https://idp.okta.com/app/exchat/sso/saml",
			"certificate":   "-----BEGIN CERTIFICATE-----\nMIIDXTCCAkWgAwIBAgIJ...\n-----END CERTIFICATE-----",
			"role_mappings": map[string]string{"Admins": "administrator", "Agents": "agent"},
		}
		res := makeReq(http.MethodPost, basePath, payload)
		if res.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", res.Code, res.Body.String())
		}
		var data struct {
			Data domain.SAMLSetting `json:"data"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &data)
		if !data.Data.Enabled {
			t.Errorf("expected setting to be enabled after create")
		}
		if data.Data.SSOURL != payload["sso_url"] {
			t.Errorf("expected sso_url %s, got %s", payload["sso_url"], data.Data.SSOURL)
		}
	})

	// 3. Update SAML Settings via PUT with Chatwoot-style nested payload
	t.Run("3_UpdateSAMLSettings_NestedPayload", func(t *testing.T) {
		payload := map[string]any{
			"saml_settings": map[string]any{
				"sso_url": "https://idp.auth0.com/samlp/exchat",
			},
		}
		res := makeReq(http.MethodPut, basePath, payload)
		if res.Code != http.StatusOK {
			t.Fatalf("expected 200 on PUT, got %d: %s", res.Code, res.Body.String())
		}
		var data struct {
			Data domain.SAMLSetting `json:"data"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &data)
		if data.Data.SSOURL != "https://idp.auth0.com/samlp/exchat" {
			t.Errorf("expected updated sso_url, got %s", data.Data.SSOURL)
		}
		// Check certificate wasn't wiped
		if data.Data.Certificate == "" {
			t.Errorf("expected certificate to be preserved during partial update")
		}
	})

	// 4. Disable SAML Settings via explicit endpoint
	t.Run("4_DisableSAMLSettings", func(t *testing.T) {
		res := makeReq(http.MethodPost, basePath+"/disable", nil)
		if res.Code != http.StatusOK {
			t.Fatalf("expected 200 on disable, got %d: %s", res.Code, res.Body.String())
		}

		// Verify GET reflects disabled state
		getRes := makeReq(http.MethodGet, basePath, nil)
		var data struct {
			Data domain.SAMLSetting `json:"data"`
		}
		_ = json.Unmarshal(getRes.Body.Bytes(), &data)
		if data.Data.Enabled {
			t.Errorf("expected SAML setting to be disabled")
		}

		// Verify SAML Login rejects login when disabled
		loginRes := makeReq(http.MethodPost, "/api/v1/auth/saml_login", map[string]any{
			"email":      "admin@enterprise.com",
			"account_id": account.ID,
		})
		if loginRes.Code != http.StatusBadRequest {
			t.Errorf("expected 400 when SAML is disabled, got %d", loginRes.Code)
		}
	})

	// 5. Re-enable SAML Settings via enable endpoint
	t.Run("5_EnableSAMLSettings", func(t *testing.T) {
		res := makeReq(http.MethodPost, basePath+"/enable", nil)
		if res.Code != http.StatusOK {
			t.Fatalf("expected 200 on enable, got %d: %s", res.Code, res.Body.String())
		}

		// Verify GET reflects enabled state
		getRes := makeReq(http.MethodGet, basePath, nil)
		var data struct {
			Data domain.SAMLSetting `json:"data"`
		}
		_ = json.Unmarshal(getRes.Body.Bytes(), &data)
		if !data.Data.Enabled {
			t.Errorf("expected SAML setting to be enabled")
		}
	})

	// 6. Disable SAML Settings via PATCH with enabled=false
	t.Run("6_DisableViaPATCH", func(t *testing.T) {
		res := makeReq(http.MethodPatch, basePath, map[string]any{
			"enabled": false,
		})
		if res.Code != http.StatusOK {
			t.Fatalf("expected 200 on PATCH, got %d: %s", res.Code, res.Body.String())
		}
		var data struct {
			Data domain.SAMLSetting `json:"data"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &data)
		if data.Data.Enabled {
			t.Errorf("expected SAML setting to be disabled after PATCH")
		}
	})

	// 7. Delete SAML Settings via DELETE
	t.Run("7_DeleteSAMLSettings", func(t *testing.T) {
		res := makeReq(http.MethodDelete, basePath, nil)
		if res.Code != http.StatusOK {
			t.Fatalf("expected 200 on DELETE, got %d: %s", res.Code, res.Body.String())
		}
		var delResp struct {
			Data struct {
				Deleted bool `json:"deleted"`
			} `json:"data"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &delResp)
		if !delResp.Data.Deleted {
			t.Errorf("expected deleted=true in response")
		}

		// Verify subsequent GET returns blank/disabled default
		getRes := makeReq(http.MethodGet, basePath, nil)
		var data struct {
			Data domain.SAMLSetting `json:"data"`
		}
		_ = json.Unmarshal(getRes.Body.Bytes(), &data)
		if data.Data.SSOURL != "" || data.Data.Certificate != "" {
			t.Errorf("expected SAML setting to be deleted from database")
		}
	})
}
