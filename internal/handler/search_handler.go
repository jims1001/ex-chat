package handler

import (
	"strconv"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type SearchHandler struct {
	db *gorm.DB
}

func NewSearchHandler(db *gorm.DB) *SearchHandler {
	return &SearchHandler{db: db}
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

	likePattern := "%" + q + "%"

	var contacts []domain.Contact
	_ = h.db.Where("account_id = ? AND (name LIKE ? OR email LIKE ? OR phone_number LIKE ?)", accountID, likePattern, likePattern, likePattern).Limit(20).Find(&contacts).Error

	var messages []domain.Message
	_ = h.db.Where("account_id = ? AND content LIKE ?", accountID, likePattern).Limit(20).Find(&messages).Error

	var conversations []domain.Conversation
	_ = h.db.Where("account_id = ? AND custom_attributes LIKE ?", accountID, likePattern).Limit(20).Find(&conversations).Error

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

	q := c.Query("q")
	likePattern := "%" + q + "%"

	var contacts []domain.Contact
	_ = h.db.Where("account_id = ? AND (name LIKE ? OR email LIKE ? OR phone_number LIKE ?)", accountID, likePattern, likePattern, likePattern).Limit(50).Find(&contacts).Error

	response.Success(c, contacts)
}

func (h *SearchHandler) SearchConversations(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	q := c.Query("q")
	likePattern := "%" + q + "%"

	var conversations []domain.Conversation
	_ = h.db.Where("account_id = ? AND (custom_attributes LIKE ? OR status LIKE ?)", accountID, likePattern, likePattern).Limit(50).Find(&conversations).Error

	response.Success(c, conversations)
}

func (h *SearchHandler) SearchMessages(c *gin.Context) {
	accountID, err := strconv.ParseUint(c.Param("account_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Account ID must be numeric")
		return
	}

	q := c.Query("q")
	likePattern := "%" + q + "%"

	var messages []domain.Message
	_ = h.db.Where("account_id = ? AND content LIKE ?", accountID, likePattern).Limit(50).Find(&messages).Error

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
