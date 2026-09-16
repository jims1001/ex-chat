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

func TestCoreSecurityAndRBACAlignment(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "core_security_alignment_secret_2026",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()

	engine := router.SetupRouter(cfg, db, hub)

	// Setup initial account and admin user
	account := domain.Account{Name: "Security Org", Locale: "en"}
	db.Create(&account)

	hash, _ := auth.HashPassword("Password123!")
	adminUser := domain.User{
		Name:         "Admin User",
		Email:        "admin@security.org",
		PasswordHash: hash,
		Role:         domain.RoleAdministrator,
		Availability: domain.AvailabilityOnline,
	}
	db.Create(&adminUser)
	db.Create(&domain.AccountUser{
		AccountID:    account.ID,
		UserID:       adminUser.ID,
		Role:         domain.RoleAdministrator,
		Availability: domain.AvailabilityOnline,
	})
	adminToken, _ := auth.GenerateToken(&adminUser, cfg.JWTSecret, 24)

	// Agent 1 (Alice)
	agent1 := domain.User{
		Name:         "Alice Agent",
		Email:        "alice@security.org",
		PasswordHash: hash,
		Role:         domain.RoleAgent,
		Availability: domain.AvailabilityOnline,
	}
	db.Create(&agent1)
	db.Create(&domain.AccountUser{
		AccountID:    account.ID,
		UserID:       agent1.ID,
		Role:         domain.RoleAgent,
		Availability: domain.AvailabilityOnline,
	})
	agent1Token, _ := auth.GenerateToken(&agent1, cfg.JWTSecret, 24)

	// Agent 2 (Bob)
	agent2 := domain.User{
		Name:         "Bob Agent",
		Email:        "bob@security.org",
		PasswordHash: hash,
		Role:         domain.RoleAgent,
		Availability: domain.AvailabilityOnline,
	}
	db.Create(&agent2)
	db.Create(&domain.AccountUser{
		AccountID:    account.ID,
		UserID:       agent2.ID,
		Role:         domain.RoleAgent,
		Availability: domain.AvailabilityOnline,
	})
	agent2Token, _ := auth.GenerateToken(&agent2, cfg.JWTSecret, 24)
	_ = agent2Token

	// Inbox
	inbox := domain.Inbox{
		AccountID:    account.ID,
		Name:         "Support Web",
		ChannelType:  domain.ChannelWebWidget,
		WebsiteToken: "web_token_support_xyz",
	}
	db.Create(&inbox)

	// -------------------------------------------------------------------------
	// 1. Public CSAT Security Hardening Tests
	// -------------------------------------------------------------------------
	t.Run("CSAT_Generic_WebsiteToken_Cannot_Access_Specific_Conversation", func(t *testing.T) {
		conv := domain.Conversation{
			AccountID: account.ID,
			InboxID:   inbox.ID,
			Status:    "resolved",
			DisplayID: 101,
		}
		db.Create(&conv)

		survey := domain.CSATSurvey{
			AccountID:      account.ID,
			ConversationID: conv.ID,
			Rating:         5,
			FeedbackText:   "Secret Feedback",
			ReviewStatus:   "approved",
		}
		db.Create(&survey)

		// 1.1 Unauthenticated numeric ID -> 403 Forbidden
		wUnauth := httptest.NewRecorder()
		reqUnauth, _ := http.NewRequest("GET", fmt.Sprintf("/public/api/v1/csat_survey/%d", conv.ID), nil)
		engine.ServeHTTP(wUnauth, reqUnauth)
		if wUnauth.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for numeric ID access without token, got %d", wUnauth.Code)
		}

		// 1.2 Using generic website_token as query param ?token=web_token_support_xyz -> MUST BE REJECTED 403
		wWebsiteToken := httptest.NewRecorder()
		reqWebsiteToken, _ := http.NewRequest("GET", fmt.Sprintf("/public/api/v1/csat_survey/%d?token=%s", conv.ID, inbox.WebsiteToken), nil)
		engine.ServeHTTP(wWebsiteToken, reqWebsiteToken)
		if wWebsiteToken.Code != http.StatusForbidden {
			t.Fatalf("security violation: expected 403 Forbidden when using generic inbox website_token, got %d", wWebsiteToken.Code)
		}

		// 1.3 Using conversation UUID directly -> 200 OK
		wUUID := httptest.NewRecorder()
		reqUUID, _ := http.NewRequest("GET", "/public/api/v1/csat_survey/"+conv.UUID, nil)
		engine.ServeHTTP(wUUID, reqUUID)
		if wUUID.Code != http.StatusOK {
			t.Fatalf("expected 200 OK using conversation UUID, got %d, body=%s", wUUID.Code, wUUID.Body.String())
		}
	})

	// -------------------------------------------------------------------------
	// 2. Custom Role Standard Permissions & Hierarchy Tests
	// -------------------------------------------------------------------------
	t.Run("Custom_Role_Standard_Permissions_Hierarchy", func(t *testing.T) {
		// Create custom role with standard Chatwoot permissions
		customRoleReq := map[string]any{
			"name":        "Tier2 Specialist",
			"description": "Chatwoot standard role with participating manage and knowledge base",
			"permissions": []string{
				domain.PermissionConversationParticipatingManage,
				domain.PermissionKnowledgeBaseManage,
				domain.PermissionReportManage,
			},
		}
		bodyBytes, _ := json.Marshal(customRoleReq)
		wCreateRole := httptest.NewRecorder()
		reqCreateRole, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/custom_roles", account.ID), bytes.NewBuffer(bodyBytes))
		reqCreateRole.Header.Set("Authorization", "Bearer "+adminToken)
		reqCreateRole.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wCreateRole, reqCreateRole)

		if wCreateRole.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created for custom role creation, got %d, body=%s", wCreateRole.Code, wCreateRole.Body.String())
		}

		// Test RoleMatchesPermission hierarchy
		grantedPerms := []string{
			domain.PermissionConversationManage,
			domain.PermissionKnowledgeBaseManage,
		}

		// conversation_manage should automatically grant unassigned and participating
		if !domain.RoleMatchesPermission(grantedPerms, domain.PermissionConversationUnassignedManage) {
			t.Errorf("expected conversation_manage to match conversation_unassigned_manage")
		}
		if !domain.RoleMatchesPermission(grantedPerms, domain.PermissionConversationParticipatingManage) {
			t.Errorf("expected conversation_manage to match conversation_participating_manage")
		}
		// knowledge_base_manage should match knowledge_view / knowledge_manage
		if !domain.RoleMatchesPermission(grantedPerms, "knowledge_view") {
			t.Errorf("expected knowledge_base_manage to match knowledge_view")
		}
	})

	// -------------------------------------------------------------------------
	// 3. QA Tasks & Appeals Agent Isolation (RBAC)
	// -------------------------------------------------------------------------
	t.Run("QA_Task_And_Appeal_Agent_Isolation", func(t *testing.T) {
		// Create Scorecard
		scorecard := domain.QAScorecard{
			AccountID:    account.ID,
			Name:         "Standard Scorecard",
			TotalScore:   100,
			PassingScore: 80,
		}
		db.Create(&scorecard)

		// Create QA Task for Alice (Agent 1)
		taskAlice := domain.QATask{
			AccountID:   account.ID,
			TaskNumber:  "QA-2026-ALICE",
			TargetType:  "conversation",
			TargetID:    201,
			ScorecardID: scorecard.ID,
			AgentID:     &agent1.ID,
			Status:      "rectifying",
			TotalScore:  60,
		}
		db.Create(&taskAlice)

		// Create QA Task for Bob (Agent 2)
		taskBob := domain.QATask{
			AccountID:   account.ID,
			TaskNumber:  "QA-2026-BOB",
			TargetType:  "conversation",
			TargetID:    202,
			ScorecardID: scorecard.ID,
			AgentID:     &agent2.ID,
			Status:      "completed",
			TotalScore:  90,
		}
		db.Create(&taskBob)

		// Create Appeal by Bob for taskBob
		appealBob := domain.QAAppeal{
			AccountID:    account.ID,
			TaskID:       taskBob.ID,
			AppealNumber: "AP-2026-BOB",
			AppellantID:  agent2.ID,
			Status:       "pending",
			Reason:       "Bob appealing score",
		}
		db.Create(&appealBob)

		// 3.1 Alice lists tasks -> MUST ONLY SEE Alice's tasks, not Bob's
		wListAlice := httptest.NewRecorder()
		reqListAlice, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/qa/tasks", account.ID), nil)
		reqListAlice.Header.Set("Authorization", "Bearer "+agent1Token)
		engine.ServeHTTP(wListAlice, reqListAlice)

		if wListAlice.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for Alice listing QA tasks, got %d, body=%s", wListAlice.Code, wListAlice.Body.String())
		}
		var listResp struct {
			Data []domain.QATask `json:"data"`
		}
		_ = json.Unmarshal(wListAlice.Body.Bytes(), &listResp)
		if len(listResp.Data) != 1 {
			t.Fatalf("expected Alice to only see 1 task (her own), got %d", len(listResp.Data))
		}
		if listResp.Data[0].TaskNumber != "QA-2026-ALICE" {
			t.Fatalf("expected Alice to see QA-2026-ALICE, got %s", listResp.Data[0].TaskNumber)
		}

		// 3.2 Alice attempts to view Bob's task directly by ID -> 403 Forbidden
		wGetBobTask := httptest.NewRecorder()
		reqGetBobTask, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/qa/tasks/%d", account.ID, taskBob.ID), nil)
		reqGetBobTask.Header.Set("Authorization", "Bearer "+agent1Token)
		engine.ServeHTTP(wGetBobTask, reqGetBobTask)

		if wGetBobTask.Code != http.StatusForbidden {
			t.Fatalf("security violation: expected 403 Forbidden for Alice viewing Bob's QA task, got %d", wGetBobTask.Code)
		}

		// 3.3 Alice attempts to list appeals -> MUST NOT see Bob's appeal
		wListAppealsAlice := httptest.NewRecorder()
		reqListAppealsAlice, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/qa/appeals", account.ID), nil)
		reqListAppealsAlice.Header.Set("Authorization", "Bearer "+agent1Token)
		engine.ServeHTTP(wListAppealsAlice, reqListAppealsAlice)

		if wListAppealsAlice.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for Alice listing appeals, got %d", wListAppealsAlice.Code)
		}
		var appealsResp struct {
			Data []domain.QAAppeal `json:"data"`
		}
		_ = json.Unmarshal(wListAppealsAlice.Body.Bytes(), &appealsResp)
		if len(appealsResp.Data) != 0 {
			t.Fatalf("expected Alice to see 0 appeals, got %d", len(appealsResp.Data))
		}

		// 3.4 Alice attempts to view Bob's appeal directly -> 403 Forbidden
		wGetBobAppeal := httptest.NewRecorder()
		reqGetBobAppeal, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/qa/appeals/%d", account.ID, appealBob.ID), nil)
		reqGetBobAppeal.Header.Set("Authorization", "Bearer "+agent1Token)
		engine.ServeHTTP(wGetBobAppeal, reqGetBobAppeal)

		if wGetBobAppeal.Code != http.StatusForbidden {
			t.Fatalf("security violation: expected 403 Forbidden for Alice viewing Bob's appeal, got %d", wGetBobAppeal.Code)
		}

		// 3.5 Admin can view all tasks and appeals
		wAdminTasks := httptest.NewRecorder()
		reqAdminTasks, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/qa/tasks", account.ID), nil)
		reqAdminTasks.Header.Set("Authorization", "Bearer "+adminToken)
		engine.ServeHTTP(wAdminTasks, reqAdminTasks)
		_ = json.Unmarshal(wAdminTasks.Body.Bytes(), &listResp)
		if len(listResp.Data) < 2 {
			t.Fatalf("expected Admin to see all tasks (at least 2), got %d", len(listResp.Data))
		}
	})

	// -------------------------------------------------------------------------
	// 4. Help Center Public Portal Markdown & Sitemap Tests
	// -------------------------------------------------------------------------
	t.Run("HelpCenter_Public_Markdown_And_Sitemap", func(t *testing.T) {
		portal := domain.Portal{
			AccountID: account.ID,
			Name:      "User Documentation",
			Slug:      "user-docs",
			Color:     "#2563eb",
		}
		db.Create(&portal)

		cat := domain.Category{
			PortalID: portal.ID,
			Name:     "Getting Started",
			Slug:     "getting-started",
		}
		db.Create(&cat)

		article := domain.Article{
			PortalID:   portal.ID,
			CategoryID: cat.ID,
			Title:      "Quickstart Guide",
			Slug:       "quickstart-guide",
			Content:    "# Welcome\nThis is a sample markdown article.",
			Status:     "published",
		}
		db.Create(&article)

		// 4.1 Test HTML render
		wHTML := httptest.NewRecorder()
		reqHTML, _ := http.NewRequest("GET", "/hc/user-docs/articles/quickstart-guide", nil)
		engine.ServeHTTP(wHTML, reqHTML)
		if wHTML.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for HTML article render, got %d", wHTML.Code)
		}
		if !bytes.Contains(wHTML.Body.Bytes(), []byte("Quickstart Guide")) {
			t.Fatalf("expected HTML body to contain article title")
		}

		// 4.2 Test Raw Markdown render via .md suffix (Chatwoot spec: show_markdown)
		wMD := httptest.NewRecorder()
		reqMD, _ := http.NewRequest("GET", "/hc/user-docs/articles/quickstart-guide.md", nil)
		engine.ServeHTTP(wMD, reqMD)
		if wMD.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for .md article route, got %d", wMD.Code)
		}
		if wMD.Header().Get("Content-Type") != "text/markdown; charset=utf-8" {
			t.Fatalf("expected Content-Type text/markdown, got: %s", wMD.Header().Get("Content-Type"))
		}
		if !bytes.Contains(wMD.Body.Bytes(), []byte("# Welcome\nThis is a sample markdown article.")) {
			t.Fatalf("expected exact markdown content, got: %s", wMD.Body.String())
		}

		// 4.3 Test Sitemap XML
		wSitemap := httptest.NewRecorder()
		reqSitemap, _ := http.NewRequest("GET", "/hc/user-docs/sitemap.xml", nil)
		engine.ServeHTTP(wSitemap, reqSitemap)
		if wSitemap.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for sitemap.xml, got %d", wSitemap.Code)
		}
		if !bytes.Contains(wSitemap.Body.Bytes(), []byte("urlset")) || !bytes.Contains(wSitemap.Body.Bytes(), []byte("quickstart-guide")) {
			t.Fatalf("expected valid sitemap xml containing article url")
		}
	})
}
