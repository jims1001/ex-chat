package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
)

func TestHistoricalDataMigrationFullResources(t *testing.T) {
	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "migration-full-resources-test-secret-2026",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up admin user
	signUpPayload := map[string]string{
		"account_name": "Migration Test Corp",
		"name":         "Migration Admin",
		"email":        "admin@migration-test.com",
		"password":     "Password123!",
	}
	body, _ := json.Marshal(signUpPayload)
	req := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("sign up failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var authResp struct {
		Data struct {
			Token    string           `json:"token"`
			User     domain.User      `json:"user"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID

	// ----------------------------------------------------
	// Test Scenario 1: Hierarchical Nested Migration (Contact -> Conversation -> Messages -> Attachments)
	// ----------------------------------------------------
	t.Run("HierarchicalNested_FullResources_Migration", func(t *testing.T) {
		nestedPayload := []map[string]any{
			{
				"id":                 "legacy_conv_1001",
				"contact_email":      "bob.nested@example.com",
				"contact_name":       "Bob Nested",
				"contact_identifier": "ext_bob_1001",
				"status":             "resolved",
				"priority":           "urgent",
				"messages": []map[string]any{
					{
						"id":           "legacy_msg_2001",
						"message_type": "incoming",
						"sender_type":  "Contact",
						"content":      "Hello, I have an urgent issue with payment receipt!",
						"private":      false,
						"attachments": []map[string]any{
							{
								"id":        "legacy_att_3001",
								"file_type": "image",
								"data_url":  "https://cdn.example.com/receipt-screenshot.png",
								"file_size": 204850,
							},
						},
					},
					{
						"id":           "legacy_msg_2002",
						"message_type": "outgoing",
						"sender_type":  "User",
						"content":      "We have resolved the payment issue for you.",
						"private":      false,
					},
					{
						"id":           "legacy_msg_2003",
						"message_type": "activity",
						"sender_type":  "User",
						"content":      "Internal note: Bank refund confirmed.",
						"private":      true,
					},
				},
			},
		}

		rawJSON, _ := json.Marshal(nestedPayload)
		migReqPayload := map[string]any{
			"source_system": "legacy_intercom",
			"job_type":      "conversations",
			"raw_data":      string(rawJSON),
		}
		body, _ := json.Marshal(migReqPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/migration_jobs", accountID), bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created for hierarchical migration, got %d body=%s", w.Code, w.Body.String())
		}

		var resp struct {
			Data domain.MigrationJob `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)

		// 1 Contact + 1 Conversation + 3 Messages + 1 Attachment = 6 records
		if resp.Data.SyncedRecords != 6 {
			t.Fatalf("expected 6 synced records for full tree, got %d (logs: %s)", resp.Data.SyncedRecords, resp.Data.Logs)
		}
		if resp.Data.Status != "completed" {
			t.Fatalf("expected status 'completed', got '%s'", resp.Data.Status)
		}

		// Verify Contact in DB
		var contact domain.Contact
		if err := db.Where("account_id = ? AND email = ?", accountID, "bob.nested@example.com").First(&contact).Error; err != nil {
			t.Fatalf("failed to find migrated contact in db: %v", err)
		}
		if contact.Name != "Bob Nested" || contact.Identifier != "ext_bob_1001" {
			t.Fatalf("unexpected contact fields: %+v", contact)
		}

		// Verify Conversation in DB
		var conv domain.Conversation
		if err := db.Where("account_id = ? AND contact_id = ?", accountID, contact.ID).First(&conv).Error; err != nil {
			t.Fatalf("failed to find migrated conversation in db: %v", err)
		}
		if conv.Status != "resolved" || conv.Priority != "urgent" {
			t.Fatalf("unexpected conversation status/priority: status=%s priority=%s", conv.Status, conv.Priority)
		}

		// Verify Messages in DB
		var msgs []domain.Message
		if err := db.Where("account_id = ? AND conversation_id = ?", accountID, conv.ID).Order("id ASC").Find(&msgs).Error; err != nil {
			t.Fatalf("failed to find migrated messages: %v", err)
		}
		if len(msgs) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(msgs))
		}
		if msgs[0].Content != "Hello, I have an urgent issue with payment receipt!" || msgs[0].MessageType != "incoming" {
			t.Fatalf("unexpected msg[0]: %+v", msgs[0])
		}
		if msgs[2].Private != true || msgs[2].Content != "Internal note: Bank refund confirmed." {
			t.Fatalf("unexpected private note msg[2]: %+v", msgs[2])
		}

		// Verify Attachment in DB
		var att domain.Attachment
		if err := db.Where("account_id = ? AND message_id = ?", accountID, msgs[0].ID).First(&att).Error; err != nil {
			t.Fatalf("failed to find migrated attachment: %v", err)
		}
		if att.FileType != "image" || att.DataURL != "https://cdn.example.com/receipt-screenshot.png" || att.FileSize != 204850 {
			t.Fatalf("unexpected attachment fields: %+v", att)
		}
	})

	// ----------------------------------------------------
	// Test Scenario 2: Zero or Invalid Migrated Results -> Status Must Be "failed"
	// ----------------------------------------------------
	t.Run("StatusMachine_ZeroValidResults_ReturnsFailed", func(t *testing.T) {
		// 2.1 MigrationJob with empty array "[]" -> Status must be "failed" (NOT "completed")
		emptyMigPayload := map[string]any{
			"source_system": "legacy_zendesk",
			"job_type":      "conversations",
			"data":          "[]",
		}
		body, _ := json.Marshal(emptyMigPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/migration_jobs", accountID), bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created for empty migration job, got %d body=%s", w.Code, w.Body.String())
		}
		var migResp struct {
			Data domain.MigrationJob `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &migResp)
		if migResp.Data.SyncedRecords != 0 {
			t.Fatalf("expected 0 synced records, got %d", migResp.Data.SyncedRecords)
		}
		if migResp.Data.Status != "failed" {
			t.Fatalf("DEFECT: expected status 'failed' when synced=0, but got '%s'!", migResp.Data.Status)
		}
		if !strings.Contains(migResp.Data.Logs, "No records migrated") {
			t.Fatalf("expected logs to state 'No records migrated', got '%s'", migResp.Data.Logs)
		}

		// 2.2 DataImport with empty items or 0 valid records -> Status must be "failed" (NOT "completed")
		emptyImpPayload := map[string]any{
			"source_provider": "json",
			"import_type":     "contacts",
			"raw_data":        `[{"invalid_field_only": true}]`,
		}
		body, _ = json.Marshal(emptyImpPayload)
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/data_imports", accountID), bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created for data import, got %d body=%s", w.Code, w.Body.String())
		}
		var impResp struct {
			Data domain.DataImport `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &impResp)
		if impResp.Data.ProcessedRecords != 0 {
			t.Fatalf("expected 0 processed records, got %d", impResp.Data.ProcessedRecords)
		}
		if impResp.Data.Status != "failed" {
			t.Fatalf("DEFECT: expected data import status 'failed' when processed=0, but got '%s'!", impResp.Data.Status)
		}
	})

	// ----------------------------------------------------
	// Test Scenario 3: Standalone Conversations Migration
	// ----------------------------------------------------
	t.Run("Standalone_Conversations_Resource_Migration", func(t *testing.T) {
		// First create a contact
		c := domain.Contact{
			AccountID: accountID,
			Name:      "Charlie Standalone",
			Email:     "charlie@standalone.org",
			CreatedAt: time.Now().UTC(),
		}
		db.Create(&c)

		convsData := []map[string]any{
			{
				"id":            "legacy_c_901",
				"contact_email": "charlie@standalone.org",
				"status":        "open",
				"priority":      "high",
			},
		}
		rawJSON, _ := json.Marshal(convsData)
		migReqPayload := map[string]any{
			"source_platform": "custom_crm",
			"resource_type":   "conversations",
			"raw_data":        string(rawJSON),
		}
		body, _ := json.Marshal(migReqPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/migration_jobs", accountID), bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created, got %d body=%s", w.Code, w.Body.String())
		}
		var migResp struct {
			Data domain.MigrationJob `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &migResp)
		if migResp.Data.SyncedRecords != 1 || migResp.Data.Status != "completed" {
			t.Fatalf("expected 1 completed record, got %+v", migResp.Data)
		}

		// Verify conversation linked to Charlie
		var conv domain.Conversation
		if err := db.Where("account_id = ? AND contact_id = ?", accountID, c.ID).First(&conv).Error; err != nil {
			t.Fatalf("failed to find conversation for Charlie: %v", err)
		}
		if conv.Status != "open" || conv.Priority != "high" {
			t.Fatalf("unexpected conv status=%s priority=%s", conv.Status, conv.Priority)
		}
	})

	// ----------------------------------------------------
	// Test Scenario 4: MigrationBundle Top-Level Structure
	// ----------------------------------------------------
	t.Run("MigrationBundle_TopLevel_CrossReference", func(t *testing.T) {
		bundle := map[string]any{
			"contacts": []map[string]any{
				{
					"id":    "bundle_contact_1",
					"name":  "David Bundle",
					"email": "david.bundle@example.com",
				},
			},
			"conversations": []map[string]any{
				{
					"id":         "bundle_conv_1",
					"contact_id": "bundle_contact_1",
					"status":     "snoozed",
					"priority":   "medium",
				},
			},
			"messages": []map[string]any{
				{
					"id":              "bundle_msg_1",
					"conversation_id": "bundle_conv_1",
					"content":         "This is a message from the bundle payload.",
					"message_type":    "incoming",
				},
			},
			"attachments": []map[string]any{
				{
					"id":         "bundle_att_1",
					"message_id": "bundle_msg_1",
					"file_type":  "document",
					"data_url":   "https://cdn.example.com/contract.pdf",
					"file_size":  1048576,
				},
			},
		}

		rawJSON, _ := json.Marshal(bundle)
		migReqPayload := map[string]any{
			"source_system": "full_archive",
			"job_type":      "all",
			"raw_data":      string(rawJSON),
		}
		body, _ := json.Marshal(migReqPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/migration_jobs", accountID), bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created for bundle, got %d body=%s", w.Code, w.Body.String())
		}
		var migResp struct {
			Data domain.MigrationJob `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &migResp)
		if migResp.Data.SyncedRecords != 4 || migResp.Data.Status != "completed" {
			t.Fatalf("expected 4 completed records, got %+v (logs: %s)", migResp.Data, migResp.Data.Logs)
		}

		// Verify database records are properly interconnected
		var contact domain.Contact
		if err := db.Where("account_id = ? AND email = ?", accountID, "david.bundle@example.com").First(&contact).Error; err != nil {
			t.Fatalf("failed to find David Bundle: %v", err)
		}

		var conv domain.Conversation
		if err := db.Where("account_id = ? AND contact_id = ?", accountID, contact.ID).First(&conv).Error; err != nil {
			t.Fatalf("failed to find conversation for David: %v", err)
		}
		if conv.Status != "snoozed" {
			t.Fatalf("expected status snoozed, got %s", conv.Status)
		}

		var msg domain.Message
		if err := db.Where("account_id = ? AND conversation_id = ?", accountID, conv.ID).First(&msg).Error; err != nil {
			t.Fatalf("failed to find message for David conv: %v", err)
		}
		if msg.Content != "This is a message from the bundle payload." {
			t.Fatalf("unexpected message content: %s", msg.Content)
		}

		var att domain.Attachment
		if err := db.Where("account_id = ? AND message_id = ?", accountID, msg.ID).First(&att).Error; err != nil {
			t.Fatalf("failed to find attachment for David msg: %v", err)
		}
		if att.FileType != "document" || att.DataURL != "https://cdn.example.com/contract.pdf" {
			t.Fatalf("unexpected attachment fields: %+v", att)
		}
	})
}
