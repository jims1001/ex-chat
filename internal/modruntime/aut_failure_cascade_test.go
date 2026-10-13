package modruntime

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestAUTFailureContinuationCascadeEnumeratesOnlyConfirmedEffects(t *testing.T) {
	for _, actions := range []string{
		`[{"action_name":"send_webhook"},{"action_name":"send_message","action_params":"one"},{"action_name":"assign_agent","action_params":8},{"action_name":"private_note","action_params":"two"},{"action_name":"change_priority","action_params":"urgent"},{"action_name":"assign_agent","action_params":8}]`,
		`[{"action_name":"change_priority","action_params":"urgent"},{"action_name":"assign_agent","action_params":8},{"action_name":"remove_assigned_agent"},{"action_name":"send_message","action_params":"one"},{"action_name":"private_note","action_params":"two"}]`,
	} {
		for _, restore := range []bool{false, true} {
			t.Run(actions+map[bool]string{false: "/live", true: "/restore"}[restore], func(t *testing.T) {
				f := newAutomationActionFixture(t, false, actions)
				cascadeRule(t, f, "message_created", "[]", "[]")
				cascadeRule(t, f, "conversation_updated", "[]", "[]")
				cascadeSources(t, f)
				root := f.call(f.startCommand(), 200)
				completed := f.drain(root.ID)
				require.Equal(t, "partially_succeeded", completed.Status)
				version := 4
				if strings.Contains(actions, "remove_assigned_agent") {
					version++ // activity-only proof advances without a descendant
				}
				require.Equal(t, uint(version), completed.RunnerTargetVersion)
				if restore {
					f = failureRecoveryRestore(t, f)
				}
				con, msg := cascadeSources(t, f)
				bridge := &AutomationBridge{Runtime: &f.aut, Sources: []AutomationFactSource{con, msg}}
				drainCascadeBridge(t, bridge)
				f.assertTarget("open", "urgent", version)
				var children []domain.AutomationRuleExecution
				require.NoError(t, f.aut.DB.Where("origin_execution_id = ?", root.ID).Order("id ASC").Find(&children).Error)
				require.Len(t, children, 3) // two message saves and one actual priority mutation
				for _, child := range children {
					require.Equal(t, "succeeded", child.Status)
					require.Equal(t, uint(version), child.TargetVersion)
				}
				// Every claimed descendant and its unchanged original owner version survives
				// an additional AUT-only snapshot validation and idempotent source replay.
				path := filepath.Join(t.TempDir(), "completed-cascade.json")
				require.NoError(t, CreateSnapshot(f.aut.DB, "AUT", "completed-cascade", path))
				target := newModuleTestDB(t, "AUT")
				require.NoError(t, RestoreSnapshot(context.Background(), target, "AUT", "completed-cascade", path, nil))
				n, err := bridge.RunOnce(context.Background())
				require.NoError(t, err)
				require.Zero(t, n)
			})
		}
	}
}
