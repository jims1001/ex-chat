package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

// wsRec is a thread-safe recorder for WebSocket events captured via hub.OnBroadcast.
type wsRec struct {
	mu     sync.Mutex
	events []*ws.Event
}

func (e *wsRec) add(ev *ws.Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, ev)
}

func (e *wsRec) names() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.events))
	for _, ev := range e.events {
		out = append(out, ev.Name)
	}
	return out
}

func (e *wsRec) has(name string) bool {
	for _, n := range e.names() {
		if n == name {
			return true
		}
	}
	return false
}

// hook wires hub.OnBroadcast to capture events and returns a cleanup func.
func (e *wsRec) hook(hub *ws.Hub) func() {
	e.mu.Lock()
	e.events = nil
	e.mu.Unlock()
	hub.OnBroadcast = func(ev *ws.Event) { e.add(ev) }
	return func() { hub.OnBroadcast = nil }
}

// TestClientStateSync verifies that after edit, delete, mark-read, and assign operations
// all clients are correctly notified via WebSocket and DB state is consistent.
func TestClientStateSync(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_client_state_sync_123456",
		JWTExpirationHours: 72,
	}
	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()
	r := router.SetupRouter(cfg, db, hub)

	// ── Fixtures ─────────────────────────────────────────────────────────────
	account := domain.Account{Name: "SyncTestAccount"}
	db.Create(&account)
	accID := account.ID
	accStr := fmt.Sprintf("%d", accID)

	inbox := domain.Inbox{AccountID: accID, Name: "SyncInbox", ChannelType: "api"}
	db.Create(&inbox)

	// Create admin (JWT issuer) + agent2 (assignee)
	admin := domain.User{
		Email:        "admin@sync.test",
		Name:         "Sync Admin",
		Role:         domain.RoleAdministrator,
		PasswordHash: "$2a$10$abcdefghijklmnopqrstuvwxyz123456",
	}
	agent2 := domain.User{
		Email:        "bob@sync.test",
		Name:         "Bob Agent",
		Role:         domain.RoleAgent,
		PasswordHash: "$2a$10$abcdefghijklmnopqrstuvwxyz123456",
	}
	db.Create(&admin)
	db.Create(&agent2)
	db.Create(&domain.AccountUser{AccountID: accID, UserID: admin.ID, Role: domain.RoleAdministrator, Availability: "online"})
	db.Create(&domain.AccountUser{AccountID: accID, UserID: agent2.ID, Role: domain.RoleAgent, Availability: "online"})

	contact := domain.Contact{AccountID: accID, Name: "Visitor", Email: "visitor@sync.test"}
	db.Create(&contact)

	conv := domain.Conversation{
		AccountID: accID,
		InboxID:   inbox.ID,
		ContactID: contact.ID,
		Status:    domain.ConversationStatusOpen,
	}
	db.Create(&conv)
	convStr := fmt.Sprintf("%d", conv.ID)

	// Outgoing message for edit/delete tests
	msg := domain.Message{
		AccountID:      accID,
		ConversationID: conv.ID,
		Content:        "original content",
		MessageType:    domain.MessageTypeOutgoing,
		ContentType:    "text",
		SenderType:     "User",
		SenderID:       admin.ID,
	}
	db.Create(&msg)

	// JWT token for admin
	token, _ := auth.GenerateToken(&admin, cfg.JWTSecret, 24)
	bearer := "Bearer " + token

	// Helper: authenticated request with JSON body
	doReq := func(method, path string, body any) *httptest.ResponseRecorder {
		var buf bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&buf).Encode(body)
		}
		req, _ := http.NewRequest(method, path, &buf)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", bearer)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	rec := &wsRec{}

	// ═══════════════════════════════════════════════════════════════════════
	// Scenario 1: 消息编辑 (UpdateMessage)
	//   PUT /api/v1/accounts/:id/conversations/:id/messages/:message_id
	//   → HTTP 200, updated content in response body and in DB
	//   → WS: message.updated broadcast
	//   → WS: NO message.deleted event
	// ═══════════════════════════════════════════════════════════════════════
	t.Run("1_Edit_Message_Broadcasts_WS", func(t *testing.T) {
		defer rec.hook(hub)()

		w := doReq(http.MethodPut,
			fmt.Sprintf("/api/v1/accounts/%s/conversations/%s/messages/%d", accStr, convStr, msg.ID),
			map[string]any{"content": "edited content"},
		)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var resp struct {
			Data struct{ Content string `json:"content"` } `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		if resp.Data.Content != "edited content" {
			t.Errorf("response content mismatch: got %q", resp.Data.Content)
		}

		time.Sleep(30 * time.Millisecond) // let hub goroutine process

		if !rec.has(ws.EventMessageUpdated) {
			t.Errorf("expected message.updated WS event; got %v", rec.names())
		}
		if rec.has(ws.EventMessageDeleted) {
			t.Errorf("edit must NOT produce message.deleted; got %v", rec.names())
		}

		// DB persists new content
		var dbMsg domain.Message
		db.First(&dbMsg, msg.ID)
		if dbMsg.Content != "edited content" {
			t.Errorf("DB content not updated; got %q", dbMsg.Content)
		}
		if dbMsg.Deleted {
			t.Errorf("Deleted flag must remain false after edit")
		}
	})

	// ═══════════════════════════════════════════════════════════════════════
	// Scenario 2: 消息删除 (DeleteMessage)
	//   DELETE /api/v1/accounts/:id/conversations/:id/messages/:message_id
	//   → HTTP 200
	//   → WS: message.deleted AND message.updated (dual broadcast for cross-client compat)
	//   → DB: Deleted=true
	//   → Re-delete: 404; Edit after delete: 400
	// ═══════════════════════════════════════════════════════════════════════
	t.Run("2_Delete_Message_Dual_WS_Broadcast", func(t *testing.T) {
		delMsg := domain.Message{
			AccountID:      accID,
			ConversationID: conv.ID,
			Content:        "to be deleted",
			MessageType:    domain.MessageTypeOutgoing,
			ContentType:    "text",
			SenderType:     "User",
			SenderID:       admin.ID,
		}
		db.Create(&delMsg)

		defer rec.hook(hub)()

		w := doReq(http.MethodDelete,
			fmt.Sprintf("/api/v1/accounts/%s/conversations/%s/messages/%d", accStr, convStr, delMsg.ID),
			nil,
		)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		time.Sleep(30 * time.Millisecond)

		// Both events required for cross-client compatibility
		if !rec.has(ws.EventMessageDeleted) {
			t.Errorf("expected message.deleted WS event; got %v", rec.names())
		}
		if !rec.has(ws.EventMessageUpdated) {
			t.Errorf("expected message.updated WS event on delete (compat); got %v", rec.names())
		}

		// DB soft-delete
		var dbMsg domain.Message
		db.Unscoped().First(&dbMsg, delMsg.ID)
		if !dbMsg.Deleted {
			t.Errorf("expected Deleted=true in DB after delete")
		}

		// Guard: re-delete 404
		w2 := doReq(http.MethodDelete,
			fmt.Sprintf("/api/v1/accounts/%s/conversations/%s/messages/%d", accStr, convStr, delMsg.ID),
			nil,
		)
		if w2.Code != http.StatusNotFound {
			t.Errorf("re-delete should 404, got %d", w2.Code)
		}

		// Guard: edit deleted msg 400
		w3 := doReq(http.MethodPut,
			fmt.Sprintf("/api/v1/accounts/%s/conversations/%s/messages/%d", accStr, convStr, delMsg.ID),
			map[string]any{"content": "attempt"},
		)
		if w3.Code != http.StatusBadRequest {
			t.Errorf("editing deleted msg should 400, got %d", w3.Code)
		}
	})

	// ═══════════════════════════════════════════════════════════════════════
	// Scenario 3: 已读同步 (UpdateLastSeen / MarkUnread)
	//   POST .../update_last_seen → AgentLastSeenAt set in DB; notification marked read
	//   POST .../unread           → UnreadCount > 0 in DB
	// ═══════════════════════════════════════════════════════════════════════
	t.Run("3_MarkRead_MarkUnread_DB_State", func(t *testing.T) {
		notif := domain.Notification{
			AccountID:        accID,
			UserID:           admin.ID,
			NotificationType: domain.NotificationTypeConversationAssignment,
			PrimaryActorType: "Conversation",
			PrimaryActorID:   conv.ID,
		}
		db.Create(&notif)

		// Mark read
		w := doReq(http.MethodPost,
			fmt.Sprintf("/api/v1/accounts/%s/conversations/%s/update_last_seen", accStr, convStr),
			map[string]any{"agent_last_seen_at": time.Now().UTC()},
		)
		if w.Code != http.StatusOK {
			t.Fatalf("UpdateLastSeen expected 200, got %d: %s", w.Code, w.Body.String())
		}

		// Notification for admin on this conversation must be marked read
		var dbNotif domain.Notification
		db.First(&dbNotif, notif.ID)
		if dbNotif.ReadAt == nil {
			t.Errorf("expected notification ReadAt to be set after UpdateLastSeen")
		}

		// DB conversation AgentLastSeenAt must be set
		var dbConv domain.Conversation
		db.First(&dbConv, conv.ID)
		if dbConv.AgentLastSeenAt == nil {
			t.Errorf("expected AgentLastSeenAt to be set in DB after UpdateLastSeen")
		}

		// Mark unread
		w2 := doReq(http.MethodPost,
			fmt.Sprintf("/api/v1/accounts/%s/conversations/%s/unread", accStr, convStr),
			nil,
		)
		if w2.Code != http.StatusOK {
			t.Fatalf("MarkUnread expected 200, got %d: %s", w2.Code, w2.Body.String())
		}

		db.First(&dbConv, conv.ID)
		if dbConv.UnreadCount == 0 {
			t.Errorf("expected UnreadCount > 0 after MarkUnread, got %d", dbConv.UnreadCount)
		}
	})

	// ═══════════════════════════════════════════════════════════════════════
	// Scenario 4: 坐席指派 (Assign)
	//   POST .../assignments { assignee_id: agent2.ID }
	//   → HTTP 200
	//   → WS: conversation.assigned AND conversation.updated
	//   → DB: assignee_id = agent2.ID
	//   → In-App Notification created for agent2
	// ═══════════════════════════════════════════════════════════════════════
	t.Run("4_Assign_WS_Events_And_Notification", func(t *testing.T) {
		defer rec.hook(hub)()

		// Clean pre-existing notifications for agent2
		db.Where("user_id = ? AND account_id = ?", agent2.ID, accID).Delete(&domain.Notification{})

		w := doReq(http.MethodPost,
			fmt.Sprintf("/api/v1/accounts/%s/conversations/%s/assignments", accStr, convStr),
			map[string]any{"assignee_id": agent2.ID},
		)
		if w.Code != http.StatusOK {
			t.Fatalf("Assign expected 200, got %d: %s", w.Code, w.Body.String())
		}

		time.Sleep(30 * time.Millisecond)

		// Both WS events required
		if !rec.has(ws.EventConversationAssigned) {
			t.Errorf("expected conversation.assigned WS event; got %v", rec.names())
		}
		if !rec.has(ws.EventConversationUpdated) {
			t.Errorf("expected conversation.updated WS event on assign; got %v", rec.names())
		}

		// DB: assignee updated
		var dbConv domain.Conversation
		db.First(&dbConv, conv.ID)
		if dbConv.AssigneeID == nil || *dbConv.AssigneeID != agent2.ID {
			t.Errorf("expected DB AssigneeID=%d, got %v", agent2.ID, dbConv.AssigneeID)
		}

		// In-App Notification created for agent2
		var notifCount int64
		db.Model(&domain.Notification{}).
			Where("user_id = ? AND account_id = ? AND notification_type = ? AND primary_actor_id = ?",
				agent2.ID, accID, domain.NotificationTypeConversationAssignment, conv.ID).
			Count(&notifCount)
		if notifCount == 0 {
			t.Errorf("expected in-app notification for assignee agent2, found 0")
		}
	})

	// ═══════════════════════════════════════════════════════════════════════
	// Scenario 5: 取消指派 (Unassign, assignee_id: null)
	//   → WS: conversation.updated (but NOT conversation.assigned)
	//   → DB: assignee_id = NULL
	// ═══════════════════════════════════════════════════════════════════════
	t.Run("5_Unassign_Broadcasts_Conversation_Updated_Only", func(t *testing.T) {
		defer rec.hook(hub)()

		w := doReq(http.MethodPost,
			fmt.Sprintf("/api/v1/accounts/%s/conversations/%s/assignments", accStr, convStr),
			map[string]any{"assignee_id": nil},
		)
		if w.Code != http.StatusOK {
			t.Fatalf("Unassign expected 200, got %d: %s", w.Code, w.Body.String())
		}

		time.Sleep(30 * time.Millisecond)

		if !rec.has(ws.EventConversationUpdated) {
			t.Errorf("expected conversation.updated on unassign; got %v", rec.names())
		}
		// unassign must NOT produce conversation.assigned (no assignee_id in req)
		if rec.has(ws.EventConversationAssigned) {
			t.Errorf("unassign must NOT produce conversation.assigned event; got %v", rec.names())
		}

		// DB: assignee cleared
		var dbConv domain.Conversation
		db.First(&dbConv, conv.ID)
		if dbConv.AssigneeID != nil {
			t.Errorf("expected DB AssigneeID=nil after unassign, got %v", dbConv.AssigneeID)
		}
	})

	// ═══════════════════════════════════════════════════════════════════════
	// Scenario 6: 全量通知已读 (MarkAllNotificationsRead)
	//   POST .../notifications/read_all
	//   → All unread notifications for admin in DB have read_at set
	// ═══════════════════════════════════════════════════════════════════════
	t.Run("6_MarkAllRead_Clears_Unread_Notifications", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			db.Create(&domain.Notification{
				AccountID:        accID,
				UserID:           admin.ID,
				NotificationType: domain.NotificationTypeConversationMention,
				PrimaryActorType: "Conversation",
				PrimaryActorID:   conv.ID,
			})
		}

		w := doReq(http.MethodPost,
			fmt.Sprintf("/api/v1/accounts/%s/notifications/read_all", accStr),
			nil,
		)
		if w.Code != http.StatusOK {
			t.Fatalf("MarkAllRead expected 200, got %d: %s", w.Code, w.Body.String())
		}

		var unread int64
		db.Model(&domain.Notification{}).
			Where("user_id = ? AND account_id = ? AND read_at IS NULL", admin.ID, accID).
			Count(&unread)
		if unread != 0 {
			t.Errorf("expected 0 unread notifications after MarkAllRead, got %d", unread)
		}
	})

	// ═══════════════════════════════════════════════════════════════════════
	// Scenario 7: WS Hub 路由校验 — 每次广播事件携带正确的 AccountID 和 ConversationID
	// ═══════════════════════════════════════════════════════════════════════
	t.Run("7_WS_Event_Carries_Correct_IDs", func(t *testing.T) {
		defer rec.hook(hub)()

		_ = doReq(http.MethodPut,
			fmt.Sprintf("/api/v1/accounts/%s/conversations/%s/messages/%d", accStr, convStr, msg.ID),
			map[string]any{"content": "routing check edit"},
		)

		time.Sleep(30 * time.Millisecond)

		var found *ws.Event
		for _, ev := range rec.events {
			if ev.Name == ws.EventMessageUpdated {
				found = ev
				break
			}
		}
		if found == nil {
			t.Fatalf("expected message.updated event; got %v", rec.names())
		}
		if found.AccountID != accID {
			t.Errorf("WS event AccountID mismatch: want %d, got %d", accID, found.AccountID)
		}
		if found.ConversationID != conv.ID {
			t.Errorf("WS event ConversationID mismatch: want %d, got %d", conv.ID, found.ConversationID)
		}
	})

	// ═══════════════════════════════════════════════════════════════════════
	// Scenario 8: WS Hub OnBroadcast 钩子独立校验
	//   Creates a separate hub, broadcasts two events, verifies hook fires for each.
	// ═══════════════════════════════════════════════════════════════════════
	t.Run("8_WS_Hub_OnBroadcast_Hook_Fires_For_Every_Event", func(t *testing.T) {
		testHub := ws.NewHub()
		go testHub.Run()

		var fired []*ws.Event
		var mu sync.Mutex
		testHub.OnBroadcast = func(ev *ws.Event) {
			mu.Lock()
			fired = append(fired, ev)
			mu.Unlock()
		}

		testHub.Broadcast(&ws.Event{
			Name:           ws.EventMessageCreated,
			AccountID:      accID,
			ConversationID: conv.ID,
			Data:           map[string]any{"test": true},
		})
		testHub.Broadcast(&ws.Event{
			Name:      ws.EventConversationUpdated,
			AccountID: accID,
		})

		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		n := len(fired)
		mu.Unlock()
		if n != 2 {
			t.Errorf("expected 2 events captured by OnBroadcast, got %d", n)
		}
	})
}
