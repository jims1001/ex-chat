package auth

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

const visitorTestSecret = "visitor-purpose-test-secret"

func TestVisitorTokenPurposesAreIsolated(t *testing.T) {
	session, err := GenerateVisitorToken(10, 20, "source-20", visitorTestSecret, 0)
	require.NoError(t, err)
	upload, err := GenerateVisitorUploadToken(10, 20, "source-20", "blobs/document.txt", visitorTestSecret, 1024, 0)
	require.NoError(t, err)
	sessionClaims, err := ParseVisitorToken(session, visitorTestSecret)
	require.NoError(t, err)
	require.Equal(t, TokenPurposeVisitorSession, sessionClaims.Purpose)
	require.Equal(t, sessionClaims.SourceID, sessionClaims.Subject)
	require.Equal(t, 72*time.Hour, sessionClaims.ExpiresAt.Sub(sessionClaims.IssuedAt.Time))
	uploadClaims, err := ParseVisitorUploadToken(upload, visitorTestSecret)
	require.NoError(t, err)
	require.Equal(t, TokenPurposeVisitorUpload, uploadClaims.Purpose)
	require.Equal(t, VisitorUploadTTL, uploadClaims.ExpiresAt.Sub(uploadClaims.IssuedAt.Time))
	require.Equal(t, "blobs/document.txt", uploadClaims.UploadKey)
	require.Equal(t, int64(1024), uploadClaims.MaxBytes)
	require.NotEmpty(t, uploadClaims.ID)
	require.NotEqual(t, sessionClaims.ID, uploadClaims.ID)
	claims, err := ParseVisitorToken(upload, visitorTestSecret)
	require.ErrorIs(t, err, ErrInvalidVisitorToken)
	require.Nil(t, claims)
	claims, err = ParseVisitorUploadToken(session, visitorTestSecret)
	require.ErrorIs(t, err, ErrInvalidVisitorToken)
	require.Nil(t, claims)
	_, err = ValidateToken(session, visitorTestSecret)
	require.Error(t, err)
	_, err = ValidateMFAToken(upload, visitorTestSecret)
	require.Error(t, err)
}

func TestVisitorParsersRejectInvalidClaimsAndAlgorithms(t *testing.T) {
	parsers := []struct {
		purpose string
		parse   func(string, string) (*VisitorClaims, error)
	}{
		{TokenPurposeVisitorSession, ParseVisitorToken},
		{TokenPurposeVisitorUpload, ParseVisitorUploadToken},
	}
	for _, parser := range parsers {
		t.Run(parser.purpose, func(t *testing.T) {
			type tokenCase struct {
				name   string
				method jwt.SigningMethod
				mutate func(*VisitorClaims)
				key    any
			}
			tests := []tokenCase{
				{name: "missing purpose", mutate: func(c *VisitorClaims) { c.Purpose = "" }},
				{name: "access purpose", mutate: func(c *VisitorClaims) { c.Purpose = TokenPurposeAccess }},
				{name: "mfa purpose", mutate: func(c *VisitorClaims) { c.Purpose = TokenPurposeMFA }},
				{name: "missing expiry", mutate: func(c *VisitorClaims) { c.ExpiresAt = nil }},
				{name: "expired", mutate: func(c *VisitorClaims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute)) }},
				{name: "missing issued at", mutate: func(c *VisitorClaims) { c.IssuedAt = nil }},
				{name: "future issued at", mutate: func(c *VisitorClaims) { c.IssuedAt = jwt.NewNumericDate(time.Now().Add(time.Minute)) }},
				{name: "future not before", mutate: func(c *VisitorClaims) { c.NotBefore = jwt.NewNumericDate(time.Now().Add(time.Minute)) }},
				{name: "invalid lifetime", mutate: func(c *VisitorClaims) { c.IssuedAt = c.ExpiresAt }},
				{name: "zero inbox", mutate: func(c *VisitorClaims) { c.InboxID = 0 }},
				{name: "zero contact", mutate: func(c *VisitorClaims) { c.ContactID = 0 }},
				{name: "missing source", mutate: func(c *VisitorClaims) { c.SourceID = "" }},
				{name: "whitespace source", mutate: func(c *VisitorClaims) { c.SourceID = " source-20 "; c.Subject = c.SourceID }},
				{name: "missing subject", mutate: func(c *VisitorClaims) { c.Subject = "" }},
				{name: "mismatched identity", mutate: func(c *VisitorClaims) { c.Subject = "other-visitor" }},
				{name: "missing identifier", mutate: func(c *VisitorClaims) { c.ID = "" }},
				{name: "wrong signing key", key: []byte("wrong-key")},
				{name: "HS384", method: jwt.SigningMethodHS384},
				{name: "HS512", method: jwt.SigningMethodHS512},
				{name: "unsigned", method: jwt.SigningMethodNone, key: jwt.UnsafeAllowNoneSignatureType},
			}
			if parser.purpose == TokenPurposeVisitorSession {
				tests = append(tests,
					tokenCase{name: "session with upload key", mutate: func(c *VisitorClaims) { c.UploadKey = "blobs/x.txt" }},
					tokenCase{name: "session with size", mutate: func(c *VisitorClaims) { c.MaxBytes = 1 }},
				)
			} else {
				tests = append(tests,
					tokenCase{name: "missing upload key", mutate: func(c *VisitorClaims) { c.UploadKey = "" }},
					tokenCase{name: "missing size", mutate: func(c *VisitorClaims) { c.MaxBytes = 0 }},
					tokenCase{name: "negative size", mutate: func(c *VisitorClaims) { c.MaxBytes = -1 }},
					tokenCase{name: "overlong capability", mutate: func(c *VisitorClaims) {
						c.ExpiresAt = jwt.NewNumericDate(c.IssuedAt.Add(VisitorUploadTTL + time.Second))
					}},
				)
			}
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					now := time.Now().Add(-time.Second)
					claims := &VisitorClaims{InboxID: 10, ContactID: 20, SourceID: "source-20", Purpose: parser.purpose,
						RegisteredClaims: jwt.RegisteredClaims{ID: "test-token-id", Subject: "source-20", IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(5 * time.Minute))}}
					if parser.purpose == TokenPurposeVisitorUpload {
						claims.UploadKey, claims.MaxBytes = "blobs/document.txt", 1024
					}
					if test.mutate != nil {
						test.mutate(claims)
					}
					method, key := test.method, test.key
					if method == nil {
						method = jwt.SigningMethodHS256
					}
					if key == nil {
						key = []byte(visitorTestSecret)
					}
					signed, err := jwt.NewWithClaims(method, claims).SignedString(key)
					require.NoError(t, err)
					parsed, err := parser.parse(signed, visitorTestSecret)
					require.ErrorIs(t, err, ErrInvalidVisitorToken)
					require.Nil(t, parsed)
				})
			}
		})
	}
}

type failedVisitorRandom struct{}

func (failedVisitorRandom) Read([]byte) (int, error) { return 0, errors.New("random unavailable") }

func TestVisitorSigningFailsClosed(t *testing.T) {
	for _, random := range []io.Reader{failedVisitorRandom{}, strings.NewReader("short")} {
		signed, err := signVisitorToken(&VisitorClaims{InboxID: 10, ContactID: 20, SourceID: "source-20", Purpose: TokenPurposeVisitorSession}, visitorTestSecret, time.Hour, random)
		require.Error(t, err)
		require.Empty(t, signed)
	}
	for _, bad := range []struct {
		inbox, contact uint
		source, secret string
		ttl            time.Duration
	}{
		{0, 20, "source", visitorTestSecret, time.Hour}, {10, 0, "source", visitorTestSecret, time.Hour},
		{10, 20, "", visitorTestSecret, time.Hour}, {10, 20, "source", "", time.Hour},
		{10, 20, "source", visitorTestSecret, -time.Hour},
	} {
		signed, err := GenerateVisitorToken(bad.inbox, bad.contact, bad.source, bad.secret, bad.ttl)
		require.Error(t, err)
		require.Empty(t, signed)
	}
	for _, ttl := range []time.Duration{-time.Second, VisitorUploadTTL + time.Second} {
		_, err := GenerateVisitorUploadToken(10, 20, "source", "blobs/x.txt", visitorTestSecret, 1, ttl)
		require.Error(t, err)
	}
	for _, parse := range []func(string, string) (*VisitorClaims, error){ParseVisitorToken, ParseVisitorUploadToken} {
		claims, err := parse("malformed", visitorTestSecret)
		require.Error(t, err)
		require.Nil(t, claims)
		_, err = parse("anything", "")
		require.Error(t, err)
	}
}
