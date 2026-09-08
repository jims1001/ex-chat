package repository

import (
	"context"
	"fmt"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// PlatformRepository manages platform-level applications and administrative provisioning
type PlatformRepository struct {
	db *gorm.DB
}

func NewPlatformRepository(db *gorm.DB) *PlatformRepository {
	return &PlatformRepository{db: db}
}

func (r *PlatformRepository) CreatePlatformApp(ctx context.Context, app *domain.PlatformApp) error {
	return r.db.WithContext(ctx).Create(app).Error
}

func (r *PlatformRepository) VerifyPlatformToken(ctx context.Context, token string) (*domain.PlatformApp, error) {
	var app domain.PlatformApp
	err := r.db.WithContext(ctx).Where("token = ?", token).First(&app).Error
	if err != nil {
		return nil, err
	}
	return &app, nil
}

func (r *PlatformRepository) CreateAccount(ctx context.Context, acc *domain.Account) error {
	return r.db.WithContext(ctx).Create(acc).Error
}

func (r *PlatformRepository) GetAccount(ctx context.Context, id uint) (*domain.Account, error) {
	var acc domain.Account
	err := r.db.WithContext(ctx).First(&acc, id).Error
	return &acc, err
}

func (r *PlatformRepository) AddUserToAccount(ctx context.Context, accountID, userID uint, role string) error {
	var existing domain.AccountUser
	err := r.db.WithContext(ctx).Where("account_id = ? AND user_id = ?", accountID, userID).First(&existing).Error
	if err == nil {
		return fmt.Errorf("user already belongs to account")
	}

	au := domain.AccountUser{
		AccountID: accountID,
		UserID:    userID,
		Role:      role,
	}
	return r.db.WithContext(ctx).Create(&au).Error
}
