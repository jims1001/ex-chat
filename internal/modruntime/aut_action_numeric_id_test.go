package modruntime

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"testing"
)

func TestAutomationNumericIDExactForms(t *testing.T) {
	for _, test := range []struct {
		token string
		want  uint64
	}{
		{"1", 1}, {"7.000", 7}, {"1e3", 1000}, {"1E+0003", 1000},
		{"1000e-3", 1}, {"0.00010e4", 1}, {"123.4500e2", 12345},
		{"4294967295", 4294967295}, {"4294967296", 4294967296},
		{"9007199254740993", 9007199254740993},
		{"9007199254740993.0000", 9007199254740993},
		{"9.007199254740993e15", 9007199254740993},
		{"18446744073709551615", 18446744073709551615},
		{"18446744073709551615.0", 18446744073709551615},
		{"184467440737095516150e-1", 18446744073709551615},
		{"1.8446744073709551615E+19", 18446744073709551615},
		{"1e-0000", 1},
	} {
		t.Run(test.token, func(t *testing.T) {
			got, ok := parseAutomationNumberUint64(json.Number(test.token))
			if !ok || got != test.want {
				t.Fatalf("parseAutomationNumberUint64(%q) = %d, %v; want %d", test.token, got, ok, test.want)
			}
			wantID := uint(test.want)
			if test.want > uint64(^uint(0)) {
				wantID = 0
			}
			if gotID := automationIDParam(json.Number(test.token)); gotID != wantID {
				t.Fatalf("automationIDParam(%q) = %d; want %d on %d-bit host", test.token, gotID, wantID, strconv.IntSize)
			}
		})
	}
}

func TestAutomationNumericIDRejectsInvalidValues(t *testing.T) {
	for _, token := range []string{
		"", "0", "0.000", "0e20", "-0", "-1", "-1e2", "1.5", "1e-1",
		"0.9999999999999999999999999999", "1.0000000000000000000000000001",
		"18446744073709551616", "18446744073709551615.1", "1.8446744073709551616e19", "1e20",
		"+1", "01", "01.0", ".1", "1.", "1e", "1e+", "1e-", "1e1e1", "1.e1",
		"1/1", "0x10", "0b1", "1_000", "NaN", "Inf", " 1", "1 ", "１", "1\x00",
	} {
		t.Run(token, func(t *testing.T) {
			if got, ok := parseAutomationNumberUint64(json.Number(token)); ok || got != 0 {
				t.Fatalf("invalid numeric ID %q returned %d, %v", token, got, ok)
			}
			if got := automationIDParam(json.Number(token)); got != 0 {
				t.Fatalf("automationIDParam accepted invalid numeric ID %q as %d", token, got)
			}
		})
	}
}

func TestAutomationNumericIDBoundedEncodings(t *testing.T) {
	// The first two inputs are mathematically one at the exact byte boundary.
	// Their one-byte-larger counterparts intentionally exceed compatibility.
	for _, token := range []string{
		"1." + strings.Repeat("0", automationIDNumberMaxBytes-2),
		"1e+" + strings.Repeat("0", automationIDNumberMaxBytes-4) + "0",
		"1" + strings.Repeat("0", 999) + "e-999",
		"0." + strings.Repeat("0", 999) + "1e1000",
	} {
		if got, ok := parseAutomationNumberUint64(json.Number(token)); !ok || got != 1 {
			t.Fatalf("rejected valid bounded encoding (%d bytes): %d, %v", len(token), got, ok)
		}
	}
	for _, token := range []string{
		"1." + strings.Repeat("0", automationIDNumberMaxBytes-1),
		"1e+" + strings.Repeat("0", automationIDNumberMaxBytes-3) + "0",
		"1e1024", "1e-1024", "1e1025", "1e-1025",
		"1e2147483647", "1e-2147483648", "1e9223372036854775807", "1e-9223372036854775808",
		"1e" + strings.Repeat("9", 1000), "1e-" + strings.Repeat("9", 1000),
		"0e9999999999999999999999999999999999999999",
		strings.Repeat("9", 1<<20),
	} {
		if got, ok := parseAutomationNumberUint64(json.Number(token)); ok {
			t.Fatalf("accepted rejected encoding (%d bytes) as %d", len(token), got)
		}
	}
}

func TestAutomationNumericIDRejectsOversizedInputWithoutAllocation(t *testing.T) {
	for name, token := range map[string]json.Number{
		"positive_exponent": "1e2147483647",
		"negative_exponent": "1e-2147483648",
		"exponent_digits":   json.Number("1e" + strings.Repeat("9", 1000)),
		"token_bytes":       json.Number(strings.Repeat("9", 1<<20)),
	} {
		t.Run(name, func(t *testing.T) {
			allocations := testing.AllocsPerRun(100, func() {
				if _, ok := parseAutomationNumberUint64(token); ok {
					t.Fatal("accepted oversized numeric ID")
				}
			})
			if allocations != 0 {
				t.Fatalf("oversized numeric ID allocated before rejection: %.1f allocations", allocations)
			}
		})
	}
}

func TestAutomationNumericIDMatchesSmallExactOracle(t *testing.T) {
	// Only small, bounded test inputs reach the arbitrary-precision oracle.
	// Adversarial large exponents above are never parsed with big.Rat.
	for coefficient := 0; coefficient <= 100; coefficient++ {
		for fractional := 0; fractional <= 3; fractional++ {
			for exponent := -4; exponent <= 4; exponent++ {
				digits := strconv.Itoa(coefficient)
				if fractional > 0 {
					if len(digits) <= fractional {
						digits = strings.Repeat("0", fractional+1-len(digits)) + digits
					}
					digits = digits[:len(digits)-fractional] + "." + digits[len(digits)-fractional:]
				}
				token := fmt.Sprintf("%se%d", digits, exponent)
				rat, valid := new(big.Rat).SetString(token)
				if !valid {
					t.Fatalf("invalid oracle test input %q", token)
				}
				wantOK := rat.IsInt() && rat.Sign() > 0 && rat.Num().BitLen() <= 64
				got, ok := parseAutomationNumberUint64(json.Number(token))
				if ok != wantOK || (ok && got != rat.Num().Uint64()) {
					t.Fatalf("numeric ID %q = %d, %v; exact oracle %s (valid %v)", token, got, ok, rat, wantOK)
				}
			}
		}
	}
}

func TestAutomationNumericIDCompilePaths(t *testing.T) {
	for _, action := range []string{"assign_agent", "assign_team", "add_label", "remove_label"} {
		for _, token := range []string{"1e2147483647", "1e-2147483648", "1e9999999999999999999999", "1.5", "18446744073709551616"} {
			raw := `[{"action_name":"` + action + `","action_params":[` + token + `]}]`
			if actions, reason := compileAutomationActions(raw); reason != "invalid_action_parameters" || len(actions) != 0 {
				t.Fatalf("%s accepted %s: actions=%+v reason=%q", action, token, actions, reason)
			}
		}
		for _, token := range []string{"12.000", "1.2e1"} {
			raw := `[{"action_name":"` + action + `","action_params":[` + token + `]}]`
			actions, reason := compileAutomationActions(raw)
			if reason != "" || len(actions) == 0 || actions[0].Value != "12" {
				t.Fatalf("%s rejected exact %s: actions=%+v reason=%q", action, token, actions, reason)
			}
		}
	}
}
