package test

import (
	"os"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

func TestDockerPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		dsn = "host=localhost user=postgres password=pwd124 dbname=ex_chat port=55432 sslmode=disable TimeZone=Asia/Shanghai"
	}

	cfg := &config.Config{
		DBDriver: "postgres",
		DBDSN:    dsn,
	}

	db, err := database.InitDB(cfg)
	if err != nil {
		t.Skipf("Skipping Postgres integration test (Docker PostgreSQL not reachable: %v)", err)
		return
	}

	// 1. Verify AutoMigrate ran and tables exist
	if !db.Migrator().HasTable(&domain.Account{}) {
		t.Fatalf("expected accounts table to exist in Postgres")
	}
	if !db.Migrator().HasTable(&domain.Ticket{}) {
		t.Fatalf("expected tickets table to exist in Postgres")
	}
	if !db.Migrator().HasTable(&domain.TicketComment{}) {
		t.Fatalf("expected ticket_comments table to exist in Postgres")
	}
	if !db.Migrator().HasTable(&domain.TicketActivity{}) {
		t.Fatalf("expected ticket_activities table to exist in Postgres")
	}

	// 2. Perform CRUD operations in PostgreSQL
	acc := domain.Account{
		Name: "Postgres Test Account " + time.Now().Format("150405"),
	}
	if err := db.Create(&acc).Error; err != nil {
		t.Fatalf("failed to create account in Postgres: %v", err)
	}

	ticket := domain.Ticket{
		AccountID:     acc.ID,
		TicketNumber:  "TK-PG-" + time.Now().Format("150405"),
		Title:         "Docker Postgres Ticket Test",
		Description:   "Testing native PostgreSQL connectivity via docker container",
		Status:        "open",
		Priority:      "high",
		AssignedGroup: "TechSupport",
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatalf("failed to create ticket in Postgres: %v", err)
	}

	var loaded domain.Ticket
	if err := db.First(&loaded, ticket.ID).Error; err != nil {
		t.Fatalf("failed to load ticket from Postgres: %v", err)
	}
	if loaded.Title != ticket.Title {
		t.Errorf("expected title %s, got %s", ticket.Title, loaded.Title)
	}

	t.Logf("Successfully verified Docker PostgreSQL integration with account %d and ticket %d (%s)", acc.ID, loaded.ID, loaded.TicketNumber)
}
