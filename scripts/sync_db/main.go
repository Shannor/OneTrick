package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/joho/godotenv"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var defaultCollections = []string{
	"users",
	"snapshots",
	"aggregates",
	"sessions",
	"configurations",
	"d2Places",
	"d2Activities",
	"d2Classes",
	"d2InventoryBuckets",
	"d2Races",
	"d2ItemCategories",
	"d2DamageTypes",
	"d2ActivityModes",
	"d2StatDefinitions",
	"d2ItemDefinitions",
	"d2SandboxPerks",
	"d2RecordDefinitions",
}

type docTask struct {
	dstRef *firestore.DocumentRef
	data   map[string]interface{}
}

func main() {
	_ = godotenv.Load()

	projectIDFlag := flag.String("project", "gruntt-destiny", "GCP Project ID for production Firestore")
	emulatorHostFlag := flag.String("emulator", "localhost:8081", "Host:port of the local Firestore emulator")
	limitFlag := flag.Int("limit", 0, "Max documents to copy per collection (0 for unlimited)")
	collectionsFlag := flag.String("collections", "", "Comma-separated list of collections to copy (leave empty for all defaults)")
	skipManifestFlag := flag.Bool("skip-manifest", false, "Skip d2* manifest collections for super-fast sync of app data only")
	concurrencyFlag := flag.Int("concurrency", 50, "Number of concurrent workers for high-speed batching")
	flag.Parse()

	ctx := context.Background()

	prodProjectID := *projectIDFlag
	emulatorHost := *emulatorHostFlag
	workers := *concurrencyFlag

	// Ensure FIRESTORE_EMULATOR_HOST is unset while initializing prod client
	origEmulatorHost := os.Getenv("FIRESTORE_EMULATOR_HOST")
	os.Unsetenv("FIRESTORE_EMULATOR_HOST")

	fmt.Printf("Connecting to production GCP Firestore (Project: %s)...\n", prodProjectID)
	prodClient, err := firestore.NewClient(ctx, prodProjectID)
	if err != nil {
		log.Fatalf("Failed to connect to production Firestore: %v", err)
	}
	defer prodClient.Close()

	// Restore FIRESTORE_EMULATOR_HOST if it was set
	if origEmulatorHost != "" {
		os.Setenv("FIRESTORE_EMULATOR_HOST", origEmulatorHost)
	}

	fmt.Printf("Connecting to local Firestore emulator (%s)...\n", emulatorHost)
	emulatorClient, err := firestore.NewClient(
		ctx,
		prodProjectID,
		option.WithEndpoint(emulatorHost),
		option.WithoutAuthentication(),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())),
	)
	if err != nil {
		log.Fatalf("Failed to connect to Firestore emulator: %v", err)
	}
	defer emulatorClient.Close()

	var targetCollections []string
	if *collectionsFlag != "" {
		targetCollections = strings.Split(*collectionsFlag, ",")
		for i, c := range targetCollections {
			targetCollections[i] = strings.TrimSpace(c)
		}
	} else {
		fmt.Println("Discovering root collections from production Firestore...")
		colIter := prodClient.Collections(ctx)
		for {
			colRef, err := colIter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				fmt.Printf("Warning: failed discovering root collections: %v. Falling back to defaults.\n", err)
				targetCollections = defaultCollections
				break
			}
			targetCollections = append(targetCollections, colRef.ID)
		}
		if len(targetCollections) == 0 {
			targetCollections = defaultCollections
		}
	}

	if *skipManifestFlag {
		fmt.Println("Skipping d2* manifest collections (-skip-manifest enabled)...")
		filtered := make([]string, 0, len(targetCollections))
		for _, c := range targetCollections {
			if !strings.HasPrefix(c, "d2") {
				filtered = append(filtered, c)
			}
		}
		targetCollections = filtered
	}

	start := time.Now()
	fmt.Printf("Starting parallel data sync (concurrency: %d, limit: %d per collection)...\n\n", workers, *limitFlag)

	bw := emulatorClient.BulkWriter(ctx)

	var totalDocs int64
	taskChan := make(chan docTask, workers*10)

	// Single writer goroutine submitting jobs to BulkWriter (BulkWriter buffers & streams in parallel internally)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for task := range taskChan {
			_, err := bw.Set(task.dstRef, task.data)
			if err != nil {
				log.Printf("Warning: failed setting doc %s: %v", task.dstRef.Path, err)
			} else {
				atomic.AddInt64(&totalDocs, 1)
			}
		}
	}()

	var colWg sync.WaitGroup
	for _, colName := range targetCollections {
		colWg.Add(1)
		c := colName
		go func(collectionName string) {
			defer colWg.Done()
			colStart := time.Now()
			count := copyCollection(ctx, prodClient.Collection(collectionName), emulatorClient.Collection(collectionName), *limitFlag, taskChan)
			fmt.Printf("  - Synced '%s': %d document(s) [%s]\n", collectionName, count, time.Since(colStart).Round(time.Millisecond))
		}(c)
	}

	colWg.Wait()
	close(taskChan)
	wg.Wait()

	fmt.Println("Flushing final writes to local emulator...")
	bw.Flush()

	fmt.Printf("\nDone! Copied %d total document(s) in %s.\n", atomic.LoadInt64(&totalDocs), time.Since(start).Round(time.Millisecond))
}

func copyCollection(ctx context.Context, srcCol *firestore.CollectionRef, dstCol *firestore.CollectionRef, limit int, taskChan chan<- docTask) int {
	var query firestore.Query = srcCol.Query
	if limit > 0 {
		query = query.Limit(limit)
	}

	iter := query.Documents(ctx)
	defer iter.Stop()

	count := 0
	for {
		docSnap, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Printf("Error fetching doc from %s: %v", srcCol.ID, err)
			break
		}

		data := docSnap.Data()
		dstRef := dstCol.Doc(docSnap.Ref.ID)

		taskChan <- docTask{
			dstRef: dstRef,
			data:   data,
		}
		count++

		// Directly sync known subcollections (e.g. "histories" under "snapshots")
		// instead of calling docSnap.Ref.Collections(ctx).GetAll() which makes thousands of network RPC calls to GCP
		if srcCol.ID == "snapshots" {
			histCol := srcCol.Doc(docSnap.Ref.ID).Collection("histories")
			subCount := copyCollection(ctx, histCol, dstRef.Collection("histories"), limit, taskChan)
			count += subCount
		}
	}

	return count
}
