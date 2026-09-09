package service

import (
	"encoding/json"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

// ParseWorkingHours extracts the location, schedule map, and whether any open days exist for an inbox
func ParseWorkingHours(inbox *domain.Inbox) (*time.Location, map[int]domain.WorkingHourConfig, bool) {
	if inbox == nil || !inbox.WorkingHoursEnabled {
		return time.UTC, nil, false
	}
	tz := inbox.Timezone
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	if inbox.WorkingHours == "" {
		return loc, nil, false
	}

	var rawSchedules []domain.WorkingHourConfig
	if err := json.Unmarshal([]byte(inbox.WorkingHours), &rawSchedules); err != nil {
		return loc, nil, false
	}

	schedules := make(map[int]domain.WorkingHourConfig)
	hasOpenDays := false
	for _, sc := range rawSchedules {
		schedules[sc.DayOfWeek] = sc
		if !sc.IsClosed() {
			if sc.IsOpenAllDay() || sc.OpenHour < sc.CloseHour || (sc.OpenHour == sc.CloseHour && sc.GetOpenMinute() < sc.GetCloseMinute()) {
				hasOpenDays = true
			}
		}
	}

	return loc, schedules, hasOpenDays
}

// DayCloseTimeFor returns the close time on date t in the inbox timezone
func DayCloseTimeFor(t time.Time, sc domain.WorkingHourConfig, loc *time.Location) time.Time {
	if sc.IsOpenAllDay() {
		// Open all 24 hours: close at midnight start of next day
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
	}
	return time.Date(t.Year(), t.Month(), t.Day(), sc.CloseHour, sc.GetCloseMinute(), 0, 0, loc)
}

// NextBusinessDayStart finds the opening time of the next available business day (searching up to 14 days ahead)
func NextBusinessDayStart(t time.Time, schedules map[int]domain.WorkingHourConfig, loc *time.Location) (time.Time, bool) {
	nextDay := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
	for i := 0; i < 14; i++ {
		wday := int(nextDay.Weekday())
		if sc, ok := schedules[wday]; ok && !sc.IsClosed() {
			openHour := sc.OpenHour
			openMin := sc.GetOpenMinute()
			if sc.IsOpenAllDay() {
				openHour = 0
				openMin = 0
			}
			return time.Date(nextDay.Year(), nextDay.Month(), nextDay.Day(), openHour, openMin, 0, 0, loc), true
		}
		nextDay = nextDay.AddDate(0, 0, 1)
	}
	return t, false
}

// IsWithinWorkingHours checks if a given time falls within the inbox's business hours
func IsWithinWorkingHours(inbox *domain.Inbox, t time.Time) bool {
	if inbox == nil || !inbox.WorkingHoursEnabled {
		return true
	}
	if inbox.WorkingHours == "" {
		return true
	}

	loc, schedules, hasOpenDays := ParseWorkingHours(inbox)
	if !hasOpenDays {
		return false
	}

	localTime := t.In(loc)
	wday := int(localTime.Weekday())
	sc, ok := schedules[wday]
	if !ok {
		// Day not explicitly specified in schedules: treat as closed if schedules has entries
		return false
	}
	if sc.IsClosed() {
		return false
	}
	if sc.IsOpenAllDay() {
		return true
	}

	curMin := localTime.Hour()*60 + localTime.Minute()
	openMin := sc.OpenHour*60 + sc.GetOpenMinute()
	closeMin := sc.CloseHour*60 + sc.GetCloseMinute()

	return curMin >= openMin && curMin < closeMin
}

// CalculateDeadline calculates the deadline based on Inbox timezone and working hours,
// rolling over non-working days, holidays/closed days, and outside daily business hours.
func CalculateDeadline(inbox *domain.Inbox, startTime time.Time, thresholdSeconds int) time.Time {
	if thresholdSeconds <= 0 {
		return startTime
	}

	loc, schedules, hasOpenDays := ParseWorkingHours(inbox)
	if !hasOpenDays {
		return startTime.Add(time.Duration(thresholdSeconds) * time.Second)
	}

	remainingSeconds := thresholdSeconds
	currentTime := startTime.In(loc)

	maxIterations := 500
	iterations := 0

	for remainingSeconds > 0 && iterations < maxIterations {
		iterations++
		wday := int(currentTime.Weekday())
		sc, exists := schedules[wday]

		if !exists || sc.IsClosed() {
			nextStart, ok := NextBusinessDayStart(currentTime, schedules, loc)
			if !ok {
				return currentTime.Add(time.Duration(remainingSeconds) * time.Second).UTC()
			}
			currentTime = nextStart
			continue
		}

		dayOpenTime := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), sc.OpenHour, sc.GetOpenMinute(), 0, 0, loc)
		if sc.IsOpenAllDay() {
			dayOpenTime = time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, loc)
		}
		dayCloseTime := DayCloseTimeFor(currentTime, sc, loc)

		if currentTime.Before(dayOpenTime) {
			currentTime = dayOpenTime
		} else if !currentTime.Before(dayCloseTime) {
			nextStart, ok := NextBusinessDayStart(currentTime, schedules, loc)
			if !ok {
				return currentTime.Add(time.Duration(remainingSeconds) * time.Second).UTC()
			}
			currentTime = nextStart
			continue
		}

		availableSeconds := int(dayCloseTime.Sub(currentTime).Seconds())
		if availableSeconds <= 0 {
			nextStart, ok := NextBusinessDayStart(currentTime, schedules, loc)
			if !ok {
				return currentTime.Add(time.Duration(remainingSeconds) * time.Second).UTC()
			}
			currentTime = nextStart
			continue
		}

		if remainingSeconds <= availableSeconds {
			currentTime = currentTime.Add(time.Duration(remainingSeconds) * time.Second)
			remainingSeconds = 0
		} else {
			remainingSeconds -= availableSeconds
			nextStart, ok := NextBusinessDayStart(currentTime, schedules, loc)
			if !ok {
				return currentTime.Add(time.Duration(remainingSeconds) * time.Second).UTC()
			}
			currentTime = nextStart
		}
	}

	return currentTime.UTC()
}

// CalculateBusinessSeconds calculates the total business seconds elapsed between startTime and endTime
// considering only open working hours in the inbox's timezone.
func CalculateBusinessSeconds(inbox *domain.Inbox, startTime, endTime time.Time) int {
	if !endTime.After(startTime) {
		return 0
	}

	loc, schedules, hasOpenDays := ParseWorkingHours(inbox)
	if !hasOpenDays {
		return int(endTime.Sub(startTime).Seconds())
	}

	totalSeconds := 0
	currentTime := startTime.In(loc)
	targetTime := endTime.In(loc)

	maxDays := 365
	days := 0

	for currentTime.Before(targetTime) && days < maxDays {
		days++
		wday := int(currentTime.Weekday())
		sc, exists := schedules[wday]
		if !exists || sc.IsClosed() {
			nextDay := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
			if nextDay.After(targetTime) {
				break
			}
			currentTime = nextDay
			continue
		}

		dayOpenTime := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), sc.OpenHour, sc.GetOpenMinute(), 0, 0, loc)
		if sc.IsOpenAllDay() {
			dayOpenTime = time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, loc)
		}
		dayCloseTime := DayCloseTimeFor(currentTime, sc, loc)

		winStart := currentTime
		if winStart.Before(dayOpenTime) {
			winStart = dayOpenTime
		}

		winEnd := targetTime
		if winEnd.After(dayCloseTime) {
			winEnd = dayCloseTime
		}

		if winEnd.After(winStart) {
			totalSeconds += int(winEnd.Sub(winStart).Seconds())
		}

		nextDay := time.Date(currentTime.Year(), currentTime.Month(), currentTime.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
		currentTime = nextDay
	}

	return totalSeconds
}
