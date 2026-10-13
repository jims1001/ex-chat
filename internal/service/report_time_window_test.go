package service

import (
	"fmt"
	"path/filepath"
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

type reportWindowFixture struct {
	service *ReportService
	stores  map[string]*gorm.DB
}

// A repository can return the same absolute instant in a non-UTC location.
// Display shifting must not apply that stored representation's offset twice.
type reportWindowLocatedConversations struct {
	reportConversationPort
	location *time.Location
}

func (r reportWindowLocatedConversations) ListForReport(account uint, since, until *time.Time) ([]domain.Conversation, error) {
	rows, err := r.reportConversationPort.ListForReport(account, since, until)
	for i := range rows {
		rows[i].CreatedAt = rows[i].CreatedAt.In(r.location)
	}
	return rows, err
}

func newReportWindowFixture(t *testing.T, open func(*testing.T, string) *gorm.DB) *reportWindowFixture {
	t.Helper()
	f := &reportWindowFixture{stores: map[string]*gorm.DB{}}
	for _, owner := range []string{"RPT", "CON", "MSG", "CUS", "CHN"} {
		db := open(t, owner)
		require.NoError(t, database.AutoMigrateModule(db, owner))
		f.stores[owner] = db
	}
	f.service = NewReportService(f.stores["RPT"])
	f.service.SetOwnerRepositories(repository.NewConversationRepository(f.stores["CON"]), repository.NewInboxRepository(f.stores["CHN"]), repository.NewMessageRepository(f.stores["MSG"]), repository.NewContactRepository(f.stores["CUS"]), nil)
	f.service.SetSummaryMessageOwner(repository.NewMessageRepository(f.stores["MSG"]))
	f.create(t, "CHN", &domain.Inbox{ID: 10, AccountID: 91, Name: "window inbox", WebsiteToken: "window-inbox-10"})
	return f
}

func (f *reportWindowFixture) create(t *testing.T, owner string, value any) {
	t.Helper()
	require.NoError(t, f.stores[owner].Create(value).Error)
}

func (f *reportWindowFixture) seed(t *testing.T, id, account uint, at time.Time) {
	t.Helper()
	user := uint(7)
	f.create(t, "CON", &domain.Conversation{ID: id, AccountID: account, InboxID: 10, ContactID: id, DisplayID: id, AssigneeID: &user, Status: domain.ConversationStatusOpen, CreatedAt: at})
	f.create(t, "MSG", &domain.Message{ID: id, AccountID: account, ConversationID: id, MessageType: domain.MessageTypeOutgoing, CreatedAt: at})
	f.create(t, "CUS", &domain.Contact{ID: id, AccountID: account, Name: fmt.Sprint(id), CreatedAt: at})
	f.create(t, "RPT", &domain.ReportingEvent{ID: id, AccountID: account, ConversationID: &id, UserID: &user, Name: "conversation_bot_resolved", CreatedAt: at})
}

func (f *reportWindowFixture) assertOwnerIDs(t *testing.T, start, end *time.Time, ids []uint) {
	t.Helper()
	convs, err := f.service.conversations.ListForReport(91, start, end)
	require.NoError(t, err)
	convIDs := []uint{}
	for _, c := range convs {
		convIDs = append(convIDs, c.ID)
	}
	require.Equal(t, ids, convIDs, "CON")
	messages, err := f.service.messages.ListForReport(91, nil, start, end)
	require.NoError(t, err)
	msgIDs := []uint{}
	for _, m := range messages {
		msgIDs = append(msgIDs, m.ID)
	}
	require.Equal(t, ids, msgIDs, "MSG nil selects all account messages")
	contacts, err := f.service.contacts.CountForReport(91, start, end)
	require.NoError(t, err)
	require.Equal(t, int64(len(ids)), contacts, "CUS")
	outcomes, err := f.service.getBotOutcomeCounts(91, ReportFilter{Since: start, Until: end})
	require.NoError(t, err)
	require.Equal(t, BotSummaryMetrics{BotResolutionsCount: int64(len(ids))}, outcomes, "RPT")
}

func TestReportTimeWindows(t *testing.T) {
	open := func(t *testing.T, owner string) *gorm.DB {
		db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), owner+".db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		conn, err := db.DB()
		require.NoError(t, err)
		conn.SetMaxOpenConns(1)
		t.Cleanup(func() { require.NoError(t, conn.Close()) })
		return db
	}
	runReportTimeWindows(t, open)
	t.Run("sqlite_matching_offset_storage", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			offset int
		}{{"positive", 9 * 60 * 60}, {"negative_fractional", -3*60*60 - 30*60}} {
			t.Run(tc.name, func(t *testing.T) {
				f := newReportWindowFixture(t, open)
				location := time.FixedZone(tc.name, tc.offset)
				since := time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC).In(location)
				until := since.Add(time.Hour)
				for i, at := range []time.Time{since.Add(-time.Microsecond), since, until.Add(-time.Microsecond), until} {
					f.seed(t, uint(i+1), 91, at)
				}
				f.assertOwnerIDs(t, &since, &until, []uint{2, 3})
			})
		}
	})
}

func runReportTimeWindows(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	t.Run("precision_carry_across_all_owners", func(t *testing.T) {
		f := newReportWindowFixture(t, open)
		for i, tc := range []struct {
			name string
			base time.Time
		}{
			{"exact_microsecond", time.Date(2024, 2, 29, 12, 30, 0, 250000000, time.UTC)},
			{"second_carry", time.Date(2024, 2, 29, 12, 30, 0, 999999000, time.UTC)},
			{"leap_day_carry", time.Date(2024, 2, 29, 23, 59, 59, 999999000, time.UTC)},
			{"pre_epoch_carry", time.Date(1969, 12, 31, 23, 59, 59, 999999000, time.UTC)},
		} {
			t.Run(tc.name, func(t *testing.T) {
				first := uint(i*10 + 1)
				for j, at := range []time.Time{tc.base.Add(-time.Microsecond), tc.base, tc.base.Add(time.Microsecond)} {
					f.seed(t, first+uint(j), 91, at)
					f.seed(t, first+uint(j)+100, 92, at)
				}
				left, right := tc.base.Add(-2*time.Microsecond), tc.base.Add(2*time.Microsecond)
				for _, fraction := range []time.Duration{time.Nanosecond, 999 * time.Nanosecond} {
					boundary := tc.base.Add(fraction)
					f.assertOwnerIDs(t, &boundary, &right, []uint{first + 2})
					f.assertOwnerIDs(t, &left, &boundary, []uint{first, first + 1})
				}
				exactEnd := tc.base.Add(time.Microsecond)
				f.assertOwnerIDs(t, &tc.base, &exactEnd, []uint{first + 1})
			})
		}
	})
	t.Run("four_owner_predicates", func(t *testing.T) {
		f := newReportWindowFixture(t, open)
		since := time.Date(2024, 2, 29, 12, 30, 0, 250000000, time.UTC)
		until := since.Add(6 * time.Hour)
		sincePlusOneNS, sincePlus999NS := since.Add(time.Nanosecond), since.Add(999*time.Nanosecond)
		sinceOffset := since.In(time.FixedZone("start offset", 9*60*60))
		untilOffset := until.In(time.FixedZone("end offset", -3*60*60-30*60))
		for i, at := range []time.Time{since.Add(-time.Microsecond), since, until.Add(-time.Microsecond), until, until.Add(time.Microsecond)} {
			f.seed(t, uint(i+1), 91, at)
			f.seed(t, uint(i+101), 92, at)
		}
		for _, tc := range []struct {
			name       string
			start, end *time.Time
			ids        []uint
		}{
			{"bounded", &since, &until, []uint{2, 3}},
			{"unbounded", nil, nil, []uint{1, 2, 3, 4, 5}},
			{"since_only", &since, nil, []uint{2, 3, 4, 5}},
			{"until_only", nil, &until, []uint{1, 2, 3}},
			{"equal", &since, &since, []uint{}},
			{"reversed", &until, &since, []uint{}},
			{"adjacent_left", nil, &since, []uint{1}},
			{"adjacent_right", &until, nil, []uint{4, 5}},
			{"since_plus_1ns", &sincePlusOneNS, nil, []uint{3, 4, 5}},
			{"since_plus_999ns", &sincePlus999NS, nil, []uint{3, 4, 5}},
			{"until_plus_1ns", nil, &sincePlusOneNS, []uint{1, 2}},
			{"until_plus_999ns", nil, &sincePlus999NS, []uint{1, 2}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				f.assertOwnerIDs(t, tc.start, tc.end, tc.ids)
			})
		}
		// PostgreSQL compares absolute instants across timestamp representations.
		// SQLite's pre-existing stored-text representation limit is separately
		// covered by matching-offset compatibility cases and exploratory evidence.
		if f.stores["CON"].Dialector.Name() == "postgres" {
			t.Run("equivalent_fixed_offsets", func(t *testing.T) {
				f.assertOwnerIDs(t, &sinceOffset, &untilOffset, []uint{2, 3})
			})
		}
		t.Run("message_id_scope", func(t *testing.T) {
			for _, ids := range [][]uint{{}, {1, 4, 101, 102}, {999}} {
				got, err := f.service.messages.ListForReport(91, ids, &since, &until)
				require.NoError(t, err)
				require.Empty(t, got)
			}
			got, err := f.service.messages.ListForReport(91, []uint{2, 3, 102, 103}, &since, &until)
			require.NoError(t, err)
			require.Len(t, got, 2)
			require.Equal(t, []uint{2, 3}, []uint{got[0].ID, got[1].ID})
		})
		t.Run("adjacent_bot_periods", func(t *testing.T) {
			got, err := f.service.GetBotSummary(91, ReportFilter{Since: &since, Until: &until})
			require.NoError(t, err)
			require.Equal(t, &BotSummaryReport{BotResolutionsCount: 2, Previous: BotSummaryMetrics{BotResolutionsCount: 1}}, got)
		})
		t.Run("separate_owner_tables", func(t *testing.T) {
			for _, model := range []any{&domain.Conversation{}, &domain.Message{}, &domain.Contact{}, &domain.Inbox{}} {
				require.False(t, f.stores["RPT"].Migrator().HasTable(model))
			}
			require.False(t, f.stores["CON"].Migrator().HasTable(&domain.Message{}))
			require.False(t, f.stores["MSG"].Migrator().HasTable(&domain.Conversation{}))
			require.False(t, f.stores["CUS"].Migrator().HasTable(&domain.Conversation{}))
		})
	})
	t.Run("exact_trends_and_partial_day_labels", func(t *testing.T) {
		f := newReportWindowFixture(t, open)
		since := time.Date(2024, 2, 29, 18, 0, 0, 0, time.UTC)
		until := time.Date(2024, 3, 1, 6, 0, 0, 0, time.UTC)
		previous := since.Add(-until.Sub(since))
		for i, at := range []time.Time{previous.Add(-time.Microsecond), previous, since.Add(-time.Microsecond), since, until.Add(-time.Microsecond), until, until.Add(time.Hour)} {
			f.seed(t, uint(i+1), 91, at)
		}
		filter := ReportFilter{Since: &since, Until: &until}
		got, err := f.service.GetConversationTrends(91, 99, filter)
		require.NoError(t, err)
		require.Equal(t, &ConversationTrendsReport{CurrentPeriodTotal: 2, PreviousPeriodTotal: 2, GrowthPercentage: 0, Trends: []TrendPoint{{Date: "2024-02-29", Count: 1}, {Date: "2024-03-01", Count: 1}}}, got)
		summary, err := f.service.GetAccountSummary(91, filter)
		require.NoError(t, err)
		require.Equal(t, got.CurrentPeriodTotal, summary.ConversationsCount)
		for _, end := range []time.Time{since, since.Add(-time.Hour)} {
			got, err := f.service.GetConversationTrends(91, 7, ReportFilter{Since: &since, Until: &end})
			require.NoError(t, err)
			require.Equal(t, &ConversationTrendsReport{Trends: []TrendPoint{}}, got)
		}
		midnight := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
		got, err = f.service.GetConversationTrends(91, 7, ReportFilter{Since: &since, Until: &midnight})
		require.NoError(t, err)
		require.Equal(t, []TrendPoint{{Date: "2024-02-29", Count: 1}}, got.Trends)
	})
	t.Run("trends_missing_bound_days_fallback", func(t *testing.T) {
		f := newReportWindowFixture(t, open)
		old := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
		for _, tc := range []struct {
			days   int
			filter ReportFilter
			want   int
		}{
			{3, ReportFilter{}, 3}, {2, ReportFilter{Since: &old}, 2}, {2, ReportFilter{Until: &old}, 2}, {0, ReportFilter{}, 7},
		} {
			before := time.Now().UTC().Format("2006-01-02")
			got, err := f.service.GetConversationTrends(91, tc.days, tc.filter)
			require.NoError(t, err)
			after := time.Now().UTC().Format("2006-01-02")
			require.Len(t, got.Trends, tc.want)
			require.Contains(t, []string{before, after}, got.Trends[len(got.Trends)-1].Date)
			require.Zero(t, got.CurrentPeriodTotal)
			require.Zero(t, got.PreviousPeriodTotal)
		}
	})
	t.Run("traffic_intersecting_display_days", func(t *testing.T) {
		f := newReportWindowFixture(t, open)
		since := time.Date(2024, 2, 29, 0, 0, 0, 0, time.UTC)
		until := since.AddDate(0, 0, 1)
		f.seed(t, 1, 91, since)
		f.seed(t, 2, 91, until.Add(-time.Microsecond))
		f.seed(t, 3, 91, until)
		f.seed(t, 4, 92, since)
		for _, tc := range []struct {
			name   string
			offset float64
			dates  []any
			hours  []int
		}{
			{"utc", 0, []any{"2024-02-29"}, []int{0, 23}},
			{"positive", 8, []any{"2024-02-29", "2024-03-01"}, []int{8, 7}},
			{"negative", -8, []any{"2024-02-28", "2024-02-29"}, []int{16, 15}},
			{"fractional_positive", 5.5, []any{"2024-02-29", "2024-03-01"}, []int{5, 5}},
			{"fractional_negative", -3.5, []any{"2024-02-28", "2024-02-29"}, []int{20, 20}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				got, err := f.service.GetConversationTraffic(91, ReportFilter{Since: &since, Until: &until}, tc.offset)
				require.NoError(t, err)
				require.Len(t, got, 25)
				require.Equal(t, append([]any{"Start of the hour"}, tc.dates...), got[0])
				var total int64
				for _, row := range got[1:] {
					for _, value := range row[1:] {
						total += value.(int64)
					}
				}
				require.Equal(t, int64(2), total)
				require.Equal(t, int64(1), got[tc.hours[0]+1][1])
				require.Equal(t, int64(1), got[tc.hours[1]+1][len(tc.dates)])
			})
		}
		t.Run("partial_final_day", func(t *testing.T) {
			start := since.Add(18 * time.Hour)
			end := until.Add(6 * time.Hour)
			got, err := f.service.GetConversationTraffic(91, ReportFilter{Since: &start, Until: &end}, 0)
			require.NoError(t, err)
			require.Equal(t, []any{"Start of the hour", "2024-02-29", "2024-03-01"}, got[0])
			require.Equal(t, int64(1), got[24][1])
			require.Equal(t, int64(1), got[1][2])
		})
		t.Run("empty_windows", func(t *testing.T) {
			for _, end := range []time.Time{since, since.Add(-time.Hour)} {
				got, err := f.service.GetConversationTraffic(91, ReportFilter{Since: &since, Until: &end}, 5.5)
				require.NoError(t, err)
				require.Len(t, got, 25)
				for _, row := range got {
					require.Len(t, row, 1)
				}
			}
		})
		t.Run("stored_timestamp_offset", func(t *testing.T) {
			f.service.conversations = reportWindowLocatedConversations{reportConversationPort: f.service.conversations, location: time.FixedZone("stored offset", 9*60*60)}
			got, err := f.service.GetConversationTraffic(91, ReportFilter{Since: &since, Until: &until}, -5.5)
			require.NoError(t, err)
			require.Equal(t, []any{"Start of the hour", "2024-02-28", "2024-02-29"}, got[0])
			require.Equal(t, int64(1), got[19][1])
			require.Equal(t, int64(1), got[19][2])
		})
	})
	t.Run("annual_and_drilldown_exclusive_metadata", func(t *testing.T) {
		f := newReportWindowFixture(t, open)
		start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
		end := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		for i, at := range []time.Time{start.Add(-time.Microsecond), start, end.Add(-time.Microsecond), end, end.Add(time.Microsecond)} {
			f.seed(t, uint(i+1), 91, at)
		}
		f.seed(t, 101, 92, end.Add(-time.Microsecond))
		user, conv := uint(7), uint(2)
		for i, at := range []time.Time{start.Add(-time.Microsecond), start, end.Add(-time.Microsecond), end, end.Add(time.Microsecond)} {
			value := float64(1000)
			if i == 1 {
				value = 10
			}
			if i == 2 {
				value = 30
			}
			f.create(t, "RPT", &domain.ReportingEvent{ID: uint(201 + i), AccountID: 91, ConversationID: &conv, UserID: &user, Name: "first_response", Value: value, CreatedAt: at})
		}
		f.create(t, "RPT", &domain.ReportingEvent{ID: 301, AccountID: 92, UserID: &user, Name: "first_response", Value: 1000, CreatedAt: start})
		other := uint(8)
		f.create(t, "RPT", &domain.ReportingEvent{ID: 302, AccountID: 91, UserID: &other, Name: "first_response", Value: 1000, CreatedAt: start})
		year, err := f.service.GetYearInReview(91, user, 2024)
		require.NoError(t, err)
		require.Equal(t, int64(2), year.TotalConversations)
		require.Equal(t, 20, year.SupportPersonality.AvgResponseTimeSeconds)
		bucket := end.Add(-time.Hour)
		ignoredStart := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
		for _, metric := range []string{"conversations_count", "outgoing_messages_count"} {
			got, err := f.service.GetDrilldown(91, metric, "account", nil, bucket, ReportFilter{Since: &ignoredStart, Until: &ignoredStart, BusinessHours: true}, 1, 25)
			require.NoError(t, err)
			require.Equal(t, end.AddDate(0, 0, -1).Unix(), got.Meta.Bucket.Since)
			require.Equal(t, end.Unix(), got.Meta.Bucket.Until)
			require.Equal(t, int64(1), got.Meta.TotalCount)
			require.Equal(t, int64(1), got.Meta.ConversationCount)
			require.Len(t, got.Payload, 1)
		}
		// The message cohort still consists only of conversations created in the bucket.
		f.create(t, "MSG", &domain.Message{ID: 201, AccountID: 91, ConversationID: 2, MessageType: domain.MessageTypeOutgoing, CreatedAt: bucket})
		got, err := f.service.GetDrilldown(91, "outgoing_messages_count", "account", nil, bucket, ReportFilter{}, 1, 25)
		require.NoError(t, err)
		require.Equal(t, int64(1), got.Meta.TotalCount)
	})
}
