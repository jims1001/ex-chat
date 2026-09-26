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
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func testInboxAgentBotBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	chnDB, extDB := openStore("CHN"), openStore("EXT")
	inboxes := []domain.Inbox{
		{AccountID: 1, Name: "Main", WebsiteToken: "bot-main"},
		{AccountID: 1, Name: "Second", WebsiteToken: "bot-second"},
		{AccountID: 2, Name: "Foreign", WebsiteToken: "bot-foreign"},
	}
	for i := range inboxes {
		if err := chnDB.Create(&inboxes[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	bots := []domain.AgentBot{
		{AccountID: 1, Name: "Owner bot", OutgoingURL: "https://example.test/owner"},
		{AccountID: 2, Name: "Foreign bot", OutgoingURL: "https://example.test/foreign"},
	}
	for i := range bots {
		if err := extDB.Create(&bots[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	inboxRepo := repository.NewInboxRepositoryWithAccountPort(chnDB, nil)
	botRepo := repository.NewAgentBotRepository(extDB)
	inboxHandler := handler.NewInboxHandler(inboxRepo, nil)
	inboxHandler.SetAgentBotRepo(botRepo)
	advanced := handler.NewAdvancedHandler(extDB, nil, nil, nil, botRepo, nil)
	advanced.SetInboxRepo(inboxRepo)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.ContextAccountID, uint(1))
		c.Next()
	})
	r.GET("/inboxes/:id/agent_bot", inboxHandler.GetInboxAgentBot)
	r.POST("/inboxes/:id/agent_bot", inboxHandler.SetInboxAgentBot)
	r.DELETE("/inboxes/:id/agent_bot", inboxHandler.UnsetInboxAgentBot)
	r.GET("/accounts/:account_id/agent_bots/:id", advanced.GetAgentBot)
	r.GET("/accounts/:account_id/agent_bots/:id/inboxes", advanced.GetAgentBotInboxes)
	r.POST("/accounts/:account_id/agent_bots/:id/inbox", advanced.ConnectAgentBotInbox)
	r.DELETE("/accounts/:account_id/agent_bots/:id/inbox/:inbox_id", advanced.DisconnectAgentBotInbox)
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	mainPath := fmt.Sprintf("/inboxes/%d/agent_bot", inboxes[0].ID)
	if w := call(http.MethodPost, mainPath, map[string]any{"agent_bot": bots[0].ID}); w.Code != 200 {
		t.Fatalf("inbox-side bind failed: %d %s", w.Code, w.Body.String())
	}
	if extDB.Migrator().HasTable(&domain.Inbox{}) || chnDB.Migrator().HasTable(&domain.AgentBot{}) || chnDB.Migrator().HasTable(&domain.AgentBotInbox{}) {
		t.Fatal("module created a foreign authority table")
	}
	w := call(http.MethodGet, mainPath, nil)
	var botResponse struct {
		Data *domain.AgentBot `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &botResponse); err != nil || w.Code != 200 || botResponse.Data == nil || botResponse.Data.ID != bots[0].ID {
		t.Fatalf("inbox-side read failed: %d %s err=%v", w.Code, w.Body.String(), err)
	}
	for _, tc := range []struct {
		path string
		bot  uint
	}{
		{mainPath, bots[1].ID},
		{fmt.Sprintf("/inboxes/%d/agent_bot", inboxes[2].ID), bots[0].ID},
	} {
		if w := call(http.MethodPost, tc.path, map[string]any{"agent_bot_id": tc.bot}); w.Code != 404 {
			t.Fatalf("cross-account bind accepted: %d %s", w.Code, w.Body.String())
		}
	}
	bound, err := botRepo.GetByInbox(t.Context(), 1, inboxes[0].ID)
	if err != nil || bound == nil || bound.ID != bots[0].ID {
		t.Fatalf("rejected request changed binding: bot=%v err=%v", bound, err)
	}
	if w := call(http.MethodDelete, mainPath, nil); w.Code != 200 {
		t.Fatalf("inbox-side unbind failed: %d %s", w.Code, w.Body.String())
	}
	if bot, err := botRepo.GetByInbox(t.Context(), 1, inboxes[0].ID); err != nil || bot != nil {
		t.Fatalf("binding remained: bot=%v err=%v", bot, err)
	}

	botPath := fmt.Sprintf("/accounts/1/agent_bots/%d", bots[0].ID)
	if w := call(http.MethodPost, botPath+"/inbox", map[string]any{"inbox_id": inboxes[1].ID}); w.Code != 200 {
		t.Fatalf("bot-side bind failed: %d %s", w.Code, w.Body.String())
	}
	for _, path := range []string{botPath, botPath + "/inboxes"} {
		if w := call(http.MethodGet, path, nil); w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"id":`+fmt.Sprint(inboxes[1].ID))) {
			t.Fatalf("bot-side composition failed: path=%s code=%d body=%s", path, w.Code, w.Body.String())
		}
	}
	if w := call(http.MethodPost, botPath+"/inbox", map[string]any{"inbox_id": inboxes[2].ID}); w.Code != 404 {
		t.Fatalf("bot-side foreign inbox accepted: %d %s", w.Code, w.Body.String())
	}
	if w := call(http.MethodDelete, botPath+"/inbox/"+fmt.Sprint(inboxes[1].ID), nil); w.Code != 200 {
		t.Fatalf("bot-side unbind failed: %d %s", w.Code, w.Body.String())
	}

	inboxHandler.SetAgentBotRepo(nil)
	if w := call(http.MethodGet, mainPath, nil); w.Code != 500 {
		t.Fatalf("missing EXT dependency hidden: %d %s", w.Code, w.Body.String())
	}
	inboxHandler.SetAgentBotRepo(botRepo)
	conn, err := extDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if w := call(http.MethodGet, mainPath, nil); w.Code != 500 {
		t.Fatalf("EXT outage masked: %d %s", w.Code, w.Body.String())
	}
	if found, err := inboxRepo.FindByWebsiteToken(inboxes[0].WebsiteToken); err != nil || found == nil {
		t.Fatalf("EXT outage broke CHN lookup: inbox=%v err=%v", found, err)
	}
}
