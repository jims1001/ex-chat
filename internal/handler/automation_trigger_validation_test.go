package handler

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAutomationTriggerValidationKeepsLegacyMatrix(t *testing.T) {
	for _, name := range []string{"conversation_created", "conversation_updated", "message_created", "conversation_opened"} {
		require.NoError(t, validateAutomationRule(name, nil, nil), name)
		require.NoError(t, validateAutomationRuleForMode(name, nil, nil, true), name)
	}
	require.Error(t, validateAutomationRule("conversation_resolved", nil, nil))
	require.NoError(t, validateAutomationRuleForMode("conversation_resolved", nil, nil, true))
	for _, name := range []string{"conversation_closed", "message_updated", "conversation_OPENED"} {
		require.Error(t, validateAutomationRule(name, nil, nil), name)
		require.Error(t, validateAutomationRuleForMode(name, nil, nil, true), name)
	}
	// Enabling one event must never skip the existing condition/action validation.
	require.Error(t, validateAutomationRuleForMode("conversation_resolved", []map[string]any{{"attribute_key": "unsupported_attribute"}}, nil, true))
	require.Error(t, validateAutomationRuleForMode("conversation_resolved", nil, []map[string]any{{"action_name": "unsupported_action"}}, true))
}
