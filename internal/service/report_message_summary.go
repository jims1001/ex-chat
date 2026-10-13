package service

import (
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

// ReportMessageSummaryPort preserves the legacy summary's optional bounds.
// This local count-only capability does not load message bodies or select a
// conversation cohort and is independent of the bounded daily-series contract.
type ReportMessageSummaryPort interface {
	CountReportSummary(uint, *time.Time, *time.Time) (domain.ReportMessageSummary, error)
}

func (s *ReportService) SetSummaryMessageOwner(messages ReportMessageSummaryPort) {
	s.summaryMessageCounts = messages
}
