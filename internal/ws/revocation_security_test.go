package ws_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type failingWSRevocation struct {
	repository.ChannelIAMRepository
}

func (failingWSRevocation) IsTokenRevoked(string) (bool, error) {
	return false, errors.New("revocation store unavailable")
}

func TestWebSocketHandshakeRevocationErrorsAndDeletedUsers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{DBDriver: "sqlite", DBPath: ":memory:", JWTSecret: "websocket-scope-test-key"}
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	users := repository.NewUserRepository(db)
	accounts := repository.NewAccountRepository(db)
	inboxes := repository.NewInboxRepository(db)
	contacts := repository.NewContactRepository(db)
	convs := repository.NewConversationRepository(db)
	iam := repository.NewChannelAuthEnterpriseRepository(db)
	u := domain.User{Email: "live@example.test", Name: "Agent", Role: domain.RoleAgent}
	require.NoError(t, users.Create(&u))
	a := domain.Account{Name: "Owner"}
	require.NoError(t, accounts.Create(&a))
	require.NoError(t, accounts.AddMember(a.ID, u.ID, domain.RoleAgent))
	i := domain.Inbox{AccountID: a.ID, Name: "Widget", WebsiteToken: "scope-widget"}
	require.NoError(t, db.Create(&i).Error)
	c := domain.Contact{AccountID: a.ID, Name: "Visitor"}
	require.NoError(t, db.Create(&c).Error)
	require.NoError(t, db.Create(&domain.ContactInbox{ContactID: c.ID, InboxID: i.ID, SourceID: "scope-owner"}).Error)
	agentToken, err := auth.GenerateToken(&u, cfg.JWTSecret, 24)
	require.NoError(t, err)
	visitorToken, err := auth.GenerateVisitorToken(i.ID, c.ID, "scope-owner", cfg.JWTSecret, time.Hour)
	require.NoError(t, err)
	broken := gin.New()
	broken.GET("/ws", ws.ServeWS(nil, cfg, users, accounts, inboxes, convs, contacts, failingWSRevocation{ChannelIAMRepository: iam}))
	for _, url := range []string{"/ws?token=" + agentToken, "/ws?website_token=scope-widget&visitor_token=" + visitorToken} {
		w := httptest.NewRecorder()
		broken.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
		require.Equal(t, 503, w.Code)
	}
	r := gin.New()
	r.GET("/ws", ws.ServeWS(nil, cfg, users, accounts, inboxes, convs, contacts, iam))
	require.NoError(t, db.Delete(&u).Error)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ws?token="+agentToken, nil))
	require.Equal(t, 401, w.Code)
	require.NoError(t, db.Where("contact_id = ? AND inbox_id = ?", c.ID, i.ID).Delete(&domain.ContactInbox{}).Error)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ws?website_token=scope-widget&visitor_token="+visitorToken, nil))
	require.Equal(t, 403, w.Code)
}
