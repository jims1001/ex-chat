package handler

import (
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

type PortalHandler struct {
	portalRepo *repository.PortalRepository
}

func NewPortalHandler(portalRepo *repository.PortalRepository) *PortalHandler {
	return &PortalHandler{portalRepo: portalRepo}
}

type CreatePortalRequest struct {
	Name         string `json:"name" binding:"required"`
	Slug         string `json:"slug" binding:"required"`
	CustomDomain string `json:"custom_domain"`
	Color        string `json:"color"`
	HeaderText   string `json:"header_text"`
}

type UpdatePortalRequest struct {
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	CustomDomain string `json:"custom_domain"`
	Color        string `json:"color"`
	HeaderText   string `json:"header_text"`
}

type CreateCategoryRequest struct {
	Name        string `json:"name" binding:"required"`
	Slug        string `json:"slug" binding:"required"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Position    int    `json:"position"`
}

type UpdateCategoryRequest struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
	Position    *int   `json:"position"`
}

type CreateArticleRequest struct {
	CategoryID uint   `json:"category_id" binding:"required"`
	Title      string `json:"title" binding:"required"`
	Slug       string `json:"slug" binding:"required"`
	Content    string `json:"content" binding:"required"`
	Status     string `json:"status"` // draft, published
}

type UpdateArticleRequest struct {
	CategoryID *uint  `json:"category_id"`
	Title      string `json:"title"`
	Slug       string `json:"slug"`
	Content    string `json:"content"`
	Status     string `json:"status"`
}


// ----------------- Agent Management -----------------

func (h *PortalHandler) ListPortals(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	portals, err := h.portalRepo.ListPortals(accountID)
	if err != nil {
		response.InternalError(c, "Failed to list portals")
		return
	}
	response.Success(c, portals)
}

func (h *PortalHandler) CreatePortal(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	var req CreatePortalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	portal := domain.Portal{
		AccountID:    accountID,
		Name:         req.Name,
		Slug:         strings.ToLower(strings.TrimSpace(req.Slug)),
		CustomDomain: req.CustomDomain,
		Color:        req.Color,
		HeaderText:   req.HeaderText,
	}

	if err := h.portalRepo.CreatePortal(&portal); err != nil {
		response.InternalError(c, "Failed to create portal (slug may already exist)")
		return
	}

	response.Created(c, portal)
}

func (h *PortalHandler) ListCategories(c *gin.Context) {
	portalID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid portal ID")
		return
	}

	categories, err := h.portalRepo.ListCategories(uint(portalID))
	if err != nil {
		response.InternalError(c, "Failed to list categories")
		return
	}
	response.Success(c, categories)
}

func (h *PortalHandler) CreateCategory(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	portalID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid portal ID")
		return
	}

	var req CreateCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	category := domain.Category{
		PortalID:    uint(portalID),
		AccountID:   accountID,
		Name:        req.Name,
		Slug:        strings.ToLower(strings.TrimSpace(req.Slug)),
		Description: req.Description,
		Icon:        req.Icon,
		Position:    req.Position,
	}

	if err := h.portalRepo.CreateCategory(&category); err != nil {
		response.InternalError(c, "Failed to create category")
		return
	}

	response.Created(c, category)
}

func (h *PortalHandler) ListArticles(c *gin.Context) {
	portalID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid portal ID")
		return
	}

	status := c.Query("status")
	search := c.Query("q")

	var categoryID *uint
	if cStr := c.Query("category_id"); cStr != "" {
		if cid, err := strconv.ParseUint(cStr, 10, 64); err == nil {
			uCID := uint(cid)
			categoryID = &uCID
		}
	}

	articles, err := h.portalRepo.ListArticles(uint(portalID), categoryID, status, search)
	if err != nil {
		response.InternalError(c, "Failed to list articles")
		return
	}

	response.Success(c, articles)
}

func (h *PortalHandler) CreateArticle(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)
	rawUserID, _ := c.Get(middleware.ContextUserID)
	userID := rawUserID.(uint)

	portalID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid portal ID")
		return
	}

	var req CreateArticleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	status := req.Status
	if status == "" {
		status = "published"
	}

	article := domain.Article{
		PortalID:   uint(portalID),
		CategoryID: req.CategoryID,
		AccountID:  accountID,
		AuthorID:   userID,
		Title:      req.Title,
		Slug:       strings.ToLower(strings.TrimSpace(req.Slug)),
		Content:    req.Content,
		Status:     status,
	}

	if err := h.portalRepo.CreateArticle(&article); err != nil {
		response.InternalError(c, "Failed to create article")
		return
	}

	response.Created(c, article)
}

func (h *PortalHandler) GetPortal(c *gin.Context) {
	portalID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid portal ID")
		return
	}
	portal, err := h.portalRepo.FindPortalByID(uint(portalID))
	if err != nil || portal == nil {
		response.NotFound(c, "Portal not found")
		return
	}
	response.Success(c, portal)
}

func (h *PortalHandler) UpdatePortal(c *gin.Context) {
	portalID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid portal ID")
		return
	}
	portal, err := h.portalRepo.FindPortalByID(uint(portalID))
	if err != nil || portal == nil {
		response.NotFound(c, "Portal not found")
		return
	}
	var req UpdatePortalRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.Name != "" {
		portal.Name = req.Name
	}
	if req.Slug != "" {
		portal.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	}
	if req.CustomDomain != "" {
		portal.CustomDomain = req.CustomDomain
	}
	if req.Color != "" {
		portal.Color = req.Color
	}
	if req.HeaderText != "" {
		portal.HeaderText = req.HeaderText
	}
	if err := h.portalRepo.UpdatePortal(portal); err != nil {
		response.InternalError(c, "Failed to update portal")
		return
	}
	response.Success(c, portal)
}

func (h *PortalHandler) DeletePortal(c *gin.Context) {
	portalID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid portal ID")
		return
	}
	if err := h.portalRepo.DeletePortal(uint(portalID)); err != nil {
		response.InternalError(c, "Failed to delete portal")
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *PortalHandler) GetCategory(c *gin.Context) {
	catID, err := strconv.ParseUint(c.Param("category_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid category ID")
		return
	}
	cat, err := h.portalRepo.FindCategoryByID(uint(catID))
	if err != nil || cat == nil {
		response.NotFound(c, "Category not found")
		return
	}
	response.Success(c, cat)
}

func (h *PortalHandler) UpdateCategory(c *gin.Context) {
	catID, err := strconv.ParseUint(c.Param("category_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid category ID")
		return
	}
	cat, err := h.portalRepo.FindCategoryByID(uint(catID))
	if err != nil || cat == nil {
		response.NotFound(c, "Category not found")
		return
	}
	var req UpdateCategoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.Name != "" {
		cat.Name = req.Name
	}
	if req.Slug != "" {
		cat.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	}
	if req.Description != "" {
		cat.Description = req.Description
	}
	if req.Icon != "" {
		cat.Icon = req.Icon
	}
	if req.Position != nil {
		cat.Position = *req.Position
	}
	if err := h.portalRepo.UpdateCategory(cat); err != nil {
		response.InternalError(c, "Failed to update category")
		return
	}
	response.Success(c, cat)
}

func (h *PortalHandler) DeleteCategory(c *gin.Context) {
	catID, err := strconv.ParseUint(c.Param("category_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid category ID")
		return
	}
	if err := h.portalRepo.DeleteCategory(uint(catID)); err != nil {
		response.InternalError(c, "Failed to delete category")
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *PortalHandler) GetArticle(c *gin.Context) {
	artID, err := strconv.ParseUint(c.Param("article_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid article ID")
		return
	}
	article, err := h.portalRepo.FindArticleByID(uint(artID))
	if err != nil || article == nil {
		response.NotFound(c, "Article not found")
		return
	}
	response.Success(c, article)
}

func (h *PortalHandler) UpdateArticle(c *gin.Context) {
	artID, err := strconv.ParseUint(c.Param("article_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid article ID")
		return
	}
	article, err := h.portalRepo.FindArticleByID(uint(artID))
	if err != nil || article == nil {
		response.NotFound(c, "Article not found")
		return
	}
	var req UpdateArticleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.CategoryID != nil && *req.CategoryID != 0 {
		article.CategoryID = *req.CategoryID
	}
	if req.Title != "" {
		article.Title = req.Title
	}
	if req.Slug != "" {
		article.Slug = strings.ToLower(strings.TrimSpace(req.Slug))
	}
	if req.Content != "" {
		article.Content = req.Content
	}
	if req.Status != "" {
		article.Status = req.Status
	}
	if err := h.portalRepo.UpdateArticle(article); err != nil {
		response.InternalError(c, "Failed to update article")
		return
	}
	response.Success(c, article)
}

func (h *PortalHandler) DeleteArticle(c *gin.Context) {
	artID, err := strconv.ParseUint(c.Param("article_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid article ID")
		return
	}
	if err := h.portalRepo.DeleteArticle(uint(artID)); err != nil {
		response.InternalError(c, "Failed to delete article")
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// ----------------- Public Visitor Endpoints -----------------

func (h *PortalHandler) PublicGetPortal(c *gin.Context) {
	slug := c.Param("slug")

	portal, err := h.portalRepo.FindPortalBySlug(slug)
	if err != nil || portal == nil {
		response.NotFound(c, "Help Center portal not found")
		return
	}

	categories, _ := h.portalRepo.ListCategories(portal.ID)
	response.Success(c, gin.H{
		"portal":     portal,
		"categories": categories,
	})
}

func (h *PortalHandler) PublicListArticles(c *gin.Context) {
	slug := c.Param("slug")

	portal, err := h.portalRepo.FindPortalBySlug(slug)
	if err != nil || portal == nil {
		response.NotFound(c, "Help Center portal not found")
		return
	}

	search := c.Query("q")
	var categoryID *uint
	if cStr := c.Query("category_id"); cStr != "" {
		if cid, err := strconv.ParseUint(cStr, 10, 64); err == nil {
			uCID := uint(cid)
			categoryID = &uCID
		}
	}

	articles, err := h.portalRepo.ListArticles(portal.ID, categoryID, "published", search)
	if err != nil {
		response.InternalError(c, "Failed to query articles")
		return
	}

	response.Success(c, articles)
}

func (h *PortalHandler) PublicGetArticle(c *gin.Context) {
	portalSlug := c.Param("slug")
	articleSlug := c.Param("article_slug")

	portal, err := h.portalRepo.FindPortalBySlug(portalSlug)
	if err != nil || portal == nil {
		response.NotFound(c, "Help Center portal not found")
		return
	}

	article, err := h.portalRepo.FindArticleBySlug(portal.ID, articleSlug)
	if err != nil || article == nil {
		response.NotFound(c, "Article not found")
		return
	}

	_ = h.portalRepo.IncrementViews(article.ID)
	article.Views++

	response.Success(c, article)
}
