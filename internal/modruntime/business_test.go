package modruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
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

func TestMEDUploadCreateAndAssetQueryOwnMetadata(t *testing.T) {
	db := newModuleTestDB(t, "MED")
	cfg := businessConfig("MED")
	newRuntime := func() Runtime {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("MED")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	command := foundation.CommandEnvelope{CommandID: "media-upload-1", CommandType: "med.upload.create.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "91"}, Input: map[string]any{"file_name": "invoice.pdf", "content_type": "application/pdf", "file_size": 4096, "checksum": "sha256:abc"}, IdempotencyKey: "media-upload-91", CorrelationID: "media-correlation", CausationID: "media-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "med.asset.get.v1"}}
	callCommand := func(payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), "/contracts/commands/med.upload.create.v1", payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, payload.CorrelationID)
		})
	}
	var firstBody string
	var firstEnvelope map[string]any
	for i := 0; i < 2; i++ {
		response := callCommand(command)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "invoice.pdf") || !strings.Contains(response.Body.String(), "upload_token") {
			t.Fatalf("upload create status=%d body=%s", response.Code, response.Body.String())
		}
		if i == 0 {
			firstBody = response.Body.String()
			if err := json.Unmarshal(response.Body.Bytes(), &firstEnvelope); err != nil {
				t.Fatal(err)
			}
		} else {
			var replayEnvelope map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &replayEnvelope); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(firstEnvelope["data"], replayEnvelope["data"]) {
				t.Fatalf("upload replay changed response first=%s second=%s", firstBody, response.Body.String())
			}
		}
	}
	var asset domain.MediaAsset
	if err := db.Where("account_id = ?", 91).First(&asset).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(firstBody, asset.StorageKey) || strings.Contains(firstBody, "token_hash") {
		t.Fatalf("upload response leaked private metadata: %s", firstBody)
	}
	query := foundation.QueryEnvelope{QueryType: "med.asset.get.v1", AccountID: 91, Actor: command.Actor, Filter: map[string]any{"id": asset.ID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "status"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	callQuery := func(accountID uint) *httptest.ResponseRecorder {
		query.AccountID = accountID
		return performContractRequest(newRuntime().Handler(), "/contracts/queries/med.asset.get.v1", query, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
		})
	}
	if response := callQuery(91); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "pending_upload") || strings.Contains(response.Body.String(), asset.StorageKey) {
		t.Fatalf("asset query status=%d body=%s", response.Code, response.Body.String())
	}
	if response := callQuery(92); response.Code != http.StatusNotFound {
		t.Fatalf("cross-account asset query status=%d body=%s", response.Code, response.Body.String())
	}
	var assets, sessions, receipts int64
	_ = db.Model(&domain.MediaAsset{}).Count(&assets).Error
	_ = db.Model(&domain.UploadSession{}).Count(&sessions).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	if assets != 1 || sessions != 1 || receipts != 1 {
		t.Fatalf("assets=%d sessions=%d receipts=%d", assets, sessions, receipts)
	}
	for _, foreign := range []any{&domain.Account{}, &domain.Attachment{}, &domain.Message{}} {
		if db.Migrator().HasTable(foreign) {
			t.Fatalf("MED runtime contains foreign table %T", foreign)
		}
	}
}

func TestMEDConfirmAndScanEnforceOwnerStateMachine(t *testing.T) {
	db := newModuleTestDB(t, "MED")
	now := time.Now().UTC()
	token := "one-time-upload-token"
	digest := sha256.Sum256([]byte(token))
	cleanAsset := domain.MediaAsset{AccountID: 91, FileName: "clean.pdf", ContentType: "application/pdf", FileSize: 512, Checksum: "sha256:clean", StorageKey: "91/private-clean", Status: "pending_upload", SecurityState: "pending", ObjectVersion: 1, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&cleanAsset).Error; err != nil {
		t.Fatal(err)
	}
	upload := domain.UploadSession{AccountID: 91, AssetID: cleanAsset.ID, TokenHash: hex.EncodeToString(digest[:]), Status: "pending", ExpiresAt: now.Add(time.Minute), CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&upload).Error; err != nil {
		t.Fatal(err)
	}
	rejectedAsset := domain.MediaAsset{AccountID: 91, FileName: "bad.php", ContentType: "application/octet-stream", FileSize: 128, StorageKey: "91/private-bad", Status: "pending_scan", SecurityState: "pending", ObjectVersion: 2, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&rejectedAsset).Error; err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("MED")
	newRuntime := func() Runtime {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("MED")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	call := func(path string, payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), path, payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, payload.CorrelationID)
		})
	}
	confirm := foundation.CommandEnvelope{CommandID: "media-confirm-1", CommandType: "med.asset.confirm.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "asset", ID: strconv.FormatUint(uint64(cleanAsset.ID), 10)}, Input: map[string]any{"upload_id": upload.ID, "upload_token": token, "checksum": cleanAsset.Checksum}, IdempotencyKey: "media-confirm-91", CorrelationID: "media-confirm-correlation", CausationID: "media-upload-1", RequestedAt: now, TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "med.asset.get.v1"}}
	for i := 0; i < 2; i++ {
		if response := call("/contracts/commands/med.asset.confirm.v1", confirm); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "pending_scan") {
			t.Fatalf("confirm status=%d body=%s", response.Code, response.Body.String())
		}
	}
	scanClean := foundation.CommandEnvelope{CommandID: "media-scan-clean", CommandType: "med.asset.scan.v1", AccountID: 91, Actor: confirm.Actor, Target: confirm.Target, Input: map[string]any{"result": "clean"}, IdempotencyKey: "media-scan-clean-91", CorrelationID: "media-scan-clean-correlation", CausationID: confirm.CommandID, RequestedAt: now, TimeoutPolicy: confirm.TimeoutPolicy}
	for i := 0; i < 2; i++ {
		if response := call("/contracts/commands/med.asset.scan.v1", scanClean); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"ready"`) {
			t.Fatalf("clean scan status=%d body=%s", response.Code, response.Body.String())
		}
	}
	scanRejected := scanClean
	scanRejected.CommandID, scanRejected.IdempotencyKey, scanRejected.CorrelationID = "media-scan-rejected", "media-scan-rejected-91", "media-scan-rejected-correlation"
	scanRejected.Target.ID = strconv.FormatUint(uint64(rejectedAsset.ID), 10)
	scanRejected.Input = map[string]any{"result": "rejected", "threat": "webshell"}
	if response := call("/contracts/commands/med.asset.scan.v1", scanRejected); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"rejected"`) {
		t.Fatalf("rejected scan status=%d body=%s", response.Code, response.Body.String())
	}
	var storedClean, storedRejected domain.MediaAsset
	_ = db.First(&storedClean, cleanAsset.ID).Error
	_ = db.First(&storedRejected, rejectedAsset.ID).Error
	var scans, receipts, readyEvents, rejectedEvents int64
	_ = db.Model(&domain.SecurityScan{}).Count(&scans).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%med.media.ready.v1%").Count(&readyEvents).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%med.media.rejected.v1%").Count(&rejectedEvents).Error
	if storedClean.Status != "ready" || storedClean.SecurityState != "clean" || storedClean.ObjectVersion != 3 || storedRejected.Status != "rejected" || scans != 2 || receipts != 3 || readyEvents != 1 || rejectedEvents != 1 {
		t.Fatalf("clean=%+v rejected=%+v scans=%d receipts=%d ready=%d rejected_events=%d", storedClean, storedRejected, scans, receipts, readyEvents, rejectedEvents)
	}
	var eventRows []domain.LocalChangeJournal
	_ = db.Where("action = ?", "event_outbox").Find(&eventRows).Error
	for _, event := range eventRows {
		if strings.Contains(event.AfterState, "private-") || strings.Contains(event.AfterState, "webshell") {
			t.Fatalf("media event leaked private details: %s", event.AfterState)
		}
	}
	foreign := confirm
	foreign.CommandID, foreign.IdempotencyKey, foreign.AccountID = "media-confirm-foreign", "media-confirm-foreign", 92
	if response := call("/contracts/commands/med.asset.confirm.v1", foreign); response.Code != http.StatusNotFound {
		t.Fatalf("foreign confirm status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMEDAccessSecurityAndDeletionQueriesExposeSafeDecisions(t *testing.T) {
	db := newModuleTestDB(t, "MED")
	now := time.Now().UTC()
	asset := domain.MediaAsset{AccountID: 91, FileName: "ready.pdf", ContentType: "application/pdf", FileSize: 256, StorageKey: "91/private-ready", Status: "ready", SecurityState: "clean", ObjectVersion: 3, CreatedAt: now, UpdatedAt: now}
	foreign := domain.MediaAsset{AccountID: 92, FileName: "foreign.pdf", ContentType: "application/pdf", FileSize: 128, StorageKey: "92/private", Status: "ready", SecurityState: "clean", ObjectVersion: 3, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&asset).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	expires := now.Add(time.Hour)
	expired := now.Add(-time.Hour)
	for _, grant := range []domain.AccessGrant{
		{AccountID: 91, AssetID: asset.ID, SubjectType: "user", SubjectID: "7", Scope: "read", ExpiresAt: &expires, CreatedAt: now},
		{AccountID: 91, AssetID: asset.ID, SubjectType: "user", SubjectID: "8", Scope: "read", ExpiresAt: &expired, CreatedAt: now},
	} {
		if err := db.Create(&grant).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&domain.SecurityScan{AccountID: 91, AssetID: asset.ID, Status: "clean", Threat: "private-engine-detail", ScannedAt: now, CreatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.UploadSession{AccountID: 91, AssetID: asset.ID, TokenHash: "private-token-hash", Status: "pending", ExpiresAt: expires, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("MED")
	runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("MED")}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	call := func(path string, query foundation.QueryEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(runtime.Handler(), path, query, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
		})
	}
	base := foundation.QueryEnvelope{AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "api", AuthSource: "module"}, Sort: []string{"id"}, Page: "1", Fields: []string{"id"}, Consistency: "strong", RequestedAt: now}
	access := base
	access.QueryType = "med.access.check.v1"
	access.Filter = map[string]any{"id": asset.ID, "subject_type": "user", "subject_id": "7", "scope": "read"}
	if response := call("/contracts/queries/med.access.check.v1", access); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"allowed":true`) || strings.Contains(response.Body.String(), asset.StorageKey) {
		t.Fatalf("allowed access status=%d body=%s", response.Code, response.Body.String())
	}
	access.Filter["subject_id"] = "8"
	if response := call("/contracts/queries/med.access.check.v1", access); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"allowed":false`) {
		t.Fatalf("expired access status=%d body=%s", response.Code, response.Body.String())
	}
	security := base
	security.QueryType = "med.security_status.get.v1"
	security.Filter = map[string]any{"id": asset.ID}
	if response := call("/contracts/queries/med.security_status.get.v1", security); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"latest_scan_result":"clean"`) || strings.Contains(response.Body.String(), "private-engine-detail") {
		t.Fatalf("security status=%d body=%s", response.Code, response.Body.String())
	}
	security.AccountID = 92
	security.Filter = map[string]any{"id": asset.ID}
	if response := call("/contracts/queries/med.security_status.get.v1", security); response.Code != http.StatusNotFound {
		t.Fatalf("cross-account security status=%d body=%s", response.Code, response.Body.String())
	}
	deletion := base
	deletion.QueryType = "med.account_deletion_status.get.v1"
	deletion.Filter = map[string]any{}
	response := call("/contracts/queries/med.account_deletion_status.get.v1", deletion)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"asset_count":1`) || !strings.Contains(response.Body.String(), `"active_grant_count":1`) || !strings.Contains(response.Body.String(), `"pending_upload_count":1`) || !strings.Contains(response.Body.String(), `"security_record_count":1`) || strings.Contains(response.Body.String(), "private-") {
		t.Fatalf("deletion status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMEDAssetDeleteAndAccountPurgeStayWithinOwnerStore(t *testing.T) {
	db := newModuleTestDB(t, "MED")
	now := time.Now().UTC()
	assets := []domain.MediaAsset{
		{AccountID: 91, FileName: "delete.pdf", ContentType: "application/pdf", FileSize: 256, StorageKey: "91/private-delete", Status: "ready", SecurityState: "clean", ObjectVersion: 3, CreatedAt: now, UpdatedAt: now},
		{AccountID: 91, FileName: "purge.pdf", ContentType: "application/pdf", FileSize: 128, StorageKey: "91/private-purge", Status: "pending_upload", SecurityState: "pending", ObjectVersion: 1, CreatedAt: now, UpdatedAt: now},
		{AccountID: 92, FileName: "foreign.pdf", ContentType: "application/pdf", FileSize: 64, StorageKey: "92/private", Status: "ready", SecurityState: "clean", ObjectVersion: 3, CreatedAt: now, UpdatedAt: now},
	}
	for index := range assets {
		if err := db.Create(&assets[index]).Error; err != nil {
			t.Fatal(err)
		}
	}
	expires := now.Add(time.Hour)
	for _, accountID := range []uint{91, 92} {
		asset := assets[0]
		if accountID == 92 {
			asset = assets[2]
		}
		if err := db.Create(&domain.UploadSession{AccountID: accountID, AssetID: asset.ID, TokenHash: "private-token", Status: "pending", ExpiresAt: expires, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&domain.AccessGrant{AccountID: accountID, AssetID: asset.ID, SubjectType: "user", SubjectID: "7", Scope: "read", CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&domain.SecurityScan{AccountID: accountID, AssetID: asset.ID, Status: "clean", Threat: "private-threat", ScannedAt: now, CreatedAt: now}).Error; err != nil {
			t.Fatal(err)
		}
	}
	cfg := businessConfig("MED")
	newRuntime := func() Runtime {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("MED")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	call := func(path string, command foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), path, command, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, command.CorrelationID)
		})
	}
	base := foundation.CommandEnvelope{AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Input: map[string]any{}, CausationID: "account-delete-91", RequestedAt: now, TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "med.account_deletion_status.get.v1"}}
	remove := base
	remove.CommandID, remove.CommandType, remove.IdempotencyKey, remove.CorrelationID = "media-delete-1", "med.asset.delete.v1", "media-delete-91", "media-delete-correlation"
	remove.Target = foundation.ContractTarget{Type: "asset", ID: strconv.FormatUint(uint64(assets[0].ID), 10)}
	for index := 0; index < 2; index++ {
		if response := call("/contracts/commands/med.asset.delete.v1", remove); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"deleted"`) || strings.Contains(response.Body.String(), "private-") {
			t.Fatalf("asset delete status=%d body=%s", response.Code, response.Body.String())
		}
	}
	var deleted domain.MediaAsset
	if err := db.First(&deleted, assets[0].ID).Error; err != nil || deleted.Status != "deleted" || deleted.ObjectVersion != 4 {
		t.Fatalf("deleted asset=%+v err=%v", deleted, err)
	}
	var pending, active, deletedEvents int64
	_ = db.Model(&domain.UploadSession{}).Where("account_id = ? AND asset_id = ? AND status = ?", 91, assets[0].ID, "pending").Count(&pending).Error
	_ = db.Model(&domain.AccessGrant{}).Where("account_id = ? AND asset_id = ? AND revoked_at IS NULL", 91, assets[0].ID).Count(&active).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%med.media.deleted.v1%").Count(&deletedEvents).Error
	if pending != 0 || active != 0 || deletedEvents != 1 {
		t.Fatalf("pending=%d active=%d delete_events=%d", pending, active, deletedEvents)
	}
	foreign := remove
	foreign.CommandID, foreign.IdempotencyKey, foreign.AccountID = "media-delete-foreign", "media-delete-foreign", 92
	if response := call("/contracts/commands/med.asset.delete.v1", foreign); response.Code != http.StatusNotFound {
		t.Fatalf("cross-account delete status=%d body=%s", response.Code, response.Body.String())
	}
	purge := base
	purge.CommandID, purge.CommandType, purge.IdempotencyKey, purge.CorrelationID = "media-purge-91", "med.account_assets.purge.v1", "media-purge-91", "media-purge-correlation"
	purge.Target = foundation.ContractTarget{Type: "account", ID: "91"}
	for index := 0; index < 2; index++ {
		if response := call("/contracts/commands/med.account_assets.purge.v1", purge); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"deleted_count":2`) || strings.Contains(response.Body.String(), "private-") {
			t.Fatalf("account purge status=%d body=%s", response.Code, response.Body.String())
		}
	}
	for _, model := range []any{&domain.MediaAsset{}, &domain.UploadSession{}, &domain.SecurityScan{}, &domain.AccessGrant{}} {
		var own, other int64
		_ = db.Model(model).Where("account_id = ?", 91).Count(&own).Error
		_ = db.Model(model).Where("account_id = ?", 92).Count(&other).Error
		if own != 0 || other != 1 {
			t.Fatalf("model=%T own=%d other=%d", model, own, other)
		}
	}
	var eventRows []domain.LocalChangeJournal
	_ = db.Where("action = ?", "event_outbox").Find(&eventRows).Error
	if len(eventRows) != 2 {
		t.Fatalf("delete event rows=%d", len(eventRows))
	}
	for _, event := range eventRows {
		if strings.Contains(event.AfterState, "private-") {
			t.Fatalf("delete event leaked private metadata: %s", event.AfterState)
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

func TestCUSAccountContactAnonymizeAndDeletionStatusAreOwnerLocal(t *testing.T) {
	db := newModuleTestDB(t, "CUS")
	repo := repository.NewContactRepositoryWithOwnerPorts(db, nil, nil, nil, nil, nil)
	owned := domain.Contact{AccountID: 91, Name: "Private Name", Email: "private@example.test", PhoneNumber: "+6100", Identifier: "private-id", AvatarURL: "https://example.test/private.png", PubsubToken: "private-token", CustomAttributes: `{"secret":true}`, ObjectVersion: 2}
	foreign := domain.Contact{AccountID: 92, Name: "Foreign", Email: "foreign@example.test", PubsubToken: "foreign-token"}
	if err := repo.Create(&owned); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(&foreign); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateContactInbox(&domain.ContactInbox{ContactID: owned.ID, InboxID: 301, SourceID: "source-private"}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateNote(&domain.ContactNote{AccountID: 91, ContactID: owned.ID, UserID: 1, Content: "private note"}); err != nil {
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
	query := func(accountID uint) *httptest.ResponseRecorder {
		envelope := foundation.QueryEnvelope{QueryType: "cus.account_deletion_status.get.v1", AccountID: accountID, Actor: foundation.ContractActor{Type: "System", ID: "api", AuthSource: "module"}, Filter: map[string]any{}, Sort: []string{"id"}, Page: "1", Fields: []string{"ready"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
		return performContractRequest(newRuntime().Handler(), "/contracts/queries/cus.account_deletion_status.get.v1", envelope, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
		})
	}
	if response := query(91); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ready":false`) {
		t.Fatalf("pre-anonymize status=%d body=%s", response.Code, response.Body.String())
	}
	command := foundation.CommandEnvelope{CommandID: "contacts-anonymize-91", CommandType: "cus.account_contacts.anonymize.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "91"}, Input: map[string]any{}, IdempotencyKey: "contacts-anonymize-91", CorrelationID: "anonymize-correlation", CausationID: "account-delete", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "cus.account_deletion_status.get.v1"}}
	for i := 0; i < 2; i++ {
		response := performContractRequest(newRuntime().Handler(), "/contracts/commands/cus.account_contacts.anonymize.v1", command, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, command.CorrelationID)
		})
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ready":true`) {
			t.Fatalf("anonymize status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if response := query(91); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"pii_contact_count":0`) || !strings.Contains(response.Body.String(), `"identity_link_count":0`) || !strings.Contains(response.Body.String(), `"ready":true`) {
		t.Fatalf("post-anonymize status=%d body=%s", response.Code, response.Body.String())
	}
	var storedOwned, storedForeign domain.Contact
	_ = db.First(&storedOwned, owned.ID).Error
	_ = db.First(&storedForeign, foreign.ID).Error
	var links, notes, receipts, events int64
	_ = db.Model(&domain.ContactInbox{}).Where("contact_id = ?", owned.ID).Count(&links).Error
	_ = db.Model(&domain.ContactNote{}).Where("contact_id = ?", owned.ID).Count(&notes).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%cus.contact.changed.v1%").Count(&events).Error
	if storedOwned.Email != "" || storedOwned.PubsubToken != "" || storedOwned.Name != "Deleted Contact" || !storedOwned.Blocked || storedOwned.ObjectVersion != 3 || links != 0 || notes != 0 || receipts != 1 || events != 1 {
		t.Fatalf("owned=%+v links=%d notes=%d receipts=%d events=%d", storedOwned, links, notes, receipts, events)
	}
	if storedForeign.Email != foreign.Email || storedForeign.PubsubToken != foreign.PubsubToken {
		t.Fatalf("foreign account changed: %+v", storedForeign)
	}
}

func TestCUSContactMergeOwnsLocalFactsAndPublishesProjectionEvent(t *testing.T) {
	db := newModuleTestDB(t, "CUS")
	repo := repository.NewContactRepositoryWithOwnerPorts(db, nil, nil, nil, nil, nil)
	base := domain.Contact{AccountID: 91, Name: "Base", Email: "base@example.test", CustomAttributes: `{"base":true}`, ObjectVersion: 2}
	mergee := domain.Contact{AccountID: 91, PhoneNumber: "+6100", Identifier: "mergee-id", CustomAttributes: `{"mergee":true}`, ObjectVersion: 4}
	foreign := domain.Contact{AccountID: 92, Name: "Foreign"}
	for _, contact := range []*domain.Contact{&base, &mergee, &foreign} {
		if err := repo.Create(contact); err != nil {
			t.Fatal(err)
		}
	}
	labelA := domain.Label{AccountID: 91, Title: "A"}
	labelB := domain.Label{AccountID: 91, Title: "B"}
	_ = db.Create(&labelA).Error
	_ = db.Create(&labelB).Error
	for _, relation := range []domain.ContactInbox{{ContactID: base.ID, InboxID: 10, SourceID: "base-10"}, {ContactID: mergee.ID, InboxID: 10, SourceID: "mergee-10"}, {ContactID: mergee.ID, InboxID: 11, SourceID: "mergee-11"}} {
		if err := db.Create(&relation).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, relation := range []domain.ContactLabel{{ContactID: base.ID, LabelID: labelA.ID}, {ContactID: mergee.ID, LabelID: labelA.ID}, {ContactID: mergee.ID, LabelID: labelB.ID}} {
		if err := db.Create(&relation).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&domain.ContactNote{AccountID: 91, ContactID: mergee.ID, UserID: 1, Content: "move me"}).Error; err != nil {
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
	command := foundation.CommandEnvelope{CommandID: "contact-merge-1", CommandType: "cus.contact.merge.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "contact", ID: strconv.FormatUint(uint64(base.ID), 10)}, Input: map[string]any{"mergee_id": mergee.ID}, IdempotencyKey: "contact-merge-91", CorrelationID: "merge-correlation", CausationID: "merge-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "cus.contact.get.v1"}}
	for i := 0; i < 2; i++ {
		response := performContractRequest(newRuntime().Handler(), "/contracts/commands/cus.contact.merge.v1", command, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, command.CorrelationID)
		})
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "mergee-id") {
			t.Fatalf("contact merge status=%d body=%s", response.Code, response.Body.String())
		}
	}
	var merged domain.Contact
	if err := db.First(&merged, base.ID).Error; err != nil {
		t.Fatal(err)
	}
	var mergeeCount, inboxes, labels, notes, receipts, events int64
	_ = db.Model(&domain.Contact{}).Where("id = ?", mergee.ID).Count(&mergeeCount).Error
	_ = db.Model(&domain.ContactInbox{}).Where("contact_id = ?", base.ID).Count(&inboxes).Error
	_ = db.Model(&domain.ContactLabel{}).Where("contact_id = ?", base.ID).Count(&labels).Error
	_ = db.Model(&domain.ContactNote{}).Where("contact_id = ?", base.ID).Count(&notes).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%cus.contact.merged.v1%").Count(&events).Error
	if mergeeCount != 0 || merged.PhoneNumber != mergee.PhoneNumber || merged.Identifier != mergee.Identifier || merged.ObjectVersion != 3 || inboxes != 2 || labels != 2 || notes != 1 || receipts != 1 || events != 1 {
		t.Fatalf("merged=%+v mergee=%d inboxes=%d labels=%d notes=%d receipts=%d events=%d", merged, mergeeCount, inboxes, labels, notes, receipts, events)
	}
	if db.Migrator().HasTable(&domain.Conversation{}) || db.Migrator().HasTable(&domain.Message{}) || db.Migrator().HasTable(&domain.Ticket{}) || db.Migrator().HasTable(&domain.Order{}) || db.Migrator().HasTable(&domain.Campaign{}) {
		t.Fatal("CUS merge runtime created foreign projection tables")
	}
}

func TestCONConversationGetAndListExposeOwnerSafeViews(t *testing.T) {
	db := newModuleTestDB(t, "CON")
	now := time.Now().UTC()
	assignee, team, sla := uint(7), uint(8), uint(9)
	owned := domain.Conversation{AccountID: 91, DisplayID: 101, InboxID: 11, ContactID: 21, AssigneeID: &assignee, TeamID: &team, SLAPolicyID: &sla, Status: domain.ConversationStatusOpen, Priority: "high", CustomAttributes: `{"private":"hidden"}`, ObjectVersion: 3, LastActivityAt: now, CreatedAt: now, UpdatedAt: now}
	other := domain.Conversation{AccountID: 92, DisplayID: 102, InboxID: 12, ContactID: 22, Status: domain.ConversationStatusOpen, Priority: "low", CustomAttributes: `{"foreign":"hidden"}`, ObjectVersion: 1, LastActivityAt: now, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&owned).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.ConversationLabel{ConversationID: owned.ID, LabelID: 31}).Error; err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("CON")
	runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CON")}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	call := func(path string, query foundation.QueryEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(runtime.Handler(), path, query, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
		})
	}
	base := foundation.QueryEnvelope{AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "api", AuthSource: "module"}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "status"}, Consistency: "strong", RequestedAt: now}
	get := base
	get.QueryType, get.Filter = "con.conversation.get.v1", map[string]any{"id": owned.ID}
	response := call("/contracts/queries/con.conversation.get.v1", get)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"display_id":101`) || !strings.Contains(response.Body.String(), `"label_ids":[31]`) || strings.Contains(response.Body.String(), "private") || strings.Contains(response.Body.String(), "sla_policy") {
		t.Fatalf("conversation get status=%d body=%s", response.Code, response.Body.String())
	}
	get.AccountID = 92
	if response := call("/contracts/queries/con.conversation.get.v1", get); response.Code != http.StatusNotFound {
		t.Fatalf("cross-account get status=%d body=%s", response.Code, response.Body.String())
	}
	list := base
	list.QueryType, list.Filter = "con.conversations.list.v1", map[string]any{"status": domain.ConversationStatusOpen, "inbox_id": owned.InboxID}
	response = call("/contracts/queries/con.conversations.list.v1", list)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"total":1`) || !strings.Contains(response.Body.String(), `"display_id":101`) || strings.Contains(response.Body.String(), `"display_id":102`) || strings.Contains(response.Body.String(), "hidden") {
		t.Fatalf("conversation list status=%d body=%s", response.Code, response.Body.String())
	}
	for _, foreign := range []any{&domain.Account{}, &domain.Inbox{}, &domain.Contact{}, &domain.User{}, &domain.Team{}, &domain.Label{}, &domain.Message{}} {
		if db.Migrator().HasTable(foreign) {
			t.Fatalf("CON runtime contains foreign table %T", foreign)
		}
	}
}

func TestMSGQueriesExposeOwnerSafeViewsAndDeletionCounts(t *testing.T) {
	db := newModuleTestDB(t, "MSG")
	messages := []domain.Message{{AccountID: 91, ConversationID: 11, SenderType: domain.SenderTypeContact, SenderID: 21, MessageType: domain.MessageTypeIncoming, ContentType: "text", Content: "public", Status: "sent"}, {AccountID: 91, ConversationID: 11, SenderType: domain.SenderTypeUser, SenderID: 7, MessageType: domain.MessageTypeOutgoing, ContentType: "text", Content: "private-note", Private: true, Status: "delivered"}, {AccountID: 92, ConversationID: 22, SenderType: domain.SenderTypeContact, SenderID: 32, MessageType: domain.MessageTypeIncoming, ContentType: "text", Content: "foreign", Status: "sent"}}
	if err := db.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.Attachment{AccountID: 91, MessageID: messages[0].ID, FileType: "text/plain", DataURL: "med://asset/1"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.DraftMessage{AccountID: 91, ConversationID: 11, UserID: 7, Message: "draft"}).Error; err != nil {
		t.Fatal(err)
	}
	conversationID := uint(11)
	email := domain.EmailLog{AccountID: 91, ConversationID: &conversationID, EmailType: "transcript", ToEmail: "secret@example.test", FromEmail: "private@example.test", Subject: "Secret", ContentText: "body", Status: "sent", DeliveryStatus: "delivered", MaxRetries: 3}
	if err := db.Create(&email).Error; err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("MSG")
	runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("MSG")}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	base := foundation.QueryEnvelope{AccountID: 91, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, Sort: []string{"id"}, Page: "1", Fields: []string{"id"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	call := func(path string, payload foundation.QueryEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(runtime.Handler(), path, payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, "msg-query-correlation")
		})
	}
	get := base
	get.QueryType, get.Filter = "msg.message.get.v1", map[string]any{"id": messages[0].ID}
	response := call("/contracts/queries/msg.message.get.v1", get)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"content":"public"`) || strings.Contains(response.Body.String(), "translations") || strings.Contains(response.Body.String(), "content_attributes") {
		t.Fatalf("get=%d body=%s", response.Code, response.Body.String())
	}
	list := base
	list.QueryType, list.Filter = "msg.messages.list.v1", map[string]any{"conversation_id": 11}
	response = call("/contracts/queries/msg.messages.list.v1", list)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"total":1`) || strings.Contains(response.Body.String(), "private-note") || strings.Contains(response.Body.String(), "foreign") {
		t.Fatalf("list=%d body=%s", response.Code, response.Body.String())
	}
	delivery := base
	delivery.QueryType, delivery.Filter = "msg.delivery_status.get.v1", map[string]any{"id": messages[1].ID}
	response = call("/contracts/queries/msg.delivery_status.get.v1", delivery)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"delivered"`) || strings.Contains(response.Body.String(), "private-note") {
		t.Fatalf("delivery=%d body=%s", response.Code, response.Body.String())
	}
	emailQuery := base
	emailQuery.QueryType, emailQuery.Filter = "msg.email_delivery.get.v1", map[string]any{"id": email.ID}
	response = call("/contracts/queries/msg.email_delivery.get.v1", emailQuery)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"delivery_status":"delivered"`) || strings.Contains(response.Body.String(), "secret@example.test") || strings.Contains(response.Body.String(), "Secret") {
		t.Fatalf("email=%d body=%s", response.Code, response.Body.String())
	}
	deletion := base
	deletion.QueryType, deletion.Filter = "msg.account_deletion_status.get.v1", map[string]any{}
	response = call("/contracts/queries/msg.account_deletion_status.get.v1", deletion)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"message_count":2`) || !strings.Contains(response.Body.String(), `"attachment_count":1`) || !strings.Contains(response.Body.String(), `"draft_count":1`) || !strings.Contains(response.Body.String(), `"email_log_count":1`) {
		t.Fatalf("deletion=%d body=%s", response.Code, response.Body.String())
	}
	cross := get
	cross.AccountID = 92
	if response := call("/contracts/queries/msg.message.get.v1", cross); response.Code != http.StatusNotFound {
		t.Fatalf("cross=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMSGMessageCreateValidatesConversationSenderAndReplay(t *testing.T) {
	conRegistry := NewContractRegistry("CON")
	if err := conRegistry.RegisterQuery("con.conversation.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
		id, err := uintFilter(query.Filter, "id")
		if err != nil || query.AccountID != 91 || id != 11 {
			return nil, &ContractFailure{Type: "resource_not_found", Message: "conversation was not found"}
		}
		return conversationView{ID: 11, AccountID: 91, ContactID: 21, Status: domain.ConversationStatusOpen}, nil
	}); err != nil {
		t.Fatal(err)
	}
	conCfg := businessConfig("CON")
	conCfg.AllowedCallers["MSG"] = true
	conServer, conAddress := startHTTPServer(t, "127.0.0.1:0", Runtime{Config: conCfg, Contracts: conRegistry}.Handler())
	defer conServer.Shutdown(context.Background())
	tenRegistry := NewContractRegistry("TEN")
	if err := tenRegistry.RegisterQuery("ten.permissions.check.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
		userID, err := uintFilter(query.Filter, "user_id")
		if err != nil {
			return nil, &ContractFailure{Type: "validation_failed", Message: "user is invalid"}
		}
		return membershipCheckView{AccountID: query.AccountID, UserID: userID, Allowed: query.AccountID == 91 && userID == 7}, nil
	}); err != nil {
		t.Fatal(err)
	}
	tenCfg := businessConfig("TEN")
	tenCfg.AllowedCallers["MSG"] = true
	tenServer, tenAddress := startHTTPServer(t, "127.0.0.1:0", Runtime{Config: tenCfg, Contracts: tenRegistry}.Handler())
	defer tenServer.Shutdown(context.Background())
	db := newModuleTestDB(t, "MSG")
	cfg := businessConfig("MSG")
	cfg.Dependencies = map[string]DependencyConfig{"CON": {URL: "http://" + conAddress, Token: conCfg.AuthToken}, "TEN": {URL: "http://" + tenAddress, Token: tenCfg.AuthToken}}
	newRuntime := func() Runtime {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("MSG")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	command := foundation.CommandEnvelope{CommandID: "message-create-1", CommandType: "msg.message.create.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "conversation", ID: "11"}, Input: map[string]any{"conversation_id": 11, "sender_type": "User", "sender_id": 7, "message_type": "outgoing", "content_type": "text", "content": "hello", "echo_id": "echo-1"}, IdempotencyKey: "message-create-key", CorrelationID: "message-create-correlation", CausationID: "message-create-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "msg.message.get.v1"}}
	call := func(payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), "/contracts/commands/msg.message.create.v1", payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, payload.CorrelationID)
		})
	}
	for index := 0; index < 2; index++ {
		response := call(command)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"content":"hello"`) {
			t.Fatalf("create=%d body=%s", response.Code, response.Body.String())
		}
	}
	second := command
	second.CommandID, second.IdempotencyKey, second.CorrelationID = "message-create-2", "message-create-second-key", "message-create-second-correlation"
	response := call(second)
	if response.Code != http.StatusOK {
		t.Fatalf("echo replay=%d body=%s", response.Code, response.Body.String())
	}
	var messages, receipts, events int64
	_ = db.Model(&domain.Message{}).Count(&messages).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%msg.message.created.v1%").Count(&events).Error
	if messages != 1 || receipts != 2 || events != 1 {
		t.Fatalf("messages=%d receipts=%d events=%d", messages, receipts, events)
	}
	badSender := command
	badSender.CommandID, badSender.IdempotencyKey, badSender.CorrelationID = "message-create-3", "message-create-bad-sender", "message-create-bad-correlation"
	badSender.Input["sender_id"] = 9
	badSender.Input["echo_id"] = "echo-2"
	if response := call(badSender); response.Code != http.StatusNotFound {
		t.Fatalf("bad sender=%d body=%s", response.Code, response.Body.String())
	}
	if err := conServer.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	failed := command
	failed.CommandID, failed.IdempotencyKey, failed.CorrelationID = "message-create-4", "message-create-dependency", "message-create-dependency-correlation"
	failed.Input["echo_id"] = "echo-3"
	if response := call(failed); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("dependency=%d body=%s", response.Code, response.Body.String())
	}
	_ = db.Model(&domain.Message{}).Count(&messages).Error
	if messages != 1 {
		t.Fatalf("dependency wrote messages=%d", messages)
	}
}

func TestMSGMessageUpdateEditsDeletesAndKeepsOwnerBoundary(t *testing.T) {
	db := newModuleTestDB(t, "MSG")
	messages := []domain.Message{{AccountID: 91, ConversationID: 11, SenderType: domain.SenderTypeUser, SenderID: 7, MessageType: domain.MessageTypeOutgoing, ContentType: "text", Content: "before", Translations: `{"zh":"私密"}`, ContentAttributes: `{"token":"private"}`, Status: "sent"}, {AccountID: 91, ConversationID: 11, SenderType: domain.SenderTypeUser, SenderID: 7, MessageType: domain.MessageTypeOutgoing, ContentType: "text", Content: "delete-me", Status: "sent"}, {AccountID: 92, ConversationID: 22, SenderType: domain.SenderTypeUser, SenderID: 8, MessageType: domain.MessageTypeOutgoing, ContentType: "text", Content: "foreign", Status: "sent"}}
	if err := db.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.Attachment{AccountID: 91, MessageID: messages[1].ID, FileType: "text/plain", DataURL: "med://asset/1"}).Error; err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("MSG")
	newRuntime := func() Runtime {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("MSG")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	call := func(payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), "/contracts/commands/msg.message.update.v1", payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, payload.CorrelationID)
		})
	}
	base := foundation.CommandEnvelope{CommandType: "msg.message.update.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "msg.message.get.v1"}}
	edit := base
	edit.CommandID, edit.IdempotencyKey, edit.CorrelationID, edit.CausationID = "message-edit-1", "message-edit-key", "message-edit-correlation", "message-edit-cause"
	edit.Target = foundation.ContractTarget{Type: "message", ID: strconv.FormatUint(uint64(messages[0].ID), 10)}
	edit.Input = map[string]any{"operation": "edit", "content": "after"}
	for index := 0; index < 2; index++ {
		response := call(edit)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"content":"after"`) {
			t.Fatalf("edit=%d body=%s", response.Code, response.Body.String())
		}
	}
	remove := base
	remove.CommandID, remove.IdempotencyKey, remove.CorrelationID, remove.CausationID = "message-delete-1", "message-delete-key", "message-delete-correlation", "message-delete-cause"
	remove.Target = foundation.ContractTarget{Type: "message", ID: strconv.FormatUint(uint64(messages[1].ID), 10)}
	remove.Input = map[string]any{"operation": "delete"}
	for index := 0; index < 2; index++ {
		response := call(remove)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"deleted":true`) || strings.Contains(response.Body.String(), "delete-me") {
			t.Fatalf("delete=%d body=%s", response.Code, response.Body.String())
		}
	}
	var edited, deleted, untouched domain.Message
	_ = db.First(&edited, messages[0].ID).Error
	_ = db.First(&deleted, messages[1].ID).Error
	_ = db.First(&untouched, messages[2].ID).Error
	if edited.Content != "after" || edited.EditedAt == nil || edited.Translations == "" || edited.ContentAttributes == "" {
		t.Fatalf("edited=%+v", edited)
	}
	if !deleted.Deleted || deleted.DeletedAt == nil || deleted.Content != "" || deleted.Translations != "" || deleted.ContentAttributes != "" {
		t.Fatalf("deleted=%+v", deleted)
	}
	if untouched.Content != "foreign" || untouched.Deleted {
		t.Fatalf("untouched=%+v", untouched)
	}
	var attachments, changedEvents, deletedEvents int64
	_ = db.Model(&domain.Attachment{}).Where("message_id = ?", messages[1].ID).Count(&attachments).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%msg.message.changed.v1%").Count(&changedEvents).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%msg.message.deleted.v1%").Count(&deletedEvents).Error
	if attachments != 0 || changedEvents != 1 || deletedEvents != 1 {
		t.Fatalf("attachments=%d changed=%d deleted=%d", attachments, changedEvents, deletedEvents)
	}
	cross := edit
	cross.CommandID, cross.IdempotencyKey, cross.CorrelationID = "message-edit-2", "message-edit-cross", "message-edit-cross-correlation"
	cross.Target.ID = strconv.FormatUint(uint64(messages[2].ID), 10)
	if response := call(cross); response.Code != http.StatusNotFound {
		t.Fatalf("cross=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMSGProviderStatusAndRetryEnforceMonotonicOwnerState(t *testing.T) {
	db := newModuleTestDB(t, "MSG")
	messages := []domain.Message{{AccountID: 91, ConversationID: 11, SenderType: domain.SenderTypeUser, SenderID: 7, MessageType: domain.MessageTypeOutgoing, ContentType: "text", Content: "sent", Status: domain.MessageStatusSent}, {AccountID: 91, ConversationID: 11, SenderType: domain.SenderTypeUser, SenderID: 7, MessageType: domain.MessageTypeOutgoing, ContentType: "text", Content: "failed", Status: domain.MessageStatusFailed}, {AccountID: 92, ConversationID: 22, SenderType: domain.SenderTypeUser, SenderID: 8, MessageType: domain.MessageTypeOutgoing, ContentType: "text", Content: "foreign", Status: domain.MessageStatusFailed}}
	if err := db.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("MSG")
	newRuntime := func() Runtime {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("MSG")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	call := func(path string, payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), path, payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, payload.CorrelationID)
		})
	}
	base := foundation.CommandEnvelope{AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "provider"}, RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "msg.delivery_status.get.v1"}}
	provider := base
	provider.CommandID, provider.CommandType, provider.IdempotencyKey, provider.CorrelationID, provider.CausationID = "provider-status-1", "msg.provider_status.update.v1", "provider-status-key", "provider-status-correlation", "provider-status-cause"
	provider.Target = foundation.ContractTarget{Type: "message", ID: strconv.FormatUint(uint64(messages[0].ID), 10)}
	provider.Input = map[string]any{"status": "delivered"}
	for index := 0; index < 2; index++ {
		response := call("/contracts/commands/msg.provider_status.update.v1", provider)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"delivered"`) {
			t.Fatalf("provider=%d body=%s", response.Code, response.Body.String())
		}
	}
	backward := provider
	backward.CommandID, backward.IdempotencyKey, backward.CorrelationID = "provider-status-2", "provider-backward-key", "provider-backward-correlation"
	backward.Input = map[string]any{"status": "sent"}
	if response := call("/contracts/commands/msg.provider_status.update.v1", backward); response.Code != http.StatusConflict {
		t.Fatalf("backward=%d body=%s", response.Code, response.Body.String())
	}
	retry := base
	retry.CommandID, retry.CommandType, retry.IdempotencyKey, retry.CorrelationID, retry.CausationID = "message-retry-1", "msg.message.retry.v1", "message-retry-key", "message-retry-correlation", "message-retry-cause"
	retry.Target = foundation.ContractTarget{Type: "message", ID: strconv.FormatUint(uint64(messages[1].ID), 10)}
	retry.Input = map[string]any{}
	for index := 0; index < 2; index++ {
		response := call("/contracts/commands/msg.message.retry.v1", retry)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"sent"`) {
			t.Fatalf("retry=%d body=%s", response.Code, response.Body.String())
		}
	}
	cross := retry
	cross.CommandID, cross.IdempotencyKey, cross.CorrelationID = "message-retry-2", "message-retry-cross", "message-retry-cross-correlation"
	cross.Target.ID = strconv.FormatUint(uint64(messages[2].ID), 10)
	if response := call("/contracts/commands/msg.message.retry.v1", cross); response.Code != http.StatusNotFound {
		t.Fatalf("cross=%d body=%s", response.Code, response.Body.String())
	}
	var delivered, retried, untouched domain.Message
	_ = db.First(&delivered, messages[0].ID).Error
	_ = db.First(&retried, messages[1].ID).Error
	_ = db.First(&untouched, messages[2].ID).Error
	if delivered.Status != domain.MessageStatusDelivered || retried.Status != domain.MessageStatusSent || untouched.Status != domain.MessageStatusFailed {
		t.Fatalf("delivered=%s retried=%s untouched=%s", delivered.Status, retried.Status, untouched.Status)
	}
	var events int64
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%msg.message_status.changed.v1%").Count(&events).Error
	if events != 2 {
		t.Fatalf("status events=%d", events)
	}
}

func TestMSGReadPositionUsesMonotonicJournalFactAndMemberBoundary(t *testing.T) {
	tenRegistry := NewContractRegistry("TEN")
	if err := tenRegistry.RegisterQuery("ten.permissions.check.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
		userID, err := uintFilter(query.Filter, "user_id")
		if err != nil {
			return nil, &ContractFailure{Type: "validation_failed", Message: "user is invalid"}
		}
		return membershipCheckView{AccountID: query.AccountID, UserID: userID, Allowed: query.AccountID == 91 && userID == 7}, nil
	}); err != nil {
		t.Fatal(err)
	}
	tenCfg := businessConfig("TEN")
	tenCfg.AllowedCallers["MSG"] = true
	tenServer, tenAddress := startHTTPServer(t, "127.0.0.1:0", Runtime{Config: tenCfg, Contracts: tenRegistry}.Handler())
	defer tenServer.Shutdown(context.Background())
	db := newModuleTestDB(t, "MSG")
	messages := []domain.Message{{AccountID: 91, ConversationID: 11, SenderType: domain.SenderTypeContact, SenderID: 21, MessageType: domain.MessageTypeIncoming, Content: "one", Status: domain.MessageStatusSent}, {AccountID: 91, ConversationID: 11, SenderType: domain.SenderTypeContact, SenderID: 21, MessageType: domain.MessageTypeIncoming, Content: "two", Status: domain.MessageStatusSent}, {AccountID: 92, ConversationID: 22, SenderType: domain.SenderTypeContact, SenderID: 32, MessageType: domain.MessageTypeIncoming, Content: "foreign", Status: domain.MessageStatusSent}}
	if err := db.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("MSG")
	cfg.Dependencies = map[string]DependencyConfig{"TEN": {URL: "http://" + tenAddress, Token: tenCfg.AuthToken}}
	newRuntime := func() Runtime {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("MSG")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	call := func(payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), "/contracts/commands/msg.read_position.update.v1", payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, payload.CorrelationID)
		})
	}
	command := foundation.CommandEnvelope{CommandID: "read-position-1", CommandType: "msg.read_position.update.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "conversation", ID: "11"}, Input: map[string]any{"message_id": messages[1].ID, "user_id": 7}, IdempotencyKey: "read-position-key", CorrelationID: "read-position-correlation", CausationID: "read-position-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "msg.delivery_status.get.v1"}}
	for index := 0; index < 2; index++ {
		response := call(command)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"message_id":`+strconv.FormatUint(uint64(messages[1].ID), 10)) {
			t.Fatalf("read position=%d body=%s", response.Code, response.Body.String())
		}
	}
	same := command
	same.CommandID, same.IdempotencyKey, same.CorrelationID = "read-position-2", "read-position-same", "read-position-same-correlation"
	if response := call(same); response.Code != http.StatusOK {
		t.Fatalf("same=%d body=%s", response.Code, response.Body.String())
	}
	backward := command
	backward.CommandID, backward.IdempotencyKey, backward.CorrelationID = "read-position-3", "read-position-backward", "read-position-backward-correlation"
	backward.Input = map[string]any{"message_id": messages[0].ID, "user_id": 7}
	if response := call(backward); response.Code != http.StatusConflict {
		t.Fatalf("backward=%d body=%s", response.Code, response.Body.String())
	}
	denied := command
	denied.CommandID, denied.IdempotencyKey, denied.CorrelationID = "read-position-4", "read-position-denied", "read-position-denied-correlation"
	denied.Input = map[string]any{"message_id": messages[1].ID, "user_id": 9}
	if response := call(denied); response.Code != http.StatusNotFound {
		t.Fatalf("denied=%d body=%s", response.Code, response.Body.String())
	}
	var events int64
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%msg.read_position.changed.v1%").Count(&events).Error
	if events != 1 {
		t.Fatalf("read position events=%d", events)
	}
}

func TestMSGEmailSendQueuesOwnerDeliveryWithoutLeakingContent(t *testing.T) {
	conRegistry := NewContractRegistry("CON")
	if err := conRegistry.RegisterQuery("con.conversation.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
		id, err := uintFilter(query.Filter, "id")
		if err != nil || query.AccountID != 91 || id != 11 {
			return nil, &ContractFailure{Type: "resource_not_found", Message: "conversation was not found"}
		}
		return conversationView{ID: 11, AccountID: 91, ContactID: 21, Status: domain.ConversationStatusOpen}, nil
	}); err != nil {
		t.Fatal(err)
	}
	conCfg := businessConfig("CON")
	conCfg.AllowedCallers["MSG"] = true
	conServer, conAddress := startHTTPServer(t, "127.0.0.1:0", Runtime{Config: conCfg, Contracts: conRegistry}.Handler())
	defer conServer.Shutdown(context.Background())
	tenRegistry := NewContractRegistry("TEN")
	if err := tenRegistry.RegisterQuery("ten.permissions.check.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
		userID, err := uintFilter(query.Filter, "user_id")
		if err != nil {
			return nil, &ContractFailure{Type: "validation_failed", Message: "user is invalid"}
		}
		return membershipCheckView{AccountID: query.AccountID, UserID: userID, Allowed: query.AccountID == 91 && userID == 7}, nil
	}); err != nil {
		t.Fatal(err)
	}
	tenCfg := businessConfig("TEN")
	tenCfg.AllowedCallers["MSG"] = true
	tenServer, tenAddress := startHTTPServer(t, "127.0.0.1:0", Runtime{Config: tenCfg, Contracts: tenRegistry}.Handler())
	defer tenServer.Shutdown(context.Background())
	db := newModuleTestDB(t, "MSG")
	cfg := businessConfig("MSG")
	cfg.Dependencies = map[string]DependencyConfig{"CON": {URL: "http://" + conAddress, Token: conCfg.AuthToken}, "TEN": {URL: "http://" + tenAddress, Token: tenCfg.AuthToken}}
	newRuntime := func() Runtime {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("MSG")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	command := foundation.CommandEnvelope{CommandID: "email-send-1", CommandType: "msg.email.send.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "conversation", ID: "11"}, Input: map[string]any{"conversation_id": 11, "user_id": 7, "email_type": "transcript", "to_email": "customer@example.test", "from_email": "support@example.test", "subject": "Private subject", "content_html": "<p>Private body</p>", "content_text": "Private body"}, IdempotencyKey: "email-send-key", CorrelationID: "email-send-correlation", CausationID: "email-send-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "msg.email_delivery.get.v1"}}
	call := func(payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), "/contracts/commands/msg.email.send.v1", payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, payload.CorrelationID)
		})
	}
	for index := 0; index < 2; index++ {
		response := call(command)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"delivery_status":"pending"`) || strings.Contains(response.Body.String(), "customer@example.test") || strings.Contains(response.Body.String(), "Private") {
			t.Fatalf("email=%d body=%s", response.Code, response.Body.String())
		}
	}
	var logs, receipts, events int64
	_ = db.Model(&domain.EmailLog{}).Count(&logs).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%msg.email_delivery.changed.v1%").Count(&events).Error
	if logs != 1 || receipts != 1 || events != 1 {
		t.Fatalf("logs=%d receipts=%d events=%d", logs, receipts, events)
	}
	var stored domain.EmailLog
	if err := db.First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ToEmail != "customer@example.test" || stored.Subject != "Private subject" || stored.ContentText != "Private body" {
		t.Fatalf("stored=%+v", stored)
	}
	var journal domain.LocalChangeJournal
	if err := db.Where("action = ?", "event_outbox").First(&journal).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(journal.AfterState, "customer@example.test") || strings.Contains(journal.AfterState, "Private") {
		t.Fatalf("event leaked content=%s", journal.AfterState)
	}
	if err := conServer.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	failed := command
	failed.CommandID, failed.IdempotencyKey, failed.CorrelationID = "email-send-2", "email-send-dependency", "email-send-dependency-correlation"
	if response := call(failed); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("dependency=%d body=%s", response.Code, response.Body.String())
	}
	_ = db.Model(&domain.EmailLog{}).Count(&logs).Error
	if logs != 1 {
		t.Fatalf("dependency wrote logs=%d", logs)
	}
}

func TestMSGAccountAnonymizeRemovesOnlyOwnerSensitiveFacts(t *testing.T) {
	db := newModuleTestDB(t, "MSG")
	messages := []domain.Message{{AccountID: 91, ConversationID: 11, SenderType: domain.SenderTypeContact, SenderID: 21, MessageType: domain.MessageTypeIncoming, Content: "private", Translations: "translation", ContentAttributes: "attributes", EchoID: "echo-private", Status: domain.MessageStatusSent}, {AccountID: 92, ConversationID: 22, SenderType: domain.SenderTypeContact, SenderID: 32, MessageType: domain.MessageTypeIncoming, Content: "keep", EchoID: "keep-echo", Status: domain.MessageStatusSent}}
	if err := db.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.Attachment{AccountID: 91, MessageID: messages[0].ID, FileType: "text/plain", DataURL: "med://private"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.DraftMessage{AccountID: 91, ConversationID: 11, UserID: 7, Message: "private draft"}).Error; err != nil {
		t.Fatal(err)
	}
	conversationID, userID := uint(11), uint(7)
	emails := []domain.EmailLog{{AccountID: 91, ConversationID: &conversationID, UserID: &userID, EmailType: "transcript", ToEmail: "private@example.test", FromEmail: "support@example.test", Subject: "private", ContentText: "private", BounceReason: "private", Error: "private", Status: "failed", DeliveryStatus: "failed"}, {AccountID: 92, EmailType: "notification", ToEmail: "keep@example.test", FromEmail: "support@example.test", Subject: "keep", ContentText: "keep", Status: "sent", DeliveryStatus: "sent"}}
	if err := db.Create(&emails).Error; err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("MSG")
	newRuntime := func() Runtime {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("MSG")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	command := foundation.CommandEnvelope{CommandID: "messages-anonymize-1", CommandType: "msg.account_messages.anonymize.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "91"}, Input: map[string]any{}, IdempotencyKey: "messages-anonymize-key", CorrelationID: "messages-anonymize-correlation", CausationID: "messages-anonymize-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "msg.account_deletion_status.get.v1"}}
	call := func(payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), "/contracts/commands/msg.account_messages.anonymize.v1", payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, payload.CorrelationID)
		})
	}
	for index := 0; index < 2; index++ {
		response := call(command)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"anonymized"`) {
			t.Fatalf("anonymize=%d body=%s", response.Code, response.Body.String())
		}
	}
	var anonymized, untouched domain.Message
	_ = db.First(&anonymized, messages[0].ID).Error
	_ = db.First(&untouched, messages[1].ID).Error
	if anonymized.SenderID != 0 || anonymized.Content != "" || anonymized.Translations != "" || anonymized.ContentAttributes != "" || anonymized.EchoID != "" {
		t.Fatalf("anonymized=%+v", anonymized)
	}
	if untouched.SenderID != 32 || untouched.Content != "keep" || untouched.EchoID != "keep-echo" {
		t.Fatalf("untouched=%+v", untouched)
	}
	var email91, email92 domain.EmailLog
	_ = db.First(&email91, emails[0].ID).Error
	_ = db.First(&email92, emails[1].ID).Error
	if email91.ToEmail != "" || email91.FromEmail != "" || email91.Subject != "" || email91.ContentText != "" || email91.ConversationID != nil || email91.UserID != nil {
		t.Fatalf("email91=%+v", email91)
	}
	if email92.ToEmail != "keep@example.test" || email92.Subject != "keep" {
		t.Fatalf("email92=%+v", email92)
	}
	var attachments, drafts, events int64
	_ = db.Model(&domain.Attachment{}).Where("account_id = ?", 91).Count(&attachments).Error
	_ = db.Model(&domain.DraftMessage{}).Where("account_id = ?", 91).Count(&drafts).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%anonymized%").Count(&events).Error
	if attachments != 0 || drafts != 0 || events != 1 {
		t.Fatalf("attachments=%d drafts=%d events=%d", attachments, drafts, events)
	}
}

func TestRTEQueriesComposeOwnerContractsAndLocalCapacity(t *testing.T) {
	assigneeID := uint(7)
	conRegistry := NewContractRegistry("CON")
	if err := conRegistry.RegisterQuery("con.conversation.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
		id, err := uintFilter(query.Filter, "id")
		if err != nil || query.AccountID != 91 || id != 101 {
			return nil, &ContractFailure{Type: "resource_not_found", Message: "conversation was not found"}
		}
		return conversationView{ID: 101, AccountID: 91, InboxID: 11, ContactID: 21, AssigneeID: &assigneeID, Status: domain.ConversationStatusOpen, Priority: domain.PriorityHigh, LastActivityAt: time.Now().UTC()}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := conRegistry.RegisterQuery("con.conversations.list.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
		assigneeID, err := uintFilter(query.Filter, "assignee_id")
		if err != nil || query.AccountID != 91 {
			return nil, &ContractFailure{Type: "resource_not_found", Message: "conversations were not found"}
		}
		total := int64(0)
		if assigneeID == 7 && query.Filter["status"] == domain.ConversationStatusOpen {
			total = 2
		}
		return map[string]any{"items": []any{}, "total": total, "page": 1, "page_size": 50}, nil
	}); err != nil {
		t.Fatal(err)
	}
	conCfg := businessConfig("CON")
	conCfg.AllowedCallers["RTE"] = true
	conServer, conAddress := startHTTPServer(t, "127.0.0.1:0", Runtime{Config: conCfg, Contracts: conRegistry}.Handler())
	defer conServer.Shutdown(context.Background())
	tenRegistry := NewContractRegistry("TEN")
	if err := tenRegistry.RegisterQuery("ten.permissions.check.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
		userID, err := uintFilter(query.Filter, "user_id")
		if err != nil {
			return nil, &ContractFailure{Type: "validation_failed", Message: "user is invalid"}
		}
		return membershipCheckView{AccountID: query.AccountID, UserID: userID, Allowed: query.AccountID == 91 && (userID == 7 || userID == 8)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := tenRegistry.RegisterQuery("ten.members.list.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
		if query.AccountID != 91 {
			return nil, &ContractFailure{Type: "resource_not_found", Message: "account was not found"}
		}
		return []memberView{{ID: 1, AccountID: 91, UserID: 7, Role: "agent", Availability: "online"}, {ID: 2, AccountID: 91, UserID: 8, Role: "agent", Availability: "offline"}}, nil
	}); err != nil {
		t.Fatal(err)
	}
	tenCfg := businessConfig("TEN")
	tenCfg.AllowedCallers["RTE"] = true
	tenServer, tenAddress := startHTTPServer(t, "127.0.0.1:0", Runtime{Config: tenCfg, Contracts: tenRegistry}.Handler())
	defer tenServer.Shutdown(context.Background())
	chnRegistry := NewContractRegistry("CHN")
	if err := chnRegistry.RegisterQuery("chn.inbox.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
		id, err := uintFilter(query.Filter, "id")
		if err != nil || query.AccountID != 91 || id != 11 {
			return nil, &ContractFailure{Type: "resource_not_found", Message: "inbox was not found"}
		}
		return inboxView{ID: 11, AccountID: 91, Name: "Support"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	chnCfg := businessConfig("CHN")
	chnCfg.AllowedCallers["RTE"] = true
	chnServer, chnAddress := startHTTPServer(t, "127.0.0.1:0", Runtime{Config: chnCfg, Contracts: chnRegistry}.Handler())
	defer chnServer.Shutdown(context.Background())
	db := newModuleTestDB(t, "RTE")
	if err := db.Create(&domain.CapacityPolicy{AccountID: 91, UserID: 7, ConversationLimit: 3}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.CapacityPolicy{AccountID: 91, UserID: 0, ConversationLimit: 5}).Error; err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("RTE")
	cfg.Dependencies = map[string]DependencyConfig{"CON": {URL: "http://" + conAddress, Token: conCfg.AuthToken}, "TEN": {URL: "http://" + tenAddress, Token: tenCfg.AuthToken}, "CHN": {URL: "http://" + chnAddress, Token: chnCfg.AuthToken}}
	runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("RTE")}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	base := foundation.QueryEnvelope{AccountID: 91, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, Sort: []string{"id"}, Page: "1", Fields: []string{"id"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	call := func(path string, payload foundation.QueryEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(runtime.Handler(), path, payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, "rte-query-correlation")
		})
	}
	assignment := base
	assignment.QueryType, assignment.Filter = "rte.assignment.get.v1", map[string]any{"conversation_id": 101}
	response := call("/contracts/queries/rte.assignment.get.v1", assignment)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"assignee_id":7`) || !strings.Contains(response.Body.String(), `"assigned":true`) {
		t.Fatalf("assignment=%d body=%s", response.Code, response.Body.String())
	}
	queue := base
	queue.QueryType, queue.Filter = "rte.queue.get.v1", map[string]any{"conversation_id": 101}
	response = call("/contracts/queries/rte.queue.get.v1", queue)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"queued":false`) {
		t.Fatalf("queue=%d body=%s", response.Code, response.Body.String())
	}
	capacity := base
	capacity.QueryType, capacity.Filter = "rte.capacity.get.v1", map[string]any{"user_id": 7}
	response = call("/contracts/queries/rte.capacity.get.v1", capacity)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"conversation_limit":3`) {
		t.Fatalf("capacity=%d body=%s", response.Code, response.Body.String())
	}
	candidates := base
	candidates.QueryType, candidates.Filter = "rte.candidates.list.v1", map[string]any{"inbox_id": 11}
	response = call("/contracts/queries/rte.candidates.list.v1", candidates)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"user_id":7`) || !strings.Contains(response.Body.String(), `"active_conversations":2`) || !strings.Contains(response.Body.String(), `"conversation_limit":3`) || !strings.Contains(response.Body.String(), `"user_id":8`) || !strings.Contains(response.Body.String(), `"available":false`) {
		t.Fatalf("candidates=%d body=%s", response.Code, response.Body.String())
	}
	cross := assignment
	cross.AccountID = 92
	if response := call("/contracts/queries/rte.assignment.get.v1", cross); response.Code != http.StatusNotFound {
		t.Fatalf("cross=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCONStatusHistoryAndDeletionStatusStayAccountScoped(t *testing.T) {
	db := newModuleTestDB(t, "CON")
	now := time.Now().UTC()
	owned := domain.Conversation{AccountID: 91, DisplayID: 201, InboxID: 11, ContactID: 21, Status: domain.ConversationStatusResolved, Priority: "medium", ObjectVersion: 2, LastActivityAt: now, CreatedAt: now, UpdatedAt: now}
	other := domain.Conversation{AccountID: 92, DisplayID: 202, InboxID: 12, ContactID: 22, Status: domain.ConversationStatusOpen, Priority: "low", ObjectVersion: 1, LastActivityAt: now, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&owned).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	for _, relation := range []any{
		&domain.ConversationLabel{ConversationID: owned.ID, LabelID: 31},
		&domain.ConversationParticipant{ConversationID: owned.ID, UserID: 41, CreatedAt: now},
		&domain.ConversationLabel{ConversationID: other.ID, LabelID: 32},
		&domain.ConversationParticipant{ConversationID: other.ID, UserID: 42, CreatedAt: now},
	} {
		if err := db.Create(relation).Error; err != nil {
			t.Fatal(err)
		}
	}
	event := foundation.EventEnvelope{EventID: "con-status-history-1", EventType: "con.conversation_status.changed.v1", EventVersion: "v1", SourceModule: "CON", AccountID: 91, AggregateType: "Conversation", AggregateID: strconv.FormatUint(uint64(owned.ID), 10), AggregateVersion: 2, OccurredAt: now, PublishedAt: now, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, CorrelationID: "con-history-correlation", CausationID: "con-status-command", ChangedFields: []string{"status"}, Payload: map[string]any{"status": "resolved", "private": "must-not-return"}, DataClassification: "internal"}
	if err := NewEventOutbox(db, "CON").Append(nil, event); err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("CON")
	runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CON")}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	call := func(path string, query foundation.QueryEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(runtime.Handler(), path, query, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
		})
	}
	base := foundation.QueryEnvelope{AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "api", AuthSource: "module"}, Sort: []string{"id"}, Page: "1", Fields: []string{"id"}, Consistency: "strong", RequestedAt: now}
	history := base
	history.QueryType, history.Filter = "con.status_history.get.v1", map[string]any{"id": owned.ID}
	response := call("/contracts/queries/con.status_history.get.v1", history)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "con.conversation_status.changed.v1") || !strings.Contains(response.Body.String(), `"version":2`) || strings.Contains(response.Body.String(), "must-not-return") {
		t.Fatalf("status history=%d body=%s", response.Code, response.Body.String())
	}
	history.AccountID = 92
	if response := call("/contracts/queries/con.status_history.get.v1", history); response.Code != http.StatusNotFound {
		t.Fatalf("cross-account history=%d body=%s", response.Code, response.Body.String())
	}
	deletion := base
	deletion.QueryType, deletion.Filter = "con.account_deletion_status.get.v1", map[string]any{}
	response = call("/contracts/queries/con.account_deletion_status.get.v1", deletion)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"conversation_count":1`) || !strings.Contains(response.Body.String(), `"label_link_count":1`) || !strings.Contains(response.Body.String(), `"participant_count":1`) {
		t.Fatalf("deletion status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCONConversationCreateValidatesOwnersAndCommitsAtomically(t *testing.T) {
	tenRegistry := NewContractRegistry("TEN")
	if err := tenRegistry.RegisterQuery("ten.account.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
		if query.AccountID != 91 {
			return nil, &ContractFailure{Type: "resource_not_found", Message: "account was not found"}
		}
		return accountView{ID: 91, Name: "Tenant", Status: "active"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	tenCfg := businessConfig("TEN")
	tenCfg.AllowedCallers["CON"] = true
	tenServer, tenAddress := startHTTPServer(t, "127.0.0.1:0", Runtime{Config: tenCfg, Contracts: tenRegistry}.Handler())
	defer tenServer.Shutdown(context.Background())

	chnDB := newModuleTestDB(t, "CHN")
	inbox := domain.Inbox{AccountID: 91, Name: "Support", ChannelType: "Channel::WebWidget"}
	if err := repository.NewInboxRepositoryWithAccountPort(chnDB, nil).Create(&inbox); err != nil {
		t.Fatal(err)
	}
	chnCfg := businessConfig("CHN")
	chnCfg.AllowedCallers["CON"] = true
	chnRuntime := Runtime{Config: chnCfg, DB: chnDB, Contracts: NewContractRegistry("CHN")}
	if err := chnRuntime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	chnServer, chnAddress := startHTTPServer(t, "127.0.0.1:0", chnRuntime.Handler())
	defer chnServer.Shutdown(context.Background())

	cusDB := newModuleTestDB(t, "CUS")
	contact := domain.Contact{AccountID: 91, Name: "Customer", Email: "customer@example.test", PubsubToken: "private-token"}
	if err := repository.NewContactRepositoryWithOwnerPorts(cusDB, nil, nil, nil, nil, nil).Create(&contact); err != nil {
		t.Fatal(err)
	}
	cusCfg := businessConfig("CUS")
	cusCfg.AllowedCallers["CON"] = true
	cusRuntime := Runtime{Config: cusCfg, DB: cusDB, Contracts: NewContractRegistry("CUS")}
	if err := cusRuntime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	cusServer, cusAddress := startHTTPServer(t, "127.0.0.1:0", cusRuntime.Handler())

	conDB := newModuleTestDB(t, "CON")
	conCfg := businessConfig("CON")
	conCfg.Dependencies = map[string]DependencyConfig{"TEN": {URL: "http://" + tenAddress, Token: tenCfg.AuthToken}, "CHN": {URL: "http://" + chnAddress, Token: chnCfg.AuthToken}, "CUS": {URL: "http://" + cusAddress, Token: cusCfg.AuthToken}}
	newRuntime := func() Runtime {
		runtime := Runtime{Config: conCfg, DB: conDB, Contracts: NewContractRegistry("CON")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	command := foundation.CommandEnvelope{CommandID: "conversation-create-1", CommandType: "con.conversation.create.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "91"}, Input: map[string]any{"inbox_id": inbox.ID, "contact_id": contact.ID, "priority": "high"}, IdempotencyKey: "conversation-create-91", CorrelationID: "conversation-create-correlation", CausationID: "conversation-create-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "con.conversation.get.v1"}}
	call := func(payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), "/contracts/commands/con.conversation.create.v1", payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+conCfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, payload.CorrelationID)
		})
	}
	for index := 0; index < 2; index++ {
		response := call(command)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"priority":"high"`) || strings.Contains(response.Body.String(), "private-token") {
			t.Fatalf("conversation create=%d body=%s", response.Code, response.Body.String())
		}
	}
	var conversations, receipts, events int64
	_ = conDB.Model(&domain.Conversation{}).Count(&conversations).Error
	_ = conDB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = conDB.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%con.conversation.created.v1%").Count(&events).Error
	if conversations != 1 || receipts != 1 || events != 1 {
		t.Fatalf("conversations=%d receipts=%d events=%d", conversations, receipts, events)
	}
	if err := cusServer.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	failed := command
	failed.CommandID, failed.IdempotencyKey, failed.CorrelationID = "conversation-create-2", "conversation-create-92", "conversation-create-correlation-2"
	if response := call(failed); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("dependency failure status=%d body=%s", response.Code, response.Body.String())
	}
	_ = conDB.Model(&domain.Conversation{}).Count(&conversations).Error
	if conversations != 1 {
		t.Fatalf("dependency failure wrote conversation count=%d", conversations)
	}
}

func TestCONConversationUpdateAndAssignmentValidateRemoteOwners(t *testing.T) {
	tenRegistry := NewContractRegistry("TEN")
	if err := tenRegistry.RegisterQuery("ten.permissions.check.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
		if _, exists := query.Filter["team_id"]; exists {
			teamID, err := uintFilter(query.Filter, "team_id")
			if err != nil {
				return nil, &ContractFailure{Type: "validation_failed", Message: "team is invalid"}
			}
			return teamView{AccountID: query.AccountID, TeamID: teamID, Allowed: query.AccountID == 91 && teamID == 8}, nil
		}
		userID, err := uintFilter(query.Filter, "user_id")
		if err != nil {
			return nil, &ContractFailure{Type: "validation_failed", Message: "user is invalid"}
		}
		return membershipCheckView{AccountID: query.AccountID, UserID: userID, Allowed: query.AccountID == 91 && userID == 7}, nil
	}); err != nil {
		t.Fatal(err)
	}
	tenCfg := businessConfig("TEN")
	tenCfg.AllowedCallers["CON"] = true
	tenServer, tenAddress := startHTTPServer(t, "127.0.0.1:0", Runtime{Config: tenCfg, Contracts: tenRegistry}.Handler())
	defer tenServer.Shutdown(context.Background())

	chnRegistry := NewContractRegistry("CHN")
	if err := chnRegistry.RegisterQuery("chn.inbox.get.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
		inboxID, err := uintFilter(query.Filter, "id")
		if err != nil || query.AccountID != 91 || inboxID != 12 {
			return nil, &ContractFailure{Type: "resource_not_found", Message: "inbox was not found"}
		}
		return inboxView{ID: 12, AccountID: 91, Name: "Moved"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	chnCfg := businessConfig("CHN")
	chnCfg.AllowedCallers["CON"] = true
	chnServer, chnAddress := startHTTPServer(t, "127.0.0.1:0", Runtime{Config: chnCfg, Contracts: chnRegistry}.Handler())
	defer chnServer.Shutdown(context.Background())

	db := newModuleTestDB(t, "CON")
	conversation := domain.Conversation{AccountID: 91, InboxID: 11, ContactID: 21, DisplayID: 1, Status: domain.ConversationStatusOpen, Priority: domain.PriorityMedium, ObjectVersion: 1, LastActivityAt: time.Now().UTC()}
	other := domain.Conversation{AccountID: 92, InboxID: 22, ContactID: 32, DisplayID: 1, Status: domain.ConversationStatusOpen, Priority: domain.PriorityMedium, ObjectVersion: 1, LastActivityAt: time.Now().UTC()}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("CON")
	cfg.Dependencies = map[string]DependencyConfig{"TEN": {URL: "http://" + tenAddress, Token: tenCfg.AuthToken}, "CHN": {URL: "http://" + chnAddress, Token: chnCfg.AuthToken}}
	newRuntime := func() Runtime {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CON")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	call := func(path string, payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), path, payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, payload.CorrelationID)
		})
	}
	base := foundation.CommandEnvelope{AccountID: 91, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "conversation", ID: strconv.FormatUint(uint64(conversation.ID), 10)}, RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "con.conversation.get.v1"}}
	update := base
	update.CommandID, update.CommandType, update.IdempotencyKey, update.CorrelationID, update.CausationID = "conversation-update-1", "con.conversation.update.v1", "conversation-update-key", "conversation-update-correlation", "conversation-update-cause"
	update.Input = map[string]any{"inbox_id": 12, "muted": true}
	for index := 0; index < 2; index++ {
		response := call("/contracts/commands/con.conversation.update.v1", update)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"inbox_id":12`) || !strings.Contains(response.Body.String(), `"muted":true`) {
			t.Fatalf("update=%d body=%s", response.Code, response.Body.String())
		}
	}
	assignment := base
	assignment.CommandID, assignment.CommandType, assignment.IdempotencyKey, assignment.CorrelationID, assignment.CausationID = "assignment-change-1", "con.assignment.change.v1", "assignment-change-key", "assignment-change-correlation", "assignment-change-cause"
	assignment.Input = map[string]any{"assignee_id": 7, "team_id": 8}
	for index := 0; index < 2; index++ {
		response := call("/contracts/commands/con.assignment.change.v1", assignment)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"assignee_id":7`) || !strings.Contains(response.Body.String(), `"team_id":8`) {
			t.Fatalf("assignment=%d body=%s", response.Code, response.Body.String())
		}
	}
	var stored domain.Conversation
	if err := db.First(&stored, conversation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.InboxID != 12 || !stored.Muted || stored.AssigneeID == nil || *stored.AssigneeID != 7 || stored.TeamID == nil || *stored.TeamID != 8 || stored.ObjectVersion != 3 {
		t.Fatalf("stored=%+v", stored)
	}
	var events, receipts int64
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%con.conversation.changed.v1%").Count(&events).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	if events != 2 || receipts != 2 {
		t.Fatalf("events=%d receipts=%d", events, receipts)
	}
	denied := assignment
	denied.CommandID, denied.IdempotencyKey, denied.CorrelationID = "assignment-change-2", "assignment-change-denied", "assignment-change-denied-correlation"
	denied.Input = map[string]any{"assignee_id": 9}
	if response := call("/contracts/commands/con.assignment.change.v1", denied); response.Code != http.StatusNotFound {
		t.Fatalf("denied assignment=%d body=%s", response.Code, response.Body.String())
	}
	cross := update
	cross.CommandID, cross.IdempotencyKey, cross.CorrelationID = "conversation-update-2", "conversation-update-cross", "conversation-update-cross-correlation"
	cross.Target.ID = strconv.FormatUint(uint64(other.ID), 10)
	if response := call("/contracts/commands/con.conversation.update.v1", cross); response.Code != http.StatusNotFound {
		t.Fatalf("cross-account update=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCONStatusAndPriorityChangesAreScopedAtomicAndIdempotent(t *testing.T) {
	db := newModuleTestDB(t, "CON")
	conversation := domain.Conversation{AccountID: 91, InboxID: 11, ContactID: 21, DisplayID: 1, Status: domain.ConversationStatusOpen, Priority: domain.PriorityMedium, ObjectVersion: 1, LastActivityAt: time.Now().UTC()}
	other := domain.Conversation{AccountID: 92, InboxID: 22, ContactID: 32, DisplayID: 1, Status: domain.ConversationStatusOpen, Priority: domain.PriorityMedium, ObjectVersion: 1, LastActivityAt: time.Now().UTC()}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("CON")
	newRuntime := func() Runtime {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CON")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	call := func(path string, payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), path, payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, payload.CorrelationID)
		})
	}
	base := foundation.CommandEnvelope{AccountID: 91, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "conversation", ID: strconv.FormatUint(uint64(conversation.ID), 10)}, RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "con.conversation.get.v1"}}
	status := base
	status.CommandID, status.CommandType, status.IdempotencyKey, status.CorrelationID, status.CausationID = "status-change-1", "con.status.change.v1", "status-change-key", "status-change-correlation", "status-change-cause"
	status.Input = map[string]any{"status": "snoozed", "snoozed_until": time.Now().UTC().Add(time.Hour).Format(time.RFC3339)}
	priority := base
	priority.CommandID, priority.CommandType, priority.IdempotencyKey, priority.CorrelationID, priority.CausationID = "priority-change-1", "con.priority.change.v1", "priority-change-key", "priority-change-correlation", "priority-change-cause"
	priority.Input = map[string]any{"priority": "urgent"}
	for _, item := range []struct {
		path     string
		command  foundation.CommandEnvelope
		contains string
	}{{"/contracts/commands/con.status.change.v1", status, `"status":"snoozed"`}, {"/contracts/commands/con.priority.change.v1", priority, `"priority":"urgent"`}} {
		for index := 0; index < 2; index++ {
			response := call(item.path, item.command)
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), item.contains) {
				t.Fatalf("change=%d body=%s", response.Code, response.Body.String())
			}
		}
	}
	var stored domain.Conversation
	if err := db.First(&stored, conversation.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.Status != domain.ConversationStatusSnoozed || stored.SnoozedUntil == nil || stored.Priority != domain.PriorityUrgent || stored.ObjectVersion != 3 {
		t.Fatalf("stored=%+v", stored)
	}
	var statusEvents, changedEvents, receipts int64
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%con.conversation_status.changed.v1%").Count(&statusEvents).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%con.conversation.changed.v1%").Count(&changedEvents).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	if statusEvents != 1 || changedEvents != 1 || receipts != 2 {
		t.Fatalf("status_events=%d changed_events=%d receipts=%d", statusEvents, changedEvents, receipts)
	}
	invalid := status
	invalid.CommandID, invalid.IdempotencyKey, invalid.CorrelationID = "status-change-2", "status-invalid-key", "status-invalid-correlation"
	invalid.Input = map[string]any{"status": "snoozed", "snoozed_until": time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)}
	if response := call("/contracts/commands/con.status.change.v1", invalid); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid status=%d body=%s", response.Code, response.Body.String())
	}
	cross := priority
	cross.CommandID, cross.IdempotencyKey, cross.CorrelationID = "priority-change-2", "priority-cross-key", "priority-cross-correlation"
	cross.Target.ID = strconv.FormatUint(uint64(other.ID), 10)
	if response := call("/contracts/commands/con.priority.change.v1", cross); response.Code != http.StatusNotFound {
		t.Fatalf("cross priority=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCONParticipantManagementAndAccountAnonymizeStayOwnerScoped(t *testing.T) {
	tenRegistry := NewContractRegistry("TEN")
	if err := tenRegistry.RegisterQuery("ten.permissions.check.v1", func(_ context.Context, query foundation.QueryEnvelope) (any, error) {
		userID, err := uintFilter(query.Filter, "user_id")
		if err != nil {
			return nil, &ContractFailure{Type: "validation_failed", Message: "user is invalid"}
		}
		return membershipCheckView{AccountID: query.AccountID, UserID: userID, Allowed: query.AccountID == 91 && (userID == 7 || userID == 8)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	tenCfg := businessConfig("TEN")
	tenCfg.AllowedCallers["CON"] = true
	tenServer, tenAddress := startHTTPServer(t, "127.0.0.1:0", Runtime{Config: tenCfg, Contracts: tenRegistry}.Handler())
	defer tenServer.Shutdown(context.Background())
	db := newModuleTestDB(t, "CON")
	assigneeID, teamID := uint(7), uint(8)
	conversation := domain.Conversation{AccountID: 91, InboxID: 11, ContactID: 21, AssigneeID: &assigneeID, TeamID: &teamID, DisplayID: 1, Status: domain.ConversationStatusOpen, Priority: domain.PriorityMedium, ObjectVersion: 1, CustomAttributes: `{"email":"private@example.test"}`, LastActivityAt: time.Now().UTC()}
	other := domain.Conversation{AccountID: 92, InboxID: 22, ContactID: 32, DisplayID: 1, Status: domain.ConversationStatusOpen, Priority: domain.PriorityMedium, ObjectVersion: 1, CustomAttributes: `{"keep":true}`, LastActivityAt: time.Now().UTC()}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.ConversationLabel{ConversationID: conversation.ID, LabelID: 3}).Error; err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("CON")
	cfg.Dependencies = map[string]DependencyConfig{"TEN": {URL: "http://" + tenAddress, Token: tenCfg.AuthToken}}
	newRuntime := func() Runtime {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CON")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	call := func(path string, payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), path, payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, payload.CorrelationID)
		})
	}
	manage := foundation.CommandEnvelope{CommandID: "participant-1", CommandType: "con.participant.manage.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "conversation", ID: strconv.FormatUint(uint64(conversation.ID), 10)}, Input: map[string]any{"operation": "add", "user_ids": []any{float64(7), float64(8)}}, IdempotencyKey: "participant-key", CorrelationID: "participant-correlation", CausationID: "participant-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "con.conversation.get.v1"}}
	for index := 0; index < 2; index++ {
		if response := call("/contracts/commands/con.participant.manage.v1", manage); response.Code != http.StatusOK {
			t.Fatalf("participants=%d body=%s", response.Code, response.Body.String())
		}
	}
	var participants int64
	_ = db.Model(&domain.ConversationParticipant{}).Where("conversation_id = ?", conversation.ID).Count(&participants).Error
	if participants != 2 {
		t.Fatalf("participants=%d", participants)
	}
	anonymize := manage
	anonymize.CommandID, anonymize.CommandType, anonymize.IdempotencyKey, anonymize.CorrelationID, anonymize.CausationID = "anonymize-1", "con.account_conversations.anonymize.v1", "anonymize-key", "anonymize-correlation", "anonymize-cause"
	anonymize.Target = foundation.ContractTarget{Type: "account", ID: "91"}
	anonymize.Input = map[string]any{}
	for index := 0; index < 2; index++ {
		if response := call("/contracts/commands/con.account_conversations.anonymize.v1", anonymize); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"anonymized"`) {
			t.Fatalf("anonymize=%d body=%s", response.Code, response.Body.String())
		}
	}
	var stored, untouched domain.Conversation
	_ = db.First(&stored, conversation.ID).Error
	_ = db.First(&untouched, other.ID).Error
	if stored.ContactID != 0 || stored.AssigneeID != nil || stored.TeamID != nil || stored.CustomAttributes != "" || stored.ObjectVersion != 3 {
		t.Fatalf("stored=%+v", stored)
	}
	if untouched.ContactID != 32 || untouched.CustomAttributes != `{"keep":true}` || untouched.ObjectVersion != 1 {
		t.Fatalf("untouched=%+v", untouched)
	}
	var labels int64
	_ = db.Model(&domain.ConversationLabel{}).Where("conversation_id = ?", conversation.ID).Count(&labels).Error
	_ = db.Model(&domain.ConversationParticipant{}).Where("conversation_id = ?", conversation.ID).Count(&participants).Error
	if labels != 0 || participants != 0 {
		t.Fatalf("labels=%d participants=%d", labels, participants)
	}
	denied := manage
	denied.CommandID, denied.IdempotencyKey, denied.CorrelationID = "participant-2", "participant-denied", "participant-denied-correlation"
	denied.Input = map[string]any{"operation": "add", "user_ids": []any{float64(9)}}
	if response := call("/contracts/commands/con.participant.manage.v1", denied); response.Code != http.StatusNotFound {
		t.Fatalf("denied=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCONConversationDeleteRemovesOnlyOwnerFactsAndPublishesEvent(t *testing.T) {
	db := newModuleTestDB(t, "CON")
	conversation := domain.Conversation{AccountID: 91, InboxID: 11, ContactID: 21, DisplayID: 1, Status: domain.ConversationStatusOpen, Priority: domain.PriorityMedium, ObjectVersion: 1, LastActivityAt: time.Now().UTC()}
	other := domain.Conversation{AccountID: 92, InboxID: 22, ContactID: 32, DisplayID: 1, Status: domain.ConversationStatusOpen, Priority: domain.PriorityMedium, ObjectVersion: 1, LastActivityAt: time.Now().UTC()}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.ConversationParticipant{ConversationID: conversation.ID, UserID: 7}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&domain.ConversationLabel{ConversationID: conversation.ID, LabelID: 8}).Error; err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("CON")
	newRuntime := func() Runtime {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CON")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	command := foundation.CommandEnvelope{CommandID: "conversation-delete-1", CommandType: "con.conversation.update.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "conversation", ID: strconv.FormatUint(uint64(conversation.ID), 10)}, Input: map[string]any{"operation": "delete"}, IdempotencyKey: "conversation-delete-key", CorrelationID: "conversation-delete-correlation", CausationID: "conversation-delete-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "con.account_deletion_status.get.v1"}}
	call := func(payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), "/contracts/commands/con.conversation.update.v1", payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, payload.CorrelationID)
		})
	}
	for index := 0; index < 2; index++ {
		response := call(command)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"deleted"`) {
			t.Fatalf("delete=%d body=%s", response.Code, response.Body.String())
		}
	}
	var owned, untouched, participants, labels, events int64
	_ = db.Model(&domain.Conversation{}).Where("account_id = ?", 91).Count(&owned).Error
	_ = db.Model(&domain.Conversation{}).Where("account_id = ?", 92).Count(&untouched).Error
	_ = db.Model(&domain.ConversationParticipant{}).Where("conversation_id = ?", conversation.ID).Count(&participants).Error
	_ = db.Model(&domain.ConversationLabel{}).Where("conversation_id = ?", conversation.ID).Count(&labels).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%con.conversation.deleted.v1%").Count(&events).Error
	if owned != 0 || untouched != 1 || participants != 0 || labels != 0 || events != 1 {
		t.Fatalf("owned=%d untouched=%d participants=%d labels=%d events=%d", owned, untouched, participants, labels, events)
	}
	cross := command
	cross.CommandID, cross.IdempotencyKey, cross.CorrelationID = "conversation-delete-2", "conversation-delete-cross", "conversation-delete-cross-correlation"
	cross.Target.ID = strconv.FormatUint(uint64(other.ID), 10)
	if response := call(cross); response.Code != http.StatusNotFound {
		t.Fatalf("cross delete=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCONConsumesContactMergeEventWithinOwnerDatabase(t *testing.T) {
	db := newModuleTestDB(t, "CON")
	conversations := []domain.Conversation{{AccountID: 91, InboxID: 10, ContactID: 202, DisplayID: 1}, {AccountID: 91, InboxID: 11, ContactID: 202, DisplayID: 2}, {AccountID: 92, InboxID: 12, ContactID: 202, DisplayID: 1}}
	if err := db.Create(&conversations).Error; err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("CON")
	cfg.AllowedCallers["CUS"] = true
	runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CON")}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	event := foundation.EventEnvelope{EventID: "contact-merged-con-1", EventType: "cus.contact.merged.v1", EventVersion: "v1", SourceModule: "CUS", AccountID: 91, AggregateType: "Contact", AggregateID: "101", AggregateVersion: 1, OccurredAt: now, PublishedAt: now, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, CorrelationID: "merge-event-correlation", CausationID: "contact-merge-1", ChangedFields: []string{"merged"}, Payload: map[string]any{"base_contact_id": 101, "merged_contact_id": 202}, DataClassification: "internal"}
	deliver := func(payload foundation.EventEnvelope) *httptest.ResponseRecorder {
		return deliverEvent(runtime.Handler(), payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "CUS")
		})
	}
	for i := 0; i < 2; i++ {
		if response := deliver(event); response.Code != http.StatusOK {
			t.Fatalf("merge event status=%d body=%s", response.Code, response.Body.String())
		}
	}
	var reassigned, foreign, receipts int64
	_ = db.Model(&domain.Conversation{}).Where("account_id = ? AND contact_id = ?", 91, 101).Count(&reassigned).Error
	_ = db.Model(&domain.Conversation{}).Where("account_id = ? AND contact_id = ?", 92, 202).Count(&foreign).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND source_type = ?", "event_consumed", "CUS").Count(&receipts).Error
	if reassigned != 2 || foreign != 1 || receipts != 1 {
		t.Fatalf("reassigned=%d foreign=%d receipts=%d", reassigned, foreign, receipts)
	}
	if db.Migrator().HasTable(&domain.Contact{}) {
		t.Fatal("CON merge consumer created CUS Contact table")
	}
	gap := event
	gap.EventID, gap.AggregateID, gap.AggregateVersion = "contact-merged-con-gap", "303", 2
	gap.Payload = map[string]any{"base_contact_id": 303, "merged_contact_id": 404}
	if response := deliver(gap); response.Code != http.StatusConflict {
		t.Fatalf("gap merge event status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestCONConsumesMessageCreatedEventAsIdempotentActivity(t *testing.T) {
	db := newModuleTestDB(t, "CON")
	conversation := domain.Conversation{AccountID: 91, InboxID: 11, ContactID: 21, DisplayID: 1, Status: domain.ConversationStatusOpen, Priority: domain.PriorityMedium, ObjectVersion: 1, LastActivityAt: time.Now().UTC().Add(-time.Hour)}
	other := domain.Conversation{AccountID: 92, InboxID: 22, ContactID: 32, DisplayID: 1, Status: domain.ConversationStatusOpen, Priority: domain.PriorityMedium, ObjectVersion: 1, LastActivityAt: time.Now().UTC().Add(-time.Hour)}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("CON")
	cfg.AllowedCallers["MSG"] = true
	runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CON")}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	event := foundation.EventEnvelope{EventID: "message-created-con-1", EventType: "msg.message.created.v1", EventVersion: "v1", SourceModule: "MSG", AccountID: 91, AggregateType: "Message", AggregateID: "101", AggregateVersion: 1, OccurredAt: now, PublishedAt: now, Actor: foundation.ContractActor{Type: "Contact", ID: "21", AuthSource: "module"}, CorrelationID: "message-event-correlation", CausationID: "message-create-1", ChangedFields: []string{"created"}, Payload: map[string]any{"message_id": 101, "conversation_id": conversation.ID, "sender_type": "Contact", "sender_id": 21, "message_type": "incoming", "status": "sent"}, DataClassification: "internal"}
	for index := 0; index < 2; index++ {
		response := deliverEvent(runtime.Handler(), event, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "MSG")
		})
		if response.Code != http.StatusOK {
			t.Fatalf("event=%d body=%s", response.Code, response.Body.String())
		}
	}
	var stored, untouched domain.Conversation
	_ = db.First(&stored, conversation.ID).Error
	_ = db.First(&untouched, other.ID).Error
	if stored.UnreadCount != 1 || stored.ObjectVersion != 2 || stored.LastActivityAt.Before(now) {
		t.Fatalf("stored=%+v", stored)
	}
	if untouched.UnreadCount != 0 || untouched.ObjectVersion != 1 {
		t.Fatalf("untouched=%+v", untouched)
	}
	var changes int64
	_ = db.Model(&domain.LocalChangeJournal{}).Where("change_id = ?", "con-activity-"+event.EventID).Count(&changes).Error
	if changes != 1 {
		t.Fatalf("activity changes=%d", changes)
	}
}

func TestMSGConsumesContactMergeEventForContactSendersOnly(t *testing.T) {
	db := newModuleTestDB(t, "MSG")
	messages := []domain.Message{
		{AccountID: 91, ConversationID: 1, SenderType: domain.SenderTypeContact, SenderID: 202, MessageType: domain.MessageTypeIncoming, ContentType: domain.ContentTypeText},
		{AccountID: 91, ConversationID: 1, SenderType: domain.SenderTypeUser, SenderID: 202, MessageType: domain.MessageTypeOutgoing, ContentType: domain.ContentTypeText},
		{AccountID: 92, ConversationID: 2, SenderType: domain.SenderTypeContact, SenderID: 202, MessageType: domain.MessageTypeIncoming, ContentType: domain.ContentTypeText},
	}
	if err := db.Create(&messages).Error; err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("MSG")
	cfg.AllowedCallers["CUS"] = true
	runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("MSG")}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	event := foundation.EventEnvelope{EventID: "contact-merged-msg-1", EventType: "cus.contact.merged.v1", EventVersion: "v1", SourceModule: "CUS", AccountID: 91, AggregateType: "Contact", AggregateID: "101", AggregateVersion: 1, OccurredAt: now, PublishedAt: now, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, CorrelationID: "merge-msg-correlation", CausationID: "contact-merge-1", ChangedFields: []string{"merged"}, Payload: map[string]any{"base_contact_id": 101, "merged_contact_id": 202}, DataClassification: "internal"}
	deliver := func() *httptest.ResponseRecorder {
		return deliverEvent(runtime.Handler(), event, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "CUS")
		})
	}
	if response := deliver(); response.Code != http.StatusOK {
		t.Fatalf("merge event status=%d body=%s", response.Code, response.Body.String())
	}
	if response := deliver(); response.Code != http.StatusOK {
		t.Fatalf("duplicate merge event status=%d body=%s", response.Code, response.Body.String())
	}
	var contactUpdated, userUntouched, foreignUntouched, receipts int64
	_ = db.Model(&domain.Message{}).Where("account_id = ? AND sender_type = ? AND sender_id = ?", 91, domain.SenderTypeContact, 101).Count(&contactUpdated).Error
	_ = db.Model(&domain.Message{}).Where("account_id = ? AND sender_type = ? AND sender_id = ?", 91, domain.SenderTypeUser, 202).Count(&userUntouched).Error
	_ = db.Model(&domain.Message{}).Where("account_id = ? AND sender_type = ? AND sender_id = ?", 92, domain.SenderTypeContact, 202).Count(&foreignUntouched).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND source_type = ?", "event_consumed", "CUS").Count(&receipts).Error
	if contactUpdated != 1 || userUntouched != 1 || foreignUntouched != 1 || receipts != 1 {
		t.Fatalf("contact=%d user=%d foreign=%d receipts=%d", contactUpdated, userUntouched, foreignUntouched, receipts)
	}
	if db.Migrator().HasTable(&domain.Contact{}) || db.Migrator().HasTable(&domain.Conversation{}) {
		t.Fatal("MSG merge consumer created foreign owner tables")
	}
}

func TestTKTBILAndOPSConsumeContactMergeWithinOwnerStores(t *testing.T) {
	baseID, mergedID := uint(101), uint(202)
	now := time.Now().UTC()
	event := foundation.EventEnvelope{EventID: "contact-merged-projections-1", EventType: "cus.contact.merged.v1", EventVersion: "v1", SourceModule: "CUS", AccountID: 91, AggregateType: "Contact", AggregateID: "101", AggregateVersion: 1, OccurredAt: now, PublishedAt: now, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, CorrelationID: "merge-projections-correlation", CausationID: "contact-merge-1", ChangedFields: []string{"merged"}, Payload: map[string]any{"base_contact_id": baseID, "merged_contact_id": mergedID}, DataClassification: "internal"}
	deliver := func(module string, db *gorm.DB) {
		t.Helper()
		cfg := businessConfig(module)
		cfg.AllowedCallers["CUS"] = true
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry(module)}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			response := deliverEvent(runtime.Handler(), event, func(request *http.Request) {
				request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
				request.Header.Set(headerCaller, "CUS")
			})
			if response.Code != http.StatusOK {
				t.Fatalf("%s merge event status=%d body=%s", module, response.Code, response.Body.String())
			}
		}
		var receipts int64
		_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND source_type = ?", "event_consumed", "CUS").Count(&receipts).Error
		if receipts != 1 || db.Migrator().HasTable(&domain.Contact{}) {
			t.Fatalf("%s receipts=%d contact_table=%v", module, receipts, db.Migrator().HasTable(&domain.Contact{}))
		}
	}

	t.Run("TKT", func(t *testing.T) {
		db := newModuleTestDB(t, "TKT")
		foreignContact := mergedID
		tickets := []domain.Ticket{{AccountID: 91, TicketNumber: "T-1", Title: "Owned", ContactID: &foreignContact}, {AccountID: 92, TicketNumber: "T-2", Title: "Foreign", ContactID: &foreignContact}}
		if err := db.Create(&tickets).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&domain.TicketComment{AccountID: 91, TicketID: tickets[0].ID, ContactID: &foreignContact, Content: "contact comment"}).Error; err != nil {
			t.Fatal(err)
		}
		deliver("TKT", db)
		var ticketUpdated, commentUpdated, foreignUntouched int64
		_ = db.Model(&domain.Ticket{}).Where("account_id = ? AND contact_id = ?", 91, baseID).Count(&ticketUpdated).Error
		_ = db.Model(&domain.TicketComment{}).Where("account_id = ? AND contact_id = ?", 91, baseID).Count(&commentUpdated).Error
		_ = db.Model(&domain.Ticket{}).Where("account_id = ? AND contact_id = ?", 92, mergedID).Count(&foreignUntouched).Error
		if ticketUpdated != 1 || commentUpdated != 1 || foreignUntouched != 1 {
			t.Fatalf("ticket=%d comment=%d foreign=%d", ticketUpdated, commentUpdated, foreignUntouched)
		}
	})

	t.Run("BIL", func(t *testing.T) {
		db := newModuleTestDB(t, "BIL")
		orders := []domain.Order{{AccountID: 91, OrderID: "O-1", ContactID: &mergedID}, {AccountID: 92, OrderID: "O-2", ContactID: &mergedID}}
		if err := db.Create(&orders).Error; err != nil {
			t.Fatal(err)
		}
		deliver("BIL", db)
		var updated, foreign int64
		_ = db.Model(&domain.Order{}).Where("account_id = ? AND contact_id = ?", 91, baseID).Count(&updated).Error
		_ = db.Model(&domain.Order{}).Where("account_id = ? AND contact_id = ?", 92, mergedID).Count(&foreign).Error
		if updated != 1 || foreign != 1 {
			t.Fatalf("orders updated=%d foreign=%d", updated, foreign)
		}
	})

	t.Run("OPS", func(t *testing.T) {
		db := newModuleTestDB(t, "OPS")
		campaigns := []domain.Campaign{{AccountID: 91, InboxID: 1, Title: "Owned", Message: "Hi"}, {AccountID: 92, InboxID: 2, Title: "Foreign", Message: "Hi"}}
		if err := db.Create(&campaigns).Error; err != nil {
			t.Fatal(err)
		}
		deliveries := []domain.CampaignDelivery{{AccountID: 91, CampaignID: campaigns[0].ID, ContactID: mergedID}, {AccountID: 92, CampaignID: campaigns[1].ID, ContactID: mergedID}}
		if err := db.Create(&deliveries).Error; err != nil {
			t.Fatal(err)
		}
		deliver("OPS", db)
		var updated, foreign int64
		_ = db.Model(&domain.CampaignDelivery{}).Where("account_id = ? AND contact_id = ?", 91, baseID).Count(&updated).Error
		_ = db.Model(&domain.CampaignDelivery{}).Where("account_id = ? AND contact_id = ?", 92, mergedID).Count(&foreign).Error
		if updated != 1 || foreign != 1 {
			t.Fatalf("deliveries updated=%d foreign=%d", updated, foreign)
		}
	})
}

func TestCUSContactMergeEventFansOutToAllProjectionOwners(t *testing.T) {
	baseID, mergedID := uint(101), uint(202)
	sourceDB := newModuleTestDB(t, "CUS")
	now := time.Now().UTC()
	event := foundation.EventEnvelope{EventID: "contact-merge-five-targets", EventType: "cus.contact.merged.v1", EventVersion: "v1", SourceModule: "CUS", AccountID: 91, AggregateType: "Contact", AggregateID: "101", AggregateVersion: 1, OccurredAt: now, PublishedAt: now, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, CorrelationID: "five-target-correlation", CausationID: "contact-merge-command", ChangedFields: []string{"merged"}, Payload: map[string]any{"base_contact_id": baseID, "merged_contact_id": mergedID}, DataClassification: "internal"}
	if err := NewEventOutbox(sourceDB, "CUS").Append(nil, event); err != nil {
		t.Fatal(err)
	}
	type consumer struct {
		db      *gorm.DB
		address string
		server  *http.Server
	}
	consumers := map[string]consumer{}
	start := func(module string, db *gorm.DB) {
		cfg := businessConfig(module)
		cfg.AllowedCallers["CUS"] = true
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry(module)}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		server, address := startHTTPServer(t, "127.0.0.1:0", runtime.Handler())
		consumers[module] = consumer{db: db, address: address, server: server}
	}
	conDB := newModuleTestDB(t, "CON")
	_ = conDB.Create(&domain.Conversation{AccountID: 91, InboxID: 1, ContactID: mergedID, DisplayID: 1}).Error
	start("CON", conDB)
	msgDB := newModuleTestDB(t, "MSG")
	_ = msgDB.Create(&domain.Message{AccountID: 91, ConversationID: 1, SenderType: domain.SenderTypeContact, SenderID: mergedID, MessageType: domain.MessageTypeIncoming, ContentType: domain.ContentTypeText}).Error
	start("MSG", msgDB)
	tktDB := newModuleTestDB(t, "TKT")
	_ = tktDB.Create(&domain.Ticket{AccountID: 91, TicketNumber: "FAN-1", Title: "Fanout", ContactID: &mergedID}).Error
	start("TKT", tktDB)
	bilDB := newModuleTestDB(t, "BIL")
	_ = bilDB.Create(&domain.Order{AccountID: 91, OrderID: "FAN-1", ContactID: &mergedID}).Error
	start("BIL", bilDB)
	opsDB := newModuleTestDB(t, "OPS")
	campaign := domain.Campaign{AccountID: 91, InboxID: 1, Title: "Fanout", Message: "Hello"}
	_ = opsDB.Create(&campaign).Error
	_ = opsDB.Create(&domain.CampaignDelivery{AccountID: 91, CampaignID: campaign.ID, ContactID: mergedID}).Error
	start("OPS", opsDB)
	for _, item := range consumers {
		defer item.server.Shutdown(context.Background())
	}
	targets := map[string]EventFanoutTarget{}
	for module, item := range consumers {
		targets[module] = EventFanoutTarget{Dependency: DependencyConfig{URL: "http://" + item.address, Token: businessConfig(module).AuthToken}, EventTypes: map[string]bool{event.EventType: true}}
	}
	if delivered, err := NewEventFanoutDispatcher(sourceDB, "CUS", targets, "v1", time.Second).DeliverPending(context.Background(), 10); err != nil || delivered != 1 {
		t.Fatalf("five-target fanout delivered=%d err=%v", delivered, err)
	}
	checks := []struct {
		db    *gorm.DB
		model any
	}{
		{conDB, &domain.Conversation{}}, {tktDB, &domain.Ticket{}}, {bilDB, &domain.Order{}}, {opsDB, &domain.CampaignDelivery{}},
	}
	for _, check := range checks {
		var count int64
		if err := check.db.Model(check.model).Where("account_id = ? AND contact_id = ?", 91, baseID).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("fanout model=%T count=%d err=%v", check.model, count, err)
		}
	}
	var messageCount int64
	if err := msgDB.Model(&domain.Message{}).Where("account_id = ? AND sender_type = ? AND sender_id = ?", 91, domain.SenderTypeContact, baseID).Count(&messageCount).Error; err != nil || messageCount != 1 {
		t.Fatalf("fanout messages=%d err=%v", messageCount, err)
	}
	var receipts, deliveredOutbox int64
	_ = sourceDB.Model(&domain.LocalChangeJournal{}).Where("action = ?", "event_delivery").Count(&receipts).Error
	_ = sourceDB.Model(&domain.LocalChangeJournal{}).Where("action = ? AND status = ?", "event_outbox", "delivered").Count(&deliveredOutbox).Error
	if receipts != 5 || deliveredOutbox != 1 {
		t.Fatalf("delivery receipts=%d outbox=%d", receipts, deliveredOutbox)
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

func TestCUSContactDeleteRemovesOwnerFactsAndPublishesEvent(t *testing.T) {
	db := newModuleTestDB(t, "CUS")
	contact := domain.Contact{AccountID: 91, Name: "Delete me", Email: "delete@example.test", ObjectVersion: 3}
	foreign := domain.Contact{AccountID: 92, Name: "Keep me"}
	if err := db.Create(&contact).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&foreign).Error; err != nil {
		t.Fatal(err)
	}
	label := domain.Label{AccountID: 91, Title: "delete-label", Color: "#111111"}
	if err := db.Create(&label).Error; err != nil {
		t.Fatal(err)
	}
	for _, record := range []any{
		&domain.ContactInbox{ContactID: contact.ID, InboxID: 7, SourceID: "delete-source"},
		&domain.ContactLabel{ContactID: contact.ID, LabelID: label.ID},
		&domain.ContactNote{AccountID: 91, ContactID: contact.ID, Content: "delete-note"},
	} {
		if err := db.Create(record).Error; err != nil {
			t.Fatal(err)
		}
	}
	cfg := businessConfig("CUS")
	newRuntime := func() Runtime {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CUS")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		return runtime
	}
	command := foundation.CommandEnvelope{CommandID: "contact-delete-1", CommandType: "cus.contact.delete.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "contact", ID: strconv.FormatUint(uint64(contact.ID), 10)}, Input: map[string]any{}, IdempotencyKey: "contact-delete-91", CorrelationID: "delete-correlation", CausationID: "delete-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "cus.contact.get.v1"}}
	call := func(payload foundation.CommandEnvelope) *httptest.ResponseRecorder {
		return performContractRequest(newRuntime().Handler(), "/contracts/commands/cus.contact.delete.v1", payload, func(request *http.Request) {
			request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
			request.Header.Set(headerCaller, "API")
			request.Header.Set(headerCorrelationID, payload.CorrelationID)
		})
	}
	for i := 0; i < 2; i++ {
		if response := call(command); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"deleted":true`) {
			t.Fatalf("contact delete status=%d body=%s", response.Code, response.Body.String())
		}
	}
	for _, model := range []any{&domain.ContactInbox{}, &domain.ContactLabel{}, &domain.ContactNote{}} {
		var count int64
		if err := db.Model(model).Where("contact_id = ?", contact.ID).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("owner relation %T count=%d err=%v", model, count, err)
		}
	}
	var deletedContact, foreignContacts, receipts, events int64
	_ = db.Model(&domain.Contact{}).Where("id = ?", contact.ID).Count(&deletedContact).Error
	_ = db.Model(&domain.Contact{}).Where("id = ? AND account_id = ?", foreign.ID, foreign.AccountID).Count(&foreignContacts).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%cus.contact.deleted.v1%").Count(&events).Error
	if deletedContact != 0 || foreignContacts != 1 || receipts != 1 || events != 1 {
		t.Fatalf("deleted=%d foreign=%d receipts=%d events=%d", deletedContact, foreignContacts, receipts, events)
	}
	foreignDelete := command
	foreignDelete.CommandID, foreignDelete.IdempotencyKey, foreignDelete.AccountID = "contact-delete-foreign", "contact-delete-foreign", 93
	if response := call(foreignDelete); response.Code != http.StatusNotFound {
		t.Fatalf("foreign delete status=%d body=%s", response.Code, response.Body.String())
	}
	for _, external := range []any{&domain.Conversation{}, &domain.Message{}, &domain.Ticket{}, &domain.Order{}, &domain.CampaignDelivery{}} {
		if db.Migrator().HasTable(external) {
			t.Fatalf("CUS delete runtime created foreign table %T", external)
		}
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

func TestCUSIndependentRuntimeRegistersCompleteDeclaredSurface(t *testing.T) {
	db := newModuleTestDB(t, "CUS")
	registry := NewContractRegistry("CUS")
	runtime := Runtime{Config: businessConfig("CUS"), DB: db, Contracts: registry}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	// module_contracts.json declares seven commands and five queries for CUS.
	if registry.Count() != 12 {
		t.Fatalf("CUS registered contracts=%d want=12", registry.Count())
	}
	if err := runtime.Readiness(); err != nil {
		t.Fatalf("CUS runtime readiness: %v", err)
	}
	for _, foreign := range []any{&domain.Account{}, &domain.Inbox{}, &domain.Conversation{}, &domain.Message{}, &domain.Ticket{}, &domain.Order{}, &domain.CampaignDelivery{}} {
		if db.Migrator().HasTable(foreign) {
			t.Fatalf("CUS runtime contains foreign table %T", foreign)
		}
	}
}

func TestMEDIndependentRuntimeRegistersCompleteDeclaredSurface(t *testing.T) {
	db := newModuleTestDB(t, "MED")
	registry := NewContractRegistry("MED")
	runtime := Runtime{Config: businessConfig("MED"), DB: db, Contracts: registry}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	// module_contracts.json declares five commands and four queries for MED.
	if registry.Count() != 9 {
		t.Fatalf("MED registered contracts=%d want=9", registry.Count())
	}
	if err := runtime.Readiness(); err != nil {
		t.Fatalf("MED runtime readiness: %v", err)
	}
	for _, foreign := range []any{&domain.Account{}, &domain.Attachment{}, &domain.Conversation{}, &domain.Message{}} {
		if db.Migrator().HasTable(foreign) {
			t.Fatalf("MED runtime contains foreign table %T", foreign)
		}
	}
}

func TestCONIndependentRuntimeRegistersCompleteDeclaredSurface(t *testing.T) {
	db := newModuleTestDB(t, "CON")
	cfg := businessConfig("CON")
	cfg.Dependencies = map[string]DependencyConfig{"TEN": {URL: "http://127.0.0.1:1", Token: "ten"}, "CHN": {URL: "http://127.0.0.1:2", Token: "chn"}, "CUS": {URL: "http://127.0.0.1:3", Token: "cus"}}
	registry := NewContractRegistry("CON")
	runtime := Runtime{Config: cfg, DB: db, Contracts: registry}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	// module_contracts.json declares seven commands and four queries for CON.
	if registry.Count() != 11 {
		t.Fatalf("CON registered contracts=%d want=11", registry.Count())
	}
	if err := runtime.Readiness(); err != nil {
		t.Fatalf("CON runtime readiness: %v", err)
	}
	for _, foreign := range []any{&domain.Account{}, &domain.Inbox{}, &domain.Contact{}, &domain.User{}, &domain.Team{}, &domain.Message{}} {
		if db.Migrator().HasTable(foreign) {
			t.Fatalf("CON runtime contains foreign table %T", foreign)
		}
	}
}

func TestMSGIndependentRuntimeRegistersCompleteDeclaredSurface(t *testing.T) {
	db := newModuleTestDB(t, "MSG")
	cfg := businessConfig("MSG")
	cfg.Dependencies = map[string]DependencyConfig{"TEN": {URL: "http://127.0.0.1:1", Token: "ten"}, "ENT": {URL: "http://127.0.0.1:2", Token: "ent"}, "CHN": {URL: "http://127.0.0.1:3", Token: "chn"}, "CON": {URL: "http://127.0.0.1:4", Token: "con"}, "MED": {URL: "http://127.0.0.1:5", Token: "med"}}
	registry := NewContractRegistry("MSG")
	runtime := Runtime{Config: cfg, DB: db, Contracts: registry}
	if err := runtime.RegisterBusinessContracts(); err != nil {
		t.Fatal(err)
	}
	if registry.Count() != 12 {
		t.Fatalf("MSG registered contracts=%d want=12", registry.Count())
	}
	if err := runtime.Readiness(); err != nil {
		t.Fatalf("MSG runtime readiness: %v", err)
	}
	for _, foreign := range []any{&domain.Account{}, &domain.Inbox{}, &domain.Contact{}, &domain.Conversation{}, &domain.User{}, &domain.Team{}} {
		if db.Migrator().HasTable(foreign) {
			t.Fatalf("MSG runtime contains foreign table %T", foreign)
		}
	}
}

func TestCONPriorityCommandAllowsOneWriteAcrossPostgresInstances(t *testing.T) {
	db := postgresModuleDB(t, "CON")
	conversation := domain.Conversation{AccountID: 91, InboxID: 11, ContactID: 21, DisplayID: 1, Status: domain.ConversationStatusOpen, Priority: domain.PriorityMedium, ObjectVersion: 1, LastActivityAt: time.Now().UTC()}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("CON")
	command := foundation.CommandEnvelope{CommandID: "capacity-conversation", CommandType: "con.priority.change.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "conversation", ID: strconv.FormatUint(uint64(conversation.ID), 10)}, Input: map[string]any{"priority": "urgent"}, IdempotencyKey: "capacity-conversation-91", CorrelationID: "capacity-conversation-correlation", CausationID: "capacity-conversation-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "con.conversation.get.v1"}}
	const instances = 8
	type result struct {
		status int
		body   string
	}
	results, start := make(chan result, instances), make(chan struct{})
	var wait sync.WaitGroup
	for index := 0; index < instances; index++ {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CON")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		wait.Add(1)
		go func(handler http.Handler) {
			defer wait.Done()
			<-start
			response := performContractRequest(handler, "/contracts/commands/con.priority.change.v1", command, func(request *http.Request) {
				request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
				request.Header.Set(headerCaller, "API")
				request.Header.Set(headerCorrelationID, command.CorrelationID)
			})
			results <- result{response.Code, response.Body.String()}
		}(runtime.Handler())
	}
	close(start)
	wait.Wait()
	close(results)
	for response := range results {
		if response.status != http.StatusOK || !strings.Contains(response.body, `"priority":"urgent"`) {
			t.Fatalf("concurrent priority status=%d body=%s", response.status, response.body)
		}
	}
	var stored domain.Conversation
	if err := db.First(&stored, conversation.ID).Error; err != nil {
		t.Fatal(err)
	}
	var receipts, events int64
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%con.conversation.changed.v1%").Count(&events).Error
	if stored.Priority != domain.PriorityUrgent || stored.ObjectVersion != 2 || receipts != 1 || events != 1 {
		t.Fatalf("priority=%s version=%d receipts=%d events=%d", stored.Priority, stored.ObjectVersion, receipts, events)
	}
}

func TestCONMixedVersionUpgradeRollbackPreservesConversationFact(t *testing.T) {
	db := newModuleTestDB(t, "CON")
	conversation := domain.Conversation{AccountID: 91, InboxID: 11, ContactID: 21, DisplayID: 1, Status: domain.ConversationStatusOpen, Priority: domain.PriorityMedium, ObjectVersion: 1, LastActivityAt: time.Now().UTC()}
	if err := db.Create(&conversation).Error; err != nil {
		t.Fatal(err)
	}
	newRuntime := func(version, release string, accepted map[string]bool) Runtime {
		cfg := businessConfig("CON")
		cfg.ContractVersion, cfg.ReleaseVersion, cfg.AcceptedVersions = version, release, accepted
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CON")}
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
			request.Header.Set(headerCorrelationID, "con-upgrade-correlation")
		})
	}
	command := foundation.CommandEnvelope{CommandID: "upgrade-conversation", CommandType: "con.priority.change.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "conversation", ID: strconv.FormatUint(uint64(conversation.ID), 10)}, Input: map[string]any{"priority": "high"}, IdempotencyKey: "upgrade-conversation-91", CorrelationID: "con-upgrade-correlation", CausationID: "con-upgrade-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "con.conversation.get.v1"}}
	v2 := newRuntime("v2", "release-2", map[string]bool{"v1": true, "v2": true})
	if response := call(v2, "v2", "/contracts/commands/con.priority.change.v1", command); response.Code != http.StatusOK {
		t.Fatalf("v2 write=%d body=%s", response.Code, response.Body.String())
	}
	v1 := newRuntime("v1", "release-1", map[string]bool{"v1": true})
	if response := call(v1, "v1", "/contracts/commands/con.priority.change.v1", command); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"priority":"high"`) {
		t.Fatalf("v1 replay=%d body=%s", response.Code, response.Body.String())
	}
	query := foundation.QueryEnvelope{QueryType: "con.conversation.get.v1", AccountID: 91, Actor: command.Actor, Filter: map[string]any{"id": conversation.ID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "priority"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	if response := call(v1, "v1", "/contracts/queries/con.conversation.get.v1", query); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"priority":"high"`) {
		t.Fatalf("v1 read=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(v1, "v2", "/contracts/queries/con.conversation.get.v1", query); response.Code != http.StatusBadRequest {
		t.Fatalf("rollback accepted v2=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMSGAnonymizeCommandAllowsOneWriteAcrossPostgresInstances(t *testing.T) {
	db := postgresModuleDB(t, "MSG")
	message := domain.Message{AccountID: 91, ConversationID: 11, SenderType: domain.SenderTypeContact, SenderID: 21, MessageType: domain.MessageTypeIncoming, Content: "capacity private", EchoID: "capacity-echo", Status: domain.MessageStatusSent}
	if err := db.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	cfg := businessConfig("MSG")
	command := foundation.CommandEnvelope{CommandID: "capacity-messages", CommandType: "msg.account_messages.anonymize.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "91"}, Input: map[string]any{}, IdempotencyKey: "capacity-messages-91", CorrelationID: "capacity-messages-correlation", CausationID: "capacity-messages-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "msg.account_deletion_status.get.v1"}}
	const instances = 8
	type result struct {
		status int
		body   string
	}
	results, start := make(chan result, instances), make(chan struct{})
	var wait sync.WaitGroup
	for index := 0; index < instances; index++ {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("MSG")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		wait.Add(1)
		go func(handler http.Handler) {
			defer wait.Done()
			<-start
			response := performContractRequest(handler, "/contracts/commands/msg.account_messages.anonymize.v1", command, func(request *http.Request) {
				request.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
				request.Header.Set(headerCaller, "API")
				request.Header.Set(headerCorrelationID, command.CorrelationID)
			})
			results <- result{response.Code, response.Body.String()}
		}(runtime.Handler())
	}
	close(start)
	wait.Wait()
	close(results)
	for response := range results {
		if response.status != http.StatusOK || !strings.Contains(response.body, `"status":"anonymized"`) {
			t.Fatalf("concurrent anonymize=%d body=%s", response.status, response.body)
		}
	}
	var stored domain.Message
	if err := db.First(&stored, message.ID).Error; err != nil {
		t.Fatal(err)
	}
	var receipts, events int64
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%anonymized%").Count(&events).Error
	if stored.Content != "" || stored.SenderID != 0 || receipts != 1 || events != 1 {
		t.Fatalf("stored=%+v receipts=%d events=%d", stored, receipts, events)
	}
}

func TestMSGMixedVersionUpgradeRollbackPreservesMessageFact(t *testing.T) {
	db := newModuleTestDB(t, "MSG")
	message := domain.Message{AccountID: 91, ConversationID: 11, SenderType: domain.SenderTypeUser, SenderID: 7, MessageType: domain.MessageTypeOutgoing, Content: "before", Status: domain.MessageStatusSent}
	if err := db.Create(&message).Error; err != nil {
		t.Fatal(err)
	}
	newRuntime := func(version, release string, accepted map[string]bool) Runtime {
		cfg := businessConfig("MSG")
		cfg.ContractVersion, cfg.ReleaseVersion, cfg.AcceptedVersions = version, release, accepted
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("MSG")}
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
			request.Header.Set(headerCorrelationID, "msg-upgrade-correlation")
		})
	}
	command := foundation.CommandEnvelope{CommandID: "upgrade-message", CommandType: "msg.message.update.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "User", ID: "7", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "message", ID: strconv.FormatUint(uint64(message.ID), 10)}, Input: map[string]any{"operation": "edit", "content": "after"}, IdempotencyKey: "upgrade-message-91", CorrelationID: "msg-upgrade-correlation", CausationID: "msg-upgrade-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "msg.message.get.v1"}}
	v2 := newRuntime("v2", "release-2", map[string]bool{"v1": true, "v2": true})
	if response := call(v2, "v2", "/contracts/commands/msg.message.update.v1", command); response.Code != http.StatusOK {
		t.Fatalf("v2 write=%d body=%s", response.Code, response.Body.String())
	}
	v1 := newRuntime("v1", "release-1", map[string]bool{"v1": true})
	if response := call(v1, "v1", "/contracts/commands/msg.message.update.v1", command); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"content":"after"`) {
		t.Fatalf("v1 replay=%d body=%s", response.Code, response.Body.String())
	}
	query := foundation.QueryEnvelope{QueryType: "msg.message.get.v1", AccountID: 91, Actor: command.Actor, Filter: map[string]any{"id": message.ID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "content"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	if response := call(v1, "v1", "/contracts/queries/msg.message.get.v1", query); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"content":"after"`) {
		t.Fatalf("v1 read=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(v1, "v2", "/contracts/queries/msg.message.get.v1", query); response.Code != http.StatusBadRequest {
		t.Fatalf("rollback accepted v2=%d body=%s", response.Code, response.Body.String())
	}
}

func TestMEDUploadCommandAllowsOneWriteAcrossPostgresInstances(t *testing.T) {
	db := postgresModuleDB(t, "MED")
	cfg := businessConfig("MED")
	command := foundation.CommandEnvelope{CommandID: "capacity-media", CommandType: "med.upload.create.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "91"}, Input: map[string]any{"file_name": "capacity.pdf", "content_type": "application/pdf", "file_size": 4096, "checksum": "sha256:capacity"}, IdempotencyKey: "capacity-media-91", CorrelationID: "capacity-media-correlation", CausationID: "capacity-media-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "med.asset.get.v1"}}
	const instances = 8
	type result struct {
		status int
		body   string
	}
	results := make(chan result, instances)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for i := 0; i < instances; i++ {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("MED")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		wait.Add(1)
		go func(handler http.Handler) {
			defer wait.Done()
			<-start
			response := performContractRequest(handler, "/contracts/commands/med.upload.create.v1", command, func(request *http.Request) {
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
		if response.status != http.StatusOK || !strings.Contains(response.body, "capacity.pdf") {
			t.Fatalf("concurrent media status=%d body=%s", response.status, response.body)
		}
	}
	var assets, uploads, receipts int64
	_ = db.Model(&domain.MediaAsset{}).Where("account_id = ? AND file_name = ?", 91, "capacity.pdf").Count(&assets).Error
	_ = db.Model(&domain.UploadSession{}).Where("account_id = ?", 91).Count(&uploads).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	if assets != 1 || uploads != 1 || receipts != 1 {
		t.Fatalf("assets=%d uploads=%d receipts=%d", assets, uploads, receipts)
	}
}

func TestMEDMixedVersionUpgradeRollbackPreservesAssetFact(t *testing.T) {
	db := newModuleTestDB(t, "MED")
	newRuntime := func(version, release string, accepted map[string]bool) Runtime {
		cfg := businessConfig("MED")
		cfg.ContractVersion, cfg.ReleaseVersion, cfg.AcceptedVersions = version, release, accepted
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("MED")}
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
			request.Header.Set(headerCorrelationID, "med-upgrade-correlation")
		})
	}
	command := foundation.CommandEnvelope{CommandID: "upgrade-media", CommandType: "med.upload.create.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "91"}, Input: map[string]any{"file_name": "rollback.pdf", "content_type": "application/pdf", "file_size": 2048}, IdempotencyKey: "upgrade-media-91", CorrelationID: "med-upgrade-correlation", CausationID: "med-upgrade-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "med.asset.get.v1"}}
	v2 := newRuntime("v2", "release-2", map[string]bool{"v1": true, "v2": true})
	response := call(v2, "v2", "/contracts/commands/med.upload.create.v1", command)
	if response.Code != http.StatusOK {
		t.Fatalf("v2 write status=%d body=%s", response.Code, response.Body.String())
	}
	var created struct {
		Data mediaUploadView `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil || created.Data.Asset.ID == 0 {
		t.Fatalf("decode created asset: %v body=%s", err, response.Body.String())
	}
	v1 := newRuntime("v1", "release-1", map[string]bool{"v1": true})
	if response := call(v1, "v1", "/contracts/commands/med.upload.create.v1", command); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "rollback.pdf") {
		t.Fatalf("v1 replay status=%d body=%s", response.Code, response.Body.String())
	}
	query := foundation.QueryEnvelope{QueryType: "med.asset.get.v1", AccountID: 91, Actor: command.Actor, Filter: map[string]any{"id": created.Data.Asset.ID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "status"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	if response := call(v1, "v1", "/contracts/queries/med.asset.get.v1", query); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "rollback.pdf") {
		t.Fatalf("v1 read status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(v1, "v2", "/contracts/queries/med.asset.get.v1", query); response.Code != http.StatusBadRequest {
		t.Fatalf("rolled-back runtime accepted v2 status=%d body=%s", response.Code, response.Body.String())
	}
	var assets, uploads, receipts int64
	_ = db.Model(&domain.MediaAsset{}).Count(&assets).Error
	_ = db.Model(&domain.UploadSession{}).Count(&uploads).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	if assets != 1 || uploads != 1 || receipts != 1 {
		t.Fatalf("rollback assets=%d uploads=%d receipts=%d", assets, uploads, receipts)
	}
}

func TestCUSContactCommandAllowsOneWriteAcrossPostgresInstances(t *testing.T) {
	db := postgresModuleDB(t, "CUS")
	cfg := businessConfig("CUS")
	command := foundation.CommandEnvelope{CommandID: "capacity-contact", CommandType: "cus.contact.create.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "91"}, Input: map[string]any{"name": "Created once", "identifier": "capacity-contact-91"}, IdempotencyKey: "capacity-contact-91", CorrelationID: "capacity-contact-correlation", CausationID: "capacity-contact-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "cus.contact.get.v1"}}
	const instances = 8
	type result struct {
		status int
		body   string
	}
	results := make(chan result, instances)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for i := 0; i < instances; i++ {
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CUS")}
		if err := runtime.RegisterBusinessContracts(); err != nil {
			t.Fatal(err)
		}
		wait.Add(1)
		go func(handler http.Handler) {
			defer wait.Done()
			<-start
			response := performContractRequest(handler, "/contracts/commands/cus.contact.create.v1", command, func(request *http.Request) {
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
		if response.status != http.StatusOK || !strings.Contains(response.body, "capacity-contact-91") {
			t.Fatalf("concurrent contact status=%d body=%s", response.status, response.body)
		}
	}
	var contacts, receipts, events int64
	_ = db.Model(&domain.Contact{}).Where("account_id = ? AND identifier = ?", 91, "capacity-contact-91").Count(&contacts).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%cus.contact.created.v1%").Count(&events).Error
	if contacts != 1 || receipts != 1 || events != 1 {
		t.Fatalf("contacts=%d receipts=%d events=%d", contacts, receipts, events)
	}
}

func TestCUSMixedVersionUpgradeRollbackPreservesContactFact(t *testing.T) {
	db := newModuleTestDB(t, "CUS")
	newRuntime := func(version, release string, accepted map[string]bool) Runtime {
		cfg := businessConfig("CUS")
		cfg.ContractVersion, cfg.ReleaseVersion, cfg.AcceptedVersions = version, release, accepted
		runtime := Runtime{Config: cfg, DB: db, Contracts: NewContractRegistry("CUS")}
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
			request.Header.Set(headerCorrelationID, "cus-upgrade-correlation")
		})
	}
	command := foundation.CommandEnvelope{CommandID: "upgrade-contact", CommandType: "cus.contact.create.v1", AccountID: 91, Actor: foundation.ContractActor{Type: "System", ID: "1", AuthSource: "module"}, Target: foundation.ContractTarget{Type: "account", ID: "91"}, Input: map[string]any{"name": "Survives rollback", "identifier": "rollback-contact-91"}, IdempotencyKey: "upgrade-contact-91", CorrelationID: "cus-upgrade-correlation", CausationID: "cus-upgrade-cause", RequestedAt: time.Now().UTC(), TimeoutPolicy: foundation.TimeoutPolicy{TimeoutMS: 1000, OnTimeout: "query", StatusQuery: "cus.contact.get.v1"}}
	v2 := newRuntime("v2", "release-2", map[string]bool{"v1": true, "v2": true})
	response := call(v2, "v2", "/contracts/commands/cus.contact.create.v1", command)
	if response.Code != http.StatusOK {
		t.Fatalf("v2 write status=%d body=%s", response.Code, response.Body.String())
	}
	var created struct {
		Data contactView `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil || created.Data.ID == 0 {
		t.Fatalf("decode created contact: %v body=%s", err, response.Body.String())
	}
	v1 := newRuntime("v1", "release-1", map[string]bool{"v1": true})
	if response := call(v1, "v1", "/contracts/commands/cus.contact.create.v1", command); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "rollback-contact-91") {
		t.Fatalf("v1 replay status=%d body=%s", response.Code, response.Body.String())
	}
	query := foundation.QueryEnvelope{QueryType: "cus.contact.get.v1", AccountID: 91, Actor: command.Actor, Filter: map[string]any{"id": created.Data.ID}, Sort: []string{"id"}, Page: "1", Fields: []string{"id", "identifier"}, Consistency: "strong", RequestedAt: time.Now().UTC()}
	if response := call(v1, "v1", "/contracts/queries/cus.contact.get.v1", query); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "rollback-contact-91") {
		t.Fatalf("v1 read status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(v1, "v2", "/contracts/queries/cus.contact.get.v1", query); response.Code != http.StatusBadRequest {
		t.Fatalf("rolled-back runtime accepted v2 status=%d body=%s", response.Code, response.Body.String())
	}
	var contacts, receipts, events int64
	_ = db.Model(&domain.Contact{}).Count(&contacts).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ?", "command_receipt").Count(&receipts).Error
	_ = db.Model(&domain.LocalChangeJournal{}).Where("action = ? AND after_state LIKE ?", "event_outbox", "%cus.contact.created.v1%").Count(&events).Error
	if contacts != 1 || receipts != 1 || events != 1 {
		t.Fatalf("rollback contacts=%d receipts=%d events=%d", contacts, receipts, events)
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
