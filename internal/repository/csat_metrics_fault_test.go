package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Use database/sql rows so AVG Row/Scan and partial streaming failures exercise
// the production scan machinery, not a substituted repository implementation.
type csatMetricReply struct {
	name    string
	columns []string
	values  [][]driver.Value
}

type csatMetricFaultRows struct {
	reply   csatMetricReply
	index   int
	nextErr error
}

func (r *csatMetricFaultRows) Columns() []string { return r.reply.columns }
func (r *csatMetricFaultRows) Close() error      { return nil }
func (r *csatMetricFaultRows) Next(dest []driver.Value) error {
	if r.index < len(r.reply.values) {
		copy(dest, r.reply.values[r.index])
		r.index++
		return nil
	}
	if r.nextErr != nil {
		return r.nextErr
	}
	return io.EOF
}

type csatMetricFaultConn struct {
	replies []csatMetricReply
	failAt  int
	mode    string
	err     error
	queries []string
}

func (c *csatMetricFaultConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected CSAT prepare")
}
func (c *csatMetricFaultConn) Close() error { return nil }
func (c *csatMetricFaultConn) Begin() (driver.Tx, error) {
	return nil, errors.New("unexpected CSAT transaction")
}
func (c *csatMetricFaultConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.queries = append(c.queries, query)
	step := len(c.queries)
	if step > len(c.replies) {
		return nil, errors.New("unexpected additional CSAT read")
	}
	rows := &csatMetricFaultRows{reply: c.replies[step-1]}
	if step == c.failAt {
		switch c.mode {
		case "query_error":
			return nil, c.err
		case "row_scan_error":
			rows.reply.values = nil
			rows.nextErr = c.err
		case "partial_rows_error":
			rows.nextErr = c.err
		case "conversion_error":
			rows.reply.values = [][]driver.Value{{"private unscannable average"}}
		}
	}
	return rows, nil
}

type csatMetricFaultConnector struct{ conn *csatMetricFaultConn }

func (c csatMetricFaultConnector) Connect(context.Context) (driver.Conn, error) { return c.conn, nil }
func (c csatMetricFaultConnector) Driver() driver.Driver                        { return csatMetricFaultDriver{c.conn} }

type csatMetricFaultDriver struct{ conn *csatMetricFaultConn }

func (d csatMetricFaultDriver) Open(string) (driver.Conn, error) { return d.conn, nil }

type csatMetricUsers struct {
	calls  []uint
	failAt int
	err    error
}

func (u *csatMetricUsers) FindByID(id uint) (*domain.User, error) {
	u.calls = append(u.calls, id)
	user := &domain.User{ID: id, Name: fmt.Sprintf("Agent %d", id)}
	if len(u.calls) == u.failAt {
		return user, u.err
	}
	return user, nil
}

func csatMetricReplies(advanced bool) []csatMetricReply {
	count := func(name string, n int64) csatMetricReply {
		return csatMetricReply{name, []string{"count"}, [][]driver.Value{{n}}}
	}
	avg := csatMetricReply{"average", []string{"AVG(rating)"}, [][]driver.Value{{float64(11) / 3}}}
	dist := csatMetricReply{"distribution", []string{"rating", "count"}, [][]driver.Value{{int64(2), int64(1)}, {int64(4), int64(1)}, {int64(5), int64(1)}}}
	if !advanced {
		return []csatMetricReply{count("total", 3), avg, dist}
	}
	return []csatMetricReply{
		count("total", 3), count("approved", 1), count("pending", 1), count("rejected", 1), avg, count("positive", 2), dist,
		{"agents", []string{"assigned_agent_id", "total", "approved", "avg_rating", "pos_count"}, [][]driver.Value{{int64(7), int64(2), int64(1), float64(3.5), int64(1)}, {int64(8), int64(1), int64(0), float64(4), int64(1)}}},
	}
}

func csatMetricFaultFixture(t *testing.T, advanced bool) (*csatMetricFaultConn, *csatMetricUsers, func() (any, error)) {
	t.Helper()
	conn := &csatMetricFaultConn{replies: csatMetricReplies(advanced)}
	pool := sql.OpenDB(csatMetricFaultConnector{conn})
	pool.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })
	db := reportCountTestSQLite(t, "csat_fault").WithContext(context.Background())
	db.Statement.ConnPool = pool
	users := &csatMetricUsers{}
	if advanced {
		repo := NewCSATExtensionRepositoryWithOwners(db, nil, nil, nil, users)
		return conn, users, func() (any, error) { return repo.GetAdvancedMetrics(91, nil, nil, nil, nil) }
	}
	repo := NewCSATRepository(db)
	return conn, users, func() (any, error) { return repo.GetMetrics(context.Background(), 91) }
}

func TestCSATMetricsEverySQLReadFailsClosed(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		name := "legacy"
		if advanced {
			name = "advanced"
		}
		for index, reply := range csatMetricReplies(advanced) {
			modes := []string{"query_error"}
			if reply.name == "average" {
				modes = append(modes, "row_scan_error", "conversion_error")
			}
			if reply.name == "distribution" || reply.name == "agents" {
				modes = append(modes, "partial_rows_error")
			}
			for _, mode := range modes {
				t.Run(name+"/"+reply.name+"/"+mode, func(t *testing.T) {
					conn, users, call := csatMetricFaultFixture(t, advanced)
					conn.failAt, conn.mode, conn.err = index+1, mode, errors.New("private QLT driver sentinel")
					got, err := call()
					if mode == "conversion_error" {
						require.ErrorContains(t, err, "private unscannable average")
					} else {
						require.Same(t, conn.err, err, "return the original required-read error")
					}
					require.Nil(t, got, "discard all partial metrics")
					require.Len(t, conn.queries, index+1, "stop before any later SQL read")
					require.Empty(t, users.calls, "do not hydrate partial agent aggregates")
					conn.failAt, conn.queries = 0, nil
					got, err = call()
					require.NoError(t, err)
					require.NotNil(t, got)
					require.Len(t, conn.queries, len(conn.replies), "same repository recovers without stale error state")
					if advanced {
						metrics := got.(*CSATMetrics)
						require.Equal(t, float64(3.67), metrics.AverageRating)
						require.Equal(t, float64(66.67), metrics.SatisfactionRate)
						require.Equal(t, []uint{7, 8}, users.calls)
					} else {
						require.Equal(t, float64(11)/3, got.(map[string]any)["average_rating"])
					}
					for i, query := range conn.queries {
						require.Contains(t, strings.ToLower(query), "csat_surveys")
						require.Contains(t, strings.ToLower(query), "account_id")
						switch conn.replies[i].name {
						case "average":
							require.Contains(t, query, "AVG(rating)")
						case "approved", "pending", "rejected":
							require.Contains(t, query, "review_status =")
						case "positive":
							require.Contains(t, query, "rating >= 4")
						case "distribution":
							require.Contains(t, query, "GROUP BY")
						case "agents":
							require.Contains(t, query, "assigned_agent_id IS NOT NULL")
						}
					}
				})
			}
		}
	}
}

func TestCSATMetricsAverageCallbackErrors(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		t.Run(fmt.Sprintf("advanced_%t", advanced), func(t *testing.T) {
			db := reportCountTestSQLite(t, "csat_callback")
			createCSATMetricTable(t, db)
			seedCSATMetricRows(t, db)
			ownerErr := errors.New("QLT average callback rejected")
			calls := 0
			require.NoError(t, db.Callback().Row().Before("gorm:row").Register("csat_avg_rejected", func(tx *gorm.DB) {
				if len(tx.Statement.Selects) == 1 && tx.Statement.Selects[0] == "AVG(rating)" {
					calls++
					_ = tx.AddError(ownerErr)
				}
			}))
			var got any
			var err error
			if advanced {
				got, err = NewCSATExtensionRepositoryWithOwners(db, nil, nil, nil, nil).GetAdvancedMetrics(91, nil, nil, nil, nil)
			} else {
				got, err = NewCSATRepository(db).GetMetrics(context.Background(), 91)
			}
			require.Same(t, ownerErr, err)
			require.Nil(t, got)
			require.Equal(t, 1, calls)
		})
	}
}
