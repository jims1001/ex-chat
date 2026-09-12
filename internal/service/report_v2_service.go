package service

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// GetLiveConversationMetrics returns real-time open/unattended/unassigned/pending conversation metrics
func (s *ReportService) GetLiveConversationMetrics(accountID uint, teamID *uint) (*LiveMetricsReport, error) {
	q := s.db.Model(&domain.Conversation{}).Where("account_id = ?", accountID)
	if teamID != nil && *teamID > 0 {
		q = q.Where("team_id = ?", *teamID)
	}

	var openCount, unassignedCount, unattendedCount, pendingCount int64

	// 1. Open count
	_ = q.Session(&gorm.Session{}).Where("status = ?", domain.ConversationStatusOpen).Count(&openCount).Error

	// 2. Unassigned (in open conversations)
	_ = q.Session(&gorm.Session{}).Where("status = ? AND (assignee_id IS NULL OR assignee_id = 0)", domain.ConversationStatusOpen).Count(&unassignedCount).Error

	// 3. Unattended: open conversations where unread_count > 0 or agent_last_seen_at is nil
	_ = q.Session(&gorm.Session{}).Where("status = ? AND (unread_count > 0 OR agent_last_seen_at IS NULL)", domain.ConversationStatusOpen).Count(&unattendedCount).Error

	// 4. Pending count
	_ = q.Session(&gorm.Session{}).Where("status = ?", domain.ConversationStatusPending).Count(&pendingCount).Error

	return &LiveMetricsReport{
		Open:       openCount,
		Unattended: unattendedCount,
		Unassigned: unassignedCount,
		Pending:    pendingCount,
	}, nil
}

// GetGroupedLiveMetrics returns real-time metrics grouped by team_id, assignee_id, or inbox_id
func (s *ReportService) GetGroupedLiveMetrics(accountID uint, groupBy string, teamID *uint) ([]GroupedLiveMetricItem, error) {
	col := "assignee_id"
	switch strings.ToLower(strings.TrimSpace(groupBy)) {
	case "team_id", "team":
		col = "team_id"
	case "inbox_id", "inbox":
		col = "inbox_id"
	default:
		col = "assignee_id"
	}

	type groupRow struct {
		GroupID    uint  `gorm:"column:group_id"`
		Open       int64 `gorm:"column:open_cnt"`
		Unassigned int64 `gorm:"column:unassigned_cnt"`
		Unattended int64 `gorm:"column:unattended_cnt"`
	}

	var rows []groupRow
	query := s.db.Model(&domain.Conversation{}).
		Select(fmt.Sprintf("%s AS group_id, "+
			"COUNT(id) AS open_cnt, "+
			"SUM(CASE WHEN assignee_id IS NULL OR assignee_id = 0 THEN 1 ELSE 0 END) AS unassigned_cnt, "+
			"SUM(CASE WHEN unread_count > 0 OR agent_last_seen_at IS NULL THEN 1 ELSE 0 END) AS unattended_cnt", col)).
		Where("account_id = ? AND status = ? AND "+col+" IS NOT NULL AND "+col+" > 0", accountID, domain.ConversationStatusOpen)

	if teamID != nil && *teamID > 0 {
		query = query.Where("team_id = ?", *teamID)
	}

	if err := query.Group(col).Scan(&rows).Error; err != nil {
		return nil, err
	}

	res := make([]GroupedLiveMetricItem, 0, len(rows))
	for _, r := range rows {
		item := GroupedLiveMetricItem{
			Open:       r.Open,
			Unattended: r.Unattended,
			Unassigned: r.Unassigned,
		}
		gID := r.GroupID
		switch col {
		case "team_id":
			item.TeamID = &gID
		case "inbox_id":
			item.InboxID = &gID
		default:
			item.AssigneeID = &gID
		}
		res = append(res, item)
	}
	return res, nil
}

// GetBotMetrics aggregates performance statistics for automated agent bots
func (s *ReportService) GetBotMetrics(accountID uint, filter ReportFilter) (*BotMetricsReport, error) {
	// Find bot-enabled inboxes
	var botInboxes []uint
	_ = s.db.Model(&domain.AgentBotInbox{}).
		Joins("JOIN inboxes ON inboxes.id = agent_bot_inboxes.inbox_id").
		Where("inboxes.account_id = ?", accountID).
		Pluck("agent_bot_inboxes.inbox_id", &botInboxes).Error

	var totalConvs int64
	cq := s.db.Model(&domain.Conversation{}).Where("account_id = ?", accountID)
	cq = applyBaseTimeFilter(cq, filter, "created_at")
	if len(botInboxes) > 0 {
		cq = cq.Where("inbox_id IN (?)", botInboxes)
	}
	_ = cq.Count(&totalConvs).Error

	// Bot messages: outgoing messages where sender_type = 'AgentBot' or in bot inboxes
	var msgCount int64
	mq := s.db.Model(&domain.Message{}).
		Joins("JOIN conversations ON conversations.id = messages.conversation_id").
		Where("messages.account_id = ? AND messages.message_type = ?", accountID, domain.MessageTypeOutgoing)
	mq = applyBaseTimeFilter(mq, filter, "messages.created_at")
	if len(botInboxes) > 0 {
		mq = mq.Where("conversations.inbox_id IN (?)", botInboxes)
	}
	_ = mq.Count(&msgCount).Error

	// Bot resolutions: reporting_events with name = 'conversation_bot_resolved' or status = 'resolved' with no human assignee
	var botResolutions int64
	eq := s.db.Model(&domain.ReportingEvent{}).Where("account_id = ? AND name = ?", accountID, "conversation_bot_resolved")
	eq = applyBaseTimeFilter(eq, filter, "created_at")
	_ = eq.Count(&botResolutions).Error

	if botResolutions == 0 && totalConvs > 0 {
		// Fallback to conversations resolved without human assignee
		s.db.Model(&domain.Conversation{}).
			Where("account_id = ? AND status = ? AND (assignee_id IS NULL OR assignee_id = 0)", accountID, domain.ConversationStatusResolved).
			Count(&botResolutions)
	}

	// Bot handoffs: reporting_events with name = 'conversation_bot_handoff'
	var botHandoffs int64
	hq := s.db.Model(&domain.ReportingEvent{}).Where("account_id = ? AND name = ?", accountID, "conversation_bot_handoff")
	hq = applyBaseTimeFilter(hq, filter, "created_at")
	_ = hq.Count(&botHandoffs).Error

	if botHandoffs == 0 && totalConvs > 0 {
		s.db.Model(&domain.Conversation{}).
			Where("account_id = ? AND assignee_id IS NOT NULL AND assignee_id > 0", accountID).
			Count(&botHandoffs)
	}

	var resRate, handoffRate float64
	if totalConvs > 0 {
		resRate = RoundToOneDecimal(float64(botResolutions) / float64(totalConvs) * 100.0)
		handoffRate = RoundToOneDecimal(float64(botHandoffs) / float64(totalConvs) * 100.0)
	}

	return &BotMetricsReport{
		ConversationCount: totalConvs,
		MessageCount:      msgCount,
		ResolutionRate:    resRate,
		HandoffRate:       handoffRate,
	}, nil
}

// GetBotSummary provides current vs previous period comparison for bot resolutions and handoffs
func (s *ReportService) GetBotSummary(accountID uint, filter ReportFilter) (*BotSummaryReport, error) {
	currentMetrics, err := s.GetBotMetrics(accountID, filter)
	if err != nil {
		return nil, err
	}

	// Calculate previous period
	var prevFilter ReportFilter
	if filter.Since != nil && filter.Until != nil {
		diff := filter.Until.Sub(*filter.Since)
		pSince := filter.Since.Add(-diff)
		pUntil := *filter.Since
		prevFilter.Since = &pSince
		prevFilter.Until = &pUntil
	} else if filter.Since != nil {
		pUntil := *filter.Since
		pSince := pUntil.AddDate(0, 0, -7)
		prevFilter.Since = &pSince
		prevFilter.Until = &pUntil
	}

	var prevResolutions, prevHandoffs int64
	if prevFilter.Since != nil {
		prevMetrics, _ := s.GetBotMetrics(accountID, prevFilter)
		if prevMetrics != nil {
			prevResolutions = int64(prevMetrics.ResolutionRate * float64(prevMetrics.ConversationCount) / 100.0)
			prevHandoffs = int64(prevMetrics.HandoffRate * float64(prevMetrics.ConversationCount) / 100.0)
		}
	}

	curRes := int64(currentMetrics.ResolutionRate * float64(currentMetrics.ConversationCount) / 100.0)
	curHandoff := int64(currentMetrics.HandoffRate * float64(currentMetrics.ConversationCount) / 100.0)

	return &BotSummaryReport{
		BotResolutionsCount: curRes,
		BotHandoffsCount:    curHandoff,
		Previous: BotSummaryMetrics{
			BotResolutionsCount: prevResolutions,
			BotHandoffsCount:    prevHandoffs,
		},
	}, nil
}

// GetChannelSummary breaks down conversation states across channel types
func (s *ReportService) GetChannelSummary(accountID uint, filter ReportFilter) (map[string]ChannelSummaryStats, error) {
	type row struct {
		ChannelType string `gorm:"column:channel_type"`
		Status      string `gorm:"column:status"`
		Count       int64  `gorm:"column:cnt"`
	}

	var rows []row
	q := s.db.Model(&domain.Conversation{}).
		Select("inboxes.channel_type, conversations.status, COUNT(conversations.id) as cnt").
		Joins("JOIN inboxes ON inboxes.id = conversations.inbox_id").
		Where("conversations.account_id = ?", accountID)
	q = applyBaseTimeFilter(q, filter, "conversations.created_at")

	if err := q.Group("inboxes.channel_type, conversations.status").Scan(&rows).Error; err != nil {
		return nil, err
	}

	res := make(map[string]ChannelSummaryStats)
	for _, r := range rows {
		ch := r.ChannelType
		if ch == "" {
			ch = "Channel::WebWidget"
		}
		stats := res[ch]
		switch r.Status {
		case domain.ConversationStatusOpen:
			stats.Open += r.Count
		case domain.ConversationStatusResolved:
			stats.Resolved += r.Count
		case domain.ConversationStatusPending:
			stats.Pending += r.Count
		case domain.ConversationStatusSnoozed:
			stats.Snoozed += r.Count
		}
		stats.Total += r.Count
		res[ch] = stats
	}

	// Ensure standard channels are present if empty
	if len(res) == 0 {
		var inboxes []domain.Inbox
		_ = s.db.Where("account_id = ?", accountID).Find(&inboxes).Error
		for _, inb := range inboxes {
			ch := inb.ChannelType
			if ch == "" {
				ch = "Channel::WebWidget"
			}
			if _, ok := res[ch]; !ok {
				res[ch] = ChannelSummaryStats{}
			}
		}
	}
	return res, nil
}

// GetInboxLabelMatrix builds a cross-matrix between inboxes and labels
func (s *ReportService) GetInboxLabelMatrix(accountID uint, filter ReportFilter, inboxIDs, labelIDs []uint) (*InboxLabelMatrixReport, error) {
	var inboxes []domain.Inbox
	iq := s.db.Where("account_id = ?", accountID)
	if len(inboxIDs) > 0 {
		iq = iq.Where("id IN (?)", inboxIDs)
	}
	if err := iq.Order("name ASC").Find(&inboxes).Error; err != nil {
		return nil, err
	}

	var labels []domain.Label
	lq := s.db.Where("account_id = ?", accountID)
	if len(labelIDs) > 0 {
		lq = lq.Where("id IN (?)", labelIDs)
	}
	if err := lq.Order("title ASC").Find(&labels).Error; err != nil {
		return nil, err
	}

	type countRow struct {
		InboxID uint  `gorm:"column:inbox_id"`
		LabelID uint  `gorm:"column:label_id"`
		Count   int64 `gorm:"column:cnt"`
	}

	var counts []countRow
	cq := s.db.Model(&domain.ConversationLabel{}).
		Select("conversations.inbox_id, conversation_labels.label_id, COUNT(DISTINCT conversations.id) AS cnt").
		Joins("JOIN conversations ON conversations.id = conversation_labels.conversation_id").
		Where("conversations.account_id = ?", accountID)
	cq = applyBaseTimeFilter(cq, filter, "conversations.created_at")

	if len(inboxIDs) > 0 {
		cq = cq.Where("conversations.inbox_id IN (?)", inboxIDs)
	}
	if len(labelIDs) > 0 {
		cq = cq.Where("conversation_labels.label_id IN (?)", labelIDs)
	}

	_ = cq.Group("conversations.inbox_id, conversation_labels.label_id").Scan(&counts).Error

	lookup := make(map[string]int64)
	for _, c := range counts {
		lookup[fmt.Sprintf("%d_%d", c.InboxID, c.LabelID)] = c.Count
	}

	inboxEntities := make([]MatrixEntity, len(inboxes))
	for i, inb := range inboxes {
		inboxEntities[i] = MatrixEntity{ID: inb.ID, Name: inb.Name}
	}

	labelEntities := make([]MatrixEntity, len(labels))
	for j, lbl := range labels {
		labelEntities[j] = MatrixEntity{ID: lbl.ID, Title: lbl.Title}
	}

	matrix := make([][]int64, len(inboxes))
	for i, inb := range inboxes {
		matrix[i] = make([]int64, len(labels))
		for j, lbl := range labels {
			matrix[i][j] = lookup[fmt.Sprintf("%d_%d", inb.ID, lbl.ID)]
		}
	}

	return &InboxLabelMatrixReport{
		Inboxes: inboxEntities,
		Labels:  labelEntities,
		Matrix:  matrix,
	}, nil
}

// GetOutgoingMessagesCount aggregates outgoing messages by agent, team, inbox, or label
func (s *ReportService) GetOutgoingMessagesCount(accountID uint, filter ReportFilter, groupBy string) ([]OutgoingMessagesCountItem, error) {
	groupBy = strings.ToLower(strings.TrimSpace(groupBy))
	var items []OutgoingMessagesCountItem

	switch groupBy {
	case "agent", "user":
		type row struct {
			ID    uint   `gorm:"column:id"`
			Name  string `gorm:"column:name"`
			Count int64  `gorm:"column:cnt"`
		}
		var rows []row
		q := s.db.Model(&domain.Message{}).
			Select("users.id, users.name, COUNT(messages.id) as cnt").
			Joins("JOIN users ON users.id = messages.sender_id").
			Where("messages.account_id = ? AND messages.sender_type = ? AND messages.message_type = ?", accountID, domain.SenderTypeUser, domain.MessageTypeOutgoing)
		q = applyBaseTimeFilter(q, filter, "messages.created_at")
		_ = q.Group("users.id, users.name").Scan(&rows).Error

		for _, r := range rows {
			items = append(items, OutgoingMessagesCountItem{
				ID:                    r.ID,
				Name:                  r.Name,
				OutgoingMessagesCount: r.Count,
			})
		}

	case "team":
		type row struct {
			ID    uint   `gorm:"column:id"`
			Name  string `gorm:"column:name"`
			Count int64  `gorm:"column:cnt"`
		}
		var rows []row
		q := s.db.Model(&domain.Message{}).
			Select("teams.id, teams.name, COUNT(messages.id) as cnt").
			Joins("JOIN conversations ON conversations.id = messages.conversation_id").
			Joins("JOIN teams ON teams.id = conversations.team_id").
			Where("messages.account_id = ? AND messages.message_type = ?", accountID, domain.MessageTypeOutgoing)
		q = applyBaseTimeFilter(q, filter, "messages.created_at")
		_ = q.Group("teams.id, teams.name").Scan(&rows).Error

		for _, r := range rows {
			items = append(items, OutgoingMessagesCountItem{
				ID:                    r.ID,
				Name:                  r.Name,
				OutgoingMessagesCount: r.Count,
			})
		}

	case "inbox":
		type row struct {
			ID    uint   `gorm:"column:id"`
			Name  string `gorm:"column:name"`
			Count int64  `gorm:"column:cnt"`
		}
		var rows []row
		q := s.db.Model(&domain.Message{}).
			Select("inboxes.id, inboxes.name, COUNT(messages.id) as cnt").
			Joins("JOIN conversations ON conversations.id = messages.conversation_id").
			Joins("JOIN inboxes ON inboxes.id = conversations.inbox_id").
			Where("messages.account_id = ? AND messages.message_type = ?", accountID, domain.MessageTypeOutgoing)
		q = applyBaseTimeFilter(q, filter, "messages.created_at")
		_ = q.Group("inboxes.id, inboxes.name").Scan(&rows).Error

		for _, r := range rows {
			items = append(items, OutgoingMessagesCountItem{
				ID:                    r.ID,
				Name:                  r.Name,
				OutgoingMessagesCount: r.Count,
			})
		}

	case "label":
		type row struct {
			ID    uint   `gorm:"column:id"`
			Name  string `gorm:"column:name"`
			Count int64  `gorm:"column:cnt"`
		}
		var rows []row
		q := s.db.Model(&domain.Message{}).
			Select("labels.id, labels.title AS name, COUNT(messages.id) as cnt").
			Joins("JOIN conversation_labels ON conversation_labels.conversation_id = messages.conversation_id").
			Joins("JOIN labels ON labels.id = conversation_labels.label_id").
			Where("messages.account_id = ? AND messages.message_type = ?", accountID, domain.MessageTypeOutgoing)
		q = applyBaseTimeFilter(q, filter, "messages.created_at")
		_ = q.Group("labels.id, labels.title").Scan(&rows).Error

		for _, r := range rows {
			items = append(items, OutgoingMessagesCountItem{
				ID:                    r.ID,
				Name:                  r.Name,
				OutgoingMessagesCount: r.Count,
			})
		}
	}

	return items, nil
}

// GetConversationTraffic produces an hourly heatmap matrix across dates
func (s *ReportService) GetConversationTraffic(accountID uint, filter ReportFilter, timezoneOffset float64) ([][]any, error) {
	startDate := time.Now().UTC().AddDate(0, 0, -6)
	startDate = time.Date(startDate.Year(), startDate.Month(), startDate.Day(), 0, 0, 0, 0, time.UTC)
	endDate := time.Now().UTC()

	if filter.Since != nil {
		startDate = *filter.Since
	}
	if filter.Until != nil {
		endDate = *filter.Until
	}

	var convs []domain.Conversation
	_ = s.db.Where("account_id = ? AND created_at >= ? AND created_at <= ?", accountID, startDate, endDate).
		Order("created_at ASC").
		Find(&convs).Error

	// Generate list of unique dates
	dateMap := make(map[string]bool)
	cur := startDate
	for !cur.After(endDate) {
		dateMap[cur.Format("2006-01-02")] = true
		cur = cur.AddDate(0, 0, 1)
	}

	var dates []string
	for d := range dateMap {
		dates = append(dates, d)
	}
	sort.Strings(dates)

	// Counts matrix: counts[hour][date]
	counts := make(map[int]map[string]int64)
	for h := 0; h < 24; h++ {
		counts[h] = make(map[string]int64)
	}

	for _, c := range convs {
		locTime := c.CreatedAt.Add(time.Duration(timezoneOffset * float64(time.Hour)))
		dStr := locTime.Format("2006-01-02")
		h := locTime.Hour()
		if counts[h] != nil {
			counts[h][dStr]++
		}
	}

	// Build result array
	var result [][]any
	headerRow := []any{"Start of the hour"}
	for _, d := range dates {
		headerRow = append(headerRow, d)
	}
	result = append(result, headerRow)

	for h := 0; h < 24; h++ {
		row := []any{fmt.Sprintf("%02d:00", h)}
		for _, d := range dates {
			row = append(row, counts[h][d])
		}
		result = append(result, row)
	}

	return result, nil
}

// GetYearInReview computes personal year in review statistics for an agent
func (s *ReportService) GetYearInReview(accountID, userID uint, year int) (*YearInReviewReport, error) {
	if year <= 0 {
		year = time.Now().Year()
	}

	start := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(year, 12, 31, 23, 59, 59, 999999999, time.UTC)

	var totalConvs int64
	_ = s.db.Model(&domain.Conversation{}).
		Where("account_id = ? AND assignee_id = ? AND created_at >= ? AND created_at <= ?", accountID, userID, start, end).
		Count(&totalConvs).Error

	// Busiest day
	type dayCount struct {
		Day   string `gorm:"column:day"`
		Count int64  `gorm:"column:cnt"`
	}
	var days []dayCount
	_ = s.db.Model(&domain.Conversation{}).
		Select("strftime('%Y-%m-%d', created_at) AS day, COUNT(id) AS cnt").
		Where("account_id = ? AND assignee_id = ? AND created_at >= ? AND created_at <= ?", accountID, userID, start, end).
		Group("strftime('%Y-%m-%d', created_at)").
		Order("cnt DESC").
		Limit(1).
		Scan(&days).Error

	var busiestDay *YearInReviewBusiestDay
	if len(days) > 0 && days[0].Count > 0 {
		t, err := time.Parse("2006-01-02", days[0].Day)
		displayDate := days[0].Day
		if err == nil {
			displayDate = t.Format("Jan 02")
		}
		busiestDay = &YearInReviewBusiestDay{
			Date:  displayDate,
			Count: days[0].Count,
		}
	}

	// Average response time
	var avgSeconds float64
	_ = s.db.Model(&domain.ReportingEvent{}).
		Select("COALESCE(AVG(value), 0)").
		Where("account_id = ? AND user_id = ? AND name = ? AND created_at >= ? AND created_at <= ?", accountID, userID, "first_response", start, end).
		Scan(&avgSeconds).Error

	return &YearInReviewReport{
		Year:               year,
		TotalConversations: totalConvs,
		BusiestDay:         busiestDay,
		SupportPersonality: &YearInReviewSupportPersonality{
			AvgResponseTimeSeconds: int(avgSeconds),
		},
	}, nil
}

// GetDrilldown fetches granular records for a specific reporting bucket
func (s *ReportService) GetDrilldown(accountID uint, metric, dimensionType string, dimensionID *uint, bucketTime time.Time, filter ReportFilter, page, perPage int) (*DrilldownReport, error) {
	if page <= 0 {
		page = 1
	}
	if perPage <= 0 {
		perPage = 25
	}
	if perPage > 100 {
		perPage = 100
	}

	bucketStart := time.Date(bucketTime.Year(), bucketTime.Month(), bucketTime.Day(), 0, 0, 0, 0, time.UTC)
	bucketEnd := bucketStart.Add(24 * time.Hour).Add(-time.Nanosecond)

	var totalCount int64
	var convCount int64
	var records []any

	isMessageMetric := metric == "incoming_messages_count" || metric == "outgoing_messages_count"

	if isMessageMetric {
		msgType := domain.MessageTypeIncoming
		if metric == "outgoing_messages_count" {
			msgType = domain.MessageTypeOutgoing
		}

		mq := s.db.Model(&domain.Message{}).
			Joins("JOIN conversations ON conversations.id = messages.conversation_id").
			Where("messages.account_id = ? AND messages.message_type = ? AND messages.created_at >= ? AND messages.created_at <= ?",
				accountID, msgType, bucketStart, bucketEnd)

		if dimensionID != nil && *dimensionID > 0 {
			switch strings.ToLower(dimensionType) {
			case "inbox":
				mq = mq.Where("conversations.inbox_id = ?", *dimensionID)
			case "agent":
				mq = mq.Where("conversations.assignee_id = ?", *dimensionID)
			case "team":
				mq = mq.Where("conversations.team_id = ?", *dimensionID)
			}
		}

		_ = mq.Count(&totalCount).Error
		_ = mq.Select("COUNT(DISTINCT messages.conversation_id)").Scan(&convCount).Error

		var msgs []domain.Message
		offset := (page - 1) * perPage
		_ = mq.Preload("Conversation").Order("messages.created_at DESC").Offset(offset).Limit(perPage).Find(&msgs).Error

		for _, m := range msgs {
			records = append(records, m)
		}
	} else {
		// Conversation metric
		cq := s.db.Model(&domain.Conversation{}).
			Where("account_id = ? AND created_at >= ? AND created_at <= ?", accountID, bucketStart, bucketEnd)

		if metric == "resolutions_count" {
			cq = cq.Where("status = ?", domain.ConversationStatusResolved)
		}

		if dimensionID != nil && *dimensionID > 0 {
			switch strings.ToLower(dimensionType) {
			case "inbox":
				cq = cq.Where("inbox_id = ?", *dimensionID)
			case "agent":
				cq = cq.Where("assignee_id = ?", *dimensionID)
			case "team":
				cq = cq.Where("team_id = ?", *dimensionID)
			}
		}

		_ = cq.Count(&totalCount).Error
		convCount = totalCount

		var convs []domain.Conversation
		offset := (page - 1) * perPage
		_ = cq.Preload("Assignee").Preload("Contact").Preload("Inbox").
			Order("created_at DESC").Offset(offset).Limit(perPage).Find(&convs).Error

		for _, c := range convs {
			records = append(records, c)
		}
	}

	recordType := "Conversation"
	if isMessageMetric {
		recordType = "Message"
	}

	return &DrilldownReport{
		Meta: DrilldownMeta{
			Metric:     metric,
			RecordType: recordType,
			Bucket: DrilldownMetaBucket{
				Since: bucketStart.Unix(),
				Until: bucketEnd.Unix(),
			},
			CurrentPage:       page,
			PerPage:           perPage,
			TotalCount:        totalCount,
			ConversationCount: convCount,
		},
		Payload: records,
	}, nil
}

// GetConversationsReport returns live conversation metrics by account or per-agent breakdown
func (s *ReportService) GetConversationsReport(accountID uint, reportType string, page, perPage int) (any, error) {
	if strings.ToLower(reportType) == "account" || reportType == "" {
		return s.GetLiveConversationMetrics(accountID, nil)
	}

	// Agent metrics breakdown
	if page <= 0 {
		page = 1
	}
	if perPage <= 0 {
		perPage = 25
	}

	var accountUsers []domain.AccountUser
	offset := (page - 1) * perPage
	err := s.db.Preload("User").
		Where("account_id = ?", accountID).
		Offset(offset).Limit(perPage).
		Find(&accountUsers).Error
	if err != nil {
		return nil, err
	}

	metrics := make([]AgentLiveConversationMetric, 0, len(accountUsers))
	for _, au := range accountUsers {
		if au.User == nil {
			continue
		}
		uID := au.UserID
		m, err := s.GetLiveConversationMetrics(accountID, nil)
		if err != nil {
			continue
		}
		// Custom open/unattended for this specific agent
		var agentOpen, agentUnattended int64
		_ = s.db.Model(&domain.Conversation{}).
			Where("account_id = ? AND assignee_id = ? AND status = ?", accountID, uID, domain.ConversationStatusOpen).
			Count(&agentOpen).Error
		_ = s.db.Model(&domain.Conversation{}).
			Where("account_id = ? AND assignee_id = ? AND status = ? AND (unread_count > 0 OR agent_last_seen_at IS NULL)", accountID, uID, domain.ConversationStatusOpen).
			Count(&agentUnattended).Error

		m.Open = agentOpen
		m.Unattended = agentUnattended
		m.Unassigned = 0

		metrics = append(metrics, AgentLiveConversationMetric{
			ID:           au.User.ID,
			Name:         au.User.Name,
			Email:        au.User.Email,
			Thumbnail:    au.User.AvatarURL,
			Availability: au.Availability,
			Metric:       *m,
		})
	}
	return metrics, nil
}
