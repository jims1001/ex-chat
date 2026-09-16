package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/handler"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupTicketSpecTestEnv(t *testing.T) (*gin.Engine, *gorm.DB, domain.Account, domain.User, domain.Contact, domain.Conversation) {
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("failed to open sqlite in memory: %v", err)
	}

	if err := db.AutoMigrate(
		&domain.Account{},
		&domain.User{},
		&domain.AccountUser{},
		&domain.Contact{},
		&domain.Conversation{},
		&domain.Ticket{},
		&domain.TicketComment{},
		&domain.TicketActivity{},
		&domain.TicketStatusHistory{},
		&domain.TicketAttachment{},
		&domain.LocalChangeJournal{},
		&domain.AuditLog{},
	); err != nil {
		t.Fatalf("failed to automigrate: %v", err)
	}

	acc := domain.Account{Name: "Ticket Spec Tenant"}
	db.Create(&acc)

	user := domain.User{Email: "agent@example.com", Name: "Service Agent"}
	db.Create(&user)

	contact := domain.Contact{AccountID: acc.ID, Name: "Customer VIP", Email: "vip@example.com"}
	db.Create(&contact)

	conv := domain.Conversation{
		AccountID: acc.ID,
		ContactID: contact.ID,
		Status:    domain.ConversationStatusOpen,
	}
	db.Create(&conv)

	ticketRepo := repository.NewTicketRepository(db)
	ticketHandler := handler.NewTicketHandler(ticketRepo)

	r := gin.New()
	tenant := r.Group("/api/v1/accounts/:account_id")
	{
		tenant.GET("/tickets", ticketHandler.List)
		tenant.POST("/tickets", ticketHandler.Create)
		tenant.GET("/tickets/search", ticketHandler.Search)
		tenant.GET("/tickets/stats", ticketHandler.Stats)
		tenant.POST("/tickets/batch_remind", ticketHandler.BatchRemind)
		tenant.GET("/tickets/:id", ticketHandler.Get)
		tenant.PATCH("/tickets/:id", ticketHandler.Update)
		tenant.PUT("/tickets/:id", ticketHandler.Update)
		tenant.DELETE("/tickets/:id", ticketHandler.Delete)
		tenant.POST("/tickets/:id/status", ticketHandler.UpdateStatus)
		tenant.POST("/tickets/:id/wait", ticketHandler.Wait)
		tenant.POST("/tickets/:id/resume", ticketHandler.Resume)
		tenant.POST("/tickets/:id/remind", ticketHandler.Remind)
		tenant.GET("/tickets/:id/status_history", ticketHandler.ListStatusHistory)
		tenant.POST("/tickets/:id/assign", ticketHandler.Assign)
		tenant.GET("/tickets/:id/comments", ticketHandler.ListComments)
		tenant.POST("/tickets/:id/comments", ticketHandler.CreateComment)
		tenant.GET("/tickets/:id/attachments", ticketHandler.ListAttachments)
		tenant.POST("/tickets/:id/attachments", ticketHandler.CreateAttachment)
		tenant.DELETE("/tickets/:id/attachments/:attachment_id", ticketHandler.DeleteAttachment)
		tenant.GET("/tickets/:id/activities", ticketHandler.ListActivities)

		tenant.GET("/conversations/:conversation_id/tickets", ticketHandler.ListByConversation)
		tenant.POST("/conversations/:conversation_id/tickets", ticketHandler.CreateForConversation)
		tenant.GET("/contacts/:contact_id/tickets", ticketHandler.ListByContact)
		tenant.POST("/contacts/:contact_id/tickets", ticketHandler.CreateForContact)
	}

	return r, db, acc, user, contact, conv
}

func TestTicketSystemFull10Capabilities(t *testing.T) {
	engine, _, acc, user, contact, conv := setupTicketSpecTestEnv(t)
	accountBase := fmt.Sprintf("/api/v1/accounts/%d", acc.ID)

	var createdTicketID uint
	var createdTicketNum string

	// Capability 3: 人工创建工单 (Create)
	t.Run("1. Manual Create Ticket", func(t *testing.T) {
		body := map[string]interface{}{
			"title":          "API Webhook 500 告警追踪",
			"description":    "第三方调用系统返回 500 内部错误，需要排查日志",
			"priority":       "urgent",
			"assigned_group": "技术支持",
			"contact_id":     contact.ID,
			"waiting_reason": "",
		}
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, accountBase+"/tickets", bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}
		var resp struct {
			Data domain.Ticket `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Data.ID == 0 || resp.Data.TicketNumber == "" {
			t.Fatalf("expected valid ticket created, got %+v", resp.Data)
		}
		createdTicketID = resp.Data.ID
		createdTicketNum = resp.Data.TicketNumber
		t.Logf("Created Ticket: %d (%s)", createdTicketID, createdTicketNum)
	})

	// Capability 2: 工单详情 (Detail with relations)
	t.Run("2. Ticket Detail View", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/tickets/%d", accountBase, createdTicketID), nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var resp struct {
			Data domain.Ticket `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Data.Title != "API Webhook 500 告警追踪" {
			t.Fatalf("unexpected title: %s", resp.Data.Title)
		}
		if resp.Data.Contact == nil || resp.Data.Contact.Name != contact.Name {
			t.Fatalf("expected contact preloaded, got %v", resp.Data.Contact)
		}
	})

	// Capability 1: 工单列表、搜索和筛选 (List, Search and Filter)
	t.Run("3. List, Search and Filter", func(t *testing.T) {
		// Filter by status=open
		req, _ := http.NewRequest(http.MethodGet, accountBase+"/tickets?status=open&priority=urgent", nil)
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		// Search
		reqSearch, _ := http.NewRequest(http.MethodGet, accountBase+"/tickets/search?q=Webhook", nil)
		wSearch := httptest.NewRecorder()
		engine.ServeHTTP(wSearch, reqSearch)
		if wSearch.Code != http.StatusOK {
			t.Fatalf("expected 200 for search, got %d", wSearch.Code)
		}
		var sResp struct {
			Data []domain.Ticket `json:"data"`
		}
		_ = json.Unmarshal(wSearch.Body.Bytes(), &sResp)
		if len(sResp.Data) == 0 {
			t.Fatalf("expected search to find ticket with 'Webhook'")
		}
	})

	// Capability 5: 优先级、团队、负责人分配 (Assign and Update)
	t.Run("4. Assign Team and Agent", func(t *testing.T) {
		body := map[string]interface{}{
			"assigned_group": "平台研发团队",
			"assignee_id":    user.ID,
		}
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/tickets/%d/assign", accountBase, createdTicketID), bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var resp struct {
			Data domain.Ticket `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Data.AssignedGroup != "平台研发团队" || resp.Data.AssigneeID == nil || *resp.Data.AssigneeID != user.ID {
			t.Fatalf("expected assignment updated, got group=%s, assignee=%v", resp.Data.AssignedGroup, resp.Data.AssigneeID)
		}
	})

	// Capability 6: 工单评论和内部备注 (Comments & Notes)
	t.Run("5. Comments and Internal Notes", func(t *testing.T) {
		isPrivate := true
		body := map[string]interface{}{
			"content":    "内部备注：已联系基础设施组核对错误日志",
			"is_private": &isPrivate,
		}
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/tickets/%d/comments", accountBase, createdTicketID), bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}

		// List comments
		reqList, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/tickets/%d/comments", accountBase, createdTicketID), nil)
		wList := httptest.NewRecorder()
		engine.ServeHTTP(wList, reqList)
		if wList.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", wList.Code)
		}
		var cResp struct {
			Data []domain.TicketComment `json:"data"`
		}
		_ = json.Unmarshal(wList.Body.Bytes(), &cResp)
		if len(cResp.Data) != 1 || !cResp.Data[0].IsPrivate {
			t.Fatalf("expected 1 private comment, got %+v", cResp.Data)
		}
	})

	// Capability 7: 工单附件 (Attachments)
	var createdAttID uint
	t.Run("6. Ticket Attachments", func(t *testing.T) {
		body := map[string]interface{}{
			"file_name": "error_log_20260912.txt",
			"file_type": "text/plain",
			"file_size": 2048,
			"data_url":  "data:text/plain;base64,ZXJyb3IgbG9nIGNvbnRlbnQ=",
		}
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/tickets/%d/attachments", accountBase, createdTicketID), bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}

		var attResp struct {
			Data domain.TicketAttachment `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &attResp)
		if attResp.Data.ID == 0 || attResp.Data.FileName != "error_log_20260912.txt" {
			t.Fatalf("expected attachment created, got %+v", attResp.Data)
		}
		createdAttID = attResp.Data.ID

		// List attachments
		reqList, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/tickets/%d/attachments", accountBase, createdTicketID), nil)
		wList := httptest.NewRecorder()
		engine.ServeHTTP(wList, reqList)
		if wList.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", wList.Code)
		}
	})

	// Capability 4 & 8: 工单状态流转、等待挂起与催办提醒 (Status transition, SLA & Remind)
	t.Run("7. Status Transition and Remind", func(t *testing.T) {
		// Wait ticket
		waitBody := map[string]interface{}{
			"waiting_reason": "等待客户提供复现环境和报错参数",
			"reason":         "客户环境排查",
		}
		dataWait, _ := json.Marshal(waitBody)
		reqWait, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/tickets/%d/wait", accountBase, createdTicketID), bytes.NewReader(dataWait))
		reqWait.Header.Set("Content-Type", "application/json")
		wWait := httptest.NewRecorder()
		engine.ServeHTTP(wWait, reqWait)
		if wWait.Code != http.StatusOK {
			t.Fatalf("expected 200 for wait, got %d: %s", wWait.Code, wWait.Body.String())
		}

		// Remind
		remindBody := map[string]interface{}{
			"message": "温馨提醒：请补充测试参数以协助我们排查",
		}
		dataRemind, _ := json.Marshal(remindBody)
		reqRemind, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/tickets/%d/remind", accountBase, createdTicketID), bytes.NewReader(dataRemind))
		reqRemind.Header.Set("Content-Type", "application/json")
		wRemind := httptest.NewRecorder()
		engine.ServeHTTP(wRemind, reqRemind)
		if wRemind.Code != http.StatusOK {
			t.Fatalf("expected 200 for remind, got %d", wRemind.Code)
		}

		// Resume
		resumeBody := map[string]interface{}{
			"reason": "客户已提供参数，恢复处理",
		}
		dataResume, _ := json.Marshal(resumeBody)
		reqResume, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/tickets/%d/resume", accountBase, createdTicketID), bytes.NewReader(dataResume))
		reqResume.Header.Set("Content-Type", "application/json")
		wResume := httptest.NewRecorder()
		engine.ServeHTTP(wResume, reqResume)
		if wResume.Code != http.StatusOK {
			t.Fatalf("expected 200 for resume, got %d", wResume.Code)
		}

		// Resolve
		statusBody := map[string]interface{}{
			"status": "resolved",
			"reason": "已修复配置并验证通过",
		}
		dataStatus, _ := json.Marshal(statusBody)
		reqStatus, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/tickets/%d/status", accountBase, createdTicketID), bytes.NewReader(dataStatus))
		reqStatus.Header.Set("Content-Type", "application/json")
		wStatus := httptest.NewRecorder()
		engine.ServeHTTP(wStatus, reqStatus)
		if wStatus.Code != http.StatusOK {
			t.Fatalf("expected 200 for resolve, got %d", wStatus.Code)
		}
	})

	// Capability 9: 工单操作历史与审计 (Activities and Status Histories)
	t.Run("8. Activities and Status History Trail", func(t *testing.T) {
		reqAct, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/tickets/%d/activities", accountBase, createdTicketID), nil)
		wAct := httptest.NewRecorder()
		engine.ServeHTTP(wAct, reqAct)
		if wAct.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", wAct.Code)
		}

		reqHist, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/tickets/%d/status_history", accountBase, createdTicketID), nil)
		wHist := httptest.NewRecorder()
		engine.ServeHTTP(wHist, reqHist)
		if wHist.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", wHist.Code)
		}
		var hResp struct {
			Data []domain.TicketStatusHistory `json:"data"`
		}
		_ = json.Unmarshal(wHist.Body.Bytes(), &hResp)
		if len(hResp.Data) < 3 {
			t.Fatalf("expected at least 3 status history records, got %d", len(hResp.Data))
		}
	})

	// Capability 10: 会话转工单、工单关联会话 (Conversation/Contact Sub-resources)
	t.Run("9. Convert Conversation to Ticket", func(t *testing.T) {
		body := map[string]interface{}{
			"title":       "由会话 #10842 转交的工单",
			"description": "客户在咨询中提出的开票抬头变更请求",
			"priority":    "medium",
			"contact_id":  contact.ID,
		}
		data, _ := json.Marshal(body)
		req, _ := http.NewRequest(http.MethodPost, fmt.Sprintf("%s/conversations/%d/tickets", accountBase, conv.ID), bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
		}

		// Query tickets for conversation
		reqList, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/conversations/%d/tickets", accountBase, conv.ID), nil)
		wList := httptest.NewRecorder()
		engine.ServeHTTP(wList, reqList)
		if wList.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", wList.Code)
		}
		var convTicketsResp struct {
			Data []domain.Ticket `json:"data"`
		}
		_ = json.Unmarshal(wList.Body.Bytes(), &convTicketsResp)
		if len(convTicketsResp.Data) != 1 || convTicketsResp.Data[0].ConversationID == nil || *convTicketsResp.Data[0].ConversationID != conv.ID {
			t.Fatalf("expected 1 conversation ticket, got %+v", convTicketsResp.Data)
		}

		// Query tickets for contact
		reqContact, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/contacts/%d/tickets", accountBase, contact.ID), nil)
		wContact := httptest.NewRecorder()
		engine.ServeHTTP(wContact, reqContact)
		if wContact.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", wContact.Code)
		}
	})

	// Capability 3 (cont): 删除附件与删除工单 (Delete)
	t.Run("10. Delete Attachment and Ticket", func(t *testing.T) {
		// Delete attachment
		reqDelAtt, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/tickets/%d/attachments/%d", accountBase, createdTicketID, createdAttID), nil)
		wDelAtt := httptest.NewRecorder()
		engine.ServeHTTP(wDelAtt, reqDelAtt)
		if wDelAtt.Code != http.StatusOK {
			t.Fatalf("expected 200 for attachment delete, got %d", wDelAtt.Code)
		}

		// Delete ticket
		reqDel, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/tickets/%d", accountBase, createdTicketID), nil)
		wDel := httptest.NewRecorder()
		engine.ServeHTTP(wDel, reqDel)
		if wDel.Code != http.StatusOK {
			t.Fatalf("expected 200 for ticket delete, got %d", wDel.Code)
		}

		// Verify 404
		reqCheck, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/tickets/%d", accountBase, createdTicketID), nil)
		wCheck := httptest.NewRecorder()
		engine.ServeHTTP(wCheck, reqCheck)
		if wCheck.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for deleted ticket, got %d", wCheck.Code)
		}
	})
}
