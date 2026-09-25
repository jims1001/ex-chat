package repository

import (
	"errors"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type TeamRepository struct {
	db    *gorm.DB
	users teamUserPort
}

type teamUserPort interface {
	FindByIDs(ids []uint) ([]domain.User, error)
}

func NewTeamRepository(db *gorm.DB) *TeamRepository {
	return NewTeamRepositoryWithUserPort(db, NewUserRepository(db))
}

func NewTeamRepositoryWithUserPort(db *gorm.DB, users teamUserPort) *TeamRepository {
	return &TeamRepository{db: db, users: users}
}

func (r *TeamRepository) Create(team *domain.Team) error {
	return r.db.Omit("Members").Create(team).Error
}

func (r *TeamRepository) FindByID(accountID, id uint) (*domain.Team, error) {
	var team domain.Team
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&team).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	team.Members, err = r.ListMembers(team.ID)
	if err != nil {
		return nil, err
	}
	return &team, nil
}

func (r *TeamRepository) FindSummaryByID(accountID, id uint) (*domain.Team, error) {
	var team domain.Team
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&team).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &team, err
}

func (r *TeamRepository) List(accountID uint) ([]domain.Team, error) {
	var teams []domain.Team
	err := r.db.Where("account_id = ?", accountID).Find(&teams).Error
	if err != nil {
		return nil, err
	}
	for i := range teams {
		teams[i].Members, err = r.ListMembers(teams[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return teams, nil
}

func (r *TeamRepository) Update(team *domain.Team) error {
	return r.db.Omit("Members").Save(team).Error
}

func (r *TeamRepository) Delete(accountID, id uint) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("team_id IN (?)", tx.Model(&domain.Team{}).Select("id").Where("account_id = ? AND id = ?", accountID, id)).Delete(&domain.TeamMember{}).Error; err != nil {
			return err
		}
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
	var ids []uint
	if err := r.db.Model(&domain.TeamMember{}).Where("team_id = ?", teamID).Pluck("user_id", &ids).Error; err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return []domain.User{}, nil
	}
	if r.users == nil {
		return nil, gorm.ErrInvalidData
	}
	return r.users.FindByIDs(ids)
}
