package repository

import (
	"errors"
	"fmt"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type AccountRepository struct {
	db    *gorm.DB
	users teamUserPort
}

func NewAccountRepository(db *gorm.DB) *AccountRepository {
	return NewAccountRepositoryWithUserPort(db, NewUserRepository(db))
}

func NewAccountRepositoryWithUserPort(db *gorm.DB, users teamUserPort) *AccountRepository {
	return &AccountRepository{db: db, users: users}
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
	return r.AddMemberWithRole(accountID, userID, role, nil)
}

func (r *AccountRepository) AddMemberWithRole(accountID, userID uint, role string, customRoleID *uint) error {
	member := domain.AccountUser{
		AccountID:    accountID,
		UserID:       userID,
		Role:         role,
		CustomRoleID: customRoleID,
		Availability: domain.AvailabilityOnline,
	}
	return r.db.Create(&member).Error
}

func (r *AccountRepository) GetMembership(accountID, userID uint) (*domain.AccountUser, error) {
	var member domain.AccountUser
	err := r.db.Preload("CustomRole").Where("account_id = ? AND user_id = ?", accountID, userID).First(&member).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	// Fallback: if CustomRole is nil, but member.Role is set to a custom role name (e.g. "Tier2 Support"), lookup custom role by name
	if member.CustomRole == nil && member.Role != domain.RoleAdministrator && member.Role != domain.RoleAgent && member.Role != "" {
		var role domain.CustomRole
		if err := r.db.Where("account_id = ? AND name = ?", accountID, member.Role).First(&role).Error; err == nil && role.ID > 0 {
			member.CustomRole = &role
			member.CustomRoleID = &role.ID
		}
	}
	return &member, nil
}

// FindMember returns TEN-owned membership data composed with its IAM user.
// Consumers do not need access to either owner's storage to validate a member.
func (r *AccountRepository) FindMember(accountID, userID uint) (*domain.AccountUser, error) {
	member, err := r.GetMembership(accountID, userID)
	if err != nil || member == nil {
		return member, err
	}
	if r.users == nil {
		return nil, gorm.ErrInvalidData
	}
	users, err := r.users.FindByIDs([]uint{userID})
	if err != nil {
		return nil, err
	}
	if len(users) == 0 {
		return nil, nil
	}
	member.User = &users[0]
	return member, nil
}

func (r *AccountRepository) HasPermission(accountID, userID uint, permission string) (bool, error) {
	member, err := r.GetMembership(accountID, userID)
	if err != nil || member == nil {
		return false, err
	}
	if member.Role == domain.RoleAdministrator {
		return true, nil
	}
	if member.CustomRole != nil {
		if domain.CheckPermissionInJSON(member.CustomRole.Permissions, permission) {
			return true, nil
		}
	}
	return false, nil
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
	err := r.db.Preload("CustomRole").Where("account_id = ?", accountID).Find(&members).Error
	if err != nil {
		return nil, err
	}
	if len(members) > 0 {
		if r.users == nil {
			return nil, gorm.ErrInvalidData
		}
		ids := make([]uint, len(members))
		for i := range members {
			ids[i] = members[i].UserID
		}
		users, err := r.users.FindByIDs(ids)
		if err != nil {
			return nil, err
		}
		byID := make(map[uint]*domain.User, len(users))
		for i := range users {
			byID[users[i].ID] = &users[i]
		}
		for i := range members {
			members[i].User = byID[members[i].UserID]
		}
	}
	for i := range members {
		if members[i].CustomRole == nil && members[i].Role != domain.RoleAdministrator && members[i].Role != domain.RoleAgent && members[i].Role != "" {
			var role domain.CustomRole
			if err := r.db.Where("account_id = ? AND name = ?", accountID, members[i].Role).First(&role).Error; err == nil && role.ID > 0 {
				members[i].CustomRole = &role
				members[i].CustomRoleID = &role.ID
			}
		}
	}
	return members, nil
}

func (r *AccountRepository) UpdateMember(member *domain.AccountUser) error {
	return r.db.Omit("User", "AgentCapacityPolicy").Save(member).Error
}

func (r *AccountRepository) ListCapacityPolicyMembers(accountID, policyID uint) ([]domain.AccountUser, error) {
	var members []domain.AccountUser
	if err := r.db.Where("account_id = ? AND agent_capacity_policy_id = ?", accountID, policyID).Order("user_id ASC").Find(&members).Error; err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return members, nil
	}
	if r.users == nil {
		return nil, gorm.ErrInvalidData
	}
	ids := make([]uint, len(members))
	for i := range members {
		ids[i] = members[i].UserID
	}
	users, err := r.users.FindByIDs(ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[uint]*domain.User, len(users))
	for i := range users {
		byID[users[i].ID] = &users[i]
	}
	for i := range members {
		members[i].User = byID[members[i].UserID]
	}
	return members, nil
}

func (r *AccountRepository) CountCapacityPolicyMembers(accountID, policyID uint) (int64, error) {
	var count int64
	err := r.db.Model(&domain.AccountUser{}).Where("account_id = ? AND agent_capacity_policy_id = ?", accountID, policyID).Count(&count).Error
	return count, err
}

func (r *AccountRepository) AssignCapacityPolicy(accountID, userID, policyID uint) error {
	result := r.db.Model(&domain.AccountUser{}).Where("account_id = ? AND user_id = ?", accountID, userID).Update("agent_capacity_policy_id", policyID)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *AccountRepository) RemoveCapacityPolicy(accountID, userID, policyID uint) error {
	result := r.db.Model(&domain.AccountUser{}).
		Where("account_id = ? AND user_id = ? AND agent_capacity_policy_id = ?", accountID, userID, policyID).
		Update("agent_capacity_policy_id", nil)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *AccountRepository) ClearCapacityPolicy(accountID, policyID uint) error {
	return r.db.Model(&domain.AccountUser{}).
		Where("account_id = ? AND agent_capacity_policy_id = ?", accountID, policyID).
		Update("agent_capacity_policy_id", nil).Error
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
	err := r.db.Save(role).Error
	if err == nil {
		_ = NewJournalRepository(r.db).RecordChange(r.db, &domain.LocalChangeJournal{
			AccountID:     role.AccountID,
			EntityType:    "CustomRole",
			EntityID:      role.ID,
			ObjectType:    "CustomRole",
			ObjectID:      role.ID,
			Action:        "role_update",
			ActorType:     "System",
			ActorID:       0,
			Reason:        fmt.Sprintf("Updated permissions or details for role %s", role.Name),
			ChangedFields: role.Permissions,
			Result:        "applied",
			OccurredAt:    time.Now().UTC(),
		})
	}
	return err
}

func (r *AccountRepository) DeleteCustomRole(accountID, id uint) error {
	return r.db.Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.CustomRole{}).Error
}
