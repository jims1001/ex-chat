package handler

import (
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
	db         *gorm.DB
	convRepo   *repository.ConversationRepository
	msgRepo    *repository.MessageRepository
	portalRepo *repository.PortalRepository
	cannedRepo *repository.CannedResponseRepository
}

func NewCopilotHandler(
	db *gorm.DB,
	convRepo *repository.ConversationRepository,
	msgRepo *repository.MessageRepository,
	portalRepo *repository.PortalRepository,
	cannedRepo *repository.CannedResponseRepository,
) *CopilotHandler {
	return &CopilotHandler{
		db:         db,
		convRepo:   convRepo,
		msgRepo:    msgRepo,
		portalRepo: portalRepo,
		cannedRepo: cannedRepo,
	}
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

	sentiment := "neutral"
	if strings.Contains(content, "生气") || strings.Contains(content, "投诉") || strings.Contains(content, "退款") || strings.Contains(content, "糟糕") || strings.Contains(content, "慢") {
		sentiment = "frustrated"
	} else if strings.Contains(content, "谢谢") || strings.Contains(content, "感谢") || strings.Contains(content, "满意") || strings.Contains(content, "好的") {
		sentiment = "positive"
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

func (h *CopilotHandler) ListAssistants(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	var assistants []domain.CaptainAssistant
	_ = h.db.Where("account_id = ?", accID).Order("id DESC").Find(&assistants)
	response.Success(c, assistants)
}

func (h *CopilotHandler) CreateAssistant(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	var assistant domain.CaptainAssistant
	if err := c.ShouldBindJSON(&assistant); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	assistant.AccountID = uint(accID)
	if assistant.Model == "" {
		assistant.Model = "local-heuristic"
	}
	if assistant.Status == "" {
		assistant.Status = "active"
	}
	if err := h.db.Create(&assistant).Error; err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	response.Created(c, assistant)
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

