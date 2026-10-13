package domain

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestReportCountOffsetInstantArithmetic(t *testing.T) {
	for _, tc := range []struct {
		at     time.Time
		offset int64
	}{
		{time.Unix(math.MaxInt64, 0), 0},
		{time.Unix(math.MinInt64, 0), -1},
		{time.Unix(math.MinInt64+10, 123), -11},
		{time.Unix(math.MaxInt64-10, 123), 11},
		{time.Now(), ReportCountMaxOffsetSeconds + 1},
	} {
		require.False(t, ReportCountOffsetInstantValid(tc.at, tc.offset))
	}
	for _, tc := range []struct {
		at     time.Time
		offset int64
	}{
		{time.Unix(-1, 999999999), 1},
		{time.Unix(0, 0), -1},
		{time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC), 20700},
		{time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), -20700},
	} {
		require.True(t, ReportCountOffsetInstantValid(tc.at, tc.offset))
	}
}
