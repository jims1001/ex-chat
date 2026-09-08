package repository

import (
	"errors"
	"strings"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type PortalRepository struct {
	db *gorm.DB
}

func NewPortalRepository(db *gorm.DB) *PortalRepository {
	return &PortalRepository{db: db}
}

// ---------------- Portal ----------------

func (r *PortalRepository) CreatePortal(p *domain.Portal) error {
	return r.db.Create(p).Error
}

func (r *PortalRepository) FindPortalBySlug(slug string) (*domain.Portal, error) {
	var p domain.Portal
	err := r.db.Preload("Categories").Where("slug = ?", slug).First(&p).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (r *PortalRepository) ListPortals(accountID uint) ([]domain.Portal, error) {
	var list []domain.Portal
	err := r.db.Where("account_id = ?", accountID).Order("id DESC").Find(&list).Error
	return list, err
}

// ---------------- Category ----------------

func (r *PortalRepository) CreateCategory(c *domain.Category) error {
	return r.db.Create(c).Error
}

func (r *PortalRepository) ListCategories(portalID uint) ([]domain.Category, error) {
	var list []domain.Category
	err := r.db.Where("portal_id = ?", portalID).Order("position ASC, id ASC").Find(&list).Error
	return list, err
}

// ---------------- Article ----------------

func (r *PortalRepository) CreateArticle(a *domain.Article) error {
	return r.db.Create(a).Error
}

func (r *PortalRepository) UpdateArticle(a *domain.Article) error {
	return r.db.Save(a).Error
}

func (r *PortalRepository) ListArticles(portalID uint, categoryID *uint, status string, search string) ([]domain.Article, error) {
	var list []domain.Article
	query := r.db.Preload("Category").Preload("Author").Where("portal_id = ?", portalID)

	if categoryID != nil {
		query = query.Where("category_id = ?", *categoryID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if search != "" {
		s := "%" + strings.ToLower(search) + "%"
		query = query.Where("LOWER(title) LIKE ? OR LOWER(content) LIKE ?", s, s)
	}

	err := query.Order("id DESC").Find(&list).Error
	return list, err
}

func (r *PortalRepository) FindArticleBySlug(portalID uint, slug string) (*domain.Article, error) {
	var a domain.Article
	err := r.db.Preload("Category").Preload("Author").
		Where("portal_id = ? AND slug = ? AND status = ?", portalID, slug, "published").
		First(&a).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

func (r *PortalRepository) IncrementViews(articleID uint) error {
	return r.db.Model(&domain.Article{}).Where("id = ?", articleID).
		UpdateColumn("views", gorm.Expr("views + 1")).Error
}

func (r *PortalRepository) FindPortalByID(id uint) (*domain.Portal, error) {
	var p domain.Portal
	err := r.db.First(&p, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (r *PortalRepository) UpdatePortal(p *domain.Portal) error {
	return r.db.Save(p).Error
}

func (r *PortalRepository) DeletePortal(id uint) error {
	return r.db.Delete(&domain.Portal{}, id).Error
}

func (r *PortalRepository) FindCategoryByID(id uint) (*domain.Category, error) {
	var c domain.Category
	err := r.db.First(&c, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func (r *PortalRepository) UpdateCategory(c *domain.Category) error {
	return r.db.Save(c).Error
}

func (r *PortalRepository) DeleteCategory(id uint) error {
	return r.db.Delete(&domain.Category{}, id).Error
}

func (r *PortalRepository) FindArticleByID(id uint) (*domain.Article, error) {
	var a domain.Article
	err := r.db.Preload("Category").Preload("Author").First(&a, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &a, nil
}

func (r *PortalRepository) DeleteArticle(id uint) error {
	return r.db.Delete(&domain.Article{}, id).Error
}

