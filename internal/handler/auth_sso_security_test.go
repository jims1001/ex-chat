package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type ssoTestVerifier struct {
	calls int
	err   error
}

func (v *ssoTestVerifier) Verify(ctx context.Context, token, audience string) (string, string, error) {
	v.calls++
	if token != "signed-credential" || audience != "configured-client" {
		return "", "", errors.New("unexpected verification parameters")
	}
	if v.err != nil {
		return "", "", v.err
	}
	return "verified@gmail.com", "Verified User", nil
}

func TestGoogleCallbackRequiresVerifiedIdentityAndMFA(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{DBDriver: "sqlite", DBPath: ":memory:", JWTSecret: "sso-regression-key", JWTExpirationHours: 24, GoogleClientID: "configured-client"}
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	users := repository.NewUserRepository(db)
	accounts := repository.NewAccountRepository(db)
	enterprise := repository.NewChannelAuthEnterpriseRepository(db)
	h := NewAuthEnterpriseHandler(enterprise, users, accounts, nil, nil, nil, nil, cfg)
	r := gin.New()
	r.GET("/google", h.GoogleOAuthCallback)
	r.POST("/google", h.GoogleOAuthCallback)
	fake := &ssoTestVerifier{}
	old := googleIDTokenVerifier
	googleIDTokenVerifier = fake
	t.Cleanup(func() { googleIDTokenVerifier = old })
	request := func(method, formCSRF, cookieCSRF string) *httptest.ResponseRecorder {
		values := url.Values{"credential": {"signed-credential"}, "g_csrf_token": {formCSRF}}
		req := httptest.NewRequest(method, "/google", strings.NewReader(values.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if cookieCSRF != "" {
			req.AddCookie(&http.Cookie{Name: "g_csrf_token", Value: cookieCSRF})
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	for _, args := range [][3]string{{http.MethodGet, "csrf", "csrf"}, {http.MethodPost, "", ""}, {http.MethodPost, "csrf", "other"}, {http.MethodPost, "csrf", ""}} {
		w := request(args[0], args[1], args[2])
		require.Equal(t, 401, w.Code)
		require.NotContains(t, w.Body.String(), `"token":`)
	}
	require.Zero(t, fake.calls)
	cfg.GoogleClientID = ""
	require.Equal(t, 503, request(http.MethodPost, "csrf", "csrf").Code)
	require.Zero(t, fake.calls)
	cfg.GoogleClientID = "configured-client"
	fake.err = errors.New("unverified")
	require.Equal(t, 401, request(http.MethodPost, "csrf", "csrf").Code)
	var count int64
	require.NoError(t, db.Model(&domain.User{}).Count(&count).Error)
	require.Zero(t, count)
	fake.err = nil
	decode := func(w *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		require.Equal(t, 200, w.Code)
		var result struct {
			Data map[string]any `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		return result.Data
	}
	data := decode(request(http.MethodPost, "csrf", "csrf"))
	token, ok := data["token"].(string)
	require.True(t, ok)
	_, err = auth.ValidateToken(token, cfg.JWTSecret)
	require.NoError(t, err)
	user, err := users.FindByEmail("verified@gmail.com")
	require.NoError(t, err)
	require.NotNil(t, user)
	require.NoError(t, enterprise.SaveMFAProfile(&domain.MFAProfile{UserID: user.ID, Secret: "JBSWY3DPEHPK3PXP", Enabled: true}))
	data = decode(request(http.MethodPost, "csrf", "csrf"))
	require.Equal(t, true, data["mfa_required"])
	require.Nil(t, data["token"])
	challenge, ok := data["mfa_token"].(string)
	require.True(t, ok)
	_, err = auth.ValidateMFAToken(challenge, cfg.JWTSecret)
	require.NoError(t, err)
	_, err = auth.ValidateToken(challenge, cfg.JWTSecret)
	require.Error(t, err)
}

func TestSAMLCallbacksNeverAuthenticateParsedXML(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AuthEnterpriseHandler{} // no repository should be touched
	r := gin.New()
	r.POST("/saml", h.SAMLCallback)
	r.POST("/saml/login", h.InitiateSAMLLogin)
	for _, path := range []string{"/saml", "/saml/login"} {
		for _, raw := range []string{"", "corrupt", auth.GenerateMockSAMLResponse("admin@example.com", "Admin", "User"), "<Response><SignatureValue>forged</SignatureValue><Assertion><Subject><NameID>admin@example.com</NameID></Subject></Assertion></Response>"} {
			form := url.Values{"SAMLResponse": {raw}}
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, 503, w.Code)
			require.NotContains(t, w.Body.String(), `"token":`)
		}
	}
}
