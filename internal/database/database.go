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
		&domain.CannedResponse{},
	)
}
