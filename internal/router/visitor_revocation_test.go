package router_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRevokedVisitorCannotUseWidgetPublicOrBootstrap(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{Environment: "test", DBDriver: "sqlite", DBPath: ":memory:", JWTSecret: "revocation-regression-secret", JWTExpirationHours: 24}
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	a := domain.Account{Name: "Revocation"}
	require.NoError(t, db.Create(&a).Error)
	i := domain.Inbox{AccountID: a.ID, Name: "Widget", WebsiteToken: "revoke-widget"}
	require.NoError(t, db.Create(&i).Error)
	c := domain.Contact{AccountID: a.ID, Name: "Owner"}
	require.NoError(t, db.Create(&c).Error)
	require.NoError(t, db.Create(&domain.ContactInbox{ContactID: c.ID, InboxID: i.ID, SourceID: "owner-source"}).Error)
	token, err := auth.GenerateVisitorToken(i.ID, c.ID, "owner-source", cfg.JWTSecret, time.Hour)
	require.NoError(t, err)
	h := router.SetupRouter(cfg, db, ws.NewHub())
	paths := []string{"/api/v1/widget/contact?website_token=revoke-widget", fmt.Sprintf("/public/api/v1/inboxes/revoke-widget/contacts/%d", c.ID), "/api/v1/widget/config?website_token=revoke-widget"}
	for _, path := range paths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Visitor-Session", token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code, w.Body.String())
	}
	repo := repository.NewChannelAuthEnterpriseRepository(db)
	require.NoError(t, repo.RevokeToken(token, 0, time.Now().Add(time.Hour)))
	for _, path := range paths {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Visitor-Session", token)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		require.Equal(t, 401, w.Code, path)
		require.NotContains(t, w.Body.String(), "owner-source")
	}
}
