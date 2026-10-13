package repository

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

func generateToken(length int) string {
	b := make([]byte, length)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type InboxRepository struct {
	db            *gorm.DB
	accounts      inboxAccountPort
	conversations inboxConversationPort
	messages      inboxMessagePort
}

type inboxAccountPort interface {
	ListMembers(accountID uint) ([]domain.AccountUser, error)
}

type inboxConversationPort interface {
	FindIDsByInbox(accountID, inboxID uint) ([]uint, error)
}

type inboxMessagePort interface {
	ChannelDeliveryStats(accountID uint, conversationIDs []uint, since time.Time) (ChannelDeliveryStats, error)
}

type AccountChannelDeletionStatus struct {
	InboxCount      int64 `json:"inbox_count"`
	CredentialCount int64 `json:"credential_count"`
	MemberCount     int64 `json:"member_count"`
	Ready           bool  `json:"ready"`
}

func NewInboxRepository(db *gorm.DB) *InboxRepository {
	repo := NewInboxRepositoryWithAccountPort(db, NewAccountRepository(db))
	repo.conversations = &ConversationRepository{db: db}
	repo.messages = &MessageRepository{db: db}
	return repo
}

func NewInboxRepositoryWithAccountPort(db *gorm.DB, accounts inboxAccountPort) *InboxRepository {
	return &InboxRepository{db: db, accounts: accounts}
}

func NewInboxRepositoryWithOwnerPorts(db *gorm.DB, accounts inboxAccountPort, conversations inboxConversationPort, messages inboxMessagePort) *InboxRepository {
	return &InboxRepository{db: db, accounts: accounts, conversations: conversations, messages: messages}
}

func (r *InboxRepository) Create(inbox *domain.Inbox) error {
	return r.db.Omit("Members", "AssignmentPolicy").Create(inbox).Error
}

// FindInboxSMTP is CHN's credential-bearing local owner port. It does not
// enrich foreign-owner data and never returns provider failures to MSG.
func (r *InboxRepository) FindInboxSMTP(ctx context.Context, accountID, inboxID uint) (*domain.Inbox, error) {
	var inbox domain.Inbox
	db := r.db.WithContext(ctx).Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	if err := db.Select("id", "account_id", "created_at", "provider_config").Where("account_id = ? AND id = ?", accountID, inboxID).First(&inbox).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, errors.New("inbox SMTP route is unavailable")
	}
	return &inbox, nil
}

func (r *InboxRepository) FindByID(accountID, id uint) (*domain.Inbox, error) {
	var inbox domain.Inbox
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&inbox).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	inbox.Members, err = r.ListMembers(inbox.ID)
	if err != nil {
		return nil, err
	}
	return &inbox, nil
}

func (r *InboxRepository) Exists(accountID, id uint) (bool, error) {
	// Do not log caller identifiers or raw dependency errors from Call reads.
	db := r.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	var count int64
	err := db.Model(&domain.Inbox{}).Where("account_id = ? AND id = ?", accountID, id).Count(&count).Error
	return count == 1, err
}

func (r *InboxRepository) FindSummaryByID(accountID, id uint) (*domain.Inbox, error) {
	var inbox domain.Inbox
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&inbox).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &inbox, err
}

func (r *InboxRepository) FindFirstByAccount(accountID uint) (*domain.Inbox, error) {
	var inbox domain.Inbox
	err := r.db.Where("account_id = ?", accountID).Order("id ASC").First(&inbox).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
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
	err := r.db.Where("website_token = ?", token).First(&inbox).Error
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
	err := r.db.Where("account_id = ?", accountID).Find(&inboxes).Error
	if err != nil {
		return nil, err
	}
	for i := range inboxes {
		inboxes[i].Members, err = r.ListMembers(inboxes[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return inboxes, nil
}

func (r *InboxRepository) AccountDeletionStatus(accountID uint) (AccountChannelDeletionStatus, error) {
	var status AccountChannelDeletionStatus
	if err := r.db.Model(&domain.Inbox{}).Where("account_id = ?", accountID).Count(&status.InboxCount).Error; err != nil {
		return status, err
	}
	if err := r.db.Model(&domain.Inbox{}).Where("account_id = ? AND (provider_config <> '' OR webhook_url <> '' OR hmac_token <> '' OR hmac_mandatory = ? OR website_token NOT LIKE ?)", accountID, true, "revoked-%").Count(&status.CredentialCount).Error; err != nil {
		return status, err
	}
	var providerCredentials int64
	if err := r.db.Model(&domain.ChannelConnection{}).Where("account_id = ? AND secret_ciphertext <> ?", accountID, "").Count(&providerCredentials).Error; err != nil {
		return status, err
	}
	status.CredentialCount += providerCredentials
	owned := r.db.Model(&domain.Inbox{}).Select("id").Where("account_id = ?", accountID)
	if err := r.db.Model(&domain.InboxMember{}).Where("inbox_id IN (?)", owned).Count(&status.MemberCount).Error; err != nil {
		return status, err
	}
	status.Ready = status.CredentialCount == 0 && status.MemberCount == 0
	return status, nil
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
	return r.db.Omit("Members", "AssignmentPolicy").Save(inbox).Error
}

func (r *InboxRepository) Delete(accountID, id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&domain.ChannelConnection{}).Where("account_id = ? AND inbox_id = ?", accountID, id).Updates(map[string]any{"secret_ciphertext": "", "recipient_id": "", "provider_account_id": "", "revoked_at": time.Now().UTC()}).Error; err != nil {
			return err
		}
		return tx.Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.Inbox{}).Error
	})
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

func (r *InboxRepository) ListMemberIDs(accountID, inboxID uint) ([]uint, error) {
	// Do not log caller identifiers or raw dependency errors from Call reads.
	db := r.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	ids := make([]uint, 0)
	ownedInbox := db.Model(&domain.Inbox{}).Select("id").Where("account_id = ? AND id = ?", accountID, inboxID)
	err := db.Model(&domain.InboxMember{}).Where("inbox_id IN (?)", ownedInbox).Order("user_id ASC").Pluck("user_id", &ids).Error
	return ids, err
}

func (r *InboxRepository) ListMembers(inboxID uint) ([]domain.User, error) {
	var inbox domain.Inbox
	if err := r.db.First(&inbox, inboxID).Error; err != nil {
		return nil, err
	}
	var ids []uint
	if err := r.db.Model(&domain.InboxMember{}).Where("inbox_id = ?", inboxID).Pluck("user_id", &ids).Error; err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []domain.User{}, nil
	}
	users, err := r.accountUsers(inbox.AccountID)
	if err != nil {
		return nil, err
	}
	wanted := make(map[uint]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	result := make([]domain.User, 0)
	for _, user := range users {
		if wanted[user.ID] {
			result = append(result, user)
		}
	}
	return result, nil
}

func (r *InboxRepository) accountUsers(accountID uint) ([]domain.User, error) {
	if r.accounts == nil {
		return nil, gorm.ErrInvalidData
	}
	members, err := r.accounts.ListMembers(accountID)
	if err != nil {
		return nil, err
	}
	users := make([]domain.User, 0, len(members))
	for _, member := range members {
		if member.User != nil {
			users = append(users, *member.User)
		}
	}
	return users, nil
}

func (r *InboxRepository) ListAssignableAgents(accountID, inboxID uint) ([]domain.User, error) {
	valid, err := r.Exists(accountID, inboxID)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, gorm.ErrRecordNotFound
	}
	members, err := r.ListMembers(inboxID)
	if err != nil {
		return nil, err
	}
	if len(members) > 0 {
		return members, nil
	}
	// Fallback to all account agents
	return r.accountUsers(accountID)
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

// UpdateChannelHealth stores the latest owner-local channel health observation
// without exposing or replacing provider credentials.
func (r *InboxRepository) UpdateChannelHealth(accountID, inboxID uint, status string, checkedAt time.Time, details map[string]any) error {
	_, err := r.UpdateProviderConfig(accountID, inboxID, map[string]any{"_health": map[string]any{"status": status, "checked_at": checkedAt.UTC().Format(time.RFC3339Nano), "details": details}})
	return err
}

// RevokeAccountChannels removes every channel credential and agent binding for
// one account while retaining inbox records needed by historical references.
func (r *InboxRepository) RevokeAccountChannels(accountID uint) (int64, error) {
	if err := r.db.Model(&domain.ChannelConnection{}).Where("account_id = ?", accountID).Updates(map[string]any{"secret_ciphertext": "", "recipient_id": "", "provider_account_id": "", "revoked_at": time.Now().UTC()}).Error; err != nil {
		return 0, err
	}
	var ids []uint
	if err := r.db.Model(&domain.Inbox{}).Where("account_id = ?", accountID).Order("id ASC").Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if err := r.db.Where("inbox_id IN ?", ids).Delete(&domain.InboxMember{}).Error; err != nil {
		return 0, err
	}
	for _, id := range ids {
		result := r.db.Model(&domain.Inbox{}).Where("account_id = ? AND id = ?", accountID, id).Updates(map[string]any{"provider_config": "", "webhook_url": "", "website_token": "revoked-" + generateToken(24), "hmac_token": "", "hmac_mandatory": false})
		if result.Error != nil {
			return 0, result.Error
		}
	}
	return int64(len(ids)), nil
}

func (r *InboxRepository) GetCSATTemplate(accountID, inboxID uint) (map[string]any, error) {
	var inbox domain.Inbox
	if err := r.db.Where("account_id = ? AND id = ?", accountID, inboxID).First(&inbox).Error; err != nil {
		return nil, err
	}

	res := make(map[string]any)
	if inbox.CSATConfig != "" {
		if err := json.Unmarshal([]byte(inbox.CSATConfig), &res); err != nil {
			return nil, err
		}
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
	result := r.db.Model(&domain.Inbox{}).
		Where("account_id = ? AND id = ?", accountID, inboxID).
		Update("csat_config", string(bytes))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
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

	return templates, nil
}

// ListStoredMessageTemplates returns only owner-local persisted templates. It
// never performs provider synchronization, which belongs to a separate command.
func (r *InboxRepository) ListStoredMessageTemplates(accountID, inboxID uint) ([]domain.InboxMessageTemplate, error) {
	var templates []domain.InboxMessageTemplate
	err := r.db.Where("account_id = ? AND inbox_id = ?", accountID, inboxID).Order("id ASC").Find(&templates).Error
	return templates, err
}

// GetMessageTemplate finds a message template by ID
func (r *InboxRepository) GetMessageTemplate(accountID, inboxID, templateID uint) (*domain.InboxMessageTemplate, error) {
	var t domain.InboxMessageTemplate
	if err := r.db.Where("account_id = ? AND inbox_id = ? AND id = ?", accountID, inboxID, templateID).First(&t).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

// lockTemplateInbox checks the current tenant/inbox pair in the same transaction
// as the write. CHN needs only its own inbox table, not another owner's store.
func lockTemplateInbox(db *gorm.DB, accountID, inboxID uint) (*domain.Inbox, error) {
	if accountID == 0 || inboxID == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	var inbox domain.Inbox
	if err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_id = ? AND id = ?", accountID, inboxID).First(&inbox).Error; err != nil {
		return nil, err
	}
	return &inbox, nil
}

// CreateMessageTemplate upserts supplied local data using the existing name key.
// A stored status is a local assertion, not evidence of provider approval.
func (r *InboxRepository) CreateMessageTemplate(template *domain.InboxMessageTemplate) error {
	if template == nil || strings.TrimSpace(template.Name) == "" {
		return errors.New("template name is required")
	}
	var saved domain.InboxMessageTemplate
	err := r.db.Transaction(func(tx *gorm.DB) error {
		if _, err := lockTemplateInbox(tx, template.AccountID, template.InboxID); err != nil {
			return err
		}
		saved = *template
		return upsertLocalMessageTemplate(tx, &saved, false)
	})
	if err == nil {
		*template = saved
	}
	return err
}

func upsertLocalMessageTemplate(db *gorm.DB, template *domain.InboxMessageTemplate, preserveStatus bool) error {
	if strings.TrimSpace(template.Name) == "" {
		return errors.New("template name is required")
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
	var existing domain.InboxMessageTemplate
	err := db.Where("account_id = ? AND inbox_id = ? AND name = ?", template.AccountID, template.InboxID, template.Name).First(&existing).Error
	if err == nil {
		if !preserveStatus {
			existing.Status = template.Status
		}
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
		if err := db.Omit(clause.Associations).Save(&existing).Error; err != nil {
			return err
		}
		*template = existing
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	template.CreatedAt, template.UpdatedAt = now, now
	return db.Omit(clause.Associations).Create(template).Error
}

// DeleteMessageTemplate removes a local row only after validating its inbox owner.
func (r *InboxRepository) DeleteMessageTemplate(accountID, inboxID, templateID uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if _, err := lockTemplateInbox(tx, accountID, inboxID); err != nil {
			return err
		}
		return tx.Where("account_id = ? AND inbox_id = ? AND id = ?", accountID, inboxID, templateID).Delete(&domain.InboxMessageTemplate{}).Error
	})
}

// SyncMessageTemplates retains the legacy method/route name. It only imports
// local ProviderConfig.templates; it does not contact or verify a provider.
// All template content and legacy-named import metadata commit together.
func (r *InboxRepository) SyncMessageTemplates(accountID, inboxID uint) ([]domain.InboxMessageTemplate, error) {
	var result []domain.InboxMessageTemplate
	err := r.db.Transaction(func(tx *gorm.DB) error {
		inbox, err := lockTemplateInbox(tx, accountID, inboxID)
		if err != nil {
			return err
		}
		var cfg struct {
			Error     string `json:"error"`
			SyncError string `json:"sync_error"`
			Templates []struct {
				Name               string          `json:"name"`
				Status             json.RawMessage `json:"status"`
				Category           string          `json:"category"`
				Language           string          `json:"language"`
				Components         json.RawMessage `json:"components"`
				RejectedReason     string          `json:"rejected_reason"`
				ProviderTemplateID string          `json:"provider_template_id"`
			} `json:"templates"`
		}
		if strings.TrimSpace(inbox.ProviderConfig) != "" {
			raw := strings.TrimSpace(inbox.ProviderConfig)
			if !strings.HasPrefix(raw, "{") || json.Unmarshal([]byte(raw), &cfg) != nil {
				return errors.New("template import configuration must be a valid JSON object")
			}
			if cfg.Error != "" || cfg.SyncError != "" {
				return errors.New("template import configuration reports an error")
			}
		}
		now := time.Now().UTC()
		for _, input := range cfg.Templates {
			var status string
			if len(input.Status) > 0 {
				if err := json.Unmarshal(input.Status, &status); err != nil {
					return errors.New("template status must be a string or null")
				}
				if strings.TrimSpace(status) == "" {
					status = ""
				}
			}
			components := string(input.Components)
			if components == "" || components == "null" {
				components = `[{"type":"BODY","text":"Hello, this is a custom provider template."}]`
			}
			template := domain.InboxMessageTemplate{
				AccountID: accountID, InboxID: inboxID, Name: input.Name, Status: status,
				Category: input.Category, Language: input.Language, Components: components,
				RejectedReason: input.RejectedReason, ProviderTemplateID: input.ProviderTemplateID,
				LastSyncAt: &now, SyncStatus: "imported",
			}
			if err := upsertLocalMessageTemplate(tx, &template, len(input.Status) == 0); err != nil {
				return err
			}
		}
		// No template definitions means no writes, including no metadata refresh.
		return tx.Where("account_id = ? AND inbox_id = ?", accountID, inboxID).Order("created_at ASC").Find(&result).Error
	})
	if err != nil {
		return nil, err
	}
	return result, nil
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
	var channelConnections []domain.ChannelConnection
	if err := r.db.Where("account_id = ? AND inbox_id = ?", accountID, inboxID).Limit(2).Find(&channelConnections).Error; err != nil {
		return nil, err
	}
	if len(channelConnections) > 1 {
		return nil, gorm.ErrInvalidData
	}
	if len(channelConnections) == 1 {
		connection := channelConnections[0]
		configured := connection.RevokedAt == nil && connection.SecretCiphertext != "" && connection.RecipientID != ""
		status := "configured_not_probed"
		if !configured {
			status = "revoked"
		}
		// Persistence is not evidence of current provider availability, quality
		// rating or quota. Do not manufacture GREEN/TIER_10K health claims.
		return &ChannelHealthResult{Status: status, ChannelType: inbox.ChannelType, QualityRating: "UNKNOWN", MessagingLimit: "UNKNOWN", Verified: false, CheckedAt: now, Checks: map[string]any{"provider": map[string]any{"status": status, "credentials_stored": configured, "live_probe": false}}, Issues: []string{"Live provider health, token expiry and delivery limits have not been checked"}}, nil
	}
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
	if r.conversations == nil || r.messages == nil {
		return nil, gorm.ErrInvalidData
	}
	conversationIDs, err := r.conversations.FindIDsByInbox(accountID, inboxID)
	if err != nil {
		return nil, err
	}
	stats, err := r.messages.ChannelDeliveryStats(accountID, conversationIDs, sinceTime)
	if err != nil {
		return nil, err
	}

	var errorRate float64
	if stats.TotalMessages > 0 {
		errorRate = float64(stats.FailedMessages) / float64(stats.TotalMessages)
	}

	// Check consecutive outgoing message failures (latest up to 5 messages)
	consecutiveFailures := 0
	for _, st := range stats.RecentOutgoingStatuses {
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
