package service

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStorageSignatureBindsExactCanonicalKey(t *testing.T) {
	s := NewLocalStorageService("uploads", "storage-signature-security-secret")
	key := "widget/inbox_12/contact_34/literal%20 space.txt"
	signed, err := s.GetSignedURL(context.Background(), key, time.Minute)
	require.NoError(t, err)
	u, err := url.Parse(signed)
	require.NoError(t, err)
	require.Equal(t, "/uploads/"+key, u.Path)
	require.Contains(t, signed, "%2520%20space.txt")
	exp, err := strconv.ParseInt(u.Query().Get("expires"), 10, 64)
	require.NoError(t, err)
	signature := u.Query().Get("sig")
	require.True(t, s.VerifySignedURL(context.Background(), key, exp, signature))
	for _, alias := range []string{"uploads/" + key, "/uploads/" + key, "/" + key, "../" + key, "widget/../" + key, strings.Replace(key, "contact_34", "contact_35", 1), strings.Replace(key, "inbox_12", "inbox_13", 1), key + "x", strings.Replace(key, "/", "//", 1)} {
		require.False(t, s.VerifySignedURL(context.Background(), alias, exp, signature), alias)
	}
	require.False(t, s.VerifySignedURL(context.Background(), key, time.Now().Unix(), signature))
	require.False(t, s.VerifySignedURL(context.Background(), key, time.Now().Add(-time.Minute).Unix(), signature))
	require.False(t, s.VerifySignedURL(context.Background(), key, exp, signature+"x"))
	for _, bad := range []string{"", "../secret", "/uploads/../secret", "account_1/../account_2/secret", "account_1//secret", "account_1/secret?fake=x", "account_1\\secret"} {
		_, err := s.GetSignedURL(context.Background(), bad, time.Minute)
		require.Error(t, err, bad)
	}
	empty := NewLocalStorageService("uploads", "")
	_, err = empty.GetSignedURL(context.Background(), key, time.Minute)
	require.Error(t, err)
	require.False(t, empty.VerifySignedURL(context.Background(), key, exp, signature))
}
