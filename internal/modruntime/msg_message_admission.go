package modruntime

import (
	"context"
	"strconv"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
)

// requireMessageCreateActor checks fresh User membership before receipt or echo
// replay. Service actors retain their existing admission and sender policy. This
// is defense in depth within the authenticated module caller's actor context.
func (r *Runtime) requireMessageCreateActor(ctx context.Context, command foundation.CommandEnvelope) error {
	if command.Actor.Type != "User" {
		return nil
	}
	if ctx.Err() != nil {
		return &ContractFailure{Type: "dependency_unavailable", Message: "message actor validation was canceled"}
	}
	parsed, err := strconv.ParseUint(command.Actor.ID, 10, strconv.IntSize)
	if err != nil || parsed == 0 {
		return &ContractFailure{Type: "validation_failed", Message: "message User actor ID is invalid"}
	}
	dependency, ok := r.Config.Dependencies["TEN"]
	if !ok {
		return &ContractFailure{Type: "dependency_unavailable", Message: "message actor validation is unavailable"}
	}
	// TEN supports decimal string IDs; do not round uint IDs through JSON float64.
	query := foundation.QueryEnvelope{QueryType: "ten.permissions.check.v1", AccountID: command.AccountID, Actor: command.Actor,
		Filter: map[string]any{"user_id": strconv.FormatUint(parsed, 10)}, Sort: []string{"user_id"}, Page: "1",
		Fields: []string{"account_id", "user_id", "allowed"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	var member membershipCheckView
	if err := newModuleClient("MSG", dependency, r.Config.ContractVersion, r.Config.MaxTimeout).query(ctx, query, &member); err != nil {
		return err
	}
	if ctx.Err() != nil {
		return &ContractFailure{Type: "dependency_unavailable", Message: "message actor validation was canceled"}
	}
	if member.AccountID != command.AccountID || member.UserID != uint(parsed) || !member.Allowed {
		return &ContractFailure{Type: "resource_not_found", Message: "account member was not found"}
	}
	return nil
}
