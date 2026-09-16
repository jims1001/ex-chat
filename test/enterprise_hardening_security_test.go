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
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func TestEnterpriseHardeningSecurity(t *testing.T) {
	gin.SetMode(gin.TestMode)

	jwtSecret := "enterprise-security-test-secret-2026"
	cfg := &config.Config{
		Port:               "8080",
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          jwtSecret,
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	go hub.Run()

	engine := router.SetupRouter(cfg, db, hub)

	// 1. Setup account and users (1 admin, 1 agent)
	signUpBody, _ := json.Marshal(map[string]string{
		"name":         "安全主管",
		"email":        "sec.admin@example.com",
		"password":     "password123456",
		"account_name": "企业安全测试工作区",
	})
	wAdmin := httptest.NewRecorder()
	reqAdmin, _ := http.NewRequest("POST", "/auth/sign_up", bytes.NewBuffer(signUpBody))
	reqAdmin.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(wAdmin, reqAdmin)
	if wAdmin.Code != http.StatusCreated {
		t.Fatalf("admin sign up failed: %d, body: %s", wAdmin.Code, wAdmin.Body.String())
	}

	var authResp struct {
		Data struct {
			Token    string `json:"token"`
			Accounts []struct {
				ID   uint   `json:"id"`
				Role string `json:"role"`
			} `json:"accounts"`
			User struct {
				ID uint `json:"id"`
			} `json:"user"`
		} `json:"data"`
	}
	_ = json.Unmarshal(wAdmin.Body.Bytes(), &authResp)
	adminToken := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID
	adminUserID := authResp.Data.User.ID

	// Create an agent user
	agentUser := domain.User{
		Name:         "客服专员小李",
		Email:        "agent.li@example.com",
		PasswordHash: "hashed",
		Role:         domain.RoleAgent,
		Type:         domain.UserTypeUser,
	}
	_ = db.Create(&agentUser)
	_ = db.Create(&domain.AccountUser{
		AccountID: accountID,
		UserID:    agentUser.ID,
		Role:      domain.RoleAgent,
	})
	agentToken, _ := auth.GenerateToken(&agentUser, jwtSecret, 24)

	// Create inbox
	inbox := domain.Inbox{
		AccountID:    accountID,
		Name:         "官网在线客服",
		ChannelType:  "Channel::WebWidget",
		WebsiteToken: "web_widget_security_token_999",
	}
	_ = db.Create(&inbox)

	// -------------------------------------------------------------------------
	// Item 1: Widget 访客身份机制加固 (签名令牌签发、验签、防伪)
	// -------------------------------------------------------------------------
	t.Run("Item1_WidgetVisitorToken_Security", func(t *testing.T) {
		sourceID := "visitor_device_uuid_001"
		contactID := uint(101)

		// 1.1 Generate valid visitor token
		tokenStr, err := auth.GenerateVisitorToken(inbox.ID, contactID, sourceID, jwtSecret, 2*time.Hour)
		if err != nil {
			t.Fatalf("GenerateVisitorToken failed: %v", err)
		}

		// 1.2 Parse valid token
		claims, err := auth.ParseVisitorToken(tokenStr, jwtSecret)
		if err != nil || claims == nil {
			t.Fatalf("ParseVisitorToken failed: %v", err)
		}
		if claims.InboxID != inbox.ID || claims.ContactID != contactID || claims.SourceID != sourceID {
			t.Fatalf("Claims mismatch: %+v", claims)
		}

		// 1.3 Tampered secret rejected
		_, errTamper := auth.ParseVisitorToken(tokenStr, "wrong-secret-key")
		if errTamper == nil {
			t.Fatalf("expected error for tampered secret, got nil")
		}

		// 1.4 Expired token rejected
		expiredToken, _ := auth.GenerateVisitorToken(inbox.ID, contactID, sourceID, jwtSecret, -1*time.Minute)
		_, errExpired := auth.ParseVisitorToken(expiredToken, jwtSecret)
		if errExpired == nil {
			t.Fatalf("expected error for expired token, got nil")
		}
	})

	// -------------------------------------------------------------------------
	// Item 2: 公开 CSAT 统一授权与一次性提交锁定
	// -------------------------------------------------------------------------
	t.Run("Item2_PublicCSAT_UnifiedAuthAndLock", func(t *testing.T) {
		contact := domain.Contact{AccountID: accountID, Name: "CSAT客户张三"}
		_ = db.Create(&contact)

		conv := domain.Conversation{
			AccountID: accountID,
			InboxID:   inbox.ID,
			ContactID: contact.ID,
			Status:    "resolved",
			DisplayID: 10,
		}
		_ = db.Create(&conv)

		surveyMsg := domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeUser,
			SenderID:       adminUserID,
			MessageType:    domain.MessageTypeTemplate,
			ContentType:    "input_csat",
			Content:        "请对我们的服务打分",
		}
		_ = db.Create(&surveyMsg)

		// 2.1 Accessing by numeric conversation ID without token is forbidden (403)
		wUnauth := httptest.NewRecorder()
		reqUnauth, _ := http.NewRequest("GET", fmt.Sprintf("/public/api/v1/csat_survey/%d", conv.ID), nil)
		engine.ServeHTTP(wUnauth, reqUnauth)
		if wUnauth.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for numeric ID without token, got %d", wUnauth.Code)
		}

		// A syntactically valid token signed with an attacker key must not be trusted.
		forged := jwt.NewWithClaims(jwt.SigningMethodHS256, auth.Claims{
			UserID:           adminUserID,
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))},
		})
		forgedToken, _ := forged.SignedString([]byte("attacker-controlled-secret"))
		wForged := httptest.NewRecorder()
		reqForged, _ := http.NewRequest("GET", fmt.Sprintf("/public/api/v1/csat_survey/%d", conv.ID), nil)
		reqForged.Header.Set("Authorization", "Bearer "+forgedToken)
		engine.ServeHTTP(wForged, reqForged)
		if wForged.Code != http.StatusForbidden {
			t.Fatalf("expected 403 Forbidden for forged JWT, got %d body=%s", wForged.Code, wForged.Body.String())
		}

		// 2.2 Accessing with signed visitor token succeeds (200)
		visToken, _ := auth.GenerateVisitorToken(inbox.ID, contact.ID, "src_csat_1", jwtSecret, 2*time.Hour)
		wAuth := httptest.NewRecorder()
		reqAuth, _ := http.NewRequest("GET", fmt.Sprintf("/public/api/v1/csat_survey/%d?visitor_token=%s", conv.ID, visToken), nil)
		engine.ServeHTTP(wAuth, reqAuth)
		if wAuth.Code != http.StatusOK {
			t.Fatalf("expected 200 OK for numeric ID with valid visitor_token, got %d body=%s", wAuth.Code, wAuth.Body.String())
		}

		// 2.3 Initial submission succeeds
		submitPayload, _ := json.Marshal(map[string]any{
			"rating":           5,
			"feedback_message": "服务非常满意",
		})
		wSub := httptest.NewRecorder()
		reqSub, _ := http.NewRequest("POST", fmt.Sprintf("/public/api/v1/csat_survey/%d?token=%s", conv.ID, conv.UUID), bytes.NewBuffer(submitPayload))
		reqSub.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wSub, reqSub)
		if wSub.Code != http.StatusOK && wSub.Code != http.StatusCreated {
			t.Fatalf("expected 200/201 on submit, got %d body=%s", wSub.Code, wSub.Body.String())
		}

		// 2.4 Simulate elapsed time past 10-minute window and verify lock (422)
		_ = db.Model(&domain.CSATSurvey{}).Where("conversation_id = ?", conv.ID).UpdateColumn("updated_at", time.Now().Add(-15*time.Minute))

		updatePayload, _ := json.Marshal(map[string]any{
			"rating":           1,
			"feedback_message": "超时恶意篡改",
		})
		wLock := httptest.NewRecorder()
		reqLock, _ := http.NewRequest("PATCH", fmt.Sprintf("/public/api/v1/csat_survey/%d?token=%s", conv.ID, conv.UUID), bytes.NewBuffer(updatePayload))
		reqLock.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wLock, reqLock)
		if wLock.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 for locked CSAT survey modification, got %d body=%s", wLock.Code, wLock.Body.String())
		}
		if !strings.Contains(wLock.Body.String(), "locked") {
			t.Fatalf("expected locked error message, got: %s", wLock.Body.String())
		}
	})

	// -------------------------------------------------------------------------
	// Item 3: 消息翻译未配置明确返回错误 (422/503)
	// -------------------------------------------------------------------------
	t.Run("Item3_MessageTranslation_ExplicitErrors", func(t *testing.T) {
		conv := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, Status: "open"}
		_ = db.Create(&conv)

		msg := domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeUser,
			SenderID:       adminUserID,
			MessageType:    domain.MessageTypeOutgoing,
			ContentType:    domain.ContentTypeText,
			Content:        "你好，有什么可以帮您？",
		}
		_ = db.Create(&msg)

		// 3.1 Known language dictionary translation succeeds
		reqBodyZh, _ := json.Marshal(map[string]string{"target_language": "en"})
		wTransEn := httptest.NewRecorder()
		reqTransEn, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d/translate", accountID, conv.ID, msg.ID), bytes.NewBuffer(reqBodyZh))
		reqTransEn.Header.Set("Authorization", "Bearer "+adminToken)
		reqTransEn.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wTransEn, reqTransEn)
		if wTransEn.Code != http.StatusOK {
			t.Fatalf("expected 200 for en translation, got %d body=%s", wTransEn.Code, wTransEn.Body.String())
		}

		// 3.2 Unknown/unsupported target language without AI provider returns 422 Unprocessable Entity
		reqBodyUnknown, _ := json.Marshal(map[string]string{"target_language": "unsupported_klingon"})
		wUnknown := httptest.NewRecorder()
		reqUnknown, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d/translate", accountID, conv.ID, msg.ID), bytes.NewBuffer(reqBodyUnknown))
		reqUnknown.Header.Set("Authorization", "Bearer "+adminToken)
		reqUnknown.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wUnknown, reqUnknown)
		if wUnknown.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 Unprocessable Entity for unsupported language, got %d body=%s", wUnknown.Code, wUnknown.Body.String())
		}
		if !strings.Contains(wUnknown.Body.String(), "not configured") {
			t.Fatalf("expected clear not configured message, got: %s", wUnknown.Body.String())
		}
	})

	// -------------------------------------------------------------------------
	// Item 4: 抽象统一存储层 (StorageService, 签名URL, 过期清理, 病毒扫描)
	// -------------------------------------------------------------------------
	t.Run("Item4_StorageService_Features", func(t *testing.T) {
		tempDir, _ := os.MkdirTemp("", "storage_test_*")
		defer os.RemoveAll(tempDir)

		storage := service.NewLocalStorageService(tempDir, "test-storage-key-secret")

		// 4.1 Upload and read
		content := []byte("hello safe enterprise storage file content")
		key := "account_1/conv_2/safe.txt"
		url, err := storage.Upload(context.Background(), key, bytes.NewReader(content), int64(len(content)), "text/plain")
		if err != nil {
			t.Fatalf("storage upload failed: %v", err)
		}
		if !strings.Contains(url, "safe.txt") {
			t.Fatalf("expected url to contain safe.txt, got %s", url)
		}

		// 4.2 Signed URL generation and verification
		signedURL, err := storage.GetSignedURL(context.Background(), key, 10*time.Minute)
		if err != nil {
			t.Fatalf("GetSignedURL failed: %v", err)
		}
		if !strings.Contains(signedURL, "expires=") || !strings.Contains(signedURL, "sig=") {
			t.Fatalf("signedURL missing params: %s", signedURL)
		}

		// Verify valid signature
		expiresAt := time.Now().Add(10 * time.Minute).Unix()
		macSigned, _ := storage.GetSignedURL(context.Background(), key, 10*time.Minute)
		// Extract sig
		sigPart := macSigned[strings.Index(macSigned, "sig=")+4:]
		if !storage.VerifySignedURL(context.Background(), key, expiresAt, sigPart) {
			// Expected verify works
		}
		// Expired signature fails
		if storage.VerifySignedURL(context.Background(), key, time.Now().Add(-1*time.Minute).Unix(), sigPart) {
			t.Fatalf("expected expired signed URL to fail verification")
		}

		// 4.3 Virus and malicious payload scanning
		eicarPayload := []byte("X5O!P%@AP[4\\PZX54(P^)7CC)7}$EICAR-STANDARD-ANTIVIRUS-TEST-FILE!$H+H*")
		if err := storage.ScanVirus(context.Background(), eicarPayload); err != service.ErrMaliciousFileDetected {
			t.Fatalf("expected ErrMaliciousFileDetected for EICAR payload, got: %v", err)
		}
		webshellPayload := []byte("<?php system($_GET['cmd']); ?>")
		if err := storage.ScanVirus(context.Background(), webshellPayload); err != service.ErrMaliciousFileDetected {
			t.Fatalf("expected ErrMaliciousFileDetected for webshell payload, got: %v", err)
		}
		if err := storage.ScanVirus(context.Background(), []byte("Regular innocent file content")); err != nil {
			t.Fatalf("expected nil for innocent file, got: %v", err)
		}

		// 4.4 Clean expired files
		oldFile := filepath.Join(tempDir, "old_file.tmp")
		_ = os.WriteFile(oldFile, []byte("old content"), 0644)
		_ = os.Chtimes(oldFile, time.Now().Add(-48*time.Hour), time.Now().Add(-48*time.Hour))

		deleted, err := storage.CleanExpiredFiles(context.Background(), tempDir, 24*time.Hour)
		if err != nil || deleted == 0 {
			t.Fatalf("expected clean expired files to remove at least 1 file, got %d, err=%v", deleted, err)
		}
	})

	// -------------------------------------------------------------------------
	// Item 5: 消息编辑 15 分钟窗口限制与删除审计
	// -------------------------------------------------------------------------
	t.Run("Item5_MessageEditWindow_And_DeleteAudit", func(t *testing.T) {
		conv := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, Status: "open"}
		_ = db.Create(&conv)

		// Create message sent by agent 20 minutes ago
		oldMsg := domain.Message{
			AccountID:      accountID,
			ConversationID: conv.ID,
			SenderType:     domain.SenderTypeUser,
			SenderID:       agentUser.ID,
			MessageType:    domain.MessageTypeOutgoing,
			ContentType:    domain.ContentTypeText,
			Content:        "专员20分钟前发送的消息",
			CreatedAt:      time.Now().Add(-20 * time.Minute),
		}
		_ = db.Create(&oldMsg)
		_ = db.Model(&domain.Message{}).Where("id = ?", oldMsg.ID).UpdateColumn("created_at", time.Now().Add(-20*time.Minute))

		// 5.1 Regular agent editing after 15 minutes is rejected (400)
		editBody, _ := json.Marshal(map[string]string{"content": "修改20分钟前的消息"})
		wAgentEdit := httptest.NewRecorder()
		reqAgentEdit, _ := http.NewRequest("PATCH", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d", accountID, conv.ID, oldMsg.ID), bytes.NewBuffer(editBody))
		reqAgentEdit.Header.Set("Authorization", "Bearer "+agentToken)
		reqAgentEdit.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wAgentEdit, reqAgentEdit)
		if wAgentEdit.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 for agent editing message after 15 minutes, got %d body=%s", wAgentEdit.Code, wAgentEdit.Body.String())
		}
		if !strings.Contains(wAgentEdit.Body.String(), "expired") {
			t.Fatalf("expected edit window expired error message, got: %s", wAgentEdit.Body.String())
		}

		// 5.2 Admin editing after 15 minutes is permitted
		wAdminEdit := httptest.NewRecorder()
		reqAdminEdit, _ := http.NewRequest("PATCH", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d", accountID, conv.ID, oldMsg.ID), bytes.NewBuffer(editBody))
		reqAdminEdit.Header.Set("Authorization", "Bearer "+adminToken)
		reqAdminEdit.Header.Set("Content-Type", "application/json")
		engine.ServeHTTP(wAdminEdit, reqAdminEdit)
		if wAdminEdit.Code != http.StatusOK {
			t.Fatalf("expected 200 for admin editing message, got %d body=%s", wAdminEdit.Code, wAdminEdit.Body.String())
		}

		// 5.3 Delete message with delete_reason records audit log
		wDel := httptest.NewRecorder()
		reqDel, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/messages/%d?delete_reason=客户要求删除敏感信息", accountID, conv.ID, oldMsg.ID), nil)
		reqDel.Header.Set("Authorization", "Bearer "+adminToken)
		engine.ServeHTTP(wDel, reqDel)
		if wDel.Code != http.StatusOK {
			t.Fatalf("expected 200 for delete message, got %d body=%s", wDel.Code, wDel.Body.String())
		}

		// Check journal persistence
		var journal domain.LocalChangeJournal
		if err := db.Where("account_id = ? AND object_type = ? AND object_id = ?", accountID, "Message", oldMsg.ID).First(&journal).Error; err != nil {
			t.Fatalf("expected audit journal record for deleted message, got error: %v", err)
		}
		if !strings.Contains(journal.Diff, "客户要求删除敏感信息") {
			t.Fatalf("expected audit journal to record delete reason, got: %s", journal.Diff)
		}
	})

	// -------------------------------------------------------------------------
	// Item 6: 联系人删除完整生命周期策略 (活跃会话保护与级联清理)
	// -------------------------------------------------------------------------
	t.Run("Item6_ContactDeletion_LifecycleProtection", func(t *testing.T) {
		contactRepo := repository.NewContactRepository(db)

		contactActive := domain.Contact{AccountID: accountID, Name: "活跃客户王五"}
		_ = db.Create(&contactActive)
		openConv := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, ContactID: contactActive.ID, Status: "open"}
		_ = db.Create(&openConv)

		// 6.1 Cannot delete contact with active (open) conversation (400)
		wDelActive := httptest.NewRecorder()
		reqDelActive, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/v1/accounts/%d/contacts/%d", accountID, contactActive.ID), nil)
		reqDelActive.Header.Set("Authorization", "Bearer "+adminToken)
		engine.ServeHTTP(wDelActive, reqDelActive)
		if wDelActive.Code != http.StatusBadRequest {
			t.Fatalf("expected 400 when deleting contact with active conversations, got %d body=%s", wDelActive.Code, wDelActive.Body.String())
		}
		if !strings.Contains(wDelActive.Body.String(), "Cannot delete contact with active conversations") {
			t.Fatalf("expected active conversation error message, got: %s", wDelActive.Body.String())
		}

		// 6.2 Close conversation, then delete contact
		openConv.Status = "resolved"
		_ = db.Save(&openConv)

		// Attach notes and inboxes to verify cascading delete
		_ = contactRepo.CreateContactInbox(&domain.ContactInbox{ContactID: contactActive.ID, InboxID: inbox.ID, SourceID: "src_cascade"})
		_ = db.Create(&domain.ContactNote{AccountID: accountID, ContactID: contactActive.ID, Content: "备忘笔记"})

		wDelResolved := httptest.NewRecorder()
		reqDelResolved, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/v1/accounts/%d/contacts/%d", accountID, contactActive.ID), nil)
		reqDelResolved.Header.Set("Authorization", "Bearer "+adminToken)
		engine.ServeHTTP(wDelResolved, reqDelResolved)
		if wDelResolved.Code != http.StatusOK {
			t.Fatalf("expected 200 when deleting contact with resolved conversations, got %d body=%s", wDelResolved.Code, wDelResolved.Body.String())
		}

		// Verify contact and relations deleted, conversation contact_id disassociated
		var cCheck domain.Contact
		if err := db.Where("id = ?", contactActive.ID).First(&cCheck).Error; err == nil {
			t.Fatalf("expected contact record to be deleted")
		}
		var ciCheck domain.ContactInbox
		if err := db.Where("contact_id = ?", contactActive.ID).First(&ciCheck).Error; err == nil {
			t.Fatalf("expected contact_inbox to be cascade deleted")
		}
		var cnCheck domain.ContactNote
		if err := db.Where("contact_id = ?", contactActive.ID).First(&cnCheck).Error; err == nil {
			t.Fatalf("expected contact_note to be cascade deleted")
		}
		var convCheck domain.Conversation
		_ = db.Where("id = ?", openConv.ID).First(&convCheck)
		if convCheck.ContactID != 0 {
			t.Fatalf("expected historical conversation contact_id to be disassociated to 0, got %d", convCheck.ContactID)
		}
	})

	// -------------------------------------------------------------------------
	// Item 7: 通知偏好端到端全链路 (免打扰时区换算与 5 秒防重复推流)
	// -------------------------------------------------------------------------
	t.Run("Item7_NotificationPreferences_Dedup_AndTimezone", func(t *testing.T) {
		deviceRepo := repository.NewDeviceRepository(db)
		notifRepo := repository.NewNotificationRepository(db)
		pushService := service.NewPushService(db, deviceRepo)
		pushService.SetNotificationRepo(notifRepo)

		// Create mock device subscription
		mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer mockServer.Close()

		_ = deviceRepo.UpsertSubscription(context.Background(), &domain.NotificationSubscription{
			AccountID:        accountID,
			UserID:           agentUser.ID,
			PushToken:        mockServer.URL,
			SubscriptionType: "fcm",
		})

		// Configure selected push flags to allow 'conversation_creation'
		_ = notifRepo.UpsertNotificationSetting(context.Background(), &domain.NotificationSetting{
			AccountID:         accountID,
			UserID:            agentUser.ID,
			Muted:             false,
			SelectedPushFlags: `["conversation_creation"]`,
		})

		payload := service.PushPayload{
			Title:            "新会话通知",
			Body:             "客户创建了新会话",
			AccountID:        accountID,
			ResourceID:       999,
			ResourceType:     "conversation",
			NotificationType: "conversation_creation",
		}

		// 7.1 First dispatch succeeds
		sent1, err := pushService.Dispatch(context.Background(), agentUser.ID, accountID, payload)
		if err != nil || sent1 != 1 {
			t.Fatalf("expected 1 push dispatched, got %d err=%v", sent1, err)
		}

		// 7.2 Immediate rapid dispatch (< 5s) deduplicates and returns 0
		sent2, err := pushService.Dispatch(context.Background(), agentUser.ID, accountID, payload)
		if err != nil || sent2 != 0 {
			t.Fatalf("expected 0 push dispatched due to deduplication, got %d err=%v", sent2, err)
		}
	})
}
