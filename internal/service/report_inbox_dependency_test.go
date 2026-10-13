package service

import (
	"errors"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
)

type reportInboxDependencyStub struct {
	rows     []domain.Inbox
	err      error
	accounts []uint
}

func (r *reportInboxDependencyStub) ListByAccount(account uint) ([]domain.Inbox, error) {
	r.accounts = append(r.accounts, account)
	// Fail only the first call. A later successful read must never hide that
	// failure (GetInboxMetrics otherwise lists inboxes a second time).
	if len(r.accounts) == 1 {
		return r.rows, r.err
	}
	return r.rows, nil
}

type reportInboxDependencyCase struct {
	name string
	call func(*ReportService, uint, ReportFilter) (any, error)
}

func reportInboxDependencyCases() []reportInboxDependencyCase {
	return []reportInboxDependencyCase{
		{"summary", func(s *ReportService, a uint, f ReportFilter) (any, error) { return s.GetAccountSummary(a, f) }},
		{"agents", func(s *ReportService, a uint, f ReportFilter) (any, error) { return s.GetAgentMetrics(a, f) }},
		{"teams", func(s *ReportService, a uint, f ReportFilter) (any, error) { return s.GetTeamMetrics(a, f) }},
		{"inboxes", func(s *ReportService, a uint, f ReportFilter) (any, error) { return s.GetInboxMetrics(a, f) }},
		{"labels", func(s *ReportService, a uint, f ReportFilter) (any, error) { return s.GetLabelMetrics(a, f) }},
		{"distribution", func(s *ReportService, a uint, f ReportFilter) (any, error) {
			return s.GetFirstResponseDistribution(a, f)
		}},
		{"export", func(s *ReportService, a uint, f ReportFilter) (any, error) { return s.GetConversationsForExport(a, f) }},
		{"trends", func(s *ReportService, a uint, f ReportFilter) (any, error) { return s.GetConversationTrends(a, 7, f) }},
	}
}

func TestReportInboxDependencyFirstFailure(t *testing.T) {
	ownerErr := errors.New("CHN owner read failed")
	for _, business := range []bool{false, true} {
		mode := "ordinary"
		if business {
			mode = "business_hours"
		}
		for _, partial := range []bool{false, true} {
			result := "no_rows"
			if partial {
				result = "partial_rows"
			}
			for _, tc := range reportInboxDependencyCases() {
				t.Run(mode+"/"+result+"/"+tc.name, func(t *testing.T) {
					owner := &reportInboxDependencyStub{err: ownerErr}
					if partial {
						owner.rows = []domain.Inbox{{ID: 10, AccountID: 91, ChannelType: "Channel::Email"}}
					}
					// Nil downstream ports ensure failure stops before other owners.
					s := &ReportService{inboxes: owner}
					got, err := tc.call(s, 91, ReportFilter{BusinessHours: business})
					require.ErrorIs(t, err, ownerErr)
					require.Nil(t, got, "an owner failure must not return partial/successful metrics")
					require.Equal(t, []uint{91}, owner.accounts)
				})
			}
		}
	}
}

type reportInboxEmptyConversations struct {
	reportConversationPort
}

func (reportInboxEmptyConversations) ListForReport(uint, *time.Time, *time.Time) ([]domain.Conversation, error) {
	return []domain.Conversation{}, nil
}

func TestReportInboxDependencyEmptyAndPartialMap(t *testing.T) {
	ownerErr := errors.New("incomplete CHN response")
	for _, tc := range []struct {
		name string
		rows []domain.Inbox
		err  error
	}{
		{"healthy_nil", nil, nil},
		{"healthy_empty", []domain.Inbox{}, nil},
		{"failed_nil", nil, ownerErr},
		{"failed_partial", []domain.Inbox{{ID: 10, AccountID: 91}}, ownerErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &ReportService{inboxes: &reportInboxDependencyStub{rows: tc.rows, err: tc.err}, conversations: reportInboxEmptyConversations{}}
			conversations, got, err := s.loadAccountConversations(91, ReportFilter{})
			require.Empty(t, conversations)
			if tc.err != nil {
				require.ErrorIs(t, err, ownerErr)
				require.Nil(t, got)
			} else {
				require.NoError(t, err)
				require.NotNil(t, got, "successful absent configuration retains an empty map")
				require.Empty(t, got)
			}
		})
	}
}

func TestReportInboxDependencyInvalidTrendWindowUnchanged(t *testing.T) {
	at := time.Date(2024, 2, 29, 16, 0, 0, 0, time.UTC)
	for _, until := range []time.Time{at, at.Add(-time.Hour)} {
		// Existing empty/reversed explicit windows return before any owner read.
		s := &ReportService{}
		got, err := s.GetConversationTrends(91, 7, ReportFilter{Since: &at, Until: &until, BusinessHours: true})
		require.NoError(t, err)
		require.Equal(t, &ConversationTrendsReport{Trends: []TrendPoint{}}, got)
	}
}
