package service

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type botReportFixture struct {
	service      *ReportService
	stores       map[string]*gorm.DB
	since, until time.Time
}

func newBotReportFixture(t *testing.T, open func(*testing.T, string) *gorm.DB) *botReportFixture {
	t.Helper()
	stores := map[string]*gorm.DB{}
	for _, owner := range []string{"RPT", "CON", "CHN", "MSG", "EXT"} {
		db := open(t, owner)
		require.NoError(t, database.AutoMigrateModule(db, owner))
		stores[owner] = db
	}
	s := NewReportService(stores["RPT"])
	s.SetOwnerRepositories(repository.NewConversationRepository(stores["CON"]), repository.NewInboxRepository(stores["CHN"]), repository.NewMessageRepository(stores["MSG"]), nil, nil)
	s.SetBotOwner(repository.NewAgentBotRepository(stores["EXT"]))
	f := &botReportFixture{service: s, stores: stores, since: time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)}
	f.until = f.since.Add(24 * time.Hour)
	f.create(t, "CHN", &domain.Inbox{ID: 10, AccountID: 91, Name: "valid bot inbox", WebsiteToken: "bot-test-10"})
	f.create(t, "EXT", &domain.AgentBot{ID: 20, AccountID: 91, Name: "valid bot"})
	f.create(t, "EXT", &domain.AgentBotInbox{AccountID: 91, InboxID: 10, AgentBotID: 20})
	return f
}
func (f *botReportFixture) create(t *testing.T, owner string, value any) {
	t.Helper()
	require.NoError(t, f.stores[owner].Create(value).Error)
}
func (f *botReportFixture) filter() ReportFilter {
	return ReportFilter{Since: &f.since, Until: &f.until, BusinessHours: true}
}
func (f *botReportFixture) conversation(t *testing.T, id, account, inbox uint, at time.Time) {
	t.Helper()
	f.create(t, "CON", &domain.Conversation{ID: id, AccountID: account, InboxID: inbox, ContactID: 1, DisplayID: id, Status: domain.ConversationStatusOpen, CreatedAt: at})
}
func (f *botReportFixture) event(t *testing.T, account, conversation uint, name string, at time.Time) {
	t.Helper()
	f.create(t, "RPT", &domain.ReportingEvent{AccountID: account, ConversationID: &conversation, Name: name, CreatedAt: at})
}
func (f *botReportFixture) message(t *testing.T, account, conversation uint, kind string, at time.Time) {
	t.Helper()
	f.create(t, "MSG", &domain.Message{AccountID: account, ConversationID: conversation, MessageType: kind, CreatedAt: at})
}
func (f *botReportFixture) assertCounts(t *testing.T, filter ReportFilter, conversations, messages, resolutions, handoffs int64, resolutionRate, handoffRate float64) {
	t.Helper()
	metrics, err := f.service.GetBotMetrics(91, filter)
	require.NoError(t, err)
	require.Equal(t, &BotMetricsReport{ConversationCount: conversations, MessageCount: messages, ResolutionRate: resolutionRate, HandoffRate: handoffRate}, metrics)
	summary, err := f.service.GetBotSummary(91, filter)
	require.NoError(t, err)
	require.Equal(t, resolutions, summary.BotResolutionsCount)
	require.Equal(t, handoffs, summary.BotHandoffsCount)
}

func TestBotReportCorrections(t *testing.T) {
	runBotReportCorrections(t, func(t *testing.T, owner string) *gorm.DB {
		db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), owner+".db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		conn, err := db.DB()
		require.NoError(t, err)
		conn.SetMaxOpenConns(1)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })
		return db
	})
}

func runBotReportCorrections(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	t.Run("distinct_outcomes_and_handoff_precedence", func(t *testing.T) {
		f := newBotReportFixture(t, open)
		for id := uint(1); id <= 3; id++ {
			f.conversation(t, id, 91, 10, f.since.Add(time.Hour))
		}
		for i := 0; i < 2; i++ {
			f.event(t, 91, 1, "conversation_bot_resolved", f.since.Add(2*time.Hour))
			f.event(t, 91, 2, "conversation_bot_handoff", f.since.Add(3*time.Hour))
		}
		f.event(t, 91, 2, "conversation_bot_resolved", f.since.Add(4*time.Hour))
		f.assertCounts(t, f.filter(), 3, 0, 1, 1, 33.3, 33.3)
	})
	t.Run("exact_current_and_previous_counts", func(t *testing.T) {
		f := newBotReportFixture(t, open)
		for id := uint(1); id <= 3; id++ {
			f.conversation(t, id, 91, 10, f.since.Add(time.Hour))
			f.conversation(t, id+3, 91, 10, f.since.Add(-time.Hour))
		}
		f.event(t, 91, 1, "conversation_bot_resolved", f.since.Add(time.Hour))
		f.event(t, 91, 2, "conversation_bot_handoff", f.since.Add(time.Hour))
		f.event(t, 91, 4, "conversation_bot_resolved", f.since.Add(-time.Hour))
		f.event(t, 91, 5, "conversation_bot_handoff", f.since.Add(-time.Hour))
		summary, err := f.service.GetBotSummary(91, f.filter())
		require.NoError(t, err)
		require.Equal(t, &BotSummaryReport{BotResolutionsCount: 1, BotHandoffsCount: 1, Previous: BotSummaryMetrics{BotResolutionsCount: 1, BotHandoffsCount: 1}}, summary)
	})
	t.Run("no_status_or_assignment_inference", func(t *testing.T) {
		f := newBotReportFixture(t, open)
		f.conversation(t, 1, 91, 10, f.since.Add(time.Hour))
		f.conversation(t, 2, 91, 10, f.since.Add(time.Hour))
		require.NoError(t, f.stores["CON"].Model(&domain.Conversation{}).Where("id = ?", 1).Update("status", domain.ConversationStatusResolved).Error)
		require.NoError(t, f.stores["CON"].Model(&domain.Conversation{}).Where("id = ?", 2).Update("assignee_id", uint(34)).Error)
		f.assertCounts(t, f.filter(), 2, 0, 0, 0, 0, 0)
	})
	for _, binding := range []string{"none", "orphan_bot", "foreign_bot", "foreign_inbox", "foreign_binding"} {
		t.Run("invalid_binding/"+binding, func(t *testing.T) {
			f := newBotReportFixture(t, open)
			require.NoError(t, f.stores["EXT"].Where("account_id = ?", 91).Delete(&domain.AgentBotInbox{}).Error)
			f.conversation(t, 1, 91, 10, f.since.Add(time.Hour))
			f.message(t, 91, 1, domain.MessageTypeOutgoing, f.since.Add(time.Hour))
			switch binding {
			case "orphan_bot":
				f.create(t, "EXT", &domain.AgentBotInbox{AccountID: 91, InboxID: 10, AgentBotID: 999})
			case "foreign_bot":
				f.create(t, "EXT", &domain.AgentBot{ID: 21, AccountID: 92, Name: "foreign"})
				f.create(t, "EXT", &domain.AgentBotInbox{AccountID: 91, InboxID: 10, AgentBotID: 21})
			case "foreign_inbox":
				require.NoError(t, f.stores["CHN"].Model(&domain.Inbox{}).Where("id = ?", 10).Update("account_id", uint(92)).Error)
				f.create(t, "EXT", &domain.AgentBotInbox{AccountID: 91, InboxID: 10, AgentBotID: 20})
			case "foreign_binding":
				f.create(t, "EXT", &domain.AgentBotInbox{AccountID: 92, InboxID: 10, AgentBotID: 20})
			}
			f.assertCounts(t, f.filter(), 0, 0, 0, 0, 0, 0)
		})
	}
	for _, period := range []string{"current", "previous"} {
		t.Run("event_query_failure/"+period, func(t *testing.T) {
			f := newBotReportFixture(t, open)
			f.conversation(t, 1, 91, 10, f.since.Add(time.Hour))
			f.conversation(t, 2, 91, 10, f.since.Add(-time.Hour))
			injected := errors.New("bot report injected query failure")
			require.NoError(t, f.stores["RPT"].Callback().Query().After("gorm:query").Register("bot_report_fail", func(db *gorm.DB) {
				if db.Statement.Table != "reporting_events" {
					return
				}
				for _, value := range db.Statement.Vars {
					if stamp, ok := value.(time.Time); ok && ((period == "current" && stamp.Equal(f.until)) || (period == "previous" && stamp.Equal(f.since.Add(-24*time.Hour)))) {
						db.AddError(injected)
						return
					}
				}
			}))
			if period == "current" {
				metrics, err := f.service.GetBotMetrics(91, f.filter())
				require.ErrorIs(t, err, injected)
				require.Nil(t, metrics)
			}
			summary, err := f.service.GetBotSummary(91, f.filter())
			require.ErrorIs(t, err, injected)
			require.Nil(t, summary)
		})
	}
	t.Run("time_and_owner_boundaries", func(t *testing.T) {
		f := newBotReportFixture(t, open)
		f.create(t, "CHN", &domain.Inbox{ID: 11, AccountID: 91, Name: "human inbox", WebsiteToken: "bot-test-11"})
		for i, at := range []time.Time{f.since.Add(-time.Second), f.since, f.until, f.until.Add(time.Second)} {
			f.conversation(t, uint(i+1), 91, 10, at)
		}
		f.conversation(t, 5, 91, 11, f.since.Add(time.Hour))
		f.conversation(t, 6, 92, 10, f.since.Add(time.Hour))
		for _, id := range []uint{1, 2, 3, 4, 5, 6} {
			f.message(t, 91, id, domain.MessageTypeOutgoing, f.since.Add(time.Hour))
		}
		f.message(t, 91, 2, domain.MessageTypeOutgoing, f.since)
		f.message(t, 91, 3, domain.MessageTypeOutgoing, f.until)
		f.message(t, 91, 2, domain.MessageTypeOutgoing, f.since.Add(-time.Second))
		f.message(t, 91, 3, domain.MessageTypeOutgoing, f.until.Add(time.Second))
		f.message(t, 91, 2, domain.MessageTypeIncoming, f.since.Add(time.Hour))
		f.message(t, 92, 2, domain.MessageTypeOutgoing, f.since.Add(time.Hour))
		f.event(t, 91, 2, "conversation_bot_resolved", f.since)
		f.event(t, 91, 3, "conversation_bot_handoff", f.until)
		f.event(t, 92, 2, "conversation_bot_handoff", f.since.Add(time.Hour))
		f.event(t, 91, 2, "conversation_bot_handoff", f.until.Add(time.Second))
		f.event(t, 91, 3, "conversation_bot_resolved", f.since.Add(-time.Second))
		f.assertCounts(t, f.filter(), 1, 2, 1, 0, 100, 0)
		summary, err := f.service.GetBotSummary(91, f.filter())
		require.NoError(t, err)
		require.Equal(t, BotSummaryMetrics{BotResolutionsCount: 1}, summary.Previous) // Half-open periods never share the since endpoint.
	})
	t.Run("empty_and_open_time_filters", func(t *testing.T) {
		f := newBotReportFixture(t, open)
		f.assertCounts(t, ReportFilter{}, 0, 0, 0, 0, 0, 0)
		f.conversation(t, 1, 91, 10, f.since.Add(-time.Hour))
		f.event(t, 91, 1, "conversation_bot_handoff", f.since.Add(-time.Hour))
		f.assertCounts(t, ReportFilter{}, 1, 0, 0, 1, 0, 100)
		f.assertCounts(t, ReportFilter{Until: &f.since}, 1, 0, 0, 1, 0, 100)
		summary, err := f.service.GetBotSummary(91, ReportFilter{Since: &f.since, BusinessHours: true})
		require.NoError(t, err)
		require.Equal(t, &BotSummaryReport{Previous: BotSummaryMetrics{BotHandoffsCount: 1}}, summary)
		start, end := f.until, f.since
		f.assertCounts(t, ReportFilter{Since: &start, Until: &end}, 0, 0, 0, 0, 0, 0)
	})
	t.Run("account_event_scope_is_independent_of_creation_cohort", func(t *testing.T) {
		f := newBotReportFixture(t, open)
		f.conversation(t, 1, 91, 10, f.since)
		f.conversation(t, 2, 91, 10, f.since.Add(-48*time.Hour))
		f.conversation(t, 3, 91, 999, f.since)
		for _, id := range []uint{1, 2, 3, 999} {
			f.event(t, 91, id, "conversation_bot_resolved", f.since.Add(time.Hour))
		}
		f.event(t, 92, 1, "conversation_bot_handoff", f.since.Add(time.Hour))
		f.assertCounts(t, f.filter(), 1, 0, 4, 0, 400, 0)
		require.NoError(t, f.stores["EXT"].Where("account_id = ?", 91).Delete(&domain.AgentBotInbox{}).Error)
		f.assertCounts(t, f.filter(), 0, 0, 4, 0, 0, 0)
	})
	for _, invalid := range []string{"null", "zero", "negative", "text", "fraction"} {
		t.Run("invalid_direct_association/"+invalid, func(t *testing.T) {
			f := newBotReportFixture(t, open)

			f.conversation(t, 1, 91, 10, f.since)
			f.event(t, 91, 1, "conversation_bot_resolved", f.since)
			var value any
			switch invalid {
			case "zero":
				value = 0
			case "negative":
				value = -1
			case "text":
				value = "private-invalid-id"
			case "fraction":
				value = 1.5
			}
			if f.stores["RPT"].Dialector.Name() == "postgres" && (invalid == "text" || invalid == "fraction") {
				// PostgreSQL cannot store a fractional/text bigint. Use invalid text input
				// here: a floating driver parameter can be coerced before storage.
				require.Error(t, f.stores["RPT"].Model(&domain.ReportingEvent{}).Where("account_id = ?", 91).Update("conversation_id", fmt.Sprint(value)).Error)
				f.assertCounts(t, f.filter(), 1, 0, 1, 0, 100, 0)
				return
			}
			require.NoError(t, f.stores["RPT"].Model(&domain.ReportingEvent{}).Where("account_id = ?", 91).Updates(map[string]any{"conversation_id": value, "metadata_json": `{"conversation_id":1}`}).Error)
			var before []map[string]any
			require.NoError(t, f.stores["RPT"].Table("reporting_events").Find(&before).Error)
			var queryLogs bytes.Buffer
			f.stores["RPT"].Config.Logger = logger.New(log.New(&queryLogs, "", 0), logger.Config{LogLevel: logger.Info})
			metrics, err := f.service.GetBotMetrics(91, f.filter())
			require.EqualError(t, err, "bot reporting events are unavailable")
			require.Nil(t, metrics)
			summary, err := f.service.GetBotSummary(91, f.filter())
			require.EqualError(t, err, "bot reporting events are unavailable")
			require.Nil(t, summary)
			require.Empty(t, queryLogs.String(), "event query diagnostics must not expose stored event values")
			var after []map[string]any
			require.NoError(t, f.stores["RPT"].Table("reporting_events").Find(&after).Error)
			require.Equal(t, before, after)
		})
	}
	t.Run("metadata_is_opaque_and_unselected_invalid_events_are_ignored", func(t *testing.T) {
		f := newBotReportFixture(t, open)
		f.conversation(t, 1, 91, 10, f.since)
		f.event(t, 91, 1, "conversation_bot_resolved", f.since)
		require.NoError(t, f.stores["RPT"].Model(&domain.ReportingEvent{}).Where("account_id = ?", 91).Update("metadata_json", `{"conversation_id":999,"conversation_id":"broken"`).Error)
		for _, event := range []domain.ReportingEvent{
			{AccountID: 92, Name: "conversation_bot_handoff", CreatedAt: f.since},
			{AccountID: 91, Name: "custom", CreatedAt: f.since},
			{AccountID: 91, Name: "conversation_bot_handoff", CreatedAt: f.until.Add(time.Second)},
		} {
			f.create(t, "RPT", &event)
		}
		f.assertCounts(t, f.filter(), 1, 0, 1, 0, 100, 0)
	})
	t.Run("previous_invalid_event_fails_summary_only", func(t *testing.T) {
		f := newBotReportFixture(t, open)
		f.conversation(t, 1, 91, 10, f.since.Add(time.Hour))
		f.event(t, 91, 1, "conversation_bot_resolved", f.since.Add(time.Hour))
		f.create(t, "RPT", &domain.ReportingEvent{AccountID: 91, Name: "conversation_bot_handoff", CreatedAt: f.since.Add(-time.Hour), MetadataJSON: `{"conversation_id":1}`})
		metrics, err := f.service.GetBotMetrics(91, f.filter())
		require.NoError(t, err)
		require.Equal(t, float64(100), metrics.ResolutionRate)
		summary, err := f.service.GetBotSummary(91, f.filter())
		require.EqualError(t, err, "bot reporting events are unavailable")
		require.Nil(t, summary)
	})
	t.Run("zero_width_is_empty_and_null_event_times_keep_existing_semantics", func(t *testing.T) {
		f := newBotReportFixture(t, open)
		f.conversation(t, 1, 91, 10, f.since)
		f.event(t, 91, 1, "conversation_bot_resolved", f.since)
		f.assertCounts(t, ReportFilter{Since: &f.since, Until: &f.since}, 0, 0, 0, 0, 0, 0)
		require.NoError(t, f.stores["RPT"].Model(&domain.ReportingEvent{}).Where("account_id = ?", 91).Update("created_at", nil).Error)
		f.assertCounts(t, f.filter(), 1, 0, 0, 0, 0, 0)
		f.assertCounts(t, ReportFilter{}, 1, 0, 1, 0, 100, 0)
	})
	t.Run("concurrent_reads_preserve_exact_counts", func(t *testing.T) {
		f := newBotReportFixture(t, open)
		for id := uint(1); id <= 3; id++ {
			f.conversation(t, id, 91, 10, f.since)
		}
		f.event(t, 91, 1, "conversation_bot_resolved", f.since.Add(time.Hour))
		f.event(t, 91, 2, "conversation_bot_handoff", f.since.Add(time.Hour))
		var group sync.WaitGroup
		errors := make(chan error, 12)
		for i := 0; i < 12; i++ {
			group.Add(1)
			go func() {
				defer group.Done()
				got, err := f.service.GetBotSummary(91, f.filter())
				if err == nil && (got.BotResolutionsCount != 1 || got.BotHandoffsCount != 1) {
					err = fmt.Errorf("unexpected concurrent summary: %+v", got)
				}
				errors <- err
			}()
		}
		group.Wait()
		close(errors)
		for err := range errors {
			require.NoError(t, err)
		}
	})
	t.Run("owner_stores_are_not_joined", func(t *testing.T) {
		f := newBotReportFixture(t, open)
		f.conversation(t, 1, 91, 10, f.since)
		f.event(t, 91, 1, "conversation_bot_resolved", f.since)
		f.assertCounts(t, f.filter(), 1, 0, 1, 0, 100, 0)
		for _, model := range []any{&domain.Conversation{}, &domain.Inbox{}, &domain.Message{}, &domain.AgentBot{}, &domain.AgentBotInbox{}} {
			require.False(t, f.stores["RPT"].Migrator().HasTable(model), fmt.Sprintf("RPT acquired foreign table %T", model))
		}
		require.False(t, f.stores["EXT"].Migrator().HasTable(&domain.Inbox{}))
	})
}
