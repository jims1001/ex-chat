package domain

import (
	"math"
	"time"
)

// ReportCountGrouping is the closed set of calendar groupings for count owners.
// It is separate from legacy report filters and from arbitrary query strings.
type ReportCountGrouping string

const (
	ReportCountHour  ReportCountGrouping = "hour"
	ReportCountDay   ReportCountGrouping = "day"
	ReportCountMonth ReportCountGrouping = "month"
	ReportCountYear  ReportCountGrouping = "year"
)

// ReportCountBucket is one fixed-offset calendar bucket clipped to the absolute interval.
// Timestamp labels its natural start; Since and Until preserve exact endpoints.
type ReportCountBucket struct {
	Timestamp     int64
	OffsetSeconds int64
	Since         time.Time
	Until         time.Time
}

// ReportCountPoint exposes only the aggregate value and UTC bucket label.
type ReportCountPoint struct {
	Value     int64 `json:"value"`
	Timestamp int64 `json:"timestamp"`
}

// ReportCountMaxOffsetSeconds is an arithmetic representation limit, not a
// geographic timezone policy. Whole-second offsets must fit time.Duration.
const ReportCountMaxOffsetSeconds int64 = math.MaxInt64 / int64(time.Second)

// ReportCountOffsetInstantValid checks the timestamp and fixed-offset shift
// without requiring a following calendar bucket (important for exclusive ends).
func ReportCountOffsetInstantValid(at time.Time, offsetSeconds int64) bool {
	if offsetSeconds < -ReportCountMaxOffsetSeconds || offsetSeconds > ReportCountMaxOffsetSeconds {
		return false
	}
	_, ok := reportCountShift(at, time.Duration(offsetSeconds)*time.Second)
	return ok
}

// ReportCountCalendarBounds returns the absolute UTC boundaries of the local
// calendar bucket containing at. The fixed offset has no daylight-saving rules.
// Both RPT and the owner use this definition; all SQL bounds remain absolute UTC.
func ReportCountCalendarBounds(at time.Time, grouping ReportCountGrouping, offsetSeconds int64) (time.Time, time.Time, bool) {
	if offsetSeconds < -ReportCountMaxOffsetSeconds || offsetSeconds > ReportCountMaxOffsetSeconds {
		return time.Time{}, time.Time{}, false
	}
	offset := time.Duration(offsetSeconds) * time.Second
	local, ok := reportCountShift(at, offset)
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	var next time.Time
	switch grouping {
	case ReportCountHour:
		start = time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, time.UTC)
		next = start.Add(time.Hour)
	case ReportCountDay:
		next = start.AddDate(0, 0, 1)
	case ReportCountMonth:
		start = time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, time.UTC)
		next = start.AddDate(0, 1, 0)
	case ReportCountYear:
		start = time.Date(local.Year(), time.January, 1, 0, 0, 0, 0, time.UTC)
		next = start.AddDate(1, 0, 0)
	default:
		return time.Time{}, time.Time{}, false
	}
	if !next.After(start) || local.Before(start) || !local.Before(next) {
		return time.Time{}, time.Time{}, false
	}
	absoluteStart, startOK := reportCountShift(start, -offset)
	absoluteEnd, endOK := reportCountShift(next, -offset)
	if !startOK || !endOK || !absoluteEnd.After(absoluteStart) || at.Before(absoluteStart) || !at.Before(absoluteEnd) {
		return time.Time{}, time.Time{}, false
	}
	return absoluteStart, absoluteEnd, true
}

func reportCountShift(at time.Time, offset time.Duration) (time.Time, bool) {
	at = at.UTC()
	seconds := at.Unix()
	delta := int64(offset / time.Second)
	if (delta > 0 && seconds > math.MaxInt64-delta) || (delta < 0 && seconds < math.MinInt64-delta) {
		return time.Time{}, false
	}
	// Invalid Unix extremes may wrap Time's internal epoch even when Unix()
	// returns the input again. Require chronology to agree with the Unix epoch.
	epoch := time.Unix(0, 0).UTC()
	if (seconds >= 0 && at.Before(epoch)) || (seconds < 0 && !at.Before(epoch)) {
		return time.Time{}, false
	}
	shifted := at.Add(offset)
	if shifted.Unix() != seconds+delta || shifted.Nanosecond() != at.Nanosecond() {
		return time.Time{}, false
	}
	// Add may saturate at Time's internal boundary; Unix conversion may wrap.
	// Verify both directions rather than letting those cases alter bucket labels.
	if !shifted.Add(-offset).Equal(at) || !time.Unix(at.Unix(), int64(at.Nanosecond())).UTC().Equal(at) || !time.Unix(shifted.Unix(), int64(shifted.Nanosecond())).UTC().Equal(shifted) {
		return time.Time{}, false
	}
	return shifted, true
}
