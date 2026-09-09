package repository

import (
	"errors"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type AssignmentPolicyRepository struct {
	db *gorm.DB
}

func NewAssignmentPolicyRepository(db *gorm.DB) *AssignmentPolicyRepository {
	return &AssignmentPolicyRepository{db: db}
}

func (r *AssignmentPolicyRepository) Create(policy *domain.AssignmentPolicy) error {
	return r.db.Create(policy).Error
}

func (r *AssignmentPolicyRepository) FindByID(accountID, id uint) (*domain.AssignmentPolicy, error) {
	var policy domain.AssignmentPolicy
	err := r.db.Preload("FallbackAssignee").Preload("FallbackTeam").
		Where("account_id = ? AND id = ?", accountID, id).First(&policy).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &policy, nil
}

func (r *AssignmentPolicyRepository) ListByAccount(accountID uint) ([]domain.AssignmentPolicy, error) {
	var policies []domain.AssignmentPolicy
	err := r.db.Preload("FallbackAssignee").Preload("FallbackTeam").
		Where("account_id = ?", accountID).Order("id ASC").Find(&policies).Error
	return policies, err
}

func (r *AssignmentPolicyRepository) Update(policy *domain.AssignmentPolicy) error {
	return r.db.Save(policy).Error
}

func (r *AssignmentPolicyRepository) Delete(accountID, id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// Reset inboxes that are bound to this policy
		if err := tx.Model(&domain.Inbox{}).
			Where("account_id = ? AND assignment_policy_id = ?", accountID, id).
			Update("assignment_policy_id", nil).Error; err != nil {
			return err
		}
		return tx.Where("account_id = ? AND id = ?", accountID, id).
			Delete(&domain.AssignmentPolicy{}).Error
	})
}

func (r *AssignmentPolicyRepository) GetInboxPolicy(inboxID uint) (*domain.AssignmentPolicy, error) {
	var inbox domain.Inbox
	if err := r.db.Select("id, account_id, assignment_policy_id").First(&inbox, inboxID).Error; err != nil {
		return nil, err
	}
	if inbox.AssignmentPolicyID == nil || *inbox.AssignmentPolicyID == 0 {
		return nil, nil
	}
	return r.FindByID(inbox.AccountID, *inbox.AssignmentPolicyID)
}
