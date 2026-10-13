package middleware

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSystemConfigQueriesAreSilentOnlyOnConfigRoutes(t *testing.T) {
	for _, path := range []string{"/api/v1/super_admin/configs", "/api/v1/super_admin/configs/"} {
		for _, query := range []string{
			"value=synthetic-query-canary", "unknown=synthetic-query-canary",
			"config_key=synthetic-query-canary&value=another-canary",
			"value=synthetic-query-canary%ZZ", "VALUE=synthetic-query-canary",
		} {
			require.Empty(t, safeRequestQuery(path, query))
		}
	}
	for _, path := range []string{"/api/search", "/api/v1/super_admin/accounts", "/api/v1/super_admin/configs_unrelated"} {
		require.Equal(t, "q=hello&sort=asc", safeRequestQuery(path, "sort=asc&q=hello"))
	}
}
