package database

import (
	"fmt"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	pkglogger "github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
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
		Logger: pkglogger.NewGORMLogger(200 * time.Millisecond),
	}

	db, err := gorm.Open(dialector, gormConfig)
	if err != nil {
		pkglogger.WithComponent("database").Error("failed to connect to database", "driver", cfg.DBDriver, "error", err.Error())
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	if sqlDB, err := db.DB(); err == nil {
		if cfg.DBPath == ":memory:" {
			sqlDB.SetMaxOpenConns(1)
		}
	}

	if err := AutoMigrate(db); err != nil {
		pkglogger.WithComponent("database").Error("failed to run migrations", "error", err.Error())
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	pkglogger.WithComponent("database").Info("database initialized successfully", "driver", cfg.DBDriver, "path", cfg.DBPath)
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
		&domain.CompanyNote{},
		&domain.Campaign{},
		&domain.SLAPolicy{},
		&domain.AppliedSLA{},
		&domain.SLAEvent{},
		&domain.AgentBot{},
		&domain.AgentBotInbox{},
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
		&domain.RevokedToken{},
		&domain.DataImport{},
		&domain.MigrationJob{},
		&domain.ReportingEvent{},
		&domain.AccountLimit{},
		&domain.AccountBilling{},
		&domain.Onboarding{},
		&domain.BrandedEmailLayout{},
		&domain.EmailChannelMigration{},
		&domain.EmailLog{},
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
		&domain.CopilotThread{},
		&domain.CopilotThreadMessage{},
		&domain.AICustomTool{},
		&domain.AIToolExecutionLog{},
		&domain.WidgetEvent{},
	)
}
