package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestAllSystemGapsCovered(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "system_gaps_jwt_secret_key_32bytes!",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// Admin Setup
	signUpBody, _ := json.Marshal(map[string]string{
		"name":         "Gap Auditor",
		"email":        "auditor@example.com",
		"password":     "Password123!",
		"account_name": "Full Coverage Org",
	})
	req := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(signUpBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("sign up failed: code=%d, body=%s", w.Code, w.Body.String())
	}

	var authResp struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accID := authResp.Data.Accounts[0].ID
	accStr := strconv.FormatUint(uint64(accID), 10)

	authReq := func(method, url string, body any) *httptest.ResponseRecorder {
		var reqBody *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reqBody = bytes.NewReader(b)
		} else {
			reqBody = bytes.NewReader([]byte{})
		}
		rq := httptest.NewRequest(method, url, reqBody)
		rq.Header.Set("Authorization", "Bearer "+token)
		rq.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, rq)
		return rec
	}

	// -------------------------------------------------------------
	// 1. 客户数据导入 (CSV & JSON parsing and real contact upsert)
	// -------------------------------------------------------------
	t.Run("Item 1: Customer Data Import", func(t *testing.T) {
		csvData := "name,email,phone_number\nAlice Green,alice.green@example.com,+123456789\nBob Blue,bob.blue@example.com,+987654321"
		rec := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/data_imports", map[string]string{
			"import_type": "contacts_csv",
			"file_format": "csv",
			"raw_data":    csvData,
		})
		if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
			t.Fatalf("CSV import failed: code=%d, body=%s", rec.Code, rec.Body.String())
		}
		var impResp struct {
			Data domain.DataImport `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &impResp)
		if impResp.Data.TotalRecords != 2 || impResp.Data.ProcessedRecords != 2 {
			t.Errorf("expected 2 processed records, got total=%d processed=%d", impResp.Data.TotalRecords, impResp.Data.ProcessedRecords)
		}

		// Verify contact in database
		var contact domain.Contact
		if err := db.Where("account_id = ? AND email = ?", accID, "alice.green@example.com").First(&contact).Error; err != nil {
			t.Errorf("failed to find imported contact: %v", err)
		}
	})

	// -------------------------------------------------------------
	// 2. 历史数据迁移 (Migration job with dynamic synced count)
	// -------------------------------------------------------------
	t.Run("Item 2: Historical Data Migration", func(t *testing.T) {
		records := []map[string]any{
			{"name": "Migrated One", "email": "mig1@example.com"},
			{"name": "Migrated Two", "email": "mig2@example.com"},
			{"name": "Migrated Three", "email": "mig3@example.com"},
		}
		rawJSON, _ := json.Marshal(records)
		rec := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/migration_jobs", map[string]string{
			"source_platform": "zendesk",
			"resource_type":   "contacts",
			"raw_data":        string(rawJSON),
		})
		if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
			t.Fatalf("Migration job failed: code=%d, body=%s", rec.Code, rec.Body.String())
		}
		var migResp struct {
			Data domain.MigrationJob `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &migResp)
		if migResp.Data.SyncedRecords != 3 {
			t.Errorf("expected 3 synced records, got %d", migResp.Data.SyncedRecords)
		}
	})

	// -------------------------------------------------------------
	// 3. 邮件渠道迁移 (Channel creation & cutover)
	// -------------------------------------------------------------
	t.Run("Item 3: Email Channel Migration", func(t *testing.T) {
		// Create source inbox
		sourceInbox := domain.Inbox{
			AccountID:    accID,
			Name:         "Old Support Inbox",
			ChannelType:  "Channel::Email",
			WebsiteToken: "old_token_123",
		}
		db.Create(&sourceInbox)

		// Create conversation in old inbox
		conv := domain.Conversation{
			AccountID: accID,
			InboxID:   sourceInbox.ID,
			ContactID: 1,
			Status:    domain.ConversationStatusOpen,
		}
		db.Create(&conv)

		rec := authReq(http.MethodPost, "/enterprise/api/v1/accounts/"+accStr+"/email_channel_migration", map[string]any{
			"source_inbox_id": sourceInbox.ID,
			"new_inbox_name":  "Modern Migrated Email",
			"email":           "migrated@company.com",
			"cutover_now":     true,
		})
		if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
			t.Fatalf("Email migration failed: code=%d, body=%s", rec.Code, rec.Body.String())
		}

		// Verify conversation moved to new inbox
		var updatedConv domain.Conversation
		db.First(&updatedConv, conv.ID)
		if updatedConv.InboxID == sourceInbox.ID {
			t.Errorf("expected conversation to be cut over from inbox %d", sourceInbox.ID)
		}
	})

	// -------------------------------------------------------------
	// 4. 营销受众筛选 (Audience filter in TriggerCampaign)
	// -------------------------------------------------------------
	t.Run("Item 4: Campaign Audience Filter", func(t *testing.T) {
		// Create 2 contacts: 1 VIP, 1 Regular
		vipContact := domain.Contact{AccountID: accID, Name: "VIP User", Email: "vip@example.com"}
		regularContact := domain.Contact{AccountID: accID, Name: "Regular User", Email: "reg@example.com"}
		db.Create(&vipContact)
		db.Create(&regularContact)

		convRepo := repository.NewConversationRepository(db)
		msgRepo := repository.NewMessageRepository(db)
		contactRepo := repository.NewContactRepository(db)
		campService := service.NewCampaignService(db, convRepo, msgRepo, contactRepo)

		camp := domain.Campaign{
			AccountID: accID,
			InboxID:   1,
			Title:     "VIP Promo",
			Message:   "Special discount for VIP!",
			Audience:  fmt.Sprintf(`{"contact_ids": [%d]}`, vipContact.ID),
		}
		db.Create(&camp)

		sentCount, err := campService.TriggerCampaign(accID, camp.ID)
		if err != nil {
			t.Fatalf("TriggerCampaign failed: %v", err)
		}
		if sentCount != 1 {
			t.Errorf("expected exactly 1 audience contact to receive campaign, got %d", sentCount)
		}
	})

	// -------------------------------------------------------------
	// 5. 营销定时设置 (scheduled_at in CreateCampaign)
	// -------------------------------------------------------------
	t.Run("Item 5: Campaign Schedule Time", func(t *testing.T) {
		futureTime := time.Now().UTC().Add(48 * time.Hour)
		rec := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/campaigns", map[string]any{
			"inbox_id":     1,
			"title":        "Scheduled Campaign",
			"message":      "See you in 48 hours!",
			"scheduled_at": futureTime.Format(time.RFC3339),
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("Create campaign failed: code=%d, body=%s", rec.Code, rec.Body.String())
		}
		var campResp struct {
			Data domain.Campaign `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &campResp)
		if campResp.Data.ScheduledAt == nil {
			t.Fatal("scheduled_at was not saved")
		}
	})

	// -------------------------------------------------------------
	// 6. 会话稍后处理 (ResumeSnoozedConversations)
	// -------------------------------------------------------------
	t.Run("Item 6: Snooze Auto-Resume", func(t *testing.T) {
		convRepo := repository.NewConversationRepository(db)
		routingService := service.NewRoutingService(db, convRepo, hub)

		pastTime := time.Now().UTC().Add(-10 * time.Minute)
		snoozedConv := domain.Conversation{
			AccountID:    accID,
			InboxID:      1,
			ContactID:    1,
			Status:       domain.ConversationStatusSnoozed,
			SnoozedUntil: &pastTime,
		}
		db.Create(&snoozedConv)

		resumed, err := routingService.ResumeSnoozedConversations(context.Background())
		if err != nil {
			t.Fatalf("ResumeSnoozedConversations failed: %v", err)
		}
		if len(resumed) == 0 {
			t.Fatal("expected at least 1 resumed conversation")
		}

		var checkConv domain.Conversation
		db.First(&checkConv, snoozedConv.ID)
		if checkConv.Status != domain.ConversationStatusOpen || checkConv.SnoozedUntil != nil {
			t.Errorf("conversation status is %s, snoozed_until is %v", checkConv.Status, checkConv.SnoozedUntil)
		}
	})

	// -------------------------------------------------------------
	// 7. 工作时间 (IsWithinWorkingHours & Out of Office)
	// -------------------------------------------------------------
	t.Run("Item 7: Working Hours & Out Of Office", func(t *testing.T) {
		convRepo := repository.NewConversationRepository(db)
		routingService := service.NewRoutingService(db, convRepo, hub)

		// Monday-Friday 09:00 - 18:00
		workingHoursJSON := `[
			{"day_of_week": 1, "open_hour": 9, "open_minute": 0, "close_hour": 18, "close_minute": 0, "closed": false},
			{"day_of_week": 0, "closed": true}
		]`

		inbox := &domain.Inbox{
			WorkingHoursEnabled: true,
			Timezone:            "UTC",
			WorkingHours:        workingHoursJSON,
			OutOfOfficeMessage:  "We are currently closed.",
		}

		// Sunday
		sundayTime := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC) // Sunday
		if routingService.IsWithinWorkingHours(inbox, sundayTime) {
			t.Error("expected Sunday to be outside working hours")
		}

		// Monday 10:30 AM
		mondayTime := time.Date(2026, 9, 7, 10, 30, 0, 0, time.UTC) // Monday
		if !routingService.IsWithinWorkingHours(inbox, mondayTime) {
			t.Error("expected Monday 10:30 to be within working hours")
		}
	})

	// -------------------------------------------------------------
	// 8. 高级报表 (Trends, Teams, Inboxes, Labels, First Response, CSV)
	// -------------------------------------------------------------
	t.Run("Item 8: Advanced Reports & CSV Export", func(t *testing.T) {
		recTrends := authReq(http.MethodGet, "/api/v2/accounts/"+accStr+"/reports/trends?days=7", nil)
		if recTrends.Code != http.StatusOK {
			t.Errorf("trends report failed: %d", recTrends.Code)
		}

		recTeams := authReq(http.MethodGet, "/api/v2/accounts/"+accStr+"/reports/teams", nil)
		if recTeams.Code != http.StatusOK {
			t.Errorf("teams report failed: %d", recTeams.Code)
		}

		recInboxes := authReq(http.MethodGet, "/api/v2/accounts/"+accStr+"/reports/inboxes", nil)
		if recInboxes.Code != http.StatusOK {
			t.Errorf("inboxes report failed: %d", recInboxes.Code)
		}

		recLabels := authReq(http.MethodGet, "/api/v2/accounts/"+accStr+"/reports/labels", nil)
		if recLabels.Code != http.StatusOK {
			t.Errorf("labels report failed: %d", recLabels.Code)
		}

		recFirstResp := authReq(http.MethodGet, "/api/v2/accounts/"+accStr+"/reports/first_response", nil)
		if recFirstResp.Code != http.StatusOK {
			t.Errorf("first response report failed: %d", recFirstResp.Code)
		}

		recCSV := authReq(http.MethodGet, "/api/v2/accounts/"+accStr+"/reports/conversations/export", nil)
		if recCSV.Code != http.StatusOK {
			t.Errorf("CSV export failed: %d", recCSV.Code)
		}
		if !strings.Contains(recCSV.Header().Get("Content-Type"), "text/csv") {
			t.Errorf("expected text/csv header, got %s", recCSV.Header().Get("Content-Type"))
		}
		if !strings.Contains(recCSV.Body.String(), "DisplayID") {
			t.Errorf("expected CSV header, got %s", recCSV.Body.String())
		}
	})

	// -------------------------------------------------------------
	// 9. 帮助中心编辑管理 (Portal, Category, Article CRUD)
	// -------------------------------------------------------------
	t.Run("Item 9: Help Center CRUD", func(t *testing.T) {
		// 1. Create Portal
		recP := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/portals", map[string]string{
			"name": "Help Desk",
			"slug": "help-desk-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		})
		if recP.Code != http.StatusCreated {
			t.Fatalf("Create portal failed: %d", recP.Code)
		}
		var pResp struct {
			Data domain.Portal `json:"data"`
		}
		_ = json.Unmarshal(recP.Body.Bytes(), &pResp)
		portalID := strconv.FormatUint(uint64(pResp.Data.ID), 10)

		// 2. Update Portal
		recPUp := authReq(http.MethodPut, "/api/v1/accounts/"+accStr+"/portals/"+portalID, map[string]string{
			"name": "Updated Help Desk",
		})
		if recPUp.Code != http.StatusOK {
			t.Fatalf("Update portal failed: %d", recPUp.Code)
		}

		// 3. Create Category
		recC := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/portals/"+portalID+"/categories", map[string]any{
			"name": "Billing FAQ",
			"slug": "billing-faq",
		})
		if recC.Code != http.StatusCreated {
			t.Fatalf("Create category failed: %d", recC.Code)
		}
		var cResp struct {
			Data domain.Category `json:"data"`
		}
		_ = json.Unmarshal(recC.Body.Bytes(), &cResp)
		catID := strconv.FormatUint(uint64(cResp.Data.ID), 10)

		// 4. Update Category
		recCUp := authReq(http.MethodPut, "/api/v1/accounts/"+accStr+"/portals/"+portalID+"/categories/"+catID, map[string]any{
			"name": "Updated Billing FAQ",
		})
		if recCUp.Code != http.StatusOK {
			t.Fatalf("Update category failed: %d", recCUp.Code)
		}

		// 5. Create Article
		recA := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/portals/"+portalID+"/articles", map[string]any{
			"category_id": cResp.Data.ID,
			"title":       "How to Refund",
			"slug":        "how-to-refund",
			"content":     "Full details on processing refund...",
			"status":      "published",
		})
		if recA.Code != http.StatusCreated {
			t.Fatalf("Create article failed: %d", recA.Code)
		}
		var aResp struct {
			Data domain.Article `json:"data"`
		}
		_ = json.Unmarshal(recA.Body.Bytes(), &aResp)
		artID := strconv.FormatUint(uint64(aResp.Data.ID), 10)

		// 6. Update Article Full Text
		recAUp := authReq(http.MethodPut, "/api/v1/accounts/"+accStr+"/portals/"+portalID+"/articles/"+artID, map[string]any{
			"title":   "How to Refund - Updated",
			"content": "New policy updated content...",
			"status":  "published",
		})
		if recAUp.Code != http.StatusOK {
			t.Fatalf("Update article failed: %d", recAUp.Code)
		}

		// 7. Get Article
		recAGet := authReq(http.MethodGet, "/api/v1/accounts/"+accStr+"/portals/"+portalID+"/articles/"+artID, nil)
		if recAGet.Code != http.StatusOK {
			t.Fatalf("Get article failed: %d", recAGet.Code)
		}

		// 8. Delete Article
		recADel := authReq(http.MethodDelete, "/api/v1/accounts/"+accStr+"/portals/"+portalID+"/articles/"+artID, nil)
		if recADel.Code != http.StatusOK {
			t.Fatalf("Delete article failed: %d", recADel.Code)
		}
	})

	// -------------------------------------------------------------
	// 10. 自定义角色 (Custom Role CRUD)
	// -------------------------------------------------------------
	t.Run("Item 10: Custom Roles", func(t *testing.T) {
		recCreate := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/custom_roles", map[string]any{
			"name":        "Tier2 Support",
			"description": "Escalation team",
			"permissions": `["conversation_manage", "report_view"]`,
		})
		if recCreate.Code != http.StatusCreated {
			t.Fatalf("Create custom role failed: %d", recCreate.Code)
		}
		var roleResp struct {
			Data domain.CustomRole `json:"data"`
		}
		_ = json.Unmarshal(recCreate.Body.Bytes(), &roleResp)
		roleID := strconv.FormatUint(uint64(roleResp.Data.ID), 10)

		// Update
		recUpdate := authReq(http.MethodPut, "/api/v1/accounts/"+accStr+"/custom_roles/"+roleID, map[string]any{
			"description": "Updated escalation team",
		})
		if recUpdate.Code != http.StatusOK {
			t.Fatalf("Update custom role failed: %d", recUpdate.Code)
		}

		// List
		recList := authReq(http.MethodGet, "/api/v1/accounts/"+accStr+"/custom_roles", nil)
		if recList.Code != http.StatusOK {
			t.Fatalf("List custom roles failed: %d", recList.Code)
		}

		// Delete
		recDel := authReq(http.MethodDelete, "/api/v1/accounts/"+accStr+"/custom_roles/"+roleID, nil)
		if recDel.Code != http.StatusOK {
			t.Fatalf("Delete custom role failed: %d", recDel.Code)
		}
	})

	// -------------------------------------------------------------
	// 11. 成员维护 (Update & Remove Agent)
	// -------------------------------------------------------------
	t.Run("Item 11: Agent Maintenance", func(t *testing.T) {
		// Add agent
		recAdd := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/agents", map[string]string{
			"name":  "Temp Agent",
			"email": "tempagent@example.com",
			"role":  "agent",
		})
		if recAdd.Code != http.StatusCreated {
			t.Fatalf("Add agent failed: %d", recAdd.Code)
		}
		var addResp struct {
			Data domain.AccountUser `json:"data"`
		}
		_ = json.Unmarshal(recAdd.Body.Bytes(), &addResp)
		agentID := strconv.FormatUint(uint64(addResp.Data.UserID), 10)

		// Update agent
		recUp := authReq(http.MethodPut, "/api/v1/accounts/"+accStr+"/agents/"+agentID, map[string]string{
			"name": "Updated Agent Name",
			"role": "administrator",
		})
		if recUp.Code != http.StatusOK {
			t.Fatalf("Update agent failed: %d", recUp.Code)
		}

		// Remove agent
		recDel := authReq(http.MethodDelete, "/api/v1/accounts/"+accStr+"/agents/"+agentID, nil)
		if recDel.Code != http.StatusOK {
			t.Fatalf("Remove agent failed: %d", recDel.Code)
		}
	})

	// -------------------------------------------------------------
	// 12. 公司维护 (Update & Delete Company)
	// -------------------------------------------------------------
	t.Run("Item 12: Company Maintenance", func(t *testing.T) {
		recCreate := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/companies", map[string]string{
			"name":     "Initial Corp",
			"domain":   "initial.com",
			"industry": "Tech",
		})
		if recCreate.Code != http.StatusCreated {
			t.Fatalf("Create company failed: %d", recCreate.Code)
		}
		var compResp struct {
			Data domain.Company `json:"data"`
		}
		_ = json.Unmarshal(recCreate.Body.Bytes(), &compResp)
		compID := strconv.FormatUint(uint64(compResp.Data.ID), 10)

		// Update
		recUp := authReq(http.MethodPut, "/api/v1/accounts/"+accStr+"/companies/"+compID, map[string]string{
			"name":   "Updated Corp Inc",
			"domain": "updated.com",
		})
		if recUp.Code != http.StatusOK {
			t.Fatalf("Update company failed: %d", recUp.Code)
		}

		// Delete
		recDel := authReq(http.MethodDelete, "/api/v1/accounts/"+accStr+"/companies/"+compID, nil)
		if recDel.Code != http.StatusOK {
			t.Fatalf("Delete company failed: %d", recDel.Code)
		}
	})

	// -------------------------------------------------------------
	// 13. 宏编辑 (Update Macro)
	// -------------------------------------------------------------
	t.Run("Item 13: Macro Update", func(t *testing.T) {
		recCreate := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/macros", map[string]any{
			"name":    "Close Conversation",
			"actions": `[{"action_name":"resolve_conversation","action_params":[]}]`,
		})
		if recCreate.Code != http.StatusCreated {
			t.Fatalf("Create macro failed: %d", recCreate.Code)
		}
		var mResp struct {
			Data domain.Macro `json:"data"`
		}
		_ = json.Unmarshal(recCreate.Body.Bytes(), &mResp)
		macroID := strconv.FormatUint(uint64(mResp.Data.ID), 10)

		// Update
		recUp := authReq(http.MethodPut, "/api/v1/accounts/"+accStr+"/macros/"+macroID, map[string]any{
			"name": "Resolve & Tag",
		})
		if recUp.Code != http.StatusOK {
			t.Fatalf("Update macro failed: %d", recUp.Code)
		}
	})

	// -------------------------------------------------------------
	// 14. AI 模型接入 (AI Providers & Completion)
	// -------------------------------------------------------------
	t.Run("Item 14: AI Model Providers", func(t *testing.T) {
		// Test local heuristic
		localProv := service.GetAIProvider("local-heuristic", "", "")
		respL, err := localProv.GenerateCompletion(context.Background(), service.AICompletionRequest{
			Messages: []service.AIMessage{{Role: "user", Content: "订单物流如何查询？"}},
		})
		if err != nil || respL == nil {
			t.Fatalf("Local heuristic provider failed: %v", err)
		}

		// Test OpenAI adapter
		recComp := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/copilot/completions", map[string]string{
			"provider": "openai",
			"model":    "gpt-4o",
			"prompt":   "帮我起草对客户的感谢回复",
		})
		if recComp.Code != http.StatusOK {
			t.Fatalf("AI completion endpoint failed: %d", recComp.Code)
		}
	})

	// -------------------------------------------------------------
	// 15. AI 知识处理 (Document Chunking & Search)
	// -------------------------------------------------------------
	t.Run("Item 15: Knowledge Chunking & Search", func(t *testing.T) {
		longText := strings.Repeat("客服服务指南与标准操作规范。涵盖售后退款、退换货以及物流追踪方案。", 25)
		recDoc := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/captain/knowledge_docs", map[string]string{
			"title":   "Customer Service Manual",
			"content": longText,
		})
		if recDoc.Code != http.StatusCreated {
			t.Fatalf("Create knowledge doc failed: %d", recDoc.Code)
		}
		var docResp struct {
			Data domain.CaptainKnowledgeDoc `json:"data"`
		}
		_ = json.Unmarshal(recDoc.Body.Bytes(), &docResp)
		if docResp.Data.ChunkCount <= 1 || docResp.Data.Status != "ready" {
			t.Errorf("expected multiple chunks and ready status, got count=%d status=%s", docResp.Data.ChunkCount, docResp.Data.Status)
		}

		// Search chunks
		recSearch := authReq(http.MethodGet, "/api/v1/accounts/"+accStr+"/captain/knowledge_chunks?q=退换货", nil)
		if recSearch.Code != http.StatusOK {
			t.Fatalf("Search knowledge chunks failed: %d", recSearch.Code)
		}
	})

	// -------------------------------------------------------------
	// 16. AI 场景与工具 (Scenario CRUD & Tool Quota)
	// -------------------------------------------------------------
	t.Run("Item 16: AI Scenarios & Tools Quota", func(t *testing.T) {
		// Scenario CRUD
		recSc := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/captain/scenarios", map[string]any{
			"name":          "Order Support Bot",
			"description":   "Handles ecommerce inquiries",
			"system_prompt": "You are an order tracking bot.",
			"allowed_tools": `["order_lookup", "kb_search"]`,
		})
		if recSc.Code != http.StatusCreated {
			t.Fatalf("Create scenario failed: %d", recSc.Code)
		}

		// Execute Tool & Quota Check
		recTool := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/captain/tools/execute", map[string]any{
			"tool_name": "order_lookup",
			"params":    map[string]any{"order_id": "ORD-12345"},
		})
		if recTool.Code != http.StatusOK {
			t.Fatalf("Execute AI tool failed: %d, body=%s", recTool.Code, recTool.Body.String())
		}

		// Verify Quota
		recQuota := authReq(http.MethodGet, "/api/v1/accounts/"+accStr+"/captain/quota", nil)
		if recQuota.Code != http.StatusOK {
			t.Fatalf("Get AI quota failed: %d", recQuota.Code)
		}
	})

	// -------------------------------------------------------------
	// 17. 第三方应用集成 (Shopify Orders & Dialogflow Process)
	// -------------------------------------------------------------
	t.Run("Item 17: Shopify & Dialogflow Integrations", func(t *testing.T) {
		// Shopify Orders
		recShp := authReq(http.MethodGet, "/api/v1/accounts/"+accStr+"/integrations/shopify/orders?email=customer@example.com", nil)
		if recShp.Code != http.StatusOK {
			t.Fatalf("Shopify orders lookup failed: %d", recShp.Code)
		}

		// Dialogflow Process with human handoff
		recDf := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/integrations/dialogflow/process", map[string]any{
			"conversation_id": 1,
			"query":           "我要转人工客服",
			"session_id":      "sess_987",
		})
		if recDf.Code != http.StatusOK {
			t.Fatalf("Dialogflow process failed: %d", recDf.Code)
		}
		var dfResp struct {
			Data struct {
				HumanHandoff bool `json:"human_handoff"`
			} `json:"data"`
		}
		_ = json.Unmarshal(recDf.Body.Bytes(), &dfResp)
		if !dfResp.Data.HumanHandoff {
			t.Error("expected human_handoff to be true")
		}
	})

	// -------------------------------------------------------------
	// 18. Facebook 入站消息 (HandleFacebookWebhook)
	// -------------------------------------------------------------
	t.Run("Item 18: Facebook Webhook Inbound", func(t *testing.T) {
		fbPayload := map[string]any{
			"object": "page",
			"entry": []map[string]any{
				{
					"id":   "page_123",
					"time": time.Now().UnixMilli(),
					"messaging": []map[string]any{
						{
							"sender":    map[string]string{"id": "fb_user_456"},
							"recipient": map[string]string{"id": "page_123"},
							"message": map[string]string{
								"mid":  "mid_test_789",
								"text": "Hello from Facebook Messenger!",
							},
						},
					},
				},
			},
		}

		bodyBytes, _ := json.Marshal(fbPayload)
		reqFB := httptest.NewRequest(http.MethodPost, "/public/api/v1/channels/facebook/webhook?account_id="+accStr, bytes.NewReader(bodyBytes))
		reqFB.Header.Set("Content-Type", "application/json")
		wFB := httptest.NewRecorder()
		r.ServeHTTP(wFB, reqFB)
		if wFB.Code != http.StatusOK {
			t.Fatalf("Facebook webhook failed: code=%d, body=%s", wFB.Code, wFB.Body.String())
		}

		// Verify contact and message created
		var contact domain.Contact
		if err := db.Where("account_id = ? AND identifier = ?", accID, "fb_user_456").First(&contact).Error; err != nil {
			t.Errorf("Facebook contact was not created: %v", err)
		}
		var msg domain.Message
		if err := db.Where("account_id = ? AND echo_id = ?", accID, "mid_test_789").First(&msg).Error; err != nil {
			t.Errorf("Facebook message was not created: %v", err)
		}
	})

	// -------------------------------------------------------------
	// 19. 通话与会议 (Call signaling & ICE candidates)
	// -------------------------------------------------------------
	t.Run("Item 19: WebRTC Call Signaling & ICE", func(t *testing.T) {
		// 1. Initiate Call
		recInit := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/contacts/1/call", map[string]any{
			"inbox_id":  1,
			"sdp_offer": "v=0\r\no=test 123 IN IP4 127.0.0.1",
		})
		if recInit.Code != http.StatusCreated {
			t.Fatalf("Initiate call failed: %d", recInit.Code)
		}
		var callResp struct {
			Data domain.Call `json:"data"`
		}
		_ = json.Unmarshal(recInit.Body.Bytes(), &callResp)
		callID := strconv.FormatUint(uint64(callResp.Data.ID), 10)

		// 2. Accept Call
		recAccept := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/calls/"+callID+"/accept", map[string]string{
			"sdp_answer": "v=0\r\no=peer 456 IN IP4 127.0.0.1",
		})
		if recAccept.Code != http.StatusOK {
			t.Fatalf("Accept call failed: %d", recAccept.Code)
		}

		// 3. Add ICE Candidate
		recCand := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/calls/"+callID+"/candidates", map[string]any{
			"candidate":        "candidate:1 1 UDP 2130706431 192.168.1.1 5000 typ host",
			"sdp_mid":          "audio",
			"sdp_m_line_index": 0,
		})
		if recCand.Code != http.StatusCreated {
			t.Fatalf("Add ICE candidate failed: %d", recCand.Code)
		}

		// 4. List ICE Candidates
		recListCands := authReq(http.MethodGet, "/api/v1/accounts/"+accStr+"/calls/"+callID+"/candidates", nil)
		if recListCands.Code != http.StatusOK {
			t.Fatalf("List ICE candidates failed: %d", recListCands.Code)
		}

		// 5. End Call
		recEnd := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/calls/"+callID+"/end", map[string]any{
			"duration": 45,
		})
		if recEnd.Code != http.StatusOK {
			t.Fatalf("End call failed: %d", recEnd.Code)
		}
	})

	// -------------------------------------------------------------
	// 20. 套餐与支付 (SaaS plans & Subscription lifecycle)
	// -------------------------------------------------------------
	t.Run("Item 20: SaaS Plans & Subscription", func(t *testing.T) {
		// List plans
		recPlans := authReq(http.MethodGet, "/api/v1/accounts/"+accStr+"/subscription/plans", nil)
		if recPlans.Code != http.StatusOK {
			t.Fatalf("List plans failed: %d", recPlans.Code)
		}

		// Create/Update subscription to pro
		recSub := authReq(http.MethodPost, "/api/v1/accounts/"+accStr+"/subscription", map[string]string{
			"plan_slug": "pro",
		})
		if recSub.Code != http.StatusOK {
			t.Fatalf("Update subscription failed: %d", recSub.Code)
		}

		// Verify subscription
		recGetSub := authReq(http.MethodGet, "/api/v1/accounts/"+accStr+"/subscription", nil)
		if recGetSub.Code != http.StatusOK {
			t.Fatalf("Get subscription failed: %d", recGetSub.Code)
		}

		// Webhook processing
		webhookBody, _ := json.Marshal(map[string]any{
			"event": "payment_succeeded",
			"data":  map[string]any{"account_id": accID},
		})
		recWh := httptest.NewRequest(http.MethodPost, "/public/api/v1/subscription/webhook", bytes.NewReader(webhookBody))
		recWh.Header.Set("Content-Type", "application/json")
		wWh := httptest.NewRecorder()
		r.ServeHTTP(wWh, recWh)
		if wWh.Code != http.StatusOK {
			t.Fatalf("Subscription webhook failed: %d", wWh.Code)
		}
	})
}
