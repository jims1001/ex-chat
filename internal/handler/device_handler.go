package handler

import (
	"net/http"
	"strconv"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/response"
	"github.com/gin-gonic/gin"
)

type DeviceHandler struct {
	deviceRepo *repository.DeviceRepository
}

func NewDeviceHandler(deviceRepo *repository.DeviceRepository) *DeviceHandler {
	return &DeviceHandler{deviceRepo: deviceRepo}
}

type RegisterSubscriptionReq struct {
	SubscriptionType string `json:"subscription_type"` // fcm, apns, browser
	Platform         string `json:"platform"`
	PushToken        string `json:"push_token"`
	DeviceToken      string `json:"device_token"`
	DeviceName       string `json:"device_name"`
	AppVersion       string `json:"app_version"`
}

func (h *DeviceHandler) RegisterSubscription(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	userID := c.GetUint("user_id")

	var req RegisterSubscriptionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	subType := req.SubscriptionType
	if subType == "" {
		subType = req.Platform
	}
	if subType == "" {
		subType = "fcm"
	}

	pushToken := req.PushToken
	if pushToken == "" {
		pushToken = req.DeviceToken
	}
	if pushToken == "" {
		response.BadRequest(c, "push_token or device_token is required")
		return
	}

	sub := domain.NotificationSubscription{
		UserID:           userID,
		AccountID:        uint(accID),
		SubscriptionType: subType,
		PushToken:        pushToken,
		DeviceName:       req.DeviceName,
		AppVersion:       req.AppVersion,
	}

	if err := h.deviceRepo.UpsertSubscription(c.Request.Context(), &sub); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Created(c, sub)
}

type DeleteSubscriptionReq struct {
	PushToken string `json:"push_token" binding:"required"`
}

func (h *DeviceHandler) DeleteSubscription(c *gin.Context) {
	accID, _ := strconv.ParseUint(c.Param("account_id"), 10, 32)
	userID := c.GetUint("user_id")

	var req DeleteSubscriptionReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if err := h.deviceRepo.DeleteSubscription(c.Request.Context(), userID, uint(accID), req.PushToken); err != nil {
		response.Error(c, http.StatusInternalServerError, err.Error())
		return
	}

	response.Success(c, gin.H{"deleted": true})
}
