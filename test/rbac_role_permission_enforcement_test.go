package test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestRBACRolePermissionEnforcement(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "rbac_enforcement_jwt_secret_32bytes!!",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up Administrator
	adminSignUp, _ := json.Marshal(map[string]string{
		"name":         "Super Admin",
		"email":        "admin@example.com",
		"password":     "AdminPassword123!",
		"account_name": "RBAC Fortress Org",
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(adminSignUp))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("admin sign up failed: code=%d, body=%s", w.Code, w.Body.String())
	}

	var adminAuthResp struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &adminAuthResp)
	adminToken := adminAuthResp.Data.Token
	accID := adminAuthResp.Data.Accounts[0].ID
	accStr := strconv.FormatUint(uint64(accID), 10)

	// Helper for making authenticated requests
	makeAuthReq := func(token, method, path string, body any) *httptest.ResponseRecorder {
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

	// 2. Add an Agent under the account using Admin token
	addAgentRec := makeAuthReq(adminToken, http.MethodPost, "/api/v1/accounts/"+accStr+"/agents", map[string]string{
		"name":     "Plain Agent",
		"email":    "plain_agent@example.com",
		"password": "AgentPassword123!",
		"role":     "agent",
	})
	if addAgentRec.Code != http.StatusCreated {
		t.Fatalf("failed to add plain agent: code=%d, body=%s", addAgentRec.Code, addAgentRec.Body.String())
	}

	// Sign in as Plain Agent to obtain agent token
	agentSignIn, _ := json.Marshal(map[string]string{
		"email":    "plain_agent@example.com",
		"password": "AgentPassword123!",
	})
	req = httptest.NewRequest(http.MethodPost, "/auth/sign_in", bytes.NewReader(agentSignIn))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("agent sign in failed: code=%d, body=%s", w.Code, w.Body.String())
	}

	var agentAuthResp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &agentAuthResp)
	agentToken := agentAuthResp.Data.Token

	// -------------------------------------------------------------
	// Scenario 1: Plain Agent MUST receive 403 on all management endpoints
	// -------------------------------------------------------------
	t.Run("Scenario 1: Plain Agent Forbidden on Management Endpoints", func(t *testing.T) {
		restrictedEndpoints := []struct {
			name   string
			method string
			path   string
			body   any
		}{
			{
				name:   "Account Settings Update",
				method: http.MethodPut,
				path:   "/api/v1/accounts/" + accStr,
				body:   map[string]any{"name": "Hacked Org Name"},
			},
			{
				name:   "Add Agent",
				method: http.MethodPost,
				path:   "/api/v1/accounts/" + accStr + "/agents",
				body:   map[string]any{"name": "Spy", "email": "spy@example.com", "role": "administrator"},
			},
			{
				name:   "List Custom Roles",
				method: http.MethodGet,
				path:   "/api/v1/accounts/" + accStr + "/custom_roles",
				body:   nil,
			},
			{
				name:   "Create Custom Role",
				method: http.MethodPost,
				path:   "/api/v1/accounts/" + accStr + "/custom_roles",
				body:   map[string]any{"name": "SuperPriv", "permissions": `["*"]`},
			},
			{
				name:   "Create Automation Rule",
				method: http.MethodPost,
				path:   "/api/v1/accounts/" + accStr + "/automation_rules",
				body:   map[string]any{"name": "Sneaky Rule", "event_name": "conversation_created"},
			},
			{
				name:   "Create SLA Policy",
				method: http.MethodPost,
				path:   "/api/v1/accounts/" + accStr + "/sla_policies",
				body:   map[string]any{"name": "Sneaky SLA", "rt_threshold": 60},
			},
			{
				name:   "Access Audit Logs",
				method: http.MethodGet,
				path:   "/api/v1/accounts/" + accStr + "/audit_logs",
				body:   nil,
			},
			{
				name:   "Access SAML Settings",
				method: http.MethodGet,
				path:   "/api/v1/accounts/" + accStr + "/saml_settings",
				body:   nil,
			},
			{
				name:   "Access Subscriptions & Billing",
				method: http.MethodGet,
				path:   "/api/v1/accounts/" + accStr + "/subscription",
				body:   nil,
			},
			{
				name:   "Create AI Captain Assistant",
				method: http.MethodPost,
				path:   "/api/v1/accounts/" + accStr + "/captain/assistants",
				body:   map[string]any{"name": "Rogue Bot", "model": "gpt-4"},
			},
			{
				name:   "View Reports Summary (V2)",
				method: http.MethodGet,
				path:   "/api/v2/accounts/" + accStr + "/reports/summary",
				body:   nil,
			},
		}

		for _, tc := range restrictedEndpoints {
			t.Run(tc.name, func(t *testing.T) {
				rec := makeAuthReq(agentToken, tc.method, tc.path, tc.body)
				if rec.Code != http.StatusForbidden {
					t.Errorf("expected 403 Forbidden for plain agent on [%s %s], got %d (body: %s)",
						tc.method, tc.path, rec.Code, rec.Body.String())
				}
			})
		}
	})

	// -------------------------------------------------------------
	// Scenario 2: Plain Agent CAN perform standard support operations
	// -------------------------------------------------------------
	t.Run("Scenario 2: Plain Agent Allowed on Support Operations", func(t *testing.T) {
		// 1. List agents (needed for assignment and mentions)
		rec := makeAuthReq(agentToken, http.MethodGet, "/api/v1/accounts/"+accStr+"/agents", nil)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK for plain agent on GET /agents, got %d", rec.Code)
		}

		// 2. List inboxes
		rec = makeAuthReq(agentToken, http.MethodGet, "/api/v1/accounts/"+accStr+"/inboxes", nil)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK for plain agent on GET /inboxes, got %d", rec.Code)
		}

		// 3. List teams
		rec = makeAuthReq(agentToken, http.MethodGet, "/api/v1/accounts/"+accStr+"/teams", nil)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK for plain agent on GET /teams, got %d", rec.Code)
		}

		// 4. List contacts
		rec = makeAuthReq(agentToken, http.MethodGet, "/api/v1/accounts/"+accStr+"/contacts", nil)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK for plain agent on GET /contacts, got %d", rec.Code)
		}

		// 5. List conversations
		rec = makeAuthReq(agentToken, http.MethodGet, "/api/v1/accounts/"+accStr+"/conversations", nil)
		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK for plain agent on GET /conversations, got %d", rec.Code)
		}
	})

	// -------------------------------------------------------------
	// Scenario 3: Granular Custom Role Authorization
	// -------------------------------------------------------------
	t.Run("Scenario 3: Granular Custom Role Authorization", func(t *testing.T) {
		// Admin creates a custom role granting "agent_manage" and "report_view"
		rolePerms := `["` + domain.PermissionAgentManage + `", "` + domain.PermissionReportView + `"]`
		recRole := makeAuthReq(adminToken, http.MethodPost, "/api/v1/accounts/"+accStr+"/custom_roles", map[string]any{
			"name":        "Team Lead",
			"description": "Can manage agents and view reports",
			"permissions": rolePerms,
		})
		if recRole.Code != http.StatusCreated {
			t.Fatalf("admin failed to create custom role: %d (body: %s)", recRole.Code, recRole.Body.String())
		}

		var roleResp struct {
			Data domain.CustomRole `json:"data"`
		}
		_ = json.Unmarshal(recRole.Body.Bytes(), &roleResp)
		customRoleID := roleResp.Data.ID

		// Admin adds a specialized agent with this custom role
		recSpecialAgent := makeAuthReq(adminToken, http.MethodPost, "/api/v1/accounts/"+accStr+"/agents", map[string]any{
			"name":           "Team Leader Bob",
			"email":          "teamlead_bob@example.com",
			"password":       "BobPassword123!",
			"role":           "agent",
			"custom_role_id": customRoleID,
		})
		if recSpecialAgent.Code != http.StatusCreated {
			t.Fatalf("failed to add team lead agent: %d (body: %s)", recSpecialAgent.Code, recSpecialAgent.Body.String())
		}

		// Sign in as Team Leader Bob
		leaderSignIn, _ := json.Marshal(map[string]string{
			"email":    "teamlead_bob@example.com",
			"password": "BobPassword123!",
		})
		req = httptest.NewRequest(http.MethodPost, "/auth/sign_in", bytes.NewReader(leaderSignIn))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("team lead sign in failed: code=%d", w.Code)
		}
		var leaderAuthResp struct {
			Data struct {
				Token string `json:"token"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &leaderAuthResp)
		leaderToken := leaderAuthResp.Data.Token

		// A. Verify Bob CAN add an agent (granted by agent_manage)
		recAddByBob := makeAuthReq(leaderToken, http.MethodPost, "/api/v1/accounts/"+accStr+"/agents", map[string]any{
			"name":     "Junior Agent",
			"email":    "junior@example.com",
			"password": "JuniorPassword123!",
			"role":     "agent",
		})
		if recAddByBob.Code != http.StatusCreated {
			t.Errorf("expected 201 Created for Team Lead on POST /agents, got %d (body: %s)", recAddByBob.Code, recAddByBob.Body.String())
		}

		// B. Verify Bob CAN view reports (granted by report_view)
		recReportByBob := makeAuthReq(leaderToken, http.MethodGet, "/api/v2/accounts/"+accStr+"/reports/summary", nil)
		if recReportByBob.Code != http.StatusOK {
			t.Errorf("expected 200 OK for Team Lead on GET /reports/summary, got %d (body: %s)", recReportByBob.Code, recReportByBob.Body.String())
		}

		// C. Verify Bob CANNOT manage SLA policies (not granted -> 403)
		recSLAByBob := makeAuthReq(leaderToken, http.MethodPost, "/api/v1/accounts/"+accStr+"/sla_policies", map[string]any{
			"name":         "Unauthorized SLA",
			"rt_threshold": 60,
		})
		if recSLAByBob.Code != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden for Team Lead on POST /sla_policies, got %d", recSLAByBob.Code)
		}

		// D. Verify Bob CANNOT view audit logs (not granted -> 403)
		recAuditByBob := makeAuthReq(leaderToken, http.MethodGet, "/api/v1/accounts/"+accStr+"/audit_logs", nil)
		if recAuditByBob.Code != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden for Team Lead on GET /audit_logs, got %d", recAuditByBob.Code)
		}

		// E. Verify Bob CANNOT access billing/subscriptions (not granted -> 403)
		recSubByBob := makeAuthReq(leaderToken, http.MethodGet, "/api/v1/accounts/"+accStr+"/subscription", nil)
		if recSubByBob.Code != http.StatusForbidden {
			t.Errorf("expected 403 Forbidden for Team Lead on GET /subscription, got %d", recSubByBob.Code)
		}
	})

	// -------------------------------------------------------------
	// Scenario 4: Administrator has full access
	// -------------------------------------------------------------
	t.Run("Scenario 4: Administrator Full Access", func(t *testing.T) {
		// Admin accesses SLA policies
		recSLA := makeAuthReq(adminToken, http.MethodGet, "/api/v1/accounts/"+accStr+"/sla_policies", nil)
		if recSLA.Code != http.StatusOK {
			t.Errorf("expected 200 OK for Admin on GET /sla_policies, got %d", recSLA.Code)
		}

		// Admin accesses Audit Logs
		recAudit := makeAuthReq(adminToken, http.MethodGet, "/api/v1/accounts/"+accStr+"/audit_logs", nil)
		if recAudit.Code != http.StatusOK {
			t.Errorf("expected 200 OK for Admin on GET /audit_logs, got %d", recAudit.Code)
		}

		// Admin accesses Subscriptions
		recSub := makeAuthReq(adminToken, http.MethodGet, "/api/v1/accounts/"+accStr+"/subscription", nil)
		if recSub.Code != http.StatusOK {
			t.Errorf("expected 200 OK for Admin on GET /subscription, got %d", recSub.Code)
		}

		// Admin accesses Reports
		recRep := makeAuthReq(adminToken, http.MethodGet, "/api/v2/accounts/"+accStr+"/reports/summary", nil)
		if recRep.Code != http.StatusOK {
			t.Errorf("expected 200 OK for Admin on GET /reports/summary, got %d", recRep.Code)
		}
	})
}
