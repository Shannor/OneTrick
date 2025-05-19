package destiny

import (
	"cloud.google.com/go/firestore"
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"log/slog"
	"net/http"
	"oneTrick/api"
	"oneTrick/clients/bungie"
	"oneTrick/ptr"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Service interface {
	GetLoadout(ctx context.Context, membershipID int64, membershipType int64, characterID string) (api.Loadout, map[string]api.ClassStat, *time.Time, error)
	GetCharacters(ctx context.Context, primaryMembershipId int64, membershipType int64) ([]api.Character, error)
	GetItemDetails(ctx context.Context, membershipID string, membershipType int64, weaponInstanceID string) (*api.ItemProperties, error)
	GetQuickPlayActivity(ctx context.Context, membershipID string, membershipType int64, characterID string, count int64, page int64) ([]api.ActivityHistory, error)
	GetAllPVPActivity(ctx context.Context, membershipID string, membershipType int64, characterID string, count int64, page int64) ([]api.ActivityHistory, error)
	GetCompetitiveActivity(ctx context.Context, membershipID string, membershipType int64, characterID string, count int64, page int64) ([]api.ActivityHistory, error)
	GetIronBannerActivity(ctx context.Context, membershipID string, membershipType int64, characterID string, count int64, page int64) ([]api.ActivityHistory, error)
	GetEnrichedActivity(ctx context.Context, characterID string, activityID string) (*EnrichedActivity, error)
}

type service struct {
	Client          *bungie.ClientWithResponses
	ManifestService ManifestService
	DB              *firestore.Client
}

func NewService(apiKey string, firestore *firestore.Client, manifestService ManifestService) Service {
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
		log.Fatal().Err(err).Msg("failed to start destiny client")
	}
	return &service{
		Client:          cli,
		ManifestService: manifestService,
		DB:              firestore,
	}
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
			Count: ptr.Of(int32(count)),
			Mode:  ptr.Of(int32(mode)),
			Page:  ptr.Of(int32(page)),
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
	manifest, err := a.ManifestService.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("manifest is not provided")
	}
	return TransformPeriodGroups(*resp.JSON200.Response.Activities, *manifest), nil
}

func (a *service) GetEnrichedActivity(ctx context.Context, characterID string, activityID string) (*EnrichedActivity, error) {
	id, err := strconv.ParseInt(activityID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid activity ID: %w", err)
	}
	resp, err := a.Client.Destiny2GetPostGameCarnageReportWithResponse(ctx, id)
	if err != nil {
		slog.With(
			"error",
			err.Error(),
			"activity id",
			activityID,
		).Error("Failed to get post game carnage report")
		return nil, err
	}
	data := resp.JSON200.PostGameCarnageReportData
	if data.Entries == nil || data.ActivityDetails == nil {
		slog.With("activity id", activityID).Error("No data found for activity")
		return nil, nil
	}

	manifest, err := a.ManifestService.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("manifest is not provided")
	}
	var (
		performance   *api.InstancePerformance
		personalStats *map[string]bungie.HistoricalStatsValue
	)
	for _, entry := range *data.Entries {
		if entry.CharacterId == nil {
			continue
		}
		// TODO: Only getting it for one character. Change for everyone
		if *entry.CharacterId == characterID {
			performance = CarnageEntryToInstancePerformance(&entry, manifest)
			personalStats = entry.Values
			break
		}

	}
	details := TransformHistoricActivity(data.ActivityDetails, *manifest)
	details.Period = *data.Period
	details.PersonalValues = ToPlayerStats(personalStats)
	if details.PersonalValues != nil && performance != nil {
		performance.PlayerStats = *details.PersonalValues
	} else {
		slog.Warn("No personal stats found for activity")
	}
	result := EnrichedActivity{
		Period:          data.Period,
		Activity:        details,
		Performance:     performance,
		Teams:           TransformTeams(data.Teams),
		PostGameEntries: *data.Entries,
	}
	return &result, nil
}

func (a *service) GetItemDetails(ctx context.Context, membershipID string, membershipType int64, weaponInstanceID string) (*api.ItemProperties, error) {
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
	manifest, err := a.ManifestService.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("manifest is not provided")
	}
	return TransformItemToDetails(response.JSON200.DestinyItem, *manifest), nil
}

func (a *service) GetLoadout(ctx context.Context, membershipID int64, membershipType int64, characterID string) (api.Loadout, map[string]api.ClassStat, *time.Time, error) {
	var components []int32
	components = append(components, CharactersEquipment, Characters)
	params := &bungie.Destiny2GetProfileParams{
		Components: &components,
	}
	test, err := a.Client.Destiny2GetProfileWithResponse(ctx, int32(membershipType), membershipID, params)
	if err != nil {
		return nil, nil, nil, err
	}

	// TODO: Update snapshot to include the guns information as it is now, since mods and perks could change on the same gun.

	if test.JSON200 == nil {
		return nil, nil, nil, fmt.Errorf("no response found")
	}

	timeStamp := test.JSON200.Response.ResponseMintedTimestamp

	// TODO: Update this function to take a snapshot for all characters at once
	results := make([]bungie.ItemComponent, 0)
	if test.JSON200.Response.CharacterEquipment.Data != nil {
		equipment := *test.JSON200.Response.CharacterEquipment.Data
		for ID, equ := range equipment {
			if characterID == ID {
				if equ.Items == nil {
					continue
				}
				buckets := map[uint32]bool{
					HelmetArmor:    true,
					GauntletsArmor: true,
					ChestArmor:     true,
					LegArmor:       true,
					ClassArmor:     true,
					KineticBucket:  true,
					EnergyBucket:   true,
					PowerBucket:    true,
					SubClass:       true,
				}

				for _, item := range *equ.Items {
					if item.BucketHash == nil {
						continue
					}
					if buckets[*item.BucketHash] {
						results = append(results, item)
					}
				}

			}
		}

	}

	manifest, err := a.ManifestService.Get(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("failed to get manifest but still will generate stats")
	}
	stats := make(map[string]api.ClassStat)
	if test.JSON200.Response.Characters.Data != nil {
		characters := *test.JSON200.Response.Characters.Data
		for ID, character := range characters {
			if characterID == ID && character.Stats != nil {
				stats = generateClassStats(manifest, *character.Stats)
			}
		}

	}
	loadout := a.buildLoadout(ctx, membershipID, membershipType, results)
	return loadout, stats, timeStamp, nil
}

func (a *service) buildLoadout(ctx context.Context, membershipID int64, membershipType int64, items []bungie.ItemComponent) api.Loadout {

	loadout := make(api.Loadout)
	for _, item := range items {
		if item.ItemInstanceId == nil {
			slog.Warn("no instance id found", "membershipId", membershipID)
			continue
		}
		snap := api.ItemSnapshot{
			InstanceID: *item.ItemInstanceId,
		}
		details, err := a.GetItemDetails(ctx, strconv.FormatInt(membershipID, 10), membershipType, *item.ItemInstanceId)
		if err != nil {
			slog.With("error", err.Error()).Error("failed to get item details")
			continue
		}
		snap.Name = details.BaseInfo.Name
		snap.ItemHash = details.BaseInfo.ItemHash
		snap.ItemProperties = *details
		snap.BucketHash = &details.BaseInfo.BucketHash
		loadout[strconv.FormatInt(snap.ItemProperties.BaseInfo.BucketHash, 10)] = snap
	}

	return loadout
}

func (a *service) GetCharacters(ctx context.Context, primaryMembershipId int64, membershipType int64) ([]api.Character, error) {
	var components []int32
	components = append(components, Characters)
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
	manifest, err := a.ManifestService.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("manifest required for characters: %w", err)
	}
	results := make([]api.Character, 0)
	for _, c := range *resp.JSON200.Response.Characters.Data {
		r := TransformCharacter(&c, *manifest)
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
