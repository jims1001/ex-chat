package modruntime

import (
	"context"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
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
