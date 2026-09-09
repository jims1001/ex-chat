package service

import (
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type ReportService struct {
	db *gorm.DB
}

func NewReportService(db *gorm.DB) *ReportService {
	return &ReportService{db: db}
}

func applyBaseTimeFilter(tx *gorm.DB, filter ReportFilter, timeCol string) *gorm.DB {
	if filter.Since != nil {
		tx = tx.Where(timeCol+" >= ?", *filter.Since)
	}
	if filter.Until != nil {
		tx = tx.Where(timeCol+" <= ?", *filter.Until)
	}
	return tx
}

// GetAccountSummary compiles holistic account-level metrics with advanced definitions
func (s *ReportService) GetAccountSummary(accountID uint, filters ...ReportFilter) (*AccountSummaryReport, error) {
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
	messagesMap := s.loadMessagesForConversations(convIDs)

	var openCount, resolvedCount, pendingCount int64
	var frtSum, rtSum, artSum float64
	var frtCount, rtCount, artCount int

	for _, c := range convs {
		switch c.Status {
		case domain.ConversationStatusOpen:
			openCount++
		case domain.ConversationStatusResolved:
			resolvedCount++
		case domain.ConversationStatusPending:
			pendingCount++
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

	totalConvs := int64(len(convs))
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
	if totalConvs > 0 {
		resRate = RoundToOneDecimal((float64(resolvedCount) / float64(totalConvs)) * 100.0)
	}

	// Message and Contact Counts
	var totalMessages, incomingMessages, outgoingMessages int64
	if len(convIDs) > 0 {
		for _, msgs := range messagesMap {
			for _, m := range msgs {
				totalMessages++
				if m.MessageType == domain.MessageTypeIncoming {
					incomingMessages++
				} else if m.MessageType == domain.MessageTypeOutgoing {
					outgoingMessages++
				}
			}
		}
	} else if !filter.BusinessHours {
		qMsg := s.db.Model(&domain.Message{}).Where("account_id = ?", accountID)
		qMsg = applyBaseTimeFilter(qMsg, filter, "created_at")
		qMsg.Count(&totalMessages)

		qIn := s.db.Model(&domain.Message{}).Where("account_id = ? AND message_type = ?", accountID, domain.MessageTypeIncoming)
		qIn = applyBaseTimeFilter(qIn, filter, "created_at")
		qIn.Count(&incomingMessages)

		qOut := s.db.Model(&domain.Message{}).Where("account_id = ? AND message_type = ?", accountID, domain.MessageTypeOutgoing)
		qOut = applyBaseTimeFilter(qOut, filter, "created_at")
		qOut.Count(&outgoingMessages)
	}

	var totalContacts int64
	qContact := s.db.Model(&domain.Contact{}).Where("account_id = ?", accountID)
	qContact = applyBaseTimeFilter(qContact, filter, "created_at")
	qContact.Count(&totalContacts)

	// FCR and Bot handling metrics
	fcrRate, botResolved, botHandoff := CalculateFCRAndBotMetrics(convs, messagesMap)

	summary := &AccountSummaryReport{
		TotalConversations:         totalConvs,
		OpenConversations:          openCount,
		ResolvedConversations:      resolvedCount,
		PendingConversations:       pendingCount,
		TotalMessages:              totalMessages,
		TotalContacts:              totalContacts,
		ConversationsCount:         totalConvs,
		IncomingMessagesCount:      incomingMessages,
		OutgoingMessagesCount:      outgoingMessages,
		ResolutionsCount:           resolvedCount,
		ResolutionRate:             resRate,
		AvgFirstResponseTime:       avgFRT,
		AvgResolutionTime:          avgRT,
		AvgReplyTime:               avgReply,
		FirstContactResolutionRate: fcrRate,
		BotResolutionsCount:        botResolved,
		BotHandoffsCount:           botHandoff,
	}
	return summary, nil
}

// GetConversationTrends compiles volume trends over specified day count
func (s *ReportService) GetConversationTrends(accountID uint, days int) (*ConversationTrendsReport, error) {
	if days <= 0 {
		days = 7
	}
	now := time.Now().UTC()
	startCurrent := now.AddDate(0, 0, -days)
	startPrevious := now.AddDate(0, 0, -2*days)

	var currentCount int64
	s.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND created_at >= ? AND created_at < ?", accountID, startCurrent, now).
		Count(&currentCount)

	var previousCount int64
	s.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND created_at >= ? AND created_at < ?", accountID, startPrevious, startCurrent).
		Count(&previousCount)

	var growth float64
	if previousCount > 0 {
		growth = float64(currentCount-previousCount) / float64(previousCount) * 100.0
	} else if currentCount > 0 {
		growth = 100.0
	}

	trends := make([]TrendPoint, 0, days)
	for i := days - 1; i >= 0; i-- {
		dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -i)
		dayEnd := dayStart.AddDate(0, 0, 1)
		var c int64
		s.db.Model(&domain.Conversation{}).
			Where("account_id = ? AND created_at >= ? AND created_at < ?", accountID, dayStart, dayEnd).
			Count(&c)
		trends = append(trends, TrendPoint{
			Date:  dayStart.Format("2006-01-02"),
			Count: c,
		})
	}

	return &ConversationTrendsReport{
		CurrentPeriodTotal:  currentCount,
		PreviousPeriodTotal: previousCount,
		GrowthPercentage:    growth,
		Trends:              trends,
	}, nil
}
