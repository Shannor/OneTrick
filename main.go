package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/oapi-codegen/gin-middleware"
	"log"
	"log/slog"
	"net/http"
	"oneTrick/api"
	"oneTrick/clients/gcp"
	"oneTrick/services/destiny"
	"os"
)

const primaryMembershipId = 4611686018434106050

func main() {
	firestore := gcp.CreateFirestore(context.Background())
	destinyService := destiny.NewService(firestore)
	server := NewServer(destinyService)

	go func() {
		err := setManifest(destinyService)
		if err != nil {
			slog.With("error", err.Error()).Error("failed to set manifest")
		}
	}()

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

const manifestLocation = "./manifest.json"
const destinyBucket = "destiny"
const objectName = "manifest.json"

func setManifest(service destiny.Service) error {
	var (
		buf      bytes.Buffer
		manifest destiny.Manifest
	)
	err := gcp.DownloadFile(&buf, destinyBucket, objectName, manifestLocation)
	if err != nil {
		slog.With("error", err.Error()).Error("Failed to download manifest.json file")
		return fmt.Errorf("failed to download manifest.json file: %w", err)
	}
	manifestFile, err := os.Open(manifestLocation)
	if err != nil {
		slog.With("error", err.Error()).Error("failed to open manifest.json file")
		return err
	}

	if err := json.NewDecoder(manifestFile).Decode(&manifest); err != nil {
		slog.With("error", err.Error()).Error("failed to parse manifest.json file:", err)
		return err
	}

	err = manifestFile.Close()
	if err != nil {
		slog.Warn("failed to close manifest.json file:", err)
	}
	service.SetManifest(&manifest)
	return nil
}
