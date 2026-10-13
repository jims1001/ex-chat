package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServerRejectsInvalidConfigBeforeStartup(t *testing.T) {
	if os.Getenv("EX_CHAT_TEST_CONFIG_HELPER") == "1" {
		main()
		return
	}

	for _, tc := range []struct {
		name     string
		secret   string
		lifetime string
		setting  string
	}{
		{name: "missing production key", lifetime: "72", setting: "JWT_SECRET"},
		{name: "published production key", secret: "ex-chat-production-ready-jwt-secret-key-2026", lifetime: "72", setting: "JWT_SECRET"},
		{name: "invalid lifetime", secret: "9f06b37ec7d125a0ea1439805d468da139cf0a27ebb6100f8a872351adb9c264", lifetime: "0", setting: "JWT_EXPIRATION_HOURS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			dbPath := filepath.Join(t.TempDir(), "must-not-be-created.db")
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestServerRejectsInvalidConfigBeforeStartup$")
			cmd.Env = append(os.Environ(),
				"EX_CHAT_TEST_CONFIG_HELPER=1", "ENV=production", "PORT=0",
				"DB_DRIVER=sqlite", "DB_PATH="+dbPath,
				"JWT_SECRET="+tc.secret, "JWT_EXPIRATION_HOURS="+tc.lifetime,
			)
			output, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatal("server did not promptly reject invalid configuration")
			}
			if err == nil {
				t.Fatal("server must exit unsuccessfully on invalid configuration")
			}
			if !strings.Contains(string(output), "configuration error:") || !strings.Contains(string(output), tc.setting) {
				t.Fatalf("server did not identify the configuration error: %s", output)
			}
			if strings.Contains(string(output), "initializing ex-chat application") {
				t.Fatal("application startup began before configuration was rejected")
			}
			if tc.secret != "" && strings.Contains(string(output), tc.secret) {
				t.Fatal("startup logging must not expose the signing key")
			}
			if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
				t.Fatal("invalid configuration must fail before creating the database")
			}
		})
	}
}
