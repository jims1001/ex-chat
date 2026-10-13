package auth

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidToken = errors.New("invalid or expired authentication token")
)

const (
	TokenPurposeAccess = "access"
	TokenPurposeMFA    = "mfa"
	MFAChallengeTTL    = 5 * time.Minute
)

type Claims struct {
	UserID      uint   `json:"user_id"`
	Email       string `json:"email"`
	Role        string `json:"role"`
	Type        string `json:"type,omitempty"`
	Purpose     string `json:"purpose"`
	AuthVersion int64  `json:"auth_version"`
	jwt.RegisteredClaims
}

func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

func CheckPasswordHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

func GenerateToken(user *domain.User, secret string, expirationHours int) (string, error) {
	return generateToken(user, secret, TokenPurposeAccess, time.Duration(expirationHours)*time.Hour)
}

// GenerateMFAToken creates a short-lived challenge that cannot authenticate API requests.
func GenerateMFAToken(user *domain.User, secret string) (string, error) {
	return generateToken(user, secret, TokenPurposeMFA, MFAChallengeTTL)
}

func generateToken(user *domain.User, secret, purpose string, ttl time.Duration) (string, error) {
	now := time.Now()
	expirationTime := now.Add(ttl)
	jtiBytes := make([]byte, 16)
	if _, err := rand.Read(jtiBytes); err != nil {
		return "", err
	}

	claims := &Claims{
		UserID:      user.ID,
		Purpose:     purpose,
		AuthVersion: user.AuthVersion,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        hex.EncodeToString(jtiBytes),
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}
	if purpose == TokenPurposeAccess {
		claims.Email = user.Email
		claims.Role = user.Role
		claims.Type = user.Type
		claims.Subject = user.Email
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		logger.WithComponent("jwt").Error("failed to sign jwt token",
			"user_id", user.ID,
			"email", user.Email,
			"error", err.Error(),
		)
		return "", err
	}

	logger.WithComponent("jwt").Debug("jwt token generated successfully",
		"user_id", user.ID,
		"email", user.Email,
		"role", user.Role,
		"expires_at", expirationTime,
	)

	return signed, nil
}

// ValidateToken accepts only access tokens. Legacy untyped tokens are intentionally
// rejected: before purpose separation, MFA challenges were indistinguishable from them.
func ValidateToken(tokenString string, secret string) (*Claims, error) {
	return validateTokenPurpose(tokenString, secret, TokenPurposeAccess)
}

// ValidateMFAToken accepts only short-lived MFA challenges, never access tokens.
func ValidateMFAToken(tokenString string, secret string) (*Claims, error) {
	claims, err := validateTokenPurpose(tokenString, secret, TokenPurposeMFA)
	if err != nil {
		return nil, err
	}
	if claims.ID == "" || claims.IssuedAt == nil || !claims.ExpiresAt.After(claims.IssuedAt.Time) ||
		claims.ExpiresAt.Sub(claims.IssuedAt.Time) > MFAChallengeTTL {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

func validateTokenPurpose(tokenString, secret, purpose string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			logger.WithComponent("jwt").Warn("unexpected jwt signing method",
				"alg", token.Header["alg"],
			)
			return nil, ErrInvalidToken
		}
		return []byte(secret), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired(), jwt.WithIssuedAt())

	if err != nil {
		logger.WithComponent("jwt").Warn("jwt token validation failed",
			"error", err.Error(),
		)
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid && claims.Purpose == purpose && claims.UserID > 0 {
		return claims, nil
	}

	logger.WithComponent("jwt").Warn("jwt claims invalid or expired")
	return nil, ErrInvalidToken
}

// ClaimsMatchUser rejects access and MFA credentials issued before an atomic
// password reset advanced the persisted user's authentication version.
func ClaimsMatchUser(claims *Claims, user *domain.User) bool {
	return claims != nil && user != nil && claims.UserID == user.ID && user.ID != 0 &&
		claims.AuthVersion >= 0 && claims.AuthVersion == user.AuthVersion
}
