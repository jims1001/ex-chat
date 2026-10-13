package handler

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/middleware"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type callReadOwnerFixture struct {
	h      *ChannelDriverHandler
	stores map[string]*gorm.DB
	logs   *bytes.Buffer
	call   domain.Call
	user   domain.User
}

func newCallReadOwnerFixture(t *testing.T) *callReadOwnerFixture {
	t.Helper()
	return newCallReadOwnerFixtureWithOpener(t, func(owner string, queryLogger gormlogger.Interface) *gorm.DB {
		db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), owner+".db")), &gorm.Config{Logger: queryLogger})
		require.NoError(t, err)
		return db
	})
}

func newCallReadOwnerFixtureWithOpener(t *testing.T, open func(string, gormlogger.Interface) *gorm.DB) *callReadOwnerFixture {
	t.Helper()
	f := &callReadOwnerFixture{stores: map[string]*gorm.DB{}, logs: &bytes.Buffer{}, user: domain.User{ID: 201, Role: domain.RoleAdministrator}}
	for _, owner := range []string{"TEN", "CHN", "CUS", "CON", "RTM"} {
		db := open(owner, gormlogger.New(log.New(f.logs, "", 0), gormlogger.Config{LogLevel: gormlogger.Info}))
		require.NoError(t, database.AutoMigrateModule(db, owner))
		pool, err := db.DB()
		require.NoError(t, err)
		t.Cleanup(func() { require.NoError(t, pool.Close()) })
		f.stores[owner] = db
	}
	account := domain.Account{ID: 101, Name: "Call reads"}
	require.NoError(t, f.stores["TEN"].Create(&account).Error)
	require.NoError(t, f.stores["TEN"].Create(&domain.AccountUser{AccountID: account.ID, UserID: f.user.ID, Role: domain.RoleAgent}).Error)
	inbox := domain.Inbox{ID: 601, AccountID: account.ID, Name: "Voice"}
	require.NoError(t, f.stores["CHN"].Create(&inbox).Error)
	require.NoError(t, f.stores["CHN"].Create(&domain.InboxMember{InboxID: inbox.ID, UserID: f.user.ID}).Error)
	contact := domain.Contact{ID: 501, AccountID: account.ID, Name: "private caller marker"}
	require.NoError(t, f.stores["CUS"].Create(&contact).Error)
	conv := domain.Conversation{ID: 301, DisplayID: 401, AccountID: account.ID, InboxID: inbox.ID, ContactID: contact.ID}
	require.NoError(t, f.stores["CON"].Create(&conv).Error)
	f.call = domain.Call{ID: 801, AccountID: account.ID, InboxID: inbox.ID, ContactID: contact.ID, ConversationID: &conv.ID, AgentID: &f.user.ID, ProviderCallID: "provider-private-marker", SDPOffer: "private-sdp-marker", RecordingURL: "https://media.example/a?token=private-recording-marker"}
	require.NoError(t, f.stores["RTM"].Create(&f.call).Error)
	require.NoError(t, f.stores["RTM"].Create(&domain.CallICECandidate{CallID: f.call.ID, Candidate: "private-ice-marker"}).Error)
	inboxes := repository.NewInboxRepositoryWithOwnerPorts(f.stores["CHN"], nil, nil, nil)
	contacts := repository.NewContactRepositoryWithOwnerPorts(f.stores["CUS"], nil, nil, nil, nil, nil)
	conversations := repository.NewConversationRepositoryWithReadPorts(f.stores["CON"], nil, nil, nil, nil, nil, nil, nil)
	calls := repository.NewChannelAuthEnterpriseRepositoryWithOwnerStores(nil, f.stores["RTM"], nil, nil, nil, nil, nil)
	f.h = NewChannelDriverHandler(calls, inboxes, contacts, conversations, nil)
	f.h.SetCallReadAuthorization(repository.NewAccountRepositoryWithUserPort(f.stores["TEN"], nil), repository.NewTeamRepositoryWithUserPort(f.stores["TEN"], nil))
	f.logs.Reset()
	return f
}

func (f *callReadOwnerFixture) request(path string, editContext func(*gin.Context)) *httptest.ResponseRecorder {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.ContextUserID, f.user.ID)
		c.Set(middleware.ContextUser, &f.user)
		// An earlier middleware snapshot is deliberately stale and privileged.
		c.Set(middleware.ContextAccountMembership, &domain.AccountUser{AccountID: f.call.AccountID, UserID: f.user.ID, Role: domain.RoleAdministrator})
		if editContext != nil {
			editContext(c)
		}
	})
	r.GET("/accounts/:account_id/calls", f.h.ListCalls)
	r.GET("/accounts/:account_id/calls/:id", f.h.GetCall)
	r.GET("/accounts/:account_id/calls/:id/whatsapp", f.h.GetWhatsAppCallDetail)
	r.GET("/accounts/:account_id/whatsapp_calls/:id", f.h.GetWhatsAppCallDetail)
	r.GET("/accounts/:account_id/calls/:id/candidates", f.h.ListCallCandidates)
	out := httptest.NewRecorder()
	r.ServeHTTP(out, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/accounts/%d/%s", f.call.AccountID, path), nil))
	return out
}

func assertCallReadFailure(t *testing.T, out *httptest.ResponseRecorder, status int) {
	t.Helper()
	require.Equal(t, status, out.Code, out.Body.String())
	require.NotContains(t, out.Body.String(), `"data"`)
	for _, marker := range []string{"private", "SELECT", "SQL", "provider-private-marker", "internal dependency"} {
		require.NotContains(t, out.Body.String(), marker)
	}
}

func TestCallReadAuthorizationUsesIsolatedOwnerStores(t *testing.T) {
	f := newCallReadOwnerFixture(t)
	for _, path := range []string{"calls", "calls/801", "calls/801/whatsapp", "whatsapp_calls/provider-private-marker", "calls/801/candidates"} {
		t.Run(path, func(t *testing.T) {
			out := f.request(path, nil)
			require.Equal(t, http.StatusOK, out.Code, out.Body.String())
		})
	}
	// The same handler must use current membership facts on each request, even
	// though its injected user and middleware membership claim administrator.
	require.NoError(t, f.stores["CHN"].Where("inbox_id = ?", f.call.InboxID).Delete(&domain.InboxMember{}).Error)
	assertCallReadFailure(t, f.request("calls/801", nil), http.StatusNotFound)
	team := domain.Team{ID: 701, AccountID: f.call.AccountID, Name: "Voice team"}
	require.NoError(t, f.stores["TEN"].Create(&team).Error)
	require.NoError(t, f.stores["TEN"].Create(&domain.TeamMember{TeamID: team.ID, UserID: f.user.ID}).Error)
	require.NoError(t, f.stores["CON"].Model(&domain.Conversation{}).Where("id = ?", *f.call.ConversationID).Update("team_id", team.ID).Error)
	require.Equal(t, http.StatusOK, f.request("calls/801", nil).Code)
	require.NoError(t, f.stores["TEN"].Where("team_id = ?", team.ID).Delete(&domain.TeamMember{}).Error)
	assertCallReadFailure(t, f.request("calls/801", nil), http.StatusNotFound)
	require.NoError(t, f.stores["TEN"].Where("account_id = ? AND user_id = ?", f.call.AccountID, f.user.ID).Delete(&domain.AccountUser{}).Error)
	assertCallReadFailure(t, f.request("calls/801", nil), http.StatusForbidden)
}

func TestCallReadAuthorizationDependencyErrorsAreStatic(t *testing.T) {
	for _, tc := range []struct{ owner, table string }{
		{"TEN", "account_users"}, {"TEN", "custom_roles"}, {"RTM", "calls"},
		{"CON", "conversations"}, {"CHN", "inboxes"}, {"CUS", "contacts"},
		{"CHN", "inbox_members"}, {"TEN", "team_members"}, {"CON", "conversation_participants"},
	} {
		t.Run(tc.table, func(t *testing.T) {
			f := newCallReadOwnerFixture(t)
			if tc.table == "custom_roles" || tc.table == "conversation_participants" {
				role := domain.CustomRole{AccountID: f.call.AccountID, Name: "participants", Permissions: `["conversation_participating_manage"]`}
				require.NoError(t, f.stores["TEN"].Create(&role).Error)
				require.NoError(t, f.stores["TEN"].Model(&domain.AccountUser{}).Where("account_id = ?", f.call.AccountID).Update("custom_role_id", role.ID).Error)
			}
			if tc.table == "team_members" {
				require.NoError(t, f.stores["CHN"].Where("inbox_id = ?", f.call.InboxID).Delete(&domain.InboxMember{}).Error)
				require.NoError(t, f.stores["CON"].Model(&domain.Conversation{}).Where("id = ?", *f.call.ConversationID).Update("team_id", 701).Error)
			}
			require.NoError(t, f.stores[tc.owner].Callback().Query().Before("gorm:query").Register("call_read_failure", func(tx *gorm.DB) {
				if tx.Statement.Table == tc.table {
					tx.AddError(errors.New("internal dependency private SQL marker"))
				}
			}))
			f.logs.Reset()
			for _, path := range []string{"calls", "calls/801", "calls/801/whatsapp", "whatsapp_calls/provider-private-marker", "calls/801/candidates"} {
				assertCallReadFailure(t, f.request(path, nil), http.StatusInternalServerError)
			}
			require.NotContains(t, f.logs.String(), "internal dependency")
			require.NotContains(t, f.logs.String(), "provider-private-marker")
		})
	}
}

func TestCallReadAuthorizationDeniesBeforeCandidateLookup(t *testing.T) {
	f := newCallReadOwnerFixture(t)
	require.NoError(t, f.stores["RTM"].Model(&domain.Call{}).Where("id = ?", f.call.ID).Update("agent_id", 999).Error)
	lookups := 0
	require.NoError(t, f.stores["RTM"].Callback().Query().Before("gorm:query").Register("candidate_lookup", func(tx *gorm.DB) {
		if tx.Statement.Table == "call_ice_candidates" {
			lookups++
			tx.AddError(errors.New("private candidate error"))
		}
	}))
	assertCallReadFailure(t, f.request("calls/801/candidates", nil), http.StatusNotFound)
	require.Zero(t, lookups)
	require.NoError(t, f.stores["RTM"].Model(&domain.Call{}).Where("id = ?", f.call.ID).Update("agent_id", f.user.ID).Error)
	f.logs.Reset()
	assertCallReadFailure(t, f.request("calls/801/candidates", nil), http.StatusInternalServerError)
	require.Equal(t, 1, lookups)
	require.NotContains(t, f.logs.String(), "private candidate error")
}

func TestCallReadAuthorizationNumericLookupFailureDoesNotFallback(t *testing.T) {
	f := newCallReadOwnerFixture(t)
	lookups := 0
	require.NoError(t, f.stores["RTM"].Callback().Query().Before("gorm:query").Register("numeric_lookup_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "calls" {
			lookups++
			tx.AddError(errors.New("private database error"))
		}
	}))
	for _, path := range []string{"calls/801/whatsapp", "whatsapp_calls/801"} {
		lookups = 0
		assertCallReadFailure(t, f.request(path, nil), http.StatusInternalServerError)
		require.Equal(t, 1, lookups)
	}
}

func TestCallReadAuthorizationMissingDependenciesAndActor(t *testing.T) {
	for _, dependency := range []string{"policy", "accounts", "calls", "inboxes", "contacts", "conversations", "teams"} {
		t.Run(dependency, func(t *testing.T) {
			f := newCallReadOwnerFixture(t)
			switch dependency {
			case "policy":
				f.h.callReads = nil
			case "accounts":
				f.h.callReads.accounts = nil
			case "calls":
				f.h.repo = nil
			case "inboxes":
				f.h.callReads.inboxes = nil
			case "contacts":
				f.h.callReads.contacts = nil
			case "conversations":
				f.h.callReads.conversations = nil
			case "teams":
				f.h.callReads.teams = nil
				require.NoError(t, f.stores["CHN"].Where("inbox_id = ?", f.call.InboxID).Delete(&domain.InboxMember{}).Error)
				require.NoError(t, f.stores["CON"].Model(&domain.Conversation{}).Where("id = ?", *f.call.ConversationID).Update("team_id", 701).Error)
			}
			assertCallReadFailure(t, f.request("calls/801", nil), http.StatusInternalServerError)
		})
	}
	f := newCallReadOwnerFixture(t)
	for _, identity := range []string{"missing", "wrong_type", "zero", "mismatch"} {
		t.Run(identity, func(t *testing.T) {
			out := f.request("calls/801", func(c *gin.Context) {
				switch identity {
				case "missing":
					delete(c.Keys, middleware.ContextUser)
				case "wrong_type":
					c.Set(middleware.ContextUser, "201")
				case "zero":
					c.Set(middleware.ContextUser, &domain.User{})
				case "mismatch":
					c.Set(middleware.ContextUserID, uint(999))
				}
			})
			assertCallReadFailure(t, out, http.StatusUnauthorized)
		})
	}
}

func TestCallReadAuthorizationListFailureDoesNotReturnPartialPayload(t *testing.T) {
	f := newCallReadOwnerFixture(t)
	conv := domain.Conversation{ID: 302, DisplayID: 402, AccountID: f.call.AccountID, InboxID: f.call.InboxID, ContactID: f.call.ContactID}
	require.NoError(t, f.stores["CON"].Create(&conv).Error)
	newer := f.call
	newer.ID, newer.ConversationID, newer.ProviderCallID = 802, &conv.ID, "newer-private-marker"
	require.NoError(t, f.stores["RTM"].Create(&newer).Error)
	queries := 0
	require.NoError(t, f.stores["CON"].Callback().Query().Before("gorm:query").Register("second_conversation_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "conversations" {
			queries++
			if queries == 2 {
				tx.AddError(errors.New("private later dependency failure"))
			}
		}
	}))
	f.logs.Reset()
	out := f.request("calls", nil)
	assertCallReadFailure(t, out, http.StatusInternalServerError)
	require.Equal(t, 2, queries)
	require.False(t, strings.Contains(f.logs.String(), "private later dependency failure"))
}

func TestCallReadAuthorizationCallerFailurePreservesShapeWithoutLogs(t *testing.T) {
	f := newCallReadOwnerFixture(t)
	queries := 0
	require.NoError(t, f.stores["CUS"].Callback().Query().Before("gorm:query").Register("caller_details_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "contacts" {
			queries++
			if queries == 2 {
				tx.AddError(errors.New("private caller dependency failure"))
			}
		}
	}))
	f.logs.Reset()
	out := f.request("whatsapp_calls/provider-private-marker", nil)
	require.Equal(t, http.StatusOK, out.Code)
	require.Contains(t, out.Body.String(), `"caller":{}`)
	require.Contains(t, out.Body.String(), `"sdp_offer":"private-sdp-marker"`)
	require.NotContains(t, out.Body.String(), "private caller dependency failure")
	require.NotContains(t, f.logs.String(), "private caller dependency failure")
	require.Equal(t, 2, queries)
}
