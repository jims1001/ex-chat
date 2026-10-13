package test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Exercise the real router, authentication, and tenant middleware. All user
// tokens come from sign_up; the database holds real account, inbox, contact,
// conversation, assistant and binding records. No provider is called.
type boundAssistantAPIFixture struct {
	db     *gorm.DB
	router http.Handler
	serial uint
}

type boundAssistantAPITenant struct {
	id    uint
	token string
}

func newBoundAssistantAPIFixture(t *testing.T) *boundAssistantAPIFixture {
	t.Helper()
	t.Chdir(t.TempDir())
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		Environment: "test", DBDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "bound-assistant.sqlite"),
		JWTSecret: "bound-assistant-api-regression-secret", JWTExpirationHours: 1,
	}
	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("initialize bound assistant API: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return &boundAssistantAPIFixture{db: db, router: router.SetupRouter(cfg, db, ws.NewHub())}
}

func (f *boundAssistantAPIFixture) create(t *testing.T, value any) {
	t.Helper()
	if err := f.db.Create(value).Error; err != nil {
		t.Fatalf("create %T: %v", value, err)
	}
}

func (f *boundAssistantAPIFixture) request(token, method, path string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	out := httptest.NewRecorder()
	f.router.ServeHTTP(out, req)
	return out
}

func (f *boundAssistantAPIFixture) tenant(t *testing.T, name string) boundAssistantAPITenant {
	t.Helper()
	body, err := json.Marshal(map[string]string{
		"account_name": name, "name": "Bound assistant " + name,
		"email": name + "@bound-assistant.example", "password": "Password123!",
	})
	if err != nil {
		t.Fatal(err)
	}
	out := f.request("", http.MethodPost, "/auth/sign_up", body)
	if out.Code != http.StatusCreated {
		t.Fatalf("sign up: status=%d body=%s", out.Code, out.Body.String())
	}
	var auth struct {
		Data struct {
			Token    string           `json:"token"`
			Accounts []domain.Account `json:"accounts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &auth); err != nil {
		t.Fatal(err)
	}
	if auth.Data.Token == "" || len(auth.Data.Accounts) != 1 || auth.Data.Accounts[0].ID == 0 {
		t.Fatal("sign_up did not return an authenticated account")
	}
	return boundAssistantAPITenant{id: auth.Data.Accounts[0].ID, token: auth.Data.Token}
}

func (a boundAssistantAPITenant) path(suffix string) string {
	return fmt.Sprintf("/api/v1/accounts/%d%s", a.id, suffix)
}

func (f *boundAssistantAPIFixture) inboxConversation(t *testing.T, a boundAssistantAPITenant) (domain.Inbox, domain.Conversation) {
	t.Helper()
	f.serial++
	inbox := domain.Inbox{AccountID: a.id, Name: "Private inbox", ChannelType: domain.ChannelWebWidget, WebsiteToken: fmt.Sprintf("bound-assistant-inbox-%d", f.serial)}
	f.create(t, &inbox)
	contact := domain.Contact{AccountID: a.id, Name: "Private customer"}
	f.create(t, &contact)
	conv := domain.Conversation{AccountID: a.id, InboxID: inbox.ID, ContactID: contact.ID, DisplayID: f.serial, Status: domain.ConversationStatusOpen}
	f.create(t, &conv)
	return inbox, conv
}

func (f *boundAssistantAPIFixture) assistant(t *testing.T, a boundAssistantAPITenant, inbox domain.Inbox) domain.CaptainAssistant {
	t.Helper()
	assistant := domain.CaptainAssistant{
		AccountID: a.id, Name: "Bound support assistant", Description: "private-assistant-description",
		SystemPrompt: "private-assistant-system-prompt", Model: "private-assistant-model", Status: "active",
		Config:             `{"provider_key":"private-assistant-provider-key","history":"private-assistant-history"}`,
		ResponseGuidelines: "private-assistant-guidelines", Guardrails: "private-assistant-guardrails",
	}
	f.create(t, &assistant)
	f.create(t, &domain.CaptainInbox{AccountID: a.id, CaptainAssistantID: assistant.ID, InboxID: inbox.ID})
	return assistant
}

func boundAssistantAPIAssertJSON(t *testing.T, out *httptest.ResponseRecorder, status int, want any) {
	t.Helper()
	if out.Code != status {
		t.Fatalf("status=%d, want=%d, body=%s", out.Code, status, out.Body.String())
	}
	var got any
	if err := json.Unmarshal(out.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v, body=%s", err, out.Body.String())
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("response must contain only the documented fields: got=%s want=%v", out.Body.String(), want)
	}
}

func TestAICBoundAssistantAPIResponseContracts(t *testing.T) {
	f := newBoundAssistantAPIFixture(t)
	a := f.tenant(t, "response-contract")
	inbox, conv := f.inboxConversation(t, a)
	assistant := f.assistant(t, a, inbox)
	wantAssistant := map[string]any{"id": float64(assistant.ID), "name": assistant.Name}

	t.Run("documented_conversation_route", func(t *testing.T) {
		out := f.request(a.token, http.MethodGet, a.path(fmt.Sprintf("/conversations/%d/inbox_assistant", conv.ID)), nil)
		boundAssistantAPIAssertJSON(t, out, http.StatusOK, map[string]any{"assistant": wantAssistant})
	})
	for _, path := range []string{
		fmt.Sprintf("/inboxes/%d/assistant", inbox.ID),
		fmt.Sprintf("/conversations/%d/assistant", conv.ID),
	} {
		t.Run(path, func(t *testing.T) {
			out := f.request(a.token, http.MethodGet, a.path(path), nil)
			boundAssistantAPIAssertJSON(t, out, http.StatusOK, map[string]any{"success": true, "data": wantAssistant})
		})
	}

	t.Run("uses_current_conversation_inbox", func(t *testing.T) {
		nextInbox, _ := f.inboxConversation(t, a)
		nextAssistant := f.assistant(t, a, nextInbox)
		nextAssistant.Name = "Current inbox assistant"
		if err := f.db.Model(&nextAssistant).Update("name", nextAssistant.Name).Error; err != nil {
			t.Fatal(err)
		}
		if err := f.db.Model(&conv).Update("inbox_id", nextInbox.ID).Error; err != nil {
			t.Fatal(err)
		}
		wantCurrent := map[string]any{"id": float64(nextAssistant.ID), "name": nextAssistant.Name}
		out := f.request(a.token, http.MethodGet, a.path(fmt.Sprintf("/conversations/%d/inbox_assistant", conv.ID)), nil)
		boundAssistantAPIAssertJSON(t, out, http.StatusOK, map[string]any{"assistant": wantCurrent})
		out = f.request(a.token, http.MethodGet, a.path(fmt.Sprintf("/conversations/%d/assistant", conv.ID)), nil)
		boundAssistantAPIAssertJSON(t, out, http.StatusOK, map[string]any{"success": true, "data": wantCurrent})
	})
}

func boundAssistantAPIRoutes(inboxID, conversationID uint) []string {
	return []string{
		fmt.Sprintf("/conversations/%d/inbox_assistant", conversationID),
		fmt.Sprintf("/conversations/%d/assistant", conversationID),
		fmt.Sprintf("/inboxes/%d/assistant", inboxID),
	}
}

func boundAssistantAPIAssertError(t *testing.T, out *httptest.ResponseRecorder, status int) {
	t.Helper()
	if out.Code != status {
		t.Fatalf("status=%d, want=%d, body=%s", out.Code, status, out.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(out.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode error response: %v, body=%s", err, out.Body.String())
	}
	message, ok := got["error"].(string)
	if len(got) != 2 || got["success"] != false || !ok || message == "" {
		t.Fatalf("expected error envelope without an assistant payload: %s", out.Body.String())
	}
	for _, forbidden := range []string{
		"private-assistant", "system_prompt", "response_guidelines", "guardrails", "provider_key",
		"bound-assistant-inbox", "captain_assistants", "captain_inboxes", "SELECT", "database failure",
	} {
		if strings.Contains(out.Body.String(), forbidden) {
			t.Errorf("error response exposed %q: %s", forbidden, out.Body.String())
		}
	}
}

func (f *boundAssistantAPIFixture) assertUnbound(t *testing.T, a boundAssistantAPITenant, inboxID, conversationID uint) {
	t.Helper()
	for i, suffix := range boundAssistantAPIRoutes(inboxID, conversationID) {
		out := f.request(a.token, http.MethodGet, a.path(suffix), nil)
		if i == 0 {
			boundAssistantAPIAssertJSON(t, out, http.StatusOK, map[string]any{"assistant": nil})
		} else {
			boundAssistantAPIAssertError(t, out, http.StatusNotFound)
		}
	}
}

func (f *boundAssistantAPIFixture) assertBindingError(t *testing.T, a boundAssistantAPITenant, inboxID, conversationID uint) {
	t.Helper()
	for _, suffix := range boundAssistantAPIRoutes(inboxID, conversationID) {
		out := f.request(a.token, http.MethodGet, a.path(suffix), nil)
		boundAssistantAPIAssertError(t, out, http.StatusInternalServerError)
	}
}

func TestAICBoundAssistantAPIUnboundAndMalformedReferences(t *testing.T) {
	f := newBoundAssistantAPIFixture(t)
	a := f.tenant(t, "unbound-owner")
	b := f.tenant(t, "unbound-foreign")
	foreignInbox, _ := f.inboxConversation(t, b)
	foreignAssistant := f.assistant(t, b, foreignInbox)

	t.Run("no_binding", func(t *testing.T) {
		inbox, conv := f.inboxConversation(t, a)
		f.assertUnbound(t, a, inbox.ID, conv.ID)
	})
	t.Run("binding_references_missing_assistant", func(t *testing.T) {
		inbox, conv := f.inboxConversation(t, a)
		f.create(t, &domain.CaptainInbox{AccountID: a.id, InboxID: inbox.ID, CaptainAssistantID: 999999})
		f.assertBindingError(t, a, inbox.ID, conv.ID)
	})
	t.Run("own_binding_references_foreign_assistant", func(t *testing.T) {
		inbox, conv := f.inboxConversation(t, a)
		f.create(t, &domain.CaptainInbox{AccountID: a.id, InboxID: inbox.ID, CaptainAssistantID: foreignAssistant.ID})
		f.assertBindingError(t, a, inbox.ID, conv.ID)
	})
	t.Run("duplicate_own_bindings", func(t *testing.T) {
		inbox, conv := f.inboxConversation(t, a)
		f.assistant(t, a, inbox)
		f.assistant(t, a, inbox)
		f.assertBindingError(t, a, inbox.ID, conv.ID)
	})
	t.Run("foreign_binding_references_own_assistant", func(t *testing.T) {
		boundInbox, _ := f.inboxConversation(t, a)
		ownAssistant := f.assistant(t, a, boundInbox)
		inbox, conv := f.inboxConversation(t, a)
		f.create(t, &domain.CaptainInbox{AccountID: b.id, InboxID: inbox.ID, CaptainAssistantID: ownAssistant.ID})
		f.assertUnbound(t, a, inbox.ID, conv.ID)
	})
	t.Run("conversation_foreign_inbox_cannot_expose_foreign_binding", func(t *testing.T) {
		_, conv := f.inboxConversation(t, a)
		if err := f.db.Model(&conv).Update("inbox_id", foreignInbox.ID).Error; err != nil {
			t.Fatal(err)
		}
		out := f.request(a.token, http.MethodGet, a.path(fmt.Sprintf("/conversations/%d/inbox_assistant", conv.ID)), nil)
		boundAssistantAPIAssertError(t, out, http.StatusNotFound)
		out = f.request(a.token, http.MethodGet, a.path(fmt.Sprintf("/conversations/%d/assistant", conv.ID)), nil)
		boundAssistantAPIAssertError(t, out, http.StatusNotFound)
	})
}

func TestAICBoundAssistantAPIExistingGatewayAuthorization(t *testing.T) {
	f := newBoundAssistantAPIFixture(t)
	a := f.tenant(t, "gateway-owner")
	b := f.tenant(t, "gateway-foreign")
	inbox, conv := f.inboxConversation(t, a)
	f.assistant(t, a, inbox)
	foreignInbox, foreignConv := f.inboxConversation(t, b)
	f.assistant(t, b, foreignInbox)

	for _, suffix := range boundAssistantAPIRoutes(inbox.ID, conv.ID) {
		t.Run("authentication"+suffix, func(t *testing.T) {
			out := f.request("", http.MethodGet, a.path(suffix), nil)
			boundAssistantAPIAssertError(t, out, http.StatusUnauthorized)
			out = f.request("invalid-token", http.MethodGet, a.path(suffix), nil)
			boundAssistantAPIAssertError(t, out, http.StatusUnauthorized)
		})
		t.Run("account_membership"+suffix, func(t *testing.T) {
			out := f.request(b.token, http.MethodGet, a.path(suffix), nil)
			boundAssistantAPIAssertError(t, out, http.StatusForbidden)
		})
	}
	for _, suffix := range boundAssistantAPIRoutes(foreignInbox.ID, foreignConv.ID) {
		t.Run("foreign_record_under_own_account"+suffix, func(t *testing.T) {
			out := f.request(a.token, http.MethodGet, a.path(suffix), nil)
			boundAssistantAPIAssertError(t, out, http.StatusNotFound)
		})
	}
	for _, suffix := range boundAssistantAPIRoutes(999999, 999999) {
		t.Run("missing_record"+suffix, func(t *testing.T) {
			out := f.request(a.token, http.MethodGet, a.path(suffix), nil)
			boundAssistantAPIAssertError(t, out, http.StatusNotFound)
		})
	}
	for _, suffix := range []string{"/conversations/invalid/inbox_assistant", "/conversations/invalid/assistant", "/inboxes/invalid/assistant"} {
		t.Run("invalid_id"+suffix, func(t *testing.T) {
			out := f.request(a.token, http.MethodGet, a.path(suffix), nil)
			boundAssistantAPIAssertError(t, out, http.StatusBadRequest)
		})
	}

	// Removing the CON or CHN owner record must still fail before consulting an
	// AIC binding. This checks the existing gateway's resource authorization.
	t.Run("deleted_conversation_is_not_authorized_by_binding", func(t *testing.T) {
		if err := f.db.Delete(&conv).Error; err != nil {
			t.Fatal(err)
		}
		for _, suffix := range boundAssistantAPIRoutes(inbox.ID, conv.ID)[:2] {
			out := f.request(a.token, http.MethodGet, a.path(suffix), nil)
			boundAssistantAPIAssertError(t, out, http.StatusNotFound)
		}
	})
	t.Run("deleted_inbox_is_not_authorized_by_binding", func(t *testing.T) {
		if err := f.db.Delete(&inbox).Error; err != nil {
			t.Fatal(err)
		}
		out := f.request(a.token, http.MethodGet, a.path(fmt.Sprintf("/inboxes/%d/assistant", inbox.ID)), nil)
		boundAssistantAPIAssertError(t, out, http.StatusNotFound)
	})
}

func TestAICBoundAssistantAPISanitizesRepositoryErrors(t *testing.T) {
	f := newBoundAssistantAPIFixture(t)
	a := f.tenant(t, "repository-error")
	inbox, conv := f.inboxConversation(t, a)
	f.assistant(t, a, inbox)

	const callbackName = "bound_assistant_api_failure"
	queries := 0
	failAssistantRead := func(tx *gorm.DB) {
		if strings.Contains(tx.Statement.Table, "captain_") || strings.Contains(tx.Statement.SQL.String(), "captain_") {
			queries++
			_ = tx.AddError(errors.New("database failure: SELECT config, system_prompt FROM captain_assistants; private-assistant-provider-key"))
		}
	}
	if err := f.db.Callback().Query().Before("gorm:query").Register(callbackName, failAssistantRead); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Callback().Row().Before("gorm:row").Register(callbackName, failAssistantRead); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = f.db.Callback().Query().Remove(callbackName)
		_ = f.db.Callback().Row().Remove(callbackName)
	})
	for _, suffix := range boundAssistantAPIRoutes(inbox.ID, conv.ID) {
		t.Run(suffix, func(t *testing.T) {
			before := queries
			out := f.request(a.token, http.MethodGet, a.path(suffix), nil)
			if queries == before {
				t.Fatal("request did not exercise the failing AIC repository read")
			}
			boundAssistantAPIAssertError(t, out, http.StatusInternalServerError)
		})
	}
}

func TestAICBoundAssistantAPIRequiresCurrentInboxOwner(t *testing.T) {
	f := newBoundAssistantAPIFixture(t)
	a := f.tenant(t, "current-inbox-owner")
	b := f.tenant(t, "current-inbox-foreign")
	foreignInbox, _ := f.inboxConversation(t, b)
	controlInbox, controlConv := f.inboxConversation(t, a)
	controlAssistant := f.assistant(t, a, controlInbox)

	const callbackName = "bound_assistant_api_owner_read_counter"
	queries := 0
	if err := f.db.Callback().Row().Before("gorm:row").Register(callbackName, func(tx *gorm.DB) {
		if strings.Contains(tx.Statement.SQL.String(), "captain_inboxes") {
			queries++
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.db.Callback().Row().Remove(callbackName) })
	// Verify the counter observes an actual successful AIC read before using it
	// to show that rejected CHN ownership never reaches the surviving binding.
	out := f.request(a.token, http.MethodGet, a.path(fmt.Sprintf("/conversations/%d/inbox_assistant", controlConv.ID)), nil)
	boundAssistantAPIAssertJSON(t, out, http.StatusOK, map[string]any{
		"assistant": map[string]any{"id": float64(controlAssistant.ID), "name": controlAssistant.Name},
	})
	if queries == 0 {
		t.Fatal("positive control did not observe an AIC binding read")
	}

	t.Run("deleted_current_inbox_with_surviving_binding", func(t *testing.T) {
		inbox, conv := f.inboxConversation(t, a)
		f.assistant(t, a, inbox)
		if err := f.db.Delete(&inbox).Error; err != nil {
			t.Fatal(err)
		}
		for _, suffix := range boundAssistantAPIRoutes(inbox.ID, conv.ID)[:2] {
			t.Run(suffix, func(t *testing.T) {
				before := queries
				out := f.request(a.token, http.MethodGet, a.path(suffix), nil)
				if queries != before {
					t.Errorf("deleted current inbox reached AIC binding read: before=%d after=%d", before, queries)
				}
				boundAssistantAPIAssertError(t, out, http.StatusNotFound)
			})
		}
	})
	t.Run("foreign_current_inbox_with_same_account_stale_binding", func(t *testing.T) {
		inbox, conv := f.inboxConversation(t, a)
		assistant := f.assistant(t, a, inbox)
		if err := f.db.Model(&conv).Update("inbox_id", foreignInbox.ID).Error; err != nil {
			t.Fatal(err)
		}
		f.create(t, &domain.CaptainInbox{AccountID: a.id, InboxID: foreignInbox.ID, CaptainAssistantID: assistant.ID})
		for _, suffix := range boundAssistantAPIRoutes(foreignInbox.ID, conv.ID)[:2] {
			t.Run(suffix, func(t *testing.T) {
				before := queries
				out := f.request(a.token, http.MethodGet, a.path(suffix), nil)
				if queries != before {
					t.Errorf("foreign current inbox reached AIC binding read: before=%d after=%d", before, queries)
				}
				boundAssistantAPIAssertError(t, out, http.StatusNotFound)
			})
		}
	})
}
