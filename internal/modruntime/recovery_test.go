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

func TestMEDSnapshotRestoresOwnerFactsJournalAndReadiness(t *testing.T) {
	source := newModuleTestDB(t, "MED")
	cfg := businessConfig("MED")
	runtime := Runtime{Config: cfg, DB: source, Contracts: NewContractRegistry("MED")}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	command := foundation.CommandEnvelope{CommandID: "snapshot-media", CommandType: "med.upload.create.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "91"}, Input: map[string]any{"file_name": "restore.pdf", "content_type": "application/pdf", "file_size": 1024}, IdempotencyKey: "snapshot-media-91", CorrelationID: "snapshot-media-correlation", CausationID: "snapshot-media-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "med.asset.get.v1"}}
	body, _ := json.Marshal(command)
	request := httptest.NewRequest(http.MethodPost, "/contracts/commands/med.upload.create.v1", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	request.Header.Set(headerCaller, "API")
	request.Header.Set(headerContractVersion, "v1")
	request.Header.Set(headerCorrelationID, command.CorrelationID)
	response := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("media command status=%d body=%s", response.Code, response.Body.String())
	}
	var asset domain.MediaAsset
	if err := source.Where("account_id = ?", 91).First(&asset).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := source.Create(&domain.SecurityScan{AccountID: 91, AssetID: asset.ID, Status: "clean", ScannedAt: now, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Create(&domain.AccessGrant{AccountID: 91, AssetID: asset.ID, SubjectType: "user", SubjectID: "7", Scope: "read", CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "med.snapshot.json")
	if err := CreateSnapshot(source, "MED", "med-gen-1", path); err != nil {
		t.Fatal(err)
	}
	target := newModuleTestDB(t, "MED")
	targetCfg := businessConfig("MED")
	targetCfg.RecoveryGeneration = "med-gen-1"
	targetRuntime := Runtime{Config: targetCfg, DB: target}
	if err := targetRuntime.Readiness(); err == nil {
		t.Fatal("MED target was ready before restore")
	}
	if err := RestoreSnapshot(context.Background(), target, "MED", "med-gen-1", path, nil); err != nil {
		t.Fatal(err)
	}
	if err := targetRuntime.Readiness(); err != nil {
		t.Fatalf("MED target not ready after restore: %v", err)
	}
	counts := map[string]int64{}
	for name, model := range map[string]any{"assets": &domain.MediaAsset{}, "uploads": &domain.UploadSession{}, "scans": &domain.SecurityScan{}, "grants": &domain.AccessGrant{}} {
		var count int64
		_ = target.Model(model).Where("account_id = ?", 91).Count(&count).Error
		counts[name] = count
	}
	var receipts int64
	_ = target.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	if counts["assets"] != 1 || counts["uploads"] != 1 || counts["scans"] != 1 || counts["grants"] != 1 || receipts != 1 {
		t.Fatalf("restored counts=%v receipts=%d", counts, receipts)
	}
	for _, foreign := range []any{&domain.Account{}, &domain.Attachment{}, &domain.Conversation{}, &domain.Message{}} {
		if target.Migrator().HasTable(foreign) {
			t.Fatalf("MED restore created foreign table %T", foreign)
		}
	}
}

func TestCONSnapshotRestoresOwnerFactsJournalAndReadiness(t *testing.T) {
	source := newModuleTestDB(t, "CON")
	conversation := domain.Conversation{AccountID: 91, InboxID: 11, ContactID: 21, DisplayID: 1, Status: domain.ConversationStatusOpen, Priority: domain.PriorityMedium, ObjectVersion: 1, LastActivityAt: time.Now().UTC()}
	if err := source.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Create(&domain.ConversationParticipant{ConversationID: conversation.ID, UserID: 7}).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Create(&domain.ConversationLabel{ConversationID: conversation.ID, LabelID: 8}).Error; err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("CON")
	runtime := Runtime{Config: cfg, DB: source, Contracts: NewContractRegistry("CON")}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	command := foundation.CommandEnvelope{CommandID: "snapshot-conversation", CommandType: "con.priority.change.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "conversation", ID: strconv.FormatUint(uint64(conversation.ID), 10)}, Input: map[string]any{"priority": "high"}, IdempotencyKey: "snapshot-conversation-91", CorrelationID: "snapshot-conversation-correlation", CausationID: "snapshot-conversation-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "con.conversation.get.v1"}}
	body, _ := json.Marshal(command)
	request := httptest.NewRequest(http.MethodPost, "/contracts/commands/con.priority.change.v1", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	request.Header.Set(headerCaller, "API")
	request.Header.Set(headerContractVersion, "v1")
	request.Header.Set(headerCorrelationID, command.CorrelationID)
	response := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("conversation command=%d body=%s", response.Code, response.Body.String())
	}
	path := filepath.Join(t.TempDir(), "con.snapshot.json")
	if err := CreateSnapshot(source, "CON", "con-gen-1", path); err != nil {
		t.Fatal(err)
	}
	target := newModuleTestDB(t, "CON")
	targetCfg := businessConfig("CON")
	targetCfg.RecoveryGeneration = "con-gen-1"
	targetRuntime := Runtime{Config: targetCfg, DB: target}
	if err := targetRuntime.Readiness(); err == nil {
		t.Fatal("CON target was ready before restore")
	}
	if err := RestoreSnapshot(context.Background(), target, "CON", "con-gen-1", path, nil); err != nil {
		t.Fatal(err)
	}
	if err := targetRuntime.Readiness(); err != nil {
		t.Fatalf("CON target not ready after restore: %v", err)
	}
	counts := map[string]int64{}
	for name, model := range map[string]any{"conversations": &domain.Conversation{}, "participants": &domain.ConversationParticipant{}, "labels": &domain.ConversationLabel{}} {
		var count int64
		_ = target.Model(model).Count(&count).Error
		counts[name] = count
	}
	var receipts, events int64
	_ = target.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = target.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_outbox").Count(&events).Error
	if counts["conversations"] != 1 || counts["participants"] != 1 || counts["labels"] != 1 || receipts != 1 || events != 1 {
		t.Fatalf("counts=%v receipts=%d events=%d", counts, receipts, events)
	}
	for _, foreign := range []any{&domain.Account{}, &domain.Inbox{}, &domain.Contact{}, &domain.User{}, &domain.Message{}} {
		if target.Migrator().HasTable(foreign) {
			t.Fatalf("CON restore created foreign table %T", foreign)
		}
	}
}

func TestMSGSnapshotRestoresOwnerFactsJournalAndReadiness(t *testing.T) {
	source := newModuleTestDB(t, "MSG")
	message := domain.Message{AccountID: 91, ConversationID: 11, SenderType: domain.SenderTypeUser, SenderID: 7, MessageType: domain.MessageTypeOutgoing, Content: "before", Status: domain.MessageStatusSent}
	if err := source.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Create(&domain.Attachment{AccountID: 91, MessageID: message.ID, FileType: "text/plain", DataURL: "med://asset/1"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Create(&domain.DraftMessage{AccountID: 91, ConversationID: 11, UserID: 7, Message: "draft"}).Error; err != nil {
		t.Fatal(err)
	}
	conversationID, userID := uint(11), uint(7)
	if err := source.Create(&domain.EmailLog{AccountID: 91, ConversationID: &conversationID, UserID: &userID, EmailType: "transcript", ToEmail: "customer@example.test", FromEmail: "support@example.test", Subject: "subject", ContentText: "body", Status: "pending", DeliveryStatus: "pending"}).Error; err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("MSG")
	runtime := Runtime{Config: cfg, DB: source, Contracts: NewContractRegistry("MSG")}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	command := foundation.CommandEnvelope{CommandID: "snapshot-message", CommandType: "msg.message.update.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "message", ID: strconv.FormatUint(uint64(message.ID), 10)}, Input: map[string]any{"operation": "edit", "content": "after"}, IdempotencyKey: "snapshot-message-91", CorrelationID: "snapshot-message-correlation", CausationID: "snapshot-message-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "msg.message.get.v1"}}
	body, _ := json.Marshal(command)
	request := httptest.NewRequest(http.MethodPost, "/contracts/commands/msg.message.update.v1", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	request.Header.Set(headerCaller, "API")
	request.Header.Set(headerContractVersion, "v1")
	request.Header.Set(headerCorrelationID, command.CorrelationID)
	response := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("message command=%d body=%s", response.Code, response.Body.String())
	}
	path := filepath.Join(t.TempDir(), "msg.snapshot.json")
	if err := CreateSnapshot(source, "MSG", "msg-gen-1", path); err != nil {
		t.Fatal(err)
	}
	target := newModuleTestDB(t, "MSG")
	targetCfg := businessConfig("MSG")
	targetCfg.RecoveryGeneration = "msg-gen-1"
	targetRuntime := Runtime{Config: targetCfg, DB: target}
	if err := targetRuntime.Readiness(); err == nil {
		t.Fatal("MSG target was ready before restore")
	}
	if err := RestoreSnapshot(context.Background(), target, "MSG", "msg-gen-1", path, nil); err != nil {
		t.Fatal(err)
	}
	if err := targetRuntime.Readiness(); err != nil {
		t.Fatalf("MSG target not ready after restore: %v", err)
	}
	counts := map[string]int64{}
	for name, model := range map[string]any{"messages": &domain.Message{}, "attachments": &domain.Attachment{}, "drafts": &domain.DraftMessage{}, "emails": &domain.EmailLog{}} {
		var count int64
		_ = target.Model(model).Count(&count).Error
		counts[name] = count
	}
	var receipts, events int64
	_ = target.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = target.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_outbox").Count(&events).Error
	if counts["messages"] != 1 || counts["attachments"] != 1 || counts["drafts"] != 1 || counts["emails"] != 1 || receipts != 1 || events != 1 {
		t.Fatalf("counts=%v receipts=%d events=%d", counts, receipts, events)
	}
	for _, foreign := range []any{&domain.Account{}, &domain.Inbox{}, &domain.Contact{}, &domain.Conversation{}, &domain.User{}} {
		if target.Migrator().HasTable(foreign) {
			t.Fatalf("MSG restore created foreign table %T", foreign)
		}
	}
}

func TestRTESnapshotRestoresOwnerFactsJournalAndReadiness(t *testing.T) {
	source := newModuleTestDB(t, "RTE")
	now := time.Now().UTC()
	agentPolicy := domain.AgentCapacityPolicy{AccountID: 91, Name: "Default capacity", Description: "restore", CreatedAt: now, UpdatedAt: now}
	if err := source.Create(&agentPolicy).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Create(&domain.InboxCapacityLimit{AgentCapacityPolicyID: agentPolicy.ID, InboxID: 11, ConversationLimit: 5, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Create(&domain.AssignmentPolicy{AccountID: 91, Name: "Round robin", StrategyType: "round_robin", Enabled: true, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	cfg := rteRuntimeConfig(t)
	runtime := Runtime{Config: cfg, DB: source, Contracts: NewContractRegistry("RTE")}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	command := foundation.CommandEnvelope{CommandID: "snapshot-rte", CommandType: "rte.capacity.manage.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "user", ID: "7"}, Input: map[string]any{"user_id": 7, "conversation_limit": 9}, IdempotencyKey: "snapshot-rte-91", CorrelationID: "snapshot-rte-correlation", CausationID: "snapshot-rte-cause", RequestedAt: now, TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "rte.capacity.get.v1"}}
	body, _ := json.Marshal(command)
	request := httptest.NewRequest(http.MethodPost, "/contracts/commands/rte.capacity.manage.v1", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	request.Header.Set(headerCaller, "API")
	request.Header.Set(headerContractVersion, "v1")
	request.Header.Set(headerCorrelationID, command.CorrelationID)
	response := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("RTE command=%d body=%s", response.Code, response.Body.String())
	}
	path := filepath.Join(t.TempDir(), "rte.snapshot.json")
	if err := CreateSnapshot(source, "RTE", "rte-gen-1", path); err != nil {
		t.Fatal(err)
	}
	target := newModuleTestDB(t, "RTE")
	targetCfg := businessConfig("RTE")
	targetCfg.RecoveryGeneration = "rte-gen-1"
	targetRuntime := Runtime{Config: targetCfg, DB: target}
	if err := targetRuntime.Readiness(); err == nil {
		t.Fatal("RTE target was ready before restore")
	}
	if err := RestoreSnapshot(context.Background(), target, "RTE", "rte-gen-1", path, nil); err != nil {
		t.Fatal(err)
	}
	if err := targetRuntime.Readiness(); err != nil {
		t.Fatalf("RTE target not ready after restore: %v", err)
	}
	counts := map[string]int64{}
	for name, model := range map[string]any{"agent_policies": &domain.AgentCapacityPolicy{}, "inbox_limits": &domain.InboxCapacityLimit{}, "assignment_policies": &domain.AssignmentPolicy{}, "legacy_capacity": &domain.CapacityPolicy{}} {
		var count int64
		_ = target.Model(model).Count(&count).Error
		counts[name] = count
	}
	var receipts, events int64
	_ = target.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = target.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_outbox").Count(&events).Error
	if counts["agent_policies"] != 1 || counts["inbox_limits"] != 1 || counts["assignment_policies"] != 1 || counts["legacy_capacity"] != 1 || receipts != 1 || events != 2 {
		t.Fatalf("counts=%v receipts=%d events=%d", counts, receipts, events)
	}
	for _, foreign := range []any{&domain.Account{}, &domain.Inbox{}, &domain.Conversation{}, &domain.Message{}, &domain.User{}, &domain.Team{}} {
		if target.Migrator().HasTable(foreign) {
			t.Fatalf("RTE restore created foreign table %T", foreign)
		}
	}
}

func TestTKTSnapshotRestoresOwnerFactsJournalAndReadiness(t *testing.T) {
	source := newModuleTestDB(t, "TKT")
	now := time.Now().UTC()
	userID := uint(7)
	ticket := domain.Ticket{AccountID: 91, TicketNumber: "TKT-SNAPSHOT", Title: "Private", Description: "Private", Status: "open", Priority: "high", CreatorID: &userID, SLAStatus: "normal", Version: 1, CreatedAt: now, UpdatedAt: now}
	if err := source.Create(&ticket).Error; err != nil {
		t.Fatal(err)
	}
	for _, fact := range []any{
		&domain.TicketComment{AccountID: 91, TicketID: ticket.ID, UserID: &userID, Content: "private", IsPrivate: true, CreatedAt: now, UpdatedAt: now},
		&domain.TicketActivity{AccountID: 91, TicketID: ticket.ID, UserID: &userID, Action: "created", Details: `{}`, CreatedAt: now},
		&domain.TicketStatusHistory{AccountID: 91, TicketID: ticket.ID, FromStatus: "", ToStatus: "open", UserID: &userID, CreatedAt: now},
		&domain.TicketAttachment{AccountID: 91, TicketID: ticket.ID, FileName: "private.pdf", FileType: "application/pdf", DataURL: "med://private", CreatedAt: now},
		&domain.TicketWatcher{AccountID: 91, TicketID: ticket.ID, UserID: userID, CreatedAt: now},
	} {
		if err := source.Create(fact).Error; err != nil {
			t.Fatal(err)
		}
	}
	cfg := businessConfig("TKT")
	runtime := Runtime{Config: cfg, DB: source, Contracts: NewContractRegistry("TKT")}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	command := foundation.CommandEnvelope{CommandID: "snapshot-tkt", CommandType: "tkt.account_tickets.anonymize.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "91"}, Input: map[string]any{}, IdempotencyKey: "snapshot-tkt-91", CorrelationID: "snapshot-tkt-correlation", CausationID: "snapshot-tkt-cause", RequestedAt: now, TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "tkt.account_deletion_status.get.v1"}}
	body, _ := json.Marshal(command)
	request := httptest.NewRequest(http.MethodPost, "/contracts/commands/tkt.account_tickets.anonymize.v1", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	request.Header.Set(headerCaller, "API")
	request.Header.Set(headerContractVersion, "v1")
	request.Header.Set(headerCorrelationID, command.CorrelationID)
	response := httptest.NewRecorder()
	runtime.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("TKT command=%d body=%s", response.Code, response.Body.String())
	}
	if err := source.Create(&domain.TicketWatcher{AccountID: 91, TicketID: ticket.ID, UserID: userID, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	event := foundation.EventEnvelope{EventID: "snapshot-tkt-event", EventType: "tkt.ticket.created.v1", EventVersion: "v1", SourceModule: "TKT", AccountID: 91, AggregateType: "Ticket", AggregateID: strconv.FormatUint(uint64(ticket.ID), 10), AggregateVersion: 1, OccurredAt: now, PublishedAt: now, Actor: command.Actor, CorrelationID: command.CorrelationID, CausationID: command.CommandID, ChangedFields: []string{"created"}, Payload: map[string]any{"ticket_id": ticket.ID, "status": "open"}, DataClassification: "internal"}
	if err := NewEventOutbox(source, "TKT").Append(nil, event); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "tkt.snapshot.json")
	if err := CreateSnapshot(source, "TKT", "tkt-gen-1", path); err != nil {
		t.Fatal(err)
	}
	target := newModuleTestDB(t, "TKT")
	targetCfg := businessConfig("TKT")
	targetCfg.RecoveryGeneration = "tkt-gen-1"
	targetRuntime := Runtime{Config: targetCfg, DB: target}
	if err := targetRuntime.Readiness(); err == nil {
		t.Fatal("TKT target was ready before restore")
	}
	if err := RestoreSnapshot(context.Background(), target, "TKT", "tkt-gen-1", path, nil); err != nil {
		t.Fatal(err)
	}
	if err := targetRuntime.Readiness(); err != nil {
		t.Fatalf("TKT target not ready after restore: %v", err)
	}
	counts := map[string]int64{}
	for name, model := range map[string]any{"tickets": &domain.Ticket{}, "comments": &domain.TicketComment{}, "activities": &domain.TicketActivity{}, "histories": &domain.TicketStatusHistory{}, "attachments": &domain.TicketAttachment{}, "watchers": &domain.TicketWatcher{}} {
		var count int64
		_ = target.Model(model).Count(&count).Error
		counts[name] = count
	}
	var receipts, events int64
	_ = target.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = target.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_outbox").Count(&events).Error
	if counts["tickets"] != 1 || counts["comments"] != 1 || counts["activities"] != 1 || counts["histories"] != 1 || counts["attachments"] != 1 || counts["watchers"] != 1 || receipts != 1 || events != 1 {
		t.Fatalf("counts=%v receipts=%d events=%d", counts, receipts, events)
	}
	for _, foreign := range []any{&domain.Account{}, &domain.User{}, &domain.Contact{}, &domain.Conversation{}, &domain.Message{}} {
		if target.Migrator().HasTable(foreign) {
			t.Fatalf("TKT restore created foreign table %T", foreign)
		}
	}
}

func sha256Bytes(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
