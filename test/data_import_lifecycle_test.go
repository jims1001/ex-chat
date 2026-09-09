package test

import (
	"bytes"
	"encoding/csv"
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
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
)

func TestDataImportLifecycleAndValidation(t *testing.T) {
	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             fmt.Sprintf("file:import_lifecycle_%d?mode=memory&cache=shared", time.Now().UnixNano()),
		JWTSecret:          "import-lifecycle-jwt-secret-2026",
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
		"account_name": "Import Test Corp A",
		"name":         "Import Admin A",
		"email":        "admin@import-test-a.com",
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

	// Seed existing contact in Account A for duplicate testing
	existingContact := domain.Contact{
		AccountID:   accountIDA,
		Name:        "Existing User",
		Email:       "exists@example.com",
		PhoneNumber: "+100000000",
		CreatedAt:   time.Now().UTC(),
	}
	if err := db.Create(&existingContact).Error; err != nil {
		t.Fatalf("failed to seed existing contact: %v", err)
	}

	// Sign up Account B for cross-tenant isolation testing
	signUpPayloadB := map[string]string{
		"account_name": "Import Test Corp B",
		"name":         "Import Admin B",
		"email":        "admin@import-test-b.com",
		"password":     "Password123!",
	}
	bodyB, _ := json.Marshal(signUpPayloadB)
	reqB := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(bodyB))
	reqB.Header.Set("Content-Type", "application/json")
	wB := httptest.NewRecorder()
	r.ServeHTTP(wB, reqB)
	if wB.Code != http.StatusCreated {
		t.Fatalf("sign up account B failed: code=%d body=%s", wB.Code, wB.Body.String())
	}
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
	// Scenario 1: Direct Pre-validation (CSV and JSON)
	// =========================================================================
	t.Run("Direct_Prevalidation_CSV_And_JSON", func(t *testing.T) {
		// 1.1 CSV with 1 valid row, 1 syntax-bad email, 1 in-batch duplicate, 1 db-existing duplicate
		csvContent := "name,email,phone_number\n" +
			"Valid Alpha,alpha@example.com,+111111\n" +
			"Bad Email,invalid-email-address,+222222\n" +
			"Batch Dup,alpha@example.com,+333333\n" +
			"DB Dup,exists@example.com,+444444\n"

		prevalBody, _ := json.Marshal(map[string]any{
			"source_provider": "csv",
			"import_type":     "contacts",
			"raw_data":        csvContent,
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/data_imports/prevalidate", accountIDA), bytes.NewReader(prevalBody))
		req.Header.Set("Authorization", "Bearer "+tokenA)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for CSV prevalidate, got %d body=%s", w.Code, w.Body.String())
		}

		var res struct {
			Data service.ValidationResult `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatalf("failed to unmarshal prevalidate result: %v", err)
		}

		if res.Data.TotalRows != 4 {
			t.Errorf("expected 4 total rows, got %d", res.Data.TotalRows)
		}
		if len(res.Data.Headers) != 3 {
			t.Errorf("expected 3 headers, got %d", len(res.Data.Headers))
		}
		if len(res.Data.Errors) == 0 {
			t.Errorf("expected validation errors for bad email, got none")
		}
		if len(res.Data.Warnings) < 2 {
			t.Errorf("expected at least 2 warnings (batch dup + db dup), got %d", len(res.Data.Warnings))
		}
		if len(res.Data.PreviewRows) == 0 {
			t.Errorf("expected preview rows, got empty")
		}

		// 1.2 JSON Pre-validation
		jsonContent := `[
			{"name": "JSON User 1", "email": "json1@example.com"},
			{"name": "JSON User 2", "email": "invalid_email"}
		]`
		prevalJSONBody, _ := json.Marshal(map[string]any{
			"source_provider": "json",
			"import_type":     "contacts",
			"raw_data":        jsonContent,
		})
		reqJSON := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/data_imports/prevalidate", accountIDA), bytes.NewReader(prevalJSONBody))
		reqJSON.Header.Set("Authorization", "Bearer "+tokenA)
		reqJSON.Header.Set("Content-Type", "application/json")
		wJSON := httptest.NewRecorder()
		r.ServeHTTP(wJSON, reqJSON)

		if wJSON.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for JSON prevalidate, got %d body=%s", wJSON.Code, wJSON.Body.String())
		}
	})

	// =========================================================================
	// Scenario 2: Staging Import Task (auto_start: false) and Task-level Prevalidate
	// =========================================================================
	var stagedImportID uint
	t.Run("Stage_Import_Task_Without_Execution", func(t *testing.T) {
		csvContent := "name,email,phone_number\n" +
			"Staged User 1,staged1@example.com,+1001\n" +
			"Staged User 2,staged2@example.com,+1002\n" +
			"Bad User,not-an-email,+1003\n" +
			"Existing Dup,exists@example.com,+1004\n"

		autoStart := false
		createBody, _ := json.Marshal(map[string]any{
			"source_provider": "csv",
			"import_type":     "contacts",
			"raw_data":        csvContent,
			"auto_start":      &autoStart,
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/data_imports", accountIDA), bytes.NewReader(createBody))
		req.Header.Set("Authorization", "Bearer "+tokenA)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created for staged import, got %d body=%s", w.Code, w.Body.String())
		}

		var createdResp struct {
			Data domain.DataImport `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &createdResp)
		stagedImportID = createdResp.Data.ID

		if stagedImportID == 0 {
			t.Fatalf("expected valid task ID, got 0")
		}
		if createdResp.Data.Status != "staged" && createdResp.Data.Status != "validated" {
			t.Errorf("expected status 'staged' or 'validated', got '%s'", createdResp.Data.Status)
		}
		if createdResp.Data.ProcessedRecords != 0 {
			t.Errorf("expected 0 processed records for staged task, got %d", createdResp.Data.ProcessedRecords)
		}

		// Verify that contacts were NOT imported into DB yet
		var count int64
		db.Model(&domain.Contact{}).Where("account_id = ? AND email = ?", accountIDA, "staged1@example.com").Count(&count)
		if count != 0 {
			t.Fatalf("staged contact should NOT be created before explicit start")
		}

		// Prevalidate by Task ID
		taskPrevalReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/data_imports/%d/prevalidate", accountIDA, stagedImportID), nil)
		taskPrevalReq.Header.Set("Authorization", "Bearer "+tokenA)
		taskPrevalW := httptest.NewRecorder()
		r.ServeHTTP(taskPrevalW, taskPrevalReq)
		if taskPrevalW.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for task prevalidate, got %d body=%s", taskPrevalW.Code, taskPrevalW.Body.String())
		}
	})

	// =========================================================================
	// Scenario 3: Independent Start Execution & Verify Records
	// =========================================================================
	t.Run("Independent_Start_Execution_And_Downloads", func(t *testing.T) {
		startReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/data_imports/%d/start", accountIDA, stagedImportID), nil)
		startReq.Header.Set("Authorization", "Bearer "+tokenA)
		startW := httptest.NewRecorder()
		r.ServeHTTP(startW, startReq)

		if startW.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for starting import task, got %d body=%s", startW.Code, startW.Body.String())
		}

		var runResp struct {
			Data domain.DataImport `json:"data"`
		}
		_ = json.Unmarshal(startW.Body.Bytes(), &runResp)
		if runResp.Data.Status != "completed" {
			t.Errorf("expected status 'completed', got '%s'", runResp.Data.Status)
		}
		if runResp.Data.ProcessedRecords != 2 {
			t.Errorf("expected 2 processed records, got %d", runResp.Data.ProcessedRecords)
		}
		if runResp.Data.FailedRecords != 1 {
			t.Errorf("expected 1 failed record (bad email), got %d", runResp.Data.FailedRecords)
		}
		if runResp.Data.SkippedRecords != 1 {
			t.Errorf("expected 1 skipped record (db duplicate), got %d", runResp.Data.SkippedRecords)
		}

		// Verify contacts are now in DB
		var c1 domain.Contact
		if err := db.Where("account_id = ? AND email = ?", accountIDA, "staged1@example.com").First(&c1).Error; err != nil {
			t.Errorf("staged contact 1 was not persisted: %v", err)
		}

		// Calling start again should fail
		repeatStartReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/data_imports/%d/start", accountIDA, stagedImportID), nil)
		repeatStartReq.Header.Set("Authorization", "Bearer "+tokenA)
		repeatStartW := httptest.NewRecorder()
		r.ServeHTTP(repeatStartW, repeatStartReq)
		if repeatStartW.Code != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request when restarting completed task, got %d", repeatStartW.Code)
		}

		// 3.1 Get Errors JSON
		errReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/data_imports/%d/errors", accountIDA, stagedImportID), nil)
		errReq.Header.Set("Authorization", "Bearer "+tokenA)
		errW := httptest.NewRecorder()
		r.ServeHTTP(errW, errReq)
		if errW.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for errors endpoint, got %d", errW.Code)
		}

		// 3.2 Download Errors CSV
		dlErrReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/data_imports/%d/errors/download", accountIDA, stagedImportID), nil)
		dlErrReq.Header.Set("Authorization", "Bearer "+tokenA)
		dlErrW := httptest.NewRecorder()
		r.ServeHTTP(dlErrW, dlErrReq)
		if dlErrW.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for download errors CSV, got %d body=%s", dlErrW.Code, dlErrW.Body.String())
		}
		if !strings.Contains(dlErrW.Header().Get("Content-Type"), "text/csv") {
			t.Errorf("expected text/csv Content-Type, got '%s'", dlErrW.Header().Get("Content-Type"))
		}
		if !strings.Contains(dlErrW.Header().Get("Content-Disposition"), "attachment") {
			t.Errorf("expected attachment Content-Disposition, got '%s'", dlErrW.Header().Get("Content-Disposition"))
		}
		// Parse CSV output
		errCSVReader := csv.NewReader(strings.NewReader(dlErrW.Body.String()))
		errRows, err := errCSVReader.ReadAll()
		if err != nil {
			t.Fatalf("failed to parse downloaded errors CSV: %v", err)
		}
		if len(errRows) < 2 { // Header + 1 row
			t.Fatalf("expected at least 2 rows in errors CSV, got %d", len(errRows))
		}

		// 3.3 Get Skipped JSON
		skipReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/data_imports/%d/skipped", accountIDA, stagedImportID), nil)
		skipReq.Header.Set("Authorization", "Bearer "+tokenA)
		skipW := httptest.NewRecorder()
		r.ServeHTTP(skipW, skipReq)
		if skipW.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for skipped endpoint, got %d", skipW.Code)
		}

		// 3.4 Download Skipped CSV
		dlSkipReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/data_imports/%d/skipped/download", accountIDA, stagedImportID), nil)
		dlSkipReq.Header.Set("Authorization", "Bearer "+tokenA)
		dlSkipW := httptest.NewRecorder()
		r.ServeHTTP(dlSkipW, dlSkipReq)
		if dlSkipW.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for download skipped CSV, got %d body=%s", dlSkipW.Code, dlSkipW.Body.String())
		}
		skipCSVReader := csv.NewReader(strings.NewReader(dlSkipW.Body.String()))
		skipRows, err := skipCSVReader.ReadAll()
		if err != nil {
			t.Fatalf("failed to parse downloaded skipped CSV: %v", err)
		}
		if len(skipRows) < 2 { // Header + 1 row
			t.Fatalf("expected at least 2 rows in skipped CSV, got %d", len(skipRows))
		}
	})

	// =========================================================================
	// Scenario 4: Independent Discard / Cancel Task
	// =========================================================================
	t.Run("Independent_Discard_Task", func(t *testing.T) {
		autoStart := false
		createBody, _ := json.Marshal(map[string]any{
			"source_provider": "csv",
			"import_type":     "contacts",
			"raw_data":        "name,email\nDiscard User,discard@example.com",
			"auto_start":      &autoStart,
		})
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/data_imports", accountIDA), bytes.NewReader(createBody))
		req.Header.Set("Authorization", "Bearer "+tokenA)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		var createdResp struct {
			Data domain.DataImport `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &createdResp)
		taskToDiscardID := createdResp.Data.ID

		// Discard task
		discardReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/data_imports/%d/discard", accountIDA, taskToDiscardID), nil)
		discardReq.Header.Set("Authorization", "Bearer "+tokenA)
		discardW := httptest.NewRecorder()
		r.ServeHTTP(discardW, discardReq)

		if discardW.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for discard, got %d body=%s", discardW.Code, discardW.Body.String())
		}

		var discardResp struct {
			Data domain.DataImport `json:"data"`
		}
		_ = json.Unmarshal(discardW.Body.Bytes(), &discardResp)
		if discardResp.Data.Status != "discarded" {
			t.Errorf("expected status 'discarded', got '%s'", discardResp.Data.Status)
		}

		// Starting a discarded task should fail
		startReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/data_imports/%d/start", accountIDA, taskToDiscardID), nil)
		startReq.Header.Set("Authorization", "Bearer "+tokenA)
		startW := httptest.NewRecorder()
		r.ServeHTTP(startW, startReq)
		if startW.Code != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request when starting discarded task, got %d", startW.Code)
		}
	})

	// =========================================================================
	// Scenario 5: Multi-tenant Security Isolation
	// =========================================================================
	t.Run("Multi_Tenant_Security_Isolation", func(t *testing.T) {
		// Account B attempts to start Account A's task
		hackStartReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/data_imports/%d/start", accountIDB, stagedImportID), nil)
		hackStartReq.Header.Set("Authorization", "Bearer "+tokenB)
		hackStartW := httptest.NewRecorder()
		r.ServeHTTP(hackStartW, hackStartReq)
		if hackStartW.Code != http.StatusNotFound {
			t.Errorf("expected 404 Not Found when Account B accesses Account A import, got %d", hackStartW.Code)
		}

		// Account B attempts to download Account A's error rows
		hackErrReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/data_imports/%d/errors/download", accountIDB, stagedImportID), nil)
		hackErrReq.Header.Set("Authorization", "Bearer "+tokenB)
		hackErrW := httptest.NewRecorder()
		r.ServeHTTP(hackErrW, hackErrReq)
		if hackErrW.Code != http.StatusNotFound {
			t.Errorf("expected 404 Not Found when Account B downloads Account A errors, got %d", hackErrW.Code)
		}
	})
}
