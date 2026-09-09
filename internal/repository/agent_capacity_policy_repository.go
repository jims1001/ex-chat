package repository

import (
	"errors"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

var (
	ErrDuplicateInboxLimit = errors.New("inbox is already assigned to this capacity policy")
	ErrUserNotInAccount    = errors.New("user is not a member of this account")
)

type AgentCapacityPolicyRepository struct {
	db *gorm.DB
}

func NewAgentCapacityPolicyRepository(db *gorm.DB) *AgentCapacityPolicyRepository {
	return &AgentCapacityPolicyRepository{db: db}
}

func (r *AgentCapacityPolicyRepository) Create(policy *domain.AgentCapacityPolicy) error {
	return r.db.Create(policy).Error
}

func (r *AgentCapacityPolicyRepository) SaveLegacyCapacityPolicy(policy *domain.CapacityPolicy) error {
	return r.db.Where("account_id = ? AND user_id = ?", policy.AccountID, policy.UserID).
		Assign(domain.CapacityPolicy{ConversationLimit: policy.ConversationLimit}).
		FirstOrCreate(policy).Error
}

func (r *AgentCapacityPolicyRepository) FindByID(accountID, id uint) (*domain.AgentCapacityPolicy, error) {
	var policy domain.AgentCapacityPolicy
	err := r.db.Preload("InboxCapacityLimits.Inbox").
		Where("account_id = ? AND id = ?", accountID, id).First(&policy).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	var count int64
	r.db.Model(&domain.AccountUser{}).
		Where("account_id = ? AND agent_capacity_policy_id = ?", accountID, id).
		Count(&count)
	policy.UsersCount = int(count)

	return &policy, nil
}

func (r *AgentCapacityPolicyRepository) ListByAccount(accountID uint) ([]domain.AgentCapacityPolicy, error) {
	var policies []domain.AgentCapacityPolicy
	err := r.db.Preload("InboxCapacityLimits.Inbox").
		Where("account_id = ?", accountID).Order("id ASC").Find(&policies).Error
	if err != nil {
		return nil, err
	}

	for i := range policies {
		var count int64
		r.db.Model(&domain.AccountUser{}).
			Where("account_id = ? AND agent_capacity_policy_id = ?", accountID, policies[i].ID).
			Count(&count)
		policies[i].UsersCount = int(count)
	}

	return policies, nil
}

func (r *AgentCapacityPolicyRepository) Update(policy *domain.AgentCapacityPolicy) error {
	return r.db.Save(policy).Error
}

func (r *AgentCapacityPolicyRepository) Delete(accountID, id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// 1. Delete associated inbox capacity limits
		if err := tx.Where("agent_capacity_policy_id = ?", id).
			Delete(&domain.InboxCapacityLimit{}).Error; err != nil {
			return err
		}

		// 2. Nullify agent_capacity_policy_id on assigned account users
		if err := tx.Model(&domain.AccountUser{}).
			Where("account_id = ? AND agent_capacity_policy_id = ?", accountID, id).
			Update("agent_capacity_policy_id", nil).Error; err != nil {
			return err
		}

		// 3. Delete the policy itself
		return tx.Where("account_id = ? AND id = ?", accountID, id).
			Delete(&domain.AgentCapacityPolicy{}).Error
	})
}

// Member Management

func (r *AgentCapacityPolicyRepository) ListUsers(accountID, policyID uint) ([]domain.User, error) {
	var users []domain.User
	err := r.db.Table("users").
		Joins("JOIN account_users ON account_users.user_id = users.id").
		Where("account_users.account_id = ? AND account_users.agent_capacity_policy_id = ?", accountID, policyID).
		Order("users.id ASC").
		Find(&users).Error
	return users, err
}

func (r *AgentCapacityPolicyRepository) AddUser(accountID, policyID, userID uint) error {
	var accountUser domain.AccountUser
	err := r.db.Where("account_id = ? AND user_id = ?", accountID, userID).First(&accountUser).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrUserNotInAccount
		}
		return err
	}

	return r.db.Model(&accountUser).Update("agent_capacity_policy_id", policyID).Error
}

func (r *AgentCapacityPolicyRepository) RemoveUser(accountID, policyID, userID uint) error {
	res := r.db.Model(&domain.AccountUser{}).
		Where("account_id = ? AND agent_capacity_policy_id = ? AND user_id = ?", accountID, policyID, userID).
		Update("agent_capacity_policy_id", nil)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// Inbox Capacity Limits Management

func (r *AgentCapacityPolicyRepository) CreateInboxLimit(accountID, policyID, inboxID uint, limit int) (*domain.InboxCapacityLimit, error) {
	// Verify inbox belongs to account
	var inbox domain.Inbox
	if err := r.db.Where("account_id = ? AND id = ?", accountID, inboxID).First(&inbox).Error; err != nil {
		return nil, err
	}

	// Validate no duplicate inbox limit under this policy
	var existing domain.InboxCapacityLimit
	if err := r.db.Where("agent_capacity_policy_id = ? AND inbox_id = ?", policyID, inboxID).First(&existing).Error; err == nil {
		return nil, ErrDuplicateInboxLimit
	}

	inboxLimit := domain.InboxCapacityLimit{
		AgentCapacityPolicyID: policyID,
		InboxID:               inboxID,
		ConversationLimit:     limit,
	}

	if err := r.db.Create(&inboxLimit).Error; err != nil {
		return nil, err
	}

	inboxLimit.Inbox = &inbox
	return &inboxLimit, nil
}

func (r *AgentCapacityPolicyRepository) UpdateInboxLimit(accountID, policyID, limitID uint, limit int) (*domain.InboxCapacityLimit, error) {
	var inboxLimit domain.InboxCapacityLimit
	if err := r.db.Preload("Inbox").
		Where("id = ? AND agent_capacity_policy_id = ?", limitID, policyID).
		First(&inboxLimit).Error; err != nil {
		return nil, err
	}

	if err := r.db.Model(&inboxLimit).Update("conversation_limit", limit).Error; err != nil {
		return nil, err
	}
	inboxLimit.ConversationLimit = limit
	return &inboxLimit, nil
}

func (r *AgentCapacityPolicyRepository) DeleteInboxLimit(accountID, policyID, limitID uint) error {
	res := r.db.Where("id = ? AND agent_capacity_policy_id = ?", limitID, policyID).
		Delete(&domain.InboxCapacityLimit{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *AgentCapacityPolicyRepository) FindInboxLimit(policyID, inboxID uint) (*domain.InboxCapacityLimit, error) {
	var limit domain.InboxCapacityLimit
	err := r.db.Where("agent_capacity_policy_id = ? AND inbox_id = ?", policyID, inboxID).First(&limit).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &limit, nil
}
