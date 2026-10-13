package modruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/stretchr/testify/require"
)

func confirmationQueueCommand(kind, key string) foundation.CommandEnvelope {
	return foundation.CommandEnvelope{CommandID: key, CommandType: "msg.email.send.v1", AccountID: 91,
		Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "91"},
		Input:          map[string]any{"email_type": kind, "to_email": "recipient@example.test", "from_email": "sender@example.test", "subject": "Confirmation queue canary", "content_html": "<a href=\"https://example.test/auth/confirmation?confirmation_token=confirmation-queue-secret-canary\">link</a>", "content_text": "confirmation-queue-secret-canary"},
		IdempotencyKey: key, CorrelationID: key, CausationID: key, RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "msg.email_delivery.get.v1"}}
}

func TestConfirmationGenericQueueRejectsBeforeAnyPersistence(t *testing.T) {
	db := newModuleTestDB(t, "MSG")
	cfg := businessConfig("MSG")
	cfg.OrdinaryEmail = OrdinaryEmailConfig{Enabled: true, From: "sender@example.test", Host: "localhost", Port: "2525"}
	runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("MSG")}
	require.NoError(t, runtime.RegisterBusinessContracts())
	call := func(command foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(runtime.Handler(), "/contracts/commands/msg.email.send.v1", command, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, command.CorrelationID)
		})
	}
	for i, kind := range []string{"confirmation", "CONFIRMATION", " confirmation ", "\tConfirmation\n"} {
		command := confirmationQueueCommand(kind, fmt.Sprintf("confirmation-reject-%d", i))
		for n := 0; n < 2; n++ {
			res := call(command)
			require.Equal(t, http.StatusBadRequest, res.Code, res.Body.String())
			require.Contains(t, res.Body.String(), "dedicated direct submission")
			require.NotContains(t, res.Body.String(), "confirmation-queue-secret-canary")
		}
		// The business handler also refuses direct invocation outside the transport.
		output, err := runtime.Contracts.commands[command.CommandType](context.Background(), command)
		require.Error(t, err)
		require.Nil(t, output)
		require.NotContains(t, err.Error(), "confirmation-queue-secret-canary")
	}
	for _, model := range []any{&domain.EmailLog{}, &domain.LocalChangeJournal{}, &domain.OutboundMessageAttempt{}} {
		var count int64
		require.NoError(t, db.Model(model).Count(&count).Error)
		require.Zero(t, count)
	}
	// Even a pre-upgrade successful receipt cannot make this retired input
	// look supported or queued after the new guard. Only the old hash is stored.
	legacy := confirmationQueueCommand("confirmation", "legacy-confirmation-receipt")
	_, err := NewCommandReceiptStore(db, "MSG").Execute(context.Background(), legacy, func(context.Context) (any, error) { return map[string]any{"delivery_status": "pending"}, nil })
	require.NoError(t, err)
	res := call(legacy)
	require.Equal(t, http.StatusBadRequest, res.Code, res.Body.String())
	var journals []domain.LocalChangeJournal
	require.NoError(t, db.Find(&journals).Error)
	require.Len(t, journals, 1)
	encoded, err := json.Marshal(journals)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "confirmation-queue-secret-canary")
	var count int64
	require.NoError(t, db.Model(&domain.EmailLog{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestConfirmationGenericQueueKeepsOtherEmailTypes(t *testing.T) {
	db := newModuleTestDB(t, "MSG")
	cfg := businessConfig("MSG")
	cfg.OrdinaryEmail = OrdinaryEmailConfig{Enabled: true, From: "sender@example.test", Host: "localhost", Port: "2525"}
	runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("MSG")}
	require.NoError(t, runtime.RegisterBusinessContracts())
	for _, kind := range []string{"transcript", "reset_password", "notification"} {
		command := confirmationQueueCommand(kind, "ordinary-"+kind)
		res := performContractRequest(runtime.Handler(), "/contracts/commands/msg.email.send.v1", command, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, command.CorrelationID)
		})
		require.Equal(t, http.StatusOK, res.Code, res.Body.String())
		var record domain.EmailLog
		require.NoError(t, db.Where("email_type = ?", kind).First(&record).Error)
		if kind == "reset_password" {
			require.Equal(t, "confirmation-queue-secret-canary", record.ContentText)
		} else {
			var intent domain.OrdinaryEmailIntent
			require.NoError(t, db.Where("email_log_id = ?", record.ID).First(&intent).Error)
			require.Contains(t, intent.Payload, "confirmation-queue-secret-canary", "ordinary type allowlist must not inspect arbitrary body text")
			require.Empty(t, record.ContentText)
		}
	}
	var logs, receipts, events int64
	require.NoError(t, db.Model(&domain.EmailLog{}).Count(&logs).Error)
	require.EqualValues(t, 3, logs)
	require.NoError(t, db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error)
	require.EqualValues(t, 3, receipts)
	require.NoError(t, db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_outbox").Count(&events).Error)
	require.EqualValues(t, 3, events)
}

func TestConfirmationGenericQueueRestrictionInventory(t *testing.T) {
	raw, err := os.ReadFile("../../architecture/module_runtime.json")
	require.NoError(t, err)
	var inventory struct {
		Modules []struct {
			Module                   string
			CommandInputRestrictions map[string]struct {
				AllowedEmailTypes, RejectedEmailTypes []string
				ConfirmationTransport                 string
				RejectBeforeReceiptReplay             bool
			}
		}
	}
	require.NoError(t, json.Unmarshal(raw, &inventory))
	found := false
	for _, module := range inventory.Modules {
		if module.Module != "MSG" {
			continue
		}
		found = true
		rule, ok := module.CommandInputRestrictions["msg.email.send.v1"]
		require.True(t, ok)
		require.Equal(t, []string{"transcript", "reset_password", "notification"}, rule.AllowedEmailTypes)
		require.Equal(t, []string{"confirmation"}, rule.RejectedEmailTypes)
		require.Equal(t, "dedicated_direct_submission", rule.ConfirmationTransport)
		require.True(t, rule.RejectBeforeReceiptReplay)
		for _, kind := range rule.AllowedEmailTypes {
			require.Nil(t, confirmationQueueRestriction(confirmationQueueCommand(kind, "metadata")))
		}
		for _, kind := range rule.RejectedEmailTypes {
			require.NotNil(t, confirmationQueueRestriction(confirmationQueueCommand(" "+strings.ToUpper(kind)+" ", "metadata")))
		}
	}
	require.True(t, found)
}
