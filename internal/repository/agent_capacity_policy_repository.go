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
	return r.db.Omit("InboxCapacityLimits", "AccountUsers").Create(policy).Error
}

func (r *AgentCapacityPolicyRepository) SaveLegacyCapacityPolicy(policy *domain.CapacityPolicy) error {
	return r.db.Where("account_id = ? AND user_id = ?", policy.AccountID, policy.UserID).
		Assign(domain.CapacityPolicy{ConversationLimit: policy.ConversationLimit}).
		FirstOrCreate(policy).Error
}

func (r *AgentCapacityPolicyRepository) FindSummaryByID(accountID, id uint) (*domain.AgentCapacityPolicy, error) {
	var policy domain.AgentCapacityPolicy
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&policy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &policy, err
}

func (r *AgentCapacityPolicyRepository) FindLegacyCapacityPolicy(accountID, userID uint) (*domain.CapacityPolicy, error) {
	var policy domain.CapacityPolicy
	err := r.db.Where("account_id = ? AND user_id = ?", accountID, userID).First(&policy).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = r.db.Where("account_id = ? AND user_id = 0", accountID).First(&policy).Error
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &policy, err
}

func (r *AgentCapacityPolicyRepository) ListLegacyCapacityPolicies(accountID uint) ([]domain.CapacityPolicy, error) {
	policies := make([]domain.CapacityPolicy, 0)
	err := r.db.Where("account_id = ?", accountID).Order("id ASC").Find(&policies).Error
	return policies, err
}

func (r *AgentCapacityPolicyRepository) FindByID(accountID, id uint) (*domain.AgentCapacityPolicy, error) {
	return r.FindSummaryByID(accountID, id)
}

func (r *AgentCapacityPolicyRepository) ListByAccount(accountID uint) ([]domain.AgentCapacityPolicy, error) {
	var policies []domain.AgentCapacityPolicy
	err := r.db.Where("account_id = ?", accountID).Order("id ASC").Find(&policies).Error
	return policies, err
}

func (r *AgentCapacityPolicyRepository) Update(policy *domain.AgentCapacityPolicy) error {
	return r.db.Omit("InboxCapacityLimits", "AccountUsers").Save(policy).Error
}

func (r *AgentCapacityPolicyRepository) Delete(accountID, id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// 1. Delete associated inbox capacity limits
		if err := tx.Where("agent_capacity_policy_id = ?", id).
			Delete(&domain.InboxCapacityLimit{}).Error; err != nil {
			return err
		}

		// TEN membership references are cleared by the orchestrating handler.
		return tx.Where("account_id = ? AND id = ?", accountID, id).
			Delete(&domain.AgentCapacityPolicy{}).Error
	})
}

// Inbox Capacity Limits Management

func (r *AgentCapacityPolicyRepository) CreateInboxLimit(policyID, inboxID uint, limit int) (*domain.InboxCapacityLimit, error) {
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

	return &inboxLimit, nil
}

func (r *AgentCapacityPolicyRepository) UpdateInboxLimit(policyID, limitID uint, limit int) (*domain.InboxCapacityLimit, error) {
	var inboxLimit domain.InboxCapacityLimit
	if err := r.db.Where("id = ? AND agent_capacity_policy_id = ?", limitID, policyID).
		First(&inboxLimit).Error; err != nil {
		return nil, err
	}

	if err := r.db.Model(&inboxLimit).Update("conversation_limit", limit).Error; err != nil {
		return nil, err
	}
	inboxLimit.ConversationLimit = limit
	return &inboxLimit, nil
}

func (r *AgentCapacityPolicyRepository) DeleteInboxLimit(policyID, limitID uint) error {
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

func (r *AgentCapacityPolicyRepository) ListInboxLimits(policyID uint) ([]domain.InboxCapacityLimit, error) {
	var limits []domain.InboxCapacityLimit
	err := r.db.Where("agent_capacity_policy_id = ?", policyID).Order("id ASC").Find(&limits).Error
	return limits, err
}

func (r *AgentCapacityPolicyRepository) FindInboxLimitByID(policyID, limitID uint) (*domain.InboxCapacityLimit, error) {
	var limit domain.InboxCapacityLimit
	err := r.db.Where("id = ? AND agent_capacity_policy_id = ?", limitID, policyID).First(&limit).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &limit, err
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
