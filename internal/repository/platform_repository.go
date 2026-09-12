package repository

import (
	"context"
	"errors"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// PlatformRepository manages platform-level applications and administrative provisioning
type PlatformRepository struct {
	db *gorm.DB
}

// NewPlatformRepository creates a new platform repository instance
func NewPlatformRepository(db *gorm.DB) *PlatformRepository {
	return &PlatformRepository{db: db}
}

// ----------------- Platform App & Authentication -----------------

// CreatePlatformApp registers a new platform application
func (r *PlatformRepository) CreatePlatformApp(ctx context.Context, app *domain.PlatformApp) error {
	return r.db.WithContext(ctx).Create(app).Error
}

// VerifyPlatformToken validates a platform access token
func (r *PlatformRepository) VerifyPlatformToken(ctx context.Context, token string) (*domain.PlatformApp, error) {
	var app domain.PlatformApp
	err := r.db.WithContext(ctx).Where("token = ?", token).First(&app).Error
	if err != nil {
		return nil, err
	}
	return &app, nil
}

// ----------------- Account Lifecycle Management -----------------

// CreateAccount provisions a new tenant account
func (r *PlatformRepository) CreateAccount(ctx context.Context, acc *domain.Account) error {
	return r.db.WithContext(ctx).Create(acc).Error
}

// GetAccount retrieves account details by primary ID
func (r *PlatformRepository) GetAccount(ctx context.Context, id uint) (*domain.Account, error) {
	var acc domain.Account
	err := r.db.WithContext(ctx).First(&acc, id).Error
	if err != nil {
		return nil, err
	}
	return &acc, nil
}

// ListAccounts queries accounts with optional search and pagination
func (r *PlatformRepository) ListAccounts(ctx context.Context, search string, page, pageSize int) ([]domain.Account, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 25
	}

	var accounts []domain.Account
	var total int64

	query := r.db.WithContext(ctx).Model(&domain.Account{})
	if strings.TrimSpace(search) != "" {
		s := "%" + strings.ToLower(strings.TrimSpace(search)) + "%"
		query = query.Where("LOWER(name) LIKE ? OR LOWER(domain) LIKE ?", s, s)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Order("id DESC").Offset(offset).Limit(pageSize).Find(&accounts).Error
	if err != nil {
		return nil, 0, err
	}
	return accounts, total, nil
}

// UpdateAccount updates account attributes
func (r *PlatformRepository) UpdateAccount(ctx context.Context, id uint, updates map[string]any) (*domain.Account, error) {
	var acc domain.Account
	if err := r.db.WithContext(ctx).First(&acc, id).Error; err != nil {
		return nil, err
	}

	if len(updates) > 0 {
		if err := r.db.WithContext(ctx).Model(&domain.Account{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return nil, err
		}
	}

	var updated domain.Account
	if err := r.db.WithContext(ctx).First(&updated, id).Error; err != nil {
		return nil, err
	}
	return &updated, nil
}

// DeleteAccount cascades deletion of account users and removes the account
func (r *PlatformRepository) DeleteAccount(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var acc domain.Account
		if err := tx.First(&acc, id).Error; err != nil {
			return err
		}
		_ = tx.Where("account_id = ?", id).Delete(&domain.AccountUser{}).Error
		return tx.Delete(&domain.Account{}, id).Error
	})
}

// ----------------- User Lifecycle Management -----------------

// ListUsers queries platform users with optional search, role filter, and pagination
func (r *PlatformRepository) ListUsers(ctx context.Context, search, role string, page, pageSize int) ([]domain.User, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 25
	}

	var users []domain.User
	var total int64

	query := r.db.WithContext(ctx).Model(&domain.User{})
	if strings.TrimSpace(search) != "" {
		s := "%" + strings.ToLower(strings.TrimSpace(search)) + "%"
		query = query.Where("LOWER(name) LIKE ? OR LOWER(email) LIKE ?", s, s)
	}
	if strings.TrimSpace(role) != "" {
		query = query.Where("role = ?", strings.TrimSpace(role))
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Order("id DESC").Offset(offset).Limit(pageSize).Find(&users).Error
	if err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

// GetUser retrieves user profile along with associated tenant accounts
func (r *PlatformRepository) GetUser(ctx context.Context, id uint) (*domain.User, []domain.AccountUser, error) {
	var user domain.User
	if err := r.db.WithContext(ctx).First(&user, id).Error; err != nil {
		return nil, nil, err
	}

	var memberships []domain.AccountUser
	_ = r.db.WithContext(ctx).Preload("Account").Where("user_id = ?", id).Find(&memberships).Error

	return &user, memberships, nil
}

// UpdateUser updates user profile fields
func (r *PlatformRepository) UpdateUser(ctx context.Context, id uint, updates map[string]any) (*domain.User, error) {
	var user domain.User
	if err := r.db.WithContext(ctx).First(&user, id).Error; err != nil {
		return nil, err
	}

	if len(updates) > 0 {
		if err := r.db.WithContext(ctx).Model(&domain.User{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return nil, err
		}
	}

	var updated domain.User
	if err := r.db.WithContext(ctx).First(&updated, id).Error; err != nil {
		return nil, err
	}
	return &updated, nil
}

// DeleteUser removes user and their tenant memberships
func (r *PlatformRepository) DeleteUser(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user domain.User
		if err := tx.First(&user, id).Error; err != nil {
			return err
		}
		_ = tx.Where("user_id = ?", id).Delete(&domain.AccountUser{}).Error
		return tx.Delete(&domain.User{}, id).Error
	})
}

// ----------------- AccountUser Membership Management -----------------

// AddUserToAccount associates a user with a tenant account, updating role if already exists
func (r *PlatformRepository) AddUserToAccount(ctx context.Context, accountID, userID uint, role string) (*domain.AccountUser, error) {
	if role == "" {
		role = "agent"
	}

	var au domain.AccountUser
	err := r.db.WithContext(ctx).Where("account_id = ? AND user_id = ?", accountID, userID).First(&au).Error
	if err == nil {
		if role != "" {
			au.Role = role
			if err := r.db.WithContext(ctx).Save(&au).Error; err != nil {
				return nil, err
			}
		}
		_ = r.db.WithContext(ctx).Preload("User").First(&au, au.ID)
		return &au, nil
	}

	au = domain.AccountUser{
		AccountID: accountID,
		UserID:    userID,
		Role:      role,
	}
	if err := r.db.WithContext(ctx).Create(&au).Error; err != nil {
		return nil, err
	}
	_ = r.db.WithContext(ctx).Preload("User").First(&au, au.ID)
	return &au, nil
}

// ListAccountUsers lists all member users within an account
func (r *PlatformRepository) ListAccountUsers(ctx context.Context, accountID uint, page, pageSize int) ([]domain.AccountUser, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 25
	}

	var members []domain.AccountUser
	var total int64

	query := r.db.WithContext(ctx).Model(&domain.AccountUser{}).Where("account_id = ?", accountID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Preload("User").Order("id ASC").Offset(offset).Limit(pageSize).Find(&members).Error
	if err != nil {
		return nil, 0, err
	}
	return members, total, nil
}

// RemoveUserFromAccount dissociates a user from an account
func (r *PlatformRepository) RemoveUserFromAccount(ctx context.Context, accountID, userID uint) error {
	res := r.db.WithContext(ctx).Where("account_id = ? AND user_id = ?", accountID, userID).Delete(&domain.AccountUser{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("account user membership not found")
	}
	return nil
}

// ----------------- AgentBot (Platform & Account) -----------------

// CreateAgentBot creates a new agent bot (platform-level if account_id=0, account-level if account_id>0)
func (r *PlatformRepository) CreateAgentBot(ctx context.Context, bot *domain.AgentBot) error {
	if bot.AccessToken == "" {
		bot.AccessToken = uuid.New().String()
	}
	return r.db.WithContext(ctx).Create(bot).Error
}

// ListAgentBots lists agent bots with optional account_id filter
func (r *PlatformRepository) ListAgentBots(ctx context.Context, accountID *uint, page, pageSize int) ([]domain.AgentBot, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 25
	}

	var bots []domain.AgentBot
	var total int64

	query := r.db.WithContext(ctx).Model(&domain.AgentBot{})
	if accountID != nil {
		query = query.Where("account_id = ?", *accountID)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Order("id DESC").Offset(offset).Limit(pageSize).Find(&bots).Error
	if err != nil {
		return nil, 0, err
	}
	return bots, total, nil
}

// GetAgentBot retrieves agent bot details by ID
func (r *PlatformRepository) GetAgentBot(ctx context.Context, id uint) (*domain.AgentBot, error) {
	var bot domain.AgentBot
	if err := r.db.WithContext(ctx).First(&bot, id).Error; err != nil {
		return nil, err
	}
	return &bot, nil
}

// UpdateAgentBot updates agent bot parameters
func (r *PlatformRepository) UpdateAgentBot(ctx context.Context, id uint, updates map[string]any) (*domain.AgentBot, error) {
	var bot domain.AgentBot
	if err := r.db.WithContext(ctx).First(&bot, id).Error; err != nil {
		return nil, err
	}

	if len(updates) > 0 {
		if err := r.db.WithContext(ctx).Model(&domain.AgentBot{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return nil, err
		}
	}

	var updated domain.AgentBot
	if err := r.db.WithContext(ctx).First(&updated, id).Error; err != nil {
		return nil, err
	}
	return &updated, nil
}

// DeleteAgentBot removes an agent bot
func (r *PlatformRepository) DeleteAgentBot(ctx context.Context, id uint) error {
	res := r.db.WithContext(ctx).Delete(&domain.AgentBot{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("agent bot not found")
	}
	return nil
}

// ListAccountAgentBots returns bots applicable to an account (account-scoped + global platform bots)
func (r *PlatformRepository) ListAccountAgentBots(ctx context.Context, accountID uint) ([]domain.AgentBot, error) {
	var bots []domain.AgentBot
	err := r.db.WithContext(ctx).
		Where("account_id = ? OR account_id = 0", accountID).
		Order("id DESC").
		Find(&bots).Error
	return bots, err
}

// DeleteAgentBotAvatar removes the avatar for an agent bot
func (r *PlatformRepository) DeleteAgentBotAvatar(ctx context.Context, id uint) error {
	res := r.db.WithContext(ctx).Model(&domain.AgentBot{}).Where("id = ?", id).Update("avatar_url", "")
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("agent bot not found")
	}
	return nil
}

