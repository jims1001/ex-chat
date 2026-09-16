package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/handler"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupGovernanceTestDB(t *testing.T) (*gorm.DB, *gin.Engine, *domain.User, uint, string) {
	gin.SetMode(gin.TestMode)

	jwtSecret := "enterprise-governance-secret-2026"
	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          jwtSecret,
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	require.NoError(t, err)

	hub := ws.NewHub()
	go hub.Run()

	engine := router.SetupRouter(cfg, db, hub)

	// Sign up admin
	signUpBody, _ := json.Marshal(map[string]string{
		"name":         "治理主管",
		"email":        "gov.admin@example.com",
		"password":     "password123456",
		"account_name": "核心能力治理工作区",
	})
	wAdmin := httptest.NewRecorder()
	reqAdmin, _ := http.NewRequest("POST", "/auth/sign_up", bytes.NewBuffer(signUpBody))
	reqAdmin.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wAdmin, reqAdmin)
	require.Equal(t, http.StatusCreated, wAdmin.Code)

	var authResp struct {
		Data struct {
			Token    string `json:"token"`
			Accounts []struct {
				ID uint `json:"id"`
			} `json:"accounts"`
			User struct {
				ID uint `json:"id"`
			} `json:"user"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wAdmin.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID
	adminUserID := authResp.Data.User.ID

	var adminUser domain.User
	db.First(&adminUser, adminUserID)

	return db, engine, &adminUser, accountID, token
}

func TestCoreCapabilitiesGovernance(t *testing.T) {
	db, r, adminUser, accountID, token := setupGovernanceTestDB(t)

	// -------------------------------------------------------------
	// 1. 自动化规则防死循环与执行日志落库
	// -------------------------------------------------------------
	t.Run("Item1_Automation_Recursion_Breaker_And_Execution_Logging", func(t *testing.T) {
		convRepo := repository.NewConversationRepository(db)
		msgRepo := repository.NewMessageRepository(db)
		autoService := service.NewAutomationService(db, convRepo, msgRepo)

		conv := &domain.Conversation{
			AccountID: accountID,
			Status:    "open",
			Priority:  "low",
		}
		require.NoError(t, db.Create(conv).Error)

		// Create a rule that updates status to resolved
		rule := &domain.AutomationRule{
			AccountID: accountID,
			Name:      "Auto Resolve Rule",
			EventName: "conversation_created",
			Active:    true,
			Conditions: `[{"attribute_key":"status","filter_operator":"equal_to","values":["open"]}]`,
			Actions:    `[{"action_name":"change_priority","action_params":{"priority":"urgent"}},{"action_name":"change_status","action_params":{"status":"resolved"}}]`,
		}
		require.NoError(t, db.Create(rule).Error)

		// Test normal execution
		autoService.HandleConversationCreated(conv)

		// Check conversation updated
		var updatedConv domain.Conversation
		require.NoError(t, db.First(&updatedConv, conv.ID).Error)
		assert.Equal(t, "resolved", updatedConv.Status)
		assert.Equal(t, "urgent", updatedConv.Priority)

		// Check execution history recorded
		var execs []domain.AutomationRuleExecution
		require.NoError(t, db.Where("account_id = ? AND rule_id = ?", accountID, rule.ID).Find(&execs).Error)
		assert.NotEmpty(t, execs)
		assert.Equal(t, "success", execs[0].Status)
		assert.Contains(t, execs[0].ActionResults, "change_priority")

		// Test recursion breaker by simulating depth limit
		ctxDepth3 := service.WithAutomationDepth(service.WithAutomationDepth(service.WithAutomationDepth(context.Background())))
		autoService.HandleConversationCreatedWithContext(ctxDepth3, conv)

		var depthExec domain.AutomationRuleExecution
		err := db.Where("account_id = ? AND status = 'depth_exceeded'", accountID).First(&depthExec).Error
		assert.NoError(t, err, "Should record depth_exceeded event when recursion exceeds limit")
	})

	// -------------------------------------------------------------
	// 2. 宏操作真附件生成、当前操作人身份与限流
	// -------------------------------------------------------------
	t.Run("Item2_Macro_Real_Attachment_And_Batch_Throttle", func(t *testing.T) {
		conv := &domain.Conversation{
			AccountID: accountID,
			Status:    "open",
		}
		require.NoError(t, db.Create(conv).Error)

		// 1. Create macro with send_attachment action
		attachmentActionJSON := `[{"action_name":"send_attachment","action_params":{"file_url":"https://example.com/receipt.pdf","file_type":"application/pdf","file_size":102400}},{"action_name":"change_status","action_params":["resolved"]}]`
		macro := &domain.Macro{
			AccountID:  accountID,
			Name:       "Send Invoice Macro",
			Actions:    attachmentActionJSON,
			Visibility: "global",
			CreatedBy:  adminUser.ID,
		}
		require.NoError(t, db.Create(macro).Error)

		// Execute macro
		reqBody, _ := json.Marshal(gin.H{
			"conversation_ids": []uint{conv.ID},
		})
		req := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/macros/%d/execute", accountID, macro.ID), bytes.NewReader(reqBody))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		// Check response format
		var resp struct {
			Data struct {
				Status    string `json:"status"`
				Total     int    `json:"total"`
				Succeeded int    `json:"succeeded"`
				Failed    int    `json:"failed"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		assert.Equal(t, "success", resp.Data.Status)
		assert.Equal(t, 1, resp.Data.Succeeded)

		// Verify real attachment was created in DB and associated with user
		var att domain.Attachment
		require.NoError(t, db.Where("account_id = ? AND data_url = ?", accountID, "https://example.com/receipt.pdf").First(&att).Error)
		assert.Equal(t, "application/pdf", att.FileType)
		assert.Equal(t, int64(102400), att.FileSize)

		// Verify message sender is adminUser
		var msg domain.Message
		require.NoError(t, db.First(&msg, att.MessageID).Error)
		assert.Equal(t, domain.SenderTypeUser, msg.SenderType)
		assert.Equal(t, adminUser.ID, msg.SenderID)

		// Test throttle > 500 limit
		largeBatch := make([]uint, 501)
		for i := 0; i < 501; i++ {
			largeBatch[i] = uint(i + 1)
		}
		reqOverLimit, _ := json.Marshal(gin.H{"conversation_ids": largeBatch})
		req2 := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/macros/%d/execute", accountID, macro.ID), bytes.NewReader(reqOverLimit))
		req2.Header.Set("Authorization", "Bearer "+token)
		req2.Header.Set("Content-Type", "application/json")
		w2 := httptest.NewRecorder()
		r.ServeHTTP(w2, req2)
		assert.Equal(t, http.StatusBadRequest, w2.Code)
		assert.Contains(t, w2.Body.String(), "exceeds maximum limit of 500")
	})

	// -------------------------------------------------------------
	// 3. 工单系统乐观锁、Watcher、批量更新与定时 Worker
	// -------------------------------------------------------------
	t.Run("Item3_Ticket_OptimisticLock_Watcher_Bulk_And_Worker", func(t *testing.T) {
		ticketRepo := repository.NewTicketRepository(db)
		ticketHandler := handler.NewTicketHandler(ticketRepo)

		ticket := &domain.Ticket{
			AccountID:    accountID,
			Title:        "System Outage Escalation",
			Status:       "open",
			Priority:     "urgent",
			TicketNumber: "TK-20260914-0001",
			Version:      1,
		}
		require.NoError(t, ticketRepo.Create(ticket))

		// Test optimistic locking collision
		// 1. First update succeeds with version 1 -> increments to 2
		v1 := 1
		reqBody1, _ := json.Marshal(gin.H{
			"title":   "System Outage Updated",
			"version": v1,
		})
		req1 := httptest.NewRequest("PATCH", fmt.Sprintf("/api/v1/accounts/%d/tickets/%d", accountID, ticket.ID), bytes.NewReader(reqBody1))
		req1.Header.Set("Authorization", "Bearer "+token)
		req1.Header.Set("Content-Type", "application/json")
		w1 := httptest.NewRecorder()
		r.ServeHTTP(w1, req1)
		assert.Equal(t, http.StatusOK, w1.Code)

		// 2. Second concurrent update provides stale version 1 -> should return 409 Conflict
		reqBody2, _ := json.Marshal(gin.H{
			"title":   "Concurrent Stale Update",
			"version": v1,
		})
		req2 := httptest.NewRequest("PATCH", fmt.Sprintf("/api/v1/accounts/%d/tickets/%d", accountID, ticket.ID), bytes.NewReader(reqBody2))
		req2.Header.Set("Authorization", "Bearer "+token)
		req2.Header.Set("Content-Type", "application/json")
		w2 := httptest.NewRecorder()
		r.ServeHTTP(w2, req2)
		assert.Equal(t, http.StatusConflict, w2.Code)
		assert.Contains(t, w2.Body.String(), "Optimistic lock conflict")

		// 3. Test Watcher API
		watchReq, _ := json.Marshal(gin.H{"user_id": adminUser.ID})
		reqWatch := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/tickets/%d/watchers", accountID, ticket.ID), bytes.NewReader(watchReq))
		reqWatch.Header.Set("Authorization", "Bearer "+token)
		reqWatch.Header.Set("Content-Type", "application/json")
		wWatch := httptest.NewRecorder()
		r.ServeHTTP(wWatch, reqWatch)
		assert.Equal(t, http.StatusOK, wWatch.Code)

		watchers, err := ticketRepo.ListWatchers(accountID, ticket.ID)
		assert.NoError(t, err)
		assert.Len(t, watchers, 1)

		// 4. Test Bulk Update
		bulkReq, _ := json.Marshal(gin.H{
			"ticket_ids": []uint{ticket.ID},
			"priority":   "medium",
		})
		reqBulk := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/tickets/bulk_update", accountID), bytes.NewReader(bulkReq))
		reqBulk.Header.Set("Authorization", "Bearer "+token)
		reqBulk.Header.Set("Content-Type", "application/json")
		wBulk := httptest.NewRecorder()
		r.ServeHTTP(wBulk, reqBulk)
		assert.Equal(t, http.StatusOK, wBulk.Code)

		// 5. Test Auto-close and Waiting Resume background scanner
		pastTime := time.Now().Add(-1 * time.Hour)
		autoCloseTicket := &domain.Ticket{
			AccountID:    accountID,
			Title:        "Auto Close Ticket",
			Status:       "resolved",
			AutoCloseAt:  &pastTime,
			TicketNumber: "TK-20260914-0002",
		}
		require.NoError(t, ticketRepo.Create(autoCloseTicket))

		resumeTicket := &domain.Ticket{
			AccountID:    accountID,
			Title:        "Auto Resume Ticket",
			Status:       "pending",
			ResumeAt:     &pastTime,
			TicketNumber: "TK-20260914-0003",
		}
		require.NoError(t, ticketRepo.Create(resumeTicket))

		closed, resumed, err := ticketRepo.ProcessAutoCloseAndResume(time.Now())
		assert.NoError(t, err)
		assert.GreaterOrEqual(t, closed, int64(1))
		assert.GreaterOrEqual(t, resumed, int64(1))

		var checkClose domain.Ticket
		db.First(&checkClose, autoCloseTicket.ID)
		assert.Equal(t, "closed", checkClose.Status)

		var checkResume domain.Ticket
		db.First(&checkResume, resumeTicket.ID)
		assert.Equal(t, "open", checkResume.Status)

		// Verify background worker routine lifecycle
		stop := ticketHandler.StartTicketBackgroundWorker(100 * time.Millisecond)
		time.Sleep(150 * time.Millisecond)
		close(stop)
	})

	// -------------------------------------------------------------
	// 4. 质检系统评分卡权重校验、利益冲突拦截与申诉时限
	// -------------------------------------------------------------
	t.Run("Item4_QA_Weight_Conflict_And_AppealDeadline", func(t *testing.T) {
		// 1. Test weight sum != 100 rejected
		invalidScorecardReq, _ := json.Marshal(gin.H{
			"name":        "Test Scorecard",
			"total_score": 100,
			"criteria": []gin.H{
				{"category": "服务规范", "title": "礼貌用语", "max_score": 30},
				{"category": "问题解决", "title": "正确解答", "max_score": 50}, // sum = 80 != 100
			},
		})
		reqCard := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/scorecards", accountID), bytes.NewReader(invalidScorecardReq))
		reqCard.Header.Set("Authorization", "Bearer "+token)
		reqCard.Header.Set("Content-Type", "application/json")
		wCard := httptest.NewRecorder()
		r.ServeHTTP(wCard, reqCard)
		assert.Equal(t, http.StatusBadRequest, wCard.Code)
		assert.Contains(t, wCard.Body.String(), "must equal scorecard total score")

		// 2. Conflict of interest: Evaluator cannot evaluate own task
		agentUser := domain.User{
			Name:         "质检专员",
			Email:        "qa.agent@example.com",
			PasswordHash: "hashed",
			Role:         domain.RoleAgent,
		}
		require.NoError(t, db.Create(&agentUser).Error)

		task := &domain.QATask{
			AccountID:     accountID,
			TaskNumber:    "QA-20260914-0001",
			TargetType:    "conversation",
			TargetID:      101,
			ScorecardID:   1,
			ScorecardName: "标准评分表",
			InspectorID:   &adminUser.ID,
			AgentID:       &adminUser.ID, // Self-evaluation scenario!
			Status:        "pending",
		}
		require.NoError(t, db.Create(task).Error)

		evalReq, _ := json.Marshal(gin.H{
			"feedback": "Self evaluation attempt",
			"evaluations": []gin.H{
				{"criterion_id": 1, "criterion_title": "合规", "category": "标准", "max_score": 100, "score": 100},
			},
		})
		reqEval := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/tasks/%d/evaluate", accountID, task.ID), bytes.NewReader(evalReq))
		reqEval.Header.Set("Authorization", "Bearer "+token)
		reqEval.Header.Set("Content-Type", "application/json")
		wEval := httptest.NewRecorder()
		r.ServeHTTP(wEval, reqEval)
		assert.Equal(t, http.StatusUnprocessableEntity, wEval.Code)
		assert.Contains(t, wEval.Body.String(), "Conflict of interest")

		// 3. Appeal deadline: > 7 days expired
		tenDaysAgo := time.Now().Add(-10 * 24 * time.Hour)
		completedTask := &domain.QATask{
			AccountID:     accountID,
			TaskNumber:    "QA-20260914-0002",
			TargetType:    "conversation",
			TargetID:      102,
			ScorecardID:   1,
			ScorecardName: "标准评分表",
			AgentID:       &adminUser.ID,
			Status:        "completed",
			CompletedAt:   &tenDaysAgo,
		}
		require.NoError(t, db.Create(completedTask).Error)

		appealReq, _ := json.Marshal(gin.H{
			"reason": "Late appeal request",
		})
		reqAppeal := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/qa/tasks/%d/appeal", accountID, completedTask.ID), bytes.NewReader(appealReq))
		reqAppeal.Header.Set("Authorization", "Bearer "+token)
		reqAppeal.Header.Set("Content-Type", "application/json")
		wAppeal := httptest.NewRecorder()
		r.ServeHTTP(wAppeal, reqAppeal)
		assert.Equal(t, http.StatusUnprocessableEntity, wAppeal.Code)
		assert.Contains(t, wAppeal.Body.String(), "Appeal deadline expired")

		// 4. Test QA summary report
		reqRep := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/qa/reports/summary", accountID), nil)
		reqRep.Header.Set("Authorization", "Bearer "+token)
		wRep := httptest.NewRecorder()
		r.ServeHTTP(wRep, reqRep)
		assert.Equal(t, http.StatusOK, wRep.Code)
		assert.Contains(t, wRep.Body.String(), "pass_rate")
	})

	// -------------------------------------------------------------
	// 5. 核心写操作真正写入审计链验证
	// -------------------------------------------------------------
	t.Run("Item5_AuditTrail_Integrity_Check", func(t *testing.T) {
		// 1. Contact Merge Audit Check
		c1 := &domain.Contact{AccountID: accountID, Name: "Contact A"}
		c2 := &domain.Contact{AccountID: accountID, Name: "Contact B"}
		require.NoError(t, db.Create(c1).Error)
		require.NoError(t, db.Create(c2).Error)

		mergeReq, _ := json.Marshal(gin.H{
			"base_contact_id":   c1.ID,
			"mergee_contact_id": c2.ID,
		})
		reqMerge := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/contacts/merge", accountID), bytes.NewReader(mergeReq))
		reqMerge.Header.Set("Authorization", "Bearer "+token)
		reqMerge.Header.Set("Content-Type", "application/json")
		wMerge := httptest.NewRecorder()
		r.ServeHTTP(wMerge, reqMerge)
		assert.Equal(t, http.StatusOK, wMerge.Code)

		// Verify contact_merge in LocalChangeJournal
		var mergeJournal domain.LocalChangeJournal
		err := db.Where("account_id = ? AND action = 'contact_merge' AND entity_id = ?", accountID, c1.ID).First(&mergeJournal).Error
		assert.NoError(t, err, "LocalChangeJournal must record contact_merge")
		assert.Contains(t, mergeJournal.Reason, "Merged contact")

		// 2. Custom Role Update Audit Check
		role := &domain.CustomRole{
			AccountID:   accountID,
			Name:        "Tier 2 Support",
			Permissions: `["conversation_manage"]`,
		}
		require.NoError(t, db.Create(role).Error)

		role.Permissions = `["conversation_manage","ticket_manage"]`
		accountRepo := repository.NewAccountRepository(db)
		require.NoError(t, accountRepo.UpdateCustomRole(role))

		var roleJournal domain.LocalChangeJournal
		err = db.Where("account_id = ? AND action = 'role_update' AND entity_id = ?", accountID, role.ID).First(&roleJournal).Error
		assert.NoError(t, err, "LocalChangeJournal must record role_update")
	})
}
