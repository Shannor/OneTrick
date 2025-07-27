package main

import (
	"context"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/go-resty/resty/v2"
	"github.com/mark-ignacio/zerolog-gcp"
	"github.com/oapi-codegen/gin-middleware"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"log/slog"
	"net/http"
	"oneTrick/api"
	"oneTrick/clients/bungie"
	"oneTrick/clients/gcp"
	"oneTrick/envvars"
	"oneTrick/services/aggregate"
	"oneTrick/services/destiny"
	"oneTrick/services/session"
	"oneTrick/services/snapshot"
	"oneTrick/services/user"
	"oneTrick/validator"
	"os"
)

func main() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	projectID := "gruntt-destiny"
	logID := "application-log"
	ctx := context.Background()

	writer, err := zlg.NewCloudLoggingWriter(ctx, projectID, logID, zlg.CloudLoggingOptions{})
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create GCP writer")
	}

	log.Logger = log.Output(writer)

	env := envvars.GetEvn()
	if envvars.IsDev(env) {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})))
		consoleWriter := zerolog.ConsoleWriter{Out: os.Stderr}
		multi := zerolog.MultiLevelWriter(consoleWriter, writer)
		log.Logger = log.Output(multi)
	}

	log.Info().Str("Env", string(env.Environment)).Msg("Starting Up")
	defer zlg.Flush()

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

	manifestService := destiny.NewManifestService(firestore, string(env.Environment))

	rClient := resty.New()
	d2AuthAService := destiny.NewAuthService(rClient, cli, env.D2ClientID, env.D2ClientSecret)
	destinyService := destiny.NewService(env.ApiKey, firestore, manifestService)
	userService := user.NewUserService(firestore)
	aggregateService := aggregate.NewService(firestore)
	sessionService := session.NewService(firestore)
	snapshotService := snapshot.NewService(firestore, userService, destinyService)
	server := NewServer(
		destinyService,
		d2AuthAService,
		userService,
		snapshotService,
		aggregateService,
		sessionService,
		manifestService,
	)

	defer firestore.Close()
	// Load OpenAPI spec file
	swagger, err := api.GetSwagger()
	if err != nil {
		log.Error().Err(err).Msg("failed to load swagger spec file")
		return
	}
	// Clear out the servers array in the swagger spec, that skips validating
	// that server names match. We don't know how this thing will be run.
	swagger.Servers = nil

	r := gin.Default()
	r.Use(cors.Default())

	if envvars.IsProd(env) {
		gin.SetMode(gin.ReleaseMode)
	}

	r.GET("/openapi", func(c *gin.Context) {
		c.Header("Content-Type", "application/json")
		c.File("./api/openapi.json")
	})

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

	log.Info().Msg("Starting HTTP server on port 8080")
	err = s.ListenAndServe()
	if err != nil {
		log.Fatal().Err(err).Msg("Server crashed")
	}
}
