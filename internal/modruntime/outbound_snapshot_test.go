package modruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func outboundSnapshotFixture(now time.Time, state string) (domain.Message, domain.OutboundMessageAttempt) {
	a := domain.OutboundMessageAttempt{
		ID: uuid.NewString(), AccountID: 91, ConversationID: 13, MessageID: 14,
		InboxID: 11, ContactID: 12, ConnectionID: 10, Sequence: 1,
		Provider: "twilio", ProviderAccountID: "provider-account", SenderID: "+15550000001", RecipientID: "+15550000002",
		ConfigFingerprint: "immutable-private-config-binding", APIVersion: "v22.0", CallbackURL: "https://example.test/status/private-attempt",
		Body: "Private immutable outbound text", ContentType: domain.ContentTypeText, MessageType: domain.MessageTypeOutgoing,
		SenderType: "User", ActorID: 7, State: domain.OutboundStateQueued, Status: domain.MessageStatusQueued,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute),
	}
	if state != "queued" {
		until := now.Add(time.Hour)
		a.ClaimToken, a.ClaimUntil = uuid.NewString(), &until
	}
	if state != "queued" && state != "claimed" && state != "cancelled" {
		started := now.Add(-2 * time.Minute)
		a.DispatchStartedAt = &started
	}
	switch state {
	case "claimed":
		a.State = domain.OutboundStateClaimed
	case "sending", "expired_sending":
		a.State = domain.OutboundStateSending
		if state == "expired_sending" {
			expired := now.Add(-time.Minute)
			a.ClaimUntil = &expired
		}
	case "sent":
		a.State, a.Status, a.ProviderMessageID = domain.OutboundStateSettled, domain.MessageStatusSent, "SM-provider-message"
	case "unknown":
		a.State, a.Status = domain.OutboundStateUnknown, domain.MessageStatusUnknown
	case "callback_failed":
		a.State, a.Status, a.ProviderMessageID, a.FailureObserved = domain.OutboundStateSettled, domain.MessageStatusFailed, "SM-provider-message", true
	case "rejected":
		a.State, a.Status, a.RetrySafe = domain.OutboundStateSettled, domain.MessageStatusFailed, true
	case "cancelled":
		a.State, a.Status = domain.OutboundStateCancelled, domain.MessageStatusCancelled
	}
	m := domain.Message{ID: a.MessageID, AccountID: a.AccountID, ConversationID: a.ConversationID,
		Content: a.Body, ContentType: a.ContentType, MessageType: a.MessageType, SenderID: a.ActorID, SenderType: a.SenderType,
		Status: a.Status, CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt}
	return m, a
}

func assertOutboundSnapshotEqual(t *testing.T, want, got domain.OutboundMessageAttempt) {
	t.Helper()
	// PostgreSQL timestamptz may decode using the executor's local timezone.
	// Compare exact instants, not semantically equivalent RFC3339 offsets.
	normalize := func(a domain.OutboundMessageAttempt) domain.OutboundMessageAttempt {
		a.CreatedAt, a.UpdatedAt = a.CreatedAt.UTC(), a.UpdatedAt.UTC()
		if a.ClaimUntil != nil {
			v := a.ClaimUntil.UTC()
			a.ClaimUntil = &v
		}
		if a.DispatchStartedAt != nil {
			v := a.DispatchStartedAt.UTC()
			a.DispatchStartedAt = &v
		}
		return a
	}
	want, got = normalize(want), normalize(got)
	left, err := marshalOutboundSnapshot([]domain.OutboundMessageAttempt{want})
	if err != nil {
		t.Fatal(err)
	}
	right, err := marshalOutboundSnapshot([]domain.OutboundMessageAttempt{got})
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("outbound snapshot lost immutable intent or dispatch state\nwant=%s\ngot=%s", left, right)
	}
	public, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"body", "config_fingerprint", "callback_url", "claim_token", "claim_until", "failure_observed", "private_dispatch"} {
		if strings.Contains(string(public), `"`+field+`"`) {
			t.Fatalf("private outbound field leaked in public JSON: %s", field)
		}
	}
	for _, secret := range []string{got.Body, got.ConfigFingerprint, got.CallbackURL, got.ClaimToken} {
		if secret != "" && strings.Contains(string(public), secret) {
			t.Fatal("private outbound value leaked in public JSON")
		}
	}
}

func TestOutboundOwnerSnapshotPreservesIdentityAndDispatchState(t *testing.T) {
	testOutboundOwnerSnapshot(t, false)
}

func TestPostgresOutboundOwnerSnapshotPreservesIdentityAndDispatchState(t *testing.T) {
	testOutboundOwnerSnapshot(t, true)
}

func testOutboundOwnerSnapshot(t *testing.T, postgres bool) {
	for _, state := range []string{"queued", "claimed", "sending", "expired_sending", "sent", "unknown", "rejected", "callback_failed", "cancelled"} {
		t.Run(state, func(t *testing.T) {
			now := time.Now().UTC().Truncate(time.Microsecond)
			open := newModuleTestDB
			if postgres {
				open = postgresModuleDB
			}
			source, target := open(t, "MSG"), open(t, "MSG")
			message, attempt := outboundSnapshotFixture(now, state)
			if err := source.Create(&message).Error; err != nil {
				t.Fatal(err)
			}
			if err := source.Create(&attempt).Error; err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "msg.snapshot.json")
			if err := CreateSnapshot(source, "MSG", "outbound-restore", path); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("unsafe private snapshot permissions: %v %v", info, err)
			}
			if err := RestoreSnapshot(context.Background(), target, "MSG", "outbound-restore", path, nil); err != nil {
				t.Fatal(err)
			}
			for _, foreign := range []any{&domain.Conversation{}, &domain.Contact{}, &domain.Inbox{}, &domain.ChannelConnection{}} {
				if target.Migrator().HasTable(foreign) {
					t.Fatalf("MSG restore crossed owner boundary: %T", foreign)
				}
			}
			repo := repository.NewMessageRepository(target)
			restored, err := repo.GetOutboundAttempt(context.Background(), attempt.AccountID, attempt.ID)
			if err != nil {
				t.Fatal(err)
			}
			assertOutboundSnapshotEqual(t, attempt, *restored)
			claimed, err := repo.ClaimNextOutbound(context.Background(), now, time.Minute)
			if state == "queued" {
				if err != nil || claimed.ID != attempt.ID || claimed.Body != attempt.Body || claimed.State != domain.OutboundStateClaimed || claimed.DispatchStartedAt != nil {
					t.Fatalf("queued intent did not resume unchanged: %+v %v", claimed, err)
				}
				if _, err := repo.StartOutboundDispatch(context.Background(), claimed.AccountID, claimed.ID, claimed.ClaimToken, now); err != nil {
					t.Fatal(err)
				}
				if err := repo.CompleteOutboundDispatch(context.Background(), claimed.AccountID, claimed.ID, claimed.ClaimToken, "SM-restored-send", domain.MessageStatusSent, "accepted"); err != nil {
					t.Fatal(err)
				}
				if next, err := repo.ClaimNextOutbound(context.Background(), now.Add(2*time.Minute), time.Minute); !errors.Is(err, gorm.ErrRecordNotFound) {
					t.Fatalf("restored queued intent dispatched twice: %+v %v", next, err)
				}
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				t.Fatalf("restore reset a durable dispatch or active claim: %+v %v", claimed, err)
			}
			if state == "expired_sending" {
				restored, err = repo.GetOutboundAttempt(context.Background(), attempt.AccountID, attempt.ID)
				if err != nil || restored.State != domain.OutboundStateUnknown || restored.Status != domain.MessageStatusUnknown || restored.DispatchStartedAt == nil || restored.ClaimToken != attempt.ClaimToken || restored.Body != attempt.Body {
					t.Fatalf("expired dispatch was not preserved as unknown: %+v %v", restored, err)
				}
			}
			if state == "callback_failed" {
				if err := repo.CompleteOutboundDispatch(context.Background(), attempt.AccountID, attempt.ID, attempt.ClaimToken, attempt.ProviderMessageID, domain.MessageStatusProviderAccepted, "accepted"); err != nil {
					t.Fatal(err)
				}
				restored, err = repo.GetOutboundAttempt(context.Background(), attempt.AccountID, attempt.ID)
				if err != nil || restored.Status != domain.MessageStatusFailed || !restored.FailureObserved || restored.RetrySafe {
					t.Fatalf("restore lost provider failure evidence: %+v %v", restored, err)
				}
			}
			if state == "unknown" || state == "expired_sending" {
				// A late result must still match the original preserved claim; it
				// settles the old send rather than making another send eligible.
				if err := repo.CompleteOutboundDispatch(context.Background(), attempt.AccountID, attempt.ID, attempt.ClaimToken, "SM-late-response", domain.MessageStatusSent, "accepted"); err != nil {
					t.Fatal(err)
				}
				if next, err := repo.ClaimNextOutbound(context.Background(), now.Add(2*time.Hour), time.Minute); !errors.Is(err, gorm.ErrRecordNotFound) {
					t.Fatalf("late settlement became dispatchable: %+v %v", next, err)
				}
			}
		})
	}
}

func TestOutboundSnapshotRejectsMissingPrivateCheckpointAtomically(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	source, target := newModuleTestDB(t, "MSG"), newModuleTestDB(t, "MSG")
	message, attempt := outboundSnapshotFixture(now, "sent")
	if err := source.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Create(&attempt).Error; err != nil {
		t.Fatal(err)
	}
	marker := domain.Message{ID: 999, AccountID: 91, ConversationID: 999, Content: "target remains intact"}
	if err := target.Create(&marker).Error; err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "msg.snapshot.json")
	if err := CreateSnapshot(source, "MSG", "reject-outbound", path); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(map[string]any){
		"missing_private":        func(row map[string]any) { delete(row, "private_dispatch") },
		"missing_body":           func(row map[string]any) { delete(row["private_dispatch"].(map[string]any), "body") },
		"missing_claim":          func(row map[string]any) { delete(row["private_dispatch"].(map[string]any), "claim_token") },
		"missing_lease":          func(row map[string]any) { delete(row["private_dispatch"].(map[string]any), "claim_until") },
		"missing_binding":        func(row map[string]any) { delete(row["private_dispatch"].(map[string]any), "config_fingerprint") },
		"missing_callback":       func(row map[string]any) { delete(row["private_dispatch"].(map[string]any), "callback_url") },
		"missing_failure_latch":  func(row map[string]any) { delete(row["private_dispatch"].(map[string]any), "failure_observed") },
		"missing_state":          func(row map[string]any) { delete(row, "state") },
		"missing_status":         func(row map[string]any) { delete(row, "status") },
		"missing_dispatch":       func(row map[string]any) { delete(row, "dispatch_started_at") },
		"reset_dispatched_state": func(row map[string]any) { row["state"] = domain.OutboundStateQueued },
		"unknown_field":          func(row map[string]any) { row["unexpected_dispatch_reset"] = true },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			var file snapshotFile
			if err := json.Unmarshal(original, &file); err != nil {
				t.Fatal(err)
			}
			var table map[string]any
			if err := json.Unmarshal(file.Snapshot.Tables["outbound_message_attempts"], &table); err != nil {
				t.Fatal(err)
			}
			mutate(table["rows"].([]any)[0].(map[string]any))
			file.Snapshot.Tables["outbound_message_attempts"], err = json.Marshal(table)
			if err != nil {
				t.Fatal(err)
			}
			canonical, err := json.Marshal(file.Snapshot)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(canonical)
			file.Checksum = hex.EncodeToString(sum[:])
			raw, err := json.Marshal(file)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if err := RestoreSnapshot(context.Background(), target, "MSG", "reject-outbound", path, nil); err == nil {
				t.Fatal("incomplete outbound snapshot was accepted")
			}
			var remaining domain.Message
			if err := target.First(&remaining, marker.ID).Error; err != nil || remaining.Content != marker.Content {
				t.Fatalf("invalid restore was not atomic: %+v %v", remaining, err)
			}
			var count int64
			if err := target.Model(&domain.OutboundMessageAttempt{}).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("invalid restore retained attempt rows: %d %v", count, err)
			}
		})
	}
}

func TestOutboundSnapshotRequiresVersionedPrivateCodec(t *testing.T) {
	_, attempt := outboundSnapshotFixture(time.Now().UTC().Truncate(time.Microsecond), "queued")
	public, err := json.Marshal([]domain.OutboundMessageAttempt{attempt})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{public, []byte(`{"codec":"msg.outbound_attempt.v99","rows":[]}`), []byte(`{"codec":"msg.outbound_attempt.v1","rows":null}`)} {
		preserved := []domain.OutboundMessageAttempt{attempt}
		if err := unmarshalSnapshotRows(raw, &preserved); err == nil {
			t.Fatal("unsupported or public-only snapshot accepted")
		}
		assertOutboundSnapshotEqual(t, attempt, preserved[0])
	}
	raw, err := marshalSnapshotRows([]domain.OutboundMessageAttempt{})
	if err != nil {
		t.Fatal(err)
	}
	var empty []domain.OutboundMessageAttempt
	if err := unmarshalSnapshotRows(raw, &empty); err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty owner checkpoint failed to round-trip: %s %v", raw, err)
	}
}

// A new private field must be explicitly handled by the owner codec rather than
// silently disappearing when public JSON tags change.
func TestOutboundSnapshotCoversEveryPrivateModelField(t *testing.T) {
	model := reflect.TypeOf(domain.OutboundMessageAttempt{})
	private := reflect.TypeOf(outboundAttemptPrivateSnapshot{})
	for i := 0; i < model.NumField(); i++ {
		field := model.Field(i)
		if field.Tag.Get("json") != "-" {
			continue
		}
		codecField, ok := private.FieldByName(field.Name)
		if !ok || codecField.Type != field.Type {
			t.Fatalf("private outbound model field is absent from snapshot codec: %s", field.Name)
		}
	}
}
