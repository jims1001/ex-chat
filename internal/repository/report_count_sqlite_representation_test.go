package repository

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func reportCountTestRawTimestamp(t *testing.T, db *gorm.DB, owner reportCountTestOwner, id, account uint, value any, messageType string) {
	t.Helper()
	row := map[string]any{"id": id, "account_id": account, "created_at": value}
	if owner.table == "messages" {
		row["message_type"] = messageType
	}
	require.NoError(t, db.Table(owner.table).Create(row).Error)
}

func TestReportCountDailySQLiteCanonicalRepresentation(t *testing.T) {
	for _, owner := range reportCountTestOwners {
		t.Run(owner.name, func(t *testing.T) {
			db := reportCountTestSQLite(t, owner.name)
			reportCountTestTable(t, db, owner)
			first := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
			values := []string{
				"2024-02-29 00:00:00+00:00",
				"2024-02-29 00:00:00.1+00:00",
				"2024-02-29 00:00:00.01+00:00",
				"2024-02-29 00:00:00.123456+00:00",
				"2024-02-29 23:59:59.999999999+00:00",
			}
			for i, value := range values {
				reportCountTestRawTimestamp(t, db, owner, uint(i+1), 91, value, owner.messageType)
			}
			// Valid historical calendar boundaries outside the range must pass
			// the inventory guard, including the Gregorian century rule.
			for i, value := range []string{"0001-01-01 00:00:00+00:00", "1900-02-28 23:59:59+00:00", "2000-02-29 00:00:00+00:00", "9999-12-31 23:59:59+00:00"} {
				reportCountTestRawTimestamp(t, db, owner, uint(101+i), 91, value, owner.messageType)
			}
			bucket := reportCountTestBucket(first, first.AddDate(0, 0, 1))
			got, err := owner.count(db, context.Background(), 91, []domain.ReportCountBucket{bucket})
			require.NoError(t, err)
			require.Equal(t, []domain.ReportCountPoint{{Value: int64(len(values)), Timestamp: bucket.Timestamp}}, got)
		})
	}
}

func TestReportCountDailySQLiteRejectsUnsupportedRepresentation(t *testing.T) {
	invalid := []struct {
		name  string
		value any
	}{
		{"null", nil}, {"integer", int64(1709164800)}, {"real", 1709164800.5}, {"blob", []byte("2024-02-29 00:00:00+00:00")},
		{"empty", ""}, {"malformed", "private malformed timestamp"},
		{"rfc3339_z", "2024-02-29T00:00:00Z"}, {"rfc3339_utc", "2024-02-29T00:00:00+00:00"},
		{"no_offset", "2024-02-29 00:00:00"}, {"negative_zero_offset", "2024-02-29 00:00:00-00:00"},
		{"positive_offset", "2024-02-29 12:00:00+09:00"}, {"negative_offset", "2024-02-29 12:00:00-03:30"},
		{"year_zero", "0000-01-01 00:00:00+00:00"}, {"five_digit_year", "10000-01-01 00:00:00+00:00"},
		{"month_zero", "2024-00-01 00:00:00+00:00"}, {"month_13", "2024-13-01 00:00:00+00:00"},
		{"day_zero", "2024-02-00 00:00:00+00:00"}, {"day_32", "2024-01-32 00:00:00+00:00"},
		{"april_31", "2024-04-31 00:00:00+00:00"}, {"february_30", "2024-02-30 00:00:00+00:00"},
		{"non_leap_day", "2023-02-29 00:00:00+00:00"}, {"non_leap_century", "1900-02-29 00:00:00+00:00"},
		{"hour_24", "2024-02-29 24:00:00+00:00"}, {"minute_60", "2024-02-29 00:60:00+00:00"},
		{"second_60", "2024-02-29 00:00:60+00:00"}, {"fraction_empty", "2024-02-29 00:00:00.+00:00"},
		{"fraction_zero", "2024-02-29 00:00:00.0+00:00"}, {"fraction_trailing_zero", "2024-02-29 00:00:00.10+00:00"},
		{"fraction_non_digit", "2024-02-29 00:00:00.a1+00:00"}, {"fraction_ten_digits", "2024-02-29 00:00:00.1234567891+00:00"},
		{"trailing_space", "2024-02-29 00:00:00+00:00 "}, {"trailing_nul", "2024-02-29 00:00:00+00:00\x00"},
	}
	for _, owner := range reportCountTestOwners {
		t.Run(owner.name, func(t *testing.T) {
			for _, tc := range invalid {
				t.Run(tc.name, func(t *testing.T) {
					db := reportCountTestSQLite(t, owner.name)
					reportCountTestTable(t, db, owner)
					// The valid count must never be returned partially. Almost all
					// invalid inventory rows are lexically outside this interval.
					day := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
					reportCountTestInsert(t, db, owner, 1, 91, day, owner.messageType)
					reportCountTestRawTimestamp(t, db, owner, 2, 91, tc.value, owner.messageType)
					queries := 0
					require.NoError(t, db.Callback().Row().After("gorm:row").Register("count_representation_query", func(tx *gorm.DB) { queries++ }))
					got, err := owner.count(db, context.Background(), 91, []domain.ReportCountBucket{reportCountTestBucket(day, day.AddDate(0, 0, 1))})
					require.ErrorIs(t, err, ErrReportCountStorageRepresentation)
					require.Nil(t, got)
					require.Equal(t, 1, queries)
				})
			}
		})
	}
}

func TestReportCountDailySQLiteRepresentationGuardScope(t *testing.T) {
	for _, owner := range reportCountTestOwners {
		t.Run(owner.name, func(t *testing.T) {
			db := reportCountTestSQLite(t, owner.name)
			reportCountTestTable(t, db, owner)
			day := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
			reportCountTestInsert(t, db, owner, 1, 91, day, owner.messageType)
			reportCountTestRawTimestamp(t, db, owner, 2, 92, "unsupported foreign timestamp", owner.messageType)
			if owner.table == "messages" {
				other := domain.MessageTypeIncoming
				if other == owner.messageType {
					other = domain.MessageTypeOutgoing
				}
				reportCountTestRawTimestamp(t, db, owner, 3, 91, "unsupported other type timestamp", other)
				reportCountTestRawTimestamp(t, db, owner, 4, 91, nil, "activity")
			}
			bucket := reportCountTestBucket(day, day.AddDate(0, 0, 1))
			got, err := owner.count(db, context.Background(), 91, []domain.ReportCountBucket{bucket})
			require.NoError(t, err)
			require.Equal(t, []domain.ReportCountPoint{{Value: 1, Timestamp: bucket.Timestamp}}, got)
			// A same-owner timestamp with an offset now blocks the otherwise
			// valid UTC result, even when the interval excludes its text value.
			reportCountTestRawTimestamp(t, db, owner, 5, 91, "1969-12-31 20:00:00-04:00", owner.messageType)
			got, err = owner.count(db, context.Background(), 91, []domain.ReportCountBucket{bucket})
			require.ErrorIs(t, err, ErrReportCountStorageRepresentation)
			require.Nil(t, got)
		})
	}
}

func TestReportCountDailySQLiteRejectsNonUTCBounds(t *testing.T) {
	for _, owner := range reportCountTestOwners {
		for _, offset := range []int{9 * 60 * 60, -3*60*60 - 30*60} {
			t.Run(owner.name+"/"+strconv.Itoa(offset), func(t *testing.T) {
				db := reportCountTestSQLite(t, owner.name)
				queries := 0
				require.NoError(t, db.Callback().Row().After("gorm:row").Register("count_non_utc_no_query", func(tx *gorm.DB) { queries++ }))
				since := time.Date(2024, 2, 29, 12, 0, 0, 123456789, time.UTC)
				until := since.Add(time.Hour)
				location := time.FixedZone("non_utc", offset)
				for _, bucket := range []domain.ReportCountBucket{reportCountTestBucket(since.In(location), until), reportCountTestBucket(since, until.In(location))} {
					got, err := owner.count(db, context.Background(), 91, []domain.ReportCountBucket{bucket})
					require.ErrorIs(t, err, ErrReportCountQueryRepresentation)
					require.Nil(t, got)
				}
				require.Zero(t, queries)
			})
		}
	}
}
