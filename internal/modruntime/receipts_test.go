package modruntime

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestCommandReceiptRollsBackBusinessWriteAndAllowsRetry(t *testing.T) {
	db := newModuleTestDB(t, "TEN")
	if err := db.Create(&domain.Account{ID: 77, Name: "Tenant"}).Error; err != nil {
		t.Fatal(err)
	}
	command := foundation.CommandEnvelope{CommandID: "command-rollback", CommandType: "ten.membership.add.v1", AccountID: 77, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "77"}, Input: map[string]any{"user_id": 9}, IdempotencyKey: "rollback-key", CorrelationID: "correlation", CausationID: "cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "ten.members.list.v1"}}
	store := NewCommandReceiptStore(db, "TEN")
	_, err := store.Execute(context.Background(), command, func(ctx context.Context) (any, error) {
		if err := Transaction(ctx, db).Create(&domain.AccountUser{AccountID: 77, UserID: 9, Role: domain.RoleAgent}).Error; err != nil {
			return nil, err
		}
		return nil, &ContractFailure{Type: "state_conflict", Message: "forced failure"}
	})
	if err == nil {
		t.Fatal("failed command unexpectedly succeeded")
	}
	var members, receipts int64
	if err := db.Model(&domain.AccountUser{}).Count(&members).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error; err != nil {
		t.Fatal(err)
	}
	if members != 0 || receipts != 0 {
		t.Fatalf("rollback members=%d receipts=%d", members, receipts)
	}
	result, err := store.Execute(context.Background(), command, func(ctx context.Context) (any, error) {
		member := domain.AccountUser{AccountID: 77, UserID: 9, Role: domain.RoleAgent}
		return map[string]any{"user_id": member.UserID}, Transaction(ctx, db).Create(&member).Error
	})
	if err != nil || result == nil {
		t.Fatalf("retry failed result=%v err=%v", result, err)
	}
}

func TestCommandReceiptAllowsOneExecutionAcrossPostgresInstances(t *testing.T) {
	db := postgresModuleDB(t, "TEN")
	command := foundation.CommandEnvelope{CommandID: "concurrent", CommandType: "ten.membership.add.v1", AccountID: 1, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "1"}, Input: map[string]any{"user_id": 9}, IdempotencyKey: "concurrent-key", CorrelationID: "correlation", CausationID: "cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "ten.members.list.v1"}}
	var executions atomic.Int64
	const instances = 8
	errorsFound := make(chan error, instances)
	var wait sync.WaitGroup
	for i := 0; i < instances; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := NewCommandReceiptStore(db, "TEN").Execute(context.Background(), command, func(context.Context) (any, error) {
				executions.Add(1)
				time.Sleep(20 * time.Millisecond)
				return map[string]any{"user_id": 9}, nil
			})
			errorsFound <- err
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatal(err)
		}
	}
	if executions.Load() != 1 {
		t.Fatalf("command executed %d times", executions.Load())
	}
}

func postgresModuleDB(t *testing.T, module string) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		t.Skip("PostgreSQL concurrency test requires DB_DSN")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := stdlib.OpenDB(*cfg)
	schema := "receipt_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec("CREATE SCHEMA " + schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE"); _ = admin.Close() })
	cfg.RuntimeParams["search_path"] = schema
	conn := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { _ = conn.Close() })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: conn}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrateModule(db, module); err != nil {
		t.Fatal(err)
	}
	return db
}
