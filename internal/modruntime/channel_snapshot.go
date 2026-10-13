package modruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

// The public model deliberately hides credentials. A private owner snapshot must
// preserve ciphertext (never plaintext) so restore cannot silently disable or
// forget channel bindings. Snapshot files remain mode 0600 and require the same
// independently stored CHANNEL_CREDENTIAL_KEY after restore.
type channelConnectionSnapshot struct {
	domain.ChannelConnection
	SecretCiphertext string `json:"credential_ciphertext"`
}

func marshalSnapshotRows(rows any) ([]byte, error) {
	if rows, ok := rows.([]domain.CannedResponse); ok {
		return marshalCannedSnapshot(rows)
	}
	if v, ok := rows.([]domain.OrdinaryEmailIntent); ok {
		return marshalOrdinaryIntents(v)
	}
	if v, ok := rows.([]domain.OrdinaryEmailAttempt); ok {
		return marshalOrdinaryAttempts(v)
	}
	if logs, ok := rows.([]domain.EmailLog); ok {
		return json.Marshal(confirmationSafeSnapshotLogs(logs))
	}
	if invitations, ok := rows.([]domain.AgentInvitation); ok {
		return marshalAgentInvitationSnapshot(invitations)
	}
	if identities, ok := rows.([]domain.InvitationIdentity); ok {
		return marshalInvitationIdentitySnapshot(identities)
	}
	if requests, ok := rows.([]domain.SAMLAuthnRequest); ok {
		return marshalSAMLRequestSnapshot(requests)
	}
	if replays, ok := rows.([]domain.SAMLReplay); ok {
		return marshalSAMLReplaySnapshot(replays)
	}
	if attempts, ok := rows.([]domain.OutboundMessageAttempt); ok {
		return marshalOutboundSnapshot(attempts)
	}
	if executions, ok := rows.([]domain.AutomationRuleExecution); ok {
		return marshalAutomationSnapshot(executions)
	}
	if connections, ok := rows.([]domain.ChannelConnection); ok {
		result := make([]channelConnectionSnapshot, len(connections))
		for i, c := range connections {
			result[i] = channelConnectionSnapshot{ChannelConnection: c, SecretCiphertext: c.SecretCiphertext}
		}
		return json.Marshal(result)
	}
	return json.Marshal(rows)
}

func unmarshalSnapshotRows(raw []byte, target any) error {
	if rows, ok := target.(*[]domain.CannedResponse); ok {
		return unmarshalCannedSnapshot(raw, rows)
	}
	if v, ok := target.(*[]domain.OrdinaryEmailIntent); ok {
		return unmarshalOrdinaryIntents(raw, v)
	}
	if v, ok := target.(*[]domain.OrdinaryEmailAttempt); ok {
		return unmarshalOrdinaryAttempts(raw, v)
	}
	if quotas, ok := target.(*[]domain.AIUsageQuota); ok {
		return unmarshalAIUsageSnapshot(raw, quotas)
	}
	if logs, ok := target.(*[]domain.EmailLog); ok {
		if err := json.Unmarshal(raw, logs); err != nil {
			return err
		}
		*logs = confirmationSafeSnapshotLogs(*logs)
		return nil
	}
	if invitations, ok := target.(*[]domain.AgentInvitation); ok {
		return unmarshalAgentInvitationSnapshot(raw, invitations)
	}
	if identities, ok := target.(*[]domain.InvitationIdentity); ok {
		return unmarshalInvitationIdentitySnapshot(raw, identities)
	}
	if requests, ok := target.(*[]domain.SAMLAuthnRequest); ok {
		return unmarshalSAMLRequestSnapshot(raw, requests)
	}
	if replays, ok := target.(*[]domain.SAMLReplay); ok {
		return unmarshalSAMLReplaySnapshot(raw, replays)
	}
	if attempts, ok := target.(*[]domain.OutboundMessageAttempt); ok {
		return unmarshalOutboundSnapshot(raw, attempts)
	}
	if executions, ok := target.(*[]domain.AutomationRuleExecution); ok {
		return unmarshalAutomationSnapshot(raw, executions)
	}
	if connections, ok := target.(*[]domain.ChannelConnection); ok {
		var rows []channelConnectionSnapshot
		if err := json.Unmarshal(raw, &rows); err != nil {
			return err
		}
		*connections = make([]domain.ChannelConnection, len(rows))
		for i, row := range rows {
			(*connections)[i] = row.ChannelConnection
			(*connections)[i].SecretCiphertext = row.SecretCiphertext
		}
		return nil
	}
	return json.Unmarshal(raw, target)
}

// The public attempt excludes the body, configuration binding and worker lease.
// Owner backups need all of them: losing a dispatch checkpoint can cause a
// duplicate external effect, and losing the immutable body changes its intent.
// This codec is private to mode-0600 owner snapshots, never an API/event codec.
const outboundSnapshotCodec = "msg.outbound_attempt.v1"

type outboundAttemptPrivateSnapshot struct {
	ConfigFingerprint string     `json:"config_fingerprint"`
	CallbackURL       string     `json:"callback_url"`
	Body              string     `json:"body"`
	ClaimToken        string     `json:"claim_token"`
	ClaimUntil        *time.Time `json:"claim_until"`
	FailureObserved   bool       `json:"failure_observed"`
}

type outboundAttemptSnapshot struct {
	domain.OutboundMessageAttempt
	Private *outboundAttemptPrivateSnapshot `json:"private_dispatch"`
}

type outboundAttemptSnapshotTable struct {
	Codec string                    `json:"codec"`
	Rows  []outboundAttemptSnapshot `json:"rows"`
}

func marshalOutboundSnapshot(attempts []domain.OutboundMessageAttempt) ([]byte, error) {
	table := outboundAttemptSnapshotTable{Codec: outboundSnapshotCodec, Rows: make([]outboundAttemptSnapshot, len(attempts))}
	for i, a := range attempts {
		table.Rows[i] = outboundAttemptSnapshot{OutboundMessageAttempt: a, Private: &outboundAttemptPrivateSnapshot{
			ConfigFingerprint: a.ConfigFingerprint, CallbackURL: a.CallbackURL, Body: a.Body,
			ClaimToken: a.ClaimToken, ClaimUntil: a.ClaimUntil, FailureObserved: a.FailureObserved,
		}}
	}
	return json.Marshal(table)
}

func unmarshalOutboundSnapshot(raw []byte, attempts *[]domain.OutboundMessageAttempt) error {
	var table outboundAttemptSnapshotTable
	if err := decodeStrictSnapshotJSON(raw, &table); err != nil {
		return err
	}
	if table.Codec != outboundSnapshotCodec || table.Rows == nil {
		return errors.New("unsupported outbound snapshot codec")
	}
	var shape struct {
		Rows []map[string]json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal(raw, &shape); err != nil {
		return err
	}
	rows := make([]domain.OutboundMessageAttempt, len(table.Rows))
	for i, row := range table.Rows {
		if row.Private == nil {
			return errors.New("outbound private checkpoint is required")
		}
		if err := requireOutboundSnapshotFields(shape.Rows[i], reflect.TypeOf(domain.OutboundMessageAttempt{})); err != nil {
			return err
		}
		var private map[string]json.RawMessage
		if err := json.Unmarshal(shape.Rows[i]["private_dispatch"], &private); err != nil {
			return err
		}
		if err := requireOutboundSnapshotFields(private, reflect.TypeOf(outboundAttemptPrivateSnapshot{})); err != nil {
			return err
		}
		a, p := row.OutboundMessageAttempt, row.Private
		a.ConfigFingerprint, a.CallbackURL, a.Body = p.ConfigFingerprint, p.CallbackURL, p.Body
		a.ClaimToken, a.ClaimUntil, a.FailureObserved = p.ClaimToken, p.ClaimUntil, p.FailureObserved
		if err := validateOutboundSnapshotAttempt(a); err != nil {
			return err
		}
		rows[i] = a
	}
	*attempts = rows
	return nil
}

func requireOutboundSnapshotFields(fields map[string]json.RawMessage, typ reflect.Type) error {
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		tag := field.Tag.Get("json")
		if tag == "-" || strings.Contains(tag, "omitempty") {
			continue
		}
		value, ok := fields[strings.Split(tag, ",")[0]]
		if !ok || (field.Type.Kind() != reflect.Pointer && bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
			return errors.New("outbound checkpoint field is missing or null")
		}
	}
	return nil
}

func validateOutboundSnapshotAttempt(a domain.OutboundMessageAttempt) error {
	invalid := errors.New("invalid outbound snapshot checkpoint")
	if a.ID == "" || a.AccountID == 0 || a.ConversationID == 0 || a.MessageID == 0 || a.InboxID == 0 || a.ContactID == 0 || a.ConnectionID == 0 || a.Sequence < 1 || a.Provider == "" || a.ProviderAccountID == "" || a.SenderID == "" || a.RecipientID == "" || a.ConfigFingerprint == "" || a.APIVersion == "" || a.CallbackURL == "" || strings.TrimSpace(a.Body) == "" || a.ActorID == 0 || a.SenderType == "" || a.Private || a.ContentType != domain.ContentTypeText || a.MessageType != domain.MessageTypeOutgoing || a.CreatedAt.IsZero() || a.UpdatedAt.IsZero() {
		return invalid
	}
	if (a.ClaimToken == "") != (a.ClaimUntil == nil) {
		return invalid
	}
	switch a.State {
	case domain.OutboundStateQueued:
		if a.Status != domain.MessageStatusQueued || a.DispatchStartedAt != nil || a.ClaimToken != "" || a.ProviderMessageID != "" {
			return invalid
		}
	case domain.OutboundStateClaimed:
		if a.Status != domain.MessageStatusQueued || a.DispatchStartedAt != nil || a.ClaimToken == "" || a.ProviderMessageID != "" {
			return invalid
		}
	case domain.OutboundStateSending:
		if a.Status != domain.MessageStatusQueued || a.DispatchStartedAt == nil || a.ClaimToken == "" || a.ProviderMessageID != "" {
			return invalid
		}
	case domain.OutboundStateUnknown:
		if a.Status != domain.MessageStatusUnknown || a.DispatchStartedAt == nil || a.ClaimToken == "" {
			return invalid
		}
	case domain.OutboundStateSettled:
		if a.DispatchStartedAt == nil || a.ClaimToken == "" {
			return invalid
		}
		switch a.Status {
		case domain.MessageStatusProviderAccepted, domain.MessageStatusSent, domain.MessageStatusDelivered, domain.MessageStatusRead:
			if a.ProviderMessageID == "" {
				return invalid
			}
		case domain.MessageStatusFailed:
		default:
			return invalid
		}
	case domain.OutboundStateCancelled:
		if a.Status != domain.MessageStatusCancelled || a.DispatchStartedAt != nil || a.ClaimToken == "" || a.ProviderMessageID != "" {
			return invalid
		}
	default:
		return invalid
	}
	if a.RetrySafe && (a.State != domain.OutboundStateSettled || a.Status != domain.MessageStatusFailed || a.ProviderMessageID != "" || a.FailureObserved) {
		return invalid
	}
	return nil
}
