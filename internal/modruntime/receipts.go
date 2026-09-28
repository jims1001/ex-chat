package modruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
)

type transactionKey struct{}

func Transaction(ctx context.Context, fallback *gorm.DB) *gorm.DB {
	if tx, ok := ctx.Value(transactionKey{}).(*gorm.DB); ok {
		return tx
	}
	return fallback
}

type CommandReceiptStore struct {
	db       *gorm.DB
	moduleID string
}

func NewCommandReceiptStore(db *gorm.DB, moduleID string) *CommandReceiptStore {
	return &CommandReceiptStore{db: db, moduleID: moduleID}
}

func (s *CommandReceiptStore) Execute(ctx context.Context, command foundation.CommandEnvelope, run func(context.Context) (any, error)) (any, error) {
	if s == nil || s.db == nil {
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "command receipt store is unavailable"}
	}
	keyHash := sha256.Sum256([]byte(command.IdempotencyKey))
	changeID := "cmd-" + hex.EncodeToString(keyHash[:])[:48]
	digest, err := commandDigest(command)
	if err != nil {
		return nil, &ContractFailure{Type: "validation_failed", Message: "command could not be normalized"}
	}
	var output any
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var receipt domain.LocalChangeJournal
		err := tx.Where("change_id = ?", changeID).First(&receipt).Error
		if err == nil {
			if receipt.Diff != digest {
				return &ContractFailure{Type: "state_conflict", Message: "idempotency key was used with different command input"}
			}
			return json.Unmarshal([]byte(receipt.AfterState), &output)
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		actorID, _ := strconv.ParseUint(command.Actor.ID, 10, 64)
		now := time.Now().UTC()
		receipt = domain.LocalChangeJournal{ChangeID: changeID, SourceModule: s.moduleID, AccountID: command.AccountID, ObjectType: command.CommandType, Action: "command_receipt", ActorType: command.Actor.Type, ActorID: uint(actorID), CommandID: command.CommandID, CorrelationID: command.CorrelationID, CausationID: command.CausationID, IdempotencyKeyHash: hex.EncodeToString(keyHash[:]), Diff: digest, OccurredAt: now, RecordedAt: now, IngestedAt: now, CreatedAt: now, Result: "pending", Status: "pending"}
		if err := tx.Create(&receipt).Error; err != nil {
			return err
		}
		output, err = run(context.WithValue(ctx, transactionKey{}, tx))
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(output)
		if err != nil {
			return err
		}
		return tx.Model(&receipt).Updates(map[string]any{"after_state": string(encoded), "result": "applied", "status": "delivered"}).Error
	})
	if err != nil {
		var failure *ContractFailure
		if errors.As(err, &failure) {
			return nil, failure
		}
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "command receipt transaction failed"}
	}
	return output, nil
}

func commandDigest(command foundation.CommandEnvelope) (string, error) {
	stable := struct {
		Type      string
		AccountID uint
		Target    foundation.ContractTarget
		Input     map[string]any
	}{command.CommandType, command.AccountID, command.Target, command.Input}
	encoded, err := json.Marshal(stable)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}
