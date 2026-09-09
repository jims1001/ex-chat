package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
)

type contextKey string

const (
	KeyRequestID     contextKey = "request_id"
	KeyCorrelationID contextKey = "correlation_id"
	KeyAccountID     contextKey = "account_id"
	KeyUserID        contextKey = "user_id"
	KeyActorType     contextKey = "actor_type"
	KeyActorID       contextKey = "actor_id"
)

// ParseLevel parses a log level string into slog.Level
func ParseLevel(lvl string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(lvl)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

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

// WithComponent returns a logger scoped to a specific architectural component
func WithComponent(component string) *slog.Logger {
	return Get().With("component", component)
}

// FromContext is an alias for WithContext
func FromContext(ctx context.Context) *slog.Logger {
	return WithContext(ctx)
}

// WithContext returns a logger decorated with fields extracted from context
func WithContext(ctx context.Context) *slog.Logger {
	l := Get()
	if ctx == nil {
		return l
	}

	attrs := make([]any, 0, 10)
	if reqID, ok := ctx.Value(KeyRequestID).(string); ok && reqID != "" {
		attrs = append(attrs, "request_id", reqID)
	}
	if corrID, ok := ctx.Value(KeyCorrelationID).(string); ok && corrID != "" {
		attrs = append(attrs, "correlation_id", corrID)
	}
	if accID := ctx.Value(KeyAccountID); accID != nil {
		attrs = append(attrs, "account_id", accID)
	}
	if userID := ctx.Value(KeyUserID); userID != nil {
		attrs = append(attrs, "user_id", userID)
	}
	if actorType, ok := ctx.Value(KeyActorType).(string); ok && actorType != "" {
		attrs = append(attrs, "actor_type", actorType)
	}
	if actorID := ctx.Value(KeyActorID); actorID != nil {
		attrs = append(attrs, "actor_id", actorID)
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

func ContextWithCorrelationID(ctx context.Context, correlationID string) context.Context {
	return context.WithValue(ctx, KeyCorrelationID, correlationID)
}

func ContextWithAccountID(ctx context.Context, accountID any) context.Context {
	return context.WithValue(ctx, KeyAccountID, accountID)
}

func ContextWithUserID(ctx context.Context, userID any) context.Context {
	return context.WithValue(ctx, KeyUserID, userID)
}

func ContextWithActor(ctx context.Context, actorType string, actorID any) context.Context {
	ctx = context.WithValue(ctx, KeyActorType, actorType)
	if actorID != nil {
		ctx = context.WithValue(ctx, KeyActorID, actorID)
	}
	return ctx
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
