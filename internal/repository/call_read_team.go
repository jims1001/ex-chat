package repository

import (
	"errors"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ListMemberIDs reads only TEN-owned teams and membership rows. A membership
// attached to a missing or foreign team cannot grant account-local Call access.
func (r *TeamRepository) ListMemberIDs(accountID, teamID uint) ([]uint, error) {
	if r == nil || r.db == nil || r.db.Config == nil || r.db.Dialector == nil {
		return nil, errors.New("call access facts unavailable")
	}
	db := r.db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	var ids []uint
	ownedTeam := db.Model(&domain.Team{}).Select("id").Where("account_id = ? AND id = ?", accountID, teamID)
	err := db.Model(&domain.TeamMember{}).Where("team_id IN (?)", ownedTeam).Order("user_id ASC").Pluck("user_id", &ids).Error
	return ids, err
}
