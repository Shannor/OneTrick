package logger

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"reflect"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
	"unicode"

	clog "github.com/Shannor/canonical-logger"
	"github.com/gin-gonic/gin"
	ginmiddleware "github.com/oapi-codegen/gin-middleware"
)

const ginFieldBagKey = "__clog_fieldbag__"

func init() {
	// Register fallback resolvers so clog.BagFromContext(ctx) always finds the FieldBag
	// whether ctx is a *gin.Context, a standard request context, or an oapi-codegen validator context.
	clog.RegisterContextResolver(func(ctx context.Context) *clog.FieldBag {
		if ginCtx, ok := ctx.Value(gin.ContextKey).(*gin.Context); ok && ginCtx != nil {
			if val, exists := ginCtx.Get(ginFieldBagKey); exists {
				if bag, ok := val.(*clog.FieldBag); ok {
					return bag
				}
			}
		}
		if ginCtx := ginmiddleware.GetGinContext(ctx); ginCtx != nil {
			if val, exists := ginCtx.Get(ginFieldBagKey); exists {
				if bag, ok := val.(*clog.FieldBag); ok {
					return bag
				}
			}
		}
		return nil
	})
}

// NewGCPHandler creates an slog.Handler configured for GCP Cloud Logging stdout ingestion
// using clog.NewJSONHandler and clog.GCPReplaceAttr.
func NewGCPHandler(level slog.Level) slog.Handler {
	return NewGCPHandlerWithWriter(os.Stdout, level)
}

// NewGCPHandlerWithWriter creates an slog.Handler targeting a custom io.Writer (useful for testing or custom stdout).
func NewGCPHandlerWithWriter(w io.Writer, level slog.Level) slog.Handler {
	return clog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: clog.GCPReplaceAttr,
	}).WithAttrs([]slog.Attr{
		slog.Any("logging.googleapis.com/labels", map[string]string{
			"logger": "onetrick-service",
		}),
	})
}

// CanonicalLogger returns a Gin middleware that opens a clog FieldBag per request and
// emits one canonical wide event when the request completes.
func CanonicalLogger(sensitiveParams ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		ctx := clog.NewContext(c.Request.Context())
		bag := clog.BagFromContext(ctx)

		c.Set(ginFieldBagKey, bag)
		c.Request = c.Request.WithContext(ctx)

		c.Next()

		stop := time.Since(start)
		path := c.Request.URL.Path
		if rawQuery := c.Request.URL.RawQuery; rawQuery != "" {
			path = path + "?" + rawQuery
		}
		for _, param := range sensitiveParams {
			if c.Param(param) != "" {
				if fullPath := c.FullPath(); fullPath != "" {
					path = fullPath
					break
				}
			}
		}

		if len(c.Errors) > 0 && !bag.HasGroup("error") {
			recordFailure(c.Request.Context(), "decode-request-body", c.Errors.Last().Err)
		}

		status := c.Writer.Status()
		level := slog.LevelInfo
		if status >= 500 || len(c.Errors) > 0 || bag.HasGroup("error") {
			level = slog.LevelError
		} else if status >= 400 || bag.HasWarning() {
			level = slog.LevelWarn
		}

		attrs := []slog.Attr{
			slog.Int("status", status),
			slog.String("method", c.Request.Method),
			slog.String("path", path),
			slog.String("ip", c.ClientIP()),
			slog.String("latency", stop.String()),
			slog.String("user-agent", c.Request.UserAgent()),
		}
		attrs = append(attrs, bag.Attrs()...)

		clog.LoggerFromContext(c.Request.Context()).LogAttrs(
			c.Request.Context(),
			level,
			bag.Message(),
			attrs...,
		)
	}
}

// SlogLogger is an alias for CanonicalLogger for backwards compatibility.
func SlogLogger() gin.HandlerFunc {
	return CanonicalLogger()
}

// SlogRecovery returns a Gin middleware that recovers panics, attaches panic details
// to the active canonical FieldBag (or emits a canonical failure if none is open),
// and aborts with HTTP 500.
func SlogRecovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				err := fmt.Errorf("%v", r)
				stack := string(debug.Stack())
				ctx := c.Request.Context()

				if bag := clog.BagFromContext(ctx); bag != nil {
					recordFailure(ctx, "http-request-panic", err)
					clog.WithGroupField(ctx, "error", "stackTrace", stack)
				} else {
					_ = clog.Run(ctx, "http-request-panic", func(runCtx context.Context) error {
						clog.WithFields(runCtx, clog.Fields{
							"path":   c.Request.URL.Path,
							"method": c.Request.Method,
							"ip":     c.ClientIP(),
						})
						clog.WithGroupField(runCtx, "error", "stackTrace", stack)
						return err
					})
				}

				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}

// IsFailureStatus decides which typed strict responses (e.g. GetActivity500JSONResponse)
// are recorded as "-failure". Typed 4xx responses are handled outcomes by default.
var IsFailureStatus = func(status int) bool { return status >= 500 }

// GinStrictMiddleware records the canonical outcome of every oapi-codegen strict operation,
// named after its operationId ("GetUser" -> "get-user-success" / "-failure").
func GinStrictMiddleware[H ~func(*gin.Context, any) (any, error)](f H, operationID string) H {
	event := EventName(operationID)
	return func(c *gin.Context, req any) (any, error) {
		return runStrict(c.Request.Context(), event, func(context.Context) (any, error) {
			return f(c, req)
		})
	}
}

type typedFailure struct {
	status int
	resp   any
}

func (e typedFailure) Error() string {
	if v := reflect.Indirect(reflect.ValueOf(e.resp)); v.IsValid() && v.Kind() == reflect.Struct {
		if f := v.FieldByName("Message"); f.IsValid() && f.Kind() == reflect.String && f.String() != "" {
			return fmt.Sprintf("responded with typed %d response (%T): %s", e.status, e.resp, f.String())
		}
	}
	return fmt.Sprintf("responded with typed %d response (%T)", e.status, e.resp)
}

func runStrict(ctx context.Context, event string, fn func(context.Context) (any, error)) (any, error) {
	var resp any
	err := clog.Run(ctx, event, func(ctx context.Context) error {
		var err error
		if resp, err = fn(ctx); err != nil {
			return err
		}
		if status, ok := ResponseStatus(resp); ok && IsFailureStatus(status) {
			return typedFailure{status: status, resp: resp}
		}
		return nil
	})
	if errors.As(err, new(typedFailure)) {
		return resp, nil
	}
	return resp, err
}

var statusInTypeName = regexp.MustCompile(`([1-5]\d\d)[A-Za-z]*Response$`)

// ResponseStatus extracts the HTTP status from a generated strict response object.
func ResponseStatus(resp any) (int, bool) {
	if resp == nil {
		return 0, false
	}
	v := reflect.Indirect(reflect.ValueOf(resp))
	if !v.IsValid() {
		return 0, false
	}
	if m := statusInTypeName.FindStringSubmatch(v.Type().Name()); m != nil {
		status, _ := strconv.Atoi(m[1])
		return status, true
	}
	if v.Kind() == reflect.Struct {
		if f := v.FieldByName("StatusCode"); f.IsValid() && f.CanInt() {
			return int(f.Int()), true
		}
	}
	return 0, false
}

// EventName converts an operationId ("GetUserById", "getUserById", "get-user-by-id")
// into the clog resource-action convention ("get-user-by-id").
func EventName(operationID string) string {
	var b strings.Builder
	rs := []rune(operationID)
	for i, r := range rs {
		switch {
		case r == '_' || r == ' ' || r == '-':
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
			continue
		case unicode.IsUpper(r):
			prevLower := i > 0 && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1]))
			acronymEnd := i > 0 && unicode.IsUpper(rs[i-1]) && i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if (prevLower || acronymEnd) && !strings.HasSuffix(b.String(), "-") {
				b.WriteByte('-')
			}
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func recordFailure(ctx context.Context, event string, err error) {
	_ = clog.Run(ctx, event, func(context.Context) error { return err })
}

// GinParamErrorHandler is a GinServerOptions.ErrorHandler for parameter binding errors.
func GinParamErrorHandler(c *gin.Context, err error, status int) {
	recordFailure(c.Request.Context(), "bind-request-params", err)
	c.JSON(status, gin.H{"msg": err.Error()})
}

// GinValidationErrorHandler is a gin-middleware Options.ErrorHandler for OpenAPI validation errors.
func GinValidationErrorHandler(c *gin.Context, message string, status int) {
	recordFailure(c.Request.Context(), "validate-request", errors.New(message))
	c.AbortWithStatusJSON(status, gin.H{"error": message})
}
