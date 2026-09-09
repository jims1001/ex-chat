package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
)

type contextKey string

const (
	KeyRequestID contextKey = "request_id"
	KeyAccountID contextKey = "account_id"
	KeyUserID    contextKey = "user_id"
)

var (
	defaultLogger *slog.Logger
	mu            sync.RWMutex
)

func init() {
	SetOutput(os.Stdout, slog.LevelInfo, "json")
}

// SetOutput configures the global logger output, level and format ("json" or "text")
func SetOutput(w io.Writer, level slog.Level, format string) {
	mu.Lock()
	defer mu.Unlock()

	opts := &slog.HandlerOptions{
		Level: level,
	}

	var handler slog.Handler
	if format == "text" {
		handler = slog.NewTextHandler(w, opts)
	} else {
		handler = slog.NewJSONHandler(w, opts)
	}

	defaultLogger = slog.New(handler)
	slog.SetDefault(defaultLogger)
}

// Get returns the current default logger
func Get() *slog.Logger {
	mu.RLock()
	defer mu.RUnlock()
	return defaultLogger
}

// WithContext returns a logger decorated with fields extracted from context
func WithContext(ctx context.Context) *slog.Logger {
	l := Get()
	if ctx == nil {
		return l
	}

	attrs := make([]any, 0, 6)
	if reqID, ok := ctx.Value(KeyRequestID).(string); ok && reqID != "" {
		attrs = append(attrs, "request_id", reqID)
	}
	if accID := ctx.Value(KeyAccountID); accID != nil {
		attrs = append(attrs, "account_id", accID)
	}
	if userID := ctx.Value(KeyUserID); userID != nil {
		attrs = append(attrs, "user_id", userID)
	}

	if len(attrs) > 0 {
		return l.With(attrs...)
	}
	return l
}

// Context helpers
func ContextWithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, KeyRequestID, requestID)
}

func ContextWithAccountID(ctx context.Context, accountID any) context.Context {
	return context.WithValue(ctx, KeyAccountID, accountID)
}

func ContextWithUserID(ctx context.Context, userID any) context.Context {
	return context.WithValue(ctx, KeyUserID, userID)
}

// Convenience package-level logging methods
func Debug(msg string, args ...any) {
	Get().Debug(msg, args...)
}

func Info(msg string, args ...any) {
	Get().Info(msg, args...)
}

func Warn(msg string, args ...any) {
	Get().Warn(msg, args...)
}

func Error(msg string, args ...any) {
	Get().Error(msg, args...)
}

func DebugContext(ctx context.Context, msg string, args ...any) {
	WithContext(ctx).DebugContext(ctx, msg, args...)
}

func InfoContext(ctx context.Context, msg string, args ...any) {
	WithContext(ctx).InfoContext(ctx, msg, args...)
}

func WarnContext(ctx context.Context, msg string, args ...any) {
	WithContext(ctx).WarnContext(ctx, msg, args...)
}

func ErrorContext(ctx context.Context, msg string, args ...any) {
	WithContext(ctx).ErrorContext(ctx, msg, args...)
}
