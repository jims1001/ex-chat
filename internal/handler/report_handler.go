package handler

import (
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

func (h *ReportHandler) GetSummary(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	summary, err := h.reportService.GetAccountSummary(accountID)
	if err != nil {
		response.InternalError(c, "Failed to generate report summary")
		return
	}

	response.Success(c, summary)
}

func (h *ReportHandler) GetAgentMetrics(c *gin.Context) {
	rawAccountID, _ := c.Get(middleware.ContextAccountID)
	accountID := rawAccountID.(uint)

	metrics, err := h.reportService.GetAgentMetrics(accountID)
	if err != nil {
		response.InternalError(c, "Failed to generate agent metrics")
		return
	}

	response.Success(c, metrics)
}
