package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrInvalidVisitorToken = errors.New("invalid or expired visitor session token")
)

// VisitorClaims represents the claims encoded in a visitor session token
type VisitorClaims struct {
	InboxID   uint   `json:"inbox_id"`
	ContactID uint   `json:"contact_id"`
	SourceID  string `json:"source_id"`
	jwt.RegisteredClaims
}

// GenerateVisitorToken creates a cryptographically signed session token for widget visitors
func GenerateVisitorToken(inboxID, contactID uint, sourceID string, secret string, ttl time.Duration) (string, error) {
	if ttl == 0 {
		ttl = 72 * time.Hour
	}
	expirationTime := time.Now().Add(ttl)

	jtiBytes := make([]byte, 16)
	_, _ = rand.Read(jtiBytes)

	claims := &VisitorClaims{
		InboxID:   inboxID,
		ContactID: contactID,
		SourceID:  sourceID,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        hex.EncodeToString(jtiBytes),
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   sourceID,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ParseVisitorToken validates and decodes a visitor session token
func ParseVisitorToken(tokenString string, secret string) (*VisitorClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &VisitorClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidVisitorToken
		}
		return []byte(secret), nil
	})

	if err != nil {
		return nil, ErrInvalidVisitorToken
	}

	claims, ok := token.Claims.(*VisitorClaims)
	if !ok || !token.Valid {
		return nil, ErrInvalidVisitorToken
	}

	return claims, nil
}
