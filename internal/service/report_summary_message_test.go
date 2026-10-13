package service

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func openSummaryMessageSQLite(t *testing.T, owner string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), owner+".db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	return db
}

func TestReportSummaryMessageInsertionInvariant(t *testing.T) {
	runReportSummaryMessageInsertionInvariant(t, openSummaryMessageSQLite)
}

func runReportSummaryMessageInsertionInvariant(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	f := newReportWindowFixture(t, open)
	since := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
	until := since.Add(24 * time.Hour)
	f.create(t, "CON", &domain.Conversation{ID: 1, AccountID: 91, InboxID: 10, ContactID: 1, DisplayID: 1, CreatedAt: since.Add(-24 * time.Hour)})
	for i := uint(1); i <= 2; i++ {
		f.create(t, "MSG", &domain.Message{ID: i, AccountID: 91, ConversationID: 1, MessageType: domain.MessageTypeIncoming, CreatedAt: since.Add(time.Hour)})
	}
	for n := 0; n <= 3; n++ {
		if n > 0 {
			f.create(t, "CON", &domain.Conversation{ID: uint(n + 1), AccountID: 91, InboxID: 10, ContactID: 1, DisplayID: uint(n + 1), CreatedAt: since.Add(2 * time.Hour)})
		}
		got, err := f.service.GetAccountSummary(91, ReportFilter{Since: &since, Until: &until})
		require.NoError(t, err)
		t.Logf("new conversations=%d: total=%d incoming=%d outgoing=%d", n, got.TotalMessages, got.IncomingMessagesCount, got.OutgoingMessagesCount)
		require.Equal(t, int64(2), got.IncomingMessagesCount, "adding unrelated conversation %d must not erase existing messages", n)
		require.Equal(t, int64(2), got.TotalMessages)
		require.Zero(t, got.OutgoingMessagesCount)
	}
}

func TestReportSummaryMessageSemantics(t *testing.T) {
	runReportSummaryMessageSemantics(t, openSummaryMessageSQLite)
}

func runReportSummaryMessageSemantics(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	t.Run("old_conversation_messages_and_non_count_metrics", func(t *testing.T) {
		f := newReportWindowFixture(t, open)
		since := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
		until := since.Add(24 * time.Hour)
		old := since.Add(-24 * time.Hour)
		resolved := since.Add(3 * time.Hour)
		f.create(t, "CON", &domain.Conversation{ID: 1, AccountID: 91, InboxID: 10, ContactID: 1, DisplayID: 1, CreatedAt: old})
		f.create(t, "CON", &domain.Conversation{ID: 2, AccountID: 91, InboxID: 10, ContactID: 1, DisplayID: 2, CreatedAt: since, Status: domain.ConversationStatusResolved, UpdatedAt: resolved})
		f.create(t, "MSG", &domain.Message{AccountID: 91, ConversationID: 2, MessageType: domain.MessageTypeIncoming, CreatedAt: since})
		f.create(t, "MSG", &domain.Message{AccountID: 91, ConversationID: 2, MessageType: domain.MessageTypeOutgoing, SenderType: domain.SenderTypeUser, SenderID: 7, CreatedAt: since.Add(time.Hour)})
		filter := ReportFilter{Since: &since, Until: &until}
		before, err := f.service.GetAccountSummary(91, filter)
		require.NoError(t, err)
		require.Equal(t, float64(3600), before.AvgFirstResponseTime)
		require.Equal(t, float64(10800), before.AvgResolutionTime)
		require.Equal(t, float64(3600), before.AvgReplyTime)
		require.Equal(t, float64(100), before.FirstContactResolutionRate)
		for _, row := range []struct {
			kind, sender, status string
			private              bool
		}{
			{"incoming", "Contact", "sent", false}, {"incoming", "Contact", "sent", false},
			{"outgoing", "User", "sent", true}, {"outgoing", "AgentBot", "failed", false}, {"outgoing", "User", "queued", false},
			{"activity", "User", "sent", false}, {"template", "User", "sent", false},
		} {
			f.create(t, "MSG", &domain.Message{AccountID: 91, ConversationID: 1, MessageType: row.kind, SenderType: row.sender, Status: row.status, Private: row.private, Content: "body must not affect counts", CreatedAt: since.Add(2 * time.Hour)})
		}
		// Foreign account rows cannot enter counts, even with this account's local
		// conversation ID in a separately owned MSG store.
		f.create(t, "MSG", &domain.Message{AccountID: 92, ConversationID: 2, MessageType: domain.MessageTypeIncoming, CreatedAt: since})
		after, err := f.service.GetAccountSummary(91, filter)
		require.NoError(t, err)
		require.Equal(t, int64(9), after.TotalMessages)
		require.Equal(t, int64(3), after.IncomingMessagesCount)
		require.Equal(t, int64(4), after.OutgoingMessagesCount)
		before.TotalMessages, before.IncomingMessagesCount, before.OutgoingMessagesCount = 0, 0, 0
		after.TotalMessages, after.IncomingMessagesCount, after.OutgoingMessagesCount = 0, 0, 0
		require.Equal(t, before, after, "old-conversation messages must not alter duration, FCR, bot or conversation formulas")
		require.False(t, f.stores["RPT"].Migrator().HasTable(&domain.Message{}))
		require.False(t, f.stores["MSG"].Migrator().HasTable(&domain.Conversation{}))
	})
	t.Run("legacy_business_hours_true_keeps_cohort_and_bypasses_aggregate", func(t *testing.T) {
		f := newReportWindowFixture(t, open)
		since := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
		until := since.Add(24 * time.Hour)
		require.NoError(t, f.stores["CHN"].Model(&domain.Inbox{}).Where("id = ?", 10).Updates(map[string]any{"timezone": "UTC", "working_hours_enabled": true, "working_hours": `[{"day_of_week":4,"closed":false,"open_hour":9,"open_minute":0,"close_hour":17,"close_minute":0}]`}).Error)
		for i, hour := range []int{-1, 1, 10} {
			id := uint(i + 1)
			f.create(t, "CON", &domain.Conversation{ID: id, AccountID: 91, InboxID: 10, ContactID: 1, DisplayID: id, CreatedAt: since.Add(time.Duration(hour) * time.Hour)})
			f.create(t, "MSG", &domain.Message{AccountID: 91, ConversationID: id, MessageType: domain.MessageTypeIncoming, CreatedAt: since.Add(12 * time.Hour)})
		}
		f.service.SetSummaryMessageOwner(nil)
		got, err := f.service.GetAccountSummary(91, ReportFilter{Since: &since, Until: &until, BusinessHours: true})
		require.NoError(t, err)
		require.Equal(t, int64(1), got.TotalMessages)
		require.Equal(t, int64(1), got.IncomingMessagesCount)
		require.Equal(t, int64(1), got.TotalConversations)
		// With all conversations outside business hours there is still no legacy
		// account-wide fallback, even when an in-window message exists.
		end := since.Add(2 * time.Hour)
		got, err = f.service.GetAccountSummary(91, ReportFilter{Since: &since, Until: &end, BusinessHours: true})
		require.NoError(t, err)
		require.Zero(t, got.TotalMessages)
	})
}

func TestPostgresReportSummaryMessages(t *testing.T) {
	open := reportWindowTestPostgres(t)
	t.Run("insertion_invariance", func(t *testing.T) { runReportSummaryMessageInsertionInvariant(t, open) })
	runReportSummaryMessageSemantics(t, open)
}

type reportSummaryMessageStub struct {
	call func(uint, *time.Time, *time.Time) (domain.ReportMessageSummary, error)
}

func (s reportSummaryMessageStub) CountReportSummary(a uint, lo, hi *time.Time) (domain.ReportMessageSummary, error) {
	return s.call(a, lo, hi)
}

func TestReportSummaryMessageOwnerFailure(t *testing.T) {
	f := newReportWindowFixture(t, openSummaryMessageSQLite)
	for _, tc := range []string{"missing_owner", "private_owner_error"} {
		t.Run(tc, func(t *testing.T) {
			f.service.SetSummaryMessageOwner(nil)
			if tc == "private_owner_error" {
				f.service.SetSummaryMessageOwner(reportSummaryMessageStub{call: func(a uint, lo, hi *time.Time) (domain.ReportMessageSummary, error) {
					require.Equal(t, uint(91), a)
					require.Nil(t, lo)
					require.Nil(t, hi)
					return domain.ReportMessageSummary{Total: 100}, errors.New("private customer body and SQL detail")
				}})
			}
			got, err := f.service.GetAccountSummary(91)
			require.Nil(t, got)
			require.EqualError(t, err, "report counts are unavailable")
		})
	}
}
