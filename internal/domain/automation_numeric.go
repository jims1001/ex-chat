package domain

import (
	"encoding/json"
	"errors"
	"strings"
)

// Adoption is per immutable rule version, never a process-wide switch.
const AutomationNumericCustomV1 = "numeric_custom.v1"
const AutomationNumericMaxLiteralBytes = 1024
const AutomationNumericMaxExponent = 1024
const AutomationNumericMaxConditions = 100
const AutomationNumericMaxDefinitionBytes = 1 << 20

var ErrAutomationConditionSemantics = errors.New("invalid automation condition semantics or numeric definition")

type AutomationNumericCondition struct {
	Index               int
	AttributeKey        string
	CustomAttributeType string
	DefinitionID        uint
	FilterOperator      string
	Threshold           json.Number
}

// AutomationConditionRecords retains the legacy wrapper/alias selection while
// bounding the new profile. Raw records let nonnumeric leaves retain the legacy
// float/string semantics rather than silently switching their decoder.
func AutomationConditionRecords(raw string) ([]json.RawMessage, error) {
	if len(raw) > AutomationNumericMaxDefinitionBytes {
		return nil, ErrAutomationConditionSemantics
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" || raw == "{}" {
		return nil, nil
	}
	var records []json.RawMessage
	if json.Unmarshal([]byte(raw), &records) != nil || len(records) == 0 {
		var wrapper struct {
			Conditions []json.RawMessage `json:"conditions"`
			Rules      []json.RawMessage `json:"rules"`
			Values     []json.RawMessage `json:"values"`
		}
		if json.Unmarshal([]byte(raw), &wrapper) == nil {
			switch {
			case len(wrapper.Conditions) > 0:
				records = wrapper.Conditions
			case len(wrapper.Rules) > 0:
				records = wrapper.Rules
			case len(wrapper.Values) > 0:
				records = wrapper.Values
			}
		}
		if len(records) == 0 {
			var single struct {
				AttributeKey string `json:"attribute_key"`
				Key          string `json:"key"`
			}
			if json.Unmarshal([]byte(raw), &single) != nil || (single.AttributeKey == "" && single.Key == "") {
				return nil, ErrAutomationConditionSemantics
			}
			records = []json.RawMessage{json.RawMessage(raw)}
		}
	}
	if len(records) > AutomationNumericMaxConditions {
		return nil, ErrAutomationConditionSemantics
	}
	return records, nil
}

func AutomationNumericConditions(raw string) ([]AutomationNumericCondition, error) {
	records, err := AutomationConditionRecords(raw)
	if err != nil {
		return nil, err
	}
	result := make([]AutomationNumericCondition, 0)
	for i, record := range records {
		var leaf struct {
			AttributeKey        string            `json:"attribute_key"`
			Key                 string            `json:"key"`
			FilterOperator      string            `json:"filter_operator"`
			Operator            string            `json:"operator"`
			CustomAttributeType string            `json:"custom_attribute_type"`
			DefinitionID        json.RawMessage   `json:"custom_attribute_definition_id"`
			Values              []json.RawMessage `json:"values"`
			Value               json.RawMessage   `json:"value"`
		}
		// Do not validate ignored typed metadata on nonnumeric leaves.
		var header struct {
			AttributeKey   string `json:"attribute_key"`
			Key            string `json:"key"`
			FilterOperator string `json:"filter_operator"`
			Operator       string `json:"operator"`
		}
		if json.Unmarshal(record, &header) != nil {
			return nil, ErrAutomationConditionSemantics
		}
		op := header.FilterOperator
		if op == "" {
			op = header.Operator
		}
		if op != "is_greater_than" && op != "is_less_than" {
			continue
		}
		if json.Unmarshal(record, &leaf) != nil {
			return nil, ErrAutomationConditionSemantics
		}
		key := leaf.AttributeKey
		if key == "" {
			key = leaf.Key
		}
		if key == "" || len(key) > 100 || (leaf.CustomAttributeType != "conversation_attribute" && leaf.CustomAttributeType != "contact_attribute") {
			return nil, ErrAutomationConditionSemantics
		}
		var id uint
		if len(leaf.DefinitionID) > 20 || json.Unmarshal(leaf.DefinitionID, &id) != nil || id == 0 || uint64(id) > uint64(1<<63-1) {
			return nil, ErrAutomationConditionSemantics
		}
		values := leaf.Values
		if len(values) == 0 && len(leaf.Value) > 0 && string(leaf.Value) != "null" {
			values = []json.RawMessage{leaf.Value}
		}
		if len(values) != 1 {
			return nil, ErrAutomationConditionSemantics
		}
		number := json.Number(strings.TrimSpace(string(values[0])))
		if _, ok := parseAutomationDecimal(number); !ok {
			return nil, ErrAutomationConditionSemantics
		}
		result = append(result, AutomationNumericCondition{Index: i, AttributeKey: key, CustomAttributeType: leaf.CustomAttributeType, DefinitionID: id, FilterOperator: op, Threshold: number})
	}
	return result, nil
}

func ValidateAutomationConditionSemantics(profile, raw string) error {
	if profile == "" {
		return nil
	}
	if profile != AutomationNumericCustomV1 {
		return ErrAutomationConditionSemantics
	}
	_, err := AutomationNumericConditions(raw)
	return err
}

type automationDecimal struct {
	sign   int
	digits string
	power  int
}

func parseAutomationDecimal(number json.Number) (automationDecimal, bool) {
	s := string(number)
	bad := automationDecimal{}
	if len(s) == 0 || len(s) > AutomationNumericMaxLiteralBytes {
		return bad, false
	}
	i, sign := 0, 1
	if s[0] == '-' {
		sign = -1
		i++
	}
	if i == len(s) {
		return bad, false
	}
	start := i
	if s[i] == '0' {
		i++
	} else {
		if s[i] < '1' || s[i] > '9' {
			return bad, false
		}
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	digits := s[start:i]
	fraction := 0
	if i < len(s) && s[i] == '.' {
		i++
		start = i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == start {
			return bad, false
		}
		fraction = i - start
		digits += s[start:i]
	}
	exponent := 0
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		es := 1
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			if s[i] == '-' {
				es = -1
			}
			i++
		}
		start = i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			exponent = exponent*10 + int(s[i]-'0')
			if exponent > AutomationNumericMaxExponent {
				return bad, false
			}
			i++
		}
		if i == start {
			return bad, false
		}
		exponent *= es
	}
	if i != len(s) {
		return bad, false
	}
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		return automationDecimal{}, true
	}
	power := exponent - fraction
	trimmed := strings.TrimRight(digits, "0")
	power += len(digits) - len(trimmed)
	return automationDecimal{sign: sign, digits: trimmed, power: power}, true
}

// CompareAutomationNumbers uses O(literal bytes) memory/time, never expands a
// decimal power, and never converts an operand to binary floating point.
func CompareAutomationNumbers(a, b json.Number) (int, bool) {
	x, ok := parseAutomationDecimal(a)
	if !ok {
		return 0, false
	}
	y, ok := parseAutomationDecimal(b)
	if !ok {
		return 0, false
	}
	if x.sign != y.sign {
		if x.sign < y.sign {
			return -1, true
		}
		return 1, true
	}
	if x.sign == 0 {
		return 0, true
	}
	xm, ym := len(x.digits)+x.power, len(y.digits)+y.power
	cmp := 0
	if xm < ym {
		cmp = -1
	} else if xm > ym {
		cmp = 1
	} else {
		n := len(x.digits)
		if len(y.digits) > n {
			n = len(y.digits)
		}
		for i := 0; i < n; i++ {
			xc, yc := byte('0'), byte('0')
			if i < len(x.digits) {
				xc = x.digits[i]
			}
			if i < len(y.digits) {
				yc = y.digits[i]
			}
			if xc < yc {
				cmp = -1
				break
			}
			if xc > yc {
				cmp = 1
				break
			}
		}
	}
	return cmp * x.sign, true
}
