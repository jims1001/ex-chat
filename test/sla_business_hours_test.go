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
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
)

// standardWorkingHoursJSON returns Mon-Fri 09:00-17:00, Sat-Sun closed
func standardWorkingHoursJSON() string {
	schedules := []domain.WorkingHourConfig{
		{DayOfWeek: 0, Closed: true}, // Sunday
		{DayOfWeek: 1, OpenHour: 9, OpenMinute: 0, CloseHour: 17, CloseMinute: 0, Closed: false}, // Monday
		{DayOfWeek: 2, OpenHour: 9, OpenMinute: 0, CloseHour: 17, CloseMinute: 0, Closed: false}, // Tuesday
		{DayOfWeek: 3, OpenHour: 9, OpenMinute: 0, CloseHour: 17, CloseMinute: 0, Closed: false}, // Wednesday
		{DayOfWeek: 4, OpenHour: 9, OpenMinute: 0, CloseHour: 17, CloseMinute: 0, Closed: false}, // Thursday
		{DayOfWeek: 5, OpenHour: 9, OpenMinute: 0, CloseHour: 17, CloseMinute: 0, Closed: false}, // Friday
		{DayOfWeek: 6, Closed: true}, // Saturday
	}
	b, _ := json.Marshal(schedules)
	return string(b)
}

func TestBusinessHours_DeadlineCalculation(t *testing.T) {
	inbox := &domain.Inbox{
		WorkingHoursEnabled: true,
		Timezone:            "UTC",
		WorkingHours:        standardWorkingHoursJSON(),
	}

	// Case 1: Wall-clock deadline when working_hours_enabled is false
	t.Run("WallClock_WhenWorkingHoursDisabled", func(t *testing.T) {
		disabledInbox := &domain.Inbox{
			WorkingHoursEnabled: false,
			Timezone:            "UTC",
			WorkingHours:        standardWorkingHoursJSON(),
		}
		startTime := time.Date(2024, 1, 19, 16, 0, 0, 0, time.UTC)
		deadline := service.CalculateDeadline(disabledInbox, startTime, 3600)
		expected := startTime.Add(1 * time.Hour)
		if !deadline.Equal(expected) {
			t.Fatalf("expected wall-clock deadline %v, got %v", expected, deadline)
		}
	})

	// Case 2: Wall-clock deadline when all days are closed
	t.Run("WallClock_WhenAllDaysClosed", func(t *testing.T) {
		allClosed := []domain.WorkingHourConfig{
			{DayOfWeek: 0, Closed: true},
			{DayOfWeek: 1, Closed: true},
			{DayOfWeek: 2, Closed: true},
			{DayOfWeek: 3, Closed: true},
			{DayOfWeek: 4, Closed: true},
			{DayOfWeek: 5, Closed: true},
			{DayOfWeek: 6, Closed: true},
		}
		raw, _ := json.Marshal(allClosed)
		closedInbox := &domain.Inbox{
			WorkingHoursEnabled: true,
			Timezone:            "UTC",
			WorkingHours:        string(raw),
		}
		startTime := time.Date(2024, 1, 19, 16, 0, 0, 0, time.UTC)
		deadline := service.CalculateDeadline(closedInbox, startTime, 3600)
		expected := startTime.Add(1 * time.Hour)
		if !deadline.Equal(expected) {
			t.Fatalf("expected wall-clock deadline %v, got %v", expected, deadline)
		}
	})

	// Case 3: Calculate deadline within the same day
	// Wednesday 10:00 AM + 2 hours = Wednesday 12:00 PM
	t.Run("WithinSameDay", func(t *testing.T) {
		startTime := time.Date(2024, 1, 17, 10, 0, 0, 0, time.UTC) // Wednesday
		expected := time.Date(2024, 1, 17, 12, 0, 0, 0, time.UTC)
		deadline := service.CalculateDeadline(inbox, startTime, 2*3600)
		if !deadline.Equal(expected) {
			t.Fatalf("expected %v, got %v", expected, deadline)
		}
	})

	// Case 4: Spans to next business day when threshold exceeds remaining hours (Friday to Monday)
	// Friday 4:00 PM + 2 hours = Monday 10:00 AM (1h Friday 16-17 + 1h Monday 9-10)
	t.Run("WeekendRollover", func(t *testing.T) {
		friday4pm := time.Date(2024, 1, 19, 16, 0, 0, 0, time.UTC)
		monday10am := time.Date(2024, 1, 22, 10, 0, 0, 0, time.UTC)
		deadline := service.CalculateDeadline(inbox, friday4pm, 2*3600)
		if !deadline.Equal(monday10am) {
			t.Fatalf("expected weekend rollover deadline %v, got %v", monday10am, deadline)
		}
	})

	// Case 5: Start time before business hours
	// Wednesday 7:00 AM + 2 hours = Wednesday 11:00 AM (starts counting at 9 AM)
	t.Run("BeforeBusinessHours", func(t *testing.T) {
		wednesday7am := time.Date(2024, 1, 17, 7, 0, 0, 0, time.UTC)
		expected := time.Date(2024, 1, 17, 11, 0, 0, 0, time.UTC)
		deadline := service.CalculateDeadline(inbox, wednesday7am, 2*3600)
		if !deadline.Equal(expected) {
			t.Fatalf("expected %v, got %v", expected, deadline)
		}
	})

	// Case 6: Start time after business hours
	// Wednesday 6:00 PM + 2 hours = Thursday 11:00 AM
	t.Run("AfterBusinessHours", func(t *testing.T) {
		wednesday6pm := time.Date(2024, 1, 17, 18, 0, 0, 0, time.UTC)
		expected := time.Date(2024, 1, 18, 11, 0, 0, 0, time.UTC)
		deadline := service.CalculateDeadline(inbox, wednesday6pm, 2*3600)
		if !deadline.Equal(expected) {
			t.Fatalf("expected %v, got %v", expected, deadline)
		}
	})

	// Case 7: Start time on a closed day
	// Saturday 10:00 AM + 2 hours = Monday 11:00 AM
	t.Run("ClosedDayStart", func(t *testing.T) {
		saturday10am := time.Date(2024, 1, 20, 10, 0, 0, 0, time.UTC)
		expected := time.Date(2024, 1, 22, 11, 0, 0, 0, time.UTC)
		deadline := service.CalculateDeadline(inbox, saturday10am, 2*3600)
		if !deadline.Equal(expected) {
			t.Fatalf("expected %v, got %v", expected, deadline)
		}
	})

	// Case 8: Threshold spans multiple business days
	// Monday 4:00 PM + 10 hours = Wednesday 10:00 AM
	// Monday: 1h (16-17), Tuesday: 8h (9-17), Wednesday: 1h (9-10 AM)
	t.Run("MultipleDaysSpan", func(t *testing.T) {
		monday4pm := time.Date(2024, 1, 15, 16, 0, 0, 0, time.UTC)
		expected := time.Date(2024, 1, 17, 10, 0, 0, 0, time.UTC)
		deadline := service.CalculateDeadline(inbox, monday4pm, 10*3600)
		if !deadline.Equal(expected) {
			t.Fatalf("expected %v, got %v", expected, deadline)
		}
	})

	// Case 9: Respects Inbox Timezone
	// Timezone America/New_York (EST, UTC-5). Friday 16:00 EST + 2 hours = Monday 10:00 EST
	t.Run("TimezoneAwareness", func(t *testing.T) {
		loc, err := time.LoadLocation("America/New_York")
		if err != nil {
			t.Skip("skipping timezone test, location not available:", err)
		}
		nyInbox := &domain.Inbox{
			WorkingHoursEnabled: true,
			Timezone:            "America/New_York",
			WorkingHours:        standardWorkingHoursJSON(),
		}
		friday4pmEST := time.Date(2024, 1, 19, 16, 0, 0, 0, loc)
		monday10amEST := time.Date(2024, 1, 22, 10, 0, 0, 0, loc)
		deadline := service.CalculateDeadline(nyInbox, friday4pmEST, 2*3600)
		if !deadline.Equal(monday10amEST.UTC()) {
			t.Fatalf("expected %v (UTC %v), got %v", monday10amEST, monday10amEST.UTC(), deadline)
		}
	})

	// Case 10: Open all day (24 hours)
	t.Run("OpenAllDay", func(t *testing.T) {
		allDaySchedule := []domain.WorkingHourConfig{
			{DayOfWeek: 6, OpenAllDay: true, Closed: false}, // Saturday open all day
			{DayOfWeek: 0, Closed: true},
			{DayOfWeek: 1, OpenHour: 9, CloseHour: 17, Closed: false},
		}
		b, _ := json.Marshal(allDaySchedule)
		allDayInbox := &domain.Inbox{
			WorkingHoursEnabled: true,
			Timezone:            "UTC",
			WorkingHours:        string(b),
		}

		saturday10am := time.Date(2024, 1, 20, 10, 0, 0, 0, time.UTC)
		expected := time.Date(2024, 1, 20, 12, 0, 0, 0, time.UTC)
		deadline := service.CalculateDeadline(allDayInbox, saturday10am, 2*3600)
		if !deadline.Equal(expected) {
			t.Fatalf("expected %v, got %v", expected, deadline)
		}
	})

	// Case 11: Different business hours on different days
	// Monday (day 1): 9:00-17:00 (8h), Tuesday (day 2): 9:00-20:00 (11h)
	t.Run("VaryingHoursPerDay", func(t *testing.T) {
		varying := []domain.WorkingHourConfig{
			{DayOfWeek: 1, OpenHour: 9, CloseHour: 17, Closed: false}, // Mon 9-17
			{DayOfWeek: 2, OpenHour: 9, CloseHour: 20, Closed: false}, // Tue 9-20
		}
		b, _ := json.Marshal(varying)
		vInbox := &domain.Inbox{
			WorkingHoursEnabled: true,
			Timezone:            "UTC",
			WorkingHours:        string(b),
		}

		// Monday 18:00 (after close) + 10 hours -> Tuesday 09:00 + 10h = Tuesday 19:00
		mon6pm := time.Date(2024, 1, 15, 18, 0, 0, 0, time.UTC)
		tue7pm := time.Date(2024, 1, 16, 19, 0, 0, 0, time.UTC)
		dl := service.CalculateDeadline(vInbox, mon6pm, 10*3600)
		if !dl.Equal(tue7pm) {
			t.Fatalf("expected %v, got %v", tue7pm, dl)
		}

		// Monday 16:00 + 12 hours -> Mon 1h (16-17) + Tue 11h (9-20) = Tuesday 20:00
		mon4pm := time.Date(2024, 1, 15, 16, 0, 0, 0, time.UTC)
		tue8pm := time.Date(2024, 1, 16, 20, 0, 0, 0, time.UTC)
		dl2 := service.CalculateDeadline(vInbox, mon4pm, 12*3600)
		if !dl2.Equal(tue8pm) {
			t.Fatalf("expected %v, got %v", tue8pm, dl2)
		}
	})
}

func TestBusinessHours_CalculateBusinessSeconds(t *testing.T) {
	inbox := &domain.Inbox{
		WorkingHoursEnabled: true,
		Timezone:            "UTC",
		WorkingHours:        standardWorkingHoursJSON(),
	}

	// Start Friday 16:00, End Monday 10:30 (open 9-17, Sat-Sun closed)
	// Friday 16-17: 1h (3600s), Sat-Sun: 0s, Monday 09:00-10:30: 1.5h (5400s)
	// Total = 9000 seconds
	start := time.Date(2024, 1, 19, 16, 0, 0, 0, time.UTC)
	end := time.Date(2024, 1, 22, 10, 30, 0, 0, time.UTC)
	sec := service.CalculateBusinessSeconds(inbox, start, end)
	if sec != 9000 {
		t.Fatalf("expected 9000 business seconds, got %d", sec)
	}

	// Over weekend only: Friday 16:00 to Saturday 12:00
	// Only 1h on Friday: 3600s
	satNoon := time.Date(2024, 1, 20, 12, 0, 0, 0, time.UTC)
	secWeekend := service.CalculateBusinessSeconds(inbox, start, satNoon)
	if secWeekend != 3600 {
		t.Fatalf("expected 3600 business seconds, got %d", secWeekend)
	}
}

func TestSLA_AccountEvaluation_BusinessHours_BreachAndNoBreach(t *testing.T) {
	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_sla_123456",
		JWTExpirationHours: 72,
	}
	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	accountRepo := repository.NewAccountRepository(db)
	inboxRepo := repository.NewInboxRepository(db)
	convRepo := repository.NewConversationRepository(db)
	slaService := service.NewSLAService(db)

	account := domain.Account{Name: "SLA Business Hours Tenant"}
	_ = accountRepo.Create(&account)

	// Inbox with Working Hours: Mon-Fri 09:00-17:00 UTC, Sat-Sun closed
	inbox := domain.Inbox{
		AccountID:           account.ID,
		Name:                "Support Inbox",
		WebsiteToken:        "sla_token_123",
		Timezone:            "UTC",
		WorkingHoursEnabled: true,
		WorkingHours:        standardWorkingHoursJSON(),
	}
	_ = inboxRepo.Create(&inbox)

	// SLA Policy: 2 hours First Response (7200s), 8 hours Resolution (28800s)
	slaPolicy := domain.SLAPolicy{
		AccountID:                   account.ID,
		Name:                        "Standard Business Hours SLA",
		FirstResponseTimeThreshold: 7200,
		ResolutionTimeThreshold:    28800,
		OnlyDuringBusinessHours:     true,
	}
	_ = db.Create(&slaPolicy).Error

	// Scenario A: Customer created conversation on Friday at 16:30 UTC
	// Available on Friday: 30 minutes (until 17:00).
	// Threshold: 2 hours (120 minutes).
	// Remaining: 90 minutes.
	// Rollover across weekend (Sat-Sun closed) to Monday 09:00.
	// Deadline: Monday 10:30 UTC!
	friday1630 := time.Date(2024, 1, 19, 16, 30, 0, 0, time.UTC)

	convA := domain.Conversation{
		AccountID:      account.ID,
		InboxID:        inbox.ID,
		ContactID:      101,
		Status:         domain.ConversationStatusOpen,
		SLAStatus:      "active",
		CreatedAt:      friday1630,
		LastActivityAt: friday1630,
	}
	_ = convRepo.Create(&convA)
	_ = db.Model(&convA).Update("created_at", friday1630).Error

	// Verify deadline calculation for convA
	frtDue, resDue, isFRT, isRes, p := slaService.GetConversationSLADeadlines(&convA)
	if p == nil || p.ID != slaPolicy.ID {
		t.Fatalf("expected resolved policy to match %d", slaPolicy.ID)
	}
	expectedFRTDue := time.Date(2024, 1, 22, 10, 30, 0, 0, time.UTC) // Monday 10:30 UTC
	if frtDue == nil || !frtDue.Equal(expectedFRTDue) {
		t.Fatalf("expected conversation FirstResponseDueAt %v, got %v", expectedFRTDue, frtDue)
	}
	// Resolution is 8 hours: Friday 30m + Monday 7.5h (09:00 - 16:30) = Monday 16:30 UTC
	expectedResDue := time.Date(2024, 1, 22, 16, 30, 0, 0, time.UTC)
	if resDue == nil || !resDue.Equal(expectedResDue) {
		t.Fatalf("expected conversation ResolutionDueAt %v, got %v", expectedResDue, resDue)
	}
	_ = isFRT
	_ = isRes

	// Now verify EvaluateAccountSLAs:
	now := time.Now().UTC()
	// Conversation created right now:
	convFuture := domain.Conversation{
		AccountID:      account.ID,
		InboxID:        inbox.ID,
		ContactID:      102,
		Status:         domain.ConversationStatusOpen,
		SLAStatus:      "active",
		CreatedAt:      now,
		LastActivityAt: now,
	}
	_ = convRepo.Create(&convFuture)

	// Add customer incoming message
	inMsg := domain.Message{
		AccountID:      account.ID,
		ConversationID: convFuture.ID,
		SenderType:     domain.SenderTypeContact,
		MessageType:    domain.MessageTypeIncoming,
		Content:        "Hello please help",
		CreatedAt:      now,
	}
	_ = db.Create(&inMsg).Error

	breaches, err := slaService.EvaluateAccountSLAs(account.ID)
	if err != nil {
		t.Fatalf("unexpected error evaluating SLAs: %v", err)
	}

	// convFuture must NOT be breached
	for _, b := range breaches {
		if b.ConversationID == convFuture.ID {
			t.Fatalf("convFuture should NOT be breached, but breach recorded: %+v", b)
		}
	}

	// Verify conversation updated with due dates
	refreshed, _ := convRepo.FindByID(account.ID, convFuture.ID)
	if refreshed.FirstResponseDueAt == nil {
		t.Fatalf("expected first_response_due_at to be populated on conversation")
	}
	if refreshed.ResolutionDueAt == nil {
		t.Fatalf("expected resolution_due_at to be populated on conversation")
	}
	if refreshed.SLAStatus != "active" {
		t.Fatalf("expected sla_status 'active', got '%s'", refreshed.SLAStatus)
	}
}

func TestSLA_APIEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             ":memory:",
		JWTSecret:          "test_secret_sla_api_123456",
		JWTExpirationHours: 72,
	}
	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// Sign up admin user
	signUpPayload := map[string]string{
		"name":         "SLA Admin",
		"email":        "sla_admin@example.com",
		"password":     "Secret123!",
		"account_name": "SLA Enterprise Corp",
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
	_ = json.Unmarshal(w.Body.Bytes(), &authResp)
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID

	// Create an Inbox
	inboxRepo := repository.NewInboxRepository(db)
	inbox := domain.Inbox{
		AccountID:           accountID,
		Name:                "API Inbox",
		WebsiteToken:        "api_sla_token",
		WorkingHoursEnabled: true,
		Timezone:            "UTC",
		WorkingHours:        standardWorkingHoursJSON(),
	}
	_ = inboxRepo.Create(&inbox)

	// Create SLA Policy via API
	createSLAPayload := map[string]any{
		"name":                          "Enterprise Standard SLA",
		"first_response_time_threshold": 1800,
		"resolution_time_threshold":    7200,
		"only_during_business_hours":    true,
	}
	createSLABody, _ := json.Marshal(createSLAPayload)
	reqSLA := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/sla_policies", accountID), bytes.NewReader(createSLABody))
	reqSLA.Header.Set("Authorization", "Bearer "+token)
	reqSLA.Header.Set("Content-Type", "application/json")
	wSLA := httptest.NewRecorder()
	r.ServeHTTP(wSLA, reqSLA)
	if wSLA.Code != http.StatusCreated {
		t.Fatalf("create sla failed: code=%d body=%s", wSLA.Code, wSLA.Body.String())
	}

	var slaResp struct {
		Data domain.SLAPolicy `json:"data"`
	}
	_ = json.Unmarshal(wSLA.Body.Bytes(), &slaResp)
	policyID := slaResp.Data.ID

	// Create a Conversation
	convRepo := repository.NewConversationRepository(db)
	conv := domain.Conversation{
		AccountID:   accountID,
		InboxID:     inbox.ID,
		ContactID:   200,
		SLAPolicyID: &policyID,
		Status:      domain.ConversationStatusOpen,
		SLAStatus:   "active",
		CreatedAt:   time.Now().UTC(),
	}
	_ = convRepo.Create(&conv)

	// 1. GET /api/v1/accounts/:account_id/conversations/:id/sla
	t.Run("GetConversationSLA", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/sla", accountID, conv.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}

		var resp struct {
			Data struct {
				ConversationID            uint   `json:"conversation_id"`
				SLAStatus                 string `json:"sla_status"`
				FirstResponseDueAt        string `json:"first_response_due_at"`
				FirstResponseRemainingSec *int   `json:"first_response_remaining_sec"`
				ResolutionDueAt           string `json:"resolution_due_at"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp.Data.ConversationID != conv.ID {
			t.Fatalf("expected conversation ID %d, got %d", conv.ID, resp.Data.ConversationID)
		}
		if resp.Data.FirstResponseDueAt == "" {
			t.Fatalf("expected non-empty first_response_due_at")
		}
	})

	// 2. GET /api/v1/accounts/:account_id/sla_policies/:id
	t.Run("GetSLAPolicy", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/sla_policies/%d", accountID, policyID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}
	})

	// 3. DELETE /api/v1/accounts/:account_id/sla_policies/:id
	t.Run("DeleteSLAPolicy", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("/api/v1/accounts/%d/sla_policies/%d", accountID, policyID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}

		// Verify deleted
		var count int64
		db.Model(&domain.SLAPolicy{}).Where("account_id = ? AND id = ?", accountID, policyID).Count(&count)
		if count != 0 {
			t.Fatalf("expected policy to be deleted from DB, but count = %d", count)
		}
	})
}
