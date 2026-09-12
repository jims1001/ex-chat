package test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestWidgetAPI_FullAlignment(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_widget_alignment_123456",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up Account
	signUpPayload := map[string]string{
		"name":         "Widget Admin",
		"email":        "widgetadmin@example.com",
		"password":     "Secret123!",
		"account_name": "Widget Tech Corp",
	}
	body, _ := json.Marshal(signUpPayload)
	req := httptest.NewRequest(http.MethodPost, "/auth/sign_up", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("sign up failed: code=%d body=%s", w.Code, w.Body.String())
	}

	var authResp struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &authResp)
	accountID := authResp.Data.Accounts[0].ID
	accIDStr := strconv.Itoa(int(accountID))
	token := authResp.Data.Token

	// 2. Create Inbox with HMAC Token
	hmacSecret := "super_hmac_secret_key"
	websiteToken := "web_token_widget_alignment_999"
	inbox := domain.Inbox{
		AccountID:           accountID,
		Name:                "Live Web Support",
		ChannelType:         "Channel::WebWidget",
		WebsiteToken:        websiteToken,
		GreetingEnabled:     true,
		GreetingMessage:     "Hello, how can we help?",
		WorkingHoursEnabled: true,
		CSATSurveyEnabled:   true,
		HMACToken:           hmacSecret,
		HMACMandatory:       true,
	}
	db.Create(&inbox)

	// Add an agent to the inbox
	agentUser := domain.User{
		Name:         "Support Agent Sam",
		DisplayName:  "Sam S.",
		Email:        "sam@example.com",
		Role:         domain.RoleAgent,
		AvatarURL:    "https://avatars.internal/sam.png",
	}
	db.Create(&agentUser)
	db.Create(&domain.AccountUser{
		AccountID: accountID,
		UserID:    agentUser.ID,
		Role:      domain.RoleAgent,
	})
	db.Create(&domain.InboxMember{
		InboxID: inbox.ID,
		UserID:  agentUser.ID,
	})

	// 3. Test: POST /api/v1/widget/config (Widget 配置创建与初始化)
	t.Run("POST /config initializes widget and creates visitor contact", func(t *testing.T) {
		configReq := map[string]any{
			"website_token": websiteToken,
			"contact": map[string]any{
				"source_id": "visitor_initial_001",
				"name":      "New Visitor",
				"email":     "visitor001@example.com",
			},
		}
		b, _ := json.Marshal(configReq)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/widget/config", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("POST /config failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var confResp struct {
			Success           bool   `json:"success"`
			InboxID           uint   `json:"inbox_id"`
			Name              string `json:"name"`
			CSATSurveyEnabled bool   `json:"csat_survey_enabled"`
			Contact           struct {
				ID    uint   `json:"id"`
				Name  string `json:"name"`
				Email string `json:"email"`
			} `json:"contact"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &confResp); err != nil {
			t.Fatalf("failed to decode config response: %v", err)
		}
		if !confResp.Success || confResp.InboxID != inbox.ID {
			t.Errorf("unexpected inbox config: %+v", confResp)
		}
		if confResp.Contact.Email != "visitor001@example.com" {
			t.Errorf("expected contact email visitor001@example.com, got %s", confResp.Contact.Email)
		}
	})

	// 4. Test: set_user (POST /api/v1/widget/contact/set_user & POST /api/v1/widget/set_user)
	t.Run("set_user identification with HMAC security", func(t *testing.T) {
		identifier := "customer_user_888"

		// A. Test invalid HMAC hash rejection (401)
		invalidReq := map[string]any{
			"identifier":      identifier,
			"identifier_hash": "invalid_fake_hash_12345",
			"email":           "cust888@example.com",
		}
		b, _ := json.Marshal(invalidReq)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/widget/contact/set_user?website_token="+websiteToken, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 for invalid HMAC hash, got %d body=%s", w.Code, w.Body.String())
		}

		// B. Test valid HMAC hash verification
		mac := hmac.New(sha256.New, []byte(hmacSecret))
		mac.Write([]byte(identifier))
		validHash := hex.EncodeToString(mac.Sum(nil))

		validReq := map[string]any{
			"identifier":      identifier,
			"identifier_hash": validHash,
			"email":           "cust888@example.com",
			"name":            "Alice Authenticated",
			"avatar_url":      "https://avatars.internal/alice.jpg",
			"custom_attributes": map[string]any{
				"membership_tier": "platinum",
				"account_balance": 1500,
				"to_be_deleted":   "temp_value",
			},
		}
		b, _ = json.Marshal(validReq)
		req = httptest.NewRequest(http.MethodPost, "/api/v1/widget/contact/set_user?website_token="+websiteToken, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("valid set_user failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var setResp struct {
			Success bool `json:"success"`
			Contact struct {
				ID               uint           `json:"id"`
				Identifier       string         `json:"identifier"`
				Name             string         `json:"name"`
				Email            string         `json:"email"`
				CustomAttributes map[string]any `json:"custom_attributes"`
			} `json:"contact"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &setResp); err != nil {
			t.Fatalf("failed to decode set_user response: %v", err)
		}
		if setResp.Contact.Identifier != identifier || setResp.Contact.Name != "Alice Authenticated" {
			t.Errorf("unexpected set_user result: %+v", setResp)
		}
		if setResp.Contact.CustomAttributes["membership_tier"] != "platinum" {
			t.Errorf("expected membership_tier platinum, got %v", setResp.Contact.CustomAttributes["membership_tier"])
		}

		// C. Also verify alias POST /api/v1/widget/set_user
		reqAlias := httptest.NewRequest(http.MethodPost, "/api/v1/widget/set_user?website_token="+websiteToken, bytes.NewReader(b))
		reqAlias.Header.Set("Content-Type", "application/json")
		wAlias := httptest.NewRecorder()
		r.ServeHTTP(wAlias, reqAlias)
		if wAlias.Code != http.StatusOK {
			t.Errorf("alias POST /set_user failed: code=%d", wAlias.Code)
		}
	})

	// 5. Test: GET /api/v1/widget/contact (Widget 联系人详情读取)
	t.Run("GET /contact retrieves current visitor profile", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/widget/contact?website_token=%s&source_id=customer_user_888", websiteToken), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /contact failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var contactResp struct {
			ID               uint           `json:"id"`
			Name             string         `json:"name"`
			Email            string         `json:"email"`
			Identifier       string         `json:"identifier"`
			CustomAttributes map[string]any `json:"custom_attributes"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &contactResp); err != nil {
			t.Fatalf("failed to parse contact: %v", err)
		}
		if contactResp.Identifier != "customer_user_888" || contactResp.Name != "Alice Authenticated" {
			t.Errorf("unexpected contact details: %+v", contactResp)
		}
		if contactResp.CustomAttributes["to_be_deleted"] != "temp_value" {
			t.Errorf("expected to_be_deleted custom attribute present")
		}
	})

	// 6. Test: POST & DELETE /api/v1/widget/contact/destroy_custom_attributes (联系人自定义属性删除)
	t.Run("destroy_custom_attributes removes specified keys", func(t *testing.T) {
		delPayload := map[string]any{
			"custom_attribute_keys": []string{"to_be_deleted"},
		}
		b, _ := json.Marshal(delPayload)
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/widget/contact/destroy_custom_attributes?website_token=%s&source_id=customer_user_888", websiteToken), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("destroy_custom_attributes failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var delResp struct {
			Success          bool           `json:"success"`
			CustomAttributes map[string]any `json:"custom_attributes"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &delResp)
		if _, exists := delResp.CustomAttributes["to_be_deleted"]; exists {
			t.Errorf("expected to_be_deleted to be deleted, but still exists")
		}
		if delResp.CustomAttributes["membership_tier"] != "platinum" {
			t.Errorf("expected membership_tier to be preserved, got %v", delResp.CustomAttributes["membership_tier"])
		}

		// Re-fetch contact to verify persistence
		req = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/widget/contact?website_token=%s&source_id=customer_user_888", websiteToken), nil)
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var verifyResp struct {
			CustomAttributes map[string]any `json:"custom_attributes"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &verifyResp)
		if _, exists := verifyResp.CustomAttributes["to_be_deleted"]; exists {
			t.Errorf("expected to_be_deleted attribute removed from database")
		}
	})

	// 7. Test: POST /api/v1/widget/direct_uploads (Widget 直接文件上传)
	t.Run("POST /direct_uploads handles multipart file and JSON blob requests", func(t *testing.T) {
		// A. Multipart file direct upload
		bodyBuf := &bytes.Buffer{}
		writer := multipart.NewWriter(bodyBuf)
		part, err := writer.CreateFormFile("file", "test_screenshot.png")
		if err != nil {
			t.Fatalf("failed to create form file: %v", err)
		}
		part.Write([]byte("fake-png-image-content-data"))
		writer.Close()

		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/widget/direct_uploads?website_token=%s", websiteToken), bodyBuf)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("direct file upload failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var uploadResp struct {
			Success  bool   `json:"success"`
			URL      string `json:"url"`
			FileURL  string `json:"file_url"`
			BlobKey  string `json:"blob_key"`
			Filename string `json:"filename"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &uploadResp); err != nil {
			t.Fatalf("failed to decode upload response: %v", err)
		}
		if uploadResp.FileURL == "" || uploadResp.Filename != "test_screenshot.png" {
			t.Errorf("unexpected file upload result: %+v", uploadResp)
		}

		// B. JSON blob initiation request
		jsonBlobReq := map[string]any{
			"blob": map[string]any{
				"filename":     "document.pdf",
				"content_type": "application/pdf",
				"byte_size":    1024,
				"checksum":     "abc123==",
			},
		}
		b, _ := json.Marshal(jsonBlobReq)
		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/widget/direct_uploads?website_token=%s", websiteToken), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("direct blob upload initiation failed: code=%d body=%s", w.Code, w.Body.String())
		}
		var blobResp struct {
			Key          string `json:"key"`
			DirectUpload struct {
				URL string `json:"url"`
			} `json:"direct_upload"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &blobResp)
		if blobResp.Key == "" || blobResp.DirectUpload.URL == "" {
			t.Errorf("unexpected blob initiation response: %+v", blobResp)
		}
	})

	// 8. Test: PATCH /api/v1/widget/messages/:id (Widget 消息更新 / 表单交互)
	t.Run("PATCH /messages/:id updates submitted values and content", func(t *testing.T) {
		// Create a conversation and interactive message first
		conv := domain.Conversation{
			AccountID: accountID,
			InboxID:   inbox.ID,
			Status:    domain.ConversationStatusOpen,
		}
		db.Create(&conv)

		msg := domain.Message{
			AccountID:         accountID,
			ConversationID:    conv.ID,
			MessageType:       domain.MessageTypeTemplate,
			Content:           "Please rate our service",
			ContentType:       domain.ContentTypeInputSelect,
			ContentAttributes: `{"options":[{"title":"Good","value":"5"},{"title":"Bad","value":"1"}]}`,
		}
		db.Create(&msg)

		updMsgPayload := map[string]any{
			"content": "Rated 5 stars",
			"submitted_values": map[string]any{
				"csat_rating": 5,
				"feedback":    "Awesome support experience!",
			},
		}
		b, _ := json.Marshal(updMsgPayload)
		req := httptest.NewRequest(http.MethodPatch, fmt.Sprintf("/api/v1/widget/messages/%d?website_token=%s", msg.ID, websiteToken), bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("PATCH /messages/:id failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var msgResp struct {
			Success bool           `json:"success"`
			Data    domain.Message `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &msgResp); err != nil {
			t.Fatalf("failed to decode message update: %v", err)
		}
		if msgResp.Data.Content != "Rated 5 stars" {
			t.Errorf("expected content 'Rated 5 stars', got %s", msgResp.Data.Content)
		}

		// Re-fetch message from DB to confirm content_attributes persisted submitted_values
		var savedMsg domain.Message
		db.First(&savedMsg, msg.ID)
		var contentAttrs map[string]any
		_ = json.Unmarshal([]byte(savedMsg.ContentAttributes), &contentAttrs)
		submitted, ok := contentAttrs["submitted_values"].(map[string]any)
		if !ok || submitted["feedback"] != "Awesome support experience!" {
			t.Errorf("expected submitted_values in ContentAttributes: %v", savedMsg.ContentAttributes)
		}
	})

	// 9. Test: GET /api/v1/widget/inbox_members (Inbox 成员列表读取)
	t.Run("GET /inbox_members returns active public agents", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/widget/inbox_members?website_token=%s", websiteToken), nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET /inbox_members failed: code=%d body=%s", w.Code, w.Body.String())
		}

		var membersResp struct {
			Success bool `json:"success"`
			Payload []struct {
				ID            uint   `json:"id"`
				Name          string `json:"name"`
				AvailableName string `json:"available_name"`
				AvatarURL     string `json:"avatar_url"`
				Role          string `json:"role"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &membersResp); err != nil {
			t.Fatalf("failed to parse members response: %v", err)
		}
		if len(membersResp.Payload) != 1 {
			t.Fatalf("expected 1 member in inbox, got %d", len(membersResp.Payload))
		}
		member := membersResp.Payload[0]
		if member.ID != agentUser.ID || member.AvailableName != "Sam S." {
			t.Errorf("unexpected member details: %+v", member)
		}
		if member.AvatarURL != "https://avatars.internal/sam.png" {
			t.Errorf("expected avatar https://avatars.internal/sam.png, got %s", member.AvatarURL)
		}
	})

	// 10. Boundary: Invalid token handling
	t.Run("Invalid website token returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/widget/inbox_members?website_token=invalid_token_999", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Errorf("expected 404 for invalid token, got %d", w.Code)
		}
	})

	// Ensure token is used
	_ = accIDStr
	_ = token
}
