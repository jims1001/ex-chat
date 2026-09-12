package repository

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"

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

func (r *InboxRepository) GetDB() *gorm.DB {
	return r.db
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

