package main

import (
	"context"
	"log/slog"
	"net/http"
	"oneTrick/api"
	"oneTrick/clients/bungie"
	"oneTrick/clients/gcp"
	"oneTrick/envvars"
	"oneTrick/logger"
	"oneTrick/services/aggregate"
	"oneTrick/services/destiny"
	"oneTrick/services/session"
	"oneTrick/services/snapshot"
	"oneTrick/services/stats"
	"oneTrick/services/user"
	"oneTrick/validator"
	"os"

	clog "github.com/Shannor/canonical-logger"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/go-resty/resty/v2"
	"github.com/joho/godotenv"
	"github.com/oapi-codegen/gin-middleware"
)

func main() {
	_ = godotenv.Load()
	env := envvars.GetEvn()
	if envvars.IsDev(env) {
		slog.SetDefault(slog.New(clog.NewTreeHandler(os.Stdout, &clog.ConsoleHandlerOptions{
			Level: clog.LevelDebug,
		})))
	} else {
		slog.SetDefault(slog.New(logger.NewGCPHandler(clog.LevelInfo)))
	}

	slog.Info("Starting Up", "env", string(env.Environment))

	hc := http.Client{}
	cli, err := bungie.NewClientWithResponses(
		"https://www.bungie.net/Platform",
		bungie.WithHTTPClient(&hc),
		bungie.WithRequestEditorFn(func(ctx context.Context, req *http.Request) error {
			req.Header.Add("X-API-KEY", env.ApiKey)
			req.Header.Add("Accept", "application/json")
			req.Header.Add("Content-Type", "application/json")
			req.Header.Add("User-Agent", "oneTrick-backend")
			return nil
		}),
	)
	firestore := gcp.CreateFirestore(context.Background())

	manifestService := destiny.NewCachedManifestService(
		destiny.NewManifestService(firestore, string(env.Environment)),
	)

	sessionStore := session.NewFirestoreStore(firestore)
	rClient := resty.New()
	d2AuthAService := destiny.NewAuthService(rClient, cli, env.D2ClientID, env.D2ClientSecret)
	destinyService := destiny.NewService(env.ApiKey, firestore, manifestService)
	userService := user.NewUserService(firestore, destinyService, activeSessionReaderAdapter{store: sessionStore})
	aggregateService := aggregate.NewService(firestore)
	snapshotService := snapshot.NewService(firestore, userService, destinyService, aggregateService)
	sessionService := session.NewService(sessionStore, aggregateService, userService, destinyService, snapshotService, manifestService)
	statsService := stats.NewService(firestore, snapshotService, userService, manifestService)
	server := NewServer(
		destinyService,
		d2AuthAService,
		userService,
		snapshotService,
		aggregateService,
		sessionService,
		manifestService,
		statsService,
		env.InternalAPIKey,
	)

	defer firestore.Close()

	if envvars.IsProd(env) {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.ContextWithFallback = true
	r.Use(logger.CanonicalLogger())
	r.Use(logger.SlogRecovery())
	r.Use(cors.Default())

	r.GET("/openapi", func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		c.File("./openapi.yaml")
	})

	// Load OpenAPI spec file
	swagger, err := api.GetSwagger()
	if err != nil {
		slog.Error("failed to load swagger spec file", "error", err)
		return
	}
	// Clear out the servers array in the swagger spec, that skips validating
	// that server names match. We don't know how this thing will be run.
	swagger.Servers = nil
	r.Use(ginmiddleware.OapiRequestValidatorWithOptions(swagger, &ginmiddleware.Options{
		ErrorHandler: logger.GinValidationErrorHandler,
		Options: openapi3filter.Options{
			AuthenticationFunc: validator.Authenticate,
		},
	}))

	h := api.NewStrictHandler(server, []api.StrictMiddlewareFunc{
		logger.GinStrictMiddleware[api.StrictHandlerFunc],
	})
	api.RegisterHandlersWithOptions(r, h, api.GinServerOptions{
		ErrorHandler: logger.GinParamErrorHandler,
	})
	s := &http.Server{
		Handler: r,
		Addr:    "0.0.0.0:8080",
	}

	slog.Info("Starting HTTP server on port 8080")
	err = s.ListenAndServe()
	if err != nil {
		slog.Error("Server crashed", "error", err)
		os.Exit(1)
	}
}

type activeSessionReaderAdapter struct {
	store *session.FirestoreStore
}

func (a activeSessionReaderAdapter) GetActiveByUserIDs(ctx context.Context, userIDs []string) ([]user.ActiveSession, error) {
	sessions, err := a.store.GetActiveByUserIDs(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	out := make([]user.ActiveSession, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, user.ActiveSession{
			SessionID:   s.SessionID,
			UserID:      s.UserID,
			CharacterID: s.CharacterID,
			StartedAt:   s.StartedAt,
			TrackComp:   s.TrackComp,
		})
	}
	return out, nil
}
