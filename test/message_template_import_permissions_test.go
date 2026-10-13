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

func TestMessageTemplateImportPermissionsAndReadPurity(t *testing.T) {
	for _, role := range []struct {
		name    string
		role    callReadRole
		allowed bool
	}{
		{"administrator", callReadRole{accountRole: domain.RoleAdministrator}, true},
		{"custom_inbox_manager", callReadRole{permissions: `["inbox_manage"]`}, true},
		{"agent", callReadRole{accountRole: domain.RoleAgent}, false},
		{"unrelated_custom_role", callReadRole{permissions: `["conversation_manage"]`}, false},
	} {
		t.Run(role.name, func(t *testing.T) {
			f := newCallReadFixture(t)
			s := f.scope(t, role.role, "none")
			require.NoError(t, f.db.Model(&domain.Inbox{}).Where("id = ?", s.inbox.ID).Update("provider_config", `{"templates":[{"name":"config_only"}]}`).Error)
			path := fmt.Sprintf("/api/v1/accounts/%d/inboxes/%d", s.account.ID, s.inbox.ID)
			request := func(method, suffix, body string) *httptest.ResponseRecorder {
				req := httptest.NewRequest(method, path+suffix, strings.NewReader(body))
				req.Header.Set("Authorization", "Bearer "+s.token)
				req.Header.Set("Content-Type", "application/json")
				out := httptest.NewRecorder()
				f.router.ServeHTTP(out, req)
				return out
			}
			count := func() int64 {
				var n int64
				require.NoError(t, f.db.Model(&domain.InboxMessageTemplate{}).Count(&n).Error)
				return n
			}
			for _, suffix := range []string{"/message_templates", "/message_templates?name=missing", "/message_templates?status=APPROVED"} {
				out := request(http.MethodGet, suffix, "")
				require.Equal(t, http.StatusOK, out.Code, out.Body.String())
				require.Zero(t, count())
				var result struct {
					Meta map[string]any `json:"meta"`
				}
				require.NoError(t, json.Unmarshal(out.Body.Bytes(), &result))
				require.Nil(t, result.Meta["last_sync_attempt_at"])
			}
			out := request(http.MethodPost, "/message_templates", `{"name":"local"}`)
			want := http.StatusForbidden
			if role.allowed {
				want = http.StatusCreated
			}
			require.Equal(t, want, out.Code, out.Body.String())
			out = request(http.MethodPost, "/sync_templates", "")
			want = http.StatusForbidden
			if role.allowed {
				want = http.StatusOK
			}
			require.Equal(t, want, out.Code, out.Body.String())
			if role.allowed {
				require.Contains(t, out.Body.String(), `"status":"imported"`)
				require.Equal(t, int64(2), count())
				var row domain.InboxMessageTemplate
				require.NoError(t, f.db.Where("name = ?", "local").First(&row).Error)
				require.Empty(t, row.Status)
				out = request(http.MethodDelete, fmt.Sprintf("/message_templates/%d", row.ID), "")
				require.Equal(t, http.StatusOK, out.Code, out.Body.String())
				require.Equal(t, int64(1), count())
			} else {
				out = request(http.MethodDelete, "/message_templates/123", "")
				require.Equal(t, http.StatusForbidden, out.Code, out.Body.String())
				require.Zero(t, count())
			}
		})
	}
}
