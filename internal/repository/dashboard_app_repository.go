package repository

import (
	"encoding/json"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// DashboardAppRepository manages database operations for embedded dashboard apps
type DashboardAppRepository struct {
	db *gorm.DB
}

// NewDashboardAppRepository creates a new DashboardAppRepository
func NewDashboardAppRepository(db *gorm.DB) *DashboardAppRepository {
	return &DashboardAppRepository{db: db}
}

// List returns all dashboard apps for a given account
func (r *DashboardAppRepository) List(accountID uint) ([]domain.DashboardApp, error) {
	var apps []domain.DashboardApp
	err := r.db.Where("account_id = ?", accountID).Order("id ASC").Find(&apps).Error
	if err != nil {
		return nil, err
	}
	for i := range apps {
		if apps[i].Content != "" {
			_ = json.Unmarshal([]byte(apps[i].Content), &apps[i].ParsedContent)
		}
		if len(apps[i].ParsedContent) == 0 && apps[i].ContentURL != "" {
			apps[i].ParsedContent = []domain.DashboardAppContentItem{
				{Type: "frame", URL: apps[i].ContentURL, Link: apps[i].ContentURL},
			}
		}
	}
	return apps, nil
}

// GetByID finds a single dashboard app by ID within an account
func (r *DashboardAppRepository) GetByID(accountID, id uint) (*domain.DashboardApp, error) {
	var app domain.DashboardApp
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&app).Error
	if err != nil {
		return nil, err
	}
	if app.Content != "" {
		_ = json.Unmarshal([]byte(app.Content), &app.ParsedContent)
	}
	if len(app.ParsedContent) == 0 && app.ContentURL != "" {
		app.ParsedContent = []domain.DashboardAppContentItem{
			{Type: "frame", URL: app.ContentURL, Link: app.ContentURL},
		}
	}
	return &app, nil
}

// Create inserts a new dashboard app into the database
func (r *DashboardAppRepository) Create(app *domain.DashboardApp) error {
	if len(app.ParsedContent) > 0 {
		b, _ := json.Marshal(app.ParsedContent)
		app.Content = string(b)
		if app.ContentURL == "" {
			app.ContentURL = app.ParsedContent[0].URL
		}
	}
	return r.db.Create(app).Error
}

// Update saves modifications to an existing dashboard app
func (r *DashboardAppRepository) Update(app *domain.DashboardApp) error {
	if len(app.ParsedContent) > 0 {
		b, _ := json.Marshal(app.ParsedContent)
		app.Content = string(b)
		if app.ContentURL == "" {
			app.ContentURL = app.ParsedContent[0].URL
		}
	}
	return r.db.Save(app).Error
}

// Delete removes a dashboard app by ID within an account
func (r *DashboardAppRepository) Delete(accountID, id uint) error {
	return r.db.Where("account_id = ? AND id = ?", accountID, id).Delete(&domain.DashboardApp{}).Error
}
