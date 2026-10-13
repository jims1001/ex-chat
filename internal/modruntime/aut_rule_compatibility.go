package modruntime

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

const compatMaxDepth = 32
const compatMaxConditions = 1000
const compatMaxOperands = 10000
const compatMaxIssues = 200

// AutomationRuleCompatibilityView is a pure definition inspector. It cannot
// start a runtime, query owners, freeze a version, or evaluate an invented target.
// A complete inspection is not a cutover/readiness or delivery assertion.
func AutomationRuleCompatibilityView(in domain.AutomationCompatibilityInput) domain.AutomationCompatibilityRule {
	rule := in.Rule
	out := domain.AutomationCompatibilityRule{ID: strconv.FormatUint(uint64(rule.ID), 10), AccountID: strconv.FormatUint(uint64(rule.AccountID), 10), Active: rule.Active, Event: compatEvent(rule.EventName), UpdatedAt: rule.UpdatedAt.UTC(), Complete: true}
	saved := compatInspectSource("saved", rule.EventName, rule.ConditionSemantics, rule.Conditions, rule.Actions, in.SavedConditionBytes, in.SavedActionBytes, in.Profile)
	source := "activation_preview"
	effective := in.Preview
	if in.Frozen != nil {
		source = "effective_frozen"
		effective = in.Frozen
		version := strconv.FormatUint(uint64(effective.Version), 10)
		out.LatestVersion = &version
	}
	var next domain.AutomationCompatibilitySource
	if effective == nil {
		next = compatNewSource(source, "unknown")
		next.ConditionSemantics = "unknown"
		next.Complete = false
		compatIssue(&next, "activation_preview_unavailable", "coverage", "unknown", "/", "not_assessed", "")
	} else {
		next = compatInspectSource(source, effective.EventName, effective.ConditionSemantics, effective.Conditions, effective.Actions, in.EffectiveConditionBytes, in.EffectiveActionBytes, in.Profile)
	}
	if in.Frozen == nil {
		compatIssue(&next, "version_not_frozen", "coverage", "notice", "/", "activation_preview_only", "")
	}
	if effective != nil && saved.Complete && next.Complete {
		if !compatDefinitionsEqual(rule.EventName, rule.Conditions, rule.Actions, rule.ConditionSemantics, effective.EventName, effective.Conditions, effective.Actions, effective.ConditionSemantics) {
			code := "saved_frozen_definition_drift"
			if in.Frozen == nil {
				code = "activation_preview_definition_change"
			}
			compatIssue(&saved, code, "behavior_change", "review", "/", "different_effective_definition", "")
		}
		metadataLost := compatHasMetadata(rule.Conditions, "custom_attribute_type") && !compatHasMetadata(effective.Conditions, "custom_attribute_type")
		if rule.ConditionSemantics == domain.AutomationNumericCustomV1 {
			metadataLost = metadataLost || (compatHasMetadata(rule.Conditions, "custom_attribute_definition_id") && !compatHasMetadata(effective.Conditions, "custom_attribute_definition_id"))
		}
		if metadataLost {
			effect := "typed_custom_scope_ignored"
			if effective.ConditionSemantics == domain.AutomationNumericCustomV1 {
				effect = "numeric_definition_invalid"
			}
			compatIssue(&next, "frozen_metadata_not_preserved", "behavior_change", "review", "/conditions", effect, "")
		}
	}
	compatFinishSource(&saved)
	compatFinishSource(&next)
	out.Sources = []domain.AutomationCompatibilitySource{saved, next}
	out.Complete = saved.Complete && next.Complete
	return out
}
func compatNewSource(source, event string) domain.AutomationCompatibilitySource {
	return domain.AutomationCompatibilitySource{Source: source, Event: compatEvent(event), Complete: true, AssessmentLevel: "static_definition", FailedLogicalIndexes: []int{}, RequiredOwners: []string{}, Issues: []domain.AutomationCompatibilityIssue{}}
}
func compatInspectSource(source, event, semantics, conditions, actions string, conditionBytes, actionBytes int, profile domain.AutomationCompatibilityProfile) domain.AutomationCompatibilitySource {
	out := compatNewSource(source, event)
	out.ConditionSemantics = compatConditionSemantics(semantics)
	if out.ConditionSemantics == "unknown" {
		compatIssue(&out, "unsupported_condition_semantics", "unsupported", "blocker", "/condition_semantics", "admission_failed_if_matched", "invalid_frozen_definition")
	}
	compatAddOwner(&out, "CON")
	if compatEvent(event) == "unknown" {
		compatIssue(&out, "unsupported_event", "unsupported", "blocker", "/event_name", "event_not_admitted", "")
	}
	if event == "conversation_opened" || event == "conversation_resolved" {
		issue := compatIssue(&out, "newly_effective_transition_behavior", "behavior_change", "review", "/event_name", "new_durable_trigger", "")
		issue.Details.KnownEvent = event
	}
	if event == "message_created" {
		compatAddOwner(&out, "MSG")
	}
	if compatInspectJSON(conditions, "conditions", conditionBytes, &out) && out.ConditionSemantics != "unknown" {
		inspectAutomationCompatibilityConditions(conditions, event, &out)
	}
	if compatInspectJSON(actions, "actions", actionBytes, &out) {
		inspectAutomationCompatibilityActions(actions, &out, profile)
	}
	if profile.Assessed {
		out.AssessmentLevel = "composition_profile"
	}
	for _, owner := range out.RequiredOwners {
		if !profile.Assessed {
			issue := compatIssue(&out, "configuration_not_assessed", "configuration", "unknown", "/configuration", "not_assessed", "")
			issue.Details.Owner = owner
		} else if !profile.Owners[owner] {
			code, effect := "dependency_missing", "requires_owner_at_execution"
			if owner == "CUS" {
				code = "conditional_dependency_missing"
			}
			issue := compatIssue(&out, code, "configuration", "unknown", "/configuration", effect, "")
			issue.Details.Owner = owner
		}
	}
	compatIssue(&out, "runtime_context_not_assessed", "coverage", "unknown", "/configuration", "not_assessed", "")
	return out
}
func compatConditionSemantics(value string) string {
	switch value {
	case "", domain.AutomationNumericCustomV1:
		return value
	default:
		return "unknown"
	}
}
func compatEvent(value string) string {
	switch value {
	case "conversation_created", "conversation_updated", "conversation_opened", "conversation_resolved", "message_created":
		return value
	}
	return "unknown"
}
func compatIssue(out *domain.AutomationCompatibilitySource, code, category, severity, path, effect, reason string) *domain.AutomationCompatibilityIssue {
	out.Issues = append(out.Issues, domain.AutomationCompatibilityIssue{Code: code, Category: category, Severity: severity, Source: out.Source, Path: path, RuntimeEffect: effect, ReasonCode: reason})
	return &out.Issues[len(out.Issues)-1]
}
func compatActionPath(index int, field string) string {
	if field == "" {
		return fmt.Sprintf("/actions/%d", index)
	}
	return fmt.Sprintf("/actions/%d/%s", index, field)
}
func compatConditionPath(index int, field string) string {
	return fmt.Sprintf("/conditions/%d/%s", index, field)
}
func compatAddOwner(out *domain.AutomationCompatibilitySource, owner string) {
	for _, v := range out.RequiredOwners {
		if v == owner {
			return
		}
	}
	out.RequiredOwners = append(out.RequiredOwners, owner)
}
func compatFinishSource(out *domain.AutomationCompatibilitySource) {
	sort.Strings(out.RequiredOwners)
	sort.SliceStable(out.Issues, func(i, j int) bool {
		a, b := out.Issues[i], out.Issues[j]
		if a.Path != b.Path {
			return compatPathLess(a.Path, b.Path)
		}
		return a.Code < b.Code
	})
	// Deduplicate identical issue locations. Output never depends on map order.
	unique := out.Issues[:0]
	for _, v := range out.Issues {
		if len(unique) == 0 || !reflect.DeepEqual(unique[len(unique)-1], v) {
			unique = append(unique, v)
		}
	}
	out.Issues = unique
	if len(out.Issues) > compatMaxIssues {
		out.IssuesOmitted = len(out.Issues) - compatMaxIssues
		out.IssuesTruncated = true
		out.Complete = false
		// Keep highest-impact findings when a source exceeds its display budget.
		rank := map[string]int{"blocker": 0, "review": 1, "unknown": 2, "notice": 3}
		sort.SliceStable(out.Issues, func(i, j int) bool { return rank[out.Issues[i].Severity] < rank[out.Issues[j].Severity] })
		out.Issues = out.Issues[:compatMaxIssues]
		sort.SliceStable(out.Issues, func(i, j int) bool {
			a, b := out.Issues[i], out.Issues[j]
			if a.Path != b.Path {
				return compatPathLess(a.Path, b.Path)
			}
			return a.Code < b.Code
		})
	}
}
func compatPathLess(a, b string) bool {
	aa, bb := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < len(aa) && i < len(bb); i++ {
		if aa[i] == bb[i] {
			continue
		}
		an, ae := strconv.Atoi(aa[i])
		bn, be := strconv.Atoi(bb[i])
		if ae == nil && be == nil {
			return an < bn
		}
		return aa[i] < bb[i]
	}
	return len(aa) < len(bb)
}

// Bound token depth before using the runtime decoders. Unknown object names
// never enter issue paths. The runtime parser remains authoritative on validity.
func compatInspectJSON(raw, kind string, byteCount int, out *domain.AutomationCompatibilitySource) bool {
	if len(raw) > byteCount {
		byteCount = len(raw)
	}
	if byteCount > domain.AutomationCompatibilityMaxBytes {
		issue := compatIssue(out, "analysis_limit_exceeded", "limit", "unknown", "/"+kind, "not_assessed", "")
		issue.Details = domain.AutomationCompatibilityDetails{Count: byteCount, Limit: domain.AutomationCompatibilityMaxBytes, Unit: "bytes"}
		out.Complete = false
		return false
	}
	if strings.TrimSpace(raw) == "" {
		return true
	}
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	var walk func(int, string) bool
	walk = func(depth int, path string) bool {
		token, err := d.Token()
		if err != nil {
			return true
		}
		// Keep inspection bounds independent of runtime acceptance. Action and
		// legacy reports retain their conservative 128-byte token budget even
		// though current numeric parsers are bounded and lexical. Exceeding a
		// report bound means incomplete analysis, never a runtime rejection.
		if number, ok := token.(json.Number); ok {
			unit, limit, count := "", 0, 0
			tokenLimit := 128
			if kind == "conditions" && out.ConditionSemantics == domain.AutomationNumericCustomV1 {
				// The adopted exact parser is linear in literal bytes and never
				// expands exponents. Its own finite literal bound is safe here.
				tokenLimit = domain.AutomationNumericMaxLiteralBytes
			}
			if len(number) > tokenLimit {
				unit, limit, count = "numeric_token_bytes", tokenLimit, len(number)
			}
			if index := strings.IndexAny(string(number), "eE"); unit == "" && index >= 0 {
				exponent, err := strconv.ParseInt(string(number)[index+1:], 10, 64)
				if err != nil || exponent < -4096 || exponent > 4096 {
					unit, limit = "numeric_exponent_magnitude", 4096
				}
			}
			if unit != "" {
				issue := compatIssue(out, "analysis_limit_exceeded", "limit", "unknown", "/"+kind, "not_assessed", "")
				issue.Details = domain.AutomationCompatibilityDetails{Count: count, Limit: limit, Unit: unit}
				out.Complete = false
				return false
			}
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return true
		}
		if depth > compatMaxDepth {
			issue := compatIssue(out, "analysis_limit_exceeded", "limit", "unknown", "/"+kind, "not_assessed", "")
			issue.Details = domain.AutomationCompatibilityDetails{Limit: compatMaxDepth, Unit: "nesting_depth"}
			out.Complete = false
			return false
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, e := d.Token()
				if e != nil {
					return true
				}
				s, ok := key.(string)
				if !ok {
					return true
				}
				lower := strings.ToLower(s)
				if seen[lower] {
					compatIssue(out, "ambiguous_json_fields", "ambiguity", "review", path, "decoder_field_precedence", "")
				}
				seen[lower] = true
				if s != lower && compatKnownField(lower) {
					compatIssue(out, "ambiguous_json_fields", "ambiguity", "review", path, "decoder_field_precedence", "")
				}
				child := path
				if compatKnownField(lower) {
					child += "/" + lower
				}
				if !walk(depth+1, child) {
					return false
				}
			}
		case '[':
			i := 0
			for d.More() {
				if !walk(depth+1, path+"/"+strconv.Itoa(i)) {
					return false
				}
				i++
			}
		default:
			return true
		}
		_, _ = d.Token()
		return true
	}
	if !walk(1, "/"+kind) {
		return false
	}
	// Invalid/trailing input is deliberately left to the executable parser.
	_, _ = d.Token()
	return true
}
func compatKnownField(key string) bool {
	switch key {
	case "conditions", "actions", "rules", "values", "value", "attribute_key", "key", "filter_operator", "operator", "query_operator", "action_name", "name", "action_params", "params", "capability_id", "capability_version", "custom_attribute_type", "custom_attribute_definition_id", "attribute_source", "value_type":
		return true
	}
	return false
}
func compatRawRecords(raw, kind string) ([]map[string]any, map[string]any) {
	d := json.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	var value any
	if d.Decode(&value) != nil || d.Decode(new(any)) != io.EOF {
		return nil, nil
	}
	asRecords := func(items []any) []map[string]any {
		out := make([]map[string]any, len(items))
		for i, item := range items {
			out[i], _ = item.(map[string]any)
		}
		return out
	}
	if items, ok := value.([]any); ok {
		return asRecords(items), nil
	}
	if obj, ok := value.(map[string]any); ok {
		for _, field := range []string{kind, "rules", "values"} {
			if items, ok := compatField(obj, field).([]any); ok && len(items) > 0 {
				return asRecords(items), obj
			}
		}
		return []map[string]any{obj}, nil
	}
	return nil, nil
}
func compatField(obj map[string]any, key string) any {
	// Exact keys win this diagnostic lookup; case collisions are separately
	// surfaced and execution assessment still calls the actual Go decoder.
	if value, ok := obj[key]; ok {
		return value
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.EqualFold(k, key) {
			return obj[k]
		}
	}
	return nil
}
func compatHasMetadata(raw, key string) bool {
	records, _ := compatRawRecords(raw, "conditions")
	for _, record := range records {
		for k := range record {
			if strings.EqualFold(k, key) {
				return true
			}
		}
	}
	return false
}
func compatEnvelopeIssues(raw, kind string, out *domain.AutomationCompatibilitySource) []map[string]any {
	return compatEnvelopeIssuesWithNumeric(raw, kind, out, nil)
}
func compatEnvelopeIssuesWithNumeric(raw, kind string, out *domain.AutomationCompatibilitySource, numeric map[int]domain.AutomationNumericCondition) []map[string]any {
	records, wrapper := compatRawRecords(raw, kind)
	nonempty := 0
	for key := range wrapper {
		lower := strings.ToLower(key)
		if lower != kind && lower != "rules" && lower != "values" {
			compatIssue(out, "ignored_definition_metadata", "ambiguity", "review", "/"+kind, "ignored_metadata", "")
			break
		}
	}
	for _, field := range []string{kind, "rules", "values"} {
		if list, ok := compatField(wrapper, field).([]any); ok && len(list) > 0 {
			nonempty++
		}
	}
	if nonempty > 1 {
		issue := compatIssue(out, "multiple_definition_wrappers", "ambiguity", "review", "/"+kind, "legacy_precedence", "")
		issue.Details.Count = nonempty
	}
	for i, obj := range records {
		root := fmt.Sprintf("/%s/%d", kind, i)
		known := map[string]bool{}
		pairs := [][2]string{}
		if kind == "conditions" {
			pairs = [][2]string{{"attribute_key", "key"}, {"filter_operator", "operator"}}
			for _, k := range []string{"attribute_key", "key", "filter_operator", "operator", "query_operator", "values", "value"} {
				known[k] = true
			}
			if _, adopted := numeric[i]; adopted {
				known["custom_attribute_type"] = true
				known["custom_attribute_definition_id"] = true
			}
		} else {
			pairs = [][2]string{{"action_name", "name"}, {"action_params", "params"}}
			for _, k := range []string{"action_name", "name", "action_params", "params", "capability_id", "capability_version"} {
				known[k] = true
			}
		}
		for _, pair := range pairs {
			a, b := compatField(obj, pair[0]), compatField(obj, pair[1])
			if a != nil && b != nil && a != "" && b != "" && !reflect.DeepEqual(a, b) {
				code := "conflicting_condition_alias"
				if kind == "actions" {
					code = "conflicting_action_alias"
				}
				compatIssue(out, code, "ambiguity", "review", root+"/"+pair[0], "legacy_precedence", "")
			}
		}
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		ignored := false
		for _, k := range keys {
			lower := strings.ToLower(k)
			if known[lower] {
				continue
			}
			if kind == "conditions" && (lower == "conditions" || lower == "rules" || lower == "query") {
				compatIssue(out, "unsupported_condition_shape", "unsupported", "review", root, "ignored_metadata", "")
			}
			if kind == "conditions" && lower == "custom_attribute_type" {
				compatIssue(out, "typed_custom_scope_ignored", "behavior_change", "review", root+"/custom_attribute_type", "literal_conversation_custom_lookup", "")
			}
			ignored = true
		}
		if ignored {
			compatIssue(out, "ignored_definition_metadata", "ambiguity", "review", root, "ignored_metadata", "")
		}
	}
	return records
}

type compatConditionMeaning struct {
	Key, Operator, Connector string
	Values                   []any
}

func compatDefinitionsEqual(ae, ac, aa, as, be, bc, ba, bs string) bool {
	if ae != be || as != bs {
		return false
	}
	// Numeric operands must not pass through the legacy float64 decoder for
	// drift comparison. Compare exact values and bound metadata internally; no
	// custom identity, threshold, or hash is included in the report.
	if as == domain.AutomationNumericCustomV1 {
		a, ae := domain.AutomationNumericConditions(ac)
		b, be := domain.AutomationNumericConditions(bc)
		if (ae == nil) != (be == nil) || len(a) != len(b) {
			return false
		}
		if ae == nil {
			for i, x := range a {
				y := b[i]
				if x.Index != y.Index || x.AttributeKey != y.AttributeKey || x.CustomAttributeType != y.CustomAttributeType || x.DefinitionID != y.DefinitionID || x.FilterOperator != y.FilterOperator {
					return false
				}
				if cmp, valid := domain.CompareAutomationNumbers(x.Threshold, y.Threshold); !valid || cmp != 0 {
					return false
				}
			}
		} else if ac != bc {
			return false
		}
	}
	normalizeConditions := func(raw string) any {
		c, _, ok := parseAutomationProfileConditions(raw, as)
		if !ok {
			var v any
			if json.Unmarshal([]byte(raw), &v) == nil {
				return v
			}
			return raw
		}
		out := make([]compatConditionMeaning, len(c))
		for i, v := range c {
			connector := "AND"
			if i == len(c)-1 {
				connector = ""
			} else if strings.ToUpper(v.QueryOperator) == "OR" {
				connector = "OR"
			}
			key, operator := v.AttributeKey, v.FilterOperator
			if compatLabelKey(key) {
				key = "labels"
			}
			switch operator {
			case "is":
				operator = "equal_to"
			case "is_not":
				operator = "not_equal_to"
			case "includes":
				operator = "contains"
			}
			values := v.Values
			if len(values) == 0 {
				values = nil
			}
			out[i] = compatConditionMeaning{key, operator, connector, values}
		}
		return out
	}
	if !reflect.DeepEqual(normalizeConditions(ac), normalizeConditions(bc)) {
		return false
	}
	ap, ar := compileAutomationContinuingActions(aa)
	bp, br := compileAutomationContinuingActions(ba)
	if ar != br || !reflect.DeepEqual(ap, bp) {
		return false
	}
	// On rejected actions, a validation placeholder does not identify the actual
	// definition. Retain internal comparison, never return its values or hashes.
	failed := ar != ""
	for _, v := range ap {
		failed = failed || v.FailureReason != ""
	}
	if failed {
		a, ao := parseAutomationActionDefinitions(aa)
		b, bo := parseAutomationActionDefinitions(ba)
		if ao != bo || !reflect.DeepEqual(a, b) {
			return false
		}
	}
	for _, pair := range [][3]string{{ac, bc, "conditions"}, {aa, ba, "actions"}} {
		if !reflect.DeepEqual(compatUnknownMetadata(pair[0], pair[2]), compatUnknownMetadata(pair[1], pair[2])) {
			return false
		}
	}
	return true
}
func compatUnknownMetadata(raw, kind string) []map[string]any {
	records, wrapper := compatRawRecords(raw, kind)
	out := make([]map[string]any, len(records))
	known := map[string]bool{}
	fields := []string{"attribute_key", "key", "filter_operator", "operator", "query_operator", "values", "value"}
	if kind == "actions" {
		fields = []string{"action_name", "name", "action_params", "params", "capability_id", "capability_version"}
	}
	for _, key := range fields {
		known[key] = true
	}
	for i, obj := range records {
		for k, v := range obj {
			if !known[strings.ToLower(k)] {
				if out[i] == nil {
					out[i] = map[string]any{}
				}
				out[i][k] = v
			}
		}
	}
	ignoredWrapper := map[string]any{}
	selected := ""
	for _, field := range []string{kind, "rules", "values"} {
		if values, ok := compatField(wrapper, field).([]any); ok && len(values) > 0 {
			selected = field
			break
		}
	}
	for key, value := range wrapper {
		if !strings.EqualFold(key, selected) {
			ignoredWrapper[key] = value
		}
	}
	if len(ignoredWrapper) > 0 {
		out = append(out, ignoredWrapper)
	}
	for len(out) > 0 && out[len(out)-1] == nil {
		out = out[:len(out)-1]
	}
	return out
}
