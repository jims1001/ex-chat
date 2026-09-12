package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port               string
	Environment        string
	DBDriver           string
	DBPath             string
	JWTSecret          string
	JWTExpirationHours int
	LogLevel           string
	LogFormat          string
	SuperAdminEmails   []string
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

func LoadConfig() *Config {
	port := getEnv("PORT", "8080")
	env := getEnv("ENV", "development")
	dbDriver := getEnv("DB_DRIVER", "sqlite")
	dbPath := getEnv("DB_PATH", "ex_chat.db")
	jwtSecret := getEnv("JWT_SECRET", "ex-chat-production-ready-jwt-secret-key-2026")
	jwtExpHoursStr := getEnv("JWT_EXPIRATION_HOURS", "72")
	jwtExpHours, err := strconv.Atoi(jwtExpHoursStr)
	if err != nil {
		jwtExpHours = 72
	}
	logLevel := getEnv("LOG_LEVEL", "info")
	logFormat := getEnv("LOG_FORMAT", "json")

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

	return &Config{
		Port:               port,
		Environment:        env,
		DBDriver:           dbDriver,
		DBPath:             dbPath,
		JWTSecret:          jwtSecret,
		JWTExpirationHours: jwtExpHours,
		LogLevel:           logLevel,
		LogFormat:          logFormat,
		SuperAdminEmails:   superAdminEmails,
	}
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return defaultVal
}
