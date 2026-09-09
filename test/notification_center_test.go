package test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

func TestNotificationCenter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_notification_center_123456",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up test user
	signUpPayload := map[string]string{
		"name":         "Notification Agent",
		"email":        "notif_agent@example.com",
		"password":     "Secret123!",
		"account_name": "Notification Enterprise",
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
			User     domain.User      `json:"user"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &authResp); err != nil {
		t.Fatalf("failed to parse sign up resp: %v", err)
	}

	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID
	userID := authResp.Data.User.ID
	basePath := fmt.Sprintf("/api/v1/accounts/%d/notifications", accountID)

	// Helper function for authorized HTTP requests
	sendReq := func(method, path string, payload any) *httptest.ResponseRecorder {
		var reqBody *bytes.Reader
		if payload != nil {
			b, _ := json.Marshal(payload)
			reqBody = bytes.NewReader(b)
		} else {
			reqBody = bytes.NewReader([]byte{})
		}
		req := httptest.NewRequest(method, path, reqBody)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec
	}

	// Sub-test 1: Unread count on clean state
	t.Run("Initial_Unread_Count", func(t *testing.T) {
		rec := sendReq(http.MethodGet, basePath+"/unread_count", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Data struct {
				UnreadCount  int64 `json:"unread_count"`
				Count        int64 `json:"count"`
				SnoozedCount int64 `json:"snoozed_count"`
			} `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if resp.Data.UnreadCount != 0 || resp.Data.Count != 0 {
			t.Fatalf("expected 0 unread, got %d", resp.Data.UnreadCount)
		}
	})

	// Create test notifications directly in DB
	n1 := domain.Notification{
		AccountID:        accountID,
		UserID:           userID,
		NotificationType: domain.NotificationTypeConversationAssignment,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	n2 := domain.Notification{
		AccountID:        accountID,
		UserID:           userID,
		NotificationType: domain.NotificationTypeConversationMention,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	n3 := domain.Notification{
		AccountID:        accountID,
		UserID:           userID,
		NotificationType: domain.NotificationTypeSLABreach,
		CreatedAt:        time.Now().UTC(),
		UpdatedAt:        time.Now().UTC(),
	}
	db.Create(&n1)
	db.Create(&n2)
	db.Create(&n3)

	// Sub-test 2: Unread Count with Active Notifications
	t.Run("Unread_Count_After_Creating_Notifications", func(t *testing.T) {
		rec := sendReq(http.MethodGet, basePath+"/unread_count", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var resp struct {
			Data struct {
				UnreadCount  int64 `json:"unread_count"`
				Count        int64 `json:"count"`
				SnoozedCount int64 `json:"snoozed_count"`
			} `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &resp)
		if resp.Data.UnreadCount != 3 || resp.Data.Count != 3 {
			t.Fatalf("expected 3 unread, got %d unread, %d total", resp.Data.UnreadCount, resp.Data.Count)
		}
	})

	// Sub-test 3: Single Notification Read and Unread
	t.Run("Single_Notification_Mark_Read_and_Unread", func(t *testing.T) {
		// Mark n1 as read
		rec := sendReq(http.MethodPost, fmt.Sprintf("%s/%d/read", basePath, n1.ID), nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var readResp struct {
			Data domain.Notification `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &readResp)
		if readResp.Data.ReadAt == nil {
			t.Fatalf("expected read_at to be non-nil")
		}

		// Verify unread count decreased to 2
		recCnt := sendReq(http.MethodGet, basePath+"/unread_count", nil)
		var cntResp struct {
			Data struct {
				UnreadCount int64 `json:"unread_count"`
			} `json:"data"`
		}
		_ = json.Unmarshal(recCnt.Body.Bytes(), &cntResp)
		if cntResp.Data.UnreadCount != 2 {
			t.Fatalf("expected 2 unread, got %d", cntResp.Data.UnreadCount)
		}

		// Mark n1 back to unread
		recUnread := sendReq(http.MethodPost, fmt.Sprintf("%s/%d/unread", basePath, n1.ID), nil)
		if recUnread.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", recUnread.Code, recUnread.Body.String())
		}
		var unreadResp struct {
			Data domain.Notification `json:"data"`
		}
		_ = json.Unmarshal(recUnread.Body.Bytes(), &unreadResp)
		if unreadResp.Data.ReadAt != nil {
			t.Fatalf("expected read_at to be nil after mark unread")
		}

		// Verify unread count restored to 3
		recCnt2 := sendReq(http.MethodGet, basePath+"/unread_count", nil)
		_ = json.Unmarshal(recCnt2.Body.Bytes(), &cntResp)
		if cntResp.Data.UnreadCount != 3 {
			t.Fatalf("expected 3 unread, got %d", cntResp.Data.UnreadCount)
		}
	})

	// Sub-test 4: Snooze (Pause) and Unsnooze
	t.Run("Snooze_and_Unsnooze_Notification", func(t *testing.T) {
		// Snooze n2 with duration "1h"
		snoozePayload := map[string]string{"duration": "1h"}
		rec := sendReq(http.MethodPost, fmt.Sprintf("%s/%d/snooze", basePath, n2.ID), snoozePayload)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}
		var snoozeResp struct {
			Data domain.Notification `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &snoozeResp)
		if snoozeResp.Data.SnoozedUntil == nil {
			t.Fatalf("expected snoozed_until to be set")
		}

		// Active unread count should now be 2 (n2 is paused/snoozed), snoozed_count should be 1
		recCnt := sendReq(http.MethodGet, basePath+"/unread_count", nil)
		var cntResp struct {
			Data struct {
				UnreadCount  int64 `json:"unread_count"`
				Count        int64 `json:"count"`
				SnoozedCount int64 `json:"snoozed_count"`
			} `json:"data"`
		}
		_ = json.Unmarshal(recCnt.Body.Bytes(), &cntResp)
		if cntResp.Data.UnreadCount != 2 || cntResp.Data.SnoozedCount != 1 {
			t.Fatalf("expected 2 active unread & 1 snoozed, got %d unread & %d snoozed", cntResp.Data.UnreadCount, cntResp.Data.SnoozedCount)
		}

		// List without include_snoozed should exclude n2
		recList := sendReq(http.MethodGet, basePath, nil)
		var listResp struct {
			Data []domain.Notification `json:"data"`
		}
		_ = json.Unmarshal(recList.Body.Bytes(), &listResp)
		if len(listResp.Data) != 2 {
			t.Fatalf("expected 2 active notifications, got %d", len(listResp.Data))
		}

		// List with status=snoozed should return only n2
		recSnoozedList := sendReq(http.MethodGet, basePath+"?status=snoozed", nil)
		var snoozedListResp struct {
			Data []domain.Notification `json:"data"`
		}
		_ = json.Unmarshal(recSnoozedList.Body.Bytes(), &snoozedListResp)
		if len(snoozedListResp.Data) != 1 || snoozedListResp.Data[0].ID != n2.ID {
			t.Fatalf("expected only snoozed n2, got %v", snoozedListResp.Data)
		}

		// Unsnooze n2
		recUnsnooze := sendReq(http.MethodPost, fmt.Sprintf("%s/%d/unsnooze", basePath, n2.ID), nil)
		if recUnsnooze.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", recUnsnooze.Code, recUnsnooze.Body.String())
		}
		var unsnoozeResp struct {
			Data domain.Notification `json:"data"`
		}
		_ = json.Unmarshal(recUnsnooze.Body.Bytes(), &unsnoozeResp)
		if unsnoozeResp.Data.SnoozedUntil != nil {
			t.Fatalf("expected snoozed_until to be nil after unsnooze")
		}

		// Active unread count returns to 3
		recCntAfter := sendReq(http.MethodGet, basePath+"/unread_count", nil)
		_ = json.Unmarshal(recCntAfter.Body.Bytes(), &cntResp)
		if cntResp.Data.UnreadCount != 3 || cntResp.Data.SnoozedCount != 0 {
			t.Fatalf("expected 3 unread & 0 snoozed after unsnooze, got %d unread & %d snoozed", cntResp.Data.UnreadCount, cntResp.Data.SnoozedCount)
		}
	})

	// Sub-test 5: PATCH notification endpoint
	t.Run("Patch_Notification", func(t *testing.T) {
		readTrue := true
		patchPayload := map[string]any{"read": readTrue}
		rec := sendReq(http.MethodPatch, fmt.Sprintf("%s/%d", basePath, n3.ID), patchPayload)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var patchResp struct {
			Data domain.Notification `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &patchResp)
		if patchResp.Data.ReadAt == nil {
			t.Fatalf("expected read_at to be non-nil after patch")
		}
	})

	// Sub-test 6: Delete single notification
	t.Run("Delete_Single_Notification", func(t *testing.T) {
		rec := sendReq(http.MethodDelete, fmt.Sprintf("%s/%d", basePath, n3.ID), nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		// Deleting again should return 404
		rec2 := sendReq(http.MethodDelete, fmt.Sprintf("%s/%d", basePath, n3.ID), nil)
		if rec2.Code != http.StatusNotFound {
			t.Fatalf("expected 404 for deleted notification, got %d", rec2.Code)
		}
	})

	// Sub-test 7: Batch Delete Notifications
	t.Run("Batch_Delete_Notifications", func(t *testing.T) {
		// First mark n1 as read, n2 remains unread
		_ = sendReq(http.MethodPost, fmt.Sprintf("%s/%d/read", basePath, n1.ID), nil)

		// Batch delete only read notifications
		batchPayload := map[string]any{"only_read": true}
		rec := sendReq(http.MethodPost, basePath+"/batch_destroy", batchPayload)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
		}

		var batchResp struct {
			Data struct {
				DeletedCount int64 `json:"deleted_count"`
			} `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &batchResp)
		if batchResp.Data.DeletedCount != 1 {
			t.Fatalf("expected 1 deleted notification, got %d", batchResp.Data.DeletedCount)
		}

		// Batch delete n2 by ID
		batchIDPayload := map[string]any{"ids": []uint{n2.ID}}
		rec2 := sendReq(http.MethodPost, basePath+"/batch_destroy", batchIDPayload)
		if rec2.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rec2.Code, rec2.Body.String())
		}
		_ = json.Unmarshal(rec2.Body.Bytes(), &batchResp)
		if batchResp.Data.DeletedCount != 1 {
			t.Fatalf("expected 1 deleted notification, got %d", batchResp.Data.DeletedCount)
		}

		// Verify count is now 0
		recCnt := sendReq(http.MethodGet, basePath+"/unread_count", nil)
		var cntResp struct {
			Data struct {
				Count int64 `json:"count"`
			} `json:"data"`
		}
		_ = json.Unmarshal(recCnt.Body.Bytes(), &cntResp)
		if cntResp.Data.Count != 0 {
			t.Fatalf("expected 0 total notifications after batch delete, got %d", cntResp.Data.Count)
		}
	})

	// Sub-test 8: Notification Preferences / Settings
	t.Run("Notification_Settings_Management", func(t *testing.T) {
		settingsPath := fmt.Sprintf("/api/v1/accounts/%d/notification_settings", accountID)

		// 1. Get default settings
		recGet := sendReq(http.MethodGet, settingsPath, nil)
		if recGet.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", recGet.Code, recGet.Body.String())
		}
		var getResp struct {
			Data domain.NotificationSetting `json:"data"`
		}
		_ = json.Unmarshal(recGet.Body.Bytes(), &getResp)
		if getResp.Data.Muted {
			t.Fatalf("expected default muted to be false")
		}
		if getResp.Data.SelectedEmailFlags == "" {
			t.Fatalf("expected default selected_email_flags to be initialized")
		}

		// 2. Update settings: mute all, enable quiet hours
		muted := true
		quietHours := true
		start := "23:00"
		end := "07:00"
		updatePayload := map[string]any{
			"selected_email_flags": []string{"conversation_assignment"},
			"selected_push_flags":  []string{"sla_breach"},
			"selected_in_app_flags": []string{
				domain.NotificationTypeConversationAssignment,
				domain.NotificationTypeSLABreach,
			},
			"muted":               &muted,
			"quiet_hours_enabled": &quietHours,
			"quiet_hours_start":   &start,
			"quiet_hours_end":     &end,
		}

		recPut := sendReq(http.MethodPut, settingsPath, updatePayload)
		if recPut.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", recPut.Code, recPut.Body.String())
		}

		var putResp struct {
			Data domain.NotificationSetting `json:"data"`
		}
		_ = json.Unmarshal(recPut.Body.Bytes(), &putResp)
		if !putResp.Data.Muted {
			t.Fatalf("expected muted to be true after update")
		}
		if !putResp.Data.QuietHoursEnabled || putResp.Data.QuietHoursStart != "23:00" || putResp.Data.QuietHoursEnd != "07:00" {
			t.Fatalf("expected quiet hours 23:00-07:00, got %s-%s", putResp.Data.QuietHoursStart, putResp.Data.QuietHoursEnd)
		}

		// 3. Verify persistence with another GET
		recVerify := sendReq(http.MethodGet, settingsPath, nil)
		var verifyResp struct {
			Data domain.NotificationSetting `json:"data"`
		}
		_ = json.Unmarshal(recVerify.Body.Bytes(), &verifyResp)
		if !verifyResp.Data.Muted || !verifyResp.Data.QuietHoursEnabled {
			t.Fatalf("expected persisted muted=true and quiet_hours=true")
		}
	})
}
