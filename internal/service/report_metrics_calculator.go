package service

import (
	"math"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

// IsConversationWithinBusinessHours checks whether a conversation was created during business hours
func IsConversationWithinBusinessHours(conv domain.Conversation, inbox *domain.Inbox) bool {
	if inbox != nil && inbox.WorkingHoursEnabled && inbox.WorkingHours != "" {
		return IsWithinWorkingHours(inbox, conv.CreatedAt)
	}

	// Fallback to inbox or account timezone with standard business hours (Mon-Fri 09:00-18:00)
	loc := time.UTC
	if inbox != nil && inbox.Timezone != "" {
		if l, err := time.LoadLocation(inbox.Timezone); err == nil {
			loc = l
		}
	}

	localTime := conv.CreatedAt.In(loc)
	wday := localTime.Weekday()
	if wday == time.Sunday || wday == time.Saturday {
		return false
	}
	hour := localTime.Hour()
	return hour >= 9 && hour < 18
}

// FilterConversationsByBusinessHours filters a slice of conversations based on their inbox business hours
func FilterConversationsByBusinessHours(convs []domain.Conversation, inboxesMap map[uint]*domain.Inbox) []domain.Conversation {
	filtered := make([]domain.Conversation, 0, len(convs))
	for _, c := range convs {
		inb := inboxesMap[c.InboxID]
		if IsConversationWithinBusinessHours(c, inb) {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

// CalculateResponseDuration calculates duration between start and end time in seconds
func CalculateResponseDuration(startTime, endTime time.Time, inbox *domain.Inbox, useBusinessHours bool) float64 {
	if endTime.Before(startTime) {
		return 0
	}
	if useBusinessHours && inbox != nil {
		sec := CalculateBusinessSeconds(inbox, startTime, endTime)
		return float64(sec)
	}
	sec := endTime.Sub(startTime).Seconds()
	if sec < 0 {
		return 0
	}
	return sec
}

// CalculateConversationFRT computes the First Response Time (in seconds) for a single conversation
func CalculateConversationFRT(conv domain.Conversation, messages []domain.Message, inbox *domain.Inbox, useBusinessHours bool) (float64, bool) {
	var firstIn *domain.Message
	for i := range messages {
		if messages[i].MessageType == domain.MessageTypeIncoming {
			firstIn = &messages[i]
			break
		}
	}
	if firstIn == nil {
		return 0, false
	}

	var firstOut *domain.Message
	for i := range messages {
		m := &messages[i]
		if m.MessageType == domain.MessageTypeOutgoing && !m.Private && !m.CreatedAt.Before(firstIn.CreatedAt) {
			firstOut = m
			break
		}
	}
	if firstOut == nil {
		return 0, false
	}

	dur := CalculateResponseDuration(firstIn.CreatedAt, firstOut.CreatedAt, inbox, useBusinessHours)
	return dur, true
}

// CalculateConversationResolutionTime computes the resolution duration (in seconds) for a conversation
func CalculateConversationResolutionTime(conv domain.Conversation, inbox *domain.Inbox, useBusinessHours bool) (float64, bool) {
	if conv.Status != domain.ConversationStatusResolved {
		return 0, false
	}
	endTime := conv.UpdatedAt
	if endTime.Before(conv.CreatedAt) {
		endTime = conv.CreatedAt
	}
	dur := CalculateResponseDuration(conv.CreatedAt, endTime, inbox, useBusinessHours)
	return dur, true
}

// CalculateConversationReplyTimes calculates the reply durations across messages in a conversation
func CalculateConversationReplyTimes(messages []domain.Message, inbox *domain.Inbox, useBusinessHours bool) []float64 {
	var durations []float64
	var pendingIn *domain.Message

	for i := range messages {
		m := &messages[i]
		if m.MessageType == domain.MessageTypeIncoming {
			if pendingIn == nil {
				pendingIn = m
			}
		} else if m.MessageType == domain.MessageTypeOutgoing && !m.Private {
			if pendingIn != nil {
				dur := CalculateResponseDuration(pendingIn.CreatedAt, m.CreatedAt, inbox, useBusinessHours)
				durations = append(durations, dur)
				pendingIn = nil
			}
		}
	}
	return durations
}

// CalculateFCRAndBotMetrics evaluates First Contact Resolution rate and Bot handling metrics
func CalculateFCRAndBotMetrics(convs []domain.Conversation, messagesMap map[uint][]domain.Message) (fcrRate float64, botResolved int64, botHandoff int64) {
	var resolvedTotal int64
	var fcrCount int64

	for _, c := range convs {
		msgs := messagesMap[c.ID]
		hasBot := false
		agentOutgoingCount := 0

		for _, m := range msgs {
			if m.SenderType == "AgentBot" || m.SenderType == "CaptainAssistant" || m.SenderType == "Captain::Assistant" {
				hasBot = true
			}
			if m.MessageType == domain.MessageTypeOutgoing && !m.Private {
				agentOutgoingCount++
			}
		}

		if c.Status == domain.ConversationStatusResolved {
			resolvedTotal++
			if agentOutgoingCount == 1 {
				fcrCount++
			}
			if hasBot && (c.AssigneeID == nil || *c.AssigneeID == 0) {
				botResolved++
			}
		}

		if hasBot && c.AssigneeID != nil && *c.AssigneeID > 0 {
			botHandoff++
		}
	}

	if resolvedTotal > 0 {
		fcrRate = RoundToOneDecimal((float64(fcrCount) / float64(resolvedTotal)) * 100.0)
	}
	return fcrRate, botResolved, botHandoff
}

// RoundToOneDecimal rounds a float64 to one decimal place
func RoundToOneDecimal(val float64) float64 {
	return math.Round(val*10) / 10
}
