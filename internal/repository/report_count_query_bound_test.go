package repository

import (
	"context"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// Reproduces the real owner query with the exclusive bound emitted by the
// shared date-only HTTP parser for until=9999-12-31, not a comparison toy.
func TestReportCountSQLiteQueryYear10000Regression(t *testing.T) {
	for _, owner := range reportCountTestOwners {
		t.Run(owner.name, func(t *testing.T) {
			db := reportCountTestSQLite(t, owner.name)
			reportCountTestTable(t, db, owner)
			day := time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)
			reportCountTestInsert(t, db, owner, 1, 91, day.Add(12*time.Hour), owner.messageType)
			got, err := owner.count(db, context.Background(), 91, []domain.ReportCountBucket{reportCountTestBucket(day, day.AddDate(0, 0, 1))})
			t.Logf("actual owner result with canonical stored year9999 and exclusive query year10000: points=%v error=%v; canonical row must never silently disappear", got, err)
			require.EqualError(t, err, "report timestamp query representation is unsupported")
			require.Nil(t, got)
		})
	}
}
