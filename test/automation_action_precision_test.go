package test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

func TestAutomationActionAPIKeepsExactNumericIdentities(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("precise-actions")
	for _, actions := range []any{json.RawMessage(`[{"action_name":"assign_agent","action_params":[9007199254740993]}]`), `{"actions":[{"name":"remove_label","params":[9007199254740993]}]}`} {
		rule := f.rule(a, "conversation_created", []any{}, actions)
		versions := f.versions(a, rule.ID)
		if len(versions) != 1 || !strings.Contains(versions[0].Actions, "9007199254740993") || strings.Contains(versions[0].Actions, "9007199254740992") {
			t.Fatalf("create lost identity: %+v", versions)
		}
		var updated domain.AutomationRule
		f.request(a.token, http.MethodPut, a.path("/automation_rules/"+fmt.Sprint(rule.ID)), map[string]any{"actions": json.RawMessage(`{"actions":[{"name":"assign_team","params":{"team_ids":[9007199254740993]}}]}`)}, http.StatusOK, &updated)
		versions = f.versions(a, rule.ID)
		if len(versions) != 2 || !strings.Contains(versions[1].Actions, "9007199254740993") {
			t.Fatalf("update lost identity: %+v", versions)
		}
	}
}
