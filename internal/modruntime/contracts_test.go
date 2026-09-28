package modruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
)

func contractTestConfig() Config {
	return Config{ModuleID: "IAM", ContractVersion: "v1", AuthToken: "secret", AllowedCallers: map[string]bool{"TEN": true}, MaxTimeout: 50 * time.Millisecond, MaxRequestBytes: 4096}
}

func validQuery() foundation.QueryEnvelope {
	return foundation.QueryEnvelope{QueryType: "iam.identity.get.v1", AccountID: 1, Actor: foundation.ContractActor{Type: "System", ID: "ten", AuthSource: "module"}, Filter: map[string]any{"id": 7}, Sort: []string{"id"}, Page: "1", Fields: []string{"id"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
}

func validCommand() foundation.CommandEnvelope {
	return foundation.CommandEnvelope{CommandID: "command-1", CommandType: "iam.user.manage.v1", AccountID: 1, Actor: foundation.ContractActor{Type: "System", ID: "ten", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "user", ID: "7"}, Input: map[string]any{"name": "agent"}, IdempotencyKey: "idem-1", CorrelationID: "correlation-1", CausationID: "cause-1", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "iam.identity.get.v1"}}
}

func performContractRequest(handler http.Handler, path string, payload any, mutate func(*http.Request)) *httptest.ResponseRecorder {
	body, _ := json.Marshal(payload)
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set(headerCaller, "TEN")
	request.Header.Set(headerContractVersion, "v1")
	request.Header.Set(headerCorrelationID, "correlation-1")
	if mutate != nil {
		mutate(request)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestContractTransportAuthenticatesVersionsAndDispatchesOwnedCapability(t *testing.T) {
	registry := NewContractRegistry("IAM")
	if err := registry.RegisterQuery("ten.account.get.v1", func(context.Context, foundation.QueryEnvelope) (any, error) { return nil, nil }); err == nil {
		t.Fatal("foreign capability was registered")
	}
	if err := registry.RegisterQuery("iam.identity.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
		return map[string]any{"account_id": query.AccountID}, nil
	}); err != nil {
		t.Fatal(err)
	}
	handler := registry.Handler(contractTestConfig())
	response := performContractRequest(handler, "/contracts/queries/iam.identity.get.v1", validQuery(), nil)
	if response.Code != http.StatusOK || response.Header().Get(headerCorrelationID) != "correlation-1" {
		t.Fatalf("valid query status=%d body=%s", response.Code, response.Body.String())
	}
	for name, mutate := range map[string]func(*http.Request){
		"missing token":  func(request *http.Request) { request.Header.Del("Authorization") },
		"foreign caller": func(request *http.Request) { request.Header.Set(headerCaller, "CUS") },
		"wrong version":  func(request *http.Request) { request.Header.Set(headerContractVersion, "v2") },
	} {
		t.Run(name, func(t *testing.T) {
			response := performContractRequest(handler, "/contracts/queries/iam.identity.get.v1", validQuery(), mutate)
			if response.Code == http.StatusOK {
				t.Fatalf("invalid request accepted: %s", response.Body.String())
			}
			var failure foundation.ContractError
			if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil || failure.Validate() != nil {
				t.Fatalf("invalid error contract: %v body=%s", err, response.Body.String())
			}
		})
	}
}

func TestContractTransportValidatesEnvelopeAndBoundsTimeout(t *testing.T) {
	registry := NewContractRegistry("IAM")
	registry.UseCommandReceipts(newModuleTestDB(t, "IAM"))
	if err := registry.RegisterCommand("iam.user.manage.v1", func(ctx context.Context, _ foundation.CommandEnvelope) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	handler := registry.Handler(contractTestConfig())
	started := time.Now()
	response := performContractRequest(handler, "/contracts/commands/iam.user.manage.v1", validCommand(), nil)
	if response.Code != http.StatusGatewayTimeout || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("bounded timeout status=%d duration=%s body=%s", response.Code, time.Since(started), response.Body.String())
	}
	var timeoutFailure foundation.ContractError
	if err := json.Unmarshal(response.Body.Bytes(), &timeoutFailure); err != nil || timeoutFailure.Validate() != nil || timeoutFailure.Type != "result_unknown" {
		t.Fatalf("invalid timeout contract: %v body=%s", err, response.Body.String())
	}

	command := validCommand()
	command.CorrelationID = "different"
	response = performContractRequest(handler, "/contracts/commands/iam.user.manage.v1", command, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("correlation mismatch accepted: %d", response.Code)
	}
}

func TestContractTransportMapsStableFailure(t *testing.T) {
	registry := NewContractRegistry("IAM")
	if err := registry.RegisterQuery("iam.identity.get.v1", func(context.Context, foundation.QueryEnvelope) (any, error) {
		return nil, &ContractFailure{Type: "permission_denied", Message: "account scope denied"}
	}); err != nil {
		t.Fatal(err)
	}
	response := performContractRequest(registry.Handler(contractTestConfig()), "/contracts/queries/iam.identity.get.v1", validQuery(), nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("failure status=%d body=%s", response.Code, response.Body.String())
	}
	var failure foundation.ContractError
	if err := json.Unmarshal(response.Body.Bytes(), &failure); err != nil || failure.Validate() != nil || failure.Type != "permission_denied" {
		t.Fatalf("invalid failure contract: %v body=%s", err, response.Body.String())
	}
}
