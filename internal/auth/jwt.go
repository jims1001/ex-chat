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

type Claims struct {
	UserID uint   `json:"user_id"`
	Email  string `json:"email"`
	Role   string `json:"role"`
	Type   string `json:"type,omitempty"`
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
	expirationTime := time.Now().Add(time.Duration(expirationHours) * time.Hour)
	jtiBytes := make([]byte, 16)
	_, _ = rand.Read(jtiBytes)

	claims := &Claims{
		UserID: user.ID,
		Email:  user.Email,
		Role:   user.Role,
		Type:   user.Type,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        hex.EncodeToString(jtiBytes),
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Subject:   user.Email,
		},
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

func ValidateToken(tokenString string, secret string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			logger.WithComponent("jwt").Warn("unexpected jwt signing method",
				"alg", token.Header["alg"],
			)
			return nil, ErrInvalidToken
		}
		return []byte(secret), nil
	})

	if err != nil {
		logger.WithComponent("jwt").Warn("jwt token validation failed",
			"error", err.Error(),
		)
		return nil, err
	}

	if claims, ok := token.Claims.(*Claims); ok && token.Valid {
		return claims, nil
	}

	logger.WithComponent("jwt").Warn("jwt claims invalid or expired")
	return nil, ErrInvalidToken
}
