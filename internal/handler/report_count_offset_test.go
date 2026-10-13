package handler

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReportCountOffsetExactDecimal(t *testing.T) {
	for _, tc := range []struct {
		text    string
		seconds int64
	}{
		{"", 0}, {"0", 0}, {"-0", 0}, {"+0.0", 0}, {"0e-99999999999999999999999", 0},
		{"5.5", 19800}, {"5.75", 20700}, {"-3.5", -12600}, {"+8", 28800}, {".1", 360}, {"-0.0025", -9}, {"2.5e1", 90000},
		{"2562047.7875", 9223372035}, {"-2562047.7875", -9223372035},
		{"0." + strings.Repeat("0", 1000), 0}, {"8." + strings.Repeat("0", 1000), 28800},
		{"8" + strings.Repeat("0", 100000) + "e-100000", 28800},
	} {
		t.Run(tc.text[:min(len(tc.text), 70)], func(t *testing.T) {
			got, err := parseReportCountOffset(tc.text)
			require.NoError(t, err)
			require.Equal(t, tc.seconds, got)
		})
	}
}

func TestReportCountOffsetRejectsWithoutRounding(t *testing.T) {
	for _, text := range []string{"8.0000000000000001", "0.10000000000000000001", "0.00000000000000000000001", "1e-99999999999999", "1e-400", ".0001", "-.0001", "2562047.79", "-2562047.79", "1e100", "1234567890123456789.123456789"} {
		t.Run(text, func(t *testing.T) {
			got, err := parseReportCountOffset(text)
			require.Zero(t, got)
			require.True(t, errors.Is(err, errReportCountOffsetUnsupported), "%v", err)
		})
	}
	for _, text := range []string{"NaN", "Inf", "-Inf", "1e309", "bad", " 0", "0 ", "1/2", "0x0p0", "0_0", "1e", "" + strings.Repeat("9", 1000)} {
		t.Run(text[:min(len(text), 70)], func(t *testing.T) {
			got, err := parseReportCountOffset(text)
			require.Zero(t, got)
			require.ErrorIs(t, err, errReportCountOffsetInvalid)
		})
	}
}
