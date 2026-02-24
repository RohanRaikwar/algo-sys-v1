// Package logger provides structured logging using Go 1.21's log/slog.
// It sets up a JSON handler with service-level context and provides
// trace ID propagation through context.Context.
package logger

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"
)

type ctxKey string

const traceIDKey ctxKey = "trace_id"

// Init creates and returns a structured logger for the given service.
// The logger outputs JSON to stdout with the service name embedded.
func Init(service string, level slog.Level) *slog.Logger {
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	})

	logger := slog.New(handler).With(
		slog.String("service", service),
	)

	// Set as default so log/slog.Info() etc. also use structured output
	slog.SetDefault(logger)

	return logger
}

// WithTraceID stores a trace ID in the context for downstream propagation.
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceIDKey, traceID)
}

// TraceID extracts the trace ID from context. Returns "" if not set.
func TraceID(ctx context.Context) string {
	if v, ok := ctx.Value(traceIDKey).(string); ok {
		return v
	}
	return ""
}

// GenerateTraceID creates a trace ID from a token and timestamp.
// Format: "{token}-{unixNano}" — lightweight, no UUID dependency.
func GenerateTraceID(token string, ts time.Time) string {
	return fmt.Sprintf("%s-%d", token, ts.UnixNano())
}

// LogWithTrace returns slog attributes including the trace ID from context.
// Usage: slog.Info("msg", logger.LogWithTrace(ctx)...)
func LogWithTrace(ctx context.Context) []any {
	tid := TraceID(ctx)
	if tid == "" {
		return nil
	}
	return []any{slog.String("trace_id", tid)}
}
