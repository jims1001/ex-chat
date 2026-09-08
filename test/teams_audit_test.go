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
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestTeams_CustomAttributes_And_AuditLogs(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "teams-audit-secret-key-2026",
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
		"name":         "Tech Lead",
		"email":        "lead@company.com",
		"password":     "password123456",
		"account_name": "Global Workspace",
	})
	wSignUp := httptest.NewRecorder()
	reqSignUp, _ := http.NewRequest("POST", "/auth/sign_up", bytes.NewBuffer(signUpBody))
	reqSignUp.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wSignUp, reqSignUp)

	var signUpRes struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wSignUp.Body.Bytes(), &signUpRes)
	token := signUpRes.Data.Token

	// 2. Create Team
	teamBody, _ := json.Marshal(map[string]any{
		"name":        "Tier 2 Escalation",
		"description": "Technical support for complex issues",
	})
	wTeam := httptest.NewRecorder()
	reqTeam, _ := http.NewRequest("POST", "/api/v1/accounts/1/teams", bytes.NewBuffer(teamBody))
	reqTeam.Header.Set("Authorization", "Bearer "+token)
	reqTeam.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wTeam, reqTeam)

	if wTeam.Code != http.StatusCreated {
		t.Fatalf("expected 201 for team creation, got %d: %s", wTeam.Code, wTeam.Body.String())
	}

	// 3. List Teams
	wListTeams := httptest.NewRecorder()
	reqListTeams, _ := http.NewRequest("GET", "/api/v1/accounts/1/teams", nil)
	reqListTeams.Header.Set("Authorization", "Bearer "+token)
	engine.ServeHTTP(wListTeams, reqListTeams)

	if wListTeams.Code != http.StatusOK {
		t.Fatalf("expected 200 for listing teams, got %d", wListTeams.Code)
	}

	// 4. Create Custom Attribute Definition
	attrBody, _ := json.Marshal(map[string]string{
		"attribute_display_name": "VIP Tier",
		"attribute_key":          "vip_tier",
		"attribute_model":        "contact_attribute",
		"attribute_display_type": "text",
		"default_value":          "standard",
	})
	wAttr := httptest.NewRecorder()
	reqAttr, _ := http.NewRequest("POST", "/api/v1/accounts/1/custom_attribute_definitions", bytes.NewBuffer(attrBody))
	reqAttr.Header.Set("Authorization", "Bearer "+token)
	reqAttr.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wAttr, reqAttr)

	if wAttr.Code != http.StatusCreated {
		t.Fatalf("expected 201 for custom attribute, got %d", wAttr.Code)
	}

	// 5. Test Audit Log recording and querying
	journalRepo := repository.NewJournalRepository(db)
	_ = journalRepo.RecordChange(db, &domain.LocalChangeJournal{
		AccountID:  1,
		EntityType: "Team",
		EntityID:   1,
		Action:     domain.ActionCreate,
		ActorType:  "User",
		ActorID:    1,
	})

	wAudit := httptest.NewRecorder()
	reqAudit, _ := http.NewRequest("GET", "/api/v1/accounts/1/audit_logs", nil)
	reqAudit.Header.Set("Authorization", "Bearer "+token)
	engine.ServeHTTP(wAudit, reqAudit)

	if wAudit.Code != http.StatusOK {
		t.Fatalf("expected 200 for audit logs, got %d", wAudit.Code)
	}

	var auditRes struct {
		Data struct {
			Total int `json:"total"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wAudit.Body.Bytes(), &auditRes)
	if auditRes.Data.Total < 1 {
		t.Fatalf("expected at least 1 audit log, got %d", auditRes.Data.Total)
	}
}
