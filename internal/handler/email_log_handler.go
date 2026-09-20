package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type EmailLogHandler struct {
	repository *repository.EmailLogRepository
	service    *service.EmailService
}

func NewEmailLogHandler(repository *repository.EmailLogRepository, service *service.EmailService) *EmailLogHandler {
	return &EmailLogHandler{repository: repository, service: service}
}

func (h *EmailLogHandler) List(c *gin.Context) {
	accountID, ok := pathUint(c, "account_id")
	if !ok {
		return
	}
	logs, err := h.repository.List(accountID, c.Query("status"), c.Query("email_type"))
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, logs)
}

func (h *EmailLogHandler) Retry(c *gin.Context) {
	accountID, logID, ok := emailLogIDs(c)
	if !ok {
		return
	}
	emailLog, retried, err := h.service.RetryFailedEmail(c.Request.Context(), accountID, logID)
	if err != nil {
		emailLogError(c, err)
		return
	}
	response.Success(c, gin.H{"retried": retried, "email_log": emailLog})
}

func (h *EmailLogHandler) Bounce(c *gin.Context) {
	accountID, logID, ok := emailLogIDs(c)
	if !ok {
		return
	}
	if _, err := h.repository.Find(accountID, logID); err != nil {
		emailLogError(c, err)
		return
	}
	var request struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&request)
	if request.Reason == "" {
		request.Reason = "Remote mailbox unavailable (bounced)"
	}
	updated, err := h.service.RecordBounceForAccount(accountID, logID, request.Reason)
	if err != nil {
		response.InternalError(c, err.Error())
		return
	}
	response.Success(c, updated)
}

func emailLogIDs(c *gin.Context) (uint, uint, bool) {
	accountID, accountOK := pathUint(c, "account_id")
	logID, logOK := pathUint(c, "id")
	return accountID, logID, accountOK && logOK
}

func pathUint(c *gin.Context, name string) (uint, bool) {
	value, err := strconv.ParseUint(c.Param(name), 10, 64)
	if err != nil || value == 0 {
		response.Error(c, http.StatusBadRequest, "Invalid path parameter")
		return 0, false
	}
	return uint(value), true
}

func emailLogError(c *gin.Context, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		response.NotFound(c, "Email log not found")
		return
	}
	response.InternalError(c, err.Error())
}
