package auth

import (
	"testing"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/golang-jwt/jwt/v5"
)

const jwtTestSecret = "mfa-purpose-regression-test-secret"

func TestGeneratedTokenPurposesAreIsolated(t *testing.T) {
	user := &domain.User{ID: 42, Email: "agent@example.test", Role: domain.RoleAdministrator, Type: "SuperAdmin"}
	accessToken, err := GenerateToken(user, jwtTestSecret, 24)
	if err != nil {
		t.Fatal(err)
	}
	access, err := ValidateToken(accessToken, jwtTestSecret)
	if err != nil {
		t.Fatal(err)
	}
	if access.Purpose != TokenPurposeAccess || access.UserID != user.ID || access.Email != user.Email ||
		access.Role != user.Role || access.Type != user.Type || access.Subject != user.Email {
		t.Fatal("ordinary access token lost identity claims")
	}
	if access.ExpiresAt.Sub(access.IssuedAt.Time) != 24*time.Hour {
		t.Fatal("ordinary access token lifetime changed")
	}
	if claims, err := ValidateMFAToken(accessToken, jwtTestSecret); err == nil || claims != nil {
		t.Fatal("access token was accepted as an MFA challenge")
	}

	mfaToken, err := GenerateMFAToken(user, jwtTestSecret)
	if err != nil {
		t.Fatal(err)
	}
	mfa, err := ValidateMFAToken(mfaToken, jwtTestSecret)
	if err != nil {
		t.Fatal(err)
	}
	if mfa.Purpose != TokenPurposeMFA || mfa.UserID != user.ID || mfa.ID == "" {
		t.Fatal("MFA challenge is missing its purpose, user, or unique identifier")
	}
	if mfa.ExpiresAt.Sub(mfa.IssuedAt.Time) != MFAChallengeTTL || MFAChallengeTTL > 5*time.Minute {
		t.Fatal("MFA challenge must expire within five minutes")
	}
	if mfa.Email != "" || mfa.Role != "" || mfa.Type != "" || mfa.Subject != "" {
		t.Fatal("MFA challenge must not carry access-only identity and privilege claims")
	}
	if claims, err := ValidateToken(mfaToken, jwtTestSecret); err == nil || claims != nil {
		t.Fatal("MFA challenge was accepted as an access token")
	}
	if access.ID == mfa.ID {
		t.Fatal("independently generated tokens must have different identifiers")
	}
}

func TestTokenValidationRejectsInvalidClaimsAndAlgorithms(t *testing.T) {
	tests := []struct {
		name   string
		method jwt.SigningMethod
		mutate func(*Claims)
		key    any
	}{
		{name: "legacy untyped token", mutate: func(c *Claims) { c.Purpose = "" }},
		{name: "unknown purpose", mutate: func(c *Claims) { c.Purpose = "visitor" }},
		{name: "missing user", mutate: func(c *Claims) { c.UserID = 0 }},
		{name: "missing expiry", mutate: func(c *Claims) { c.ExpiresAt = nil }},
		{name: "expired", mutate: func(c *Claims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute)) }},
		{name: "future issued at", mutate: func(c *Claims) { c.IssuedAt = jwt.NewNumericDate(time.Now().Add(time.Hour)) }},
		{name: "future not before", mutate: func(c *Claims) { c.NotBefore = jwt.NewNumericDate(time.Now().Add(time.Hour)) }},
		{name: "HS384", method: jwt.SigningMethodHS384},
		{name: "HS512", method: jwt.SigningMethodHS512},
		{name: "unsigned", method: jwt.SigningMethodNone, key: jwt.UnsafeAllowNoneSignatureType},
		{name: "wrong signature", key: []byte("different-test-secret")},
	}
	validators := []struct {
		purpose  string
		validate func(string, string) (*Claims, error)
	}{
		{TokenPurposeAccess, ValidateToken},
		{TokenPurposeMFA, ValidateMFAToken},
	}
	for _, validator := range validators {
		t.Run(validator.purpose, func(t *testing.T) {
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) {
					claims := validTestClaims(validator.purpose)
					if test.mutate != nil {
						test.mutate(claims)
					}
					method := test.method
					if method == nil {
						method = jwt.SigningMethodHS256
					}
					key := test.key
					if key == nil {
						key = []byte(jwtTestSecret)
					}
					token, err := jwt.NewWithClaims(method, claims).SignedString(key)
					if err != nil {
						t.Fatal(err)
					}
					if result, err := validator.validate(token, jwtTestSecret); err == nil || result != nil {
						t.Fatal("invalid token was accepted")
					}
				})
			}
		})
	}
}

func TestMFAChallengeLifetimeIsEnforced(t *testing.T) {
	for name, mutate := range map[string]func(*Claims){
		"missing challenge ID": func(c *Claims) { c.ID = "" },
		"missing issued at":    func(c *Claims) { c.IssuedAt = nil },
		"long lifetime":        func(c *Claims) { c.ExpiresAt = jwt.NewNumericDate(c.IssuedAt.Add(time.Hour)) },
		"expiry before issuance": func(c *Claims) {
			c.IssuedAt = jwt.NewNumericDate(time.Now().Add(2 * time.Minute))
			c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(time.Minute))
		},
	} {
		t.Run(name, func(t *testing.T) {
			claims := validTestClaims(TokenPurposeMFA)
			mutate(claims)
			token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(jwtTestSecret))
			if err != nil {
				t.Fatal(err)
			}
			if result, err := ValidateMFAToken(token, jwtTestSecret); err == nil || result != nil {
				t.Fatal("MFA challenge with invalid lifetime was accepted")
			}
		})
	}
}

func validTestClaims(purpose string) *Claims {
	now := time.Now()
	return &Claims{
		UserID:  42,
		Purpose: purpose,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        "test-challenge-id",
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(MFAChallengeTTL)),
		},
	}
}

func TestUserAuthVersionRevokesAccessAndMFAClaims(t *testing.T) {
	user := &domain.User{ID: 42, Email: "version@example.test", AuthVersion: 3}
	access, err := GenerateToken(user, jwtTestSecret, 24)
	if err != nil {
		t.Fatal(err)
	}
	challenge, err := GenerateMFAToken(user, jwtTestSecret)
	if err != nil {
		t.Fatal(err)
	}
	a, err := ValidateToken(access, jwtTestSecret)
	if err != nil {
		t.Fatal(err)
	}
	m, err := ValidateMFAToken(challenge, jwtTestSecret)
	if err != nil {
		t.Fatal(err)
	}
	if !ClaimsMatchUser(a, user) || !ClaimsMatchUser(m, user) {
		t.Fatal("current auth version should match")
	}
	user.AuthVersion++
	if ClaimsMatchUser(a, user) || ClaimsMatchUser(m, user) {
		t.Fatal("pre-reset credentials still match")
	}
	if ClaimsMatchUser(nil, user) || ClaimsMatchUser(a, nil) {
		t.Fatal("missing identity accepted")
	}
}
