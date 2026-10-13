package service

import (
	"context"
	"net"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Opt-in, loopback-only native PostgreSQL acceptance. Each run creates a unique
// disposable schema; it neither starts PostgreSQL nor uses production schemas.
func TestAutomationCompatibilityPostgresReadOnlySnapshotAndByteGuards(t *testing.T) {
	if os.Getenv("EX_CHAT_AUT_POSTGRES_TESTS") != "1" {
		t.Skip("set EX_CHAT_AUT_POSTGRES_TESTS=1 and loopback DB_DSN")
	}
	cfg, err := pgx.ParseConfig(os.Getenv("DB_DSN"))
	require.NoError(t, err)
	ip := net.ParseIP(cfg.Host)
	require.True(t, cfg.Host == "localhost" || (ip != nil && ip.IsLoopback()), "test database must be loopback")
	admin := stdlib.OpenDB(*cfg)
	require.NoError(t, admin.Ping())
	schema := "aut_inventory_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.Exec("DROP SCHEMA " + schema + " CASCADE")
		require.NoError(t, err)
		_ = admin.Close()
	})
	cfg.RuntimeParams["search_path"] = schema
	open := func() *gorm.DB {
		pool := stdlib.OpenDB(*cfg)
		pool.SetMaxOpenConns(2)
		t.Cleanup(func() { _ = pool.Close() })
		db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), IgnoreRelationshipsWhenMigrating: true})
		require.NoError(t, err)
		return db
	}
	db, writer := open(), open()
	require.NoError(t, db.AutoMigrate(&domain.AutomationRule{}, &domain.AutomationRuleVersion{}))
	rule := compatibilityTestRule(1, 91, true)
	guarded := compatibilityTestRule(2, 91, true)
	payload := strings.Repeat("界", domain.AutomationCompatibilityMaxBytes/3) + "xx"
	require.Equal(t, domain.AutomationCompatibilityMaxBytes+1, len(payload))
	guarded.Conditions = payload
	guarded.Actions = payload
	require.NoError(t, db.Create(&[]domain.AutomationRule{rule, guarded}).Error)
	require.NoError(t, db.Create(&domain.AutomationRuleVersion{AccountID: 91, RuleID: 1, Version: 1, EventName: "conversation_created", Conditions: "[]", Actions: "[]"}).Error)
	require.NoError(t, db.Create(&domain.AutomationRuleVersion{AccountID: 91, RuleID: 2, Version: 1, EventName: "conversation_created", Conditions: payload, Actions: payload}).Error)
	var once sync.Once
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("inventory_verify_snapshot", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*automationCompatibilityRuleRow); !ok {
			return
		}
		once.Do(func() {
			var isolation, readonly string
			require.NoError(t, tx.Session(&gorm.Session{NewDB: true}).Raw("SHOW transaction_isolation").Scan(&isolation).Error)
			require.NoError(t, tx.Session(&gorm.Session{NewDB: true}).Raw("SHOW transaction_read_only").Scan(&readonly).Error)
			require.Equal(t, "repeatable read", isolation)
			require.Equal(t, "on", readonly)
			require.NoError(t, writer.Transaction(func(w *gorm.DB) error {
				if err := w.Model(&domain.AutomationRule{}).Where("account_id = ? AND id = ?", 91, 1).Update("actions", "new saved value").Error; err != nil {
					return err
				}
				return w.Create(&domain.AutomationRuleVersion{AccountID: 91, RuleID: 1, Version: 2, EventName: "conversation_created", Conditions: "[]", Actions: "new frozen value"}).Error
			}))
		})
	}))
	var input []domain.AutomationCompatibilityInput
	s := compatibilityTestService(db)
	s.compatibilityProjector = func(in domain.AutomationCompatibilityInput) domain.AutomationCompatibilityRule {
		input = append(input, in)
		return compatibilityTestProjector(in)
	}
	_, err = s.ListRuleCompatibility(context.Background(), 91, compatibilityTestOptions())
	require.NoError(t, err)
	require.Len(t, input, 2)
	require.Equal(t, rule.Actions, input[0].Rule.Actions)
	require.EqualValues(t, 1, input[0].Frozen.Version)
	require.Empty(t, input[1].Rule.Conditions)
	require.Empty(t, input[1].Rule.Actions)
	require.Empty(t, input[1].Frozen.Conditions)
	require.Empty(t, input[1].Frozen.Actions)
	require.Equal(t, len(payload), input[1].SavedConditionBytes)
	require.Equal(t, len(payload), input[1].EffectiveActionBytes)
	input = nil
	_, err = s.ListRuleCompatibility(context.Background(), 91, compatibilityTestOptions())
	require.NoError(t, err)
	require.Equal(t, "new saved value", input[0].Rule.Actions)
	require.EqualValues(t, 2, input[0].Frozen.Version)
}
