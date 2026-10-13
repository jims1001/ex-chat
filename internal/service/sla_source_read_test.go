package service

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type slaReadFixture struct {
	stores  map[string]*gorm.DB
	service *SLAService
	conv    domain.Conversation
	policy  domain.SLAPolicy
	start   time.Time
}

func newSLAReadFixture(t *testing.T, open func(*testing.T, string) *gorm.DB) *slaReadFixture {
	t.Helper()
	f := &slaReadFixture{stores: map[string]*gorm.DB{}, start: time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)}
	for _, owner := range []string{"RPT", "CON", "CHN", "MSG"} {
		f.stores[owner] = open(t, owner)
		require.NoError(t, database.AutoMigrateModule(f.stores[owner], owner))
	}
	f.policy = domain.SLAPolicy{AccountID: 91, Name: "60 second SLA", FirstResponseTimeThreshold: 60, NextResponseTimeThreshold: 60, ResolutionTimeThreshold: 7200}
	require.NoError(t, f.stores["RPT"].Create(&f.policy).Error)
	f.conv = domain.Conversation{ID: 1, DisplayID: 1, AccountID: 91, InboxID: 10, ContactID: 1, SLAPolicyID: &f.policy.ID, Status: domain.ConversationStatusOpen, CreatedAt: f.start, UpdatedAt: f.start, LastActivityAt: f.start}
	require.NoError(t, f.stores["CON"].Create(&f.conv).Error)
	require.NoError(t, f.stores["CHN"].Create(&domain.Inbox{ID: 10, AccountID: 91, Name: "SLA inbox", WebsiteToken: "sla-source"}).Error)
	f.service = NewSLAService(f.stores["RPT"], repository.NewConversationRepository(f.stores["CON"]))
	f.service.SetOwnerRepositories(repository.NewInboxRepository(f.stores["CHN"]), repository.NewMessageRepository(f.stores["MSG"]))
	return f
}

func (f *slaReadFixture) add(t *testing.T, conv uint, offset time.Duration, kind, sender, status string, private bool) domain.Message {
	t.Helper()
	m := domain.Message{AccountID: 91, ConversationID: conv, MessageType: kind, SenderType: sender, Status: status, Private: private, ContentType: "text", Content: "SLA source fixture", CreatedAt: f.start.Add(offset)}
	require.NoError(t, f.stores["MSG"].Create(&m).Error)
	return m
}

type slaStoredSnapshot struct {
	Conversations []domain.Conversation
	Applied       []domain.AppliedSLA
	Events        []domain.SLAEvent
	Breaches      []domain.SLABreachLog
}

func (f *slaReadFixture) snapshot(t *testing.T) slaStoredSnapshot {
	t.Helper()
	var got slaStoredSnapshot
	require.NoError(t, f.stores["CON"].Order("id").Find(&got.Conversations).Error)
	require.NoError(t, f.stores["RPT"].Order("id").Find(&got.Applied).Error)
	require.NoError(t, f.stores["RPT"].Order("id").Find(&got.Events).Error)
	require.NoError(t, f.stores["RPT"].Order("id").Find(&got.Breaches).Error)
	return got
}

type slaTimelineStub struct {
	rows             []domain.Message
	err              error
	calls            int
	delegate         *repository.MessageRepository
	failConversation uint
}

func (s *slaTimelineStub) ListSLATimeline(account, conversation uint) ([]domain.Message, error) {
	s.calls++
	if s.failConversation != 0 && s.failConversation != conversation {
		return s.delegate.ListSLATimeline(account, conversation)
	}
	return s.rows, s.err
}

type slaInboxStub struct {
	row *domain.Inbox
	err error
}

func (s slaInboxStub) FindSummaryByID(uint, uint) (*domain.Inbox, error) { return s.row, s.err }

func TestSLASourceReadOwnerStores(t *testing.T) {
	runSLASourceReadOwnerStores(t, openSummaryMessageSQLite)
}
func TestPostgresSLASourceReadOwnerStores(t *testing.T) {
	runSLASourceReadOwnerStores(t, reportWindowTestPostgres(t))
}

func runSLASourceReadOwnerStores(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	for _, existing := range []bool{false, true} {
		for _, source := range []string{"timeline_empty_error", "timeline_partial_error", "inbox_empty_error", "inbox_partial_error", "policy_error", "first_breach_error", "next_breach_error", "resolution_breach_error", "applied_error", "events_error"} {
			if source == "events_error" && !existing {
				continue
			}
			t.Run(fmt.Sprintf("preflight/existing_%t/%s", existing, source), func(t *testing.T) {
				f := newSLAReadFixture(t, open)
				f.add(t, 1, 0, "incoming", "Contact", "sent", false)
				f.add(t, 1, 30*time.Second, "outgoing", "User", "sent", false)
				if existing {
					applied, err := f.service.appliedSLARepo.EnsureAppliedSLA(91, 1, f.policy.ID)
					require.NoError(t, err)
					require.NoError(t, f.service.appliedSLARepo.RecordSLAEvent(&domain.SLAEvent{AppliedSLAID: applied.ID, AccountID: 91, ConversationID: 1, SLAPolicyID: f.policy.ID, EventType: "rt", CreatedAt: f.start}))
					require.NoError(t, f.stores["RPT"].Model(&domain.AppliedSLA{}).Where("id = ?", applied.ID).Update("sla_status", "active_with_misses").Error)
					// A different selected policy would otherwise mutate the existing application.
					other := f.policy
					other.ID = 0
					other.Name = "alternate"
					require.NoError(t, f.stores["RPT"].Create(&other).Error)
					f.conv.SLAPolicyID = &other.ID
				}
				before := f.snapshot(t)
				ownerErr := errors.New("SLA required-read sentinel")
				var remove func()
				switch source {
				case "timeline_empty_error", "timeline_partial_error":
					rows := []domain.Message(nil)
					if source == "timeline_partial_error" {
						rows = []domain.Message{{ID: 101, MessageType: "incoming", CreatedAt: f.start}}
					}
					f.service.messages = &slaTimelineStub{rows: rows, err: ownerErr}
				case "inbox_empty_error", "inbox_partial_error":
					var row *domain.Inbox
					if source == "inbox_partial_error" {
						row = &domain.Inbox{ID: 10, AccountID: 91}
					}
					f.service.inboxes = slaInboxStub{row: row, err: ownerErr}
				default:
					table, at := "sla_policies", 1
					switch source {
					case "first_breach_error":
						table = "sla_breach_logs"
					case "next_breach_error":
						table = "sla_breach_logs"
						at = 2
					case "resolution_breach_error":
						table = "sla_breach_logs"
						at = 3
					case "applied_error":
						table = "applied_slas"
					case "events_error":
						table = "sla_events"
					}
					seen := 0
					require.NoError(t, f.stores["RPT"].Callback().Query().Before("gorm:query").Register("sla_read_fault", func(tx *gorm.DB) {
						if tx.Statement.Table == table {
							seen++
							if seen == at {
								_ = tx.AddError(ownerErr)
							}
						}
					}))
					remove = func() { require.NoError(t, f.stores["RPT"].Callback().Query().Remove("sla_read_fault")) }
				}
				var writes []string
				for owner, db := range f.stores {
					owner := owner
					record := func(tx *gorm.DB) { writes = append(writes, owner+":"+tx.Statement.Table) }
					require.NoError(t, db.Callback().Create().Before("gorm:create").Register("sla_preflight_writes", record))
					require.NoError(t, db.Callback().Update().Before("gorm:update").Register("sla_preflight_writes", record))
				}
				got, err := f.service.EvaluateConversation(&f.conv)
				require.ErrorIs(t, err, ownerErr)
				require.Nil(t, got)
				require.Empty(t, writes, "required reads must all finish before even EnsureAppliedSLA")
				require.Nil(t, f.conv.Inbox, "failed preflight must not partially hydrate the caller")
				if remove != nil {
					remove()
				}
				require.Equal(t, before, f.snapshot(t))
				for _, db := range f.stores {
					require.NoError(t, db.Callback().Create().Remove("sla_preflight_writes"))
					require.NoError(t, db.Callback().Update().Remove("sla_preflight_writes"))
				}
				f.service.SetOwnerRepositories(repository.NewInboxRepository(f.stores["CHN"]), repository.NewMessageRepository(f.stores["MSG"]))
				got, err = f.service.EvaluateConversation(&f.conv)
				require.NoError(t, err)
				require.Empty(t, got, "30 second human reply stays timely after recovery at 10 minutes")
				require.NotNil(t, f.conv.Inbox, "healthy owner hydration remains compatible")
				require.Equal(t, uint(10), f.conv.Inbox.ID)
				after := f.snapshot(t)
				require.Empty(t, after.Breaches)
				if existing {
					require.Len(t, after.Events, 1)
					require.Equal(t, "active_with_misses", after.Applied[0].SLAStatus)
				} else {
					require.Empty(t, after.Events)
					require.Equal(t, "active", after.Applied[0].SLAStatus)
				}
			})
		}
	}
	for _, failure := range []string{"policy", "inbox", "timeline", "first_breach", "next_breach", "resolution_breach"} {
		t.Run("deadline/"+failure, func(t *testing.T) {
			f := newSLAReadFixture(t, open)
			f.add(t, 1, 0, "incoming", "Contact", "sent", false)
			ownerErr := errors.New("deadline source unavailable")
			if failure == "inbox" {
				f.service.inboxes = slaInboxStub{err: ownerErr}
			} else if failure == "timeline" {
				f.service.messages = &slaTimelineStub{rows: []domain.Message{{MessageType: "incoming", CreatedAt: f.start}}, err: ownerErr}
			} else {
				table, at := "sla_breach_logs", 1
				switch failure {
				case "policy":
					table = "sla_policies"
				case "next_breach":
					at = 2
				case "resolution_breach":
					at = 3
				}
				seen := 0
				require.NoError(t, f.stores["RPT"].Callback().Query().Before("gorm:query").Register("sla_deadline_fault", func(tx *gorm.DB) {
					if tx.Statement.Table == table {
						seen++
						if seen == at {
							_ = tx.AddError(ownerErr)
						}
					}
				}))
				defer func() { require.NoError(t, f.stores["RPT"].Callback().Query().Remove("sla_deadline_fault")) }()
			}
			frt, nrt, rt, fb, nb, rb, policy, err := f.service.GetConversationSLADeadlines(&f.conv)
			require.ErrorIs(t, err, ownerErr)
			require.Nil(t, frt)
			require.Nil(t, nrt)
			require.Nil(t, rt)
			require.False(t, fb)
			require.False(t, nb)
			require.False(t, rb)
			require.Nil(t, policy)
			after := f.snapshot(t)
			require.Empty(t, after.Applied)
			require.Empty(t, after.Events)
			require.Empty(t, after.Breaches)
		})
	}
	t.Run("one_snapshot_for_frt_nrt_and_metadata", func(t *testing.T) {
		f := newSLAReadFixture(t, open)
		rows := []domain.Message{{ID: 1, MessageType: "incoming", CreatedAt: f.start}, {ID: 2, MessageType: "outgoing", SenderType: "User", CreatedAt: f.start.Add(30 * time.Second)}, {ID: 3, MessageType: "incoming", CreatedAt: f.start.Add(time.Minute)}, {ID: 4, MessageType: "incoming", CreatedAt: f.start.Add(2 * time.Minute)}}
		owner := &slaTimelineStub{rows: rows}
		f.service.messages = owner
		got, err := f.service.EvaluateConversation(&f.conv)
		require.NoError(t, err)
		require.Equal(t, 1, owner.calls)
		require.Len(t, got, 1)
		require.Equal(t, "next_response", got[0].BreachType)
		after := f.snapshot(t)
		require.Len(t, after.Events, 1)
		require.Equal(t, `{"message_id":4}`, after.Events[0].Meta)
		require.Equal(t, f.start.Add(2*time.Minute).Unix(), after.Conversations[0].NextResponseDueAt.Unix(), "existing NRT turn starts at first waiting customer message")
		_, _, _, _, _, _, _, err = f.service.GetConversationSLADeadlines(&f.conv)
		require.NoError(t, err)
		require.Equal(t, 2, owner.calls, "each separate deadline evaluation reads the timeline once")
	})
	t.Run("business_hours_failure_does_not_use_wallclock", func(t *testing.T) {
		f := newSLAReadFixture(t, open)
		// Start/response straddle a closed period: wall-clock is late, business time timely.
		f.start = time.Date(2024, 1, 19, 17, 0, 0, 0, time.UTC)
		f.conv.CreatedAt = f.start
		f.conv.UpdatedAt = time.Date(2024, 1, 22, 9, 0, 30, 0, time.UTC)
		f.conv.Status = domain.ConversationStatusResolved
		require.NoError(t, f.stores["CON"].Model(&domain.Conversation{}).Where("id = ?", f.conv.ID).UpdateColumns(map[string]any{
			"created_at": f.conv.CreatedAt, "updated_at": f.conv.UpdatedAt, "status": f.conv.Status,
		}).Error)
		require.NoError(t, f.stores["RPT"].Model(&f.policy).Update("only_during_business_hours", true).Error)
		hours := `[{"day_of_week":1,"open_hour":9,"close_hour":17},{"day_of_week":5,"open_hour":9,"close_hour":17}]`
		require.NoError(t, f.stores["CHN"].Model(&domain.Inbox{}).Where("id = ?", 10).Updates(map[string]any{"working_hours_enabled": true, "timezone": "UTC", "working_hours": hours}).Error)
		f.add(t, 1, 0, "incoming", "Contact", "sent", false)
		f.add(t, 1, f.conv.UpdatedAt.Sub(f.start), "outgoing", "User", "sent", false)
		before := f.snapshot(t)
		ownerErr := errors.New("CHN outage")
		f.service.inboxes = slaInboxStub{err: ownerErr}
		got, err := f.service.EvaluateConversation(&f.conv)
		require.ErrorIs(t, err, ownerErr)
		require.Nil(t, got)
		require.Equal(t, before, f.snapshot(t))
		f.service.inboxes = repository.NewInboxRepository(f.stores["CHN"])
		got, err = f.service.EvaluateConversation(&f.conv)
		require.NoError(t, err)
		require.Empty(t, got)
		after := f.snapshot(t)
		require.Empty(t, after.Breaches)
		require.Empty(t, after.Events)
		require.Equal(t, "hit", after.Applied[0].SLAStatus)
		require.Equal(t, time.Date(2024, 1, 22, 9, 1, 0, 0, time.UTC).Unix(), after.Conversations[0].FirstResponseDueAt.Unix())
		require.Equal(t, time.Date(2024, 1, 22, 11, 0, 0, 0, time.UTC).Unix(), after.Conversations[0].ResolutionDueAt.Unix())
	})
	for _, mode := range []string{"empty_history", "missing_inbox", "bot_private_failed", "late_human", "on_time_human", "no_policy", "nil_conversation", "unconfigured_message", "unconfigured_inbox"} {
		t.Run("compatibility/"+mode, func(t *testing.T) {
			f := newSLAReadFixture(t, open)
			switch mode {
			case "nil_conversation":
				got, err := f.service.EvaluateConversation(nil)
				require.NoError(t, err)
				require.Nil(t, got)
				return
			case "no_policy":
				require.NoError(t, f.stores["RPT"].Delete(&f.policy).Error)
			case "unconfigured_message":
				f.service.messages = nil
			case "unconfigured_inbox":
				f.service.inboxes = nil
			case "missing_inbox":
				f.conv.InboxID = 999
				require.NoError(t, f.stores["RPT"].Model(&f.policy).Update("only_during_business_hours", true).Error)
			case "bot_private_failed":
				f.add(t, 1, 0, "incoming", "Contact", "sent", false)
				f.add(t, 1, 10*time.Second, "outgoing", "AgentBot", "sent", false)
				f.add(t, 1, 20*time.Second, "outgoing", "User", "sent", true)
				f.add(t, 1, 30*time.Second, "outgoing", "User", "failed", false)
			case "late_human", "on_time_human":
				f.add(t, 1, 0, "incoming", "Contact", "sent", false)
				delay := 30 * time.Second
				if mode == "late_human" {
					delay = 90 * time.Second
				}
				f.add(t, 1, delay, "outgoing", "User", "sent", false)
			}
			got, err := f.service.EvaluateConversation(&f.conv)
			if mode == "unconfigured_message" || mode == "unconfigured_inbox" {
				require.Error(t, err)
				require.Nil(t, got)
				require.Empty(t, f.snapshot(t).Applied)
				return
			}
			require.NoError(t, err)
			if mode == "on_time_human" || mode == "no_policy" {
				require.Empty(t, got)
			} else {
				require.Len(t, got, 1)
				require.Equal(t, "first_response", got[0].BreachType)
			}
		})
	}
	t.Run("account_continues_independent_conversations", func(t *testing.T) {
		f := newSLAReadFixture(t, open)
		second := f.conv
		second.ID = 2
		second.DisplayID = 2
		require.NoError(t, f.stores["CON"].Create(&second).Error)
		before := f.snapshot(t)
		ownerErr := errors.New("first conversation unavailable")
		f.service.messages = &slaTimelineStub{err: ownerErr, failConversation: 1, delegate: repository.NewMessageRepository(f.stores["MSG"])}
		got, err := f.service.EvaluateAccountSLAs(91)
		require.ErrorIs(t, err, ownerErr)
		require.Len(t, got, 1)
		require.Equal(t, uint(2), got[0].ConversationID)
		after := f.snapshot(t)
		require.Len(t, after.Applied, 1)
		require.Equal(t, uint(2), after.Applied[0].ConversationID)
		require.Equal(t, before.Conversations[0], after.Conversations[0], "failed conversation retains all persisted fields, including default active status")
		require.Equal(t, "breached", after.Conversations[1].SLAStatus)
	})
	for _, owner := range []string{"MSG", "CHN", "RPT"} {
		t.Run("unavailable_table/"+owner, func(t *testing.T) {
			f := newSLAReadFixture(t, open)
			table := map[string]string{"MSG": "messages", "CHN": "inboxes", "RPT": "sla_breach_logs"}[owner]
			before := f.snapshot(t)
			require.NoError(t, f.stores[owner].Migrator().RenameTable(table, table+"_offline"))
			got, err := f.service.EvaluateConversation(&f.conv)
			require.Error(t, err)
			require.Nil(t, got)
			require.NoError(t, f.stores[owner].Migrator().RenameTable(table+"_offline", table))
			require.Equal(t, before, f.snapshot(t))
			got, err = f.service.EvaluateConversation(&f.conv)
			require.NoError(t, err)
			require.Len(t, got, 1)
		})
	}
	t.Run("isolated_owners_and_tenant_scope", func(t *testing.T) {
		f := newSLAReadFixture(t, open)
		for _, model := range []any{&domain.Conversation{}, &domain.Inbox{}, &domain.Message{}} {
			require.False(t, f.stores["RPT"].Migrator().HasTable(model))
		}
		require.False(t, f.stores["CON"].Migrator().HasTable(&domain.SLAPolicy{}))
		require.False(t, f.stores["MSG"].Migrator().HasTable(&domain.Conversation{}))
		f.add(t, 1, 0, "incoming", "Contact", "sent", false)
		foreign := domain.Message{AccountID: 92, ConversationID: 1, MessageType: "outgoing", SenderType: "User", Status: "sent", CreatedAt: f.start.Add(30 * time.Second)}
		require.NoError(t, f.stores["MSG"].Create(&foreign).Error)
		got, err := f.service.EvaluateConversation(&f.conv)
		require.NoError(t, err)
		require.Len(t, got, 1)
		got, err = f.service.EvaluateAccountSLAs(92)
		require.NoError(t, err)
		require.Empty(t, got)
	})
}

func TestSLANextResponseSourceFailure(t *testing.T) {
	ownerErr := errors.New("NRT timeline unavailable")
	owner := &slaTimelineStub{err: ownerErr}
	s := &SLAService{messages: owner}
	due, breached, actual, at, err := s.EvaluateNextResponse(&domain.Conversation{ID: 1, AccountID: 91}, nil, 60, time.Now())
	require.ErrorIs(t, err, ownerErr)
	require.Nil(t, due)
	require.False(t, breached)
	require.Zero(t, actual)
	require.True(t, at.IsZero())
	_, _, _, _, err = s.EvaluateNextResponse(nil, nil, 60, time.Now())
	require.NoError(t, err)
	_, _, _, _, err = s.EvaluateNextResponse(&domain.Conversation{}, nil, 0, time.Now())
	require.NoError(t, err)
	require.Equal(t, 1, owner.calls)
}
