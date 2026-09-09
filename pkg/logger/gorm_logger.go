package logger

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	gormlogger "gorm.io/gorm/logger"
)

// GORMLogger bridges GORM database operations to pkg/logger slog
type GORMLogger struct {
	SlowThreshold time.Duration
	LogLevel      gormlogger.LogLevel
	logger        *slog.Logger
}

// NewGORMLogger initializes a new GORM structured logger
func NewGORMLogger(slowThreshold time.Duration) *GORMLogger {
	return &GORMLogger{
		SlowThreshold: slowThreshold,
		LogLevel:      gormlogger.Warn,
		logger:        WithComponent("gorm"),
	}
}

// LogMode sets the GORM logging mode
func (l *GORMLogger) LogMode(level gormlogger.LogLevel) gormlogger.Interface {
	newLogger := *l
	newLogger.LogLevel = level
	return &newLogger
}

// Info logs info messages
func (l *GORMLogger) Info(ctx context.Context, msg string, data ...interface{}) {
	if l.LogLevel >= gormlogger.Info {
		WithContext(ctx).With("component", "gorm").Info(fmt.Sprintf(msg, data...))
	}
}

// Warn logs warning messages
func (l *GORMLogger) Warn(ctx context.Context, msg string, data ...interface{}) {
	if l.LogLevel >= gormlogger.Warn {
		WithContext(ctx).With("component", "gorm").Warn(fmt.Sprintf(msg, data...))
	}
}

// Error logs error messages
func (l *GORMLogger) Error(ctx context.Context, msg string, data ...interface{}) {
	if l.LogLevel >= gormlogger.Error {
		WithContext(ctx).With("component", "gorm").Error(fmt.Sprintf(msg, data...))
	}
}

// Trace logs SQL query details, duration, errors and slow queries
func (l *GORMLogger) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	if l.LogLevel <= gormlogger.Silent {
		return
	}

	elapsed := time.Since(begin)
	sql, rows := fc()
	log := WithContext(ctx).With(
		"component", "gorm",
		"latency_ms", float64(elapsed.Microseconds())/1000.0,
		"rows_affected", rows,
	)

	switch {
	case err != nil && l.LogLevel >= gormlogger.Error && !errors.Is(err, gormlogger.ErrRecordNotFound):
		log.Error("database query error", "sql", sql, "error", err.Error())
	case l.SlowThreshold != 0 && elapsed > l.SlowThreshold && l.LogLevel >= gormlogger.Warn:
		log.Warn("database slow query", "sql", sql, "threshold_ms", l.SlowThreshold.Milliseconds())
	case l.LogLevel >= gormlogger.Info:
		log.Debug("database query executed", "sql", sql)
	}
}
