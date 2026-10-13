package modruntime

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"gorm.io/gorm"
)

const cannedResponseGet = "ops.canned_response.get.v1"

var cannedResponseFields = [...]string{"id", "account_id", "short_code", "content", "created_at", "updated_at"}

func cannedResponseID(raw string) (uint, bool) {
	value, err := strconv.ParseUint(raw, 10, 63)
	return uint(value), err == nil && value > 0 && uint64(uint(value)) == value && strconv.FormatUint(value, 10) == raw
}

func (r *Runtime) registerCannedResponseRead() error {
	return r.Contracts.RegisterQuery(cannedResponseGet, func(ctx context.Context, q foundation.QueryEnvelope) (any, error) {
		invalid := func() (any, error) {
			return nil, &ContractFailure{Type: "validation_failed", Message: "canned response query shape is unsupported"}
		}
		userID, ok := cannedResponseID(q.Actor.ID)
		if q.Actor.Type != "User" || q.Actor.AuthSource != "module" || !ok {
			return nil, &ContractFailure{Type: "permission_denied", Message: "canned responses require a trusted module User actor"}
		}
		if q.Validate() != nil || q.QueryType != cannedResponseGet || uint64(q.AccountID) > math.MaxInt64 || q.Consistency != "strong" || len(q.Sort) != 1 {
			return invalid()
		}
		mode, ok := q.Filter["mode"].(string)
		if !ok {
			return invalid()
		}
		expectedFields := append([]string(nil), cannedResponseFields[:]...)
		if mode == "detail_versioned" {
			expectedFields = append(expectedFields, "object_version")
		}
		if len(q.Fields) != len(expectedFields) {
			return invalid()
		}
		for i, field := range expectedFields {
			if q.Fields[i] != field {
				return invalid()
			}
		}
		var id, before uint
		text := ""
		limit := 50
		switch mode {
		case "detail", "detail_versioned":
			if len(q.Filter) != 2 || q.Sort[0] != "id" || q.Page != "1" || q.Cursor != "" {
				return invalid()
			}
			raw, ok := q.Filter["id"].(json.Number)
			if !ok {
				return invalid()
			}
			id, ok = cannedResponseID(string(raw))
			if !ok {
				return invalid()
			}
		case "match":
			if len(q.Filter) != 2 || q.Sort[0] != "id" || q.Page != "1" || q.Cursor != "" {
				return invalid()
			}
			text, ok = q.Filter["query"].(string)
			if !ok {
				return invalid()
			}
		case "list":
			if q.Sort[0] != "-id" {
				return invalid()
			}
			if q.Cursor == "" {
				if q.Page != "1" {
					return invalid()
				}
			} else {
				before, ok = cannedResponseID(q.Cursor)
				if !ok || q.Page != "" {
					return invalid()
				}
			}
			for key, value := range q.Filter {
				switch key {
				case "mode":
				case "search":
					text, ok = value.(string)
					if !ok {
						return invalid()
					}
				case "limit":
					raw, ok := value.(json.Number)
					if !ok {
						return invalid()
					}
					parsed, ok := cannedResponseID(string(raw))
					if !ok || parsed > repository.CannedResponseReadLimit {
						return invalid()
					}
					limit = int(parsed)
				default:
					return invalid()
				}
			}
		default:
			return invalid()
		}
		if !utf8.ValidString(text) || len(text) > 4096 {
			return invalid()
		}
		dependency, ok := r.Config.Dependencies["TEN"]
		if !ok {
			return nil, &ContractFailure{Type: "dependency_unavailable", Message: "TEN membership dependency is unavailable"}
		}
		client := newModuleClient("OPS", dependency, r.Config.ContractVersion, r.Config.MaxTimeout)
		if err := requireCannedResponseMember(ctx, client, q.AccountID, q.Actor, userID); err != nil {
			return nil, err
		}
		page, err := repository.NewCannedResponseRepository(r.DB).ReadBounded(ctx, q.AccountID, mode, id, text, before, limit)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, &ContractFailure{Type: "resource_not_found", Message: "canned response was not found"}
		}
		if err != nil {
			return nil, &ContractFailure{Type: "dependency_unavailable", Message: "canned response data is unavailable within read bounds"}
		}
		if mode == "list" {
			return page, nil
		}
		if mode == "detail_versioned" {
			return cannedVersionView{StoredCannedResponse: page.Items[0], ObjectVersion: page.DetailVersion}, nil
		}
		return page.Items[0], nil
	})
}

// TEN's existing user_id string representation preserves IDs beyond float64
// precision without changing TEN or the other callers of its membership port.
func requireCannedResponseMember(ctx context.Context, client *moduleClient, accountID uint, actor foundation.ContractActor, userID uint) error {
	envelope := foundation.QueryEnvelope{QueryType: "ten.permissions.check.v1", AccountID: accountID, Actor: actor,
		Filter: map[string]any{"user_id": strconv.FormatUint(uint64(userID), 10)}, Sort: []string{"user_id"}, Page: "1",
		Fields: []string{"account_id", "user_id", "allowed"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	var member membershipCheckView
	if err := client.query(ctx, envelope, &member); err != nil {
		return err
	}
	if member.AccountID != accountID || member.UserID != userID || !member.Allowed {
		return &ContractFailure{Type: "resource_not_found", Message: "account member was not found"}
	}
	return nil
}
