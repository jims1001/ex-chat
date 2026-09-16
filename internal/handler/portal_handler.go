package handler

import (
	"encoding/xml"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
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
		logger.WithComponent("portal").Error("failed to create portal",
			"account_id", accountID,
			"name", req.Name,
			"slug", portal.Slug,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to create portal (slug may already exist)")
		return
	}

	logger.WithComponent("portal").Info("portal created",
		"account_id", accountID,
		"portal_id", portal.ID,
		"name", portal.Name,
		"slug", portal.Slug,
	)

	response.Created(c, portal)
}

func (h *PortalHandler) ListCategories(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	portalID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid portal ID")
		return
	}

	portal, err := h.portalRepo.FindPortalByID(uint(portalID))
	if err != nil || portal == nil || portal.AccountID != accountID {
		response.NotFound(c, "Portal not found")
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

	portal, err := h.portalRepo.FindPortalByID(uint(portalID))
	if err != nil || portal == nil || portal.AccountID != accountID {
		response.NotFound(c, "Portal not found")
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
		logger.WithComponent("portal").Error("failed to create category",
			"account_id", accountID,
			"portal_id", portalID,
			"name", req.Name,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to create category")
		return
	}

	logger.WithComponent("portal").Info("portal category created",
		"account_id", accountID,
		"portal_id", portalID,
		"category_id", category.ID,
		"name", category.Name,
	)

	response.Created(c, category)
}

func (h *PortalHandler) ListArticles(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	portalID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid portal ID")
		return
	}

	portal, err := h.portalRepo.FindPortalByID(uint(portalID))
	if err != nil || portal == nil || portal.AccountID != accountID {
		response.NotFound(c, "Portal not found")
		return
	}

	status := c.Query("status")
	search := c.Query("q")

	var categoryID *uint
	if cStr := c.Query("category_id"); cStr != "" {
		if cid, err := strconv.ParseUint(cStr, 10, 64); err == nil {
			uCID := uint(cid)
			cat, err := h.portalRepo.FindCategoryByID(uCID)
			if err != nil || cat == nil || cat.PortalID != portal.ID || cat.AccountID != accountID {
				response.BadRequest(c, "Invalid category ID for this portal")
				return
			}
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

	portal, err := h.portalRepo.FindPortalByID(uint(portalID))
	if err != nil || portal == nil || portal.AccountID != accountID {
		response.NotFound(c, "Portal not found")
		return
	}

	var req CreateArticleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	category, err := h.portalRepo.FindCategoryByID(req.CategoryID)
	if err != nil || category == nil || category.PortalID != portal.ID || category.AccountID != accountID {
		response.BadRequest(c, "Invalid category ID for this portal")
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
		logger.WithComponent("portal").Error("failed to create article",
			"account_id", accountID,
			"portal_id", portalID,
			"title", req.Title,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to create article")
		return
	}

	logger.WithComponent("portal").Info("portal article created",
		"account_id", accountID,
		"portal_id", portalID,
		"article_id", article.ID,
		"title", article.Title,
		"status", article.Status,
	)

	response.Created(c, article)
}

func (h *PortalHandler) GetPortal(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	portalID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid portal ID")
		return
	}
	portal, err := h.portalRepo.FindPortalByID(uint(portalID))
	if err != nil || portal == nil || portal.AccountID != accountID {
		response.NotFound(c, "Portal not found")
		return
	}
	response.Success(c, portal)
}

func (h *PortalHandler) UpdatePortal(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	portalID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid portal ID")
		return
	}
	portal, err := h.portalRepo.FindPortalByID(uint(portalID))
	if err != nil || portal == nil || portal.AccountID != accountID {
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
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	portalID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid portal ID")
		return
	}
	portal, err := h.portalRepo.FindPortalByID(uint(portalID))
	if err != nil || portal == nil || portal.AccountID != accountID {
		response.NotFound(c, "Portal not found")
		return
	}

	if err := h.portalRepo.DeletePortal(uint(portalID)); err != nil {
		logger.WithComponent("portal").Error("failed to delete portal",
			"portal_id", portalID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to delete portal")
		return
	}

	logger.WithComponent("portal").Info("portal deleted",
		"portal_id", portalID,
	)

	response.Success(c, gin.H{"deleted": true})
}

func (h *PortalHandler) GetCategory(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	portalID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid portal ID")
		return
	}

	catID, err := strconv.ParseUint(c.Param("category_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid category ID")
		return
	}
	cat, err := h.portalRepo.FindCategoryByID(uint(catID))
	if err != nil || cat == nil || cat.AccountID != accountID || cat.PortalID != uint(portalID) {
		response.NotFound(c, "Category not found")
		return
	}
	response.Success(c, cat)
}

func (h *PortalHandler) UpdateCategory(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	portalID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid portal ID")
		return
	}

	catID, err := strconv.ParseUint(c.Param("category_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid category ID")
		return
	}
	cat, err := h.portalRepo.FindCategoryByID(uint(catID))
	if err != nil || cat == nil || cat.AccountID != accountID || cat.PortalID != uint(portalID) {
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
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	portalID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid portal ID")
		return
	}

	catID, err := strconv.ParseUint(c.Param("category_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid category ID")
		return
	}
	cat, err := h.portalRepo.FindCategoryByID(uint(catID))
	if err != nil || cat == nil || cat.AccountID != accountID || cat.PortalID != uint(portalID) {
		response.NotFound(c, "Category not found")
		return
	}
	if err := h.portalRepo.DeleteCategory(uint(catID)); err != nil {
		response.InternalError(c, "Failed to delete category")
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *PortalHandler) GetArticle(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	portalID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid portal ID")
		return
	}

	artID, err := strconv.ParseUint(c.Param("article_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid article ID")
		return
	}
	article, err := h.portalRepo.FindArticleByID(uint(artID))
	if err != nil || article == nil || article.AccountID != accountID || article.PortalID != uint(portalID) {
		response.NotFound(c, "Article not found")
		return
	}
	response.Success(c, article)
}

func (h *PortalHandler) UpdateArticle(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	portalID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid portal ID")
		return
	}

	artID, err := strconv.ParseUint(c.Param("article_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid article ID")
		return
	}
	article, err := h.portalRepo.FindArticleByID(uint(artID))
	if err != nil || article == nil || article.AccountID != accountID || article.PortalID != uint(portalID) {
		response.NotFound(c, "Article not found")
		return
	}
	var req UpdateArticleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if req.CategoryID != nil && *req.CategoryID != 0 {
		cat, err := h.portalRepo.FindCategoryByID(*req.CategoryID)
		if err != nil || cat == nil || cat.AccountID != accountID || cat.PortalID != article.PortalID {
			response.BadRequest(c, "Invalid category ID for this portal")
			return
		}
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
		logger.WithComponent("portal").Error("failed to update article",
			"article_id", artID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to update article")
		return
	}

	logger.WithComponent("portal").Info("portal article updated",
		"article_id", artID,
		"title", article.Title,
		"status", article.Status,
	)

	response.Success(c, article)
}

func (h *PortalHandler) DeleteArticle(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	portalID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid portal ID")
		return
	}

	artID, err := strconv.ParseUint(c.Param("article_id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid article ID")
		return
	}
	article, err := h.portalRepo.FindArticleByID(uint(artID))
	if err != nil || article == nil || article.AccountID != accountID || article.PortalID != uint(portalID) {
		response.NotFound(c, "Article not found")
		return
	}
	if err := h.portalRepo.DeleteArticle(uint(artID)); err != nil {
		logger.WithComponent("portal").Error("failed to delete article",
			"article_id", artID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to delete article")
		return
	}

	logger.WithComponent("portal").Info("portal article deleted",
		"article_id", artID,
	)

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

// ----------------- Public Visitor Web Portal Rendering (/hc/:slug) -----------------

func (h *PortalHandler) PublicRenderHome(c *gin.Context) {
	slug := c.Param("slug")
	portal, err := h.portalRepo.FindPortalBySlug(slug)
	if err != nil || portal == nil {
		c.Data(404, "text/html; charset=utf-8", []byte("<h1>404 - 帮助中心不存在</h1><p>未找到对应的帮助中心站点。</p>"))
		return
	}

	categories, _ := h.portalRepo.ListCategories(portal.ID)
	articles, _ := h.portalRepo.ListArticles(portal.ID, nil, "published", "")

	// Build categories cards HTML
	var catsHTML strings.Builder
	catsHTML.WriteString(`<div class="categories-grid">`)
	for _, cat := range categories {
		catsHTML.WriteString(fmt.Sprintf(`
			<a class="category-card" href="/hc/%s/categories/%s">
				<div class="category-icon">%s</div>
				<h3 class="category-title">%s</h3>
				<p class="category-desc">%s</p>
			</a>`,
			portal.Slug,
			cat.Slug,
			html.EscapeString(cat.Icon),
			html.EscapeString(cat.Name),
			html.EscapeString(cat.Description),
		))
	}
	catsHTML.WriteString(`</div>`)

	// Build recent articles HTML
	var artsHTML strings.Builder
	artsHTML.WriteString(`<div class="recent-articles"><h2>常见问题与推荐文章</h2><ul>`)
	for i, art := range articles {
		if i >= 10 {
			break
		}
		artsHTML.WriteString(fmt.Sprintf(`
			<li>
				<a href="/hc/%s/articles/%s">
					<span class="art-title">%s</span>
					<span class="art-views">%d 次阅读</span>
				</a>
			</li>`,
			portal.Slug,
			art.Slug,
			html.EscapeString(art.Title),
			art.Views,
		))
	}
	if len(articles) == 0 {
		artsHTML.WriteString(`<li class="empty-state">知识库正在完善中，暂无已发布的公开文章。</li>`)
	}
	artsHTML.WriteString(`</ul></div>`)

	headerText := portal.HeaderText
	if headerText == "" {
		headerText = "我们能为您提供什么帮助？"
	}

	heroHTML := fmt.Sprintf(`
		<div class="hero">
			<h1>%s</h1>
			<p class="hero-sub">%s</p>
			<form class="search-box" action="/hc/%s/search" method="GET">
				<input type="text" name="q" placeholder="输入关键词搜索帮助文档..." required />
				<button type="submit">搜索</button>
			</form>
		</div>`,
		html.EscapeString(portal.Name),
		html.EscapeString(headerText),
		portal.Slug,
	)

	body := heroHTML + `<div class="container section"><h2>知识分类</h2>` + catsHTML.String() + artsHTML.String() + `</div>`
	pageHTML := renderPortalLayout(portal.Name+" - 帮助中心", headerText, portal.Color, body, portal)
	c.Data(200, "text/html; charset=utf-8", []byte(pageHTML))
}

func (h *PortalHandler) PublicRenderCategory(c *gin.Context) {
	slug := c.Param("slug")
	catSlug := c.Param("category_slug")

	portal, err := h.portalRepo.FindPortalBySlug(slug)
	if err != nil || portal == nil {
		c.Data(404, "text/html; charset=utf-8", []byte("<h1>404 - 帮助中心不存在</h1>"))
		return
	}

	cat, err := h.portalRepo.FindCategoryBySlug(portal.ID, catSlug)
	if err != nil || cat == nil {
		c.Data(404, "text/html; charset=utf-8", []byte("<h1>404 - 知识分类不存在</h1>"))
		return
	}

	articles, _ := h.portalRepo.ListArticles(portal.ID, &cat.ID, "published", "")

	var artsHTML strings.Builder
	artsHTML.WriteString(`<ul class="category-article-list">`)
	for _, art := range articles {
		artsHTML.WriteString(fmt.Sprintf(`
			<li>
				<a href="/hc/%s/articles/%s">
					<div class="art-title">%s</div>
					<div class="art-meta">更新于 %s · %d 次浏览</div>
				</a>
			</li>`,
			portal.Slug,
			art.Slug,
			html.EscapeString(art.Title),
			art.UpdatedAt.Format("2006-01-02"),
			art.Views,
		))
	}
	if len(articles) == 0 {
		artsHTML.WriteString(`<li class="empty-state">此分类下暂无已发布文章。</li>`)
	}
	artsHTML.WriteString(`</ul>`)

	body := fmt.Sprintf(`
		<div class="container section">
			<nav class="breadcrumb">
				<a href="/hc/%s">帮助中心</a> &gt; <span>%s</span>
			</nav>
			<div class="category-header">
				<h1>%s</h1>
				<p class="category-desc">%s</p>
			</div>
			%s
		</div>`,
		portal.Slug,
		html.EscapeString(cat.Name),
		html.EscapeString(cat.Name),
		html.EscapeString(cat.Description),
		artsHTML.String(),
	)

	pageHTML := renderPortalLayout(cat.Name+" - "+portal.Name, cat.Description, portal.Color, body, portal)
	c.Data(200, "text/html; charset=utf-8", []byte(pageHTML))
}

func (h *PortalHandler) PublicRenderArticle(c *gin.Context) {
	portalSlug := c.Param("slug")
	articleSlug := c.Param("article_slug")

	isMarkdown := false
	if strings.HasSuffix(articleSlug, ".md") {
		isMarkdown = true
		articleSlug = strings.TrimSuffix(articleSlug, ".md")
	}

	portal, err := h.portalRepo.FindPortalBySlug(portalSlug)
	if err != nil || portal == nil {
		if isMarkdown {
			c.String(404, "# 404 - Help Center Portal Not Found")
			return
		}
		c.Data(404, "text/html; charset=utf-8", []byte("<h1>404 - 帮助中心不存在</h1>"))
		return
	}

	article, err := h.portalRepo.FindArticleBySlug(portal.ID, articleSlug)
	if err != nil || article == nil {
		if isMarkdown {
			c.String(404, "# 404 - Article Not Found")
			return
		}
		c.Data(404, "text/html; charset=utf-8", []byte("<h1>404 - 文章不存在或未公开</h1>"))
		return
	}

	_ = h.portalRepo.IncrementViews(article.ID)
	article.Views++

	if isMarkdown {
		c.Data(200, "text/markdown; charset=utf-8", []byte(article.Content))
		return
	}

	catName := "通用文档"
	catSlug := ""
	if article.Category != nil {
		catName = article.Category.Name
		catSlug = article.Category.Slug
	}

	contentHTML := markdownToHTML(article.Content)

	authorName := "客服团队"
	if article.Author != nil && article.Author.Name != "" {
		authorName = article.Author.Name
	}

	body := fmt.Sprintf(`
		<div class="container section article-layout">
			<nav class="breadcrumb">
				<a href="/hc/%s">帮助中心</a> &gt;
				<a href="/hc/%s/categories/%s">%s</a> &gt;
				<span>%s</span>
			</nav>
			<article class="article-content">
				<h1 class="article-title">%s</h1>
				<div class="article-meta">
					<span>作者：%s</span>
					<span>更新时间：%s</span>
					<span>阅读：%d</span>
				</div>
				<div class="article-body">
					%s
				</div>
			</article>
		</div>`,
		portal.Slug,
		portal.Slug,
		catSlug,
		html.EscapeString(catName),
		html.EscapeString(article.Title),
		html.EscapeString(article.Title),
		html.EscapeString(authorName),
		article.UpdatedAt.Format("2006-01-02 15:04"),
		article.Views,
		contentHTML,
	)

	pageHTML := renderPortalLayout(article.Title+" - "+portal.Name, article.Title, portal.Color, body, portal)
	c.Data(200, "text/html; charset=utf-8", []byte(pageHTML))
}

func (h *PortalHandler) PublicRenderSearch(c *gin.Context) {
	slug := c.Param("slug")
	query := strings.TrimSpace(c.Query("q"))

	portal, err := h.portalRepo.FindPortalBySlug(slug)
	if err != nil || portal == nil {
		c.Data(404, "text/html; charset=utf-8", []byte("<h1>404 - 帮助中心不存在</h1>"))
		return
	}

	articles, _ := h.portalRepo.ListArticles(portal.ID, nil, "published", query)

	var artsHTML strings.Builder
	artsHTML.WriteString(`<ul class="search-article-list">`)
	for _, art := range articles {
		summary := art.Content
		if len(summary) > 160 {
			summary = summary[:160] + "..."
		}
		artsHTML.WriteString(fmt.Sprintf(`
			<li>
				<a href="/hc/%s/articles/%s">
					<div class="art-title">%s</div>
					<p class="art-summary">%s</p>
					<div class="art-meta">%s · %d 次阅读</div>
				</a>
			</li>`,
			portal.Slug,
			art.Slug,
			html.EscapeString(art.Title),
			html.EscapeString(summary),
			art.UpdatedAt.Format("2006-01-02"),
			art.Views,
		))
	}
	if len(articles) == 0 {
		artsHTML.WriteString(fmt.Sprintf(`<li class="empty-state">未找到与 “%s” 相关的文章，请尝试其他关键词。</li>`, html.EscapeString(query)))
	}
	artsHTML.WriteString(`</ul>`)

	body := fmt.Sprintf(`
		<div class="container section">
			<nav class="breadcrumb">
				<a href="/hc/%s">帮助中心</a> &gt; <span>搜索结果</span>
			</nav>
			<div class="search-header">
				<h1>搜索 “%s”</h1>
				<p>找到 %d 篇相关文章</p>
				<form class="search-box" action="/hc/%s/search" method="GET">
					<input type="text" name="q" value="%s" required />
					<button type="submit">搜索</button>
				</form>
			</div>
			%s
		</div>`,
		portal.Slug,
		html.EscapeString(query),
		len(articles),
		portal.Slug,
		html.EscapeString(query),
		artsHTML.String(),
	)

	pageHTML := renderPortalLayout("搜索："+query+" - "+portal.Name, "帮助中心搜索结果", portal.Color, body, portal)
	c.Data(200, "text/html; charset=utf-8", []byte(pageHTML))
}

func (h *PortalHandler) PublicRenderSitemap(c *gin.Context) {
	slug := c.Param("slug")
	portal, err := h.portalRepo.FindPortalBySlug(slug)
	if err != nil || portal == nil {
		c.String(404, "Portal not found")
		return
	}

	articles, _ := h.portalRepo.ListArticles(portal.ID, nil, "published", "")
	categories, _ := h.portalRepo.ListCategories(portal.ID)

	scheme := "http"
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := c.Request.Host
	if host == "" {
		host = "localhost"
	}

	escapeXML := func(val string) string {
		var b strings.Builder
		_ = xml.EscapeText(&b, []byte(val))
		return b.String()
	}

	cleanPortalSlug := url.PathEscape(portal.Slug)

	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	sb.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")

	// Portal root
	rootURL := fmt.Sprintf("%s://%s/hc/%s", scheme, host, cleanPortalSlug)
	sb.WriteString(fmt.Sprintf("  <url>\n    <loc>%s</loc>\n    <changefreq>daily</changefreq>\n    <priority>1.0</priority>\n  </url>\n", escapeXML(rootURL)))

	// Categories
	for _, cat := range categories {
		cleanCatSlug := url.PathEscape(cat.Slug)
		catURL := fmt.Sprintf("%s://%s/hc/%s/categories/%s", scheme, host, cleanPortalSlug, cleanCatSlug)
		sb.WriteString(fmt.Sprintf("  <url>\n    <loc>%s</loc>\n    <changefreq>weekly</changefreq>\n    <priority>0.8</priority>\n  </url>\n", escapeXML(catURL)))
	}

	// Articles
	for _, art := range articles {
		cleanArtSlug := url.PathEscape(art.Slug)
		lastmod := art.UpdatedAt.Format("2006-01-02")
		artURL := fmt.Sprintf("%s://%s/hc/%s/articles/%s", scheme, host, cleanPortalSlug, cleanArtSlug)
		sb.WriteString(fmt.Sprintf("  <url>\n    <loc>%s</loc>\n    <lastmod>%s</lastmod>\n    <changefreq>weekly</changefreq>\n    <priority>0.9</priority>\n  </url>\n", escapeXML(artURL), escapeXML(lastmod)))
	}

	sb.WriteString(`</urlset>`)
	c.Data(200, "application/xml; charset=utf-8", []byte(sb.String()))
}

// ----------------- Helpers -----------------

func renderPortalLayout(title, description, color, bodyContent string, portal *domain.Portal) string {
	brandColor := "#1f93ff"
	if color != "" {
		brandColor = color
	}
	if description == "" {
		description = portal.Name + " 官方帮助中心与客户服务支持文档。"
	}

	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>%s</title>
	<meta name="description" content="%s">
	<meta property="og:title" content="%s">
	<meta property="og:description" content="%s">
	<meta property="og:type" content="website">
	<style>
		:root {
			--primary: %s;
			--bg: #f8fafc;
			--card-bg: #ffffff;
			--text-main: #0f172a;
			--text-muted: #64748b;
			--border: #e2e8f0;
			--radius: 12px;
		}
		* { box-sizing: border-box; margin: 0; padding: 0; }
		body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif; background: var(--bg); color: var(--text-main); line-height: 1.6; }
		a { color: var(--primary); text-decoration: none; }
		a:hover { text-decoration: underline; }
		.navbar { background: var(--card-bg); border-bottom: 1px solid var(--border); padding: 1rem 2rem; display: flex; justify-content: space-between; align-items: center; position: sticky; top: 0; z-index: 50; }
		.navbar-brand { font-size: 1.25rem; font-weight: 700; color: var(--text-main); display: flex; align-items: center; gap: 8px; }
		.hero { background: linear-gradient(135deg, var(--primary) 0%%, #0284c7 100%%); color: white; padding: 4rem 1.5rem; text-align: center; }
		.hero h1 { font-size: 2.25rem; margin-bottom: 0.75rem; }
		.hero-sub { font-size: 1.125rem; opacity: 0.9; margin-bottom: 2rem; }
		.search-box { display: flex; max-width: 600px; margin: 0 auto; gap: 8px; }
		.search-box input { flex: 1; padding: 12px 18px; border-radius: 9999px; border: none; font-size: 1rem; outline: none; box-shadow: 0 4px 6px -1px rgba(0,0,0,0.1); }
		.search-box button { background: var(--text-main); color: white; border: none; padding: 12px 24px; border-radius: 9999px; font-weight: 600; cursor: pointer; transition: opacity 0.2s; }
		.search-box button:hover { opacity: 0.9; }
		.container { max-width: 1080px; margin: 0 auto; padding: 2rem 1.5rem; }
		.section { margin-top: 1rem; }
		.section h2 { font-size: 1.5rem; margin-bottom: 1.5rem; color: var(--text-main); }
		.categories-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(280px, 1fr)); gap: 1.5rem; margin-bottom: 3rem; }
		.category-card { background: var(--card-bg); border: 1px solid var(--border); border-radius: var(--radius); padding: 1.5rem; transition: transform 0.2s, box-shadow 0.2s; display: block; color: inherit; }
		.category-card:hover { transform: translateY(-3px); box-shadow: 0 10px 15px -3px rgba(0,0,0,0.08); text-decoration: none; }
		.category-icon { font-size: 2rem; margin-bottom: 0.75rem; }
		.category-title { font-size: 1.25rem; font-weight: 600; margin-bottom: 0.5rem; }
		.category-desc { color: var(--text-muted); font-size: 0.9rem; }
		.recent-articles, .category-article-list, .search-article-list { background: var(--card-bg); border: 1px solid var(--border); border-radius: var(--radius); padding: 1.5rem; list-style: none; }
		.recent-articles ul, .category-article-list, .search-article-list { list-style: none; }
		.recent-articles li, .category-article-list li, .search-article-list li { border-bottom: 1px solid var(--border); padding: 1rem 0; }
		.recent-articles li:last-child, .category-article-list li:last-child, .search-article-list li:last-child { border-bottom: none; }
		.art-title { font-weight: 600; font-size: 1.1rem; color: var(--text-main); margin-bottom: 0.25rem; }
		.art-views, .art-meta { font-size: 0.85rem; color: var(--text-muted); }
		.art-summary { color: var(--text-muted); font-size: 0.95rem; margin: 0.25rem 0; }
		.breadcrumb { margin-bottom: 1.5rem; color: var(--text-muted); font-size: 0.9rem; }
		.breadcrumb a { color: var(--text-muted); }
		.article-layout { background: var(--card-bg); border: 1px solid var(--border); border-radius: var(--radius); padding: 2.5rem; margin-top: 2rem; }
		.article-title { font-size: 2rem; margin-bottom: 1rem; line-height: 1.3; }
		.article-meta { display: flex; gap: 1.5rem; font-size: 0.875rem; color: var(--text-muted); border-bottom: 1px solid var(--border); padding-bottom: 1.25rem; margin-bottom: 2rem; }
		.article-body { font-size: 1.05rem; line-height: 1.8; color: #1e293b; }
		.article-body h2 { font-size: 1.5rem; margin: 1.5rem 0 0.75rem; }
		.article-body h3 { font-size: 1.25rem; margin: 1.25rem 0 0.5rem; }
		.article-body p { margin-bottom: 1rem; }
		.article-body ul { margin-left: 1.5rem; margin-bottom: 1rem; }
		.article-body pre { background: #1e293b; color: #f8fafc; padding: 1rem; border-radius: 8px; overflow-x: auto; margin-bottom: 1rem; }
		.article-body code { font-family: monospace; background: #e2e8f0; padding: 2px 6px; border-radius: 4px; font-size: 0.9em; }
		.article-body pre code { background: none; padding: 0; color: inherit; }
		.footer { text-align: center; padding: 3rem 1rem; color: var(--text-muted); font-size: 0.875rem; border-top: 1px solid var(--border); margin-top: 4rem; }
		.empty-state { text-align: center; color: var(--text-muted); padding: 2rem 0; }
	</style>
</head>
<body>
	<header class="navbar">
		<a class="navbar-brand" href="/hc/%s">
			<span>%s</span>
		</a>
		<a href="/hc/%s" style="font-weight: 500;">首页</a>
	</header>
	<main>
		%s
	</main>
	<footer class="footer">
		<p>&copy; %d %s. All rights reserved. Powered by OracleBetX ExChat.</p>
	</footer>
</body>
</html>`,
		html.EscapeString(title),
		html.EscapeString(description),
		html.EscapeString(title),
		html.EscapeString(description),
		brandColor,
		portal.Slug,
		html.EscapeString(portal.Name),
		portal.Slug,
		bodyContent,
		time.Now().Year(),
		html.EscapeString(portal.Name),
	)
}

func markdownToHTML(md string) string {
	if strings.TrimSpace(md) == "" {
		return ""
	}

	lines := strings.Split(md, "\n")
	var out strings.Builder
	inList := false
	inCode := false

	codeFence := regexp.MustCompile("^```")
	boldRegex := regexp.MustCompile(`\*\*(.*?)\*\*`)
	italicRegex := regexp.MustCompile(`\*(.*?)\*`)
	inlineCodeRegex := regexp.MustCompile("`([^`]+)`")
	linkRegex := regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)

	renderLinks := func(text string) string {
		return linkRegex.ReplaceAllStringFunc(text, func(m string) string {
			sub := linkRegex.FindStringSubmatch(m)
			if len(sub) < 3 {
				return m
			}
			linkText := sub[1]
			linkURL := strings.TrimSpace(sub[2])

			// Validate URL scheme: allow http, https, mailto, tel, relative paths / and anchor #
			lowerURL := strings.ToLower(linkURL)
			if strings.HasPrefix(lowerURL, "javascript:") ||
				strings.HasPrefix(lowerURL, "data:") ||
				strings.HasPrefix(lowerURL, "vbscript:") ||
				strings.HasPrefix(lowerURL, "file:") {
				return linkText
			}

			return fmt.Sprintf(`<a href="%s" target="_blank" rel="noopener noreferrer">%s</a>`, linkURL, linkText)
		})
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if codeFence.MatchString(trimmed) {
			if inCode {
				out.WriteString("</code></pre>\n")
				inCode = false
			} else {
				if inList {
					out.WriteString("</ul>\n")
					inList = false
				}
				out.WriteString("<pre><code>")
				inCode = true
			}
			continue
		}

		if inCode {
			out.WriteString(html.EscapeString(line) + "\n")
			continue
		}

		if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			if !inList {
				out.WriteString("<ul>\n")
				inList = true
			}
			item := strings.TrimPrefix(strings.TrimPrefix(trimmed, "- "), "* ")
			item = html.EscapeString(item)
			item = boldRegex.ReplaceAllString(item, "<strong>$1</strong>")
			item = italicRegex.ReplaceAllString(item, "<em>$1</em>")
			item = inlineCodeRegex.ReplaceAllString(item, "<code>$1</code>")
			item = renderLinks(item)
			out.WriteString(fmt.Sprintf("<li>%s</li>\n", item))
			continue
		}

		if inList {
			out.WriteString("</ul>\n")
			inList = false
		}

		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "### ") {
			out.WriteString(fmt.Sprintf("<h3>%s</h3>\n", html.EscapeString(strings.TrimPrefix(trimmed, "### "))))
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			out.WriteString(fmt.Sprintf("<h2>%s</h2>\n", html.EscapeString(strings.TrimPrefix(trimmed, "## "))))
			continue
		}
		if strings.HasPrefix(trimmed, "# ") {
			out.WriteString(fmt.Sprintf("<h1>%s</h1>\n", html.EscapeString(strings.TrimPrefix(trimmed, "# "))))
			continue
		}

		content := html.EscapeString(trimmed)
		content = boldRegex.ReplaceAllString(content, "<strong>$1</strong>")
		content = italicRegex.ReplaceAllString(content, "<em>$1</em>")
		content = inlineCodeRegex.ReplaceAllString(content, "<code>$1</code>")
		content = renderLinks(content)
		out.WriteString(fmt.Sprintf("<p>%s</p>\n", content))
	}

	if inList {
		out.WriteString("</ul>\n")
	}
	if inCode {
		out.WriteString("</code></pre>\n")
	}

	return out.String()
}
