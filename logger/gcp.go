package logger

import (
	"io"
	"log/slog"
	"os"
)

// NewGCPHandler creates an slog.Handler configured for GCP Cloud Logging stdout ingestion.
// It maps "msg" -> "message" and "level" -> "severity" with GCP severity levels (DEBUG, INFO, WARNING, ERROR).
func NewGCPHandler(level slog.Level) slog.Handler {
	return NewGCPHandlerWithWriter(os.Stdout, level)
}

// NewGCPHandlerWithWriter creates an slog.Handler targeting a custom io.Writer (useful for testing or custom stdout).
func NewGCPHandlerWithWriter(w io.Writer, level slog.Level) slog.Handler {
	return slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Only transform top-level keys
			if len(groups) > 0 {
				return a
			}

			switch a.Key {
			case slog.TimeKey:
				a.Key = "timestamp"
			case slog.MessageKey:
				a.Key = "message"
			case slog.LevelKey:
				a.Key = "severity"
				if l, ok := a.Value.Any().(slog.Level); ok {
					switch l {
					case slog.LevelDebug:
						a.Value = slog.StringValue("DEBUG")
					case slog.LevelInfo:
						a.Value = slog.StringValue("INFO")
					case slog.LevelWarn:
						a.Value = slog.StringValue("WARNING")
					case slog.LevelError:
						a.Value = slog.StringValue("ERROR")
					default:
						a.Value = slog.StringValue(l.String())
					}
				}
			}

			return a
		},
	}).WithAttrs([]slog.Attr{
		slog.String("logger", "go"),
	})
}
