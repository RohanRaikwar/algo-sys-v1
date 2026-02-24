package logger

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestInit(t *testing.T) {
	logger := Init("test-service", slog.LevelInfo)
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestTraceID_RoundTrip(t *testing.T) {
	ctx := context.Background()

	// No trace ID set
	if tid := TraceID(ctx); tid != "" {
		t.Errorf("expected empty trace id, got %q", tid)
	}

	// Set and retrieve
	ctx = WithTraceID(ctx, "test-trace-123")
	if tid := TraceID(ctx); tid != "test-trace-123" {
		t.Errorf("expected 'test-trace-123', got %q", tid)
	}
}

func TestGenerateTraceID(t *testing.T) {
	ts := time.Date(2024, 1, 15, 10, 30, 0, 123456789, time.UTC)
	tid := GenerateTraceID("NIFTY", ts)

	if tid == "" {
		t.Fatal("expected non-empty trace id")
	}
	if !strings.HasPrefix(tid, "NIFTY-") {
		t.Errorf("expected trace id to start with 'NIFTY-', got %s", tid)
	}
	// Verify it contains the nano timestamp
	if !strings.Contains(tid, "123456789") {
		t.Errorf("expected trace id to contain nanoseconds, got %s", tid)
	}
}

func TestLogWithTrace(t *testing.T) {
	ctx := context.Background()

	// No trace ID
	attrs := LogWithTrace(ctx)
	if attrs != nil {
		t.Errorf("expected nil attrs when no trace id, got %v", attrs)
	}

	// With trace ID — returns [slog.Attr] which is a single element
	ctx = WithTraceID(ctx, "abc-123")
	attrs = LogWithTrace(ctx)
	if len(attrs) == 0 {
		t.Fatal("expected non-empty attrs with trace id set")
	}
}
