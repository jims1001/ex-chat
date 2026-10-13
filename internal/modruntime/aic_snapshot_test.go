package modruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func aicSnapshotFixtures() []domain.AIUsageQuota {
	base := time.Date(2026, time.October, 7, 1, 2, 3, 456789000, time.UTC)
	return []domain.AIUsageQuota{
		{ID: 701, AccountID: 91, MonthlyTokenLimit: 0, UsedTokens: 19, MonthlyRequestLimit: 0, UsedRequests: 3, ResetAt: base.Add(-time.Hour), CreatedAt: base.Add(-48 * time.Hour), UpdatedAt: base.Add(-24 * time.Hour)},
		{ID: 702, AccountID: 92, MonthlyTokenLimit: 0, UsedTokens: 29, MonthlyRequestLimit: 47, UsedRequests: 5, ResetAt: base.Add(2 * time.Hour), CreatedAt: base.Add(-47 * time.Hour), UpdatedAt: base.Add(-23 * time.Hour)},
		{ID: 703, AccountID: 93, MonthlyTokenLimit: 987654, UsedTokens: 39, MonthlyRequestLimit: 0, UsedRequests: 7, ResetAt: base.Add(3 * time.Hour), CreatedAt: base.Add(-46 * time.Hour), UpdatedAt: base.Add(-22 * time.Hour)},
		{ID: 704, AccountID: 94, MonthlyTokenLimit: -11, UsedTokens: -2, MonthlyRequestLimit: -13, UsedRequests: -3, ResetAt: base.AddDate(-5, 0, 0), CreatedAt: base.AddDate(-6, 0, 0), UpdatedAt: base.AddDate(-4, 0, 0)},
		{ID: 705, AccountID: 95}, // Explicit zeros, including all three timestamps.
		{ID: 706, AccountID: 96, MonthlyTokenLimit: math.MinInt64, UsedTokens: math.MaxInt64, MonthlyRequestLimit: math.MaxInt, UsedRequests: math.MinInt, ResetAt: base, CreatedAt: base, UpdatedAt: base},
	}
}

func aicSnapshotColumns(row domain.AIUsageQuota) map[string]any {
	return map[string]any{
		"id": row.ID, "account_id": row.AccountID,
		"monthly_token_limit": row.MonthlyTokenLimit, "used_tokens": row.UsedTokens,
		"monthly_request_limit": row.MonthlyRequestLimit, "used_requests": row.UsedRequests,
		"reset_at": row.ResetAt, "created_at": row.CreatedAt, "updated_at": row.UpdatedAt,
	}
}

func aicSnapshotSeed(t *testing.T, db *gorm.DB, row domain.AIUsageQuota) {
	t.Helper()
	require.NoError(t, db.Model(&domain.AIUsageQuota{}).Create(aicSnapshotColumns(row)).Error)
}

func assertAICSnapshotExact(t *testing.T, got, expected domain.AIUsageQuota) {
	t.Helper()
	// This is the original failing-before diagnostic's per-field assertion.
	// Keep it explicit so every lost default, counter or timestamp is visible.
	for _, field := range []struct {
		name string
		got  any
		want any
	}{
		{"id", got.ID, expected.ID},
		{"account_id", got.AccountID, expected.AccountID},
		{"monthly_token_limit", got.MonthlyTokenLimit, expected.MonthlyTokenLimit},
		{"used_tokens", got.UsedTokens, expected.UsedTokens},
		{"monthly_request_limit", got.MonthlyRequestLimit, expected.MonthlyRequestLimit},
		{"used_requests", got.UsedRequests, expected.UsedRequests},
		{"reset_at", got.ResetAt.UTC().Format(time.RFC3339Nano), expected.ResetAt.UTC().Format(time.RFC3339Nano)},
		{"created_at", got.CreatedAt.UTC().Format(time.RFC3339Nano), expected.CreatedAt.UTC().Format(time.RFC3339Nano)},
		{"updated_at", got.UpdatedAt.UTC().Format(time.RFC3339Nano), expected.UpdatedAt.UTC().Format(time.RFC3339Nano)},
	} {
		require.Equal(t, field.want, field.got, "account_id=%d field=%s", expected.AccountID, field.name)
	}
}

func TestAICSnapshotPreservesPersistedQuotaValues(t *testing.T) {
	testAICSnapshotPreservesPersistedQuotaValues(t, newModuleTestDB)
}

func TestPostgresAICSnapshotPreservesPersistedQuotaValues(t *testing.T) {
	testAICSnapshotPreservesPersistedQuotaValues(t, postgresModuleDB)
}

func testAICSnapshotPreservesPersistedQuotaValues(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	source, target := open(t, "AIC"), open(t, "AIC")
	want := aicSnapshotFixtures()
	if source.Dialector.Name() == "sqlite" {
		want[3].ResetAt = want[3].ResetAt.Add(time.Nanosecond)
		want[3].CreatedAt = want[3].CreatedAt.Add(2 * time.Nanosecond)
		want[3].UpdatedAt = want[3].UpdatedAt.Add(3 * time.Nanosecond)
	}
	for _, row := range want {
		aicSnapshotSeed(t, source, row)
		var persisted domain.AIUsageQuota
		require.NoError(t, source.Where("account_id = ?", row.AccountID).First(&persisted).Error)
		assertAICSnapshotExact(t, persisted, row)
	}
	path := filepath.Join(t.TempDir(), "aic.snapshot.json")
	require.NoError(t, CreateSnapshot(source, "AIC", "aic-quota-v1", path))
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	payload, err := os.ReadFile(path)
	require.NoError(t, err)
	var checkpoint snapshotFile
	require.NoError(t, json.Unmarshal(payload, &checkpoint))
	table, err := modelTable(source, &domain.AIUsageQuota{})
	require.NoError(t, err)
	var snapshotted []domain.AIUsageQuota
	require.NoError(t, unmarshalSnapshotRows(checkpoint.Snapshot.Tables[table], &snapshotted))
	require.Len(t, snapshotted, len(want))
	for i, row := range want {
		assertAICSnapshotExact(t, snapshotted[i], row)
	}
	t.Log("source and snapshot retain all nine fields for six accounts, including zero/negative limits, signed counters and zero/past timestamps")
	stale := want[0]
	stale.ID, stale.AccountID = 999, 999
	aicSnapshotSeed(t, target, stale)
	require.NoError(t, RestoreSnapshot(context.Background(), target, "AIC", "aic-quota-v1", path, nil))
	var restored []domain.AIUsageQuota
	require.NoError(t, target.Order("id ASC").Find(&restored).Error)
	require.Len(t, restored, len(want))
	for i, row := range want {
		t.Run(fmt.Sprintf("account_%d", row.AccountID), func(t *testing.T) {
			assertAICSnapshotExact(t, restored[i], row)
			query := aicUsageQuery()
			query.AccountID = row.AccountID
			got := aicUsageData(t, aicUsageRequest(aicUsageHandler(t, target), query))
			encoded, err := json.Marshal(row)
			require.NoError(t, err)
			var expected aiUsageView
			require.NoError(t, json.Unmarshal(encoded, &expected))
			require.Equal(t, expected, got, "restored usage query changed persisted accounting")
		})
	}
	for _, foreign := range []any{&domain.Account{}, &domain.AccountUser{}, &domain.User{}, &domain.Conversation{}} {
		require.False(t, target.Migrator().HasTable(foreign), "AIC restore created foreign table %T", foreign)
	}
	t.Run("sequence_continuation", func(t *testing.T) {
		next := domain.AIUsageQuota{AccountID: 101}
		require.NoError(t, target.Create(&next).Error)
		require.Greater(t, next.ID, want[len(want)-1].ID)
		if target.Dialector.Name() == "postgres" {
			require.Equal(t, want[len(want)-1].ID+1, next.ID)
		}
		// The ordinary writer retains its pre-existing defaults.
		require.Equal(t, int64(100000), next.MonthlyTokenLimit)
		require.Equal(t, 1000, next.MonthlyRequestLimit)
	})
	t.Run("empty", func(t *testing.T) {
		require.NoError(t, source.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&domain.AIUsageQuota{}).Error)
		require.NoError(t, CreateSnapshot(source, "AIC", "aic-empty-v1", path))
		require.NoError(t, RestoreSnapshot(context.Background(), target, "AIC", "aic-empty-v1", path, nil))
		var count int64
		require.NoError(t, target.Model(&domain.AIUsageQuota{}).Count(&count).Error)
		require.Zero(t, count)
	})
}

type aicSnapshotInvalidCase struct {
	name string
	rows json.RawMessage
}

func aicSnapshotInvalidCases(t *testing.T) []aicSnapshotInvalidCase {
	t.Helper()
	encoded, err := json.Marshal(aicSnapshotFixtures()[0])
	require.NoError(t, err)
	valid := string(encoded)
	result := []aicSnapshotInvalidCase{}
	add := func(name, value string) {
		result = append(result, aicSnapshotInvalidCase{name, json.RawMessage(value)})
	}
	replace := func(field, value string) string {
		var row map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(encoded, &row))
		if value == "" {
			delete(row, field)
		} else {
			row[field] = json.RawMessage(value)
		}
		raw, err := json.Marshal(row)
		require.NoError(t, err)
		return "[" + string(raw) + "]"
	}
	for _, field := range aiUsageFields {
		add("missing/"+field, replace(field, ""))
		add("null/"+field, replace(field, "null"))
		add("duplicate_field/"+field, "["+strings.TrimSuffix(valid, "}")+`,"`+field+`":null}]`)
		add("case_alias/"+field, "["+strings.Replace(valid, `"`+field+`":`, `"`+strings.ToUpper(field)+`":`, 1)+"]")
	}
	for _, field := range aiUsageFields[:6] {
		for _, bad := range []struct{ name, value string }{
			{"string", `"7"`}, {"fraction", "1.5"}, {"exponent", "1e2"}, {"boolean", "true"},
			{"object", "{}"}, {"overflow", "9223372036854775808"}, {"underflow", "-9223372036854775809"},
		} {
			add("numeric_"+bad.name+"/"+field, replace(field, bad.value))
		}
	}
	for _, field := range aiUsageFields[:2] {
		add("zero_identity/"+field, replace(field, "0"))
		add("negative_identity/"+field, replace(field, "-1"))
	}
	for _, field := range aiUsageFields[6:] {
		for _, bad := range []struct{ name, value string }{
			{"empty", `""`}, {"numeric", "0"}, {"malformed", `"not-a-time"`}, {"date_only", `"2020-01-01"`},
			{"calendar", `"2020-02-31T00:00:00Z"`}, {"zero_year", `"0000-01-01T00:00:00Z"`},
			{"offset", `"2020-01-01T00:00:00+24:00"`}, {"precision", `"2020-01-01T00:00:00.1234567891Z"`},
			{"utc_underflow", `"0001-01-01T00:00:00+01:00"`}, {"utc_overflow", `"9999-12-31T23:59:59-01:00"`},
		} {
			add("time_"+bad.name+"/"+field, replace(field, bad.value))
		}
	}
	add("null_table", "null")
	add("object_table", "{}")
	add("null_row", "[null]")
	add("array_row", "[[]]")
	add("empty_row", "[{}]")
	add("unknown_field", "["+strings.TrimSuffix(valid, "}")+`,"quota_policy":"allow"}]`)
	add("escaped_duplicate_field", "["+strings.TrimSuffix(valid, "}")+`,"\u0069d":701}]`)
	add("duplicate_id", "["+valid+","+strings.Replace(valid, `"account_id":91`, `"account_id":92`, 1)+"]")
	add("duplicate_account", "["+valid+","+strings.Replace(valid, `"id":701`, `"id":702`, 1)+"]")
	return result
}

func aicSnapshotRewriteRows(t *testing.T, original []byte, path string, raw json.RawMessage) {
	t.Helper()
	var file snapshotFile
	require.NoError(t, json.Unmarshal(original, &file))
	file.Snapshot.Tables["ai_usage_quota"] = raw
	canonical, err := json.Marshal(file.Snapshot)
	require.NoError(t, err)
	file.Checksum = sha256Bytes(canonical)
	encoded, err := json.Marshal(file)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, encoded, 0600))
}

func TestAICSnapshotRejectsInvalidRowsAtomically(t *testing.T) {
	testAICSnapshotRejectsInvalidRowsAtomically(t, newModuleTestDB)
}

func TestPostgresAICSnapshotRejectsInvalidRowsAtomically(t *testing.T) {
	testAICSnapshotRejectsInvalidRowsAtomically(t, postgresModuleDB)
}

func testAICSnapshotRejectsInvalidRowsAtomically(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	source, target := open(t, "AIC"), open(t, "AIC")
	aicSnapshotSeed(t, source, aicSnapshotFixtures()[0])
	sentinel := aicSnapshotFixtures()[3]
	sentinel.ID, sentinel.AccountID = 0, 991
	// Establish the target's own generated-ID sequence before exercising
	// rollback; an explicit-ID PostgreSQL insert does not advance it.
	require.NoError(t, target.Create(&sentinel).Error)
	require.Equal(t, uint(1), sentinel.ID)
	journal := domain.LocalChangeJournal{ChangeID: "aic-original-marker", SourceModule: "AIC", ObjectType: "AIUsageQuota", Action: "create"}
	require.NoError(t, target.Create(&journal).Error)
	path := filepath.Join(t.TempDir(), "aic-rollback.snapshot.json")
	require.NoError(t, CreateSnapshot(source, "AIC", "aic-rollback-v1", path))
	original, err := os.ReadFile(path)
	require.NoError(t, err)
	verify := func(t *testing.T) {
		t.Helper()
		var rows []domain.AIUsageQuota
		require.NoError(t, target.Find(&rows).Error)
		require.Len(t, rows, 1)
		assertAICSnapshotExact(t, rows[0], sentinel)
		var journals []domain.LocalChangeJournal
		require.NoError(t, target.Find(&journals).Error)
		require.Len(t, journals, 1)
		require.Equal(t, journal.ChangeID, journals[0].ChangeID)
	}
	for _, test := range aicSnapshotInvalidCases(t) {
		t.Run(test.name, func(t *testing.T) {
			aicSnapshotRewriteRows(t, original, path, test.rows)
			called := false
			err := RestoreSnapshot(context.Background(), target, "AIC", "aic-rollback-v1", path, func(context.Context, *gorm.DB) error { called = true; return nil })
			require.EqualError(t, err, "restore table ai_usage_quota: invalid rows")
			require.False(t, called)
			verify(t)
		})
	}
	if target.Dialector.Name() == "postgres" {
		for _, field := range aiUsageFields[6:] {
			t.Run("storage_time_precision/"+field, func(t *testing.T) {
				row := aicSnapshotColumns(aicSnapshotFixtures()[0])
				row[field] = time.Date(2020, 1, 1, 0, 0, 0, 1, time.UTC)
				raw, err := json.Marshal([]map[string]any{row})
				require.NoError(t, err)
				aicSnapshotRewriteRows(t, original, path, raw)
				require.EqualError(t, RestoreSnapshot(context.Background(), target, "AIC", "aic-rollback-v1", path, nil), "restore table ai_usage_quota: AI usage restore failed")
				verify(t)
			})
		}
	}
	t.Run("insert_failure", func(t *testing.T) {
		require.NoError(t, os.WriteFile(path, original, 0600))
		// The target has a local constraint the valid snapshot cannot satisfy.
		if target.Dialector.Name() == "postgres" {
			require.NoError(t, target.Exec("ALTER TABLE ai_usage_quota ADD CONSTRAINT aic_restore_reject CHECK (id < 100)").Error)
			defer target.Exec("ALTER TABLE ai_usage_quota DROP CONSTRAINT aic_restore_reject")
		} else {
			require.NoError(t, target.Exec("CREATE TRIGGER aic_restore_reject BEFORE INSERT ON ai_usage_quota WHEN NEW.id >= 100 BEGIN SELECT RAISE(ABORT, 'test restore insert failure'); END").Error)
			defer target.Exec("DROP TRIGGER aic_restore_reject")
		}
		require.EqualError(t, RestoreSnapshot(context.Background(), target, "AIC", "aic-rollback-v1", path, nil), "restore table ai_usage_quota: AI usage restore failed")
		verify(t)
	})
	t.Run("rebuild_failure", func(t *testing.T) {
		require.NoError(t, os.WriteFile(path, original, 0600))
		failure := errors.New("aic test rebuild failure")
		called := false
		err := RestoreSnapshot(context.Background(), target, "AIC", "aic-rollback-v1", path, func(_ context.Context, tx *gorm.DB) error {
			called = true
			var restored domain.AIUsageQuota
			require.NoError(t, tx.First(&restored).Error)
			assertAICSnapshotExact(t, restored, aicSnapshotFixtures()[0])
			return failure
		})
		require.ErrorIs(t, err, failure)
		require.True(t, called)
		verify(t)
	})
	t.Run("sequence_rollback", func(t *testing.T) {
		next := domain.AIUsageQuota{AccountID: 992}
		require.NoError(t, target.Create(&next).Error)
		require.Equal(t, uint(2), next.ID, "failed restores advanced the target quota sequence")
	})
}

func TestAICSnapshotRejectsMalformedJSON(t *testing.T) {
	for _, raw := range []string{`[`, `[{}] true`, `[{"id":NaN}]`, `[{"id":01}]`, `[{"id":+1}]`} {
		before := aicSnapshotFixtures()
		rows := append([]domain.AIUsageQuota(nil), before...)
		require.Error(t, unmarshalSnapshotRows([]byte(raw), &rows))
		require.Equal(t, before, rows, "failed decode modified caller rows")
	}
	// Codec field inventory must change explicitly if the domain model changes.
	typ := reflect.TypeOf(domain.AIUsageQuota{})
	require.Equal(t, len(aiUsageFields), typ.NumField())
	for i, field := range aiUsageFields {
		require.Equal(t, field, typ.Field(i).Tag.Get("json"))
	}
}

func TestAICSnapshotRejectsCorruptSource(t *testing.T) {
	testAICSnapshotRejectsCorruptSource(t, newModuleTestDB)
}

func TestPostgresAICSnapshotRejectsCorruptSource(t *testing.T) {
	testAICSnapshotRejectsCorruptSource(t, postgresModuleDB)
}

func testAICSnapshotRejectsCorruptSource(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	db := open(t, "AIC")
	// A legacy/damaged source can lack expected constraints. Test nullable and
	// duplicate identities as well as nullable numeric/timestamp fields.
	require.NoError(t, db.Migrator().DropTable(&domain.AIUsageQuota{}))
	timeType := "datetime"
	if db.Dialector.Name() == "postgres" {
		timeType = "timestamptz"
	}
	require.NoError(t, db.Exec("CREATE TABLE ai_usage_quota (id bigint, account_id bigint, monthly_token_limit bigint, used_tokens bigint, monthly_request_limit bigint, used_requests bigint, reset_at "+timeType+", created_at "+timeType+", updated_at "+timeType+")").Error)
	valid := aicSnapshotFixtures()[0]
	aicSnapshotSeed(t, db, valid)
	path := filepath.Join(t.TempDir(), "aic-source.snapshot.json")
	require.NoError(t, CreateSnapshot(db, "AIC", "aic-source-v1", path))
	original, err := os.ReadFile(path)
	require.NoError(t, err)
	reject := func(t *testing.T, mutate func(*gorm.DB)) {
		t.Helper()
		tx := db.Begin()
		require.NoError(t, tx.Error)
		defer tx.Rollback()
		mutate(tx)
		require.EqualError(t, CreateSnapshot(tx, "AIC", "aic-source-v1", path), "snapshot table ai_usage_quota: invalid AI usage checkpoint")
		after, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, original, after, "invalid source replaced the existing checkpoint")
	}
	for _, field := range aiUsageFields {
		t.Run("null/"+field, func(t *testing.T) {
			reject(t, func(tx *gorm.DB) { require.NoError(t, tx.Exec("UPDATE ai_usage_quota SET "+field+" = NULL").Error) })
		})
		t.Run("missing/"+field, func(t *testing.T) {
			reject(t, func(tx *gorm.DB) { require.NoError(t, tx.Exec("ALTER TABLE ai_usage_quota DROP COLUMN "+field).Error) })
		})
		t.Run("empty_missing/"+field, func(t *testing.T) {
			reject(t, func(tx *gorm.DB) {
				require.NoError(t, tx.Exec("DELETE FROM ai_usage_quota").Error)
				require.NoError(t, tx.Exec("ALTER TABLE ai_usage_quota DROP COLUMN "+field).Error)
			})
		})
	}
	for _, field := range aiUsageFields[:2] {
		for _, value := range []int64{0, -1} {
			t.Run(fmt.Sprintf("identity_%d/%s", value, field), func(t *testing.T) {
				reject(t, func(tx *gorm.DB) { require.NoError(t, tx.Exec("UPDATE ai_usage_quota SET "+field+" = ?", value).Error) })
			})
		}
	}
	for _, field := range []string{"id", "account_id"} {
		t.Run("duplicate/"+field, func(t *testing.T) {
			reject(t, func(tx *gorm.DB) {
				duplicate := valid
				if field == "id" {
					duplicate.AccountID++
				} else {
					duplicate.ID++
				}
				aicSnapshotSeed(t, tx, duplicate)
			})
		})
	}
	if db.Dialector.Name() == "sqlite" {
		for _, field := range aiUsageFields[:6] {
			for _, bad := range []struct {
				name  string
				value any
			}{{"text", "invalid"}, {"real", 0.5}, {"blob", []byte("12")}} {
				t.Run("storage_"+bad.name+"/"+field, func(t *testing.T) {
					reject(t, func(tx *gorm.DB) {
						require.NoError(t, tx.Exec("UPDATE ai_usage_quota SET "+field+" = ?", bad.value).Error)
					})
				})
			}
		}
	}
	for _, field := range aiUsageFields[6:] {
		values := []struct{ name, value string }{{"infinity", "infinity"}, {"year_range", "12345-01-01"}}
		if db.Dialector.Name() == "sqlite" {
			values = append(values, struct{ name, value string }{"malformed", "garbage"}, struct{ name, value string }{"precision", "2020-01-01T00:00:00.1234567891Z"})
		}
		for _, bad := range values {
			t.Run("time_"+bad.name+"/"+field, func(t *testing.T) {
				reject(t, func(tx *gorm.DB) {
					require.NoError(t, tx.Exec("UPDATE ai_usage_quota SET "+field+" = ?", bad.value).Error)
				})
			})
		}
	}
	t.Run("missing_table", func(t *testing.T) {
		reject(t, func(tx *gorm.DB) { require.NoError(t, tx.Exec("DROP TABLE ai_usage_quota").Error) })
	})
}
