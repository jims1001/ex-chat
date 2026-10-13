package uploadpath

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalUploadKeysAndURLs(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "../file", "/absolute", "a/../b", "a/./b", "a//b", "a/", "a\\b", "a?x", "a#x", "a\x00b", "a\nb"} {
		_, err := CanonicalKey(bad)
		require.Error(t, err, bad)
	}
	for _, bad := range []string{"inbox_0", "inbox_-1", "inbox_01", "inbox_1extra", "inbox_+1", "inbox_18446744073709551616"} {
		_, err := NamespaceID(bad, "inbox_")
		require.Error(t, err, bad)
	}
	key, err := WidgetKey(1, 2, "100% complete.txt")
	require.NoError(t, err)
	raw := URL(key)
	require.Equal(t, "/uploads/widget/inbox_1/contact_2/100%25%20complete.txt", raw)
	parsed, local, err := LocalURLKey(raw + "?expires=123&sig=example")
	require.NoError(t, err)
	require.True(t, local)
	require.Equal(t, key, parsed)
	_, local, err = LocalURLKey("https://untrusted.example" + raw)
	require.NoError(t, err)
	require.False(t, local)
	_, local, err = LocalURLKey("//untrusted.example" + raw)
	require.NoError(t, err)
	require.False(t, local)
	for _, bad := range []string{"/uploads/a/%2e%2e/b", "/uploads/a/%5cb", "/uploads/a/%3fx", "/uploads/a/b#fragment"} {
		_, local, err = LocalURLKey(bad)
		require.True(t, local)
		require.Error(t, err)
	}
}
