package logger

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestGCPHandler(t *testing.T) {
	var buf bytes.Buffer
	handler := NewGCPHandlerWithWriter(&buf, slog.LevelDebug)
	l := slog.New(handler)

	l.With("sessionID", "test-session-123", "userId", "user-456").Warn("Session warning test")

	var parsed map[string]any
	err := json.Unmarshal(buf.Bytes(), &parsed)
	if err != nil {
		t.Fatalf("Failed to unmarshal log output: %v, raw: %s", err, buf.String())
	}

	if parsed["severity"] != "WARNING" {
		t.Errorf("Expected severity 'WARNING', got %v", parsed["severity"])
	}

	if parsed["timestamp"] == nil {
		t.Errorf("Expected timestamp field to be present, got nil")
	}

	if parsed["message"] != "Session warning test" {
		t.Errorf("Expected message 'Session warning test', got %v", parsed["message"])
	}

	if parsed["sessionID"] != "test-session-123" {
		t.Errorf("Expected sessionID 'test-session-123', got %v", parsed["sessionID"])
	}

	if parsed["userId"] != "user-456" {
		t.Errorf("Expected userId 'user-456', got %v", parsed["userId"])
	}

	if parsed["logger"] != "go" {
		t.Errorf("Expected logger 'go', got %v", parsed["logger"])
	}
}
