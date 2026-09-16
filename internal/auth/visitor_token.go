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
	UploadKey string `json:"upload_key,omitempty"`
	MaxBytes  int64  `json:"max_bytes,omitempty"`
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

// GenerateVisitorUploadToken creates a short-lived capability bound to one
// visitor, inbox, upload key, and negotiated maximum size.
func GenerateVisitorUploadToken(inboxID, contactID uint, sourceID, uploadKey, secret string, maxBytes int64, ttl time.Duration) (string, error) {
	if uploadKey == "" || maxBytes <= 0 {
		return "", ErrInvalidVisitorToken
	}
	if ttl <= 0 {
		ttl = 15 * time.Minute
	}

	claims := &VisitorClaims{
		InboxID:   inboxID,
		ContactID: contactID,
		SourceID:  sourceID,
		UploadKey: uploadKey,
		MaxBytes:  maxBytes,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
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
