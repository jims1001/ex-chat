package repository

import (
	"errors"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type ChannelAuthEnterpriseRepository interface {
	// Calls & Conferences
	CreateCall(call *domain.Call) error
	ListCalls(accountID uint) ([]domain.Call, error)
	GetCall(accountID uint, id uint) (*domain.Call, error)
	UpdateCall(call *domain.Call) error
	CreateConference(conf *domain.Conference) error
	GetConference(accountID uint, inboxID uint) (*domain.Conference, error)

	// SAML
	GetSAMLSetting(accountID uint) (*domain.SAMLSetting, error)
	SaveSAMLSetting(setting *domain.SAMLSetting) error

	// Sessions & MFA & Password Reset
	CreateSession(sess *domain.UserSession) error
	ListSessions(userID uint) ([]domain.UserSession, error)
	DeleteSession(userID uint, sessionID uint) error
	CreatePasswordResetToken(token *domain.PasswordResetToken) error
	GetValidPasswordResetToken(tokenStr string) (*domain.PasswordResetToken, error)
	MarkPasswordResetTokenUsed(tokenID uint) error
	GetMFAProfile(userID uint) (*domain.MFAProfile, error)
	SaveMFAProfile(profile *domain.MFAProfile) error

	// Data Imports & Migrations
	CreateDataImport(imp *domain.DataImport) error
	ListDataImports(accountID uint) ([]domain.DataImport, error)
	UpdateDataImport(imp *domain.DataImport) error
	CreateMigrationJob(job *domain.MigrationJob) error
	ListMigrationJobs(accountID uint) ([]domain.MigrationJob, error)

	// Reporting Events
	CreateReportingEvent(evt *domain.ReportingEvent) error
	ListReportingEvents(accountID uint, name string) ([]domain.ReportingEvent, error)

	// Enterprise Limits & Billing
	GetAccountLimit(accountID uint) (*domain.AccountLimit, error)
	SaveAccountLimit(limit *domain.AccountLimit) error
	RecordBilling(billing *domain.AccountBilling) error
	ListBillings(accountID uint) ([]domain.AccountBilling, error)

	// Onboarding & Branded Email & Email Migrations
	GetOnboarding(accountID uint) (*domain.Onboarding, error)
	SaveOnboarding(onboarding *domain.Onboarding) error
	GetBrandedEmailLayout(accountID uint) (*domain.BrandedEmailLayout, error)
	SaveBrandedEmailLayout(layout *domain.BrandedEmailLayout) error
	CreateEmailMigration(mig *domain.EmailChannelMigration) error
	ListEmailMigrations(accountID uint) ([]domain.EmailChannelMigration, error)

	// WebRTC ICE Candidates
	CreateCallCandidate(cand *domain.CallICECandidate) error
	ListCallCandidates(callID uint) ([]domain.CallICECandidate, error)

	// SaaS Subscriptions
	ListSubscriptionPlans() ([]domain.SubscriptionPlan, error)
	GetSubscriptionPlan(id uint) (*domain.SubscriptionPlan, error)
	GetAccountSubscription(accountID uint) (*domain.AccountSubscription, error)
	SaveAccountSubscription(sub *domain.AccountSubscription) error

	GetDB() *gorm.DB
}

type channelAuthEnterpriseRepository struct {
	db *gorm.DB
}

func NewChannelAuthEnterpriseRepository(db *gorm.DB) ChannelAuthEnterpriseRepository {
	return &channelAuthEnterpriseRepository{db: db}
}

// Calls & Conferences
func (r *channelAuthEnterpriseRepository) CreateCall(call *domain.Call) error {
	return r.db.Create(call).Error
}

func (r *channelAuthEnterpriseRepository) ListCalls(accountID uint) ([]domain.Call, error) {
	var calls []domain.Call
	err := r.db.Where("account_id = ?", accountID).Order("id DESC").Find(&calls).Error
	return calls, err
}

func (r *channelAuthEnterpriseRepository) GetCall(accountID uint, id uint) (*domain.Call, error) {
	var call domain.Call
	err := r.db.Where("account_id = ? AND id = ?", accountID, id).First(&call).Error
	if err != nil {
		return nil, err
	}
	return &call, nil
}

func (r *channelAuthEnterpriseRepository) UpdateCall(call *domain.Call) error {
	return r.db.Save(call).Error
}

func (r *channelAuthEnterpriseRepository) CreateConference(conf *domain.Conference) error {
	return r.db.Create(conf).Error
}

func (r *channelAuthEnterpriseRepository) GetConference(accountID uint, inboxID uint) (*domain.Conference, error) {
	var conf domain.Conference
	err := r.db.Where("account_id = ? AND inbox_id = ?", accountID, inboxID).Order("id DESC").First(&conf).Error
	if err != nil {
		return nil, err
	}
	return &conf, nil
}

// SAML
func (r *channelAuthEnterpriseRepository) GetSAMLSetting(accountID uint) (*domain.SAMLSetting, error) {
	var setting domain.SAMLSetting
	err := r.db.Where("account_id = ?", accountID).First(&setting).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &setting, nil
}

func (r *channelAuthEnterpriseRepository) SaveSAMLSetting(setting *domain.SAMLSetting) error {
	var existing domain.SAMLSetting
	err := r.db.Where("account_id = ?", setting.AccountID).First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.Create(setting).Error
		}
		return err
	}
	setting.ID = existing.ID
	return r.db.Save(setting).Error
}

// Sessions & MFA & Password Reset
func (r *channelAuthEnterpriseRepository) CreateSession(sess *domain.UserSession) error {
	return r.db.Create(sess).Error
}

func (r *channelAuthEnterpriseRepository) ListSessions(userID uint) ([]domain.UserSession, error) {
	var sessions []domain.UserSession
	err := r.db.Where("user_id = ?", userID).Order("id DESC").Find(&sessions).Error
	return sessions, err
}

func (r *channelAuthEnterpriseRepository) DeleteSession(userID uint, sessionID uint) error {
	return r.db.Where("user_id = ? AND id = ?", userID, sessionID).Delete(&domain.UserSession{}).Error
}

func (r *channelAuthEnterpriseRepository) CreatePasswordResetToken(token *domain.PasswordResetToken) error {
	return r.db.Create(token).Error
}

func (r *channelAuthEnterpriseRepository) GetValidPasswordResetToken(tokenStr string) (*domain.PasswordResetToken, error) {
	var token domain.PasswordResetToken
	err := r.db.Where("token = ? AND used = ? AND expires_at > ?", tokenStr, false, time.Now()).First(&token).Error
	if err != nil {
		return nil, err
	}
	return &token, nil
}

func (r *channelAuthEnterpriseRepository) MarkPasswordResetTokenUsed(tokenID uint) error {
	return r.db.Model(&domain.PasswordResetToken{}).Where("id = ?", tokenID).Update("used", true).Error
}

func (r *channelAuthEnterpriseRepository) GetMFAProfile(userID uint) (*domain.MFAProfile, error) {
	var profile domain.MFAProfile
	err := r.db.Where("user_id = ?", userID).First(&profile).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &profile, nil
}

func (r *channelAuthEnterpriseRepository) SaveMFAProfile(profile *domain.MFAProfile) error {
	var existing domain.MFAProfile
	err := r.db.Where("user_id = ?", profile.UserID).First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.Create(profile).Error
		}
		return err
	}
	profile.ID = existing.ID
	return r.db.Save(profile).Error
}

// Data Imports & Migrations
func (r *channelAuthEnterpriseRepository) CreateDataImport(imp *domain.DataImport) error {
	return r.db.Create(imp).Error
}

func (r *channelAuthEnterpriseRepository) ListDataImports(accountID uint) ([]domain.DataImport, error) {
	var imports []domain.DataImport
	err := r.db.Where("account_id = ?", accountID).Order("id DESC").Find(&imports).Error
	return imports, err
}

func (r *channelAuthEnterpriseRepository) UpdateDataImport(imp *domain.DataImport) error {
	return r.db.Save(imp).Error
}

func (r *channelAuthEnterpriseRepository) CreateMigrationJob(job *domain.MigrationJob) error {
	return r.db.Create(job).Error
}

func (r *channelAuthEnterpriseRepository) ListMigrationJobs(accountID uint) ([]domain.MigrationJob, error) {
	var jobs []domain.MigrationJob
	err := r.db.Where("account_id = ?", accountID).Order("id DESC").Find(&jobs).Error
	return jobs, err
}

// Reporting Events
func (r *channelAuthEnterpriseRepository) CreateReportingEvent(evt *domain.ReportingEvent) error {
	return r.db.Create(evt).Error
}

func (r *channelAuthEnterpriseRepository) ListReportingEvents(accountID uint, name string) ([]domain.ReportingEvent, error) {
	var events []domain.ReportingEvent
	query := r.db.Where("account_id = ?", accountID)
	if name != "" {
		query = query.Where("name = ?", name)
	}
	err := query.Order("id DESC").Find(&events).Error
	return events, err
}

// Enterprise Limits & Billing
func (r *channelAuthEnterpriseRepository) GetAccountLimit(accountID uint) (*domain.AccountLimit, error) {
	var limit domain.AccountLimit
	err := r.db.Where("account_id = ?", accountID).First(&limit).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &domain.AccountLimit{
				AccountID:         accountID,
				ConversationLimit: 10000,
				AgentLimit:        50,
				InboxLimit:        20,
				Credits:           1000.0,
				Currency:          "USD",
			}, nil
		}
		return nil, err
	}
	return &limit, nil
}

func (r *channelAuthEnterpriseRepository) SaveAccountLimit(limit *domain.AccountLimit) error {
	var existing domain.AccountLimit
	err := r.db.Where("account_id = ?", limit.AccountID).First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.Create(limit).Error
		}
		return err
	}
	limit.ID = existing.ID
	return r.db.Save(limit).Error
}

func (r *channelAuthEnterpriseRepository) RecordBilling(billing *domain.AccountBilling) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(billing).Error; err != nil {
			return err
		}
		var limit domain.AccountLimit
		if err := tx.Where("account_id = ?", billing.AccountID).First(&limit).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				limit = domain.AccountLimit{
					AccountID: billing.AccountID,
					Credits:   1000.0,
					Currency:  billing.Currency,
				}
				if err := tx.Create(&limit).Error; err != nil {
					return err
				}
			} else {
				return err
			}
		}
		if billing.ActionType == "deposit" {
			limit.Credits += billing.Amount
		} else if billing.ActionType == "deduction" {
			limit.Credits -= billing.Amount
		}
		return tx.Save(&limit).Error
	})
}

func (r *channelAuthEnterpriseRepository) ListBillings(accountID uint) ([]domain.AccountBilling, error) {
	var list []domain.AccountBilling
	err := r.db.Where("account_id = ?", accountID).Order("id DESC").Find(&list).Error
	return list, err
}

// Onboarding & Branded Email & Email Migrations
func (r *channelAuthEnterpriseRepository) GetOnboarding(accountID uint) (*domain.Onboarding, error) {
	var onb domain.Onboarding
	err := r.db.Where("account_id = ?", accountID).First(&onb).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &domain.Onboarding{
				AccountID: accountID,
				Step:      "welcome",
				Completed: false,
			}, nil
		}
		return nil, err
	}
	return &onb, nil
}

func (r *channelAuthEnterpriseRepository) SaveOnboarding(onboarding *domain.Onboarding) error {
	var existing domain.Onboarding
	err := r.db.Where("account_id = ?", onboarding.AccountID).First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.Create(onboarding).Error
		}
		return err
	}
	onboarding.ID = existing.ID
	return r.db.Save(onboarding).Error
}

func (r *channelAuthEnterpriseRepository) GetBrandedEmailLayout(accountID uint) (*domain.BrandedEmailLayout, error) {
	var layout domain.BrandedEmailLayout
	err := r.db.Where("account_id = ?", accountID).First(&layout).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &layout, nil
}

func (r *channelAuthEnterpriseRepository) SaveBrandedEmailLayout(layout *domain.BrandedEmailLayout) error {
	var existing domain.BrandedEmailLayout
	err := r.db.Where("account_id = ?", layout.AccountID).First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.Create(layout).Error
		}
		return err
	}
	layout.ID = existing.ID
	return r.db.Save(layout).Error
}

func (r *channelAuthEnterpriseRepository) CreateEmailMigration(mig *domain.EmailChannelMigration) error {
	return r.db.Create(mig).Error
}

func (r *channelAuthEnterpriseRepository) ListEmailMigrations(accountID uint) ([]domain.EmailChannelMigration, error) {
	var migrations []domain.EmailChannelMigration
	err := r.db.Where("account_id = ?", accountID).Order("id DESC").Find(&migrations).Error
	return migrations, err
}

func (r *channelAuthEnterpriseRepository) GetDB() *gorm.DB {
	return r.db
}

func (r *channelAuthEnterpriseRepository) CreateCallCandidate(cand *domain.CallICECandidate) error {
	return r.db.Create(cand).Error
}

func (r *channelAuthEnterpriseRepository) ListCallCandidates(callID uint) ([]domain.CallICECandidate, error) {
	var list []domain.CallICECandidate
	err := r.db.Where("call_id = ?", callID).Order("id ASC").Find(&list).Error
	return list, err
}

func (r *channelAuthEnterpriseRepository) ListSubscriptionPlans() ([]domain.SubscriptionPlan, error) {
	var plans []domain.SubscriptionPlan
	err := r.db.Find(&plans).Error
	if err == nil && len(plans) == 0 {
		// Initialize default tiers
		plans = []domain.SubscriptionPlan{
			{Name: "Community Free", Slug: "free", PriceCents: 0, AgentLimit: 2, AITokenLimit: 10000, Features: `["basic_chat","inbox"]`},
			{Name: "Team Starter", Slug: "starter", PriceCents: 2900, AgentLimit: 10, AITokenLimit: 100000, Features: `["basic_chat","inbox","automation","sla"]`},
			{Name: "Business Pro", Slug: "pro", PriceCents: 9900, AgentLimit: 50, AITokenLimit: 500000, Features: `["basic_chat","inbox","automation","sla","copilot","reports_export"]`},
			{Name: "Enterprise Elite", Slug: "enterprise", PriceCents: 29900, AgentLimit: 500, AITokenLimit: 5000000, Features: `["basic_chat","inbox","automation","sla","copilot","reports_export","saml","custom_roles"]`},
		}
		for i := range plans {
			_ = r.db.Create(&plans[i]).Error
		}
	}
	return plans, err
}

func (r *channelAuthEnterpriseRepository) GetSubscriptionPlan(id uint) (*domain.SubscriptionPlan, error) {
	var plan domain.SubscriptionPlan
	err := r.db.First(&plan, id).Error
	return &plan, err
}

func (r *channelAuthEnterpriseRepository) GetAccountSubscription(accountID uint) (*domain.AccountSubscription, error) {
	var sub domain.AccountSubscription
	err := r.db.Preload("Plan").Where("account_id = ?", accountID).First(&sub).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// default to free tier
			plans, _ := r.ListSubscriptionPlans()
			var freePlanID uint = 1
			for _, p := range plans {
				if p.Slug == "free" {
					freePlanID = p.ID
					break
				}
			}
			now := time.Now().UTC()
			defaultSub := domain.AccountSubscription{
				AccountID:          accountID,
				PlanID:             freePlanID,
				Status:             "active",
				CurrentPeriodStart: now,
				CurrentPeriodEnd:   now.AddDate(1, 0, 0),
			}
			_ = r.db.Create(&defaultSub).Error
			_ = r.db.Preload("Plan").First(&defaultSub, defaultSub.ID).Error
			return &defaultSub, nil
		}
		return nil, err
	}
	return &sub, nil
}

func (r *channelAuthEnterpriseRepository) SaveAccountSubscription(sub *domain.AccountSubscription) error {
	var existing domain.AccountSubscription
	err := r.db.Where("account_id = ?", sub.AccountID).First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return r.db.Create(sub).Error
		}
		return err
	}
	sub.ID = existing.ID
	return r.db.Save(sub).Error
}
