package domain

import (
	"encoding/json"
	"strconv"
	"strings"
)

const AutomationIDNumberMaxBytes = 1024
const AutomationIDNumberMaxExponent = 1024

// ParseAutomationNumberUint64 accepts positive, integral JSON numbers exactly,
// including decimal and exponent forms, without float or arbitrary-precision
// arithmetic. Numeric ID tokens are limited to 1,024 bytes and an absolute
// exponent of 1,024. These are deliberate compatibility limits: excessively
// padded encodings are rejected even when their mathematical value is a valid
// ID. Work is bounded before scanning or allocating from an untrusted token.
func ParseAutomationNumberUint64(number json.Number) (uint64, bool) {
	s := string(number)
	if len(s) == 0 || len(s) > AutomationIDNumberMaxBytes {
		return 0, false
	}
	// A negative sign cannot describe a positive ID. Reject non-JSON forms
	// (leading '+', leading integer zeroes, fractions, hex, and whitespace).
	i := 0
	if s[0] == '0' {
		i = 1
	} else if s[0] >= '1' && s[0] <= '9' {
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	} else {
		return 0, false
	}
	integerEnd := i
	fractionDigits := 0
	if i < len(s) && s[i] == '.' {
		i++
		fractionStart := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		fractionDigits = i - fractionStart
		if fractionDigits == 0 {
			return 0, false
		}
	}
	mantissaEnd := i
	exponent := 0
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		negative := false
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			negative = s[i] == '-'
			i++
		}
		exponentStart := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			digit := int(s[i] - '0')
			if exponent > (AutomationIDNumberMaxExponent-digit)/10 {
				return 0, false
			}
			exponent = exponent*10 + digit
			i++
		}
		if i == exponentStart {
			return 0, false
		}
		if negative {
			exponent = -exponent
		}
	}
	if i != len(s) {
		return 0, false
	}

	digits := s[:integerEnd]
	if fractionDigits > 0 {
		digits += s[integerEnd+1 : mantissaEnd]
	}
	digits = strings.TrimLeft(digits, "0")
	if digits == "" {
		return 0, false
	}
	trimmed := strings.TrimRight(digits, "0")
	// Discarding trailing zeroes adjusts the decimal scale without constructing
	// a power of ten. A negative resulting scale is necessarily non-integral.
	scale := exponent - fractionDigits + len(digits) - len(trimmed)
	if scale < 0 || len(trimmed)+scale > 20 {
		return 0, false
	}
	normalized := trimmed + strings.Repeat("0", scale)
	parsed, err := strconv.ParseUint(normalized, 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}
