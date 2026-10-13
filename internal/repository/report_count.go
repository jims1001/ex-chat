package repository

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const reportCountMaxBuckets = 400

var errReportCountUnavailable = errors.New("report counts are unavailable")

// ErrReportCountStorageRepresentation means SQLite contains timestamps whose
// textual ordering cannot safely establish the requested UTC aggregate.
var ErrReportCountStorageRepresentation = errors.New("report timestamp storage representation is unsupported")

// ErrReportCountQueryRepresentation distinguishes unsupported SQLite SQL-bound
// text from unsupported stored data. It is checked on the actual clipped bounds.
var ErrReportCountQueryRepresentation = errors.New("report timestamp query representation is unsupported")

func reportCountFailure(ctx context.Context) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return errReportCountUnavailable
}

// countReportBuckets uses a single statement snapshot in the selected owner's
// database. The two callers supply fixed table names; request data is bound.
func countReportBuckets(ctx context.Context, db *gorm.DB, accountID uint, table, messageType string, grouping domain.ReportCountGrouping, buckets []domain.ReportCountBucket) ([]domain.ReportCountPoint, error) {
	if ctx == nil || ctx.Err() != nil || db == nil || db.Config == nil || db.Dialector == nil || db.Statement == nil || accountID == 0 || uint64(accountID) > math.MaxInt64 {
		return nil, reportCountFailure(ctx)
	}
	if table != "conversations" && table != "messages" {
		return nil, errReportCountUnavailable
	}
	if (table == "conversations" && messageType != "") || (table == "messages" && messageType != domain.MessageTypeIncoming && messageType != domain.MessageTypeOutgoing) {
		return nil, errReportCountUnavailable
	}
	dialect := db.Dialector.Name()
	if dialect != "sqlite" && dialect != "postgres" {
		return nil, errReportCountUnavailable
	}
	if len(buckets) > reportCountMaxBuckets {
		return nil, errReportCountUnavailable
	}
	switch grouping {
	case domain.ReportCountHour, domain.ReportCountDay, domain.ReportCountMonth, domain.ReportCountYear:
	default:
		return nil, errReportCountUnavailable
	}
	for i, bucket := range buckets {
		label := time.Unix(bucket.Timestamp, 0).UTC()
		natural, next, valid := domain.ReportCountCalendarBounds(label, grouping, bucket.OffsetSeconds)
		if !valid || !domain.ReportCountOffsetInstantValid(bucket.Since, bucket.OffsetSeconds) || !domain.ReportCountOffsetInstantValid(bucket.Until, bucket.OffsetSeconds) || !natural.Equal(label) || !bucket.Since.Before(bucket.Until) || bucket.Since.Before(label) || bucket.Until.After(next) || (i > 0 && (bucket.OffsetSeconds != buckets[0].OffsetSeconds || bucket.Timestamp <= buckets[i-1].Timestamp || bucket.Since.Before(buckets[i-1].Until))) {
			return nil, errReportCountUnavailable
		}
		if dialect == "sqlite" {
			_, sinceOffset := bucket.Since.Zone()
			_, untilOffset := bucket.Until.Zone()
			// Validate actual clipped SQL bounds, never the nominal next bucket start.
			// The latter may legitimately be year 10000 for a partial final-year bucket.
			if sinceOffset != 0 || untilOffset != 0 || bucket.Since.Year() < 1 || bucket.Since.Year() > 9999 || bucket.Until.Year() < 1 || bucket.Until.Year() > 9999 {
				return nil, ErrReportCountQueryRepresentation
			}
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	points := make([]domain.ReportCountPoint, 0, len(buckets))
	if len(buckets) == 0 {
		return points, nil
	}

	var sql strings.Builder
	sql.WriteString("WITH report_count_buckets(bucket_index, since_at, until_at) AS (VALUES ")
	params := make([]any, 0, 2*len(buckets)+2)
	for i, bucket := range buckets {
		if i > 0 {
			sql.WriteByte(',')
		}
		sql.WriteByte('(')
		// A generated index needs no binding, keeping 400 buckets below SQLite's
		// 999-variable limit: 800 bounds, one account and at most one type.
		sql.WriteString(strconv.Itoa(i))
		if dialect == "postgres" {
			sql.WriteString(", CAST(? AS timestamptz), CAST(? AS timestamptz))")
		} else {
			sql.WriteString(", ?, ?)")
		}
		// PostgreSQL rounds each bound upward to its stored microsecond grid.
		// The SQLite-only guard below requires canonical UTC representations;
		// existing phase43 owner reads and their time adapter remain unchanged.
		params = append(params, ReportTimeBoundary(db, bucket.Since), ReportTimeBoundary(db, bucket.Until))
	}
	params = append(params, accountID)
	if table == "messages" {
		params = append(params, messageType)
	}
	if dialect == "sqlite" {
		// Inspect the entire selected account/type, including rows outside the
		// interval: a noncanonical timestamp could otherwise hide from the
		// lexical bounds. The same snapshot produces either counts or a single
		// sentinel; no stored timestamp or body is transferred to the caller.
		sql.WriteString("), report_count_owner_rows AS (SELECT created_at FROM ")
		sql.WriteString(table)
		sql.WriteString(" WHERE account_id = ?")
		if table == "messages" {
			sql.WriteString(" AND message_type = ?")
		}
		sql.WriteString("), report_count_validity AS (SELECT CASE WHEN EXISTS (SELECT 1 FROM report_count_owner_rows WHERE NOT COALESCE((")
		sql.WriteString(reportCountSQLiteCanonicalUTC)
		sql.WriteString("), 0)) THEN 0 ELSE 1 END AS valid) SELECT b.bucket_index AS bucket_index, COUNT(*) AS value FROM report_count_buckets AS b JOIN report_count_owner_rows AS owned ON owned.created_at >= b.since_at AND owned.created_at < b.until_at WHERE (SELECT valid FROM report_count_validity) = 1 GROUP BY b.bucket_index UNION ALL SELECT -1, 0 WHERE (SELECT valid FROM report_count_validity) = 0 ORDER BY bucket_index ASC")
	} else {
		sql.WriteString(") SELECT b.bucket_index, COUNT(*) FROM report_count_buckets AS b JOIN ")
		sql.WriteString(table)
		sql.WriteString(" AS owned ON owned.created_at >= b.since_at AND owned.created_at < b.until_at WHERE owned.account_id = ?")
		if table == "messages" {
			sql.WriteString(" AND owned.message_type = ?")
		}
		sql.WriteString(" GROUP BY b.bucket_index ORDER BY b.bucket_index ASC")
	}
	query := db.WithContext(ctx).Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	rows, err := query.Raw(sql.String(), params...).Rows()
	if err != nil {
		return nil, reportCountFailure(ctx)
	}
	defer rows.Close()
	previous := -1
	for rows.Next() {
		var index int
		var count int64
		if ctx.Err() != nil || rows.Scan(&index, &count) != nil {
			return nil, reportCountFailure(ctx)
		}
		if dialect == "sqlite" && index == -1 && count == 0 {
			return nil, ErrReportCountStorageRepresentation
		}
		if index <= previous || index < 0 || index >= len(buckets) || count <= 0 {
			return nil, reportCountFailure(ctx)
		}
		points = append(points, domain.ReportCountPoint{Value: count, Timestamp: buckets[index].Timestamp})
		previous = index
	}
	if rows.Err() != nil || rows.Close() != nil || ctx.Err() != nil {
		return nil, reportCountFailure(ctx)
	}
	return points, nil
}

// The installed SQLite drivers write this exact UTC text form. Validate only
// its bytes and calendar fields; never interpret, normalize or migrate stored
// timestamps with SQLite's precision-losing date functions. Length equality
// also rejects embedded NUL bytes and non-ASCII encodings. Fractional seconds
// have 1..9 digits and no trailing zero, as emitted by Go's time formatter.
const reportCountSQLiteCanonicalUTC = `typeof(created_at) = 'text'
 AND length(CAST(created_at AS BLOB)) BETWEEN 25 AND 35
 AND length(created_at) = length(CAST(created_at AS BLOB))
 AND substr(created_at, -6) = '+00:00'
 AND substr(created_at, 1, 19) GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9] [0-9][0-9]:[0-9][0-9]:[0-9][0-9]'
 AND substr(created_at, 1, 4) BETWEEN '0001' AND '9999'
 AND substr(created_at, 6, 2) BETWEEN '01' AND '12'
 AND CAST(substr(created_at, 9, 2) AS INTEGER) BETWEEN 1 AND CASE
   WHEN substr(created_at, 6, 2) = '02' THEN CASE
     WHEN CAST(substr(created_at, 1, 4) AS INTEGER) % 400 = 0
       OR (CAST(substr(created_at, 1, 4) AS INTEGER) % 4 = 0 AND CAST(substr(created_at, 1, 4) AS INTEGER) % 100 <> 0)
     THEN 29 ELSE 28 END
   WHEN substr(created_at, 6, 2) IN ('04', '06', '09', '11') THEN 30
   ELSE 31 END
 AND substr(created_at, 12, 2) BETWEEN '00' AND '23'
 AND substr(created_at, 15, 2) BETWEEN '00' AND '59'
 AND substr(created_at, 18, 2) BETWEEN '00' AND '59'
 AND (length(created_at) = 25 OR (length(created_at) BETWEEN 27 AND 35
   AND substr(created_at, 20, 1) = '.'
   AND substr(created_at, 21, length(created_at) - 26) NOT GLOB '*[^0-9]*'
   AND substr(created_at, -7, 1) GLOB '[1-9]'))`
