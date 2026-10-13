package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type retryHTTPTransport func(*http.Request) (*http.Response, error)

func (f retryHTTPTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type retryHTTPPayload struct {
	Event string         `json:"event"`
	Data  domain.Message `json:"data"`
}

type retryHTTPFixture struct {
	db              *gorm.DB
	h               *ConversationHandler
	r               *gin.Engine
	conv            domain.Conversation
	message         domain.Message
	wsEvents        chan []byte
	webhookPayloads chan []byte
	webhookQueries  atomic.Int64
}

func newRetryHTTPFixture(t *testing.T, durable bool, messageType string, private bool) *retryHTTPFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "retry.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), IgnoreRelationshipsWhenMigrating: true})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = pool.Close() })
	require.NoError(t, db.AutoMigrate(&domain.Conversation{}, &domain.Message{}, &domain.Inbox{}, &domain.Contact{}, &domain.ConversationLabel{}, &domain.ConversationParticipant{}, &domain.LocalChangeJournal{}, &domain.AutomationRule{}, &domain.AutomationRuleExecution{}, &domain.Webhook{}, &domain.WebhookDelivery{}, &domain.SLAPolicy{}, &domain.AppliedSLA{}, &domain.SLABreachLog{}, &domain.SLAEvent{}))
	f := &retryHTTPFixture{db: db, r: gin.New(), wsEvents: make(chan []byte, 16), webhookPayloads: make(chan []byte, 16)}
	inbox := domain.Inbox{ID: 21, AccountID: 91, Name: "Retry inbox"}
	contact := domain.Contact{ID: 31, AccountID: 91, Name: "Blocked retry contact", Blocked: true}
	require.NoError(t, db.Create(&inbox).Error)
	require.NoError(t, db.Create(&contact).Error)
	past := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond)
	snoozedUntil := past.Add(time.Hour)
	f.conv = domain.Conversation{ID: 41, AccountID: 91, InboxID: inbox.ID, ContactID: contact.ID, ObjectVersion: 7, Status: domain.ConversationStatusResolved, LastActivityAt: past, CreatedAt: past, UpdatedAt: past, UnreadCount: 3, Muted: true, SnoozedUntil: &snoozedUntil}
	require.NoError(t, db.Create(&f.conv).Error)
	require.NoError(t, db.Create(&domain.ConversationParticipant{ConversationID: f.conv.ID, UserID: 99, CreatedAt: past}).Error)
	f.message = domain.Message{AccountID: 91, ConversationID: f.conv.ID, MessageType: messageType, Private: private, Status: domain.MessageStatusFailed, Content: "Keep the original message body", ContentType: domain.ContentTypeText, SenderType: domain.SenderTypeUser, SenderID: 7, CreatedAt: past.Add(time.Minute), UpdatedAt: past.Add(time.Minute), ContentAttributes: `{"error":"previous local failure"}`}
	if messageType == domain.MessageTypeIncoming {
		f.message.SenderType, f.message.SenderID = domain.SenderTypeContact, contact.ID
	}
	require.NoError(t, db.Create(&f.message).Error)
	conversations := repository.NewConversationRepository(db)
	messages := repository.NewMessageRepositoryWithConversationPort(db, conversations)
	conversations.SetAutomationCapture(durable)
	messages.SetAutomationCapture(durable)
	hub := ws.NewHub()
	hub.OnBroadcast = func(event *ws.Event) {
		encoded, err := json.Marshal(event)
		if err != nil {
			f.wsEvents <- []byte(`{"event":"encoding_failed"}`)
			return
		}
		f.wsEvents <- encoded
	}
	go hub.Run()
	f.h = NewConversationHandler(conversations, messages, repository.NewInboxRepository(db), repository.NewContactRepository(db), nil, hub)
	webhooks := service.NewWebhookService(db, repository.NewWebhookRepository(db))
	webhooks.SetHTTPClient(&http.Client{Transport: retryHTTPTransport(func(req *http.Request) (*http.Response, error) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		f.webhookPayloads <- body
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewBufferString(`{"ack":"mock webhook only"}`)), Header: make(http.Header)}, nil
	})})
	f.h.SetAutomationAndWebhook(nil, webhooks)
	require.NoError(t, db.Create(&domain.Webhook{AccountID: 91, URL: "https://retry.example.com/mock-only", Subscriptions: `["message_created"]`}).Error)
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:retry_webhook_queries", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "webhooks" {
			f.webhookQueries.Add(1)
		}
	}))
	f.r.Use(func(c *gin.Context) { c.Set(middleware.ContextAccountID, uint(91)); c.Next() })
	f.r.POST("/conversations/:id/messages/:message_id/retry", f.h.RetryMessage)
	return f
}

func (f *retryHTTPFixture) request() *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	f.r.ServeHTTP(response, httptest.NewRequest(http.MethodPost, fmt.Sprintf("/conversations/%d/messages/%d/retry", f.conv.ID, f.message.ID), nil))
	return response
}

func retryReceive(t *testing.T, channel <-chan []byte) []byte {
	t.Helper()
	select {
	case payload := <-channel:
		return payload
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for local retry fanout")
		return nil
	}
}

func TestRetryMessageHTTPLocalOutcomeAndReplayFanout(t *testing.T) {
	for _, durable := range []bool{false, true} {
		for _, messageType := range []string{domain.MessageTypeOutgoing, domain.MessageTypeIncoming, domain.MessageTypeActivity} {
			for _, private := range []bool{false, true} {
				t.Run(fmt.Sprintf("durable=%t/%s/private=%t", durable, messageType, private), func(t *testing.T) {
					f := newRetryHTTPFixture(t, durable, messageType, private)
					outcome := domain.MessageStatusSaved
					if messageType == domain.MessageTypeOutgoing && !private {
						outcome = domain.MessageStatusQueued
					}
					response := f.request()
					require.Equal(t, http.StatusOK, response.Code, response.Body.String())
					var result retryHTTPPayload
					require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result))
					require.Equal(t, outcome, result.Data.Status)
					require.Equal(t, f.message.ID, result.Data.ID)
					require.Equal(t, f.message.Content, result.Data.Content)
					require.Equal(t, f.message.ContentAttributes, result.Data.ContentAttributes)
					require.Equal(t, messageType, result.Data.MessageType)
					require.Equal(t, private, result.Data.Private)
					require.True(t, f.message.CreatedAt.Equal(result.Data.CreatedAt))
					var stored domain.Message
					require.NoError(t, f.db.First(&stored, f.message.ID).Error)
					require.Equal(t, outcome, stored.Status)
					require.True(t, stored.UpdatedAt.Equal(result.Data.UpdatedAt))
					var messages int64
					require.NoError(t, f.db.Model(&domain.Message{}).Count(&messages).Error)
					require.EqualValues(t, 1, messages, "retry must keep the same row")
					for _, name := range []string{ws.EventMessageUpdated, ws.EventMessageCreated} {
						var event retryHTTPPayload
						require.NoError(t, json.Unmarshal(retryReceive(t, f.wsEvents), &event))
						require.Equal(t, name, event.Event)
						require.Equal(t, result.Data, event.Data, "WS and HTTP must report the same local outcome")
					}
					var webhook retryHTTPPayload
					require.NoError(t, json.Unmarshal(retryReceive(t, f.webhookPayloads), &webhook))
					require.Equal(t, "message_created", webhook.Event)
					require.Equal(t, result.Data, webhook.Data, "mock webhook acceptance is not provider delivery")
					require.Eventually(t, func() bool {
						var count int64
						return f.db.Model(&domain.WebhookDelivery{}).Where("status = ?", "delivered").Count(&count).Error == nil && count == 1
					}, 2*time.Second, 5*time.Millisecond)
					var after domain.Conversation
					require.NoError(t, f.db.First(&after, f.conv.ID).Error)
					require.Equal(t, f.conv.ObjectVersion+1, after.ObjectVersion, "handler must not add a second durable activity update")
					require.Equal(t, f.conv.UnreadCount, after.UnreadCount)
					require.True(t, after.LastActivityAt.After(f.conv.LastActivityAt))
					wantStatus := domain.ConversationStatusResolved
					if messageType == domain.MessageTypeIncoming {
						wantStatus = domain.ConversationStatusOpen
						require.Nil(t, after.SnoozedUntil)
					}
					require.Equal(t, wantStatus, after.Status, "legacy retry reopens incoming even when private, muted and blocked")
					var participants []domain.ConversationParticipant
					require.NoError(t, f.db.Find(&participants).Error)
					require.Len(t, participants, 1)
					require.EqualValues(t, 99, participants[0].UserID)
					facts, err := f.h.msgRepo.PendingAutomationFacts(context.Background(), 10)
					require.NoError(t, err)
					if durable {
						require.Len(t, facts, 1)
						require.Equal(t, outcome, facts[0].Payload["local_outcome"])
						require.Equal(t, "retry", facts[0].Payload["operation"])
						if messageType == domain.MessageTypeIncoming {
							require.Equal(t, domain.ConversationStatusResolved, facts[0].Payload["status_before"])
							require.Equal(t, domain.ConversationStatusOpen, facts[0].Payload["status_after"])
						} else {
							require.NotContains(t, facts[0].Payload, "status_before")
						}
					} else {
						require.Empty(t, facts)
					}
					// Exact HTTP replay must not fan out or bump activity again. The hub
					// barrier and synchronous webhook query count avoid sleep-based checks.
					queries := f.webhookQueries.Load()
					replayed := f.request()
					if durable {
						require.Equal(t, http.StatusOK, replayed.Code, replayed.Body.String())
						require.JSONEq(t, response.Body.String(), replayed.Body.String())
					} else {
						require.Equal(t, http.StatusBadRequest, replayed.Code)
					}
					require.Equal(t, queries, f.webhookQueries.Load(), "replay must not dispatch another webhook")
					f.h.hub.Broadcast(&ws.Event{Name: "retry.test.barrier"})
					var barrier retryHTTPPayload
					require.NoError(t, json.Unmarshal(retryReceive(t, f.wsEvents), &barrier))
					require.Equal(t, "retry.test.barrier", barrier.Event, "replay must not emit another WS event")
					var replayedConversation domain.Conversation
					require.NoError(t, f.db.First(&replayedConversation, f.conv.ID).Error)
					require.Equal(t, after, replayedConversation)
					for _, action := range []string{"retry_attempt", "retry_activity", "automation_trigger"} {
						var count int64
						require.NoError(t, f.db.Model(&domain.LocalChangeJournal{}).Where("action = ?", action).Count(&count).Error)
						want := int64(0)
						if durable {
							want = 1
						}
						require.Equal(t, want, count, action)
					}
				})
			}
		}
	}
}

func TestRetryMessageHTTPLegacyCallbackObservesResolvedBeforeReopen(t *testing.T) {
	f := newRetryHTTPFixture(t, false, domain.MessageTypeIncoming, true)
	f.h.hub, f.h.webhookService = nil, nil
	f.h.automationService = service.NewAutomationService(f.db, f.h.convRepo, f.h.msgRepo)
	resolvedRule := domain.AutomationRule{AccountID: 91, Name: "Pre-reopen resolved condition", EventName: "message_created", Active: true, Conditions: `[{"attribute_key":"status","filter_operator":"is","values":["resolved"]}]`, Actions: `[{"action_name":"change_priority","action_params":["urgent"]}]`}
	openRule := domain.AutomationRule{AccountID: 91, Name: "Post-reopen must not match", EventName: "message_created", Active: true, Conditions: `[{"attribute_key":"status","filter_operator":"is","values":["open"]}]`, Actions: `[{"action_name":"change_priority","action_params":["low"]}]`}
	require.NoError(t, f.db.Create(&resolvedRule).Error)
	require.NoError(t, f.db.Create(&openRule).Error)
	response := f.request()
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var after domain.Conversation
	require.NoError(t, f.db.First(&after, f.conv.ID).Error)
	require.Equal(t, domain.ConversationStatusOpen, after.Status)
	require.Equal(t, "urgent", after.Priority, "actual legacy callback must use the pre-reopen conversation observation")
	require.Equal(t, f.conv.UnreadCount, after.UnreadCount)
	var executions []domain.AutomationRuleExecution
	require.NoError(t, f.db.Find(&executions).Error)
	require.Len(t, executions, 1)
	require.Equal(t, resolvedRule.ID, executions[0].RuleID)
	require.Equal(t, "message_created", executions[0].EventName)
	require.Equal(t, "success", executions[0].Status)
	require.NotNil(t, executions[0].MessageID)
	require.Equal(t, f.message.ID, *executions[0].MessageID)
	var participants int64
	require.NoError(t, f.db.Model(&domain.ConversationParticipant{}).Count(&participants).Error)
	require.EqualValues(t, 1, participants)
	require.Equal(t, http.StatusBadRequest, f.request().Code)
	var count int64
	require.NoError(t, f.db.Model(&domain.AutomationRuleExecution{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestRetryMessageHTTPPendingOutcomeDoesNotEvaluateSLA(t *testing.T) {
	for _, durable := range []bool{false, true} {
		for _, private := range []bool{false, true} {
			t.Run(fmt.Sprintf("durable=%t/private=%t", durable, private), func(t *testing.T) {
				f := newRetryHTTPFixture(t, durable, domain.MessageTypeOutgoing, private)
				f.h.hub, f.h.webhookService = nil, nil
				f.h.SetSLAService(service.NewSLAService(f.db, f.h.convRepo))
				deadline := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
				require.NoError(t, f.db.Model(&domain.Conversation{}).Where("id = ?", f.conv.ID).Updates(map[string]any{"status": domain.ConversationStatusOpen, "first_response_due_at": deadline, "next_response_due_at": deadline, "resolution_due_at": deadline}).Error)
				policy := domain.SLAPolicy{AccountID: 91, Name: "Retry SLA", FirstResponseTimeThreshold: 3600, NextResponseTimeThreshold: 3600, ResolutionTimeThreshold: 3600}
				require.NoError(t, f.db.Create(&policy).Error)
				applied := domain.AppliedSLA{AccountID: 91, ConversationID: f.conv.ID, SLAPolicyID: policy.ID, SLAStatus: domain.AppliedSLAStatusActive}
				require.NoError(t, f.db.Create(&applied).Error)
				var evaluations atomic.Int64
				require.NoError(t, f.db.Callback().Query().Before("gorm:query").Register("test:retry_sla_evaluation", func(tx *gorm.DB) {
					if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "sla_policies" {
						evaluations.Add(1)
					}
				}))
				response := f.request()
				require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				require.Zero(t, evaluations.Load(), "pending retry must never invoke reply-SLA evaluation")
				var after domain.Conversation
				require.NoError(t, f.db.First(&after, f.conv.ID).Error)
				require.NotNil(t, after.FirstResponseDueAt)
				require.NotNil(t, after.NextResponseDueAt)
				require.NotNil(t, after.ResolutionDueAt)
				require.True(t, deadline.Equal(*after.FirstResponseDueAt))
				require.True(t, deadline.Equal(*after.NextResponseDueAt))
				require.True(t, deadline.Equal(*after.ResolutionDueAt))
				var afterApplied domain.AppliedSLA
				require.NoError(t, f.db.First(&afterApplied, applied.ID).Error)
				require.Equal(t, applied.SLAStatus, afterApplied.SLAStatus)
				require.True(t, applied.UpdatedAt.Equal(afterApplied.UpdatedAt))
				var events int64
				require.NoError(t, f.db.Model(&domain.SLAEvent{}).Count(&events).Error)
				require.Zero(t, events)
			})
		}
	}
}

func TestRetryMessageSLAPeriodicEvaluationRequiresTransportStatus(t *testing.T) {
	for _, metric := range []string{"first_response", "next_response"} {
		for _, status := range []string{domain.MessageStatusFailed, domain.MessageStatusQueued, domain.MessageStatusSaved, domain.MessageStatusSent, domain.MessageStatusDelivered, domain.MessageStatusRead} {
			t.Run(metric+"/"+status, func(t *testing.T) {
				f := newRetryHTTPFixture(t, true, domain.MessageTypeOutgoing, false)
				f.h.hub, f.h.webhookService = nil, nil
				require.NoError(t, f.db.Model(&domain.Conversation{}).Where("id = ?", f.conv.ID).Update("status", domain.ConversationStatusOpen).Error)
				if status == domain.MessageStatusQueued {
					response := f.request()
					require.Equal(t, http.StatusOK, response.Code, response.Body.String())
				} else {
					// Receipt-bearing statuses are control fixtures, never provider sends.
					require.NoError(t, f.db.Model(&domain.Message{}).Where("id = ?", f.message.ID).Update("status", status).Error)
				}
				incoming := domain.Message{AccountID: 91, ConversationID: f.conv.ID, SenderType: domain.SenderTypeContact, SenderID: 31, MessageType: domain.MessageTypeIncoming, Content: "Awaiting a reply", Status: domain.MessageStatusSaved, CreatedAt: f.conv.CreatedAt, UpdatedAt: f.conv.CreatedAt}
				require.NoError(t, f.db.Create(&incoming).Error)
				if metric == "next_response" {
					firstIncoming := incoming
					firstIncoming.ID = 0
					firstIncoming.CreatedAt = incoming.CreatedAt.Add(-15 * time.Minute)
					firstReply := domain.Message{AccountID: 91, ConversationID: f.conv.ID, SenderType: domain.SenderTypeUser, SenderID: 7, MessageType: domain.MessageTypeOutgoing, Content: "Previous acknowledged reply", Status: domain.MessageStatusSent, CreatedAt: incoming.CreatedAt.Add(-14 * time.Minute)}
					require.NoError(t, f.db.Create(&firstIncoming).Error)
					require.NoError(t, f.db.Create(&firstReply).Error)
				}
				policy := domain.SLAPolicy{AccountID: 91, Name: "Periodic retry SLA", FirstResponseTimeThreshold: 3600, NextResponseTimeThreshold: 3600, ResolutionTimeThreshold: 86400}
				require.NoError(t, f.db.Create(&policy).Error)
				require.NoError(t, f.db.Model(&policy).Update("only_during_business_hours", false).Error)
				timeline, err := f.h.msgRepo.ListSLATimeline(91, f.conv.ID)
				require.NoError(t, err)
				var ids []uint
				for _, message := range timeline {
					ids = append(ids, message.ID)
				}
				require.Contains(t, ids, incoming.ID, "saved incoming records still start response clocks")
				eligible := status == domain.MessageStatusSent || status == domain.MessageStatusDelivered || status == domain.MessageStatusRead
				if eligible {
					require.Contains(t, ids, f.message.ID)
				} else {
					require.NotContains(t, ids, f.message.ID, "unacknowledged outgoing work must not satisfy SLA response clocks")
				}
				sla := service.NewSLAService(f.db, f.h.convRepo)
				sla.SetOwnerRepositories(repository.NewInboxRepository(f.db), f.h.msgRepo)
				_, err = sla.EvaluateAccountSLAs(91)
				require.NoError(t, err)
				var breaches int64
				require.NoError(t, f.db.Model(&domain.SLABreachLog{}).Where("conversation_id = ? AND breach_type = ?", f.conv.ID, metric).Count(&breaches).Error)
				if eligible {
					require.Zero(t, breaches, "an actual receipt-bearing reply before deadline is eligible")
				} else {
					require.EqualValues(t, 1, breaches, "queued/failed/saved outgoing rows cannot hide an overdue unanswered message")
				}
				if metric == "next_response" {
					var after domain.Conversation
					require.NoError(t, f.db.First(&after, f.conv.ID).Error)
					if eligible {
						require.Nil(t, after.NextResponseDueAt)
					} else {
						require.NotNil(t, after.NextResponseDueAt)
						require.True(t, incoming.CreatedAt.Add(time.Hour).Equal(*after.NextResponseDueAt))
					}
				}
			})
		}
	}
}

func TestRetryMessageHTTPDurableNonRetryableStateRemainsBadRequest(t *testing.T) {
	for _, status := range []string{domain.MessageStatusQueued, domain.MessageStatusSaved, domain.MessageStatusSent, domain.MessageStatusDelivered, domain.MessageStatusRead} {
		t.Run("no_receipt/"+status, func(t *testing.T) {
			f := newRetryHTTPFixture(t, true, domain.MessageTypeOutgoing, false)
			require.NoError(t, f.db.Model(&domain.Message{}).Where("id = ?", f.message.ID).Update("status", status).Error)
			response := f.request()
			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			require.Zero(t, f.webhookQueries.Load())
			f.h.hub.Broadcast(&ws.Event{Name: "retry.test.barrier"})
			var event retryHTTPPayload
			require.NoError(t, json.Unmarshal(retryReceive(t, f.wsEvents), &event))
			require.Equal(t, "retry.test.barrier", event.Event)
			var after domain.Conversation
			require.NoError(t, f.db.First(&after, f.conv.ID).Error)
			require.Equal(t, f.conv.ObjectVersion, after.ObjectVersion)
			require.True(t, f.conv.LastActivityAt.Equal(after.LastActivityAt))
			var message domain.Message
			require.NoError(t, f.db.First(&message, f.message.ID).Error)
			require.Equal(t, status, message.Status)
			var journals int64
			require.NoError(t, f.db.Model(&domain.LocalChangeJournal{}).Count(&journals).Error)
			require.Zero(t, journals, "an unrelated nonfailed row has no retry attempt to replay")
		})
	}
	for _, status := range []string{domain.MessageStatusSent, domain.MessageStatusDelivered, domain.MessageStatusRead} {
		t.Run("provider_advanced/"+status, func(t *testing.T) {
			f := newRetryHTTPFixture(t, true, domain.MessageTypeOutgoing, false)
			f.h.hub, f.h.webhookService = nil, nil
			accepted := f.request()
			require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())
			var acceptedMessage retryHTTPPayload
			require.NoError(t, json.Unmarshal(accepted.Body.Bytes(), &acceptedMessage))
			// Seed a later receipt-bearing state locally; no provider is contacted.
			require.NoError(t, f.db.Model(&domain.Message{}).Where("id = ?", f.message.ID).Updates(map[string]any{"status": status, "updated_at": acceptedMessage.Data.UpdatedAt.Add(time.Second)}).Error)
			var before domain.Conversation
			require.NoError(t, f.db.First(&before, f.conv.ID).Error)
			rejected := f.request()
			require.Equal(t, http.StatusBadRequest, rejected.Code, rejected.Body.String())
			var message domain.Message
			require.NoError(t, f.db.First(&message, f.message.ID).Error)
			require.Equal(t, status, message.Status, "old retry acceptance must not overwrite an advanced provider state")
			var after domain.Conversation
			require.NoError(t, f.db.First(&after, f.conv.ID).Error)
			require.Equal(t, before, after)
			for _, action := range []string{"retry_attempt", "retry_activity", "automation_trigger"} {
				var count int64
				require.NoError(t, f.db.Model(&domain.LocalChangeJournal{}).Where("action = ?", action).Count(&count).Error)
				require.EqualValues(t, 1, count, action)
			}
		})
	}
}
