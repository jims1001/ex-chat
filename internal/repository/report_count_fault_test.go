package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
)

// Fault rows exercise errors after a valid aggregate has already been scanned.
// The production query still goes through database/sql's real Rows machinery.
type reportCountFaultRows struct {
	values   [][]driver.Value
	index    int
	nextErr  error
	closeErr error
	onClose  func()
}

func (rows *reportCountFaultRows) Columns() []string { return []string{"bucket_index", "count"} }
func (rows *reportCountFaultRows) Close() error {
	if rows.onClose != nil {
		rows.onClose()
	}
	return rows.closeErr
}
func (rows *reportCountFaultRows) Next(dest []driver.Value) error {
	if rows.index < len(rows.values) {
		copy(dest, rows.values[rows.index])
		rows.index++
		return nil
	}
	if rows.nextErr != nil {
		return rows.nextErr
	}
	return io.EOF
}

type reportCountFaultConn struct{ rows *reportCountFaultRows }

func (conn *reportCountFaultConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepare")
}
func (conn *reportCountFaultConn) Close() error { return nil }
func (conn *reportCountFaultConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected transaction")
}
func (conn *reportCountFaultConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return conn.rows, nil
}

type reportCountFaultConnector struct{ conn *reportCountFaultConn }

func (connector reportCountFaultConnector) Connect(context.Context) (driver.Conn, error) {
	return connector.conn, nil
}
func (connector reportCountFaultConnector) Driver() driver.Driver {
	return reportCountFaultDriver{conn: connector.conn}
}

type reportCountFaultDriver struct{ conn *reportCountFaultConn }

func (d reportCountFaultDriver) Open(string) (driver.Conn, error) { return d.conn, nil }

func TestReportCountDailyDiscardsPartialResults(t *testing.T) {
	for _, owner := range reportCountTestOwners {
		for _, mode := range []string{"stream_error", "scan_error", "close_error", "duplicate_index", "outside_index", "zero_count", "canceled_at_close"} {
			t.Run(owner.name+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				rows := &reportCountFaultRows{values: [][]driver.Value{{int64(0), int64(1)}, {int64(1), int64(1)}}}
				privateError := errors.New("private driver detail SELECT body FROM private_table")
				switch mode {
				case "stream_error":
					rows.nextErr = privateError
				case "scan_error":
					rows.values[1][1] = "private unscannable value"
				case "close_error":
					rows.closeErr = privateError
				case "duplicate_index":
					rows.values[1][0] = int64(0)
				case "outside_index":
					rows.values[1][0] = int64(2)
				case "zero_count":
					rows.values[1][1] = int64(0)
				case "canceled_at_close":
					rows.onClose = cancel
				}
				pool := sql.OpenDB(reportCountFaultConnector{conn: &reportCountFaultConn{rows: rows}})
				t.Cleanup(func() { require.NoError(t, pool.Close()) })
				// Clone the real dialect/callback setup, then replace only this
				// query session's connection with a faulting database/sql pool.
				db := reportCountTestSQLite(t, owner.name).WithContext(ctx)
				db.Statement.ConnPool = pool
				first := time.Date(2024, 2, 28, 0, 0, 0, 0, time.UTC)
				buckets := []domain.ReportCountBucket{
					reportCountTestBucket(first, first.AddDate(0, 0, 1)),
					reportCountTestBucket(first.AddDate(0, 0, 1), first.AddDate(0, 0, 2)),
				}
				got, err := owner.count(db, ctx, 91, buckets)
				if mode == "canceled_at_close" {
					require.ErrorIs(t, err, context.Canceled)
				} else {
					require.EqualError(t, err, "report counts are unavailable")
				}
				require.Nil(t, got)
			})
		}
	}
}
