package database

import (
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestConfirmationMigrationInvalidatesLegacyAndPreservesCurrent(t *testing.T) {
	testConfirmationMigration(t, "sqlite")
}
func TestPostgresConfirmationMigrationInvalidatesLegacyAndPreservesCurrent(t *testing.T) {
	testConfirmationMigration(t, "postgres")
}

func testConfirmationMigration(t *testing.T, driver string) {
	for _, module := range []string{"IAM", "MSG", "TEN"} {
		t.Run(module, func(t *testing.T) {
			db := moduleTestDB(t, driver)
			require.NoError(t, AutoMigrateModule(db, module))
			require.Equal(t, module == "IAM", db.Migrator().HasTable(&domain.User{}))
			require.Equal(t, module == "MSG", db.Migrator().HasTable(&domain.EmailLog{}))
			now := time.Now().UTC()
			expires := now.Add(time.Hour)
			if module == "IAM" {
				legacy := domain.User{Email: "legacy@example.test", PasswordHash: "unchanged", ConfirmationToken: strings.Repeat("c", 40), ConfirmationSentAt: &now}
				current := domain.User{Email: "current@example.test", PasswordHash: "unchanged", ConfirmationToken: "sha256:" + strings.Repeat("a", 64), ConfirmationEmail: "current@example.test", ConfirmationSentAt: &now, ConfirmationExpiresAt: &expires, ConfirmationIssuedAt: &now}
				pending := domain.User{Email: "pending@example.test", PasswordHash: "unchanged", ConfirmationToken: "sha256:" + strings.Repeat("b", 64), ConfirmationEmail: "pending@example.test", ConfirmationSentAt: &now, ConfirmationExpiresAt: &expires}
				for _, u := range []*domain.User{&legacy, &current, &pending} {
					require.NoError(t, db.Create(u).Error)
				}
				for i := 0; i < 2; i++ {
					require.NoError(t, AutoMigrateModule(db, module))
				}
				var got domain.User
				require.NoError(t, db.First(&got, legacy.ID).Error)
				require.Empty(t, got.ConfirmationToken)
				require.Nil(t, got.ConfirmationSentAt)
				require.Nil(t, got.ConfirmedAt)
				require.Equal(t, "unchanged", got.PasswordHash)
				got = domain.User{}
				require.NoError(t, db.First(&got, current.ID).Error)
				require.Equal(t, current.ConfirmationToken, got.ConfirmationToken)
				require.NotNil(t, got.ConfirmationIssuedAt)
				got = domain.User{}
				require.NoError(t, db.First(&got, pending.ID).Error)
				require.Equal(t, pending.ConfirmationToken, got.ConfirmationToken)
				require.Nil(t, got.ConfirmationIssuedAt)
			}
			if module == "MSG" {
				legacy := domain.EmailLog{AccountID: 1, EmailType: "confirmation", ToEmail: "user@example.test", FromEmail: "sender@example.test", Subject: "old", ContentHTML: "secret", ContentText: "secret", Error: "secret", BounceReason: "secret", Status: "failed", MaxRetries: 3, NextRetryAt: &now}
				other := domain.EmailLog{AccountID: 1, EmailType: "transcript", ToEmail: "user@example.test", FromEmail: "sender@example.test", Subject: "other", ContentHTML: "preserve", ContentText: "preserve", Status: "failed", MaxRetries: 3}
				require.NoError(t, db.Create(&legacy).Error)
				require.NoError(t, db.Create(&other).Error)
				for i := 0; i < 2; i++ {
					require.NoError(t, AutoMigrateModule(db, module))
				}
				var got domain.EmailLog
				require.NoError(t, db.First(&got, legacy.ID).Error)
				require.Empty(t, got.ContentHTML)
				require.Empty(t, got.ContentText)
				require.Empty(t, got.Error)
				require.Empty(t, got.BounceReason)
				require.Zero(t, got.MaxRetries)
				require.Nil(t, got.NextRetryAt)
				require.Equal(t, "failed", got.Status)
				got = domain.EmailLog{}
				require.NoError(t, db.First(&got, other.ID).Error)
				require.Equal(t, "preserve", got.ContentHTML)
				require.Equal(t, 3, got.MaxRetries)
			}
		})
	}
}

func TestConfirmationMigrationFailureRollsBackCleanup(t *testing.T) {
	db := moduleTestDB(t, "sqlite")
	require.NoError(t, db.AutoMigrate(&domain.User{}, &domain.EmailLog{}))
	legacy := domain.User{Email: "old@example.test", PasswordHash: "unchanged", ConfirmationToken: "legacy-raw"}
	mail := domain.EmailLog{EmailType: "confirmation", ToEmail: "old@example.test", FromEmail: "sender@example.test", Subject: "old", ContentText: "legacy-raw"}
	require.NoError(t, db.Create(&legacy).Error)
	require.NoError(t, db.Create(&mail).Error)
	require.NoError(t, db.Exec("CREATE TRIGGER confirmation_migration_fail BEFORE UPDATE ON email_logs BEGIN SELECT RAISE(ABORT, 'injected migration failure'); END").Error)
	require.Error(t, migrateConfirmationSecurity(db, ""))
	var u domain.User
	require.NoError(t, db.First(&u, legacy.ID).Error)
	require.Equal(t, "legacy-raw", u.ConfirmationToken)
	require.NoError(t, db.Exec("DROP TRIGGER confirmation_migration_fail").Error)
	require.NoError(t, migrateConfirmationSecurity(db, ""))
	require.NoError(t, db.First(&u, legacy.ID).Error)
	require.Empty(t, u.ConfirmationToken)
}

func TestConfirmationMigrationFailureAbortsStartup(t *testing.T) {
	db := moduleTestDB(t, "sqlite")
	require.NoError(t, AutoMigrateModule(db, "IAM"))
	require.NoError(t, db.Create(&domain.User{Email: "old@example.test", PasswordHash: "unchanged", ConfirmationToken: "legacy-raw"}).Error)
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register("confirmation_migration_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			tx.AddError(gorm.ErrInvalidTransaction)
		}
	}))
	require.Error(t, AutoMigrateModule(db, "IAM"))
	require.NoError(t, db.Callback().Update().Remove("confirmation_migration_failure"))
	require.NoError(t, AutoMigrateModule(db, "IAM"))
}

// Exercise a real pre-upgrade users schema without any digest-binding columns.
func TestConfirmationLegacySchemaUpgrade(t *testing.T) {
	db := moduleTestDB(t, "sqlite")
	require.NoError(t, db.Exec(`CREATE TABLE users (id integer PRIMARY KEY, name text NOT NULL, email text NOT NULL UNIQUE, password_hash text NOT NULL, confirmed_at datetime, confirmation_token text, confirmation_sent_at datetime)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO users (id,name,email,password_hash,confirmation_token,confirmation_sent_at) VALUES (1,'legacy','legacy@example.test','unchanged',?,?)`, strings.Repeat("f", 40), time.Now()).Error)
	require.NoError(t, AutoMigrateModule(db, "IAM"))
	var user domain.User
	require.NoError(t, db.First(&user, 1).Error)
	require.Empty(t, user.ConfirmationToken)
	require.Nil(t, user.ConfirmationExpiresAt)
	require.Nil(t, user.ConfirmationIssuedAt)
	require.Nil(t, user.ConfirmedAt)
	require.Equal(t, "unchanged", user.PasswordHash)
	require.NoError(t, AutoMigrateModule(db, "IAM"))
}
