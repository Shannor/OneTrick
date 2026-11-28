package main

import (
	"context"
	"log/slog"
	"net/http"
	activitydriven "oneTrick/adapters/driven/activity"
	bungiedriven "oneTrick/adapters/driven/bungie"
	cachedriven "oneTrick/adapters/driven/cache"
	firestoredriven "oneTrick/adapters/driven/firestore"
	fireteamdriven "oneTrick/adapters/driven/fireteam"
	telemetrydriven "oneTrick/adapters/driven/telemetry"
	"oneTrick/api"
	"oneTrick/clients/gcp"
	"oneTrick/core/activity"
	"oneTrick/core/fireteam"
	"oneTrick/core/telemetry"
	"oneTrick/envvars"
	"oneTrick/logger"
	"oneTrick/services/aggregate"
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
	tokenRefresher := bungiedriven.NewTokenRefresher(&hc, env.D2ClientID, env.D2ClientSecret)
	bungieClient, err := bungiedriven.NewClient(
		env.ApiKey,
		&hc,
		bungiedriven.WithOAuthRefresher(tokenRefresher),
	)
	if err != nil {
		slog.Error("failed to initialize bungie client", "error", err)
		os.Exit(1)
	}

	firestore := gcp.CreateFirestore(context.Background())
	defer firestore.Close()

	manifestStore := firestoredriven.NewManifestStore(firestore)
	manifestCache := cachedriven.NewManifestCache(manifestStore)

	oauthGateway := bungiedriven.NewOAuthGateway(&hc, bungieClient, env.D2ClientID, env.D2ClientSecret)
	partyGateway := bungiedriven.NewPartyGateway(bungieClient)
	compRankGateway := bungiedriven.NewCompRankGateway(bungieClient, manifestCache)
	carnageGateway := bungiedriven.NewCarnageGateway(bungieClient, manifestCache)
	characterGateway := bungiedriven.NewCharacterGateway(bungieClient, manifestCache)
	liveLoadoutGateway := bungiedriven.NewLiveLoadoutGateway(bungieClient, manifestCache)
	userSearchGateway := bungiedriven.NewUserSearchGateway(bungieClient)
	activityHistoryGateway := bungiedriven.NewActivityHistoryGateway(bungieClient, manifestCache)

	sessionStore := session.NewFirestoreStore(firestore)
	userService := user.NewUserService(firestore, characterGateway, userSearchGateway)
	aggregateService := aggregate.NewService(firestore)
	snapshotService := snapshot.NewService(firestore, userService, liveLoadoutGateway, aggregateService)
	sessionService := session.NewService(
		sessionStore,
		aggregateService,
		userService,
		compRankGateway,
		activityHistoryGateway,
		carnageGateway,
		snapshotService,
		manifestCache,
	)
	fireteamService := fireteam.NewService(
		fireteamdriven.NewUserReader(userService),
		partyGateway,
		firestoredriven.NewFireteamSessionStore(firestore),
		compRankGateway,
	)
	statsStore := firestoredriven.NewStatsStore(firestore)
	statsService := stats.NewService(firestore, statsStore, snapshotService, userService, manifestCache)
	telemetryService := telemetry.NewService(
		telemetrydriven.NewUserReader(userService),
		telemetrydriven.NewSnapshotReader(snapshotService),
		firestoredriven.NewTelemetryMatchStore(firestore),
	)
	activityService := activity.NewService(
		carnageGateway,
		manifestCache,
		activitydriven.NewAggregateReader(aggregateService),
		activitydriven.NewSnapshotReader(snapshotService),
		activitydriven.NewUserLookup(userService),
	)
	server := NewServer(
		characterGateway,
		activityHistoryGateway,
		oauthGateway,
		userService,
		snapshotService,
		aggregateService,
		sessionService,
		fireteamService,
		manifestCache,
		statsService,
		telemetryService,
		activityService,
		env.InternalAPIKey,
	)

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
