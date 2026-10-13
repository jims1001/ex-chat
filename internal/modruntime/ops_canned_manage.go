package modruntime

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"gorm.io/gorm"
)

const cannedResponseManage = "ops.canned_response.manage.v1"
const cannedResponseChanged = "ops.canned_response.changed.v1"

type cannedVersionView struct {
	repository.StoredCannedResponse
	ObjectVersion int64 `json:"object_version"`
}

func cannedManagedView(row domain.CannedResponse) cannedVersionView {
	return cannedVersionView{StoredCannedResponse: repository.StoredCannedResponse{ID: row.ID, AccountID: row.AccountID, ShortCode: row.ShortCode, Content: row.Content, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}, ObjectVersion: row.ObjectVersion}
}
func cannedInvalid() error {
	return &ContractFailure{Type: "validation_failed", Message: "canned response command shape is unsupported"}
}

func parseCannedCommand(c foundation.CommandEnvelope) (string, domain.CannedResponse, error) {
	row := domain.CannedResponse{AccountID: c.AccountID}
	if c.Validate() != nil || c.CommandType != cannedResponseManage || uint64(c.AccountID) > math.MaxInt64 || strings.TrimSpace(c.IdempotencyKey) != c.IdempotencyKey || c.IdempotencyKey == "" {
		return "", row, cannedInvalid()
	}
	operation, ok := c.Input["operation"].(string)
	if !ok {
		return "", row, cannedInvalid()
	}
	if operation == "create" {
		if c.Target.Type != "account" || c.Target.ID != strconv.FormatUint(uint64(c.AccountID), 10) || c.Target.ExpectedVersion != 0 {
			return "", row, cannedInvalid()
		}
	} else {
		id, valid := cannedResponseID(c.Target.ID)
		if !valid || c.Target.Type != "canned_response" || c.Target.ExpectedVersion == 0 || c.Target.ExpectedVersion >= math.MaxInt64 {
			return "", row, cannedInvalid()
		}
		row.ID = id
	}
	switch operation {
	case "create", "update":
		if len(c.Input) != 3 {
			return "", row, cannedInvalid()
		}
		code, ok := c.Input["short_code"].(string)
		if !ok || !utf8.ValidString(code) {
			return "", row, cannedInvalid()
		}
		row.ShortCode = repository.NormalizeCannedShortcode(code)
		if row.ShortCode == "" || utf8.RuneCountInString(row.ShortCode) > 100 {
			return "", row, cannedInvalid()
		}
		content, ok := c.Input["content"].(string)
		if !ok || content == "" || !utf8.ValidString(content) {
			return "", row, cannedInvalid()
		}
		row.Content = content
	case "delete":
		if len(c.Input) != 1 {
			return "", row, cannedInvalid()
		}
	default:
		return "", row, cannedInvalid()
	}
	return operation, row, nil
}

func (r *Runtime) registerCannedResponseManage() error {
	r.Contracts.authorizers[cannedResponseManage] = func(ctx context.Context, c foundation.CommandEnvelope) error {
		userID, ok := cannedResponseID(c.Actor.ID)
		if !ok || c.Actor.Type != "User" || c.Actor.AuthSource != "module" {
			return &ContractFailure{Type: "permission_denied", Message: "canned responses require a trusted module User actor"}
		}
		if _, _, err := parseCannedCommand(c); err != nil {
			return err
		}
		dep, ok := r.Config.Dependencies["TEN"]
		if !ok {
			return &ContractFailure{Type: "dependency_unavailable", Message: "TEN membership dependency is unavailable"}
		}
		return requireCannedResponseMember(ctx, newModuleClient("OPS", dep, r.Config.ContractVersion, r.Config.MaxTimeout), c.AccountID, c.Actor, userID)
	}
	return r.Contracts.RegisterCommand(cannedResponseManage, func(ctx context.Context, c foundation.CommandEnvelope) (any, error) {
		operation, row, err := parseCannedCommand(c)
		if err != nil {
			return nil, err
		}
		tx, ok := ctx.Value(transactionKey{}).(*gorm.DB)
		if !ok || tx == nil {
			return nil, &ContractFailure{Type: "dependency_unavailable", Message: "canned response transaction is unavailable"}
		}
		err = repository.NewCannedResponseRepository(tx.WithContext(ctx)).ManageVersioned(operation, &row, c.Target.ExpectedVersion)
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			return nil, &ContractFailure{Type: "resource_not_found", Message: "canned response was not found"}
		case errors.Is(err, repository.ErrCannedResponseDuplicate), errors.Is(err, repository.ErrCannedResponseVersion):
			return nil, &ContractFailure{Type: "state_conflict", Message: "canned response shortcode or version conflicts"}
		case err != nil:
			return nil, &ContractFailure{Type: "dependency_unavailable", Message: "canned response owner write failed"}
		}
		now := time.Now().UTC()
		event := foundation.EventEnvelope{EventID: foundation.GenerateUUID(), EventType: cannedResponseChanged, EventVersion: r.Config.ContractVersion, SourceModule: "OPS", AccountID: row.AccountID, AggregateType: "CannedResponse", AggregateID: strconv.FormatUint(uint64(row.ID), 10), AggregateVersion: uint64(row.ObjectVersion), OccurredAt: now, PublishedAt: now, Actor: c.Actor, CorrelationID: c.CorrelationID, CausationID: c.CommandID, ChangedFields: []string{operation}, Payload: map[string]any{"id": row.ID, "operation": operation, "object_version": row.ObjectVersion}, DataClassification: "internal"}
		if err := NewEventOutbox(tx, "OPS").Append(tx, event); err != nil {
			return nil, &ContractFailure{Type: "dependency_unavailable", Message: "canned response event write failed"}
		}
		if operation == "delete" {
			return map[string]any{"id": row.ID, "account_id": row.AccountID, "object_version": row.ObjectVersion, "deleted": true}, nil
		}
		return cannedManagedView(row), nil
	})
}
