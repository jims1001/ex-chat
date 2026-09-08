package repository

import (
	"errors"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

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
	err := r.db.Preload("Members").Where("account_id = ? AND id = ?", accountID, id).First(&inbox).Error
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
	err := r.db.Preload("Members").Where("account_id = ?", accountID).Find(&inboxes).Error
	return inboxes, err
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
