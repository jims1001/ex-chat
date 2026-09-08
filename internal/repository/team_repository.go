package repository

import (
	"errors"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type TeamRepository struct {
	db *gorm.DB
}

func NewTeamRepository(db *gorm.DB) *TeamRepository {
	return &TeamRepository{db: db}
}

func (r *TeamRepository) Create(team *domain.Team) error {
	return r.db.Create(team).Error
}

func (r *TeamRepository) FindByID(accountID, id uint) (*domain.Team, error) {
	var team domain.Team
	err := r.db.Preload("Members").Where("account_id = ? AND id = ?", accountID, id).First(&team).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &team, nil
}

func (r *TeamRepository) List(accountID uint) ([]domain.Team, error) {
	var teams []domain.Team
	err := r.db.Preload("Members").Where("account_id = ?", accountID).Find(&teams).Error
	return teams, err
}

func (r *TeamRepository) Update(team *domain.Team) error {
	return r.db.Save(team).Error
}

func (r *TeamRepository) Delete(accountID, id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		_ = tx.Where("team_id = ?", id).Delete(&domain.TeamMember{}).Error
		return tx.Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.Team{}).Error
	})
}

func (r *TeamRepository) AddMember(teamID, userID uint) error {
	tm := domain.TeamMember{
		TeamID: teamID,
		UserID: userID,
	}
	return r.db.Where(tm).FirstOrCreate(&tm).Error
}

func (r *TeamRepository) RemoveMember(teamID, userID uint) error {
	return r.db.Where("team_id = ? AND user_id = ?", teamID, userID).Delete(&domain.TeamMember{}).Error
}

func (r *TeamRepository) ListMembers(teamID uint) ([]domain.User, error) {
	var users []domain.User
	err := r.db.Joins("JOIN team_members ON team_members.user_id = users.id").
		Where("team_members.team_id = ?", teamID).
		Find(&users).Error
	return users, err
}
