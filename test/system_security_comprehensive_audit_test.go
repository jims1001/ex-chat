package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/OracleBetX-Projects/ex-chat/pkg/ratelimit"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAuditTestEnv(t *testing.T, env string) (*gorm.DB, *gin.Engine, *config.Config, *domain.User, uint, string, *domain.User, uint, string) {
	gin.SetMode(gin.TestMode)

	jwtSecret := "enterprise-security-audit-secret-2026"
	cfg := &config.Config{
		Port:               "8080",
		Environment:        env,
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          jwtSecret,
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	require.NoError(t, err)

	hub := ws.NewHub()
	go hub.Run()

	engine := router.SetupRouter(cfg, db, hub)

	// Create Tenant 1 Admin
	signUpBody1, _ := json.Marshal(map[string]string{
		"name":         "租户1管理员",
		"email":        "admin.tenant1@example.com",
		"password":     "password123456",
		"account_name": "租户1工作区",
	})
	w1 := httptest.NewRecorder()
	req1, _ := http.NewRequest("POST", "/auth/sign_up", bytes.NewBuffer(signUpBody1))
	req1.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w1, req1)
	require.Equal(t, http.StatusCreated, w1.Code)

	var authResp1 struct {
		Data struct {
			Token    string `json:"token"`
			Accounts []struct {
				ID uint `json:"id"`
			} `json:"accounts"`
			User struct {
				ID uint `json:"id"`
			} `json:"user"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w1.Body.Bytes(), &authResp1)
	token1 := authResp1.Data.Token
	accID1 := authResp1.Data.Accounts[0].ID
	var user1 domain.User
	db.First(&user1, authResp1.Data.User.ID)

	// Create Tenant 2 Admin
	signUpBody2, _ := json.Marshal(map[string]string{
		"name":         "租户2管理员",
		"email":        "admin.tenant2@example.com",
		"password":     "password123456",
		"account_name": "租户2工作区",
	})
	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("POST", "/auth/sign_up", bytes.NewBuffer(signUpBody2))
	req2.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusCreated, w2.Code)

	var authResp2 struct {
		Data struct {
			Token    string `json:"token"`
			Accounts []struct {
				ID uint `json:"id"`
			} `json:"accounts"`
			User struct {
				ID uint `json:"id"`
			} `json:"user"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &authResp2)
	token2 := authResp2.Data.Token
	accID2 := authResp2.Data.Accounts[0].ID
	var user2 domain.User
	db.First(&user2, authResp2.Data.User.ID)

	return db, engine, cfg, &user1, accID1, token1, &user2, accID2, token2
}

func TestComprehensiveSecurityAudit(t *testing.T) {
	db, r, cfg, _, accID1, token1, _, accID2, _ := setupAuditTestEnv(t, "test")

	// -------------------------------------------------------------
	// 1. WebSocket 跨租户 IDOR 与会话监听加固
	// -------------------------------------------------------------
	t.Run("Item1_WebSocket_CrossTenant_IDOR_And_Revoked_Token", func(t *testing.T) {
		// User1 attempts to connect to Account 2 via WS request
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", fmt.Sprintf("/ws?token=%s&account_id=%d", token1, accID2), nil)
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code, "User 1 should be forbidden from accessing Account 2 via WS")

		// Revoke token1 in database
		entRepo := repository.NewChannelAuthEnterpriseRepository(db)
		_ = entRepo.RevokeToken(token1, 1, time.Now().Add(24*time.Hour))

		wRevoked := httptest.NewRecorder()
		reqRevoked, _ := http.NewRequest("GET", fmt.Sprintf("/ws?token=%s&account_id=%d", token1, accID1), nil)
		r.ServeHTTP(wRevoked, reqRevoked)
		assert.Equal(t, http.StatusUnauthorized, wRevoked.Code, "Revoked token should be rejected on WS connection")

		// Create an inbox for account 1
		inbox := domain.Inbox{
			AccountID:    accID1,
			Name:         "Audit Web Inbox",
			ChannelType:  "Channel::WebWidget",
			WebsiteToken: "audit_website_token_123",
		}
		db.Create(&inbox)

		// Visitor attempts to connect with conversation_id 99999 that doesn't exist
		wVis := httptest.NewRecorder()
		reqVis, _ := http.NewRequest("GET", "/ws?website_token=audit_website_token_123&conversation_id=99999", nil)
		r.ServeHTTP(wVis, reqVis)
		assert.Equal(t, http.StatusNotFound, wVis.Code, "Visitor specifying invalid conversation should be 404")
	})

	// -------------------------------------------------------------
	// 2. 全系统 SSRF 防御 (Webhook 与 AI Custom Tool)
	// -------------------------------------------------------------
	t.Run("Item2_SSRF_Protection_Webhook_And_AITool", func(t *testing.T) {
		var user1 domain.User
		db.First(&user1, "email = ?", "admin.tenant1@example.com")
		freshTok1, _ := auth.GenerateToken(&user1, cfg.JWTSecret, 24)

		// Attempt to create Webhook with private IP / localhost
		ssrfPayloads := []string{
			"http://127.0.0.1:8080/api/v1",
			"http://localhost:5432",
			"http://169.254.169.254/latest/meta-data",
			"http://10.0.0.1/admin",
		}

		for _, badURL := range ssrfPayloads {
			body, _ := json.Marshal(map[string]any{
				"url":           badURL,
				"subscriptions": []string{"conversation_created"},
			})
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/webhooks", accID1), bytes.NewBuffer(body))
			req.Header.Set("Authorization", "Bearer "+freshTok1)
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			assert.Equal(t, http.StatusBadRequest, w.Code, "Webhook URL with %s should be rejected with 400", badURL)
		}

		// Attempt to create AI Custom Tool with metadata URL
		toolBody, _ := json.Marshal(map[string]any{
			"name":         "cloud_metadata_tool",
			"title":        "Cloud Metadata",
			"description":  "Exploit tool",
			"endpoint_url": "http://169.254.169.254/latest/meta-data",
			"input_schema": `{"type":"object"}`,
		})
		wTool := httptest.NewRecorder()
		reqTool, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/captain/tools", accID1), bytes.NewBuffer(toolBody))
		reqTool.Header.Set("Authorization", "Bearer "+freshTok1)
		reqTool.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(wTool, reqTool)
		assert.Equal(t, http.StatusBadRequest, wTool.Code, "AI Custom Tool with metadata URL should be rejected with 400")
	})

	// -------------------------------------------------------------
	// 3. 安全附件上传服务与下载鉴权 (防跨租户与防 Stored XSS)
	// -------------------------------------------------------------
	t.Run("Item3_Secure_Uploads_Serving_And_XSS_Defense", func(t *testing.T) {
		// Prepare a test file on disk in uploads/account_1/
		testDir := filepath.Join("uploads", fmt.Sprintf("account_%d", accID1))
		_ = os.MkdirAll(testDir, 0755)
		testHTML := filepath.Join(testDir, "test_malicious.html")
		_ = os.WriteFile(testHTML, []byte("<script>alert('xss')</script>"), 0644)
		defer os.RemoveAll(filepath.Join("uploads", fmt.Sprintf("account_%d", accID1)))

		// 1. Anonymous unauthenticated access -> 401
		wAnon := httptest.NewRecorder()
		reqAnon, _ := http.NewRequest("GET", fmt.Sprintf("/uploads/account_%d/test_malicious.html", accID1), nil)
		r.ServeHTTP(wAnon, reqAnon)
		assert.Equal(t, http.StatusUnauthorized, wAnon.Code, "Anonymous access to upload should be rejected")

		// 2. Signed URL access -> 200 with XSS defense headers
		storageService := service.NewLocalStorageService("uploads", cfg.JWTSecret)
		signedURL, err := storageService.GetSignedURL(context.Background(), fmt.Sprintf("account_%d/test_malicious.html", accID1), 10*time.Minute)
		require.NoError(t, err)

		wSigned := httptest.NewRecorder()
		reqSigned, _ := http.NewRequest("GET", signedURL, nil)
		r.ServeHTTP(wSigned, reqSigned)
		assert.Equal(t, http.StatusOK, wSigned.Code)
		assert.Equal(t, "nosniff", wSigned.Header().Get("X-Content-Type-Options"))
		assert.Contains(t, wSigned.Header().Get("Content-Disposition"), "attachment")

		// 3. User 2 (from Account 2) attempting to access Account 1 file with User 2 token -> 403
		wOther := httptest.NewRecorder()
		var user2 domain.User
		db.First(&user2, "email = ?", "admin.tenant2@example.com")
		tok2, _ := auth.GenerateToken(&user2, cfg.JWTSecret, 24)

		reqOther, _ := http.NewRequest("GET", fmt.Sprintf("/uploads/account_%d/test_malicious.html", accID1), nil)
		reqOther.Header.Set("Authorization", "Bearer "+tok2)
		r.ServeHTTP(wOther, reqOther)
		assert.Equal(t, http.StatusForbidden, wOther.Code, "User from Tenant 2 should not access Tenant 1 file")
	})

	// -------------------------------------------------------------
	// 4. 生产模式密码重置 Token 隐蔽性 (防账号劫持)
	// -------------------------------------------------------------
	t.Run("Item4_Production_Password_Reset_Token_Masking", func(t *testing.T) {
		// Setup separate environment in production mode
		_, rProd, _, _, _, _, _, _, _ := setupAuditTestEnv(t, "production")

		body, _ := json.Marshal(map[string]string{
			"email": "admin.tenant1@example.com",
		})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/auth/password", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		rProd.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		var resp map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		_, hasToken := resp["reset_token"]
		assert.False(t, hasToken, "Production mode MUST NOT return reset_token in response body")
	})

	// -------------------------------------------------------------
	// 5. CORS 规范修复 (动态 Echo Origin)
	// -------------------------------------------------------------
	t.Run("Item5_CORS_W3C_Specification_Compliance", func(t *testing.T) {
		t.Setenv("CORS_ALLOWED_ORIGINS", "https://dashboard.example.com")
		req, _ := http.NewRequest("OPTIONS", "/auth/sign_in", nil)
		req.Header.Set("Origin", "https://dashboard.example.com")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNoContent, w.Code)
		assert.Equal(t, "https://dashboard.example.com", w.Header().Get("Access-Control-Allow-Origin"))
		assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"))
		assert.Equal(t, "Origin", w.Header().Get("Vary"))
	})

	// -------------------------------------------------------------
	// 6. 会话参与人不存在状态严格 404 校验
	// -------------------------------------------------------------
	t.Run("Item6_Participant_NonExistent_Conversation_404", func(t *testing.T) {
		var user1 domain.User
		db.First(&user1, "email = ?", "admin.tenant1@example.com")
		freshTok, _ := auth.GenerateToken(&user1, cfg.JWTSecret, 24)

		body, _ := json.Marshal(map[string]any{
			"user_ids": []uint{user1.ID},
		})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/999999/participants", accID1), bytes.NewBuffer(body))
		req.Header.Set("Authorization", "Bearer "+freshTok)
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code, "Non-existent conversation should return 404 Not Found")
	})

	// -------------------------------------------------------------
	// 7. 令牌桶限流保护
	// -------------------------------------------------------------
	t.Run("Item7_TokenBucket_RateLimiting", func(t *testing.T) {
		lowLimiter := ratelimit.New(2, 3)
		testEngine := gin.New()
		testEngine.POST("/test/limited", middleware.IPRateLimit(lowLimiter), func(c *gin.Context) {
			c.JSON(http.StatusOK, gin.H{"status": "ok"})
		})

		// 3 requests allowed (burst = 3)
		for i := 0; i < 3; i++ {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", "/test/limited", nil)
			testEngine.ServeHTTP(w, req)
			assert.Equal(t, http.StatusOK, w.Code)
		}

		// 4th request must be rejected with 429
		w4 := httptest.NewRecorder()
		req4, _ := http.NewRequest("POST", "/test/limited", nil)
		testEngine.ServeHTTP(w4, req4)
		assert.Equal(t, http.StatusTooManyRequests, w4.Code)
		assert.Equal(t, "5", w4.Header().Get("Retry-After"))
	})
}
