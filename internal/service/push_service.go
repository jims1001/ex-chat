package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"gorm.io/gorm"
)

type PushPayload struct {
	Title            string `json:"title"`
	Body             string `json:"body"`
	AccountID        uint   `json:"account_id"`
	ResourceID       uint   `json:"resource_id"`
	ResourceType     string `json:"resource_type"`
	DeepLink         string `json:"deep_link"`
	NotificationType string `json:"notification_type,omitempty"`
}

type PushService struct {
	db               *gorm.DB
	deviceRepo       *repository.DeviceRepository
	notificationRepo *repository.NotificationRepository
	httpClient       *http.Client
	dedupCache       sync.Map
}

func NewPushService(db *gorm.DB, deviceRepo *repository.DeviceRepository) *PushService {
	var notifRepo *repository.NotificationRepository
	if db != nil {
		notifRepo = repository.NewNotificationRepository(db)
	}
	return &PushService{
		db:               db,
		deviceRepo:       deviceRepo,
		notificationRepo: notifRepo,
		httpClient: &http.Client{
			Timeout: 4 * time.Second,
		},
	}
}

func (s *PushService) SetNotificationRepo(repo *repository.NotificationRepository) {
	s.notificationRepo = repo
}

func (s *PushService) SetHTTPClient(client *http.Client) {
	if client != nil {
		s.httpClient = client
	}
}

func isTimeInQuietHours(currentTime, start, end string) bool {
	if start == "" || end == "" {
		return false
	}
	if start == end {
		return false
	}
	if start < end {
		// e.g. 09:00 to 17:00
		return currentTime >= start && currentTime < end
	}
	// Overnight e.g. 22:00 to 08:00
	return currentTime >= start || currentTime < end
}

// Dispatch sends a push notification payload to all active user devices and records delivery audit log
func (s *PushService) Dispatch(ctx context.Context, userID, accountID uint, payload PushPayload) (int, error) {
	// 0. Deduplicate rapid repeated dispatches within 5 seconds for same user and payload
	dedupKey := fmt.Sprintf("%d:%d:%d:%s:%s", accountID, userID, payload.ResourceID, payload.ResourceType, payload.NotificationType)
	if lastTimeRaw, ok := s.dedupCache.Load(dedupKey); ok {
		if lastTime, ok := lastTimeRaw.(time.Time); ok && time.Since(lastTime) < 5*time.Second {
			logger.WithComponent("push").Info("push notification skipped: rapid repeated dispatch deduplicated",
				"user_id", userID, "account_id", accountID, "dedup_key", dedupKey)
			return 0, nil
		}
	}
	s.dedupCache.Store(dedupKey, time.Now())

	// 1. Check user notification settings (mute, quiet hours, push flags)
	if s.notificationRepo != nil {
		setting, err := s.notificationRepo.GetNotificationSetting(ctx, accountID, userID)
		if err == nil && setting != nil {
			// Global mute for this user
			if setting.Muted {
				logger.WithComponent("push").Info("push notification skipped: user muted all notifications",
					"user_id", userID, "account_id", accountID)
				return 0, nil
			}

			// Check quiet hours with user timezone resolution
			if setting.QuietHoursEnabled && setting.QuietHoursStart != "" && setting.QuietHoursEnd != "" {
				loc := time.UTC
				if s.db != nil {
					var u domain.User
					if s.db.Where("id = ?", userID).First(&u).Error == nil && u.Timezone != "" {
						if parsedLoc, err := time.LoadLocation(u.Timezone); err == nil {
							loc = parsedLoc
						}
					}
				}
				nowTimeStr := time.Now().In(loc).Format("15:04")
				if isTimeInQuietHours(nowTimeStr, setting.QuietHoursStart, setting.QuietHoursEnd) {
					logger.WithComponent("push").Info("push notification skipped: quiet hours active",
						"user_id", userID, "account_id", accountID, "current_time", nowTimeStr, "timezone", loc.String(),
						"quiet_start", setting.QuietHoursStart, "quiet_end", setting.QuietHoursEnd)
					return 0, nil
				}
			}

			// Check SelectedPushFlags
			if payload.NotificationType != "" {
				flagsStr := setting.SelectedPushFlags
				if strings.TrimSpace(flagsStr) == "" {
					flagsStr = "[]"
				}
				var allowedFlags []string
				_ = json.Unmarshal([]byte(flagsStr), &allowedFlags)

				allowed := false
				for _, f := range allowedFlags {
					if f == payload.NotificationType {
						allowed = true
						break
					}
				}
				if !allowed {
					logger.WithComponent("push").Info("push notification skipped: flag not enabled in selected_push_flags",
						"user_id", userID, "account_id", accountID, "notification_type", payload.NotificationType)
					return 0, nil
				}
			}
		}
	}

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
			logger.WithComponent("push").Info("push notification delivered",
				"user_id", userID,
				"account_id", accountID,
				"status", status,
				"http_code", httpCode,
			)
		} else {
			logger.WithComponent("push").Warn("push notification delivery failed",
				"user_id", userID,
				"account_id", accountID,
				"status", status,
				"http_code", httpCode,
				"error", errorDetail,
			)
		}
	}

	return successCount, nil
}
