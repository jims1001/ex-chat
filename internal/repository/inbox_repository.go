package repository

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

func generateToken(length int) string {
	b := make([]byte, length)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type InboxRepository struct {
	db *gorm.DB
}

func NewInboxRepository(db *gorm.DB) *InboxRepository {
	return &InboxRepository{db: db}
}

func (r *InboxRepository) Create(inbox *domain.Inbox) error {
	return r.db.Create(inbox).Error
}

func (r *InboxRepository) FindByID(accountID, id uint) (*domain.Inbox, error) {
	var inbox domain.Inbox
	err := r.db.Preload("Members").Preload("AssignmentPolicy").Where("account_id = ? AND id = ?", accountID, id).First(&inbox).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &inbox, nil
}

// FindByGlobalID is reserved for infrastructure authorization paths that must
// first resolve an inbox before the caller's account scope can be checked.
func (r *InboxRepository) FindByGlobalID(id uint) (*domain.Inbox, error) {
	var inbox domain.Inbox
	if err := r.db.First(&inbox, id).Error; err != nil {
		return nil, err
	}
	return &inbox, nil
}

func (r *InboxRepository) FindByWebsiteToken(token string) (*domain.Inbox, error) {
	var inbox domain.Inbox
	err := r.db.Preload("AssignmentPolicy").Where("website_token = ?", token).First(&inbox).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &inbox, nil
}

func (r *InboxRepository) ListByAccount(accountID uint) ([]domain.Inbox, error) {
	var inboxes []domain.Inbox
	err := r.db.Preload("Members").Preload("AssignmentPolicy").Where("account_id = ?", accountID).Find(&inboxes).Error
	return inboxes, err
}

func (r *InboxRepository) BindAssignmentPolicy(accountID, inboxID uint, policyID *uint) error {
	return r.db.Model(&domain.Inbox{}).
		Where("account_id = ? AND id = ?", accountID, inboxID).
		Update("assignment_policy_id", policyID).Error
}

func (r *InboxRepository) UnbindAssignmentPolicy(accountID, inboxID uint) error {
	return r.db.Model(&domain.Inbox{}).
		Where("account_id = ? AND id = ?", accountID, inboxID).
		Update("assignment_policy_id", nil).Error
}

func (r *InboxRepository) FindByIDs(accountID uint, inboxIDs []uint) ([]domain.Inbox, error) {
	var inboxes []domain.Inbox
	err := r.db.Where("account_id = ? AND id IN (?)", accountID, inboxIDs).Order("id ASC").Find(&inboxes).Error
	return inboxes, err
}

func (r *InboxRepository) ListByAssignmentPolicy(accountID, policyID uint) ([]domain.Inbox, error) {
	var inboxes []domain.Inbox
	err := r.db.Where("account_id = ? AND assignment_policy_id = ?", accountID, policyID).Order("id ASC").Find(&inboxes).Error
	return inboxes, err
}

func (r *InboxRepository) AssignPolicy(accountID, policyID uint, inboxIDs []uint) ([]domain.Inbox, error) {
	if err := r.db.Model(&domain.Inbox{}).Where("account_id = ? AND id IN (?)", accountID, inboxIDs).Update("assignment_policy_id", policyID).Error; err != nil {
		return nil, err
	}
	return r.FindByIDs(accountID, inboxIDs)
}

func (r *InboxRepository) RemovePolicy(accountID, policyID uint, inboxIDs []uint) error {
	q := r.db.Model(&domain.Inbox{}).Where("account_id = ? AND assignment_policy_id = ?", accountID, policyID)
	if len(inboxIDs) > 0 {
		q = q.Where("id IN (?)", inboxIDs)
	}
	return q.Update("assignment_policy_id", nil).Error
}

func (r *InboxRepository) Update(inbox *domain.Inbox) error {
	return r.db.Save(inbox).Error
}

func (r *InboxRepository) Delete(accountID, id uint) error {
	return r.db.Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.Inbox{}).Error
}

func (r *InboxRepository) AddMember(inboxID, userID uint) error {
	member := domain.InboxMember{
		InboxID: inboxID,
		UserID:  userID,
	}
	return r.db.Where(domain.InboxMember{InboxID: inboxID, UserID: userID}).FirstOrCreate(&member).Error
}

func (r *InboxRepository) RemoveMember(inboxID, userID uint) error {
	return r.db.Where("inbox_id = ? AND user_id = ?", inboxID, userID).Delete(&domain.InboxMember{}).Error
}

func (r *InboxRepository) ListMembers(inboxID uint) ([]domain.User, error) {
	var users []domain.User
	err := r.db.Joins("JOIN inbox_members ON inbox_members.user_id = users.id").
		Where("inbox_members.inbox_id = ?", inboxID).
		Find(&users).Error
	return users, err
}

func (r *InboxRepository) ListAssignableAgents(accountID, inboxID uint) ([]domain.User, error) {
	members, err := r.ListMembers(inboxID)
	if err == nil && len(members) > 0 {
		return members, nil
	}
	// Fallback to all account agents
	var agents []domain.User
	err = r.db.Joins("JOIN account_users ON account_users.user_id = users.id").
		Where("account_users.account_id = ?", accountID).
		Find(&agents).Error
	return agents, err
}

func (r *InboxRepository) ListCampaigns(accountID, inboxID uint) ([]domain.Campaign, error) {
	var campaigns []domain.Campaign
	err := r.db.Where("account_id = ? AND inbox_id = ?", accountID, inboxID).
		Order("id DESC").
		Find(&campaigns).Error
	return campaigns, err
}

func (r *InboxRepository) GetAgentBot(accountID, inboxID uint) (*domain.AgentBot, error) {
	var binding domain.AgentBotInbox
	err := r.db.Preload("AgentBot").
		Where("account_id = ? AND inbox_id = ?", accountID, inboxID).
		First(&binding).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return binding.AgentBot, nil
}

func (r *InboxRepository) SetAgentBot(accountID, inboxID, agentBotID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Clean up existing binding
		if err := tx.Where("account_id = ? AND inbox_id = ?", accountID, inboxID).Delete(&domain.AgentBotInbox{}).Error; err != nil {
			return err
		}
		if agentBotID == 0 {
			return nil
		}
		binding := domain.AgentBotInbox{
			AccountID:  accountID,
			AgentBotID: agentBotID,
			InboxID:    inboxID,
		}
		return tx.Create(&binding).Error
	})
}

func (r *InboxRepository) UnsetAgentBot(accountID, inboxID uint) error {
	return r.db.Where("account_id = ? AND inbox_id = ?", accountID, inboxID).Delete(&domain.AgentBotInbox{}).Error
}

func (r *InboxRepository) DeleteAvatar(accountID, inboxID uint) error {
	return r.db.Model(&domain.Inbox{}).
		Where("account_id = ? AND id = ?", accountID, inboxID).
		Update("avatar_url", "").Error
}

func (r *InboxRepository) ResetSecret(accountID, inboxID uint) (string, error) {
	newToken := generateToken(16)
	err := r.db.Model(&domain.Inbox{}).
		Where("account_id = ? AND id = ?", accountID, inboxID).
		Update("website_token", newToken).Error
	return newToken, err
}

func (r *InboxRepository) RotateHMACToken(accountID, inboxID uint) (string, error) {
	newToken := generateToken(24)
	err := r.db.Model(&domain.Inbox{}).
		Where("account_id = ? AND id = ?", accountID, inboxID).
		Update("hmac_token", newToken).Error
	return newToken, err
}

func (r *InboxRepository) UpdateProviderConfig(accountID, inboxID uint, updates map[string]any) (map[string]any, error) {
	var inbox domain.Inbox
	if err := r.db.Where("account_id = ? AND id = ?", accountID, inboxID).First(&inbox).Error; err != nil {
		return nil, err
	}

	merged := make(map[string]any)
	if inbox.ProviderConfig != "" {
		_ = json.Unmarshal([]byte(inbox.ProviderConfig), &merged)
	}
	for k, v := range updates {
		merged[k] = v
	}

	bytes, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}

	inbox.ProviderConfig = string(bytes)
	err = r.db.Model(&domain.Inbox{}).
		Where("account_id = ? AND id = ?", accountID, inboxID).
		Update("provider_config", inbox.ProviderConfig).Error
	return merged, err
}

func (r *InboxRepository) GetCSATTemplate(accountID, inboxID uint) (map[string]any, error) {
	var inbox domain.Inbox
	if err := r.db.Where("account_id = ? AND id = ?", accountID, inboxID).First(&inbox).Error; err != nil {
		return nil, err
	}

	res := make(map[string]any)
	if inbox.CSATConfig != "" {
		_ = json.Unmarshal([]byte(inbox.CSATConfig), &res)
	}
	if len(res) == 0 {
		res = map[string]any{
			"display_type": "emoji",
			"message":      "How would you rate our support?",
			"button_text":  "Rate Conversation",
			"language":     "en",
			"status":       "active",
		}
	}
	return res, nil
}

func (r *InboxRepository) SaveCSATTemplate(accountID, inboxID uint, template map[string]any) error {
	bytes, err := json.Marshal(template)
	if err != nil {
		return err
	}
	return r.db.Model(&domain.Inbox{}).
		Where("account_id = ? AND id = ?", accountID, inboxID).
		Update("csat_config", string(bytes)).Error
}

func (r *InboxRepository) RegisterWebhook(accountID, inboxID uint, webhookURL string) error {
	return r.db.Model(&domain.Inbox{}).
		Where("account_id = ? AND id = ?", accountID, inboxID).
		Update("webhook_url", webhookURL).Error
}

func (r *InboxRepository) BulkSyncMembers(inboxID uint, userIDs []uint) ([]domain.User, error) {
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("inbox_id = ?", inboxID).Delete(&domain.InboxMember{}).Error; err != nil {
			return err
		}
		for _, uid := range userIDs {
			member := domain.InboxMember{
				InboxID: inboxID,
				UserID:  uid,
			}
			if err := tx.Create(&member).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return r.ListMembers(inboxID)
}

func (r *InboxRepository) BulkRemoveMembers(inboxID uint, userIDs []uint) error {
	if len(userIDs) == 0 {
		return nil
	}
	return r.db.Where("inbox_id = ? AND user_id IN (?)", inboxID, userIDs).Delete(&domain.InboxMember{}).Error
}

// ListMessageTemplates lists persistent message templates for an inbox
func (r *InboxRepository) ListMessageTemplates(accountID, inboxID uint, nameFilter, statusFilter string) ([]domain.InboxMessageTemplate, error) {
	query := r.db.Where("account_id = ? AND inbox_id = ?", accountID, inboxID)
	if nameFilter != "" {
		query = query.Where("name LIKE ?", "%"+nameFilter+"%")
	}
	if statusFilter != "" {
		query = query.Where("status = ?", statusFilter)
	}

	var templates []domain.InboxMessageTemplate
	if err := query.Order("created_at ASC").Find(&templates).Error; err != nil {
		return nil, err
	}

	if len(templates) == 0 && nameFilter == "" && statusFilter == "" {
		// Sync or initialize default templates for inbox
		return r.SyncMessageTemplates(accountID, inboxID)
	}
	return templates, nil
}

// GetMessageTemplate finds a message template by ID
func (r *InboxRepository) GetMessageTemplate(accountID, inboxID, templateID uint) (*domain.InboxMessageTemplate, error) {
	var t domain.InboxMessageTemplate
	if err := r.db.Where("account_id = ? AND inbox_id = ? AND id = ?", accountID, inboxID, templateID).First(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// CreateMessageTemplate stores a new message template in database
func (r *InboxRepository) CreateMessageTemplate(template *domain.InboxMessageTemplate) error {
	if template.AccountID == 0 || template.InboxID == 0 {
		return errors.New("account_id and inbox_id are required")
	}
	if strings.TrimSpace(template.Name) == "" {
		return errors.New("template name is required")
	}
	if template.Status == "" {
		template.Status = "APPROVED"
	}
	if template.Category == "" {
		template.Category = "UTILITY"
	}
	if template.Language == "" {
		template.Language = "en_US"
	}
	if template.Components == "" {
		template.Components = `[{"type":"BODY","text":"Hello {{1}}, this is an automated message."}]`
	}
	now := time.Now().UTC()
	template.CreatedAt = now
	template.UpdatedAt = now

	// Check if already exists with same name in this inbox
	var existing domain.InboxMessageTemplate
	if err := r.db.Where("account_id = ? AND inbox_id = ? AND name = ?", template.AccountID, template.InboxID, template.Name).First(&existing).Error; err == nil {
		existing.Status = template.Status
		existing.Category = template.Category
		existing.Language = template.Language
		existing.Components = template.Components
		existing.RejectedReason = template.RejectedReason
		existing.LastSyncAt = template.LastSyncAt
		existing.SyncStatus = template.SyncStatus
		if template.ProviderTemplateID != "" {
			existing.ProviderTemplateID = template.ProviderTemplateID
		}
		existing.UpdatedAt = now
		*template = existing
		return r.db.Save(&existing).Error
	}

	return r.db.Create(template).Error
}

// DeleteMessageTemplate removes a message template from database
func (r *InboxRepository) DeleteMessageTemplate(accountID, inboxID, templateID uint) error {
	return r.db.Where("account_id = ? AND inbox_id = ? AND id = ?", accountID, inboxID, templateID).Delete(&domain.InboxMessageTemplate{}).Error
}

// SyncMessageTemplates synchronizes message templates with provider_config or initializes persistent defaults
func (r *InboxRepository) SyncMessageTemplates(accountID, inboxID uint) ([]domain.InboxMessageTemplate, error) {
	inbox, err := r.FindByID(accountID, inboxID)
	if err != nil || inbox == nil {
		return nil, errors.New("inbox not found")
	}

	now := time.Now().UTC()
	var templatesToUpsert []domain.InboxMessageTemplate

	// 1. Check if inbox.ProviderConfig defines custom templates or errors
	if inbox.ProviderConfig != "" {
		var cfg struct {
			Error     string `json:"error"`
			SyncError string `json:"sync_error"`
			Templates []struct {
				Name               string          `json:"name"`
				Status             string          `json:"status"`
				Category           string          `json:"category"`
				Language           string          `json:"language"`
				Components         json.RawMessage `json:"components"`
				RejectedReason     string          `json:"rejected_reason"`
				ProviderTemplateID string          `json:"provider_template_id"`
			} `json:"templates"`
		}
		if err := json.Unmarshal([]byte(inbox.ProviderConfig), &cfg); err == nil {
			syncErr := cfg.Error
			if syncErr == "" {
				syncErr = cfg.SyncError
			}

			if len(cfg.Templates) > 0 {
				for _, t := range cfg.Templates {
					compStr := string(t.Components)
					if compStr == "" || compStr == "null" {
						compStr = `[{"type":"BODY","text":"Hello, this is a custom provider template."}]`
					}
					status := t.Status
					if status == "" {
						status = "APPROVED"
					}
					category := t.Category
					if category == "" {
						category = "UTILITY"
					}
					lang := t.Language
					if lang == "" {
						lang = "en_US"
					}
					syncStatus := "synced"
					rejReason := t.RejectedReason
					if syncErr != "" {
						syncStatus = "sync_failed"
						if rejReason == "" {
							rejReason = syncErr
						}
					}
					templatesToUpsert = append(templatesToUpsert, domain.InboxMessageTemplate{
						AccountID:          accountID,
						InboxID:            inboxID,
						Name:               t.Name,
						Status:             status,
						Category:           category,
						Language:           lang,
						Components:         compStr,
						RejectedReason:     rejReason,
						ProviderTemplateID: t.ProviderTemplateID,
						LastSyncAt:         &now,
						SyncStatus:         syncStatus,
						CreatedAt:          now,
						UpdatedAt:          now,
					})
				}
			} else if syncErr != "" {
				// Mark existing templates with sync failure reason
				r.db.Model(&domain.InboxMessageTemplate{}).
					Where("account_id = ? AND inbox_id = ?", accountID, inboxID).
					Updates(map[string]interface{}{
						"sync_status":     "sync_failed",
						"rejected_reason": syncErr,
						"last_sync_at":    &now,
						"updated_at":      now,
					})
			}
		}
	}

	// 2. Upsert any templates parsed from ProviderConfig
	for i := range templatesToUpsert {
		_ = r.CreateMessageTemplate(&templatesToUpsert[i])
	}

	// 3. If no new templates from ProviderConfig, update existing templates' sync status
	if len(templatesToUpsert) == 0 {
		r.db.Model(&domain.InboxMessageTemplate{}).
			Where("account_id = ? AND inbox_id = ?", accountID, inboxID).
			Updates(map[string]interface{}{
				"last_sync_at": &now,
				"sync_status":  "synced",
				"updated_at":   now,
			})
	}

	// 4. Return all persisted templates for this inbox
	var allTemplates []domain.InboxMessageTemplate
	if err := r.db.Where("account_id = ? AND inbox_id = ?", accountID, inboxID).Order("created_at ASC").Find(&allTemplates).Error; err != nil {
		return nil, err
	}
	return allTemplates, nil
}

// ChannelHealthResult holds the evaluated health indicators and diagnostics of an inbox channel
type ChannelHealthResult struct {
	Status         string         `json:"status"` // healthy, degraded, unhealthy
	ChannelType    string         `json:"channel_type"`
	QualityRating  string         `json:"quality_rating"`  // GREEN, YELLOW, RED
	MessagingLimit string         `json:"messaging_limit"` // TIER_10K, TIER_1K, TIER_50, UNVERIFIED
	Verified       bool           `json:"verified"`
	Checks         map[string]any `json:"checks"`
	Issues         []string       `json:"issues,omitempty"`
	CheckedAt      time.Time      `json:"checked_at"`
}

// CheckChannelHealth dynamically assesses token credentials, webhook routing, provider connectivity, and recent delivery error rates
func (r *InboxRepository) CheckChannelHealth(accountID, inboxID uint) (*ChannelHealthResult, error) {
	inbox, err := r.FindByID(accountID, inboxID)
	if err != nil || inbox == nil {
		return nil, errors.New("inbox not found")
	}

	now := time.Now().UTC()
	checks := make(map[string]any)
	var issues []string

	tokenValid := true
	webhookValid := true
	providerConnected := true
	deliveryHealthy := true

	// 1. Token Check
	tokenStatus := "valid"
	if strings.TrimSpace(inbox.WebsiteToken) == "" || len(inbox.WebsiteToken) < 6 {
		tokenStatus = "missing"
		tokenValid = false
		issues = append(issues, "渠道访问令牌 (website_token) 缺失或长度异常")
	}
	if inbox.HMACMandatory && strings.TrimSpace(inbox.HMACToken) == "" {
		tokenStatus = "hmac_missing"
		tokenValid = false
		issues = append(issues, "渠道已启用强制 HMAC 验证，但未配置密钥 (hmac_token)")
	}
	checks["token"] = map[string]any{
		"status":         tokenStatus,
		"has_token":      strings.TrimSpace(inbox.WebsiteToken) != "",
		"hmac_mandatory": inbox.HMACMandatory,
		"has_hmac_token": strings.TrimSpace(inbox.HMACToken) != "",
	}

	// 2. Webhook Check
	webhookStatus := "configured"
	if strings.TrimSpace(inbox.WebhookURL) == "" {
		webhookStatus = "not_configured"
		if inbox.ChannelType != "Channel::WebWidget" && inbox.ChannelType != "Channel::Api" {
			issues = append(issues, "外部渠道未配置事件回调 Webhook URL")
		}
	} else {
		url := strings.TrimSpace(inbox.WebhookURL)
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "/") {
			webhookStatus = "invalid_format"
			webhookValid = false
			issues = append(issues, "Webhook 回调地址格式不合规 (需以 http://, https:// 或 / 开头)")
		}
	}
	checks["webhook"] = map[string]any{
		"status":      webhookStatus,
		"webhook_url": inbox.WebhookURL,
	}

	// 3. Provider Connectivity & Configuration Check
	providerStatus := "connected"
	switch inbox.ChannelType {
	case "Channel::Whatsapp":
		if strings.TrimSpace(inbox.ProviderConfig) == "" {
			providerStatus = "unconfigured"
			providerConnected = false
			issues = append(issues, "WhatsApp 渠道未配置供应商认证参数 (provider_config)")
		} else {
			var cfg map[string]any
			if err := json.Unmarshal([]byte(inbox.ProviderConfig), &cfg); err != nil {
				providerStatus = "invalid_config"
				providerConnected = false
				issues = append(issues, "WhatsApp provider_config JSON 格式解析失败")
			} else {
				hasPhone := cfg["phone_number_id"] != nil || cfg["phone_number"] != nil
				hasAuth := cfg["api_key"] != nil || cfg["access_token"] != nil || cfg["auth_token"] != nil || cfg["api_key_encrypted"] != nil
				if !hasPhone && !hasAuth && len(cfg) == 0 {
					providerStatus = "disconnected"
					providerConnected = false
					issues = append(issues, "WhatsApp 缺少有效的 phone_number_id 或 API 访问凭据")
				}
			}
		}
	case "Channel::Email":
		if strings.TrimSpace(inbox.ProviderConfig) == "" {
			providerStatus = "smtp_pending"
		} else {
			var cfg map[string]any
			if err := json.Unmarshal([]byte(inbox.ProviderConfig), &cfg); err == nil {
				if cfg["smtp_address"] == nil && cfg["smtp_host"] == nil {
					providerStatus = "smtp_missing_host"
					issues = append(issues, "邮件渠道 provider_config 缺少 smtp_address 发信服务器")
				}
			}
		}
	case "Channel::TwilioSms", "Channel::Sms":
		if strings.TrimSpace(inbox.ProviderConfig) == "" {
			providerStatus = "unconfigured"
			providerConnected = false
			issues = append(issues, "Twilio/SMS 渠道未配置 Account SID 与 Auth Token")
		} else {
			var cfg map[string]any
			if err := json.Unmarshal([]byte(inbox.ProviderConfig), &cfg); err == nil {
				if cfg["account_sid"] == nil || cfg["auth_token"] == nil {
					providerStatus = "credentials_missing"
					providerConnected = false
					issues = append(issues, "Twilio/SMS 缺少必要凭据 (account_sid 或 auth_token)")
				}
			}
		}
	default:
		if !tokenValid {
			providerStatus = "degraded"
		}
	}
	checks["provider"] = map[string]any{
		"status":       providerStatus,
		"channel_type": inbox.ChannelType,
	}

	// 4. Recent Delivery Errors & Consecutive Failures Check (Within last 24h)
	sinceTime := now.Add(-24 * time.Hour)
	var stats struct {
		TotalMessages  int64
		FailedMessages int64
	}
	r.db.Table("messages").
		Select("COUNT(*) as total_messages, SUM(CASE WHEN messages.status = 'failed' THEN 1 ELSE 0 END) as failed_messages").
		Joins("JOIN conversations ON conversations.id = messages.conversation_id").
		Where("conversations.account_id = ? AND conversations.inbox_id = ? AND messages.created_at >= ?", accountID, inboxID, sinceTime).
		Scan(&stats)

	var errorRate float64
	if stats.TotalMessages > 0 {
		errorRate = float64(stats.FailedMessages) / float64(stats.TotalMessages)
	}

	// Check consecutive outgoing message failures (latest up to 5 messages)
	var recentStatuses []string
	r.db.Table("messages").
		Joins("JOIN conversations ON conversations.id = messages.conversation_id").
		Where("conversations.account_id = ? AND conversations.inbox_id = ? AND messages.message_type = ?", accountID, inboxID, domain.MessageTypeOutgoing).
		Order("messages.created_at DESC").
		Limit(5).
		Pluck("messages.status", &recentStatuses)

	consecutiveFailures := 0
	for _, st := range recentStatuses {
		if st == "failed" {
			consecutiveFailures++
		} else {
			break
		}
	}

	deliveryStatus := "healthy"
	if consecutiveFailures >= 3 {
		deliveryStatus = "consecutive_failures"
		deliveryHealthy = false
		issues = append(issues, fmt.Sprintf("检测到最近连续 %d 条消息发送失败，通道可能已中断或遭服务商阻断", consecutiveFailures))
	} else if stats.FailedMessages >= 5 || errorRate >= 0.20 {
		deliveryStatus = "high_failure_rate"
		deliveryHealthy = false
		issues = append(issues, fmt.Sprintf("最近 24 小时消息投递失败率过高 (失败 %d/%d 条，失败率 %.1f%%)", stats.FailedMessages, stats.TotalMessages, errorRate*100))
	} else if stats.FailedMessages > 0 {
		deliveryStatus = "isolated_failures"
		issues = append(issues, fmt.Sprintf("检测到近期有 %d 条消息发送失败，请关注网络或供应商排队状况", stats.FailedMessages))
	}

	checks["delivery"] = map[string]any{
		"status":               deliveryStatus,
		"total_messages":       stats.TotalMessages,
		"failed_messages":      stats.FailedMessages,
		"consecutive_failures": consecutiveFailures,
		"error_rate":           fmt.Sprintf("%.1f%%", errorRate*100),
		"period_hours":         24,
	}

	// 5. Synthesis & Overall Status Evaluation
	overallStatus := "healthy"
	qualityRating := "GREEN"
	messagingLimit := "TIER_10K"
	verified := tokenValid && providerConnected && webhookValid

	if !tokenValid {
		overallStatus = "unhealthy"
		qualityRating = "RED"
		messagingLimit = "UNVERIFIED"
		verified = false
	} else if !providerConnected || !deliveryHealthy {
		overallStatus = "unhealthy"
		qualityRating = "RED"
		messagingLimit = "UNVERIFIED"
		verified = false
	} else if !webhookValid {
		overallStatus = "degraded"
		qualityRating = "YELLOW"
		messagingLimit = "TIER_1K"
		verified = false
	} else if len(issues) > 0 {
		overallStatus = "degraded"
		qualityRating = "YELLOW"
		messagingLimit = "TIER_1K"
	}

	return &ChannelHealthResult{
		Status:         overallStatus,
		ChannelType:    inbox.ChannelType,
		QualityRating:  qualityRating,
		MessagingLimit: messagingLimit,
		Verified:       verified,
		Checks:         checks,
		Issues:         issues,
		CheckedAt:      now,
	}, nil
}
