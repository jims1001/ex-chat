package test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
)

type moduleContractRegistry struct {
	Version           string `json:"version"`
	FoundationVersion string `json:"foundationVersion"`
	ReopenTask        string `json:"reopenTask"`
	Rules             struct {
		CommandOwnerCount         int  `json:"commandOwnerCount"`
		QuerySideEffects          bool `json:"querySideEffects"`
		EventsArePersistedFacts   bool `json:"eventsArePersistedFacts"`
		EventsMayRequestActions   bool `json:"eventsMayRequestActions"`
		RequireIdempotency        bool `json:"requireIdempotency"`
		RequireVersion            bool `json:"requireVersion"`
		RequireCorrelation        bool `json:"requireCorrelation"`
		RequireCausation          bool `json:"requireCausation"`
		RequireTimeoutPolicy      bool `json:"requireTimeoutPolicy"`
		RequireDataClassification bool `json:"requireDataClassification"`
	} `json:"rules"`
	Modules []struct {
		Name     string   `json:"name"`
		Commands []string `json:"commands"`
		Queries  []string `json:"queries"`
		Events   []string `json:"events"`
	} `json:"modules"`
}

func jsonFields(value any) map[string]bool {
	typ := reflect.TypeOf(value)
	fields := make(map[string]bool, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		name := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		fields[name] = true
	}
	return fields
}

func TestModuleContractRegistryIsCompleteAndFrozen(t *testing.T) {
	raw, err := os.ReadFile("../architecture/module_contracts.json")
	if err != nil {
		t.Fatal(err)
	}
	const approvedSHA = "a262a62fddc63fcd137a0f715e730f21da29472211812bd1e1159a8472a5ce51"
	if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != approvedSHA {
		t.Fatalf("module contracts changed (%s); reopen ARCH-04 and approve a new fingerprint", got)
	}
	var registry moduleContractRegistry
	if err := json.Unmarshal(raw, &registry); err != nil {
		t.Fatal(err)
	}
	if registry.Version != "module-contracts-v1" || registry.FoundationVersion != "foundation-v1" || registry.ReopenTask != "ARCH-04" {
		t.Fatal("invalid contract registry metadata")
	}
	r := registry.Rules
	if r.CommandOwnerCount != 1 || r.QuerySideEffects || !r.EventsArePersistedFacts || r.EventsMayRequestActions || !r.RequireIdempotency || !r.RequireVersion || !r.RequireCorrelation || !r.RequireCausation || !r.RequireTimeoutPolicy || !r.RequireDataClassification {
		t.Fatalf("invalid contract rules: %+v", r)
	}

	depRaw, err := os.ReadFile("../architecture/module_dependencies.json")
	if err != nil {
		t.Fatal(err)
	}
	var deps architectureDependencyManifest
	if err := json.Unmarshal(depRaw, &deps); err != nil {
		t.Fatal(err)
	}
	expected := make([]string, 0, len(deps.Modules))
	for _, module := range deps.Modules {
		expected = append(expected, module.Name)
	}
	sort.Strings(expected)

	contractName := regexp.MustCompile(`^[a-z]{2,3}\.[a-z0-9_]+(?:\.[a-z0-9_]+)*\.v[1-9][0-9]*$`)
	factSuffixes := map[string]bool{"created": true, "revoked": true, "changed": true, "exhausted": true, "ready": true, "rejected": true, "deleted": true, "merged": true, "received": true, "rebuilt": true, "failed": true, "invalidated": true, "executed": true, "blocked": true, "installed": true, "disabled": true, "read": true, "started": true, "paused": true, "completed": true, "invoked": true, "progressed": true, "applied": true, "breached": true, "refunded": true, "connected": true, "disconnected": true, "ingested": true, "triggered": true, "compensating": true, "commented": true, "published": true, "archived": true}
	owners := map[string]string{}
	actual := make([]string, 0, len(registry.Modules))
	for _, module := range registry.Modules {
		actual = append(actual, module.Name)
		if len(module.Commands) == 0 || len(module.Queries) == 0 || len(module.Events) == 0 {
			t.Fatalf("module %s must register Command, Query and Event", module.Name)
		}
		prefix := strings.ToLower(module.Name) + "."
		for kind, names := range map[string][]string{"command": module.Commands, "query": module.Queries, "event": module.Events} {
			seen := map[string]bool{}
			for _, name := range names {
				if !contractName.MatchString(name) || !strings.HasPrefix(name, prefix) {
					t.Fatalf("invalid %s contract %s for %s", kind, name, module.Name)
				}
				parts := strings.Split(name, ".")
				if kind == "event" && !factSuffixes[parts[len(parts)-2]] {
					t.Fatalf("event %s lacks an approved fact suffix", name)
				}
				if seen[name] {
					t.Fatalf("duplicate %s contract %s", kind, name)
				}
				seen[name] = true
				key := kind + ":" + name
				if prior, exists := owners[key]; exists {
					t.Fatalf("%s has multiple owners: %s and %s", name, prior, module.Name)
				}
				owners[key] = module.Name
			}
		}
	}
	sort.Strings(actual)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("contract module set mismatch: got %v want %v", actual, expected)
	}
}

func TestPublicContractEnvelopesContainRequiredFieldsAndValidate(t *testing.T) {
	for name, tc := range map[string]struct {
		value    any
		required []string
	}{
		"command":    {foundation.CommandEnvelope{}, []string{"command_id", "command_type", "account_id", "actor", "target", "input", "idempotency_key", "correlation_id", "causation_id", "requested_at", "timeout_policy"}},
		"query":      {foundation.QueryEnvelope{}, []string{"query_type", "account_id", "actor", "filter", "sort", "page", "cursor", "fields", "consistency", "requested_at"}},
		"event":      {foundation.EventEnvelope{}, []string{"event_id", "event_type", "event_version", "source_module", "account_id", "aggregate_type", "aggregate_id", "aggregate_version", "occurred_at", "published_at", "actor", "correlation_id", "causation_id", "changed_fields", "payload", "data_classification"}},
		"signal":     {foundation.ProcessSignal{}, []string{"signal_id", "signal_type", "version", "source_module", "account_id", "process_id", "step_id", "status", "correlation_id", "causation_id", "occurred_at", "payload"}},
		"capability": {foundation.CapabilityCall{}, []string{"capability", "command", "query"}},
		"error":      {foundation.ContractError{}, []string{"type", "message", "retryable", "retry_after_ms", "correlation_id", "details"}},
		"actor":      {foundation.ContractActor{}, []string{"type", "id", "auth_source"}},
		"target":     {foundation.ContractTarget{}, []string{"type", "id", "expected_version"}},
		"timeout":    {foundation.TimeoutPolicy{}, []string{"timeout_ms", "on_timeout", "status_query", "compensation"}},
	} {
		fields := jsonFields(tc.value)
		for _, required := range tc.required {
			if !fields[required] {
				t.Errorf("%s envelope missing %s", name, required)
			}
		}
	}
	now := time.Now().UTC()
	actor := foundation.ContractActor{Type: "User", ID: "1", AuthSource: "jwt"}
	command := foundation.CommandEnvelope{CommandID: "c1", CommandType: "con.status.change.v1", AccountID: 1, Actor: actor, Target: foundation.ContractTarget{Type: "conversation", ID: "2"}, Input: map[string]any{"status": "resolved"}, IdempotencyKey: "i1", CorrelationID: "corr", CausationID: "cause", RequestedAt: now, TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "con.conversation.get.v1"}}
	if err := command.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := command.ValidateWithQueries(map[string]bool{"con.conversation.get.v1": true}); err != nil {
		t.Fatal(err)
	}
	command.TimeoutPolicy.StatusQuery = "missing.query.v1"
	if command.ValidateWithQueries(map[string]bool{"con.conversation.get.v1": true}) == nil {
		t.Fatal("unregistered timeout query passed")
	}
	command.TimeoutPolicy.StatusQuery = "con.conversation.get.v1"
	command.TimeoutPolicy.OnTimeout = "retry"
	if command.Validate() == nil {
		t.Fatal("unknown timeout policy passed")
	}
	command.TimeoutPolicy.OnTimeout = "query"
	command.CorrelationID = ""
	if command.Validate() == nil {
		t.Fatal("command without correlation_id passed")
	}
	command.CorrelationID = "corr"
	command.Actor.ID = ""
	if command.Validate() == nil {
		t.Fatal("command without actor id passed")
	}
	command.Actor.ID = "1"
	query := foundation.QueryEnvelope{QueryType: "con.conversation.get.v1", AccountID: 1, Actor: actor, Filter: map[string]any{"id": 2}, Sort: []string{"id"}, Page: "1", Fields: []string{"id"}, Consistency: "current", RequestedAt: now}
	if err := query.Validate(); err != nil {
		t.Fatal(err)
	}
	event := foundation.EventEnvelope{EventID: "e1", EventType: "con.conversation_status.changed.v1", EventVersion: "v1", SourceModule: "CON", AccountID: 1, AggregateType: "conversation", AggregateID: "2", AggregateVersion: 2, OccurredAt: now, PublishedAt: now, Actor: actor, CorrelationID: "corr", CausationID: "c1", ChangedFields: []string{"status"}, Payload: map[string]any{"status": "resolved"}, DataClassification: "internal"}
	if err := event.Validate(); err != nil {
		t.Fatal(err)
	}
	event.DataClassification = ""
	if event.Validate() == nil {
		t.Fatal("event without data classification passed")
	}
	capability := foundation.CapabilityCall{Capability: "conversation.write", Command: &command}
	if err := capability.Validate(); err != nil {
		t.Fatal(err)
	}
	capability.Query = &query
	if capability.Validate() == nil {
		t.Fatal("capability with command and query passed")
	}
	signal := foundation.ProcessSignal{SignalID: "s1", SignalType: "step.completed.v1", Version: "v1", SourceModule: "CON", AccountID: 1, ProcessID: "p1", StepID: "step1", Status: "completed", CorrelationID: "corr", CausationID: "c1", OccurredAt: now, Payload: map[string]any{}}
	if err := signal.Validate(); err != nil {
		t.Fatal(err)
	}
	signal.StepID = ""
	if signal.Validate() == nil {
		t.Fatal("signal without step id passed")
	}
	if len(foundation.ContractErrorTypes) != 11 {
		t.Fatalf("error contract count=%d", len(foundation.ContractErrorTypes))
	}
	for _, errorType := range foundation.ContractErrorTypes {
		if foundation.ContractErrorRetryPolicy[errorType] == "" {
			t.Fatalf("error %s has no retry policy", errorType)
		}
	}
	contractErr := foundation.ContractError{Type: "rate_limited", Message: "slow down", Retryable: true, RetryAfterMS: 1000, CorrelationID: "corr"}
	if err := contractErr.Validate(); err != nil {
		t.Fatal(err)
	}
	contractErr.Retryable = false
	if contractErr.Validate() == nil {
		t.Fatal("error contradicting retry policy passed")
	}
}

func TestSourceCollaborationsResolveToOwnedContracts(t *testing.T) {
	type binding struct{ Provider, ReadQuery, WriteCommand, ReplicaEvent string }
	var collaboration struct {
		Version, SourceIndex, ContractRegistry, ReopenTask string
		ProviderBindings                                   []binding                                                                                `json:"providerBindings"`
		ComponentBindings                                  []struct{ Symbol, Provider, ReadQuery, WriteCommand, ReplicaEvent string }               `json:"componentBindings"`
		EdgeBindings                                       []struct{ Consumer, Provider, File, Symbol, Method, Use, Contract, ReplicaEvent string } `json:"edgeBindings"`
	}
	raw, err := os.ReadFile("../architecture/collaboration_contracts.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &collaboration); err != nil {
		t.Fatal(err)
	}
	const approvedCollaborationSHA = "ef188ae3288e7c0f0d0272ccfb3908840d59208063f8d78985c5165618317863"
	if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != approvedCollaborationSHA {
		t.Fatalf("collaboration contracts changed (%s); reopen ARCH-04", got)
	}
	if collaboration.Version != "collaboration-contracts-v1" || collaboration.ReopenTask != "ARCH-04" || collaboration.SourceIndex != "architecture/dependency_sources.json" || collaboration.ContractRegistry != "architecture/module_contracts.json" {
		t.Fatal("invalid collaboration registry metadata")
	}
	contractRaw, _ := os.ReadFile("../architecture/module_contracts.json")
	var registry moduleContractRegistry
	if err := json.Unmarshal(contractRaw, &registry); err != nil {
		t.Fatal(err)
	}
	type owned struct{ kind, owner string }
	contracts := map[string]owned{}
	for _, m := range registry.Modules {
		for _, n := range m.Commands {
			contracts[n] = owned{"command", m.Name}
		}
		for _, n := range m.Queries {
			contracts[n] = owned{"query", m.Name}
		}
		for _, n := range m.Events {
			contracts[n] = owned{"event", m.Name}
		}
	}
	bindings := map[string]binding{}
	for _, b := range collaboration.ProviderBindings {
		if _, exists := bindings[b.Provider]; exists {
			t.Fatalf("duplicate provider binding %s", b.Provider)
		}
		bindings[b.Provider] = b
		for name, wantKind := range map[string]string{b.ReadQuery: "query", b.WriteCommand: "command", b.ReplicaEvent: "event"} {
			got, ok := contracts[name]
			if !ok || got.kind != wantKind || got.owner != b.Provider {
				t.Fatalf("%s binding %s does not resolve to owned %s", b.Provider, name, wantKind)
			}
		}
	}
	componentBindings := map[string]binding{}
	for _, component := range collaboration.ComponentBindings {
		if _, exists := componentBindings[component.Symbol]; exists {
			t.Fatalf("duplicate component binding %s", component.Symbol)
		}
		b := binding{component.Provider, component.ReadQuery, component.WriteCommand, component.ReplicaEvent}
		componentBindings[component.Symbol] = b
		for name, wantKind := range map[string]string{b.ReadQuery: "query", b.WriteCommand: "command", b.ReplicaEvent: "event"} {
			got, ok := contracts[name]
			if !ok || got.kind != wantKind || got.owner != b.Provider {
				t.Fatalf("component %s binding %s does not resolve to owned %s", component.Symbol, name, wantKind)
			}
		}
	}
	depRaw, _ := os.ReadFile("../architecture/dependency_sources.json")
	var sources architectureDependencySources
	if err := json.Unmarshal(depRaw, &sources); err != nil {
		t.Fatal(err)
	}
	observedKeys, resolvedKeys, usedComponents := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, edge := range sources.ObservedCode {
		if edge.Provider == "L0" {
			continue
		}
		key := strings.Join([]string{edge.Consumer, edge.Provider, edge.File, edge.Symbol}, "|")
		observedKeys[key] = true
		binding, ok := componentBindings[edge.Symbol]
		if !ok || binding.Provider != edge.Provider {
			t.Fatalf("source edge %s has no matching component contract binding", key)
		}
		usedComponents[edge.Symbol] = true
		resolvedKeys[key] = true
	}
	if !reflect.DeepEqual(observedKeys, resolvedKeys) {
		t.Fatal("observed source keys and resolved contract keys differ")
	}
	sourceMethodKeys := map[string]bool{}
	fieldPattern := regexp.MustCompile(`(?m)^\s*([a-zA-Z]\w*)\s+\*?(?:repository|service)\.([A-Z]\w*(?:Repository|Service))\b`)
	for _, scope := range sources.ScanScopes {
		code, err := os.ReadFile("../" + scope.File)
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range fieldPattern.FindAllStringSubmatch(string(code), -1) {
			fieldName, symbol := field[1], field[2]
			provider, ok := sources.RepositoryOwners[symbol]
			if !ok || provider == "L0" || provider == scope.Consumer {
				continue
			}
			usedComponents[symbol] = true
			callPattern := regexp.MustCompile(`\.` + regexp.QuoteMeta(fieldName) + `\.([A-Z]\w*)\s*\(`)
			for _, call := range callPattern.FindAllStringSubmatch(string(code), -1) {
				if call[1] == "GetDB" || call[1] == "DB" {
					t.Fatalf("%s exposes shared storage through forbidden method %s", symbol, call[1])
				}
				key := strings.Join([]string{scope.Consumer, provider, scope.File, symbol, call[1]}, "|")
				sourceMethodKeys[key] = true
			}
		}
	}
	bindingMethodKeys := map[string]bool{}
	semanticContracts := map[string][3]string{
		"ContactRepository.FindOrCreateContactInbox": {"write", "cus.contact.create.v1", "cus.contact.created.v1"},
		"LabelRepository.FindOrCreateByTitle":        {"write", "cus.label.manage.v1", "cus.label.changed.v1"},
		"IntegrationRepository.Disable":              {"write", "ext.plugin.disable.v1", "ext.plugin.disabled.v1"},
		"OrderRepository.Update":                     {"write", "bil.order.update.v1", "bil.order.changed.v1"},
		"MessageRepository.SaveDraft":                {"write", "msg.message.update.v1", "msg.message.changed.v1"},
		"MessageRepository.DeleteDraft":              {"write", "msg.message.update.v1", "msg.message.deleted.v1"},
		"MessageRepository.UpdateContentAttributes":  {"write", "msg.message.update.v1", "msg.message.changed.v1"},
	}
	for _, edge := range collaboration.EdgeBindings {
		key := strings.Join([]string{edge.Consumer, edge.Provider, edge.File, edge.Symbol, edge.Method}, "|")
		if bindingMethodKeys[key] {
			t.Fatalf("duplicate method edge binding %s", key)
		}
		bindingMethodKeys[key] = true
		contract, ok := contracts[edge.Contract]
		wantKind := map[string]string{"read": "query", "write": "command"}[edge.Use]
		if wantKind == "" || !ok || contract.owner != edge.Provider || contract.kind != wantKind {
			t.Fatalf("method edge %s resolves to invalid %s contract %s", key, edge.Use, edge.Contract)
		}
		event, ok := contracts[edge.ReplicaEvent]
		if !ok || event.owner != edge.Provider || event.kind != "event" {
			t.Fatalf("method edge %s has invalid replica event %s", key, edge.ReplicaEvent)
		}
		if expected, exists := semanticContracts[edge.Symbol+"."+edge.Method]; exists && [3]string{edge.Use, edge.Contract, edge.ReplicaEvent} != expected {
			t.Fatalf("method edge %s has semantic mapping %v, want %v", key, [3]string{edge.Use, edge.Contract, edge.ReplicaEvent}, expected)
		}
	}
	if !reflect.DeepEqual(sourceMethodKeys, bindingMethodKeys) {
		t.Fatalf("source method keys (%d) and edge binding keys (%d) differ", len(sourceMethodKeys), len(bindingMethodKeys))
	}
	for symbol := range componentBindings {
		if !usedComponents[symbol] {
			t.Fatalf("unused component binding %s", symbol)
		}
	}
	if len(bindings) != len(registry.Modules) {
		t.Fatalf("provider bindings=%d, want %d", len(bindings), len(registry.Modules))
	}
}

func TestOwnerRepositoriesDoNotQueryForeignAggregates(t *testing.T) {
	for file, forbidden := range map[string][]string{
		"../internal/repository/assignment_policy_repository.go": {"domain.Inbox"},
		"../internal/repository/csat_extension_repository.go":    {"domain.Conversation", "domain.Message", "domain.Inbox", "domain.Contact", "domain.ContactInbox", "domain.AccountUser"},
		"../internal/repository/inbox_repository.go":             {"domain.AssignmentPolicy", "domain.Conversation"},
	} {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, token := range forbidden {
			if strings.Contains(string(raw), token) {
				t.Fatalf("%s queries foreign aggregate %s", file, token)
			}
		}
	}
}
