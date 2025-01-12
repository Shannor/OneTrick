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
	"oneTrick/utils"
	"os"
	"strconv"
	"time"
)

const characterID = "2305843009261519028"

// Reference ID == ItemHash
const referenceID = "882778888"

const Kinetic = 1498876634
const Energy = 2465295065
const Power = 953998645
const SubClass = 3284755031

type Service interface {
	GetCurrentInventory(membershipID int64) ([]bungie.ItemComponent, *time.Time, error)
	// SaveCharacterSnapshot TODO: Pass membershipID in the future and character ID
	SaveCharacterSnapshot(snapshot api.CharacterSnapshot) error
	GetAllCharacterSnapshots() ([]api.CharacterSnapshot, error)
	GetClosestSnapshot(membershipID int64, activityPeriod *time.Time) (*api.CharacterSnapshot, error)
	GetWeaponDetails(ctx context.Context, membershipID string, weaponInstanceID string) (*api.ItemDetails, error)
	GetQuickPlayActivity(membershipID, characterID int64, count int64, page int64) ([]api.ActivityHistory, error)
	GetAllPVPActivity(membershipID, characterID int64, count int64, page int64) ([]api.ActivityHistory, error)
	GetCompetitiveActivity(membershipID, characterID int64, count int64, page int64) ([]api.ActivityHistory, error)
	GetActivity(ctx context.Context, characterID string, activityID int64) (*api.ActivityHistory, []bungie.HistoricalWeaponStats, *time.Time, error)
	EnrichWeaponStats(ctx context.Context, primaryMembershipId string, items []api.ItemSnapshot, stats []bungie.HistoricalWeaponStats) ([]api.WeaponStats, error)
	SetManifest(manifest *Manifest)
}

type service struct {
	client   *bungie.ClientWithResponses
	Manifest *Manifest
	DB       *firestore.Client
}

const accessToken = "CPjuBhKGAgAgPaFF75otR0QMEd5aiJ9/Zwm9DEam9oZfHluU556o3mbgAAAACMiTGscENoFDeffOB30j3GhPHUhp1ZbXJsdzjOFhGLw8HFA7triZ5s0wx965nNXdn3IDxjBjxjd65Xg+2b6yM0cgRzQAnIhPy/uvq/oBT2s9lIkPKripHs5yCOmSbZXnOHLCOr0ZvN1Dx3aWBtXDd8bgZEJrAfmnTHnBsZhTWmHMLT6A8CoNJJHJiRLgAI0EsGcbYZDTAZzt+OVur1CLS+/F/yQnhNwKzP1cmVHnu02Zq2meNcQQazkxNUPEwFcxPRycTMXEHNQH0T0pbGvX0Q3FJe9OuNLS+5VyCvJPdpo="

func NewService(apiKey string, firestore *firestore.Client) Service {
	hc := http.Client{}
	cli, err := bungie.NewClientWithResponses(
		"https://www.bungie.net/Platform",
		bungie.WithHTTPClient(&hc),
		bungie.WithRequestEditorFn(func(ctx context.Context, req *http.Request) error {
			req.Header.Add("X-API-KEY", apiKey)
			req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", accessToken))
			req.Header.Add("Accept", "application/json")
			req.Header.Add("Content-Type", "application/json")
			req.Header.Add("User-Agent", "oneTrick-backend")
			return nil
		}),
	)
	if err != nil {
		log.Fatal(err)
	}
	return &service{
		client:   cli,
		Manifest: nil,
		DB:       firestore,
	}
}

func (a *service) SetManifest(manifest *Manifest) {
	a.Manifest = manifest
	slog.Info("Manifest set")
}

func (a *service) GetQuickPlayActivity(membershipID, characterID int64, count int64, page int64) ([]api.ActivityHistory, error) {
	return getActivity(a, membershipID, characterID, count, int64(bungie.CurrentActivityModeTypePvPQuickplay), page)
}

func (a *service) GetAllPVPActivity(membershipID, characterID int64, count int64, page int64) ([]api.ActivityHistory, error) {
	return getActivity(a, membershipID, characterID, count, int64(bungie.CurrentActivityModeTypeAllPvP), page)
}

func (a *service) GetCompetitiveActivity(membershipID, characterID int64, count int64, page int64) ([]api.ActivityHistory, error) {
	return getActivity(a, membershipID, characterID, count, int64(bungie.CurrentActivityModeTypePvPCompetitive), page)
}

func getActivity(a *service, membershipID, characterID int64, count int64, mode int64, page int64) (
	[]api.ActivityHistory,
	error,
) {
	resp, err := a.client.Destiny2GetActivityHistoryWithResponse(
		context.Background(),
		2,
		membershipID,
		characterID,
		&bungie.Destiny2GetActivityHistoryParams{
			Count: utils.ToPointer(int32(count)),
			Mode:  utils.ToPointer(int32(mode)),
			Page:  utils.ToPointer(int32(page)),
		},
	)
	if err != nil {
		return nil, err
	}
	if resp.JSON200.Response.Activities == nil {
		return nil, nil
	}
	if a.Manifest == nil {
		return nil, fmt.Errorf("manifest is not provided")
	}
	return TransformPeriodGroups(*resp.JSON200.Response.Activities, *a.Manifest), nil
}

const profileFile = "profile_data.json"

func (a *service) SaveCharacterSnapshot(snapshot api.CharacterSnapshot) error {
	existingData := make(map[string]api.CharacterSnapshot)
	stat, err := os.Stat(profileFile)
	if err == nil && !stat.IsDir() {
		content, readErr := os.ReadFile(profileFile)
		if readErr == nil {
			_ = json.Unmarshal(content, &existingData) // Ignore error for simplicity
		} else {
			slog.With("error", readErr.Error()).Error("Failed to read file")
		}
	}

	file, err := os.OpenFile(profileFile, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0644)
	if err != nil {
		slog.With("error", err.Error()).Error("Failed to open file")
		return err
	}
	defer file.Close()

	existingData[snapshot.Timestamp.Format(time.RFC3339)] = snapshot
	// Marshal the updated data
	data, err := json.MarshalIndent(existingData, "", "  ")
	if err != nil {
		slog.With("error", err.Error()).Error("Failed to marshal items")
		return err
	}

	if _, err := file.Write(data); err != nil {
		slog.With("error", err.Error()).Error("Failed to write to file")
		return err
	}
	return nil
}

func (a *service) GetAllCharacterSnapshots() ([]api.CharacterSnapshot, error) {
	existingData := make(map[string]api.CharacterSnapshot)
	stat, err := os.Stat(profileFile)
	if err == nil && !stat.IsDir() {
		content, readErr := os.ReadFile(profileFile)
		if readErr == nil {
			_ = json.Unmarshal(content, &existingData) // Ignore error for simplicity
		} else {
			slog.With("error", readErr.Error()).Error("Failed to read file")
		}
	}
	var results []api.CharacterSnapshot
	for _, snapshot := range existingData {
		results = append(results, snapshot)
	}
	return results, nil
}
func (a *service) GetActivity(ctx context.Context, characterID string, activityID int64) (*api.ActivityHistory, []bungie.HistoricalWeaponStats, *time.Time, error) {

	resp, err := a.client.Destiny2GetPostGameCarnageReportWithResponse(ctx, activityID)
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

func (a *service) GetClosestSnapshot(membershipID int64, activityPeriod *time.Time) (*api.CharacterSnapshot, error) {
	existingData := make(map[string]api.CharacterSnapshot)
	stat, err := os.Stat(profileFile)
	if err == nil && !stat.IsDir() {
		content, readErr := os.ReadFile(profileFile)
		if readErr == nil {
			_ = json.Unmarshal(content, &existingData) // Ignore error for simplicity
		} else {
			slog.With("error", readErr.Error()).Error("Failed to read file")
		}
	}

	var closestSnapshot string
	minDuration := time.Duration(1<<63 - 1) // Max duration value

	for snapshot := range existingData {
		t, err := time.Parse(time.RFC3339, snapshot)
		if err != nil {
			slog.With("error", err.Error(), "snapshot", snapshot).Error("Failed to parse snapshot time")
			continue
		}

		duration := t.Sub(*activityPeriod)
		if duration < 0 {
			duration = -duration
		}

		if duration < minDuration {
			minDuration = duration
			closestSnapshot = snapshot
		}
	}

	if closestSnapshot == "" {
		return nil, fmt.Errorf("no matching snapshot found for membership ID %d", membershipID)
	}

	snap := existingData[closestSnapshot]
	return &snap, nil
}

const (
	ItemPerks      = 302
	ItemStatsCode  = 304
	ItemSockets    = 305
	ItemCommonData = 307
)

func (a *service) GetWeaponDetails(ctx context.Context, membershipID string, weaponInstanceID string) (*api.ItemDetails, error) {
	components := []int32{ItemPerks, ItemStatsCode, ItemSockets, ItemCommonData}
	membershipIdInt64, err := strconv.ParseInt(membershipID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to convert membershipId to int64: %v", err)
	}
	weaponInstanceIDInt64, err := strconv.ParseInt(weaponInstanceID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to convert membershipId to int64: %v", err)
	}
	params := bungie.Destiny2GetItemParams{Components: &components}
	response, err := a.client.Destiny2GetItemWithResponse(
		ctx,
		2,
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
func (a *service) GetCurrentInventory(membershipId int64) ([]bungie.ItemComponent, *time.Time, error) {
	var components []int32
	components = append(components, 205)
	params := &bungie.Destiny2GetProfileParams{
		Components: &components,
	}
	test, err := a.client.Destiny2GetProfileWithResponse(context.Background(), 2, membershipId, params)
	if err != nil {
		slog.With(err).Error(err.Error())
		return nil, nil, err
	}
	// TODO: Update snapshot to include the guns information as it is now, since mods and perks could change on the same gun.

	if test.JSON200 == nil {
		return nil, nil, fmt.Errorf("no response found")
	}

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
func (a *service) EnrichWeaponStats(ctx context.Context, primaryMembershipId string, items []api.ItemSnapshot, stats []bungie.HistoricalWeaponStats) ([]api.WeaponStats, error) {
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
		//details, err := a.GetWeaponDetails(ctx, primaryMembershipId, id)
		//if err != nil {
		//	slog.With(
		//		"error",
		//		err.Error(),
		//		"weapon instance id",
		//		id,
		//	).Error("failed to get details for weapon")
		//}

		result.ItemDetails = &details
		results = append(results, result)
	}

	return results, nil
}
