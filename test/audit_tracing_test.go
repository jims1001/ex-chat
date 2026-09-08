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
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestAuditTracing_FullLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "audit-tracing-secret-key-2026",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()

	engine := router.SetupRouter(cfg, db, hub)

	// 1. Sign up admin
	signUpBody, _ := json.Marshal(map[string]string{
		"name":         "Audit Administrator",
		"email":        "auditor@enterprise.com",
		"password":     "securePassword999",
		"account_name": "Audited Enterprise Workspace",
	})
	wSignUp := httptest.NewRecorder()
	reqSignUp, _ := http.NewRequest("POST", "/auth/sign_up", bytes.NewBuffer(signUpBody))
	reqSignUp.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wSignUp, reqSignUp)

	if wSignUp.Code != http.StatusCreated {
		t.Fatalf("expected 201 for sign up, got %d: %s", wSignUp.Code, wSignUp.Body.String())
	}

	var signUpRes struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wSignUp.Body.Bytes(), &signUpRes)
	token := signUpRes.Data.Token

	// 2. Atomically record business change with LocalChangeJournal (LOG-01, LOG-02, LOG-03)
	journalRepo := repository.NewJournalRepository(db)
	testCorrelationID := "corr-proc-1001"
	testChangeID := "chg-test-conv-001"

	err = journalRepo.RecordChange(db, &domain.LocalChangeJournal{
		ChangeID:            testChangeID,
		SchemaVersion:       "1.0",
		SourceModule:        "CON",
		AccountID:           1,
		ObjectType:          "Conversation",
		ObjectID:            101,
		ObjectVersionBefore: 0,
		ObjectVersionAfter:  1,
		Action:              domain.ActionCreate,
		ChangedFields:       "status,inbox_id,contact_id",
		Diff:                `{"status":{"before":null,"after":"open"}}`,
		ActorType:           "user",
		ActorID:             1,
		ActorAccountRole:    "administrator",
		SourceType:          "account_api",
		CorrelationID:       testCorrelationID,
		OccurredAt:          time.Now().UTC(),
		Result:              "applied",
		DataClassification:  domain.ClassificationInternal,
		RetentionClass:      domain.RetentionBusinessChange,
	})
	if err != nil {
		t.Fatalf("failed to record local change journal: %v", err)
	}

	// Record second change on same object (status change)
	_ = journalRepo.RecordChange(db, &domain.LocalChangeJournal{
		ChangeID:            "chg-test-conv-002",
		SchemaVersion:       "1.0",
		SourceModule:        "CON",
		AccountID:           1,
		ObjectType:          "Conversation",
		ObjectID:            101,
		ObjectVersionBefore: 1,
		ObjectVersionAfter:  2,
		Action:              domain.ActionStatusChange,
		ChangedFields:       "status",
		Diff:                `{"status":{"before":"open","after":"resolved"}}`,
		ActorType:           "user",
		ActorID:             1,
		CorrelationID:       testCorrelationID,
		OccurredAt:          time.Now().UTC().Add(time.Second),
		Result:              "applied",
		DataClassification:  domain.ClassificationInternal,
		RetentionClass:      domain.RetentionBusinessChange,
	})

	// 3. Test GET /api/v1/accounts/1/audit_logs (Chatwoot Compatible Summary)
	wAudit := httptest.NewRecorder()
	reqAudit, _ := http.NewRequest("GET", "/api/v1/accounts/1/audit_logs", nil)
	reqAudit.Header.Set("Authorization", "Bearer "+token)
	engine.ServeHTTP(wAudit, reqAudit)

	if wAudit.Code != http.StatusOK {
		t.Fatalf("expected 200 for audit_logs, got %d: %s", wAudit.Code, wAudit.Body.String())
	}

	var auditRes struct {
		Data struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wAudit.Body.Bytes(), &auditRes)
	if auditRes.Data.Total < 2 {
		t.Fatalf("expected at least 2 audit logs, got %d", auditRes.Data.Total)
	}

	// 4. Test GET /api/v1/accounts/1/data_changes (LOG-05 Section 2.1 Ledger Query)
	wChanges := httptest.NewRecorder()
	reqChanges, _ := http.NewRequest("GET", "/api/v1/accounts/1/data_changes?object_type=Conversation", nil)
	reqChanges.Header.Set("Authorization", "Bearer "+token)
	engine.ServeHTTP(wChanges, reqChanges)

	if wChanges.Code != http.StatusOK {
		t.Fatalf("expected 200 for data_changes, got %d: %s", wChanges.Code, wChanges.Body.String())
	}

	// 5. Test GET /api/v1/accounts/1/data_changes/:change_id (Single change details & hash)
	wSingleChange := httptest.NewRecorder()
	reqSingleChange, _ := http.NewRequest("GET", "/api/v1/accounts/1/data_changes/"+testChangeID, nil)
	reqSingleChange.Header.Set("Authorization", "Bearer "+token)
	engine.ServeHTTP(wSingleChange, reqSingleChange)

	if wSingleChange.Code != http.StatusOK {
		t.Fatalf("expected 200 for single data_change, got %d: %s", wSingleChange.Code, wSingleChange.Body.String())
	}

	var changeDetail struct {
		Data struct {
			ChangeID      string `json:"change_id"`
			IntegrityHash string `json:"integrity_hash"`
			Diff          string `json:"diff"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wSingleChange.Body.Bytes(), &changeDetail)
	if changeDetail.Data.ChangeID != testChangeID {
		t.Fatalf("expected change_id %s, got %s", testChangeID, changeDetail.Data.ChangeID)
	}
	if changeDetail.Data.IntegrityHash == "" {
		t.Fatalf("expected non-empty cryptographic integrity_hash")
	}

	// 6. Test POST /api/v1/accounts/1/data_changes/:change_id/corrections (LOG-02, LOG-05 Append-only Correction)
	corrBody, _ := json.Marshal(map[string]string{
		"corrected_fields": `{"reason":"Customer reopened conversation via phone call"}`,
		"reason":           "Correcting context summary for customer record",
		"evidence_ref":     "ticket-ref-9021",
	})
	wCorr := httptest.NewRecorder()
	reqCorr, _ := http.NewRequest("POST", "/api/v1/accounts/1/data_changes/"+testChangeID+"/corrections", bytes.NewBuffer(corrBody))
	reqCorr.Header.Set("Authorization", "Bearer "+token)
	reqCorr.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wCorr, reqCorr)

	if wCorr.Code != http.StatusCreated {
		t.Fatalf("expected 201 for data change correction, got %d: %s", wCorr.Code, wCorr.Body.String())
	}

	// 7. Test GET /api/v1/accounts/1/audit_objects/:object_type/:object_id/timeline (Object Version Timeline)
	wObjTimeline := httptest.NewRecorder()
	reqObjTimeline, _ := http.NewRequest("GET", "/api/v1/accounts/1/audit_objects/Conversation/101/timeline", nil)
	reqObjTimeline.Header.Set("Authorization", "Bearer "+token)
	engine.ServeHTTP(wObjTimeline, reqObjTimeline)

	if wObjTimeline.Code != http.StatusOK {
		t.Fatalf("expected 200 for object timeline, got %d: %s", wObjTimeline.Code, wObjTimeline.Body.String())
	}

	var timelineRes struct {
		Data []domain.LocalChangeJournal `json:"data"`
	}
	_ = json.Unmarshal(wObjTimeline.Body.Bytes(), &timelineRes)
	if len(timelineRes.Data) < 2 {
		t.Fatalf("expected at least 2 events in object timeline, got %d", len(timelineRes.Data))
	}
	if timelineRes.Data[0].ObjectVersionAfter != 1 || timelineRes.Data[1].ObjectVersionAfter != 2 {
		t.Fatalf("expected ordered object versions 1 and 2, got %d and %d",
			timelineRes.Data[0].ObjectVersionAfter, timelineRes.Data[1].ObjectVersionAfter)
	}

	// 8. Test GET /api/v1/accounts/1/audit_processes/:correlation_id/timeline (Cross-module Process Timeline)
	wProcTimeline := httptest.NewRecorder()
	reqProcTimeline, _ := http.NewRequest("GET", "/api/v1/accounts/1/audit_processes/"+testCorrelationID+"/timeline", nil)
	reqProcTimeline.Header.Set("Authorization", "Bearer "+token)
	engine.ServeHTTP(wProcTimeline, reqProcTimeline)

	if wProcTimeline.Code != http.StatusOK {
		t.Fatalf("expected 200 for process timeline, got %d: %s", wProcTimeline.Code, wProcTimeline.Body.String())
	}

	// 9. Test Security Audit Logs (LOG-01, LOG-05)
	_ = journalRepo.RecordSecurityAudit(&domain.SecurityAuditLog{
		AccountID: 1,
		ActorType: "user",
		ActorID:   1,
		Action:    "mfa_setup",
		Details:   "TOTP authenticator paired successfully",
	})

	wSecAudit := httptest.NewRecorder()
	reqSecAudit, _ := http.NewRequest("GET", "/api/v1/accounts/1/security_audit_logs", nil)
	reqSecAudit.Header.Set("Authorization", "Bearer "+token)
	engine.ServeHTTP(wSecAudit, reqSecAudit)

	if wSecAudit.Code != http.StatusOK {
		t.Fatalf("expected 200 for security audit logs, got %d: %s", wSecAudit.Code, wSecAudit.Body.String())
	}

	// 10. Test POST /api/v1/accounts/1/audit_exports & GET /audit_exports/:id (LOG-05 Async Export)
	exportReqBody, _ := json.Marshal(map[string]string{
		"format":  "json",
		"purpose": "gdpr_compliance_audit",
	})
	wExport := httptest.NewRecorder()
	reqExport, _ := http.NewRequest("POST", "/api/v1/accounts/1/audit_exports", bytes.NewBuffer(exportReqBody))
	reqExport.Header.Set("Authorization", "Bearer "+token)
	reqExport.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wExport, reqExport)

	if wExport.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for audit export, got %d: %s", wExport.Code, wExport.Body.String())
	}

	var exportRes struct {
		Data struct {
			ExportID string `json:"export_id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wExport.Body.Bytes(), &exportRes)

	wGetExport := httptest.NewRecorder()
	reqGetExport, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/1/audit_exports/%s", exportRes.Data.ExportID), nil)
	reqGetExport.Header.Set("Authorization", "Bearer "+token)
	engine.ServeHTTP(wGetExport, reqGetExport)

	if wGetExport.Code != http.StatusOK {
		t.Fatalf("expected 200 for get audit export, got %d: %s", wGetExport.Code, wGetExport.Body.String())
	}

	// 11. Test POST /api/v1/accounts/1/audit_integrity/verifications & GET (LOG-04, LOG-07 Integrity Verification)
	verifReqBody, _ := json.Marshal(map[string]string{
		"partition_id": "account-1-partition",
		"purpose":      "quarterly_regulatory_proof",
	})
	wVerif := httptest.NewRecorder()
	reqVerif, _ := http.NewRequest("POST", "/api/v1/accounts/1/audit_integrity/verifications", bytes.NewBuffer(verifReqBody))
	reqVerif.Header.Set("Authorization", "Bearer "+token)
	reqVerif.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wVerif, reqVerif)

	if wVerif.Code != http.StatusCreated {
		t.Fatalf("expected 201 for integrity verification, got %d: %s", wVerif.Code, wVerif.Body.String())
	}

	var verifRes struct {
		Data struct {
			VerificationID string `json:"verification_id"`
			Verified       bool   `json:"verified"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wVerif.Body.Bytes(), &verifRes)
	if !verifRes.Data.Verified {
		t.Fatalf("expected ledger verification to be verified=true")
	}

	wGetVerif := httptest.NewRecorder()
	reqGetVerif, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/1/audit_integrity/verifications/%s", verifRes.Data.VerificationID), nil)
	reqGetVerif.Header.Set("Authorization", "Bearer "+token)
	engine.ServeHTTP(wGetVerif, reqGetVerif)

	if wGetVerif.Code != http.StatusOK {
		t.Fatalf("expected 200 for get verification, got %d: %s", wGetVerif.Code, wGetVerif.Body.String())
	}
}
