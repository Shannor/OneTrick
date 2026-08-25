package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"cloud.google.com/go/firestore"
	"github.com/joho/godotenv"
	"google.golang.org/api/iterator"
)

func main() {
	_ = godotenv.Load()

	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		projectID = "gruntt-destiny"
	}

	// Unset FIRESTORE_EMULATOR_HOST to talk to real GCP production
	os.Unsetenv("FIRESTORE_EMULATOR_HOST")

	ctx := context.Background()
	client, err := firestore.NewClient(ctx, projectID)
	if err != nil {
		log.Fatalf("Failed to connect to GCP project %s: %v", projectID, err)
	}
	defer client.Close()

	fmt.Printf("Querying collection document counts for production GCP project '%s'...\n\n", projectID)

	colIter := client.Collections(ctx)
	totalDocs := int64(0)

	for {
		colRef, err := colIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Fatalf("Error iterating collections: %v", err)
		}

		docs, err := colRef.Limit(1000).Documents(ctx).GetAll()
		if err != nil {
			fmt.Printf("Collection '%s': Error fetching docs (%v)\n", colRef.ID, err)
			continue
		}
		count := len(docs)
		fmt.Printf(" - %-25s : %d documents\n", colRef.ID, count)
		totalDocs += int64(count)
	}

	fmt.Printf("\nTotal across all root collections: %d documents\n", totalDocs)
}
