package router_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/stretchr/testify/require"
)

func TestEnterpriseFeatureRESTFalsePersistenceAndExistingPermissions(t *testing.T) {
	f := newCompatibilityRouterFixture(t)
	path := fmt.Sprintf("/api/v1/accounts/%d/features", f.account.ID)
	require.NoError(t, repository.NewEnterpriseRepository(f.db).SetAccountFeature(f.other.ID, "captain", true))
	request := func(method, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		f.r.ServeHTTP(w, req)
		return w
	}
	for _, tc := range []struct {
		name, token string
		code        int
	}{
		{"anonymous", "", 401}, {"agent", f.agentToken, 403}, {"other_account", f.otherToken, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := request(http.MethodPost, tc.token, `{"feature_name":"captain","enabled":false}`)
			require.Equal(t, tc.code, w.Code, w.Body.String())
		})
	}
	for _, enabled := range []bool{false, true, false} {
		w := request(http.MethodPost, f.adminToken, fmt.Sprintf(`{"feature_name":"captain","enabled":%t}`, enabled))
		require.Equal(t, 200, w.Code, w.Body.String())
		var envelope struct {
			Success bool            `json:"success"`
			Data    map[string]bool `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
		require.True(t, envelope.Success)
		require.Equal(t, map[string]bool{"captain": enabled}, envelope.Data)
		// Existing member read access and boolean response shape stay intact.
		w = request(http.MethodGet, f.agentToken, "")
		require.Equal(t, 200, w.Code, w.Body.String())
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
		require.Equal(t, map[string]bool{"captain": enabled}, envelope.Data)
		other, err := repository.NewEnterpriseRepository(f.db).GetAccountFeatures(f.other.ID)
		require.NoError(t, err)
		require.Equal(t, map[string]bool{"captain": true}, other)
	}
}

func TestEnterpriseFeatureMissingOrNullEnabledMustNotDisable(t *testing.T) {
	for name, body := range map[string]string{"missing": `{"feature_name":"captain"}`, "null": `{"feature_name":"captain","enabled":null}`} {
		t.Run(name, func(t *testing.T) {
			f := newCompatibilityRouterFixture(t)
			repo := repository.NewEnterpriseRepository(f.db)
			require.NoError(t, repo.SetAccountFeature(f.account.ID, "captain", true))
			req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/accounts/%d/features", f.account.ID), strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+f.adminToken)
			w := httptest.NewRecorder()
			f.r.ServeHTTP(w, req)
			flags, err := repo.GetAccountFeatures(f.account.ID)
			require.NoError(t, err)
			t.Logf("status=%d persisted=%v", w.Code, flags)
			require.Equal(t, 400, w.Code)
			require.Equal(t, map[string]bool{"captain": true}, flags)
		})
	}
}
