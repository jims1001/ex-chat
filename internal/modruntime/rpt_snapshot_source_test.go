package modruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func rptSnapshotSourceScalarCases(t *testing.T, makeDB func(*testing.T) *gorm.DB) {
	for _, field := range []string{"value", "metadata_json", "created_at", "event_start_time", "event_end_time"} {
		t.Run(field, func(t *testing.T) {
			db := makeDB(t)
			rptEventsSeed(t, db, 1, 91, "event", "{}", nil)
			require.NoError(t, db.Model(&domain.ReportingEvent{}).Where("id = ?", 1).Update(field, nil).Error)
			require.Error(t, CreateSnapshot(db, "RPT", "rpt-corrupt-source", filepath.Join(t.TempDir(), "source.json")), "a NULL nonnullable scalar must not normalize to zero/empty in a snapshot")
		})
	}
}
func TestRPTStoredEventsSnapshotNumericUnderflow(t *testing.T) {
	db := newModuleTestDB(t, "RPT")
	raw := json.RawMessage(`[{"id":1,"account_id":91,"name":"event","value":1e-400,"event_start_time":"0001-01-01T00:00:00Z","event_end_time":"0001-01-01T00:00:00Z","metadata_json":"{}","created_at":"0001-01-01T00:00:00Z"}]`)
	err := db.WithContext(context.Background()).Transaction(func(tx *gorm.DB) error {
		_, err := restoreRPTReportingRows(tx, &domain.ReportingEvent{}, raw)
		return err
	})
	require.Error(t, err, "nonzero source numeric must not silently become zero")
}

func TestRPTStoredEventsSnapshotSourceScalars(t *testing.T) {
	rptSnapshotSourceScalarCases(t, func(t *testing.T) *gorm.DB { return newModuleTestDB(t, "RPT") })
}

func rptSnapshotCompleteMetadataCase(t *testing.T, source, target *gorm.DB) {
	rptEventsSeed(t, source, 1, 91, "", strings.Repeat("<", 20*1024)+" opaque {", nil)
	path := filepath.Join(t.TempDir(), "complete.json")
	before := rptEventsRows(t, source)
	require.NoError(t, CreateSnapshot(source, "RPT", "rpt-complete", path))
	require.NoError(t, RestoreSnapshot(context.Background(), target, "RPT", "rpt-complete", path, nil))
	require.Equal(t, before, rptEventsRows(t, target))
	// Complete private snapshots deliberately accept metadata above query bounds.
	sysPublicFailure(t, rptEventsRequest(rptEventsRuntime(t, target).Handler(), rptEventsQuery()), 503, "dependency_unavailable", "stored reporting events are unavailable")
}
func TestRPTStoredEventsSnapshotCompleteMetadata(t *testing.T) {
	rptSnapshotCompleteMetadataCase(t, newModuleTestDB(t, "RPT"), newModuleTestDB(t, "RPT"))
}

func TestRPTStoredEventsSnapshotRejectsMalformedSource(t *testing.T) {
	for label, statement := range map[string]string{"time_text": "UPDATE reporting_events SET created_at='private-marker'", "time_blob": "UPDATE reporting_events SET created_at=x'323032352d30322d3033'", "value_blob": "UPDATE reporting_events SET value=x'31'", "name_blob": "UPDATE reporting_events SET name=x'616263'", "metadata_invalid_utf8": "UPDATE reporting_events SET metadata_json=CAST(x'ff' AS TEXT)"} {
		t.Run(label, func(t *testing.T) {
			db := newModuleTestDB(t, "RPT")
			rptEventsSeed(t, db, 1, 91, "event", "{}", nil)
			require.NoError(t, db.Exec(statement).Error)
			path := filepath.Join(t.TempDir(), "unchanged.json")
			require.NoError(t, os.WriteFile(path, []byte("previous complete snapshot"), 0600))
			var output bytes.Buffer
			db = db.Session(&gorm.Session{Logger: logger.New(log.New(&output, "", 0), logger.Config{LogLevel: logger.Info})})
			err := CreateSnapshot(db, "RPT", "rpt-invalid", path)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "private-marker")
			require.NotContains(t, output.String(), "private-marker")
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, "previous complete snapshot", string(data))
		})
	}
}

func TestRPTStoredEventsSnapshotNanoseconds(t *testing.T) {
	source, target := newModuleTestDB(t, "RPT"), newModuleTestDB(t, "RPT")
	rptEventsSeed(t, source, 1, 91, "event", "{}", nil)
	stamp := time.Date(2025, 1, 2, 3, 4, 5, 987654321, time.UTC)
	require.NoError(t, source.Model(&domain.ReportingEvent{}).Where("id = ?", 1).Updates(map[string]any{"created_at": stamp, "event_start_time": stamp, "event_end_time": stamp}).Error)
	before := rptEventsRows(t, source)
	path := filepath.Join(t.TempDir(), "nanos.json")
	require.NoError(t, CreateSnapshot(source, "RPT", "rpt-nanos", path))
	require.NoError(t, RestoreSnapshot(context.Background(), target, "RPT", "rpt-nanos", path, nil))
	require.Equal(t, before, rptEventsRows(t, target))
}
