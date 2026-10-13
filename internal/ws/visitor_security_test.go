package ws_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	"github.com/stretchr/testify/require"
)

func TestVisitorWebSocketRequiresOwnedSession(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })
	cfg := &config.Config{DBDriver: "sqlite", DBPath: ":memory:", JWTSecret: "visitor-websocket-test-key"}
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	account := domain.Account{Name: "Visitor sessions"}
	require.NoError(t, db.Create(&account).Error)
	inbox := domain.Inbox{AccountID: account.ID, Name: "Widget", WebsiteToken: "visitor-ws-site"}
	require.NoError(t, db.Create(&inbox).Error)
	contact := domain.Contact{AccountID: account.ID, Name: "Owner"}
	require.NoError(t, db.Create(&contact).Error)
	require.NoError(t, db.Create(&domain.ContactInbox{ContactID: contact.ID, InboxID: inbox.ID, SourceID: "ws-owner"}).Error)
	conversation := domain.Conversation{AccountID: account.ID, InboxID: inbox.ID, ContactID: contact.ID, Status: "open", DisplayID: 1}
	require.NoError(t, db.Create(&conversation).Error)
	session, err := auth.GenerateVisitorToken(inbox.ID, contact.ID, "ws-owner", cfg.JWTSecret, time.Hour)
	require.NoError(t, err)
	upload, err := auth.GenerateVisitorUploadToken(inbox.ID, contact.ID, "ws-owner", "blobs/x.txt", cfg.JWTSecret, 4, time.Minute)
	require.NoError(t, err)
	otherContact, err := auth.GenerateVisitorToken(inbox.ID, contact.ID+100, "other-owner", cfg.JWTSecret, time.Hour)
	require.NoError(t, err)
	otherInbox, err := auth.GenerateVisitorToken(inbox.ID+100, contact.ID, "ws-owner", cfg.JWTSecret, time.Hour)
	require.NoError(t, err)
	hub := ws.NewHub()
	go hub.Run()
	r := gin.New()
	r.GET("/ws", ws.ServeWS(hub, cfg, repository.NewUserRepository(db), repository.NewAccountRepository(db), repository.NewInboxRepository(db), repository.NewConversationRepository(db), repository.NewContactRepository(db), repository.NewChannelAuthEnterpriseRepository(db)))
	base := fmt.Sprintf("/ws?website_token=%s&conversation_id=%d", inbox.WebsiteToken, conversation.ID)
	for _, test := range []struct {
		name, query string
		status      int
	}{
		{"missing session", "", http.StatusForbidden},
		{"invalid session", "&visitor_token=invalid", http.StatusForbidden},
		{"upload capability", "&visitor_token=" + upload, http.StatusForbidden},
		{"other contact", "&visitor_token=" + otherContact, http.StatusForbidden},
		{"other inbox", "&visitor_token=" + otherInbox, http.StatusForbidden},
		{"precedence fails closed", "&visitor_session=invalid&visitor_token=" + session, http.StatusForbidden},
		{"raw source not accepted", "&contact_token=ws-owner", http.StatusForbidden},
		{"explicit empty primary token", "&visitor_session=&visitor_token=" + session, http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, base+test.query, nil))
			require.Equal(t, test.status, w.Code)
		})
	}
	for _, id := range []string{"invalid", "0"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ws?website_token="+inbox.WebsiteToken+"&visitor_token="+session+"&conversation_id="+id, nil))
		require.Equal(t, http.StatusBadRequest, w.Code)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ws?website_token="+inbox.WebsiteToken, nil))
	require.Equal(t, http.StatusForbidden, w.Code)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ws?website_token="+inbox.WebsiteToken+"&visitor_token="+session+"&conversation_id=99999", nil))
	require.Equal(t, http.StatusNotFound, w.Code)

	server := httptest.NewServer(r)
	t.Cleanup(server.Close)
	address := "ws" + strings.TrimPrefix(server.URL, "http") + base + "&visitor_token=" + url.QueryEscape(session)
	conn, response, err := websocket.DefaultDialer.Dial(address, nil)
	require.NoError(t, err, "a valid owned session must complete the websocket handshake")
	require.Equal(t, http.StatusSwitchingProtocols, response.StatusCode)
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	// The handler registers the connection immediately after the handshake.
	// Repeat a harmless broadcast until registration instead of relying on a sleep.
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				hub.Broadcast(&ws.Event{Name: ws.EventMessageCreated, AccountID: account.ID, ConversationID: conversation.ID, Data: "owned event"})
			}
		}
	}()
	var event ws.Event
	require.NoError(t, conn.ReadJSON(&event))
	require.Equal(t, conversation.ID, event.ConversationID)
	require.Equal(t, "owned event", event.Data)
	// Revocation is enforced on every new handshake.
	require.NoError(t, repository.NewChannelAuthEnterpriseRepository(db).RevokeToken(session, 0, time.Now().Add(time.Hour)))
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, base+"&visitor_token="+url.QueryEscape(session), nil))
	require.Equal(t, http.StatusUnauthorized, w.Code)

}
