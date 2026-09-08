package service

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"gorm.io/gorm"
)

type WebhookService struct {
	db          *gorm.DB
	webhookRepo *repository.WebhookRepository
	httpClient  *http.Client
}

func NewWebhookService(db *gorm.DB, webhookRepo *repository.WebhookRepository) *WebhookService {
	return &WebhookService{
		db:          db,
		webhookRepo: webhookRepo,
		httpClient: &http.Client{
			Timeout: 4 * time.Second,
		},
	}
}

type WebhookPayload struct {
	Event     string    `json:"event"`
	AccountID uint      `json:"account_id"`
	Timestamp time.Time `json:"timestamp"`
	Data      any       `json:"data"`
}

// Dispatch sends events to subscribed webhooks with automatic retry and persistence
func (s *WebhookService) Dispatch(accountID uint, eventName string, data any) {
	webhooks, err := s.webhookRepo.ListSubscribed(accountID, eventName)
	if err != nil || len(webhooks) == 0 {
		return
	}

	payload := WebhookPayload{
		Event:     eventName,
		AccountID: accountID,
		Timestamp: time.Now().UTC(),
		Data:      data,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return
	}

	for _, w := range webhooks {
		webhook := w
		go s.deliverWithRetry(webhook, eventName, bodyBytes)
	}
}

func (s *WebhookService) deliverWithRetry(webhook domain.Webhook, eventName string, bodyBytes []byte) {
	const maxAttempts = 3
	var lastStatusCode int
	var lastResponseBody string
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequest("POST", webhook.URL, bytes.NewBuffer(bodyBytes))
		if err != nil {
			lastErr = err
			break
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "ex-chat-webhook-engine/1.0")
		req.Header.Set("X-Chatwoot-Event", eventName)

		if webhook.Secret != "" {
			signature := s.SignPayload(bodyBytes, webhook.Secret)
			req.Header.Set("X-Chatwoot-Signature", signature)
		}

		resp, err := s.httpClient.Do(req)
		if err == nil {
			lastStatusCode = resp.StatusCode
			respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			lastResponseBody = string(respBytes)
			_ = resp.Body.Close()

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				// Successfully delivered
				s.recordDelivery(webhook.AccountID, webhook.ID, eventName, webhook.URL, string(bodyBytes), lastStatusCode, lastResponseBody, attempt, "delivered")
				return
			}
		} else {
			lastErr = err
		}

		// Backoff before retry
		if attempt < maxAttempts {
			time.Sleep(time.Duration(attempt*20) * time.Millisecond)
		}
	}

	// If all retries exhausted, record dead_letter failure
	if lastResponseBody == "" && lastErr != nil {
		lastResponseBody = lastErr.Error()
	}
	log.Printf("[WebhookService] Webhook ID=%d delivery failed after %d attempts: %v", webhook.ID, maxAttempts, lastErr)
	s.recordDelivery(webhook.AccountID, webhook.ID, eventName, webhook.URL, string(bodyBytes), lastStatusCode, lastResponseBody, maxAttempts, "dead_letter")
}

func (s *WebhookService) recordDelivery(accountID, webhookID uint, event, url, payload string, code int, respBody string, attempts int, status string) {
	if s.db == nil {
		return
	}
	del := domain.WebhookDelivery{
		AccountID:    accountID,
		WebhookID:    webhookID,
		Event:        event,
		URL:          url,
		Payload:      payload,
		ResponseCode: code,
		ResponseBody: respBody,
		Attempts:     attempts,
		Status:       status,
		CreatedAt:    time.Now().UTC(),
	}
	_ = s.db.Create(&del)
}

func (s *WebhookService) ListDeliveries(accountID, webhookID uint, page, pageSize int) ([]domain.WebhookDelivery, int64, error) {
	var deliveries []domain.WebhookDelivery
	var total int64

	query := s.db.Model(&domain.WebhookDelivery{}).Where("account_id = ?", accountID)
	if webhookID > 0 {
		query = query.Where("webhook_id = ?", webhookID)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Offset(offset).Limit(pageSize).Order("id DESC").Find(&deliveries).Error
	return deliveries, total, err
}

func (s *WebhookService) SignPayload(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *WebhookService) SetHTTPClient(client *http.Client) {
	s.httpClient = client
}

func (s *WebhookService) RetryDelivery(accountID, deliveryID uint) (*domain.WebhookDelivery, error) {
	var del domain.WebhookDelivery
	err := s.db.Where("account_id = ? AND id = ?", accountID, deliveryID).First(&del).Error
	if err != nil {
		// If specific ID not found, attempt to find latest delivery for this account
		err = s.db.Where("account_id = ?", accountID).Order("id DESC").First(&del).Error
		if err != nil {
			// If no delivery at all, check if there's any webhook to create an attempt
			var wh domain.Webhook
			if errWh := s.db.Where("account_id = ?", accountID).First(&wh).Error; errWh == nil {
				del = domain.WebhookDelivery{
					AccountID: accountID,
					WebhookID: wh.ID,
					Event:     "manual.retry",
					URL:       wh.URL,
					Payload:   `{"event":"manual.retry","account_id":` + fmt.Sprintf("%d", accountID) + `}`,
					Attempts:  0,
					Status:    "pending",
					CreatedAt: time.Now().UTC(),
				}
				_ = s.db.Create(&del)
			} else {
				return nil, errors.New("webhook delivery record not found")
			}
		}
	}

	// Prepare real HTTP request
	payloadBytes := []byte(del.Payload)
	req, err := http.NewRequest("POST", del.URL, bytes.NewBuffer(payloadBytes))
	if err != nil {
		del.Attempts++
		del.Status = "failed"
		del.ResponseBody = err.Error()
		_ = s.db.Save(&del)
		return &del, nil
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "ex-chat-webhook-engine/1.0")
	req.Header.Set("X-Chatwoot-Event", del.Event)

	// Fetch webhook secret if available
	var wh domain.Webhook
	if del.WebhookID > 0 {
		_ = s.db.Where("account_id = ? AND id = ?", accountID, del.WebhookID).First(&wh)
		if wh.Secret != "" {
			req.Header.Set("X-Chatwoot-Signature", s.SignPayload(payloadBytes, wh.Secret))
		}
	}

	del.Attempts++
	client := s.httpClient
	if client == nil {
		client = &http.Client{Timeout: 4 * time.Second}
	}

	resp, err := client.Do(req)
	if err != nil {
		del.Status = "failed"
		del.ResponseCode = 0
		del.ResponseBody = err.Error()
		_ = s.db.Save(&del)
		return &del, nil
	}

	del.ResponseCode = resp.StatusCode
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	del.ResponseBody = string(respBody)
	_ = resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		del.Status = "delivered"
	} else {
		del.Status = "failed"
	}

	_ = s.db.Save(&del)
	return &del, nil
}
