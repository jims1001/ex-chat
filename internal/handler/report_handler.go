package handler

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
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

func parseTimeParam(param string, isEnd bool) *time.Time {
	param = strings.TrimSpace(param)
	if param == "" {
		return nil
	}
	if sec, err := strconv.ParseInt(param, 10, 64); err == nil {
		t := time.Unix(sec, 0).UTC()
		return &t
	}
	if t, err := time.Parse(time.RFC3339, param); err == nil {
		utc := t.UTC()
		return &utc
	}
	if t, err := time.Parse("2006-01-02", param); err == nil {
		if isEnd {
			utc := time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 999999999, time.UTC)
			return &utc
		}
		utc := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
		return &utc
	}
	return nil
}

func parseReportFilter(c *gin.Context) service.ReportFilter {
	filter := service.ReportFilter{}

	sinceParam := c.Query("since")
	if sinceParam == "" {
		sinceParam = c.Query("from")
	}
	untilParam := c.Query("until")
	if untilParam == "" {
		untilParam = c.Query("to")
	}

	filter.Since = parseTimeParam(sinceParam, false)
	filter.Until = parseTimeParam(untilParam, true)

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
		logger.WithComponent("report").Error("failed to generate report summary",
			"account_id", accountID,
			"error", err.Error(),
		)
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
		logger.WithComponent("report").Error("failed to generate agent metrics",
			"account_id", accountID,
			"error", err.Error(),
		)
		response.InternalError(c, "Failed to generate agent metrics")
		return
	}

	response.Success(c, metrics)
}

func (h *ReportHandler) GetTrends(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)
	filter := parseReportFilter(c)

	days := 7
	if dStr := c.Query("days"); dStr != "" {
		if d, err := strconv.Atoi(dStr); err == nil && d > 0 {
			days = d
		}
	}

	trends, err := h.reportService.GetConversationTrends(accountID, days, filter)
	if err != nil {
		logger.WithComponent("report").Error("failed to generate conversation trends",
			"account_id", accountID,
			"days", days,
			"error", err.Error(),
		)
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
		logger.WithComponent("report").Error("failed to generate team metrics",
			"account_id", accountID,
			"error", err.Error(),
		)
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
		logger.WithComponent("report").Error("failed to generate inbox metrics",
			"account_id", accountID,
			"error", err.Error(),
		)
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
		logger.WithComponent("report").Error("failed to generate label metrics",
			"account_id", accountID,
			"error", err.Error(),
		)
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
		logger.WithComponent("report").Error("failed to calculate first response distribution",
			"account_id", accountID,
			"error", err.Error(),
		)
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
		logger.WithComponent("report").Error("failed to export conversations CSV",
			"account_id", accountID,
			"error", err.Error(),
		)
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

	logger.WithComponent("report").Info("exported conversations CSV successfully",
		"account_id", accountID,
		"exported_count", len(convs),
	)
}

func parseUintList(c *gin.Context, key string) []uint {
	rawArr := c.QueryArray(key)
	if len(rawArr) == 0 {
		raw := c.Query(key)
		if raw != "" {
			rawArr = strings.Split(raw, ",")
		}
	}
	var res []uint
	for _, item := range rawArr {
		item = strings.Trim(strings.TrimSpace(item), "[]")
		for _, part := range strings.Split(item, ",") {
			part = strings.TrimSpace(part)
			if id, err := strconv.ParseUint(part, 10, 64); err == nil && id > 0 {
				res = append(res, uint(id))
			}
		}
	}
	return res
}

// GetConversationsReport returns live conversation metrics by account or per-agent breakdown
func (h *ReportHandler) GetConversationsReport(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	repType := c.DefaultQuery("type", "account")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "25"))

	data, err := h.reportService.GetConversationsReport(accountID, repType, page, perPage)
	if err != nil {
		logger.WithComponent("report").Error("failed to get conversations report", "account_id", accountID, "error", err.Error())
		response.InternalError(c, "Failed to get conversations report")
		return
	}
	c.JSON(http.StatusOK, data)
}

// GetConversationsSummary returns conversations summary as JSON or CSV
func (h *ReportHandler) GetConversationsSummary(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)
	filter := parseReportFilter(c)

	summary, err := h.reportService.GetAccountSummary(accountID, filter)
	if err != nil {
		logger.WithComponent("report").Error("failed to generate conversations summary", "account_id", accountID, "error", err.Error())
		response.InternalError(c, "Failed to generate conversations summary")
		return
	}

	if c.Query("format") == "csv" || strings.Contains(c.GetHeader("Accept"), "text/csv") {
		c.Header("Content-Type", "text/csv")
		c.Header("Content-Disposition", "attachment;filename=conversations_summary_report.csv")
		w := csv.NewWriter(c.Writer)
		_ = w.Write([]string{"Conversations", "Incoming Messages", "Outgoing Messages", "Avg First Response Time (s)", "Avg Resolution Time (s)", "Resolutions Count", "Reply Time (s)"})
		_ = w.Write([]string{
			strconv.FormatInt(summary.ConversationsCount, 10),
			strconv.FormatInt(summary.IncomingMessagesCount, 10),
			strconv.FormatInt(summary.OutgoingMessagesCount, 10),
			strconv.FormatFloat(summary.AvgFirstResponseTime, 'f', 1, 64),
			strconv.FormatFloat(summary.AvgResolutionTime, 'f', 1, 64),
			strconv.FormatInt(summary.ResolutionsCount, 10),
			strconv.FormatFloat(summary.AvgReplyTime, 'f', 1, 64),
		})
		w.Flush()
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    summary,
		"payload": summary,
	})
}

// GetConversationTraffic produces an hourly heatmap traffic report
func (h *ReportHandler) GetConversationTraffic(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)
	filter := parseReportFilter(c)

	tzOffset := 0.0
	if tzStr := c.Query("timezone_offset"); tzStr != "" {
		if val, err := strconv.ParseFloat(tzStr, 64); err == nil {
			tzOffset = val
		}
	}

	traffic, err := h.reportService.GetConversationTraffic(accountID, filter, tzOffset)
	if err != nil {
		logger.WithComponent("report").Error("failed to get conversation traffic", "account_id", accountID, "error", err.Error())
		response.InternalError(c, "Failed to get conversation traffic")
		return
	}

	if c.Query("format") == "csv" || strings.Contains(c.GetHeader("Accept"), "text/csv") {
		c.Header("Content-Type", "text/csv")
		c.Header("Content-Disposition", "attachment;filename=conversation_traffic_reports.csv")
		w := csv.NewWriter(c.Writer)
		for _, row := range traffic {
			strRow := make([]string, len(row))
			for i, val := range row {
				strRow[i] = fmt.Sprintf("%v", val)
			}
			_ = w.Write(strRow)
		}
		w.Flush()
		return
	}

	c.JSON(http.StatusOK, traffic)
}

// GetDrilldown returns paginated records for a specific metric bucket
func (h *ReportHandler) GetDrilldown(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)
	filter := parseReportFilter(c)

	metric := strings.TrimSpace(c.Query("metric"))
	if metric == "" {
		response.BadRequest(c, "metric is required")
		return
	}

	bucketStr := strings.TrimSpace(c.Query("bucket_timestamp"))
	if bucketStr == "" {
		response.BadRequest(c, "bucket_timestamp is required")
		return
	}

	bucketTime := time.Now().UTC()
	if sec, err := strconv.ParseInt(bucketStr, 10, 64); err == nil {
		bucketTime = time.Unix(sec, 0).UTC()
	} else if t, err := time.Parse(time.RFC3339, bucketStr); err == nil {
		bucketTime = t.UTC()
	} else if t, err := time.Parse("2006-01-02", bucketStr); err == nil {
		bucketTime = t.UTC()
	}

	dimType := c.DefaultQuery("type", "account")
	var dimID *uint
	if idStr := c.Query("id"); idStr != "" {
		if id, err := strconv.ParseUint(idStr, 10, 64); err == nil {
			uID := uint(id)
			dimID = &uID
		}
	}

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "25"))

	rep, err := h.reportService.GetDrilldown(accountID, metric, dimType, dimID, bucketTime, filter, page, perPage)
	if err != nil {
		logger.WithComponent("report").Error("failed to get drilldown report", "account_id", accountID, "error", err.Error())
		response.InternalError(c, "Failed to get drilldown report")
		return
	}

	c.JSON(http.StatusOK, rep)
}

// GetChannelSummary returns conversation volume and state distribution by channel type
func (h *ReportHandler) GetChannelSummary(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)
	filter := parseReportFilter(c)

	summary, err := h.reportService.GetChannelSummary(accountID, filter)
	if err != nil {
		logger.WithComponent("report").Error("failed to get channel summary", "account_id", accountID, "error", err.Error())
		response.InternalError(c, "Failed to get channel summary")
		return
	}

	c.JSON(http.StatusOK, summary)
}

// GetBotSummary returns comparison of bot resolutions and handoffs
func (h *ReportHandler) GetBotSummary(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)
	filter := parseReportFilter(c)

	summary, err := h.reportService.GetBotSummary(accountID, filter)
	if err != nil {
		logger.WithComponent("report").Error("failed to get bot summary", "account_id", accountID, "error", err.Error())
		response.InternalError(c, "Failed to get bot summary")
		return
	}

	c.JSON(http.StatusOK, summary)
}

// GetBotMetrics returns aggregated bot performance metrics
func (h *ReportHandler) GetBotMetrics(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)
	filter := parseReportFilter(c)

	metrics, err := h.reportService.GetBotMetrics(accountID, filter)
	if err != nil {
		logger.WithComponent("report").Error("failed to get bot metrics", "account_id", accountID, "error", err.Error())
		response.InternalError(c, "Failed to get bot metrics")
		return
	}

	c.JSON(http.StatusOK, metrics)
}

// GetInboxLabelMatrix returns 2D matrix of inboxes vs labels
func (h *ReportHandler) GetInboxLabelMatrix(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)
	filter := parseReportFilter(c)

	inboxIDs := parseUintList(c, "inbox_ids")
	labelIDs := parseUintList(c, "label_ids")

	matrix, err := h.reportService.GetInboxLabelMatrix(accountID, filter, inboxIDs, labelIDs)
	if err != nil {
		logger.WithComponent("report").Error("failed to get inbox-label matrix", "account_id", accountID, "error", err.Error())
		response.InternalError(c, "Failed to get inbox-label matrix")
		return
	}

	c.JSON(http.StatusOK, matrix)
}

// GetOutgoingMessagesCount aggregates outgoing messages by group_by dimension
func (h *ReportHandler) GetOutgoingMessagesCount(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)
	filter := parseReportFilter(c)

	groupBy := strings.ToLower(strings.TrimSpace(c.Query("group_by")))
	if groupBy == "" {
		groupBy = "agent"
	}

	allowed := map[string]bool{"agent": true, "user": true, "team": true, "inbox": true, "label": true}
	if !allowed[groupBy] {
		response.BadRequest(c, "invalid group_by parameter: must be agent, team, inbox, or label")
		return
	}

	counts, err := h.reportService.GetOutgoingMessagesCount(accountID, filter, groupBy)
	if err != nil {
		logger.WithComponent("report").Error("failed to get outgoing messages count", "account_id", accountID, "error", err.Error())
		response.InternalError(c, "Failed to get outgoing messages count")
		return
	}

	c.JSON(http.StatusOK, counts)
}

// GetLiveConversationMetrics returns live open, unattended, unassigned conversation counts
func (h *ReportHandler) GetLiveConversationMetrics(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	var teamID *uint
	if tStr := c.Query("team_id"); tStr != "" {
		if tid, err := strconv.ParseUint(tStr, 10, 64); err == nil && tid > 0 {
			uTid := uint(tid)
			teamID = &uTid
		}
	}

	metrics, err := h.reportService.GetLiveConversationMetrics(accountID, teamID)
	if err != nil {
		logger.WithComponent("report").Error("failed to get live conversation metrics", "account_id", accountID, "error", err.Error())
		response.InternalError(c, "Failed to get live conversation metrics")
		return
	}

	c.JSON(http.StatusOK, metrics)
}

// GetGroupedLiveMetrics returns live metrics grouped by team_id or assignee_id
func (h *ReportHandler) GetGroupedLiveMetrics(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	groupBy := strings.ToLower(strings.TrimSpace(c.Query("group_by")))
	if groupBy == "" {
		groupBy = "assignee_id"
	}

	var teamID *uint
	if tStr := c.Query("team_id"); tStr != "" {
		if tid, err := strconv.ParseUint(tStr, 10, 64); err == nil && tid > 0 {
			uTid := uint(tid)
			teamID = &uTid
		}
	}

	metrics, err := h.reportService.GetGroupedLiveMetrics(accountID, groupBy, teamID)
	if err != nil {
		logger.WithComponent("report").Error("failed to get grouped live metrics", "account_id", accountID, "error", err.Error())
		response.InternalError(c, "Failed to get grouped live metrics")
		return
	}

	c.JSON(http.StatusOK, metrics)
}

// GetYearInReview returns annual service highlights for an agent
func (h *ReportHandler) GetYearInReview(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	rawUserID, _ := c.Get(middleware.ContextUserID)
	userID := uint(0)
	if rawUserID != nil {
		if u, ok := rawUserID.(uint); ok {
			userID = u
		}
	}

	if uStr := c.Query("user_id"); uStr != "" {
		if uid, err := strconv.ParseUint(uStr, 10, 64); err == nil && uid > 0 {
			userID = uint(uid)
		}
	}

	year := time.Now().Year()
	if yStr := c.Query("year"); yStr != "" {
		if y, err := strconv.Atoi(yStr); err == nil && y > 2000 {
			year = y
		}
	}

	review, err := h.reportService.GetYearInReview(accountID, userID, year)
	if err != nil {
		logger.WithComponent("report").Error("failed to get year in review", "account_id", accountID, "error", err.Error())
		response.InternalError(c, "Failed to get year in review")
		return
	}

	c.JSON(http.StatusOK, review)
}


