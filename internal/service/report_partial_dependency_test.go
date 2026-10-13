package service

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
)

type reportPartialMembersStub struct {
	rows     []domain.AccountUser
	err      error
	accounts []uint
	trace    *[]string
}

func (s *reportPartialMembersStub) ListMembers(account uint) ([]domain.AccountUser, error) {
	s.accounts = append(s.accounts, account)
	if s.trace != nil {
		*s.trace = append(*s.trace, "members")
	}
	return s.rows, s.err
}

type reportPartialConversationsStub struct {
	reportConversationPort
	rows     []domain.Conversation
	failAt   int
	err      error
	partial  bool
	accounts []uint
	bounds   [][2]*time.Time
	trace    *[]string
}

func (s *reportPartialConversationsStub) ListForReport(account uint, since, until *time.Time) ([]domain.Conversation, error) {
	s.accounts = append(s.accounts, account)
	s.bounds = append(s.bounds, [2]*time.Time{since, until})
	if s.trace != nil {
		*s.trace = append(*s.trace, "conversations")
	}
	if len(s.accounts) == s.failAt {
		if s.partial {
			return s.rows, s.err
		}
		return nil, s.err
	}
	return s.rows, nil
}

func reportPartialMembers() []domain.AccountUser {
	return []domain.AccountUser{
		{UserID: 9, User: &domain.User{ID: 19, Name: "First", Email: "first@example.test", AvatarURL: "first.png"}, Availability: "busy"},
		{UserID: 99},
		{UserID: 7, User: &domain.User{ID: 17, Name: "Second", Email: "second@example.test"}, Availability: "online"},
	}
}

func reportPartialConversations() []domain.Conversation {
	first, second := uint(9), uint(7)
	seen := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	return []domain.Conversation{
		{AssigneeID: &first, Status: domain.ConversationStatusOpen, UnreadCount: 1, AgentLastSeenAt: &seen},
		{AssigneeID: &first, Status: domain.ConversationStatusOpen, AgentLastSeenAt: &seen},
		{AssigneeID: &second, Status: domain.ConversationStatusOpen},
		{Status: domain.ConversationStatusOpen},
		{Status: domain.ConversationStatusPending},
		{AssigneeID: &first, Status: domain.ConversationStatusResolved},
	}
}

func reportPartialExpectedAgents() []AgentLiveConversationMetric {
	return []AgentLiveConversationMetric{
		{ID: 19, Name: "First", Email: "first@example.test", Thumbnail: "first.png", Availability: "busy", Metric: LiveMetricsReport{Open: 2, Unattended: 1, Pending: 1}},
		{ID: 17, Name: "Second", Email: "second@example.test", Availability: "online", Metric: LiveMetricsReport{Open: 1, Unattended: 1, Pending: 1}},
	}
}

func TestReportAgentDependencyStopsAtFirstError(t *testing.T) {
	ownerErr := errors.New("CON dependency unavailable")
	for failAt := 1; failAt <= 4; failAt++ {
		for _, partial := range []bool{false, true} {
			t.Run(fmt.Sprintf("read_%d/partial_%t", failAt, partial), func(t *testing.T) {
				conversations := &reportPartialConversationsStub{rows: reportPartialConversations(), failAt: failAt, partial: partial, err: ownerErr}
				members := &reportPartialMembersStub{rows: reportPartialMembers()}
				// All unrelated ports remain nil; no later fallback may hide the error.
				s := &ReportService{accounts: members, conversations: conversations}
				got, err := s.GetConversationsReport(91, "agent", 1, 25)
				require.ErrorIs(t, err, ownerErr)
				require.Nil(t, got, "discard even earlier complete agent rows on failure")
				require.Len(t, conversations.accounts, failAt, "the next owner call would succeed but must not run")
				require.Equal(t, []uint{91}, members.accounts)
				for i, account := range conversations.accounts {
					require.Equal(t, uint(91), account)
					require.Equal(t, [2]*time.Time{}, conversations.bounds[i])
				}
				got, err = s.GetConversationsReport(91, "agent", 1, 25)
				require.NoError(t, err)
				require.Equal(t, reportPartialExpectedAgents(), got)
				require.Len(t, conversations.accounts, failAt+4)
			})
		}
	}
}

func TestReportAgentHealthyPaginationAndOrder(t *testing.T) {
	for _, tc := range []struct {
		name          string
		page, perPage int
		want          []AgentLiveConversationMetric
	}{
		{"default", 0, 0, reportPartialExpectedAgents()},
		{"first_page", 1, 1, reportPartialExpectedAgents()[:1]},
		{"nil_user_page", 2, 1, []AgentLiveConversationMetric{}},
		{"last_page", 3, 1, reportPartialExpectedAgents()[1:]},
		{"past_end", 9, 2, []AgentLiveConversationMetric{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trace := []string{}
			members := &reportPartialMembersStub{rows: reportPartialMembers(), trace: &trace}
			conversations := &reportPartialConversationsStub{rows: reportPartialConversations(), trace: &trace}
			s := &ReportService{accounts: members, conversations: conversations}
			got, err := s.GetConversationsReport(91, "agent", tc.page, tc.perPage)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
			wantTrace := []string{"members"}
			for range tc.want {
				wantTrace = append(wantTrace, "conversations", "conversations")
			}
			require.Equal(t, wantTrace, trace, "preserve successful read ordering, without optimizing N+1")
		})
	}
}

func TestReportAgentHealthyEmptyWithoutConversationOwner(t *testing.T) {
	for _, rows := range [][]domain.AccountUser{nil, {}, {{UserID: 7}}} {
		s := &ReportService{accounts: &reportPartialMembersStub{rows: rows}}
		got, err := s.GetConversationsReport(91, "agent", 1, 25)
		require.NoError(t, err)
		require.Equal(t, []AgentLiveConversationMetric{}, got)
	}
}

func TestReportAgentMemberErrorBeforeConversationOwner(t *testing.T) {
	ownerErr := errors.New("TEN dependency unavailable")
	s := &ReportService{accounts: &reportPartialMembersStub{rows: reportPartialMembers(), err: ownerErr}}
	got, err := s.GetConversationsReport(91, "agent", 1, 25)
	require.ErrorIs(t, err, ownerErr)
	require.Nil(t, got)
}

func TestReportConversationsAccountModeUnchanged(t *testing.T) {
	for _, mode := range []string{"", "account", "ACCOUNT"} {
		t.Run(mode, func(t *testing.T) {
			conversations := &reportPartialConversationsStub{rows: reportPartialConversations()}
			s := &ReportService{conversations: conversations}
			got, err := s.GetConversationsReport(91, mode, 9, 1)
			require.NoError(t, err)
			require.Equal(t, &LiveMetricsReport{Open: 4, Unattended: 3, Unassigned: 1, Pending: 1}, got)
			require.Equal(t, []uint{91}, conversations.accounts)
			ownerErr := errors.New("CON account read failed")
			conversations.failAt, conversations.err, conversations.partial = 2, ownerErr, true
			got, err = s.GetConversationsReport(91, mode, 1, 25)
			require.ErrorIs(t, err, ownerErr)
			require.Nil(t, got)
		})
	}
}

func TestReportAnnualConversationErrorBeforeReportingStore(t *testing.T) {
	ownerErr := errors.New("CON annual dependency unavailable")
	conversations := &reportPartialConversationsStub{rows: reportPartialConversations(), failAt: 1, partial: true, err: ownerErr}
	s := &ReportService{conversations: conversations}
	got, err := s.GetYearInReview(91, 7, 2024)
	require.ErrorIs(t, err, ownerErr)
	require.Nil(t, got)
	require.Equal(t, []uint{91}, conversations.accounts)
	require.Equal(t, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), *conversations.bounds[0][0])
	require.Equal(t, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), *conversations.bounds[0][1])
}
