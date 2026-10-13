package modruntime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func rptSnapshotWrite(t *testing.T, path string, snapshot snapshotFile, raw []byte) {
	t.Helper()
	snapshot.Snapshot.Tables["reporting_events"] = raw
	payload, err := json.Marshal(snapshot.Snapshot)
	require.NoError(t, err)
	sum := sha256.Sum256(payload)
	snapshot.Checksum = hex.EncodeToString(sum[:])
	encoded, err := json.Marshal(snapshot)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, encoded, 0600))
}
func rptSnapshotValidationCases(t *testing.T, source, target *gorm.DB) {
	rptEventsSeed(t, source, 1, 91, "source", "private-marker", nil)
	rptEventsSeed(t, target, 17, 91, "retained", "retained", nil)
	path := filepath.Join(t.TempDir(), "rpt-validation.json")
	require.NoError(t, CreateSnapshot(source, "RPT", "rpt-validation", path))
	encoded, err := os.ReadFile(path)
	require.NoError(t, err)
	var snapshot snapshotFile
	require.NoError(t, json.Unmarshal(encoded, &snapshot))
	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(snapshot.Snapshot.Tables["reporting_events"], &rows))
	before := rptEventsRows(t, target)
	var countBefore int64
	require.NoError(t, target.Model(&domain.LocalChangeJournal{}).Count(&countBefore).Error)
	variants := map[string][]byte{}
	for _, field := range []string{"id", "account_id", "name", "value", "event_start_time", "event_end_time", "metadata_json", "created_at"} {
		for _, kind := range []string{"missing", "null"} {
			var copyRows []map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(snapshot.Snapshot.Tables["reporting_events"], &copyRows))
			if kind == "missing" {
				delete(copyRows[0], field)
			} else {
				copyRows[0][field] = json.RawMessage("null")
			}
			variants[kind+"_"+field], err = json.Marshal(copyRows)
			require.NoError(t, err)
		}
	}
	var compact bytes.Buffer
	require.NoError(t, json.Compact(&compact, snapshot.Snapshot.Tables["reporting_events"]))
	raw := compact.Bytes()
	variants["duplicate_value"] = bytes.Replace(raw, []byte(`"value":0`), []byte(`"value":0,"value":3`), 1)
	variants["case_alias"] = bytes.Replace(raw, []byte(`"value":0`), []byte(`"value":0,"Value":3`), 1)
	variants["unknown"] = bytes.Replace(raw, []byte(`"value":0`), []byte(`"value":0,"extra":"private-marker"`), 1)
	variants["bad_time"] = bytes.Replace(raw, []byte(`"created_at":`), []byte(`"created_at_bad":`), 1)
	variants["null_table"] = []byte(`null`)
	for _, field := range []string{"conversation_id", "user_id", "inbox_id"} {
		for _, value := range []string{`-1`, `1.5`, `"1"`, `1e2`, `true`, `18446744073709551616`} {
			variants[field+value] = bytes.Replace(raw, []byte(`"value":0`), []byte(`"value":0,"`+field+`":`+value), 1)
		}
	}
	for name, raw := range variants {
		t.Run(name, func(t *testing.T) {
			rptSnapshotWrite(t, path, snapshot, raw)
			err := RestoreSnapshot(context.Background(), target, "RPT", "rpt-validation", path, nil)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-marker")
			require.Equal(t, before, rptEventsRows(t, target))
			var count int64
			require.NoError(t, target.Model(&domain.LocalChangeJournal{}).Count(&count).Error)
			require.Equal(t, countBefore, count)
		})
	}
	// Explicit zero and null nullable IDs are faithfully restored, as are legacy
	// snapshots in which nil relationships were omitted by omitempty.
	for _, fields := range []string{``, `,"conversation_id":null,"user_id":null,"inbox_id":null`, `,"conversation_id":0,"user_id":0,"inbox_id":0`, `,"conversation_id":9223372036854775807,"user_id":7,"inbox_id":8`} {
		candidate := bytes.Replace(raw, []byte(`"value":0`), []byte(`"value":0`+fields), 1)
		rptSnapshotWrite(t, path, snapshot, candidate)
		require.NoError(t, RestoreSnapshot(context.Background(), target, "RPT", "rpt-validation", path, nil))
		restored := rptEventsRows(t, target)
		require.Len(t, restored, 1)
		page := rptEventsPage(t, rptEventsRequest(rptEventsRuntime(t, target).Handler(), rptEventsQuery()))
		require.Equal(t, restored[0].ConversationID, page.Events[0].ConversationID)
		if fields == "" || strings.Contains(fields, "null") {
			require.Nil(t, restored[0].ConversationID)
		} else {
			require.NotNil(t, restored[0].ConversationID)
		}
	}
	restoredBefore := rptEventsRows(t, target)
	rptSnapshotWrite(t, path, snapshot, raw)
	require.Error(t, RestoreSnapshot(context.Background(), target, "RPT", "rpt-validation", path, func(context.Context, *gorm.DB) error { return errors.New("rebuild failed") }))
	require.Equal(t, restoredBefore, rptEventsRows(t, target))
}
func TestRPTStoredEventsSnapshotValidationAndRollback(t *testing.T) {
	rptSnapshotValidationCases(t, newModuleTestDB(t, "RPT"), newModuleTestDB(t, "RPT"))
}

func rptEventsEncodedBoundary(t *testing.T, db *gorm.DB, extra int) {
	stamp := time.Date(2025, 2, 3, 4, 5, 6, 123456000, time.UTC)
	page := repository.StoredReportingEventPage{Events: make([]repository.StoredReportingEvent, 25)}
	for i := range page.Events {
		page.Events[i] = repository.StoredReportingEvent{ID: uint(25 - i), AccountID: 91, Name: "event", EventStartTime: stamp, EventEndTime: stamp.Add(time.Minute), CreatedAt: stamp}
	}
	encoded, err := json.Marshal(page)
	require.NoError(t, err)
	remaining := repository.ReportingEventResponseBytes + extra - len(encoded)
	for i := range page.Events {
		repeat := min(repository.ReportingEventMetadataBytes, remaining/6)
		page.Events[i].MetadataJSON = strings.Repeat("<", repeat)
		remaining -= repeat * 6
		if remaining > 0 && repeat < repository.ReportingEventMetadataBytes {
			tail := min(repository.ReportingEventMetadataBytes-repeat, remaining)
			page.Events[i].MetadataJSON += strings.Repeat("x", tail)
			remaining -= tail
		}
		rptEventsSeed(t, db, page.Events[i].ID, 91, "event", page.Events[i].MetadataJSON, nil)
	}
	require.Zero(t, remaining)
	encoded, err = json.Marshal(page)
	require.NoError(t, err)
	require.Len(t, encoded, repository.ReportingEventResponseBytes+extra)
	w := rptEventsRequest(rptEventsRuntime(t, db).Handler(), rptEventsQuery())
	if extra == 0 {
		require.Equal(t, page, rptEventsPage(t, w))
	} else {
		sysPublicFailure(t, w, 503, "dependency_unavailable", "stored reporting events are unavailable")
	}
}
func TestRPTStoredEventsEncodedBoundary(t *testing.T) {
	for _, extra := range []int{0, 1} {
		t.Run(string(rune('0'+extra)), func(t *testing.T) { rptEventsEncodedBoundary(t, newModuleTestDB(t, "RPT"), extra) })
	}
}

func TestRPTStoredEventsEmbeddedNULBounds(t *testing.T) {
	for _, field := range []string{"name", "metadata_json"} {
		t.Run(field, func(t *testing.T) {
			db := newModuleTestDB(t, "RPT")
			rptEventsSeed(t, db, 1, 91, "event", "{}", nil)
			require.NoError(t, db.Model(&domain.ReportingEvent{}).Where("id = ?", 1).Update(field, "private-marker\x00"+strings.Repeat("x", 17000)).Error)
			sysPublicFailure(t, rptEventsRequest(rptEventsRuntime(t, db).Handler(), rptEventsQuery()), 503, "dependency_unavailable", "stored reporting events are unavailable")
		})
	}
}
