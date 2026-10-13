package config

import (
	"bytes"
	"encoding/hex"
	"strconv"
	"strings"
	"testing"
)

// A public fixture for validation tests only; never a deployment secret.
const validTestJWTSecret = "9f06b37ec7d125a0ea1439805d468da139cf0a27ebb6100f8a872351adb9c264"

func resetConfigEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"PORT", "ENV", "DB_DRIVER", "DB_PATH", "DB_DSN", "REDIS_URL",
		"JWT_SECRET", "JWT_EXPIRATION_HOURS", "GOOGLE_CLIENT_ID", "LOG_LEVEL",
		"LOG_FORMAT", "SUPER_ADMIN_EMAILS", "TRUSTED_PROXIES",
	} {
		t.Setenv(key, "")
	}
}

func TestLoadConfigRejectsUnsafeJWTSecrets(t *testing.T) {
	unsafe := map[string]string{
		"missing":             "",
		"blank":               strings.Repeat(" ", 40),
		"short":               "short-key",
		"31 bytes":            validTestJWTSecret[:31],
		"published default":   "ex-chat-production-ready-jwt-secret-key-2026",
		"uppercase default":   "EX-CHAT-PRODUCTION-READY-JWT-SECRET-KEY-2026",
		"placeholder":         "replace-with-a-strong-random-password",
		"change me":           "change_me_before_you_start_the_application",
		"your secret":         "your-jwt-secret-key-must-be-long-enough",
		"repeated byte":       strings.Repeat("a", 64),
		"repeated pattern":    strings.Repeat("1234567890", 4),
		"leading whitespace":  " " + validTestJWTSecret,
		"trailing whitespace": validTestJWTSecret + "\n",
	}
	for _, environment := range []string{"production", "prod", "staging", "preview", "unknown"} {
		for name, secret := range unsafe {
			t.Run(environment+"/"+name, func(t *testing.T) {
				resetConfigEnvironment(t)
				t.Setenv("ENV", environment)
				t.Setenv("JWT_SECRET", secret)
				cfg, err := LoadConfig()
				if err == nil || cfg != nil {
					t.Fatal("unsafe JWT configuration must fail without returning a usable config")
				}
				if !strings.Contains(err.Error(), "JWT_SECRET") {
					t.Fatalf("error should identify JWT_SECRET, got %v", err)
				}
				if secret != "" && strings.Contains(err.Error(), secret) {
					t.Fatal("error leaked the configured secret")
				}
			})
		}
	}
}

func TestLoadConfigRejectsUnsafeExplicitDevSecret(t *testing.T) {
	resetConfigEnvironment(t)
	t.Setenv("ENV", "development")
	t.Setenv("JWT_SECRET", "ex-chat-production-ready-jwt-secret-key-2026")
	if cfg, err := LoadConfig(); err == nil || cfg != nil {
		t.Fatal("an explicitly configured public key must not be accepted even in development")
	}
}

func TestLoadConfigDevelopmentSecretIsRandomAndEphemeral(t *testing.T) {
	for _, environment := range []string{"", "development", "dev", "test", " DeVeLoPmEnT "} {
		t.Run(environment, func(t *testing.T) {
			resetConfigEnvironment(t)
			t.Setenv("ENV", environment)
			first, err := LoadConfig()
			if err != nil {
				t.Fatal(err)
			}
			second, err := LoadConfig()
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := hex.DecodeString(first.JWTSecret)
			if err != nil || len(decoded) != 32 {
				t.Fatal("development key must contain 32 random bytes encoded as hex")
			}
			if first.JWTSecret == second.JWTSecret {
				t.Fatal("independent loads must not share a predictable default signing key")
			}
		})
	}
}

func TestLoadJWTSecretFailsWhenRandomnessUnavailable(t *testing.T) {
	resetConfigEnvironment(t)
	secret, err := loadJWTSecret("development", bytes.NewReader(nil))
	if err == nil || secret != "" {
		t.Fatal("random generation failure must not produce a fallback secret")
	}
}

func TestLoadConfigPreservesConfiguredSecret(t *testing.T) {
	for _, environment := range []string{"production", "staging", "development", "test"} {
		t.Run(environment, func(t *testing.T) {
			resetConfigEnvironment(t)
			t.Setenv("ENV", " "+strings.ToUpper(environment)+" ")
			t.Setenv("JWT_SECRET", validTestJWTSecret)
			t.Setenv("GOOGLE_CLIENT_ID", "  application.apps.googleusercontent.com  ")
			cfg, err := LoadConfig()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.JWTSecret != validTestJWTSecret {
				t.Fatal("configured signing key was unexpectedly changed")
			}
			if cfg.Environment != environment {
				t.Fatalf("expected normalized environment, got %q", cfg.Environment)
			}
			if cfg.GoogleClientID != "application.apps.googleusercontent.com" {
				t.Fatal("expected trimmed Google OAuth client ID")
			}
		})
	}
}

func TestLoadConfigRejectsInvalidJWTLifetime(t *testing.T) {
	for _, environment := range []string{"production", "development"} {
		for _, lifetime := range []string{"0", "-1", "721", "1000000", "9999999999999999999999999999999", "abc", "1.5", " 72 ", " "} {
			t.Run(environment+"/"+lifetime, func(t *testing.T) {
				resetConfigEnvironment(t)
				t.Setenv("ENV", environment)
				t.Setenv("JWT_SECRET", validTestJWTSecret)
				t.Setenv("JWT_EXPIRATION_HOURS", lifetime)
				cfg, err := LoadConfig()
				if err == nil || cfg != nil {
					t.Fatal("invalid lifetime must fail without a usable config")
				}
				if !strings.Contains(err.Error(), "JWT_EXPIRATION_HOURS") {
					t.Fatalf("error should identify the invalid setting, got %v", err)
				}
			})
		}
	}
}

func TestLoadConfigAcceptsJWTLifetimeBounds(t *testing.T) {
	for _, lifetime := range []int{1, 72, 720} {
		t.Run(strconv.Itoa(lifetime), func(t *testing.T) {
			resetConfigEnvironment(t)
			t.Setenv("ENV", "production")
			t.Setenv("JWT_SECRET", validTestJWTSecret)
			t.Setenv("JWT_EXPIRATION_HOURS", strconv.Itoa(lifetime))
			cfg, err := LoadConfig()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.JWTExpirationHours != lifetime {
				t.Fatalf("wanted %d hours, got %d", lifetime, cfg.JWTExpirationHours)
			}
		})
	}
}

func TestLoadConfigDefaultsAndLists(t *testing.T) {
	resetConfigEnvironment(t)
	t.Setenv("SUPER_ADMIN_EMAILS", " First@Example.com, , second@example.com ")
	t.Setenv("TRUSTED_PROXIES", " 127.0.0.1, ,10.0.0.0/8 ")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != "8080" || cfg.Environment != "development" || cfg.DBDriver != "sqlite" || cfg.DBPath != "ex_chat.db" {
		t.Fatal("default server or database configuration unexpectedly changed")
	}
	if cfg.JWTExpirationHours != 72 || cfg.GoogleClientID != "" {
		t.Fatal("expected default JWT lifetime and disabled Google sign-in")
	}
	if len(cfg.SuperAdminEmails) != 2 || !cfg.IsSuperAdminEmail("FIRST@example.com") {
		t.Fatal("super-admin email normalization unexpectedly changed")
	}
	if len(cfg.TrustedProxies) != 2 || cfg.TrustedProxies[0] != "127.0.0.1" || cfg.TrustedProxies[1] != "10.0.0.0/8" {
		t.Fatal("trusted proxy parsing unexpectedly changed")
	}
}

func TestLoadConfigRejectsWhitespaceEnvironment(t *testing.T) {
	resetConfigEnvironment(t)
	t.Setenv("ENV", " \t ")
	if cfg, err := LoadConfig(); err == nil || cfg != nil {
		t.Fatal("a whitespace environment must not silently become development mode")
	}
}

func TestLoadConfigAutomationExecutionMode(t *testing.T) {
	for _, mode := range []string{"legacy", "durable", "mixed"} {
		t.Run(mode, func(t *testing.T) {
			resetConfigEnvironment(t)
			t.Setenv("ENV", "test")
			t.Setenv("AUTOMATION_EXECUTION_MODE", mode)
			cfg, err := LoadConfig()
			if mode == "mixed" {
				if err == nil {
					t.Fatal("mixed execution mode accepted")
				}
				return
			}
			if err != nil || cfg.AutomationExecutionMode != mode {
				t.Fatalf("cfg=%+v err=%v", cfg, err)
			}
		})
	}
}
