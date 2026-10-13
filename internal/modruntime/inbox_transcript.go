package modruntime

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
)

const inboxTranscriptReceiptRoute = "_inbox_route"

func loadInboxTranscriptAdmission(getenv func(string) string) (bool, error) {
	flag := strings.TrimSpace(getenv("INBOX_TRANSCRIPT_QUEUE_ENABLED"))
	if flag != "" && flag != "true" && flag != "false" {
		return false, errors.New("INBOX_TRANSCRIPT_QUEUE_ENABLED must be true or false")
	}
	return flag == "true", nil
}
func inboxTranscriptCommandID(prefix, scope string) string {
	raw, _ := hex.DecodeString(scope)
	return prefix + base64.RawURLEncoding.EncodeToString(raw)
}
func accountInboxTranscriptCommandID(scope string) string {
	return inboxTranscriptCommandID("acct-inbox-v1:", scope)
}
func widgetInboxTranscriptCommandID(scope string) string {
	return inboxTranscriptCommandID("wdg-inbox-v1:", scope)
}
func ordinaryAccountTranscriptIdentity(commandID, scope string) bool {
	return commandID == accountTranscriptCommandID(scope) || commandID == accountInboxTranscriptCommandID(scope)
}
func ordinaryWidgetTranscriptIdentity(commandID, scope string) bool {
	return commandID == widgetTranscriptCommandID(scope) || commandID == widgetInboxTranscriptCommandID(scope)
}
func ordinaryInboxTranscriptCommand(commandID string) bool {
	return strings.HasPrefix(commandID, "acct-inbox-v1:") || strings.HasPrefix(commandID, "wdg-inbox-v1:")
}

// Command IDs were arbitrary before Inbox admission existed. A prefix alone
// cannot reserve an old or new global caller's identity. Retained request
// metadata distinguishes an Inbox request, even after its route is scrubbed.
func ordinaryIntentInboxRequest(item domain.OrdinaryEmailIntent) bool {
	return item.RequestScopeHash != nil && ordinaryInboxTranscriptCommand(item.CommandID)
}

func validInboxTranscriptCommand(command foundation.CommandEnvelope) bool {
	for _, producer := range []struct {
		keyPrefix string
		commandID func(string) string
	}{
		{"account-transcript-v1:", accountInboxTranscriptCommandID},
		{"widget-transcript-v1:", widgetInboxTranscriptCommandID},
	} {
		if strings.HasPrefix(command.IdempotencyKey, producer.keyPrefix) {
			scope := strings.TrimPrefix(command.IdempotencyKey, producer.keyPrefix)
			return ordinaryRequestHash(scope) && command.CommandID == producer.commandID(scope) && command.CorrelationID == command.CommandID
		}
	}
	return false
}

// This runs before public receipt replay, not only when the command is new.
// Wire callers can never supply the private bridge's trusted routing evidence.
func rejectPublicInboxRoute(_ context.Context, command foundation.CommandEnvelope) error {
	invalid := false
	for key := range command.Input {
		k := strings.ToLower(key)
		if k == inboxTranscriptReceiptRoute || k == "inbox_route" || k == "inboxroute" || k == "_inbox_transcript_route" || k == "route" || k == "inbox_id" || k == "transport_key" || strings.HasPrefix(k, "route_") || strings.HasPrefix(k, "smtp_") {
			invalid = true
		}
	}
	if invalid {
		return &ContractFailure{Type: "validation_failed", Message: "inbox routing is unavailable on the public email command"}
	}
	return nil
}

// executeTranscriptAdmission is a local typed entry. Route evidence enters only
// after the existing REST authority checks. The receipt hashes a password-free
// route copy alongside the frozen email, preventing same-key route collisions.
func (r Runtime) executeTranscriptAdmission(ctx context.Context, command foundation.CommandEnvelope, route *domain.InboxSMTPBinding) (any, error) {
	if route == nil {
		if err := rejectPublicInboxRoute(ctx, command); err != nil {
			return nil, err
		}
	} else {
		if domain.ValidateInboxSMTPBinding(*route) != nil || route.AccountID != strconv.FormatUint(uint64(command.AccountID), 10) || !validInboxTranscriptCommand(command) {
			return nil, domain.ErrAccountTranscriptUnavailable
		}
		// The exact typed copy must also be the value included in the receipt digest.
		bound, ok := command.Input[inboxTranscriptReceiptRoute].(domain.InboxSMTPBinding)
		if !ok || bound != *route {
			return nil, domain.ErrAccountTranscriptUnavailable
		}
	}
	return r.Contracts.receipts.ExecutePrepared(ctx, command, r.Contracts.preflights[command.CommandType], func(commandCtx context.Context) (any, error) {
		return r.sendEmailCommand(commandCtx, command, route)
	})
}
func ordinaryIntentInboxBinding(item domain.OrdinaryEmailIntent) domain.InboxSMTPBinding {
	return domain.InboxSMTPBinding{Kind: item.RouteKind, AccountID: item.RouteAccountID, InboxID: item.RouteInboxID, CreatedAt: item.RouteCreatedAt, Fingerprint: item.RouteFingerprint}
}
func ordinaryIntentGlobalRoute(item domain.OrdinaryEmailIntent) bool {
	return item.RouteKind == "" && item.RouteAccountID == "" && item.RouteInboxID == "" && item.RouteCreatedAt == "" && item.RouteFingerprint == "" && !ordinaryIntentInboxRequest(item)
}
