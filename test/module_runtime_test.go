package test

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
)

func TestModuleRuntimeInventoryMatchesSourceRegistries(t *testing.T) {
	raw, err := os.ReadFile("../architecture/module_runtime.json")
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		FoundationVersion               string
		ModuleRuntimeEntryPoint         string
		IndependentReadinessImplemented bool
		Modules                         []struct {
			Module                   string
			DefaultUnit              string
			ContractVersion          string
			CapabilitiesRef          string
			StartupRequired          []string
			CapabilityDependencies   []string
			AsynchronousDependencies []string
			StoredModels             []string
			LocalJournal             string
			FailurePolicy            struct{ WhenStopped, Recovery string }
			RuntimeShellImplemented  bool
		}
	}
	if err := json.Unmarshal(raw, &inventory); err != nil {
		t.Fatal(err)
	}
	if inventory.FoundationVersion != "foundation-v1" {
		t.Fatal("invalid foundation version")
	}
	if inventory.ModuleRuntimeEntryPoint != "cmd/module-server" || !inventory.IndependentReadinessImplemented {
		t.Fatal("independent module runtime shell is not registered")
	}
	var dependencies architectureDependencyManifest
	raw, err = os.ReadFile("../architecture/module_dependencies.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &dependencies); err != nil {
		t.Fatal(err)
	}
	var contracts moduleContractRegistry
	raw, err = os.ReadFile("../architecture/module_contracts.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &contracts); err != nil {
		t.Fatal(err)
	}
	byModule := map[string]architectureDependencyModule{}
	for _, module := range dependencies.Modules {
		byModule[module.Name] = module
	}
	capabilities := map[string]bool{}
	for _, module := range contracts.Modules {
		capabilities[module.Name] = true
	}
	var names []string
	for _, module := range inventory.Modules {
		names = append(names, module.Module)
		dep, ok := byModule[module.Module]
		if !ok || !reflect.DeepEqual(dep.Startup, module.StartupRequired) || !reflect.DeepEqual(dep.Synchronous, module.CapabilityDependencies) || !reflect.DeepEqual(dep.Events, module.AsynchronousDependencies) {
			t.Fatalf("runtime dependency drift for %s", module.Module)
		}
		if module.CapabilitiesRef != module.Module || !capabilities[module.CapabilitiesRef] || module.ContractVersion != "v1" {
			t.Fatalf("runtime capability drift for %s", module.Module)
		}
		if module.DefaultUnit < "U1" || module.DefaultUnit > "U5" || module.LocalJournal != "LocalChangeJournal" || module.FailurePolicy.WhenStopped == "" || module.FailurePolicy.Recovery == "" {
			t.Fatalf("incomplete runtime inventory for %s", module.Module)
		}
		if !module.RuntimeShellImplemented {
			t.Fatalf("runtime shell missing for %s", module.Module)
		}
		models, err := database.ModuleModels(module.Module)
		if err != nil {
			t.Fatal(err)
		}
		stored := []string{}
		for _, model := range models {
			name := reflect.TypeOf(model).Elem().Name()
			if name != "LocalChangeJournal" {
				stored = append(stored, name)
			}
		}
		sort.Strings(stored)
		if !reflect.DeepEqual(stored, module.StoredModels) {
			t.Fatalf("runtime storage drift for %s", module.Module)
		}
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, database.ModuleIDs()) {
		t.Fatal("runtime inventory must cover each module exactly once")
	}
}
