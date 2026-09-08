package ws_test

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestWebSocket_AgentBroadcasting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "secret-key-ws-testing-1234567890",
		JWTExpirationHours: 24,
	}

	db, _ := database.InitDB(cfg)
	userRepo := repository.NewUserRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	inboxRepo := repository.NewInboxRepository(db)

	agent := domain.User{
		Name:         "Agent",
		Email:        "agent@ws.com",
		PasswordHash: "dummy",
		Role:         domain.RoleAgent,
	}
	_ = userRepo.Create(&agent)

	account := domain.Account{Name: "WS Account"}
	_ = accountRepo.Create(&account)
	_ = accountRepo.AddMember(account.ID, agent.ID, domain.RoleAdministrator)

	token, _ := auth.GenerateToken(&agent, cfg.JWTSecret, 24)

	hub := ws.NewHub()
	go hub.Run()

	r := gin.New()
	r.GET("/cable", ws.ServeWS(hub, cfg, userRepo, accountRepo, inboxRepo))

	server := httptest.NewServer(r)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/cable?token=" + token + "&account_id=1"

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		if strings.Contains(err.Error(), "operation not permitted") {
			t.Skipf("skipping live websocket dial due to sandbox network restrictions: %v", err)
			return
		}
		t.Fatalf("failed to dial websocket: %v", err)
	}
	defer conn.Close()

	// Wait for registration
	time.Sleep(50 * time.Millisecond)

	// Broadcast an event
	hub.Broadcast(&ws.Event{
		Name:           ws.EventMessageCreated,
		AccountID:      1,
		ConversationID: 10,
		Data: map[string]string{
			"content": "Hello via realtime!",
		},
	})

	// Read message from websocket
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, p, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read from websocket: %v", err)
	}

	var received ws.Event
	if err := json.Unmarshal(p, &received); err != nil {
		t.Fatalf("failed to parse received event json: %v", err)
	}

	if received.Name != ws.EventMessageCreated || received.AccountID != 1 {
		t.Fatalf("unexpected event: %+v", received)
	}
}
