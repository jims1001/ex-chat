package test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestAutomationDurableStatusEventsAPICreateUpdateClone(t *testing.T) {
	f := newDurableAutomationAPIFixture(t)
	a := f.tenant("status-event-validation")
	for _, event := range []string{"conversation_opened", "conversation_resolved"} {
		rule := f.rule(a, event, []any{}, []any{durableAutomationAction("change_priority", "high")})
		var updated domain.AutomationRule
		f.request(a.token, http.MethodPut, a.path(fmt.Sprintf("/automation_rules/%d", rule.ID)), map[string]any{"event_name": event, "name": "Valid status rule"}, http.StatusOK, &updated)
		require.Equal(t, event, updated.EventName)
		var cloned domain.AutomationRule
		f.request(a.token, http.MethodPost, a.path(fmt.Sprintf("/automation_rules/%d/clone", rule.ID)), map[string]any{"name": "Cloned status rule"}, http.StatusCreated, &cloned)
		require.Equal(t, event, cloned.EventName)
		require.NotEqual(t, rule.ID, cloned.ID)
		require.False(t, cloned.Active)
	}
}
