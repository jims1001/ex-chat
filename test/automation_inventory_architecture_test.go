package test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Keep the read projection in AUT. This is additional coverage, not a relaxed
// substitute for source collaboration, persisted-model or contract guards.
func TestAutomationInventorySourceOwnershipAndPureProjection(t *testing.T) {
	raw, err := os.ReadFile("../internal/database/model_owners.go")
	if err != nil {
		t.Fatal(err)
	}
	models := map[string]string{}
	for _, m := range regexp.MustCompile(`"(\w+)":\s*"(\w+)"`).FindAllStringSubmatch(string(raw), -1) {
		models[m[1]] = m[2]
	}
	pure := []string{"internal/modruntime/aut_rule_compatibility.go", "internal/modruntime/aut_rule_compatibility_conditions.go", "internal/modruntime/aut_rule_compatibility_actions.go", "internal/domain/automation_compatibility.go"}
	for _, path := range append(pure, "internal/service/automation_rule_compatibility.go") {
		source, err := os.ReadFile("../" + path)
		if err != nil {
			t.Fatal(err)
		}
		if err := checkAutomationActionOwnerSource(string(source), "AUT", models, map[string][]string{}); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		for _, forbidden := range []string{".Create(", ".Save(", ".Update(", ".Updates(", ".Delete(", ".AutoMigrate(", "PrepareDurableRules(", "NewMonolithAutomation(", "SetDurableExecution(", "newModuleClient(", ".Dispatch("} {
			if strings.Contains(string(source), forbidden) {
				t.Fatalf("inventory %s contains effect %s", path, forbidden)
			}
		}
	}
	for _, path := range pure {
		source, _ := os.ReadFile("../" + path)
		for _, forbidden := range []string{"\"gorm.io/", "\"net/", "\"os\"", "\"context\"", "*Runtime", "*AutomationService", "newModuleClient"} {
			if strings.Contains(string(source), forbidden) {
				t.Fatalf("pure inventory %s imports execution %s", path, forbidden)
			}
		}
	}
	route, err := os.ReadFile("../internal/router/router.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(route), `tenant.GET("/automation_rules/compatibility", reqPerm(domain.PermissionAutomationManage), advancedHandler.ListAutomationRuleCompatibility)`) {
		t.Fatal("inventory definition-sensitive permission missing")
	}
}
