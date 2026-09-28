package handler

import (
	"strconv"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type SearchHandler struct {
	db            *gorm.DB
	contacts      *repository.ContactRepository
	conversations *repository.ConversationRepository
	messages      *repository.MessageRepository
}

func NewSearchHandler(db *gorm.DB, contacts *repository.ContactRepository, conversations *repository.ConversationRepository, messages *repository.MessageRepository) *SearchHandler {
	return &SearchHandler{db: db, contacts: contacts, conversations: conversations, messages: messages}
}

func (h *SearchHandler) GlobalSearch(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	q := c.Query("q")
	if q == "" {
		response.Success(c, gin.H{
			"contacts":      []domain.Contact{},
			"conversations": []domain.Conversation{},
			"messages":      []domain.Message{},
			"articles":      []domain.Article{},
		})
		return
	}

	contacts, err := h.contacts.SearchForGlobal(uint(accountID), q, 20)
	if err != nil {
		response.InternalError(c, "Failed to search contacts")
		return
	}
	messages, err := h.messages.SearchForGlobal(uint(accountID), q, 20)
	if err != nil {
		response.InternalError(c, "Failed to search messages")
		return
	}
	conversations, err := h.conversations.SearchForGlobal(uint(accountID), q, false, 20)
	if err != nil {
		response.InternalError(c, "Failed to search conversations")
		return
	}

	likePattern := "%" + q + "%"
	var articles []domain.Article
	_ = h.db.Where("account_id = ? AND (title LIKE ? OR content LIKE ?)", accountID, likePattern, likePattern).Limit(20).Find(&articles).Error

	response.Success(c, gin.H{
		"contacts":      contacts,
		"conversations": conversations,
		"messages":      messages,
		"articles":      articles,
	})
}

func (h *SearchHandler) SearchContacts(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	contacts, err := h.contacts.SearchForGlobal(uint(accountID), c.Query("q"), 50)
	if err != nil {
		response.InternalError(c, "Failed to search contacts")
		return
	}

	response.Success(c, contacts)
}

func (h *SearchHandler) SearchConversations(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	conversations, err := h.conversations.SearchForGlobal(uint(accountID), c.Query("q"), true, 50)
	if err != nil {
		response.InternalError(c, "Failed to search conversations")
		return
	}

	response.Success(c, conversations)
}

func (h *SearchHandler) SearchMessages(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	messages, err := h.messages.SearchForGlobal(uint(accountID), c.Query("q"), 50)
	if err != nil {
		response.InternalError(c, "Failed to search messages")
		return
	}

	response.Success(c, messages)
}

func (h *SearchHandler) SearchArticles(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	q := c.Query("q")
	likePattern := "%" + q + "%"

	var articles []domain.Article
	_ = h.db.Where("account_id = ? AND (title LIKE ? OR content LIKE ?)", accountID, likePattern, likePattern).Limit(50).Find(&articles).Error

	response.Success(c, articles)
}
