package test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/stretchr/testify/require"
)

// Exercise both endpoint families through real authentication, tenant and
// permission middleware. Read authority intentionally remains unchanged.
func TestCSATTemplateInboxManagePermissions(t *testing.T) {
	for _, role := range []struct {
		name    string
		role    callReadRole
		allowed bool
	}{
		{"administrator", callReadRole{accountRole: domain.RoleAdministrator}, true},
		{"custom_inbox_manager", callReadRole{permissions: `["inbox_manage"]`}, true},
		{"custom_inbox_manager_by_name", callReadRole{permissions: `["inbox_manage"]`, byName: true}, true},
		{"agent", callReadRole{accountRole: domain.RoleAgent}, false},
		{"unrelated_custom_role", callReadRole{permissions: `["conversation_manage"]`}, false},
	} {
		t.Run(role.name, func(t *testing.T) {
			f := newCallReadFixture(t)
			s := f.scope(t, role.role, "none")
			local := domain.Inbox{AccountID: s.account.ID, Name: "Local CSAT", ChannelType: "Channel::WebWidget", WebsiteToken: "local-csat", CSATConfig: `{"question":"original"}`}
			f.create(t, &local)
			request := func(method, suffix string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(method, fmt.Sprintf("/api/v1/accounts/%d%s", s.account.ID, suffix), strings.NewReader(`{"csat_template":{"question":"Saved survey","ratings":[1,2,3,4,5]},"message":"Saved survey"}`))
				req.Header.Set("Authorization", "Bearer "+s.token)
				req.Header.Set("Content-Type", "application/json")
				out := httptest.NewRecorder()
				f.router.ServeHTTP(out, req)
				return out
			}
			path := fmt.Sprintf("/inboxes/%d/csat_template", local.ID)
			out := request(http.MethodPost, path)
			want := http.StatusForbidden
			if role.allowed {
				want = http.StatusOK
			}
			require.Equal(t, want, out.Code, out.Body.String())
			var stored domain.Inbox
			require.NoError(t, f.db.First(&stored, local.ID).Error)
			if role.allowed {
				require.JSONEq(t, `{"question":"Saved survey","ratings":[1,2,3,4,5]}`, stored.CSATConfig)
			} else {
				require.Equal(t, local.CSATConfig, stored.CSATConfig)
			}
			// Existing member access to local configuration reads is preserved.
			out = request(http.MethodGet, path)
			require.Equal(t, http.StatusOK, out.Code, out.Body.String())
			var read map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(out.Body.Bytes(), &read))
			for _, alias := range []string{"template", "csat_template", "data"} {
				require.JSONEq(t, stored.CSATConfig, string(read[alias]))
			}
			for _, providerPath := range []string{"/whatsapp/csat_template", fmt.Sprintf("/inboxes/%d/csat_template", s.inbox.ID)} {
				out = request(http.MethodPost, providerPath)
				want = http.StatusForbidden
				if role.allowed {
					want = http.StatusServiceUnavailable
				}
				require.Equal(t, want, out.Code, out.Body.String())
				require.NotContains(t, out.Body.String(), `"success":true`)
			}
			out = request(http.MethodGet, fmt.Sprintf("/inboxes/%d/csat_template", s.inbox.ID))
			require.Equal(t, http.StatusServiceUnavailable, out.Code, out.Body.String())
			var unchanged domain.Inbox
			require.NoError(t, f.db.First(&unchanged, s.inbox.ID).Error)
			require.Equal(t, s.inbox.CSATConfig, unchanged.CSATConfig)
		})
	}
}
