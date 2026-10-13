package modruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type siblingParityRule struct{ conditions, actions string }

type siblingParityCase struct {
	name          string
	priority      string
	labels        []string
	rules         []siblingParityRule
	matched       []int // Sanity-check the real legacy fixture, not a matching implementation.
	finalPriority string
	finalLabels   []string
}

func siblingCondition(key, operator string, values ...any) string {
	raw, _ := json.Marshal([]map[string]any{{"attribute_key": key, "filter_operator": operator, "values": values}})
	return string(raw)
}

func siblingAction(name string, params ...any) string {
	raw, _ := json.Marshal([]map[string]any{{"action_name": name, "action_params": params}})
	return string(raw)
}

// The expected decisions below are additionally checked against the unchanged
// AutomationService, then durable executions and final effects are compared with
// that real service. In particular, an initially true condition can become false
// and an initially false condition can become true before its sibling runs.
func TestAUTSiblingConditionsMatchActualLegacyService(t *testing.T) {
	cases := []siblingParityCase{
		{
			name: "priority_urgent_enables_later_label", priority: "low",
			rules: []siblingParityRule{
				{"[]", siblingAction("change_priority", "urgent")},
				{siblingCondition("priority", "is", "urgent"), siblingAction("add_label", "promoted")},
			},
			matched: []int{0, 1}, finalPriority: "urgent", finalLabels: []string{"promoted"},
		},
		{
			name: "priority_low_no_longer_matches", priority: "low",
			rules: []siblingParityRule{
				{"[]", siblingAction("change_priority", "urgent")},
				{siblingCondition("priority", "is", "low"), siblingAction("add_label", "must-not-run")},
			},
			matched: []int{0}, finalPriority: "urgent", finalLabels: []string{},
		},
		{
			name: "status_follows_shared_conversation", priority: "medium",
			rules: []siblingParityRule{
				{"[]", siblingAction("resolve_conversation")},
				{siblingCondition("status", "is", "resolved"), siblingAction("change_priority", "urgent")},
				{siblingCondition("status", "is", "open"), siblingAction("add_label", "must-not-run")},
				{siblingCondition("priority", "is", "urgent"), siblingAction("open_conversation")},
				{siblingCondition("status", "is", "open"), siblingAction("add_label", "reopened")},
			},
			matched: []int{0, 1, 3, 4}, finalPriority: "urgent", finalLabels: []string{"reopened"},
		},
		{
			name: "agent_assignment_then_removal", priority: "medium",
			rules: []siblingParityRule{
				{"[]", siblingAction("assign_agent", 7)},
				{siblingCondition("assignee_id", "is", 7), `[{"action_name":"add_label","action_params":["assigned"]},{"action_name":"remove_assigned_agent"}]`},
				{siblingCondition("assignee_id", "is_not_present"), siblingAction("add_label", "removed")},
				{siblingCondition("assignee_id", "is_present"), siblingAction("add_label", "must-not-run")},
			},
			matched: []int{0, 1, 2}, finalPriority: "medium", finalLabels: []string{"assigned", "removed"},
		},
		{
			name: "team_assignment_then_removal", priority: "medium",
			rules: []siblingParityRule{
				{"[]", siblingAction("assign_team", 3)},
				{siblingCondition("team_id", "is", 3), `[{"action_name":"add_label","action_params":["assigned"]},{"action_name":"remove_assigned_team"}]`},
				{siblingCondition("team_id", "is_not_present"), siblingAction("add_label", "removed")},
				{siblingCondition("team_id", "is_present"), siblingAction("add_label", "must-not-run")},
			},
			matched: []int{0, 1, 2}, finalPriority: "medium", finalLabels: []string{"assigned", "removed"},
		},
		{
			name: "added_labels_are_read_from_storage", priority: "medium",
			rules: []siblingParityRule{
				{"[]", siblingAction("add_label", "vip")},
				{siblingCondition("labels", "is", "VIP"), siblingAction("change_priority", "urgent")},
				{siblingCondition("labels", "is_not", "vip"), siblingAction("add_label", "must-not-run")},
				{siblingCondition("labels", "is", "phantom-cache"), siblingAction("add_label", "must-not-run-either")},
			},
			matched: []int{0, 1}, finalPriority: "urgent", finalLabels: []string{"vip"},
		},
		{
			name: "removed_labels_are_read_from_storage", priority: "medium", labels: []string{"vip"},
			rules: []siblingParityRule{
				{"[]", siblingAction("remove_label", "vip")},
				{siblingCondition("tags", "is_not_present"), siblingAction("change_priority", "urgent")},
				{siblingCondition("labels", "is", "vip"), siblingAction("add_label", "must-not-run")},
				{siblingCondition("labels", "is", "phantom-cache"), siblingAction("add_label", "must-not-run-either")},
			},
			matched: []int{0, 1}, finalPriority: "urgent", finalLabels: []string{},
		},
		{
			name: "interleaved_no_match_does_not_break_chain", priority: "medium",
			rules: []siblingParityRule{
				{siblingCondition("status", "is", "resolved"), siblingAction("change_priority", "low")},
				{siblingCondition("priority", "is", "medium"), siblingAction("change_priority", "urgent")},
				{siblingCondition("priority", "is", "low"), siblingAction("add_label", "must-not-run")},
				{siblingCondition("priority", "is", "urgent"), siblingAction("add_label", "promoted")},
				{siblingCondition("labels", "is_not", "promoted"), siblingAction("change_priority", "low")},
				{siblingCondition("labels", "is", "promoted"), siblingAction("add_label", "verified")},
			},
			matched: []int{1, 3, 5}, finalPriority: "urgent", finalLabels: []string{"promoted", "verified"},
		},
	}
	for _, eventName := range []string{"conversation_created", "conversation_updated"} {
		for _, tc := range cases {
			t.Run(eventName+"/"+tc.name, func(t *testing.T) {
				f, rules := newSiblingParityFixture(t, tc, eventName)
				legacy, legacyMatches := runActualLegacySiblings(t, f, tc, rules, eventName)
				wantIDs := make([]uint, 0, len(tc.matched))
				for _, index := range tc.matched {
					wantIDs = append(wantIDs, rules[index].ID)
				}
				require.Equal(t, wantIDs, legacyMatches, "legacy fixture did not exercise the intended condition changes")
				require.Equal(t, tc.finalPriority, legacy.Priority)
				require.Equal(t, tc.finalLabels, legacy.Labels)

				event := bridgeFact(f.autRunnerFixture, eventName)
				if eventName == "conversation_updated" {
					// Exercise the real ordinary owner write and its durable fact, not
					// a manually synthesized action-origin envelope or direct start.
					source := triggerAliasSource(f.autRunnerFixture)
					require.NoError(t, source.UpdatePriority(91, f.conversation.ID, tc.priority))
					event = triggerAliasFact(t, source)
				}
				require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), event))
				rows := causalExecutions(t, f.autRunnerFixture, event.EventID)
				require.Len(t, rows, len(rules))
				for i, row := range rows {
					require.Equal(t, rules[i].ID, row.RuleID, "siblings must retain ascending rule order")
					if i > 0 {
						require.NotNil(t, row.RunnerPredecessorID)
						require.Equal(t, rows[i-1].ID, *row.RunnerPredecessorID)
						assert.Equal(t, "evaluating", row.Status, "later conditions must not freeze at intake")
						assert.Nil(t, row.CompletedAt)
						assert.Zero(t, row.RunnerNextAction)
					}
				}
				// Replaying intake while evaluation is pending cannot complete or
				// duplicate any later decision, even before the predecessor runs.
				require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), event))
				require.Equal(t, rows, causalExecutions(t, f.autRunnerFixture, event.EventID))
				drainSiblingParityRunner(t, f)
				rows = causalExecutions(t, f.autRunnerFixture, event.EventID)
				gotIDs := []uint{}
				for _, row := range rows {
					require.NotNil(t, row.CompletedAt, "execution=%+v", row)
					switch row.Status {
					case "succeeded":
						gotIDs = append(gotIDs, row.RuleID)
					case "no_match":
						require.Equal(t, "conditions_not_matched", row.Error)
						var results []automationActionResult
						require.NoError(t, json.Unmarshal([]byte(row.ActionResults), &results))
						require.NotEmpty(t, results)
						for _, result := range results {
							require.Equal(t, "skipped", result.Status)
						}
					default:
						t.Fatalf("sibling failed to finish successfully: %+v", row)
					}
				}
				require.Equal(t, legacyMatches, gotIDs, "durable matching differs from actual sequential legacy execution")
				require.Equal(t, legacy, siblingParityState(t, f.con, f.owners["CUS"].DB, f.conversation.ID))
				// Final replay is frozen even when every owner is unavailable.
				f.aut.Config.Dependencies = map[string]DependencyConfig{}
				require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), event))
				require.Equal(t, rows, causalExecutions(t, f.autRunnerFixture, event.EventID))
				ran, err := f.aut.RunAutomationOnce(context.Background())
				require.NoError(t, err)
				require.False(t, ran)
			})
		}
	}
}

func newSiblingParityFixture(t *testing.T, tc siblingParityCase, eventName string) (*automationActionFixture, []domain.AutomationRule) {
	t.Helper()
	f := newAutomationActionFixture(t, false, tc.rules[0].actions)
	require.NoError(t, f.owners["IAM"].DB.Create(&domain.User{ID: 7, Name: "Agent", Email: "sibling-agent@example.test"}).Error)
	require.NoError(t, f.con.Model(&f.conversation).Update("priority", tc.priority).Error)
	f.conversation.Priority = tc.priority
	for _, title := range tc.labels {
		label := domain.Label{AccountID: 91, Title: title}
		require.NoError(t, f.owners["CUS"].DB.Create(&label).Error)
		require.NoError(t, f.con.Create(&domain.ConversationLabel{ConversationID: f.conversation.ID, LabelID: label.ID}).Error)
	}
	f.rule.EventName, f.rule.Conditions = eventName, tc.rules[0].conditions
	for _, model := range []any{&domain.AutomationRule{}, &domain.AutomationRuleVersion{}} {
		require.NoError(t, f.aut.DB.Model(model).Where("account_id = ?", 91).Updates(map[string]any{"event_name": eventName, "conditions": tc.rules[0].conditions}).Error)
	}
	rules := []domain.AutomationRule{f.rule}
	for _, rule := range tc.rules[1:] {
		rules = append(rules, cascadeRule(t, f, eventName, rule.conditions, rule.actions))
	}
	return f, rules
}

func runActualLegacySiblings(t *testing.T, f *automationActionFixture, tc siblingParityCase, rules []domain.AutomationRule, eventName string) (siblingConversationState, []uint) {
	t.Helper()
	db := newModuleTestDB(t, "AUT")
	require.NoError(t, db.AutoMigrate(&domain.Conversation{}, &domain.ConversationLabel{}, &domain.Label{}, &domain.User{}, &domain.AccountUser{}, &domain.Team{}))
	conversation := f.conversation
	require.NoError(t, db.Create(&conversation).Error)
	for _, value := range []any{
		&domain.User{ID: 7, Name: "Agent", Email: "sibling-agent@example.test"},
		&domain.AccountUser{AccountID: 91, UserID: 7},
		&domain.Team{ID: 3, AccountID: 91, Name: "support"},
	} {
		require.NoError(t, db.Create(value).Error)
	}
	for _, title := range tc.labels {
		label := domain.Label{AccountID: 91, Title: title}
		require.NoError(t, db.Create(&label).Error)
		require.NoError(t, db.Create(&domain.ConversationLabel{ConversationID: conversation.ID, LabelID: label.ID}).Error)
	}
	for _, rule := range rules {
		copy := rule
		require.NoError(t, db.Create(&copy).Error)
	}
	convRepo := repository.NewConversationRepository(db)
	if eventName == "conversation_updated" {
		require.NoError(t, convRepo.UpdatePriority(91, conversation.ID, tc.priority))
		require.NoError(t, db.First(&conversation, conversation.ID).Error)
	}
	// Legacy scalar conditions observe the same pointer that earlier actions
	// mutate, but label conditions consult storage. This deliberately incorrect
	// preload must neither hide real persisted labels nor invent a matching one.
	conversation.Labels = []domain.Label{{ID: 999, AccountID: 91, Title: "phantom-cache"}}
	legacy := service.NewAutomationService(db, convRepo, repository.NewMessageRepository(db))
	if eventName == "conversation_updated" {
		legacy.HandleConversationUpdated(&conversation)
	} else {
		legacy.HandleConversationCreated(&conversation)
	}
	var executions []domain.AutomationRuleExecution
	require.NoError(t, db.Where("conversation_id = ?", conversation.ID).Order("id ASC").Find(&executions).Error)
	matched := []uint{}
	for _, row := range executions {
		require.Equal(t, "success", row.Status, "legacy action failure invalidates this differential fixture: %+v", row)
		matched = append(matched, row.RuleID)
	}
	return siblingParityState(t, db, db, conversation.ID), matched
}

type siblingConversationState struct {
	Status, Priority string
	Assignee, Team   uint
	Muted, Snoozed   bool
	Labels           []string
}

func siblingParityState(t *testing.T, con, cus *gorm.DB, id uint) siblingConversationState {
	t.Helper()
	var conversation domain.Conversation
	require.NoError(t, con.First(&conversation, id).Error)
	state := siblingConversationState{Status: conversation.Status, Priority: conversation.Priority, Muted: conversation.Muted, Snoozed: conversation.SnoozedUntil != nil, Labels: []string{}}
	if conversation.AssigneeID != nil {
		state.Assignee = *conversation.AssigneeID
	}
	if conversation.TeamID != nil {
		state.Team = *conversation.TeamID
	}
	var links []domain.ConversationLabel
	require.NoError(t, con.Where("conversation_id = ?", id).Find(&links).Error)
	for _, link := range links {
		var label domain.Label
		require.NoError(t, cus.Where("account_id = ? AND id = ?", conversation.AccountID, link.LabelID).First(&label).Error)
		state.Labels = append(state.Labels, label.Title)
	}
	sort.Strings(state.Labels)
	return state
}

func drainSiblingParityRunner(t *testing.T, f *automationActionFixture) {
	t.Helper()
	for i := 0; i < 200; i++ {
		ran, err := f.aut.RunAutomationOnce(context.Background())
		require.NoError(t, err)
		if !ran {
			var unfinished []domain.AutomationRuleExecution
			require.NoError(t, f.aut.DB.Where("status IN ?", []string{"received", "evaluating", "running", "waiting"}).Find(&unfinished).Error)
			require.Empty(t, unfinished, "runner became idle with unfinished siblings: %s", fmt.Sprint(unfinished))
			return
		}
	}
	t.Fatal("sibling runner failed to become idle")
}
