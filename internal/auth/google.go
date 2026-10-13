package auth

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var ErrGoogleNotConfigured = errors.New("Google login unavailable: GOOGLE_CLIENT_ID is not configured")

const googleCertificatesURL = "https://www.googleapis.com/oauth2/v1/certs"

type googleClaims struct {
	Email           string `json:"email"`
	EmailVerified   bool   `json:"email_verified"`
	Name            string `json:"name"`
	HostedDomain    string `json:"hd"`
	AuthorizedParty string `json:"azp"`
	jwt.RegisteredClaims
}

// GoogleVerifier verifies ID tokens against Google's HTTPS certificate endpoint.
// No token-supplied URL, certificate, or key can select the trust source.
type GoogleVerifier struct {
	client     *http.Client
	mu         sync.Mutex
	keys       map[string]*rsa.PublicKey
	expires    time.Time
	retryAfter time.Time
	refreshing chan struct{}
}

func NewGoogleVerifier() *GoogleVerifier {
	return &GoogleVerifier{client: &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

var defaultGoogleVerifier = NewGoogleVerifier()

// ParseGoogleIDToken requires an explicitly configured audience. The variadic
// argument preserves source compatibility, but calls without it fail closed.
func ParseGoogleIDToken(tokenString string, audience ...string) (string, string, error) {
	if len(audience) != 1 {
		return "", "", ErrGoogleNotConfigured
	}
	return defaultGoogleVerifier.Verify(context.Background(), tokenString, audience[0])
}

func (v *GoogleVerifier) Verify(ctx context.Context, raw, audience string) (string, string, error) {
	if strings.TrimSpace(audience) == "" {
		return "", "", ErrGoogleNotConfigured
	}
	if raw == "" || len(raw) > 16384 {
		return "", "", ErrInvalidToken
	}
	claims := &googleClaims{}
	token, err := jwt.ParseWithClaims(raw, claims, func(t *jwt.Token) (any, error) {
		kid, ok := t.Header["kid"].(string)
		if !ok || kid == "" {
			return nil, ErrInvalidToken
		}
		return v.key(ctx, kid)
	}, jwt.WithValidMethods([]string{"RS256"}), jwt.WithAudience(audience),
		jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil || !token.Valid {
		return "", "", ErrInvalidToken
	}
	if claims.Issuer != "accounts.google.com" && claims.Issuer != "https://accounts.google.com" {
		return "", "", ErrInvalidToken
	}
	if claims.Subject == "" || !claims.EmailVerified || claims.IssuedAt == nil ||
		(claims.AuthorizedParty != "" && claims.AuthorizedParty != audience) ||
		(len(claims.Audience) > 1 && claims.AuthorizedParty != audience) {
		return "", "", ErrInvalidToken
	}
	addr, err := mail.ParseAddress(claims.Email)
	if err != nil || addr.Address != claims.Email {
		return "", "", ErrInvalidToken
	}
	// Existing accounts are linked by email. Only link addresses for which
	// Google is authoritative; third-party Google-account emails need a separate
	// account-linking challenge, even if email_verified was true in the past.
	if !strings.HasSuffix(strings.ToLower(claims.Email), "@gmail.com") && claims.HostedDomain == "" {
		return "", "", ErrInvalidToken
	}
	return claims.Email, claims.Name, nil
}

func (v *GoogleVerifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		v.mu.Lock()
		if time.Now().Before(v.expires) {
			key := v.keys[kid]
			v.mu.Unlock()
			if key != nil {
				return key, nil
			}
			return nil, ErrInvalidToken
		}
		if time.Now().Before(v.retryAfter) {
			v.mu.Unlock()
			return nil, errors.New("Google signing keys temporarily unavailable")
		}
		done := v.refreshing
		if done == nil {
			done = make(chan struct{})
			v.refreshing = done
			go v.refreshKeys(done)
		}
		v.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-done:
			continue
		}
	}
}

// A shared refresh has its own strict deadline. A disconnected initiating
// request must not cancel other callers' verification or poison outage backoff.
func (v *GoogleVerifier) refreshKeys(done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	keys, expires, err := v.fetchKeys(ctx)
	v.mu.Lock()
	defer v.mu.Unlock()
	if err != nil {
		v.retryAfter = time.Now().Add(5 * time.Second)
	} else {
		v.keys, v.expires = keys, expires
		v.retryAfter = time.Time{}
	}
	v.refreshing = nil
	close(done)
}

func (v *GoogleVerifier) fetchKeys(ctx context.Context) (map[string]*rsa.PublicKey, time.Time, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleCertificatesURL, nil)
	if err != nil {
		return nil, time.Time{}, err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, time.Time{}, errors.New("Google signing keys unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil || len(body) > 1<<20 {
		return nil, time.Time{}, errors.New("invalid Google signing key response")
	}
	var certs map[string]string
	if err := json.Unmarshal(body, &certs); err != nil {
		return nil, time.Time{}, err
	}
	keys := make(map[string]*rsa.PublicKey)
	expires := time.Now().Add(5 * time.Minute)
	for id, value := range certs {
		block, _ := pem.Decode([]byte(value))
		if block == nil || block.Type != "CERTIFICATE" {
			return nil, time.Time{}, ErrInvalidToken
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, time.Time{}, err
		}
		if time.Now().Before(cert.NotBefore) || !time.Now().Before(cert.NotAfter) {
			return nil, time.Time{}, ErrInvalidToken
		}
		key, ok := cert.PublicKey.(*rsa.PublicKey)
		if !ok || key.N.BitLen() < 2048 {
			return nil, time.Time{}, ErrInvalidToken
		}
		keys[id] = key
		if cert.NotAfter.Before(expires) {
			expires = cert.NotAfter
		}
	}
	if len(keys) == 0 {
		return nil, time.Time{}, ErrInvalidToken
	}
	return keys, expires, nil
}
