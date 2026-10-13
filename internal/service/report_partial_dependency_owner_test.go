package service

import (
	"errors"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReportPartialDependencyOwnerStores(t *testing.T) {
	runReportPartialDependencyOwnerStores(t, openSummaryMessageSQLite)
}

func TestPostgresReportPartialDependencyOwnerStores(t *testing.T) {
	runReportPartialDependencyOwnerStores(t, reportWindowTestPostgres(t))
}

func seedReportPartialAgents(t *testing.T, f *reportWindowFixture) []AgentLiveConversationMetric {
	t.Helper()
	f.create(t, "IAM", &domain.User{ID: 8, Name: "Second agent", Email: "second-partial@example.test", AvatarURL: "second.png"})
	f.create(t, "IAM", &domain.User{ID: 9, Name: "Foreign agent", Email: "foreign-partial@example.test"})
	f.create(t, "TEN", &domain.AccountUser{AccountID: 91, UserID: 8, Role: domain.RoleAgent, Availability: "busy"})
	f.create(t, "TEN", &domain.AccountUser{AccountID: 92, UserID: 9, Role: domain.RoleAgent})
	first, second := uint(7), uint(8)
	seen := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, row := range []domain.Conversation{
		{AccountID: 91, AssigneeID: &first, Status: domain.ConversationStatusOpen, UnreadCount: 1, AgentLastSeenAt: &seen},
		{AccountID: 91, AssigneeID: &first, Status: domain.ConversationStatusOpen, AgentLastSeenAt: &seen},
		{AccountID: 91, AssigneeID: &second, Status: domain.ConversationStatusOpen},
		{AccountID: 91, Status: domain.ConversationStatusOpen},
		{AccountID: 91, AssigneeID: &second, Status: domain.ConversationStatusPending},
		{AccountID: 91, AssigneeID: &first, Status: domain.ConversationStatusResolved},
		{AccountID: 92, AssigneeID: &first, Status: domain.ConversationStatusOpen},
		{AccountID: 92, AssigneeID: &second, Status: domain.ConversationStatusPending},
	} {
		row.ID, row.DisplayID, row.InboxID, row.ContactID, row.CreatedAt = uint(i+1), uint(i+1), 10, 1, seen
		f.create(t, "CON", &row)
	}
	members, err := f.service.accounts.ListMembers(91)
	require.NoError(t, err)
	require.Len(t, members, 2)
	want := make([]AgentLiveConversationMetric, 0, 2)
	for _, member := range members {
		metric := LiveMetricsReport{Open: 1, Unattended: 1, Pending: 1}
		if member.UserID == first {
			metric.Open = 2
		}
		want = append(want, AgentLiveConversationMetric{ID: member.User.ID, Name: member.User.Name, Email: member.User.Email, Thumbnail: member.User.AvatarURL, Availability: member.Availability, Metric: metric})
	}
	return want
}

func seedReportPartialAnnual(t *testing.T, f *reportWindowFixture) *YearInReviewReport {
	t.Helper()
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	user, other := uint(7), uint(8)
	for i, at := range []time.Time{start.Add(-time.Microsecond), start, end.Add(-time.Microsecond), end, end.Add(time.Microsecond)} {
		id := uint(i + 1)
		f.create(t, "CON", &domain.Conversation{ID: id, DisplayID: id, AccountID: 91, InboxID: 10, ContactID: 1, AssigneeID: &user, Status: domain.ConversationStatusOpen, CreatedAt: at})
		value := float64(1000)
		if i == 1 {
			value = 10.25
		}
		if i == 2 {
			value = 31.25
		}
		f.create(t, "RPT", &domain.ReportingEvent{AccountID: 91, UserID: &user, Name: "first_response", Value: value, CreatedAt: at})
	}
	f.create(t, "CON", &domain.Conversation{ID: 6, DisplayID: 6, AccountID: 92, InboxID: 10, ContactID: 1, AssigneeID: &user, Status: domain.ConversationStatusOpen, CreatedAt: start})
	f.create(t, "CON", &domain.Conversation{ID: 7, DisplayID: 7, AccountID: 91, InboxID: 10, ContactID: 1, AssigneeID: &other, Status: domain.ConversationStatusOpen, CreatedAt: start})
	for _, row := range []domain.ReportingEvent{
		{AccountID: 92, UserID: &user, Name: "first_response", Value: 9000, CreatedAt: start},
		{AccountID: 91, UserID: &other, Name: "first_response", Value: 8000, CreatedAt: start},
		{AccountID: 91, UserID: &user, Name: "reply_time", Value: 7000, CreatedAt: start},
		{AccountID: 91, Name: "first_response", Value: 6000, CreatedAt: start},
	} {
		f.create(t, "RPT", &row)
	}
	return &YearInReviewReport{Year: 2024, TotalConversations: 2, BusiestDay: &YearInReviewBusiestDay{Date: "Jan 01", Count: 1}, SupportPersonality: &YearInReviewSupportPersonality{AvgResponseTimeSeconds: 20}}
}

func runReportPartialDependencyOwnerStores(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	t.Run("agent_conversations", func(t *testing.T) {
		f := newReportHumanDurationOwnerFixture(t, open)
		want := seedReportPartialAgents(t, f)
		for _, tc := range []struct {
			name string
			read int
		}{{"first_live_read", 1}, {"first_agent_read", 2}, {"late_live_read", 3}, {"late_agent_read", 4}} {
			t.Run(tc.name, func(t *testing.T) {
				before, err := f.service.GetConversationsReport(91, "agent", 1, 25)
				require.NoError(t, err)
				require.Equal(t, want, before)
				ownerErr := errors.New("CON isolated owner read failure")
				reads := 0
				callback := "report_partial_con_owner_failure"
				require.NoError(t, f.stores["CON"].Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
					if tx.Statement.Table == "conversations" {
						reads++
						if reads == tc.read {
							_ = tx.AddError(ownerErr)
						}
					}
				}))
				defer func() { require.NoError(t, f.stores["CON"].Callback().Query().Remove(callback)) }()
				got, err := f.service.GetConversationsReport(91, "agent", 1, 25)
				require.ErrorIs(t, err, ownerErr)
				require.Nil(t, got)
				require.Equal(t, tc.read, reads, "stop on the first dependency error")
				after, err := f.service.GetConversationsReport(91, "agent", 1, 25)
				require.NoError(t, err)
				require.Equal(t, before, after)
				require.Equal(t, tc.read+4, reads)
			})
		}
		t.Run("healthy_scope_and_pagination", func(t *testing.T) {
			got, err := f.service.GetConversationsReport(91, "agent", 2, 1)
			require.NoError(t, err)
			require.Equal(t, want[1:], got)
			got, err = f.service.GetConversationsReport(91, "account", 2, 1)
			require.NoError(t, err)
			require.Equal(t, &LiveMetricsReport{Open: 4, Unattended: 3, Unassigned: 1, Pending: 1}, got)
			got, err = f.service.GetConversationsReport(92, "agent", 1, 25)
			require.NoError(t, err)
			foreign, ok := got.([]AgentLiveConversationMetric)
			require.True(t, ok)
			require.Len(t, foreign, 1)
			require.Equal(t, uint(9), foreign[0].ID)
			require.Equal(t, LiveMetricsReport{Pending: 1}, foreign[0].Metric)
		})
		t.Run("healthy_empty_members", func(t *testing.T) {
			// No conversation port proves an empty member page needs no CON read.
			s := *f.service
			s.conversations = nil
			got, err := s.GetConversationsReport(101, "agent", 1, 25)
			require.NoError(t, err)
			require.Equal(t, []AgentLiveConversationMetric{}, got)
		})
		t.Run("owner_table_isolation", func(t *testing.T) {
			for _, model := range []any{&domain.Conversation{}, &domain.AccountUser{}, &domain.User{}, &domain.Message{}, &domain.Inbox{}} {
				require.False(t, f.stores["RPT"].Migrator().HasTable(model))
			}
			require.False(t, f.stores["TEN"].Migrator().HasTable(&domain.Conversation{}))
			require.False(t, f.stores["TEN"].Migrator().HasTable(&domain.User{}))
			require.False(t, f.stores["CON"].Migrator().HasTable(&domain.ReportingEvent{}))
		})
	})
	t.Run("year_in_review", func(t *testing.T) {
		f := newReportWindowFixture(t, open)
		want := seedReportPartialAnnual(t, f)
		t.Run("row_error_and_recovery", func(t *testing.T) {
			ownerErr := errors.New("RPT isolated aggregate failure")
			reads := 0
			callback := "report_partial_rpt_owner_failure"
			// GORM Scan executes Rows through the Row processor, not Query.
			require.NoError(t, f.stores["RPT"].Callback().Row().Before("gorm:row").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Table == "reporting_events" {
					reads++
					if reads == 1 {
						_ = tx.AddError(ownerErr)
					}
				}
			}))
			defer func() { require.NoError(t, f.stores["RPT"].Callback().Row().Remove(callback)) }()
			got, err := f.service.GetYearInReview(91, 7, 2024)
			require.ErrorIs(t, err, ownerErr)
			require.Nil(t, got)
			require.Equal(t, 1, reads)
			got, err = f.service.GetYearInReview(91, 7, 2024)
			require.NoError(t, err)
			require.Equal(t, want, got)
			require.Equal(t, 2, reads)
		})
		t.Run("sql_error_and_recovery", func(t *testing.T) {
			db := f.stores["RPT"]
			require.NoError(t, db.Migrator().RenameTable("reporting_events", "reporting_events_offline"))
			restored := false
			defer func() {
				if !restored {
					require.NoError(t, db.Migrator().RenameTable("reporting_events_offline", "reporting_events"))
				}
			}()
			got, err := f.service.GetYearInReview(91, 7, 2024)
			require.Error(t, err, "a real SQL failure must not become a zero average")
			require.Nil(t, got)
			require.NoError(t, db.Migrator().RenameTable("reporting_events_offline", "reporting_events"))
			restored = true
			got, err = f.service.GetYearInReview(91, 7, 2024)
			require.NoError(t, err)
			require.Equal(t, want, got)
		})
		t.Run("user_tenant_and_utc_boundaries", func(t *testing.T) {
			got, err := f.service.GetYearInReview(91, 7, 2024)
			require.NoError(t, err)
			require.Equal(t, want, got, "retain half-open UTC year and integer truncation of 20.75")
			got, err = f.service.GetYearInReview(91, 8, 2024)
			require.NoError(t, err)
			require.Equal(t, int64(1), got.TotalConversations)
			require.Equal(t, 8000, got.SupportPersonality.AvgResponseTimeSeconds)
			got, err = f.service.GetYearInReview(92, 7, 2024)
			require.NoError(t, err)
			require.Equal(t, int64(1), got.TotalConversations)
			require.Equal(t, 9000, got.SupportPersonality.AvgResponseTimeSeconds)
		})
		t.Run("healthy_empty_average", func(t *testing.T) {
			user := uint(9)
			f.create(t, "CON", &domain.Conversation{ID: 8, DisplayID: 8, AccountID: 91, InboxID: 10, ContactID: 1, AssigneeID: &user, Status: domain.ConversationStatusOpen, CreatedAt: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)})
			got, err := f.service.GetYearInReview(91, user, 2024)
			require.NoError(t, err)
			require.Equal(t, &YearInReviewReport{Year: 2024, TotalConversations: 1, BusiestDay: &YearInReviewBusiestDay{Date: "Jan 01", Count: 1}, SupportPersonality: &YearInReviewSupportPersonality{}}, got)
			got, err = f.service.GetYearInReview(101, user, 2024)
			require.NoError(t, err)
			require.Equal(t, &YearInReviewReport{Year: 2024, SupportPersonality: &YearInReviewSupportPersonality{}}, got)
		})
	})
}
