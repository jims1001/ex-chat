package modruntime

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"unicode/utf8"

	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
)

const storedReportingEventsQuery = "rpt.report.get.v1"

var storedReportingEventsFields = [...]string{"id", "account_id", "conversation_id", "user_id", "inbox_id", "name", "value", "event_start_time", "event_end_time", "metadata_json", "created_at"}

func reportingEventCanonicalID(raw string) (uint, bool) {
	value, err := strconv.ParseUint(raw, 10, 63)
	return uint(value), err == nil && value > 0 && uint64(uint(value)) == value && strconv.FormatUint(value, 10) == raw
}

func (r *Runtime) registerStoredReportingEvents() error {
	return r.Contracts.RegisterQuery(storedReportingEventsQuery, func(ctx context.Context, query foundation.QueryEnvelope) (any, error) {
		invalid := func() (any, error) {
			return nil, &ContractFailure{Type: "validation_failed", Message: "stored reporting events query shape is unsupported"}
		}
		// Authenticated module callers supply tenant scope. Actor claims are context
		// only; no claimed role, ID or source can broaden the envelope account.
		if query.Validate() != nil || query.QueryType != storedReportingEventsQuery || uint64(query.AccountID) > math.MaxInt64 || query.Consistency != "strong" || len(query.Sort) != 1 || query.Sort[0] != "-id" || len(query.Fields) != len(storedReportingEventsFields) {
			return invalid()
		}
		for i, field := range storedReportingEventsFields {
			if query.Fields[i] != field {
				return invalid()
			}
		}
		var before, conversation uint
		if query.Cursor == "" {
			if query.Page != "1" {
				return invalid()
			}
		} else {
			var ok bool
			before, ok = reportingEventCanonicalID(query.Cursor)
			if !ok || query.Page != "" {
				return invalid()
			}
		}
		var name *string
		for key, raw := range query.Filter {
			switch key {
			case "name":
				value, ok := raw.(string)
				if !ok || len(value) > repository.ReportingEventNameBytes || !utf8.ValidString(value) {
					return invalid()
				}
				name = &value
			case "conversation_id":
				value, ok := raw.(json.Number)
				if !ok {
					return invalid()
				}
				conversation, ok = reportingEventCanonicalID(string(value))
				if !ok {
					return invalid()
				}
			default:
				return invalid()
			}
		}
		page, err := repository.NewReportingEventReadRepository(r.DB).ListStored(ctx, query.AccountID, name, conversation, before)
		if err != nil {
			return nil, &ContractFailure{Type: "dependency_unavailable", Message: "stored reporting events are unavailable"}
		}
		return page, nil
	})
}
