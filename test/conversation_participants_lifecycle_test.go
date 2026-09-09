package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
)

func TestConversationParticipantsLifecycle(t *testing.T) {
	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             fmt.Sprintf("file:conv_parts_%d?mode=memory&cache=shared", time.Now().UnixNano()),
		JWTSecret:          "conv-parts-jwt-secret-2026",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up Account A
	signUpPayloadA := map[string]string{
		"account_name": "Collaboration Corp A",
		"name":         "Collab Admin A",
		"email":        "admin@collab-a.com",
		"password":     "Password123!",
	}
	bodyA, _ := json.Marshal(signUpPayloadA)
	reqA := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(bodyA))
	reqA.Header.Set("Content-Type", "application/json")
	wA := httptest.NewRecorder()
	r.ServeHTTP(wA, reqA)
	if wA.Code != http.StatusCreated {
		t.Fatalf("sign up account A failed: code=%d body=%s", wA.Code, wA.Body.String())
	}

	var authRespA struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wA.Body.Bytes(), &authRespA)
	tokenA := authRespA.Data.Token
	accountIDA := authRespA.Data.Accounts[0].ID

	// Create test inbox and contact for Account A
	inboxA := domain.Inbox{
		AccountID:   accountIDA,
		Name:        "Support Inbox A",
		ChannelType: "web",
	}
	db.Create(&inboxA)

	contactA := domain.Contact{
		AccountID: accountIDA,
		Name:      "Customer A",
		Email:     "customer@collab-a.com",
	}
	db.Create(&contactA)

	convA := domain.Conversation{
		AccountID: accountIDA,
		InboxID:   inboxA.ID,
		ContactID: contactA.ID,
		Status:    "open",
	}
	db.Create(&convA)
	convIDA := convA.ID

	// Sign up Account B for cross-tenant isolation testing
	signUpPayloadB := map[string]string{
		"account_name": "Collaboration Corp B",
		"name":         "Collab Admin B",
		"email":        "admin@collab-b.com",
		"password":     "Password123!",
	}
	bodyB, _ := json.Marshal(signUpPayloadB)
	reqB := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(bodyB))
	reqB.Header.Set("Content-Type", "application/json")
	wB := httptest.NewRecorder()
	r.ServeHTTP(wB, reqB)
	var authRespB struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wB.Body.Bytes(), &authRespB)
	tokenB := authRespB.Data.Token
	accountIDB := authRespB.Data.Accounts[0].ID

	// =========================================================================
	// Scenario 1: Add Participants & Verify Idempotency
	// =========================================================================
	t.Run("Add_Participants_And_List", func(t *testing.T) {
		addPayload := map[string]any{
			"user_ids": []uint{101, 102, 103},
		}
		body, _ := json.Marshal(addPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/participants", accountIDA, convIDA), bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tokenA)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for add participants, got %d body=%s", w.Code, w.Body.String())
		}

		var addResp struct {
			Data struct {
				Status     string `json:"status"`
				AddedCount int    `json:"added_count"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &addResp)
		if addResp.Data.Status != "ok" || addResp.Data.AddedCount != 3 {
			t.Errorf("expected status ok with 3 added, got status=%s, count=%d", addResp.Data.Status, addResp.Data.AddedCount)
		}

		// List participants
		listReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/participants", accountIDA, convIDA), nil)
		listReq.Header.Set("Authorization", "Bearer "+tokenA)
		listW := httptest.NewRecorder()
		r.ServeHTTP(listW, listReq)

		if listW.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for list participants, got %d", listW.Code)
		}

		var parts []domain.ConversationParticipant
		var listResp struct {
			Data []domain.ConversationParticipant `json:"data"`
		}
		_ = json.Unmarshal(listW.Body.Bytes(), &listResp)
		parts = listResp.Data

		if len(parts) != 3 {
			t.Fatalf("expected 3 participants, got %d", len(parts))
		}

		// Idempotent re-add should not duplicate records
		reAddReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/participants", accountIDA, convIDA), bytes.NewReader(body))
		reAddReq.Header.Set("Authorization", "Bearer "+tokenA)
		reAddReq.Header.Set("Content-Type", "application/json")
		reAddW := httptest.NewRecorder()
		r.ServeHTTP(reAddW, reAddReq)

		var count int64
		db.Model(&domain.ConversationParticipant{}).Where("conversation_id = ?", convIDA).Count(&count)
		if count != 3 {
			t.Errorf("expected count to remain 3 after re-add, got %d", count)
		}
	})

	// =========================================================================
	// Scenario 2: Single Participant Removal (DELETE /participants/:user_id)
	// =========================================================================
	t.Run("Remove_Single_Participant_Via_Path_Param", func(t *testing.T) {
		delReq := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/participants/101", accountIDA, convIDA), nil)
		delReq.Header.Set("Authorization", "Bearer "+tokenA)
		delW := httptest.NewRecorder()
		r.ServeHTTP(delW, delReq)

		if delW.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for remove participant, got %d body=%s", delW.Code, delW.Body.String())
		}

		var delResp struct {
			Data struct {
				Status       string `json:"status"`
				RemovedCount int64  `json:"removed_count"`
			} `json:"data"`
		}
		_ = json.Unmarshal(delW.Body.Bytes(), &delResp)
		if delResp.Data.RemovedCount != 1 {
			t.Errorf("expected 1 removed participant, got %d", delResp.Data.RemovedCount)
		}

		// Verify participant 101 is gone from DB
		var count int64
		db.Model(&domain.ConversationParticipant{}).Where("conversation_id = ? AND user_id = 101", convIDA).Count(&count)
		if count != 0 {
			t.Errorf("participant 101 was not deleted from DB")
		}

		// Deleting again should succeed gracefully with removed_count=0
		reDelReq := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/participants/101", accountIDA, convIDA), nil)
		reDelReq.Header.Set("Authorization", "Bearer "+tokenA)
		reDelW := httptest.NewRecorder()
		r.ServeHTTP(reDelW, reDelReq)
		if reDelW.Code != http.StatusOK {
			t.Errorf("expected 200 OK on re-delete, got %d", reDelW.Code)
		}
	})

	// =========================================================================
	// Scenario 3: Batch Participant Removal (DELETE /participants with body)
	// =========================================================================
	t.Run("Remove_Batch_Participants_Via_Body", func(t *testing.T) {
		delBody, _ := json.Marshal(map[string]any{
			"user_ids": []uint{102, 103},
		})
		delReq := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/participants", accountIDA, convIDA), bytes.NewReader(delBody))
		delReq.Header.Set("Authorization", "Bearer "+tokenA)
		delReq.Header.Set("Content-Type", "application/json")
		delW := httptest.NewRecorder()
		r.ServeHTTP(delW, delReq)

		if delW.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for batch remove, got %d body=%s", delW.Code, delW.Body.String())
		}

		var delResp struct {
			Data struct {
				Status       string `json:"status"`
				RemovedCount int64  `json:"removed_count"`
			} `json:"data"`
		}
		_ = json.Unmarshal(delW.Body.Bytes(), &delResp)
		if delResp.Data.RemovedCount != 2 {
			t.Errorf("expected 2 removed participants, got %d", delResp.Data.RemovedCount)
		}

		var totalLeft int64
		db.Model(&domain.ConversationParticipant{}).Where("conversation_id = ?", convIDA).Count(&totalLeft)
		if totalLeft != 0 {
			t.Errorf("expected 0 participants left, got %d", totalLeft)
		}
	})

	// =========================================================================
	// Scenario 4: Full Update Workflow (PUT /participants - Replace/Sync)
	// =========================================================================
	t.Run("Full_Update_Workflow_Diff_Sync", func(t *testing.T) {
		// First seed initial participants: [201, 202]
		p1 := domain.ConversationParticipant{ConversationID: convIDA, UserID: 201, CreatedAt: time.Now().UTC()}
		p2 := domain.ConversationParticipant{ConversationID: convIDA, UserID: 202, CreatedAt: time.Now().UTC()}
		db.Create(&p1)
		db.Create(&p2)

		// Full update to [202, 203, 204] (201 should be removed, 202 retained, 203 & 204 added)
		updateBody, _ := json.Marshal(map[string]any{
			"user_ids": []uint{202, 203, 204},
		})
		updateReq := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/participants", accountIDA, convIDA), bytes.NewReader(updateBody))
		updateReq.Header.Set("Authorization", "Bearer "+tokenA)
		updateReq.Header.Set("Content-Type", "application/json")
		updateW := httptest.NewRecorder()
		r.ServeHTTP(updateW, updateReq)

		if updateW.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for full update, got %d body=%s", updateW.Code, updateW.Body.String())
		}

		var updateResp struct {
			Data struct {
				Status       string                          `json:"status"`
				Added        []uint                          `json:"added"`
				Removed      []uint                          `json:"removed"`
				Participants []domain.ConversationParticipant `json:"participants"`
				TotalCount   int                             `json:"total_count"`
			} `json:"data"`
		}
		_ = json.Unmarshal(updateW.Body.Bytes(), &updateResp)

		if updateResp.Data.TotalCount != 3 {
			t.Errorf("expected 3 participants after full update, got %d", updateResp.Data.TotalCount)
		}
		if len(updateResp.Data.Added) != 2 { // 203, 204 added
			t.Errorf("expected 2 added user IDs, got %v", updateResp.Data.Added)
		}
		if len(updateResp.Data.Removed) != 1 || updateResp.Data.Removed[0] != 201 { // 201 removed
			t.Errorf("expected user 201 removed, got %v", updateResp.Data.Removed)
		}

		// Verify in DB
		var partsInDB []domain.ConversationParticipant
		db.Where("conversation_id = ?", convIDA).Order("user_id ASC").Find(&partsInDB)
		if len(partsInDB) != 3 {
			t.Fatalf("expected 3 in DB, got %d", len(partsInDB))
		}
		expectedIDs := []uint{202, 203, 204}
		for i, id := range expectedIDs {
			if partsInDB[i].UserID != id {
				t.Errorf("expected user_id %d at pos %d, got %d", id, i, partsInDB[i].UserID)
			}
		}

		// Test PATCH route as well
		patchBody, _ := json.Marshal(map[string]any{
			"user_ids": []uint{202},
		})
		patchReq := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/participants", accountIDA, convIDA), bytes.NewReader(patchBody))
		patchReq.Header.Set("Authorization", "Bearer "+tokenA)
		patchReq.Header.Set("Content-Type", "application/json")
		patchW := httptest.NewRecorder()
		r.ServeHTTP(patchW, patchReq)
		if patchW.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for PATCH participants, got %d", patchW.Code)
		}
		var finalCount int64
		db.Model(&domain.ConversationParticipant{}).Where("conversation_id = ?", convIDA).Count(&finalCount)
		if finalCount != 1 {
			t.Errorf("expected 1 participant after patch update, got %d", finalCount)
		}
	})

	// =========================================================================
	// Scenario 5: Multi-tenant Security Isolation
	// =========================================================================
	t.Run("Multi_Tenant_Isolation", func(t *testing.T) {
		// Account B attempts to add participants to Account A's conversation
		hackAddBody, _ := json.Marshal(map[string]any{"user_ids": []uint{999}})
		hackAddReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/participants", accountIDB, convIDA), bytes.NewReader(hackAddBody))
		hackAddReq.Header.Set("Authorization", "Bearer "+tokenB)
		hackAddReq.Header.Set("Content-Type", "application/json")
		hackAddW := httptest.NewRecorder()
		r.ServeHTTP(hackAddW, hackAddReq)
		if hackAddW.Code != http.StatusNotFound {
			t.Errorf("expected 404 Not Found when Account B adds participant to Account A conv, got %d", hackAddW.Code)
		}

		// Account B attempts to list Account A's participants
		hackListReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/participants", accountIDB, convIDA), nil)
		hackListReq.Header.Set("Authorization", "Bearer "+tokenB)
		hackListW := httptest.NewRecorder()
		r.ServeHTTP(hackListW, hackListReq)
		if hackListW.Code != http.StatusNotFound {
			t.Errorf("expected 404 Not Found when Account B lists Account A participants, got %d", hackListW.Code)
		}

		// Account B attempts to delete Account A's participant
		hackDelReq := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/participants/202", accountIDB, convIDA), nil)
		hackDelReq.Header.Set("Authorization", "Bearer "+tokenB)
		hackDelW := httptest.NewRecorder()
		r.ServeHTTP(hackDelW, hackDelReq)
		if hackDelW.Code != http.StatusNotFound {
			t.Errorf("expected 404 Not Found when Account B deletes Account A participant, got %d", hackDelW.Code)
		}
	})
}
