package modruntime

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"gorm.io/gorm"
)

func businessConfig(module string) Config {
	return Config{ModuleID: module, ContractVersion: "v1", AcceptedVersions: map[string]bool{"v1": true}, AuthToken: strings.ToLower(module) + "-secret", AllowedCallers: map[string]bool{"IAM": true, "TEN": true, "API": true}, MaxTimeout: 150 * time.Millisecond, MaxRequestBytes: 4096, Dependencies: map[string]DependencyConfig{}}
}

func startHTTPServer(t *testing.T, address string, handler http.Handler) (*http.Server, string) {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	return server, listener.Addr().String()
}

func tenMembersQuery() foundation.QueryEnvelope {
	return foundation.QueryEnvelope{QueryType: "ten.members.list.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "api", AuthSource: "module"}, Filter: map[string]any{}, Sort: []string{"user_id"}, Page: "1", Fields: []string{"user_id", "role", "identity"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
}

func TestTENMembersContractUsesIndependentIAMRuntimeAndRecovers(t *testing.T) {
	iamDB := newModuleTestDB(t, "IAM")
	user := domain.User{Name: "Agent", DisplayName: "Agent One", Email: "agent@example.test", PasswordHash: "must-not-leak", Role: domain.RoleAgent, Timezone: "Australia/Sydney"}
	if err := repository.NewUserRepository(iamDB).Create(&user); err != nil {
		t.Fatal(err)
	}
	secondUser := domain.User{Name: "Agent Two", Email: "agent2@example.test", PasswordHash: "must-not-leak", Role: domain.RoleAgent}
	if err := repository.NewUserRepository(iamDB).Create(&secondUser); err != nil {
		t.Fatal(err)
	}
	iamConfig := businessConfig("IAM")
	iamRuntime := Runtime{Config: iamConfig, DB: iamDB, Contracts: NewContractRegistry("IAM")}
	if err := iamRuntime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	var iamCalls atomic.Int64
	iamHandler := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if strings.Contains(request.URL.Path, "iam.identity.get.v1") {
			iamCalls.Add(1)
		}
		iamRuntime.Handler().ServeHTTP(w, request)
	})
	iamServer, address := startHTTPServer(t, "127.0.0.1:0", iamHandler)

	tenDB := newModuleTestDB(t, "TEN")
	if err := tenDB.Create(&domain.Account{ID: 91, Name: "Tenant"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := tenDB.Create(&domain.AccountUser{AccountID: 91, UserID: user.ID, Role: domain.RoleAgent, Availability: domain.AvailabilityOnline}).Error; err != nil {
		t.Fatal(err)
	}
	if err := tenDB.Create(&domain.AccountUser{AccountID: 91, UserID: secondUser.ID, Role: domain.RoleAgent}).Error; err != nil {
		t.Fatal(err)
	}
	tenConfig := businessConfig("TEN")
	tenConfig.Dependencies["IAM"] = DependencyConfig{URL: "http://" + address, Token: iamConfig.AuthToken}
	tenRuntime := Runtime{Config: tenConfig, DB: tenDB, Contracts: NewContractRegistry("TEN")}
	if err := tenRuntime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}

	assertMembers := func(expectedStatus int) string {
		response := performContractRequest(tenRuntime.Handler(), "/contracts/queries/ten.members.list.v1", tenMembersQuery(), func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+tenConfig.AuthToken)
			request.Header.Set(headerCaller, "API")
		})
		if response.Code != expectedStatus {
			t.Fatalf("members status=%d want=%d body=%s", response.Code, expectedStatus, response.Body.String())
		}
		return response.Body.String()
	}
	body := assertMembers(http.StatusOK)
	if strings.Contains(body, user.PasswordHash) || !strings.Contains(body, user.Email) {
		t.Fatalf("identity projection is unsafe or incomplete: %s", body)
	}
	if iamCalls.Load() != 1 {
		t.Fatalf("member list used %d IAM calls, want one batch", iamCalls.Load())
	}

	if err := iamServer.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	failureBody := assertMembers(http.StatusServiceUnavailable)
	var failure foundation.ContractError
	if err := json.Unmarshal([]byte(failureBody), &failure); err != nil || failure.Type != "dependency_unavailable" {
		t.Fatalf("invalid dependency failure: %v body=%s", err, failureBody)
	}

	iamServer, _ = startHTTPServer(t, address, iamHandler)
	defer iamServer.Shutdown(context.Background())
	assertMembers(http.StatusOK)
}

func TestTENMembersContractBoundsIAMTimeout(t *testing.T) {
	slowRegistry := NewContractRegistry("IAM")
	if err := slowRegistry.RegisterQuery("iam.identity.get.v1", func(ctx context.Context, _ foundation.QueryEnvelope) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	iamConfig := businessConfig("IAM")
	iamServer, address := startHTTPServer(t, "127.0.0.1:0", Runtime{Config: iamConfig, Contracts: slowRegistry}.Handler())
	defer iamServer.Shutdown(context.Background())

	tenDB := newModuleTestDB(t, "TEN")
	if err := tenDB.Create(&domain.Account{ID: 91, Name: "Tenant"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := tenDB.Create(&domain.AccountUser{AccountID: 91, UserID: 7, Role: domain.RoleAgent}).Error; err != nil {
		t.Fatal(err)
	}
	tenConfig := businessConfig("TEN")
	tenConfig.MaxTimeout = 40 * time.Millisecond
	tenConfig.Dependencies["IAM"] = DependencyConfig{URL: "http://" + address, Token: iamConfig.AuthToken}
	tenRuntime := Runtime{Config: tenConfig, DB: tenDB, Contracts: NewContractRegistry("TEN")}
	if err := tenRuntime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	response := performContractRequest(tenRuntime.Handler(), "/contracts/queries/ten.members.list.v1", tenMembersQuery(), func(request *http.Request) {
		request.Header.Set("Authorization", "Bearer "+tenConfig.AuthToken)
		request.Header.Set(headerCaller, "API")
	})
	if response.Code != http.StatusGatewayTimeout && response.Code != http.StatusServiceUnavailable {
		t.Fatalf("timeout status=%d body=%s", response.Code, response.Body.String())
	}
	if time.Since(started) > 500*time.Millisecond {
		t.Fatalf("dependency timeout was not bounded: %s", time.Since(started))
	}
}

func TestTENMembershipCommandPersistsIdempotencyAcrossRuntimeRestart(t *testing.T) {
	iamDB := newModuleTestDB(t, "IAM")
	user := domain.User{Name: "Agent", Email: "command-agent@example.test", PasswordHash: "secret", Role: domain.RoleAgent}
	if err := repository.NewUserRepository(iamDB).Create(&user); err != nil {
		t.Fatal(err)
	}
	iamConfig := businessConfig("IAM")
	iamRuntime := Runtime{Config: iamConfig, DB: iamDB, Contracts: NewContractRegistry("IAM")}
	if err := iamRuntime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	iamServer, address := startHTTPServer(t, "127.0.0.1:0", iamRuntime.Handler())
	defer iamServer.Shutdown(context.Background())

	tenDB := newModuleTestDB(t, "TEN")
	if err := tenDB.Create(&domain.Account{ID: 91, Name: "Tenant"}).Error; err != nil {
		t.Fatal(err)
	}
	tenConfig := businessConfig("TEN")
	tenConfig.Dependencies["IAM"] = DependencyConfig{URL: "http://" + address, Token: iamConfig.AuthToken}
	newTENRuntime := func() Runtime {
		runtime := Runtime{Config: tenConfig, DB: tenDB, Contracts: NewContractRegistry("TEN")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	command := foundation.CommandEnvelope{CommandID: "command-add-1", CommandType: "ten.membership.add.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "91"}, Input: map[string]any{"user_id": user.ID, "role": domain.RoleAgent}, IdempotencyKey: "membership-91-1", CorrelationID: "correlation-1", CausationID: "cause-1", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "ten.members.list.v1"}}
	call := func(runtime Runtime, payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(runtime.Handler(), "/contracts/commands/ten.membership.add.v1", payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+tenConfig.AuthToken)
			request.Header.Set(headerCaller, "API")
		})
	}
	first := call(newTENRuntime(), command)
	if first.Code != http.StatusOK {
		t.Fatalf("first command status=%d body=%s", first.Code, first.Body.String())
	}
	second := call(newTENRuntime(), command)
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), user.Email) {
		t.Fatalf("restart replay mismatch first=%s second=%s", first.Body.String(), second.Body.String())
	}
	var count int64
	if err := tenDB.Model(&domain.AccountUser{}).Where("account_id = ? AND user_id = ?", 91, user.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("membership count=%d err=%v", count, err)
	}
	command.Input["role"] = domain.RoleAdministrator
	conflict := call(newTENRuntime(), command)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("changed replay status=%d body=%s", conflict.Code, conflict.Body.String())
	}
}

func TestMixedVersionUpgradeAndRollbackPreserveTENFact(t *testing.T) {
	iamDB := newModuleTestDB(t, "IAM")
	user := domain.User{Name: "Upgrade Agent", Email: "upgrade@example.test", PasswordHash: "secret", Role: domain.RoleAgent}
	if err := repository.NewUserRepository(iamDB).Create(&user); err != nil {
		t.Fatal(err)
	}
	iamConfig := businessConfig("IAM")
	iamConfig.ContractVersion = "v2"
	iamConfig.AcceptedVersions = map[string]bool{"v1": true, "v2": true}
	iamConfig.ReleaseVersion = "release-2"
	iamRuntime := Runtime{Config: iamConfig, DB: iamDB, Contracts: NewContractRegistry("IAM")}
	if err := iamRuntime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	server, address := startHTTPServer(t, "127.0.0.1:0", iamRuntime.Handler())
	defer server.Shutdown(context.Background())
	tenDB := newModuleTestDB(t, "TEN")
	if err := tenDB.Create(&domain.Account{ID: 92, Name: "Upgrade Tenant"}).Error; err != nil {
		t.Fatal(err)
	}
	makeRuntime := func(version, release string, accepted map[string]bool) Runtime {
		cfg := businessConfig("TEN")
		cfg.ContractVersion = version
		cfg.AcceptedVersions = accepted
		cfg.ReleaseVersion = release
		cfg.Dependencies["IAM"] = DependencyConfig{URL: "http://" + address, Token: iamConfig.AuthToken}
		runtime := Runtime{Config: cfg, DB: tenDB, Contracts: NewContractRegistry("TEN")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	v2 := makeRuntime("v2", "release-2", map[string]bool{"v1": true, "v2": true})
	command := foundation.CommandEnvelope{CommandID: "upgrade-command", CommandType: "ten.membership.add.v1", AccountID: 92, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "92"}, Input: map[string]any{"user_id": user.ID}, IdempotencyKey: "upgrade-idempotency", CorrelationID: "correlation-1", CausationID: "cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "ten.members.list.v1"}}
	call := func(runtime Runtime, version string) *httptest.ResponseRecorder {
		return performContractRequest(runtime.Handler(), "/contracts/commands/ten.membership.add.v1", command, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer ten-secret")
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerContractVersion, version)
		})
	}
	if response := call(v2, "v2"); response.Code != http.StatusOK {
		t.Fatalf("v2 command status=%d body=%s", response.Code, response.Body.String())
	}
	v1 := makeRuntime("v1", "release-1", map[string]bool{"v1": true})
	if response := call(v1, "v1"); response.Code != http.StatusOK {
		t.Fatalf("rollback replay status=%d body=%s", response.Code, response.Body.String())
	}
	var count int64
	if err := tenDB.Model(&domain.AccountUser{}).Where("account_id = ? AND user_id = ?", 92, user.ID).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("rollback fact count=%d err=%v", count, err)
	}
}

func newModuleTestDB(t *testing.T, module string) *gorm.DB {
	t.Helper()
	cfg := businessConfig(module)
	cfg.DBDriver = "sqlite"
	cfg.DBPath = t.TempDir() + "/" + strings.ToLower(module) + ".db"
	db, err := OpenDatabase(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.AutoMigrateModule(db, module); err != nil {
		t.Fatal(err)
	}
	return db
}
