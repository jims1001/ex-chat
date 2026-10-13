package repository

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func createCSATMetricTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	stamp := "datetime"
	if db.Dialector.Name() == "postgres" {
		stamp = "timestamptz"
	}
	require.NoError(t, db.Exec("CREATE TABLE csat_surveys (id bigint PRIMARY KEY, account_id bigint, conversation_id bigint, rating integer, review_status text, assigned_agent_id bigint, created_at "+stamp+")").Error)
}

func seedCSATMetricRows(t *testing.T, db *gorm.DB) {
	t.Helper()
	at := time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC)
	for index, rating := range []int{5, 4, 2} {
		agent := uint(7)
		if index == 2 {
			agent = 8
		}
		require.NoError(t, db.Exec("INSERT INTO csat_surveys (id, account_id, conversation_id, rating, review_status, assigned_agent_id, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)", index+1, 91, index+1, rating, []string{"approved", "pending", "rejected"}[index], agent, at.Add(time.Duration(index)*time.Hour)).Error)
	}
}

type csatMetricOwnerFixture struct {
	qlt, con, iam *gorm.DB
	legacy        *CSATRepository
	advanced      *CSATExtensionRepository
}

func newCSATMetricOwnerFixture(t *testing.T, open func(*testing.T, string) *gorm.DB) *csatMetricOwnerFixture {
	t.Helper()
	f := &csatMetricOwnerFixture{qlt: open(t, "QLT"), con: open(t, "CON"), iam: open(t, "IAM")}
	createCSATMetricTable(t, f.qlt)
	seedCSATMetricRows(t, f.qlt)
	require.NoError(t, f.con.Exec("CREATE TABLE conversations (id bigint PRIMARY KEY, account_id bigint, inbox_id bigint)").Error)
	require.NoError(t, f.con.Exec("INSERT INTO conversations (id, account_id, inbox_id) VALUES (1, 91, 10), (2, 91, 11), (3, 91, 10), (9, 92, 10)").Error)
	require.NoError(t, f.iam.Exec("CREATE TABLE users (id bigint PRIMARY KEY, name text)").Error)
	require.NoError(t, f.iam.Exec("INSERT INTO users (id, name) VALUES (7, 'Agent 7'), (8, 'Agent 8')").Error)
	f.legacy = NewCSATRepository(f.qlt)
	f.advanced = NewCSATExtensionRepositoryWithOwners(f.qlt, NewConversationRepository(f.con), nil, nil, NewUserRepository(f.iam))
	return f
}

func csatMetricJSON(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(data, &result))
	return result
}

func TestCSATMetricsDependencyOwners(t *testing.T) {
	runCSATMetricsDependencyOwners(t, reportCountTestSQLite)
}

func TestPostgresCSATMetricsDependencyOwners(t *testing.T) {
	runCSATMetricsDependencyOwners(t, reportCountTestPostgres(t))
}

func runCSATMetricsDependencyOwners(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	t.Run("healthy_scope_rounding_distribution", func(t *testing.T) {
		f := newCSATMetricOwnerFixture(t, open)
		start := time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC)
		end := start.Add(2 * time.Hour)
		for _, row := range []struct {
			id, account uint
			rating      int
			at          time.Time
		}{
			{4, 91, 0, start}, {5, 91, -1, start}, {6, 92, 1, start}, {7, 91, 1, start.Add(-time.Second)}, {8, 91, 5, end.Add(time.Second)}, {9, 91, 6, start.Add(time.Hour)},
		} {
			require.NoError(t, f.qlt.Exec("INSERT INTO csat_surveys (id, account_id, conversation_id, rating, review_status, created_at) VALUES (?, ?, ?, ?, ?, ?)", row.id, row.account, row.id, row.rating, "flagged", row.at).Error)
		}
		legacy, err := f.legacy.GetMetrics(context.Background(), 91)
		require.NoError(t, err)
		require.Equal(t, int64(8), legacy["total_responses"])
		require.Equal(t, float64(2.75), legacy["average_rating"])
		legacyJSON := csatMetricJSON(t, legacy)
		require.ElementsMatch(t, []any{
			map[string]any{"rating": float64(-1), "count": float64(1)}, map[string]any{"rating": float64(0), "count": float64(1)},
			map[string]any{"rating": float64(1), "count": float64(1)}, map[string]any{"rating": float64(2), "count": float64(1)},
			map[string]any{"rating": float64(4), "count": float64(1)}, map[string]any{"rating": float64(5), "count": float64(2)}, map[string]any{"rating": float64(6), "count": float64(1)},
		}, legacyJSON["distribution"])
		all, err := f.advanced.GetAdvancedMetrics(91, nil, nil, nil, nil)
		require.NoError(t, err)
		require.Equal(t, int64(6), all.TotalResponses)
		require.Equal(t, 3.83, all.AverageRating)
		require.Equal(t, 66.67, all.SatisfactionRate)
		require.Equal(t, map[string]int64{"approved": 1, "pending": 1, "rejected": 1}, all.ReviewStatusBreakdown)
		bounded, err := f.advanced.GetAdvancedMetrics(91, nil, nil, &start, &end)
		require.NoError(t, err)
		require.Equal(t, int64(4), bounded.TotalResponses, "both CSAT endpoints remain inclusive")
		require.Equal(t, 4.25, bounded.AverageRating)
		require.Equal(t, float64(75), bounded.SatisfactionRate)
		require.Equal(t, map[int]int64{1: 0, 2: 1, 3: 0, 4: 1, 5: 1, 6: 1}, bounded.RatingBreakdown, "do not invent a new rating policy")
		require.ElementsMatch(t, []CSATAgentMetric{{AgentID: 7, AgentName: "Agent 7", TotalResponses: 2, ApprovedResponses: 1, AverageRating: 4.5, SatisfactionRate: 100}, {AgentID: 8, AgentName: "Agent 8", TotalResponses: 1, AverageRating: 2}}, bounded.AgentMetrics)
		agent, inbox := uint(7), uint(10)
		selected, err := f.advanced.GetAdvancedMetrics(91, &inbox, &agent, &start, &end)
		require.NoError(t, err)
		require.Equal(t, int64(1), selected.TotalResponses)
		require.Equal(t, float64(5), selected.AverageRating)
		selected, err = f.advanced.GetAdvancedMetrics(91, &inbox, nil, &start, &end)
		require.NoError(t, err)
		require.Equal(t, int64(2), selected.TotalResponses, "CON must exclude foreign account conversation 9")
		for _, bounds := range []struct {
			since, until *time.Time
			count        int64
		}{{&start, nil, 5}, {nil, &end, 5}, {&start, &start, 1}, {&end, &start, 0}} {
			got, err := f.advanced.GetAdvancedMetrics(91, nil, nil, bounds.since, bounds.until)
			require.NoError(t, err)
			require.Equal(t, bounds.count, got.TotalResponses, "retain optional, equal and reversed CSAT bounds")
		}
		foreign, err := f.legacy.GetMetrics(context.Background(), 92)
		require.NoError(t, err)
		require.Equal(t, int64(1), foreign["total_responses"])
		require.Equal(t, float64(1), foreign["average_rating"])
	})

	t.Run("healthy_empty_shapes_and_null_average", func(t *testing.T) {
		f := newCSATMetricOwnerFixture(t, open)
		legacy, err := f.legacy.GetMetrics(context.Background(), 999)
		require.NoError(t, err)
		require.Equal(t, map[string]any{"total_responses": float64(0), "average_rating": float64(0), "distribution": nil}, csatMetricJSON(t, legacy))
		advanced, err := f.advanced.GetAdvancedMetrics(999, nil, nil, nil, nil)
		require.NoError(t, err)
		require.Equal(t, &CSATMetrics{RatingBreakdown: map[int]int64{1: 0, 2: 0, 3: 0, 4: 0, 5: 0}, ReviewStatusBreakdown: map[string]int64{"approved": 0, "pending": 0, "rejected": 0}}, advanced)
		require.NotContains(t, csatMetricJSON(t, advanced), "agent_metrics")
		inbox := uint(999)
		advanced, err = f.advanced.GetAdvancedMetrics(91, &inbox, nil, nil, nil)
		require.NoError(t, err)
		require.Zero(t, advanced.TotalResponses)
	})

	t.Run("owner_table_isolation", func(t *testing.T) {
		f := newCSATMetricOwnerFixture(t, open)
		for _, table := range []string{"conversations", "users", "contacts", "inboxes"} {
			require.False(t, f.qlt.Migrator().HasTable(table))
		}
		require.False(t, f.con.Migrator().HasTable("csat_surveys"))
		require.False(t, f.iam.Migrator().HasTable("csat_surveys"))
		inbox := uint(10)
		got, err := f.advanced.GetAdvancedMetrics(91, &inbox, nil, nil, nil)
		require.NoError(t, err)
		require.Len(t, got.AgentMetrics, 2)
	})

	for _, advanced := range []bool{false, true} {
		name := "legacy"
		if advanced {
			name = "advanced"
		}
		t.Run(name, func(t *testing.T) {
			f := newCSATMetricOwnerFixture(t, open)
			call := func() (any, error) {
				if advanced {
					return f.advanced.GetAdvancedMetrics(91, nil, nil, nil, nil)
				}
				return f.legacy.GetMetrics(context.Background(), 91)
			}
			before, err := call()
			require.NoError(t, err)
			for index, reply := range csatMetricReplies(advanced) {
				t.Run(reply.name+"_error_and_recovery", func(t *testing.T) {
					ownerErr := errors.New("private QLT callback sentinel")
					reads := 0
					fail := true
					callback := "csat_required_read_error"
					check := func(tx *gorm.DB) {
						if tx.Statement.Table != "csat_surveys" {
							return
						}
						reads++
						if fail && reads == index+1 {
							_ = tx.AddError(ownerErr)
						}
					}
					require.NoError(t, f.qlt.Callback().Query().Before("gorm:query").Register(callback, check))
					require.NoError(t, f.qlt.Callback().Row().Before("gorm:row").Register(callback, check))
					defer func() {
						require.NoError(t, f.qlt.Callback().Query().Remove(callback))
						require.NoError(t, f.qlt.Callback().Row().Remove(callback))
					}()
					got, err := call()
					require.Same(t, ownerErr, err)
					require.Nil(t, got)
					require.Equal(t, index+1, reads)
					fail, reads = false, 0
					got, err = call()
					require.NoError(t, err)
					require.Equal(t, before, got)
					require.Equal(t, len(csatMetricReplies(advanced)), reads)
				})
			}
			t.Run("actual_table_unavailable_and_recovery", func(t *testing.T) {
				require.NoError(t, f.qlt.Migrator().RenameTable("csat_surveys", "csat_surveys_unavailable"))
				restored := false
				defer func() {
					if !restored {
						require.NoError(t, f.qlt.Migrator().RenameTable("csat_surveys_unavailable", "csat_surveys"))
					}
				}()
				got, err := call()
				require.Error(t, err)
				require.Nil(t, got)
				require.NoError(t, f.qlt.Migrator().RenameTable("csat_surveys_unavailable", "csat_surveys"))
				restored = true
				got, err = call()
				require.NoError(t, err)
				require.Equal(t, before, got)
			})
		})
	}

	t.Run("existing_con_error_stops_before_qlt", func(t *testing.T) {
		f := newCSATMetricOwnerFixture(t, open)
		ownerErr := errors.New("CON filter unavailable")
		reads := 0
		require.NoError(t, f.qlt.Callback().Query().Before("gorm:query").Register("csat_no_qlt_query", func(*gorm.DB) { reads++ }))
		require.NoError(t, f.qlt.Callback().Row().Before("gorm:row").Register("csat_no_qlt_row", func(*gorm.DB) { reads++ }))
		require.NoError(t, f.con.Callback().Query().Before("gorm:query").Register("csat_con_failure", func(tx *gorm.DB) { _ = tx.AddError(ownerErr) }))
		inbox := uint(10)
		got, err := f.advanced.GetAdvancedMetrics(91, &inbox, nil, nil, nil)
		require.Same(t, ownerErr, err)
		require.Nil(t, got)
		require.Zero(t, reads)
		require.NoError(t, f.con.Callback().Query().Remove("csat_con_failure"))
		got, err = f.advanced.GetAdvancedMetrics(91, &inbox, nil, nil, nil)
		require.NoError(t, err)
		require.Equal(t, int64(2), got.TotalResponses)
	})

	t.Run("existing_iam_error_discards_partial_agents", func(t *testing.T) {
		f := newCSATMetricOwnerFixture(t, open)
		ownerErr := errors.New("IAM hydration unavailable")
		users := &csatMetricUsers{failAt: 2, err: ownerErr}
		f.advanced.users = users
		got, err := f.advanced.GetAdvancedMetrics(91, nil, nil, nil, nil)
		require.Same(t, ownerErr, err)
		require.Nil(t, got)
		require.Len(t, users.calls, 2)
		got, err = f.advanced.GetAdvancedMetrics(91, nil, nil, nil, nil)
		require.NoError(t, err)
		require.Len(t, got.AgentMetrics, 2)
	})
}

func TestCSATMetricsAverageNullAfterNonzeroCount(t *testing.T) {
	conn, _, call := csatMetricFaultFixture(t, true)
	conn.replies[4].values = [][]driver.Value{{nil}}
	got, err := call()
	require.NoError(t, err, "a concurrent empty AVG remains zero without claiming snapshot consistency")
	require.Zero(t, got.(*CSATMetrics).AverageRating)
}
