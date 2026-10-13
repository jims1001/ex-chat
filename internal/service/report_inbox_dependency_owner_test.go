package service

import (
	"errors"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReportInboxDependencyOwnerStores(t *testing.T) {
	runReportInboxDependencyOwnerStores(t, openSummaryMessageSQLite)
}

func TestPostgresReportInboxDependencyOwnerStores(t *testing.T) {
	runReportInboxDependencyOwnerStores(t, reportWindowTestPostgres(t))
}

func seedReportInboxDependency(t *testing.T, f *reportWindowFixture, inboxID uint) ReportFilter {
	t.Helper()
	start := time.Date(2024, 2, 29, 16, 0, 0, 0, time.UTC)
	end := start.Add(19 * time.Hour)
	user, team := uint(7), uint(8)
	f.create(t, "CON", &domain.Conversation{ID: 1, DisplayID: 1, AccountID: 91, InboxID: inboxID, ContactID: 1, AssigneeID: &user, TeamID: &team, Status: domain.ConversationStatusResolved, CreatedAt: start, UpdatedAt: start.Add(18 * time.Hour)})
	f.create(t, "CON", &domain.ConversationLabel{ConversationID: 1, LabelID: 9})
	f.create(t, "CUS", &domain.Contact{ID: 1, AccountID: 91, Name: "Dependency contact", CreatedAt: start})
	f.create(t, "MSG", &domain.Message{AccountID: 91, ConversationID: 1, SenderType: "Contact", MessageType: "incoming", CreatedAt: start})
	f.create(t, "MSG", &domain.Message{AccountID: 91, ConversationID: 1, SenderID: user, SenderType: "User", MessageType: "outgoing", CreatedAt: start.Add(18 * time.Hour)})
	// Excluded half-open endpoint and another tenant must stay excluded after recovery.
	f.create(t, "CON", &domain.Conversation{ID: 2, DisplayID: 2, AccountID: 91, InboxID: inboxID, ContactID: 1, AssigneeID: &user, TeamID: &team, Status: domain.ConversationStatusOpen, CreatedAt: end})
	f.create(t, "CON", &domain.Conversation{ID: 3, DisplayID: 3, AccountID: 92, InboxID: inboxID, ContactID: 1, AssigneeID: &user, TeamID: &team, Status: domain.ConversationStatusOpen, CreatedAt: start})
	f.create(t, "MSG", &domain.Message{AccountID: 92, ConversationID: 1, SenderType: "User", MessageType: "outgoing", CreatedAt: start.Add(time.Minute)})
	require.NoError(t, f.stores["CHN"].Model(&domain.Inbox{}).Where("id = ?", 10).Update("channel_type", "Channel::Email").Error)
	return ReportFilter{Since: &start, Until: &end}
}

func runReportInboxDependencyOwnerStores(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	for _, business := range []bool{false, true} {
		mode := "ordinary"
		if business {
			mode = "business_hours"
		}
		t.Run(mode, func(t *testing.T) {
			f := newReportHumanDurationOwnerFixture(t, open)
			filter := seedReportInboxDependency(t, f, 10)
			filter.BusinessHours = business
			for _, model := range []any{&domain.Inbox{}, &domain.Conversation{}, &domain.Message{}, &domain.Contact{}, &domain.User{}, &domain.Team{}} {
				require.False(t, f.stores["RPT"].Migrator().HasTable(model), "RPT must not have another owner's tables")
			}
			wantDuration := float64(64800)
			if business {
				wantDuration = 7200
			}
			summary, err := f.service.GetAccountSummary(91, filter)
			require.NoError(t, err)
			require.Equal(t, &AccountSummaryReport{TotalConversations: 1, ResolvedConversations: 1, TotalMessages: 2, TotalContacts: 1, ConversationsCount: 1, IncomingMessagesCount: 1, OutgoingMessagesCount: 1, ResolutionsCount: 1, ResolutionRate: 100, AvgFirstResponseTime: wantDuration, AvgResolutionTime: wantDuration, AvgReplyTime: wantDuration, FirstContactResolutionRate: 100}, summary)

			for _, tc := range reportInboxDependencyCases() {
				t.Run(tc.name, func(t *testing.T) {
					before, err := tc.call(f.service, 91, filter)
					require.NoError(t, err)
					ownerErr := errors.New("CHN isolated owner read unavailable")
					injected := 0
					callback := "report_inbox_dependency_owner_failure"
					require.NoError(t, f.stores["CHN"].Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
						if tx.Statement.Table == "inboxes" && injected == 0 {
							injected++
							_ = tx.AddError(ownerErr)
						}
					}))
					defer func() { require.NoError(t, f.stores["CHN"].Callback().Query().Remove(callback)) }()
					got, err := tc.call(f.service, 91, filter)
					require.ErrorIs(t, err, ownerErr)
					require.Nil(t, got)
					require.Equal(t, 1, injected)
					// The same service and same owner handle a later healthy request.
					after, err := tc.call(f.service, 91, filter)
					require.NoError(t, err)
					require.Equal(t, before, after, "recovery must preserve complete shape, counts and time window")
				})
			}
		})
	}

	t.Run("healthy_empty_account", func(t *testing.T) {
		f := newReportHumanDurationOwnerFixture(t, open)
		start := time.Date(2024, 2, 29, 16, 0, 0, 0, time.UTC)
		end := start.Add(time.Hour)
		for _, business := range []bool{false, true} {
			filter := ReportFilter{Since: &start, Until: &end, BusinessHours: business}
			for _, tc := range reportInboxDependencyCases() {
				got, err := tc.call(f.service, 101, filter)
				require.NoError(t, err, tc.name)
				if tc.name != "export" { // CON's historical empty slice may be nil.
					require.NotNil(t, got, tc.name)
				}
			}
			summary, err := f.service.GetAccountSummary(101, filter)
			require.NoError(t, err)
			require.Equal(t, &AccountSummaryReport{}, summary)
			inboxes, err := f.service.GetInboxMetrics(101, filter)
			require.NoError(t, err)
			require.Equal(t, []InboxMetric{}, inboxes)
			distribution, err := f.service.GetFirstResponseDistribution(101, filter)
			require.NoError(t, err)
			require.Equal(t, &FirstResponseDistributionReport{Channels: []ChannelFRTDistribution{}}, distribution)
		}
	})

	t.Run("healthy_missing_inbox_legacy_fallback", func(t *testing.T) {
		f := newReportHumanDurationOwnerFixture(t, open)
		filter := seedReportInboxDependency(t, f, 999)
		filter.BusinessHours = true
		for _, tc := range reportInboxDependencyCases() {
			_, err := tc.call(f.service, 91, filter)
			require.NoError(t, err, tc.name)
		}
		summary, err := f.service.GetAccountSummary(91, filter)
		require.NoError(t, err)
		require.Equal(t, int64(1), summary.ConversationsCount)
		require.Equal(t, float64(64800), summary.AvgFirstResponseTime, "a genuinely missing inbox retains existing wall-clock duration semantics")
		require.Equal(t, float64(64800), summary.AvgResolutionTime)
		require.Equal(t, float64(64800), summary.AvgReplyTime)
		distribution, err := f.service.GetFirstResponseDistribution(91, filter)
		require.NoError(t, err)
		buckets := FirstResponseDistributionBuckets{EightToTwentyFour: 1}
		require.Equal(t, &FirstResponseDistributionReport{Total: buckets, Channels: []ChannelFRTDistribution{{ChannelType: "Channel::WebWidget", Distribution: buckets}}, Over4h: 1}, distribution)
	})
}
