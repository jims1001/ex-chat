package modruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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

func sha256Bytes(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
