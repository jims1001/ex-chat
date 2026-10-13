package test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/auth"
	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/router"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// These tests use the production router, authentication and tenant middleware.
// Every positive fixture has real account, IAM user, inbox, contact and CON
// owners; malformed historical references are introduced explicitly below.
type callReadFixture struct {
	db     *gorm.DB
	router http.Handler
	cfg    *config.Config
	serial uint
}

type callReadScope struct {
	account domain.Account
	actor   domain.User
	other   domain.User
	member  domain.AccountUser
	role    *domain.CustomRole
	inbox   domain.Inbox
	contact domain.Contact
	team    domain.Team
	token   string
}

type callReadRole struct {
	name        string
	accountRole string
	globalRole  string
	permissions string
	byName      bool
	allowed     []string
}

func newCallReadFixture(t *testing.T) *callReadFixture {
	t.Helper()
	t.Chdir(t.TempDir())
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{
		Environment: "test", DBDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "calls.sqlite"),
		JWTSecret: "call_read_regression_secret_32_bytes", JWTExpirationHours: 1,
	}
	db, err := database.InitDB(cfg)
	if err != nil {
		t.Fatalf("initialize call-read database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get call-read database handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return &callReadFixture{db: db, cfg: cfg, router: router.SetupRouter(cfg, db, ws.NewHub()), serial: 1000}
}

func (f *callReadFixture) create(t *testing.T, value any) {
	t.Helper()
	if err := f.db.Create(value).Error; err != nil {
		t.Fatalf("create %T fixture: %v", value, err)
	}
}

func (f *callReadFixture) update(t *testing.T, model any, id uint, fields map[string]any) {
	t.Helper()
	result := f.db.Model(model).Where("id = ?", id).Updates(fields)
	if result.Error != nil || result.RowsAffected != 1 {
		t.Fatalf("update %T %d: rows=%d error=%v", model, id, result.RowsAffected, result.Error)
	}
}

func (f *callReadFixture) user(t *testing.T, role string) domain.User {
	t.Helper()
	f.serial++
	now := time.Now().UTC()
	user := domain.User{
		ID: f.serial, Name: fmt.Sprintf("Call agent %d", f.serial),
		Email: fmt.Sprintf("call-agent-%d@example.test", f.serial), Role: role,
		Type: domain.UserTypeUser, ConfirmedAt: &now,
	}
	f.create(t, &user)
	return user
}

func (f *callReadFixture) scope(t *testing.T, role callReadRole, base string) *callReadScope {
	t.Helper()
	s := &callReadScope{account: domain.Account{Name: "Call-read " + t.Name()}}
	f.create(t, &s.account)
	globalRole := role.globalRole
	if globalRole == "" {
		globalRole = domain.RoleAgent
	}
	s.actor = f.user(t, globalRole)
	s.other = f.user(t, domain.RoleAgent)
	s.member = domain.AccountUser{AccountID: s.account.ID, UserID: s.actor.ID, Role: role.accountRole}
	if s.member.Role == "" {
		s.member.Role = domain.RoleAgent
	}
	if role.permissions != "" {
		s.role = &domain.CustomRole{AccountID: s.account.ID, Name: "Call support", Permissions: role.permissions}
		f.create(t, s.role)
		if role.byName {
			s.member.Role = s.role.Name
		} else {
			s.member.CustomRoleID = &s.role.ID
		}
	}
	f.create(t, &s.member)
	f.create(t, &domain.AccountUser{AccountID: s.account.ID, UserID: s.other.ID, Role: domain.RoleAgent})
	if s.actor.ID == s.member.ID {
		t.Fatal("fixture must distinguish IAM User.ID from AccountUser.ID")
	}
	s.inbox = domain.Inbox{AccountID: s.account.ID, Name: "Call inbox", ChannelType: "Channel::Whatsapp", WebsiteToken: fmt.Sprintf("calls-%d", s.account.ID)}
	s.contact = domain.Contact{AccountID: s.account.ID, Name: fmt.Sprintf("Private caller %d", s.account.ID), PhoneNumber: "+15551234567", AvatarURL: "https://example.test/avatar.png"}
	s.team = domain.Team{AccountID: s.account.ID, Name: "Call team"}
	f.create(t, &s.inbox)
	f.create(t, &s.contact)
	f.create(t, &s.team)
	if base == "inbox" || base == "both" {
		f.create(t, &domain.InboxMember{InboxID: s.inbox.ID, UserID: s.actor.ID})
	}
	if base == "team" || base == "both" {
		f.create(t, &domain.TeamMember{TeamID: s.team.ID, UserID: s.actor.ID})
	}
	var err error
	s.token, err = auth.GenerateToken(&s.actor, f.cfg.JWTSecret, f.cfg.JWTExpirationHours)
	if err != nil {
		t.Fatalf("generate call-read token: %v", err)
	}
	return s
}

func (f *callReadFixture) conversation(t *testing.T, s *callReadScope, state string) domain.Conversation {
	t.Helper()
	f.serial++
	conv := domain.Conversation{
		AccountID: s.account.ID, InboxID: s.inbox.ID, ContactID: s.contact.ID,
		DisplayID: 100000 + f.serial, TeamID: &s.team.ID, Status: domain.ConversationStatusOpen,
	}
	switch state {
	case "self":
		conv.AssigneeID = &s.actor.ID
	case "participating", "other":
		conv.AssigneeID = &s.other.ID
	case "unassigned":
	default:
		t.Fatalf("unknown conversation state %q", state)
	}
	f.create(t, &conv)
	if state == "participating" {
		f.create(t, &domain.ConversationParticipant{ConversationID: conv.ID, UserID: s.actor.ID})
	}
	return conv
}

func (f *callReadFixture) call(t *testing.T, s *callReadScope, conv *domain.Conversation) domain.Call {
	t.Helper()
	f.serial++
	call := domain.Call{
		AccountID: s.account.ID, InboxID: s.inbox.ID, ContactID: s.contact.ID, AgentID: &s.actor.ID,
		Provider: "whatsapp", ProviderCallID: fmt.Sprintf("provider-call-secret-%d", f.serial),
		Status: "completed", Direction: "outbound", Duration: 42,
		SDPOffer:     fmt.Sprintf("v=0\r\ns=offer-secret-%d\r\n", f.serial),
		SDPAnswer:    fmt.Sprintf("v=0\r\ns=answer-secret-%d\r\n", f.serial),
		RecordingURL: fmt.Sprintf("https://recordings.example.test/%d.wav?signature=private%%2Bsignature&expires=123&download=1", f.serial),
		RecordingSID: fmt.Sprintf("recording-secret-%d", f.serial), TerminateReason: "completed",
	}
	if conv != nil {
		call.ConversationID = &conv.ID
	}
	f.create(t, &call)
	f.create(t, &domain.CallICECandidate{CallID: call.ID, Candidate: "candidate-secret-" + call.ProviderCallID, SDPMid: "audio", SDPMLine: 1})
	return call
}

func (f *callReadFixture) request(token string, accountID uint, suffix string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/accounts/%d%s", accountID, suffix), nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	out := httptest.NewRecorder()
	f.router.ServeHTTP(out, req)
	return out
}

func callReadRoutes(call domain.Call) []string {
	return []string{
		fmt.Sprintf("/calls/%d", call.ID),
		fmt.Sprintf("/calls/%d/whatsapp", call.ID),
		"/calls/" + url.PathEscape(call.ProviderCallID) + "/whatsapp",
		fmt.Sprintf("/whatsapp_calls/%d", call.ID),
		"/whatsapp_calls/" + url.PathEscape(call.ProviderCallID),
		fmt.Sprintf("/calls/%d/candidates", call.ID),
	}
}

func callReadData(t *testing.T, out *httptest.ResponseRecorder) json.RawMessage {
	t.Helper()
	if out.Code != http.StatusOK {
		t.Fatalf("expected 200: got %d, body=%s", out.Code, out.Body.String())
	}
	var envelope struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(out.Body.Bytes(), &envelope); err != nil || !envelope.Success || len(envelope.Data) == 0 {
		t.Fatalf("invalid success response: error=%v body=%s", err, out.Body.String())
	}
	return envelope.Data
}

func callReadDenied(t *testing.T, out *httptest.ResponseRecorder) {
	t.Helper()
	if out.Code != http.StatusForbidden && out.Code != http.StatusNotFound {
		t.Fatalf("expected forbidden or hidden call: got %d, body=%s", out.Code, out.Body.String())
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(out.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("invalid denied response: %v", err)
	}
	for key := range envelope {
		if key != "success" && key != "error" && key != "message" && key != "data" {
			t.Errorf("denied response contains unexpected field %q: %s", key, out.Body.String())
		}
	}
	if string(envelope["success"]) != "false" || (len(envelope["data"]) != 0 && string(envelope["data"]) != "null") {
		t.Errorf("denied response exposed data: %s", out.Body.String())
	}
	for _, secret := range []string{"provider-call-secret-", "offer-secret-", "answer-secret-", "recording-secret-", "candidate-secret-", "Private caller", "recordings.example.test"} {
		if strings.Contains(out.Body.String(), secret) {
			t.Errorf("denied response leaked %q: %s", secret, out.Body.String())
		}
	}
}

func (f *callReadFixture) visibility(t *testing.T, s *callReadScope, call domain.Call, allowed bool) {
	t.Helper()
	for _, route := range callReadRoutes(call) {
		t.Run(route, func(t *testing.T) {
			out := f.request(s.token, s.account.ID, route)
			if allowed {
				callReadData(t, out)
			} else {
				callReadDenied(t, out)
			}
		})
	}
}

func (f *callReadFixture) list(t *testing.T, s *callReadScope, query string, expected ...domain.Call) []domain.Call {
	t.Helper()
	data := callReadData(t, f.request(s.token, s.account.ID, "/calls"+query))
	var calls []domain.Call
	if err := json.Unmarshal(data, &calls); err != nil {
		t.Fatalf("decode calls list: %v; data=%s", err, data)
	}
	want := make([]uint, len(expected))
	for i := range expected {
		want[i] = expected[i].ID
	}
	sort.Slice(want, func(i, j int) bool { return want[i] > want[j] })
	got := make([]uint, len(calls))
	for i := range calls {
		got[i] = calls[i].ID
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("call list must contain only authorized calls in existing descending order: got=%v want=%v data=%s", got, want, data)
	}
	return calls
}

func TestCallReadPermissions(t *testing.T) {
	f := newCallReadFixture(t)
	t.Run("ordinary role and current conversation access matrix", func(t *testing.T) {
		roles := []callReadRole{
			{name: "plain agent", allowed: []string{"self", "unassigned", "participating", "other"}},
			{name: "conversation manage", permissions: `["conversation_manage"]`, allowed: []string{"self", "unassigned", "participating", "other"}},
			{name: "unassigned manage", permissions: `["conversation_unassigned_manage"]`, allowed: []string{"self", "unassigned"}},
			{name: "participating manage", permissions: `["conversation_participating_manage"]`, allowed: []string{"self", "participating"}},
			{name: "unassigned and participating union", permissions: `["conversation_unassigned_manage","conversation_participating_manage"]`, allowed: []string{"self", "unassigned", "participating"}},
			{name: "custom role without conversation permission", permissions: `["contact_manage"]`},
			{name: "malformed custom permissions", permissions: `not-json`},
			{name: "unresolved custom role name", accountRole: "Deleted support role"},
			{name: "tenant local custom role name", permissions: `["conversation_manage"]`, byName: true, allowed: []string{"self", "unassigned", "participating", "other"}},
		}
		for _, role := range roles {
			t.Run(role.name, func(t *testing.T) {
				for _, base := range []string{"inbox", "team", "none"} {
					t.Run(base, func(t *testing.T) {
						s := f.scope(t, role, base)
						var visible []domain.Call
						for _, state := range []string{"self", "unassigned", "participating", "other"} {
							t.Run(state, func(t *testing.T) {
								conv := f.conversation(t, s, state)
								call := f.call(t, s, &conv)
								allowed := false
								for _, grantedState := range role.allowed {
									if base != "none" && state == grantedState {
										allowed = true
									}
								}
								f.visibility(t, s, call, allowed)
								if allowed {
									visible = append(visible, call)
								}
							})
						}
						f.list(t, s, "", visible...)
					})
				}
			})
		}
	})

	t.Run("current account administrators and report permissions retain account wide metadata", func(t *testing.T) {
		roles := []callReadRole{{name: "account administrator with global agent role", accountRole: domain.RoleAdministrator}}
		for _, permission := range []string{domain.PermissionReportView, domain.PermissionReportManage, domain.PermissionAdministrator, "报表:查看", "报表:处理", "报表:管理", "报表:导出"} {
			encoded, err := json.Marshal([]string{permission})
			if err != nil {
				t.Fatal(err)
			}
			roles = append(roles, callReadRole{name: permission, permissions: string(encoded)})
		}
		for _, role := range roles {
			t.Run(role.name, func(t *testing.T) {
				s := f.scope(t, role, "none")
				conv := f.conversation(t, s, "other")
				other := f.call(t, s, &conv)
				f.update(t, &domain.Call{}, other.ID, map[string]any{"agent_id": s.other.ID})
				other.AgentID = &s.other.ID
				unowned := f.call(t, s, &conv)
				f.update(t, &domain.Call{}, unowned.ID, map[string]any{"agent_id": nil})
				unowned.AgentID = nil
				historical := f.call(t, s, nil)
				unknown := uint(900000000)
				f.update(t, &domain.Call{}, historical.ID, map[string]any{"agent_id": unknown, "conversation_id": unknown})
				historical.AgentID, historical.ConversationID = &unknown, &unknown
				withoutConversation := f.call(t, s, nil)
				for _, call := range []domain.Call{other, unowned, historical, withoutConversation} {
					f.visibility(t, s, call, true)
				}
				f.list(t, s, "", other, unowned, historical, withoutConversation)
			})
		}
	})

	t.Run("global role and query parameters cannot choose another call owner", func(t *testing.T) {
		for _, globalRole := range []string{domain.RoleAgent, domain.RoleAdministrator, domain.RoleSuperAdmin} {
			t.Run(globalRole, func(t *testing.T) {
				s := f.scope(t, callReadRole{globalRole: globalRole}, "inbox")
				conv := f.conversation(t, s, "self")
				own, other := f.call(t, s, &conv), f.call(t, s, &conv)
				f.update(t, &domain.Call{}, other.ID, map[string]any{"agent_id": s.other.ID})
				other.AgentID = &s.other.ID
				f.visibility(t, s, own, true)
				f.visibility(t, s, other, false)
				for _, query := range []string{"", fmt.Sprintf("?agent_id=%d", s.other.ID), "?agent_id=0", "?agent_id=all", fmt.Sprintf("?agent_id=%d&user_id=%d", s.other.ID, s.other.ID)} {
					f.list(t, s, query, own)
				}
				for _, route := range callReadRoutes(other) {
					callReadDenied(t, f.request(s.token, s.account.ID, route+fmt.Sprintf("?agent_id=%d", s.other.ID)))
				}
			})
		}
	})

	t.Run("numeric call precedence and numeric provider fallback remain secure", func(t *testing.T) {
		s := f.scope(t, callReadRole{}, "inbox")
		conv := f.conversation(t, s, "self")
		forbidden := f.call(t, s, &conv)
		f.update(t, &domain.Call{}, forbidden.ID, map[string]any{"agent_id": s.other.ID})
		forbidden.AgentID = &s.other.ID
		providerMatch := f.call(t, s, &conv)
		providerMatch.ProviderCallID = fmt.Sprint(forbidden.ID)
		f.update(t, &domain.Call{}, providerMatch.ID, map[string]any{"provider_call_id": providerMatch.ProviderCallID})
		for _, route := range []string{
			fmt.Sprintf("/calls/%d/whatsapp", forbidden.ID),
			fmt.Sprintf("/whatsapp_calls/%d", forbidden.ID),
		} {
			// A real numeric Call has precedence. Its denial must not trigger a
			// second lookup that returns a different, authorized provider-ID row.
			callReadDenied(t, f.request(s.token, s.account.ID, route))
		}
		callReadData(t, f.request(s.token, s.account.ID, fmt.Sprintf("/calls/%d", providerMatch.ID)))
		f.list(t, s, "", providerMatch)

		fallback := f.call(t, s, &conv)
		fallback.ProviderCallID = "900000001"
		f.update(t, &domain.Call{}, fallback.ID, map[string]any{"provider_call_id": fallback.ProviderCallID})
		var numericRows int64
		if err := f.db.Model(&domain.Call{}).Where("account_id = ? AND id = ?", s.account.ID, 900000001).Count(&numericRows).Error; err != nil || numericRows != 0 {
			t.Fatalf("numeric provider fallback fixture must have no numeric Call: count=%d err=%v", numericRows, err)
		}
		for _, route := range []string{"/calls/" + fallback.ProviderCallID + "/whatsapp", "/whatsapp_calls/" + fallback.ProviderCallID} {
			data := callReadData(t, f.request(s.token, s.account.ID, route))
			var returned struct {
				ID     uint   `json:"id"`
				CallID string `json:"call_id"`
			}
			if err := json.Unmarshal(data, &returned); err != nil || returned.ID != fallback.ID || returned.CallID != fallback.ProviderCallID {
				t.Fatalf("numeric provider-ID fallback changed: err=%v data=%s", err, data)
			}
		}
		f.list(t, s, "", providerMatch, fallback)
		f.update(t, &domain.Call{}, fallback.ID, map[string]any{"agent_id": s.other.ID})
		for _, route := range []string{"/calls/" + fallback.ProviderCallID + "/whatsapp", "/whatsapp_calls/" + fallback.ProviderCallID} {
			callReadDenied(t, f.request(s.token, s.account.ID, route))
		}
		f.list(t, s, "", providerMatch)
	})

	t.Run("historical and mismatched references fail closed for ordinary agents", func(t *testing.T) {
		cases := []struct {
			name   string
			change func(*testing.T, *callReadScope, *domain.Conversation, *domain.Call)
		}{
			{"missing agent", func(t *testing.T, s *callReadScope, conv *domain.Conversation, call *domain.Call) { call.AgentID = nil }},
			{"zero agent", func(t *testing.T, s *callReadScope, conv *domain.Conversation, call *domain.Call) {
				zero := uint(0)
				call.AgentID = &zero
			}},
			{"unknown agent", func(t *testing.T, s *callReadScope, conv *domain.Conversation, call *domain.Call) {
				unknown := uint(900000000)
				call.AgentID = &unknown
			}},
			{"membership ID is not IAM user ID", func(t *testing.T, s *callReadScope, conv *domain.Conversation, call *domain.Call) {
				call.AgentID = &s.member.ID
			}},
			{"missing conversation", func(t *testing.T, s *callReadScope, conv *domain.Conversation, call *domain.Call) {
				call.ConversationID = nil
			}},
			{"zero conversation", func(t *testing.T, s *callReadScope, conv *domain.Conversation, call *domain.Call) {
				zero := uint(0)
				call.ConversationID = &zero
			}},
			{"unknown conversation", func(t *testing.T, s *callReadScope, conv *domain.Conversation, call *domain.Call) {
				unknown := uint(900000000)
				call.ConversationID = &unknown
			}},
			{"display ID is not an internal conversation reference", func(t *testing.T, s *callReadScope, conv *domain.Conversation, call *domain.Call) {
				call.ConversationID = &conv.DisplayID
			}},
			{"different inbox", func(t *testing.T, s *callReadScope, conv *domain.Conversation, call *domain.Call) {
				inbox := domain.Inbox{AccountID: s.account.ID, Name: "Another inbox", WebsiteToken: fmt.Sprintf("other-%d", s.account.ID)}
				f.create(t, &inbox)
				f.create(t, &domain.InboxMember{InboxID: inbox.ID, UserID: s.actor.ID})
				call.InboxID = inbox.ID
			}},
			{"different contact", func(t *testing.T, s *callReadScope, conv *domain.Conversation, call *domain.Call) {
				contact := domain.Contact{AccountID: s.account.ID, Name: "Another contact"}
				f.create(t, &contact)
				call.ContactID = contact.ID
			}},
			{"missing inbox owner even when conversation and call agree", func(t *testing.T, s *callReadScope, conv *domain.Conversation, call *domain.Call) {
				call.InboxID = 900000000
				f.update(t, &domain.Conversation{}, conv.ID, map[string]any{"inbox_id": call.InboxID})
			}},
			{"missing contact owner even when conversation and call agree", func(t *testing.T, s *callReadScope, conv *domain.Conversation, call *domain.Call) {
				call.ContactID = 900000000
				f.update(t, &domain.Conversation{}, conv.ID, map[string]any{"contact_id": call.ContactID})
			}},
			{"foreign inbox owner even when conversation and call agree", func(t *testing.T, s *callReadScope, conv *domain.Conversation, call *domain.Call) {
				foreign := f.scope(t, callReadRole{}, "none")
				call.InboxID = foreign.inbox.ID
				f.create(t, &domain.InboxMember{InboxID: foreign.inbox.ID, UserID: s.actor.ID})
				f.update(t, &domain.Conversation{}, conv.ID, map[string]any{"inbox_id": call.InboxID})
			}},
			{"foreign contact owner even when conversation and call agree", func(t *testing.T, s *callReadScope, conv *domain.Conversation, call *domain.Call) {
				foreign := f.scope(t, callReadRole{}, "none")
				call.ContactID = foreign.contact.ID
				f.update(t, &domain.Conversation{}, conv.ID, map[string]any{"contact_id": call.ContactID})
			}},
			{"foreign conversation", func(t *testing.T, s *callReadScope, conv *domain.Conversation, call *domain.Call) {
				foreign := f.scope(t, callReadRole{}, "none")
				foreignConv := f.conversation(t, foreign, "self")
				call.ConversationID = &foreignConv.ID
			}},
			{"ambiguous internal ID and another display ID", func(t *testing.T, s *callReadScope, conv *domain.Conversation, call *domain.Call) {
				decoy := f.conversation(t, s, "self")
				f.update(t, &domain.Conversation{}, decoy.ID, map[string]any{"display_id": conv.ID})
			}},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				s := f.scope(t, callReadRole{}, "both")
				conv := f.conversation(t, s, "self")
				call := f.call(t, s, &conv)
				tc.change(t, s, &conv, &call)
				f.update(t, &domain.Call{}, call.ID, map[string]any{"agent_id": call.AgentID, "conversation_id": call.ConversationID, "inbox_id": call.InboxID, "contact_id": call.ContactID})
				f.visibility(t, s, call, false)
				f.list(t, s, "")
			})
		}
		t.Run("same row internal and display IDs are valid", func(t *testing.T) {
			s := f.scope(t, callReadRole{}, "inbox")
			conv := f.conversation(t, s, "self")
			f.update(t, &domain.Conversation{}, conv.ID, map[string]any{"display_id": conv.ID})
			call := f.call(t, s, &conv)
			f.visibility(t, s, call, true)
			f.list(t, s, "", call)
		})
		t.Run("foreign team membership cannot supply base access", func(t *testing.T) {
			s := f.scope(t, callReadRole{}, "none")
			foreign := f.scope(t, callReadRole{}, "none")
			conv := f.conversation(t, s, "self")
			f.update(t, &domain.Conversation{}, conv.ID, map[string]any{"team_id": foreign.team.ID})
			f.create(t, &domain.TeamMember{TeamID: foreign.team.ID, UserID: s.actor.ID})
			call := f.call(t, s, &conv)
			f.visibility(t, s, call, false)
			f.list(t, s, "")
		})
	})

	t.Run("cross tenant calls are hidden on every alias", func(t *testing.T) {
		s := f.scope(t, callReadRole{accountRole: domain.RoleAdministrator}, "none")
		foreign := f.scope(t, callReadRole{}, "inbox")
		conv := f.conversation(t, foreign, "self")
		call := f.call(t, foreign, &conv)
		// Membership in both tenants cannot make a call visible under the wrong account URL.
		f.create(t, &domain.AccountUser{AccountID: foreign.account.ID, UserID: s.actor.ID, Role: domain.RoleAdministrator})
		f.visibility(t, s, call, false)
		f.list(t, s, "")
		for _, route := range callReadRoutes(call) {
			out := f.request(s.token, foreign.account.ID, route)
			callReadData(t, out)
		}
	})

	t.Run("current access is rechecked using the same token", func(t *testing.T) {
		for _, base := range []string{"inbox", "team"} {
			t.Run("lost "+base+" membership", func(t *testing.T) {
				s := f.scope(t, callReadRole{}, base)
				conv := f.conversation(t, s, "self")
				f.create(t, &domain.ConversationParticipant{ConversationID: conv.ID, UserID: s.actor.ID})
				call := f.call(t, s, &conv)
				f.visibility(t, s, call, true)
				f.list(t, s, "", call)
				var removed *gorm.DB
				if base == "inbox" {
					removed = f.db.Where("inbox_id = ? AND user_id = ?", s.inbox.ID, s.actor.ID).Delete(&domain.InboxMember{})
				} else {
					removed = f.db.Where("team_id = ? AND user_id = ?", s.team.ID, s.actor.ID).Delete(&domain.TeamMember{})
				}
				if removed.Error != nil || removed.RowsAffected != 1 {
					t.Fatalf("remove membership: rows=%d err=%v", removed.RowsAffected, removed.Error)
				}
				f.visibility(t, s, call, false)
				f.list(t, s, "")
			})
		}
		t.Run("inbox or current team grants base access", func(t *testing.T) {
			s := f.scope(t, callReadRole{}, "both")
			conv := f.conversation(t, s, "other")
			call := f.call(t, s, &conv)
			if err := f.db.Where("inbox_id = ? AND user_id = ?", s.inbox.ID, s.actor.ID).Delete(&domain.InboxMember{}).Error; err != nil {
				t.Fatal(err)
			}
			f.visibility(t, s, call, true)
			otherTeam := domain.Team{AccountID: s.account.ID, Name: "Different current team"}
			f.create(t, &otherTeam)
			f.update(t, &domain.Conversation{}, conv.ID, map[string]any{"team_id": otherTeam.ID})
			f.visibility(t, s, call, false)
			f.list(t, s, "")
		})
		t.Run("participant removal revokes limited role", func(t *testing.T) {
			s := f.scope(t, callReadRole{permissions: `["conversation_participating_manage"]`}, "inbox")
			conv := f.conversation(t, s, "participating")
			call := f.call(t, s, &conv)
			f.visibility(t, s, call, true)
			if err := f.db.Where("conversation_id = ? AND user_id = ?", conv.ID, s.actor.ID).Delete(&domain.ConversationParticipant{}).Error; err != nil {
				t.Fatal(err)
			}
			f.visibility(t, s, call, false)
			f.list(t, s, "")
		})
		t.Run("current assignee changes limited role visibility", func(t *testing.T) {
			s := f.scope(t, callReadRole{permissions: `["conversation_unassigned_manage"]`}, "inbox")
			conv := f.conversation(t, s, "unassigned")
			call := f.call(t, s, &conv)
			f.visibility(t, s, call, true)
			f.update(t, &domain.Conversation{}, conv.ID, map[string]any{"assignee_id": s.other.ID})
			f.visibility(t, s, call, false)
			f.list(t, s, "")
			f.update(t, &domain.Conversation{}, conv.ID, map[string]any{"assignee_id": s.actor.ID})
			f.visibility(t, s, call, true)
		})
		t.Run("report permission removal revokes account wide access", func(t *testing.T) {
			s := f.scope(t, callReadRole{permissions: `["report_view"]`}, "none")
			call := f.call(t, s, nil)
			f.visibility(t, s, call, true)
			f.update(t, &domain.CustomRole{}, s.role.ID, map[string]any{"permissions": `[]`})
			f.visibility(t, s, call, false)
			f.list(t, s, "")
		})
		t.Run("account administrator demotion revokes account wide access", func(t *testing.T) {
			s := f.scope(t, callReadRole{accountRole: domain.RoleAdministrator, globalRole: domain.RoleAdministrator}, "none")
			call := f.call(t, s, nil)
			f.visibility(t, s, call, true)
			f.update(t, &domain.AccountUser{}, s.member.ID, map[string]any{"role": domain.RoleAgent})
			f.visibility(t, s, call, false)
			f.list(t, s, "")
		})
		t.Run("tenant membership removal is enforced before call reads", func(t *testing.T) {
			s := f.scope(t, callReadRole{accountRole: domain.RoleAdministrator}, "inbox")
			conv := f.conversation(t, s, "self")
			call := f.call(t, s, &conv)
			f.visibility(t, s, call, true)
			if err := f.db.Delete(&s.member).Error; err != nil {
				t.Fatal(err)
			}
			f.visibility(t, s, call, false)
			callReadDenied(t, f.request(s.token, s.account.ID, "/calls"))
		})
	})

	t.Run("custom roles resolve only from the current tenant and explicit ID", func(t *testing.T) {
		for _, mode := range []string{"missing ID", "missing ID with matching name", "foreign report role ID"} {
			t.Run(mode, func(t *testing.T) {
				s := f.scope(t, callReadRole{permissions: `["conversation_manage"]`}, "inbox")
				conv := f.conversation(t, s, "self")
				call := f.call(t, s, &conv)
				f.visibility(t, s, call, true)
				fields := map[string]any{"custom_role_id": uint(900000000)}
				if mode == "missing ID with matching name" {
					fields["role"] = s.role.Name
				}
				if mode == "foreign report role ID" {
					foreign := f.scope(t, callReadRole{permissions: `["report_view"]`}, "none")
					fields["custom_role_id"] = foreign.role.ID
				}
				f.update(t, &domain.AccountUser{}, s.member.ID, fields)
				f.visibility(t, s, call, false)
				f.list(t, s, "")
			})
		}
	})

	t.Run("authorized metadata and signaling response shapes are preserved", func(t *testing.T) {
		s := f.scope(t, callReadRole{}, "inbox")
		conv := f.conversation(t, s, "self")
		call := f.call(t, s, &conv)
		local := f.call(t, s, &conv)
		local.RecordingURL = fmt.Sprintf("/uploads/account_%d/calls/recording.wav?token=signed%%2Btoken&expires=123&download=1", s.account.ID)
		f.update(t, &domain.Call{}, local.ID, map[string]any{"recording_url": local.RecordingURL})
		for _, expected := range []domain.Call{call, local} {
			for _, route := range callReadRoutes(expected) {
				t.Run(route, func(t *testing.T) {
					data := callReadData(t, f.request(s.token, s.account.ID, route))
					if strings.HasSuffix(route, "/candidates") {
						var candidates []domain.CallICECandidate
						if err := json.Unmarshal(data, &candidates); err != nil {
							t.Fatal(err)
						}
						if len(candidates) != 1 || candidates[0].CallID != expected.ID || candidates[0].Candidate != "candidate-secret-"+expected.ProviderCallID || candidates[0].SDPMid != "audio" || candidates[0].SDPMLine != 1 {
							t.Fatalf("candidate payload changed: %s", data)
						}
						return
					}
					var fields map[string]any
					if err := json.Unmarshal(data, &fields); err != nil {
						t.Fatal(err)
					}
					for key, want := range map[string]any{
						"id": float64(expected.ID), "conversation_id": float64(conv.ID), "inbox_id": float64(s.inbox.ID),
						"status": expected.Status, "direction": expected.Direction, "provider": expected.Provider,
						"duration": float64(expected.Duration), "recording_url": expected.RecordingURL,
						"recording_sid": expected.RecordingSID, "sdp_offer": expected.SDPOffer, "sdp_answer": expected.SDPAnswer,
						"terminate_reason": expected.TerminateReason,
					} {
						if !reflect.DeepEqual(fields[key], want) {
							t.Errorf("%s changed: got=%v want=%v", key, fields[key], want)
						}
					}
					if strings.Contains(route, "whatsapp") {
						if fields["call_id"] != expected.ProviderCallID || fields["accepted_by_agent_id"] != float64(s.actor.ID) || fields["elapsed_seconds"] != float64(expected.Duration) {
							t.Errorf("WhatsApp identity or duration changed: %s", data)
						}
						if value, exists := fields["message_id"]; !exists || value != nil {
							t.Errorf("WhatsApp message_id shape changed: %s", data)
						}
						caller := map[string]any{"name": s.contact.Name, "phone": s.contact.PhoneNumber, "avatar": s.contact.AvatarURL}
						if !reflect.DeepEqual(fields["caller"], caller) {
							t.Errorf("caller payload changed: %s", data)
						}
						servers, ok := fields["ice_servers"].([]any)
						if !ok || len(servers) != 1 || !reflect.DeepEqual(servers[0], map[string]any{"urls": "stun:stun.l.google.com:19302"}) {
							t.Errorf("ICE server payload changed: %s", data)
						}
					} else if fields["agent_id"] != float64(s.actor.ID) || fields["provider_call_id"] != expected.ProviderCallID || fields["contact_id"] != float64(s.contact.ID) || fields["account_id"] != float64(s.account.ID) {
						t.Errorf("standard call ownership or provider payload changed: %s", data)
					}
				})
			}
		}
		calls := f.list(t, s, "", call, local)
		if calls[0].RecordingURL != local.RecordingURL || calls[1].RecordingURL != call.RecordingURL || calls[0].SDPOffer != local.SDPOffer || calls[1].SDPAnswer != call.SDPAnswer {
			t.Fatalf("list altered authorized SDP or signed/external recording URLs: %+v", calls)
		}
		for _, route := range append(callReadRoutes(call), "/calls") {
			out := f.request("", s.account.ID, route)
			if out.Code != http.StatusUnauthorized || strings.Contains(out.Body.String(), "provider-call-secret-") {
				t.Errorf("unauthenticated call read leaked or skipped Auth middleware: status=%d body=%s", out.Code, out.Body.String())
			}
		}
	})
}
