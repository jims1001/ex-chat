package repository

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type CaptainRepository struct {
	db *gorm.DB
}

func NewCaptainRepository(db *gorm.DB) *CaptainRepository {
	return &CaptainRepository{db: db}
}

// ----------------- Captain Assistants -----------------

func (r *CaptainRepository) FindByID(accountID, id uint) (*domain.CaptainAssistant, error) {
	var assistant domain.CaptainAssistant
	err := r.db.Preload("Inboxes").Where("account_id = ? AND id = ?", accountID, id).First(&assistant).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &assistant, nil
}

func (r *CaptainRepository) ListByAccount(accountID uint) ([]domain.CaptainAssistant, error) {
	var assistants []domain.CaptainAssistant
	err := r.db.Preload("Inboxes").Where("account_id = ?", accountID).Order("id DESC").Find(&assistants).Error
	return assistants, err
}

func (r *CaptainRepository) Create(assistant *domain.CaptainAssistant) error {
	return r.db.Create(assistant).Error
}

func (r *CaptainRepository) Update(assistant *domain.CaptainAssistant) error {
	return r.db.Save(assistant).Error
}

func (r *CaptainRepository) Delete(accountID, id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Clean up inbox associations
		if err := tx.Where("account_id = ? AND captain_assistant_id = ?", accountID, id).Delete(&domain.CaptainInbox{}).Error; err != nil {
			return err
		}
		// Clean up FAQ responses
		if err := tx.Where("account_id = ? AND assistant_id = ?", accountID, id).Delete(&domain.CaptainAssistantResponse{}).Error; err != nil {
			return err
		}
		// Delete assistant
		return tx.Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.CaptainAssistant{}).Error
	})
}

// ----------------- Inbox Bindings -----------------

func (r *CaptainRepository) ListBoundInboxes(accountID, assistantID uint) ([]domain.Inbox, error) {
	var inboxes []domain.Inbox
	err := r.db.Table("inboxes").
		Joins("INNER JOIN captain_inboxes ON captain_inboxes.inbox_id = inboxes.id").
		Where("captain_inboxes.account_id = ? AND captain_inboxes.captain_assistant_id = ?", accountID, assistantID).
		Find(&inboxes).Error
	return inboxes, err
}

func (r *CaptainRepository) BindInbox(accountID, assistantID, inboxID uint) (*domain.CaptainInbox, error) {
	// Verify inbox belongs to this account
	var inbox domain.Inbox
	if err := r.db.Where("account_id = ? AND id = ?", accountID, inboxID).First(&inbox).Error; err != nil {
		return nil, fmt.Errorf("inbox not found: %w", err)
	}

	var binding domain.CaptainInbox
	err := r.db.Where("account_id = ? AND captain_assistant_id = ? AND inbox_id = ?", accountID, assistantID, inboxID).First(&binding).Error
	if err == nil {
		binding.Inbox = &inbox
		return &binding, nil
	}

	binding = domain.CaptainInbox{
		AccountID:          accountID,
		CaptainAssistantID: assistantID,
		InboxID:            inboxID,
	}
	if err := r.db.Create(&binding).Error; err != nil {
		return nil, err
	}
	binding.Inbox = &inbox
	return &binding, nil
}

func (r *CaptainRepository) UnbindInbox(accountID, assistantID, inboxID uint) error {
	return r.db.Where("account_id = ? AND captain_assistant_id = ? AND inbox_id = ?", accountID, assistantID, inboxID).
		Delete(&domain.CaptainInbox{}).Error
}

func (r *CaptainRepository) GetBoundInboxIDs(accountID, assistantID uint) ([]uint, error) {
	var ids []uint
	err := r.db.Model(&domain.CaptainInbox{}).
		Where("account_id = ? AND captain_assistant_id = ?", accountID, assistantID).
		Pluck("inbox_id", &ids).Error
	return ids, err
}

// FindAssistantByInbox retrieves the CaptainAssistant bound to an inbox
func (r *CaptainRepository) FindAssistantByInbox(accountID, inboxID uint) (*domain.CaptainAssistant, error) {
	var binding domain.CaptainInbox
	err := r.db.Where("account_id = ? AND inbox_id = ?", accountID, inboxID).First(&binding).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return r.FindByID(accountID, binding.CaptainAssistantID)
}

// ----------------- FAQ / Assistant Responses -----------------

func (r *CaptainRepository) FindResponseByID(accountID, id uint) (*domain.CaptainAssistantResponse, error) {
	var resp domain.CaptainAssistantResponse
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&resp).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &resp, nil
}

func (r *CaptainRepository) ListResponses(accountID, assistantID uint, status, search string, page, perPage int) ([]domain.CaptainAssistantResponse, int64, error) {
	if page <= 0 {
		page = 1
	}
	if perPage <= 0 || perPage > 100 {
		perPage = 25
	}

	query := r.db.Model(&domain.CaptainAssistantResponse{}).Where("account_id = ?", accountID)
	if assistantID > 0 {
		query = query.Where("assistant_id = ?", assistantID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if search != "" {
		term := "%" + strings.ToLower(search) + "%"
		query = query.Where("LOWER(question) LIKE ? OR LOWER(answer) LIKE ?", term, term)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var responses []domain.CaptainAssistantResponse
	offset := (page - 1) * perPage
	err := query.Order("id DESC").Offset(offset).Limit(perPage).Find(&responses).Error
	return responses, total, err
}

func (r *CaptainRepository) CreateResponse(resp *domain.CaptainAssistantResponse) error {
	return r.db.Create(resp).Error
}

func (r *CaptainRepository) UpdateResponse(resp *domain.CaptainAssistantResponse) error {
	return r.db.Save(resp).Error
}

func (r *CaptainRepository) DeleteResponse(accountID, id uint) error {
	return r.db.Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.CaptainAssistantResponse{}).Error
}

func (r *CaptainRepository) SearchMatchingFAQs(accountID, assistantID uint, query string, limit int) ([]domain.CaptainAssistantResponse, error) {
	if limit <= 0 {
		limit = 5
	}
	cleanQuery := strings.ToLower(strings.TrimSpace(query))
	if cleanQuery == "" {
		return nil, nil
	}

	baseQuery := r.db.Model(&domain.CaptainAssistantResponse{}).
		Where("account_id = ? AND status = 'active'", accountID)
	if assistantID > 0 {
		baseQuery = baseQuery.Where("assistant_id = ?", assistantID)
	}

	var allResponses []domain.CaptainAssistantResponse
	if err := baseQuery.Find(&allResponses).Error; err != nil {
		return nil, err
	}

	type Scored struct {
		resp  domain.CaptainAssistantResponse
		score int
	}

	var scoredList []Scored
	tokens := strings.Fields(cleanQuery)
	queryRunes := []rune(cleanQuery)

	for _, resp := range allResponses {
		score := 0
		qLower := strings.ToLower(resp.Question)
		aLower := strings.ToLower(resp.Answer)

		// 1. Direct contains
		if strings.Contains(cleanQuery, qLower) || strings.Contains(qLower, cleanQuery) {
			score += 20
		}

		// 2. Space tokens (English / words)
		for _, token := range tokens {
			if len(token) > 1 {
				if strings.Contains(qLower, token) {
					score += 5
				}
				if strings.Contains(aLower, token) {
					score += 2
				}
			}
		}

		// 3. CJK / N-gram matching (2-character shingles)
		qRunes := []rune(qLower)
		for i := 0; i < len(qRunes)-1; i++ {
			bigram := string(qRunes[i : i+2])
			if strings.ContainsAny(bigram, " ，。！？；：、“”‘’（）《》[]{}()!?.,;:") {
				continue
			}
			if strings.Contains(cleanQuery, bigram) {
				score += 3
			}
		}

		for i := 0; i < len(queryRunes)-1; i++ {
			bigram := string(queryRunes[i : i+2])
			if strings.ContainsAny(bigram, " ，。！？；：、“”‘’（）《》[]{}()!?.,;:") {
				continue
			}
			if strings.Contains(aLower, bigram) {
				score += 1
			}
		}

		if score > 0 {
			scoredList = append(scoredList, Scored{resp: resp, score: score})
		}
	}

	// Sort by score desc
	for i := 0; i < len(scoredList); i++ {
		for j := i + 1; j < len(scoredList); j++ {
			if scoredList[j].score > scoredList[i].score {
				scoredList[i], scoredList[j] = scoredList[j], scoredList[i]
			}
		}
	}

	var results []domain.CaptainAssistantResponse
	for i := 0; i < len(scoredList) && i < limit; i++ {
		results = append(results, scoredList[i].resp)
	}

	return results, nil
}

// ----------------- Stats, Summary, Drilldown -----------------

type MetricPack struct {
	Value         any `json:"value"`
	PreviousValue any `json:"previous_value,omitempty"`
	Change        any `json:"change,omitempty"`
}

type AssistantStatsResult struct {
	ConversationsHandled MetricPack       `json:"conversations_handled"`
	AutoResolutionRate   MetricPack       `json:"auto_resolution_rate"`
	HandoffRate          MetricPack       `json:"handoff_rate"`
	HoursSaved           MetricPack       `json:"hours_saved"`
	ReopenRate           MetricPack       `json:"reopen_rate"`
	ConversationDepth    MetricPack       `json:"conversation_depth"`
	Knowledge            map[string]int64 `json:"knowledge"`
}

func parseDateRange(timeRange string) (time.Time, time.Time, time.Time) {
	now := time.Now()
	days := 30
	switch timeRange {
	case "7":
		days = 7
	case "30":
		days = 30
	case "90":
		days = 90
	case "this_month":
		days = now.Day()
		if days < 1 {
			days = 1
		}
	case "last_month":
		days = 30
	}

	currentStart := now.AddDate(0, 0, -days)
	prevStart := currentStart.AddDate(0, 0, -days)
	return currentStart, now, prevStart
}

func (r *CaptainRepository) getHandledConversationQuery(accountID, assistantID uint, start, end time.Time) *gorm.DB {
	inboxIDs, _ := r.GetBoundInboxIDs(accountID, assistantID)

	q := r.db.Model(&domain.Conversation{}).
		Where("conversations.account_id = ? AND conversations.created_at >= ? AND conversations.created_at <= ?", accountID, start, end)

	if len(inboxIDs) > 0 {
		q = q.Where("conversations.inbox_id IN (?) OR conversations.id IN (?)",
			inboxIDs,
			r.db.Model(&domain.Message{}).
				Select("conversation_id").
				Where("account_id = ? AND (sender_type = 'CaptainAssistant' OR sender_type = 'Captain::Assistant') AND sender_id = ?", accountID, assistantID),
		)
	} else {
		q = q.Where("conversations.id IN (?)",
			r.db.Model(&domain.Message{}).
				Select("conversation_id").
				Where("account_id = ? AND (sender_type = 'CaptainAssistant' OR sender_type = 'Captain::Assistant') AND sender_id = ?", accountID, assistantID),
		)
	}
	return q
}

func (r *CaptainRepository) GetStats(accountID, assistantID uint, timeRange string, tzOffset int) (*AssistantStatsResult, error) {
	curStart, curEnd, prevStart := parseDateRange(timeRange)
	prevEnd := curStart

	// 1. Current Window
	var curHandled int64
	_ = r.getHandledConversationQuery(accountID, assistantID, curStart, curEnd).Count(&curHandled)

	var curResolved int64
	_ = r.getHandledConversationQuery(accountID, assistantID, curStart, curEnd).Where("status = ?", "resolved").Count(&curResolved)

	var curHandoff int64
	_ = r.getHandledConversationQuery(accountID, assistantID, curStart, curEnd).Where("assignee_id IS NOT NULL AND assignee_id > 0").Count(&curHandoff)

	// Assistant public reply count
	var curAssistantReplies int64
	_ = r.db.Model(&domain.Message{}).
		Where("account_id = ? AND (sender_type = 'CaptainAssistant' OR sender_type = 'Captain::Assistant') AND sender_id = ? AND message_type = ? AND created_at >= ? AND created_at <= ?",
			accountID, assistantID, domain.MessageTypeOutgoing, curStart, curEnd).
		Count(&curAssistantReplies)

	// 2. Previous Window
	var prevHandled int64
	_ = r.getHandledConversationQuery(accountID, assistantID, prevStart, prevEnd).Count(&prevHandled)

	var prevResolved int64
	_ = r.getHandledConversationQuery(accountID, assistantID, prevStart, prevEnd).Where("status = ?", "resolved").Count(&prevResolved)

	var prevHandoff int64
	_ = r.getHandledConversationQuery(accountID, assistantID, prevStart, prevEnd).Where("assignee_id IS NOT NULL AND assignee_id > 0").Count(&prevHandoff)

	var prevAssistantReplies int64
	_ = r.db.Model(&domain.Message{}).
		Where("account_id = ? AND (sender_type = 'CaptainAssistant' OR sender_type = 'Captain::Assistant') AND sender_id = ? AND message_type = ? AND created_at >= ? AND created_at <= ?",
			accountID, assistantID, domain.MessageTypeOutgoing, prevStart, prevEnd).
		Count(&prevAssistantReplies)

	// Calculate rates
	curAutoResRate := 0.0
	if curHandled > 0 {
		curAutoResRate = math.Round((float64(curResolved)/float64(curHandled))*1000) / 10
	}
	prevAutoResRate := 0.0
	if prevHandled > 0 {
		prevAutoResRate = math.Round((float64(prevResolved)/float64(prevHandled))*1000) / 10
	}

	curHandoffRate := 0.0
	if curHandled > 0 {
		curHandoffRate = math.Round((float64(curHandoff)/float64(curHandled))*1000) / 10
	}
	prevHandoffRate := 0.0
	if prevHandled > 0 {
		prevHandoffRate = math.Round((float64(prevHandoff)/float64(prevHandled))*1000) / 10
	}

	curHoursSaved := int(math.Round(float64(curAssistantReplies) * 2.0 / 60.0))
	prevHoursSaved := int(math.Round(float64(prevAssistantReplies) * 2.0 / 60.0))

	curDepth := 0.0
	if curHandled > 0 {
		curDepth = math.Round((float64(curAssistantReplies)/float64(curHandled))*10) / 10
	}
	prevDepth := 0.0
	if prevHandled > 0 {
		prevDepth = math.Round((float64(prevAssistantReplies)/float64(prevHandled))*10) / 10
	}

	// Knowledge counts
	var faqCount int64
	_ = r.db.Model(&domain.CaptainAssistantResponse{}).Where("account_id = ? AND assistant_id = ?", accountID, assistantID).Count(&faqCount)

	var docCount int64
	_ = r.db.Model(&domain.CaptainKnowledgeDoc{}).Where("account_id = ?", accountID).Count(&docCount)

	handledChange := int64(0)
	if prevHandled > 0 {
		handledChange = int64(math.Round(float64(curHandled-prevHandled) / float64(prevHandled) * 100))
	}

	res := &AssistantStatsResult{
		ConversationsHandled: MetricPack{
			Value:         curHandled,
			PreviousValue: prevHandled,
			Change:        handledChange,
		},
		AutoResolutionRate: MetricPack{
			Value:         curAutoResRate,
			PreviousValue: prevAutoResRate,
			Change:        math.Round((curAutoResRate-prevAutoResRate)*10) / 10,
		},
		HandoffRate: MetricPack{
			Value:         curHandoffRate,
			PreviousValue: prevHandoffRate,
			Change:        math.Round((curHandoffRate-prevHandoffRate)*10) / 10,
		},
		HoursSaved: MetricPack{
			Value:         curHoursSaved,
			PreviousValue: prevHoursSaved,
			Change:        curHoursSaved - prevHoursSaved,
		},
		ReopenRate: MetricPack{
			Value:         0.0,
			PreviousValue: 0.0,
			Change:        0.0,
		},
		ConversationDepth: MetricPack{
			Value:         curDepth,
			PreviousValue: prevDepth,
			Change:        math.Round((curDepth-prevDepth)*10) / 10,
		},
		Knowledge: map[string]int64{
			"faqs_count":      faqCount,
			"documents_count": docCount,
		},
	}
	return res, nil
}

func (r *CaptainRepository) GetSummary(accountID, assistantID uint, timeRange string) (string, error) {
	stats, err := r.GetStats(accountID, assistantID, timeRange, 0)
	if err != nil {
		return "", err
	}
	handled, _ := stats.ConversationsHandled.Value.(int64)
	resRate, _ := stats.AutoResolutionRate.Value.(float64)
	hours, _ := stats.HoursSaved.Value.(int)
	faqs := stats.Knowledge["faqs_count"]

	summary := fmt.Sprintf(
		"Captain助手已处理 %d 个客户对话，自动解决率达到 %.1f%%，已为客服团队累计节省约 %d 小时工时。知识库中已维护 %d 条常见问答（FAQ）。运行状态良好。",
		handled, resRate, hours, faqs,
	)
	return summary, nil
}

func (r *CaptainRepository) GetDrilldown(accountID, assistantID uint, metric, timeRange string, page, perPage int) ([]domain.Conversation, int64, error) {
	if page <= 0 {
		page = 1
	}
	if perPage <= 0 || perPage > 100 {
		perPage = 25
	}

	curStart, curEnd, _ := parseDateRange(timeRange)
	baseQuery := r.getHandledConversationQuery(accountID, assistantID, curStart, curEnd)

	switch metric {
	case "conversations_handled":
		// all handled
	case "auto_resolution_rate":
		baseQuery = baseQuery.Where("status = ?", "resolved")
	case "handoff_rate":
		baseQuery = baseQuery.Where("assignee_id IS NOT NULL AND assignee_id > 0")
	case "reopen_rate":
		baseQuery = baseQuery.Where("status = ?", "open")
	}

	var total int64
	if err := baseQuery.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var convs []domain.Conversation
	offset := (page - 1) * perPage
	err := baseQuery.Preload("Contact").Preload("Inbox").
		Order("conversations.id DESC").
		Offset(offset).Limit(perPage).
		Find(&convs).Error

	return convs, total, err
}
