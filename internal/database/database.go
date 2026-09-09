package database

import (
	"fmt"
	"log"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func InitDB(cfg *config.Config) (*gorm.DB, error) {
	var dialector gorm.Dialector

	switch cfg.DBDriver {
	case "sqlite":
		dialector = sqlite.Open(cfg.DBPath)
	default:
		dialector = sqlite.Open(cfg.DBPath)
	}

	gormConfig := &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	}

	db, err := gorm.Open(dialector, gormConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	if sqlDB, err := db.DB(); err == nil {
		if cfg.DBPath == ":memory:" {
			sqlDB.SetMaxOpenConns(1)
		}
	}

	if err := AutoMigrate(db); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	log.Printf("database initialized successfully (%s)", cfg.DBDriver)
	return db, nil
}

func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&domain.Account{},
		&domain.User{},
		&domain.AccountUser{},
		&domain.Inbox{},
		&domain.InboxMember{},
		&domain.Contact{},
		&domain.ContactInbox{},
		&domain.Conversation{},
		&domain.Message{},
		&domain.Label{},
		&domain.ConversationLabel{},
		&domain.ContactLabel{},
		&domain.CannedResponse{},
		&domain.LocalChangeJournal{},
		&domain.AuditLog{},
		&domain.SecurityAuditLog{},
		&domain.AuditExport{},
		&domain.IntegrityVerification{},
		&domain.DataChangeCorrection{},
		&domain.AccessLog{},
		&domain.Team{},
		&domain.TeamMember{},
		&domain.CapacityPolicy{},
		&domain.AgentCapacityPolicy{},
		&domain.InboxCapacityLimit{},
		&domain.AssignmentPolicy{},
		&domain.CustomAttributeDefinition{},
		&domain.ContactNote{},
		&domain.AutomationRule{},
		&domain.Portal{},
		&domain.Category{},
		&domain.Article{},
		&domain.Webhook{},
		&domain.DashboardApp{},
		&domain.SystemConfig{},
		&domain.AccountFeature{},
		&domain.Macro{},
		&domain.Notification{},
		&domain.NotificationSetting{},
		&domain.CSATSurvey{},
		&domain.PlatformApp{},
		&domain.NotificationSubscription{},
		&domain.Company{},
		&domain.Campaign{},
		&domain.SLAPolicy{},
		&domain.AgentBot{},
		&domain.Attachment{},
		&domain.CustomFilter{},
		&domain.DraftMessage{},
		&domain.ConversationParticipant{},
		&domain.Call{},
		&domain.Conference{},
		&domain.SAMLSetting{},
		&domain.UserSession{},
		&domain.PasswordResetToken{},
		&domain.MFAProfile{},
		&domain.DataImport{},
		&domain.MigrationJob{},
		&domain.ReportingEvent{},
		&domain.AccountLimit{},
		&domain.AccountBilling{},
		&domain.Onboarding{},
		&domain.BrandedEmailLayout{},
		&domain.EmailChannelMigration{},
		&domain.WebhookDelivery{},
		&domain.IntegrationInstallation{},
		&domain.SLABreachLog{},
		&domain.CampaignDelivery{},
		&domain.PushDeliveryLog{},
		&domain.CaptainAssistant{},
		&domain.CaptainInbox{},
		&domain.CaptainAssistantResponse{},
		&domain.CaptainKnowledgeDoc{},
		&domain.CaptainDocChunk{},
		&domain.AIScenario{},
		&domain.AIUsageQuota{},
		&domain.CustomRole{},
		&domain.CallICECandidate{},
		&domain.SubscriptionPlan{},
		&domain.AccountSubscription{},
	)
}
