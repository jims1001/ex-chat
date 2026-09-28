package modruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
)

func TestModuleSnapshotRestoreRebuildAndReadinessGate(t *testing.T) {
	source := newModuleTestDB(t, "TEN")
	if err := source.Create(&domain.Account{ID: 91, Name: "Recovered tenant"}).Error; err != nil {
		t.Fatal(err)
	}
	event := identityEvent(1)
	event.AccountID = 91
	if _, err := NewEventInbox(source, "TEN").Consume(context.Background(), event, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	snapshotPath := filepath.Join(t.TempDir(), "ten-gen-1.snapshot.json")
	if err := CreateSnapshot(source, "TEN", "gen-1", snapshotPath); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("snapshot permissions=%v", info.Mode().Perm())
	}

	target := newModuleTestDB(t, "TEN")
	cfg := businessConfig("TEN")
	cfg.RecoveryGeneration = "gen-1"
	runtime := Runtime{Config: cfg, DB: target}
	response := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("pre-restore readiness=%d body=%s", response.Code, response.Body.String())
	}
	rebuild := func(ctx context.Context, tx *gorm.DB) error {
		return ReplayConsumedEvents(ctx, tx, "TEN", func(eventCtx context.Context, replay foundation.EventEnvelope) error {
			return Transaction(eventCtx, tx).Create(&domain.Onboarding{AccountID: replay.AccountID, Step: "identity-replayed", Timezone: "UTC"}).Error
		})
	}
	if err := RestoreSnapshot(context.Background(), target, "TEN", "gen-1", snapshotPath, rebuild); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	runtime.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ready", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("post-restore readiness=%d body=%s", response.Code, response.Body.String())
	}
	var account domain.Account
	if err := target.First(&account, 91).Error; err != nil || account.Name != "Recovered tenant" {
		t.Fatalf("restored account=%+v err=%v", account, err)
	}
	var projection domain.Onboarding
	if err := target.Where("account_id = ?", 91).First(&projection).Error; err != nil || projection.Step != "identity-replayed" {
		t.Fatalf("rebuilt projection=%+v err=%v", projection, err)
	}
	var consumed, completed int64
	_ = target.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_consumed").Count(&consumed).Error
	_ = target.Model(&domain.LocalChangeJournal{}).Where("action = ? AND object_display_id = ?", "recovery_complete", "gen-1").Count(&completed).Error
	if consumed != 1 || completed != 1 {
		t.Fatalf("journal consumed=%d recovery_complete=%d", consumed, completed)
	}
	if target.Migrator().HasTable(&domain.User{}) {
		t.Fatal("TEN restore created foreign IAM user table")
	}
}

func TestModuleSnapshotRejectsTamperIdentityAndForeignTablesAtomically(t *testing.T) {
	source := newModuleTestDB(t, "TEN")
	if err := source.Create(&domain.Account{ID: 91, Name: "Source"}).Error; err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := CreateSnapshot(source, "TEN", "gen-1", path); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	assertUnchanged := func(name string, content []byte, module, generation string) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			candidate := filepath.Join(t.TempDir(), "candidate.json")
			if err := os.WriteFile(candidate, content, 0600); err != nil {
				t.Fatal(err)
			}
			target := newModuleTestDB(t, "TEN")
			if err := target.Create(&domain.Account{ID: 7, Name: "Keep"}).Error; err != nil {
				t.Fatal(err)
			}
			if err := RestoreSnapshot(context.Background(), target, module, generation, candidate, nil); err == nil {
				t.Fatal("invalid snapshot was accepted")
			}
			var count int64
			if err := target.Model(&domain.Account{}).Where("id = ? AND name = ?", 7, "Keep").Count(&count).Error; err != nil || count != 1 {
				t.Fatalf("restore changed target count=%d err=%v", count, err)
			}
		})
	}
	tampered := append([]byte(nil), original...)
	for index, value := range tampered {
		if value == 'S' {
			tampered[index] = 'X'
			break
		}
	}
	assertUnchanged("checksum", tampered, "TEN", "gen-1")
	assertUnchanged("module", original, "IAM", "gen-1")
	assertUnchanged("generation", original, "TEN", "gen-2")

	var file snapshotFile
	if err := json.Unmarshal(original, &file); err != nil {
		t.Fatal(err)
	}
	file.Snapshot.Tables["users"] = json.RawMessage(`[]`)
	payload, _ := json.Marshal(file.Snapshot)
	sum := sha256Bytes(payload)
	file.Checksum = sum
	foreign, _ := json.Marshal(file)
	assertUnchanged("foreign-table", foreign, "TEN", "gen-1")
}

func TestCHNSnapshotRestoresInboxTemplateJournalAndReadiness(t *testing.T) {
	source := newModuleTestDB(t, "CHN")
	inbox := domain.Inbox{AccountID: 91, Name: "Recovered channel", ChannelType: "Channel::Whatsapp", WebsiteToken: "channel-token"}
	if err := source.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("CHN")
	runtime := Runtime{Config: cfg, DB: source, Contracts: NewContractRegistry("CHN")}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	command := foundation.CommandEnvelope{CommandID: "snapshot-template", CommandType: "chn.template.manage.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "inbox", ID: strconv.FormatUint(uint64(inbox.ID), 10)}, Input: map[string]any{"action": "upsert", "name": "restore_me", "components": `[{"type":"BODY","text":"Restore"}]`}, IdempotencyKey: "snapshot-template-91", CorrelationID: "snapshot-correlation", CausationID: "snapshot-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "chn.templates.list.v1"}}
	body, _ := json.Marshal(command)
	request := httptest.NewRequest(http.MethodPost, "/contracts/commands/chn.template.manage.v1", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	request.Header.Set(headerCaller, "API")
	request.Header.Set(headerContractVersion, "v1")
	request.Header.Set(headerCorrelationID, command.CorrelationID)
	response := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("template command status=%d body=%s", response.Code, response.Body.String())
	}
	path := filepath.Join(t.TempDir(), "chn.snapshot.json")
	if err := CreateSnapshot(source, "CHN", "chn-gen-1", path); err != nil {
		t.Fatal(err)
	}
	target := newModuleTestDB(t, "CHN")
	targetCfg := businessConfig("CHN")
	targetCfg.RecoveryGeneration = "chn-gen-1"
	targetRuntime := Runtime{Config: targetCfg, DB: target}
	if err := targetRuntime.Readiness(); err == nil {
		t.Fatal("CHN target was ready before restore")
	}
	if err := RestoreSnapshot(context.Background(), target, "CHN", "chn-gen-1", path, nil); err != nil {
		t.Fatal(err)
	}
	if err := targetRuntime.Readiness(); err != nil {
		t.Fatalf("CHN target not ready after restore: %v", err)
	}
	var inboxes, templates, receipts, events int64
	_ = target.Model(&domain.Inbox{}).Count(&inboxes).Error
	_ = target.Model(&domain.InboxMessageTemplate{}).Count(&templates).Error
	_ = target.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = target.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_outbox").Count(&events).Error
	if inboxes != 1 || templates != 1 || receipts != 1 || events != 1 {
		t.Fatalf("restored inboxes=%d templates=%d receipts=%d events=%d", inboxes, templates, receipts, events)
	}
	for _, foreign := range []any{&domain.Account{}, &domain.User{}, &domain.Conversation{}, &domain.Message{}} {
		if target.Migrator().HasTable(foreign) {
			t.Fatalf("CHN restore created foreign table %T", foreign)
		}
	}
}

func TestCUSSnapshotRestoresOwnerFactsJournalAndReadiness(t *testing.T) {
	source := newModuleTestDB(t, "CUS")
	cfg := businessConfig("CUS")
	runtime := Runtime{Config: cfg, DB: source, Contracts: NewContractRegistry("CUS")}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	command := foundation.CommandEnvelope{CommandID: "snapshot-contact", CommandType: "cus.contact.create.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "91"}, Input: map[string]any{"name": "Recovered contact", "identifier": "restore-contact-91"}, IdempotencyKey: "snapshot-contact-91", CorrelationID: "snapshot-contact-correlation", CausationID: "snapshot-contact-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "cus.contact.get.v1"}}
	body, _ := json.Marshal(command)
	request := httptest.NewRequest(http.MethodPost, "/contracts/commands/cus.contact.create.v1", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	request.Header.Set(headerCaller, "API")
	request.Header.Set(headerContractVersion, "v1")
	request.Header.Set(headerCorrelationID, command.CorrelationID)
	response := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("contact command status=%d body=%s", response.Code, response.Body.String())
	}
	path := filepath.Join(t.TempDir(), "cus.snapshot.json")
	if err := CreateSnapshot(source, "CUS", "cus-gen-1", path); err != nil {
		t.Fatal(err)
	}
	target := newModuleTestDB(t, "CUS")
	targetCfg := businessConfig("CUS")
	targetCfg.RecoveryGeneration = "cus-gen-1"
	targetRuntime := Runtime{Config: targetCfg, DB: target}
	if err := targetRuntime.Readiness(); err == nil {
		t.Fatal("CUS target was ready before restore")
	}
	if err := RestoreSnapshot(context.Background(), target, "CUS", "cus-gen-1", path, nil); err != nil {
		t.Fatal(err)
	}
	if err := targetRuntime.Readiness(); err != nil {
		t.Fatalf("CUS target not ready after restore: %v", err)
	}
	var contacts, receipts, events int64
	_ = target.Model(&domain.Contact{}).Where("account_id = ? AND identifier = ?", 91, "restore-contact-91").Count(&contacts).Error
	_ = target.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = target.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_outbox").Count(&events).Error
	if contacts != 1 || receipts != 1 || events != 1 {
		t.Fatalf("restored contacts=%d receipts=%d events=%d", contacts, receipts, events)
	}
	for _, foreign := range []any{&domain.Account{}, &domain.Inbox{}, &domain.Conversation{}, &domain.Message{}, &domain.Ticket{}, &domain.Order{}} {
		if target.Migrator().HasTable(foreign) {
			t.Fatalf("CUS restore created foreign table %T", foreign)
		}
	}
}

func sha256Bytes(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
