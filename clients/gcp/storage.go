package gcp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"cloud.google.com/go/storage"
)

// DownloadFile downloads an object to a local file destination on the machine.
func DownloadFile(bucketName, objectName, destFileName string) error {
	ctx := context.Background()
	client, err := storage.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("storage.NewClient: %w", err)
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(ctx, time.Second*60)
	defer cancel()

	slog.Debug("Downloading blob", "objectName", objectName, "destFileName", destFileName)
	f, err := os.Create(destFileName)
	if err != nil {
		return fmt.Errorf("os.Create: %w", err)
	}
	slog.Debug("Created file", "objectName", objectName, "destFileName", destFileName)

	rc, err := client.Bucket(bucketName).Object(objectName).NewReader(ctx)
	if err != nil {
		return fmt.Errorf("Object(%q).NewReader: %w", objectName, err)
	}
	defer rc.Close()

	if _, err := io.Copy(f, rc); err != nil {
		return fmt.Errorf("io.Copy: %w", err)
	}

	if err = f.Close(); err != nil {
		return fmt.Errorf("f.Close: %w", err)
	}

	slog.Debug("Blob downloaded successfully", "objectName", objectName, "destFileName", destFileName)
	return nil

}
