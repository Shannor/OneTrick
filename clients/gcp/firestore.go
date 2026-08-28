package gcp

import (
	"cloud.google.com/go/firestore"
	"context"
	"log/slog"
	"os"
)

func CreateFirestore(ctx context.Context) *firestore.Client {
	// Sets your Google Cloud Platform project ID.
	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		projectID = "gruntt-destiny"
	}

	emulatorHost := os.Getenv("FIRESTORE_EMULATOR_HOST")
	if emulatorHost != "" {
		slog.Info("Connecting to Firestore emulator", "host", emulatorHost, "projectID", projectID)
	} else {
		slog.Info("Connecting to Google Cloud Firestore", "projectID", projectID)
	}

	client, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		slog.Error("Failed to create Firestore client", "projectID", projectID, "error", err)
		os.Exit(1)
	}
	slog.Info("Successfully initialized Firestore client", "projectID", projectID)
	return client
}
