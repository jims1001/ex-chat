package modruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestMessageTemplateSnapshotCompatibility(t *testing.T) {
	for _, tc := range []struct{ name, rows, status, syncStatus string }{
		{"old_explicit_status", `[{"id":41,"account_id":91,"inbox_id":11,"name":"legacy","status":"APPROVED","sync_status":"synced"}]`, "APPROVED", "synced"},
		{"old_omitted_fields", `[{"id":41,"account_id":91,"inbox_id":11,"name":"legacy"}]`, "", ""},
		{"explicit_unknown_fields", `[{"id":41,"account_id":91,"inbox_id":11,"name":"legacy","status":"","sync_status":""}]`, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := newModuleTestDB(t, "CHN")
			require.NoError(t, source.Create(&domain.Inbox{ID: 11, AccountID: 91, Name: "Templates", WebsiteToken: "template-snapshot"}).Error)
			path := filepath.Join(t.TempDir(), "legacy-template.json")
			require.NoError(t, CreateSnapshot(source, "CHN", "templates", path))
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			var file snapshotFile
			require.NoError(t, json.Unmarshal(raw, &file))
			file.Snapshot.Tables["inbox_message_templates"] = json.RawMessage(tc.rows)
			canonical, err := json.Marshal(file.Snapshot)
			require.NoError(t, err)
			sum := sha256.Sum256(canonical)
			file.Checksum = hex.EncodeToString(sum[:])
			raw, err = json.Marshal(file)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, raw, 0600))
			target := newModuleTestDB(t, "CHN")
			require.NoError(t, RestoreSnapshot(context.Background(), target, "CHN", "templates", path, nil))
			var got domain.InboxMessageTemplate
			require.NoError(t, target.First(&got, 41).Error)
			require.Equal(t, uint(41), got.ID)
			require.Equal(t, tc.status, got.Status)
			require.Equal(t, tc.syncStatus, got.SyncStatus)
			// The current codec remains the same plain row JSON, so round-trip it too.
			roundtrip := filepath.Join(t.TempDir(), "roundtrip.json")
			require.NoError(t, CreateSnapshot(target, "CHN", "templates-roundtrip", roundtrip))
			require.NoError(t, RestoreSnapshot(context.Background(), target, "CHN", "templates-roundtrip", roundtrip, nil))
			var again domain.InboxMessageTemplate
			require.NoError(t, target.First(&again, 41).Error)
			require.Equal(t, got, again)
		})
	}
}
