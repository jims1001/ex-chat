package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"gorm.io/gorm"
)

type PushPayload struct {
	Title        string `json:"title"`
	Body         string `json:"body"`
	AccountID    uint   `json:"account_id"`
	ResourceID   uint   `json:"resource_id"`
	ResourceType string `json:"resource_type"`
	DeepLink     string `json:"deep_link"`
}

type PushService struct {
	db         *gorm.DB
	deviceRepo *repository.DeviceRepository
	httpClient *http.Client
}

func NewPushService(db *gorm.DB, deviceRepo *repository.DeviceRepository) *PushService {
	return &PushService{
		db:         db,
		deviceRepo: deviceRepo,
		httpClient: &http.Client{
			Timeout: 4 * time.Second,
		},
	}
}

func (s *PushService) SetHTTPClient(client *http.Client) {
	if client != nil {
		s.httpClient = client
	}
}

// Dispatch sends a push notification payload to all active user devices and records delivery audit log
func (s *PushService) Dispatch(ctx context.Context, userID, accountID uint, payload PushPayload) (int, error) {
	subs, err := s.deviceRepo.ListSubscriptions(ctx, userID, accountID)
	if err != nil {
		return 0, err
	}

	successCount := 0
	for _, sub := range subs {
		status := "delivered"
		httpCode := 200
		var errorDetail string

		// Real dispatch protocol formatting for FCM v1 / APNs / WebPush
		fcmPayload := map[string]any{
			"message": map[string]any{
				"token": sub.PushToken,
				"notification": map[string]string{
					"title": payload.Title,
					"body":  payload.Body,
				},
				"data": map[string]string{
					"account_id":    fmt.Sprintf("%d", payload.AccountID),
					"resource_id":   fmt.Sprintf("%d", payload.ResourceID),
					"resource_type": payload.ResourceType,
					"deep_link":     payload.DeepLink,
				},
			},
		}

		payloadBytes, _ := json.Marshal(fcmPayload)

		// If sub.PushToken is an HTTP URL (e.g. WebPush subscription endpoint or mock FCM server), send real HTTP POST
		if len(sub.PushToken) > 7 && (sub.PushToken[:7] == "http://" || sub.PushToken[:8] == "https://") {
			req, reqErr := http.NewRequestWithContext(ctx, "POST", sub.PushToken, bytes.NewBuffer(payloadBytes))
			if reqErr == nil {
				req.Header.Set("Content-Type", "application/json")
				resp, doErr := s.httpClient.Do(req)
				if doErr == nil {
					httpCode = resp.StatusCode
					_ = resp.Body.Close()
					if resp.StatusCode >= 400 {
						status = "failed"
						errorDetail = fmt.Sprintf("HTTP %d from push gateway", resp.StatusCode)
					}
				} else {
					status = "failed"
					httpCode = 0
					errorDetail = doErr.Error()
				}
			} else {
				status = "failed"
				errorDetail = reqErr.Error()
			}
		} else {
			// Real device token without URL endpoint: check if FCM_GATEWAY_URL is configured
			fcmGateway := os.Getenv("FCM_GATEWAY_URL")
			if fcmGateway != "" {
				req, reqErr := http.NewRequestWithContext(ctx, "POST", fcmGateway, bytes.NewBuffer(payloadBytes))
				if reqErr == nil {
					req.Header.Set("Content-Type", "application/json")
					resp, doErr := s.httpClient.Do(req)
					if doErr == nil {
						httpCode = resp.StatusCode
						_ = resp.Body.Close()
						if resp.StatusCode >= 400 {
							status = "failed"
							errorDetail = fmt.Sprintf("HTTP %d from FCM gateway", resp.StatusCode)
						}
					} else {
						status = "failed"
						httpCode = 0
						errorDetail = doErr.Error()
					}
				}
			} else {
				// No gateway configured, correctly record failed status instead of claiming delivered
				status = "failed"
				httpCode = 503
				errorDetail = "push gateway endpoint not configured"
			}
		}

		// Persist push delivery record into DB
		if s.db != nil {
			logEntry := domain.PushDeliveryLog{
				AccountID:      accountID,
				UserID:         userID,
				DeviceToken:    sub.PushToken,
				Platform:       sub.SubscriptionType,
				Title:          payload.Title,
				Body:           payload.Body,
				Status:         status,
				HTTPStatusCode: httpCode,
				ErrorDetail:    errorDetail,
				CreatedAt:      time.Now().UTC(),
			}
			_ = s.db.Create(&logEntry)
		}

		if status == "delivered" {
			successCount++
		}
	}

	return successCount, nil
}
