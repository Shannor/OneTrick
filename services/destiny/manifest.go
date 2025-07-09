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
	"os"
	"strconv"
	"time"
)

type ManifestService interface {
	Init() error
	Get(ctx context.Context) (*Manifest, error)
	Update(ctx context.Context) error
}

const (
	InventoryBucketCollection = "inventoryBucket"
	ItemCategoryCollection    = "itemCategory"
)

// ManifestService provides access to the current Destiny manifest data
type manifestService struct {
	Current *Manifest
	db      *firestore.Client
	env     string
}

func NewManifestService(db *firestore.Client, env string) ManifestService {
	return &manifestService{
		db:  db,
		env: env,
	}
}
func (m *manifestService) Init() error {
	// Check if the local file is here (local) or gcp (production)
	// If not there, go grab the data and bring it in
	return nil
}

func (m *manifestService) Get(ctx context.Context) (*Manifest, error) {
	if m.Current != nil {
		return m.Current, nil
	}

	if m.env == "production" {
		data, err := readManifestFromMount()
		if err != nil {
			return nil, err
		}
		m.Current = data
	} else {
		data, err := readManifestFromLocal(ctx)
		if err != nil {
			return nil, err
		}
		m.Current = data
	}
	return m.Current, nil
}

func readManifestFromLocal(ctx context.Context) (*Manifest, error) {
	var manifest *Manifest
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

func (m *manifestService) Update(ctx context.Context) error {
	update, err := checkManifestUpdate(ctx, m.db)
	if err != nil {
		return fmt.Errorf("failed the check manifest: %v", err)
	}

	if update.ShouldUpdate {
		log.Info().Msg("need to update the manifest")
		err := setLatestManifest(ctx, m.env, update.ManifestURL)
		if err != nil {
			log.Error().Err(err).Msg("failed to update to latest manifest")
			return err
		}
		err = updateManifest(ctx, m.db, update.Version)
		if err != nil {
			log.Error().Err(err).Msg("failed to set latest manifest version")
			return err
		}
		log.Info().Msg("updated manifest in bucket and firebase")
	} else {
		if m.Current != nil {
			log.Info().Msg("no need to update and already have most up to date manifest")
			return nil
		}
	}

	if m.env == "production" {
		data, err := readManifestFromMount()
		if err != nil {
			return fmt.Errorf("failed to set the updated mainfest at run time: %v", err)
		}
		m.Current = data
	} else {
		data, err := readManifestFromLocal(ctx)
		if err != nil {
			return fmt.Errorf("failed to set the updated mainfest at run time: %v", err)
		}
		m.Current = data
	}

	// TODO: Add logic to upload data to firebase for testing
	err = updateTables(ctx, m.db, m.Current)
	if err != nil {
		log.Error().Err(err).Msg("failed to update table entries")
	}
	log.Info().Msg("set manifest in memory successfully")
	return nil
}

func updateTables(ctx context.Context, db *firestore.Client, manifest *Manifest) error {
	if manifest == nil {
		return nil
	}
	for _, definition := range manifest.InventoryBucketDefinition {
		_, err := db.Collection(InventoryBucketCollection).Doc(strconv.FormatInt(definition.Hash, 10)).Set(ctx, definition)
		if err != nil {
			log.Error().Int64("hash", definition.Hash).Err(err).Msg("failed to save definition")
		}

	}
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
		err := downloadAndUpload(ctx, manifestURL, DestinyBucket, ManifestObjectName)
		if err != nil {
			return err
		}
	} else {
		err := downloadJSON(context.Background(), manifestURL, LocalManifestLocation)
		if err != nil {
			return err
		}
	}
	return nil
}

func updateManifest(ctx context.Context, db *firestore.Client, version string) error {
	_, err := db.
		Collection(ConfigurationCollection).
		Doc(DestinyDocument).
		Set(ctx, map[string]interface{}{
			"manifestVersion": version,
		}, firestore.MergeAll)
	if err != nil {
		log.Error().Err(err).Msg("failed to update config")
		return err
	}
	return nil
}

func requestManifestInformation(ctx context.Context) (*ManifestResponse, error) {
	// Create a request to the Bungie.net manifest endpoint
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.bungie.net/Platform/Destiny2/Manifest/", nil)
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

	// Optionally, you can verify it's valid JSON before uploading
	// Create a temporary file to validate JSON
	tmpFile, err := os.CreateTemp("", "json-validation-*.json")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName) // Clean up after ourselves

	// Copy response to temp file for validation
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	if _, err := tmpFile.Write(respBody); err != nil {
		return fmt.Errorf("failed to write to temp file: %w", err)
	}
	tmpFile.Close()

	// Validate JSON
	validateFile, err := os.Open(tmpName)
	if err != nil {
		return fmt.Errorf("failed to open temp file: %w", err)
	}
	defer validateFile.Close()

	var jsonObj interface{}
	jsonErr := json.NewDecoder(validateFile).Decode(&jsonObj)
	if jsonErr != nil {
		return fmt.Errorf("downloaded file is not valid JSON: %w", jsonErr)
	}

	// Upload to GCP bucket
	uploadFile, err := os.Open(tmpName)
	if err != nil {
		return fmt.Errorf("failed to open temp file for upload: %w", err)
	}
	defer uploadFile.Close()

	if err := uploadToBucket(ctx, bucketName, objectName, uploadFile); err != nil {
		return fmt.Errorf("failed to upload to bucket: %w", err)
	}

	slog.Info("Successfully downloaded and uploaded JSON file",
		"url", url,
		"bucket", bucketName,
		"object", objectName,
		"size", len(respBody))

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
	ctx, cancel := context.WithTimeout(ctx, time.Minute*5)
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
