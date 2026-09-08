package database_test

import (
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

func TestInitDB_Memory(t *testing.T) {
	cfg := &config.Config{
		DBDriver: "sqlite",
		DBPath:   ":memory:",
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	// Verify Account table works
	account := domain.Account{
		Name:   "Test Workspace",
		Locale: "en",
	}
	if err := db.Create(&account).Error; err != nil {
		t.Fatalf("failed to insert account: %v", err)
	}

	if account.ID == 0 {
		t.Fatalf("expected account.ID > 0, got %d", account.ID)
	}

	var found domain.Account
	if err := db.First(&found, account.ID).Error; err != nil {
		t.Fatalf("failed to retrieve account: %v", err)
	}
	if found.Name != "Test Workspace" {
		t.Errorf("expected name 'Test Workspace', got '%s'", found.Name)
	}
}
