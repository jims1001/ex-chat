package modruntime

import (
	"context"
	"encoding/json"
	"math"
	"strconv"

	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
)

const boundAssistantQuery = "aic.assistant.get.v1"

func (r *Runtime) registerBoundAssistantRead() error {
	return r.Contracts.RegisterQuery(boundAssistantQuery, func(ctx context.Context, query foundation.QueryEnvelope) (any, error) {
		// Authenticated allowlisted module callers assert account scope. The
		// gateway still owns current end-user and conversation/inbox access;
		// actor claims here do not authenticate a user or confer global access.
		raw, number := query.Filter["inbox_id"].(json.Number)
		inboxID, err := strconv.ParseUint(string(raw), 10, 63)
		valid := number && err == nil && inboxID > 0 && uint64(uint(inboxID)) == inboxID && strconv.FormatUint(inboxID, 10) == string(raw) &&
			query.Validate() == nil && query.QueryType == boundAssistantQuery && uint64(query.AccountID) <= math.MaxInt64 && len(query.Filter) == 1 &&
			len(query.Fields) == 2 && query.Fields[0] == "id" && query.Fields[1] == "name" &&
			len(query.Sort) == 1 && query.Sort[0] == "inbox_id" && query.Page == "1" && query.Cursor == "" && query.Consistency == "strong"
		if !valid {
			return nil, &ContractFailure{Type: "validation_failed", Message: "bound assistant query shape is unsupported"}
		}
		assistant, err := repository.NewCaptainRepositoryWithOwnerPorts(r.DB, nil, nil, nil).FindAssistantByInbox(ctx, query.AccountID, uint(inboxID))
		if err != nil {
			return nil, &ContractFailure{Type: "dependency_unavailable", Message: "bound assistant is unavailable"}
		}
		return struct {
			Assistant *repository.BoundAssistantSummary `json:"assistant"`
		}{Assistant: assistant}, nil
	})
}
