package repository

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestRPTReportingEventReadMissingStorage(t *testing.T) {
	for label, repo := range map[string]*ReportingEventReadRepository{
		"nil_receiver": nil,
		"nil_database": NewReportingEventReadRepository(nil),
		"nil_config":   NewReportingEventReadRepository(&gorm.DB{}),
		"nil_dialect":  NewReportingEventReadRepository(&gorm.DB{Config: &gorm.Config{}}),
	} {
		t.Run(label, func(t *testing.T) {
			page, err := repo.ListStored(context.Background(), 91, nil, 0, 0)
			require.EqualError(t, err, "stored reporting events are unavailable")
			require.Zero(t, page)
			rows, err := repo.SnapshotRows(context.Background())
			require.EqualError(t, err, "stored reporting event snapshot is unavailable")
			require.Nil(t, rows)
		})
	}
}

func TestRPTReportingEventReadCancellation(t *testing.T) {
	for _, operation := range []string{"page", "snapshot"} {
		for _, phase := range []string{"nil_context", "before_query", "after_query"} {
			t.Run(operation+"_"+phase, func(t *testing.T) {
				db := openMFATestDB(t, filepath.Join(t.TempDir(), "rpt-context.db"))
				require.NoError(t, db.AutoMigrate(&domain.ReportingEvent{}))
				stamp := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
				require.NoError(t, db.Model(&domain.ReportingEvent{}).Create(map[string]any{"id": 1, "account_id": 91, "name": "event", "value": 0, "event_start_time": stamp, "event_end_time": stamp, "created_at": stamp, "metadata_json": "{}"}).Error)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				queried := false
				require.NoError(t, db.Callback().Row().After("gorm:row").Register("rpt_cancel_after_query", func(tx *gorm.DB) {
					queried = true
					if phase == "after_query" {
						cancel()
					}
				}))
				if phase == "before_query" {
					cancel()
				}
				if phase == "nil_context" {
					ctx = nil
				}
				repo := NewReportingEventReadRepository(db)
				if operation == "page" {
					page, err := repo.ListStored(ctx, 91, nil, 0, 0)
					require.EqualError(t, err, "stored reporting events are unavailable")
					require.Zero(t, page)
				} else {
					rows, err := repo.SnapshotRows(ctx)
					require.EqualError(t, err, "stored reporting event snapshot is unavailable")
					require.Nil(t, rows)
				}
				require.Equal(t, phase == "after_query", queried)
			})
		}
	}
}
