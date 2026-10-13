package modruntime

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
	"strconv"
	"strings"
	"time"
)

func (r *Runtime) registerAutomationCustomerActions() error {
	return r.Contracts.RegisterCommand("cus.automation.label.resolve.v1", func(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
		if _, _, err := automationOwnerProvenance(command); err != nil {
			return nil, err
		}
		if command.Target.Type != "account" || command.Target.ID != strconv.FormatUint(uint64(command.AccountID), 10) {
			return nil, &ContractFailure{Type: "validation_failed", Message: "account target is required"}
		}
		operation, _ := command.Input["operation"].(string)
		label, ok := command.Input["label"].(string)
		label = strings.TrimSpace(label)
		if !ok || label == "" || len(label) > 100 || (operation != "add" && operation != "remove") {
			return nil, &ContractFailure{Type: "validation_failed", Message: "label resolution is invalid"}
		}
		tx := Transaction(ctx, r.DB)
		// Label titles predate a uniqueness constraint. Serialize cooperating CUS
		// automation writers without changing legacy duplicate-title semantics.
		if operation == "add" && tx.Dialector.Name() == "postgres" {
			key := sha256.Sum256([]byte(fmt.Sprintf("automation-label:%d:%s", command.AccountID, label)))
			if err := tx.Exec("SELECT pg_advisory_xact_lock(?)", int64(binary.BigEndian.Uint64(key[:8]))).Error; err != nil {
				return nil, &ContractFailure{Type: "dependency_unavailable", Message: "label resolution lock is unavailable"}
			}
		}
		lookup := tx.Where("account_id = ?", command.AccountID)
		id, parseErr := strconv.ParseUint(label, 10, 64)
		if operation == "remove" && parseErr == nil && id > 0 {
			lookup = lookup.Where("id = ?", id)
		} else {
			lookup = lookup.Where("title = ?", label)
		}
		var found domain.Label
		err := lookup.Order("id ASC").First(&found).Error
		if err == nil {
			return labelFromModel(&found), nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, &ContractFailure{Type: "dependency_unavailable", Message: "label store is unavailable"}
		}
		if operation == "remove" {
			return &labelView{AccountID: command.AccountID}, nil
		}
		now := time.Now().UTC()
		found = domain.Label{AccountID: command.AccountID, Title: label, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&found).Error; err != nil {
			return nil, &ContractFailure{Type: "state_conflict", Message: "label could not be created"}
		}
		if err := r.appendBusinessEvent(ctx, command, "cus.label.changed.v1", "Label", strconv.FormatUint(uint64(found.ID), 10), []string{"created"}, map[string]any{"label_id": found.ID, "operation": "created"}); err != nil {
			return nil, err
		}
		return labelFromModel(&found), nil
	})
}
