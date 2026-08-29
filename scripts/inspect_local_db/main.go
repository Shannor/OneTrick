package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"cloud.google.com/go/firestore"
	"github.com/joho/godotenv"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	_ = godotenv.Load()

	emulatorHost := os.Getenv("FIRESTORE_EMULATOR_HOST")
	if emulatorHost == "" {
		emulatorHost = "localhost:8081"
	}
	projectID := os.Getenv("GCP_PROJECT_ID")
	if projectID == "" {
		projectID = "gruntt-destiny"
	}

	ctx := context.Background()
	client, err := firestore.NewClient(
		ctx,
		projectID,
		option.WithEndpoint(emulatorHost),
		option.WithoutAuthentication(),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
	)
	if err != nil {
		log.Fatalf("Failed to connect to emulator: %v", err)
	}
	defer client.Close()

	fmt.Println("=== Inspecting Local Firestore Database ===")

	// 1. Inspect Snapshots
	snaps, err := client.Collection("snapshots").Limit(5).Documents(ctx).GetAll()
	if err != nil {
		fmt.Printf("Error reading snapshots: %v\n", err)
	} else {
		fmt.Printf("\n--- Snapshots (Sample %d) ---\n", len(snaps))
		for _, s := range snaps {
			d := s.Data()
			fmt.Printf("ID: %s | UserID: %v | CharacterID: %v | CreatedAt: %v | Name: %v\n",
				s.Ref.ID, d["userId"], d["characterId"], d["createdAt"], d["name"])
		}
	}

	// 2. Inspect Aggregates
	aggs, err := client.Collection("aggregates").Limit(5).Documents(ctx).GetAll()
	if err != nil {
		fmt.Printf("Error reading aggregates: %v\n", err)
	} else {
		fmt.Printf("\n--- Aggregates (Sample %d) ---\n", len(aggs))
		for _, a := range aggs {
			d := a.Data()
			fmt.Printf("ID: %s | ActivityID: %v | SnapshotIDs: %v | SnapshotLinks: %v | Performance: %v\n",
				a.Ref.ID, d["activityId"], d["snapshotIds"], d["snapshotLinks"], d["performance"])
		}
	}

	// 3. Inspect Users
	users, err := client.Collection("users").Limit(5).Documents(ctx).GetAll()
	if err != nil {
		fmt.Printf("Error reading users: %v\n", err)
	} else {
		fmt.Printf("\n--- Users (Sample %d) ---\n", len(users))
		for _, u := range users {
			d := u.Data()
			fmt.Printf("ID: %s | DisplayName: %v | UniqueName: %v\n",
				u.Ref.ID, d["displayName"], d["uniqueName"])
		}
	}

	// 4. Inspect Sessions
	sessions, err := client.Collection("sessions").Limit(5).Documents(ctx).GetAll()
	if err != nil {
		fmt.Printf("Error reading sessions: %v\n", err)
	} else {
		fmt.Printf("\n--- Sessions (Sample %d) ---\n", len(sessions))
		for _, s := range sessions {
			d := s.Data()
			fmt.Printf("ID: %s | UserID: %v | CharacterID: %v | Status: %v | Summary: %+v\n",
				s.Ref.ID, d["userId"], d["characterId"], d["status"], d["sessionSummary"])
		}
	}
}
