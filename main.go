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

	"github.com/algolia/algoliasearch-client-go/v4/algolia/search"
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
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})))
	} else {
		slog.SetDefault(slog.New(logger.NewGCPHandler(slog.LevelInfo)))
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
	searchClient, err := search.NewClient("WCLL3JHGK2", env.AlgoliaAPIKey)
	if err != nil {
		return
	}

	manifestService := destiny.NewManifestService(firestore, string(env.Environment))

	rClient := resty.New()
	d2AuthAService := destiny.NewAuthService(rClient, cli, env.D2ClientID, env.D2ClientSecret)
	destinyService := destiny.NewService(env.ApiKey, firestore, manifestService)
	userService := user.NewUserService(firestore, destinyService, searchClient)
	aggregateService := aggregate.NewService(firestore)
	sessionService := session.NewService(firestore, aggregateService)
	snapshotService := snapshot.NewService(firestore, userService, destinyService, aggregateService)
	statsService := stats.NewService(firestore, snapshotService, userService)
	server := NewServer(
		destinyService,
		d2AuthAService,
		userService,
		snapshotService,
		aggregateService,
		sessionService,
		manifestService,
		statsService,
	)

	defer firestore.Close()

	r := gin.New()
	r.Use(logger.SlogLogger())
	r.Use(logger.SlogRecovery())
	r.Use(cors.Default())

	if envvars.IsProd(env) {
		gin.SetMode(gin.ReleaseMode)
	}

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
		Options: openapi3filter.Options{
			AuthenticationFunc: validator.Authenticate,
		},
	}))

	h := api.NewStrictHandler(server, nil)
	api.RegisterHandlers(r, h)
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
