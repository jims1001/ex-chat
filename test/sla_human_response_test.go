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
	"github.com/stretchr/testify/require"
)

func TestSLAHumanResponseEligibility(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{
		Environment:        "test",
		DBDriver:           "sqlite",
		DBPath:             fmt.Sprintf("file:sla_first_resp_%d?mode=memory&cache=shared", time.Now().UnixNano()),
		JWTSecret:          "test_secret_sla_frt_123456",
		JWTExpirationHours: 72,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}

	hub := ws.NewHub()
	r := router.SetupRouter(cfg, db, hub)

	// 1. Sign up admin user to obtain token & account
	signUpPayload := map[string]string{
		"name":         "SLA Admin",
		"email":        fmt.Sprintf("sla_admin_%d@example.com", time.Now().UnixNano()),
		"password":     "Secret123!",
		"account_name": "SLA Late Reply Account",
	}
	body, err := json.Marshal(signUpPayload)
	require.NoError(t, err)
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
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &authResp))
	require.NotEmpty(t, authResp.Data.Accounts)
	token := authResp.Data.Token
	accountID := authResp.Data.Accounts[0].ID
	agentUser := authResp.Data.User

	// 2. Create Inbox and Contact
	inbox := domain.Inbox{
		AccountID:    accountID,
		Name:         "Support Web Inbox",
		WebsiteToken: "web_inbox_token",
	}
	require.NoError(t, db.Create(&inbox).Error)

	contact := domain.Contact{
		AccountID: accountID,
		Name:      "Customer Alice",
		Email:     "alice@example.com",
	}
	require.NoError(t, db.Create(&contact).Error)

	// 3. Create SLA Policy with 60 seconds first response threshold
	slaPolicy := domain.SLAPolicy{
		AccountID:                  accountID,
		Name:                       "Strict 60s First Response SLA",
		Description:                "First response within 60 seconds",
		FirstResponseTimeThreshold: 60, // 60 seconds
		NextResponseTimeThreshold:  60,
		ResolutionTimeThreshold:    7200, // 2 hours
		OnlyDuringBusinessHours:    false,
	}
	require.NoError(t, db.Create(&slaPolicy).Error)

	slaService := service.NewSLAService(db)
	convRepo := repository.NewConversationRepository(db)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	type replyCase struct {
		humanDelay                        time.Duration
		name, sender, status, contentType string
		private, eligible                 bool
	}
	cases := []replyCase{}
	for _, sender := range []string{"AgentBot", "CaptainAssistant", "Captain::Assistant"} {
		for _, status := range []string{"sent", "delivered", "read"} {
			cases = append(cases, replyCase{name: sender + "_" + status, sender: sender, status: status}, replyCase{name: sender + "_" + status + "_then_timely_human", sender: sender, status: status, humanDelay: 30 * time.Second, eligible: true})
		}
	}
	for _, sender := range []string{"User", "", "HistoricalAgent"} {
		for _, status := range []string{"sent", "delivered", "read"} {
			cases = append(cases, replyCase{name: sender + "_" + status, sender: sender, status: status, eligible: true})
		}
	}
	cases = append(cases,
		replyCase{name: "private", sender: "User", status: "sent", private: true},
		replyCase{name: "activity", sender: "User", status: "sent", contentType: "activity"},
		replyCase{name: "internal_note", sender: "User", status: "sent", contentType: "internal_note"},
		replyCase{name: "failed", sender: "User", status: "failed"},
		replyCase{name: "queued", sender: "User", status: "queued"},
		replyCase{name: "saved", sender: "User", status: "saved"},
	)
	for _, next := range []bool{false, true} {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("next=%t/%s", next, tc.name), func(t *testing.T) {
				start := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
				conv := domain.Conversation{AccountID: accountID, InboxID: inbox.ID, ContactID: contact.ID, Status: domain.ConversationStatusOpen, CreatedAt: start, LastActivityAt: start}
				require.NoError(t, convRepo.Create(&conv))
				add := func(sender, status, contentType string, private bool, kind string, at time.Time) {
					m := domain.Message{AccountID: accountID, ConversationID: conv.ID, SenderType: sender, SenderID: agentUser.ID, MessageType: kind, Status: status, ContentType: contentType, Private: private, Content: "SLA fixture", CreatedAt: at}
					require.NoError(t, db.Create(&m).Error)
				}
				add("Contact", "sent", "text", false, domain.MessageTypeIncoming, start)
				turn := start
				if next {
					add("User", "sent", "text", false, domain.MessageTypeOutgoing, start.Add(5*time.Second))
					turn = start.Add(time.Minute)
					add("Contact", "sent", "text", false, domain.MessageTypeIncoming, turn)
				}
				add(tc.sender, tc.status, tc.contentType, tc.private, domain.MessageTypeOutgoing, turn.Add(10*time.Second))
				if tc.humanDelay > 0 {
					add("User", "delivered", "text", false, domain.MessageTypeOutgoing, turn.Add(tc.humanDelay))
				}

				check := func(wantBreach, pending bool) {
					t.Helper()
					req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d/conversations/%d/sla", accountID, conv.ID), nil)
					req.Header.Set("Authorization", "Bearer "+token)
					w := httptest.NewRecorder()
					r.ServeHTTP(w, req)
					require.Equal(t, http.StatusOK, w.Code, w.Body.String())
					var got struct {
						Data struct {
							First bool       `json:"first_response_breached"`
							Next  bool       `json:"next_response_breached"`
							Due   *time.Time `json:"next_response_due_at"`
						} `json:"data"`
					}
					require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
					require.Equal(t, !next && wantBreach, got.Data.First, "GET first response")
					require.Equal(t, next && wantBreach, got.Data.Next, "GET next response")
					if next && pending {
						require.NotNil(t, got.Data.Due)
						require.True(t, turn.Add(time.Minute).Equal(*got.Data.Due))
					} else {
						require.Nil(t, got.Data.Due)
					}
				}
				check(!tc.eligible, !tc.eligible)
				breaches, err := slaService.EvaluateConversation(&conv)
				require.NoError(t, err)
				wantCount := 0
				if !tc.eligible {
					wantCount = 1
				}
				require.Len(t, breaches, wantCount)
				var logs []domain.SLABreachLog
				require.NoError(t, db.Where("conversation_id = ?", conv.ID).Find(&logs).Error)
				require.Len(t, logs, wantCount)
				if !tc.eligible {
					wantType := "first_response"
					if next {
						wantType = "next_response"
					}
					require.Equal(t, wantType, logs[0].BreachType)
					require.Greater(t, logs[0].ActualSeconds, 60)
				}
				var stored domain.Conversation
				require.NoError(t, db.First(&stored, conv.ID).Error)
				wantStatus := "active"
				if !tc.eligible {
					wantStatus = "breached"
				}
				require.Equal(t, wantStatus, stored.SLAStatus)
				var applied domain.AppliedSLA
				require.NoError(t, db.Where("conversation_id = ?", conv.ID).First(&applied).Error)
				wantApplied := "active"
				if !tc.eligible {
					wantApplied = "active_with_misses"
				}
				require.Equal(t, wantApplied, applied.SLAStatus)
				var events []domain.SLAEvent
				require.NoError(t, db.Where("applied_sla_id = ?", applied.ID).Find(&events).Error)
				require.Len(t, events, wantCount)
				if !tc.eligible {
					wantEvent := "frt"
					if next {
						wantEvent = "nrt"
					}
					require.Equal(t, wantEvent, events[0].EventType)
				}
				if next && !tc.eligible {
					require.NotNil(t, stored.NextResponseDueAt)
				} else {
					require.Nil(t, stored.NextResponseDueAt)
				}
				check(!tc.eligible, !tc.eligible)

				// A later effective human reply stops the pending clock, while retaining
				// the breach. Actual duration must reach the human, not the earlier bot.
				if !tc.eligible {
					add("User", "read", "text", false, domain.MessageTypeOutgoing, turn.Add(90*time.Second))
					check(true, false)
					_, err = slaService.EvaluateConversation(&conv)
					require.NoError(t, err)
					logs = nil
					require.NoError(t, db.Where("conversation_id = ?", conv.ID).Find(&logs).Error)
					require.Len(t, logs, 1)
					require.Equal(t, 90, logs[0].ActualSeconds)
					require.NoError(t, db.First(&stored, conv.ID).Error)
					if next {
						var fresh domain.Conversation
						require.NoError(t, db.First(&fresh, conv.ID).Error)
						require.Nil(t, fresh.NextResponseDueAt)
					}
				}
			})
		}
	}
}
