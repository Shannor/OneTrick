package destiny

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"oneTrick/api"
	"oneTrick/clients/bungie"
	"oneTrick/ptr"
	"oneTrick/set"
	"slices"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/rs/zerolog/log"
)

type Service interface {
	GetLoadout(ctx context.Context, membershipID int64, membershipType int64, characterID string) (api.Loadout, map[string]api.ClassStat, *time.Time, error)
	GetCharacters(ctx context.Context, primaryMembershipId int64, membershipType int64) ([]api.Character, error)
	GetItemDetails(ctx context.Context, membershipID int64, membershipType int64, weaponInstanceID string) (*bungie.DestinyItem, error)
	GetPartyMembers(ctx context.Context, primaryMembershipId int64, membershipType int64) ([]bungie.PartyMember, error)
	GetQuickPlayActivity(ctx context.Context, membershipID string, membershipType int64, characterID string, count int64, page int64) ([]api.ActivityHistory, error)
	GetAllPVPActivity(ctx context.Context, membershipID string, membershipType int64, characterID string, count int64, page int64) ([]api.ActivityHistory, error)
	GetCompetitiveActivity(ctx context.Context, membershipID string, membershipType int64, characterID string, count int64, page int64) ([]api.ActivityHistory, error)
	GetIronBannerActivity(ctx context.Context, membershipID string, membershipType int64, characterID string, count int64, page int64) ([]api.ActivityHistory, error)
	GetActivity(ctx context.Context, activityID string) (*bungie.PostGameCarnageReportData, []api.Team, error)
	GetPerformances(ctx context.Context, activityID string, characterIDs []string) (map[string]api.InstancePerformance, error)
	GetEnrichedActivity(ctx context.Context, activityID string, characterIDs []string) (*EnrichedActivity, error)
	Search(ctx context.Context, prefix string, page int32) ([]api.SearchUserResult, bool, error)
	GetActivityModesFromGameMode(gameMode *api.GameMode) ([]string, error)
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

	activities, err := a.ManifestService.GetActivities(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot enhance activities: %v", err)
	}
	modes, err := a.ManifestService.GetActivityModes(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot enhance activities: %v", err)
	}

	return TransformPeriodGroups(*resp.JSON200.Response.Activities, activities, modes), nil
}

func (a *service) GetPerformances(ctx context.Context, activityID string, characterIDs []string) (map[string]api.InstancePerformance, error) {
	id, err := strconv.ParseInt(activityID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid activity ID: %w", err)
	}

	l := log.With().Str("activityId", activityID).Logger()

	resp, err := a.Client.Destiny2GetPostGameCarnageReportWithResponse(ctx, id)
	if err != nil {
		l.Error().Err(err).Msg("Failed to get post game carnage report")
		return nil, err
	}
	data := resp.JSON200.PostGameCarnageReportData
	if data.Entries == nil || data.ActivityDetails == nil {
		l.Error().Msg("No data found for activity")
		return nil, fmt.Errorf("nil data response")
	}

	performances := make(map[string]api.InstancePerformance)
	characterSet := set.FromSlice(characterIDs)
	items := buildItemsSet(ctx, data, characterSet, a)
	for _, entry := range *data.Entries {
		if entry.CharacterId == nil {
			continue
		}
		if characterSet.Contains(*entry.CharacterId) {
			p := CarnageEntryToInstancePerformance(&entry, items)
			if p == nil {
				continue
			}
			performances[*entry.CharacterId] = *p
		}
	}

	return performances, nil
}

func (a *service) GetEnrichedActivity(ctx context.Context, activityID string, characterIDs []string) (*EnrichedActivity, error) {
	id, err := strconv.ParseInt(activityID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid activity ID: %w", err)
	}

	l := log.With().Str("activityId", activityID).Logger()

	resp, err := a.Client.Destiny2GetPostGameCarnageReportWithResponse(ctx, id)
	if err != nil {
		l.Error().Err(err).Msg("Failed to get post game carnage report")
		return nil, err
	}
	data := resp.JSON200.PostGameCarnageReportData
	if data.Entries == nil || data.ActivityDetails == nil {
		l.Error().Msg("No data found for activity")
		return nil, fmt.Errorf("nil data response")
	}

	performances := make(map[string]api.InstancePerformance)
	characterSet := set.FromSlice(characterIDs)
	items := buildItemsSet(ctx, data, characterSet, a)
	for _, entry := range *data.Entries {
		if entry.CharacterId == nil {
			continue
		}
		if characterSet.Contains(*entry.CharacterId) {
			p := CarnageEntryToInstancePerformance(&entry, items)
			if p == nil {
				continue
			}
			performances[*entry.CharacterId] = *p
		}
	}
	activityDef, err := a.ManifestService.GetActivity(ctx, int64(*data.ActivityDetails.ReferenceId))
	if err != nil {
		return nil, err
	}
	directoryDef, err := a.ManifestService.GetActivity(ctx, int64(*data.ActivityDetails.DirectorActivityHash))
	if err != nil {
		return nil, err
	}
	mode, err := a.ManifestService.GetActivityMode(ctx, int64(activityDef.DirectActivityModeHash))
	if err != nil {
		return nil, err
	}

	details := TransformHistoricActivity(data.ActivityDetails, *activityDef, *directoryDef, *mode)
	details.Period = *data.Period
	result := EnrichedActivity{
		Period:          data.Period,
		Activity:        details,
		Performances:    performances,
		Teams:           TransformTeams(data.Teams),
		PostGameEntries: *data.Entries,
	}
	return &result, nil
}

func buildItemsSet(ctx context.Context, data *bungie.PostGameCarnageReportData, characterSet *set.Set[string], a *service) map[string]ItemDefinition {
	items := make(map[string]ItemDefinition)
	for _, entry := range *data.Entries {
		if entry.CharacterId == nil {
			continue
		}
		if characterSet.Contains(*entry.CharacterId) {
			if entry.Extended.Weapons != nil {
				for _, stats := range *entry.Extended.Weapons {
					if stats.ReferenceId != nil {
						id := *stats.ReferenceId
						item, err := a.ManifestService.GetItem(ctx, int64(id))
						if err != nil {
							continue
						}
						if item != nil {
							items[strconv.FormatInt(item.Hash, 10)] = *item
						}
					}
				}
			}
		}
	}
	return items
}

func (a *service) GetItemDetails(ctx context.Context, membershipID int64, membershipType int64, weaponInstanceID string) (*bungie.DestinyItem, error) {
	components := []int32{ItemPerksCode, ItemStatsCode, ItemSocketsCode, ItemCommonDataCode, ItemInstanceCode}
	weaponInstanceIDInt64, err := strconv.ParseInt(weaponInstanceID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to convert membershipId to int64: %v", err)
	}
	params := bungie.Destiny2GetItemParams{Components: &components}
	response, err := a.Client.Destiny2GetItemWithResponse(
		ctx,
		int32(membershipType),
		membershipID,
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

	return response.JSON200.DestinyItem, nil
}

func (a *service) GetLoadout(ctx context.Context, membershipID int64, membershipType int64, characterID string) (api.Loadout, map[string]api.ClassStat, *time.Time, error) {
	var components []int32
	components = append(components, CharactersEquipment, CharactersCode)
	params := &bungie.Destiny2GetProfileParams{
		Components: &components,
	}
	test, err := a.Client.Destiny2GetProfileWithResponse(ctx, int32(membershipType), membershipID, params)
	if err != nil {
		return nil, nil, nil, err
	}

	// TODO: Migrate snapshot to include the guns information as it is now, since mods and perks could change on the same gun.

	if test.JSON200 == nil {
		return nil, nil, nil, fmt.Errorf("no response found")
	}

	timeStamp := test.JSON200.Response.ResponseMintedTimestamp

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

	statDefinitions, err := a.ManifestService.GetStats(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("failed to get statDefinitions but still will generate stats")
	}
	stats := make(map[string]api.ClassStat)
	if test.JSON200.Response.Characters.Data != nil {
		characters := *test.JSON200.Response.Characters.Data
		for ID, character := range characters {
			if characterID == ID && character.Stats != nil {
				stats = generateClassStats(statDefinitions, *character.Stats)
			}
		}

	}
	loadout, err := a.buildLoadout(ctx, membershipID, membershipType, results, statDefinitions)
	if err != nil {
		log.Error().Err(err).Msg("couldn't build the loadout")
		return nil, nil, nil, err
	}
	return loadout, stats, timeStamp, nil
}

func (a *service) buildLoadout(ctx context.Context, membershipID int64, membershipType int64, items []bungie.ItemComponent, stats map[string]StatDefinition) (api.Loadout, error) {

	// TODO: Could convert this to build items by ID requests
	d2Items, err := a.ManifestService.GetItems(ctx)
	if err != nil {
		return nil, err
	}
	damageTypes, err := a.ManifestService.GetDamageTypes(ctx)
	if err != nil {
		return nil, err
	}

	// TODO: Could convert this to build items by ID requests
	perks, err := a.ManifestService.GetPerks(ctx)
	if err != nil {
		return nil, err
	}

	loadout := make(api.Loadout)
	for _, item := range items {
		if item.ItemInstanceId == nil {
			slog.Warn("no instance id found", "membershipId", membershipID)
			continue
		}
		snap := api.ItemSnapshot{
			InstanceID: *item.ItemInstanceId,
		}
		d, err := a.GetItemDetails(ctx, membershipID, membershipType, *item.ItemInstanceId)
		if err != nil {
			slog.With("error", err.Error()).Error("failed to get item details")
			continue
		}
		details := TransformItemToDetails(d, d2Items, damageTypes, perks, stats)
		snap.Name = details.BaseInfo.Name
		snap.ItemHash = details.BaseInfo.ItemHash
		snap.ItemProperties = *details
		snap.BucketHash = &details.BaseInfo.BucketHash
		loadout[strconv.FormatInt(snap.ItemProperties.BaseInfo.BucketHash, 10)] = snap
	}

	return loadout, nil
}

func (a *service) GetCharacters(ctx context.Context, primaryMembershipId int64, membershipType int64) ([]api.Character, error) {
	var components []int32
	components = append(components, CharactersCode)
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
	classes, err := a.ManifestService.GetClasses(ctx)
	if err != nil {
		return nil, fmt.Errorf("manifest required for characters: %v", err)
	}
	races, err := a.ManifestService.GetRaces(ctx)
	if err != nil {
		return nil, err
	}
	data := *resp.JSON200.Response.Characters.Data
	records := make(map[string]RecordDefinition)
	for _, d := range data {
		if d.TitleRecordHash != nil {
			record, err := a.ManifestService.GetRecord(ctx, int64(*d.TitleRecordHash))
			if err != nil {
				log.Warn().Msg("missing id for title record")
				continue
			}
			if record == nil {
				log.Warn().Uint32("Hash", *d.TitleRecordHash).Msg("record was nil")
				continue
			}
			records[strconv.Itoa(record.Hash)] = *record
		}
	}
	results := make([]api.Character, 0)
	for _, c := range data {
		r := TransformCharacter(&c, classes, races, records)
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

func (a *service) GetPartyMembers(ctx context.Context, primaryMembershipId int64, membershipType int64) ([]bungie.PartyMember, error) {
	var components []int32
	components = append(components, TransitoryCode)
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

	if resp.JSON200.Response.ProfileTransitoryData.Data == nil {
		return nil, nil
	}
	if resp.JSON200.Response.ProfileTransitoryData.Data.PartyMembers == nil {
		return nil, nil
	}
	return *resp.JSON200.Response.ProfileTransitoryData.Data.PartyMembers, nil
}

func (a *service) Search(ctx context.Context, prefix string, page int32) ([]api.SearchUserResult, bool, error) {
	body := bungie.UserSearchPrefixRequest{DisplayNamePrefix: ptr.Of(prefix)}
	resp, err := a.Client.UserSearchByGlobalNamePostWithResponse(ctx, page, body)
	if err != nil {
		return nil, false, err
	}
	if resp.JSON200 == nil {
		return nil, false, fmt.Errorf("no response")
	}
	if resp.JSON200.SearchResponse == nil {
		return nil, false, fmt.Errorf("empty response")
	}
	results := make([]api.SearchUserResult, 0)
	for _, data := range *resp.JSON200.SearchResponse.SearchResults {
		d := TransformUserSearchDetail(data)
		if d == nil {
			continue
		}
		results = append(results, *d)
	}

	return results, *resp.JSON200.SearchResponse.HasMore, nil
}

func (a *service) GetActivity(ctx context.Context, activityID string) (*bungie.PostGameCarnageReportData, []api.Team, error) {
	id, err := strconv.ParseInt(activityID, 10, 64)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid activity ID: %w", err)
	}

	l := log.With().Str("activityId", activityID).Logger()

	resp, err := a.Client.Destiny2GetPostGameCarnageReportWithResponse(ctx, id)
	if err != nil {
		l.Error().Err(err).Msg("Failed to get post game carnage report")
		return nil, nil, err
	}
	data := resp.JSON200.PostGameCarnageReportData
	if data.Entries == nil || data.ActivityDetails == nil {
		l.Error().Msg("No data found for activity")
		return nil, nil, fmt.Errorf("nil data response")
	}

	return data, TransformTeams(data.Teams), nil
}

func (a *service) GetActivityModesFromGameMode(gameMode *api.GameMode) ([]string, error) {
	if gameMode == nil {
		return nil, nil
	}
	return gameModeToActivityModes(*gameMode)
}
