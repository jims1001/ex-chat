package test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type architectureDependencyManifest struct {
	Version           string                         `json:"version"`
	FoundationVersion string                         `json:"foundationVersion"`
	ReopenTask        string                         `json:"reopenTask"`
	Modules           []architectureDependencyModule `json:"modules"`
}

type architectureDependencyModule struct {
	Name        string   `json:"name"`
	Layer       int      `json:"layer"`
	Order       int      `json:"order"`
	Synchronous []string `json:"synchronous"`
	Events      []string `json:"events"`
	Shared      []string `json:"shared"`
	Startup     []string `json:"startup"`
}

type architectureDependencySources struct {
	RepositoryOwners map[string]string `json:"repositoryOwners"`
	ScopePolicy      string            `json:"scopePolicy"`
	ScanScopes       []struct {
		Consumer string `json:"consumer"`
		File     string `json:"file"`
	} `json:"scanScopes"`
	Modules      []architectureDependencyModule `json:"modules"`
	ObservedCode []struct {
		Consumer string `json:"consumer"`
		Provider string `json:"provider"`
		Kind     string `json:"kind"`
		File     string `json:"file"`
		Symbol   string `json:"symbol"`
	} `json:"observedCode"`
}

func architectureTopologicalCount(modules []architectureDependencyModule, edgeKinds ...string) int {
	byName := make(map[string]bool, len(modules))
	degree := make(map[string]int, len(modules))
	next := make(map[string][]string, len(modules))
	for _, module := range modules {
		byName[module.Name] = true
	}
	for _, consumer := range modules {
		providers := make(map[string]bool)
		for _, kind := range edgeKinds {
			var names []string
			switch kind {
			case "synchronous":
				names = consumer.Synchronous
			case "events":
				names = consumer.Events
			case "shared":
				names = consumer.Shared
			case "startup":
				names = consumer.Startup
			}
			for _, provider := range names {
				providers[provider] = true
			}
		}
		for provider := range providers {
			degree[consumer.Name]++
			next[provider] = append(next[provider], consumer.Name)
		}
	}
	queue := make([]string, 0)
	for name := range byName {
		if degree[name] == 0 {
			queue = append(queue, name)
		}
	}
	visitedCount := 0
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		visitedCount++
		for _, consumer := range next[name] {
			degree[consumer]--
			if degree[consumer] == 0 {
				queue = append(queue, consumer)
			}
		}
	}
	return visitedCount
}

func TestArchitectureDependencyGraphIsCompleteAndAcyclic(t *testing.T) {
	raw, err := os.ReadFile("../architecture/module_dependencies.json")
	if err != nil {
		t.Fatal(err)
	}
	const approvedManifestSHA256 = "3b62633653a389988b4fcea42eda1b2ea13ed7b24e7f3de2518ec887f8c856f9"
	if actual := fmt.Sprintf("%x", sha256.Sum256(raw)); actual != approvedManifestSHA256 {
		t.Fatalf("dependency manifest changed (%s); reopen ARCH-03 and approve a new fingerprint", actual)
	}
	var manifest architectureDependencyManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version == "" || manifest.FoundationVersion != "foundation-v1" || manifest.ReopenTask != "ARCH-03" {
		t.Fatalf("invalid dependency manifest metadata: %+v", manifest)
	}

	expected := []string{"IAM", "TEN", "ENT", "SYS", "CHN", "CUS", "MED", "CON", "MSG", "RTE", "TKT", "QLT", "KB", "AUT", "OPS", "NTF", "AIC", "EXT", "RPT", "SRH", "API", "RTM", "MIG", "BIL", "AUD", "ORC"}
	sort.Strings(expected)
	if len(manifest.Modules) != len(expected) {
		t.Fatalf("module count=%d, want %d", len(manifest.Modules), len(expected))
	}

	byName := make(map[string]architectureDependencyModule, len(manifest.Modules))
	byOrder := make(map[int]string, len(manifest.Modules))
	for _, module := range manifest.Modules {
		if _, exists := byName[module.Name]; exists {
			t.Fatalf("duplicate module %s", module.Name)
		}
		if prior, exists := byOrder[module.Order]; exists {
			t.Fatalf("duplicate order %d for %s and %s", module.Order, prior, module.Name)
		}
		if module.Layer < 1 || module.Layer > 6 || module.Order < 1 || module.Order > len(expected) {
			t.Fatalf("invalid layer/order for %s", module.Name)
		}
		byName[module.Name] = module
		byOrder[module.Order] = module.Name
	}
	approvedOrder := []string{"IAM", "TEN", "ENT", "SYS", "CHN", "CUS", "MED", "CON", "MSG", "RTE", "TKT", "QLT", "KB", "BIL", "EXT", "NTF", "AUT", "OPS", "AIC", "MIG", "RPT", "SRH", "ORC", "API", "RTM", "AUD"}
	approvedLayers := map[string]int{
		"IAM": 1, "TEN": 1, "ENT": 1, "SYS": 1, "BIL": 1,
		"CHN": 2, "CUS": 2, "MED": 2, "KB": 2,
		"CON": 3, "MSG": 3, "RTE": 3, "TKT": 3, "QLT": 3,
		"AUT": 4, "OPS": 4, "NTF": 4, "AIC": 4, "EXT": 4, "RPT": 4, "SRH": 4, "MIG": 4,
		"API": 5, "RTM": 5, "ORC": 5, "AUD": 6,
	}
	for index, name := range approvedOrder {
		module := byName[name]
		if module.Order != index+1 || module.Layer != approvedLayers[name] {
			t.Fatalf("frozen layer/order mismatch for %s: L%d order=%d", name, module.Layer, module.Order)
		}
	}
	actual := make([]string, 0, len(byName))
	for name := range byName {
		actual = append(actual, name)
	}
	sort.Strings(actual)
	for i := range expected {
		if actual[i] != expected[i] {
			t.Fatalf("module set mismatch at %d: got %s want %s", i, actual[i], expected[i])
		}
	}

	indegree := make(map[string]int, len(byName))
	consumers := make(map[string][]string, len(byName))
	edgeCountByKind := map[string]int{"synchronous": 0, "events": 0, "shared": 0, "startup": 0}
	for _, consumer := range manifest.Modules {
		kinds := map[string][]string{
			"synchronous": consumer.Synchronous,
			"events":      consumer.Events,
			"shared":      consumer.Shared,
			"startup":     consumer.Startup,
		}
		seen := make(map[string]bool)
		for kind, providers := range kinds {
			edgeCountByKind[kind] += len(providers)
			for _, providerName := range providers {
				provider, ok := byName[providerName]
				if !ok {
					t.Fatalf("%s %s references unknown provider %s", consumer.Name, kind, providerName)
				}
				if providerName == consumer.Name {
					t.Fatalf("%s has a self dependency in %s", consumer.Name, kind)
				}
				if provider.Order >= consumer.Order {
					t.Fatalf("reverse dependency %s -> %s in %s violates provider-first order", consumer.Name, providerName, kind)
				}
				if kind != "events" && provider.Layer > consumer.Layer {
					t.Fatalf("lower-layer reverse dependency %s(L%d) -> %s(L%d)", consumer.Name, consumer.Layer, providerName, provider.Layer)
				}
				if consumer.Layer < 5 && (providerName == "API" || providerName == "RTM" || providerName == "ORC") {
					t.Fatalf("lower module %s depends on access/orchestration module %s", consumer.Name, providerName)
				}
				if !seen[providerName] {
					seen[providerName] = true
					indegree[consumer.Name]++
					consumers[providerName] = append(consumers[providerName], consumer.Name)
				}
			}
		}
	}
	for kind, count := range edgeCountByKind {
		if count == 0 {
			t.Fatalf("dependency kind %s is not registered", kind)
		}
	}

	sourceRaw, err := os.ReadFile("../architecture/dependency_sources.json")
	if err != nil {
		t.Fatal(err)
	}
	var sources architectureDependencySources
	if err := json.Unmarshal(sourceRaw, &sources); err != nil {
		t.Fatal(err)
	}
	if len(sources.Modules) != len(manifest.Modules) {
		t.Fatalf("dependency source module count=%d, want %d", len(sources.Modules), len(manifest.Modules))
	}
	sourceNames := make(map[string]bool, len(sources.Modules))
	for _, sourceModule := range sources.Modules {
		if sourceNames[sourceModule.Name] {
			t.Fatalf("dependency sources contain duplicate module %s", sourceModule.Name)
		}
		sourceNames[sourceModule.Name] = true
		declared, ok := byName[sourceModule.Name]
		if !ok {
			t.Fatalf("dependency sources contain unknown module %s", sourceModule.Name)
		}
		for kind, pair := range map[string][2][]string{
			"synchronous": {declared.Synchronous, sourceModule.Synchronous},
			"events":      {declared.Events, sourceModule.Events},
			"shared":      {declared.Shared, sourceModule.Shared},
			"startup":     {declared.Startup, sourceModule.Startup},
		} {
			left, right := append([]string(nil), pair[0]...), append([]string(nil), pair[1]...)
			sort.Strings(left)
			sort.Strings(right)
			if !reflect.DeepEqual(left, right) {
				t.Fatalf("%s %s differs between manifest and dependency sources: %v != %v", sourceModule.Name, kind, left, right)
			}
		}
	}
	for name := range byName {
		if !sourceNames[name] {
			t.Fatalf("dependency sources are missing module %s", name)
		}
	}

	observedKeys := make(map[string]bool, len(sources.ObservedCode))
	observedEdges := make(map[string]bool, len(sources.ObservedCode))
	for _, observed := range sources.ObservedCode {
		module, ok := byName[observed.Consumer]
		if !ok {
			t.Fatalf("observed dependency has unknown consumer %s", observed.Consumer)
		}
		var providers []string
		switch observed.Kind {
		case "synchronous":
			providers = module.Synchronous
		case "events":
			providers = module.Events
		case "shared":
			providers = module.Shared
		case "startup":
			providers = module.Startup
		default:
			t.Fatalf("unknown observed dependency kind %s", observed.Kind)
		}
		foundProvider := false
		for _, provider := range providers {
			foundProvider = foundProvider || provider == observed.Provider
		}
		if !foundProvider {
			t.Fatalf("observed edge %s -> %s (%s) is missing from manifest", observed.Consumer, observed.Provider, observed.Kind)
		}
		code, err := os.ReadFile("../" + observed.File)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(code), observed.Symbol) {
			t.Fatalf("observed source %s no longer contains symbol %s", observed.File, observed.Symbol)
		}
		key := strings.Join([]string{observed.Consumer, observed.Provider, observed.Kind, observed.File, observed.Symbol}, "|")
		if observedKeys[key] {
			t.Fatalf("duplicate observed source dependency %s", key)
		}
		observedKeys[key] = true
		observedEdges[strings.Join([]string{observed.Consumer, observed.Provider, observed.Kind}, "|")] = true
	}

	repositoryPattern := regexp.MustCompile(`\b(?:New)?([A-Z][A-Za-z0-9]*Repository)\b`)
	servicePattern := regexp.MustCompile(`\bservice\.(?:New)?([A-Z][A-Za-z0-9]*Service)\b`)
	dependencyMatches := func(code string) [][]string {
		return append(repositoryPattern.FindAllStringSubmatch(code, -1), servicePattern.FindAllStringSubmatch(code, -1)...)
	}
	scannedKeys := make(map[string]bool)
	scannedEdges := make(map[string]bool)
	scopeFiles := make(map[string]bool, len(sources.ScanScopes))
	for _, scope := range sources.ScanScopes {
		if scopeFiles[scope.File] {
			t.Fatalf("duplicate scan scope %s", scope.File)
		}
		scopeFiles[scope.File] = true
		module, ok := byName[scope.Consumer]
		if !ok {
			t.Fatalf("scan scope has unknown consumer %s", scope.Consumer)
		}
		code, err := os.ReadFile("../" + scope.File)
		if err != nil {
			t.Fatal(err)
		}
		seenSymbols := make(map[string]bool)
		for _, match := range dependencyMatches(string(code)) {
			symbol := match[1]
			if seenSymbols[symbol] {
				continue
			}
			seenSymbols[symbol] = true
			provider, exists := sources.RepositoryOwners[symbol]
			if !exists {
				t.Fatalf("repository owner is not registered for %s found in %s", symbol, scope.File)
			}
			if provider == "L0" || provider == scope.Consumer {
				continue
			}
			registered := false
			for _, name := range module.Synchronous {
				registered = registered || name == provider
			}
			if !registered {
				t.Fatalf("source dependency %s -> %s via %s in %s is missing from manifest", scope.Consumer, provider, symbol, scope.File)
			}
			key := strings.Join([]string{scope.Consumer, provider, "synchronous", scope.File, symbol}, "|")
			scannedKeys[key] = true
			scannedEdges[strings.Join([]string{scope.Consumer, provider, "synchronous"}, "|")] = true
		}
	}
	for key := range observedKeys {
		if !scannedKeys[key] {
			t.Fatalf("observedCode entry is outside the reverse source scan: %s", key)
		}
	}
	for edge := range scannedEdges {
		if !observedEdges[edge] {
			t.Fatalf("scanned source edge is missing from observedCode: %s", edge)
		}
	}
	if sources.ScopePolicy == "" {
		t.Fatal("dependency source scope policy is required")
	}
	for _, dir := range []string{"../internal/handler", "../internal/service"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if strings.HasSuffix(file, "_test.go") {
				continue
			}
			code, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			rel := strings.TrimPrefix(file, "../")
			if len(dependencyMatches(string(code))) > 0 && !scopeFiles[rel] {
				t.Fatalf("production source %s contains dependency symbols but has no scan scope", rel)
			}
		}
	}

	if count := architectureTopologicalCount(manifest.Modules, "synchronous", "shared", "startup"); count != len(byName) {
		t.Fatalf("synchronous/shared/startup graph contains a cycle: sorted %d of %d", count, len(byName))
	}
	if count := architectureTopologicalCount(manifest.Modules, "events"); count != len(byName) {
		t.Fatalf("event graph contains a cycle: sorted %d of %d", count, len(byName))
	}
	if count := architectureTopologicalCount(manifest.Modules, "synchronous", "events", "shared", "startup"); count != len(byName) {
		t.Fatalf("combined graph contains a cycle: sorted %d of %d", count, len(byName))
	}

	ready := make([]string, 0)
	for name := range byName {
		if indegree[name] == 0 {
			ready = append(ready, name)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return byName[ready[i]].Order < byName[ready[j]].Order })
	visited := make([]string, 0, len(byName))
	for len(ready) > 0 {
		name := ready[0]
		ready = ready[1:]
		visited = append(visited, name)
		for _, consumer := range consumers[name] {
			indegree[consumer]--
			if indegree[consumer] == 0 {
				ready = append(ready, consumer)
				sort.Slice(ready, func(i, j int) bool { return byName[ready[i]].Order < byName[ready[j]].Order })
			}
		}
	}
	if len(visited) != len(byName) {
		t.Fatalf("dependency graph contains a cycle: sorted %d of %d modules", len(visited), len(byName))
	}
	for index, name := range visited {
		if byName[name].Order != index+1 {
			t.Fatalf("topological order mismatch at %d: %s declares order %d", index+1, name, byName[name].Order)
		}
	}
}

func TestArchitectureDependencyCycleDetectorRejectsMutations(t *testing.T) {
	tests := []struct {
		name  string
		graph []architectureDependencyModule
		kinds []string
	}{
		{
			name: "direct synchronous two-cycle",
			graph: []architectureDependencyModule{
				{Name: "A", Synchronous: []string{"B"}},
				{Name: "B", Synchronous: []string{"A"}},
			},
			kinds: []string{"synchronous"},
		},
		{
			name: "indirect synchronous three-cycle",
			graph: []architectureDependencyModule{
				{Name: "A", Synchronous: []string{"C"}},
				{Name: "B", Synchronous: []string{"A"}},
				{Name: "C", Synchronous: []string{"B"}},
			},
			kinds: []string{"synchronous"},
		},
		{
			name: "event cycle",
			graph: []architectureDependencyModule{
				{Name: "A", Events: []string{"B"}},
				{Name: "B", Events: []string{"A"}},
			},
			kinds: []string{"events"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := architectureTopologicalCount(tt.graph, tt.kinds...); got == len(tt.graph) {
				t.Fatalf("mutated graph unexpectedly sorted all %d modules", got)
			}
		})
	}
}
