package database

import (
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
)

func TestInvitationMigrationOwnerIsolation(t *testing.T) {
	testInvitationMigrationOwnerIsolation(t, "sqlite")
}

func TestPostgresInvitationMigrationOwnerIsolation(t *testing.T) {
	testInvitationMigrationOwnerIsolation(t, "postgres")
}

func testInvitationMigrationOwnerIsolation(t *testing.T, driver string) {
	t.Helper()
	for _, module := range []string{"TEN", "IAM", "MSG"} {
		t.Run(module, func(t *testing.T) {
			db := moduleTestDB(t, driver)
			for i := 0; i < 2; i++ {
				if err := AutoMigrateModule(db, module); err != nil {
					t.Fatal(err)
				}
			}
			if db.Migrator().HasTable(&domain.AgentInvitation{}) != (module == "TEN") {
				t.Fatal("agent invitation must exist only in TEN")
			}
			if db.Migrator().HasTable(&domain.InvitationIdentity{}) != (module == "IAM") {
				t.Fatal("activation checkpoint must exist only in IAM")
			}
			if module == "TEN" && db.Migrator().HasTable(&domain.User{}) {
				t.Fatal("TEN migration created IAM identity table")
			}
			if module == "IAM" && db.Migrator().HasTable(&domain.AccountUser{}) {
				t.Fatal("IAM migration created TEN membership table")
			}
		})
	}
}
