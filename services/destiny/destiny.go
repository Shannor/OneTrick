package destiny

import (
	"cloud.google.com/go/firestore"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"oneTrick/api"
	"oneTrick/clients/bungie"
	"oneTrick/clients/gcp"
	"oneTrick/envvars"
	"oneTrick/utils"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

const Kinetic = 1498876634
const Energy = 2465295065
const Power = 953998645
const SubClass = 3284755031

type Service interface {
	GetCurrentInventory(ctx context.Context, membershipID int64, membershipType int64, characterID string) ([]bungie.ItemComponent, *time.Time, error)
	GetCharacters(ctx context.Context, primaryMembershipId int64, membershipType int64) ([]api.Character, error)
	GetWeaponDetails(ctx context.Context, membershipID string, membershipType int64, weaponInstanceID string) (*api.ItemDetails, error)
	GetQuickPlayActivity(ctx context.Context, membershipID string, membershipType int64, characterID string, count int64, page int64) ([]api.ActivityHistory, error)
	GetAllPVPActivity(ctx context.Context, membershipID string, membershipType int64, characterID string, count int64, page int64) ([]api.ActivityHistory, error)
	GetCompetitiveActivity(ctx context.Context, membershipID string, membershipType int64, characterID string, count int64, page int64) ([]api.ActivityHistory, error)
	GetIronBannerActivity(ctx context.Context, membershipID string, membershipType int64, characterID string, count int64, page int64) ([]api.ActivityHistory, error)
	GetActivity(ctx context.Context, characterID string, activityID int64) (*api.ActivityHistory, []bungie.HistoricalWeaponStats, *time.Time, error)
	EnrichWeaponStats(items []api.ItemSnapshot, stats []bungie.HistoricalWeaponStats) ([]api.WeaponStats, error)
}

type service struct {
	Client   *bungie.ClientWithResponses
	Manifest *Manifest
	DB       *firestore.Client
}

func NewService(apiKey string, firestore *firestore.Client) Service {
	hc := http.Client{}
	cli, err := bungie.NewClientWithResponses(
		"https://www.bungie.net/Platform",
		bungie.WithHTTPClient(&hc),
		bungie.WithRequestEditorFn(func(ctx context.Context, req *http.Request) error {
			req.Header.Add("X-API-KEY", apiKey)
			req.Header.Add("Accept", "application/json")
			req.Header.Add("Content-Type", "application/json")
			req.Header.Add("User-Agent", "oneTrick-backend")
			return nil
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	manifest, err := getManifest()
	if err != nil {
		slog.With("error", err.Error()).Error("Failed to get manifest")
	}
	slog.Info("Manifest loaded")
	return &service{
		Client:   cli,
		Manifest: manifest,
		DB:       firestore,
	}
}

const mntLocation = "mnt/destiny/manifest.json"
const manifestLocation = "./manifest.json"
const destinyBucket = "destiny"
const objectName = "manifest.json"

func getManifest() (*Manifest, error) {
	var (
		manifest = &Manifest{}
	)

	env := envvars.GetEvn()
	if env.Environment == "production" {
		slog.Info("Attempting to set manifest.json file for production environment")
		stat, err := os.Stat(mntLocation)
		if err != nil {
			slog.With("error", err.Error()).Error("File does not exist at specified location")
			return nil, err
		}
		if stat.IsDir() {
			slog.With("error", "path is a directory").Error("Invalid file path")
			return nil, fmt.Errorf("path is a directory")
		}
		file, err := os.Open(mntLocation)
		if err != nil {
			slog.With("error", err.Error()).Error("Failed to open file")
			return nil, err
		}
		if err := json.NewDecoder(file).Decode(&manifest); err != nil {
			slog.With("error", err.Error()).Error("failed to parse manifest.json file:", err)
			return nil, err
		}

		err = file.Close()
		if err != nil {
			slog.Warn("failed to close manifest.json file:", err)
		}
		defer file.Close()
		return manifest, nil
	}

	slog.Info("Attempting to set manifest.json file for dev environment")
	stat, err := os.Stat(manifestLocation)
	if err != nil {
		slog.With("error", err.Error()).Error("File does not exist at specified location")
		return nil, err
	}
	if !stat.IsDir() {
		slog.Info("File exists at specified location")
	} else {
		err := gcp.DownloadFile(destinyBucket, objectName, manifestLocation)
		if err != nil {
			slog.With("error", err.Error()).Error("Failed to download manifest.json file")
			return nil, err
		}
	}
	manifestFile, err := os.Open(manifestLocation)
	if err != nil {
		slog.With("error", err.Error()).Error("failed to open manifest.json file")
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

func (a *service) GetQuickPlayActivity(ctx context.Context, membershipID string, membershipType int64, characterID string, count int64, page int64) ([]api.ActivityHistory, error) {
	return getActivity(a, ctx, membershipID, membershipType, characterID, count, int64(bungie.CurrentActivityModeTypePvPQuickplay), page)
}

func (a *service) GetAllPVPActivity(ctx context.Context, membershipID string, membershipType int64, characterID string, count int64, page int64) ([]api.ActivityHistory, error) {
	return getActivity(a, ctx, membershipID, membershipType, characterID, count, int64(bungie.CurrentActivityModeTypeAllPvP), page)
}

func (a *service) GetCompetitiveActivity(ctx context.Context, membershipID string, membershipType int64, characterID string, count int64, page int64) ([]api.ActivityHistory, error) {
	return getActivity(a, ctx, membershipID, membershipType, characterID, count, int64(bungie.CurrentActivityModeTypePvPCompetitive), page)
}

func (a *service) GetIronBannerActivity(ctx context.Context, membershipID string, membershipType int64, characterID string, count int64, page int64) ([]api.ActivityHistory, error) {
	return getActivity(a, ctx, membershipID, membershipType, characterID, count, int64(bungie.CurrentActivityModeTypeIronBanner), page)
}

func getActivity(a *service, ctx context.Context, membershipID string, membershipType int64, characterID string, count int64, mode int64, page int64) (
	[]api.ActivityHistory,
	error,
) {
	cID, err := strconv.ParseInt(characterID, 10, 64)
	if err != nil {
		return nil, err
	}
	mID, err := strconv.ParseInt(membershipID, 10, 64)
	if err != nil {
		return nil, err
	}
	s := slog.With(
		"membershipID", membershipID,
		"membershipType", membershipType,
		"characterID", characterID,
		"count", count,
		"mode", mode,
		"page", page,
	)
	resp, err := a.Client.Destiny2GetActivityHistoryWithResponse(
		ctx,
		int32(membershipType),
		mID,
		cID,
		&bungie.Destiny2GetActivityHistoryParams{
			Count: utils.ToPointer(int32(count)),
			Mode:  utils.ToPointer(int32(mode)),
			Page:  utils.ToPointer(int32(page)),
		},
	)
	if err != nil {
		s.With("error", err.Error()).Error("Failed to get activity history")
		return nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		s.With("status", resp.Status(), "status code", resp.StatusCode()).Error("Failed to get activity history")
		return nil, fmt.Errorf("failed to get activity history")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("no response found")
	}
	if resp.JSON200.Response == nil {
		return nil, fmt.Errorf("no response found")
	}
	if resp.JSON200.Response.Activities == nil {
		return nil, fmt.Errorf("no activities found")
	}
	if a.Manifest == nil {
		return nil, fmt.Errorf("manifest is not provided")
	}
	return TransformPeriodGroups(*resp.JSON200.Response.Activities, *a.Manifest), nil
}

func (a *service) GetActivity(ctx context.Context, characterID string, activityID int64) (*api.ActivityHistory, []bungie.HistoricalWeaponStats, *time.Time, error) {

	resp, err := a.Client.Destiny2GetPostGameCarnageReportWithResponse(ctx, activityID)
	if err != nil {
		slog.With(
			"error",
			err.Error(),
			"activity id",
			activityID,
		).Error("Failed to get post game carnage report")
		return nil, nil, nil, err
	}
	data := resp.JSON200.PostGameCarnageReportData
	if data.Entries == nil || data.ActivityDetails == nil {
		slog.With("activity id", activityID).Error("No data found for activity")
		return nil, nil, nil, nil
	}

	var weapons []bungie.HistoricalWeaponStats
	for _, entry := range *data.Entries {
		if entry.CharacterId == nil {
			continue
		}
		if *entry.CharacterId == characterID {
			weapons = *entry.Extended.Weapons
			break
		}

	}
	if weapons == nil {
		return nil, nil, nil, fmt.Errorf("no data found for characterID: %s", characterID)
	}
	if a.Manifest == nil {
		return nil, nil, nil, fmt.Errorf("manifest is not provided")
	}
	return TransformHistoricActivity(data.ActivityDetails, *a.Manifest), weapons, data.Period, nil
}

const (
	ItemInstanceCode   = 300
	ItemPerksCode      = 302
	ItemStatsCode      = 304
	ItemSocketsCode    = 305
	ItemCommonDataCode = 307
)

func (a *service) GetWeaponDetails(ctx context.Context, membershipID string, membershipType int64, weaponInstanceID string) (*api.ItemDetails, error) {
	components := []int32{ItemPerksCode, ItemStatsCode, ItemSocketsCode, ItemCommonDataCode, ItemInstanceCode}
	membershipIdInt64, err := strconv.ParseInt(membershipID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to convert membershipId to int64: %v", err)
	}
	weaponInstanceIDInt64, err := strconv.ParseInt(weaponInstanceID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to convert membershipId to int64: %v", err)
	}
	params := bungie.Destiny2GetItemParams{Components: &components}
	response, err := a.Client.Destiny2GetItemWithResponse(
		ctx,
		int32(membershipType),
		membershipIdInt64,
		weaponInstanceIDInt64,
		&params,
	)
	if err != nil {
		slog.With(
			"error",
			err.Error(),
			"weapon instance id",
			weaponInstanceID,
		).Error("Failed to get item details")
		return nil, err
	}
	if response.JSON200.DestinyItem == nil {
		return nil, nil
	}
	if a.Manifest == nil {
		return nil, fmt.Errorf("manifest is not provided")
	}
	return TransformItemToDetails(response.JSON200.DestinyItem, *a.Manifest), nil
}
func (a *service) GetCurrentInventory(ctx context.Context, membershipID int64, membershipType int64, characterID string) ([]bungie.ItemComponent, *time.Time, error) {
	var components []int32
	components = append(components, 205)
	params := &bungie.Destiny2GetProfileParams{
		Components: &components,
	}
	test, err := a.Client.Destiny2GetProfileWithResponse(ctx, int32(membershipType), membershipID, params)
	if err != nil {
		return nil, nil, err
	}

	// TODO: Update snapshot to include the guns information as it is now, since mods and perks could change on the same gun.

	if test.JSON200 == nil {
		return nil, nil, fmt.Errorf("no response found")
	}

	// TODO: Update this function to take a snapshot for all characters at once
	timeStamp := test.JSON200.Response.ResponseMintedTimestamp
	var items []bungie.ItemComponent
	if test.JSON200.Response.CharacterEquipment.Data != nil {
		equipment := *test.JSON200.Response.CharacterEquipment.Data
		for ID, equ := range equipment {
			if characterID == ID {
				items = *equ.Items
			}
		}

	}
	results := make([]bungie.ItemComponent, 0)
	for _, item := range items {
		switch *item.BucketHash {
		case Kinetic:
			results = append(results, item)
		case Energy:
			results = append(results, item)
		case Power:
			results = append(results, item)
		case SubClass:
			results = append(results, item)
		}
	}
	return results, timeStamp, nil
}

func (a *service) GetCharacters(ctx context.Context, primaryMembershipId int64, membershipType int64) ([]api.Character, error) {
	var components []int32
	components = append(components, 200)
	params := &bungie.Destiny2GetProfileParams{
		Components: &components,
	}
	resp, err := a.Client.Destiny2GetProfileWithResponse(ctx, int32(membershipType), primaryMembershipId, params)
	if err != nil {
		slog.With("error", err.Error()).Error("failed to get profile")
		return nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		if resp.StatusCode() == http.StatusServiceUnavailable {
			return nil, ErrDestinyServerDown
		}
		slog.With("status", resp.Status(), "status code", resp.StatusCode()).Error("failed to get profile")
		return nil, fmt.Errorf("failed to get characters")
	}

	if resp.JSON200 == nil {
		return nil, fmt.Errorf("no response found")
	}

	if resp.JSON200.Response.Characters == nil {
		return nil, fmt.Errorf("no response found")
	}
	results := make([]api.Character, 0)
	for _, c := range *resp.JSON200.Response.Characters.Data {
		r := TransformCharacter(&c, *a.Manifest)
		results = append(results, r)
	}
	slices.SortFunc(results, func(a, b api.Character) int {
		if a.Light != b.Light {
			return int(b.Light - a.Light)
		}
		return strings.Compare(a.Class, b.Class)
	})
	return results, nil
}

func (a *service) EnrichWeaponStats(items []api.ItemSnapshot, stats []bungie.HistoricalWeaponStats) ([]api.WeaponStats, error) {
	mapping := map[int64]api.ItemDetails{}
	for _, component := range items {
		mapping[component.ItemHash] = component.ItemDetails
	}

	results := make([]api.WeaponStats, 0)
	for _, stats := range stats {
		result := api.WeaponStats{}
		details, ok := mapping[int64(*stats.ReferenceId)]
		if !ok {
			slog.Warn("No instance id found for reference id: ", *stats.ReferenceId)
			continue
		}
		result.ReferenceId = uintToInt64(stats.ReferenceId)
		result.Stats = TransformD2HistoricalStatValues(stats.Values)
		result.ItemDetails = &details
		results = append(results, result)
	}

	return results, nil
}
