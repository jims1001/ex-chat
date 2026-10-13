package modruntime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/stretchr/testify/require"
)

type failureParityCase struct {
	name                 string
	actions              string
	denyTeam             bool
	legacyStatus         string
	legacyActionStatuses []string
	durableStatus        string
	failureReason        string
	durableStepStatuses  []string
	finalStatus          string
	finalPriority        string
	finalLabels          []string
}

// G10 is a differential test against the unchanged legacy action loop, not a
// second implementation of that loop. The same explicit team-owner rejection
// is injected at each service boundary; successful CON/CUS effects remain real.
// Legacy treats unsupported actions and absent foreign teams as successful
// no-ops. Durable execution must report those failures truthfully while retaining
// parity for every supported local effect that follows them.
func TestAUTFailureContinuationMatchesActualLegacyService(t *testing.T) {
	cases := []failureParityCase{
		{
			name: "first_team_failure_does_not_skip_later_priority",
			actions: `[{"action_name":"assign_team","action_params":[3]},
				{"action_name":"change_priority","action_params":["urgent"]}]`,
			denyTeam: true, legacyStatus: "partial_failed", legacyActionStatuses: []string{"failed", "success"},
			durableStatus: "partially_succeeded", durableStepStatuses: []string{"failed", "succeeded"},
			finalStatus: "open", finalPriority: "urgent", finalLabels: []string{"continued"},
		},
		{
			name: "middle_team_failure_preserves_effects_on_both_sides",
			actions: `[{"action_name":"change_priority","action_params":["urgent"]},
				{"action_name":"assign_team","action_params":[3]},
				{"action_name":"resolve_conversation"}]`,
			denyTeam: true, legacyStatus: "partial_failed", legacyActionStatuses: []string{"success", "failed", "success"},
			durableStatus: "partially_succeeded", durableStepStatuses: []string{"succeeded", "failed", "succeeded"},
			finalStatus: "resolved", finalPriority: "urgent", finalLabels: []string{"continued"},
		},
		{
			name: "last_team_failure_retains_successful_prefix",
			actions: `[{"action_name":"change_priority","action_params":["urgent"]},
				{"action_name":"resolve_conversation"},
				{"action_name":"assign_team","action_params":[3]}]`,
			denyTeam: true, legacyStatus: "partial_failed", legacyActionStatuses: []string{"success", "success", "failed"},
			durableStatus: "partially_succeeded", durableStepStatuses: []string{"succeeded", "succeeded", "failed"},
			finalStatus: "resolved", finalPriority: "urgent", finalLabels: []string{"continued"},
		},
		{
			name: "unsupported_between_local_actions_is_truthful_without_suppressing_effects",
			actions: `[{"action_name":"add_label","action_params":["before"]},
				{"action_name":"send_webhook","action_params":["unsupported-placeholder"]},
				{"action_name":"change_priority","action_params":["urgent"]}]`,
			legacyStatus: "success", legacyActionStatuses: []string{"success", "success", "success"},
			durableStatus: "partially_succeeded", failureReason: "action_capability_executor_unavailable", durableStepStatuses: []string{"succeeded", "succeeded", "failed", "succeeded"},
			finalStatus: "open", finalPriority: "urgent", finalLabels: []string{"before", "continued"},
		},
		{
			name: "foreign_team_rejection_does_not_stop_same_tenant_actions",
			actions: `[{"action_name":"assign_team","action_params":[4]},
				{"action_name":"change_priority","action_params":["urgent"]}]`,
			legacyStatus: "success", legacyActionStatuses: []string{"success", "success"},
			durableStatus: "partially_succeeded", durableStepStatuses: []string{"failed", "succeeded"},
			finalStatus: "open", finalPriority: "urgent", finalLabels: []string{"continued"},
		},
		{
			name:     "entirely_failed_rule_still_releases_later_siblings",
			actions:  `[{"action_name":"assign_team","action_params":[3]}]`,
			denyTeam: true, legacyStatus: "failed", legacyActionStatuses: []string{"failed"},
			durableStatus: "failed", durableStepStatuses: []string{"failed"},
			finalStatus: "open", finalPriority: "medium", finalLabels: []string{"continued"},
		},
	}
	for _, eventName := range []string{"conversation_created", "conversation_updated"} {
		for _, tc := range cases {
			t.Run(eventName+"/"+tc.name, func(t *testing.T) {
				// The middle sibling is deliberately false after the first rule's
				// partial effects. A later true sibling must cross that no-match row.
				nonMatchingPriority := "medium"
				if tc.finalPriority == "medium" {
					nonMatchingPriority = "low"
				}
				fixtureCase := siblingParityCase{priority: "medium", rules: []siblingParityRule{
					{"[]", tc.actions},
					{siblingCondition("priority", "is", nonMatchingPriority), siblingAction("change_priority", "low")},
					{siblingCondition("priority", "is", tc.finalPriority), siblingAction("add_label", "continued")},
					{siblingCondition("team_id", "is_present"), siblingAction("change_priority", "low")},
				}}
				f, rules := newSiblingParityFixture(t, fixtureCase, eventName)
				foreign := f.conversation
				foreign.ID, foreign.AccountID, foreign.InboxID, foreign.ContactID = f.conversation.ID+1000, 92, 12, 22
				foreign.Status, foreign.Priority = "pending", "low"
				require.NoError(t, f.con.Create(&foreign).Error)
				foreignRule := domain.AutomationRule{ID: rules[len(rules)-1].ID + 1000, AccountID: 92, Name: "Foreign", EventName: eventName, Conditions: "[]", Actions: siblingAction("resolve_conversation"), Active: true}
				require.NoError(t, f.aut.DB.Create(&foreignRule).Error)
				foreignBefore := siblingParityState(t, f.con, f.owners["CUS"].DB, foreign.ID)

				legacyState, legacyRows, legacyTeamCalls := runActualFailureLegacy(t, f, tc, rules, foreign, foreignRule, eventName)
				require.Len(t, legacyRows, 2, "legacy must execute the first and last matching siblings")
				require.Equal(t, rules[0].ID, legacyRows[0].RuleID)
				require.Equal(t, rules[2].ID, legacyRows[1].RuleID)
				require.Equal(t, tc.legacyStatus, legacyRows[0].Status)
				require.Equal(t, "success", legacyRows[1].Status)
				var legacyResults []service.ActionResult
				require.NoError(t, json.Unmarshal([]byte(legacyRows[0].ActionResults), &legacyResults))
				legacyStatuses := make([]string, len(legacyResults))
				for i, result := range legacyResults {
					legacyStatuses[i] = result.Status
					if result.Status == "failed" {
						require.Equal(t, "controlled team owner denial", result.Error)
					}
				}
				require.Equal(t, tc.legacyActionStatuses, legacyStatuses, "fixture must exercise the actual legacy action loop")
				require.Equal(t, tc.finalStatus, legacyState.Status)
				require.Equal(t, tc.finalPriority, legacyState.Priority)
				require.Equal(t, tc.finalLabels, legacyState.Labels)
				require.Zero(t, legacyState.Team)

				durableTeamCalls := 0
				var durableTeamAccount uint
				if tc.denyTeam {
					original := f.owners["TEN"].Contracts.queries["ten.permissions.check.v1"]
					f.owners["TEN"].Contracts.queries["ten.permissions.check.v1"] = func(ctx context.Context, query foundation.QueryEnvelope) (any, error) {
						if _, exists := query.Filter["team_id"]; exists {
							durableTeamCalls++
							durableTeamAccount = query.AccountID
							return nil, &ContractFailure{Type: "permission_denied", Message: "controlled team owner denial"}
						}
						return original(ctx, query)
					}
				}
				event := bridgeFact(f.autRunnerFixture, eventName)
				if eventName == "conversation_updated" {
					source := triggerAliasSource(f.autRunnerFixture)
					require.NoError(t, source.UpdatePriority(91, f.conversation.ID, fixtureCase.priority))
					event = triggerAliasFact(t, source)
				}
				require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), event))
				drainSiblingParityRunner(t, f)
				rows := causalExecutions(t, f.autRunnerFixture, event.EventID)
				require.Len(t, rows, len(rules))
				require.Equal(t, tc.durableStatus, rows[0].Status, "first execution=%+v", rows[0])
				require.NotEmpty(t, rows[0].Error, "partial success must retain truthful failure summary")
				var results []automationActionResult
				require.NoError(t, json.Unmarshal([]byte(rows[0].ActionResults), &results))
				failureReason := tc.failureReason
				if failureReason == "" {
					failureReason = "owner_command_rejected"
				}
				statuses := make([]string, len(results))
				for i, result := range results {
					statuses[i] = result.Status
					if result.Status == "failed" {
						require.Equal(t, failureReason, result.Reason)
						require.Zero(t, result.ObjectVersion)
						require.Zero(t, result.ResourceID)
						require.Empty(t, result.Outcome)
					}
				}
				require.Equal(t, tc.durableStepStatuses, statuses, "independent actions after a confirmed failure must still run")
				for i, row := range rows {
					require.Equal(t, rules[i].ID, row.RuleID)
					require.Equal(t, uint(91), row.AccountID)
					require.NotNil(t, row.CompletedAt)
					if i > 0 {
						require.NotNil(t, row.RunnerPredecessorID)
						require.Equal(t, rows[i-1].ID, *row.RunnerPredecessorID)
					}
					if i == 1 || i == 3 {
						require.Equal(t, "no_match", row.Status)
						require.Equal(t, "conditions_not_matched", row.Error)
						var skipped []automationActionResult
						require.NoError(t, json.Unmarshal([]byte(row.ActionResults), &skipped))
						require.NotEmpty(t, skipped)
						for _, result := range skipped {
							require.Equal(t, "skipped", result.Status)
						}
					} else if i == 2 {
						require.Equal(t, "succeeded", row.Status)
					}
				}
				if tc.denyTeam {
					require.Equal(t, 1, legacyTeamCalls)
					require.Equal(t, uint(91), durableTeamAccount)
					require.Equal(t, legacyTeamCalls, durableTeamCalls, "a confirmed rejection must not be retried")
				}
				require.Equal(t, legacyState, siblingParityState(t, f.con, f.owners["CUS"].DB, f.conversation.ID))
				require.Equal(t, foreignBefore, siblingParityState(t, f.con, f.owners["CUS"].DB, foreign.ID))
				var foreignExecutions int64
				require.NoError(t, f.aut.DB.Model(&domain.AutomationRuleExecution{}).Where("account_id = ?", 92).Count(&foreignExecutions).Error)
				require.Zero(t, foreignExecutions)

				// Replay cannot revisit a rejected action, duplicate local effects,
				// or revise a sibling's frozen decision once every row is terminal.
				finalState := f.conversationState()
				f.aut.Config.Dependencies = map[string]DependencyConfig{}
				require.NoError(t, f.aut.AcceptAutomationFact(context.Background(), event))
				require.Equal(t, rows, causalExecutions(t, f.autRunnerFixture, event.EventID))
				ran, err := f.aut.RunAutomationOnce(context.Background())
				require.NoError(t, err)
				require.False(t, ran)
				require.Equal(t, finalState, f.conversationState())
			})
		}
	}
}

type failureParityTeamPort struct {
	repository *repository.TeamRepository
	deny       bool
	calls      int
}

func (p *failureParityTeamPort) FindSummaryByID(accountID, id uint) (*domain.Team, error) {
	p.calls++
	if p.deny {
		return nil, errors.New("controlled team owner denial")
	}
	return p.repository.FindSummaryByID(accountID, id)
}

func runActualFailureLegacy(t *testing.T, f *automationActionFixture, tc failureParityCase, rules []domain.AutomationRule, foreign domain.Conversation, foreignRule domain.AutomationRule, eventName string) (siblingConversationState, []domain.AutomationRuleExecution, int) {
	t.Helper()
	db := newModuleTestDB(t, "AUT")
	require.NoError(t, db.AutoMigrate(&domain.Conversation{}, &domain.ConversationLabel{}, &domain.Label{}, &domain.User{}, &domain.AccountUser{}, &domain.Team{}))
	conversation := f.conversation
	for _, value := range []any{
		&conversation, &foreign,
		&domain.Team{ID: 3, AccountID: 91, Name: "support"},
		&domain.Team{ID: 4, AccountID: 92, Name: "foreign"},
		&foreignRule,
	} {
		require.NoError(t, db.Create(value).Error)
	}
	for _, rule := range rules {
		copy := rule
		require.NoError(t, db.Create(&copy).Error)
	}
	foreignBefore := siblingParityState(t, db, db, foreign.ID)
	convRepo := repository.NewConversationRepository(db)
	teamPort := &failureParityTeamPort{repository: repository.NewTeamRepository(db), deny: tc.denyTeam}
	legacy := service.NewAutomationServiceWithOwnerPorts(db, convRepo, repository.NewMessageRepository(db), repository.NewLabelRepositoryWithConversationPort(db, convRepo), repository.NewAccountRepository(db), teamPort, repository.NewContactRepository(db))
	if eventName == "conversation_updated" {
		require.NoError(t, convRepo.UpdatePriority(91, conversation.ID, f.conversation.Priority))
		require.NoError(t, db.First(&conversation, conversation.ID).Error)
		legacy.HandleConversationUpdated(&conversation)
	} else {
		legacy.HandleConversationCreated(&conversation)
	}
	var rows []domain.AutomationRuleExecution
	require.NoError(t, db.Order("id ASC").Find(&rows).Error)
	for _, row := range rows {
		require.Equal(t, uint(91), row.AccountID)
		require.Equal(t, conversation.ID, row.ConversationID)
	}
	require.Equal(t, foreignBefore, siblingParityState(t, db, db, foreign.ID))
	return siblingParityState(t, db, db, conversation.ID), rows, teamPort.calls
}
