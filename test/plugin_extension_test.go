package test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
)

type pluginExtensionRegistry struct {
	Version, FoundationVersion, ReopenTask string
	Rules                                  map[string]bool `json:"rules"`
	ExtensionPoints                        []struct {
		ID, Owner, FailureMode string
		Modules, Capabilities  []string
		HighRisk               bool
	}
	RegisteredEntrypoints                                []struct{ Symbol, ExtensionPoint, StorageOwner, Source string }
	RequiredManifestFields, Lifecycle, InvocationResults []string
}

func TestPluginExtensionRegistryIsCompleteAndFrozen(t *testing.T) {
	raw, err := os.ReadFile("../architecture/plugin_extensions.json")
	if err != nil {
		t.Fatal(err)
	}
	const approvedSHA = "df64a669c2a92666bef5b2e7c886893e4d4f1f05949cecbda4dff8f37313b5fa"
	if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != approvedSHA {
		t.Fatalf("plugin extension registry changed (%s); reopen ARCH-05", got)
	}
	var registry pluginExtensionRegistry
	if err := json.Unmarshal(raw, &registry); err != nil {
		t.Fatal(err)
	}
	if registry.Version != "plugin-extensions-v1" || registry.FoundationVersion != "foundation-v1" || registry.ReopenTask != "ARCH-05" {
		t.Fatal("invalid plugin registry metadata")
	}
	for _, rule := range []string{"denyPrivateEntrypoints", "denyCoreStorageAccess", "requireAccountIsolation", "requireSignedEvents", "requireIdempotency", "requireTimeout", "requireFailureIsolation", "requireHighRiskConfirmation"} {
		if !registry.Rules[rule] {
			t.Fatalf("required rule %s is disabled", rule)
		}
	}
	wantPoints := []string{"ai_provider", "authentication_provider", "automation_action", "automation_condition", "channel_adapter", "custom_tool", "import_source", "integration_connector", "knowledge_source", "message_content_handler", "notification_channel", "report_exporter"}
	points := map[string]bool{}
	pointModules := map[string]map[string]bool{}
	for _, point := range registry.ExtensionPoints {
		if point.ID == "" || point.Owner == "" || point.FailureMode == "" || len(point.Modules) == 0 || len(point.Capabilities) == 0 {
			t.Fatalf("incomplete extension point %+v", point)
		}
		if points[point.ID] {
			t.Fatalf("duplicate extension point %s", point.ID)
		}
		points[point.ID] = true
		pointModules[point.ID] = map[string]bool{}
		for _, module := range point.Modules {
			pointModules[point.ID][module] = true
		}
		policy, exists := foundation.PluginExtensionPolicies[point.ID]
		if !exists || policy.HighRisk != point.HighRisk {
			t.Fatalf("runtime policy mismatch for %s", point.ID)
		}
		declaredCapabilities := append([]string{}, point.Capabilities...)
		sort.Strings(declaredCapabilities)
		runtimeCapabilities := []string{}
		for capability := range policy.Capabilities {
			runtimeCapabilities = append(runtimeCapabilities, capability)
		}
		sort.Strings(runtimeCapabilities)
		if !reflect.DeepEqual(runtimeCapabilities, declaredCapabilities) {
			t.Fatalf("capabilities runtime=%v declared=%v", runtimeCapabilities, declaredCapabilities)
		}
	}
	actual := make([]string, 0, len(points))
	for point := range points {
		actual = append(actual, point)
	}
	sort.Strings(actual)
	if !reflect.DeepEqual(actual, wantPoints) {
		t.Fatalf("extension point set=%v want=%v", actual, wantPoints)
	}
	runtimePoints := make([]string, 0, len(foundation.PluginExtensionPolicies))
	for point := range foundation.PluginExtensionPolicies {
		runtimePoints = append(runtimePoints, point)
	}
	sort.Strings(runtimePoints)
	if !reflect.DeepEqual(runtimePoints, actual) {
		t.Fatalf("runtime extension points=%v declared=%v", runtimePoints, actual)
	}
	manifestFields := []string{}
	manifestType := reflect.TypeOf(foundation.PluginManifest{})
	for i := 0; i < manifestType.NumField(); i++ {
		manifestFields = append(manifestFields, strings.Split(manifestType.Field(i).Tag.Get("json"), ",")[0])
	}
	sort.Strings(manifestFields)
	declaredFields := append([]string{}, registry.RequiredManifestFields...)
	sort.Strings(declaredFields)
	if !reflect.DeepEqual(manifestFields, declaredFields) {
		t.Fatalf("manifest runtime fields=%v declared=%v", manifestFields, declaredFields)
	}
	dependencyRaw, _ := os.ReadFile("../architecture/dependency_sources.json")
	var sources architectureDependencySources
	if err := json.Unmarshal(dependencyRaw, &sources); err != nil {
		t.Fatal(err)
	}
	entrypoints := map[string]bool{}
	for _, entry := range registry.RegisteredEntrypoints {
		if !points[entry.ExtensionPoint] {
			t.Fatalf("entrypoint %s uses unknown extension point", entry.Symbol)
		}
		if sources.RepositoryOwners[entry.Symbol] != entry.StorageOwner {
			t.Fatalf("entrypoint %s storage owner=%s want=%s", entry.Symbol, entry.StorageOwner, sources.RepositoryOwners[entry.Symbol])
		}
		if !pointModules[entry.ExtensionPoint][entry.StorageOwner] {
			t.Fatalf("entrypoint %s owner %s is outside %s modules", entry.Symbol, entry.StorageOwner, entry.ExtensionPoint)
		}
		if !sourceDeclaresSymbol("../"+entry.Source, entry.Symbol) {
			t.Fatalf("entrypoint %s has no production source %s", entry.Symbol, entry.Source)
		}
		entrypoints[entry.Symbol] = true
	}
	for _, required := range []string{"IntegrationRepository", "WebhookRepository", "DashboardAppRepository", "AgentBotRepository", "AICustomToolRepository", "DataImportService", "MigrationService", "EmailService", "PushService", "AutomationService", "StorageService", "ChannelAuthEnterpriseRepository"} {
		if !entrypoints[required] {
			t.Fatalf("private or unregistered extension entrypoint %s", required)
		}
	}
	runtimeLifecycle := []string{}
	for state := range foundation.PluginLifecycleStates {
		runtimeLifecycle = append(runtimeLifecycle, state)
	}
	sort.Strings(runtimeLifecycle)
	declaredLifecycle := append([]string{}, registry.Lifecycle...)
	sort.Strings(declaredLifecycle)
	if !reflect.DeepEqual(runtimeLifecycle, declaredLifecycle) {
		t.Fatalf("lifecycle runtime=%v declared=%v", runtimeLifecycle, declaredLifecycle)
	}
	runtimeResults := []string{}
	for state := range foundation.PluginInvocationResults {
		runtimeResults = append(runtimeResults, state)
	}
	sort.Strings(runtimeResults)
	declaredResults := append([]string{}, registry.InvocationResults...)
	sort.Strings(declaredResults)
	if !reflect.DeepEqual(runtimeResults, declaredResults) {
		t.Fatalf("results runtime=%v declared=%v", runtimeResults, declaredResults)
	}
}

func TestPluginContractsValidateSecurityLifecycleAndIsolation(t *testing.T) {
	manifest := foundation.PluginManifest{PluginID: "crm.example", Name: "Example CRM", Publisher: "Example", Version: "1.0.0", ContractVersion: "v1", MinSystemVersion: "1.0.0", FoundationVersion: "foundation-v1", RequiredFeatures: []string{"integrations"}, RequiredScopes: []string{"contact.read"}, OptionalScopes: []string{"contact.write"}, DataClasses: []string{"internal"}, ObjectRanges: []string{"account"}, ProvidedCapabilities: []string{"connector.query"}, Commands: []string{}, Queries: []string{"cus.contact.get.v1"}, Subscriptions: []string{"cus.contact.changed.v1"}, Callbacks: []string{"https://crm.example/callback"}, ConfigurationSchema: map[string]any{"type": "object"}, SecretFields: []string{"client_secret"}, AllowedDomains: []string{"crm.example"}, Protocols: []string{"https"}, TimeoutMS: 3000, Concurrency: 4, RateLimitPerMinute: 60, PayloadSizeBytes: 65536, Quota: 1000, Lifecycle: []string{"inactive", "active", "paused", "upgrading", "rolling_back", "uninstalling", "uninstalled"}, PrivateDataPolicy: "account_partitioned", RetentionDays: 30, ExportPolicy: "on_request", DeletionPolicy: "delete_on_uninstall", DataLocation: "plugin_store", HealthURL: "https://crm.example/health", SupportURL: "https://crm.example/support", Signature: "sha256:approved", HighRiskConfirmation: true, Compensation: "query_status_then_reverse"}
	manifest.ExtensionPoint = "integration_connector"
	manifest.MaxSystemVersion = "2.0.0"
	manifest.Signature = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	manifest.AllowedDomains = []string{"*.example"}
	if manifest.Validate() == nil {
		t.Fatal("wildcard domain passed")
	}
	manifest.AllowedDomains = []string{"crm.example"}
	manifest.Compensation = ""
	if manifest.Validate() == nil {
		t.Fatal("high-risk manifest without compensation passed")
	}
	manifest.Compensation = "query_status_then_reverse"
	manifest.Callbacks = []string{"http://crm.example/callback"}
	if manifest.Validate() == nil {
		t.Fatal("non-https callback passed")
	}
	manifest.Callbacks = []string{"https://crm.example/callback"}
	manifest.SupportURL = "https://support.example/help"
	if manifest.Validate() == nil {
		t.Fatal("support URL outside allowed domains passed")
	}
	manifest.SupportURL = "https://crm.example/support"
	manifest.RequiredScopes = []string{"*"}
	if manifest.Validate() == nil {
		t.Fatal("wildcard scope passed")
	}
	manifest.RequiredScopes = []string{"contact.read"}
	manifest.HighRiskConfirmation = false
	if manifest.Validate() == nil {
		t.Fatal("high-risk extension point bypassed confirmation")
	}
	manifest.HighRiskConfirmation = true
	manifest.ExtensionPoint = "unknown"
	if manifest.Validate() == nil {
		t.Fatal("unknown extension point passed")
	}
	manifest.ExtensionPoint = "channel_adapter"
	if manifest.Validate() == nil {
		t.Fatal("cross-point connector capability passed")
	}
	manifest.ExtensionPoint = "integration_connector"
	now := time.Now().UTC()
	invocation := foundation.PluginInvocation{InvocationID: "inv-1", InstallationID: "ins-1", AccountID: 1, Actor: foundation.ContractActor{Type: "User", ID: "1", AuthSource: "jwt"}, Capability: "connector.query", ContractVersion: "v1", IdempotencyKey: "idem-1", CorrelationID: "corr-1", Deadline: now.Add(time.Second), Payload: map[string]any{"contact_id": 1}, DataClassification: "internal", Result: "accepted"}
	if err := invocation.Validate(now); err != nil {
		t.Fatal(err)
	}
	invocation.Deadline = now.Add(-time.Second)
	if invocation.Validate(now) == nil {
		t.Fatal("expired invocation passed")
	}
	delivery := foundation.PluginEventDelivery{InstallationID: "ins-1", EventID: "evt-1", DeliveryID: "del-1", EventVersion: "v1", Signature: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", OccurredAt: now}
	if err := delivery.Validate(); err != nil {
		t.Fatal(err)
	}
	delivery.Signature = ""
	if delivery.Validate() == nil {
		t.Fatal("unsigned delivery passed")
	}
	if strings.Contains(strings.Join(manifest.RequiredScopes, ","), "*") {
		t.Fatal("wildcard scope passed")
	}
	authorization := foundation.PluginAuthorization{InstallationAccountID: 1, RequestAccountID: 1, InstallationState: "active", VersionSupported: true, FeatureAllowed: true, ActorAllowed: true, GrantedScopes: map[string]bool{"contact.read": true}, RequiredScope: "contact.read", GrantedObjects: map[string]bool{"inbox:1": true}, TargetObject: "inbox:1", AllowedDataClasses: map[string]bool{"internal": true}, DataClassification: "internal", WithinQuota: true, HighRisk: true, Confirmed: true}
	if err := authorization.Validate(); err != nil {
		t.Fatal(err)
	}
	authorization.RequestAccountID = 2
	if authorization.Validate() == nil {
		t.Fatal("cross-account authorization passed")
	}
	authorization.RequestAccountID = 1
	authorization.Confirmed = false
	if authorization.Validate() == nil {
		t.Fatal("unconfirmed high-risk authorization passed")
	}
}

func sourceDeclaresSymbol(path, symbol string) bool {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return false
	}
	found := false
	ast.Inspect(file, func(node ast.Node) bool {
		switch declaration := node.(type) {
		case *ast.TypeSpec:
			found = found || declaration.Name.Name == symbol
		case *ast.FuncDecl:
			found = found || declaration.Name.Name == symbol
		}
		return !found
	})
	return found
}
