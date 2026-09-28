package service

import (
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

func (s *ReportService) loadInboxesMap(accountID uint) map[uint]*domain.Inbox {
	inboxes, _ := s.inboxes.ListByAccount(accountID)
	m := make(map[uint]*domain.Inbox, len(inboxes))
	for i := range inboxes {
		m[inboxes[i].ID] = &inboxes[i]
	}
	return m
}

func (s *ReportService) loadAccountConversations(accountID uint, filter ReportFilter) ([]domain.Conversation, map[uint]*domain.Inbox, error) {
	inboxesMap := s.loadInboxesMap(accountID)

	convs, err := s.conversations.ListForReport(accountID, filter.Since, filter.Until)
	if err != nil {
		return nil, nil, err
	}

	if filter.BusinessHours {
		convs = FilterConversationsByBusinessHours(convs, inboxesMap)
	}
	return convs, inboxesMap, nil
}

func (s *ReportService) loadMessagesForConversations(accountID uint, convIDs []uint, filter ReportFilter) (map[uint][]domain.Message, error) {
	res := make(map[uint][]domain.Message)
	if len(convIDs) == 0 {
		return res, nil
	}
	msgs, err := s.messages.ListForReport(accountID, convIDs, filter.Since, filter.Until)
	if err != nil {
		return nil, err
	}
	for _, m := range msgs {
		res[m.ConversationID] = append(res[m.ConversationID], m)
	}
	return res, nil
}

// GetAgentMetrics compiles agent productivity and performance metrics
func (s *ReportService) GetAgentMetrics(accountID uint, filters ...ReportFilter) ([]AgentMetric, error) {
	var filter ReportFilter
	if len(filters) > 0 {
		filter = filters[0]
	}

	convs, inboxesMap, err := s.loadAccountConversations(accountID, filter)
	if err != nil {
		return nil, err
	}

	var convIDs []uint
	for _, c := range convs {
		convIDs = append(convIDs, c.ID)
	}
	messagesMap, err := s.loadMessagesForConversations(accountID, convIDs, filter)
	if err != nil {
		return nil, err
	}

	members, err := s.accounts.ListMembers(accountID)
	if err != nil {
		return nil, err
	}

	metrics := make([]AgentMetric, 0, len(members))
	for _, m := range members {
		if m.User == nil {
			continue
		}

		var assignedCount int64
		var resolvedCount int64
		var frtSum, rtSum, artSum float64
		var frtCount, rtCount, artCount int

		for _, c := range convs {
			if c.AssigneeID != nil && *c.AssigneeID == m.UserID {
				assignedCount++
				if c.Status == domain.ConversationStatusResolved {
					resolvedCount++
				}
				inb := inboxesMap[c.InboxID]
				msgs := messagesMap[c.ID]

				if frt, ok := CalculateConversationFRT(c, msgs, inb, filter.BusinessHours); ok {
					frtSum += frt
					frtCount++
				}
				if rt, ok := CalculateConversationResolutionTime(c, inb, filter.BusinessHours); ok {
					rtSum += rt
					rtCount++
				}
				replyTimes := CalculateConversationReplyTimes(msgs, inb, filter.BusinessHours)
				for _, r := range replyTimes {
					artSum += r
					artCount++
				}
			}
		}

		var avgFRT, avgRT, avgReply, resRate float64
		if frtCount > 0 {
			avgFRT = RoundToOneDecimal(frtSum / float64(frtCount))
		}
		if rtCount > 0 {
			avgRT = RoundToOneDecimal(rtSum / float64(rtCount))
		}
		if artCount > 0 {
			avgReply = RoundToOneDecimal(artSum / float64(artCount))
		}
		if assignedCount > 0 {
			resRate = RoundToOneDecimal((float64(resolvedCount) / float64(assignedCount)) * 100.0)
		}

		var totalSent int64
		for _, messages := range messagesMap {
			for _, message := range messages {
				if message.SenderType == domain.SenderTypeUser && message.SenderID == m.UserID {
					totalSent++
				}
			}
		}

		metrics = append(metrics, AgentMetric{
			UserID:                m.UserID,
			Name:                  m.User.Name,
			Email:                 m.User.Email,
			AssignedConversations: assignedCount,
			ResolvedConversations: resolvedCount,
			AvgFirstResponseTime:  avgFRT,
			AvgResolutionTime:     avgRT,
			AvgReplyTime:          avgReply,
			ResolutionRate:        resRate,
			TotalMessagesSent:     totalSent,
		})
	}
	return metrics, nil
}

// GetTeamMetrics compiles team productivity metrics
func (s *ReportService) GetTeamMetrics(accountID uint, filters ...ReportFilter) ([]TeamMetric, error) {
	var filter ReportFilter
	if len(filters) > 0 {
		filter = filters[0]
	}

	convs, inboxesMap, err := s.loadAccountConversations(accountID, filter)
	if err != nil {
		return nil, err
	}

	var convIDs []uint
	for _, c := range convs {
		convIDs = append(convIDs, c.ID)
	}
	messagesMap, err := s.loadMessagesForConversations(accountID, convIDs, filter)
	if err != nil {
		return nil, err
	}

	teams, err := s.teams.List(accountID)
	if err != nil {
		return nil, err
	}

	metrics := make([]TeamMetric, 0, len(teams))
	for _, t := range teams {
		memberIDs := make([]uint, 0, len(t.Members))
		for _, member := range t.Members {
			memberIDs = append(memberIDs, member.ID)
		}
		memberSet := make(map[uint]bool, len(memberIDs))
		for _, mid := range memberIDs {
			memberSet[mid] = true
		}

		var assignedCount int64
		var resolvedCount int64
		var frtSum, rtSum float64
		var frtCount, rtCount int

		for _, c := range convs {
			belongs := false
			if c.TeamID != nil && *c.TeamID == t.ID {
				belongs = true
			} else if c.AssigneeID != nil && memberSet[*c.AssigneeID] {
				belongs = true
			}

			if belongs {
				assignedCount++
				if c.Status == domain.ConversationStatusResolved {
					resolvedCount++
				}
				inb := inboxesMap[c.InboxID]
				msgs := messagesMap[c.ID]

				if frt, ok := CalculateConversationFRT(c, msgs, inb, filter.BusinessHours); ok {
					frtSum += frt
					frtCount++
				}
				if rt, ok := CalculateConversationResolutionTime(c, inb, filter.BusinessHours); ok {
					rtSum += rt
					rtCount++
				}
			}
		}

		var avgFRT, avgRT, resRate float64
		if frtCount > 0 {
			avgFRT = RoundToOneDecimal(frtSum / float64(frtCount))
		}
		if rtCount > 0 {
			avgRT = RoundToOneDecimal(rtSum / float64(rtCount))
		}
		if assignedCount > 0 {
			resRate = RoundToOneDecimal((float64(resolvedCount) / float64(assignedCount)) * 100.0)
		}

		metrics = append(metrics, TeamMetric{
			TeamID:                t.ID,
			Name:                  t.Name,
			MemberCount:           len(memberIDs),
			AssignedConversations: assignedCount,
			ResolvedConversations: resolvedCount,
			AvgFirstResponseTime:  avgFRT,
			AvgResolutionTime:     avgRT,
			ResolutionRate:        resRate,
		})
	}
	return metrics, nil
}

// GetInboxMetrics compiles metrics per inbox channel
func (s *ReportService) GetInboxMetrics(accountID uint, filters ...ReportFilter) ([]InboxMetric, error) {
	var filter ReportFilter
	if len(filters) > 0 {
		filter = filters[0]
	}

	convs, inboxesMap, err := s.loadAccountConversations(accountID, filter)
	if err != nil {
		return nil, err
	}

	var convIDs []uint
	for _, c := range convs {
		convIDs = append(convIDs, c.ID)
	}
	messagesMap, err := s.loadMessagesForConversations(accountID, convIDs, filter)
	if err != nil {
		return nil, err
	}

	inboxes, err := s.inboxes.ListByAccount(accountID)
	if err != nil {
		return nil, err
	}

	metrics := make([]InboxMetric, 0, len(inboxes))
	for _, inb := range inboxes {
		var total int64
		var open int64
		var resolved int64
		var pending int64
		var frtSum, rtSum, artSum float64
		var frtCount, rtCount, artCount int

		for _, c := range convs {
			if c.InboxID == inb.ID {
				total++
				if c.Status == domain.ConversationStatusOpen {
					open++
				} else if c.Status == domain.ConversationStatusResolved {
					resolved++
				} else if c.Status == domain.ConversationStatusPending {
					pending++
				}

				msgs := messagesMap[c.ID]
				if frt, ok := CalculateConversationFRT(c, msgs, inboxesMap[c.InboxID], filter.BusinessHours); ok {
					frtSum += frt
					frtCount++
				}
				if rt, ok := CalculateConversationResolutionTime(c, inboxesMap[c.InboxID], filter.BusinessHours); ok {
					rtSum += rt
					rtCount++
				}
				replyTimes := CalculateConversationReplyTimes(msgs, inboxesMap[c.InboxID], filter.BusinessHours)
				for _, r := range replyTimes {
					artSum += r
					artCount++
				}
			}
		}

		var avgFRT, avgRT, avgReply, resRate float64
		if frtCount > 0 {
			avgFRT = RoundToOneDecimal(frtSum / float64(frtCount))
		}
		if rtCount > 0 {
			avgRT = RoundToOneDecimal(rtSum / float64(rtCount))
		}
		if artCount > 0 {
			avgReply = RoundToOneDecimal(artSum / float64(artCount))
		}
		if total > 0 {
			resRate = RoundToOneDecimal((float64(resolved) / float64(total)) * 100.0)
		}

		metrics = append(metrics, InboxMetric{
			InboxID:               inb.ID,
			Name:                  inb.Name,
			ChannelType:           inb.ChannelType,
			TotalConversations:    total,
			OpenConversations:     open,
			ResolvedConversations: resolved,
			PendingConversations:  pending,
			AvgFirstResponseTime:  avgFRT,
			AvgResolutionTime:     avgRT,
			AvgReplyTime:          avgReply,
			ResolutionRate:        resRate,
		})
	}
	return metrics, nil
}

// GetLabelMetrics compiles metrics for conversation labels
func (s *ReportService) GetLabelMetrics(accountID uint, filters ...ReportFilter) ([]LabelMetric, error) {
	var filter ReportFilter
	if len(filters) > 0 {
		filter = filters[0]
	}

	convs, inboxesMap, err := s.loadAccountConversations(accountID, filter)
	if err != nil {
		return nil, err
	}

	var convIDs []uint
	convMap := make(map[uint]domain.Conversation, len(convs))
	for _, c := range convs {
		convIDs = append(convIDs, c.ID)
		convMap[c.ID] = c
	}
	messagesMap, err := s.loadMessagesForConversations(accountID, convIDs, filter)
	if err != nil {
		return nil, err
	}

	labels, err := s.labels.List(accountID)
	if err != nil {
		return nil, err
	}
	relations, err := s.conversations.ListLabelRelationsForReport(accountID, convIDs)
	if err != nil {
		return nil, err
	}
	byLabel := make(map[uint][]uint)
	for _, relation := range relations {
		byLabel[relation.LabelID] = append(byLabel[relation.LabelID], relation.ConversationID)
	}

	metrics := make([]LabelMetric, 0, len(labels))
	for _, l := range labels {
		matchedConvIDs := byLabel[l.ID]

		var count, resolvedCount int64
		var frtSum, rtSum float64
		var frtCount, rtCount int

		for _, cid := range matchedConvIDs {
			c, exists := convMap[cid]
			if !exists {
				continue
			}
			count++
			if c.Status == domain.ConversationStatusResolved {
				resolvedCount++
			}
			inb := inboxesMap[c.InboxID]
			msgs := messagesMap[c.ID]

			if frt, ok := CalculateConversationFRT(c, msgs, inb, filter.BusinessHours); ok {
				frtSum += frt
				frtCount++
			}
			if rt, ok := CalculateConversationResolutionTime(c, inb, filter.BusinessHours); ok {
				rtSum += rt
				rtCount++
			}
		}

		var avgFRT, avgRT float64
		if frtCount > 0 {
			avgFRT = RoundToOneDecimal(frtSum / float64(frtCount))
		}
		if rtCount > 0 {
			avgRT = RoundToOneDecimal(rtSum / float64(rtCount))
		}

		metrics = append(metrics, LabelMetric{
			LabelID:               l.ID,
			Title:                 l.Title,
			Color:                 l.Color,
			ConversationCount:     count,
			ResolvedConversations: resolvedCount,
			AvgFirstResponseTime:  avgFRT,
			AvgResolutionTime:     avgRT,
		})
	}
	return metrics, nil
}

// GetFirstResponseDistribution compiles first response distribution into standard buckets
func (s *ReportService) GetFirstResponseDistribution(accountID uint, filters ...ReportFilter) (*FirstResponseDistributionReport, error) {
	var filter ReportFilter
	if len(filters) > 0 {
		filter = filters[0]
	}

	convs, inboxesMap, err := s.loadAccountConversations(accountID, filter)
	if err != nil {
		return nil, err
	}

	var convIDs []uint
	for _, c := range convs {
		convIDs = append(convIDs, c.ID)
	}
	messagesMap, err := s.loadMessagesForConversations(accountID, convIDs, filter)
	if err != nil {
		return nil, err
	}

	report := &FirstResponseDistributionReport{
		Channels: make([]ChannelFRTDistribution, 0),
	}
	channelMap := make(map[string]*FirstResponseDistributionBuckets)

	for _, c := range convs {
		inb := inboxesMap[c.InboxID]
		chanType := "Channel::WebWidget"
		if inb != nil && inb.ChannelType != "" {
			chanType = inb.ChannelType
		}

		if _, exists := channelMap[chanType]; !exists {
			channelMap[chanType] = &FirstResponseDistributionBuckets{}
		}
		chBuckets := channelMap[chanType]

		msgs := messagesMap[c.ID]
		frtSeconds, ok := CalculateConversationFRT(c, msgs, inb, filter.BusinessHours)
		if !ok {
			continue
		}

		diff := time.Duration(frtSeconds * float64(time.Second))

		// 5 standard buckets: 0-1h, 1-4h, 4-8h, 8-24h, 24h+
		if diff <= time.Hour {
			report.Total.ZeroToOneHour++
			chBuckets.ZeroToOneHour++
		} else if diff <= 4*time.Hour {
			report.Total.OneToFourHours++
			chBuckets.OneToFourHours++
		} else if diff <= 8*time.Hour {
			report.Total.FourToEightHours++
			chBuckets.FourToEightHours++
		} else if diff <= 24*time.Hour {
			report.Total.EightToTwentyFour++
			chBuckets.EightToTwentyFour++
		} else {
			report.Total.TwentyFourHoursPlus++
			chBuckets.TwentyFourHoursPlus++
		}

		// Legacy 4 buckets for backward compatibility
		if diff < 15*time.Minute {
			report.Under15m++
		} else if diff < time.Hour {
			report.Under1h++
		} else if diff < 4*time.Hour {
			report.Under4h++
		} else {
			report.Over4h++
		}
	}

	for chType, b := range channelMap {
		report.Channels = append(report.Channels, ChannelFRTDistribution{
			ChannelType:  chType,
			Distribution: *b,
		})
	}
	return report, nil
}

// GetConversationsForExport exports conversations matching filter
func (s *ReportService) GetConversationsForExport(accountID uint, filters ...ReportFilter) ([]domain.Conversation, error) {
	var filter ReportFilter
	if len(filters) > 0 {
		filter = filters[0]
	}
	convs, _, err := s.loadAccountConversations(accountID, filter)
	return convs, err
}
