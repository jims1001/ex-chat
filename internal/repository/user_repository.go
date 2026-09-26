package repository

import (
	"errors"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type UserRepository struct {
	db *gorm.DB
}

func (r *UserRepository) List(search, role string, page, pageSize int) ([]domain.User, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 25
	}
	query := r.db.Model(&domain.User{})
	if strings.TrimSpace(search) != "" {
		value := "%" + strings.ToLower(strings.TrimSpace(search)) + "%"
		query = query.Where("LOWER(name) LIKE ? OR LOWER(email) LIKE ?", value, value)
	}
	if strings.TrimSpace(role) != "" {
		query = query.Where("role = ?", strings.TrimSpace(role))
	}
	var users []domain.User
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&users).Error
	return users, total, err
}

func (r *UserRepository) UpdateFields(id uint, updates map[string]any) (*domain.User, error) {
	user, err := r.FindByID(id)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, gorm.ErrRecordNotFound
	}
	if len(updates) > 0 {
		if err := r.db.Model(&domain.User{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return nil, err
		}
	}
	return r.FindByID(id)
}

func (r *UserRepository) Delete(id uint) error {
	result := r.db.Delete(&domain.User{}, id)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func NewUserRepository(db *gorm.DB) *UserRepository {
	return &UserRepository{db: db}
}

func (r *UserRepository) Create(user *domain.User) error {
	return r.db.Create(user).Error
}

func (r *UserRepository) FindByEmail(email string) (*domain.User, error) {
	var user domain.User
	err := r.db.Where("email = ?", email).First(&user).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (r *UserRepository) FindByID(id uint) (*domain.User, error) {
	var user domain.User
	err := r.db.First(&user, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &user, nil
}

func (r *UserRepository) FindByIDs(ids []uint) ([]domain.User, error) {
	users := make([]domain.User, 0)
	if len(ids) == 0 {
		return users, nil
	}
	err := r.db.Where("id IN ?", ids).Order("id ASC").Find(&users).Error
	return users, err
}

func (r *UserRepository) Update(user *domain.User) error {
	return r.db.Save(user).Error
}

func (r *UserRepository) UpdateAvailability(userID uint, availability string) error {
	return r.db.Model(&domain.User{}).Where("id = ?", userID).Update("availability", availability).Error
}
