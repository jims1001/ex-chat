package modruntime

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestAutomationActivityOwnerNoopReceiptPolicy(t *testing.T) {
	for _, operation := range []string{"remove_assigned_agent", "remove_assigned_team", "mute_conversation", "snooze_conversation", "remove_label"} {
		t.Run(operation, func(t *testing.T) {
			r := newAutomationOwnerRuntime(t, "CON")
			initial := seedAutomationOwnerConversation(t, r)
			// Future activity deliberately exercises non-regression during a clock skew.
			initial.LastActivityAt = time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
			input := map[string]any{"operation": operation, "activity_policy": automationTouchValidActivity}
			if operation == "mute_conversation" {
				initial.Status = "resolved"
				initial.Muted = true
			}
			if operation == "snooze_conversation" {
				until := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Microsecond)
				initial.Status = "snoozed"
				initial.SnoozedUntil = &until
				input["snoozed_until"] = until.Format(time.RFC3339Nano)
			}
			if operation == "remove_label" {
				input["label_id"] = 0
				input["missing_label"] = "missing"
			}
			require.NoError(t, r.DB.Save(&initial).Error)
			command := automationOwnerCommand("touch", "con.automation.apply.v1", 1, input)
			first := callAutomationOwner(t, r, command, http.StatusOK)
			var envelope struct {
				Data automationConversationActivityView `json:"data"`
			}
			require.NoError(t, json.Unmarshal(first.Body.Bytes(), &envelope))
			require.True(t, envelope.Data.ActivityOnly)
			require.Equal(t, 2, envelope.Data.ObjectVersion)
			require.True(t, envelope.Data.LastActivityAt.After(initial.LastActivityAt))
			require.Equal(t, initial.Status, envelope.Data.Status)
			require.Equal(t, initial.Muted, envelope.Data.Muted)
			require.Equal(t, initial.SnoozedUntil, envelope.Data.SnoozedUntil)
			var receipt domain.LocalChangeJournal
			require.NoError(t, r.DB.Where("command_id = ? AND action = ?", command.CommandID, "command_receipt").First(&receipt).Error)
			require.Contains(t, receipt.AfterState, `"activity_only":true`)
			replay := automationOwnerConversation(t, callAutomationOwner(t, r, command, http.StatusOK))
			require.Equal(t, 2, replay.ObjectVersion)
			require.True(t, replay.LastActivityAt.Equal(envelope.Data.LastActivityAt))
			var after domain.LocalChangeJournal
			require.NoError(t, r.DB.First(&after, receipt.ID).Error)
			require.Equal(t, receipt, after)
			var events int64
			require.NoError(t, r.DB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_outbox").Count(&events).Error)
			require.Zero(t, events, "activity-only must not invent an automation descendant")
			// A distinct valid action touches once; reusing the old key with changed
			// policy conflicts instead of reinterpreting its successful receipt.
			without := command
			without.Input = map[string]any{}
			for k, v := range command.Input {
				if k != "activity_policy" {
					without.Input[k] = v
				}
			}
			callAutomationOwner(t, r, without, http.StatusConflict)
			second := command
			second.CommandID = "touch-again"
			second.IdempotencyKey = "touch-again"
			second.Target.ExpectedVersion = 2
			next := automationOwnerConversation(t, callAutomationOwner(t, r, second, http.StatusOK))
			require.Equal(t, 3, next.ObjectVersion)
			require.True(t, next.LastActivityAt.After(replay.LastActivityAt))
		})
	}
}

func TestAutomationActivityOwnerRejectsUnrecognizedOrUnprovedTouches(t *testing.T) {
	r := newAutomationOwnerRuntime(t, "CON")
	initial := seedAutomationOwnerConversation(t, r)
	cases := []map[string]any{
		{"operation": "remove_assigned_agent", "activity_policy": ""},
		{"operation": "remove_assigned_agent", "activity_policy": "touch_future.v2"},
		{"operation": "remove_assigned_agent", "activity_policy": true},
		{"operation": "send_webhook", "activity_policy": automationTouchValidActivity},
		{"operation": "remove_label", "label_id": 0, "activity_policy": automationTouchValidActivity},
		{"operation": "remove_label", "label_id": 0, "missing_label": " ", "activity_policy": automationTouchValidActivity},
		{"operation": "remove_label", "label_id": nil, "missing_label": "missing", "activity_policy": automationTouchValidActivity},
		{"operation": "assign_agent", "assignee_id": 0, "activity_policy": automationTouchValidActivity},
	}
	for i, input := range cases {
		callAutomationOwner(t, r, automationOwnerCommand(fmt.Sprint("reject-", i), "con.automation.apply.v1", 1, input), http.StatusBadRequest)
	}
	var after domain.Conversation
	require.NoError(t, r.DB.First(&after, initial.ID).Error)
	require.Equal(t, initial.ObjectVersion, after.ObjectVersion)
	require.True(t, initial.LastActivityAt.Equal(after.LastActivityAt))
	var count int64
	require.NoError(t, r.DB.Model(&domain.LocalChangeJournal{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestAutomationActivityOwnerReceiptFailureRollsBackTouch(t *testing.T) {
	r := newAutomationOwnerRuntime(t, "CON")
	initial := seedAutomationOwnerConversation(t, r)
	require.NoError(t, r.DB.Exec(`CREATE TRIGGER reject_activity_receipt BEFORE UPDATE ON local_change_journals WHEN NEW.action = 'command_receipt' AND NEW.result = 'applied' BEGIN SELECT RAISE(ABORT, 'receipt completion unavailable'); END`).Error)
	command := automationOwnerCommand("rollback-touch", "con.automation.apply.v1", 1, map[string]any{"operation": "remove_assigned_agent", "activity_policy": automationTouchValidActivity})
	callAutomationOwner(t, r, command, http.StatusServiceUnavailable)
	var after domain.Conversation
	require.NoError(t, r.DB.First(&after, initial.ID).Error)
	require.Equal(t, initial.ObjectVersion, after.ObjectVersion)
	require.True(t, initial.LastActivityAt.Equal(after.LastActivityAt))
	var count int64
	require.NoError(t, r.DB.Model(&domain.LocalChangeJournal{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, r.DB.Exec(`DROP TRIGGER reject_activity_receipt`).Error)
	applied := automationOwnerConversation(t, callAutomationOwner(t, r, command, http.StatusOK))
	require.Equal(t, 2, applied.ObjectVersion)
	require.True(t, applied.LastActivityAt.After(initial.LastActivityAt))
}

func TestPostgresAutomationActivityOwnerConcurrentNoops(t *testing.T) {
	cfg := businessConfig("CON")
	cfg.MaxTimeout = 10 * time.Second
	cfg.AllowedCallers["AUT"] = true
	r := &Runtime{Config: cfg, DB: postgresModuleDB(t, "CON"), Contracts: NewContractRegistry("CON")}
	require.NoError(t, r.RegisterBusinessContracts())
	initial := seedAutomationOwnerConversation(t, r)
	handler := r.Handler()
	for round := 0; round < 2; round++ {
		command := automationOwnerCommand(fmt.Sprint("concurrent-activity-", round), "con.automation.apply.v1", uint64(round+1), map[string]any{"operation": "remove_assigned_agent", "activity_policy": automationTouchValidActivity})
		start, done := make(chan struct{}), make(chan int, 8)
		for i := 0; i < 8; i++ {
			cmd := command
			if round == 1 {
				cmd.CommandID = fmt.Sprintf("distinct-noop-%d", i)
				cmd.IdempotencyKey = cmd.CommandID
			}
			go func() {
				<-start
				response := performContractRequest(handler, "/contracts/commands/"+cmd.CommandType, cmd, func(req *http.Request) {
					req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
					req.Header.Set(headerCaller, "AUT")
					req.Header.Set(headerCorrelationID, cmd.CorrelationID)
				})
				done <- response.Code
			}()
		}
		close(start)
		counts := map[int]int{}
		for i := 0; i < 8; i++ {
			counts[<-done]++
		}
		if round == 0 {
			require.Equal(t, map[int]int{200: 8}, counts)
		} else {
			require.Equal(t, map[int]int{200: 1, 409: 7}, counts)
		}
		var current domain.Conversation
		require.NoError(t, r.DB.First(&current, initial.ID).Error)
		require.Equal(t, round+2, current.ObjectVersion)
		require.True(t, current.LastActivityAt.After(initial.LastActivityAt))
	}
	var receipts, events int64
	require.NoError(t, r.DB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error)
	require.NoError(t, r.DB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_outbox").Count(&events).Error)
	require.Equal(t, int64(2), receipts)
	require.Zero(t, events)
}
