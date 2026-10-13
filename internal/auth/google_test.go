package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type googleTestTransport func(*http.Request) (*http.Response, error)

func (f googleTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestGoogleIDTokenValidation(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	base := func() *googleClaims {
		return &googleClaims{
			Email: "user@gmail.com", EmailVerified: true, Name: "Verified User",
			RegisteredClaims: jwt.RegisteredClaims{Issuer: "https://accounts.google.com", Subject: "google-subject",
				Audience: jwt.ClaimStrings{"configured-client"}, ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)), IssuedAt: jwt.NewNumericDate(now.Add(-time.Minute))},
		}
	}
	for _, tc := range []struct {
		name    string
		change  func(*googleClaims)
		signKey *rsa.PrivateKey
		kid     string
		valid   bool
	}{
		{name: "valid", kid: "trusted", valid: true},
		{name: "legacy issuer", kid: "trusted", valid: true, change: func(c *googleClaims) { c.Issuer = "accounts.google.com" }},
		{name: "wrong issuer", kid: "trusted", change: func(c *googleClaims) { c.Issuer = "https://attacker.example" }},
		{name: "wrong audience", kid: "trusted", change: func(c *googleClaims) { c.Audience = jwt.ClaimStrings{"other-client"} }},
		{name: "missing audience", kid: "trusted", change: func(c *googleClaims) { c.Audience = nil }},
		{name: "expired", kid: "trusted", change: func(c *googleClaims) { c.ExpiresAt = jwt.NewNumericDate(now.Add(-time.Minute)) }},
		{name: "missing expiry", kid: "trusted", change: func(c *googleClaims) { c.ExpiresAt = nil }},
		{name: "unverified email", kid: "trusted", change: func(c *googleClaims) { c.EmailVerified = false }},
		{name: "missing subject", kid: "trusted", change: func(c *googleClaims) { c.Subject = "" }},
		{name: "future issued", kid: "trusted", change: func(c *googleClaims) { c.IssuedAt = jwt.NewNumericDate(now.Add(time.Hour)) }},
		{name: "missing issued", kid: "trusted", change: func(c *googleClaims) { c.IssuedAt = nil }},
		{name: "bad email", kid: "trusted", change: func(c *googleClaims) { c.Email = "Name <user@gmail.com>" }},
		{name: "non authoritative email", kid: "trusted", change: func(c *googleClaims) { c.Email = "user@thirdparty.example" }},
		{name: "workspace", kid: "trusted", valid: true, change: func(c *googleClaims) { c.Email = "user@corp.example"; c.HostedDomain = "corp.example" }},
		{name: "wrong azp", kid: "trusted", change: func(c *googleClaims) { c.AuthorizedParty = "another-client" }},
		{name: "multi audience missing azp", kid: "trusted", change: func(c *googleClaims) { c.Audience = jwt.ClaimStrings{"configured-client", "another-client"} }},
		{name: "forged signature", kid: "trusted", signKey: other},
		{name: "unknown kid", kid: "attacker"},
		{name: "missing kid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := base()
			if tc.change != nil {
				tc.change(claims)
			}
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
			token.Header["kid"] = tc.kid
			sk := key
			if tc.signKey != nil {
				sk = tc.signKey
			}
			raw, err := token.SignedString(sk)
			if err != nil {
				t.Fatal(err)
			}
			v := &GoogleVerifier{keys: map[string]*rsa.PublicKey{"trusted": &key.PublicKey}, expires: now.Add(time.Hour)}
			email, name, err := v.Verify(context.Background(), raw, "configured-client")
			if tc.valid {
				if err != nil || email != claims.Email || name != claims.Name {
					t.Fatalf("valid token rejected: %v", err)
				}
			} else if err == nil || email != "" || name != "" {
				t.Fatal("untrusted identity accepted")
			}
		})
	}
	v := &GoogleVerifier{keys: map[string]*rsa.PublicKey{"trusted": &key.PublicKey}, expires: now.Add(time.Hour)}
	for _, method := range []jwt.SigningMethod{jwt.SigningMethodNone, jwt.SigningMethodHS256} {
		tok := jwt.NewWithClaims(method, base())
		tok.Header["kid"] = "trusted"
		var signingKey any = []byte("forged")
		if method == jwt.SigningMethodNone {
			signingKey = jwt.UnsafeAllowNoneSignatureType
		}
		raw, err := tok.SignedString(signingKey)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = v.Verify(context.Background(), raw, "configured-client"); err == nil {
			t.Fatal("wrong algorithm accepted")
		}
	}
	if _, _, err := ParseGoogleIDToken(GenerateMockGoogleIDToken("attacker@gmail.com", "Attacker")); err != ErrGoogleNotConfigured {
		t.Fatalf("missing client ID should fail closed: %v", err)
	}
	if _, _, err := v.Verify(context.Background(), GenerateMockGoogleIDToken("attacker@gmail.com", "Attacker"), "configured-client"); err == nil {
		t.Fatal("old forged mock accepted")
	}
}

func TestGoogleSigningKeysTrustedSourceAndCache(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"known": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))})
	calls := 0
	v := NewGoogleVerifier()
	v.client.Transport = googleTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != googleCertificatesURL {
			t.Fatalf("untrusted key URL: %s", r.URL)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})
	for i := 0; i < 2; i++ {
		if _, err := v.key(context.Background(), "known"); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("expected cached keys, requests=%d", calls)
	}
	if _, err := v.key(context.Background(), "unknown"); err == nil {
		t.Fatal("unknown signing key accepted")
	}
	v.expires = now.Add(-time.Second)
	v.client.Transport = googleTestTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
	})
	if _, err := v.key(context.Background(), "known"); err == nil {
		t.Fatal("expired cache accepted on key fetch failure")
	}
}

func TestSAMLSignatureFailsClosed(t *testing.T) {
	for _, payload := range []string{"", "<SignatureValue>forged</SignatureValue>", GenerateMockSAMLResponse("admin@example.com", "Admin", "User")} {
		for _, cert := range []string{"", "malformed certificate"} {
			ok, err := VerifySAMLSignature(payload, cert)
			if ok || err != ErrSAMLVerificationUnavailable {
				t.Fatal("unverified SAML accepted")
			}
		}
	}
}

func TestGoogleKeyRefreshWaitCanCancelAndFailuresBackOff(t *testing.T) {
	v := NewGoogleVerifier()
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	v.client.Transport = googleTestTransport(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
	})
	firstDone := make(chan error, 1)
	go func() { _, err := v.key(context.Background(), "trusted"); firstDone <- err }()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := v.key(ctx, "trusted"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait must respect context: %v", err)
	}
	close(release)
	if err := <-firstDone; err == nil {
		t.Fatal("503 accepted")
	}
	for i := 0; i < 10; i++ {
		if _, err := v.key(context.Background(), "trusted"); err == nil {
			t.Fatal("failed key response accepted")
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("failed refresh should back off, got %d calls", got)
	}
}

func TestGoogleInitiatingCallerCannotCancelSharedRefresh(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	cert := &x509.Certificate{SerialNumber: big.NewInt(2), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"trusted": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))})
	started, release := make(chan struct{}), make(chan struct{})
	v := NewGoogleVerifier()
	var calls atomic.Int32
	v.client.Transport = googleTestTransport(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-release:
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := v.key(ctx, "trusted"); first <- err }()
	<-started
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("initiator should cancel: %v", err)
	}
	second := make(chan error, 1)
	go func() { _, err := v.key(context.Background(), "trusted"); second <- err }()
	close(release)
	if err := <-second; err != nil {
		t.Fatalf("live caller should receive shared keys: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected one shared fetch, got %d", calls.Load())
	}
}
