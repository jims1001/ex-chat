package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestVoiceCallLifecycle(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "voice_call_test_secret_32bytes_rnd!",
		JWTExpirationHours: 24,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// Create test account and user
	account := &domain.Account{Name: "Voice Call Org"}
	if err := db.Create(account).Error; err != nil {
		t.Fatalf("failed to create account: %v", err)
	}

	user := &domain.User{
		Email: "agent@voicecall.org",
		Name:  "Voice Agent",
		Role:  domain.RoleAdministrator,
	}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	accountUser := &domain.AccountUser{
		AccountID: account.ID,
		UserID:    user.ID,
		Role:      domain.RoleAdministrator,
	}
	if err := db.Create(accountUser).Error; err != nil {
		t.Fatalf("failed to create account user: %v", err)
	}

	// Create Inbox and Contact
	inbox := &domain.Inbox{
		AccountID:   account.ID,
		Name:        "WhatsApp Voice Channel",
		ChannelType: "Channel::Whatsapp",
	}
	if err := db.Create(inbox).Error; err != nil {
		t.Fatalf("failed to create inbox: %v", err)
	}

	contact := &domain.Contact{
		AccountID:   account.ID,
		Name:        "Alice Caller",
		PhoneNumber: "+15551234567",
		Identifier:  "+15551234567",
	}
	if err := db.Create(contact).Error; err != nil {
		t.Fatalf("failed to create contact: %v", err)
	}

	// Generate Auth Token
	token, err := auth.GenerateToken(user, cfg.JWTSecret, cfg.JWTExpirationHours)
	if err != nil {
		t.Fatalf("failed to generate token: %v", err)
	}

	doAuthRequest := func(method, url string, body any) *httptest.ResponseRecorder {
		var reqBody *bytes.Buffer
		if body != nil {
			jsonBytes, _ := json.Marshal(body)
			reqBody = bytes.NewBuffer(jsonBytes)
		} else {
			reqBody = bytes.NewBuffer(nil)
		}
		req, _ := http.NewRequest(method, url, reqBody)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// -------------------------------------------------------------
	// 1. Create a Call and verify WhatsApp Call 详情 (Detail)
	// -------------------------------------------------------------
	call := &domain.Call{
		AccountID:      account.ID,
		ContactID:      contact.ID,
		InboxID:        inbox.ID,
		AgentID:        &user.ID,
		Status:         "ringing",
		Direction:      "outbound",
		Provider:       "whatsapp",
		ProviderCallID: "wamid_call_test_12345",
		SDPOffer:       "v=0\r\no=test 123456 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\n",
		CreatedAt:      time.Now().Add(-10 * time.Second),
	}
	if err := db.Create(call).Error; err != nil {
		t.Fatalf("failed to create seed call: %v", err)
	}

	t.Run("1. Get WhatsApp Call Detail", func(t *testing.T) {
		// By numeric Call ID
		url := fmt.Sprintf("/api/v1/accounts/%d/whatsapp_calls/%d", account.ID, call.ID)
		w := doAuthRequest("GET", url, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d, body: %s", w.Code, w.Body.String())
		}

		var resp struct {
			Data struct {
				ID             uint   `json:"id"`
				CallID         string `json:"call_id"`
				Provider       string `json:"provider"`
				Status         string `json:"status"`
				Direction      string `json:"direction"`
				SDPOffer       string `json:"sdp_offer"`
				Caller         struct {
					Name  string `json:"name"`
					Phone string `json:"phone"`
				} `json:"caller"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}
		if resp.Data.ID != call.ID {
			t.Fatalf("expected call id %d, got %d", call.ID, resp.Data.ID)
		}
		if resp.Data.CallID != "wamid_call_test_12345" {
			t.Fatalf("expected provider_call_id, got %s", resp.Data.CallID)
		}
		if resp.Data.Caller.Name != "Alice Caller" || resp.Data.Caller.Phone != "+15551234567" {
			t.Fatalf("expected caller Alice Caller, got %+v", resp.Data.Caller)
		}

		// Also check alias /calls/:id/whatsapp
		urlAlias := fmt.Sprintf("/api/v1/accounts/%d/calls/%d/whatsapp", account.ID, call.ID)
		wAlias := doAuthRequest("GET", urlAlias, nil)
		if wAlias.Code != http.StatusOK {
			t.Fatalf("alias route expected 200, got %d", wAlias.Code)
		}

		// Also check standard /calls/:id
		wCall := doAuthRequest("GET", fmt.Sprintf("/api/v1/accounts/%d/calls/%d", account.ID, call.ID), nil)
		if wCall.Code != http.StatusOK {
			t.Fatalf("standard get call expected 200, got %d", wCall.Code)
		}
	})

	// -------------------------------------------------------------
	// 2. 录音上传 (Recording Upload) via JSON & Multipart Form
	// -------------------------------------------------------------
	t.Run("2. Upload Call Recording", func(t *testing.T) {
		// Test JSON upload to /calls/:id/recordings
		recPayload := map[string]any{
			"recording_url": "https://storage.exchat.internal/recordings/call_12345.mp3",
			"recording_sid": "RE1234567890abcdef",
			"duration":      125,
		}
		url := fmt.Sprintf("/api/v1/accounts/%d/calls/%d/recordings", account.ID, call.ID)
		w := doAuthRequest("POST", url, recPayload)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d, body: %s", w.Code, w.Body.String())
		}

		var resp struct {
			Data struct {
				Status       string `json:"status"`
				CallID       uint   `json:"call_id"`
				RecordingURL string `json:"recording_url"`
				RecordingSID string `json:"recording_sid"`
				Duration     int    `json:"duration"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal recording upload response: %v", err)
		}
		if resp.Data.Status != "uploaded" {
			t.Fatalf("expected status uploaded, got %s", resp.Data.Status)
		}
		if resp.Data.Duration != 125 {
			t.Fatalf("expected duration 125, got %d", resp.Data.Duration)
		}

		// Test multipart upload to /whatsapp_calls/:id/upload_recording
		bodyBuf := &bytes.Buffer{}
		writer := multipart.NewWriter(bodyBuf)
		part, err := writer.CreateFormFile("recording", "call_audio.wav")
		if err != nil {
			t.Fatalf("failed to create form file: %v", err)
		}
		_, _ = part.Write([]byte("RIFFmockaudiobytes"))
		_ = writer.Close()

		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/whatsapp_calls/%d/upload_recording", account.ID, call.ID), bodyBuf)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		recRecorder := httptest.NewRecorder()
		r.ServeHTTP(recRecorder, req)
		if recRecorder.Code != http.StatusOK {
			t.Fatalf("multipart recording upload failed, code: %d, body: %s", recRecorder.Code, recRecorder.Body.String())
		}
	})

	// -------------------------------------------------------------
	// 3. Conference Token & Conference Creation
	// -------------------------------------------------------------
	t.Run("3. Conference Token", func(t *testing.T) {
		// GET /inboxes/:id/conference/token
		url := fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/conference/token", account.ID, inbox.ID)
		w := doAuthRequest("GET", url, nil)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d, body: %s", w.Code, w.Body.String())
		}

		var resp struct {
			Data struct {
				Token      string `json:"token"`
				Identity   string `json:"identity"`
				Room       string `json:"room"`
				ICEServers []any  `json:"ice_servers"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal conference token response: %v", err)
		}
		if resp.Data.Token == "" {
			t.Fatalf("expected non-empty conference token")
		}
		if len(resp.Data.ICEServers) == 0 {
			t.Fatalf("expected ice_servers in conference token response")
		}
	})

	// -------------------------------------------------------------
	// 4. Conference 删除 (Delete Conference)
	// -------------------------------------------------------------
	t.Run("4. Delete Conference", func(t *testing.T) {
		// First create a conference
		conf := &domain.Conference{
			AccountID: account.ID,
			InboxID:   inbox.ID,
			CallSID:   "CF1234567890abcdef",
			Token:     "mock_conf_token_xyz",
			Status:    "active",
		}
		if err := db.Create(conf).Error; err != nil {
			t.Fatalf("failed to create conference: %v", err)
		}

		// Delete via /inboxes/:id/conference
		urlInbox := fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d/conference", account.ID, inbox.ID)
		wInbox := doAuthRequest("DELETE", urlInbox, nil)
		if wInbox.Code != http.StatusOK {
			t.Fatalf("expected 200 for inbox conference delete, got %d", wInbox.Code)
		}

		// Re-create conference and delete via /conferences/:id
		conf2 := &domain.Conference{
			AccountID: account.ID,
			InboxID:   inbox.ID,
			CallSID:   "CF0987654321fedcba",
			Token:     "mock_conf_token_abc",
			Status:    "active",
		}
		if err := db.Create(conf2).Error; err != nil {
			t.Fatalf("failed to create second conference: %v", err)
		}

		urlConf := fmt.Sprintf("/api/v1/accounts/%d/conferences/%d", account.ID, conf2.ID)
		wConf := doAuthRequest("DELETE", urlConf, nil)
		if wConf.Code != http.StatusOK {
			t.Fatalf("expected 200 for direct conference delete, got %d", wConf.Code)
		}

		// Verify deletion
		var check domain.Conference
		err := db.Where("id = ?", conf2.ID).First(&check).Error
		if err == nil {
			t.Fatalf("expected conference to be deleted")
		}
	})

	// -------------------------------------------------------------
	// 5. 完整 Terminate 流程 (Complete Terminate Flow)
	// -------------------------------------------------------------
	t.Run("5. Complete Terminate Flow", func(t *testing.T) {
		// Create an in-progress call to terminate
		termCall := &domain.Call{
			AccountID: account.ID,
			ContactID: contact.ID,
			InboxID:   inbox.ID,
			Status:    "in_progress",
			Direction: "inbound",
			Provider:  "whatsapp",
			CreatedAt: time.Now().Add(-45 * time.Second),
		}
		if err := db.Create(termCall).Error; err != nil {
			t.Fatalf("failed to create call for terminate test: %v", err)
		}

		// Terminate with reason and explicit duration
		termPayload := map[string]any{
			"reason":   "completed",
			"duration": 50,
		}
		url := fmt.Sprintf("/api/v1/accounts/%d/calls/%d/terminate", account.ID, termCall.ID)
		w := doAuthRequest("POST", url, termPayload)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d, body: %s", w.Code, w.Body.String())
		}

		var resp struct {
			Data domain.Call `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal terminate response: %v", err)
		}
		if resp.Data.Status != "completed" {
			t.Fatalf("expected call status completed, got %s", resp.Data.Status)
		}
		if resp.Data.TerminateReason != "completed" {
			t.Fatalf("expected reason completed, got %s", resp.Data.TerminateReason)
		}
		if resp.Data.Duration != 50 {
			t.Fatalf("expected duration 50, got %d", resp.Data.Duration)
		}

		// Also test rejection termination via /whatsapp_calls/:id/terminate
		rejectCall := &domain.Call{
			AccountID: account.ID,
			ContactID: contact.ID,
			InboxID:   inbox.ID,
			Status:    "ringing",
			Direction: "outbound",
			Provider:  "whatsapp",
		}
		if err := db.Create(rejectCall).Error; err != nil {
			t.Fatalf("failed to create reject call: %v", err)
		}

		rejectPayload := map[string]any{
			"reason": "agent_rejected",
		}
		wReject := doAuthRequest("POST", fmt.Sprintf("/api/v1/accounts/%d/whatsapp_calls/%d/terminate", account.ID, rejectCall.ID), rejectPayload)
		if wReject.Code != http.StatusOK {
			t.Fatalf("expected 200 for reject terminate, got %d", wReject.Code)
		}
		var rejectResp struct {
			Data domain.Call `json:"data"`
		}
		_ = json.Unmarshal(wReject.Body.Bytes(), &rejectResp)
		if rejectResp.Data.Status != "rejected" {
			t.Fatalf("expected rejected status, got %s", rejectResp.Data.Status)
		}
		if rejectResp.Data.TerminateReason != "agent_rejected" {
			t.Fatalf("expected agent_rejected reason, got %s", rejectResp.Data.TerminateReason)
		}
	})
}
