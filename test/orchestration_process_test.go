package test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
)

type orchestrationRegistry struct {
	Version, FoundationVersion, ReopenTask, Orchestrator        string
	States, FailurePolicies, StepResultStatuses, TerminalStates []string
	Transitions                                                 map[string][]string
	Rules                                                       map[string]bool
	Processes                                                   []struct {
		ID, Version  string
		Modules      []string
		Participants []struct{ Module, Interaction, Contract string }
		Steps        []struct {
			ID, Module, Command, ResultQuery, FailurePolicy, Compensation string
			TimeoutMS                                                     int
			Irreversible                                                  bool
			Preconditions                                                 []string
		}
	}
}

func TestOrchestrationRegistryIsCompleteAndFrozen(t *testing.T) {
	raw, err := os.ReadFile("../architecture/orchestration_processes.json")
	if err != nil {
		t.Fatal(err)
	}
	const approvedSHA = "7d79a39a5665afd6e595c98be6a6f51a4c38cb83213aedb3d9f5482f00d2b71c"
	if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != approvedSHA {
		t.Fatalf("orchestration registry changed (%s); reopen ARCH-07", got)
	}
	var registry orchestrationRegistry
	if err := json.Unmarshal(raw, &registry); err != nil {
		t.Fatal(err)
	}
	if registry.Version != "orchestration-processes-v1" || registry.FoundationVersion != "foundation-v1" || registry.ReopenTask != "ARCH-07" || registry.Orchestrator != "ORC" {
		t.Fatal("invalid orchestration metadata")
	}
	for _, rule := range []string{"singleOrchestrator", "publicCommandsOnly", "requireIdempotency", "queryAfterTimeout", "explicitCompensation", "persistCheckpoint", "optimisticVersion", "auditEveryStep", "forbidDomainAuthority"} {
		if !registry.Rules[rule] {
			t.Fatalf("orchestration rule %s disabled", rule)
		}
	}
	if !reflect.DeepEqual(sortedBoolKeys(foundation.ProcessStates), sortedStrings(registry.States)) {
		t.Fatal("runtime process states differ from registry")
	}
	if !reflect.DeepEqual(sortedBoolKeys(foundation.ProcessFailurePolicies), sortedStrings(registry.FailurePolicies)) {
		t.Fatal("runtime failure policies differ from registry")
	}
	if !reflect.DeepEqual(sortedBoolKeys(foundation.ProcessStepStatuses), sortedStrings(registry.StepResultStatuses)) || !reflect.DeepEqual(sortedBoolKeys(foundation.ProcessTerminalStates), sortedStrings(registry.TerminalStates)) {
		t.Fatal("runtime result or terminal states differ from registry")
	}
	for state, declaredTargets := range registry.Transitions {
		runtimeTargets := sortedBoolKeys(foundation.ProcessStateTransitions[state])
		if !reflect.DeepEqual(runtimeTargets, sortedStrings(declaredTargets)) {
			t.Fatalf("transition mismatch for %s", state)
		}
	}
	if len(registry.Transitions) != len(foundation.ProcessStates) {
		t.Fatal("transition matrix does not cover every state")
	}
	commands, queries, owners := loadModuleContracts(t)
	wantProcesses := []string{"account_deletion", "ai_answer", "automatic_assignment", "automation_action", "campaign_delivery", "import_migration", "inbound_message", "outbound_message"}
	actualProcesses := []string{}
	for _, process := range registry.Processes {
		actualProcesses = append(actualProcesses, process.ID)
		if process.Version == "" || len(process.Modules) < 2 || len(process.Steps) < 2 {
			t.Fatalf("incomplete cross-module process %s", process.ID)
		}
		moduleSet := map[string]bool{}
		coveredModules := map[string]bool{}
		for _, module := range process.Modules {
			if moduleSet[module] {
				t.Fatalf("process %s declares duplicate module %s", process.ID, module)
			}
			moduleSet[module] = true
		}
		stepIDs := map[string]bool{}
		idempotencyKeys := map[string]bool{}
		participants := map[string]bool{}
		for _, participant := range process.Participants {
			participantKey := participant.Module + ":" + participant.Interaction + ":" + participant.Contract
			if participants[participantKey] || participant.Interaction != "query" || !moduleSet[participant.Module] || !queries[participant.Contract] || owners[participant.Contract] != participant.Module {
				t.Fatalf("invalid participant %s/%s", process.ID, participant.Module)
			}
			participants[participantKey] = true
			coveredModules[participant.Module] = true
		}
		declaredStepOrder := make([]string, 0, len(process.Steps))
		for _, rawStep := range process.Steps {
			if stepIDs[rawStep.ID] || !moduleSet[rawStep.Module] {
				t.Fatalf("invalid step ownership %s/%s", process.ID, rawStep.ID)
			}
			stepIDs[rawStep.ID] = true
			declaredStepOrder = append(declaredStepOrder, rawStep.ID)
			coveredModules[rawStep.Module] = true
			step := foundation.ProcessStepDefinition{ID: rawStep.ID, Module: rawStep.Module, Command: rawStep.Command, ResultQuery: rawStep.ResultQuery, TimeoutMS: rawStep.TimeoutMS, FailurePolicy: rawStep.FailurePolicy, Compensation: rawStep.Compensation, Irreversible: rawStep.Irreversible, Preconditions: rawStep.Preconditions}
			if err := step.Validate(commands, queries); err != nil {
				t.Fatalf("%s/%s: %v", process.ID, rawStep.ID, err)
			}
			key, err := step.IdempotencyKey("process-sample", process.Version)
			if err != nil || idempotencyKeys[key] {
				t.Fatalf("unstable or duplicate idempotency key for %s/%s", process.ID, rawStep.ID)
			}
			idempotencyKeys[key] = true
			if owners[rawStep.Command] != rawStep.Module || owners[rawStep.ResultQuery] != rawStep.Module {
				t.Fatalf("step %s/%s crosses command owner", process.ID, rawStep.ID)
			}
			if rawStep.Compensation != "" && owners[rawStep.Compensation] != rawStep.Module {
				t.Fatalf("compensation owner mismatch for %s/%s", process.ID, rawStep.ID)
			}
		}
		if !reflect.DeepEqual(declaredStepOrder, foundation.ProcessStepOrders[process.ID]) {
			t.Fatalf("process %s runtime step order differs from registry", process.ID)
		}
		if process.ID == "account_deletion" {
			expectedCommands := map[string]string{
				"schedule": "ten.account_deletion.schedule.v1", "access": "iam.account_access.revoke.v1", "channels": "chn.account_channels.revoke.v1",
				"contacts": "cus.account_contacts.anonymize.v1", "media": "med.account_assets.purge.v1", "conversations": "con.account_conversations.anonymize.v1",
				"messages": "msg.account_messages.anonymize.v1", "tickets": "tkt.account_tickets.anonymize.v1", "knowledge": "kb.account_content.purge.v1",
				"plugins": "ext.account_authorizations.revoke.v1", "notifications": "ntf.account_subscriptions.purge.v1", "campaigns": "ops.account_campaigns.cancel.v1",
				"ai_index": "aic.account_index.purge.v1", "reporting": "rpt.account_projection.purge.v1", "search": "srh.account_index.purge.v1",
				"realtime": "rtm.account_subscriptions.close.v1", "audit": "aud.retention.set.v1", "complete": "ten.account_deletion.finalize.v1",
			}
			for _, step := range process.Steps {
				if expectedCommands[step.ID] != step.Command {
					t.Fatalf("account deletion step %s has non-semantic command %s", step.ID, step.Command)
				}
			}
			if len(expectedCommands) != len(process.Steps) {
				t.Fatal("account deletion semantic command coverage is incomplete")
			}
		}
		if !reflect.DeepEqual(sortedBoolKeys(coveredModules), sortedBoolKeys(moduleSet)) {
			t.Fatalf("process %s has uncovered modules", process.ID)
		}
	}
	sort.Strings(actualProcesses)
	if !reflect.DeepEqual(actualProcesses, wantProcesses) {
		t.Fatalf("processes=%v want=%v", actualProcesses, wantProcesses)
	}
	if !reflect.DeepEqual(sortedBoolKeys(foundation.ProcessTypes), wantProcesses) {
		t.Fatal("runtime process types differ from registry")
	}
	if len(foundation.ProcessStepOrders) != len(registry.Processes) || len(foundation.ProcessReferenceKeys) != len(registry.Processes) {
		t.Fatal("runtime orchestration maps contain undeclared process types")
	}
	stepOrderTypes, referenceTypes := []string{}, []string{}
	for processType := range foundation.ProcessStepOrders {
		stepOrderTypes = append(stepOrderTypes, processType)
	}
	for processType := range foundation.ProcessReferenceKeys {
		referenceTypes = append(referenceTypes, processType)
	}
	sort.Strings(stepOrderTypes)
	sort.Strings(referenceTypes)
	if !reflect.DeepEqual(stepOrderTypes, wantProcesses) || !reflect.DeepEqual(referenceTypes, wantProcesses) {
		t.Fatal("runtime orchestration maps use undeclared process keys")
	}
}

func TestProcessIntentCheckpointTransitionAndFailureGuards(t *testing.T) {
	now := time.Now().UTC()
	intent := foundation.ProcessIntent{ProcessID: "p1", ProcessType: "outbound_message", ProcessVersion: "v1", CorrelationID: "corr", CausationID: "cause", AccountID: 1, Actor: foundation.ContractActor{Type: "User", ID: "1", AuthSource: "jwt"}, State: "created", Version: 1, References: map[string]string{"conversation_id": "1"}, CreatedAt: now}
	if err := intent.Validate(); err != nil {
		t.Fatal(err)
	}
	intent.References = nil
	if intent.Validate() == nil {
		t.Fatal("intent with domain payload or missing references passed")
	}
	intent.References = map[string]string{"conversation_id": "{\"body\":\"forbidden\"}"}
	if intent.Validate() == nil {
		t.Fatal("domain body passed as stable reference")
	}
	intent.References = map[string]string{"conversation_id": "cGxhaW4tdGV4dA=="}
	if intent.Validate() == nil {
		t.Fatal("encoded payload passed as stable reference")
	}
	intent.References = map[string]string{"unexpected_id": "1"}
	if intent.Validate() == nil {
		t.Fatal("undeclared reference key passed")
	}
	intent.References = map[string]string{"conversation_id": "1"}
	intent.References = map[string]string{}
	if intent.Validate() == nil {
		t.Fatal("empty references passed")
	}
	intent.References = map[string]string{"conversation_id": "letters-only"}
	if intent.Validate() == nil {
		t.Fatal("non-identifier reference passed")
	}
	intent.References = map[string]string{"conversation_id": "1"}
	stepResult := foundation.ProcessStepResult{StepID: "message", CommandID: "cmd-1", IdempotencyKey: "p1:v1:message", Status: "completed", CorrelationID: "corr", ChangeID: "change-1", AuditID: "audit-1", Attempt: 1, StartedAt: now.Add(-time.Second), FinishedAt: now}
	if err := stepResult.Validate(); err != nil {
		t.Fatal(err)
	}
	checkpoint := foundation.ProcessCheckpoint{ProcessID: "p1", ProcessType: "outbound_message", ProcessVersion: "v1", State: "waiting", CurrentStepID: "provider", CorrelationID: "corr", AccountID: 1, ExpectedVersion: 1, Version: 2, StepOrder: []string{"message", "provider", "status"}, CompletedSteps: []string{"message"}, StepResults: map[string]foundation.ProcessStepResult{"message": stepResult}, UpdatedAt: now}
	if err := checkpoint.Validate(); err != nil {
		t.Fatal(err)
	}
	checkpoint.StepOrder = []string{"provider", "message", "status"}
	if checkpoint.Validate() == nil {
		t.Fatal("caller-defined step order passed")
	}
	checkpoint.StepOrder = []string{"message", "provider", "status"}
	checkpoint.CompletedSteps = []string{}
	if checkpoint.Validate() == nil {
		t.Fatal("completed result missing from completed steps passed")
	}
	checkpoint.CompletedSteps = []string{"message"}
	checkpoint.CurrentStepID = ""
	if checkpoint.Validate() == nil {
		t.Fatal("non-resumable checkpoint passed")
	}
	for _, transition := range [][2]string{{"created", "running"}, {"running", "waiting"}, {"waiting", "running"}, {"running", "compensating"}, {"compensating", "failed"}} {
		if err := foundation.ValidateProcessTransition(transition[0], transition[1]); err != nil {
			t.Fatal(err)
		}
	}
	if foundation.ValidateProcessTransition("completed", "running") == nil {
		t.Fatal("terminal process restarted")
	}
	commands := map[string]bool{"msg.message.create.v1": true}
	queries := map[string]bool{"msg.message.get.v1": true}
	bad := foundation.ProcessStepDefinition{ID: "send", Module: "MSG", Command: "msg.message.create.v1", ResultQuery: "msg.message.get.v1", TimeoutMS: 1000, FailurePolicy: "compensate", Irreversible: true}
	if bad.Validate(commands, queries) == nil {
		t.Fatal("irreversible step without precondition or compensation passed")
	}
}

func loadModuleContracts(t *testing.T) (map[string]bool, map[string]bool, map[string]string) {
	t.Helper()
	raw, err := os.ReadFile("../architecture/module_contracts.json")
	if err != nil {
		t.Fatal(err)
	}
	var registry struct {
		Modules []struct {
			Name              string
			Commands, Queries []string
		}
	}
	if err := json.Unmarshal(raw, &registry); err != nil {
		t.Fatal(err)
	}
	commands, queries, owners := map[string]bool{}, map[string]bool{}, map[string]string{}
	for _, module := range registry.Modules {
		for _, command := range module.Commands {
			commands[command], owners[command] = true, module.Name
		}
		for _, query := range module.Queries {
			queries[query], owners[query] = true, module.Name
		}
	}
	return commands, queries, owners
}

func sortedBoolKeys(values map[string]bool) []string {
	keys := []string{}
	for key, enabled := range values {
		if enabled {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}
func sortedStrings(values []string) []string {
	result := append([]string{}, values...)
	sort.Strings(result)
	return result
}

func TestOrchestrationDoesNotStoreDomainAuthority(t *testing.T) {
	typeOfIntent := reflect.TypeOf(foundation.ProcessIntent{})
	for i := 0; i < typeOfIntent.NumField(); i++ {
		name := strings.ToLower(typeOfIntent.Field(i).Name)
		for _, forbidden := range []string{"contact", "conversation", "message", "payment", "knowledge"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("process intent owns domain field %s", name)
			}
		}
	}
}
