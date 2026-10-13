package handler

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

var errReportCountOffsetInvalid = errors.New("timezone offset must be a finite decimal number")
var errReportCountOffsetUnsupported = errors.New("timezone offset must be exactly representable as whole seconds within the supported arithmetic range")
var reportCountOffsetDecimal = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

// parseReportCountOffset converts decimal hours exactly. Floating point is
// used only to classify rejected values, never to decide acceptance or round.
// Linear zero normalization and bounded significant-digit arithmetic avoid
// huge allocations, including decimals that underflow floating point to zero.
func parseReportCountOffset(value string) (int64, error) {
	if value == "" {
		return 0, nil
	}
	if !reportCountOffsetDecimal.MatchString(value) {
		return 0, errReportCountOffsetInvalid
	}
	raw := value
	negative := value[0] == '-'
	value = strings.TrimPrefix(strings.TrimPrefix(value, "+"), "-")
	significand, exponentText := value, "0"
	if i := strings.IndexAny(value, "eE"); i >= 0 {
		significand, exponentText = value[:i], value[i+1:]
	}
	fractionalDigits := 0
	if i := strings.IndexByte(significand, '.'); i >= 0 {
		fractionalDigits = len(significand) - i - 1
		significand = significand[:i] + significand[i+1:]
	}
	significand = strings.TrimLeft(significand, "0")
	if significand == "" {
		return 0, nil
	}
	exponent, err := strconv.ParseInt(exponentText, 10, 64)
	if err != nil {
		return 0, reportCountOffsetRepresentationError(raw)
	}
	// Strip zeroes before any big integer construction. Multiplication by 36
	// can remove at most two further trailing zeroes, so more than eleven
	// significant digits cannot yield an in-range ten-digit integral result.
	trimmedSignificand := strings.TrimRight(significand, "0")
	trailingZeros := len(significand) - len(trimmedSignificand)
	significand = trimmedSignificand
	if len(significand) > 11 {
		return 0, reportCountOffsetRepresentationError(raw)
	}
	// Only a tiny effective decimal power can produce a supported integer.
	// Compare before subtracting, avoiding overflow for extreme exponent text.
	needed := int64(fractionalDigits) - int64(trailingZeros) - 2
	if exponent < needed-2 || exponent > needed+10 {
		return 0, reportCountOffsetRepresentationError(raw)
	}
	// hours * 3600 = significand * 36 * 10^(exponent-needed).
	coefficient, err := strconv.ParseInt(significand, 10, 64)
	if err != nil {
		return 0, errReportCountOffsetInvalid
	}
	// At most eleven digits times 36 is safely inside int64.
	digits := strconv.FormatInt(coefficient*36, 10)
	power := exponent - needed
	trimmed := strings.TrimRight(digits, "0")
	power += int64(len(digits) - len(trimmed))
	digits = trimmed
	if power < 0 || power > 10 || int64(len(digits))+power > 10 {
		return 0, reportCountOffsetRepresentationError(raw)
	}
	seconds, err := strconv.ParseInt(digits+strings.Repeat("0", int(power)), 10, 64)
	if err != nil || seconds > domain.ReportCountMaxOffsetSeconds {
		return 0, reportCountOffsetRepresentationError(raw)
	}
	if negative {
		seconds = -seconds
	}
	return seconds, nil
}

// Retain the preexisting 400 classification for values outside finite float64
// range. A supported exact decimal never passes through this approximation.
func reportCountOffsetRepresentationError(raw string) error {
	approximate, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(approximate) || math.IsInf(approximate, 0) {
		return errReportCountOffsetInvalid
	}
	return errReportCountOffsetUnsupported
}
