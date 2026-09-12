package handler

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

// DashboardAppHandler handles API endpoints for embedded dashboard applications
type DashboardAppHandler struct {
	repo *repository.DashboardAppRepository
}

// NewDashboardAppHandler creates a new DashboardAppHandler
func NewDashboardAppHandler(repo *repository.DashboardAppRepository) *DashboardAppHandler {
	return &DashboardAppHandler{repo: repo}
}

func (h *DashboardAppHandler) getAccountID(c *gin.Context) uint {
	if raw, exists := c.Get(middleware.ContextAccountID); exists {
		if u, ok := raw.(uint); ok && u > 0 {
			return u
		}
	}
	if param := c.Param("account_id"); param != "" {
		if id, err := strconv.ParseUint(param, 10, 32); err == nil && id > 0 {
			return uint(id)
		}
	}
	return 0
}

func (h *DashboardAppHandler) getUserID(c *gin.Context) *uint {
	if raw, exists := c.Get(middleware.ContextUserID); exists {
		if u, ok := raw.(uint); ok && u > 0 {
			return &u
		}
	}
	return nil
}

// DashboardAppReq represents incoming payload for create and update
type DashboardAppReq struct {
	Title        string                          `json:"title"`
	Content      []domain.DashboardAppContentItem `json:"content"`
	DashboardApp *DashboardAppReq                `json:"dashboard_app"`
}

func (h *DashboardAppHandler) validateContent(content []domain.DashboardAppContentItem) bool {
	if len(content) == 0 {
		return false
	}
	for _, item := range content {
		if item.Type != "frame" {
			return false
		}
		raw := strings.TrimSpace(item.URL)
		if raw == "" && item.Link != "" {
			raw = strings.TrimSpace(item.Link)
		}
		if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
			return false
		}
		u, err := url.Parse(raw)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return false
		}
	}
	return true
}

// List handles GET /api/v1/accounts/:account_id/dashboard_apps
func (h *DashboardAppHandler) List(c *gin.Context) {
	accountID := h.getAccountID(c)
	apps, err := h.repo.List(accountID)
	if err != nil {
		logger.WithComponent("dashboard_apps").Error("failed to list dashboard apps",
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to fetch dashboard apps")
		return
	}

	if apps == nil {
		apps = []domain.DashboardApp{}
	}

	c.JSON(http.StatusOK, apps)
}

// Get handles GET /api/v1/accounts/:account_id/dashboard_apps/:id
func (h *DashboardAppHandler) Get(c *gin.Context) {
	accountID := h.getAccountID(c)
	rawID := c.Param("id")
	id, err := strconv.ParseUint(rawID, 10, 32)
	if err != nil || id == 0 {
		response.BadRequest(c, "Invalid dashboard app ID")
		return
	}

	app, err := h.repo.GetByID(accountID, uint(id))
	if err != nil || app == nil {
		response.NotFound(c, "Dashboard app not found")
		return
	}

	c.JSON(http.StatusOK, app)
}

// Create handles POST /api/v1/accounts/:account_id/dashboard_apps
func (h *DashboardAppHandler) Create(c *gin.Context) {
	accountID := h.getAccountID(c)
	var req DashboardAppReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	if req.DashboardApp != nil {
		if req.DashboardApp.Title != "" {
			req.Title = req.DashboardApp.Title
		}
		if len(req.DashboardApp.Content) > 0 {
			req.Content = req.DashboardApp.Content
		}
	}

	if strings.TrimSpace(req.Title) == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"message": "Title is required",
			"error":   "Title is required",
		})
		return
	}

	if !h.validateContent(req.Content) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"message": "Content : Invalid data",
			"error":   "Content : Invalid data",
		})
		return
	}

	for i := range req.Content {
		if req.Content[i].URL == "" && req.Content[i].Link != "" {
			req.Content[i].URL = req.Content[i].Link
		}
		if req.Content[i].Link == "" {
			req.Content[i].Link = req.Content[i].URL
		}
	}

	app := domain.DashboardApp{
		AccountID:     accountID,
		UserID:        h.getUserID(c),
		Title:         strings.TrimSpace(req.Title),
		ParsedContent: req.Content,
		ContentURL:    req.Content[0].URL,
	}

	if err := h.repo.Create(&app); err != nil {
		logger.WithComponent("dashboard_apps").Error("failed to create dashboard app",
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to create dashboard app")
		return
	}

	logger.WithComponent("dashboard_apps").Info("dashboard app created successfully",
		"account_id", accountID,
		"dashboard_app_id", app.ID,
		"title", app.Title,
	)

	c.JSON(http.StatusOK, app)
}

// Update handles PUT/PATCH /api/v1/accounts/:account_id/dashboard_apps/:id
func (h *DashboardAppHandler) Update(c *gin.Context) {
	accountID := h.getAccountID(c)
	rawID := c.Param("id")
	id, err := strconv.ParseUint(rawID, 10, 32)
	if err != nil || id == 0 {
		response.BadRequest(c, "Invalid dashboard app ID")
		return
	}

	app, err := h.repo.GetByID(accountID, uint(id))
	if err != nil || app == nil {
		response.NotFound(c, "Dashboard app not found")
		return
	}

	var req DashboardAppReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request payload: "+err.Error())
		return
	}

	if req.DashboardApp != nil {
		if req.DashboardApp.Title != "" {
			req.Title = req.DashboardApp.Title
		}
		if len(req.DashboardApp.Content) > 0 {
			req.Content = req.DashboardApp.Content
		}
	}

	if strings.TrimSpace(req.Title) != "" {
		app.Title = strings.TrimSpace(req.Title)
	}

	if len(req.Content) > 0 {
		if !h.validateContent(req.Content) {
			c.JSON(http.StatusUnprocessableEntity, gin.H{
				"message": "Content : Invalid data",
				"error":   "Content : Invalid data",
			})
			return
		}
		for i := range req.Content {
			if req.Content[i].URL == "" && req.Content[i].Link != "" {
				req.Content[i].URL = req.Content[i].Link
			}
			if req.Content[i].Link == "" {
				req.Content[i].Link = req.Content[i].URL
			}
		}
		app.ParsedContent = req.Content
		app.ContentURL = req.Content[0].URL
	}

	if err := h.repo.Update(app); err != nil {
		logger.WithComponent("dashboard_apps").Error("failed to update dashboard app",
			"account_id", accountID,
			"dashboard_app_id", id,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to update dashboard app")
		return
	}

	logger.WithComponent("dashboard_apps").Info("dashboard app updated successfully",
		"account_id", accountID,
		"dashboard_app_id", app.ID,
		"title", app.Title,
	)

	c.JSON(http.StatusOK, app)
}

// Delete handles DELETE /api/v1/accounts/:account_id/dashboard_apps/:id
func (h *DashboardAppHandler) Delete(c *gin.Context) {
	accountID := h.getAccountID(c)
	rawID := c.Param("id")
	id, err := strconv.ParseUint(rawID, 10, 32)
	if err != nil || id == 0 {
		response.BadRequest(c, "Invalid dashboard app ID")
		return
	}

	app, err := h.repo.GetByID(accountID, uint(id))
	if err != nil || app == nil {
		response.NotFound(c, "Dashboard app not found")
		return
	}

	if err := h.repo.Delete(accountID, uint(id)); err != nil {
		logger.WithComponent("dashboard_apps").Error("failed to delete dashboard app",
			"account_id", accountID,
			"dashboard_app_id", id,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to delete dashboard app")
		return
	}

	logger.WithComponent("dashboard_apps").Info("dashboard app deleted successfully",
		"account_id", accountID,
		"dashboard_app_id", id,
	)

	c.Status(http.StatusNoContent)
}
