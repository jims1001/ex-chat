package logger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
)

func TestLogger(t *testing.T) {
	buf := &bytes.Buffer{}
	logger.SetOutput(buf, slog.LevelDebug, "json")

	t.Run("Standard JSON Logging", func(t *testing.T) {
		buf.Reset()
		logger.Info("server starting", "port", 8080)

		var entry map[string]any
		if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
			t.Fatalf("failed to parse JSON log: %v", err)
		}

		if entry["msg"] != "server starting" {
			t.Errorf("expected msg 'server starting', got %v", entry["msg"])
		}
		if entry["port"] != float64(8080) {
			t.Errorf("expected port 8080, got %v", entry["port"])
		}
	})

	t.Run("Context Trace Extraction", func(t *testing.T) {
		buf.Reset()
		ctx := context.Background()
		ctx = logger.ContextWithRequestID(ctx, "req-xyz-123")
		ctx = logger.ContextWithAccountID(ctx, uint(42))

		logger.ErrorContext(ctx, "database query failed", "query_time_ms", 150)

		var entry map[string]any
		if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
			t.Fatalf("failed to parse JSON log: %v", err)
		}

		if entry["request_id"] != "req-xyz-123" {
			t.Errorf("expected request_id req-xyz-123, got %v", entry["request_id"])
		}
		if entry["account_id"] != float64(42) {
			t.Errorf("expected account_id 42, got %v", entry["account_id"])
		}
		if entry["level"] != "ERROR" {
			t.Errorf("expected level ERROR, got %v", entry["level"])
		}
	})

	t.Run("Text Format Output", func(t *testing.T) {
		buf.Reset()
		logger.SetOutput(buf, slog.LevelWarn, "text")

		logger.Warn("disk usage high", "percent", 89)

		output := buf.String()
		if !strings.Contains(output, "level=WARN") || !strings.Contains(output, "disk usage high") {
			t.Errorf("unexpected text log output: %s", output)
		}
	})
}
