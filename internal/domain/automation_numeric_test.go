package domain

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
	"testing"
)

func TestAutomationNumericExactComparison(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"9007199254740993", "9007199254740992", 1}, {"0.1000000000000000000000000001", "0.1", 1}, {"-0.1000000000000000000000000001", "-0.1", -1}, {"1e1024", "9e1023", 1}, {"1e-1024", "0", 1}, {"-0e1024", "0", 0}, {"100.000e-2", "1", 0}, {"1.2", "1.2001", -1}, {"-12", "-1.2e1", 0}, {"-9e999", "-1e1000", 1},
	}
	for _, tc := range cases {
		got, ok := CompareAutomationNumbers(json.Number(tc.a), json.Number(tc.b))
		if !ok || got != tc.want {
			t.Fatalf("%s versus %s = %d/%v", tc.a, tc.b, got, ok)
		}
	}
	for _, bad := range []string{"", "+1", "01", "1.", ".1", "1e", "1e1025", "1e-1025", "1e9999999999999999999", "NaN", "Infinity", "true", "\"1\"", "[]", "{}", " 1", "1 ", strings.Repeat("1", 1025)} {
		if _, ok := CompareAutomationNumbers(json.Number(bad), "0"); ok {
			t.Fatalf("accepted %q", bad)
		}
	}
	valid := "1." + strings.Repeat("0", 1022)
	if _, ok := CompareAutomationNumbers(json.Number(valid), "1"); !ok {
		t.Fatal("1024-byte literal rejected")
	}
	for a := -20; a <= 20; a++ {
		for b := -20; b <= 20; b++ {
			for e := -4; e <= 4; e++ {
				sa, sb := fmt.Sprintf("%de%d", a, e), fmt.Sprintf("%d.000", b)
				x, _ := new(big.Rat).SetString(sa)
				y, _ := new(big.Rat).SetString(sb)
				got, ok := CompareAutomationNumbers(json.Number(sa), json.Number(sb))
				if !ok || got != x.Cmp(y) {
					t.Fatalf("oracle %s %s %d", sa, sb, got)
				}
			}
		}
	}
	oversized := json.Number("1e" + strings.Repeat("9", 1<<20))
	if got := testing.AllocsPerRun(100, func() { CompareAutomationNumbers(oversized, "1") }); got != 0 {
		t.Fatalf("oversized allocation %v", got)
	}
}
func TestAutomationNumericProfileValidation(t *testing.T) {
	good := `[{"attribute_key":"score","custom_attribute_type":"contact_attribute","custom_attribute_definition_id":1,"filter_operator":"is_greater_than","values":[9007199254740993]}]`
	if err := ValidateAutomationConditionSemantics(AutomationNumericCustomV1, good); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{strings.Replace(good, `[9007199254740993]`, `[1,2]`, 1), strings.Replace(good, `[9007199254740993]`, `["1"]`, 1), strings.Replace(good, `[9007199254740993]`, `[true]`, 1), strings.Replace(good, `[9007199254740993]`, `[null]`, 1), strings.Replace(good, `"contact_attribute"`, `"currency"`, 1), strings.Replace(good, `"custom_attribute_definition_id":1`, `"custom_attribute_definition_id":1e0`, 1), strings.Replace(good, `"custom_attribute_definition_id":1`, `"custom_attribute_definition_id":0`, 1), "null", `{"conditions":[]}`, `[] []`, "[" + strings.Repeat(good[1:len(good)-1]+",", 100) + good[1:len(good)-1] + "]"} {
		if ValidateAutomationConditionSemantics(AutomationNumericCustomV1, raw) == nil {
			t.Fatalf("accepted invalid profile definition %s", raw)
		}
	}
	if ValidateAutomationConditionSemantics("unknown", good) == nil {
		t.Fatal("unknown profile")
	}
	if ValidateAutomationConditionSemantics("", `invalid legacy payload`) != nil {
		t.Fatal("legacy validation changed")
	}
}
