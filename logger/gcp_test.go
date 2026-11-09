package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	clog "github.com/Shannor/canonical-logger"
	"github.com/gin-gonic/gin"
)

func TestGCPHandler(t *testing.T) {
	var buf bytes.Buffer
	handler := NewGCPHandlerWithWriter(&buf, slog.LevelDebug)
	slog.SetDefault(slog.New(handler))

	_ = clog.Run(context.Background(), "session-warning-test", func(ctx context.Context) error {
		clog.WithGroupFields(ctx, "request", clog.Fields{
			"sessionId": "test-session-123",
			"userId":    "user-456",
			"warning":   "session warning test",
		})
		return nil
	})

	var parsed map[string]any
	err := json.Unmarshal(buf.Bytes(), &parsed)
	if err != nil {
		t.Fatalf("Failed to unmarshal log output: %v, raw: %s", err, buf.String())
	}

	if parsed["severity"] != "WARNING" {
		t.Errorf("Expected severity 'WARNING', got %v", parsed["severity"])
	}

	if parsed["time"] == nil {
		t.Errorf("Expected time field to be present, got nil")
	}

	if parsed["message"] != "session-warning-test-success" {
		t.Errorf("Expected message 'session-warning-test-success', got %v", parsed["message"])
	}

	reqGroup, ok := parsed["request"].(map[string]any)
	if !ok {
		t.Fatalf("Expected request group in output, got %v", parsed["request"])
	}
	if reqGroup["sessionId"] != "test-session-123" {
		t.Errorf("Expected sessionId 'test-session-123', got %v", reqGroup["sessionId"])
	}
	if reqGroup["userId"] != "user-456" {
		t.Errorf("Expected userId 'user-456', got %v", reqGroup["userId"])
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
	r.Use(CanonicalLogger())
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

	if parsed["message"] != "http-request-panic-failure" {
		t.Errorf("Expected message 'http-request-panic-failure', got %v", parsed["message"])
	}

	errGroup, ok := parsed["error"].(map[string]any)
	if !ok {
		t.Fatalf("Expected error group in log output, got %v", parsed["error"])
	}
	if errGroup["err"] != "test panic message" {
		t.Errorf("Expected error.err 'test panic message', got %v", errGroup["err"])
	}
	if errGroup["stackTrace"] == nil {
		t.Errorf("Expected error.stackTrace to be present")
	}

	if parsed["path"] != "/panic" {
		t.Errorf("Expected path '/panic', got %v", parsed["path"])
	}
}

func TestSlogLogger(t *testing.T) {
	var buf bytes.Buffer
	handler := NewGCPHandlerWithWriter(&buf, slog.LevelInfo)
	slog.SetDefault(slog.New(handler))

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(CanonicalLogger())

	r.GET("/ping", func(c *gin.Context) {
		_ = clog.Run(c.Request.Context(), "get-ping", func(ctx context.Context) error {
			clog.WithGroupField(ctx, "request", "ping", true)
			c.String(http.StatusOK, "pong")
			return nil
		})
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/ping", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code 200, got %d", w.Code)
	}

	var parsed map[string]any
	err := json.Unmarshal(buf.Bytes(), &parsed)
	if err != nil {
		t.Fatalf("Failed to unmarshal log output: %v, raw: %s", err, buf.String())
	}

	if parsed["severity"] != "INFO" {
		t.Errorf("Expected severity 'INFO', got %v", parsed["severity"])
	}

	if parsed["message"] != "get-ping-success" {
		t.Errorf("Expected message 'get-ping-success', got %v", parsed["message"])
	}

	if parsed["path"] != "/ping" {
		t.Errorf("Expected path '/ping', got %v", parsed["path"])
	}

	if parsed["method"] != "GET" {
		t.Errorf("Expected method 'GET', got %v", parsed["method"])
	}
}

type sample500JSONResponse struct {
	Message string
}

type sample404JSONResponse struct {
	Message string
}

func TestGinStrictMiddleware(t *testing.T) {
	t.Run("typed 500 response records failure with message", func(t *testing.T) {
		var buf bytes.Buffer
		handler := NewGCPHandlerWithWriter(&buf, slog.LevelInfo)
		slog.SetDefault(slog.New(handler))

		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.ContextWithFallback = true
		r.Use(CanonicalLogger())

		type strictFunc func(*gin.Context, any) (any, error)
		mw := GinStrictMiddleware[strictFunc](func(c *gin.Context, req any) (any, error) {
			clog.WithGroupField(c, "request", "activityId", "act-123")
			return sample500JSONResponse{Message: "no activity data"}, nil
		}, "GetActivity")

		r.GET("/activities/:id", func(c *gin.Context) {
			_, _ = mw(c, nil)
			c.Status(http.StatusInternalServerError)
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("GET", "/activities/act-123", nil)
		r.ServeHTTP(w, req)

		var parsed map[string]any
		if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
			t.Fatalf("Failed to unmarshal log output: %v", err)
		}
		if parsed["severity"] != "ERROR" {
			t.Errorf("Expected severity 'ERROR', got %v", parsed["severity"])
		}
		if parsed["message"] != "get-activity-failure" {
			t.Errorf("Expected message 'get-activity-failure', got %v", parsed["message"])
		}
		reqGroup, ok := parsed["request"].(map[string]any)
		if !ok || reqGroup["activityId"] != "act-123" {
			t.Errorf("Expected request.activityId 'act-123', got %v", parsed["request"])
		}
	})

	t.Run("typed 404 response records handled success at WARNING level", func(t *testing.T) {
		var buf bytes.Buffer
		handler := NewGCPHandlerWithWriter(&buf, slog.LevelInfo)
		slog.SetDefault(slog.New(handler))

		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.ContextWithFallback = true
		r.Use(CanonicalLogger())

		type strictFunc func(*gin.Context, any) (any, error)
		mw := GinStrictMiddleware[strictFunc](func(c *gin.Context, req any) (any, error) {
			clog.WithGroupField(c, "request", "sessionId", "ses-404")
			return sample404JSONResponse{Message: "not found"}, nil
		}, "DeleteSession")

		r.DELETE("/sessions/:id", func(c *gin.Context) {
			_, _ = mw(c, nil)
			c.Status(http.StatusNotFound)
		})

		w := httptest.NewRecorder()
		req, _ := http.NewRequest("DELETE", "/sessions/ses-404", nil)
		r.ServeHTTP(w, req)

		var parsed map[string]any
		if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
			t.Fatalf("Failed to unmarshal log output: %v", err)
		}
		if parsed["severity"] != "WARNING" {
			t.Errorf("Expected severity 'WARNING', got %v", parsed["severity"])
		}
		if parsed["message"] != "delete-session-success" {
			t.Errorf("Expected message 'delete-session-success', got %v", parsed["message"])
		}
	})
}
