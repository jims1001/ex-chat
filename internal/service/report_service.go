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

type AccountSummaryReport struct {
	TotalConversations    int64 `json:"total_conversations"`
	OpenConversations     int64 `json:"open_conversations"`
	ResolvedConversations int64 `json:"resolved_conversations"`
	PendingConversations  int64 `json:"pending_conversations"`
	TotalMessages         int64 `json:"total_messages"`
	TotalContacts         int64 `json:"total_contacts"`
}

type AgentMetric struct {
	UserID                uint   `json:"user_id"`
	Name                  string `json:"name"`
	Email                 string `json:"email"`
	AssignedConversations int64  `json:"assigned_conversations"`
	ResolvedConversations int64  `json:"resolved_conversations"`
}

type TrendPoint struct {
	Date  string `json:"date"`
	Count int64  `json:"count"`
}

type ConversationTrendsReport struct {
	CurrentPeriodTotal  int64        `json:"current_period_total"`
	PreviousPeriodTotal int64        `json:"previous_period_total"`
	GrowthPercentage    float64      `json:"growth_percentage"`
	Trends              []TrendPoint `json:"trends"`
}

type TeamMetric struct {
	TeamID                uint   `json:"team_id"`
	Name                  string `json:"name"`
	MemberCount           int    `json:"member_count"`
	AssignedConversations int64  `json:"assigned_conversations"`
	ResolvedConversations int64  `json:"resolved_conversations"`
}

type InboxMetric struct {
	InboxID               uint   `json:"inbox_id"`
	Name                  string `json:"name"`
	ChannelType           string `json:"channel_type"`
	TotalConversations    int64  `json:"total_conversations"`
	OpenConversations     int64  `json:"open_conversations"`
	ResolvedConversations int64  `json:"resolved_conversations"`
}

type LabelMetric struct {
	LabelID           uint   `json:"label_id"`
	Title             string `json:"title"`
	Color             string `json:"color"`
	ConversationCount int64  `json:"conversation_count"`
}

type ReportFilter struct {
	Since         *time.Time
	Until         *time.Time
	BusinessHours bool
}

func applyReportFilter(tx *gorm.DB, filter ReportFilter, timeCol string) *gorm.DB {
	if filter.Since != nil {
		tx = tx.Where(timeCol+" >= ?", *filter.Since)
	}
	if filter.Until != nil {
		tx = tx.Where(timeCol+" <= ?", *filter.Until)
	}
	if filter.BusinessHours {
		tx = tx.Where("strftime('%w', " + timeCol + ") NOT IN ('0', '6') AND strftime('%H', " + timeCol + ") >= '09' AND strftime('%H', " + timeCol + ") < '18'")
	}
	return tx
}

type FirstResponseDistributionBuckets struct {
	ZeroToOneHour       int64 `json:"0_to_1h"`
	OneToFourHours      int64 `json:"1_to_4h"`
	FourToEightHours    int64 `json:"4_to_8h"`
	EightToTwentyFour   int64 `json:"8_to_24h"`
	TwentyFourHoursPlus int64 `json:"24h_plus"`
}

type ChannelFRTDistribution struct {
	ChannelType  string                           `json:"channel_type"`
	Distribution FirstResponseDistributionBuckets `json:"distribution"`
}

type FirstResponseDistributionReport struct {
	Total    FirstResponseDistributionBuckets `json:"total"`
	Channels []ChannelFRTDistribution         `json:"channels"`
	Under15m int64                            `json:"under_15m"`
	Under1h  int64                            `json:"under_1h"`
	Under4h  int64                            `json:"under_4h"`
	Over4h   int64                            `json:"over_4h"`
}

func (s *ReportService) GetAccountSummary(accountID uint, filters ...ReportFilter) (*AccountSummaryReport, error) {
	var summary AccountSummaryReport
	var filter ReportFilter
	if len(filters) > 0 {
		filter = filters[0]
	}

	qConv := s.db.Model(&domain.Conversation{}).Where("account_id = ?", accountID)
	qConv = applyReportFilter(qConv, filter, "created_at")
	qConv.Count(&summary.TotalConversations)

	qOpen := s.db.Model(&domain.Conversation{}).Where("account_id = ? AND status = ?", accountID, domain.ConversationStatusOpen)
	qOpen = applyReportFilter(qOpen, filter, "created_at")
	qOpen.Count(&summary.OpenConversations)

	qRes := s.db.Model(&domain.Conversation{}).Where("account_id = ? AND status = ?", accountID, domain.ConversationStatusResolved)
	qRes = applyReportFilter(qRes, filter, "created_at")
	qRes.Count(&summary.ResolvedConversations)

	qPend := s.db.Model(&domain.Conversation{}).Where("account_id = ? AND status = ?", accountID, domain.ConversationStatusPending)
	qPend = applyReportFilter(qPend, filter, "created_at")
	qPend.Count(&summary.PendingConversations)

	qMsg := s.db.Model(&domain.Message{}).Where("account_id = ?", accountID)
	qMsg = applyReportFilter(qMsg, filter, "created_at")
	qMsg.Count(&summary.TotalMessages)

	qContact := s.db.Model(&domain.Contact{}).Where("account_id = ?", accountID)
	qContact = applyReportFilter(qContact, filter, "created_at")
	qContact.Count(&summary.TotalContacts)

	return &summary, nil
}

func (s *ReportService) GetAgentMetrics(accountID uint, filters ...ReportFilter) ([]AgentMetric, error) {
	var filter ReportFilter
	if len(filters) > 0 {
		filter = filters[0]
	}

	var members []domain.AccountUser
	if err := s.db.Preload("User").Where("account_id = ?", accountID).Find(&members).Error; err != nil {
		return nil, err
	}

	metrics := make([]AgentMetric, 0, len(members))
	for _, m := range members {
		if m.User == nil {
			continue
		}

		var assignedCount int64
		var resolvedCount int64

		qAssigned := s.db.Model(&domain.Conversation{}).
			Where("account_id = ? AND assignee_id = ?", accountID, m.UserID)
		qAssigned = applyReportFilter(qAssigned, filter, "created_at")
		qAssigned.Count(&assignedCount)

		qResolved := s.db.Model(&domain.Conversation{}).
			Where("account_id = ? AND assignee_id = ? AND status = ?", accountID, m.UserID, domain.ConversationStatusResolved)
		qResolved = applyReportFilter(qResolved, filter, "created_at")
		qResolved.Count(&resolvedCount)

		metrics = append(metrics, AgentMetric{
			UserID:                m.UserID,
			Name:                  m.User.Name,
			Email:                 m.User.Email,
			AssignedConversations: assignedCount,
			ResolvedConversations: resolvedCount,
		})
	}

	return metrics, nil
}

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

func (s *ReportService) GetTeamMetrics(accountID uint, filters ...ReportFilter) ([]TeamMetric, error) {
	var filter ReportFilter
	if len(filters) > 0 {
		filter = filters[0]
	}

	var teams []domain.Team
	if err := s.db.Where("account_id = ?", accountID).Find(&teams).Error; err != nil {
		return nil, err
	}

	metrics := make([]TeamMetric, 0, len(teams))
	for _, t := range teams {
		var memberIDs []uint
		s.db.Model(&domain.TeamMember{}).Where("team_id = ?", t.ID).Pluck("user_id", &memberIDs)

		var assignedCount int64
		var resolvedCount int64

		qAssigned := s.db.Model(&domain.Conversation{}).Where("account_id = ?", accountID)
		if len(memberIDs) > 0 {
			qAssigned = qAssigned.Where("team_id = ? OR assignee_id IN (?)", t.ID, memberIDs)
		} else {
			qAssigned = qAssigned.Where("team_id = ?", t.ID)
		}
		qAssigned = applyReportFilter(qAssigned, filter, "created_at")
		qAssigned.Count(&assignedCount)

		qResolved := s.db.Model(&domain.Conversation{}).Where("account_id = ? AND status = ?", accountID, domain.ConversationStatusResolved)
		if len(memberIDs) > 0 {
			qResolved = qResolved.Where("team_id = ? OR assignee_id IN (?)", t.ID, memberIDs)
		} else {
			qResolved = qResolved.Where("team_id = ?", t.ID)
		}
		qResolved = applyReportFilter(qResolved, filter, "created_at")
		qResolved.Count(&resolvedCount)

		metrics = append(metrics, TeamMetric{
			TeamID:                t.ID,
			Name:                  t.Name,
			MemberCount:           len(memberIDs),
			AssignedConversations: assignedCount,
			ResolvedConversations: resolvedCount,
		})
	}
	return metrics, nil
}

func (s *ReportService) GetInboxMetrics(accountID uint, filters ...ReportFilter) ([]InboxMetric, error) {
	var filter ReportFilter
	if len(filters) > 0 {
		filter = filters[0]
	}

	var inboxes []domain.Inbox
	if err := s.db.Where("account_id = ?", accountID).Find(&inboxes).Error; err != nil {
		return nil, err
	}

	metrics := make([]InboxMetric, 0, len(inboxes))
	for _, inb := range inboxes {
		var total int64
		var open int64
		var resolved int64

		qTot := s.db.Model(&domain.Conversation{}).Where("account_id = ? AND inbox_id = ?", accountID, inb.ID)
		qTot = applyReportFilter(qTot, filter, "created_at")
		qTot.Count(&total)

		qOpen := s.db.Model(&domain.Conversation{}).Where("account_id = ? AND inbox_id = ? AND status = ?", accountID, inb.ID, domain.ConversationStatusOpen)
		qOpen = applyReportFilter(qOpen, filter, "created_at")
		qOpen.Count(&open)

		qRes := s.db.Model(&domain.Conversation{}).Where("account_id = ? AND inbox_id = ? AND status = ?", accountID, inb.ID, domain.ConversationStatusResolved)
		qRes = applyReportFilter(qRes, filter, "created_at")
		qRes.Count(&resolved)

		metrics = append(metrics, InboxMetric{
			InboxID:               inb.ID,
			Name:                  inb.Name,
			ChannelType:           inb.ChannelType,
			TotalConversations:    total,
			OpenConversations:     open,
			ResolvedConversations: resolved,
		})
	}
	return metrics, nil
}

func (s *ReportService) GetLabelMetrics(accountID uint, filters ...ReportFilter) ([]LabelMetric, error) {
	var filter ReportFilter
	if len(filters) > 0 {
		filter = filters[0]
	}

	var labels []domain.Label
	if err := s.db.Where("account_id = ?", accountID).Find(&labels).Error; err != nil {
		return nil, err
	}

	metrics := make([]LabelMetric, 0, len(labels))
	for _, l := range labels {
		var count int64
		q := s.db.Model(&domain.ConversationLabel{}).
			Joins("JOIN conversations ON conversations.id = conversation_labels.conversation_id").
			Where("conversations.account_id = ? AND conversation_labels.label_id = ?", accountID, l.ID)
		q = applyReportFilter(q, filter, "conversations.created_at")
		q.Count(&count)

		metrics = append(metrics, LabelMetric{
			LabelID:           l.ID,
			Title:             l.Title,
			Color:             l.Color,
			ConversationCount: count,
		})
	}
	return metrics, nil
}

func (s *ReportService) GetFirstResponseDistribution(accountID uint, filters ...ReportFilter) (*FirstResponseDistributionReport, error) {
	var filter ReportFilter
	if len(filters) > 0 {
		filter = filters[0]
	}

	report := &FirstResponseDistributionReport{
		Channels: make([]ChannelFRTDistribution, 0),
	}

	channelMap := make(map[string]*FirstResponseDistributionBuckets)

	q := s.db.Where("account_id = ?", accountID)
	q = applyReportFilter(q, filter, "created_at")

	var convs []domain.Conversation
	if err := q.Find(&convs).Error; err != nil {
		return nil, err
	}

	for _, c := range convs {
		var inb domain.Inbox
		_ = s.db.Where("id = ?", c.InboxID).First(&inb)
		chanType := inb.ChannelType
		if chanType == "" {
			chanType = "Channel::WebWidget"
		}
		if _, exists := channelMap[chanType]; !exists {
			channelMap[chanType] = &FirstResponseDistributionBuckets{}
		}
		chBuckets := channelMap[chanType]

		var firstIn domain.Message
		errIn := s.db.Where("conversation_id = ? AND message_type = ?", c.ID, domain.MessageTypeIncoming).
			Order("id ASC").First(&firstIn).Error
		if errIn != nil {
			continue
		}

		var firstOut domain.Message
		errOut := s.db.Where("conversation_id = ? AND message_type = ? AND (private = 0 OR private = false OR private IS NULL) AND created_at >= ?",
			c.ID, domain.MessageTypeOutgoing, firstIn.CreatedAt).
			Order("id ASC").First(&firstOut).Error
		if errOut != nil {
			continue
		}

		diff := firstOut.CreatedAt.Sub(firstIn.CreatedAt)

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

		// Legacy 4 buckets for backward compatibility:
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

func (s *ReportService) GetConversationsForExport(accountID uint, filters ...ReportFilter) ([]domain.Conversation, error) {
	var filter ReportFilter
	if len(filters) > 0 {
		filter = filters[0]
	}

	var convs []domain.Conversation
	q := s.db.Where("account_id = ?", accountID)
	q = applyReportFilter(q, filter, "created_at")
	err := q.Order("id DESC").Find(&convs).Error
	return convs, err
}

