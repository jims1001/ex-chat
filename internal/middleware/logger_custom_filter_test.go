package middleware

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCustomFilterQueryNeverAppearsInLogs(t *testing.T) {
	for _, path := range []string{"/api/v1/accounts/91/custom_filters", "/api/v1/accounts/91/custom_filters/", "/api/v1/accounts/91/custom_filters/1"} {
		for _, raw := range []string{"filter_type=PRIVATE-TYPE", "query=PRIVATE-CRITERIA&name=PRIVATE-NAME", "PRIVATE-KEY=PRIVATE-VALUE", "query=PRIVATE%ZZ"} {
			require.Empty(t, safeRequestQuery(path, raw))
		}
	}
	require.Equal(t, "q=hello", safeRequestQuery("/api/search", "q=hello"))
}
