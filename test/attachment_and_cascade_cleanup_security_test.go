package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAttachmentAndCascadeCleanupSecurity(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             fmt.Sprintf("file:attach_security_%d?mode=memory&cache=shared", time.Now().UnixNano()),
		JWTSecret:          "test_jwt_secret_attachment_and_cleanup_32chars!!",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	require.NoError(t, err)

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// Helper to sign up user and create account
	createUserAndAccount := func(email, role string) (string, uint, domain.User) {
		signUpPayload := map[string]string{
			"name":         "Test User",
			"email":        email,
			"password":     "Password123!",
			"account_name": "Test Company",
		}
		body, _ := json.Marshal(signUpPayload)
		req := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusCreated, w.Code)

		var authResp struct {
			Data struct {
				Token    string           `json:"token"`
				User     domain.User      `json:"user"`
				Accounts []domain.Account `json:"accounts"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &authResp)

		// Set role if not administrator
		if role != domain.RoleAdministrator && len(authResp.Data.Accounts) > 0 {
			db.Model(&domain.AccountUser{}).
				Where("account_id = ? AND user_id = ?", authResp.Data.Accounts[0].ID, authResp.Data.User.ID).
				Update("role", role)
		}

		return authResp.Data.Token, authResp.Data.Accounts[0].ID, authResp.Data.User
	}

	admin1Token, acc1ID, _ := createUserAndAccount(fmt.Sprintf("admin1_%d@test.com", time.Now().UnixNano()), domain.RoleAdministrator)
	_, acc2ID, _ := createUserAndAccount(fmt.Sprintf("admin2_%d@test.com", time.Now().UnixNano()), domain.RoleAdministrator)

	// Agent in Account 1
	agent1 := domain.User{
		Name:         "Agent One",
		Email:        fmt.Sprintf("agent1_%d@test.com", time.Now().UnixNano()),
		PasswordHash: "hashed",
		Role:         domain.RoleAgent,
	}
	db.Create(&agent1)
	db.Create(&domain.AccountUser{
		AccountID: acc1ID,
		UserID:    agent1.ID,
		Role:      domain.RoleAgent,
	})
	agent1Token, _ := auth.GenerateToken(&agent1, cfg.JWTSecret, 24)

	// Inboxes, Contacts and Conversations for Acc 1
	inbox1 := domain.Inbox{AccountID: acc1ID, Name: "Acc1 Inbox", ChannelType: domain.ChannelWebWidget, WebsiteToken: "tok_acc1"}
	db.Create(&inbox1)
	contact1 := domain.Contact{AccountID: acc1ID, Name: "Contact 1", Email: "contact1@test.com"}
	db.Create(&contact1)
	conv1 := domain.Conversation{AccountID: acc1ID, InboxID: inbox1.ID, ContactID: contact1.ID, Status: domain.ConversationStatusOpen}
	db.Create(&conv1)
	msg1 := domain.Message{AccountID: acc1ID, ConversationID: conv1.ID, SenderType: domain.SenderTypeUser, SenderID: 1, Content: "Msg 1"}
	db.Create(&msg1)

	// Inboxes, Contacts and Conversations for Acc 2
	inbox2 := domain.Inbox{AccountID: acc2ID, Name: "Acc2 Inbox", ChannelType: domain.ChannelWebWidget, WebsiteToken: "tok_acc2"}
	db.Create(&inbox2)
	contact2 := domain.Contact{AccountID: acc2ID, Name: "Contact 2", Email: "contact2@test.com"}
	db.Create(&contact2)
	conv2 := domain.Conversation{AccountID: acc2ID, InboxID: inbox2.ID, ContactID: contact2.ID, Status: domain.ConversationStatusOpen}
	db.Create(&conv2)
	msg2 := domain.Message{AccountID: acc2ID, ConversationID: conv2.ID, SenderType: domain.SenderTypeUser, SenderID: 2, Content: "Msg 2"}
	db.Create(&msg2)

	// -------------------------------------------------------------
	// 1. Cross-Account & Invalid Conversation/Message Attachment Upload
	// -------------------------------------------------------------
	t.Run("CrossAccount_And_Invalid_Attachment_Upload_Prevention", func(t *testing.T) {
		// Attempt to upload attachment in Acc1 targeting conv2 (from Acc2) -> 404
		body := map[string]any{
			"message_id": msg2.ID,
			"file_type":  "image/png",
			"data_url":   "https://storage.internal/uploads/test.png",
			"file_size":  1024,
		}
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/attachments", acc1ID, conv2.ID), bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+admin1Token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code, "should reject conversation not belonging to account")

		// Attempt to upload in Acc1 conv1 targeting msg2 (which belongs to conv2) -> 404
		body["message_id"] = msg2.ID
		raw, _ = json.Marshal(body)
		req = httptest.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/attachments", acc1ID, conv1.ID), bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+admin1Token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code, "should reject message not belonging to conversation")

		// Attempt with non-existent message_id 99999 -> 404
		body["message_id"] = 99999
		raw, _ = json.Marshal(body)
		req = httptest.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/attachments", acc1ID, conv1.ID), bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+admin1Token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusNotFound, w.Code, "should reject non-existent message")
	})

	// -------------------------------------------------------------
	// 2. Attachment Upload RBAC Enforcement
	// -------------------------------------------------------------
	t.Run("Attachment_Upload_RBAC_Enforcement", func(t *testing.T) {
		// Create a custom role with no conversation_manage permission
		customRole := domain.CustomRole{
			AccountID:   acc1ID,
			Name:        "readonly_role",
			Permissions: `["contact_read","conversation_read"]`,
		}
		db.Create(&customRole)

		noPermUser := domain.User{
			Name:         "NoPerm User",
			Email:        fmt.Sprintf("noperm_%d@test.com", time.Now().UnixNano()),
			PasswordHash: "hashed",
			Role:         domain.RoleAgent,
		}
		db.Create(&noPermUser)
		db.Create(&domain.AccountUser{
			AccountID:    acc1ID,
			UserID:       noPermUser.ID,
			Role:         domain.RoleAgent,
			CustomRoleID: &customRole.ID,
		})
		noPermUserToken, _ := auth.GenerateToken(&noPermUser, cfg.JWTSecret, 24)

		body := map[string]any{
			"message_id": msg1.ID,
			"file_type":  "image/png",
			"data_url":   "https://storage.internal/uploads/valid.png",
			"file_size":  1024,
		}
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/attachments", acc1ID, conv1.ID), bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+noPermUserToken)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusForbidden, w.Code, "user without conversation_manage permission should be 403")

		// Agent with conversation_manage fallback should succeed
		req = httptest.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/attachments", acc1ID, conv1.ID), bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+agent1Token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusCreated, w.Code, "agent should be allowed to upload attachment")
	})

	// -------------------------------------------------------------
	// 3. JSON Attachment Security (XSS / Size / HTML / SVG)
	// -------------------------------------------------------------
	t.Run("JSON_Attachment_Security_Validation", func(t *testing.T) {
		// javascript: pseudo-protocol -> 400
		badPayload := map[string]any{
			"message_id": msg1.ID,
			"file_type":  "image/png",
			"data_url":   "javascript:alert('xss')",
			"file_size":  100,
		}
		raw, _ := json.Marshal(badPayload)
		req := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/attachments", acc1ID, conv1.ID), bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+admin1Token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "Dangerous URL protocol detected")

		// data:text/html payload -> 400
		badPayload["data_url"] = "data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg=="
		raw, _ = json.Marshal(badPayload)
		req = httptest.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/attachments", acc1ID, conv1.ID), bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+admin1Token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "Dangerous data URL content type detected")

		// file_type image/svg+xml -> 400
		badPayload["data_url"] = "https://safe.com/test.png"
		badPayload["file_type"] = "image/svg+xml"
		raw, _ = json.Marshal(badPayload)
		req = httptest.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/attachments", acc1ID, conv1.ID), bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+admin1Token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "Dangerous file type detected")

		// Oversized file_size (> 25MB) -> 400
		badPayload["file_type"] = "image/png"
		badPayload["file_size"] = 26 * 1024 * 1024
		raw, _ = json.Marshal(badPayload)
		req = httptest.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/attachments", acc1ID, conv1.ID), bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+admin1Token)
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "Attachment exceeds maximum size of 25MB")
	})

	// -------------------------------------------------------------
	// 4. Physical File Cleanup on Message Deletion
	// -------------------------------------------------------------
	t.Run("Physical_File_Cleanup_On_Message_Deletion", func(t *testing.T) {
		// Upload real multipart file
		msgForDelete := domain.Message{AccountID: acc1ID, ConversationID: conv1.ID, SenderType: domain.SenderTypeUser, SenderID: 1, Content: "Msg with file"}
		db.Create(&msgForDelete)

		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		_ = writer.WriteField("message_id", fmt.Sprintf("%d", msgForDelete.ID))
		part, _ := writer.CreateFormFile("attachment", "phys_test.png")
		_, _ = part.Write([]byte("\x89PNG\r\n\x1a\nfakeimagecontent"))
		_ = writer.Close()

		req := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/attachments", acc1ID, conv1.ID), body)
		req.Header.Set("Authorization", "Bearer "+admin1Token)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusCreated, w.Code)

		var attResp struct {
			Data domain.Attachment `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &attResp)
		require.NotEmpty(t, attResp.Data.DataURL)

		relPath := strings.TrimPrefix(attResp.Data.DataURL, "/")
		require.FileExists(t, relPath, "physical file should exist on disk")

		// Delete the message via HTTP
		reqDel := httptest.NewRequest("DELETE", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d", acc1ID, conv1.ID, msgForDelete.ID), nil)
		reqDel.Header.Set("Authorization", "Bearer "+admin1Token)
		wDel := httptest.NewRecorder()
		r.ServeHTTP(wDel, reqDel)
		assert.Equal(t, http.StatusOK, wDel.Code)

		// Verify physical file was deleted
		_, statErr := os.Stat(relPath)
		assert.True(t, os.IsNotExist(statErr), "physical file should have been removed upon message deletion")
	})

	// -------------------------------------------------------------
	// 5. Cascade Deletion on Conversation Deletion
	// -------------------------------------------------------------
	t.Run("Cascade_Deletion_On_Conversation_Deletion", func(t *testing.T) {
		// Create conv with CSAT, Ticket, WidgetEvent, Call, CopilotThread, EmailLog
		cascadeConv := domain.Conversation{AccountID: acc1ID, InboxID: inbox1.ID, ContactID: contact1.ID, Status: domain.ConversationStatusOpen}
		db.Create(&cascadeConv)

		cascadeMsg := domain.Message{AccountID: acc1ID, ConversationID: cascadeConv.ID, SenderType: domain.SenderTypeUser, SenderID: 1, Content: "Cascade test"}
		db.Create(&cascadeMsg)

		// Create attachment on disk
		convDir := filepath.Join("uploads", fmt.Sprintf("account_%d", acc1ID), fmt.Sprintf("conv_%d", cascadeConv.ID))
		_ = os.MkdirAll(convDir, 0o755)
		dummyFilePath := filepath.Join(convDir, "test.png")
		_ = os.WriteFile(dummyFilePath, []byte("png"), 0o644)
		att := domain.Attachment{AccountID: acc1ID, MessageID: cascadeMsg.ID, FileType: "image/png", DataURL: "/" + dummyFilePath, FileSize: 3}
		db.Create(&att)

		// Associated records
		csat := domain.CSATSurvey{AccountID: acc1ID, ConversationID: cascadeConv.ID, Rating: 5}
		db.Create(&csat)

		ticket := domain.Ticket{AccountID: acc1ID, ConversationID: &cascadeConv.ID, TicketNumber: "TK-CASC-01", Title: "Cascade Ticket"}
		db.Create(&ticket)

		call := domain.Call{AccountID: acc1ID, ConversationID: &cascadeConv.ID, Status: "completed"}
		db.Create(&call)

		we := domain.WidgetEvent{AccountID: acc1ID, ConversationID: &cascadeConv.ID, Name: "test_event"}
		db.Create(&we)

		ct := domain.CopilotThread{AccountID: acc1ID, ConversationID: &cascadeConv.ID, Status: "active"}
		db.Create(&ct)

		emailLog := domain.EmailLog{AccountID: acc1ID, ConversationID: &cascadeConv.ID, ToEmail: "test@example.com", Status: "sent"}
		db.Create(&emailLog)

		// Delete conversation
		reqDel := httptest.NewRequest("DELETE", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d", acc1ID, cascadeConv.ID), nil)
		reqDel.Header.Set("Authorization", "Bearer "+admin1Token)
		wDel := httptest.NewRecorder()
		r.ServeHTTP(wDel, reqDel)
		assert.Equal(t, http.StatusOK, wDel.Code)

		// Verify conversation deleted
		var count int64
		db.Model(&domain.Conversation{}).Where("id = ?", cascadeConv.ID).Count(&count)
		assert.Equal(t, int64(0), count)

		// Verify disk directory deleted
		_, dirErr := os.Stat(convDir)
		assert.True(t, os.IsNotExist(dirErr), "conv upload dir should be removed")

		// Verify CSAT deleted
		db.Model(&domain.CSATSurvey{}).Where("conversation_id = ?", cascadeConv.ID).Count(&count)
		assert.Equal(t, int64(0), count)

		// Verify Ticket conversation_id set to null (not dangling)
		var checkTicket domain.Ticket
		db.First(&checkTicket, ticket.ID)
		assert.Nil(t, checkTicket.ConversationID, "ticket conversation_id should be nullified")

		// Verify Call deleted
		db.Model(&domain.Call{}).Where("conversation_id = ?", cascadeConv.ID).Count(&count)
		assert.Equal(t, int64(0), count)

		// Verify WidgetEvent deleted
		db.Model(&domain.WidgetEvent{}).Where("conversation_id = ?", cascadeConv.ID).Count(&count)
		assert.Equal(t, int64(0), count)

		// Verify CopilotThread deleted
		db.Model(&domain.CopilotThread{}).Where("conversation_id = ?", cascadeConv.ID).Count(&count)
		assert.Equal(t, int64(0), count)

		// Verify EmailLog deleted
		db.Model(&domain.EmailLog{}).Where("conversation_id = ?", cascadeConv.ID).Count(&count)
		assert.Equal(t, int64(0), count)
	})

	// -------------------------------------------------------------
	// 6. Help Center Markdown Link Sanitization & Sitemap Escaping
	// -------------------------------------------------------------
	t.Run("Portal_Markdown_And_Sitemap_Security", func(t *testing.T) {
		// Create a portal article with javascript: links and special characters
		portal := domain.Portal{AccountID: acc1ID, Name: "Help Portal", Slug: "help-center"}
		db.Create(&portal)
		category := domain.Category{AccountID: acc1ID, PortalID: portal.ID, Name: "General", Slug: "general"}
		db.Create(&category)

		article := domain.Article{
			AccountID:  acc1ID,
			PortalID:   portal.ID,
			CategoryID: category.ID,
			Title:      "Security Article & Guide",
			Slug:       "security-guide",
			Status:     "published",
			Content:    "Click [evil](javascript:alert(1)) or [vbscript](vbscript:msgbox) or [safe](https://example.com?a=1&b=2)",
		}
		db.Create(&article)

		// Test article public render
		req := httptest.NewRequest("GET", fmt.Sprintf("/hc/%s/articles/%s", portal.Slug, article.Slug), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
		respBody := w.Body.String()
		assert.NotContains(t, respBody, "href=\"javascript:", "should not output javascript: href")
		assert.NotContains(t, respBody, "href=\"vbscript:", "should not output vbscript: href")
		assert.Contains(t, respBody, "href=\"https://example.com?a=1&amp;b=2\"", "safe href should be present and escaped")

		// Test sitemap render
		reqSitemap := httptest.NewRequest("GET", fmt.Sprintf("/hc/%s/sitemap.xml", portal.Slug), nil)
		reqSitemap.Host = "portal.example.com"
		reqSitemap.Header.Set("X-Forwarded-Proto", "https")
		wSitemap := httptest.NewRecorder()
		r.ServeHTTP(wSitemap, reqSitemap)
		assert.Equal(t, http.StatusOK, wSitemap.Code)
		assert.Equal(t, "application/xml; charset=utf-8", wSitemap.Header().Get("Content-Type"))
		sitemapBody := wSitemap.Body.String()
		assert.Contains(t, sitemapBody, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>")
		assert.Contains(t, sitemapBody, "<urlset xmlns=\"http://www.sitemaps.org/schemas/sitemap/0.9\">")
		assert.Contains(t, sitemapBody, "<loc>https://portal.example.com/hc/help-center/articles/security-guide</loc>")
	})
}
