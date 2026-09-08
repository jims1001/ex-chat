package repository

import (
	"errors"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type AccountRepository struct {
	db *gorm.DB
}

func NewAccountRepository(db *gorm.DB) *AccountRepository {
	return &AccountRepository{db: db}
}

func (r *AccountRepository) Create(account *domain.Account) error {
	return r.db.Create(account).Error
}

func (r *AccountRepository) FindByID(id uint) (*domain.Account, error) {
	var account domain.Account
	err := r.db.First(&account, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &account, nil
}

func (r *AccountRepository) Update(account *domain.Account) error {
	return r.db.Save(account).Error
}

func (r *AccountRepository) AddMember(accountID, userID uint, role string) error {
	member := domain.AccountUser{
		AccountID:    accountID,
		UserID:       userID,
		Role:         role,
		Availability: domain.AvailabilityOnline,
	}
	return r.db.Create(&member).Error
}

func (r *AccountRepository) GetMembership(accountID, userID uint) (*domain.AccountUser, error) {
	var member domain.AccountUser
	err := r.db.Where("account_id = ? AND user_id = ?", accountID, userID).First(&member).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &member, nil
}

func (r *AccountRepository) ListAccountsForUser(userID uint) ([]domain.Account, error) {
	var accounts []domain.Account
	err := r.db.Joins("JOIN account_users ON account_users.account_id = accounts.id").
		Where("account_users.user_id = ?", userID).
		Find(&accounts).Error
	return accounts, err
}

func (r *AccountRepository) ListMembers(accountID uint) ([]domain.AccountUser, error) {
	var members []domain.AccountUser
	err := r.db.Preload("User").Where("account_id = ?", accountID).Find(&members).Error
	return members, err
}

func (r *AccountRepository) UpdateMember(member *domain.AccountUser) error {
	return r.db.Save(member).Error
}

func (r *AccountRepository) RemoveMember(accountID, userID uint) error {
	return r.db.Where("account_id = ? AND user_id = ?", accountID, userID).Delete(&domain.AccountUser{}).Error
}

func (r *AccountRepository) ListCustomRoles(accountID uint) ([]domain.CustomRole, error) {
	var roles []domain.CustomRole
	err := r.db.Where("account_id = ?", accountID).Order("id ASC").Find(&roles).Error
	return roles, err
}

func (r *AccountRepository) CreateCustomRole(role *domain.CustomRole) error {
	return r.db.Create(role).Error
}

func (r *AccountRepository) GetCustomRole(accountID, id uint) (*domain.CustomRole, error) {
	var role domain.CustomRole
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&role).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &role, nil
}

func (r *AccountRepository) UpdateCustomRole(role *domain.CustomRole) error {
	return r.db.Save(role).Error
}

func (r *AccountRepository) DeleteCustomRole(accountID, id uint) error {
	return r.db.Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.CustomRole{}).Error
}


