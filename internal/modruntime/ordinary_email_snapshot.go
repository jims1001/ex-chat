package modruntime

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Private owner codec. A snapshot is not proof of the provider's current state:
// restored pending/active records are quarantined, never automatically activated.
type ordinaryTranscriptRequestPrivate struct {
	ScopeHash string `json:"scope_hash"`
	InputHash string `json:"input_hash"`
	Messages  int    `json:"messages"`
}
type ordinaryIntentPrivate struct {
	Request       *ordinaryTranscriptRequestPrivate `json:"request"`
	Payload       string                            `json:"payload"`
	PayloadHash   string                            `json:"payload_hash"`
	TransportKey  string                            `json:"transport_key"`
	CommandID     string                            `json:"command_id"`
	CorrelationID string                            `json:"correlation_id"`
	LeaseToken    string                            `json:"lease_token"`
	LeaseUntil    *time.Time                        `json:"lease_until"`
}
type ordinaryIntentSnapshot struct {
	domain.OrdinaryEmailIntent
	Private *ordinaryIntentPrivate `json:"private"`
}
type ordinaryIntentTable struct {
	Codec string                   `json:"codec"`
	Rows  []ordinaryIntentSnapshot `json:"rows"`
}

// Keep the v1-v3 wire shape unchanged. Version 4 alone requires an explicit
// route on every row, including null for global records and scrubbed tombstones.
type ordinaryInboxRoutePrivate struct {
	Kind        string `json:"kind"`
	AccountID   string `json:"account_id"`
	InboxID     string `json:"inbox_id"`
	CreatedAt   string `json:"created_at"`
	Fingerprint string `json:"fingerprint"`
}
type ordinaryIntentPrivateV4 struct {
	ordinaryIntentPrivate
	Route *ordinaryInboxRoutePrivate `json:"route"`
}
type ordinaryIntentSnapshotV4 struct {
	domain.OrdinaryEmailIntent
	Private *ordinaryIntentPrivateV4 `json:"private"`
}
type ordinaryIntentTableV4 struct {
	Codec string                     `json:"codec"`
	Rows  []ordinaryIntentSnapshotV4 `json:"rows"`
}

type ordinaryAttemptPrivate struct {
	LeaseToken string    `json:"lease_token"`
	LeaseUntil time.Time `json:"lease_until"`
}
type ordinaryAttemptSnapshot struct {
	domain.OrdinaryEmailAttempt
	Private *ordinaryAttemptPrivate `json:"private"`
}
type ordinaryAttemptTable struct {
	Codec string                    `json:"codec"`
	Rows  []ordinaryAttemptSnapshot `json:"rows"`
}

func marshalOrdinaryIntents(rows []domain.OrdinaryEmailIntent) ([]byte, error) {
	table := ordinaryIntentTable{Codec: "msg.ordinary_email_intent.v2", Rows: make([]ordinaryIntentSnapshot, len(rows))}
	inbox := false
	for i, r := range rows {
		if strings.HasPrefix(r.CommandID, "wdg-transcript-v1:") {
			table.Codec = "msg.ordinary_email_intent.v3"
		}
		// Real Inbox tombstones retain their scoped request evidence. Arbitrary
		// unscoped global command IDs never acquire Inbox identity by prefix.
		inbox = inbox || ordinarySnapshotHasRoute(r) || ordinaryIntentInboxRequest(r)
		private := &ordinaryIntentPrivate{Payload: r.Payload, PayloadHash: r.PayloadHash, TransportKey: r.TransportKey, CommandID: r.CommandID, CorrelationID: r.CorrelationID, LeaseToken: r.LeaseToken, LeaseUntil: r.LeaseUntil}
		if r.RequestScopeHash != nil {
			private.Request = &ordinaryTranscriptRequestPrivate{ScopeHash: *r.RequestScopeHash, InputHash: r.RequestInputHash, Messages: r.TranscriptMessages}
		}
		table.Rows[i] = ordinaryIntentSnapshot{r, private}
	}
	if !inbox {
		return json.Marshal(table)
	}
	v4 := ordinaryIntentTableV4{Codec: "msg.ordinary_email_intent.v4", Rows: make([]ordinaryIntentSnapshotV4, len(rows))}
	for i, row := range table.Rows {
		private := &ordinaryIntentPrivateV4{ordinaryIntentPrivate: *row.Private}
		if ordinarySnapshotHasRoute(row.OrdinaryEmailIntent) {
			private.Route = &ordinaryInboxRoutePrivate{Kind: row.RouteKind, AccountID: row.RouteAccountID, InboxID: row.RouteInboxID, CreatedAt: row.RouteCreatedAt, Fingerprint: row.RouteFingerprint}
		}
		v4.Rows[i] = ordinaryIntentSnapshotV4{row.OrdinaryEmailIntent, private}
	}
	return json.Marshal(v4)
}
func marshalOrdinaryAttempts(rows []domain.OrdinaryEmailAttempt) ([]byte, error) {
	table := ordinaryAttemptTable{Codec: "msg.ordinary_email_attempt.v1", Rows: make([]ordinaryAttemptSnapshot, len(rows))}
	for i, r := range rows {
		table.Rows[i] = ordinaryAttemptSnapshot{r, &ordinaryAttemptPrivate{r.LeaseToken, r.LeaseUntil}}
	}
	return json.Marshal(table)
}
func unmarshalOrdinaryIntents(raw []byte, target *[]domain.OrdinaryEmailIntent) error {
	invalid := errors.New("invalid ordinary email intent checkpoint")
	var header struct {
		Codec string          `json:"codec"`
		Rows  json.RawMessage `json:"rows"`
	}
	if decodeStrictSnapshotJSON(raw, &header) != nil {
		return invalid
	}
	var table ordinaryIntentTable
	var v4 ordinaryIntentTableV4
	if header.Codec == "msg.ordinary_email_intent.v4" || ordinarySnapshotDeclaresV4(raw) {
		if validateOrdinarySnapshotV4Fields(raw) != nil || decodeStrictSnapshotJSON(raw, &v4) != nil || v4.Rows == nil {
			return invalid
		}
		table.Codec = v4.Codec
		table.Rows = make([]ordinaryIntentSnapshot, len(v4.Rows))
		for i, row := range v4.Rows {
			if row.Private == nil {
				return invalid
			}
			table.Rows[i] = ordinaryIntentSnapshot{row.OrdinaryEmailIntent, &row.Private.ordinaryIntentPrivate}
		}
	} else if decodeStrictSnapshotJSON(raw, &table) != nil || (table.Codec != "msg.ordinary_email_intent.v1" && table.Codec != "msg.ordinary_email_intent.v2" && table.Codec != "msg.ordinary_email_intent.v3") || table.Rows == nil {
		return invalid
	}
	var shape struct {
		Rows []map[string]json.RawMessage `json:"rows"`
	}
	if json.Unmarshal(raw, &shape) != nil {
		return invalid
	}
	rows := make([]domain.OrdinaryEmailIntent, len(table.Rows))
	for i, row := range table.Rows {
		if row.Private == nil || requireOutboundSnapshotFields(shape.Rows[i], reflect.TypeOf(domain.OrdinaryEmailIntent{})) != nil {
			return invalid
		}
		var private map[string]json.RawMessage
		if json.Unmarshal(shape.Rows[i]["private"], &private) != nil {
			return invalid
		}
		// Phase38 v1 has every original private field, but no REST request identity.
		// Only that exact absence is backward compatible; never infer an owner/key.
		if table.Codec == "msg.ordinary_email_intent.v1" {
			if _, exists := private["request"]; exists {
				return invalid
			}
			private["request"] = json.RawMessage("null")
		}
		if requireOutboundSnapshotFields(private, reflect.TypeOf(ordinaryIntentPrivate{})) != nil {
			return invalid
		}
		r, p := row.OrdinaryEmailIntent, row.Private
		r.Payload = p.Payload
		r.PayloadHash = p.PayloadHash
		r.TransportKey = p.TransportKey
		r.CommandID = p.CommandID
		r.CorrelationID = p.CorrelationID
		r.LeaseToken = p.LeaseToken
		r.LeaseUntil = p.LeaseUntil
		if p.Request != nil {
			var requestShape map[string]json.RawMessage
			if json.Unmarshal(private["request"], &requestShape) != nil || requireOutboundSnapshotFields(requestShape, reflect.TypeOf(ordinaryTranscriptRequestPrivate{})) != nil {
				return invalid
			}
			scope := p.Request.ScopeHash
			r.RequestScopeHash = &scope
			r.RequestInputHash = p.Request.InputHash
			r.TranscriptMessages = p.Request.Messages
		}
		if table.Codec == "msg.ordinary_email_intent.v4" {
			routeRaw, exists := private["route"]
			if !exists || len(private) != reflect.TypeOf(ordinaryIntentPrivate{}).NumField()+1 {
				return invalid
			}
			if route := v4.Rows[i].Private.Route; route != nil {
				var routeShape map[string]json.RawMessage
				if json.Unmarshal(routeRaw, &routeShape) != nil || len(routeShape) != reflect.TypeOf(ordinaryInboxRoutePrivate{}).NumField() || requireOutboundSnapshotFields(routeShape, reflect.TypeOf(ordinaryInboxRoutePrivate{})) != nil {
					return invalid
				}
				r.RouteKind, r.RouteAccountID, r.RouteInboxID = route.Kind, route.AccountID, route.InboxID
				r.RouteCreatedAt, r.RouteFingerprint = route.CreatedAt, route.Fingerprint
				if !validOrdinaryInboxRoute(r) {
					return invalid
				}
			} else if !bytes.Equal(bytes.TrimSpace(routeRaw), []byte("null")) {
				return invalid
			}
		} else if ordinaryIntentInboxRequest(r) {
			// A retained Inbox request cannot lose its required v4 route codec.
			return invalid
		}
		if (table.Codec != "msg.ordinary_email_intent.v3" && table.Codec != "msg.ordinary_email_intent.v4" && strings.HasPrefix(r.CommandID, "wdg-transcript-v1:")) || !validOrdinaryIntent(r) {
			return invalid
		}
		rows[i] = r
	}
	*target = rows
	return nil
}

// An alternate-cased or repeated codec can otherwise hide an earlier v4
// declaration from Go's last-value-wins header decoder. Inspect all declarations
// before choosing a codec, without changing historical v1-v3 decoding.
func ordinarySnapshotDeclaresV4(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return false
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return false
		}
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return false
		}
		name, ok := key.(string)
		if ok && strings.EqualFold(name, "codec") {
			var codec string
			if json.Unmarshal(value, &codec) == nil && codec == "msg.ordinary_email_intent.v4" {
				return true
			}
		}
	}
	return false
}

// Go matches struct fields case-insensitively, including Unicode case aliases.
// Require the codec's canonical lowercase ASCII keys and reject duplicates at
// every object depth. Payload strings remain opaque and are never inspected.
func validateOrdinarySnapshotV4Fields(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var read func() error
	read = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delim, container := token.(json.Delim)
		if !container {
			return nil
		}
		seen := map[string]bool{}
		for decoder.More() {
			if delim == '{' {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] || !ordinarySnapshotCanonicalField(name) {
					return errors.New("noncanonical ordinary email checkpoint field")
				}
				seen[name] = true
			}
			if err := read(); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	}
	return read()
}

func ordinarySnapshotCanonicalField(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range []byte(name) {
		if c != '_' && (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

func ordinarySnapshotHasRoute(r domain.OrdinaryEmailIntent) bool {
	return r.RouteKind != "" || r.RouteAccountID != "" || r.RouteInboxID != "" || r.RouteCreatedAt != "" || r.RouteFingerprint != ""
}

func validOrdinaryInboxRoute(r domain.OrdinaryEmailIntent) bool {
	binding := domain.InboxSMTPBinding{Kind: r.RouteKind, AccountID: r.RouteAccountID, InboxID: r.RouteInboxID, CreatedAt: r.RouteCreatedAt, Fingerprint: r.RouteFingerprint}
	return domain.ValidateInboxSMTPBinding(binding) == nil && r.RouteAccountID == strconv.FormatUint(uint64(r.AccountID), 10)
}

func validOrdinaryIntent(r domain.OrdinaryEmailIntent) bool {
	if !validOrdinaryErrorCode(r.ErrorCode) || r.ID == 0 || r.AccountID == 0 || r.EmailLogID == 0 || !ordinaryEmailType(r.EmailType) || r.MaxAttempts != 4 || r.AttemptCount < 0 || r.AttemptCount > r.MaxAttempts || r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() || r.CommandID == "" || r.CorrelationID == "" {
		return false
	}
	if r.RequestScopeHash == nil {
		if r.RequestInputHash != "" || r.TranscriptMessages != 0 || strings.HasPrefix(r.CommandID, "acct-transcript-v1:") || strings.HasPrefix(r.CommandID, "wdg-transcript-v1:") {
			return false
		}
	} else {
		if !ordinaryRequestHash(*r.RequestScopeHash) || r.EmailType != "transcript" || (!ordinaryAccountTranscriptIdentity(r.CommandID, *r.RequestScopeHash) && !ordinaryWidgetTranscriptIdentity(r.CommandID, *r.RequestScopeHash)) {
			return false
		}
		if (ordinaryWidgetTranscriptIdentity(r.CommandID, *r.RequestScopeHash) || ordinaryInboxTranscriptCommand(r.CommandID)) && r.CorrelationID != r.CommandID {
			return false
		}
		if r.Anonymized {
			if r.RequestInputHash != "" || r.TranscriptMessages != 0 {
				return false
			}
		} else if !ordinaryRequestHash(r.RequestInputHash) || r.TranscriptMessages < 0 || r.TranscriptMessages > 200 || (ordinaryWidgetTranscriptIdentity(r.CommandID, *r.RequestScopeHash) && r.TranscriptMessages > 100) {
			return false
		}
	}
	if (r.AttemptCount == 0) != (r.LatestAttemptID == "") {
		return false
	}
	if r.Anonymized {
		if r.Payload != "" || r.PayloadHash != "" || r.TransportKey != "" || ordinarySnapshotHasRoute(r) {
			return false
		}
	} else {
		if r.Payload == "" || ordinaryPayloadHash(r.Payload) != r.PayloadHash {
			return false
		}
		if ordinaryIntentInboxRequest(r) {
			if r.TransportKey != "" || !validOrdinaryInboxRoute(r) {
				return false
			}
		} else if ordinarySnapshotHasRoute(r) || len(r.TransportKey) != 64 {
			return false
		}
	}
	active := r.State == "leased" || r.State == "submitting"
	if active {
		if r.Anonymized || r.LeaseToken == "" || r.LeaseUntil == nil || r.AttemptCount == 0 || r.NextAttemptAt != nil {
			return false
		}
	} else if r.LeaseToken != "" || r.LeaseUntil != nil {
		return false
	}
	switch r.State {
	case "pending":
		return !r.Anonymized && r.AttemptCount < r.MaxAttempts
	case "leased", "submitting", "sent", "failed", "unknown", "cancelled":
		return r.State == "pending" || r.NextAttemptAt == nil
	default:
		return false
	}
}
func unmarshalOrdinaryAttempts(raw []byte, target *[]domain.OrdinaryEmailAttempt) error {
	invalid := errors.New("invalid ordinary email attempt checkpoint")
	var table ordinaryAttemptTable
	if decodeStrictSnapshotJSON(raw, &table) != nil || table.Codec != "msg.ordinary_email_attempt.v1" || table.Rows == nil {
		return invalid
	}
	var shape struct {
		Rows []map[string]json.RawMessage `json:"rows"`
	}
	if json.Unmarshal(raw, &shape) != nil {
		return invalid
	}
	rows := make([]domain.OrdinaryEmailAttempt, len(table.Rows))
	for i, row := range table.Rows {
		if row.Private == nil || requireOutboundSnapshotFields(shape.Rows[i], reflect.TypeOf(domain.OrdinaryEmailAttempt{})) != nil {
			return invalid
		}
		var private map[string]json.RawMessage
		if json.Unmarshal(shape.Rows[i]["private"], &private) != nil || requireOutboundSnapshotFields(private, reflect.TypeOf(ordinaryAttemptPrivate{})) != nil {
			return invalid
		}
		r := row.OrdinaryEmailAttempt
		r.LeaseToken = row.Private.LeaseToken
		r.LeaseUntil = row.Private.LeaseUntil
		if !validOrdinaryErrorCode(r.ErrorCode) || r.ID == "" || r.IntentID == 0 || r.AccountID == 0 || r.Sequence < 1 || r.Sequence > 4 || r.LeaseToken == "" || r.LeaseUntil.IsZero() || r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() {
			return invalid
		}
		switch r.State {
		case "leased":
			if r.SubmittingAt != nil || r.CompletedAt != nil {
				return invalid
			}
		case "submitting":
			if r.SubmittingAt == nil || r.CompletedAt != nil {
				return invalid
			}
		case "sent":
			if r.SubmittingAt == nil || r.CompletedAt == nil {
				return invalid
			}
		case "pre_submission_failed", "abandoned", "cancelled":
			if r.SubmittingAt != nil || r.CompletedAt == nil {
				return invalid
			}
		case "failed", "unknown":
			if r.CompletedAt == nil {
				return invalid
			}
		default:
			return invalid
		}
		rows[i] = r
	}
	*target = rows
	return nil
}

func restoreOrdinaryEmailSafety(tx *gorm.DB) (err error) {
	tx = ordinaryEmailPrivateDB(tx)
	defer func() {
		if err != nil {
			err = errors.New("ordinary email restore checkpoint is invalid or unavailable")
		}
	}()
	var items []domain.OrdinaryEmailIntent
	if err := tx.Order("id ASC").Find(&items).Error; err != nil {
		return err
	}
	var logs []domain.EmailLog
	if err := tx.Where("managed_queue <> ?", "").Find(&logs).Error; err != nil {
		return err
	}
	if len(items) != len(logs) {
		return errors.New("ordinary email checkpoint/log mismatch")
	}
	r := Runtime{DB: tx, Config: Config{ModuleID: "MSG", ContractVersion: "v1"}}
	// GORM's EmailLog default must not re-enable pre-upgrade legacy retries.
	if err := tx.Model(&domain.EmailLog{}).Where("managed_queue = ?", ordinaryEmailQueue).UpdateColumn("max_retries", 0).Error; err != nil {
		return err
	}
	for _, item := range items {
		var log domain.EmailLog
		if err := tx.Where("id = ? AND account_id = ? AND managed_queue = ? AND email_type = ?", item.EmailLogID, item.AccountID, ordinaryEmailQueue, item.EmailType).First(&log).Error; err != nil {
			return errors.New("ordinary email checkpoint/log mismatch")
		}
		if item.RequestScopeHash != nil && !item.Anonymized {
			widget := ordinaryWidgetTranscriptIdentity(item.CommandID, *item.RequestScopeHash)
			if log.ConversationID == nil || (widget && log.UserID != nil) || (!widget && log.UserID == nil) {
				return errors.New("ordinary transcript request ownership mismatch")
			}
		}
		if log.Status != item.State || log.DeliveryStatus != item.State || log.ContentText != "" || log.ContentHTML != "" {
			return errors.New("ordinary email checkpoint status mismatch")
		}
		var attempts []domain.OrdinaryEmailAttempt
		if err := tx.Where("intent_id = ?", item.ID).Order("sequence ASC").Find(&attempts).Error; err != nil {
			return err
		}
		if len(attempts) != item.AttemptCount {
			return errors.New("ordinary email checkpoint attempt mismatch")
		}
		for i, a := range attempts {
			if a.AccountID != item.AccountID || a.Sequence != i+1 || (i == len(attempts)-1 && a.ID != item.LatestAttemptID) {
				return errors.New("ordinary email checkpoint attempt mismatch")
			}
		}
		if !validateOrdinaryAttemptChain(item, attempts) {
			return errors.New("ordinary email checkpoint attempt evidence mismatch")
		}
		if item.State == "pending" || item.State == "leased" || item.State == "submitting" {
			now := time.Now().UTC()
			// A pending retry has a completed previous attempt; quarantine its intent
			// without rewriting that proof. An active attempt becomes unknown too.
			if item.State == "pending" {
				if err := tx.Model(&item).Updates(map[string]any{"state": "unknown", "next_attempt_at": nil, "error_code": "restored_checkpoint_unverified", "updated_at": now}).Error; err != nil {
					return err
				}
				item.State = "unknown"
				item.NextAttemptAt = nil
				item.ErrorCode = "restored_checkpoint_unverified"
				if err := r.mirrorOrdinaryEmail(tx, item, now); err != nil {
					return err
				}
			} else if err := r.finishOrdinaryTx(tx, &item, "unknown", "restored_checkpoint_unverified", false, now); err != nil {
				return err
			}
		}
	}
	var orphan int64
	if err := tx.Model(&domain.OrdinaryEmailAttempt{}).Where("NOT EXISTS (SELECT 1 FROM ordinary_email_intents i WHERE i.id = ordinary_email_attempts.intent_id AND i.account_id = ordinary_email_attempts.account_id)").Count(&orphan).Error; err != nil {
		return err
	}
	if orphan != 0 {
		return errors.New("orphan ordinary email attempt")
	}
	return nil
}

func validOrdinaryErrorCode(code string) bool {
	switch code {
	case "", "pre_submission_transient", "pre_submission_cancelled", "smtp_authentication_failed", "smtp_permanent_failure", "submission_outcome_unknown", "worker_lost_after_submission", "attempts_exhausted", "invalid_intent_or_transport", "lease_expired_before_submission", "account_anonymized_before_submission", "restored_checkpoint_unverified":
		return true
	default:
		return false
	}
}
func validateOrdinaryAttemptChain(item domain.OrdinaryEmailIntent, attempts []domain.OrdinaryEmailAttempt) bool {
	if len(attempts) != item.AttemptCount {
		return false
	}
	if len(attempts) == 0 {
		return item.State == "pending" || item.State == "cancelled" || (item.State == "unknown" && item.ErrorCode == "restored_checkpoint_unverified")
	}
	for i, a := range attempts {
		if a.AccountID != item.AccountID || a.IntentID != item.ID || a.Sequence != i+1 {
			return false
		}
		if i < len(attempts)-1 && (a.CompletedAt == nil || (a.State != "pre_submission_failed" && a.State != "abandoned")) {
			return false
		}
	}
	last := attempts[len(attempts)-1]
	if last.ID != item.LatestAttemptID {
		return false
	}
	switch item.State {
	case "leased", "submitting":
		return last.State == item.State && last.LeaseToken == item.LeaseToken && item.LeaseUntil != nil && last.LeaseUntil.Equal(*item.LeaseUntil)
	case "pending":
		return last.State == "pre_submission_failed" && last.CompletedAt != nil
	case "sent":
		return last.State == "sent" && last.SubmittingAt != nil && last.CompletedAt != nil
	case "failed":
		return last.State == "failed" && last.CompletedAt != nil
	case "cancelled":
		return last.CompletedAt != nil && (last.State == "cancelled" || last.State == "pre_submission_failed")
	case "unknown":
		return last.CompletedAt != nil && (last.State == "unknown" || (item.ErrorCode == "restored_checkpoint_unverified" && last.State == "pre_submission_failed"))
	}
	return false
}

func restoreOrdinaryEmailRows(tx *gorm.DB, rows any) (bool, error) {
	count := 0
	switch v := rows.(type) {
	case *[]domain.OrdinaryEmailIntent:
		count = len(*v)
	case *[]domain.OrdinaryEmailAttempt:
		count = len(*v)
	default:
		return false, nil
	}
	if count == 0 {
		return true, nil
	}
	if err := ordinaryEmailPrivateDB(tx).Create(rows).Error; err != nil {
		return true, errOrdinaryEmailStore
	}
	return true, nil
}

func ordinaryRequestHash(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && strings.ToLower(value) == value
}
