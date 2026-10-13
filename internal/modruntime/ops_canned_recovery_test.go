package modruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func rewriteCannedSnapshot(t *testing.T, path string, edit func(*ModuleSnapshot)) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var file snapshotFile
	require.NoError(t, json.Unmarshal(raw, &file))
	edit(&file.Snapshot)
	canonical, err := json.Marshal(file.Snapshot)
	require.NoError(t, err)
	sum := sha256.Sum256(canonical)
	file.Checksum = hex.EncodeToString(sum[:])
	raw, err = json.Marshal(file)
	require.NoError(t, err)
	out := filepath.Join(t.TempDir(), "modified.json")
	require.NoError(t, os.WriteFile(out, raw, 0600))
	return out
}
func cannedRecovery(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	t.Run("version_receipt_and_deleted_high_water_survive", func(t *testing.T) {
		f := newCannedReadFixture(t, open)
		first := cannedCreated(t, f, "first")
		c := cannedCommand("second", "create")
		c.Input["short_code"] = "second"
		var second cannedVersionView
		require.NoError(t, json.Unmarshal(cannedWrite(t, f, c, 200), &second))
		remove := cannedCommand("remove", "delete")
		cannedTarget(&remove, second.ID, 1)
		before := cannedWrite(t, f, remove, 200)
		path := filepath.Join(t.TempDir(), "snapshot.json")
		require.NoError(t, CreateSnapshot(f.runtime.DB, "OPS", "recovery", path))
		restored := open(t, "OPS")
		require.NoError(t, RestoreSnapshot(context.Background(), restored, "OPS", "recovery", path, nil))
		f.runtime.DB = restored
		f.runtime.Contracts.UseCommandReceipts(restored)
		require.JSONEq(t, string(before), string(cannedWrite(t, f, remove, 200)))
		var row domain.CannedResponse
		require.NoError(t, restored.First(&row, first.ID).Error)
		require.Equal(t, int64(1), row.ObjectVersion)
		c = cannedCommand("after", "create")
		c.Input["short_code"] = "after"
		var after cannedVersionView
		require.NoError(t, json.Unmarshal(cannedWrite(t, f, c, 200), &after))
		require.Greater(t, after.ID, second.ID)
	})
	t.Run("deleted_empty_table_never_reuses_identity", func(t *testing.T) {
		f := newCannedReadFixture(t, open)
		row := cannedCreated(t, f, "seed")
		c := cannedCommand("delete", "delete")
		cannedTarget(&c, row.ID, 1)
		cannedWrite(t, f, c, 200)
		path := filepath.Join(t.TempDir(), "snapshot.json")
		require.NoError(t, CreateSnapshot(f.runtime.DB, "OPS", "empty", path))
		restored := open(t, "OPS")
		require.NoError(t, RestoreSnapshot(context.Background(), restored, "OPS", "empty", path, nil))
		f.runtime.DB = restored
		f.runtime.Contracts.UseCommandReceipts(restored)
		after := cannedCreated(t, f, "after")
		require.Greater(t, after.ID, row.ID)
	})
	for _, name := range []string{"missing_version", "null_version", "negative_version", "unknown_codec", "duplicate_runtime_shortcode"} {
		t.Run(name, func(t *testing.T) {
			f := newCannedReadFixture(t, open)
			cannedCreated(t, f, "seed")
			path := filepath.Join(t.TempDir(), "snapshot.json")
			require.NoError(t, CreateSnapshot(f.runtime.DB, "OPS", "invalid", path))
			path = rewriteCannedSnapshot(t, path, func(s *ModuleSnapshot) {
				var rows map[string]any
				require.NoError(t, json.Unmarshal(s.Tables["canned_responses"], &rows))
				item := rows["rows"].([]any)[0].(map[string]any)
				switch name {
				case "missing_version":
					delete(item, "object_version")
				case "null_version":
					item["object_version"] = nil
				case "negative_version":
					item["object_version"] = -1
				case "unknown_codec":
					rows["codec"] = "unknown"
				case "duplicate_runtime_shortcode":
					copy := map[string]any{}
					for k, v := range item {
						copy[k] = v
					}
					copy["id"] = 42
					rows["rows"] = append(rows["rows"].([]any), copy)
				}
				s.Tables["canned_responses"], _ = json.Marshal(rows)
			})
			target := open(t, "OPS")
			sentinel := domain.CannedResponse{AccountID: 92, ShortCode: "sentinel", Content: "keep"}
			require.NoError(t, target.Create(&sentinel).Error)
			require.Error(t, RestoreSnapshot(context.Background(), target, "OPS", "invalid", path, nil))
			var after domain.CannedResponse
			require.NoError(t, target.First(&after, sentinel.ID).Error)
			require.Equal(t, sentinel.Content, after.Content)
		})
	}
	t.Run("explicit_legacy_six_field_snapshot_branch", func(t *testing.T) {
		f := newCannedReadFixture(t, open)
		legacy := domain.CannedResponse{AccountID: 91, ShortCode: " MiXeD ", Content: "unchanged"}
		require.NoError(t, f.runtime.DB.Create(&legacy).Error)
		path := filepath.Join(t.TempDir(), "snapshot.json")
		require.NoError(t, CreateSnapshot(f.runtime.DB, "OPS", "legacy", path))
		path = rewriteCannedSnapshot(t, path, func(s *ModuleSnapshot) {
			s.Tables["canned_responses"], _ = json.Marshal([]domain.CannedResponse{legacy})
		})
		target := open(t, "OPS")
		require.NoError(t, RestoreSnapshot(context.Background(), target, "OPS", "legacy", path, nil))
		var row domain.CannedResponse
		require.NoError(t, target.First(&row, legacy.ID).Error)
		require.Zero(t, row.ObjectVersion)
		require.Equal(t, legacy.ShortCode, row.ShortCode)
	})
	t.Run("failed_rebuild_rolls_back_rows_and_sequence", func(t *testing.T) {
		f := newCannedReadFixture(t, open)
		cannedCreated(t, f, "seed")
		path := filepath.Join(t.TempDir(), "snapshot.json")
		require.NoError(t, CreateSnapshot(f.runtime.DB, "OPS", "rollback", path))
		target := open(t, "OPS")
		for i := 0; i < 7; i++ {
			require.NoError(t, target.Create(&domain.CannedResponse{AccountID: 92, ShortCode: "legacy", Content: "keep"}).Error)
		}
		require.Error(t, RestoreSnapshot(context.Background(), target, "OPS", "rollback", path, func(context.Context, *gorm.DB) error { return errors.New("rebuild failure") }))
		var count int64
		require.NoError(t, target.Model(&domain.CannedResponse{}).Count(&count).Error)
		require.Equal(t, int64(7), count)
		row := domain.CannedResponse{AccountID: 92, ShortCode: "after", Content: "keep"}
		require.NoError(t, target.Create(&row).Error)
		require.Equal(t, uint(8), row.ID)
	})
}
func TestOPSCannedResponseRecovery(t *testing.T)         { cannedRecovery(t, newModuleTestDB) }
func TestPostgresOPSCannedResponseRecovery(t *testing.T) { cannedRecovery(t, postgresModuleDB) }

func cannedMigration(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	db := open(t, "OPS")
	require.NoError(t, db.Migrator().DropTable(&domain.CannedResponse{}))
	idType, timeType := "INTEGER PRIMARY KEY AUTOINCREMENT", "datetime"
	if db.Dialector.Name() == "postgres" {
		idType = "BIGSERIAL PRIMARY KEY"
		timeType = "timestamptz"
	}
	require.NoError(t, db.Exec("CREATE TABLE canned_responses (id "+idType+", account_id bigint NOT NULL, short_code varchar(100) NOT NULL, content text NOT NULL, created_at "+timeType+", updated_at "+timeType+")").Error)
	at := time.Date(2024, 2, 3, 4, 5, 6, 123000000, time.UTC)
	for _, account := range []uint{91, 91, 92} {
		require.NoError(t, db.Exec("INSERT INTO canned_responses(account_id,short_code,content,created_at,updated_at) VALUES (?,?,?,?,?)", account, " MiXeD ", " \nlegacy <b>content</b> ", at, at).Error)
	}
	var before []domain.CannedResponse
	require.NoError(t, db.Find(&before).Error)
	for i := 0; i < 2; i++ {
		require.NoError(t, database.AutoMigrateModule(db, "OPS"))
		var after []domain.CannedResponse
		require.NoError(t, db.Order("id").Find(&after).Error)
		require.Len(t, after, 3)
		for j, row := range after {
			require.Zero(t, row.ObjectVersion)
			require.Equal(t, before[j].ShortCode, row.ShortCode)
			require.Equal(t, before[j].Content, row.Content)
			require.True(t, before[j].CreatedAt.Equal(row.CreatedAt))
			require.True(t, before[j].UpdatedAt.Equal(row.UpdatedAt))
		}
	}
	require.Error(t, db.Exec("UPDATE canned_responses SET object_version = NULL WHERE id = ?", before[0].ID).Error)
	require.NoError(t, db.Exec("INSERT INTO canned_responses(account_id,short_code,content) VALUES (?,?,?)", 91, "default", "body").Error)
	var row domain.CannedResponse
	require.NoError(t, db.Where("short_code = ?", "default").First(&row).Error)
	require.Zero(t, row.ObjectVersion)
}
func TestOPSCannedResponseMigration(t *testing.T)         { cannedMigration(t, newModuleTestDB) }
func TestPostgresOPSCannedResponseMigration(t *testing.T) { cannedMigration(t, postgresModuleDB) }
