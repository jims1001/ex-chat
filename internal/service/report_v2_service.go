package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// GetLiveConversationMetrics returns real-time open/unattended/unassigned/pending conversation metrics
func (s *ReportService) GetLiveConversationMetrics(accountID uint, teamID *uint) (*LiveMetricsReport, error) {
	conversations, err := s.conversations.ListForReport(accountID, nil, nil)
	if err != nil {
		return nil, err
	}
	result := &LiveMetricsReport{}
	for _, conversation := range conversations {
		if teamID != nil && *teamID > 0 && (conversation.TeamID == nil || *conversation.TeamID != *teamID) {
			continue
		}
		switch conversation.Status {
		case domain.ConversationStatusOpen:
			result.Open++
			if conversation.AssigneeID == nil || *conversation.AssigneeID == 0 {
				result.Unassigned++
			}
			if conversation.UnreadCount > 0 || conversation.AgentLastSeenAt == nil {
				result.Unattended++
			}
		case domain.ConversationStatusPending:
			result.Pending++
		}
	}
	return result, nil
}

// GetGroupedLiveMetrics returns real-time metrics grouped by team_id, assignee_id, or inbox_id
func (s *ReportService) GetGroupedLiveMetrics(accountID uint, groupBy string, teamID *uint) ([]GroupedLiveMetricItem, error) {
	conversations, err := s.conversations.ListForReport(accountID, nil, nil)
	if err != nil {
		return nil, err
	}
	groupBy = strings.ToLower(strings.TrimSpace(groupBy))
	groups := map[uint]*GroupedLiveMetricItem{}
	for _, conversation := range conversations {
		if conversation.Status != domain.ConversationStatusOpen || (teamID != nil && *teamID > 0 && (conversation.TeamID == nil || *conversation.TeamID != *teamID)) {
			continue
		}
		var id uint
		switch groupBy {
		case "team_id", "team":
			if conversation.TeamID != nil {
				id = *conversation.TeamID
			}
		case "inbox_id", "inbox":
			id = conversation.InboxID
		default:
			if conversation.AssigneeID != nil {
				id = *conversation.AssigneeID
			}
		}
		if id == 0 {
			continue
		}
		item := groups[id]
		if item == nil {
			item = &GroupedLiveMetricItem{}
			groups[id] = item
		}
		item.Open++
		if conversation.AssigneeID == nil || *conversation.AssigneeID == 0 {
			item.Unassigned++
		}
		if conversation.UnreadCount > 0 || conversation.AgentLastSeenAt == nil {
			item.Unattended++
		}
	}
	ids := make([]int, 0, len(groups))
	for id := range groups {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	result := make([]GroupedLiveMetricItem, 0, len(ids))
	for _, raw := range ids {
		id := uint(raw)
		item := groups[id]
		switch groupBy {
		case "team_id", "team":
			item.TeamID = &id
		case "inbox_id", "inbox":
			item.InboxID = &id
		default:
			item.AssigneeID = &id
		}
		result = append(result, *item)
	}
	return result, nil
}

// GetBotMetrics aggregates performance statistics for automated agent bots
func (s *ReportService) GetBotMetrics(accountID uint, filter ReportFilter) (*BotMetricsReport, error) {
	inboxes, err := s.inboxes.ListByAccount(accountID)
	if err != nil {
		return nil, err
	}
	inboxIDs := make([]uint, 0, len(inboxes))
	for _, inbox := range inboxes {
		inboxIDs = append(inboxIDs, inbox.ID)
	}
	botInboxes, err := s.bots.ListBoundInboxIDs(context.Background(), accountID)
	if err != nil {
		return nil, err
	}
	owned := uintSet(inboxIDs)
	botInboxes = filterUintIDs(botInboxes, owned)
	allowed := uintSet(botInboxes)
	conversations, err := s.conversations.ListForReport(accountID, filter.Since, filter.Until)
	if err != nil {
		return nil, err
	}
	selected := []domain.Conversation{}
	ids := []uint{}
	for _, c := range conversations {
		if allowed[c.InboxID] {
			selected = append(selected, c)
			ids = append(ids, c.ID)
		}
	}
	messages, err := s.messages.ListForReport(accountID, ids, filter.Since, filter.Until)
	if err != nil {
		return nil, err
	}
	var msgCount int64
	for _, m := range messages {
		if m.MessageType == domain.MessageTypeOutgoing {
			msgCount++
		}
	}
	outcomes, err := s.getBotOutcomeCounts(accountID, filter)
	if err != nil {
		return nil, err
	}
	total := int64(len(selected))
	var rr, hr float64
	if total > 0 {
		rr = RoundToOneDecimal(float64(outcomes.BotResolutionsCount) / float64(total) * 100)
		hr = RoundToOneDecimal(float64(outcomes.BotHandoffsCount) / float64(total) * 100)
	}
	return &BotMetricsReport{ConversationCount: total, MessageCount: msgCount, ResolutionRate: rr, HandoffRate: hr}, nil
}

func filterUintIDs(ids []uint, allowed map[uint]bool) []uint {
	result := make([]uint, 0, len(ids))
	for _, id := range ids {
		if allowed[id] {
			result = append(result, id)
		}
	}
	return result
}

// botReportingEventsUnavailable keeps malformed stored associations and query
// details out of the HTTP/log error string while retaining dependency error identity.
type botReportingEventsUnavailable struct {
	cause error
}

func (e botReportingEventsUnavailable) Error() string { return "bot reporting events are unavailable" }
func (e botReportingEventsUnavailable) Unwrap() error { return e.cause }

// getBotOutcomeCounts uses account-scoped events in the requested event window.
// This is intentionally independent of the creation-time denominator cohort.
// A direct positive conversation_id is required; metadata remains opaque.
func (s *ReportService) getBotOutcomeCounts(accountID uint, filter ReportFilter) (BotSummaryMetrics, error) {
	var events []struct {
		ConversationID *uint
		Name           string
	}
	query := s.db.Session(&gorm.Session{Logger: gormlogger.Discard}).
		Model(&domain.ReportingEvent{}).
		Distinct("conversation_id", "name").
		Where("account_id = ? AND name IN ?", accountID, []string{"conversation_bot_resolved", "conversation_bot_handoff"})
	query = applyBaseTimeFilter(query, filter, "created_at")
	if err := query.Find(&events).Error; err != nil {
		return BotSummaryMetrics{}, botReportingEventsUnavailable{cause: err}
	}
	resolutions, handoffs := map[uint]bool{}, map[uint]bool{}
	for _, event := range events {
		if event.ConversationID == nil || *event.ConversationID == 0 {
			return BotSummaryMetrics{}, botReportingEventsUnavailable{}
		}
		if event.Name == "conversation_bot_handoff" {
			handoffs[*event.ConversationID] = true
		} else {
			resolutions[*event.ConversationID] = true
		}
	}
	for id := range handoffs {
		delete(resolutions, id)
	}
	return BotSummaryMetrics{BotResolutionsCount: int64(len(resolutions)), BotHandoffsCount: int64(len(handoffs))}, nil
}

// GetBotSummary provides exact current and previous event counts without
// reconstructing integers from rounded percentages.
func (s *ReportService) GetBotSummary(accountID uint, filter ReportFilter) (*BotSummaryReport, error) {
	current, err := s.getBotOutcomeCounts(accountID, filter)
	if err != nil {
		return nil, err
	}

	previous := BotSummaryMetrics{}
	if filter.Since != nil {
		prevFilter := filter
		pUntil := *filter.Since
		pSince := pUntil.AddDate(0, 0, -7)
		if filter.Until != nil {
			pSince = filter.Since.Add(-filter.Until.Sub(*filter.Since))
		}
		prevFilter.Since, prevFilter.Until = &pSince, &pUntil
		previous, err = s.getBotOutcomeCounts(accountID, prevFilter)
		if err != nil {
			return nil, err
		}
	}
	return &BotSummaryReport{
		BotResolutionsCount: current.BotResolutionsCount,
		BotHandoffsCount:    current.BotHandoffsCount,
		Previous:            previous,
	}, nil
}

// GetChannelSummary breaks down conversation states across channel types
func (s *ReportService) GetChannelSummary(accountID uint, filter ReportFilter) (map[string]ChannelSummaryStats, error) {
	conversations, err := s.conversations.ListForReport(accountID, filter.Since, filter.Until)
	if err != nil {
		return nil, err
	}
	inboxes, err := s.inboxes.ListByAccount(accountID)
	if err != nil {
		return nil, err
	}
	byID := map[uint]string{}
	result := map[string]ChannelSummaryStats{}
	for _, inbox := range inboxes {
		channel := inbox.ChannelType
		if channel == "" {
			channel = "Channel::WebWidget"
		}
		byID[inbox.ID] = channel
		result[channel] = result[channel]
	}
	for _, conversation := range conversations {
		channel := byID[conversation.InboxID]
		if channel == "" {
			channel = "Channel::WebWidget"
		}
		stats := result[channel]
		switch conversation.Status {
		case domain.ConversationStatusOpen:
			stats.Open++
		case domain.ConversationStatusResolved:
			stats.Resolved++
		case domain.ConversationStatusPending:
			stats.Pending++
		case domain.ConversationStatusSnoozed:
			stats.Snoozed++
		}
		stats.Total++
		result[channel] = stats
	}
	return result, nil
}

// GetInboxLabelMatrix builds a cross-matrix between inboxes and labels
func (s *ReportService) GetInboxLabelMatrix(accountID uint, filter ReportFilter, inboxIDs, labelIDs []uint) (*InboxLabelMatrixReport, error) {
	inboxes, err := s.inboxes.ListByAccount(accountID)
	if err != nil {
		return nil, err
	}
	labels, err := s.labels.List(accountID)
	if err != nil {
		return nil, err
	}
	allowInbox := uintSet(inboxIDs)
	allowLabel := uintSet(labelIDs)
	inboxes = filterInboxes(inboxes, allowInbox)
	labels = filterLabels(labels, allowLabel)
	sort.Slice(inboxes, func(i, j int) bool { return inboxes[i].Name < inboxes[j].Name })
	sort.Slice(labels, func(i, j int) bool { return labels[i].Title < labels[j].Title })
	conversations, err := s.conversations.ListForReport(accountID, filter.Since, filter.Until)
	if err != nil {
		return nil, err
	}
	convInbox := map[uint]uint{}
	ids := []uint{}
	for _, c := range conversations {
		if len(allowInbox) == 0 || allowInbox[c.InboxID] {
			convInbox[c.ID] = c.InboxID
			ids = append(ids, c.ID)
		}
	}
	relations, err := s.conversations.ListLabelRelationsForReport(accountID, ids)
	if err != nil {
		return nil, err
	}
	counts := map[string]int64{}
	for _, relation := range relations {
		if len(allowLabel) > 0 && !allowLabel[relation.LabelID] {
			continue
		}
		counts[fmt.Sprintf("%d_%d", convInbox[relation.ConversationID], relation.LabelID)]++
	}
	ie := make([]MatrixEntity, len(inboxes))
	matrix := make([][]int64, len(inboxes))
	le := make([]MatrixEntity, len(labels))
	for j, l := range labels {
		le[j] = MatrixEntity{ID: l.ID, Title: l.Title}
	}
	for i, in := range inboxes {
		ie[i] = MatrixEntity{ID: in.ID, Name: in.Name}
		matrix[i] = make([]int64, len(labels))
		for j, l := range labels {
			matrix[i][j] = counts[fmt.Sprintf("%d_%d", in.ID, l.ID)]
		}
	}
	return &InboxLabelMatrixReport{Inboxes: ie, Labels: le, Matrix: matrix}, nil
}

func uintSet(ids []uint) map[uint]bool {
	result := map[uint]bool{}
	for _, id := range ids {
		result[id] = true
	}
	return result
}
func filterInboxes(items []domain.Inbox, allowed map[uint]bool) []domain.Inbox {
	if len(allowed) == 0 {
		return items
	}
	out := []domain.Inbox{}
	for _, item := range items {
		if allowed[item.ID] {
			out = append(out, item)
		}
	}
	return out
}
func filterLabels(items []domain.Label, allowed map[uint]bool) []domain.Label {
	if len(allowed) == 0 {
		return items
	}
	out := []domain.Label{}
	for _, item := range items {
		if allowed[item.ID] {
			out = append(out, item)
		}
	}
	return out
}

// GetOutgoingMessagesCount aggregates outgoing messages by agent, team, inbox, or label
func (s *ReportService) GetOutgoingMessagesCount(accountID uint, filter ReportFilter, groupBy string) ([]OutgoingMessagesCountItem, error) {
	conversations, err := s.conversations.ListForReport(accountID, nil, nil)
	if err != nil {
		return nil, err
	}
	convByID := map[uint]domain.Conversation{}
	ids := []uint{}
	for _, c := range conversations {
		convByID[c.ID] = c
		ids = append(ids, c.ID)
	}
	messages, err := s.messages.ListForReport(accountID, ids, filter.Since, filter.Until)
	if err != nil {
		return nil, err
	}
	counts := map[uint]int64{}
	names := map[uint]string{}
	groupBy = strings.ToLower(strings.TrimSpace(groupBy))
	switch groupBy {
	case "agent", "user":
		users, loadErr := s.users.FindByIDs(senderIDs(messages))
		if loadErr != nil {
			return nil, loadErr
		}
		for _, u := range users {
			names[u.ID] = u.Name
		}
		for _, m := range messages {
			if m.MessageType == domain.MessageTypeOutgoing && m.SenderType == domain.SenderTypeUser {
				counts[m.SenderID]++
			}
		}
	case "team":
		teams, loadErr := s.teams.List(accountID)
		if loadErr != nil {
			return nil, loadErr
		}
		for _, t := range teams {
			names[t.ID] = t.Name
		}
		for _, m := range messages {
			if m.MessageType == domain.MessageTypeOutgoing {
				if c, ok := convByID[m.ConversationID]; ok && c.TeamID != nil {
					counts[*c.TeamID]++
				}
			}
		}
	case "inbox":
		inboxes, e := s.inboxes.ListByAccount(accountID)
		if e != nil {
			return nil, e
		}
		for _, i := range inboxes {
			names[i.ID] = i.Name
		}
		for _, m := range messages {
			if m.MessageType == domain.MessageTypeOutgoing {
				if c, ok := convByID[m.ConversationID]; ok {
					counts[c.InboxID]++
				}
			}
		}
	case "label":
		labels, e := s.labels.List(accountID)
		if e != nil {
			return nil, e
		}
		for _, l := range labels {
			names[l.ID] = l.Title
		}
		relations, e := s.conversations.ListLabelRelationsForReport(accountID, ids)
		if e != nil {
			return nil, e
		}
		byConv := map[uint][]uint{}
		for _, r := range relations {
			byConv[r.ConversationID] = append(byConv[r.ConversationID], r.LabelID)
		}
		for _, m := range messages {
			if m.MessageType == domain.MessageTypeOutgoing {
				for _, id := range byConv[m.ConversationID] {
					counts[id]++
				}
			}
		}
	}
	keys := make([]int, 0, len(counts))
	for id := range counts {
		keys = append(keys, int(id))
	}
	sort.Ints(keys)
	items := make([]OutgoingMessagesCountItem, 0, len(keys))
	for _, raw := range keys {
		id := uint(raw)
		items = append(items, OutgoingMessagesCountItem{ID: id, Name: names[id], OutgoingMessagesCount: counts[id]})
	}
	return items, nil
}
func senderIDs(messages []domain.Message) []uint {
	seen := map[uint]bool{}
	ids := []uint{}
	for _, m := range messages {
		if m.SenderType == domain.SenderTypeUser && !seen[m.SenderID] {
			seen[m.SenderID] = true
			ids = append(ids, m.SenderID)
		}
	}
	return ids
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

	convs, err := s.conversations.ListForReport(accountID, &startDate, &endDate)
	if err != nil {
		return nil, err
	}

	// Enumerate UTC calendar labels after the existing numeric display shift.
	// Keep query bounds absolute, and include only days intersecting [start,end).
	offset := time.Duration(timezoneOffset * float64(time.Hour))
	dates := []string{}
	if endDate.After(startDate) {
		displayStart, displayEnd := startDate.UTC().Add(offset), endDate.UTC().Add(offset)
		for cur := reportUTCDayStart(displayStart); cur.Before(displayEnd); cur = cur.AddDate(0, 0, 1) {
			dates = append(dates, cur.Format("2006-01-02"))
		}
	}

	// Counts matrix: counts[hour][date]
	counts := make(map[int]map[string]int64)
	for h := 0; h < 24; h++ {
		counts[h] = make(map[string]int64)
	}

	for _, c := range convs {
		locTime := c.CreatedAt.UTC().Add(offset)
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
	end := time.Date(year+1, 1, 1, 0, 0, 0, 0, time.UTC)

	conversations, err := s.conversations.ListForReport(accountID, &start, &end)
	if err != nil {
		return nil, err
	}
	var totalConvs int64
	dayCounts := map[string]int64{}
	for _, conversation := range conversations {
		if conversation.AssigneeID != nil && *conversation.AssigneeID == userID {
			totalConvs++
			dayCounts[conversation.CreatedAt.UTC().Format("2006-01-02")]++
		}
	}
	type dayCount struct {
		Day   string
		Count int64
	}
	var days []dayCount
	for day, count := range dayCounts {
		days = append(days, dayCount{Day: day, Count: count})
	}
	sort.Slice(days, func(i, j int) bool {
		if days[i].Count == days[j].Count {
			return days[i].Day < days[j].Day
		}
		return days[i].Count > days[j].Count
	})

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
	responseEvents := s.db.Model(&domain.ReportingEvent{}).
		Select("COALESCE(AVG(value), 0)").
		Where("account_id = ? AND user_id = ? AND name = ?", accountID, userID, "first_response")
	if err := applyBaseTimeFilter(responseEvents, ReportFilter{Since: &start, Until: &end}, "created_at").Scan(&avgSeconds).Error; err != nil {
		return nil, err
	}

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
	start := reportUTCDayStart(bucketTime)
	end := start.AddDate(0, 0, 1)
	conversations, err := s.conversations.ListForReport(accountID, &start, &end)
	if err != nil {
		return nil, err
	}
	matches := func(c domain.Conversation) bool {
		if dimensionID == nil || *dimensionID == 0 {
			return true
		}
		switch strings.ToLower(dimensionType) {
		case "inbox":
			return c.InboxID == *dimensionID
		case "agent":
			return c.AssigneeID != nil && *c.AssigneeID == *dimensionID
		case "team":
			return c.TeamID != nil && *c.TeamID == *dimensionID
		}
		return true
	}
	selected := []domain.Conversation{}
	ids := []uint{}
	for _, c := range conversations {
		if matches(c) && (metric != "resolutions_count" || c.Status == domain.ConversationStatusResolved) {
			selected = append(selected, c)
			ids = append(ids, c.ID)
		}
	}
	isMessage := metric == "incoming_messages_count" || metric == "outgoing_messages_count"
	records := []any{}
	var total, convCount int64
	if isMessage {
		messages, e := s.messages.ListForReport(accountID, ids, &start, &end)
		if e != nil {
			return nil, e
		}
		wanted := domain.MessageTypeIncoming
		if metric == "outgoing_messages_count" {
			wanted = domain.MessageTypeOutgoing
		}
		filtered := []domain.Message{}
		seen := map[uint]bool{}
		for _, m := range messages {
			if m.MessageType == wanted {
				filtered = append(filtered, m)
				seen[m.ConversationID] = true
			}
		}
		sort.Slice(filtered, func(i, j int) bool { return filtered[i].CreatedAt.After(filtered[j].CreatedAt) })
		total = int64(len(filtered))
		convCount = int64(len(seen))
		from := (page - 1) * perPage
		to := from + perPage
		if from > len(filtered) {
			from = len(filtered)
		}
		if to > len(filtered) {
			to = len(filtered)
		}
		for _, m := range filtered[from:to] {
			records = append(records, m)
		}
	} else {
		sort.Slice(selected, func(i, j int) bool { return selected[i].CreatedAt.After(selected[j].CreatedAt) })
		total = int64(len(selected))
		convCount = total
		from := (page - 1) * perPage
		to := from + perPage
		if from > len(selected) {
			from = len(selected)
		}
		if to > len(selected) {
			to = len(selected)
		}
		for _, c := range selected[from:to] {
			records = append(records, c)
		}
	}
	recordType := "Conversation"
	if isMessage {
		recordType = "Message"
	}
	return &DrilldownReport{Meta: DrilldownMeta{Metric: metric, RecordType: recordType, Bucket: DrilldownMetaBucket{Since: start.Unix(), Until: end.Unix()}, CurrentPage: page, PerPage: perPage, TotalCount: total, ConversationCount: convCount}, Payload: records}, nil
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

	accountUsers, err := s.accounts.ListMembers(accountID)
	if err != nil {
		return nil, err
	}
	offset := (page - 1) * perPage
	if offset > len(accountUsers) {
		offset = len(accountUsers)
	}
	end := offset + perPage
	if end > len(accountUsers) {
		end = len(accountUsers)
	}
	accountUsers = accountUsers[offset:end]

	metrics := make([]AgentLiveConversationMetric, 0, len(accountUsers))
	for _, au := range accountUsers {
		if au.User == nil {
			continue
		}
		uID := au.UserID
		m, err := s.GetLiveConversationMetrics(accountID, nil)
		if err != nil {
			return nil, err
		}
		// Custom open/unattended for this specific agent
		var agentOpen, agentUnattended int64
		conversations, loadErr := s.conversations.ListForReport(accountID, nil, nil)
		if loadErr != nil {
			return nil, loadErr
		}
		for _, conversation := range conversations {
			if conversation.AssigneeID != nil && *conversation.AssigneeID == uID && conversation.Status == domain.ConversationStatusOpen {
				agentOpen++
				if conversation.UnreadCount > 0 || conversation.AgentLastSeenAt == nil {
					agentUnattended++
				}
			}
		}

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
