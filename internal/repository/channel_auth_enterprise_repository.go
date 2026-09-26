package repository

import (
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type ChannelIAMRepository interface {
	GetSAMLSetting(uint) (*domain.SAMLSetting, error)
	SaveSAMLSetting(*domain.SAMLSetting) error
	DeleteSAMLSetting(uint) error
	DisableSAMLSetting(uint) (*domain.SAMLSetting, error)
	CreateSession(*domain.UserSession) error
	ListSessions(uint) ([]domain.UserSession, error)
	DeleteSession(uint, uint) error
	CreatePasswordResetToken(*domain.PasswordResetToken) error
	GetValidPasswordResetToken(string) (*domain.PasswordResetToken, error)
	MarkPasswordResetTokenUsed(uint) error
	GetMFAProfile(uint) (*domain.MFAProfile, error)
	SaveMFAProfile(*domain.MFAProfile) error
	GenerateMFABackupCodes(uint) ([]string, error)
	RevokeToken(string, uint, time.Time) error
	IsTokenRevoked(string) (bool, error)
	ConfirmUserByToken(string) (*domain.User, error)
	GenerateConfirmationToken(uint) (string, error)
}

type ChannelRTMRepository interface {
	CreateCall(*domain.Call) error
	ListCalls(uint) ([]domain.Call, error)
	GetCall(uint, uint) (*domain.Call, error)
	UpdateCall(*domain.Call) error
	CreateConference(*domain.Conference) error
	GetConference(uint, uint) (*domain.Conference, error)
	GetConferenceByID(uint, uint) (*domain.Conference, error)
	DeleteConference(uint, uint) error
	DeleteConferenceByInbox(uint, uint) error
	GetCallByProviderCallID(uint, string) (*domain.Call, error)
	CreateCallCandidate(*domain.CallICECandidate) error
	ListCallCandidates(uint) ([]domain.CallICECandidate, error)
}

type ChannelMIGRepository interface {
	CreateDataImport(*domain.DataImport) error
	ListDataImports(uint) ([]domain.DataImport, error)
	GetDataImport(uint, uint) (*domain.DataImport, error)
	UpdateDataImport(*domain.DataImport) error
	DeleteDataImport(uint, uint) error
	CreateMigrationJob(*domain.MigrationJob) error
	ListMigrationJobs(uint) ([]domain.MigrationJob, error)
	CreateEmailMigration(*domain.EmailChannelMigration) error
	ListEmailMigrations(uint) ([]domain.EmailChannelMigration, error)
}

type ChannelRPTRepository interface {
	CreateReportingEvent(*domain.ReportingEvent) error
	ListReportingEvents(uint, string) ([]domain.ReportingEvent, error)
	ListReportingEventsByConversation(uint, uint) ([]domain.ReportingEvent, error)
}

type ChannelENTRepository interface {
	GetAccountLimit(uint) (*domain.AccountLimit, error)
	SaveAccountLimit(*domain.AccountLimit) error
	GetBrandedEmailLayout(uint) (*domain.BrandedEmailLayout, error)
	SaveBrandedEmailLayout(*domain.BrandedEmailLayout) error
}

type ChannelBILRepository interface {
	RecordBilling(*domain.AccountBilling) error
	ListBillings(uint) ([]domain.AccountBilling, error)
	ListSubscriptionPlans() ([]domain.SubscriptionPlan, error)
	GetSubscriptionPlan(uint) (*domain.SubscriptionPlan, error)
	GetSubscriptionPlanBySlug(string) (*domain.SubscriptionPlan, error)
	GetAccountSubscription(uint) (*domain.AccountSubscription, error)
	SaveAccountSubscription(*domain.AccountSubscription) error
}

type ChannelTENRepository interface {
	GetOnboarding(uint) (*domain.Onboarding, error)
	SaveOnboarding(*domain.Onboarding) error
}

type ChannelAuthEnterpriseRepository interface {
	// Calls & Conferences
	CreateCall(call *domain.Call) error
	ListCalls(accountID uint) ([]domain.Call, error)
	GetCall(accountID uint, id uint) (*domain.Call, error)
	UpdateCall(call *domain.Call) error
	CreateConference(conf *domain.Conference) error
	GetConference(accountID uint, inboxID uint) (*domain.Conference, error)
	GetConferenceByID(accountID uint, id uint) (*domain.Conference, error)
	DeleteConference(accountID uint, id uint) error
	DeleteConferenceByInbox(accountID uint, inboxID uint) error
	GetCallByProviderCallID(accountID uint, providerCallID string) (*domain.Call, error)

	// SAML
	GetSAMLSetting(accountID uint) (*domain.SAMLSetting, error)
	SaveSAMLSetting(setting *domain.SAMLSetting) error
	DeleteSAMLSetting(accountID uint) error
	DisableSAMLSetting(accountID uint) (*domain.SAMLSetting, error)

	// Sessions & MFA & Password Reset
	CreateSession(sess *domain.UserSession) error
	ListSessions(userID uint) ([]domain.UserSession, error)
	DeleteSession(userID uint, sessionID uint) error
	CreatePasswordResetToken(token *domain.PasswordResetToken) error
	GetValidPasswordResetToken(tokenStr string) (*domain.PasswordResetToken, error)
	MarkPasswordResetTokenUsed(tokenID uint) error
	GetMFAProfile(userID uint) (*domain.MFAProfile, error)
	SaveMFAProfile(profile *domain.MFAProfile) error
	GenerateMFABackupCodes(userID uint) ([]string, error)

	// Token Revocation
	RevokeToken(token string, userID uint, expiresAt time.Time) error
	IsTokenRevoked(token string) (bool, error)

	// Email Confirmation
	ConfirmUserByToken(tokenStr string) (*domain.User, error)
	GenerateConfirmationToken(userID uint) (string, error)

	// Data Imports & Migrations
	CreateDataImport(imp *domain.DataImport) error
	ListDataImports(accountID uint) ([]domain.DataImport, error)
	GetDataImport(accountID, id uint) (*domain.DataImport, error)
	UpdateDataImport(imp *domain.DataImport) error
	DeleteDataImport(accountID, id uint) error
	CreateMigrationJob(job *domain.MigrationJob) error
	ListMigrationJobs(accountID uint) ([]domain.MigrationJob, error)

	// Reporting Events
	CreateReportingEvent(evt *domain.ReportingEvent) error
	ListReportingEvents(accountID uint, name string) ([]domain.ReportingEvent, error)
	ListReportingEventsByConversation(accountID, conversationID uint) ([]domain.ReportingEvent, error)

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
	GetSubscriptionPlanBySlug(slug string) (*domain.SubscriptionPlan, error)
	GetAccountSubscription(accountID uint) (*domain.AccountSubscription, error)
	SaveAccountSubscription(sub *domain.AccountSubscription) error
}

type channelAuthEnterpriseRepository struct {
	*channelAuthIAMRepository
	*channelAuthRTMRepository
	*channelAuthMIGRepository
	*channelAuthRPTRepository
	*channelAuthENTRepository
	*channelAuthBILRepository
	*channelAuthTENRepository
}

func NewChannelAuthEnterpriseRepository(db *gorm.DB) ChannelAuthEnterpriseRepository {
	return NewChannelAuthEnterpriseRepositoryWithOwnerStores(db, db, db, db, db, db, db)
}

func NewChannelAuthEnterpriseRepositoryWithOwnerStores(iamDB, rtmDB, migDB, rptDB, entDB, bilDB, tenDB *gorm.DB) ChannelAuthEnterpriseRepository {
	return &channelAuthEnterpriseRepository{
		channelAuthIAMRepository: &channelAuthIAMRepository{db: iamDB},
		channelAuthRTMRepository: &channelAuthRTMRepository{db: rtmDB},
		channelAuthMIGRepository: &channelAuthMIGRepository{db: migDB},
		channelAuthRPTRepository: &channelAuthRPTRepository{db: rptDB},
		channelAuthENTRepository: &channelAuthENTRepository{db: entDB},
		channelAuthBILRepository: &channelAuthBILRepository{db: bilDB},
		channelAuthTENRepository: &channelAuthTENRepository{db: tenDB},
	}
}

func (r *channelAuthEnterpriseRepository) RecordBilling(billing *domain.AccountBilling) error {
	if err := r.channelAuthBILRepository.CreateBilling(billing); err != nil {
		return err
	}
	return r.channelAuthENTRepository.ApplyBilling(billing)
}
