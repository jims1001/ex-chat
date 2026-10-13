package modruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
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

// ExecutePrepared preserves successful receipt replay even when a dependency
// required only for a new admission is unavailable. Execute rechecks the receipt
// under its transaction after preparation, so racing identical calls stay safe.
func (s *CommandReceiptStore) ExecutePrepared(ctx context.Context, command foundation.CommandEnvelope, prepare CommandPreflight, run func(context.Context) (any, error)) (any, error) {
	if prepare == nil {
		return s.Execute(ctx, command, run)
	}
	output, found, err := s.preparedReceipt(ctx, command)
	if found || err != nil {
		return output, err
	}
	prepared, err := prepare(ctx, command)
	if err != nil {
		// Another identical request may have committed while this request observed
		// a transient dependency failure. The committed receipt remains authoritative.
		output, found, lookupErr := s.preparedReceipt(ctx, command)
		if found || lookupErr != nil {
			return output, lookupErr
		}
		return nil, err
	}
	if prepared == nil {
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "command preparation returned no context"}
	}
	return s.Execute(prepared, command, run)
}

func (s *CommandReceiptStore) preparedReceipt(ctx context.Context, command foundation.CommandEnvelope) (any, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, &ContractFailure{Type: "dependency_unavailable", Message: "command receipt store is unavailable"}
	}
	keyHash := sha256.Sum256([]byte(command.IdempotencyKey))
	changeID := "cmd-" + hex.EncodeToString(keyHash[:])[:48]
	digest, err := commandDigest(command)
	if err != nil {
		return nil, false, &ContractFailure{Type: "validation_failed", Message: "command could not be normalized"}
	}
	var receipt domain.LocalChangeJournal
	err = s.db.WithContext(ctx).Where("change_id = ?", changeID).First(&receipt).Error
	if err == nil {
		if receipt.Diff != digest {
			return nil, true, &ContractFailure{Type: "state_conflict", Message: "idempotency key was used with different command input"}
		}
		var output any
		if decodeReceipt(command, receipt.AfterState, &output) != nil {
			return nil, true, &ContractFailure{Type: "dependency_unavailable", Message: "command receipt could not be decoded"}
		}
		return output, true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, &ContractFailure{Type: "dependency_unavailable", Message: "command receipt store is unavailable"}
	}
	return nil, false, nil
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
	db := s.db
	if command.CommandType == cannedResponseManage || command.CommandType == messageFromCannedPreview || command.CommandType == cannedResponsePrepareSend || command.CommandType == cannedResponseSend {
		db = db.WithContext(ctx)
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		// This owner capability serializes before receipt reads too, avoiding
		// SQLite deferred read-to-write upgrades and sharing the legacy lock.
		if command.CommandType == cannedResponseManage {
			if err := repository.LockCannedResponseAccount(tx, command.AccountID); err != nil {
				return err
			}
		}
		var receipt domain.LocalChangeJournal
		err := tx.Where("change_id = ?", changeID).First(&receipt).Error
		if err == nil {
			if receipt.Diff != digest {
				return &ContractFailure{Type: "state_conflict", Message: "idempotency key was used with different command input"}
			}
			return decodeReceipt(command, receipt.AfterState, &output)
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
		if isUniqueConflict(err) {
			var receipt domain.LocalChangeJournal
			if loadErr := s.db.Where("change_id = ?", changeID).First(&receipt).Error; loadErr == nil {
				if receipt.Diff != digest {
					return nil, &ContractFailure{Type: "state_conflict", Message: "idempotency key was used with different command input"}
				}
				if decodeErr := decodeReceipt(command, receipt.AfterState, &output); decodeErr == nil {
					return output, nil
				}
			}
		}
		var failure *ContractFailure
		if errors.As(err, &failure) {
			return nil, failure
		}
		if command.CommandType == cannedResponseSend {
			return nil, cannedSendUnknown()
		}
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "command receipt transaction failed"}
	}
	return output, nil
}

func isUniqueConflict(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate key") || strings.Contains(message, "unique constraint") || strings.Contains(message, "unique constraint failed")
}

func commandDigest(command foundation.CommandEnvelope) (string, error) {
	stable := struct {
		Type      string
		AccountID uint
		Target    foundation.ContractTarget
		Input     map[string]any
		Actor     *foundation.ContractActor `json:",omitempty"`
	}{Type: command.CommandType, AccountID: command.AccountID, Target: command.Target, Input: command.Input}
	// These owner action contracts are new and bind their System actor authority
	// to the receipt. Existing capabilities retain byte-identical legacy digests.
	switch command.CommandType {
	case savedFilterManage, cannedResponseManage, messageFromCannedPreview, cannedResponsePrepareSend, cannedResponseSend:
		actor := command.Actor
		stable.Actor = &actor
	case "con.automation.apply.v1", "con.automation.message.prepare.v1", "cus.automation.label.resolve.v1", "msg.automation.message.create.v1":
		actor := command.Actor
		stable.Actor = &actor
	}
	encoded, err := json.Marshal(stable)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

// Saved criteria are opaque JSON: receipt replay must not round their numbers.
// Other capabilities keep their existing output decoding behavior.
func decodeReceipt(command foundation.CommandEnvelope, raw string, output *any) error {
	if command.CommandType != savedFilterManage && command.CommandType != cannedResponseManage && command.CommandType != messageFromCannedPreview && command.CommandType != cannedResponsePrepareSend && command.CommandType != cannedResponseSend {
		return json.Unmarshal([]byte(raw), output)
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(output)
}
