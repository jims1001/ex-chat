package repository

import (
	"errors"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// GetCallReadMembership returns fresh TEN-owned facts without hydrating IAM
// identities. An explicit custom-role ID must resolve within this account; a
// missing/foreign ID must not fall back to a role name or ordinary-agent scope.
// This narrower read does not change the existing membership mutation contract.
func (r *AccountRepository) GetCallReadMembership(accountID, userID uint) (*domain.AccountUser, error) {
	if r == nil || r.db == nil || r.db.Config == nil || r.db.Dialector == nil {
		return nil, errors.New("call membership unavailable")
	}
	db := r.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	var member domain.AccountUser
	if err := db.Where("account_id = ? AND user_id = ?", accountID, userID).First(&member).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if member.Role == domain.RoleAdministrator {
		return &member, nil
	}
	var roles []domain.CustomRole
	query := db.Where("account_id = ?", accountID)
	if member.CustomRoleID != nil {
		if *member.CustomRoleID == 0 {
			return &member, nil
		}
		query = query.Where("id = ?", *member.CustomRoleID)
	} else if member.Role != domain.RoleAgent && member.Role != "" {
		query = query.Where("name = ?", member.Role)
	} else {
		return &member, nil
	}
	if err := query.Limit(2).Find(&roles).Error; err != nil {
		return nil, err
	}
	if len(roles) == 1 {
		member.CustomRole = &roles[0]
	}
	return &member, nil
}
