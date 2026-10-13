package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var ErrInvalidVisitorToken = errors.New("invalid or expired visitor token")

const (
	TokenPurposeVisitorSession = "visitor_session"
	TokenPurposeVisitorUpload  = "visitor_upload"
	VisitorUploadTTL           = 15 * time.Minute
)

// VisitorClaims carries a visitor session or a single-upload capability. The
// purpose-specific parsers must be used at each authorization boundary.
type VisitorClaims struct {
	InboxID   uint   `json:"inbox_id"`
	ContactID uint   `json:"contact_id"`
	SourceID  string `json:"source_id"`
	Purpose   string `json:"purpose"`
	UploadKey string `json:"upload_key,omitempty"`
	MaxBytes  int64  `json:"max_bytes,omitempty"`
	jwt.RegisteredClaims
}

// GenerateVisitorToken creates a cryptographically signed session token for widget visitors.
func GenerateVisitorToken(inboxID, contactID uint, sourceID string, secret string, ttl time.Duration) (string, error) {
	if ttl == 0 {
		ttl = 72 * time.Hour
	}
	return signVisitorToken(&VisitorClaims{
		InboxID: inboxID, ContactID: contactID, SourceID: sourceID,
		Purpose: TokenPurposeVisitorSession,
	}, secret, ttl, rand.Reader)
}

// GenerateVisitorUploadToken creates a short-lived capability bound to one
// visitor, inbox, upload key, and negotiated maximum size.
func GenerateVisitorUploadToken(inboxID, contactID uint, sourceID, uploadKey, secret string, maxBytes int64, ttl time.Duration) (string, error) {
	if strings.TrimSpace(uploadKey) == "" || maxBytes <= 0 {
		return "", ErrInvalidVisitorToken
	}
	if ttl == 0 {
		ttl = VisitorUploadTTL
	}
	if ttl > VisitorUploadTTL {
		return "", ErrInvalidVisitorToken
	}
	return signVisitorToken(&VisitorClaims{
		InboxID: inboxID, ContactID: contactID, SourceID: sourceID,
		Purpose: TokenPurposeVisitorUpload, UploadKey: uploadKey, MaxBytes: maxBytes,
	}, secret, ttl, rand.Reader)
}

func signVisitorToken(claims *VisitorClaims, secret string, ttl time.Duration, random io.Reader) (string, error) {
	if strings.TrimSpace(secret) == "" || ttl <= 0 || !validVisitorIdentity(claims) {
		return "", ErrInvalidVisitorToken
	}
	jtiBytes := make([]byte, 16)
	if _, err := io.ReadFull(random, jtiBytes); err != nil {
		return "", err
	}
	now := time.Now()
	claims.RegisteredClaims = jwt.RegisteredClaims{
		ID: hex.EncodeToString(jtiBytes), Subject: claims.SourceID,
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)), IssuedAt: jwt.NewNumericDate(now),
	}
	if !claims.ExpiresAt.After(claims.IssuedAt.Time) {
		return "", ErrInvalidVisitorToken
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
}

// ParseVisitorToken accepts only signed visitor sessions, never upload
// capabilities or legacy tokens without an explicit purpose and lifetime.
func ParseVisitorToken(tokenString string, secret string) (*VisitorClaims, error) {
	return parseVisitorTokenPurpose(tokenString, secret, TokenPurposeVisitorSession)
}

// ParseVisitorUploadToken accepts only a short-lived upload capability. The
// caller must also match its inbox, contact, upload key, and size restrictions.
func ParseVisitorUploadToken(tokenString string, secret string) (*VisitorClaims, error) {
	return parseVisitorTokenPurpose(tokenString, secret, TokenPurposeVisitorUpload)
}

func parseVisitorTokenPurpose(tokenString, secret, purpose string) (*VisitorClaims, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, ErrInvalidVisitorToken
	}
	token, err := jwt.ParseWithClaims(tokenString, &VisitorClaims{}, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, ErrInvalidVisitorToken
		}
		return []byte(secret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil || token == nil || !token.Valid {
		return nil, ErrInvalidVisitorToken
	}
	claims, ok := token.Claims.(*VisitorClaims)
	if !ok || claims.Purpose != purpose || !validVisitorIdentity(claims) ||
		claims.Subject != claims.SourceID || strings.TrimSpace(claims.ID) == "" ||
		claims.ExpiresAt == nil || claims.IssuedAt == nil || !claims.ExpiresAt.After(claims.IssuedAt.Time) {
		return nil, ErrInvalidVisitorToken
	}
	if purpose == TokenPurposeVisitorSession {
		if claims.UploadKey != "" || claims.MaxBytes != 0 {
			return nil, ErrInvalidVisitorToken
		}
	} else if strings.TrimSpace(claims.UploadKey) == "" || claims.MaxBytes <= 0 || claims.ExpiresAt.Sub(claims.IssuedAt.Time) > VisitorUploadTTL {
		return nil, ErrInvalidVisitorToken
	}
	return claims, nil
}

func validVisitorIdentity(claims *VisitorClaims) bool {
	return claims.InboxID > 0 && claims.ContactID > 0 && claims.SourceID != "" && strings.TrimSpace(claims.SourceID) == claims.SourceID
}
