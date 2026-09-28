package modruntime

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
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

func TestCHNInboxQueryRunsFromOwnerDatabaseOnly(t *testing.T) {
	db := newModuleTestDB(t, "CHN")
	inbox := domain.Inbox{AccountID: 91, Name: "Support", ChannelType: "Channel::WebWidget", WebsiteToken: "private-token", HMACToken: "private-hmac", ProviderConfig: `{"secret":"hidden"}`, GreetingMessage: "Hello", GreetingEnabled: true, Timezone: "Australia/Sydney"}
	if err := repository.NewInboxRepositoryWithAccountPort(db, nil).Create(&inbox); err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("CHN")
	runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CHN")}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	query := foundation.QueryEnvelope{QueryType: "chn.inbox.get.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "api", AuthSource: "module"}, Filter: map[string]any{"id": inbox.ID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "name", "channel_type"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	response := performContractRequest(runtime.Handler(), "/contracts/queries/chn.inbox.get.v1", query, func(request *http.Request) {
		request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
		request.Header.Set(headerCaller, "API")
	})
	if response.Code != http.StatusOK {
		t.Fatalf("inbox query status=%d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if !strings.Contains(body, "Support") || strings.Contains(body, "private-token") || strings.Contains(body, "private-hmac") || strings.Contains(body, "hidden") {
		t.Fatalf("inbox public view is unsafe or incomplete: %s", body)
	}
	for _, foreign := range []any{&domain.Account{}, &domain.User{}, &domain.Conversation{}} {
		if db.Migrator().HasTable(foreign) {
			t.Fatalf("CHN runtime created foreign table %T", foreign)
		}
	}

	query.AccountID = 92
	response = performContractRequest(runtime.Handler(), "/contracts/queries/chn.inbox.get.v1", query, func(request *http.Request) {
		request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
		request.Header.Set(headerCaller, "API")
	})
	if response.Code != http.StatusNotFound {
		t.Fatalf("cross-account inbox query status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCUSContactQueryRunsFromOwnerDatabaseOnly(t *testing.T) {
	db := newModuleTestDB(t, "CUS")
	contact := domain.Contact{AccountID: 91, Name: "Customer", Email: "customer@example.test", PhoneNumber: "+61000000000", Identifier: "customer-91", AvatarURL: "https://example.test/avatar.png", PubsubToken: "must-not-leak", CustomAttributes: `{"tier":"gold"}`, ObjectVersion: 3}
	if err := repository.NewContactRepositoryWithOwnerPorts(db, nil, nil, nil, nil, nil).Create(&contact); err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("CUS")
	runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CUS")}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	query := foundation.QueryEnvelope{QueryType: "cus.contact.get.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "api", AuthSource: "module"}, Filter: map[string]any{"id": contact.ID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "name", "email"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	call := func(accountID uint) *httptest.ResponseRecorder {
		query.AccountID = accountID
		return performContractRequest(runtime.Handler(), "/contracts/queries/cus.contact.get.v1", query, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
		})
	}
	response := call(91)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "customer@example.test") || strings.Contains(response.Body.String(), contact.PubsubToken) || strings.Contains(response.Body.String(), "gold") {
		t.Fatalf("contact query status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(92); response.Code != http.StatusNotFound {
		t.Fatalf("cross-account contact status=%d body=%s", response.Code, response.Body.String())
	}
	for _, foreign := range []any{&domain.Inbox{}, &domain.Conversation{}, &domain.Message{}} {
		if db.Migrator().HasTable(foreign) {
			t.Fatalf("CUS runtime created foreign table %T", foreign)
		}
	}
}

func TestCUSIdentityResolveUsesAccountScopedPriority(t *testing.T) {
	db := newModuleTestDB(t, "CUS")
	repo := repository.NewContactRepositoryWithOwnerPorts(db, nil, nil, nil, nil, nil)
	contacts := []domain.Contact{
		{AccountID: 91, Name: "Email Match", Email: "match@example.test", Identifier: "identifier-a", PhoneNumber: "+6101", PubsubToken: "secret-a"},
		{AccountID: 91, Name: "Identifier Match", Email: "other@example.test", Identifier: "identifier-b", PhoneNumber: "+6102"},
		{AccountID: 92, Name: "Foreign", Email: "match@example.test", Identifier: "identifier-b", PhoneNumber: "+6101"},
	}
	for i := range contacts {
		if err := repo.Create(&contacts[i]); err != nil {
			t.Fatal(err)
		}
	}
	cfg := businessConfig("CUS")
	runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CUS")}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	call := func(accountID uint, filter map[string]any) *httptest.ResponseRecorder {
		query := foundation.QueryEnvelope{QueryType: "cus.identity.resolve.v1", AccountID: accountID, Actor: foundation.ContractActor{Type: "System", ID: "api", AuthSource: "module"}, Filter: filter, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "name"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
		return performContractRequest(runtime.Handler(), "/contracts/queries/cus.identity.resolve.v1", query, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
		})
	}
	response := call(91, map[string]any{"email": "match@example.test", "identifier": "identifier-b"})
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Email Match") || strings.Contains(response.Body.String(), "secret-a") {
		t.Fatalf("priority resolve status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(92, map[string]any{"email": "other@example.test"}); response.Code != http.StatusNotFound {
		t.Fatalf("cross-account resolve status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(91, map[string]any{}); response.Code != http.StatusBadRequest {
		t.Fatalf("empty resolve status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCUSCompanyQueryUsesOwnerDatabaseAndSafeView(t *testing.T) {
	db := newModuleTestDB(t, "CUS")
	company := domain.Company{AccountID: 91, Name: "Acme", Domain: "acme.example", Industry: "Retail", Description: "Customer company", AvatarURL: "https://example.test/acme.png", CustomAttributes: `{"private":"value"}`, AdditionalAttributes: `{"internal":"value"}`}
	if err := repository.NewCompanyRepository(db).Create(context.Background(), &company); err != nil {
		t.Fatal(err)
	}
	if err := repository.NewContactRepositoryWithOwnerPorts(db, nil, nil, nil, nil, nil).Create(&domain.Contact{AccountID: 91, CompanyID: &company.ID, Name: "Member"}); err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("CUS")
	runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CUS")}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	query := foundation.QueryEnvelope{QueryType: "cus.company.get.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "api", AuthSource: "module"}, Filter: map[string]any{"id": company.ID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "name"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	call := func(accountID uint) *httptest.ResponseRecorder {
		query.AccountID = accountID
		return performContractRequest(runtime.Handler(), "/contracts/queries/cus.company.get.v1", query, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
		})
	}
	response := call(91)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Acme") || !strings.Contains(response.Body.String(), `"contacts_count":1`) || strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "internal") {
		t.Fatalf("company query status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(92); response.Code != http.StatusNotFound {
		t.Fatalf("cross-account company status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCUSCompanyCreateUpdateAreExplicitAndIdempotent(t *testing.T) {
	db := newModuleTestDB(t, "CUS")
	cfg := businessConfig("CUS")
	newRuntime := func() Runtime {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CUS")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	call := func(command foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), "/contracts/commands/cus.company.update.v1", command, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, command.CorrelationID)
		})
	}
	create := foundation.CommandEnvelope{CommandID: "company-create-1", CommandType: "cus.company.update.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "91"}, Input: map[string]any{"action": "create", "name": "Acme", "domain": "acme.example", "custom_attributes": map[string]any{"private": "stored"}}, IdempotencyKey: "company-create-91", CorrelationID: "company-correlation", CausationID: "company-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "cus.company.get.v1"}}
	for i := 0; i < 2; i++ {
		if response := call(create); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Acme") || strings.Contains(response.Body.String(), "private") {
			t.Fatalf("company create status=%d body=%s", response.Code, response.Body.String())
		}
	}
	var company domain.Company
	if err := db.First(&company).Error; err != nil {
		t.Fatal(err)
	}
	update := create
	update.CommandID, update.IdempotencyKey, update.Target = "company-update-1", "company-update-91", foundation.ContractTarget{Type: "company", ID: strconv.FormatUint(uint64(company.ID), 10)}
	update.Input = map[string]any{"action": "update", "name": "Acme Updated", "industry": "Retail"}
	if response := call(update); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Acme Updated") {
		t.Fatalf("company update status=%d body=%s", response.Code, response.Body.String())
	}
	var companies, receipts, events int64
	_ = db.Model(&domain.Company{}).Count(&companies).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%cus.company.changed.v1%").Count(&events).Error
	if companies != 1 || receipts != 2 || events != 2 {
		t.Fatalf("companies=%d receipts=%d events=%d", companies, receipts, events)
	}
	foreign := update
	foreign.CommandID, foreign.IdempotencyKey, foreign.AccountID = "company-update-foreign", "company-update-foreign", 92
	if response := call(foreign); response.Code != http.StatusNotFound {
		t.Fatalf("foreign company update status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCUSLabelQueryManageAreAccountScopedAndIdempotent(t *testing.T) {
	db := newModuleTestDB(t, "CUS")
	cfg := businessConfig("CUS")
	newRuntime := func() Runtime {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CUS")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	callCommand := func(command foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), "/contracts/commands/cus.label.manage.v1", command, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, command.CorrelationID)
		})
	}
	create := foundation.CommandEnvelope{CommandID: "label-create-1", CommandType: "cus.label.manage.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "91"}, Input: map[string]any{"action": "create", "title": "VIP", "color": "#ff0000", "show_on_sidebar": true}, IdempotencyKey: "label-create-91", CorrelationID: "label-correlation", CausationID: "label-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "cus.label.get.v1"}}
	for i := 0; i < 2; i++ {
		if response := callCommand(create); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "VIP") {
			t.Fatalf("label create status=%d body=%s", response.Code, response.Body.String())
		}
	}
	var label domain.Label
	if err := db.First(&label).Error; err != nil {
		t.Fatal(err)
	}
	update := create
	update.CommandID, update.IdempotencyKey = "label-update-1", "label-update-91"
	update.Target, update.Input = foundation.ContractTarget{Type: "label", ID: strconv.FormatUint(uint64(label.ID), 10)}, map[string]any{"action": "update", "title": "Priority", "description": "Important"}
	if response := callCommand(update); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Priority") {
		t.Fatalf("label update status=%d body=%s", response.Code, response.Body.String())
	}
	query := foundation.QueryEnvelope{QueryType: "cus.label.get.v1", AccountID: 91, Actor: create.Actor, Filter: map[string]any{"id": label.ID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "title"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	callQuery := func(accountID uint) *httptest.ResponseRecorder {
		query.AccountID = accountID
		return performContractRequest(newRuntime().Handler(), "/contracts/queries/cus.label.get.v1", query, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
		})
	}
	if response := callQuery(91); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Priority") {
		t.Fatalf("label query status=%d body=%s", response.Code, response.Body.String())
	}
	if response := callQuery(92); response.Code != http.StatusNotFound {
		t.Fatalf("cross-account label status=%d body=%s", response.Code, response.Body.String())
	}
	var labels, receipts, events int64
	_ = db.Model(&domain.Label{}).Count(&labels).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%cus.label.changed.v1%").Count(&events).Error
	if labels != 1 || receipts != 2 || events != 2 {
		t.Fatalf("labels=%d receipts=%d events=%d", labels, receipts, events)
	}
}

func TestCUSContactCreatePersistsReceiptAndEventAtomically(t *testing.T) {
	db := newModuleTestDB(t, "CUS")
	cfg := businessConfig("CUS")
	newRuntime := func() Runtime {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CUS")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	command := foundation.CommandEnvelope{CommandID: "contact-create-1", CommandType: "cus.contact.create.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "91"}, Input: map[string]any{"name": "Customer", "email": "customer@example.test", "phone_number": "+61000000000", "identifier": "customer-91", "avatar_url": "https://example.test/avatar.png", "pubsub_token": "ignored-secret", "custom_attributes": `{"ignored":true}`}, IdempotencyKey: "contact-create-91", CorrelationID: "contact-correlation", CausationID: "contact-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "cus.contact.get.v1"}}
	call := func(payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), "/contracts/commands/cus.contact.create.v1", payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, payload.CorrelationID)
		})
	}
	for i := 0; i < 2; i++ {
		response := call(command)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "customer@example.test") || strings.Contains(response.Body.String(), "ignored-secret") {
			t.Fatalf("contact create status=%d body=%s", response.Code, response.Body.String())
		}
	}
	var contacts, receipts, events int64
	_ = db.Model(&domain.Contact{}).Count(&contacts).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%cus.contact.created.v1%").Count(&events).Error
	var stored domain.Contact
	if err := db.First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if contacts != 1 || receipts != 1 || events != 1 || stored.PubsubToken != "" || stored.CustomAttributes != "" {
		t.Fatalf("contacts=%d receipts=%d events=%d stored=%+v", contacts, receipts, events, stored)
	}
	invalid := command
	invalid.CommandID, invalid.IdempotencyKey, invalid.Input = "contact-create-invalid", "contact-create-invalid-91", map[string]any{}
	if response := call(invalid); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid contact status=%d body=%s", response.Code, response.Body.String())
	}
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	if receipts != 1 {
		t.Fatalf("failed command left receipt count=%d", receipts)
	}
}

func TestCUSContactUpdateEnforcesOwnerAndWritableFields(t *testing.T) {
	db := newModuleTestDB(t, "CUS")
	contact := domain.Contact{AccountID: 91, Name: "Before", Email: "before@example.test", PubsubToken: "preserve-secret", CustomAttributes: `{"tier":"gold"}`, ObjectVersion: 2}
	if err := repository.NewContactRepositoryWithOwnerPorts(db, nil, nil, nil, nil, nil).Create(&contact); err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("CUS")
	newRuntime := func() Runtime {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CUS")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	command := foundation.CommandEnvelope{CommandID: "contact-update-1", CommandType: "cus.contact.update.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "contact", ID: strconv.FormatUint(uint64(contact.ID), 10)}, Input: map[string]any{"name": "After", "blocked": true}, IdempotencyKey: "contact-update-91", CorrelationID: "update-correlation", CausationID: "update-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "cus.contact.get.v1"}}
	call := func(payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), "/contracts/commands/cus.contact.update.v1", payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, payload.CorrelationID)
		})
	}
	for i := 0; i < 2; i++ {
		if response := call(command); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "After") {
			t.Fatalf("contact update status=%d body=%s", response.Code, response.Body.String())
		}
	}
	var stored domain.Contact
	if err := db.First(&stored, contact.ID).Error; err != nil {
		t.Fatal(err)
	}
	var receipts, events int64
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%cus.contact.changed.v1%").Count(&events).Error
	if stored.Name != "After" || !stored.Blocked || stored.ObjectVersion != 3 || stored.PubsubToken != "preserve-secret" || stored.CustomAttributes != `{"tier":"gold"}` || receipts != 1 || events != 1 {
		t.Fatalf("stored=%+v receipts=%d events=%d", stored, receipts, events)
	}
	foreign := command
	foreign.CommandID, foreign.IdempotencyKey, foreign.AccountID = "contact-update-foreign", "contact-update-foreign", 92
	if response := call(foreign); response.Code != http.StatusNotFound {
		t.Fatalf("foreign update status=%d body=%s", response.Code, response.Body.String())
	}
	unsafe := command
	unsafe.CommandID, unsafe.IdempotencyKey, unsafe.Input = "contact-update-unsafe", "contact-update-unsafe", map[string]any{"pubsub_token": "replace"}
	if response := call(unsafe); response.Code != http.StatusBadRequest {
		t.Fatalf("unsafe update status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCHNInboxCreateValidatesTENAndPersistsIdempotently(t *testing.T) {
	iamDB := newModuleTestDB(t, "IAM")
	iamCfg := businessConfig("IAM")
	iamRuntime := Runtime{Config: iamCfg, DB: iamDB, Contracts: NewContractRegistry("IAM")}
	if err := iamRuntime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	iamServer, iamAddress := startHTTPServer(t, "127.0.0.1:0", iamRuntime.Handler())
	defer iamServer.Shutdown(context.Background())

	tenDB := newModuleTestDB(t, "TEN")
	if err := tenDB.Create(&domain.Account{ID: 91, Name: "Tenant", Locale: "en", Status: "active", Timezone: "UTC"}).Error; err != nil {
		t.Fatal(err)
	}
	tenCfg := businessConfig("TEN")
	tenCfg.AllowedCallers["CHN"] = true
	tenCfg.Dependencies["IAM"] = DependencyConfig{URL: "http://" + iamAddress, Token: iamCfg.AuthToken}
	tenRuntime := Runtime{Config: tenCfg, DB: tenDB, Contracts: NewContractRegistry("TEN")}
	if err := tenRuntime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	tenServer, tenAddress := startHTTPServer(t, "127.0.0.1:0", tenRuntime.Handler())

	chnDB := newModuleTestDB(t, "CHN")
	chnCfg := businessConfig("CHN")
	chnCfg.Dependencies["TEN"] = DependencyConfig{URL: "http://" + tenAddress, Token: tenCfg.AuthToken}
	newCHNRuntime := func() Runtime {
		runtime := Runtime{Config: chnCfg, DB: chnDB, Contracts: NewContractRegistry("CHN")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	command := foundation.CommandEnvelope{CommandID: "create-inbox-1", CommandType: "chn.inbox.create.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "91"}, Input: map[string]any{"name": "Support", "channel_type": "Channel::WebWidget"}, IdempotencyKey: "create-inbox-91-support", CorrelationID: "correlation-1", CausationID: "account-91", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "chn.inbox.get.v1"}}
	call := func(runtime Runtime, payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(runtime.Handler(), "/contracts/commands/chn.inbox.create.v1", payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+chnCfg.AuthToken)
			request.Header.Set(headerCaller, "API")
		})
	}
	first := call(newCHNRuntime(), command)
	if first.Code != http.StatusOK {
		t.Fatalf("create status=%d body=%s", first.Code, first.Body.String())
	}
	replay := call(newCHNRuntime(), command)
	if replay.Code != http.StatusOK || !strings.Contains(replay.Body.String(), `"id":1`) {
		t.Fatalf("replay mismatch first=%s replay=%s", first.Body.String(), replay.Body.String())
	}
	var inboxes, receipts, events int64
	_ = chnDB.Model(&domain.Inbox{}).Where("account_id = ?", 91).Count(&inboxes).Error
	_ = chnDB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = chnDB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_outbox").Count(&events).Error
	if inboxes != 1 || receipts != 1 || events != 1 {
		t.Fatalf("inboxes=%d receipts=%d events=%d", inboxes, receipts, events)
	}

	if err := tenServer.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	missing := command
	missing.CommandID, missing.IdempotencyKey, missing.CorrelationID = "create-inbox-2", "create-inbox-92", "correlation-1"
	missing.AccountID, missing.Target.ID = 92, "92"
	failed := call(newCHNRuntime(), missing)
	if failed.Code != http.StatusServiceUnavailable {
		t.Fatalf("stopped TEN status=%d body=%s", failed.Code, failed.Body.String())
	}
	_ = chnDB.Model(&domain.Inbox{}).Count(&inboxes).Error
	if inboxes != 1 {
		t.Fatalf("failed dependency left inbox count=%d", inboxes)
	}
}

func TestCHNInboxUpdateBoundsFieldsAccountAndReplay(t *testing.T) {
	db := newModuleTestDB(t, "CHN")
	inbox := domain.Inbox{AccountID: 91, Name: "Before", ChannelType: "Channel::WebWidget", WebsiteToken: "keep-token", ProviderConfig: `{"secret":"keep"}`, Timezone: "UTC"}
	if err := repository.NewInboxRepositoryWithAccountPort(db, nil).Create(&inbox); err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("CHN")
	newRuntime := func() Runtime {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CHN")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	command := foundation.CommandEnvelope{CommandID: "update-inbox-1", CommandType: "chn.inbox.update.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "inbox", ID: strconv.FormatUint(uint64(inbox.ID), 10)}, Input: map[string]any{"name": "After", "timezone": "Australia/Sydney", "greeting_enabled": true}, IdempotencyKey: "update-inbox-91", CorrelationID: "correlation-1", CausationID: "create-inbox-1", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "chn.inbox.get.v1"}}
	call := func(payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), "/contracts/commands/chn.inbox.update.v1", payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
		})
	}
	if response := call(command); response.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(command); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Australia/Sydney") {
		t.Fatalf("replay status=%d body=%s", response.Code, response.Body.String())
	}
	var stored domain.Inbox
	if err := db.First(&stored, inbox.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Name != "After" || stored.WebsiteToken != "keep-token" || stored.ProviderConfig != `{"secret":"keep"}` {
		t.Fatalf("unexpected stored inbox: %+v", stored)
	}

	foreign := command
	foreign.CommandID, foreign.IdempotencyKey, foreign.AccountID = "update-inbox-foreign", "update-inbox-foreign", 92
	if response := call(foreign); response.Code != http.StatusNotFound {
		t.Fatalf("cross-account status=%d body=%s", response.Code, response.Body.String())
	}
	unsafe := command
	unsafe.CommandID, unsafe.IdempotencyKey, unsafe.Input = "update-inbox-unsafe", "update-inbox-unsafe", map[string]any{"provider_config": "replace"}
	if response := call(unsafe); response.Code != http.StatusBadRequest {
		t.Fatalf("unsafe update status=%d body=%s", response.Code, response.Body.String())
	}
	var receipts int64
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	var events int64
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_outbox").Count(&events).Error
	if receipts != 1 || events != 1 {
		t.Fatalf("failed updates left receipts=%d events=%d", receipts, events)
	}
}

func TestCHNMemberBindUsesTENMembershipAndRollsBackFailures(t *testing.T) {
	iamDB := newModuleTestDB(t, "IAM")
	user := domain.User{Name: "Agent", Email: "chn-member@example.test", PasswordHash: "secret", Role: domain.RoleAgent}
	if err := repository.NewUserRepository(iamDB).Create(&user); err != nil {
		t.Fatal(err)
	}
	iamCfg := businessConfig("IAM")
	iamRuntime := Runtime{Config: iamCfg, DB: iamDB, Contracts: NewContractRegistry("IAM")}
	if err := iamRuntime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	iamServer, iamAddress := startHTTPServer(t, "127.0.0.1:0", iamRuntime.Handler())
	defer iamServer.Shutdown(context.Background())

	tenDB := newModuleTestDB(t, "TEN")
	if err := tenDB.Create(&domain.Account{ID: 91, Name: "Tenant"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := tenDB.Create(&domain.AccountUser{AccountID: 91, UserID: user.ID, Role: domain.RoleAgent}).Error; err != nil {
		t.Fatal(err)
	}
	tenCfg := businessConfig("TEN")
	tenCfg.AllowedCallers["CHN"] = true
	tenCfg.Dependencies["IAM"] = DependencyConfig{URL: "http://" + iamAddress, Token: iamCfg.AuthToken}
	tenRuntime := Runtime{Config: tenCfg, DB: tenDB, Contracts: NewContractRegistry("TEN")}
	if err := tenRuntime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	tenServer, tenAddress := startHTTPServer(t, "127.0.0.1:0", tenRuntime.Handler())

	chnDB := newModuleTestDB(t, "CHN")
	inbox := domain.Inbox{AccountID: 91, Name: "Support", ChannelType: "Channel::WebWidget", WebsiteToken: "token"}
	if err := repository.NewInboxRepositoryWithAccountPort(chnDB, nil).Create(&inbox); err != nil {
		t.Fatal(err)
	}
	chnCfg := businessConfig("CHN")
	chnCfg.Dependencies["TEN"] = DependencyConfig{URL: "http://" + tenAddress, Token: tenCfg.AuthToken}
	newRuntime := func() Runtime {
		runtime := Runtime{Config: chnCfg, DB: chnDB, Contracts: NewContractRegistry("CHN")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	command := foundation.CommandEnvelope{CommandID: "bind-1", CommandType: "chn.member.bind.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "inbox", ID: strconv.FormatUint(uint64(inbox.ID), 10)}, Input: map[string]any{"user_id": user.ID, "operation": "bind"}, IdempotencyKey: "bind-91-1", CorrelationID: "correlation-1", CausationID: "inbox-1", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "chn.inbox.get.v1"}}
	call := func(payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), "/contracts/commands/chn.member.bind.v1", payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+chnCfg.AuthToken)
			request.Header.Set(headerCaller, "API")
		})
	}
	if response := call(command); response.Code != http.StatusOK {
		t.Fatalf("bind status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(command); response.Code != http.StatusOK {
		t.Fatalf("bind replay status=%d body=%s", response.Code, response.Body.String())
	}
	var bindings int64
	_ = chnDB.Model(&domain.InboxMember{}).Count(&bindings).Error
	if bindings != 1 {
		t.Fatalf("binding count=%d", bindings)
	}

	nonmember := command
	nonmember.CommandID, nonmember.IdempotencyKey, nonmember.Input = "bind-2", "bind-91-999", map[string]any{"user_id": 999, "operation": "bind"}
	if response := call(nonmember); response.Code != http.StatusNotFound {
		t.Fatalf("nonmember status=%d body=%s", response.Code, response.Body.String())
	}
	if err := tenServer.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	unavailable := command
	unavailable.CommandID, unavailable.IdempotencyKey = "bind-3", "bind-unavailable"
	if response := call(unavailable); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("TEN down status=%d body=%s", response.Code, response.Body.String())
	}

	// Restart TEN at the same address, then unbind through the same dependency configuration.
	tenServer, _ = startHTTPServer(t, tenAddress, tenRuntime.Handler())
	defer tenServer.Shutdown(context.Background())
	unbind := command
	unbind.CommandID, unbind.IdempotencyKey, unbind.Input = "unbind-1", "unbind-91-1", map[string]any{"user_id": user.ID, "operation": "unbind"}
	if response := call(unbind); response.Code != http.StatusOK {
		t.Fatalf("unbind status=%d body=%s", response.Code, response.Body.String())
	}
	_ = chnDB.Model(&domain.InboxMember{}).Count(&bindings).Error
	var receipts int64
	_ = chnDB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	var events int64
	_ = chnDB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_outbox").Count(&events).Error
	if bindings != 0 || receipts != 2 || events != 2 {
		t.Fatalf("bindings=%d receipts=%d events=%d", bindings, receipts, events)
	}
}

func TestCHNChannelAuthorizeUsesENTAndKeepsSecretsLocal(t *testing.T) {
	entDB := newModuleTestDB(t, "ENT")
	if err := repository.NewEnterpriseRepository(entDB).SetAccountFeature(91, "channel_authorization", true); err != nil {
		t.Fatal(err)
	}
	entCfg := businessConfig("ENT")
	entCfg.AllowedCallers["CHN"] = true
	entRuntime := Runtime{Config: entCfg, DB: entDB, Contracts: NewContractRegistry("ENT")}
	if err := entRuntime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	entServer, entAddress := startHTTPServer(t, "127.0.0.1:0", entRuntime.Handler())

	chnDB := newModuleTestDB(t, "CHN")
	inbox := domain.Inbox{AccountID: 91, Name: "WhatsApp", ChannelType: "Channel::Whatsapp", WebsiteToken: "token"}
	if err := repository.NewInboxRepositoryWithAccountPort(chnDB, nil).Create(&inbox); err != nil {
		t.Fatal(err)
	}
	chnCfg := businessConfig("CHN")
	chnCfg.Dependencies["TEN"] = DependencyConfig{URL: "http://127.0.0.1:1", Token: "unused"}
	chnCfg.Dependencies["ENT"] = DependencyConfig{URL: "http://" + entAddress, Token: entCfg.AuthToken}
	newRuntime := func() Runtime {
		runtime := Runtime{Config: chnCfg, DB: chnDB, Contracts: NewContractRegistry("CHN")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	command := foundation.CommandEnvelope{CommandID: "authorize-1", CommandType: "chn.channel.authorize.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "inbox", ID: strconv.FormatUint(uint64(inbox.ID), 10)}, Input: map[string]any{"provider_config": map[string]any{"access_token": "top-secret", "phone_number_id": "123"}, "webhook_url": "https://example.test/hooks", "hmac_mandatory": true}, IdempotencyKey: "authorize-91-1", CorrelationID: "correlation-1", CausationID: "inbox-1", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "chn.inbox.get.v1"}}
	call := func(payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), "/contracts/commands/chn.channel.authorize.v1", payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+chnCfg.AuthToken)
			request.Header.Set(headerCaller, "API")
		})
	}
	first := call(command)
	if first.Code != http.StatusOK || strings.Contains(first.Body.String(), "top-secret") {
		t.Fatalf("authorize status=%d body=%s", first.Code, first.Body.String())
	}
	if replay := call(command); replay.Code != http.StatusOK || strings.Contains(replay.Body.String(), "top-secret") {
		t.Fatalf("replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	var stored domain.Inbox
	if err := chnDB.First(&stored, inbox.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stored.ProviderConfig, "top-secret") || stored.WebhookURL != "https://example.test/hooks" || !stored.HMACMandatory {
		t.Fatalf("authorization was not persisted: %+v", stored)
	}

	disabled := command
	disabled.CommandID, disabled.IdempotencyKey, disabled.AccountID = "authorize-disabled", "authorize-disabled", 92
	disabled.Target.ID = strconv.FormatUint(uint64(inbox.ID), 10)
	if response := call(disabled); response.Code != http.StatusServiceUnavailable { // feature_unavailable maps to 503
		t.Fatalf("disabled feature status=%d body=%s", response.Code, response.Body.String())
	}
	if err := entServer.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	unavailable := command
	unavailable.CommandID, unavailable.IdempotencyKey = "authorize-down", "authorize-down"
	if response := call(unavailable); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("ENT down status=%d body=%s", response.Code, response.Body.String())
	}
	var receipts int64
	_ = chnDB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	var authorizationEvent domain.LocalChangeJournal
	eventErr := chnDB.Where("action = ?", "event_outbox").First(&authorizationEvent).Error
	if receipts != 1 || eventErr != nil || strings.Contains(authorizationEvent.AfterState, "top-secret") {
		t.Fatalf("authorization receipts=%d eventErr=%v payload=%s", receipts, eventErr, authorizationEvent.AfterState)
	}
}

func TestCHNHealthAndAccountRevokeStayWithinOwnerBoundary(t *testing.T) {
	db := newModuleTestDB(t, "CHN")
	repo := repository.NewInboxRepositoryWithAccountPort(db, nil)
	inboxes := []domain.Inbox{
		{AccountID: 91, Name: "One", ChannelType: "Channel::Whatsapp", WebsiteToken: "web-1", HMACToken: "hmac-1", HMACMandatory: true, WebhookURL: "https://one.test", ProviderConfig: `{"access_token":"secret-1"}`},
		{AccountID: 91, Name: "Two", ChannelType: "Channel::Api", WebsiteToken: "web-2", ProviderConfig: `{"api_key":"secret-2"}`},
		{AccountID: 92, Name: "Other", ChannelType: "Channel::Api", WebsiteToken: "keep-web", HMACToken: "keep-hmac", ProviderConfig: `{"api_key":"keep-secret"}`},
	}
	for i := range inboxes {
		if err := repo.Create(&inboxes[i]); err != nil {
			t.Fatal(err)
		}
		if err := repo.AddMember(inboxes[i].ID, uint(i+1)); err != nil {
			t.Fatal(err)
		}
	}
	cfg := businessConfig("CHN")
	newRuntime := func() Runtime {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CHN")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	call := func(path string, payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), path, payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
		})
	}
	query := func(capability string, accountID uint, filter map[string]any) *httptest.ResponseRecorder {
		envelope := foundation.QueryEnvelope{QueryType: capability, AccountID: accountID, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Filter: filter, Sort: []string{"id"}, Page: "1", Fields: []string{"status"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
		return performContractRequest(newRuntime().Handler(), "/contracts/queries/"+capability, envelope, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
		})
	}
	checkedAt := time.Now().UTC().Truncate(time.Second)
	health := foundation.CommandEnvelope{CommandID: "health-1", CommandType: "chn.health.update.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "inbox", ID: strconv.FormatUint(uint64(inboxes[0].ID), 10)}, Input: map[string]any{"status": "degraded", "checked_at": checkedAt.Format(time.RFC3339Nano), "details": map[string]any{"delivery_error_rate": 0.2}}, IdempotencyKey: "health-91-1", CorrelationID: "correlation-1", CausationID: "probe-1", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "chn.inbox.get.v1"}}
	if response := call("/contracts/commands/chn.health.update.v1", health); response.Code != http.StatusOK || strings.Contains(response.Body.String(), "secret-1") {
		t.Fatalf("health status=%d body=%s", response.Code, response.Body.String())
	}
	var afterHealth domain.Inbox
	if err := db.First(&afterHealth, inboxes[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(afterHealth.ProviderConfig, "secret-1") || !strings.Contains(afterHealth.ProviderConfig, `"status":"degraded"`) {
		t.Fatalf("health did not preserve credentials: %s", afterHealth.ProviderConfig)
	}
	if response := query("chn.connection.status.v1", 91, map[string]any{"id": inboxes[0].ID}); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"degraded"`) || strings.Contains(response.Body.String(), "secret-1") {
		t.Fatalf("connection status=%d body=%s", response.Code, response.Body.String())
	}
	if response := query("chn.capabilities.get.v1", 91, map[string]any{"id": inboxes[0].ID}); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "templates") || strings.Contains(response.Body.String(), "secret-1") {
		t.Fatalf("capabilities status=%d body=%s", response.Code, response.Body.String())
	}
	invalid := health
	invalid.CommandID, invalid.IdempotencyKey, invalid.Input = "health-invalid", "health-invalid", map[string]any{"status": "unknown", "checked_at": checkedAt.Format(time.RFC3339Nano)}
	if response := call("/contracts/commands/chn.health.update.v1", invalid); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid health status=%d body=%s", response.Code, response.Body.String())
	}

	revoke := foundation.CommandEnvelope{CommandID: "revoke-91", CommandType: "chn.account_channels.revoke.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "91"}, Input: map[string]any{"reason": "account_deletion"}, IdempotencyKey: "revoke-91", CorrelationID: "correlation-1", CausationID: "delete-91", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "chn.inbox.get.v1"}}
	if response := call("/contracts/commands/chn.account_channels.revoke.v1", revoke); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"revoked_inboxes":2`) {
		t.Fatalf("revoke status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call("/contracts/commands/chn.account_channels.revoke.v1", revoke); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"revoked_inboxes":2`) {
		t.Fatalf("revoke replay status=%d body=%s", response.Code, response.Body.String())
	}
	var account91 []domain.Inbox
	if err := db.Where("account_id = ?", 91).Find(&account91).Error; err != nil {
		t.Fatal(err)
	}
	for _, inbox := range account91 {
		if inbox.ProviderConfig != "" || !strings.HasPrefix(inbox.WebsiteToken, "revoked-") || inbox.HMACToken != "" || inbox.WebhookURL != "" || inbox.HMACMandatory {
			t.Fatalf("credential survived revoke: %+v", inbox)
		}
	}
	var other domain.Inbox
	if err := db.First(&other, inboxes[2].ID).Error; err != nil {
		t.Fatal(err)
	}
	if other.WebsiteToken != "keep-web" || !strings.Contains(other.ProviderConfig, "keep-secret") {
		t.Fatalf("other account was modified: %+v", other)
	}
	var members91, members92 int64
	ids91 := db.Model(&domain.Inbox{}).Select("id").Where("account_id = ?", 91)
	ids92 := db.Model(&domain.Inbox{}).Select("id").Where("account_id = ?", 92)
	_ = db.Model(&domain.InboxMember{}).Where("inbox_id IN (?)", ids91).Count(&members91).Error
	_ = db.Model(&domain.InboxMember{}).Where("inbox_id IN (?)", ids92).Count(&members92).Error
	var receipts int64
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	var events int64
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_outbox").Count(&events).Error
	if members91 != 0 || members92 != 1 || receipts != 2 || events != 2 {
		t.Fatalf("members91=%d members92=%d receipts=%d events=%d", members91, members92, receipts, events)
	}
	rptDB := newModuleTestDB(t, "RPT")
	rptRegistry := NewContractRegistry("RPT")
	var applied atomic.Int64
	for _, eventType := range []string{"chn.inbox.changed.v1", "chn.channel_health.changed.v1"} {
		if err := rptRegistry.RegisterEvent(eventType, func(context.Context, foundation.EventEnvelope) error { applied.Add(1); return nil }); err != nil {
			t.Fatal(err)
		}
	}
	rptCfg := businessConfig("RPT")
	rptCfg.AllowedCallers["CHN"] = true
	rptServer, rptAddress := startHTTPServer(t, "127.0.0.1:0", Runtime{Config: rptCfg, DB: rptDB, Contracts: rptRegistry}.Handler())
	defer rptServer.Shutdown(context.Background())
	dispatcher := NewEventDispatcher(db, "CHN", DependencyConfig{URL: "http://" + rptAddress, Token: rptCfg.AuthToken}, "v1", time.Second)
	if delivered, err := dispatcher.DeliverPending(context.Background(), 10); err != nil || delivered != 2 || applied.Load() != 2 {
		t.Fatalf("event delivery delivered=%d applied=%d err=%v", delivered, applied.Load(), err)
	}
	if delivered, err := dispatcher.DeliverPending(context.Background(), 10); err != nil || delivered != 0 {
		t.Fatalf("event redelivery delivered=%d err=%v", delivered, err)
	}
	if response := query("chn.account_deletion_status.get.v1", 91, map[string]any{}); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"credential_count":0`) || !strings.Contains(response.Body.String(), `"member_count":0`) || !strings.Contains(response.Body.String(), `"ready":true`) {
		t.Fatalf("deletion status=%d body=%s", response.Code, response.Body.String())
	}
	if response := query("chn.account_deletion_status.get.v1", 92, map[string]any{}); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ready":false`) {
		t.Fatalf("other deletion status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCHNTemplateManageListAndEventsAreOwnerLocal(t *testing.T) {
	db := newModuleTestDB(t, "CHN")
	inbox := domain.Inbox{AccountID: 91, Name: "WhatsApp", ChannelType: "Channel::Whatsapp", WebsiteToken: "token"}
	if err := repository.NewInboxRepositoryWithAccountPort(db, nil).Create(&inbox); err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("CHN")
	newRuntime := func() Runtime {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CHN")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	call := func(payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), "/contracts/commands/chn.template.manage.v1", payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
		})
	}
	command := foundation.CommandEnvelope{CommandID: "template-upsert-1", CommandType: "chn.template.manage.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "inbox", ID: strconv.FormatUint(uint64(inbox.ID), 10)}, Input: map[string]any{"action": "upsert", "name": "order_ready", "status": "APPROVED", "category": "UTILITY", "language": "en_US", "components": `[{"type":"BODY","text":"Order {{1}} is ready"}]`}, IdempotencyKey: "template-upsert-91", CorrelationID: "correlation-1", CausationID: "inbox-1", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "chn.templates.list.v1"}}
	first := call(command)
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), "order_ready") {
		t.Fatalf("template upsert status=%d body=%s", first.Code, first.Body.String())
	}
	if replay := call(command); replay.Code != http.StatusOK {
		t.Fatalf("template replay status=%d body=%s", replay.Code, replay.Body.String())
	}
	var template domain.InboxMessageTemplate
	if err := db.Where("account_id = ? AND inbox_id = ? AND name = ?", 91, inbox.ID, "order_ready").First(&template).Error; err != nil {
		t.Fatal(err)
	}
	queryEnvelope := foundation.QueryEnvelope{QueryType: "chn.templates.list.v1", AccountID: 91, Actor: command.Actor, Filter: map[string]any{"inbox_id": inbox.ID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "name"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	query := func(accountID uint) *httptest.ResponseRecorder {
		queryEnvelope.AccountID = accountID
		return performContractRequest(newRuntime().Handler(), "/contracts/queries/chn.templates.list.v1", queryEnvelope, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
		})
	}
	if response := query(91); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "order_ready") {
		t.Fatalf("template list status=%d body=%s", response.Code, response.Body.String())
	}
	if response := query(92); response.Code != http.StatusNotFound {
		t.Fatalf("cross-account list status=%d body=%s", response.Code, response.Body.String())
	}
	deleteCommand := command
	deleteCommand.CommandID, deleteCommand.IdempotencyKey, deleteCommand.Input = "template-delete-1", "template-delete-91", map[string]any{"action": "delete", "template_id": template.ID}
	if response := call(deleteCommand); response.Code != http.StatusOK {
		t.Fatalf("template delete status=%d body=%s", response.Code, response.Body.String())
	}
	var templates, receipts int64
	_ = db.Model(&domain.InboxMessageTemplate{}).Count(&templates).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	var events []domain.LocalChangeJournal
	if err := db.Where("action = ? AND event_id <> ''", "event_outbox").Order("object_version_after ASC").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if templates != 0 || receipts != 2 || len(events) != 2 || events[0].ObjectVersionAfter != 1 || events[1].ObjectVersionAfter != 2 {
		t.Fatalf("templates=%d receipts=%d events=%+v", templates, receipts, events)
	}
	for _, event := range events {
		if strings.Contains(event.AfterState, "access_token") {
			t.Fatalf("template event leaked credential: %s", event.AfterState)
		}
	}
}

func TestCHNTemplateCommandAllowsOneWriteAcrossPostgresInstances(t *testing.T) {
	db := postgresModuleDB(t, "CHN")
	inbox := domain.Inbox{AccountID: 91, Name: "WhatsApp", ChannelType: "Channel::Whatsapp", WebsiteToken: "capacity-token"}
	if err := repository.NewInboxRepositoryWithAccountPort(db, nil).Create(&inbox); err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("CHN")
	command := foundation.CommandEnvelope{CommandID: "capacity-template", CommandType: "chn.template.manage.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "inbox", ID: strconv.FormatUint(uint64(inbox.ID), 10)}, Input: map[string]any{"action": "upsert", "name": "capacity_once", "components": `[{"type":"BODY","text":"Created once"}]`}, IdempotencyKey: "capacity-template-91", CorrelationID: "capacity-correlation", CausationID: "capacity-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "chn.templates.list.v1"}}

	const instances = 8
	type result struct {
		status int
		body   string
	}
	results := make(chan result, instances)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for i := 0; i < instances; i++ {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CHN")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		wait.Add(1)
		go func(handler http.Handler) {
			defer wait.Done()
			<-start
			response := performContractRequest(handler, "/contracts/commands/chn.template.manage.v1", command, func(request *http.Request) {
				request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
				request.Header.Set(headerCaller, "API")
				request.Header.Set(headerCorrelationID, command.CorrelationID)
			})
			results <- result{status: response.Code, body: response.Body.String()}
		}(runtime.Handler())
	}
	close(start)
	wait.Wait()
	close(results)
	for response := range results {
		if response.status != http.StatusOK || !strings.Contains(response.body, "capacity_once") {
			t.Fatalf("concurrent template status=%d body=%s", response.status, response.body)
		}
	}
	var templates, receipts, events int64
	if err := db.Model(&domain.InboxMessageTemplate{}).Where("account_id = ? AND inbox_id = ? AND name = ?", 91, inbox.ID, "capacity_once").Count(&templates).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%chn.template.changed.v1%").Count(&events).Error; err != nil {
		t.Fatal(err)
	}
	if templates != 1 || receipts != 1 || events != 1 {
		t.Fatalf("templates=%d receipts=%d events=%d", templates, receipts, events)
	}
}

func TestCHNMixedVersionUpgradeRollbackPreservesTemplateFact(t *testing.T) {
	db := newModuleTestDB(t, "CHN")
	inbox := domain.Inbox{AccountID: 91, Name: "WhatsApp", ChannelType: "Channel::Whatsapp", WebsiteToken: "upgrade-token"}
	if err := repository.NewInboxRepositoryWithAccountPort(db, nil).Create(&inbox); err != nil {
		t.Fatal(err)
	}
	newRuntime := func(version, release string, accepted map[string]bool) Runtime {
		cfg := businessConfig("CHN")
		cfg.ContractVersion = version
		cfg.ReleaseVersion = release
		cfg.AcceptedVersions = accepted
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CHN")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	call := func(runtime Runtime, version, path string, payload any) *httptest.ResponseRecorder {
		return performContractRequest(runtime.Handler(), path, payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+runtime.Config.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerContractVersion, version)
			request.Header.Set(headerCorrelationID, "upgrade-correlation")
		})
	}
	command := foundation.CommandEnvelope{CommandID: "upgrade-template", CommandType: "chn.template.manage.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "inbox", ID: strconv.FormatUint(uint64(inbox.ID), 10)}, Input: map[string]any{"action": "upsert", "name": "survives_rollback", "components": `[{"type":"BODY","text":"Still readable"}]`}, IdempotencyKey: "upgrade-template-91", CorrelationID: "upgrade-correlation", CausationID: "upgrade-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "chn.templates.list.v1"}}
	v2 := newRuntime("v2", "release-2", map[string]bool{"v1": true, "v2": true})
	if response := call(v2, "v2", "/contracts/commands/chn.template.manage.v1", command); response.Code != http.StatusOK {
		t.Fatalf("v2 write status=%d body=%s", response.Code, response.Body.String())
	}
	v1 := newRuntime("v1", "release-1", map[string]bool{"v1": true})
	if response := call(v1, "v1", "/contracts/commands/chn.template.manage.v1", command); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "survives_rollback") {
		t.Fatalf("v1 replay status=%d body=%s", response.Code, response.Body.String())
	}
	query := foundation.QueryEnvelope{QueryType: "chn.templates.list.v1", AccountID: 91, Actor: command.Actor, Filter: map[string]any{"inbox_id": inbox.ID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "name"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	if response := call(v1, "v1", "/contracts/queries/chn.templates.list.v1", query); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "survives_rollback") {
		t.Fatalf("v1 read status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(v1, "v2", "/contracts/queries/chn.templates.list.v1", query); response.Code != http.StatusBadRequest {
		t.Fatalf("rolled-back runtime accepted v2 status=%d body=%s", response.Code, response.Body.String())
	}
	var templates, receipts, events int64
	_ = db.Model(&domain.InboxMessageTemplate{}).Count(&templates).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%chn.template.changed.v1%").Count(&events).Error
	if templates != 1 || receipts != 1 || events != 1 {
		t.Fatalf("rollback templates=%d receipts=%d events=%d", templates, receipts, events)
	}
}

func TestCHNIndependentRuntimeRegistersCompleteDeclaredSurface(t *testing.T) {
	db := newModuleTestDB(t, "CHN")
	cfg := businessConfig("CHN")
	cfg.Dependencies["TEN"] = DependencyConfig{URL: "http://127.0.0.1:1", Token: "ten"}
	cfg.Dependencies["ENT"] = DependencyConfig{URL: "http://127.0.0.1:2", Token: "ent"}
	registry := NewContractRegistry("CHN")
	runtime := Runtime{Config: cfg, DB: db, Contracts: registry}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	// module_contracts.json declares seven commands and five queries for CHN.
	if registry.Count() != 12 {
		t.Fatalf("CHN registered contracts=%d want=12", registry.Count())
	}
	if err := runtime.Readiness(); err != nil {
		t.Fatalf("CHN runtime readiness: %v", err)
	}
	for _, foreign := range []any{&domain.Account{}, &domain.AccountFeature{}, &domain.User{}, &domain.Conversation{}, &domain.Message{}} {
		if db.Migrator().HasTable(foreign) {
			t.Fatalf("CHN runtime contains foreign table %T", foreign)
		}
	}
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
