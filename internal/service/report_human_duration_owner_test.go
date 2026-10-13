package service

import (
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestReportHumanDurationOwnerStores(t *testing.T) {
	runReportHumanDurationOwnerStores(t, openSummaryMessageSQLite)
}

func TestPostgresReportHumanDurationOwnerStores(t *testing.T) {
	runReportHumanDurationOwnerStores(t, reportWindowTestPostgres(t))
}

func newReportHumanDurationOwnerFixture(t *testing.T, open func(*testing.T, string) *gorm.DB) *reportWindowFixture {
	t.Helper()
	f := newReportWindowFixture(t, open)
	for _, owner := range []string{"TEN", "IAM"} {
		f.stores[owner] = open(t, owner)
		require.NoError(t, database.AutoMigrateModule(f.stores[owner], owner))
	}
	users := repository.NewUserRepository(f.stores["IAM"])
	f.service.SetWorkforceRepositories(repository.NewAccountRepositoryWithUserPort(f.stores["TEN"], users), repository.NewTeamRepositoryWithUserPort(f.stores["TEN"], users), users)
	f.service.labels = repository.NewLabelRepository(f.stores["CUS"])
	f.create(t, "IAM", &domain.User{ID: 7, Name: "Report agent", Email: "human-duration@example.test"})
	f.create(t, "TEN", &domain.AccountUser{AccountID: 91, UserID: 7, Role: domain.RoleAgent})
	f.create(t, "TEN", &domain.Team{ID: 8, AccountID: 91, Name: "Report team"})
	f.create(t, "TEN", &domain.TeamMember{TeamID: 8, UserID: 7})
	f.create(t, "CUS", &domain.Label{ID: 9, AccountID: 91, Title: "human-duration"})
	f.create(t, "CUS", &domain.Label{ID: 99, AccountID: 92, Title: "foreign-duration"})
	require.NoError(t, f.stores["CHN"].Model(&domain.Inbox{}).Where("id = ?", 10).Updates(map[string]any{
		"timezone": "UTC", "working_hours_enabled": true,
		"working_hours": `[{"day_of_week":4,"open_hour":9,"close_hour":17},{"day_of_week":5,"open_hour":9,"close_hour":17}]`,
	}).Error)
	return f
}

func runReportHumanDurationOwnerStores(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	for _, tc := range []struct {
		name                   string
		business               bool
		frt, reply, resolution float64
		buckets                FirstResponseDistributionBuckets
		under4h, over4h        int64
	}{
		{"ordinary", false, 64800, 34200, 37800, FirstResponseDistributionBuckets{EightToTwentyFour: 1}, 0, 1},
		{"business_hours", true, 7200, 5400, 9000, FirstResponseDistributionBuckets{OneToFourHours: 1}, 1, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newReportHumanDurationOwnerFixture(t, open)
			start := time.Date(2024, 2, 29, 16, 0, 0, 0, time.UTC)
			since, until := start.Add(-time.Hour), start.Add(25*time.Hour)
			user, team := uint(7), uint(8)
			for _, row := range []struct {
				id       uint
				resolved time.Time
			}{{1, start.Add(20 * time.Hour)}, {2, start.Add(time.Hour)}} {
				f.create(t, "CON", &domain.Conversation{ID: row.id, DisplayID: row.id, AccountID: 91, InboxID: 10, ContactID: 1, AssigneeID: &user, TeamID: &team, Status: domain.ConversationStatusResolved, CreatedAt: start, UpdatedAt: row.resolved})
				f.create(t, "CON", &domain.ConversationLabel{ConversationID: row.id, LabelID: 9})
			}
			f.create(t, "CUS", &domain.Contact{ID: 1, AccountID: 91, Name: "Duration contact", CreatedAt: start})
			add := func(conversation uint, offset time.Duration, kind, sender string, private bool) {
				f.create(t, "MSG", &domain.Message{AccountID: 91, ConversationID: conversation, SenderID: 7, SenderType: sender, MessageType: kind, Private: private, Status: "failed", CreatedAt: start.Add(offset)})
			}
			add(1, 0, "incoming", "Contact", false)
			add(1, time.Minute, "outgoing", "AgentBot", false)
			add(1, 2*time.Minute, "outgoing", "CaptainAssistant", false)
			add(1, 3*time.Minute, "outgoing", "Captain::Assistant", false)
			add(1, 4*time.Minute, "outgoing", "User", true)
			add(1, 18*time.Hour, "outgoing", "User", false)
			add(1, 19*time.Hour, "incoming", "Contact", false)
			add(1, 19*time.Hour+time.Minute, "outgoing", "AgentBot", false)
			add(1, 20*time.Hour, "outgoing", "", false)
			add(2, 0, "incoming", "Contact", false)
			add(2, time.Minute, "outgoing", "Captain::Assistant", false)
			// MSG account scope wins even when a foreign row names an owned CON ID.
			f.create(t, "MSG", &domain.Message{AccountID: 92, ConversationID: 1, MessageType: "outgoing", SenderType: "User", CreatedAt: start.Add(30 * time.Second)})
			filter := ReportFilter{Since: &since, Until: &until, BusinessHours: tc.business}
			summary, err := f.service.GetAccountSummary(91, filter)
			require.NoError(t, err)
			require.Equal(t, &AccountSummaryReport{
				TotalConversations: 2, ResolvedConversations: 2, TotalMessages: 11, TotalContacts: 1,
				ConversationsCount: 2, IncomingMessagesCount: 3, OutgoingMessagesCount: 8,
				ResolutionsCount: 2, ResolutionRate: 100, AvgFirstResponseTime: tc.frt,
				AvgReplyTime: tc.reply, AvgResolutionTime: tc.resolution,
				FirstContactResolutionRate: 50, BotHandoffsCount: 2,
			}, summary)
			agents, err := f.service.GetAgentMetrics(91, filter)
			require.NoError(t, err)
			require.Len(t, agents, 1)
			require.Equal(t, tc.frt, agents[0].AvgFirstResponseTime)
			require.Equal(t, tc.reply, agents[0].AvgReplyTime)
			require.Equal(t, tc.resolution, agents[0].AvgResolutionTime)
			require.Equal(t, int64(2), agents[0].AssignedConversations)
			require.Equal(t, int64(2), agents[0].ResolvedConversations)
			require.Equal(t, int64(2), agents[0].TotalMessagesSent, "retain all User messages, including private/failed")
			teams, err := f.service.GetTeamMetrics(91, filter)
			require.NoError(t, err)
			require.Len(t, teams, 1)
			require.Equal(t, tc.frt, teams[0].AvgFirstResponseTime)
			require.Equal(t, tc.resolution, teams[0].AvgResolutionTime)
			require.Equal(t, int64(2), teams[0].AssignedConversations)
			inboxes, err := f.service.GetInboxMetrics(91, filter)
			require.NoError(t, err)
			require.Len(t, inboxes, 1)
			require.Equal(t, tc.frt, inboxes[0].AvgFirstResponseTime)
			require.Equal(t, tc.reply, inboxes[0].AvgReplyTime)
			require.Equal(t, tc.resolution, inboxes[0].AvgResolutionTime)
			require.Equal(t, int64(2), inboxes[0].TotalConversations)
			labels, err := f.service.GetLabelMetrics(91, filter)
			require.NoError(t, err)
			require.Len(t, labels, 1)
			require.Equal(t, tc.frt, labels[0].AvgFirstResponseTime)
			require.Equal(t, tc.resolution, labels[0].AvgResolutionTime)
			require.Equal(t, int64(2), labels[0].ConversationCount)
			distribution, err := f.service.GetFirstResponseDistribution(91, filter)
			require.NoError(t, err)
			require.Equal(t, &FirstResponseDistributionReport{Total: tc.buckets, Channels: []ChannelFRTDistribution{{ChannelType: "Channel::WebWidget", Distribution: tc.buckets}}, Under4h: tc.under4h, Over4h: tc.over4h}, distribution, "bot-only conversation must not create a zero-duration sample")
			for _, model := range []any{&domain.Conversation{}, &domain.Message{}, &domain.Inbox{}, &domain.User{}, &domain.Label{}, &domain.Team{}} {
				require.False(t, f.stores["RPT"].Migrator().HasTable(model))
			}
			require.False(t, f.stores["MSG"].Migrator().HasTable(&domain.Conversation{}))
			require.False(t, f.stores["TEN"].Migrator().HasTable(&domain.User{}))
		})
	}
	t.Run("cohort_and_half_open_message_window_unchanged", func(t *testing.T) {
		f := newReportWindowFixture(t, open)
		since := time.Date(2024, 2, 29, 9, 0, 0, 0, time.UTC)
		until := since.Add(time.Hour)
		for _, row := range []struct {
			id      uint
			created time.Time
		}{{1, since}, {2, since.Add(-time.Second)}, {3, until}, {4, since}} {
			f.create(t, "CON", &domain.Conversation{ID: row.id, DisplayID: row.id, AccountID: 91, InboxID: 10, ContactID: 1, CreatedAt: row.created})
		}
		add := func(conversation uint, at time.Time, kind, sender string) {
			f.create(t, "MSG", &domain.Message{AccountID: 91, ConversationID: conversation, MessageType: kind, SenderType: sender, CreatedAt: at})
		}
		add(1, since.Add(-time.Minute), "incoming", "Contact")
		add(1, since, "incoming", "Contact")
		add(1, since.Add(time.Second), "outgoing", "AgentBot")
		add(1, since.Add(10*time.Second), "incoming", "Contact")
		add(1, since.Add(30*time.Second), "outgoing", "User")
		add(1, until, "outgoing", "User")
		for _, id := range []uint{2, 3} {
			add(id, since, "incoming", "Contact")
			add(id, since.Add(time.Second), "outgoing", "User")
		}
		add(4, since, "incoming", "Contact")
		add(4, since.Add(time.Second), "outgoing", "CaptainAssistant")
		add(4, until, "outgoing", "User")
		got, err := f.service.GetAccountSummary(91, ReportFilter{Since: &since, Until: &until})
		require.NoError(t, err)
		require.Equal(t, int64(2), got.TotalConversations)
		require.Equal(t, float64(30), got.AvgFirstResponseTime)
		require.Equal(t, float64(30), got.AvgReplyTime)
		require.Equal(t, int64(10), got.TotalMessages, "default counts retain all in-window account messages")
		require.Equal(t, int64(5), got.IncomingMessagesCount)
		require.Equal(t, int64(5), got.OutgoingMessagesCount)
	})
}
