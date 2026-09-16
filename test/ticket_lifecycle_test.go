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
	"github.com/gin-gonic/gin"
)

func TestTicketLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "ticket-lifecycle-secret-key-2026",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()

	engine := router.SetupRouter(cfg, db, hub)

	// Step 0: Sign up Admin User & Account
	signUpBody, _ := json.Marshal(map[string]string{
		"name":         "工单主管",
		"email":        "ticket.admin@example.com",
		"password":     "password123456",
		"account_name": "智能工单服务中心",
	})
	wSignUp := httptest.NewRecorder()
	reqSignUp, _ := http.NewRequest("POST", "/auth/sign_up", bytes.NewBuffer(signUpBody))
	reqSignUp.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wSignUp, reqSignUp)
	if wSignUp.Code != http.StatusCreated {
		t.Fatalf("sign up failed: %d body: %s", wSignUp.Code, wSignUp.Body.String())
	}

	var authResp struct {
		Data struct {
			Token    string `json:"token"`
			Accounts []struct {
				ID uint `json:"id"`
			} `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wSignUp.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID

	// Create a secondary agent for assignment tests
	agentUser := domain.User{
		Name:         "二线技术坐席",
		Email:        "tech.agent@example.com",
		PasswordHash: "fakehash",
		Role:         domain.RoleAgent,
	}
	db.Create(&agentUser)
	db.Create(&domain.AccountUser{
		AccountID: accountID,
		UserID:    agentUser.ID,
		Role:      domain.RoleAgent,
	})

	// Create a customer contact for ticket linking
	contact := domain.Contact{
		AccountID:   accountID,
		Name:        "华为技术对接人",
		Email:       "contact.huawei@example.com",
		PhoneNumber: "+8613800138000",
	}
	db.Create(&contact)

	otherAccount := domain.Account{Name: "Other tenant"}
	db.Create(&otherAccount)
	otherContact := domain.Contact{AccountID: otherAccount.ID, Name: "Other tenant contact", Email: "private@other.example"}
	db.Create(&otherContact)

	t.Run("rejects cross-tenant references", func(t *testing.T) {
		body, _ := json.Marshal(map[string]interface{}{
			"title":      "must not link foreign contact",
			"contact_id": otherContact.ID,
		})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/tickets", accountID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for cross-tenant contact, got %d body=%s", w.Code, w.Body.String())
		}
	})

	var createdTicketID uint
	var createdTicketNumber string

	// 1. Create Ticket
	t.Run("Scenario 1: Ticket Creation & Auto Number Generation", func(t *testing.T) {
		reqBody, _ := json.Marshal(map[string]interface{}{
			"title":          "API 签名验证异常排查",
			"description":    "客户在生产环境调用 Webhook 签名验证返回 401，需协调二线排查",
			"priority":       "high",
			"assigned_group": "技术支持",
			"contact_id":     contact.ID,
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/tickets", accountID), bytes.NewBuffer(reqBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d body: %s", w.Code, w.Body.String())
		}

		var resp struct {
			Data domain.Ticket `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		if resp.Data.ID == 0 {
			t.Errorf("expected non-zero ID")
		}
		if resp.Data.TicketNumber == "" {
			t.Errorf("expected auto-generated ticket_number")
		}
		if resp.Data.Status != "open" {
			t.Errorf("expected default status 'open', got %s", resp.Data.Status)
		}
		if resp.Data.DueAt == nil {
			t.Errorf("expected auto-calculated due_at SLA deadline")
		}
		if resp.Data.ContactID == nil || *resp.Data.ContactID != contact.ID {
			t.Errorf("expected contact_id %d", contact.ID)
		}

		createdTicketID = resp.Data.ID
		createdTicketNumber = resp.Data.TicketNumber
	})

	// 2. Ticket Detail & Query by TicketNumber
	t.Run("Scenario 2: Ticket Detail Retrieval by ID and Ticket Number", func(t *testing.T) {
		// By numeric ID
		wID := httptest.NewRecorder()
		reqID, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/tickets/%d", accountID, createdTicketID), nil)
		reqID.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wID, reqID)
		if wID.Code != http.StatusOK {
			t.Fatalf("expected 200 by ID, got %d body: %s", wID.Code, wID.Body.String())
		}

		// By ticket number
		wNum := httptest.NewRecorder()
		reqNum, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/tickets/%s", accountID, createdTicketNumber), nil)
		reqNum.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wNum, reqNum)
		if wNum.Code != http.StatusOK {
			t.Fatalf("expected 200 by number, got %d body: %s", wNum.Code, wNum.Body.String())
		}
	})

	// 3. Ticket Update
	t.Run("Scenario 3: Ticket Update (Priority & Custom Attributes)", func(t *testing.T) {
		newTitle := "【紧急升级】API 签名验证异常排查"
		newPriority := "urgent"
		customAttrs := `{"environment":"production","cluster":"cn-east-1"}`

		reqBody, _ := json.Marshal(map[string]interface{}{
			"title":             newTitle,
			"priority":          newPriority,
			"custom_attributes": customAttrs,
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("PATCH", fmt.Sprintf("/api/v1/accounts/%d/tickets/%d", accountID, createdTicketID), bytes.NewBuffer(reqBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
		}

		var resp struct {
			Data domain.Ticket `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Data.Title != newTitle || resp.Data.Priority != newPriority {
			t.Errorf("update failed, title: %s, priority: %s", resp.Data.Title, resp.Data.Priority)
		}
	})

	// 4. Ticket Assignment
	t.Run("Scenario 4: Ticket Assignment to Agent and Team", func(t *testing.T) {
		reqBody, _ := json.Marshal(map[string]interface{}{
			"assignee_id":    agentUser.ID,
			"assigned_group": "研发二线专家组",
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/tickets/%d/assign", accountID, createdTicketID), bytes.NewBuffer(reqBody))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
		}

		var resp struct {
			Data domain.Ticket `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Data.AssigneeID == nil || *resp.Data.AssigneeID != agentUser.ID {
			t.Errorf("expected assignee_id %d", agentUser.ID)
		}
		if resp.Data.AssignedGroup != "研发二线专家组" {
			t.Errorf("expected group '研发二线专家组', got %s", resp.Data.AssignedGroup)
		}
	})

	// 5. Comments & Internal Notes
	t.Run("Scenario 5: Ticket Internal Notes and Comments", func(t *testing.T) {
		commentBody, _ := json.Marshal(map[string]interface{}{
			"content":    "已抓取网关签名日志，疑似时间戳偏移超出 300 秒，等待客户确认本地 NTP",
			"is_private": true,
		})

		wPost := httptest.NewRecorder()
		reqPost, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/tickets/%d/comments", accountID, createdTicketID), bytes.NewBuffer(commentBody))
		reqPost.Header.Set("Content-Type", "application/json")
		reqPost.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wPost, reqPost)

		if wPost.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d body: %s", wPost.Code, wPost.Body.String())
		}

		// List comments
		wList := httptest.NewRecorder()
		reqList, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/tickets/%d/comments", accountID, createdTicketID), nil)
		reqList.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wList, reqList)

		if wList.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body: %s", wList.Code, wList.Body.String())
		}

		var resp struct {
			Data []domain.TicketComment `json:"data"`
		}
		_ = json.Unmarshal(wList.Body.Bytes(), &resp)
		if len(resp.Data) == 0 {
			t.Fatalf("expected at least 1 comment, got %d", len(resp.Data))
		}
		if !resp.Data[0].IsPrivate {
			t.Errorf("expected is_private to be true")
		}
	})

	// 6. Status Flow (open -> pending -> resolved -> closed)
	t.Run("Scenario 6: Status Flow Lifecycle and SLA Outcome Check", func(t *testing.T) {
		// Transition to pending (waiting for customer)
		bodyPending, _ := json.Marshal(map[string]interface{}{
			"status":  "pending",
			"reason":  "等待客户同步服务器 NTP 对齐结果",
			"comment": "已通知客户同步确认",
		})
		wPending := httptest.NewRecorder()
		reqPending, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/tickets/%d/status", accountID, createdTicketID), bytes.NewBuffer(bodyPending))
		reqPending.Header.Set("Content-Type", "application/json")
		reqPending.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wPending, reqPending)
		if wPending.Code != http.StatusOK {
			t.Fatalf("expected 200 on pending status, got %d", wPending.Code)
		}

		// Transition to resolved
		bodyResolved, _ := json.Marshal(map[string]interface{}{
			"status":  "resolved",
			"reason":  "客户完成 NTP 校准，验证签名已恢复 200",
			"comment": "问题彻底根治并完成复测",
		})
		wResolved := httptest.NewRecorder()
		reqResolved, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/tickets/%d/status", accountID, createdTicketID), bytes.NewBuffer(bodyResolved))
		reqResolved.Header.Set("Content-Type", "application/json")
		reqResolved.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wResolved, reqResolved)
		if wResolved.Code != http.StatusOK {
			t.Fatalf("expected 200 on resolved status, got %d", wResolved.Code)
		}

		var respResolved struct {
			Data domain.Ticket `json:"data"`
		}
		_ = json.Unmarshal(wResolved.Body.Bytes(), &respResolved)
		if respResolved.Data.Status != "resolved" {
			t.Errorf("expected status 'resolved', got %s", respResolved.Data.Status)
		}
		if respResolved.Data.ResolvedAt == nil {
			t.Errorf("expected resolved_at to be set")
		}
		if respResolved.Data.SLAStatus != "achieved" {
			t.Errorf("expected SLA status 'achieved', got %s", respResolved.Data.SLAStatus)
		}

		// Transition to closed
		bodyClosed, _ := json.Marshal(map[string]interface{}{
			"status": "closed",
			"reason": "服务闭环归档",
		})
		wClosed := httptest.NewRecorder()
		reqClosed, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/tickets/%d/status", accountID, createdTicketID), bytes.NewBuffer(bodyClosed))
		reqClosed.Header.Set("Content-Type", "application/json")
		reqClosed.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wClosed, reqClosed)
		if wClosed.Code != http.StatusOK {
			t.Fatalf("expected 200 on closed status, got %d", wClosed.Code)
		}
	})

	// 7. Timeline Activities Retrieval
	t.Run("Scenario 7: Activity Timeline Retrieval", func(t *testing.T) {
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/tickets/%d/activities", accountID, createdTicketID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d body: %s", w.Code, w.Body.String())
		}

		var resp struct {
			Data []domain.TicketActivity `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if len(resp.Data) < 3 {
			t.Fatalf("expected at least 3 activities (created, updated/assigned, status_changed), got %d", len(resp.Data))
		}
	})

	// 8. Dynamic SLA Breach & Stats Endpoint
	t.Run("Scenario 8: SLA Dynamic Breach Detection & Dashboard Stats", func(t *testing.T) {
		// Insert an overdue ticket
		pastTime := time.Now().Add(-2 * time.Hour)
		overdueTicket := domain.Ticket{
			AccountID:     accountID,
			TicketNumber:  "TK-OVERDUE-001",
			Title:         "严重超时工单",
			Status:        "open",
			Priority:      "urgent",
			AssignedGroup: "技术支持",
			DueAt:         &pastTime,
			SLAStatus:     "normal",
		}
		db.Create(&overdueTicket)

		// Calling list should refresh SLAStatus to 'breached'
		wList := httptest.NewRecorder()
		reqList, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/tickets?status=open", accountID), nil)
		reqList.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wList, reqList)
		if wList.Code != http.StatusOK {
			t.Fatalf("expected 200 on list, got %d", wList.Code)
		}

		// Verify stats endpoint
		wStats := httptest.NewRecorder()
		reqStats, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/tickets/stats", accountID), nil)
		reqStats.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wStats, reqStats)

		if wStats.Code != http.StatusOK {
			t.Fatalf("expected 200 on stats, got %d body: %s", wStats.Code, wStats.Body.String())
		}

		var respStats struct {
			Data map[string]int64 `json:"data"`
		}
		_ = json.Unmarshal(wStats.Body.Bytes(), &respStats)
		if respStats.Data["urgent"] < 1 {
			t.Errorf("expected urgent count >= 1 for breached ticket, got %d", respStats.Data["urgent"])
		}
		if respStats.Data["total"] < 2 {
			t.Errorf("expected total count >= 2, got %d", respStats.Data["total"])
		}
	})

	// 9. Multi-tenant Isolation
	t.Run("Scenario 9: Multi-tenant Security Isolation", func(t *testing.T) {
		otherAccount := domain.Account{Name: "外部租户"}
		db.Create(&otherAccount)

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/tickets/%d", otherAccount.ID, createdTicketID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(w, req)

		// Current user is not a member of otherAccount -> TenantMiddleware rejects with 404 or 403
		if w.Code != http.StatusNotFound && w.Code != http.StatusForbidden {
			t.Errorf("expected 404 or 403 for unauthorized cross-tenant access, got %d", w.Code)
		}
	})

	// 10. Waiting Ticket Lifecycle: Wait transition
	var waitingTicketID uint
	t.Run("Scenario 10: Ticket Waiting State Transition", func(t *testing.T) {
		// Create a fresh open ticket to test waiting workflow
		createBody, _ := json.Marshal(map[string]interface{}{
			"title":          "退货退款审核流程",
			"description":    "客户申请退款，等待上传快递单据",
			"priority":       "medium",
			"assigned_group": "售后客服",
		})
		wCreate := httptest.NewRecorder()
		reqCreate, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/tickets", accountID), bytes.NewBuffer(createBody))
		reqCreate.Header.Set("Content-Type", "application/json")
		reqCreate.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wCreate, reqCreate)
		if wCreate.Code != http.StatusCreated {
			t.Fatalf("expected 201 on create ticket, got %d body: %s", wCreate.Code, wCreate.Body.String())
		}
		var cr struct {
			Data domain.Ticket `json:"data"`
		}
		_ = json.Unmarshal(wCreate.Body.Bytes(), &cr)
		waitingTicketID = cr.Data.ID

		// Transition to wait
		futureResume := time.Now().Add(48 * time.Hour)
		futureClose := time.Now().Add(72 * time.Hour)
		waitBody, _ := json.Marshal(map[string]interface{}{
			"waiting_reason": "等待客户补充快递单号与包装照片",
			"resume_at":      futureResume.Format(time.RFC3339),
			"auto_close_at":  futureClose.Format(time.RFC3339),
			"reason":         "缺少有效寄回凭证",
			"comment":        "已向客户发送补寄单号指引邮件",
		})
		wWait := httptest.NewRecorder()
		reqWait, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/tickets/%d/wait", accountID, waitingTicketID), bytes.NewBuffer(waitBody))
		reqWait.Header.Set("Content-Type", "application/json")
		reqWait.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wWait, reqWait)

		if wWait.Code != http.StatusOK {
			t.Fatalf("expected 200 on wait, got %d body: %s", wWait.Code, wWait.Body.String())
		}

		var waitResp struct {
			Data domain.Ticket `json:"data"`
		}
		_ = json.Unmarshal(wWait.Body.Bytes(), &waitResp)
		if waitResp.Data.Status != "pending" {
			t.Errorf("expected status 'pending', got %s", waitResp.Data.Status)
		}
		if waitResp.Data.WaitingReason != "等待客户补充快递单号与包装照片" {
			t.Errorf("unexpected waiting reason: %s", waitResp.Data.WaitingReason)
		}
		if waitResp.Data.ResumeAt == nil || waitResp.Data.AutoCloseAt == nil {
			t.Errorf("expected resume_at and auto_close_at to be populated")
		}
	})

	// 11. Status History Retrieval
	t.Run("Scenario 11: Independent Status History Audit Retrieval", func(t *testing.T) {
		wHist := httptest.NewRecorder()
		reqHist, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/tickets/%d/status_history", accountID, waitingTicketID), nil)
		reqHist.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wHist, reqHist)

		if wHist.Code != http.StatusOK {
			t.Fatalf("expected 200 on status_history, got %d body: %s", wHist.Code, wHist.Body.String())
		}

		var respHist struct {
			Data []domain.TicketStatusHistory `json:"data"`
		}
		_ = json.Unmarshal(wHist.Body.Bytes(), &respHist)
		if len(respHist.Data) < 2 {
			t.Fatalf("expected at least 2 status history entries (created, wait), got %d", len(respHist.Data))
		}

		// Initial entry
		first := respHist.Data[0]
		if first.ToStatus != "open" {
			t.Errorf("expected first to_status 'open', got %s", first.ToStatus)
		}

		// Waiting transition entry
		latest := respHist.Data[len(respHist.Data)-1]
		if latest.FromStatus != "open" || latest.ToStatus != "pending" {
			t.Errorf("expected transition open -> pending, got %s -> %s", latest.FromStatus, latest.ToStatus)
		}
		if latest.WaitingReason != "等待客户补充快递单号与包装照片" {
			t.Errorf("unexpected waiting_reason in history: %s", latest.WaitingReason)
		}
	})

	// 12. Single and Batch Reminders
	t.Run("Scenario 12: Ticket Reminder and Batch Reminder Lifecycle", func(t *testing.T) {
		// Single Remind
		remindBody, _ := json.Marshal(map[string]interface{}{
			"message": "温馨提醒：请尽快提供退货单号以加速退款办理",
		})
		wRemind := httptest.NewRecorder()
		reqRemind, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/tickets/%d/remind", accountID, waitingTicketID), bytes.NewBuffer(remindBody))
		reqRemind.Header.Set("Content-Type", "application/json")
		reqRemind.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wRemind, reqRemind)

		if wRemind.Code != http.StatusOK {
			t.Fatalf("expected 200 on remind, got %d body: %s", wRemind.Code, wRemind.Body.String())
		}

		var remindResp struct {
			Data          domain.Ticket `json:"data"`
			ReminderCount int           `json:"reminder_count"`
		}
		_ = json.Unmarshal(wRemind.Body.Bytes(), &remindResp)
		if remindResp.ReminderCount != 1 || remindResp.Data.ReminderCount != 1 {
			t.Errorf("expected reminder_count 1, got %d", remindResp.ReminderCount)
		}
		if remindResp.Data.LastRemindedAt == nil {
			t.Errorf("expected last_reminded_at to be updated")
		}

		// Batch Remind across account
		batchBody, _ := json.Marshal(map[string]interface{}{
			"message": "批量催办：系统提醒您补充工单资料",
		})
		wBatch := httptest.NewRecorder()
		reqBatch, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/tickets/batch_remind", accountID), bytes.NewBuffer(batchBody))
		reqBatch.Header.Set("Content-Type", "application/json")
		reqBatch.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wBatch, reqBatch)

		if wBatch.Code != http.StatusOK {
			t.Fatalf("expected 200 on batch_remind, got %d body: %s", wBatch.Code, wBatch.Body.String())
		}

		var batchResp struct {
			RemindedCount int    `json:"reminded_count"`
			TicketIDs     []uint `json:"ticket_ids"`
		}
		_ = json.Unmarshal(wBatch.Body.Bytes(), &batchResp)
		if batchResp.RemindedCount < 1 {
			t.Errorf("expected reminded_count >= 1, got %d", batchResp.RemindedCount)
		}

		// Verify reminder count incremented again
		var checkTicket domain.Ticket
		db.First(&checkTicket, waitingTicketID)
		if checkTicket.ReminderCount != 2 {
			t.Errorf("expected reminder_count 2 after batch remind, got %d", checkTicket.ReminderCount)
		}
	})

	// 13. Ticket Resume back to open
	t.Run("Scenario 13: Ticket Resume back to Open Status", func(t *testing.T) {
		resumeBody, _ := json.Marshal(map[string]interface{}{
			"reason":  "客户已回复寄回快递单号 SF123456789",
			"comment": "单号核验有效，工单恢复正常处理",
		})
		wResume := httptest.NewRecorder()
		reqResume, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/tickets/%d/resume", accountID, waitingTicketID), bytes.NewBuffer(resumeBody))
		reqResume.Header.Set("Content-Type", "application/json")
		reqResume.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wResume, reqResume)

		if wResume.Code != http.StatusOK {
			t.Fatalf("expected 200 on resume, got %d body: %s", wResume.Code, wResume.Body.String())
		}

		var resumeResp struct {
			Data domain.Ticket `json:"data"`
		}
		_ = json.Unmarshal(wResume.Body.Bytes(), &resumeResp)
		if resumeResp.Data.Status != "open" {
			t.Errorf("expected status 'open' after resume, got %s", resumeResp.Data.Status)
		}
		if resumeResp.Data.StatusReason != "客户已回复寄回快递单号 SF123456789" {
			t.Errorf("unexpected status reason: %s", resumeResp.Data.StatusReason)
		}

		// Verify status history now has 3 records
		wHist := httptest.NewRecorder()
		reqHist, _ := http.NewRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/tickets/%d/status_history", accountID, waitingTicketID), nil)
		reqHist.Header.Set("Authorization", "Bearer "+token)
		engine.ServeHTTP(wHist, reqHist)

		var respHist struct {
			Data []domain.TicketStatusHistory `json:"data"`
		}
		_ = json.Unmarshal(wHist.Body.Bytes(), &respHist)
		if len(respHist.Data) < 3 {
			t.Fatalf("expected at least 3 status history entries, got %d", len(respHist.Data))
		}
		latest := respHist.Data[len(respHist.Data)-1]
		if latest.FromStatus != "pending" || latest.ToStatus != "open" {
			t.Errorf("expected pending -> open transition in latest history, got %s -> %s", latest.FromStatus, latest.ToStatus)
		}
	})
}
