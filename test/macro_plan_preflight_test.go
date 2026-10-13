package test

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMacroPlanPreflightOwners(t *testing.T) { runMacroPlanPreflightOwners(t, macroPlanSQLite) }

func runMacroPlanPreflightOwners(t *testing.T, open func(*testing.T, string) *gorm.DB) {
	f := newMacroPlanFixture(t, open)
	ctx := context.Background()
	t.Run("reject_entire_plan_before_effects", func(t *testing.T) {
		for _, tc := range []struct{ name, action string }{
			{"email_transcript", "send_email_transcript"}, {"webhook_event", "send_webhook_event"},
			{"unknown", "future_macro_action"}, {"empty_name", ""}, {"whitespace_name", " open"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				a, b, foreign := f.conversation(t, 91), f.conversation(t, 91), f.conversation(t, 92)
				// These actions previously committed labels, notes, attachments and
				// activity before reaching the silently ignored unsupported action.
				m := f.macro(t, macroPlanJSON(t, []any{
					macroPlanAction("add_label", "must-not-be-created"),
					macroPlanAction("add_private_note", "must-not-be-written"),
					macroPlanAction("send_attachment", "https://example.test/no-delivery.bin"),
					macroPlanAction("resolve"), macroPlanAction("assign_agent", "91"),
					macroPlanAction("assign_team", fmt.Sprint(f.team.ID)), macroPlanAction(tc.action),
				}))
				before := macroPlanSnapshot(t, f.stores)
				counts := macroPlanObserve(t, f.stores)
				var reads atomic.Int64
				require.NoError(t, f.stores["CON"].Callback().Query().Before("gorm:query").Register("macro_plan_target", func(*gorm.DB) { reads.Add(1) }))
				t.Cleanup(func() { require.NoError(t, f.stores["CON"].Callback().Query().Remove("macro_plan_target")) })
				res, err := f.repo.ExecuteForUser(ctx, 91, m.ID, 91, false, []uint{a.ID, b.ID, foreign.ID, 999999})
				require.ErrorContains(t, err, "unsupported macro action")
				require.Nil(t, res)
				require.Zero(t, reads.Load(), "the whole plan must be validated before target lookup")
				macroPlanAssertUnchanged(t, f.stores, before, counts)
			})
		}
	})
	t.Run("reject_before_target_selection", func(t *testing.T) {
		for _, name := range []string{"nil_targets", "empty_targets", "missing_target"} {
			t.Run(name, func(t *testing.T) {
				m := f.macro(t, `[{"name":"send_webhook_event"}]`)
				var ids []uint
				if name == "empty_targets" {
					ids = []uint{}
				}
				if name == "missing_target" {
					ids = []uint{999999}
				}
				before := macroPlanSnapshot(t, f.stores)
				counts := macroPlanObserve(t, f.stores)
				res, err := f.repo.ExecuteForUser(ctx, 91, m.ID, 91, false, ids)
				require.ErrorContains(t, err, "unsupported macro action")
				require.Nil(t, res)
				macroPlanAssertUnchanged(t, f.stores, before, counts)
			})
		}
	})
	t.Run("parsing_and_name_precedence", func(t *testing.T) {
		for _, tc := range []struct {
			name, raw string
			invalid   bool
		}{
			{"array", `[{"name":"resolve"}]`, false},
			{"actions_wrapper", `{"actions":[{"name":"resolve"}]}`, false},
			{"values_wrapper", `{"values":[{"name":"resolve"}]}`, false},
			{"single", `{"name":"resolve"}`, false},
			{"actions_before_values", `{"actions":[{"name":"resolve"}],"values":[{"name":"unknown"}]}`, false},
			{"empty_actions_fall_back_values", `{"actions":[],"values":[{"name":"resolve"}]}`, false},
			{"action_name_before_name", `[{"action_name":"resolve","name":"send_webhook_event"}]`, false},
			{"empty_action_name_uses_name", `[{"action_name":"","name":"resolve"}]`, false},
			{"unsupported_action_name_wins", `[{"action_name":"send_email_transcript","name":"resolve"}]`, true},
			{"unsupported_actions_wrapper_wins", `{"actions":[{"name":"unknown"}],"values":[{"name":"resolve"}]}`, true},
			{"unsupported_values_wrapper", `{"values":[{"name":"resolve"},{"name":"send_webhook_event"}]}`, true},
			{"unsupported_single", `{"name":"send_email_transcript"}`, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				c := f.conversation(t, 91)
				m := f.macro(t, tc.raw)
				before := macroPlanSnapshot(t, f.stores)
				counts := macroPlanObserve(t, f.stores)
				res, err := f.repo.ExecuteForUser(ctx, 91, m.ID, 91, false, []uint{c.ID})
				if tc.invalid {
					require.ErrorContains(t, err, "unsupported macro action")
					require.Nil(t, res)
					macroPlanAssertUnchanged(t, f.stores, before, counts)
					return
				}
				require.NoError(t, err)
				require.Equal(t, map[uint]string{c.ID: "success"}, res)
				var stored domain.Conversation
				require.NoError(t, f.stores["CON"].First(&stored, c.ID).Error)
				require.Equal(t, "resolved", stored.Status)
			})
		}
	})
	t.Run("empty_parsed_plan_contract", func(t *testing.T) {
		for _, tc := range []struct{ name, raw string }{
			{"blank", ""}, {"whitespace", " "}, {"empty_array", "[]"}, {"empty_object", "{}"},
			{"null", "null"}, {"malformed", "[broken"}, {"empty_wrappers", `{"actions":[],"values":[]}`},
		} {
			t.Run(tc.name, func(t *testing.T) {
				c := f.conversation(t, 91)
				m := f.macro(t, tc.raw)
				before := macroPlanSnapshot(t, f.stores)
				counts := macroPlanObserve(t, f.stores)
				res, err := f.repo.ExecuteForUser(ctx, 91, m.ID, 91, false, []uint{c.ID})
				require.EqualError(t, err, "invalid macro actions: no executable actions found")
				require.Nil(t, res)
				macroPlanAssertUnchanged(t, f.stores, before, counts)
			})
		}
	})
	t.Run("visibility_before_plan_validation", func(t *testing.T) {
		for _, name := range []string{"foreign_account", "other_personal_owner", "missing_macro"} {
			t.Run(name, func(t *testing.T) {
				m := f.macro(t, `[{"action_name":"send_email_transcript"}]`)
				require.NoError(t, f.stores["OPS"].Model(&m).Update("visibility", "personal").Error)
				account, user, id := uint(91), uint(91), m.ID
				if name == "foreign_account" {
					account = 92
				}
				if name == "other_personal_owner" {
					user = 92
				}
				if name == "missing_macro" {
					id = 999999
				}
				before := macroPlanSnapshot(t, f.stores)
				counts := macroPlanObserve(t, f.stores)
				res, err := f.repo.ExecuteForUser(ctx, account, id, user, false, []uint{1})
				require.ErrorIs(t, err, gorm.ErrRecordNotFound)
				require.ErrorContains(t, err, "macro not found or not accessible")
				require.NotContains(t, err.Error(), "unsupported")
				require.Nil(t, res)
				macroPlanAssertUnchanged(t, f.stores, before, counts)
			})
		}
	})
	t.Run("implemented_names_keep_owner_effects", func(t *testing.T) {
		names := []string{"resolve_conversation", "close_conversation", "close", "resolve", "open_conversation", "open", "snooze_conversation", "snooze", "mute_conversation", "change_status", "assign_agent", "remove_assigned_agent", "assign_team", "remove_assigned_team", "change_priority", "add_label", "add_labels", "remove_label", "remove_labels", "send_message", "send_reply", "reply", "add_private_note", "private_note", "send_attachment"}
		require.Len(t, names, 25)
		for _, name := range names {
			t.Run(name, func(t *testing.T) {
				c := f.conversation(t, 91)
				// Start these aliases in a different state so the assertions
				// cannot pass solely because the final activity write ran.
				switch name {
				case "open_conversation", "open":
					until := time.Now().UTC().Add(time.Hour)
					require.NoError(t, f.stores["CON"].Model(&c).Updates(map[string]any{"status": "snoozed", "snoozed_until": until}).Error)
				case "assign_agent":
					require.NoError(t, f.stores["CON"].Model(&c).Update("assignee_id", nil).Error)
				case "assign_team":
					require.NoError(t, f.stores["CON"].Model(&c).Update("team_id", nil).Error)
				}
				params := []string{}
				switch name {
				case "snooze_conversation", "snooze":
					params = []string{"2h"}
				case "change_status":
					params = []string{"closed"}
				case "change_priority":
					params = []string{"urgent"}
				case "assign_agent":
					params = []string{"91"}
				case "assign_team":
					params = []string{fmt.Sprint(f.team.ID)}
				case "add_label", "add_labels":
					params = []string{"added-plan-label"}
				case "remove_label", "remove_labels":
					params = []string{f.label.Title}
				case "send_message", "send_reply", "reply", "add_private_note", "private_note":
					params = []string{"preserved macro message"}
				case "send_attachment":
					params = []string{"https://example.test/local-attachment.bin"}
				}
				m := f.macro(t, macroPlanJSON(t, []any{macroPlanAction(name, params...)}))
				res, err := f.repo.ExecuteForUser(ctx, 91, m.ID, 91, false, []uint{c.ID})
				require.NoError(t, err)
				require.Equal(t, map[uint]string{c.ID: "success"}, res)
				var stored domain.Conversation
				require.NoError(t, f.stores["CON"].First(&stored, c.ID).Error)
				require.Greater(t, stored.ObjectVersion, c.ObjectVersion)
				require.True(t, stored.LastActivityAt.After(c.LastActivityAt))
				switch name {
				case "resolve_conversation", "close_conversation", "close", "resolve", "change_status", "mute_conversation":
					require.Equal(t, "resolved", stored.Status)
				case "open_conversation", "open":
					require.Equal(t, "open", stored.Status)
					require.Nil(t, stored.SnoozedUntil)
				case "snooze_conversation", "snooze":
					require.Equal(t, "snoozed", stored.Status)
					require.NotNil(t, stored.SnoozedUntil)
				case "assign_agent":
					require.NotNil(t, stored.AssigneeID)
					require.EqualValues(t, 91, *stored.AssigneeID)
				case "remove_assigned_agent":
					require.Nil(t, stored.AssigneeID)
				case "assign_team":
					require.NotNil(t, stored.TeamID)
					require.Equal(t, f.team.ID, *stored.TeamID)
				case "remove_assigned_team":
					require.Nil(t, stored.TeamID)
				case "change_priority":
					require.Equal(t, "urgent", stored.Priority)
				}
				if name == "mute_conversation" {
					require.True(t, stored.Muted)
				}
				if strings.Contains(name, "label") {
					var links []domain.ConversationLabel
					require.NoError(t, f.stores["CON"].Where("conversation_id = ?", c.ID).Find(&links).Error)
					if strings.HasPrefix(name, "remove") {
						require.Empty(t, links)
					} else {
						require.Len(t, links, 2)
					}
				}
				if name == "send_message" || name == "send_reply" || name == "reply" || name == "add_private_note" || name == "private_note" || name == "send_attachment" {
					var messages []domain.Message
					require.NoError(t, f.stores["MSG"].Where("conversation_id = ?", c.ID).Find(&messages).Error)
					require.Len(t, messages, 1)
					require.Equal(t, params[0], messages[0].Content)
					require.Equal(t, name == "add_private_note" || name == "private_note", messages[0].Private)
					require.Equal(t, domain.MessageTypeOutgoing, messages[0].MessageType)
					require.EqualValues(t, 91, messages[0].SenderID)
					if name == "send_attachment" {
						var attachments []domain.Attachment
						require.NoError(t, f.stores["MSG"].Where("message_id = ?", messages[0].ID).Find(&attachments).Error)
						require.Len(t, attachments, 1)
						require.Equal(t, params[0], attachments[0].DataURL)
					}
				}
			})
		}
	})
	t.Run("valid_target_and_visibility_contracts", func(t *testing.T) {
		c, foreign := f.conversation(t, 91), f.conversation(t, 92)
		m := f.macro(t, `[{"name":"resolve"}]`)
		res, err := f.repo.ExecuteForUser(ctx, 91, m.ID, 91, false, nil)
		require.NoError(t, err)
		require.NotNil(t, res)
		require.Empty(t, res)
		res, err = f.repo.ExecuteForUser(ctx, 91, m.ID, 91, false, []uint{c.ID, foreign.ID, 999999})
		require.NoError(t, err)
		require.Equal(t, map[uint]string{c.ID: "success", foreign.ID: "conversation not found", 999999: "conversation not found"}, res)
		require.NoError(t, f.stores["OPS"].Model(&m).Update("visibility", "personal").Error)
		for _, principal := range []struct {
			user  uint
			admin bool
		}{{91, false}, {92, true}} {
			res, err := f.repo.ExecuteForUser(ctx, 91, m.ID, principal.user, principal.admin, []uint{c.ID})
			require.NoError(t, err)
			require.Equal(t, "success", res[c.ID])
		}
	})
	t.Run("preflight_before_owner_availability", func(t *testing.T) {
		missing := repository.NewMacroRepositoryWithOwnerPorts(f.stores["OPS"], nil, nil, nil, nil, nil)
		bad := f.macro(t, `[{"name":"send_webhook_event"}]`)
		res, err := missing.ExecuteForUser(ctx, 91, bad.ID, 91, false, []uint{1})
		require.ErrorContains(t, err, "unsupported macro action")
		require.Nil(t, res)
		valid := f.macro(t, `[{"name":"resolve"}]`)
		res, err = missing.ExecuteForUser(ctx, 91, valid.ID, 91, false, []uint{1})
		require.ErrorIs(t, err, gorm.ErrInvalidData)
		require.Nil(t, res)
	})
}
