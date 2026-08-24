package logger

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
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

	if parsed["message"] != "[onetrick-service] Session warning test" {
		t.Errorf("Expected message '[onetrick-service] Session warning test', got %v", parsed["message"])
	}

	if parsed["sessionID"] != "test-session-123" {
		t.Errorf("Expected sessionID 'test-session-123', got %v", parsed["sessionID"])
	}

	if parsed["userId"] != "user-456" {
		t.Errorf("Expected userId 'user-456', got %v", parsed["userId"])
	}

	labels, ok := parsed["logging.googleapis.com/labels"].(map[string]any)
	if !ok || labels["logger"] != "onetrick-service" {
		t.Errorf("Expected logging.googleapis.com/labels with logger 'onetrick-service', got %v", parsed["logging.googleapis.com/labels"])
	}
}

func TestSlogRecovery(t *testing.T) {
	var buf bytes.Buffer
	handler := NewGCPHandlerWithWriter(&buf, slog.LevelInfo)
	slog.SetDefault(slog.New(handler))

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(SlogRecovery())

	r.GET("/panic", func(c *gin.Context) {
		panic("test panic message")
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/panic", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status code 500, got %d", w.Code)
	}

	var parsed map[string]any
	err := json.Unmarshal(buf.Bytes(), &parsed)
	if err != nil {
		t.Fatalf("Failed to unmarshal log output: %v, raw: %s", err, buf.String())
	}

	if parsed["severity"] != "ERROR" {
		t.Errorf("Expected severity 'ERROR', got %v", parsed["severity"])
	}

	if parsed["message"] != "[onetrick-service] HTTP request panic recovered" {
		t.Errorf("Expected message '[onetrick-service] HTTP request panic recovered', got %v", parsed["message"])
	}

	if parsed["error"] != "test panic message" {
		t.Errorf("Expected error 'test panic message', got %v", parsed["error"])
	}

	if parsed["path"] != "/panic" {
		t.Errorf("Expected path '/panic', got %v", parsed["path"])
	}

	if parsed["stackTrace"] == nil {
		t.Errorf("Expected stackTrace to be present")
	}
}
