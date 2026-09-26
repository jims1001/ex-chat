package repository

import (
	"context"
	"errors"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// PlatformRepository manages platform-level applications and administrative provisioning
type PlatformRepository struct {
	db       *gorm.DB
	accounts *AccountRepository
	users    *UserRepository
}

// NewPlatformRepository creates a new platform repository instance
func NewPlatformRepository(db *gorm.DB) *PlatformRepository {
	users := NewUserRepository(db)
	return NewPlatformRepositoryWithOwnerPorts(db, NewAccountRepositoryWithUserPort(db, users), users)
}

func NewPlatformRepositoryWithOwnerPorts(db *gorm.DB, accounts *AccountRepository, users *UserRepository) *PlatformRepository {
	return &PlatformRepository{db: db, accounts: accounts, users: users}
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
	return r.accounts.Create(acc)
}

// GetAccount retrieves account details by primary ID
func (r *PlatformRepository) GetAccount(ctx context.Context, id uint) (*domain.Account, error) {
	acc, err := r.accounts.FindByID(id)
	if err == nil && acc == nil {
		return nil, gorm.ErrRecordNotFound
	}
	return acc, err
}

// ListAccounts queries accounts with optional search and pagination
func (r *PlatformRepository) ListAccounts(ctx context.Context, search string, page, pageSize int) ([]domain.Account, int64, error) {
	return r.accounts.List(search, page, pageSize)
}

// UpdateAccount updates account attributes
func (r *PlatformRepository) UpdateAccount(ctx context.Context, id uint, updates map[string]any) (*domain.Account, error) {
	return r.accounts.UpdateFields(id, updates)
}

// DeleteAccount cascades deletion of account users and removes the account
func (r *PlatformRepository) DeleteAccount(ctx context.Context, id uint) error {
	return r.accounts.Delete(id)
}

// ----------------- User Lifecycle Management -----------------

// ListUsers queries platform users with optional search, role filter, and pagination
func (r *PlatformRepository) ListUsers(ctx context.Context, search, role string, page, pageSize int) ([]domain.User, int64, error) {
	return r.users.List(search, role, page, pageSize)
}

// GetUser retrieves user profile along with associated tenant accounts
func (r *PlatformRepository) GetUser(ctx context.Context, id uint) (*domain.User, []domain.AccountUser, error) {
	user, err := r.users.FindByID(id)
	if err != nil {
		return nil, nil, err
	}
	if user == nil {
		return nil, nil, gorm.ErrRecordNotFound
	}
	memberships, err := r.accounts.ListMembershipsForUser(id)
	return user, memberships, err
}

// UpdateUser updates user profile fields
func (r *PlatformRepository) UpdateUser(ctx context.Context, id uint, updates map[string]any) (*domain.User, error) {
	return r.users.UpdateFields(id, updates)
}

// DeleteUser removes user and their tenant memberships
func (r *PlatformRepository) DeleteUser(ctx context.Context, id uint) error {
	if _, err := r.users.FindByID(id); err != nil {
		return err
	}
	if err := r.accounts.RemoveUserMemberships(id); err != nil {
		return err
	}
	return r.users.Delete(id)
}

// ----------------- AccountUser Membership Management -----------------

// AddUserToAccount associates a user with a tenant account, updating role if already exists
func (r *PlatformRepository) AddUserToAccount(ctx context.Context, accountID, userID uint, role string) (*domain.AccountUser, error) {
	return r.accounts.UpsertMember(accountID, userID, role)
}

// ListAccountUsers lists all member users within an account
func (r *PlatformRepository) ListAccountUsers(ctx context.Context, accountID uint, page, pageSize int) ([]domain.AccountUser, int64, error) {
	return r.accounts.ListMembersPage(accountID, page, pageSize)
}

// RemoveUserFromAccount dissociates a user from an account
func (r *PlatformRepository) RemoveUserFromAccount(ctx context.Context, accountID, userID uint) error {
	membership, err := r.accounts.GetMembership(accountID, userID)
	if err != nil {
		return err
	}
	if membership == nil {
		return errors.New("account user membership not found")
	}
	return r.accounts.RemoveMember(accountID, userID)
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
