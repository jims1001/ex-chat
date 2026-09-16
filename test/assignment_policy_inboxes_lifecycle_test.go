package test

import (
	"bytes"
	"encoding/json"
	"fmt"
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

func TestAssignmentPolicyInboxesLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_policy_inboxes_lifecycle_12345",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// Helper to signup an account
	signup := func(name, email, accountName string) (string, uint) {
		payload := map[string]string{
			"name":         name,
			"email":        email,
			"password":     "Password123!",
			"account_name": accountName,
		}
		b, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("signup failed: %s", w.Body.String())
		}
		var res struct {
			Data struct {
				Token    string           `json:"token"`
				Accounts []domain.Account `json:"accounts"`
			} `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &res)
		return res.Data.Token, res.Data.Accounts[0].ID
	}

	tokenA, accountA := signup("Admin A", "adminA@example.com", "Corp A")
	tokenB, accountB := signup("Admin B", "adminB@example.com", "Corp B")

	// Helper to create an inbox for an account
	createInbox := func(token string, accID uint, name string) uint {
		payload := map[string]interface{}{
			"name":         name,
			"channel_type": "Channel::Api",
		}
		b, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes", accID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated && w.Code != http.StatusOK {
			t.Fatalf("create inbox failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var res struct {
			Data struct {
				ID uint `json:"id"`
			} `json:"data"`
			ID uint `json:"id"`
		}
		json.Unmarshal(w.Body.Bytes(), &res)
		if res.Data.ID > 0 {
			return res.Data.ID
		}
		return res.ID
	}

	// Helper to create assignment policy
	createPolicy := func(token string, accID uint, name string) uint {
		payload := map[string]interface{}{
			"name":          name,
			"strategy_type": "round_robin",
		}
		b, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/assignment_policies", accID), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated && w.Code != http.StatusOK {
			t.Fatalf("create policy failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var res struct {
			Data struct {
				ID uint `json:"id"`
			} `json:"data"`
			ID uint `json:"id"`
		}
		json.Unmarshal(w.Body.Bytes(), &res)
		if res.Data.ID > 0 {
			return res.Data.ID
		}
		return res.ID
	}

	inboxA1 := createInbox(tokenA, accountA, "Support Inbox 1")
	inboxA2 := createInbox(tokenA, accountA, "Support Inbox 2")
	inboxA3 := createInbox(tokenA, accountA, "Support Inbox 3")
	policyA := createPolicy(tokenA, accountA, "Standard Policy")

	inboxB1 := createInbox(tokenB, accountB, "Tenant B Inbox")
	policyB := createPolicy(tokenB, accountB, "Tenant B Policy")

	t.Run("GET /assignment_policies/:id/inboxes empty initially", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/assignment_policies/%d/inboxes", accountA, policyA), nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var res struct {
			Inboxes []domain.Inbox `json:"inboxes"`
			Data    struct {
				Inboxes []domain.Inbox `json:"inboxes"`
			} `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &res)
		if len(res.Inboxes) != 0 {
			t.Fatalf("expected 0 inboxes, got %d", len(res.Inboxes))
		}
	})

	t.Run("GET /assignment_policies/:id/inboxes unauthenticated returns 401", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/assignment_policies/%d/inboxes", accountA, policyA), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", w.Code)
		}
	})

	t.Run("POST /assignment_policies/:id/inboxes associates inboxes", func(t *testing.T) {
		payload := map[string]interface{}{
			"inbox_ids": []uint{inboxA1, inboxA2},
		}
		b, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/assignment_policies/%d/inboxes", accountA, policyA), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var res struct {
			Inboxes []domain.Inbox `json:"inboxes"`
		}
		json.Unmarshal(w.Body.Bytes(), &res)
		if len(res.Inboxes) != 2 {
			t.Fatalf("expected 2 inboxes attached, got %d", len(res.Inboxes))
		}
	})

	t.Run("GET /assignment_policies/:id/inboxes returns associated inboxes", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/assignment_policies/%d/inboxes", accountA, policyA), nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var res struct {
			Inboxes []domain.Inbox `json:"inboxes"`
		}
		json.Unmarshal(w.Body.Bytes(), &res)
		if len(res.Inboxes) != 2 {
			t.Fatalf("expected 2 inboxes, got %d", len(res.Inboxes))
		}
		if res.Inboxes[0].ID != inboxA1 && res.Inboxes[1].ID != inboxA1 {
			t.Fatalf("inboxA1 not found in policy inboxes list")
		}
	})

	t.Run("GET /inboxes/:id/assignment_policy returns current policy", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/assignment_policy", accountA, inboxA1), nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var res struct {
			ID   uint   `json:"id"`
			Name string `json:"name"`
		}
		json.Unmarshal(w.Body.Bytes(), &res)
		if res.ID != policyA {
			t.Fatalf("expected policy %d, got %d", policyA, res.ID)
		}
	})

	t.Run("GET /inboxes/:id/assignment_policy returns 404 when no policy is bound", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/assignment_policy", accountA, inboxA3), nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("DELETE /assignment_policies/:id/inboxes/:inbox_id removes inbox from policy", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/assignment_policies/%d/inboxes/%d", accountA, policyA, inboxA1), nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		// Verify GET /inboxes/:id/assignment_policy now returns 404
		chkReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/assignment_policy", accountA, inboxA1), nil)
		chkReq.Header.Set("Authorization", "Bearer "+tokenA)
		chkW := httptest.NewRecorder()
		r.ServeHTTP(chkW, chkReq)
		if chkW.Code != http.StatusNotFound {
			t.Fatalf("expected 404 after removal, got %d: %s", chkW.Code, chkW.Body.String())
		}

		// Verify policy inboxes count decreased to 1
		listReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/assignment_policies/%d/inboxes", accountA, policyA), nil)
		listReq.Header.Set("Authorization", "Bearer "+tokenA)
		listW := httptest.NewRecorder()
		r.ServeHTTP(listW, listReq)
		var res struct {
			Inboxes []domain.Inbox `json:"inboxes"`
		}
		json.Unmarshal(listW.Body.Bytes(), &res)
		if len(res.Inboxes) != 1 || res.Inboxes[0].ID != inboxA2 {
			t.Fatalf("expected 1 remaining inbox (inboxA2), got: %+v", res.Inboxes)
		}
	})

	t.Run("DELETE /inboxes/:id/assignment_policy unbinds policy from inbox", func(t *testing.T) {
		// inboxA2 still has policyA bound
		req := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/assignment_policy", accountA, inboxA2), nil)
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		// Subsequent DELETE should return 404
		w2 := httptest.NewRecorder()
		req2 := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/assignment_policy", accountA, inboxA2), nil)
		req2.Header.Set("Authorization", "Bearer "+tokenA)
		r.ServeHTTP(w2, req2)
		if w2.Code != http.StatusNotFound {
			t.Fatalf("expected 404 when unbinding unbound inbox, got %d: %s", w2.Code, w2.Body.String())
		}
	})

	t.Run("Tenant isolation: cannot bind another account's policy or inbox", func(t *testing.T) {
		// Account A tries to bind Account B's policy
		payload := map[string]interface{}{
			"assignment_policy_id": policyB,
		}
		b, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/assignment_policy", accountA, inboxA3), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("expected 404 when binding cross-tenant policy, got %d: %s", w.Code, w.Body.String())
		}

		// Account A tries to add Account B's inbox to policy A
		payload2 := map[string]interface{}{
			"inbox_id": inboxB1,
		}
		b2, _ := json.Marshal(payload2)
		req2 := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/assignment_policies/%d/inboxes", accountA, policyA), bytes.NewReader(b2))
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("Authorization", "Bearer "+tokenA)
		w2 := httptest.NewRecorder()
		r.ServeHTTP(w2, req2)
		if w2.Code != http.StatusNotFound {
			t.Fatalf("expected 404 when adding cross-tenant inbox, got %d: %s", w2.Code, w2.Body.String())
		}
	})

	t.Run("Conflict prevention: cannot bind inbox already associated with another policy without reassign=true", func(t *testing.T) {
		// Create a second policy for Account A
		policyA2 := createPolicy(tokenA, accountA, "Secondary Policy")

		// Associate inboxA1 with policyA
		b, _ := json.Marshal(map[string]interface{}{
			"inbox_id": inboxA1,
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/assignment_policies/%d/inboxes", accountA, policyA), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tokenA)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 associating inboxA1 with policyA, got %d: %s", w.Code, w.Body.String())
		}

		// Try to associate inboxA1 with policyA2 without reassign -> should return 409 Conflict
		bConflict, _ := json.Marshal(map[string]interface{}{
			"inbox_id": inboxA1,
		})
		reqConflict := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/assignment_policies/%d/inboxes", accountA, policyA2), bytes.NewReader(bConflict))
		reqConflict.Header.Set("Content-Type", "application/json")
		reqConflict.Header.Set("Authorization", "Bearer "+tokenA)
		wConflict := httptest.NewRecorder()
		r.ServeHTTP(wConflict, reqConflict)
		if wConflict.Code != http.StatusConflict {
			t.Fatalf("expected 409 Conflict when attaching inbox already bound to another policy, got %d: %s", wConflict.Code, wConflict.Body.String())
		}

		// Also check via POST /inboxes/:id/assignment_policy without reassign
		bBindConflict, _ := json.Marshal(map[string]interface{}{
			"assignment_policy_id": policyA2,
		})
		reqBindConflict := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/assignment_policy", accountA, inboxA1), bytes.NewReader(bBindConflict))
		reqBindConflict.Header.Set("Content-Type", "application/json")
		reqBindConflict.Header.Set("Authorization", "Bearer "+tokenA)
		wBindConflict := httptest.NewRecorder()
		r.ServeHTTP(wBindConflict, reqBindConflict)
		if wBindConflict.Code != http.StatusConflict {
			t.Fatalf("expected 409 Conflict from POST /inboxes/:id/assignment_policy, got %d: %s", wBindConflict.Code, wBindConflict.Body.String())
		}

		// Now associate with reassign=true -> should succeed 200
		bReassign, _ := json.Marshal(map[string]interface{}{
			"inbox_id": inboxA1,
			"reassign": true,
		})
		reqReassign := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/assignment_policies/%d/inboxes", accountA, policyA2), bytes.NewReader(bReassign))
		reqReassign.Header.Set("Content-Type", "application/json")
		reqReassign.Header.Set("Authorization", "Bearer "+tokenA)
		wReassign := httptest.NewRecorder()
		r.ServeHTTP(wReassign, reqReassign)
		if wReassign.Code != http.StatusOK {
			t.Fatalf("expected 200 when reassign=true, got %d: %s", wReassign.Code, wReassign.Body.String())
		}

		// Verify inboxA1 is now associated with policyA2 and not policyA
		chkReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/assignment_policy", accountA, inboxA1), nil)
		chkReq.Header.Set("Authorization", "Bearer "+tokenA)
		chkW := httptest.NewRecorder()
		r.ServeHTTP(chkW, chkReq)
		if chkW.Code != http.StatusOK {
			t.Fatalf("expected 200 getting current policy, got %d", chkW.Code)
		}
		var chkRes struct {
			ID uint `json:"id"`
		}
		json.Unmarshal(chkW.Body.Bytes(), &chkRes)
		if chkRes.ID != policyA2 {
			t.Fatalf("expected policy %d after reassign, got %d", policyA2, chkRes.ID)
		}
	})
}
