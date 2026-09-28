package modruntime

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
)

const (
	headerCaller          = "X-Module-Caller"
	headerContractVersion = "X-Contract-Version"
	headerCorrelationID   = "X-Correlation-Id"
)

type CommandHandler func(context.Context, foundation.CommandEnvelope) (any, error)
type QueryHandler func(context.Context, foundation.QueryEnvelope) (any, error)
type EventHandler func(context.Context, foundation.EventEnvelope) error

type ContractFailure struct {
	Type         string
	Message      string
	RetryAfterMS int
	Details      map[string]any
}

func (e *ContractFailure) Error() string { return e.Message }

type ContractRegistry struct {
	moduleID string
	commands map[string]CommandHandler
	queries  map[string]QueryHandler
	events   map[string]EventHandler
	receipts *CommandReceiptStore
	inbox    *EventInbox
}

func NewContractRegistry(moduleID string) *ContractRegistry {
	return &ContractRegistry{moduleID: strings.ToUpper(moduleID), commands: map[string]CommandHandler{}, queries: map[string]QueryHandler{}, events: map[string]EventHandler{}}
}

func (r *ContractRegistry) Count() int { return len(r.commands) + len(r.queries) }

func (r *ContractRegistry) UseCommandReceipts(db *gorm.DB) {
	r.receipts = NewCommandReceiptStore(db, r.moduleID)
	r.inbox = NewEventInbox(db, r.moduleID)
}

func (r *ContractRegistry) RegisterEvent(eventType string, handler EventHandler) error {
	if !strings.HasSuffix(eventType, ".v1") || len(strings.Split(eventType, ".")) < 4 {
		return errors.New("event type is invalid")
	}
	if handler == nil || r.events[eventType] != nil {
		return errors.New("event handler is nil or already registered")
	}
	r.events[eventType] = handler
	return nil
}

func (r *ContractRegistry) RegisterCommand(capability string, handler CommandHandler) error {
	if err := r.validateCapability(capability); err != nil {
		return err
	}
	if handler == nil || r.commands[capability] != nil {
		return errors.New("command handler is nil or already registered")
	}
	r.commands[capability] = handler
	return nil
}

func (r *ContractRegistry) RegisterQuery(capability string, handler QueryHandler) error {
	if err := r.validateCapability(capability); err != nil {
		return err
	}
	if handler == nil || r.queries[capability] != nil {
		return errors.New("query handler is nil or already registered")
	}
	r.queries[capability] = handler
	return nil
}

func (r *ContractRegistry) validateCapability(capability string) error {
	prefix := strings.ToLower(r.moduleID) + "."
	if !strings.HasPrefix(capability, prefix) || !strings.HasSuffix(capability, ".v1") {
		return fmt.Errorf("capability %s is not owned by %s", capability, r.moduleID)
	}
	return nil
}

func (r *ContractRegistry) Handler(cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		correlationID := strings.TrimSpace(request.Header.Get(headerCorrelationID))
		if correlationID == "" {
			correlationID = foundation.GenerateUUID()
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set(headerCorrelationID, correlationID)
		fail := func(status int, failure *ContractFailure) {
			_ = json.NewEncoder(w).Encode(contractError(correlationID, failure))
		}
		if request.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			fail(http.StatusMethodNotAllowed, &ContractFailure{Type: "validation_failed", Message: "contract transport requires POST"})
			return
		}
		requestVersion := request.Header.Get(headerContractVersion)
		if !cfg.AcceptedVersions[requestVersion] {
			w.WriteHeader(http.StatusBadRequest)
			fail(http.StatusBadRequest, &ContractFailure{Type: "validation_failed", Message: "contract version is unsupported"})
			return
		}
		caller := strings.ToUpper(strings.TrimSpace(request.Header.Get(headerCaller)))
		if !cfg.AllowedCallers[caller] || !validBearer(request.Header.Get("Authorization"), cfg.AuthToken) {
			w.WriteHeader(http.StatusUnauthorized)
			fail(http.StatusUnauthorized, &ContractFailure{Type: "authentication_failed", Message: "module caller authentication failed"})
			return
		}
		parts := strings.Split(strings.TrimPrefix(request.URL.Path, "/contracts/"), "/")
		if len(parts) != 2 || parts[1] == "" {
			w.WriteHeader(http.StatusNotFound)
			fail(http.StatusNotFound, &ContractFailure{Type: "resource_not_found", Message: "contract route was not found"})
			return
		}
		request.Body = http.MaxBytesReader(w, request.Body, cfg.MaxRequestBytes)
		switch parts[0] {
		case "commands":
			r.handleCommand(w, request, parts[1], correlationID, cfg, fail)
		case "queries":
			r.handleQuery(w, request, parts[1], correlationID, cfg, fail)
		case "events":
			r.handleEvent(w, request, parts[1], caller, requestVersion, correlationID, cfg, fail)
		default:
			w.WriteHeader(http.StatusNotFound)
			fail(http.StatusNotFound, &ContractFailure{Type: "resource_not_found", Message: "contract route was not found"})
		}
	})
}

func (r *ContractRegistry) handleEvent(w http.ResponseWriter, request *http.Request, eventType, caller, requestVersion, correlationID string, cfg Config, fail func(int, *ContractFailure)) {
	handler := r.events[eventType]
	if handler == nil {
		w.WriteHeader(http.StatusNotFound)
		fail(http.StatusNotFound, &ContractFailure{Type: "resource_not_found", Message: "event capability is not registered"})
		return
	}
	var event foundation.EventEnvelope
	if err := decodeJSON(request, &event); err != nil || event.Validate() != nil || event.EventType != eventType || event.EventVersion != requestVersion || strings.ToUpper(event.SourceModule) != caller || event.CorrelationID != correlationID {
		w.WriteHeader(http.StatusBadRequest)
		fail(http.StatusBadRequest, &ContractFailure{Type: "validation_failed", Message: "event envelope is invalid"})
		return
	}
	invoke(w, request, cfg.MaxTimeout, correlationID, func(ctx context.Context) (any, error) {
		if r.inbox == nil {
			return nil, &ContractFailure{Type: "dependency_unavailable", Message: "event inbox is unavailable"}
		}
		return r.inbox.Consume(ctx, event, func(txContext context.Context) error { return handler(txContext, event) })
	}, fail)
}

func (r *ContractRegistry) handleCommand(w http.ResponseWriter, request *http.Request, capability, correlationID string, cfg Config, fail func(int, *ContractFailure)) {
	handler := r.commands[capability]
	if handler == nil {
		w.WriteHeader(http.StatusNotFound)
		fail(http.StatusNotFound, &ContractFailure{Type: "resource_not_found", Message: "command capability is not registered"})
		return
	}
	var envelope foundation.CommandEnvelope
	if err := decodeJSON(request, &envelope); err != nil || envelope.Validate() != nil || envelope.CommandType != capability || envelope.CorrelationID != correlationID {
		w.WriteHeader(http.StatusBadRequest)
		fail(http.StatusBadRequest, &ContractFailure{Type: "validation_failed", Message: "command envelope is invalid"})
		return
	}
	timeout := time.Duration(envelope.TimeoutPolicy.TimeoutMS) * time.Millisecond
	if timeout > cfg.MaxTimeout {
		timeout = cfg.MaxTimeout
	}
	invoke(w, request, timeout, correlationID, func(ctx context.Context) (any, error) {
		if r.receipts == nil {
			return nil, &ContractFailure{Type: "dependency_unavailable", Message: "command receipt store is unavailable"}
		}
		return r.receipts.Execute(ctx, envelope, func(txContext context.Context) (any, error) { return handler(txContext, envelope) })
	}, fail)
}

func (r *ContractRegistry) handleQuery(w http.ResponseWriter, request *http.Request, capability, correlationID string, cfg Config, fail func(int, *ContractFailure)) {
	handler := r.queries[capability]
	if handler == nil {
		w.WriteHeader(http.StatusNotFound)
		fail(http.StatusNotFound, &ContractFailure{Type: "resource_not_found", Message: "query capability is not registered"})
		return
	}
	var envelope foundation.QueryEnvelope
	if err := decodeJSON(request, &envelope); err != nil || envelope.Validate() != nil || envelope.QueryType != capability {
		w.WriteHeader(http.StatusBadRequest)
		fail(http.StatusBadRequest, &ContractFailure{Type: "validation_failed", Message: "query envelope is invalid"})
		return
	}
	invoke(w, request, cfg.MaxTimeout, correlationID, func(ctx context.Context) (any, error) { return handler(ctx, envelope) }, fail)
}

func invoke(w http.ResponseWriter, request *http.Request, timeout time.Duration, correlationID string, handler func(context.Context) (any, error), fail func(int, *ContractFailure)) {
	ctx, cancel := context.WithTimeout(request.Context(), timeout)
	defer cancel()
	type result struct {
		value any
		err   error
	}
	done := make(chan result, 1)
	go func() { value, err := handler(ctx); done <- result{value, err} }()
	select {
	case <-ctx.Done():
		w.WriteHeader(http.StatusGatewayTimeout)
		fail(http.StatusGatewayTimeout, &ContractFailure{Type: "result_unknown", Message: "contract execution timed out"})
	case outcome := <-done:
		if outcome.err != nil {
			failure := &ContractFailure{Type: "permanent_external_failure", Message: "contract execution failed"}
			if errors.As(outcome.err, &failure) == false {
				failure = &ContractFailure{Type: "permanent_external_failure", Message: "contract execution failed"}
			}
			w.WriteHeader(contractFailureStatus(failure.Type))
			fail(contractFailureStatus(failure.Type), failure)
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": outcome.value, "correlation_id": correlationID})
	}
}

func decodeJSON(request *http.Request, target any) error {
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) == nil {
		return errors.New("multiple JSON values")
	}
	return nil
}

func validBearer(header, expected string) bool {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	actual := strings.TrimPrefix(header, prefix)
	return len(actual) == len(expected) && subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}

func contractError(correlationID string, failure *ContractFailure) foundation.ContractError {
	policy, known := foundation.ContractErrorRetryPolicy[failure.Type]
	if !known || (failure.Type == "rate_limited" && failure.RetryAfterMS <= 0) || (failure.Type != "rate_limited" && failure.RetryAfterMS != 0) {
		failure = &ContractFailure{Type: "permanent_external_failure", Message: "contract execution failed"}
		policy = foundation.ContractErrorRetryPolicy[failure.Type]
	}
	return foundation.ContractError{Type: failure.Type, Message: failure.Message, Retryable: policy != "never", RetryAfterMS: failure.RetryAfterMS, CorrelationID: correlationID, Details: failure.Details}
}

func contractFailureStatus(errorType string) int {
	switch errorType {
	case "authentication_failed":
		return http.StatusUnauthorized
	case "permission_denied":
		return http.StatusForbidden
	case "resource_not_found":
		return http.StatusNotFound
	case "validation_failed":
		return http.StatusBadRequest
	case "state_conflict":
		return http.StatusConflict
	case "rate_limited":
		return http.StatusTooManyRequests
	case "dependency_unavailable":
		return http.StatusServiceUnavailable
	case "result_unknown":
		return http.StatusGatewayTimeout
	default:
		return http.StatusInternalServerError
	}
}
