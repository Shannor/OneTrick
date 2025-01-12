package main

import (
	"context"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/oapi-codegen/gin-middleware"
	"log"
	"log/slog"
	"net/http"
	"oneTrick/api"
	"oneTrick/clients/gcp"
	"oneTrick/envvars"
	"oneTrick/services/destiny"
)

const primaryMembershipId = 4611686018434106050

func main() {
	env := envvars.GetEvn()

	firestore := gcp.CreateFirestore(context.Background())
	destinyService := destiny.NewService(env.ApiKey, firestore)
	server := NewServer(destinyService)

	defer firestore.Close()
	// Load OpenAPI spec file
	swagger, err := api.GetSwagger()
	if err != nil {
		slog.Error("failed to load swagger spec file")
		return
	}
	// Clear out the servers array in the swagger spec, that skips validating
	// that server names match. We don't know how this thing will be run.
	swagger.Servers = nil

	r := gin.Default()
	r.Use(cors.Default())

	if env.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r.GET("/openapi", func(c *gin.Context) {
		c.Header("Content-Type", "application/x-yaml")
		c.File("./api/openapi.yaml")
	})

	r.Use(ginmiddleware.OapiRequestValidator(swagger))
	h := api.NewStrictHandler(server, nil)
	api.RegisterHandlers(r, h)
	s := &http.Server{
		Handler: r,
		Addr:    "0.0.0.0:8080",
	}

	slog.Info("Starting HTTP server on port 8080")
	log.Fatal(s.ListenAndServe())
}
