package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type CopilotHandler struct {
	db          *gorm.DB
	convRepo    *repository.ConversationRepository
	msgRepo     *repository.MessageRepository
	portalRepo  *repository.PortalRepository
	cannedRepo  *repository.CannedResponseRepository
	captainRepo *repository.CaptainRepository
}

func NewCopilotHandler(
	db *gorm.DB,
	convRepo *repository.ConversationRepository,
	msgRepo *repository.MessageRepository,
	portalRepo *repository.PortalRepository,
	cannedRepo *repository.CannedResponseRepository,
) *CopilotHandler {
	return &CopilotHandler{
		db:          db,
		convRepo:    convRepo,
		msgRepo:     msgRepo,
		portalRepo:  portalRepo,
		cannedRepo:  cannedRepo,
		captainRepo: repository.NewCaptainRepository(db),
	}
}

func (h *CopilotHandler) SetCaptainRepo(repo *repository.CaptainRepository) {
	h.captainRepo = repo
}

// ----------------- Copilot Endpoints -----------------

type ReplySuggestion struct {
	Reply      string  `json:"reply"`
	Confidence float64 `json:"confidence"`
	Source     string  `json:"source"`
}

// ReplySuggestions generates context-aware reply suggestions using local knowledge heuristics
func (h *CopilotHandler) ReplySuggestions(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	convID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	conv, err := h.convRepo.FindByID(uint(accID), uint(convID))
	if err != nil || conv == nil {
		response.NotFound(c, "Conversation not found")
		return
	}

	// Fetch recent messages
	messages, _, _ := h.msgRepo.ListByConversation(uint(accID), uint(convID), true, 1, 10)
	var lastIncomingText string
	for _, m := range messages {
		if m.MessageType == domain.MessageTypeIncoming {
			lastIncomingText = strings.ToLower(m.Content)
			break
		}
	}

	var suggestions []ReplySuggestion

	// 1. Search knowledge docs
	var docs []domain.CaptainKnowledgeDoc
	if h.db != nil {
		_ = h.db.Where("account_id = ?", accID).Find(&docs)
		for _, doc := range docs {
			keywords := strings.Fields(strings.ToLower(doc.Title))
			matchCount := 0
			for _, kw := range keywords {
				if len(kw) > 1 && strings.Contains(lastIncomingText, kw) {
					matchCount++
				}
			}
			if matchCount > 0 || len(suggestions) == 0 {
				suggestions = append(suggestions, ReplySuggestion{
					Reply:      fmt.Sprintf("根据知识库《%s》：\n%s", doc.Title, doc.Content),
					Confidence: 0.85,
					Source:     "Captain Knowledge Base",
				})
			}
		}
	}

	// 2. Search canned responses
	if h.cannedRepo != nil {
		cannedList, _ := h.cannedRepo.List(uint(accID), "")
		for _, cr := range cannedList {
			if strings.Contains(lastIncomingText, strings.ToLower(cr.ShortCode)) || len(suggestions) < 3 {
				suggestions = append(suggestions, ReplySuggestion{
					Reply:      cr.Content,
					Confidence: 0.90,
					Source:     "Canned Response [" + cr.ShortCode + "]",
				})
			}
		}
	}

	// 3. Fallback standard courteous suggestions
	if len(suggestions) == 0 {
		suggestions = append(suggestions,
			ReplySuggestion{
				Reply:      "您好！已收到您反馈的问题，正在为您核实处理，请您稍候。",
				Confidence: 0.75,
				Source:     "Heuristic Copilot",
			},
			ReplySuggestion{
				Reply:      "非常抱歉给您带来不便，请问您可以提供更多具体的订单号或详细信息吗？以便我们尽快协助您。",
				Confidence: 0.70,
				Source:     "Heuristic Copilot",
			},
		)
	}

	response.Success(c, gin.H{
		"conversation_id": convID,
		"suggestions":     suggestions,
	})
}

// SummarizeConversation summarizes conversation progress and customer sentiment
func (h *CopilotHandler) SummarizeConversation(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	convID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	messages, _, _ := h.msgRepo.ListByConversation(uint(accID), uint(convID), true, 1, 50)
	if len(messages) == 0 {
		response.Success(c, gin.H{
			"summary":     "尚无历史会话记录",
			"sentiment":   "neutral",
			"key_points":  []string{},
			"status":      "no_messages",
		})
		return
	}

	var allText strings.Builder
	for _, m := range messages {
		allText.WriteString(m.Content + " ")
	}
	content := strings.ToLower(allText.String())

	sentiment := domain.SentimentNeutral
	sentimentRules := map[string][]string{
		domain.SentimentFrustrated: {"生气", "投诉", "退款", "糟糕", "慢", "不满意", "差评"},
		domain.SentimentPositive:   {"谢谢", "感谢", "满意", "好的", "棒", "赞"},
	}
	for cat, keywords := range sentimentRules {
		matched := false
		for _, kw := range keywords {
			if strings.Contains(content, kw) {
				sentiment = cat
				matched = true
				break
			}
		}
		if matched {
			break
		}
	}

	keyPoints := []string{
		fmt.Sprintf("累计交互 %d 条消息", len(messages)),
		fmt.Sprintf("客户近期关注要点：%s", messages[0].Content),
	}

	summaryText := fmt.Sprintf("会话共计 %d 条消息沟通。客户情绪评估为【%s】，最新诉求为：“%s”。目前正处于处理链路中。",
		len(messages), sentiment, messages[0].Content)

	response.Success(c, gin.H{
		"conversation_id": convID,
		"summary":         summaryText,
		"sentiment":       sentiment,
		"key_points":      keyPoints,
	})
}

type RephraseReq struct {
	Text string `json:"text" binding:"required"`
	Tone string `json:"tone"` // polite, professional, concise, expanded
}

// RephraseText adjusts tone of draft replies
func (h *CopilotHandler) RephraseText(c *gin.Context) {
	var req RephraseReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	rephrased := req.Text
	switch strings.ToLower(req.Tone) {
	case "polite":
		rephrased = fmt.Sprintf("您好，感谢您的耐心等待。%s 如有任何疑问，请随时联系我们，竭诚为您服务！", req.Text)
	case "professional":
		rephrased = fmt.Sprintf("尊敬的客户：%s 我们将持续跟踪该事项的进展并第一时间反馈于您。", req.Text)
	case "concise":
		rephrased = strings.TrimSpace(req.Text)
	case "expanded":
		rephrased = fmt.Sprintf("您好，关于您关心的事项：%s 我们已记录并会跟进。请放心，客服团队将全程协助直至彻底解决。", req.Text)
	default:
		rephrased = fmt.Sprintf("您好：%s", req.Text)
	}

	response.Success(c, gin.H{
		"original":  req.Text,
		"rephrased": rephrased,
		"tone":      req.Tone,
	})
}

// ----------------- Captain Assistants & Knowledge Bases -----------------

func stringifyJSONField(val any) string {
	if val == nil {
		return ""
	}
	switch v := val.(type) {
	case string:
		return v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

type AssistantReq struct {
	Name               string `json:"name"`
	Description        string `json:"description"`
	SystemPrompt       string `json:"system_prompt"`
	Model              string `json:"model"`
	ModelName          string `json:"model_name"`
	Status             string `json:"status"`
	Config             any    `json:"config"`
	ResponseGuidelines any    `json:"response_guidelines"`
	Guardrails         any    `json:"guardrails"`
	Assistant          *struct {
		Name               string `json:"name"`
		Description        string `json:"description"`
		SystemPrompt       string `json:"system_prompt"`
		Model              string `json:"model"`
		ModelName          string `json:"model_name"`
		Status             string `json:"status"`
		Config             any    `json:"config"`
		ResponseGuidelines any    `json:"response_guidelines"`
		Guardrails         any    `json:"guardrails"`
	} `json:"assistant"`
}

func (h *CopilotHandler) ListAssistants(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	assistants, err := h.captainRepo.ListByAccount(uint(accID))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, assistants)
}

func (h *CopilotHandler) CreateAssistant(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	var req AssistantReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.Assistant != nil {
		if req.Name == "" {
			req.Name = req.Assistant.Name
		}
		if req.Description == "" {
			req.Description = req.Assistant.Description
		}
		if req.SystemPrompt == "" {
			req.SystemPrompt = req.Assistant.SystemPrompt
		}
		if req.Model == "" {
			req.Model = req.Assistant.Model
		}
		if req.ModelName == "" {
			req.ModelName = req.Assistant.ModelName
		}
		if req.Status == "" {
			req.Status = req.Assistant.Status
		}
		if req.Config == nil {
			req.Config = req.Assistant.Config
		}
		if req.ResponseGuidelines == nil {
			req.ResponseGuidelines = req.Assistant.ResponseGuidelines
		}
		if req.Guardrails == nil {
			req.Guardrails = req.Assistant.Guardrails
		}
	}
	if req.Name == "" {
		response.BadRequest(c, "name is required")
		return
	}
	model := req.Model
	if model == "" {
		model = req.ModelName
	}
	if model == "" {
		model = "local-heuristic"
	}
	status := req.Status
	if status == "" {
		status = "active"
	}

	assistant := domain.CaptainAssistant{
		AccountID:          uint(accID),
		Name:               req.Name,
		Description:        req.Description,
		SystemPrompt:       req.SystemPrompt,
		Model:              model,
		Status:             status,
		Config:             stringifyJSONField(req.Config),
		ResponseGuidelines: stringifyJSONField(req.ResponseGuidelines),
		Guardrails:         stringifyJSONField(req.Guardrails),
	}
	if err := h.captainRepo.Create(&assistant); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Created(c, assistant)
}

func (h *CopilotHandler) GetAssistant(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid assistant ID")
		return
	}
	assistant, err := h.captainRepo.FindByID(uint(accID), uint(id))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	if assistant == nil {
		response.NotFound(c, "Captain assistant not found")
		return
	}
	response.Success(c, assistant)
}

func (h *CopilotHandler) UpdateAssistant(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid assistant ID")
		return
	}
	assistant, err := h.captainRepo.FindByID(uint(accID), uint(id))
	if err != nil || assistant == nil {
		response.NotFound(c, "Captain assistant not found")
		return
	}

	var req AssistantReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.Assistant != nil {
		if req.Name == "" {
			req.Name = req.Assistant.Name
		}
		if req.Description == "" {
			req.Description = req.Assistant.Description
		}
		if req.SystemPrompt == "" {
			req.SystemPrompt = req.Assistant.SystemPrompt
		}
		if req.Model == "" {
			req.Model = req.Assistant.Model
		}
		if req.ModelName == "" {
			req.ModelName = req.Assistant.ModelName
		}
		if req.Status == "" {
			req.Status = req.Assistant.Status
		}
		if req.Config == nil {
			req.Config = req.Assistant.Config
		}
		if req.ResponseGuidelines == nil {
			req.ResponseGuidelines = req.Assistant.ResponseGuidelines
		}
		if req.Guardrails == nil {
			req.Guardrails = req.Assistant.Guardrails
		}
	}

	if req.Name != "" {
		assistant.Name = req.Name
	}
	if req.Description != "" {
		assistant.Description = req.Description
	}
	if req.SystemPrompt != "" {
		assistant.SystemPrompt = req.SystemPrompt
	}
	if req.Model != "" {
		assistant.Model = req.Model
	} else if req.ModelName != "" {
		assistant.Model = req.ModelName
	}
	if req.Status != "" {
		assistant.Status = req.Status
	}
	if req.Config != nil {
		assistant.Config = stringifyJSONField(req.Config)
	}
	if req.ResponseGuidelines != nil {
		assistant.ResponseGuidelines = stringifyJSONField(req.ResponseGuidelines)
	}
	if req.Guardrails != nil {
		assistant.Guardrails = stringifyJSONField(req.Guardrails)
	}

	if err := h.captainRepo.Update(assistant); err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, assistant)
}

func (h *CopilotHandler) DeleteAssistant(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid assistant ID")
		return
	}
	assistant, err := h.captainRepo.FindByID(uint(accID), uint(id))
	if err != nil || assistant == nil {
		response.NotFound(c, "Captain assistant not found")
		return
	}
	if err := h.captainRepo.Delete(uint(accID), uint(id)); err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// ----------------- Inbox Bindings -----------------

func (h *CopilotHandler) ListAssistantInboxes(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid assistant ID")
		return
	}
	assistant, err := h.captainRepo.FindByID(uint(accID), uint(id))
	if err != nil || assistant == nil {
		response.NotFound(c, "Captain assistant not found")
		return
	}
	inboxes, err := h.captainRepo.ListBoundInboxes(uint(accID), uint(id))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, inboxes)
}

type BindInboxReq struct {
	InboxID uint `json:"inbox_id"`
	Inbox   *struct {
		InboxID uint `json:"inbox_id"`
	} `json:"inbox"`
}

func (h *CopilotHandler) BindAssistantInbox(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid assistant ID")
		return
	}
	assistant, err := h.captainRepo.FindByID(uint(accID), uint(id))
	if err != nil || assistant == nil {
		response.NotFound(c, "Captain assistant not found")
		return
	}

	var req BindInboxReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	inboxID := req.InboxID
	if inboxID == 0 && req.Inbox != nil {
		inboxID = req.Inbox.InboxID
	}
	if inboxID == 0 {
		response.BadRequest(c, "inbox_id is required")
		return
	}

	binding, err := h.captainRepo.BindInbox(uint(accID), uint(id), inboxID)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Created(c, binding)
}

func (h *CopilotHandler) UnbindAssistantInbox(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid assistant ID")
		return
	}
	inboxID, err := strconv.ParseUint(c.Param("inbox_id"), 10, 32)
	if err != nil || inboxID == 0 {
		response.BadRequest(c, "Invalid inbox ID")
		return
	}
	if err := h.captainRepo.UnbindInbox(uint(accID), uint(id), uint(inboxID)); err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, gin.H{"unbound": true})
}

// ----------------- AI Playground -----------------

type PlaygroundReq struct {
	MessageContent string `json:"message_content"`
	MessageHistory []struct {
		Role      string `json:"role"`
		Content   string `json:"content"`
		AgentName string `json:"agent_name"`
	} `json:"message_history"`
	Assistant *struct {
		MessageContent string `json:"message_content"`
		MessageHistory []struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			AgentName string `json:"agent_name"`
		} `json:"message_history"`
	} `json:"assistant"`
}

func (h *CopilotHandler) Playground(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid assistant ID")
		return
	}
	assistant, err := h.captainRepo.FindByID(uint(accID), uint(id))
	if err != nil || assistant == nil {
		response.NotFound(c, "Captain assistant not found")
		return
	}

	var req PlaygroundReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.Assistant != nil {
		if req.MessageContent == "" {
			req.MessageContent = req.Assistant.MessageContent
		}
		if len(req.MessageHistory) == 0 {
			req.MessageHistory = req.Assistant.MessageHistory
		}
	}

	content := strings.TrimSpace(req.MessageContent)
	if content == "" && len(req.MessageHistory) > 0 {
		content = strings.TrimSpace(req.MessageHistory[len(req.MessageHistory)-1].Content)
	}
	if content == "" {
		response.BadRequest(c, "message_content or message_history is required")
		return
	}

	// 1. Search matching FAQs
	matchedFAQs, _ := h.captainRepo.SearchMatchingFAQs(uint(accID), uint(id), content, 3)

	// 2. Search matching knowledge chunks
	var chunks []domain.CaptainDocChunk
	_ = h.db.Where("account_id = ? AND LOWER(content) LIKE ?", accID, "%"+strings.ToLower(content)+"%").Limit(3).Find(&chunks)

	// 3. Build knowledge context & citations
	var citations []map[string]string
	var kbSnippets []string
	for _, faq := range matchedFAQs {
		citations = append(citations, map[string]string{
			"type":   "faq",
			"title":  faq.Question,
			"source": fmt.Sprintf("FAQ #%d", faq.ID),
		})
		kbSnippets = append(kbSnippets, fmt.Sprintf("Q: %s\nA: %s", faq.Question, faq.Answer))
	}
	for _, ch := range chunks {
		citations = append(citations, map[string]string{
			"type":   "document_chunk",
			"title":  fmt.Sprintf("Doc #%d Chunk #%d", ch.DocID, ch.ChunkIndex),
			"source": "Knowledge Document",
		})
		kbSnippets = append(kbSnippets, ch.Content)
	}

	// 4. Construct System Prompt & Guidelines
	sysPrompt := assistant.SystemPrompt
	if sysPrompt == "" {
		sysPrompt = "你是一个专业、礼貌、高效的AI客服助手。"
	}
	if assistant.ResponseGuidelines != "" {
		sysPrompt += "\n\n【回复指南】\n" + assistant.ResponseGuidelines
	}
	if assistant.Guardrails != "" {
		sysPrompt += "\n\n【安全护栏】\n" + assistant.Guardrails
	}
	if len(kbSnippets) > 0 {
		sysPrompt += "\n\n【参考知识库与常见问答】\n" + strings.Join(kbSnippets, "\n\n")
	}

	var aiMessages []service.AIMessage
	aiMessages = append(aiMessages, service.AIMessage{
		Role:    "system",
		Content: sysPrompt,
	})
	for _, hMsg := range req.MessageHistory {
		aiMessages = append(aiMessages, service.AIMessage{
			Role:    hMsg.Role,
			Content: hMsg.Content,
		})
	}
	if len(req.MessageHistory) == 0 || req.MessageHistory[len(req.MessageHistory)-1].Content != content {
		aiMessages = append(aiMessages, service.AIMessage{
			Role:    "user",
			Content: content,
		})
	}

	// 5. Invoke AI provider
	aiProv := service.GetAIProvider(assistant.Model, "", assistant.Model)
	completionResp, err := aiProv.GenerateCompletion(c.Request.Context(), service.AICompletionRequest{
		Model:    assistant.Model,
		Messages: aiMessages,
	})

	var replyText string
	var usage map[string]int
	if len(matchedFAQs) > 0 {
		topFAQ := matchedFAQs[0]
		replyText = fmt.Sprintf("您好！关于您的问题，参考常见问答（FAQ）：\n%s\n如有其他问题，请随时告诉我！", topFAQ.Answer)
		usage = map[string]int{
			"prompt_tokens":     len(content) + 50,
			"completion_tokens": len(replyText) + 20,
			"total_tokens":      len(content) + len(replyText) + 70,
		}
	} else if err == nil && completionResp != nil && completionResp.Content != "" {
		replyText = completionResp.Content
		usage = map[string]int{
			"prompt_tokens":     completionResp.PromptTokens,
			"completion_tokens": completionResp.CompTokens,
			"total_tokens":      completionResp.TotalTokens,
		}
	} else {
		replyText = fmt.Sprintf("您好！已收到您的消息：“%s”。我正在为您查询相关解答，请稍候。", content)
		usage = map[string]int{
			"prompt_tokens":     len(content) + 20,
			"completion_tokens": len(replyText) + 10,
			"total_tokens":      len(content) + len(replyText) + 30,
		}
	}

	// 6. Record quota
	var quota domain.AIUsageQuota
	if err := h.db.Where("account_id = ?", accID).First(&quota).Error; err == nil {
		quota.UsedRequests++
		quota.UsedTokens += int64(usage["total_tokens"])
		_ = h.db.Save(&quota)
	}

	response.Success(c, gin.H{
		"role":    "assistant",
		"content": replyText,
		"message": gin.H{
			"role":    "assistant",
			"content": replyText,
		},
		"matched_faqs": matchedFAQs,
		"citations":    citations,
		"usage":        usage,
		"model":        assistant.Model,
	})
}

// ----------------- FAQ / Assistant Responses -----------------

type AssistantResponseReq struct {
	AssistantID       uint   `json:"assistant_id"`
	DocumentID        *uint  `json:"document_id"`
	Question          string `json:"question"`
	Answer            string `json:"answer"`
	Status            string `json:"status"`
	AssistantResponse *struct {
		AssistantID uint   `json:"assistant_id"`
		DocumentID  *uint  `json:"document_id"`
		Question    string `json:"question"`
		Answer      string `json:"answer"`
		Status      string `json:"status"`
	} `json:"assistant_response"`
}

func (h *CopilotHandler) ListAssistantResponses(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	var assistantID uint
	if idStr := c.Param("id"); idStr != "" {
		aid, _ := strconv.ParseUint(idStr, 10, 32)
		assistantID = uint(aid)
	}
	if assistantID == 0 {
		if qAid := c.Query("assistant_id"); qAid != "" {
			aid, _ := strconv.ParseUint(qAid, 10, 32)
			assistantID = uint(aid)
		}
	}
	status := c.Query("status")
	search := c.Query("search")
	if search == "" {
		search = c.Query("q")
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "25"))

	items, total, err := h.captainRepo.ListResponses(uint(accID), assistantID, status, search, page, perPage)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, gin.H{
		"items":    items,
		"total":    total,
		"page":     page,
		"per_page": perPage,
	})
}

func (h *CopilotHandler) CreateAssistantResponse(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	var req AssistantResponseReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.AssistantResponse != nil {
		if req.AssistantID == 0 {
			req.AssistantID = req.AssistantResponse.AssistantID
		}
		if req.Question == "" {
			req.Question = req.AssistantResponse.Question
		}
		if req.Answer == "" {
			req.Answer = req.AssistantResponse.Answer
		}
		if req.Status == "" {
			req.Status = req.AssistantResponse.Status
		}
		if req.DocumentID == nil {
			req.DocumentID = req.AssistantResponse.DocumentID
		}
	}
	if idStr := c.Param("id"); idStr != "" && req.AssistantID == 0 {
		aid, _ := strconv.ParseUint(idStr, 10, 32)
		req.AssistantID = uint(aid)
	}
	if req.Question == "" || req.Answer == "" {
		response.BadRequest(c, "question and answer are required")
		return
	}
	if req.AssistantID == 0 {
		response.BadRequest(c, "assistant_id is required")
		return
	}
	asst, err := h.captainRepo.FindByID(uint(accID), req.AssistantID)
	if err != nil || asst == nil {
		response.NotFound(c, "Assistant not found")
		return
	}
	if req.Status == "" {
		req.Status = "active"
	}

	faq := domain.CaptainAssistantResponse{
		AccountID:   uint(accID),
		AssistantID: req.AssistantID,
		DocumentID:  req.DocumentID,
		Question:    req.Question,
		Answer:      req.Answer,
		Status:      req.Status,
	}
	if err := h.captainRepo.CreateResponse(&faq); err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Created(c, faq)
}

func (h *CopilotHandler) GetAssistantResponse(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid response ID")
		return
	}
	faq, err := h.captainRepo.FindResponseByID(uint(accID), uint(id))
	if err != nil || faq == nil {
		response.NotFound(c, "Assistant response not found")
		return
	}
	response.Success(c, faq)
}

func (h *CopilotHandler) UpdateAssistantResponse(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid response ID")
		return
	}
	faq, err := h.captainRepo.FindResponseByID(uint(accID), uint(id))
	if err != nil || faq == nil {
		response.NotFound(c, "Assistant response not found")
		return
	}

	var req AssistantResponseReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.AssistantResponse != nil {
		if req.Question == "" {
			req.Question = req.AssistantResponse.Question
		}
		if req.Answer == "" {
			req.Answer = req.AssistantResponse.Answer
		}
		if req.Status == "" {
			req.Status = req.AssistantResponse.Status
		}
	}
	if req.Question != "" {
		faq.Question = req.Question
	}
	if req.Answer != "" {
		faq.Answer = req.Answer
	}
	if req.Status != "" {
		faq.Status = req.Status
	}
	if req.AssistantID > 0 {
		faq.AssistantID = req.AssistantID
	}
	if err := h.captainRepo.UpdateResponse(faq); err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, faq)
}

func (h *CopilotHandler) DeleteAssistantResponse(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid response ID")
		return
	}
	faq, err := h.captainRepo.FindResponseByID(uint(accID), uint(id))
	if err != nil || faq == nil {
		response.NotFound(c, "Assistant response not found")
		return
	}
	if err := h.captainRepo.DeleteResponse(uint(accID), uint(id)); err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// ----------------- Stats, Summary, Drilldown -----------------

func (h *CopilotHandler) GetAssistantStats(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid assistant ID")
		return
	}
	assistant, err := h.captainRepo.FindByID(uint(accID), uint(id))
	if err != nil || assistant == nil {
		response.NotFound(c, "Captain assistant not found")
		return
	}
	rangeStr := c.DefaultQuery("range", "30")
	tzOffset, _ := strconv.Atoi(c.DefaultQuery("timezone_offset", "0"))

	stats, err := h.captainRepo.GetStats(uint(accID), uint(id), rangeStr, tzOffset)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, stats)
}

func (h *CopilotHandler) GetAssistantSummary(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid assistant ID")
		return
	}
	assistant, err := h.captainRepo.FindByID(uint(accID), uint(id))
	if err != nil || assistant == nil {
		response.NotFound(c, "Captain assistant not found")
		return
	}
	rangeStr := c.DefaultQuery("range", "30")

	summary, err := h.captainRepo.GetSummary(uint(accID), uint(id), rangeStr)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, gin.H{"message": summary})
}

func (h *CopilotHandler) GetAssistantDrilldown(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil {
		response.BadRequest(c, "Invalid assistant ID")
		return
	}
	assistant, err := h.captainRepo.FindByID(uint(accID), uint(id))
	if err != nil || assistant == nil {
		response.NotFound(c, "Captain assistant not found")
		return
	}
	metric := c.DefaultQuery("metric", "conversations_handled")
	rangeStr := c.DefaultQuery("range", "30")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "25"))

	convs, total, err := h.captainRepo.GetDrilldown(uint(accID), uint(id), metric, rangeStr, page, perPage)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, gin.H{
		"meta": gin.H{
			"metric":             metric,
			"current_page":       page,
			"per_page":           perPage,
			"total_count":        total,
			"conversation_count": total,
		},
		"payload": convs,
	})
}

func (h *CopilotHandler) ListKnowledgeDocs(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	var docs []domain.CaptainKnowledgeDoc
	_ = h.db.Where("account_id = ?", accID).Order("id DESC").Find(&docs)
	response.Success(c, docs)
}

func (h *CopilotHandler) CreateKnowledgeDoc(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	var doc domain.CaptainKnowledgeDoc
	if err := c.ShouldBindJSON(&doc); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	doc.AccountID = uint(accID)
	doc.Status = "processing"
	if err := h.db.Create(&doc).Error; err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	// Chunking pipeline: split document text into discrete indexed chunks
	content := strings.TrimSpace(doc.Content)
	var rawChunks []string
	if len(content) <= 400 {
		rawChunks = append(rawChunks, content)
	} else {
		runes := []rune(content)
		chunkSize := 400
		for i := 0; i < len(runes); i += chunkSize {
			end := i + chunkSize
			if end > len(runes) {
				end = len(runes)
			}
			rawChunks = append(rawChunks, string(runes[i:end]))
		}
	}

	for idx, chunkText := range rawChunks {
		chunk := domain.CaptainDocChunk{
			AccountID:  doc.AccountID,
			DocID:      doc.ID,
			ChunkIndex: idx,
			Content:    chunkText,
			Keywords:   "",
		}
		_ = h.db.Create(&chunk)
	}

	doc.ChunkCount = len(rawChunks)
	doc.Status = "ready"
	_ = h.db.Save(&doc)

	response.Created(c, doc)
}

func (h *CopilotHandler) SearchKnowledgeChunks(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	query := strings.TrimSpace(c.Query("q"))
	if query == "" {
		response.BadRequest(c, "query parameter 'q' is required")
		return
	}

	var chunks []domain.CaptainDocChunk
	err := h.db.Where("account_id = ? AND LOWER(content) LIKE ?", accID, "%"+strings.ToLower(query)+"%").
		Limit(20).Find(&chunks).Error
	if err != nil {
		response.InternalError(c, "Failed to search chunks")
		return
	}

	response.Success(c, chunks)
}

type GenerateCompletionReq struct {
	Provider string `json:"provider"` // openai, gemini, local-heuristic
	Model    string `json:"model"`
	Prompt   string `json:"prompt" binding:"required"`
	APIKey   string `json:"api_key"`
}

func (h *CopilotHandler) GenerateCompletion(c *gin.Context) {
	var req GenerateCompletionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	prov := service.GetAIProvider(req.Provider, req.APIKey, req.Model)
	resp, err := prov.GenerateCompletion(c.Request.Context(), service.AICompletionRequest{
		Model: req.Model,
		Messages: []service.AIMessage{
			{Role: "user", Content: req.Prompt},
		},
	})
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}

	response.Success(c, resp)
}

// ----------------- AI Scenarios & Tools (Item 16) -----------------

type CreateScenarioReq struct {
	Name         string `json:"name" binding:"required"`
	Description  string `json:"description"`
	SystemPrompt string `json:"system_prompt" binding:"required"`
	AllowedTools string `json:"allowed_tools"`
}

func (h *CopilotHandler) ListScenarios(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	var list []domain.AIScenario
	_ = h.db.Where("account_id = ?", accID).Order("id DESC").Find(&list)
	response.Success(c, list)
}

func (h *CopilotHandler) CreateScenario(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	var req CreateScenarioReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	sc := domain.AIScenario{
		AccountID:    uint(accID),
		Name:         req.Name,
		Description:  req.Description,
		SystemPrompt: req.SystemPrompt,
		AllowedTools: req.AllowedTools,
	}
	if err := h.db.Create(&sc).Error; err != nil {
		response.InternalError(c, "Failed to create scenario")
		return
	}
	response.Created(c, sc)
}

func (h *CopilotHandler) UpdateScenario(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	var sc domain.AIScenario
	if err := h.db.Where("account_id = ? AND id = ?", accID, id).First(&sc).Error; err != nil {
		response.NotFound(c, "Scenario not found")
		return
	}
	var req CreateScenarioReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.Name != "" {
		sc.Name = req.Name
	}
	if req.Description != "" {
		sc.Description = req.Description
	}
	if req.SystemPrompt != "" {
		sc.SystemPrompt = req.SystemPrompt
	}
	if req.AllowedTools != "" {
		sc.AllowedTools = req.AllowedTools
	}
	_ = h.db.Save(&sc)
	response.Success(c, sc)
}

func (h *CopilotHandler) DeleteScenario(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	id, _ := strconv.ParseUint(c.Param("id"), 10, 32)
	if err := h.db.Where("account_id = ? AND id = ?", accID, id).Delete(&domain.AIScenario{}).Error; err != nil {
		response.InternalError(c, "Failed to delete scenario")
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

type ExecuteToolReq struct {
	ToolName string         `json:"tool_name" binding:"required"`
	Params   map[string]any `json:"params"`
}

func (h *CopilotHandler) ExecuteAITool(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	var req ExecuteToolReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	var quota domain.AIUsageQuota
	err := h.db.Where("account_id = ?", accID).First(&quota).Error
	if err != nil {
		quota = domain.AIUsageQuota{
			AccountID:           uint(accID),
			MonthlyTokenLimit:   1000000,
			UsedTokens:          0,
			MonthlyRequestLimit: 5000,
			UsedRequests:        0,
			ResetAt:             time.Now().AddDate(0, 1, 0),
		}
		_ = h.db.Create(&quota)
	}

	if (quota.MonthlyRequestLimit > 0 && quota.UsedRequests >= quota.MonthlyRequestLimit) ||
		(quota.MonthlyTokenLimit > 0 && quota.UsedTokens >= quota.MonthlyTokenLimit) {
		response.Error(c, http.StatusTooManyRequests, "Monthly AI quota exceeded")
		return
	}

	var result any
	consumedTokens := 20
	switch req.ToolName {
	case "order_lookup":
		orderID, _ := req.Params["order_id"].(string)
		result = gin.H{
			"order_id": orderID,
			"status":   "shipped",
			"carrier":  "FedEx",
			"tracking": "FX-9823412",
		}
	case "kb_search":
		kw, _ := req.Params["keyword"].(string)
		var chunks []domain.CaptainDocChunk
		_ = h.db.Where("account_id = ? AND LOWER(content) LIKE ?", accID, "%"+strings.ToLower(kw)+"%").Limit(3).Find(&chunks)
		result = chunks
	default:
		result = gin.H{"executed": true, "tool": req.ToolName, "output": "Tool executed successfully"}
	}

	quota.UsedRequests++
	quota.UsedTokens += int64(consumedTokens)
	_ = h.db.Save(&quota)

	response.Success(c, gin.H{
		"tool":            req.ToolName,
		"result":          result,
		"tokens_consumed": consumedTokens,
		"quota_remaining": gin.H{
			"calls":  quota.MonthlyRequestLimit - quota.UsedRequests,
			"tokens": quota.MonthlyTokenLimit - quota.UsedTokens,
		},
	})
}

func (h *CopilotHandler) GetAIQuota(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	var quota domain.AIUsageQuota
	err := h.db.Where("account_id = ?", accID).First(&quota).Error
	if err != nil {
		quota = domain.AIUsageQuota{
			AccountID:           uint(accID),
			MonthlyTokenLimit:   1000000,
			UsedTokens:          0,
			MonthlyRequestLimit: 5000,
			UsedRequests:        0,
			ResetAt:             time.Now().AddDate(0, 1, 0),
		}
		_ = h.db.Create(&quota)
	}
	response.Success(c, quota)
}

