package main

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/oapi-codegen/gin-middleware"
	"log"
	"log/slog"
	"net/http"
	"oneTrick/api"
	"oneTrick/services"
)

const primaryMembershipId = 4611686018434106050

func main() {
	destinyService := services.NewDestinyService()
	server := api.NewServer(destinyService)

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

	r.GET("/openapi", func(c *gin.Context) {
		c.Header("Content-Type", "application/x-yaml")
		c.File("./api/openapi.yaml")
	})

	// TODO: Convert to a POST method in openapi spec
	r.GET("/profile", func(c *gin.Context) {
		items, timestamp, err := destinyService.GetUserSnapshot(primaryMembershipId)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch profile data"})
			return
		}

		err = destinyService.WriteToFile(items, timestamp)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save profile data"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Profile data saved successfully!"})
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
