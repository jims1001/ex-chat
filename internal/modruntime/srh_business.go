package modruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"gorm.io/gorm"
)

const savedFilterManage = "srh.custom_filter.manage.v1"
const savedFilterGet = "srh.custom_filter.get.v1"

type customFilterView struct {
	ID            uint            `json:"id"`
	AccountID     uint            `json:"account_id"`
	UserID        uint            `json:"user_id"`
	Name          string          `json:"name"`
	FilterType    string          `json:"filter_type"`
	Query         json.RawMessage `json:"query"`
	ObjectVersion int             `json:"object_version"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
	ChangeID      string          `json:"change_id,omitempty"`
}

func savedFilterView(row *domain.CustomFilter) customFilterView {
	return customFilterView{ID: row.ID, AccountID: row.AccountID, UserID: row.UserID, Name: row.Name, FilterType: row.FilterType, Query: json.RawMessage(row.Query), ObjectVersion: row.ObjectVersion, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

func savedFilterActor(actor foundation.ContractActor) (uint, error) {
	id, err := strconv.ParseUint(actor.ID, 10, strconv.IntSize)
	if actor.Type != "User" || actor.AuthSource != "module" || err != nil || id == 0 || strconv.FormatUint(id, 10) != actor.ID {
		return 0, &ContractFailure{Type: "permission_denied", Message: "saved filters require a trusted module User actor"}
	}
	return uint(id), nil
}

func (r *Runtime) registerSavedFilterContracts() error {
	dependency, ok := r.Config.Dependencies["TEN"]
	if !ok {
		return errors.New("SRH saved filters require TEN dependency")
	}
	client := newModuleClient("SRH", dependency, r.Config.ContractVersion, r.Config.MaxTimeout)
	authorize := func(ctx context.Context, account uint, actor foundation.ContractActor) error {
		userID, err := savedFilterActor(actor)
		if err != nil {
			return err
		}
		return client.requireMember(ctx, account, actor, userID)
	}
	// This capability alone requires current membership before receipt replay.
	r.Contracts.authorizers[savedFilterManage] = func(ctx context.Context, command foundation.CommandEnvelope) error {
		return authorize(ctx, command.AccountID, command.Actor)
	}
	if err := r.Contracts.RegisterCommand(savedFilterManage, r.manageSavedFilter); err != nil {
		return err
	}
	return r.Contracts.RegisterQuery(savedFilterGet, func(ctx context.Context, query foundation.QueryEnvelope) (any, error) {
		if err := authorize(ctx, query.AccountID, query.Actor); err != nil {
			return nil, err
		}
		userID, _ := savedFilterActor(query.Actor)
		if query.Consistency != "strong" || query.Page != "1" || query.Cursor != "" || len(query.Sort) != 1 || query.Sort[0] != "-id" {
			return nil, savedFilterInvalid("saved filter queries require strong consistency, page 1, no cursor and sort [-id]")
		}
		// Fields selects the documented complete DTO only; projections are not implied.
		if len(query.Fields) != 1 || query.Fields[0] != "*" {
			return nil, savedFilterInvalid("saved filter queries require fields [*]")
		}
		for key := range query.Filter {
			if key != "id" && key != "filter_type" {
				return nil, savedFilterInvalid("unsupported saved filter query field")
			}
		}
		filterType := ""
		if value, exists := query.Filter["filter_type"]; exists {
			kind, valid := value.(string)
			if !valid || !savedFilterType(kind) {
				return nil, savedFilterInvalid("filter_type is invalid")
			}
			filterType = kind
		}
		repo := repository.NewCustomFilterRepository(r.DB)
		if raw, exists := query.Filter["id"]; exists {
			if len(query.Filter) != 1 {
				return nil, savedFilterInvalid("detail query accepts only id")
			}
			id, err := savedFilterID(raw)
			if err != nil {
				return nil, err
			}
			row, err := repo.FindPrivate(ctx, query.AccountID, userID, id, false)
			if err != nil {
				return nil, savedFilterStoreError(err)
			}
			return savedFilterView(row), nil
		}
		rows, err := repo.ListPrivate(ctx, query.AccountID, userID, filterType)
		if err != nil {
			return nil, savedFilterStoreError(err)
		}
		result := make([]customFilterView, 0, len(rows))
		for i := range rows {
			result = append(result, savedFilterView(&rows[i]))
		}
		return result, nil
	})
}

func (r *Runtime) manageSavedFilter(ctx context.Context, command foundation.CommandEnvelope) (any, error) {
	userID, err := savedFilterActor(command.Actor)
	if err != nil {
		return nil, err
	}
	operation, ok := command.Input["operation"].(string)
	if !ok || (operation != "create" && operation != "update" && operation != "delete") {
		return nil, savedFilterInvalid("operation must be create, update or delete")
	}
	for key := range command.Input {
		if key != "operation" && (operation == "delete" || (key != "name" && key != "filter_type" && key != "query")) {
			return nil, savedFilterInvalid("unsupported saved filter input field")
		}
	}
	tx := Transaction(ctx, r.DB)
	repo := repository.NewCustomFilterRepository(tx)
	var row *domain.CustomFilter
	before := 0
	if operation == "create" {
		if command.Target.Type != "account" || command.Target.ID != strconv.FormatUint(uint64(command.AccountID), 10) || command.Target.ExpectedVersion != 0 {
			return nil, savedFilterInvalid("create requires the account target without expected_version")
		}
		row = &domain.CustomFilter{AccountID: command.AccountID, UserID: userID, ObjectVersion: 1}
	} else {
		if command.Target.Type != "custom_filter" || command.Target.ExpectedVersion == 0 || command.Target.ExpectedVersion >= uint64(^uint(0)>>1) {
			return nil, savedFilterInvalid("custom_filter target and valid expected_version are required")
		}
		id, err := savedFilterID(command.Target.ID)
		if err != nil {
			return nil, err
		}
		row, err = repo.FindPrivate(ctx, command.AccountID, userID, id, true)
		if err != nil {
			return nil, savedFilterStoreError(err)
		}
		if uint64(row.ObjectVersion) != command.Target.ExpectedVersion {
			return nil, savedFilterConflict()
		}
		before = row.ObjectVersion
	}
	changed := []string{}
	if operation != "delete" {
		if value, exists := command.Input["name"]; exists {
			name, ok := value.(string)
			if !ok || strings.TrimSpace(name) == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 255 {
				return nil, savedFilterInvalid("name must be a nonblank string of at most 255 characters")
			}
			if row.Name != name {
				row.Name = name
				changed = append(changed, "name")
			}
		} else if operation == "create" {
			return nil, savedFilterInvalid("name is required")
		}
		if value, exists := command.Input["filter_type"]; exists {
			kind, ok := value.(string)
			if !ok || !savedFilterType(kind) {
				return nil, savedFilterInvalid("filter_type is invalid")
			}
			if row.FilterType != kind {
				row.FilterType = kind
				changed = append(changed, "filter_type")
			}
		} else if operation == "create" {
			return nil, savedFilterInvalid("filter_type is required")
		}
		if value, exists := command.Input["query"]; exists {
			encoded, err := savedFilterObject(value)
			if err != nil {
				return nil, err
			}
			if !savedFilterJSONEqual(row.Query, string(encoded)) {
				row.Query = string(encoded)
				changed = append(changed, "query")
			}
		} else if operation == "create" {
			return nil, savedFilterInvalid("query is required")
		}
		if operation == "update" && len(command.Input) == 1 {
			return nil, savedFilterInvalid("update requires a mutable field")
		}
		if operation == "update" && len(changed) == 0 {
			return savedFilterView(row), nil
		}
	}
	now := time.Now().UTC()
	row.UpdatedAt = now
	if operation == "create" {
		row.CreatedAt = now
		if err := repo.Create(ctx, row); err != nil {
			return nil, savedFilterStoreError(err)
		}
	} else {
		row.ObjectVersion = before + 1
		var applied bool
		if operation == "update" {
			applied, err = repo.SavePrivateCAS(ctx, row, before)
		} else {
			changed = []string{"deleted"}
			applied, err = repo.DeletePrivateCAS(ctx, command.AccountID, userID, row.ID, before)
		}
		if err != nil {
			return nil, savedFilterStoreError(err)
		}
		if !applied {
			return nil, savedFilterConflict()
		}
	}
	changeID, err := r.recordSavedFilterChange(ctx, command, row, operation, before, changed)
	if err != nil {
		return nil, err
	}
	if operation == "delete" {
		return map[string]any{"id": row.ID, "account_id": row.AccountID, "user_id": row.UserID, "deleted": true, "object_version": row.ObjectVersion, "change_id": changeID}, nil
	}
	result := savedFilterView(row)
	result.ChangeID = changeID
	return result, nil
}

func (r *Runtime) recordSavedFilterChange(ctx context.Context, command foundation.CommandEnvelope, row *domain.CustomFilter, operation string, before int, changed []string) (string, error) {
	tx := Transaction(ctx, r.DB)
	changeID, eventID := foundation.GenerateUUID(), foundation.GenerateUUID()
	fields, _ := json.Marshal(changed)
	payload := map[string]any{"filter_id": row.ID, "user_id": row.UserID, "filter_type": row.FilterType, "operation": operation, "object_version": row.ObjectVersion}
	metadata, _ := json.Marshal(payload)
	now := time.Now().UTC()
	journal := domain.LocalChangeJournal{ChangeID: changeID, SourceModule: "SRH", AccountID: row.AccountID, ObjectType: "CustomFilter", ObjectID: row.ID, ObjectDisplayID: strconv.FormatUint(uint64(row.ID), 10), ObjectVersionBefore: before, ObjectVersionAfter: row.ObjectVersion, Action: operation, ChangedFields: string(fields), Diff: string(metadata), ActorType: command.Actor.Type, ActorID: row.UserID, CommandID: command.CommandID, EventID: eventID, CorrelationID: command.CorrelationID, CausationID: command.CausationID, CreatedAt: now}
	if err := repository.NewJournalRepository(tx).RecordLocalChange(tx, &journal); err != nil {
		return "", savedFilterStoreError(err)
	}
	event := foundation.EventEnvelope{EventID: eventID, EventType: "srh.custom_filter.changed.v1", EventVersion: "v1", SourceModule: "SRH", AccountID: row.AccountID, AggregateType: "CustomFilter", AggregateID: strconv.FormatUint(uint64(row.ID), 10), AggregateVersion: uint64(row.ObjectVersion), OccurredAt: now, PublishedAt: now, Actor: command.Actor, CorrelationID: command.CorrelationID, CausationID: command.CommandID, ChangedFields: changed, Payload: payload, DataClassification: "internal"}
	if err := NewEventOutbox(tx, "SRH").Append(tx, event); err != nil {
		return "", savedFilterStoreError(err)
	}
	return changeID, nil
}

func savedFilterType(kind string) bool {
	return kind == "conversation" || kind == "contact" || kind == "report"
}
func savedFilterInvalid(message string) error {
	return &ContractFailure{Type: "validation_failed", Message: message}
}
func savedFilterConflict() error {
	return &ContractFailure{Type: "state_conflict", Message: "saved filter version changed"}
}
func savedFilterStoreError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &ContractFailure{Type: "resource_not_found", Message: "saved filter was not found"}
	}
	return &ContractFailure{Type: "dependency_unavailable", Message: "saved filter store is unavailable"}
}
func savedFilterID(raw any) (uint, error) {
	encoded := ""
	switch value := raw.(type) {
	case string:
		encoded = value
	case json.Number:
		encoded = string(value)
	default:
		bytes, err := json.Marshal(raw)
		if err == nil {
			encoded = string(bytes)
		}
	}
	id, err := strconv.ParseUint(encoded, 10, strconv.IntSize)
	if err != nil || id == 0 || strconv.FormatUint(id, 10) != encoded {
		return 0, savedFilterInvalid("saved filter id is invalid")
	}
	return uint(id), nil
}
func savedFilterObject(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, savedFilterInvalid("query must be a JSON object")
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return nil, savedFilterInvalid("query must be a JSON object")
	}
	return raw, nil
}
func savedFilterJSONEqual(a, b string) bool {
	decode := func(raw string) (any, error) {
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.UseNumber()
		var value any
		err := decoder.Decode(&value)
		return value, err
	}
	left, err := decode(a)
	if err != nil {
		return false
	}
	right, err := decode(b)
	if err != nil {
		return false
	}
	return savedFilterValueEqual(left, right)
}
func savedFilterValueEqual(a, b any) bool {
	switch left := a.(type) {
	case json.Number:
		right, ok := b.(json.Number)
		return ok && savedFilterNumber(left) == savedFilterNumber(right)
	case map[string]any:
		right, ok := b.(map[string]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for key, value := range left {
			other, ok := right[key]
			if !ok || !savedFilterValueEqual(value, other) {
				return false
			}
		}
		return true
	case []any:
		right, ok := b.([]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for i := range left {
			if !savedFilterValueEqual(left[i], right[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}

// Compare decimal values without floating-point rounding or expanding powers.
// Even an opaque JSON number with an enormous exponent remains bounded by input.
func savedFilterNumber(number json.Number) string {
	value := strings.ToLower(string(number))
	sign := ""
	if strings.HasPrefix(value, "-") {
		sign = "-"
		value = value[1:]
	}
	exponent := new(big.Int)
	if index := strings.IndexByte(value, 'e'); index >= 0 {
		exponent.SetString(value[index+1:], 10)
		value = value[:index]
	}
	if index := strings.IndexByte(value, '.'); index >= 0 {
		exponent.Sub(exponent, big.NewInt(int64(len(value)-index-1)))
		value = value[:index] + value[index+1:]
	}
	value = strings.TrimLeft(value, "0")
	if value == "" {
		return "0"
	}
	trimmed := strings.TrimRight(value, "0")
	exponent.Add(exponent, big.NewInt(int64(len(value)-len(trimmed))))
	return sign + trimmed + "e" + exponent.String()
}
