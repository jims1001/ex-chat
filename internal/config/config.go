package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const (
	defaultJWTExpirationHours = 72
	maxJWTExpirationHours     = 30 * 24
	minimumJWTSecretBytes     = 32
)

type Config struct {
	Port                    string
	AutomationExecutionMode string
	Environment             string
	DBDriver                string
	DBPath                  string
	DBDSN                   string
	RedisURL                string
	JWTSecret               string
	JWTExpirationHours      int
	GoogleClientID          string
	LogLevel                string
	LogFormat               string
	SuperAdminEmails        []string
	TrustedProxies          []string
}

func (c *Config) IsSuperAdminEmail(email string) bool {
	if c == nil || email == "" {
		return false
	}
	target := strings.ToLower(strings.TrimSpace(email))
	for _, adminEmail := range c.SuperAdminEmails {
		if target == strings.ToLower(strings.TrimSpace(adminEmail)) {
			return true
		}
	}
	return false
}

// LoadConfig validates security settings before any server services are started.
// A missing key is only allowed in development/test modes, where a
// cryptographically random, process-local key makes local startup convenient.
func LoadConfig() (*Config, error) {
	port := getEnv("PORT", "8080")
	env := strings.ToLower(strings.TrimSpace(getEnv("ENV", "development")))
	if env == "" {
		return nil, errors.New("ENV must not be whitespace")
	}
	dbDriver := getEnv("DB_DRIVER", "sqlite")
	dbPath := getEnv("DB_PATH", "ex_chat.db")
	dbDSN := getEnv("DB_DSN", "")
	redisURL := getEnv("REDIS_URL", "")
	jwtSecret, err := loadJWTSecret(env, rand.Reader)
	if err != nil {
		return nil, err
	}
	jwtExpHoursStr := getEnv("JWT_EXPIRATION_HOURS", strconv.Itoa(defaultJWTExpirationHours))
	jwtExpHours, err := strconv.Atoi(jwtExpHoursStr)
	if err != nil || jwtExpHours < 1 || jwtExpHours > maxJWTExpirationHours {
		return nil, fmt.Errorf("JWT_EXPIRATION_HOURS must be an integer between 1 and %d", maxJWTExpirationHours)
	}
	logLevel := getEnv("LOG_LEVEL", "info")
	logFormat := getEnv("LOG_FORMAT", "json")
	trustedProxiesRaw := getEnv("TRUSTED_PROXIES", "")
	var trustedProxies []string
	for _, proxy := range strings.Split(trustedProxiesRaw, ",") {
		if trimmed := strings.TrimSpace(proxy); trimmed != "" {
			trustedProxies = append(trustedProxies, trimmed)
		}
	}

	superAdminEmailsRaw := getEnv("SUPER_ADMIN_EMAILS", "")
	var superAdminEmails []string
	if superAdminEmailsRaw != "" {
		for _, e := range strings.Split(superAdminEmailsRaw, ",") {
			trimmed := strings.ToLower(strings.TrimSpace(e))
			if trimmed != "" {
				superAdminEmails = append(superAdminEmails, trimmed)
			}
		}
	}

	automationMode := strings.ToLower(strings.TrimSpace(getEnv("AUTOMATION_EXECUTION_MODE", "legacy")))
	if automationMode != "legacy" && automationMode != "durable" {
		return nil, errors.New("AUTOMATION_EXECUTION_MODE must be legacy or durable")
	}
	return &Config{
		Port:                    port,
		AutomationExecutionMode: automationMode,
		Environment:             env,
		DBDriver:                dbDriver,
		DBPath:                  dbPath,
		DBDSN:                   dbDSN,
		RedisURL:                redisURL,
		JWTSecret:               jwtSecret,
		JWTExpirationHours:      jwtExpHours,
		GoogleClientID:          strings.TrimSpace(getEnv("GOOGLE_CLIENT_ID", "")),
		LogLevel:                logLevel,
		LogFormat:               logFormat,
		SuperAdminEmails:        superAdminEmails,
		TrustedProxies:          trustedProxies,
	}, nil
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return defaultVal
}

// loadJWTSecret never includes a configured secret in errors or logs.
func loadJWTSecret(environment string, random io.Reader) (string, error) {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		switch environment {
		case "development", "dev", "test":
			key := make([]byte, minimumJWTSecretBytes)
			if _, err := io.ReadFull(random, key); err != nil {
				return "", errors.New("could not generate ephemeral development JWT_SECRET")
			}
			return hex.EncodeToString(key), nil
		default:
			return "", errors.New("JWT_SECRET is required outside development/test mode")
		}
	}

	if len(secret) < minimumJWTSecretBytes || strings.TrimSpace(secret) != secret {
		return "", fmt.Errorf("JWT_SECRET must contain at least %d bytes without surrounding whitespace", minimumJWTSecretBytes)
	}

	// These published examples must never become signing keys, even in dev.
	// Length alone cannot make a known value a secret.
	normalized := strings.ToLower(strings.NewReplacer("_", "-", " ", "-").Replace(secret))
	for _, placeholder := range []string{
		"ex-chat-production-ready-jwt-secret-key-2026",
		"replace-with-", "change-me", "changeme", "your-secret", "your-jwt-secret",
	} {
		if strings.Contains(normalized, placeholder) {
			return "", errors.New("JWT_SECRET must not be a default or placeholder value; generate a random secret")
		}
	}
	// Reject trivially repeated values. This is not an entropy estimate: operators
	// must still generate a random key using a cryptographically secure tool.
	for size := 1; size <= len(secret)/2; size++ {
		if len(secret)%size == 0 && strings.Repeat(secret[:size], len(secret)/size) == secret {
			return "", errors.New("JWT_SECRET must not be a repeated value; generate a random secret")
		}
	}
	return secret, nil
}
