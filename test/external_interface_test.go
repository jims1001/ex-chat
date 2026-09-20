package test

import (
	"bufio"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
)

type externalInterfaceRegistry struct {
	Version, FoundationVersion, ReopenTask string
	Layers                                 []string
	Families                               []struct {
		ID, Identity, DataScope, AllowedPurpose, ForbiddenUse string
	}
	RouteClassification struct {
		RealtimePaths, WidgetPrefixes, PublicPrefixes, PlatformPrefixes, AdminPrefixes, ProviderMarkers, PluginPrefixes []string
		DefaultFamily                                                                                                   string
	}
	WriteMapping struct {
		AllowedActionTypes        []string
		RequireIdempotency        bool
		AliasesReuseHandlerAction bool
	}
	VersionRules                       []string
	EntrypointRule                     string
	DirectInternalStorageAccessAllowed bool
}

type discoveredRoute struct {
	Method, Path, Handler, SourceLine string
}

func TestExternalInterfaceRegistryAndRuntimeAreFrozen(t *testing.T) {
	raw, err := os.ReadFile("../architecture/external_interfaces.json")
	if err != nil {
		t.Fatal(err)
	}
	const approvedSHA = "f200c859504763d94c3900a7046935d7738245842f3f39b556dca5de3007f3e9"
	if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != approvedSHA {
		t.Fatalf("external interface registry changed (%s); reopen ARCH-06", got)
	}
	var registry externalInterfaceRegistry
	if err := json.Unmarshal(raw, &registry); err != nil {
		t.Fatal(err)
	}
	if registry.Version != "external-interfaces-v1" || registry.FoundationVersion != "foundation-v1" || registry.ReopenTask != "ARCH-06" {
		t.Fatal("invalid external interface registry metadata")
	}
	if !reflect.DeepEqual(registry.Layers, []string{"E1", "E2", "E3", "E4", "E5", "E6", "E7"}) {
		t.Fatalf("external layers=%v", registry.Layers)
	}
	declaredFamilies := map[string]foundation.ExternalInterfacePolicy{}
	for _, family := range registry.Families {
		if _, duplicate := declaredFamilies[family.ID]; duplicate {
			t.Fatalf("duplicate family %s", family.ID)
		}
		declaredFamilies[family.ID] = foundation.ExternalInterfacePolicy{Identity: family.Identity, DataScope: family.DataScope, AllowedPurpose: family.AllowedPurpose, ForbiddenUse: family.ForbiddenUse}
	}
	if !reflect.DeepEqual(declaredFamilies, foundation.ExternalInterfacePolicies) {
		t.Fatalf("runtime interface policies differ from registry")
	}
	if len(declaredFamilies) != 9 || registry.EntrypointRule != "transport_to_public_contract_only" || registry.DirectInternalStorageAccessAllowed {
		t.Fatal("external entrypoint boundary is incomplete")
	}
	if len(foundation.ExternalLayerErrors) != 7 {
		t.Fatal("all seven layers require distinct error contracts")
	}
	runtimeLayers := make([]string, 0, len(foundation.ExternalLayerErrors))
	for layer := range foundation.ExternalLayerErrors {
		runtimeLayers = append(runtimeLayers, layer)
	}
	sort.Strings(runtimeLayers)
	if !reflect.DeepEqual(runtimeLayers, registry.Layers) {
		t.Fatalf("runtime error layers=%v declared=%v", runtimeLayers, registry.Layers)
	}
	if !reflect.DeepEqual(foundation.ExternalVersionRules, registry.VersionRules) {
		t.Fatalf("runtime version rules=%v declared=%v", foundation.ExternalVersionRules, registry.VersionRules)
	}
}

func TestEveryRouterEntrypointIsClassifiedAndEveryWriteHasOneAction(t *testing.T) {
	raw, err := os.ReadFile("../architecture/external_interfaces.json")
	if err != nil {
		t.Fatal(err)
	}
	var registry externalInterfaceRegistry
	if err := json.Unmarshal(raw, &registry); err != nil {
		t.Fatal(err)
	}
	routes := discoverRouterRoutes(t, "../internal/router/router.go")
	if len(routes) < 750 {
		t.Fatalf("route inventory unexpectedly small: %d", len(routes))
	}
	registeredActions := loadRegisteredCommands(t)
	routerSource, err := os.ReadFile("../internal/router/router.go")
	if err != nil || strings.Contains(string(routerSource), "db.") {
		t.Fatal("router transport layer directly accesses the database")
	}
	familyCounts := map[string]int{}
	writeActions := map[string]string{}
	handlerActions := map[string]string{}
	externalActionOwners := map[string]string{}
	usedOverrides := map[string]bool{}
	for _, route := range routes {
		family := classifyExternalRoute(route.Path, registry)
		if _, ok := foundation.ExternalInterfacePolicies[family]; !ok {
			t.Fatalf("unclassified route %s %s", route.Method, route.Path)
		}
		familyCounts[family]++
		if strings.Contains(route.SourceLine, "db.") || strings.Contains(route.SourceLine, "Repo.") || strings.Contains(route.SourceLine, "repository.") {
			t.Fatalf("external entrypoint directly accesses storage: %s", route.SourceLine)
		}
		if isWriteMethod(route.Method) {
			if strings.Contains(route.SourceLine, "func(") {
				t.Fatalf("write route must use a named public action: %s", route.SourceLine)
			}
			if len(strings.SplitN(route.Handler, ".", 2)) != 2 {
				t.Fatalf("write route has no public action: %s %s", route.Method, route.Path)
			}
			policy := foundation.ResolveExternalWriteAction(route.Handler)
			if _, overridden := foundation.ExternalWriteDispatchOverrides[route.Handler]; overridden {
				usedOverrides[route.Handler] = true
			}
			if policy.ExternalActionID == "" || !containsExact(registry.WriteMapping.AllowedActionTypes, policy.ActionType) || !registeredActions[policy.Action] {
				t.Fatalf("write route %s %s has no registered Command/ORC action", route.Method, route.Path)
			}
			if !registry.WriteMapping.RequireIdempotency || !registry.WriteMapping.AliasesReuseHandlerAction {
				t.Fatal("write mapping safety rules are disabled")
			}
			action := policy.ExternalActionID + "->" + policy.ActionType + ":" + policy.Action
			if previous, exists := writeActions[route.Method+" "+route.Path]; exists && previous != action {
				t.Fatalf("write route maps to multiple actions: %s %s", route.Method, route.Path)
			}
			writeActions[route.Method+" "+route.Path] = action
			if previous, exists := handlerActions[route.Handler]; exists && previous != action {
				t.Fatalf("alias handler %s maps to multiple actions", route.Handler)
			}
			handlerActions[route.Handler] = action
			if previous, exists := externalActionOwners[policy.ExternalActionID]; exists && previous != route.Handler {
				t.Fatalf("external action %s is shared by %s and %s", policy.ExternalActionID, previous, route.Handler)
			}
			externalActionOwners[policy.ExternalActionID] = route.Handler
		}
	}
	for _, required := range []string{"account_api", "widget_api", "public_api", "platform_api", "admin_api", "provider_callback", "realtime"} {
		if familyCounts[required] == 0 {
			t.Fatalf("no production route exercises %s", required)
		}
	}
	if len(writeActions) == 0 {
		t.Fatal("no external writes discovered")
	}
	for handler := range foundation.ExternalWriteDispatchOverrides {
		if !usedOverrides[handler] {
			t.Fatalf("stale write dispatch override %s", handler)
		}
	}
	t.Logf("classified %d routes and mapped %d writes; families=%v", len(routes), len(writeActions), familyCounts)
}

func TestExternalContractsEnforceWriteAggregationAndCallbackRules(t *testing.T) {
	action := foundation.ExternalActionContract{Family: "account_api", ExternalActionID: "api.external.account_update.v1", AccountID: 1, Actor: foundation.ContractActor{Type: "User", ID: "1", AuthSource: "jwt"}, ContractVersion: "v1", CorrelationID: "corr-1", ActionType: "command", Action: "ten.account.create.v1", IdempotencyKey: "idem-1", SensitiveView: "account_member", RateLimitKey: "account:1"}
	if err := action.Validate(); err != nil {
		t.Fatal(err)
	}
	action.IdempotencyKey = ""
	if action.Validate() == nil {
		t.Fatal("write without idempotency passed")
	}
	action.IdempotencyKey = "idem-1"
	action.Family = "unknown"
	if action.Validate() == nil {
		t.Fatal("unknown interface family passed")
	}
	source := foundation.AggregateFieldSource{FieldGroup: "conversation", OwnerModule: "CON", StableReference: "conversation_id", Projection: true, Version: "42"}
	if err := source.Validate(); err != nil {
		t.Fatal(err)
	}
	source.Version = ""
	if source.Validate() == nil {
		t.Fatal("projection without freshness passed")
	}
	callback := foundation.ProviderCallbackContract{Provider: "whatsapp", ConnectionID: "conn-1", ExternalEventID: "evt-1", SignatureVerified: true, WithinTimeWindow: true, ReceiptStored: true, StandardFact: "chn.inbound_message.received.v1", Compensation: "retry_internal_job"}
	if err := callback.Validate(); err != nil {
		t.Fatal(err)
	}
	callback.SignatureVerified = false
	if callback.Validate() == nil {
		t.Fatal("unsigned callback passed")
	}
}

func discoverRouterRoutes(t *testing.T, path string) []discoveredRoute {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	groupPattern := regexp.MustCompile(`^\s*([A-Za-z][A-Za-z0-9]*)\s*:=\s*([A-Za-z][A-Za-z0-9]*)\.Group\("([^"]*)"`)
	routePattern := regexp.MustCompile(`^\s*([A-Za-z][A-Za-z0-9]*)\.(GET|POST|PUT|PATCH|DELETE)\("([^"]*)"(.*)\)`)
	selectorPattern := regexp.MustCompile(`([A-Za-z][A-Za-z0-9]*)\.([A-Za-z][A-Za-z0-9]*)`)
	groups := map[string]string{"r": ""}
	routes := []discoveredRoute{}
	reportTemplates := []discoveredRoute{}
	reportCallPattern := regexp.MustCompile(`^\s*registerReports\(([A-Za-z][A-Za-z0-9]*)\)`)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if match := groupPattern.FindStringSubmatch(line); match != nil {
			prefix, ok := groups[match[2]]
			if !ok {
				t.Fatalf("unknown route group parent %s", match[2])
			}
			groups[match[1]] = strings.TrimSuffix(prefix, "/") + match[3]
			continue
		}
		match := routePattern.FindStringSubmatch(line)
		if call := reportCallPattern.FindStringSubmatch(line); call != nil {
			prefix, ok := groups[call[1]]
			if !ok {
				t.Fatalf("unknown report route group %s", call[1])
			}
			for _, template := range reportTemplates {
				route := template
				route.Path = strings.TrimSuffix(prefix, "/") + template.Path
				routes = append(routes, route)
			}
			continue
		}
		if match == nil {
			continue
		}
		if match[1] == "rg" {
			selectors := selectorPattern.FindAllStringSubmatch(match[4], -1)
			handler := "inline"
			if len(selectors) > 0 {
				last := selectors[len(selectors)-1]
				handler = last[1] + "." + last[2]
			}
			reportTemplates = append(reportTemplates, discoveredRoute{Method: match[2], Path: match[3], Handler: handler, SourceLine: line})
			continue
		}
		prefix, ok := groups[match[1]]
		if !ok {
			t.Fatalf("unknown route receiver %s", match[1])
		}
		selectors := selectorPattern.FindAllStringSubmatch(match[4], -1)
		handler := "inline"
		if len(selectors) > 0 {
			last := selectors[len(selectors)-1]
			handler = last[1] + "." + last[2]
		}
		routes = append(routes, discoveredRoute{Method: match[2], Path: strings.TrimSuffix(prefix, "/") + match[3], Handler: handler, SourceLine: line})
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return routes
}

func loadRegisteredCommands(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile("../architecture/module_contracts.json")
	if err != nil {
		t.Fatal(err)
	}
	var registry struct{ Modules []struct{ Commands []string } }
	if err := json.Unmarshal(raw, &registry); err != nil {
		t.Fatal(err)
	}
	commands := map[string]bool{}
	for _, module := range registry.Modules {
		for _, command := range module.Commands {
			commands[command] = true
		}
	}
	return commands
}

func classifyExternalRoute(path string, registry externalInterfaceRegistry) string {
	if containsExact(registry.RouteClassification.RealtimePaths, path) {
		return "realtime"
	}
	if containsAny(path, registry.RouteClassification.ProviderMarkers) {
		return "provider_callback"
	}
	for family, prefixes := range map[string][]string{"widget_api": registry.RouteClassification.WidgetPrefixes, "public_api": registry.RouteClassification.PublicPrefixes, "platform_api": registry.RouteClassification.PlatformPrefixes, "admin_api": registry.RouteClassification.AdminPrefixes, "plugin_capability_api": registry.RouteClassification.PluginPrefixes} {
		if hasAnyPrefix(path, prefixes) {
			return family
		}
	}
	return registry.RouteClassification.DefaultFamily
}

func isWriteMethod(method string) bool {
	return method == "POST" || method == "PUT" || method == "PATCH" || method == "DELETE"
}
func containsExact(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
func containsAny(value string, markers []string) bool {
	for _, marker := range markers {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
func hasAnyPrefix(value string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}
