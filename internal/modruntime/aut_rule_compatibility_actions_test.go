package modruntime

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

func compatTestAction(t *testing.T, raw string, profile domain.AutomationCompatibilityProfile) domain.AutomationCompatibilitySource {
	t.Helper()
	out := compatNewSource("saved", "conversation_created")
	inspectAutomationCompatibilityActions(raw, &out, profile)
	plan, admissionReason := compileAutomationContinuingActions(raw)
	if out.CompiledSteps != len(plan) {
		t.Fatalf("compiled count = %d, compiler = %d", out.CompiledSteps, len(plan))
	}
	wantReasons := map[int]string{}
	for _, action := range plan {
		if action.FailureReason != "" {
			if action.LogicalIndex == nil {
				t.Fatal("compiler failure has no logical index")
			}
			wantReasons[*action.LogicalIndex] = action.FailureReason
		}
	}
	gotReasons := map[int]string{}
	gotAdmission := ""
	for _, issue := range out.Issues {
		if issue.ReasonCode == "" {
			continue
		}
		if issue.LogicalActionIndex == nil {
			gotAdmission = issue.ReasonCode
			if issue.RuntimeEffect != "admission_failed_if_matched" {
				t.Fatalf("admission issue has effect %q", issue.RuntimeEffect)
			}
		} else {
			gotReasons[*issue.LogicalActionIndex] = issue.ReasonCode
			if issue.RuntimeEffect != "logical_action_failed_if_reached" {
				t.Fatalf("logical failure has effect %q", issue.RuntimeEffect)
			}
		}
	}
	if admissionReason != gotAdmission || !reflect.DeepEqual(wantReasons, gotReasons) {
		t.Fatalf("inspector reasons = %q/%v, compiler = %q/%v", gotAdmission, gotReasons, admissionReason, wantReasons)
	}
	if len(out.FailedLogicalIndexes) != len(wantReasons) {
		t.Fatalf("failure indexes = %v, compiler = %v", out.FailedLogicalIndexes, wantReasons)
	}
	for i, index := range out.FailedLogicalIndexes {
		if wantReasons[index] == "" || (i > 0 && index <= out.FailedLogicalIndexes[i-1]) {
			t.Fatalf("wrong/unsorted failure index %v", out.FailedLogicalIndexes)
		}
	}
	return out
}

func compatTestActionJSON(t *testing.T, name string, param any) string {
	t.Helper()
	raw, err := json.Marshal([]any{map[string]any{"action_name": name, "action_params": param}})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func compatTestHasActionIssue(out domain.AutomationCompatibilitySource, code string) bool {
	for _, issue := range out.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func TestAutomationCompatibilityActionsFixtureMatrix(t *testing.T) {
	cases := []struct {
		name, raw, code string
		steps           int
		failed          bool
	}{
		{"capability message", `[{"capability_id":"message.create_public","capability_version":"v1","action_params":{"content":"x"}}]`, "", 2, false},
		{"capability and name", `[{"capability_id":"message.create_public","action_name":"send_message","action_params":"x"}]`, "conflicting_action_capability", 1, true},
		{"unknown version", `[{"action_name":"change_priority","capability_version":"v2","action_params":"urgent"}]`, "unsupported_action_version", 1, true},
		{"unknown capability", `[{"capability_id":"not_a_capability"}]`, "unsupported_action", 1, true},
		{"agent aliases", `[{"action_name":"assign_agent","action_params":{"user_id":7,"agent_id":8}}]`, "conflicting_parameter_alias", 1, false},
		{"team aliases", `[{"action_name":"assign_team","action_params":{"team_id":7,"team_ids":[8],"id":9}}]`, "conflicting_parameter_alias", 1, false},
		{"extra priority", `[{"action_name":"change_priority","action_params":["urgent","low"]}]`, "ignored_extra_action_parameters", 1, false},
		{"missing status", `[{"action_name":"change_status"}]`, "default_applied_open", 1, false},
		{"numeric status", `[{"action_name":"change_status","action_params":[7]}]`, "default_applied_open", 1, false},
		{"unknown status object", `[{"action_name":"change_status","action_params":{"x":"a","y":"b"}}]`, "default_applied_open", 1, false},
		{"forced status", `[{"action_name":"resolve_conversation","action_params":{"status":"open"}}]`, "forced_resolved", 1, false},
		{"open alias is not forced", `[{"action_name":"open_conversation","action_params":{"status":"pending"}}]`, "", 1, false},
		{"closed normalization", `[{"action_name":"change_status","action_params":"closed"}]`, "normalized_resolved", 1, false},
		{"snoozed status invalid", `[{"action_name":"change_status","action_params":"snoozed"}]`, "invalid_action_parameters", 1, true},
		{"message aliases", `[{"action_name":"send_message","action_params":{"message":"x","content":"y"}}]`, "conflicting_parameter_alias", 2, false},
		{"identical aliases", `[{"action_name":"send_message","action_params":{"message":"x","content":"x"}}]`, "redundant_parameter_alias", 2, false},
		{"singleton fallback", `[{"action_name":"send_message","action_params":{"legacy":"x"}}]`, "singleton_object_fallback", 2, false},
		{"unknown object fails", `[{"action_name":"send_message","action_params":{"legacy":"x","other":"y"}}]`, "invalid_action_parameters", 1, true},
		{"trim and dedupe", `[{"action_name":"add_labels","action_params":{"labels":[" x ","x","X",""]}}]`, "duplicate_labels_ignored", 4, false},
		{"labels alias conflict", `[{"action_name":"add_labels","action_params":{"labels":["x"],"label":"y"}}]`, "conflicting_parameter_alias", 2, false},
		{"remove alias conflict", `[{"action_name":"remove_labels","action_params":{"label":"x","remove":["y"]}}]`, "conflicting_parameter_alias", 2, false},
		{"numeric add title", `[{"action_name":"add_label","action_params":7}]`, "numeric_label_used_as_title", 2, false},
		{"numeric remove identity", `[{"action_name":"remove_label","action_params":"7"}]`, "numeric_label_used_as_id", 2, false},
		{"null params fallback", `[{"action_name":"change_priority","action_params":null,"params":["urgent"]}]`, "", 1, false},
		{"empty actions", `[]`, "empty_action_history_difference", 0, false},
		{"empty object actions", `{}`, "empty_action_history_difference", 0, false},
		{"empty raw actions", ``, "empty_action_history_difference", 0, false},
		{"null rejected", `null`, "invalid_action_definition", 0, false},
		{"null wrapper", `{"actions":null}`, "invalid_action_definition", 0, false},
		{"empty wrapper", `{"actions":[]}`, "invalid_action_definition", 0, false},
		{"spaced empty array", `[ ]`, "invalid_action_definition", 0, false},
		{"trailing malformed", `[{"action_name":"mute_conversation"}] broken`, "invalid_action_definition", 0, false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			out := compatTestAction(t, test.raw, domain.AutomationCompatibilityProfile{})
			if out.CompiledSteps != test.steps || (len(out.FailedLogicalIndexes) > 0) != test.failed {
				t.Fatalf("steps/failures = %d/%v, want %d/%t", out.CompiledSteps, out.FailedLogicalIndexes, test.steps, test.failed)
			}
			if test.code != "" && !compatTestHasActionIssue(out, test.code) {
				t.Fatalf("missing %s: %+v", test.code, out.Issues)
			}
			if test.name == "open alias is not forced" && (compatTestHasActionIssue(out, "forced_resolved") || compatTestHasActionIssue(out, "default_applied_open")) {
				t.Fatal("open_conversation must preserve a valid pending parameter")
			}
		})
	}
}

func TestAutomationCompatibilityActionsAliasesAndInvalidParameters(t *testing.T) {
	for _, name := range []string{"send_email_to_team", "send_email_transcript", "send_webhook_event", "send_attachment", "add_sla", "open", "pending_conversation", "UNKNOWN_ACTION"} {
		t.Run(name, func(t *testing.T) {
			raw := fmt.Sprintf(`[{"action_name":%q},{"action_name":"change_priority","action_params":"urgent"}]`, name)
			out := compatTestAction(t, raw, domain.AutomationCompatibilityProfile{})
			if !reflect.DeepEqual(out.FailedLogicalIndexes, []int{0}) || out.CompiledSteps != 2 || !compatTestHasActionIssue(out, "unsupported_action") {
				t.Fatalf("unsupported action must preserve later action: %+v", out)
			}
		})
	}
	for _, name := range []string{"send_message", "send_reply", "reply", "add_private_note", "private_note"} {
		t.Run(name, func(t *testing.T) {
			out := compatTestAction(t, compatTestActionJSON(t, name, "x"), domain.AutomationCompatibilityProfile{})
			if out.CompiledSteps != 2 || len(out.FailedLogicalIndexes) != 0 {
				t.Fatalf("supported message alias: %+v", out)
			}
		})
	}
	for _, name := range []string{"resolve_conversation", "close_conversation", "close", "resolve"} {
		out := compatTestAction(t, compatTestActionJSON(t, name, map[string]any{"status": "pending"}), domain.AutomationCompatibilityProfile{})
		if !compatTestHasActionIssue(out, "forced_resolved") || len(out.FailedLogicalIndexes) != 0 {
			t.Fatalf("supported forced status alias %s: %+v", name, out)
		}
	}
	for _, param := range []any{nil, []any{}, "", "none", "Urgent", 7, map[string]any{"priority": ""}} {
		out := compatTestAction(t, compatTestActionJSON(t, "change_priority", param), domain.AutomationCompatibilityProfile{})
		if !reflect.DeepEqual(out.FailedLogicalIndexes, []int{0}) {
			t.Fatalf("priority parameter %#v incorrectly accepted", param)
		}
	}
	for _, param := range []any{nil, "", " \n\t ", []any{7, "x"}, map[string]any{"message": 7}} {
		out := compatTestAction(t, compatTestActionJSON(t, "send_message", param), domain.AutomationCompatibilityProfile{})
		if !reflect.DeepEqual(out.FailedLogicalIndexes, []int{0}) {
			t.Fatalf("message parameter %#v incorrectly accepted", param)
		}
	}
	for _, param := range []any{true, false, map[string]any{"unknown": "x"}, json.Number("1.5"), json.Number("0"), json.Number("-1"), []any{"x", true}} {
		out := compatTestAction(t, compatTestActionJSON(t, "add_label", param), domain.AutomationCompatibilityProfile{})
		if !reflect.DeepEqual(out.FailedLogicalIndexes, []int{0}) {
			t.Fatalf("label parameter %#v incorrectly accepted", param)
		}
	}
}

func TestAutomationCompatibilityActionsSnoozeFallback(t *testing.T) {
	for _, param := range []any{nil, "", "nonsense", "0s", "-1h", "999999999999999999h", []any{7}, map[string]any{"first": "x", "second": "y"}} {
		out := compatTestAction(t, compatTestActionJSON(t, "snooze", param), domain.AutomationCompatibilityProfile{})
		if len(out.FailedLogicalIndexes) != 0 || !compatTestHasActionIssue(out, "fallback_24h") {
			t.Fatalf("snooze fallback %#v: %+v", param, out)
		}
	}
	for _, param := range []string{"1ns", "9223372036854775807ns", "24h"} {
		out := compatTestAction(t, compatTestActionJSON(t, "snooze_conversation", param), domain.AutomationCompatibilityProfile{})
		if len(out.FailedLogicalIndexes) != 0 || compatTestHasActionIssue(out, "fallback_24h") {
			t.Fatalf("positive duration %q must be accepted: %+v", param, out)
		}
	}
}

func TestAutomationCompatibilityActionsIdentityBounds(t *testing.T) {
	cases := []struct {
		token    string
		valid64  bool
		valid32  bool
		platform bool
	}{
		{"1", true, true, false}, {"1.5", false, false, false}, {"0", false, false, false}, {"-1", false, false, false},
		{"1e3", true, true, false}, {"4294967295", true, true, false}, {"4294967296", true, false, true},
		{"9007199254740993", true, false, true}, {"18446744073709551615", true, false, true}, {"18446744073709551616", false, false, false},
		{`"4294967295"`, true, true, false}, {`"4294967296"`, true, false, true}, {`"4294967297"`, true, true, true},
		{`"18446744073709551615"`, true, true, true}, {`"18446744073709551616"`, false, false, false},
		{`"bad"`, false, false, false}, {`"last_responding_agent"`, false, false, false}, {`" 7 "`, true, true, false},
	}
	for _, test := range cases {
		t.Run(test.token, func(t *testing.T) {
			raw := `[{"action_name":"assign_agent","action_params":[` + test.token + `]}]`
			out := compatTestAction(t, raw, domain.AutomationCompatibilityProfile{})
			wantValid := test.valid64
			if strconv.IntSize == 32 {
				wantValid = test.valid32
			}
			if (len(out.FailedLogicalIndexes) == 0) != wantValid {
				t.Fatalf("identity on %d-bit compiler: %+v", strconv.IntSize, out)
			}
			if compatTestHasActionIssue(out, "platform_dependent_identity_range") != test.platform {
				t.Fatalf("platform coverage issue mismatch: %+v", out.Issues)
			}
			if compatTestHasActionIssue(out, "condition_numeric_precision_risk") {
				t.Fatal("exact action JSON numbers must not be classified as condition float values")
			}
		})
	}
	// String labels never pass through the platform-sized numeric action helper.
	out := compatTestAction(t, compatTestActionJSON(t, "remove_label", "9007199254740993"), domain.AutomationCompatibilityProfile{})
	if compatTestHasActionIssue(out, "platform_dependent_identity_range") || len(out.FailedLogicalIndexes) != 0 {
		t.Fatalf("string label should use fixed-width owner lookup: %+v", out)
	}
}

func TestAutomationCompatibilityActionsByteLimits(t *testing.T) {
	for _, kind := range []struct {
		name string
		max  int
	}{{"add_label", 100}, {"send_message", 100000}, {"add_private_note", 100000}} {
		for _, delta := range []int{-1, 0, 1} {
			for _, unicode := range []bool{false, true} {
				n := kind.max + delta
				body := strings.Repeat("x", n)
				if unicode {
					body = strings.Repeat("界", n/3) + strings.Repeat("x", n%3)
				}
				t.Run(fmt.Sprintf("%s/%d/unicode=%t", kind.name, n, unicode), func(t *testing.T) {
					out := compatTestAction(t, compatTestActionJSON(t, kind.name, body), domain.AutomationCompatibilityProfile{})
					if (len(out.FailedLogicalIndexes) > 0) != (n > kind.max) {
						t.Fatalf("%d-byte boundary mismatch: %+v", n, out)
					}
					if out.CompiledSteps != 2 && n <= kind.max {
						t.Fatalf("valid label/message must compile two steps: %+v", out)
					}
				})
			}
		}
	}
}

func compatTestLabels(n int) []string {
	labels := make([]string, n)
	for i := range labels {
		labels[i] = fmt.Sprintf("label-%03d", i)
	}
	return labels
}

func TestAutomationCompatibilityActionsCountLimitsAndContinuation(t *testing.T) {
	for _, count := range []int{99, 100, 101} {
		t.Run(fmt.Sprintf("labels-%d", count), func(t *testing.T) {
			out := compatTestAction(t, compatTestActionJSON(t, "add_labels", compatTestLabels(count)), domain.AutomationCompatibilityProfile{})
			if (len(out.FailedLogicalIndexes) > 0) != (count > 100) || compatTestHasActionIssue(out, "label_count_limit_exceeded") != (count > 100) {
				t.Fatalf("label count limit mismatch: %+v", out)
			}
		})
		t.Run(fmt.Sprintf("definitions-%d", count), func(t *testing.T) {
			definitions := make([]any, count)
			for i := range definitions {
				definitions[i] = map[string]any{"action_name": "change_priority", "action_params": "urgent"}
			}
			encoded, _ := json.Marshal(definitions)
			out := compatTestAction(t, string(encoded), domain.AutomationCompatibilityProfile{})
			if out.LogicalActions != count || compatTestHasActionIssue(out, "action_definition_limit_exceeded") != (count > 100) {
				t.Fatalf("definition count limit mismatch: %+v", out)
			}
			if count > 100 && (out.AttemptedSteps != 0 || len(out.FailedLogicalIndexes) > 0) {
				t.Fatal("admission failure must not invent logical failures or attempted plans")
			}
		})
	}
	duplicates := make([]string, 1001)
	for i := range duplicates {
		duplicates[i] = " x "
	}
	out := compatTestAction(t, compatTestActionJSON(t, "add_labels", duplicates), domain.AutomationCompatibilityProfile{})
	if out.CompiledSteps != 2 || len(out.FailedLogicalIndexes) > 0 {
		t.Fatalf("raw duplicates must normalize before count cap: %+v", out)
	}
	for _, trailingPriority := range []bool{false, true} {
		definitions := make([]any, 5)
		for i := range definitions {
			definitions[i] = map[string]any{"action_name": "add_labels", "action_params": compatTestLabels(100)}
		}
		if trailingPriority {
			definitions = append(definitions, map[string]any{"action_name": "change_priority", "action_params": "urgent"})
		}
		encoded, _ := json.Marshal(definitions)
		out := compatTestAction(t, string(encoded), domain.AutomationCompatibilityProfile{})
		if !trailingPriority {
			if out.AttemptedSteps != 1000 || out.CompiledSteps != 1000 || len(out.FailedLogicalIndexes) > 0 {
				t.Fatalf("exact 1000 owner-step bound: %+v", out)
			}
		} else if out.AttemptedSteps != 1001 || out.CompiledSteps != 802 || !reflect.DeepEqual(out.FailedLogicalIndexes, []int{4}) || !compatTestHasActionIssue(out, "action_step_limit_exceeded") {
			t.Fatalf("reserve for later logical action: %+v", out)
		}
	}
	out = compatTestAction(t, `[{"action_name":"change_priority","action_params":"low"},{"action_name":"send_attachment"},{"action_name":"change_priority","action_params":"urgent"}]`, domain.AutomationCompatibilityProfile{})
	if out.AttemptedSteps != 3 || out.CompiledSteps != 3 || !reflect.DeepEqual(out.FailedLogicalIndexes, []int{1}) {
		t.Fatalf("independent later actions must continue: %+v", out)
	}
}

func TestAutomationCompatibilityActionsOwnersAndEncodedBounds(t *testing.T) {
	profile := domain.AutomationCompatibilityProfile{Assessed: true, Owners: map[string]bool{"CON": true, "CUS": true, "MSG": true, "TEN": true}, RequestCapBytes: map[string]int{"CON": 100, "MSG": 100, "CUS": 100}}
	out := compatTestAction(t, compatTestActionJSON(t, "send_message", strings.Repeat("<", 20)), profile)
	if !reflect.DeepEqual(out.RequiredOwners, []string{"CON", "MSG"}) {
		t.Fatalf("message owners: %v", out.RequiredOwners)
	}
	lowerBounds := map[string]int{}
	for _, issue := range out.Issues {
		if issue.Code == "owner_request_size_lower_bound_exceeded" {
			lowerBounds[issue.Details.Owner] = issue.Details.Count
		}
	}
	if !reflect.DeepEqual(lowerBounds, map[string]int{"CON": 122, "MSG": 122}) {
		t.Fatalf("escaped JSON lower bound must include mandatory string quotes: %v", lowerBounds)
	}
	out = compatTestAction(t, compatTestActionJSON(t, "add_label", strings.Repeat("<", 20)), profile)
	for _, issue := range out.Issues {
		if issue.Code == "owner_request_size_lower_bound_exceeded" && issue.Details.Owner != "CUS" {
			t.Fatal("CON normally sends a resolved label ID; title length is not an unconditional CON lower bound")
		}
	}
	if !compatTestHasActionIssue(out, "owner_request_size_lower_bound_exceeded") || !compatTestHasActionIssue(out, "owner_request_size_fit_unknown") {
		t.Fatalf("label lower bound + unknown full envelope expected: %+v", out.Issues)
	}
	for _, capBytes := range []int{0, 2, 3, 1000} {
		profile.RequestCapBytes["CON"], profile.RequestCapBytes["MSG"] = capBytes, capBytes
		out = compatTestAction(t, compatTestActionJSON(t, "send_message", "x"), profile)
		if compatTestHasActionIssue(out, "owner_request_size_lower_bound_exceeded") != (capBytes > 0 && capBytes < 3) {
			t.Fatalf("cap %d lower bound mismatch: %+v", capBytes, out.Issues)
		}
		if capBytes >= 3 && !compatTestHasActionIssue(out, "owner_request_size_fit_unknown") {
			t.Fatal("fitting lower bound must not claim full envelope transport fit")
		}
	}
	out = compatTestAction(t, `[{"action_name":"assign_agent","action_params":7}]`, domain.AutomationCompatibilityProfile{})
	if !reflect.DeepEqual(out.RequiredOwners, []string{"CON", "TEN"}) {
		t.Fatalf("assignment must declare CON -> TEN chain: %v", out.RequiredOwners)
	}
}

func TestAutomationCompatibilityActionsRedactionAndDeterminism(t *testing.T) {
	makeRaw := func(secret string) string {
		return fmt.Sprintf(`[{"action_name":"send_message","action_params":{"message":%q,"content":%q}},{"action_name":"send_message","action_params":{%q:%q}},{"action_name":%q},{"capability_id":%q},{"action_name":"add_labels","action_params":[%q,%q]},{"action_name":"assign_agent","action_params":"998877665544332211"}]`, secret, secret+"x", secret, secret, secret, secret, secret, " "+secret+" ")
	}
	var previous string
	for i := 0; i < 10; i++ {
		secret := "SECRET_ACTION_A"
		if i%2 != 0 {
			secret = "SECRET_ACTION_B"
		}
		out := compatTestAction(t, makeRaw(secret), domain.AutomationCompatibilityProfile{})
		encoded, _ := json.Marshal(out)
		text := string(encoded)
		if strings.Contains(text, secret) || strings.Contains(text, "998877665544332211") {
			t.Fatalf("raw secret reached report: %s", text)
		}
		if previous != "" && text != previous {
			t.Fatalf("structurally equal inputs must have identical redacted reports\n%s\n%s", previous, text)
		}
		previous = text
	}
}

func TestAutomationCompatibilityActionsSupportedCapabilities(t *testing.T) {
	cases := []struct {
		capability string
		param      any
		steps      int
	}{
		{"conversation.change_status", "pending", 1}, {"conversation.change_priority", "low", 1},
		{"conversation.assign_agent", 7, 1}, {"conversation.assign_team", 8, 1},
		{"conversation.remove_assigned_agent", nil, 1}, {"conversation.remove_assigned_team", nil, 1},
		{"conversation.mute", nil, 1}, {"conversation.snooze", "1ns", 1},
		{"conversation.add_label", "x", 2}, {"conversation.remove_label", "x", 2},
		{"message.create_public", "x", 2}, {"message.create_private_note", "x", 2},
	}
	for _, test := range cases {
		t.Run(test.capability, func(t *testing.T) {
			encoded, _ := json.Marshal([]any{map[string]any{"capability_id": test.capability, "capability_version": "v1", "action_params": test.param}})
			out := compatTestAction(t, string(encoded), domain.AutomationCompatibilityProfile{})
			if out.CompiledSteps != test.steps || out.AttemptedSteps != test.steps || len(out.FailedLogicalIndexes) != 0 {
				t.Fatalf("capability projection mismatch: %+v", out)
			}
		})
	}
}

func TestAutomationCompatibilityActionsWrapperAndParameterPrecedence(t *testing.T) {
	raw := `{"actions":[{"action_name":"change_priority","name":"send_attachment","action_params":["urgent"],"params":["low"]}],"rules":[{"action_name":"send_attachment"}],"values":[{"action_name":"send_attachment"}]}`
	out := compatTestAction(t, raw, domain.AutomationCompatibilityProfile{})
	if out.CompiledSteps != 1 || len(out.FailedLogicalIndexes) > 0 || !compatTestHasActionIssue(out, "multiple_definition_wrappers") || !compatTestHasActionIssue(out, "conflicting_action_alias") {
		t.Fatalf("wrapper/action alias precedence mismatch: %+v", out)
	}
	for _, raw := range []string{
		`[{"action_name":"assign_team","action_params":{"team_ids":[[7,8]]}}]`,
		`[{"action_name":"assign_agent","action_params":[[7,8]]}]`,
	} {
		out = compatTestAction(t, raw, domain.AutomationCompatibilityProfile{})
		if len(out.FailedLogicalIndexes) > 0 || !compatTestHasActionIssue(out, "ignored_extra_action_parameters") {
			t.Fatalf("nested ID first-element extraction mismatch: %+v", out)
		}
	}
	// Wrong/empty first named values fall through to the next valid named value.
	for _, raw := range []string{
		`[{"action_name":"assign_agent","action_params":{"user_id":0,"agent_id":7}}]`,
		`[{"action_name":"send_message","action_params":{"message":"","content":"x"}}]`,
	} {
		out = compatTestAction(t, raw, domain.AutomationCompatibilityProfile{})
		if len(out.FailedLogicalIndexes) > 0 || !compatTestHasActionIssue(out, "ignored_extra_action_parameters") {
			t.Fatalf("named parameter fallback mismatch: %+v", out)
		}
	}
}
