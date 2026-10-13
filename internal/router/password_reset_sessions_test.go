package router_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPasswordResetRevokesHTTPFileWebSocketAndMFAProof(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Chdir(t.TempDir())
	t.Setenv("FRONTEND_URL", "https://chat.example.test")
	cfg := &config.Config{Environment: "test", DBDriver: "sqlite", DBPath: ":memory:", JWTSecret: "reset-session-version-test", JWTExpirationHours: 24}
	db, err := database.InitDB(cfg)
	require.NoError(t, err)
	sqldb, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqldb.Close() })
	users := repository.NewUserRepository(db)
	accounts := repository.NewAccountRepository(db)
	hash, err := auth.HashPassword("OldPassword123!")
	require.NoError(t, err)
	u := domain.User{Name: "Reset Owner", Email: "reset-owner@example.test", PasswordHash: hash, Role: domain.RoleAdministrator}
	require.NoError(t, users.Create(&u))
	a := domain.Account{Name: "Reset account"}
	require.NoError(t, accounts.Create(&a))
	require.NoError(t, accounts.AddMember(a.ID, u.ID, domain.RoleAdministrator))
	old, err := auth.GenerateToken(&u, cfg.JWTSecret, 24)
	require.NoError(t, err)
	mfa, err := auth.GenerateMFAToken(&u, cfg.JWTSecret)
	require.NoError(t, err)
	file := fmt.Sprintf("account_%d/proof.txt", a.ID)
	require.NoError(t, os.MkdirAll(filepath.Join("uploads", filepath.Dir(file)), 0700))
	require.NoError(t, os.WriteFile(filepath.Join("uploads", file), []byte("owned data"), 0600))
	sender := service.NewMockEmailSender()
	r := router.SetupRouterWithOptions(cfg, db, ws.NewHub(), router.Options{EmailSender: sender})
	request := func(method, path string, body any, token string) *httptest.ResponseRecorder {
		t.Helper()
		var b bytes.Buffer
		if body != nil {
			require.NoError(t, json.NewEncoder(&b).Encode(body))
		}
		req := httptest.NewRequest(method, path, &b)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	require.Equal(t, 200, request(http.MethodGet, "/api/v1/profile", nil, old).Code)
	require.Equal(t, 200, request(http.MethodGet, "/uploads/"+file, nil, old).Code)
	w := request(http.MethodPost, "/auth/password", map[string]string{"email": u.Email}, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "reset_token")
	reset := ""
	for _, mail := range sender.GetSentEmails() {
		for _, field := range strings.Fields(mail.TextBody) {
			link, err := url.Parse(field)
			if err == nil && link.Query().Get("reset_password_token") != "" {
				reset = link.Query().Get("reset_password_token")
			}
		}
	}
	require.NotEmpty(t, reset)
	w = request(http.MethodPut, "/auth/password", map[string]string{"reset_password_token": reset, "password": "NewPassword123!"}, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Equal(t, 401, request(http.MethodGet, "/api/v1/profile", nil, old).Code)
	require.Equal(t, 401, request(http.MethodGet, "/uploads/"+file, nil, old).Code)
	require.Equal(t, 401, request(http.MethodGet, "/ws?token="+url.QueryEscape(old), nil, "").Code)
	require.Equal(t, 401, request(http.MethodPost, "/auth/mfa/verify", map[string]string{"mfa_token": mfa, "otp_code": "000000"}, "").Code)
	freshUser, err := users.FindByID(u.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, freshUser.AuthVersion)
	w = request(http.MethodPost, "/auth/sign_in", map[string]string{"email": u.Email, "password": "NewPassword123!"}, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	var loggedIn struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &loggedIn))
	require.NotEmpty(t, loggedIn.Data.Token)
	require.Equal(t, 200, request(http.MethodGet, "/api/v1/profile", nil, loggedIn.Data.Token).Code)
	require.Equal(t, 200, request(http.MethodGet, "/uploads/"+file, nil, loggedIn.Data.Token).Code)
	require.Equal(t, 400, request(http.MethodPut, "/auth/password", map[string]string{"token": reset, "password": "AnotherPassword123!"}, "").Code)
}
