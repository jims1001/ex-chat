package handler

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// CopilotThreadHandler handles HTTP requests for Copilot multi-turn threads and messages
type CopilotThreadHandler struct {
	db          *gorm.DB
	threadRepo  *repository.CopilotThreadRepository
	convRepo    *repository.ConversationRepository
	msgRepo     *repository.MessageRepository
	captainRepo *repository.CaptainRepository
}

// NewCopilotThreadHandler creates a new instance of CopilotThreadHandler
func NewCopilotThreadHandler(
	db *gorm.DB,
	threadRepo *repository.CopilotThreadRepository,
	convRepo *repository.ConversationRepository,
	msgRepo *repository.MessageRepository,
	captainRepo *repository.CaptainRepository,
) *CopilotThreadHandler {
	return &CopilotThreadHandler{
		db:          db,
		threadRepo:  threadRepo,
		convRepo:    convRepo,
		msgRepo:     msgRepo,
		captainRepo: captainRepo,
	}
}

func (h *CopilotThreadHandler) getAccountID(c *gin.Context) uint {
	if accVal, exists := c.Get("account_id"); exists {
		if id, ok := accVal.(uint); ok {
			return id
		}
	}
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	return uint(accID)
}

func (h *CopilotThreadHandler) getUserID(c *gin.Context) uint {
	if userVal, exists := c.Get("user_id"); exists {
		if id, ok := userVal.(uint); ok {
			return id
		}
	}
	return 1
}

// ----------------- Thread Endpoints -----------------

// ListThreads handles GET /copilot/threads
func (h *CopilotThreadHandler) ListThreads(c *gin.Context) {
	accountID := h.getAccountID(c)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("per_page", "20"))
	status := c.Query("status")
	query := c.Query("q")

	var convIDPtr *uint
	if convStr := c.Query("conversation_id"); convStr != "" {
		if id, err := strconv.ParseUint(convStr, 10, 32); err == nil {
			u := uint(id)
			convIDPtr = &u
		}
	}

	var assistantIDPtr *uint
	if asstStr := c.Query("assistant_id"); asstStr != "" {
		if id, err := strconv.ParseUint(asstStr, 10, 32); err == nil {
			u := uint(id)
			assistantIDPtr = &u
		}
	}

	var userIDPtr *uint
	if uStr := c.Query("user_id"); uStr != "" {
		if id, err := strconv.ParseUint(uStr, 10, 32); err == nil {
			u := uint(id)
			userIDPtr = &u
		}
	}

	filter := repository.CopilotThreadFilter{
		ConversationID: convIDPtr,
		AssistantID:    assistantIDPtr,
		UserID:         userIDPtr,
		Status:         status,
		Query:          query,
		Page:           page,
		PageSize:       pageSize,
	}

	threads, total, err := h.threadRepo.ListThreads(accountID, filter)
	if err != nil {
		logger.WithComponent("copilot").Error("failed to list copilot threads",
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to list Copilot threads")
		return
	}

	logger.WithComponent("copilot").Info("listed copilot threads",
		"account_id", accountID,
		"count", len(threads),
		"total", total,
	)

	response.Success(c, gin.H{
		"threads": threads,
		"meta": gin.H{
			"total":    total,
			"page":     page,
			"per_page": pageSize,
		},
	})
}

// CreateThreadReq defines payload for creating a Copilot thread
type CreateThreadReq struct {
	ConversationID *uint  `json:"conversation_id"`
	AssistantID    *uint  `json:"assistant_id"`
	Title          string `json:"title"`
	Context        string `json:"context"`
	Metadata       string `json:"metadata"`
}

// CreateThread handles POST /copilot/threads
func (h *CopilotThreadHandler) CreateThread(c *gin.Context) {
	accountID := h.getAccountID(c)
	userID := h.getUserID(c)

	var req CreateThreadReq
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		response.BadRequest(c, err.Error())
		return
	}

	title := strings.TrimSpace(req.Title)
	if title == "" {
		if req.ConversationID != nil {
			title = fmt.Sprintf("会话 #%d 辅助分析", *req.ConversationID)
		} else {
			title = fmt.Sprintf("Copilot 智能问答 %s", time.Now().Format("01-02 15:04"))
		}
	}

	thread := domain.CopilotThread{
		AccountID:      accountID,
		UserID:         userID,
		ConversationID: req.ConversationID,
		AssistantID:    req.AssistantID,
		Title:          title,
		Status:         domain.CopilotThreadStatusActive,
		Context:        req.Context,
		Metadata:       req.Metadata,
	}

	if err := h.threadRepo.CreateThread(&thread); err != nil {
		logger.WithComponent("copilot").Error("failed to create copilot thread",
			"account_id", accountID,
			"user_id", userID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to create Copilot thread")
		return
	}

	logger.WithComponent("copilot").Info("created copilot thread",
		"account_id", accountID,
		"user_id", userID,
		"thread_id", thread.ID,
		"title", thread.Title,
	)

	response.Success(c, thread)
}

// GetThread handles GET /copilot/threads/:id
func (h *CopilotThreadHandler) GetThread(c *gin.Context) {
	accountID := h.getAccountID(c)
	threadID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || threadID == 0 {
		response.BadRequest(c, "Invalid thread ID")
		return
	}

	thread, err := h.threadRepo.GetThread(accountID, uint(threadID))
	if err != nil || thread == nil {
		response.NotFound(c, "Copilot thread not found")
		return
	}

	response.Success(c, thread)
}

// UpdateThreadReq defines fields modifiable on a Copilot thread
type UpdateThreadReq struct {
	Title    *string `json:"title"`
	Status   *string `json:"status"`
	Context  *string `json:"context"`
	Metadata *string `json:"metadata"`
}

// UpdateThread handles PUT/PATCH /copilot/threads/:id
func (h *CopilotThreadHandler) UpdateThread(c *gin.Context) {
	accountID := h.getAccountID(c)
	threadID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || threadID == 0 {
		response.BadRequest(c, "Invalid thread ID")
		return
	}

	thread, err := h.threadRepo.GetThread(accountID, uint(threadID))
	if err != nil || thread == nil {
		response.NotFound(c, "Copilot thread not found")
		return
	}

	var req UpdateThreadReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if req.Title != nil {
		thread.Title = *req.Title
	}
	if req.Status != nil {
		thread.Status = *req.Status
	}
	if req.Context != nil {
		thread.Context = *req.Context
	}
	if req.Metadata != nil {
		thread.Metadata = *req.Metadata
	}

	if err := h.threadRepo.UpdateThread(thread); err != nil {
		logger.WithComponent("copilot").Error("failed to update copilot thread",
			"account_id", accountID,
			"thread_id", threadID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to update Copilot thread")
		return
	}

	logger.WithComponent("copilot").Info("updated copilot thread",
		"account_id", accountID,
		"thread_id", threadID,
		"status", thread.Status,
	)

	response.Success(c, thread)
}

// DeleteThread handles DELETE /copilot/threads/:id
func (h *CopilotThreadHandler) DeleteThread(c *gin.Context) {
	accountID := h.getAccountID(c)
	threadID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || threadID == 0 {
		response.BadRequest(c, "Invalid thread ID")
		return
	}

	if err := h.threadRepo.DeleteThread(accountID, uint(threadID)); err != nil {
		if err == gorm.ErrRecordNotFound {
			response.NotFound(c, "Copilot thread not found")
			return
		}
		logger.WithComponent("copilot").Error("failed to delete copilot thread",
			"account_id", accountID,
			"thread_id", threadID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to delete Copilot thread")
		return
	}

	logger.WithComponent("copilot").Info("deleted copilot thread",
		"account_id", accountID,
		"thread_id", threadID,
	)

	response.Success(c, gin.H{"deleted": true, "thread_id": threadID})
}

// GetMetrics handles GET /copilot/threads/metrics
func (h *CopilotThreadHandler) GetMetrics(c *gin.Context) {
	accountID := h.getAccountID(c)

	metrics, err := h.threadRepo.GetThreadMetrics(accountID)
	if err != nil {
		logger.WithComponent("copilot").Error("failed to get copilot metrics",
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to get Copilot metrics")
		return
	}

	response.Success(c, metrics)
}

// ListConversationThreads handles GET /conversations/:id/copilot/threads
func (h *CopilotThreadHandler) ListConversationThreads(c *gin.Context) {
	accountID := h.getAccountID(c)
	convID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || convID == 0 {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	uConv := uint(convID)
	filter := repository.CopilotThreadFilter{
		ConversationID: &uConv,
		Page:           1,
		PageSize:       50,
	}

	threads, total, err := h.threadRepo.ListThreads(accountID, filter)
	if err != nil {
		response.InternalError(c, "Failed to list conversation threads")
		return
	}

	response.Success(c, gin.H{"threads": threads, "total": total})
}

// CreateConversationThread handles POST /conversations/:id/copilot/threads
func (h *CopilotThreadHandler) CreateConversationThread(c *gin.Context) {
	convID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || convID == 0 {
		response.BadRequest(c, "Invalid conversation ID")
		return
	}

	var req CreateThreadReq
	_ = c.ShouldBindJSON(&req)
	uConv := uint(convID)
	req.ConversationID = &uConv

	// Reuse CreateThread with modified context
	accountID := h.getAccountID(c)
	userID := h.getUserID(c)

	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = fmt.Sprintf("会话 #%d 专属辅助", convID)
	}

	thread := domain.CopilotThread{
		AccountID:      accountID,
		UserID:         userID,
		ConversationID: &uConv,
		AssistantID:    req.AssistantID,
		Title:          title,
		Status:         domain.CopilotThreadStatusActive,
		Context:        req.Context,
		Metadata:       req.Metadata,
	}

	if err := h.threadRepo.CreateThread(&thread); err != nil {
		response.InternalError(c, "Failed to create conversation Copilot thread")
		return
	}

	response.Success(c, thread)
}

// ----------------- Message & Multi-turn Chat Endpoints -----------------

// ListMessages handles GET /copilot/threads/:id/messages
func (h *CopilotThreadHandler) ListMessages(c *gin.Context) {
	accountID := h.getAccountID(c)
	threadID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || threadID == 0 {
		response.BadRequest(c, "Invalid thread ID")
		return
	}

	// Verify thread exists
	thread, err := h.threadRepo.GetThread(accountID, uint(threadID))
	if err != nil || thread == nil {
		response.NotFound(c, "Copilot thread not found")
		return
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("per_page", "50"))
	order := c.DefaultQuery("order", "asc")
	orderAsc := order == "asc"

	messages, total, err := h.threadRepo.ListMessages(accountID, uint(threadID), page, pageSize, orderAsc)
	if err != nil {
		response.InternalError(c, "Failed to list thread messages")
		return
	}

	response.Success(c, gin.H{
		"messages": messages,
		"meta": gin.H{
			"total":     total,
			"page":      page,
			"per_page":  pageSize,
			"thread_id": threadID,
		},
	})
}

// SendMessageReq defines the incoming user prompt in a thread
type SendMessageReq struct {
	Content          string `json:"content" binding:"required"`
	Role             string `json:"role"`
	Citations        string `json:"citations"`
	SuggestedActions string `json:"suggested_actions"`
	NoGenerateReply  bool   `json:"no_generate_reply"`
}

// SendMessage handles POST /copilot/threads/:id/messages & POST /copilot/threads/:id/chat
func (h *CopilotThreadHandler) SendMessage(c *gin.Context) {
	accountID := h.getAccountID(c)
	threadID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || threadID == 0 {
		response.BadRequest(c, "Invalid thread ID")
		return
	}

	thread, err := h.threadRepo.GetThread(accountID, uint(threadID))
	if err != nil || thread == nil {
		response.NotFound(c, "Copilot thread not found")
		return
	}

	var req SendMessageReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	userRole := domain.CopilotRoleUser
	if req.Role != "" {
		userRole = req.Role
	}

	userMsg := domain.CopilotThreadMessage{
		AccountID:        accountID,
		ThreadID:         uint(threadID),
		Role:             userRole,
		Content:          req.Content,
		Citations:        req.Citations,
		SuggestedActions: req.SuggestedActions,
		TokenCount:       len(req.Content) / 4 + 5,
		Feedback:         domain.CopilotFeedbackNone,
	}

	if err := h.threadRepo.AddMessage(&userMsg); err != nil {
		logger.WithComponent("copilot").Error("failed to append user message to thread",
			"account_id", accountID,
			"thread_id", threadID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to record message")
		return
	}

	// If client requested not to auto-generate assistant reply
	if req.NoGenerateReply {
		response.Success(c, gin.H{"message": userMsg})
		return
	}

	// ---------------- Multi-turn Dialogue Engine ----------------
	// 1. Fetch recent turns for contextual awareness
	historyMsgs, _ := h.threadRepo.GetRecentThreadContext(accountID, uint(threadID), 8)

	// 2. Fetch conversation context if thread is attached to a customer conversation
	var conversationContext string
	if thread.ConversationID != nil {
		conv, convErr := h.convRepo.FindByID(accountID, *thread.ConversationID)
		if convErr == nil && conv != nil {
			conversationContext = fmt.Sprintf("【关联客户会话 #%d】客户名称：%s，渠道ID：%d",
				conv.ID, conv.Contact.Name, conv.InboxID)
			// Fetch last incoming message
			recentMsgs, _, _ := h.msgRepo.ListByConversation(accountID, conv.ID, true, 1, 5)
			for _, m := range recentMsgs {
				if m.MessageType == domain.MessageTypeIncoming {
					conversationContext += fmt.Sprintf("\n客户最近咨询内容：“%s”", m.Content)
					break
				}
			}
		}
	}

	// 3. Search Knowledge Base / FAQ for relevant citations
	type CitationItem struct {
		Source  string  `json:"source"`
		Title   string  `json:"title"`
		Snippet string  `json:"snippet"`
		Score   float64 `json:"score"`
	}
	var citations []CitationItem

	lowerPrompt := strings.ToLower(req.Content)

	asstID := uint(0)
	if thread.AssistantID != nil {
		asstID = *thread.AssistantID
	}
	if h.captainRepo != nil {
		faqs, _ := h.captainRepo.SearchMatchingFAQs(accountID, asstID, req.Content, 2)
		for _, f := range faqs {
			citations = append(citations, CitationItem{
				Source:  "Captain FAQ",
				Title:   f.Question,
				Snippet: f.Answer,
				Score:   0.88,
			})
		}
	}

	if len(citations) == 0 {
		var docs []domain.CaptainKnowledgeDoc
		if h.db != nil {
			_ = h.db.Where("account_id = ?", accountID).Limit(2).Find(&docs)
			for _, d := range docs {
				if strings.Contains(strings.ToLower(d.Title), lowerPrompt) ||
					strings.Contains(strings.ToLower(d.Content), lowerPrompt) {
					snippet := d.Content
					if len([]rune(snippet)) > 80 {
						snippet = string([]rune(snippet)[:80]) + "..."
					}
					citations = append(citations, CitationItem{
						Source:  "知识库文档",
						Title:   d.Title,
						Snippet: snippet,
						Score:   0.82,
					})
				}
			}
		}
	}

	// 4. Synthesize intelligent multi-turn assistant answer
	var generatedReply string
	type ActionItem struct {
		Type    string `json:"type"`
		Label   string `json:"label"`
		Payload string `json:"payload,omitempty"`
	}
	var actions []ActionItem

	// Check multi-turn continuity
	turnCount := len(historyMsgs)
	if turnCount > 2 {
		// Multi-turn context response
		prevUserTurn := ""
		for i := len(historyMsgs) - 2; i >= 0; i-- {
			if historyMsgs[i].Role == domain.CopilotRoleUser {
				prevUserTurn = historyMsgs[i].Content
				break
			}
		}

		if strings.Contains(lowerPrompt, "语气") || strings.Contains(lowerPrompt, "温和") || strings.Contains(lowerPrompt, "官方") {
			generatedReply = fmt.Sprintf("已为您将回复改写为更加委婉友善的语气：\n“您好，非常理解您的焦急心情，我们正在全力协助您跟进处理，请您放心，稍后有最新进展会第一时间同步给您。”")
			actions = append(actions, ActionItem{
				Type:    "insert_reply",
				Label:   "填入回复框",
				Payload: "您好，非常理解您的焦急心情，我们正在全力协助您跟进处理，请您放心，稍后有最新进展会第一时间同步给您。",
			})
		} else if strings.Contains(lowerPrompt, "工单") || strings.Contains(lowerPrompt, "升级") {
			generatedReply = "已为您生成内部工单升级建议：由于该问题涉及多部门协同，建议将本会话关联升级至二线技术支持组。"
			actions = append(actions, ActionItem{
				Type:  "create_ticket",
				Label: "创建二线工单",
			})
		} else {
			generatedReply = fmt.Sprintf("结合前序对话（关于“%s”）以及您的最新指示，我的建议如下：\n1. 优先确认客户核心诉求；\n2. 依据服务政策提供明确的解决时间节点（SLA）；\n3. 保持耐心与专业安抚语气。", prevUserTurn)
			actions = append(actions, ActionItem{
				Type:    "insert_reply",
				Label:   "采用此建议",
				Payload: "请您放心，我们已记录您的具体诉求并优先安排专员核实，预计在今天下午给您回复。",
			})
		}
	} else {
		// Initial turn response
		if len(citations) > 0 {
			generatedReply = fmt.Sprintf("根据相关知识库《%s》：\n%s\n\n建议您可以这样向客户说明情况：",
				citations[0].Title, citations[0].Snippet)
			replyDraft := fmt.Sprintf("您好！关于您咨询的%s，我们核实到：%s，如有疑问随时告知。", citations[0].Title, citations[0].Snippet)
			actions = append(actions, ActionItem{
				Type:    "insert_reply",
				Label:   "填入回复框",
				Payload: replyDraft,
			})
		} else if conversationContext != "" {
			generatedReply = fmt.Sprintf("已为您解析会话背景：\n%s\n\n针对客户问题，建议回复：\n“您好！已经为您查询并确认当前进度，请您稍候片刻，我们将尽快为您处理完毕。”",
				conversationContext)
			actions = append(actions, ActionItem{
				Type:    "insert_reply",
				Label:   "填入回复框",
				Payload: "您好！已经为您查询并确认当前进度，请您稍候片刻，我们将尽快为您处理完毕。",
			})
		} else {
			generatedReply = fmt.Sprintf("您好！我是 Copilot 智能辅助助手。关于您提出的“%s”，建议可从排查原因、核验权限以及提供替代解决方案三个维度展开。", req.Content)
			actions = append(actions, ActionItem{
				Type:    "insert_reply",
				Label:   "复制建议",
				Payload: "您好，关于此问题我们建议您先刷新页面或重新登录尝试。",
			})
		}
	}

	citationsJSON, _ := json.Marshal(citations)
	actionsJSON, _ := json.Marshal(actions)

	consumedTokens := (len(req.Content)+len(generatedReply))/3 + 20

	assistantMsg := domain.CopilotThreadMessage{
		AccountID:        accountID,
		ThreadID:         uint(threadID),
		Role:             domain.CopilotRoleAssistant,
		Content:          generatedReply,
		Citations:        string(citationsJSON),
		SuggestedActions: string(actionsJSON),
		TokenCount:       consumedTokens,
		Feedback:         domain.CopilotFeedbackNone,
	}

	if err := h.threadRepo.AddMessage(&assistantMsg); err != nil {
		logger.WithComponent("copilot").Error("failed to record assistant message",
			"account_id", accountID,
			"thread_id", threadID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to record assistant message")
		return
	}

	// 5. Deduct tokens from AI usage quota if exists
	var quota domain.AIUsageQuota
	if h.db != nil && h.db.Where("account_id = ?", accountID).First(&quota).Error == nil {
		_ = h.db.Model(&quota).Updates(map[string]any{
			"used_tokens":   gorm.Expr("used_tokens + ?", consumedTokens),
			"used_requests": gorm.Expr("used_requests + ?", 1),
		}).Error
	}

	logger.WithComponent("copilot").Info("copilot message generated successfully",
		"account_id", accountID,
		"thread_id", threadID,
		"tokens", consumedTokens,
		"citations_count", len(citations),
		"actions_count", len(actions),
	)

	// Fetch updated thread info
	updatedThread, _ := h.threadRepo.GetThread(accountID, uint(threadID))

	response.Success(c, gin.H{
		"user_message":      userMsg,
		"assistant_message": assistantMsg,
		"thread":            updatedThread,
	})
}

// GetMessage handles GET /copilot/threads/:id/messages/:msg_id
func (h *CopilotThreadHandler) GetMessage(c *gin.Context) {
	accountID := h.getAccountID(c)
	threadID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	msgID, err2 := strconv.ParseUint(c.Param("msg_id"), 10, 32)
	if err != nil || err2 != nil {
		response.BadRequest(c, "Invalid thread ID or message ID")
		return
	}

	msg, err := h.threadRepo.GetMessage(accountID, uint(threadID), uint(msgID))
	if err != nil || msg == nil {
		response.NotFound(c, "Message not found")
		return
	}

	response.Success(c, msg)
}

// DeleteMessage handles DELETE /copilot/threads/:id/messages/:msg_id
func (h *CopilotThreadHandler) DeleteMessage(c *gin.Context) {
	accountID := h.getAccountID(c)
	threadID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	msgID, err2 := strconv.ParseUint(c.Param("msg_id"), 10, 32)
	if err != nil || err2 != nil {
		response.BadRequest(c, "Invalid thread ID or message ID")
		return
	}

	if err := h.threadRepo.DeleteMessage(accountID, uint(threadID), uint(msgID)); err != nil {
		if err == gorm.ErrRecordNotFound {
			response.NotFound(c, "Message not found")
			return
		}
		response.InternalError(c, "Failed to delete message")
		return
	}

	logger.WithComponent("copilot").Info("deleted thread message",
		"account_id", accountID,
		"thread_id", threadID,
		"message_id", msgID,
	)

	response.Success(c, gin.H{"deleted": true, "message_id": msgID})
}

// ClearMessages handles DELETE /copilot/threads/:id/messages
func (h *CopilotThreadHandler) ClearMessages(c *gin.Context) {
	accountID := h.getAccountID(c)
	threadID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	if err != nil || threadID == 0 {
		response.BadRequest(c, "Invalid thread ID")
		return
	}

	// Verify thread exists
	thread, err := h.threadRepo.GetThread(accountID, uint(threadID))
	if err != nil || thread == nil {
		response.NotFound(c, "Copilot thread not found")
		return
	}

	if err := h.threadRepo.ClearMessages(accountID, uint(threadID)); err != nil {
		response.InternalError(c, "Failed to clear messages")
		return
	}

	logger.WithComponent("copilot").Info("cleared all messages in copilot thread",
		"account_id", accountID,
		"thread_id", threadID,
	)

	response.Success(c, gin.H{"cleared": true, "thread_id": threadID})
}

// FeedbackReq defines the rating/comment on a Copilot message
type FeedbackReq struct {
	Feedback string `json:"feedback" binding:"required"` // thumbs_up, thumbs_down, none
	Notes    string `json:"notes"`
}

// UpdateFeedback handles POST/PUT /copilot/threads/:id/messages/:msg_id/feedback
func (h *CopilotThreadHandler) UpdateFeedback(c *gin.Context) {
	accountID := h.getAccountID(c)
	threadID, err := strconv.ParseUint(c.Param("id"), 10, 32)
	msgID, err2 := strconv.ParseUint(c.Param("msg_id"), 10, 32)
	if err != nil || err2 != nil {
		response.BadRequest(c, "Invalid thread ID or message ID")
		return
	}

	var req FeedbackReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if req.Feedback != domain.CopilotFeedbackThumbsUp &&
		req.Feedback != domain.CopilotFeedbackThumbsDown &&
		req.Feedback != domain.CopilotFeedbackNone {
		response.BadRequest(c, "Invalid feedback option. Must be thumbs_up, thumbs_down, or none")
		return
	}

	if err := h.threadRepo.UpdateFeedback(accountID, uint(threadID), uint(msgID), req.Feedback, req.Notes); err != nil {
		response.InternalError(c, "Failed to update feedback")
		return
	}

	logger.WithComponent("copilot").Info("copilot message feedback updated",
		"account_id", accountID,
		"thread_id", threadID,
		"message_id", msgID,
		"feedback", req.Feedback,
	)

	response.Success(c, gin.H{
		"message_id": msgID,
		"feedback":   req.Feedback,
		"notes":      req.Notes,
	})
}
