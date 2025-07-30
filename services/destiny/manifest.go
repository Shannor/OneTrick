package destiny

import (
	"cloud.google.com/go/firestore"
	"cloud.google.com/go/storage"
	"context"
	"encoding/json"
	"fmt"
	"github.com/rs/zerolog/log"
	"io"
	"log/slog"
	"net/http"
	"oneTrick/envvars"
	"oneTrick/utils"
	"os"
	"strconv"
	"time"
)

type ManifestService interface {
	Migrate(ctx context.Context) error

	GetPlaces(ctx context.Context) (map[string]PlaceDefinition, error)
	GetActivities(ctx context.Context) (map[string]ActivityDefinition, error)
	GetClasses(ctx context.Context) (map[string]ClassDefinition, error)
	GetInventoryBuckets(ctx context.Context) (map[string]InventoryBucketDefinition, error)
	GetRaces(ctx context.Context) (map[string]RaceDefinition, error)
	GetItemCategories(ctx context.Context) (map[string]ItemCategory, error)
	GetDamageTypes(ctx context.Context) (map[string]DamageType, error)
	GetActivityModes(ctx context.Context) (map[string]ActivityModeDefinition, error)
	GetStats(ctx context.Context) (map[string]StatDefinition, error)
	GetItems(ctx context.Context) (map[string]ItemDefinition, error)
	GetPerks(ctx context.Context) (map[string]PerkDefinition, error)
	GetRecords(ctx context.Context) (map[string]RecordDefinition, error)
}
type ManifestCollection string

const (
	PlaceCollection            ManifestCollection = "d2Places"
	ActivityCollection         ManifestCollection = "d2Activities"
	ClassCollection            ManifestCollection = "d2Classes"
	InventoryBucketCollection  ManifestCollection = "d2InventoryBuckets"
	RaceCollection             ManifestCollection = "d2Races"
	ItemCategoryCollection     ManifestCollection = "d2ItemCategories"
	DamageCollection           ManifestCollection = "d2DamageTypes"
	ActivityModeCollection     ManifestCollection = "d2ActivityModes"
	StatDefinitionCollection   ManifestCollection = "d2StatDefinitions"
	ItemDefinitionCollection   ManifestCollection = "d2ItemDefinitions"
	SandboxPerkCollection      ManifestCollection = "d2SandboxPerks"
	RecordDefinitionCollection ManifestCollection = "d2RecordDefinitions"
)

// ManifestService provides access to the current Destiny manifest data
type manifestService struct {
	db  *firestore.Client
	env string
}

func NewManifestService(db *firestore.Client, env string) ManifestService {
	return &manifestService{
		db:  db,
		env: env,
	}
}
func (m *manifestService) GetPlaces(ctx context.Context) (map[string]PlaceDefinition, error) {
	docs, err := m.db.Collection(string(PlaceCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[PlaceDefinition](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[PlaceDefinition, string](results, func(t PlaceDefinition) string {
		return strconv.FormatInt(t.Hash, 10)
	})
}

func (m *manifestService) GetActivities(ctx context.Context) (map[string]ActivityDefinition, error) {
	docs, err := m.db.Collection(string(ActivityCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[ActivityDefinition](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[ActivityDefinition, string](results, func(t ActivityDefinition) string {
		return strconv.FormatInt(int64(t.Hash), 10)
	})
}

func (m *manifestService) GetClasses(ctx context.Context) (map[string]ClassDefinition, error) {
	docs, err := m.db.Collection(string(ClassCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[ClassDefinition](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[ClassDefinition, string](results, func(t ClassDefinition) string {
		return strconv.FormatInt(t.Hash, 10)
	})
}

func (m *manifestService) GetInventoryBuckets(ctx context.Context) (map[string]InventoryBucketDefinition, error) {
	docs, err := m.db.Collection(string(InventoryBucketCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[InventoryBucketDefinition](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[InventoryBucketDefinition, string](results, func(t InventoryBucketDefinition) string {
		return strconv.FormatInt(t.Hash, 10)
	})
}

func (m *manifestService) GetRaces(ctx context.Context) (map[string]RaceDefinition, error) {
	docs, err := m.db.Collection(string(RaceCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[RaceDefinition](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[RaceDefinition, string](results, func(t RaceDefinition) string {
		return strconv.FormatInt(int64(t.Hash), 10)
	})
}

func (m *manifestService) GetItemCategories(ctx context.Context) (map[string]ItemCategory, error) {
	docs, err := m.db.Collection(string(ItemCategoryCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[ItemCategory](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[ItemCategory, string](results, func(t ItemCategory) string {
		return strconv.FormatInt(t.Hash, 10)
	})
}

func (m *manifestService) GetDamageTypes(ctx context.Context) (map[string]DamageType, error) {
	docs, err := m.db.Collection(string(DamageCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[DamageType](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[DamageType, string](results, func(t DamageType) string {
		return strconv.FormatInt(t.Hash, 10)
	})
}

func (m *manifestService) GetActivityModes(ctx context.Context) (map[string]ActivityModeDefinition, error) {
	docs, err := m.db.Collection(string(ActivityModeCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[ActivityModeDefinition](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[ActivityModeDefinition, string](results, func(t ActivityModeDefinition) string {
		return strconv.FormatInt(t.Hash, 10)
	})
}

func (m *manifestService) GetStats(ctx context.Context) (map[string]StatDefinition, error) {
	docs, err := m.db.Collection(string(StatDefinitionCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[StatDefinition](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[StatDefinition, string](results, func(t StatDefinition) string {
		return strconv.FormatInt(t.Hash, 10)
	})
}

func (m *manifestService) GetItems(ctx context.Context) (map[string]ItemDefinition, error) {
	docs, err := m.db.Collection(string(ItemDefinitionCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[ItemDefinition](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[ItemDefinition, string](results, func(t ItemDefinition) string {
		return strconv.FormatInt(t.Hash, 10)
	})
}

func (m *manifestService) GetPerks(ctx context.Context) (map[string]PerkDefinition, error) {
	docs, err := m.db.Collection(string(SandboxPerkCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[PerkDefinition](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[PerkDefinition, string](results, func(t PerkDefinition) string {
		return strconv.FormatInt(t.Hash, 10)
	})
}

func (m *manifestService) GetRecords(ctx context.Context) (map[string]RecordDefinition, error) {
	docs, err := m.db.Collection(string(RecordDefinitionCollection)).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[RecordDefinition](docs)
	if err != nil {
		return nil, err
	}
	return utils.ToMap[RecordDefinition, string](results, func(t RecordDefinition) string {
		return strconv.FormatInt(int64(t.Hash), 10)
	})
}

func readManifestFromLocal(ctx context.Context) (*Manifest, error) {
	var manifest *Manifest
	log.Info().Msg("reading manifest from local files")
	_, err := os.Stat(LocalManifestLocation)
	// Need to download the file
	if err != nil {
		manifestResponse, err := requestManifestInformation(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get manifest from bungie: %v", err)
		}

		path := manifestResponse.Response.JsonWorldContentPaths.EN
		manifestURL := setBaseBungieURL(&path)
		destPath := LocalManifestLocation

		err = downloadJSON(context.Background(), manifestURL, destPath)
		if err != nil {
			return nil, fmt.Errorf("failed to download manifest: %w", err)
		}
	}

	manifestFile, err := os.Open(LocalManifestLocation)
	if err != nil {
		log.Error().Err(err).Msg("failed to open manifest.json file")
		return nil, err
	}

	if err := json.NewDecoder(manifestFile).Decode(&manifest); err != nil {
		slog.With("error", err.Error()).Error("failed to parse manifest.json file:", err)
		return nil, err
	}

	err = manifestFile.Close()
	if err != nil {
		slog.Warn("failed to close manifest.json file:", err)
	}

	return manifest, nil
}

func readManifestFromMount() (*Manifest, error) {
	var manifest *Manifest
	log.Info().Msg("attempting to get manifest.json file from mount")
	stat, err := os.Stat(mntLocation)
	if err != nil {
		log.
			Error().
			Str("mountLocation", mntLocation).
			Err(err).
			Msg("file does not exist at specified location")
		return nil, fmt.Errorf("failed to find manifest at the mount location: %v", err)
	}
	if stat.IsDir() {
		return nil, fmt.Errorf("no file found at location: %s", mntLocation)
	}
	file, err := os.Open(mntLocation)
	if err != nil {
		return nil, fmt.Errorf("couldn't open manifest file: %v", err)
	}

	if err := json.NewDecoder(file).Decode(&manifest); err != nil {
		return nil, fmt.Errorf("failed to read in manfiest from file: %v", err)
	}

	err = file.Close()
	if err != nil {
		log.Warn().Err(err).Msg("failed to close manifest.json file")
	}
	log.Info().Msg("returned manifest.json file from mount")
	return manifest, nil
}

func (m *manifestService) Migrate(ctx context.Context) error {
	update, err := checkManifestUpdate(ctx, m.db)
	if err != nil {
		return fmt.Errorf("failed the check manifest: %v", err)
	}
	l := log.With().Str("version", update.Version).Logger()

	if !update.ShouldUpdate {
		l.Info().Msg("up to date")
		return nil
	}

	l.Info().Msg("manifest update required")
	err = setLatestManifest(ctx, m.env, update.ManifestURL)
	if err != nil {
		l.Error().Err(err).Msg("failed to update to latest manifest")
		return err
	}

	l.Info().Msg("reading new manifest")
	var manifest *Manifest
	if m.env == string(envvars.DevEnv) {
		manifest, err = readManifestFromLocal(ctx)
		if err != nil {
			return fmt.Errorf("failed to set the updated mainfest at run time: %v", err)
		}
	} else {
		manifest, err = readManifestFromMount()
		if err != nil {
			return fmt.Errorf("failed to set the updated mainfest at run time: %v", err)
		}
	}

	l.Info().Msg("starting migration steps")
	err = migrateD2Data(ctx, m.db, manifest)
	if err != nil {
		l.Error().Err(err).Msg("failed to migrate table entries")
	}

	l.Info().Msg("updating version")
	err = updateManifestVersion(ctx, m.db, update.Version)
	if err != nil {
		l.Error().Err(err).Msg("failed to set latest manifest version")
		return err
	}

	l.Info().Msg("migration finished")
	return nil
}

func migrateD2Data(ctx context.Context, db *firestore.Client, manifest *Manifest) error {
	if manifest == nil {
		log.Error().Msg("no manifest provided. cannot perform migration")
		return nil
	}
	log.Info().Msg("starting the migration")
	startTime := time.Now()

	// Create a channel to wait for all goroutines to finish
	migrationsDone := make(chan bool)

	// InventoryBucketDefinition
	go func() {
		loopStartTime := time.Now()
		for _, definition := range manifest.InventoryBucketDefinition {
			_, err := db.Collection(string(InventoryBucketCollection)).Doc(strconv.FormatInt(definition.Hash, 10)).Set(
				ctx, definition,
			)
			if err != nil {
				log.Error().Int64("hash", definition.Hash).Err(err).Msg("failed to save definition")
			}
		}
		log.Info().Str("collection", string(InventoryBucketCollection)).Dur("duration", time.Since(loopStartTime)).Msg("finished migrating collection")
		migrationsDone <- true
	}()

	// ClassDefinition
	go func() {
		loopStartTime := time.Now()
		for _, definition := range manifest.ClassDefinition {
			_, err := db.Collection(string(ClassCollection)).Doc(strconv.FormatInt(definition.Hash, 10)).Set(
				ctx, definition,
			)
			if err != nil {
				log.Error().Int64("hash", definition.Hash).Err(err).Msg("failed to save definition")
			}
		}
		log.Info().Str("collection", string(ClassCollection)).Dur("duration", time.Since(loopStartTime)).Msg("finished migrating collection")
		migrationsDone <- true
	}()

	// PlaceDefinition
	go func() {
		loopStartTime := time.Now()
		for _, definition := range manifest.PlaceDefinition {
			_, err := db.Collection(string(PlaceCollection)).Doc(strconv.FormatInt(definition.Hash, 10)).Set(
				ctx, definition,
			)
			if err != nil {
				log.Error().Int64("hash", definition.Hash).Err(err).Msg("failed to save definition")
			}
		}
		log.Info().Str("collection", string(PlaceCollection)).Dur("duration", time.Since(loopStartTime)).Msg("finished migrating collection")
		migrationsDone <- true
	}()

	// DamageTypeDefinition
	go func() {
		loopStartTime := time.Now()
		for _, definition := range manifest.DamageTypeDefinition {
			_, err := db.Collection(string(DamageCollection)).Doc(strconv.FormatInt(definition.Hash, 10)).Set(
				ctx, definition,
			)
			if err != nil {
				log.Error().Int64("hash", definition.Hash).Err(err).Msg("failed to save damage type")
			}
		}
		log.Info().Str("collection", string(DamageCollection)).Dur("duration", time.Since(loopStartTime)).Msg("finished migrating collection")
		migrationsDone <- true
	}()

	// ActivityModeDefinition
	go func() {
		loopStartTime := time.Now()
		for _, definition := range manifest.ActivityModeDefinition {
			_, err := db.Collection(string(ActivityModeCollection)).Doc(strconv.FormatInt(definition.Hash, 10)).Set(
				ctx, definition,
			)
			if err != nil {
				log.Error().Int64("hash", definition.Hash).Err(err).Msg("failed to save activity mode")
			}
		}
		log.Info().Str("collection", string(ActivityModeCollection)).Dur("duration", time.Since(loopStartTime)).Msg("finished migrating collection")
		migrationsDone <- true
	}()

	// ActivityDefinition
	go func() {
		loopStartTime := time.Now()
		for _, definition := range manifest.ActivityDefinition {
			_, err := db.Collection(string(ActivityCollection)).Doc(strconv.FormatInt(int64(definition.Hash), 10)).Set(
				ctx, definition,
			)
			if err != nil {
				log.Error().Int("hash", definition.Hash).Err(err).Msg("failed to save definition")
			}
		}
		log.Info().Str("collection", string(ActivityCollection)).Dur("duration", time.Since(loopStartTime)).Msg("finished migrating collection")
		migrationsDone <- true
	}()

	// ItemCategoryDefinition
	go func() {
		loopStartTime := time.Now()
		for _, definition := range manifest.ItemCategoryDefinition {
			_, err := db.Collection(string(ItemCategoryCollection)).Doc(strconv.FormatInt(definition.Hash, 10)).Set(
				ctx, definition,
			)
			if err != nil {
				log.Error().Int64("hash", definition.Hash).Err(err).Msg("failed to save definition")
			}
		}
		log.Info().Str("collection", string(ItemCategoryCollection)).Dur("duration", time.Since(loopStartTime)).Msg("finished migrating collection")
		migrationsDone <- true
	}()

	// InventoryItemDefinition
	go func() {
		loopStartTime := time.Now()
		for _, definition := range manifest.InventoryItemDefinition {
			_, err := db.Collection(string(ItemDefinitionCollection)).Doc(strconv.FormatInt(definition.Hash, 10)).Set(
				ctx, definition,
			)
			if err != nil {
				log.Error().Int64("hash", definition.Hash).Err(err).Msg("failed to save definition")
			}
		}
		log.Info().Str("collection", string(ItemDefinitionCollection)).Dur("duration", time.Since(loopStartTime)).Msg("finished migrating collection")
		migrationsDone <- true
	}()

	// StatDefinition
	go func() {
		loopStartTime := time.Now()
		for _, definition := range manifest.StatDefinition {
			_, err := db.Collection(string(StatDefinitionCollection)).Doc(strconv.FormatInt(definition.Hash, 10)).Set(
				ctx, definition,
			)
			if err != nil {
				log.Error().Int64("hash", definition.Hash).Err(err).Msg("failed to save definition")
			}
		}
		log.Info().Str("collection", string(StatDefinitionCollection)).Dur("duration", time.Since(loopStartTime)).Msg("finished migrating collection")
		migrationsDone <- true
	}()

	// RaceDefinition
	go func() {
		loopStartTime := time.Now()
		for _, definition := range manifest.RaceDefinition {
			_, err := db.Collection(string(RaceCollection)).Doc(strconv.FormatInt(int64(definition.Hash), 10)).Set(
				ctx, definition,
			)
			if err != nil {
				log.Error().Float64("hash", definition.Hash).Err(err).Msg("failed to save definition")
			}
		}
		log.Info().Str("collection", string(RaceCollection)).Dur("duration", time.Since(loopStartTime)).Msg("finished migrating collection")
		migrationsDone <- true
	}()

	// PerkDefinition
	go func() {
		loopStartTime := time.Now()
		for _, definition := range manifest.SandboxPerkDefinition {
			_, err := db.Collection(string(SandboxPerkCollection)).Doc(strconv.FormatInt(definition.Hash, 10)).Set(
				ctx, definition,
			)
			if err != nil {
				log.Error().Int64("hash", definition.Hash).Err(err).Msg("failed to save definition")
			}
		}
		log.Info().Str("collection", string(SandboxPerkCollection)).Dur("duration", time.Since(loopStartTime)).Msg("finished migrating collection")
		migrationsDone <- true
	}()

	// RecordDefinition
	go func() {
		loopStartTime := time.Now()
		for _, definition := range manifest.RecordDefinition {
			_, err := db.Collection(string(RecordDefinitionCollection)).Doc(strconv.FormatInt(int64(definition.Hash), 10)).Set(
				ctx, definition,
			)
			if err != nil {
				log.Error().Int("hash", definition.Hash).Err(err).Msg("failed to save definition")
			}
		}
		log.Info().Str("collection", string(RecordDefinitionCollection)).Dur("duration", time.Since(loopStartTime)).Msg("finished migrating collection")
		migrationsDone <- true
	}()

	// Wait for all migrations to complete
	for i := 0; i < 12; i++ {
		<-migrationsDone
	}

	log.Info().Dur("totalDuration", time.Since(startTime)).Msg("completed all migrations")
	return nil
}

func checkManifestUpdate(ctx context.Context, db *firestore.Client) (ManifestUpdate, error) {
	snapshot, err := db.Collection(ConfigurationCollection).Doc(DestinyDocument).Get(ctx)
	if err != nil {
		return ManifestUpdate{
			ShouldUpdate: false,
		}, fmt.Errorf("failed to get manifest snapshot: %v", err)
	}

	var data Configuration
	err = snapshot.DataTo(&data)
	if err != nil {
		return ManifestUpdate{ShouldUpdate: false}, err
	}
	manifestResponse, err := requestManifestInformation(context.Background())
	if err != nil {
		log.Error().Err(err).Msg("failed download")
		return ManifestUpdate{ShouldUpdate: false}, err
	}
	if data.ManifestVersion != manifestResponse.Response.Version {
		return ManifestUpdate{
			ShouldUpdate: true,
			Version:      manifestResponse.Response.Version,
			ManifestURL:  manifestResponse.Response.JsonWorldContentPaths.EN,
		}, nil
	}
	return ManifestUpdate{ShouldUpdate: false}, nil
}

func setLatestManifest(ctx context.Context, env, URL string) error {
	manifestURL := setBaseBungieURL(&URL)
	if env == "production" {
		log.Info().Str("manifestUrl", manifestURL).Msg("downloading and uploading manifest")
		err := downloadAndUpload(ctx, manifestURL, DestinyBucket, ManifestObjectName)
		if err != nil {
			return err
		}
		log.Info().Msg("download and upload finished")
	} else {
		err := downloadJSON(context.Background(), manifestURL, LocalManifestLocation)
		if err != nil {
			return err
		}
	}
	return nil
}

func updateManifestVersion(ctx context.Context, db *firestore.Client, version string) error {
	_, err := db.
		Collection(ConfigurationCollection).
		Doc(DestinyDocument).
		Set(
			ctx, map[string]interface{}{
				"manifestVersion": version,
			}, firestore.MergeAll,
		)
	if err != nil {
		log.Error().Err(err).Msg("failed to update config")
		return err
	}
	return nil
}

func requestManifestInformation(ctx context.Context) (*ManifestResponse, error) {
	// Create a request to the Bungie.net manifest endpoint
	req, err := http.NewRequestWithContext(
		ctx, http.MethodGet, "https://www.bungie.net/Platform/Destiny2/Manifest/", nil,
	)
	if err != nil {
		return nil, fmt.Errorf("building request failed: %w", err)
	}
	req.Header.Add("Accept", "application/json")
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("User-Agent", "oneTrick")

	c := &http.Client{}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot get manifest because of http failure: %w", err)
	}
	defer resp.Body.Close()

	// Check for success
	if resp.StatusCode != http.StatusOK {
		log.
			Error().
			Any("value", resp).
			Str("status", resp.Status).
			Int("statusCode", resp.StatusCode).
			Msg("issue with reaching destiny api")
		return nil, fmt.Errorf("failed to retrieve manifest")
	}

	var manifestResponse ManifestResponse
	if err := json.NewDecoder(resp.Body).Decode(&manifestResponse); err != nil {
		return nil, fmt.Errorf("failed to decode manifest: %w", err)
	}

	log.
		Info().
		Str("version", manifestResponse.Response.Version).
		Msg("Successfully downloaded manifest")

	return &manifestResponse, nil
}

func downloadAndUpload(ctx context.Context, url, bucketName, objectName string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Add headers that might be necessary for the request
	req.Header.Add("Accept", "application/json")
	req.Header.Add("User-Agent", "oneTrick")

	// Execute the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad response status: %s (code: %d)", resp.Status, resp.StatusCode)
	}

	log.Info().
		Str("url", url).
		Int64("size", resp.ContentLength).
		Msg("downloaded file from source")

	//tmpFile, err := os.CreateTemp("", "tmp-download-file.json")
	//if err != nil {
	//	return fmt.Errorf("failed to create temp file: %w", err)
	//}
	//tmpName := tmpFile.Name()
	//defer func(name string) {
	//	err := os.Remove(name)
	//	if err != nil {
	//		log.Error().Err(err).Msg("failed to remove tempfile on defer")
	//	}
	//}(tmpName) // Clean up after ourselves
	//
	//respBody, err := io.ReadAll(resp.Body)
	//if err != nil {
	//	return fmt.Errorf("failed to read response body: %w", err)
	//}
	//
	//if _, err := tmpFile.Write(respBody); err != nil {
	//	return fmt.Errorf("failed to write to temp file: %w", err)
	//}
	//defer tmpFile.Close()

	if err := uploadToBucket(ctx, bucketName, objectName, resp.Body); err != nil {
		return fmt.Errorf("failed to upload to bucket: %w", err)
	}

	log.Info().
		Str("url", url).
		Str("bucket", bucketName).
		Str("object", objectName).
		Msg("Successfully uploaded the JSON file to gcp")

	return nil
}

func uploadToBucket(ctx context.Context, bucketName, objectName string, data io.Reader) error {
	// Create a client
	client, err := storage.NewClient(ctx)
	if err != nil {
		return err
	}
	defer client.Close()

	bucket := client.Bucket(bucketName)
	obj := bucket.Object(objectName)
	// Set timeout for the upload operation
	ctx, cancel := context.WithTimeout(ctx, time.Minute*10)
	defer cancel()

	wc := obj.NewWriter(ctx)
	wc.ContentType = "application/json"

	// Copy data from the reader to the bucket
	if _, err = io.Copy(wc, data); err != nil {
		return err
	}

	// Close the writer to finalize the upload
	if err := wc.Close(); err != nil {
		return err
	}

	log.Info().
		Str("bucket", bucketName).
		Str("object", objectName).
		Msg("Successfully uploaded file to GCP bucket")
	return nil
}

// downloadJSON downloads a JSON file from the specified URL and saves it to the given destination path.
// It returns an error if any part of the process fails.
func downloadJSON(ctx context.Context, url string, destPath string) error {
	// Create a request with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Add headers that might be necessary for the request
	req.Header.Add("Accept", "application/json")
	req.Header.Add("User-Agent", "oneTrick")

	// Execute the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad response status: %s (code: %d)", resp.Status, resp.StatusCode)
	}

	// Create the destination file
	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer func() {
		closeErr := out.Close()
		if err == nil && closeErr != nil {
			err = fmt.Errorf("failed to close destination file: %w", closeErr)
		}
	}()

	// Copy the response body to the file
	_, err = io.Copy(out, resp.Body)
	if err != nil {
		return fmt.Errorf("failed to write data to file: %w", err)
	}

	// Verify JSON format by parsing it
	out.Seek(0, 0) // Reset file pointer to beginning
	var jsonObj interface{}
	jsonErr := json.NewDecoder(out).Decode(&jsonObj)
	if jsonErr != nil {
		// If the file exists but isn't valid JSON, we should remove it
		out.Close()
		os.Remove(destPath)
		return fmt.Errorf("downloaded file is not valid JSON: %w", jsonErr)
	}

	log.Info().
		Str("destination", destPath).
		Int64("size", resp.ContentLength).
		Msg("Successfully downloaded and saved JSON file")

	return nil
}
