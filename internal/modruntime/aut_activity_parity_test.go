package modruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type activityParityFixture struct {
	*automationActionFixture
	legacy   *gorm.DB
	initial  domain.Conversation
	neighbor domain.Conversation
}

// G12 compares observable activity with the actual unchanged legacy service.
// Every action in these rules is already satisfied before the rule starts, so
// an unrelated mutating action cannot hide a missing successful no-op touch.
// Wall-clock timestamps and legacy/durable write counts are intentionally not
// compared: activity must advance meaningfully and change the real list order.
func TestAUTG12SuccessfulNoopActivityMatchesActualLegacyService(t *testing.T) {
	cases := []struct {
		name, action string
		seed         func(*domain.Conversation)
	}{
		{"same_agent_assignment", siblingAction("assign_agent", 7), func(c *domain.Conversation) { id := uint(7); c.AssigneeID = &id }},
		{"same_team_assignment", siblingAction("assign_team", 3), func(c *domain.Conversation) { id := uint(3); c.TeamID = &id }},
		{"already_removed_agent", siblingAction("remove_assigned_agent"), nil},
		{"already_removed_team", siblingAction("remove_assigned_team"), nil},
		{"duplicate_attached_label", siblingAction("add_label", "vip"), nil},
		{"duplicate_labels_in_one_action", siblingAction("add_labels", "vip", "vip"), nil},
		{"remove_existing_unattached_label", siblingAction("remove_label", "unattached"), nil},
		{"remove_missing_title_resolution", siblingAction("remove_label", "never-created"), nil},
		{"remove_missing_numeric_resolution", siblingAction("remove_label", "99999"), nil},
		{"same_open_status", siblingAction("open_conversation"), nil},
		{"same_resolved_status", siblingAction("resolve_conversation"), func(c *domain.Conversation) { c.Status = "resolved" }},
		{"same_pending_status", siblingAction("change_status", "pending"), func(c *domain.Conversation) { c.Status = "pending" }},
		{"same_priority", siblingAction("change_priority", "medium"), nil},
		{"already_muted_and_resolved", siblingAction("mute_conversation"), func(c *domain.Conversation) { c.Muted, c.Status = true, "resolved" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Repeated logical actions have distinct receipts. They are not
			// retries, even though every requested business value is identical.
			action := strings.TrimSuffix(strings.TrimPrefix(tc.action, "["), "]")
			f := newActivityParityFixture(t, "["+action+","+action+"]", tc.seed)
			before := siblingParityState(t, f.con, f.owners["CUS"].DB, f.initial.ID)
			legacyRows := f.runLegacy(t)
			require.Len(t, legacyRows, 1)
			require.Equal(t, "success", legacyRows[0].Status)
			var legacyResults []service.ActionResult
			require.NoError(t, json.Unmarshal([]byte(legacyRows[0].ActionResults), &legacyResults))
			require.Len(t, legacyResults, 2)
			for _, result := range legacyResults {
				require.Equal(t, "success", result.Status)
			}
			event := f.admit(t, "no-op-activity")
			drainSiblingParityRunner(t, f.automationActionFixture)
			rows := causalExecutions(t, f.autRunnerFixture, event.EventID)
			require.Len(t, rows, 1)
			require.Equal(t, "succeeded", rows[0].Status, "%+v", rows[0])
			legacy := activityParityObservation(t, f.legacy, f.legacy, f.initial)
			durable := activityParityObservation(t, f.con, f.owners["CUS"].DB, f.initial)
			require.Equal(t, before, legacy.State, "legacy fixture must be a semantic no-op")
			require.Equal(t, legacy, durable)
			require.True(t, durable.Advanced)
			require.Equal(t, []uint{f.initial.ID, f.neighbor.ID}, durable.Order)
			f.assertNeighborUnchanged(t)

			// Intake/runner replay cannot turn these two successful ordinary
			// actions into a third touch or reorder the conversation again.
			final := f.conversationState()
			require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), event))
			ran, err := f.aut.RunAutomationOnce(context.Background())
			require.NoError(t, err)
			require.False(t, ran)
			require.Equal(t, final, f.conversationState())
		})
	}
}

// Legacy reports these invalid or unsupported actions as successful and touches
// activity anyway. This bug is explicitly not part of successful-no-op parity.
func TestAUTG12RejectedActionsDoNotCopyLegacyFakeSuccessActivity(t *testing.T) {
	for _, tc := range []struct{ name, actions string }{
		{"foreign_team", siblingAction("assign_team", 4)},
		{"foreign_agent", siblingAction("assign_agent", 8)},
		{"missing_agent_parameter", siblingAction("assign_agent")},
		{"unsupported_webhook", siblingAction("send_webhook", "unsupported-placeholder")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newActivityParityFixture(t, tc.actions, nil)
			before := siblingParityState(t, f.con, f.owners["CUS"].DB, f.initial.ID)
			legacyRows := f.runLegacy(t)
			require.Len(t, legacyRows, 1)
			require.Equal(t, "success", legacyRows[0].Status)
			legacy := activityParityObservation(t, f.legacy, f.legacy, f.initial)
			require.Equal(t, before, legacy.State)
			require.True(t, legacy.Advanced, "fixture must demonstrate the excluded legacy fake-success touch")
			event := f.admit(t, "rejected-activity")
			drainSiblingParityRunner(t, f.automationActionFixture)
			rows := causalExecutions(t, f.autRunnerFixture, event.EventID)
			require.Len(t, rows, 1)
			require.Equal(t, "failed", rows[0].Status, "%+v", rows[0])
			durable := activityParityObservation(t, f.con, f.owners["CUS"].DB, f.initial)
			require.Equal(t, before, durable.State)
			require.False(t, durable.Advanced)
			require.Equal(t, []uint{f.neighbor.ID, f.initial.ID}, durable.Order)
			require.True(t, f.initial.LastActivityAt.Equal(f.conversationState().LastActivityAt))
			f.assertNeighborUnchanged(t)
		})
	}
}

func TestAUTG12SnoozeActivityAndFreshDeadlineMatchActualLegacyService(t *testing.T) {
	f := newActivityParityFixture(t, siblingAction("snooze", "2h"), func(c *domain.Conversation) {
		until := time.Now().UTC().Add(15 * time.Minute)
		c.Status, c.SnoozedUntil = "snoozed", &until
	})
	legacyRows := f.runLegacy(t)
	require.Equal(t, "success", legacyRows[0].Status)
	firstEvent := f.admit(t, "first-snooze")
	drainSiblingParityRunner(t, f.automationActionFixture)
	require.Equal(t, "succeeded", causalExecutions(t, f.autRunnerFixture, firstEvent.EventID)[0].Status)
	first := f.conversationState()
	var firstLegacy domain.Conversation
	require.NoError(t, f.legacy.First(&firstLegacy, f.initial.ID).Error)
	for _, state := range []domain.Conversation{firstLegacy, first} {
		require.NotNil(t, state.SnoozedUntil)
		require.True(t, state.SnoozedUntil.After(*f.initial.SnoozedUntil), "a fresh relative snooze changes the deadline")
	}
	require.Equal(t, activityParityObservation(t, f.legacy, f.legacy, f.initial), activityParityObservation(t, f.con, f.owners["CUS"].DB, f.initial))
	require.True(t, activityParityObservation(t, f.con, f.owners["CUS"].DB, f.initial).Advanced)

	// A distinct invocation is allowed to compute a fresh deadline. Replaying
	// its receipt, by contrast, must retain that frozen deadline and activity.
	f.runLegacy(t) // A restarted legacy service has no same-second in-memory cache.
	secondEvent := f.admit(t, "fresh-snooze")
	drainSiblingParityRunner(t, f.automationActionFixture)
	require.Equal(t, "succeeded", causalExecutions(t, f.autRunnerFixture, secondEvent.EventID)[0].Status)
	second := f.conversationState()
	var secondLegacy domain.Conversation
	require.NoError(t, f.legacy.First(&secondLegacy, f.initial.ID).Error)
	require.True(t, secondLegacy.SnoozedUntil.After(*firstLegacy.SnoozedUntil))
	require.True(t, second.SnoozedUntil.After(*first.SnoozedUntil))
	require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), secondEvent))
	ran, err := f.aut.RunAutomationOnce(context.Background())
	require.NoError(t, err)
	require.False(t, ran)
	require.Equal(t, second, f.conversationState())
	f.assertNeighborUnchanged(t)
}

func TestAUTG12LocalMessageActivityMatchesActualLegacyService(t *testing.T) {
	for _, private := range []bool{false, true} {
		t.Run(fmt.Sprintf("private_%t", private), func(t *testing.T) {
			action := "send_reply"
			if private {
				action = "private_note"
			}
			f := newActivityParityFixture(t, siblingAction(action, "local body"), nil)
			legacyRows := f.runLegacy(t)
			require.Equal(t, "success", legacyRows[0].Status)
			event := f.admit(t, "message-activity")
			drainSiblingParityRunner(t, f.automationActionFixture)
			require.Equal(t, "succeeded", causalExecutions(t, f.autRunnerFixture, event.EventID)[0].Status)
			legacy := activityParityObservation(t, f.legacy, f.legacy, f.initial)
			require.True(t, legacy.Advanced)
			require.Equal(t, legacy, activityParityObservation(t, f.con, f.owners["CUS"].DB, f.initial))
			for _, db := range []*gorm.DB{f.legacy, f.owners["MSG"].DB} {
				var messages []domain.Message
				require.NoError(t, db.Where("conversation_id = ?", f.initial.ID).Find(&messages).Error)
				require.Len(t, messages, 1)
				require.Equal(t, "local body", messages[0].Content)
				require.Equal(t, private, messages[0].Private)
				require.Equal(t, domain.MessageTypeOutgoing, messages[0].MessageType)
				if db == f.owners["MSG"].DB {
					want := domain.MessageStatusQueued
					if private {
						want = domain.MessageStatusSaved
					}
					require.Equal(t, want, messages[0].Status, "local persistence must not claim provider delivery")
				}
			}
			f.assertNeighborUnchanged(t)
		})
	}
}

func TestAUTG12NoopActivityCarriesAcrossRuleSiblings(t *testing.T) {
	f := newActivityParityFixture(t, siblingAction("remove_assigned_agent"), nil)
	for _, spec := range []siblingParityRule{
		{siblingCondition("assignee_id", "is_not_present"), siblingAction("change_priority", "medium")},
		{siblingCondition("assignee_id", "is_present"), siblingAction("change_priority", "urgent")},
		{siblingCondition("priority", "is", "medium"), siblingAction("remove_label", "never-created")},
	} {
		rule := cascadeRule(t, f.automationActionFixture, f.rule.EventName, spec.conditions, spec.actions)
		require.NoError(t, f.legacy.Create(&rule).Error)
	}
	legacyRows := f.runLegacy(t)
	require.Len(t, legacyRows, 3)
	for _, row := range legacyRows {
		require.Equal(t, "success", row.Status)
	}
	event := f.admit(t, "sibling-activity")
	f.run()
	require.True(t, activityParityObservation(t, f.con, f.owners["CUS"].DB, f.initial).Advanced, "first no-op must touch before later siblings can mask it")
	drainSiblingParityRunner(t, f.automationActionFixture)
	rows := causalExecutions(t, f.autRunnerFixture, event.EventID)
	require.Len(t, rows, 4)
	for i, want := range []string{"succeeded", "succeeded", "no_match", "succeeded"} {
		require.Equal(t, want, rows[i].Status, "%+v", rows[i])
		if i > 0 {
			require.Equal(t, rows[i-1].ID, *rows[i].RunnerPredecessorID)
		}
	}
	require.Equal(t, activityParityObservation(t, f.legacy, f.legacy, f.initial), activityParityObservation(t, f.con, f.owners["CUS"].DB, f.initial))
	f.assertNeighborUnchanged(t)
}

// The ordinary repository path is still used by manual/API callers in both
// execution modes. Its successful same-value writes must keep their original
// activity policy and stale-snapshot behavior independently of an automation touch.
func TestAUTG12OrdinaryOwnerNoopWritesRetainActivity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write func(*repository.ConversationRepository, domain.Conversation) error
	}{
		{"priority", func(r *repository.ConversationRepository, c domain.Conversation) error {
			return r.UpdatePriority(c.AccountID, c.ID, c.Priority)
		}},
		{"status", func(r *repository.ConversationRepository, c domain.Conversation) error {
			return r.UpdateStatus(c.AccountID, c.ID, c.Status, c.SnoozedUntil)
		}},
		{"agent", func(r *repository.ConversationRepository, c domain.Conversation) error {
			return r.Assign(c.AccountID, c.ID, c.AssigneeID)
		}},
		{"team", func(r *repository.ConversationRepository, c domain.Conversation) error {
			return r.AssignTeam(c.AccountID, c.ID, c.TeamID)
		}},
		{"mute", func(r *repository.ConversationRepository, c domain.Conversation) error {
			return r.ToggleMute(c.AccountID, c.ID, c.Muted)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newActivityParityFixture(t, "[]", nil)
			before := siblingParityState(t, f.con, f.owners["CUS"].DB, f.initial.ID)
			for _, db := range []*gorm.DB{f.legacy, f.con} {
				repo := repository.NewConversationRepositoryWithReadPorts(db, nil, nil, nil, nil, nil, nil, nil)
				require.NoError(t, tc.write(repo, f.initial))
				var current domain.Conversation
				require.NoError(t, db.First(&current, f.initial.ID).Error)
				require.Greater(t, current.ObjectVersion, f.initial.ObjectVersion)
				stale := f.initial
				stale.Priority = "urgent"
				require.ErrorIs(t, repo.Update(&stale), repository.ErrConversationVersionConflict)
			}
			legacy := activityParityObservation(t, f.legacy, f.legacy, f.initial)
			require.Equal(t, before, legacy.State)
			// ToggleMute intentionally does not touch activity on the ordinary
			// repository path; the legacy automation loop adds its own touch.
			wantOrder := []uint{f.initial.ID, f.neighbor.ID}
			if tc.name == "mute" {
				wantOrder = []uint{f.neighbor.ID, f.initial.ID}
			}
			require.Equal(t, tc.name != "mute", legacy.Advanced)
			require.Equal(t, wantOrder, legacy.Order)
			require.Equal(t, legacy, activityParityObservation(t, f.con, f.owners["CUS"].DB, f.initial))
			f.assertNeighborUnchanged(t)
		})
	}
}

func TestAUTG12OrdinaryNoopUpdateKeepsActivityAndSiblingGuard(t *testing.T) {
	f := newActivityParityFixture(t, siblingAction("remove_assigned_agent"), nil)
	cascadeRule(t, f.automationActionFixture, f.rule.EventName, siblingCondition("status", "is", "open"), siblingAction("change_priority", "urgent"))
	event := f.admit(t, "manual-interleave")
	f.run()
	afterAction := f.conversationState()
	require.True(t, activityParityObservation(t, f.con, f.owners["CUS"].DB, f.initial).Advanced)

	// An ordinary same-priority write remains a real owner update. A later
	// sibling cannot treat it as its predecessor's confirmed version.
	source := triggerAliasSource(f.autRunnerFixture)
	require.NoError(t, source.UpdatePriority(91, f.initial.ID, "medium"))
	afterManual := f.conversationState()
	require.False(t, afterManual.LastActivityAt.Before(afterAction.LastActivityAt))
	require.Greater(t, afterManual.ObjectVersion, afterAction.ObjectVersion)
	fact := triggerAliasFact(t, source)
	require.Equal(t, uint64(afterManual.ObjectVersion), fact.AggregateVersion)
	require.Contains(t, fact.ChangedFields, "last_activity_at")
	stale := afterAction
	stale.Priority = "low"
	require.ErrorIs(t, source.Update(&stale), repository.ErrConversationVersionConflict)
	drainSiblingParityRunner(t, f.automationActionFixture)
	rows := causalExecutions(t, f.autRunnerFixture, event.EventID)
	require.Len(t, rows, 2)
	require.Equal(t, "succeeded", rows[0].Status)
	require.Equal(t, "failed", rows[1].Status)
	require.Equal(t, "target_version_stale", rows[1].Error)
	require.Equal(t, afterManual, f.conversationState(), "failed stale sibling must not touch or overwrite the manual update")
	require.Equal(t, []uint{f.initial.ID, f.neighbor.ID}, activityParityOrder(t, f.con))
	f.assertNeighborUnchanged(t)
}

func newActivityParityFixture(t *testing.T, actions string, seed func(*domain.Conversation)) *activityParityFixture {
	t.Helper()
	f := &activityParityFixture{automationActionFixture: newAutomationActionFixture(t, false, actions)}
	old := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	f.conversation.LastActivityAt, f.conversation.UpdatedAt = old, old
	if seed != nil {
		seed(&f.conversation)
	}
	require.NoError(t, f.con.Save(&f.conversation).Error)
	f.initial = f.conversationState()
	f.neighbor = domain.Conversation{ID: f.initial.ID + 100, AccountID: 91, InboxID: 11, ContactID: 22, DisplayID: 2, Status: "open", Priority: "medium", ObjectVersion: 1, CreatedAt: old, UpdatedAt: old, LastActivityAt: old.Add(24 * time.Hour)}
	require.NoError(t, f.con.Create(&f.neighbor).Error)
	require.NoError(t, f.owners["IAM"].DB.Create(&domain.User{ID: 7, Name: "Agent", Email: "activity-agent@example.test"}).Error)

	f.legacy = newModuleTestDB(t, "AUT")
	require.NoError(t, f.legacy.AutoMigrate(&domain.Conversation{}, &domain.ConversationLabel{}, &domain.Label{}, &domain.User{}, &domain.AccountUser{}, &domain.Team{}, &domain.Message{}))
	for _, value := range []any{
		&f.initial, &f.neighbor,
		&domain.User{ID: 7, Name: "Agent", Email: "activity-agent@example.test"},
		&domain.User{ID: 8, Name: "Foreign", Email: "foreign-activity@example.test"},
		&domain.AccountUser{AccountID: 91, UserID: 7}, &domain.AccountUser{AccountID: 92, UserID: 8},
		&domain.Team{ID: 3, AccountID: 91, Name: "support"}, &domain.Team{ID: 4, AccountID: 92, Name: "foreign"},
	} {
		require.NoError(t, f.legacy.Create(value).Error)
	}
	rule := f.rule
	require.NoError(t, f.legacy.Create(&rule).Error)
	for i, title := range []string{"vip", "unattached"} {
		label := domain.Label{ID: uint(i + 1), AccountID: 91, Title: title}
		require.NoError(t, f.legacy.Create(&label).Error)
		require.NoError(t, f.owners["CUS"].DB.Create(&label).Error)
		if title == "vip" {
			for _, db := range []*gorm.DB{f.legacy, f.con} {
				require.NoError(t, db.Create(&domain.ConversationLabel{ConversationID: f.initial.ID, LabelID: label.ID}).Error)
			}
		}
	}
	for _, db := range []*gorm.DB{f.legacy, f.con} {
		require.Equal(t, []uint{f.neighbor.ID, f.initial.ID}, activityParityOrder(t, db))
	}
	return f
}

func (f *activityParityFixture) runLegacy(t *testing.T) []domain.AutomationRuleExecution {
	t.Helper()
	var current domain.Conversation
	require.NoError(t, f.legacy.First(&current, f.initial.ID).Error)
	legacy := service.NewAutomationService(f.legacy, repository.NewConversationRepository(f.legacy), repository.NewMessageRepository(f.legacy))
	legacy.HandleConversationCreated(&current)
	var rows []domain.AutomationRuleExecution
	require.NoError(t, f.legacy.Where("conversation_id = ?", f.initial.ID).Order("id ASC").Find(&rows).Error)
	return rows
}

func (f *activityParityFixture) admit(t *testing.T, eventID string) foundation.EventEnvelope {
	t.Helper()
	state := f.conversationState()
	event := bridgeFact(f.autRunnerFixture, "conversation_created")
	event.EventID = eventID
	event.AggregateVersion = uint64(state.ObjectVersion)
	event.Payload["object_version"] = state.ObjectVersion
	require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), event))
	return event
}

type activityParityView struct {
	State    siblingConversationState
	Advanced bool
	Order    []uint
}

func activityParityObservation(t *testing.T, con, cus *gorm.DB, initial domain.Conversation) activityParityView {
	t.Helper()
	var current domain.Conversation
	require.NoError(t, con.First(&current, initial.ID).Error)
	return activityParityView{
		State:    siblingParityState(t, con, cus, initial.ID),
		Advanced: current.LastActivityAt.After(initial.LastActivityAt.Add(24 * time.Hour)),
		Order:    activityParityOrder(t, con),
	}
}

func activityParityOrder(t *testing.T, db *gorm.DB) []uint {
	t.Helper()
	// Exercise the public repository ordering, with read ports omitted only
	// because conversation references have separate owner databases here.
	repo := repository.NewConversationRepositoryWithReadPorts(db, nil, nil, nil, nil, nil, nil, nil)
	rows, count, err := repo.List(91, "", "", nil, nil, 1, 10)
	require.NoError(t, err)
	require.Equal(t, int64(2), count)
	ids := make([]uint, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return ids
}

func (f *activityParityFixture) assertNeighborUnchanged(t *testing.T) {
	t.Helper()
	for _, db := range []*gorm.DB{f.legacy, f.con} {
		var current domain.Conversation
		require.NoError(t, db.First(&current, f.neighbor.ID).Error)
		require.Equal(t, f.neighbor, current)
	}
}
