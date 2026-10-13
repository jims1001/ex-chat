package repository

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Explicit opt-in, loopback only, disposable per-test schema. Ordinary unit
// tests never connect to a database supplied by an unrelated shell session.
func isolatedAuthPostgres(t *testing.T) (*gorm.DB, *gorm.DB) {
	t.Helper()
	if os.Getenv("EX_CHAT_AUTH_POSTGRES_TESTS") != "1" {
		t.Skip("set EX_CHAT_AUTH_POSTGRES_TESTS=1 with loopback DB_DSN for PostgreSQL transaction checks")
	}
	cfg, err := pgx.ParseConfig(os.Getenv("DB_DSN"))
	require.NoError(t, err)
	ip := net.ParseIP(cfg.Host)
	require.True(t, cfg.Host == "localhost" || (ip != nil && ip.IsLoopback()), "test database must be loopback")
	admin := stdlib.OpenDB(*cfg)
	require.NoError(t, admin.Ping())
	schema := "auth_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	cfg.RuntimeParams["search_path"] = schema
	conns := []*gorm.DB{}
	for i := 0; i < 2; i++ {
		sqlDB := stdlib.OpenDB(*cfg)
		sqlDB.SetMaxOpenConns(8)
		db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		require.NoError(t, err)
		conns = append(conns, db)
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
	t.Cleanup(func() {
		_, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		require.NoError(t, err)
		_ = admin.Close()
	})
	require.NoError(t, conns[0].AutoMigrate(&domain.User{}, &domain.MFAProfile{}, &domain.PasswordResetToken{}, &domain.UserSession{}))
	return conns[0], conns[1]
}

func TestPostgresPasswordResetAndMFASerialization(t *testing.T) {
	first, second := isolatedAuthPostgres(t)
	r1 := &channelAuthIAMRepository{db: first}
	r2 := &channelAuthIAMRepository{db: second}
	oldHash, err := auth.HashPassword("OldPassword123!")
	require.NoError(t, err)
	newHash, err := auth.HashPassword("NewPassword123!")
	require.NoError(t, err)
	newUser := func(n int) *domain.User {
		u := &domain.User{Name: "PG transaction", Email: fmt.Sprintf("pg%d@example.test", n), PasswordHash: oldHash}
		require.NoError(t, first.Create(u).Error)
		return u
	}
	parallel := func(n int, action func(int, *channelAuthIAMRepository) error) int32 {
		var won atomic.Int32
		var group sync.WaitGroup
		for i := 0; i < n; i++ {
			group.Add(1)
			go func(i int) {
				defer group.Done()
				repo := r1
				if i%2 == 1 {
					repo = r2
				}
				if action(i, repo) == nil {
					won.Add(1)
				}
			}(i)
		}
		group.Wait()
		return won.Load()
	}
	t.Run("one reset winner and atomic session invalidation", func(t *testing.T) {
		u := newUser(1)
		_, raw := activeResetToken(t, r1, u, 801)
		require.NoError(t, r1.SaveMFAProfile(&domain.MFAProfile{UserID: u.ID, Enabled: true, Secret: "JBSWY3DPEHPK3PXP", BackupCodes: `["12345678"]`}))
		require.NoError(t, first.Create(&domain.UserSession{UserID: u.ID, DeviceName: "before-reset"}).Error)
		require.EqualValues(t, 1, parallel(16, func(_ int, r *channelAuthIAMRepository) error { return r.ConsumePasswordResetToken(raw, newHash) }))
		var current domain.User
		require.NoError(t, first.First(&current, u.ID).Error)
		require.EqualValues(t, 1, current.AuthVersion)
		require.True(t, auth.CheckPasswordHash("NewPassword123!", current.PasswordHash))
		var sessions int64
		require.NoError(t, first.Model(&domain.UserSession{}).Where("user_id = ?", u.ID).Count(&sessions).Error)
		require.Zero(t, sessions)
		profile, err := r2.GetMFAProfile(u.ID)
		require.NoError(t, err)
		require.True(t, profile.Enabled)
	})
	t.Run("backup factor and persistent limit", func(t *testing.T) {
		u := newUser(2)
		require.NoError(t, r1.SaveMFAProfile(&domain.MFAProfile{UserID: u.ID, Enabled: true, Secret: "JBSWY3DPEHPK3PXP", BackupCodes: `["12345678"]`}))
		require.EqualValues(t, 1, parallel(8, func(i int, r *channelAuthIAMRepository) error {
			return r.VerifyMFALogin(u.ID, "12345678", fmt.Sprintf("pg-challenge-%d", i), time.Now().Add(time.Minute))
		}))
		p, err := r2.GetMFAProfile(u.ID)
		require.NoError(t, err)
		require.EqualValues(t, 8, p.VerificationAttempts)
		require.NotContains(t, p.BackupCodes, "12345678")
	})
	t.Run("one TOTP timestep", func(t *testing.T) {
		u := newUser(3)
		require.NoError(t, r1.SaveMFAProfile(&domain.MFAProfile{UserID: u.ID, Enabled: true, Secret: "JBSWY3DPEHPK3PXP"}))
		code, err := auth.GenerateTOTPCode("JBSWY3DPEHPK3PXP", time.Now())
		require.NoError(t, err)
		require.EqualValues(t, 1, parallel(8, func(i int, r *channelAuthIAMRepository) error {
			return r.VerifyMFALogin(u.ID, code, fmt.Sprintf("totp-%d", i), time.Now().Add(time.Minute))
		}))
	})
	t.Run("challenge replay with distinct factors", func(t *testing.T) {
		u := newUser(5)
		codes := `["11111111","22222222","33333333","44444444","55555555","66666666","77777777","88888888"]`
		require.NoError(t, r1.SaveMFAProfile(&domain.MFAProfile{UserID: u.ID, Enabled: true, Secret: "JBSWY3DPEHPK3PXP", BackupCodes: codes}))
		require.EqualValues(t, 1, parallel(8, func(i int, r *channelAuthIAMRepository) error {
			return r.VerifyMFALogin(u.ID, strings.Repeat(fmt.Sprint(i+1), 8), "same-pg-challenge", time.Now().Add(time.Minute))
		}))
		p, err := r2.GetMFAProfile(u.ID)
		require.NoError(t, err)
		var remaining []string
		require.NoError(t, json.Unmarshal([]byte(p.BackupCodes), &remaining))
		require.Len(t, remaining, 7)
	})
	t.Run("sensitive MFA changes consume one factor", func(t *testing.T) {
		u := newUser(6)
		require.NoError(t, r1.SaveMFAProfile(&domain.MFAProfile{UserID: u.ID, Enabled: true, Secret: "JBSWY3DPEHPK3PXP", BackupCodes: `["12345678"]`}))
		require.EqualValues(t, 1, parallel(8, func(_ int, r *channelAuthIAMRepository) error {
			_, _, err := r.ManageMFA(u.ID, "OldPassword123!", "backup_codes", "12345678")
			return err
		}))
		p, err := r2.GetMFAProfile(u.ID)
		require.NoError(t, err)
		require.True(t, p.Enabled)
		require.EqualValues(t, 8, p.VerificationAttempts)
		require.NotContains(t, p.BackupCodes, "12345678")
	})
	t.Run("reset rollback remains retryable", func(t *testing.T) {
		u := newUser(4)
		record, raw := activeResetToken(t, r1, u, 804)
		require.NoError(t, first.Exec(`CREATE FUNCTION deny_reset_version() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.auth_version > OLD.auth_version THEN RAISE EXCEPTION 'injected reset write failure'; END IF; RETURN NEW; END $$`).Error)
		require.NoError(t, first.Exec(`CREATE TRIGGER deny_reset_version BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION deny_reset_version()`).Error)
		require.Error(t, r1.ConsumePasswordResetToken(raw, newHash))
		var token domain.PasswordResetToken
		require.NoError(t, first.First(&token, record.ID).Error)
		require.False(t, token.Used)
		var current domain.User
		require.NoError(t, first.First(&current, u.ID).Error)
		require.Zero(t, current.AuthVersion)
		require.Equal(t, oldHash, current.PasswordHash)
		require.NoError(t, first.Exec(`DROP TRIGGER deny_reset_version ON users`).Error)
		require.NoError(t, r2.ConsumePasswordResetToken(raw, newHash))
	})
}

func TestPostgresAuthMigrationPreservesLegacyRows(t *testing.T) {
	db, _ := isolatedAuthPostgres(t)
	require.NoError(t, db.Migrator().DropColumn(&domain.User{}, "AuthVersion"))
	require.NoError(t, db.Migrator().DropColumn(&domain.PasswordResetToken{}, "IssuedAt"))
	hash, err := auth.HashPassword("ExistingPassword123!")
	require.NoError(t, err)
	require.NoError(t, db.Exec(`INSERT INTO users (name,email,password_hash) VALUES (?,?,?)`, "Existing", "existing@example.test", hash).Error)
	require.NoError(t, db.AutoMigrate(&domain.User{}, &domain.PasswordResetToken{}))
	var user domain.User
	require.NoError(t, db.Where("email = ?", "existing@example.test").First(&user).Error)
	require.Zero(t, user.AuthVersion)
	require.Equal(t, hash, user.PasswordHash)
}
