package handler

import (
	"encoding/csv"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

type ReportHandler struct {
	reportService *service.ReportService
}

func NewReportHandler(reportService *service.ReportService) *ReportHandler {
	return &ReportHandler{
		reportService: reportService,
	}
}

func parseReportFilter(c *gin.Context) service.ReportFilter {
	filter := service.ReportFilter{}
	if s := strings.TrimSpace(c.Query("since")); s != "" {
		if sec, err := strconv.ParseInt(s, 10, 64); err == nil {
			t := time.Unix(sec, 0).UTC()
			filter.Since = &t
		} else if t, err := time.Parse(time.RFC3339, s); err == nil {
			filter.Since = &t
		}
	}
	if u := strings.TrimSpace(c.Query("until")); u != "" {
		if sec, err := strconv.ParseInt(u, 10, 64); err == nil {
			t := time.Unix(sec, 0).UTC()
			filter.Until = &t
		} else if t, err := time.Parse(time.RFC3339, u); err == nil {
			filter.Until = &t
		}
	}
	bh := strings.ToLower(strings.TrimSpace(c.Query("business_hours")))
	if bh == "true" || bh == "1" {
		filter.BusinessHours = true
	}
	return filter
}

func (h *ReportHandler) GetSummary(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)
	filter := parseReportFilter(c)

	summary, err := h.reportService.GetAccountSummary(accountID, filter)
	if err != nil {
		response.InternalError(c, "Failed to generate report summary")
		return
	}

	response.Success(c, summary)
}

func (h *ReportHandler) GetAgentMetrics(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)
	filter := parseReportFilter(c)

	metrics, err := h.reportService.GetAgentMetrics(accountID, filter)
	if err != nil {
		response.InternalError(c, "Failed to generate agent metrics")
		return
	}

	response.Success(c, metrics)
}

func (h *ReportHandler) GetTrends(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	days := 7
	if dStr := c.Query("days"); dStr != "" {
		if d, err := strconv.Atoi(dStr); err == nil && d > 0 {
			days = d
		}
	}

	trends, err := h.reportService.GetConversationTrends(accountID, days)
	if err != nil {
		response.InternalError(c, "Failed to generate conversation trends")
		return
	}

	response.Success(c, trends)
}

func (h *ReportHandler) GetTeamMetrics(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)
	filter := parseReportFilter(c)

	metrics, err := h.reportService.GetTeamMetrics(accountID, filter)
	if err != nil {
		response.InternalError(c, "Failed to generate team metrics")
		return
	}

	response.Success(c, metrics)
}

func (h *ReportHandler) GetInboxMetrics(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)
	filter := parseReportFilter(c)

	metrics, err := h.reportService.GetInboxMetrics(accountID, filter)
	if err != nil {
		response.InternalError(c, "Failed to generate inbox metrics")
		return
	}

	response.Success(c, metrics)
}

func (h *ReportHandler) GetLabelMetrics(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)
	filter := parseReportFilter(c)

	metrics, err := h.reportService.GetLabelMetrics(accountID, filter)
	if err != nil {
		response.InternalError(c, "Failed to generate label metrics")
		return
	}

	response.Success(c, metrics)
}

func (h *ReportHandler) GetFirstResponseDistribution(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)
	filter := parseReportFilter(c)

	dist, err := h.reportService.GetFirstResponseDistribution(accountID, filter)
	if err != nil {
		response.InternalError(c, "Failed to calculate first response distribution")
		return
	}

	response.Success(c, dist)
}

func (h *ReportHandler) ExportConversationsCSV(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)
	filter := parseReportFilter(c)

	convs, err := h.reportService.GetConversationsForExport(accountID, filter)
	if err != nil {
		response.InternalError(c, "Failed to export conversations")
		return
	}

	c.Header("Content-Type", "text/csv")
	c.Header("Content-Disposition", "attachment;filename=conversations_report.csv")

	w := csv.NewWriter(c.Writer)
	_ = w.Write([]string{"ID", "DisplayID", "Status", "Priority", "InboxID", "ContactID", "AssigneeID", "CreatedAt", "LastActivityAt"})

	for _, conv := range convs {
		assignee := ""
		if conv.AssigneeID != nil {
			assignee = strconv.FormatUint(uint64(*conv.AssigneeID), 10)
		}
		_ = w.Write([]string{
			strconv.FormatUint(uint64(conv.ID), 10),
			strconv.FormatUint(uint64(conv.DisplayID), 10),
			conv.Status,
			conv.Priority,
			strconv.FormatUint(uint64(conv.InboxID), 10),
			strconv.FormatUint(uint64(conv.ContactID), 10),
			assignee,
			conv.CreatedAt.Format(time.RFC3339),
			conv.LastActivityAt.Format(time.RFC3339),
		})
	}
	w.Flush()
}

