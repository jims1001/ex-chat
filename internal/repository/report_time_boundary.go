package repository

import (
	"time"

	"gorm.io/gorm"
)

// ReportTimeBoundary preserves exact half-open comparisons against PostgreSQL's
// stored microsecond grid. PostgreSQL truncates time parameters to microseconds;
// for a grid-aligned stored x, both x >= t and x < t instead require ceil(t).
// This adapts only reporting SQL parameters, never parsed endpoints, period
// durations, stored records or SQLite's nanosecond-capable parameters.
func ReportTimeBoundary(db *gorm.DB, at time.Time) time.Time {
	if db.Dialector.Name() != "postgres" {
		return at
	}
	remainder := time.Duration(at.Nanosecond()) % time.Microsecond
	if remainder != 0 {
		return at.Add(time.Microsecond - remainder)
	}
	return at
}
