package destiny

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"oneTrick/api"
	"oneTrick/clients/bungie"
	"oneTrick/set"
	"slices"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	clog "github.com/Shannor/canonical-logger"
)

type Service interface {
	GetLoadout(ctx context.Context, membershipID int64, membershipType int64, characterID string) (api.Loadout, map[string]api.ClassStat, []api.ArmorSetBonus, *time.Time, error)
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
	GetCompetitiveRank(ctx context.Context, membershipID int64, membershipType int64, characterID string) (*api.CompRankSnapshot, error)
	Search(ctx context.Context, prefix string, page int32) ([]api.SearchUserResult, bool, error)
}

type service struct {
	Client          bungie.ClientWithResponsesInterface
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
		slog.Error("failed to start destiny client", "error", err)
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
	clog.WithGroupFields(ctx, "destinyService.getActivityHistory", clog.Fields{
		"membershipId":   membershipID,
		"membershipType": membershipType,
		"characterId":    characterID,
		"count":          count,
		"mode":           mode,
		"page":           page,
	})
	resp, err := a.Client.Destiny2GetActivityHistoryWithResponse(
		ctx,
		int32(membershipType),
		mID,
		cID,
		&bungie.Destiny2GetActivityHistoryParams{
			Count: new(int32(count)),
			Mode:  new(int32(mode)),
			Page:  new(int32(page)),
		},
	)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		clog.WithGroupField(ctx, "destinyService.getActivityHistory", "statusCode", resp.StatusCode())
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

	periods := *resp.JSON200.Response.Activities
	activityIDs := make([]int64, 0, len(periods)*2)
	for _, p := range periods {
		if p.ActivityDetails == nil {
			continue
		}
		if p.ActivityDetails.ReferenceId != nil {
			activityIDs = append(activityIDs, int64(*p.ActivityDetails.ReferenceId))
		}
		if p.ActivityDetails.DirectorActivityHash != nil {
			activityIDs = append(activityIDs, int64(*p.ActivityDetails.DirectorActivityHash))
		}
	}

	activities, err := a.ManifestService.GetActivitiesByIDs(ctx, activityIDs)
	if err != nil {
		return nil, fmt.Errorf("cannot enhance activities: %v", err)
	}

	modeIDs := make([]int64, 0, len(activities))
	for _, act := range activities {
		if act.DirectActivityModeHash != 0 {
			modeIDs = append(modeIDs, int64(act.DirectActivityModeHash))
		}
	}

	modes, err := a.ManifestService.GetActivityModesByIDs(ctx, modeIDs)
	if err != nil {
		return nil, fmt.Errorf("cannot enhance activities: %v", err)
	}

	needsModeTypeFallback := false
	for _, p := range periods {
		if p.ActivityDetails == nil || p.ActivityDetails.DirectorActivityHash == nil || p.ActivityDetails.Mode == nil || *p.ActivityDetails.Mode == 0 {
			continue
		}
		act, ok := activities[strconv.Itoa(int(*p.ActivityDetails.DirectorActivityHash))]
		if !ok {
			continue
		}
		if _, found := modes[strconv.Itoa(act.DirectActivityModeHash)]; !found {
			needsModeTypeFallback = true
			break
		}
	}
	if needsModeTypeFallback {
		if allModes, modeErr := a.ManifestService.GetActivityModes(ctx); modeErr == nil {
			modes = allModes
		}
	}

	history := TransformPeriodGroups(periods, activities, modes)
	clog.WithGroupField(ctx, "destinyService.getActivityHistory", "returnedCount", len(history))
	return history, nil
}

func (a *service) GetPerformances(ctx context.Context, activityID string, characterIDs []string) (map[string]api.InstancePerformance, error) {
	id, err := strconv.ParseInt(activityID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid activity ID: %w", err)
	}

	clog.WithGroupFields(ctx, "destinyService.getPerformances", clog.Fields{
		"activityId":     activityID,
		"characterCount": len(characterIDs),
	})

	resp, err := a.Client.Destiny2GetPostGameCarnageReportWithResponse(ctx, id)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil || resp.JSON200.PostGameCarnageReportData == nil {
		return nil, fmt.Errorf("nil data response")
	}
	data := resp.JSON200.PostGameCarnageReportData

	performances := a.extractPerformances(ctx, data, characterIDs)

	clog.WithGroupField(ctx, "destinyService.getPerformances", "returnedCount", len(performances))
	return performances, nil
}

func (a *service) extractPerformances(ctx context.Context, data *bungie.PostGameCarnageReportData, characterIDs []string) map[string]api.InstancePerformance {
	if data == nil || data.Entries == nil {
		return make(map[string]api.InstancePerformance)
	}

	var characterSet *set.Set[string]
	capHint := len(*data.Entries)
	if len(characterIDs) > 0 {
		characterSet = set.FromSlice(characterIDs)
		capHint = min(len(characterIDs), len(*data.Entries))
	}

	performances := make(map[string]api.InstancePerformance, capHint)
	items := buildItemsSet(ctx, data, characterSet, a)
	for _, entry := range *data.Entries {
		if entry.CharacterId == nil {
			continue
		}
		if characterSet == nil || characterSet.Contains(*entry.CharacterId) {
			p := CarnageEntryToInstancePerformance(&entry, items)
			if p == nil {
				continue
			}
			performances[*entry.CharacterId] = *p
		}
	}
	return performances
}

func (a *service) GetEnrichedActivity(ctx context.Context, activityID string, characterIDs []string) (*EnrichedActivity, error) {
	id, err := strconv.ParseInt(activityID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid activity ID: %w", err)
	}

	clog.WithGroupFields(ctx, "destinyService.getEnrichedActivity", clog.Fields{
		"activityId":     activityID,
		"characterCount": len(characterIDs),
	})

	resp, err := a.Client.Destiny2GetPostGameCarnageReportWithResponse(ctx, id)
	if err != nil {
		return nil, err
	}
	if resp.JSON200 == nil || resp.JSON200.PostGameCarnageReportData == nil {
		return nil, fmt.Errorf("nil data response")
	}
	data := resp.JSON200.PostGameCarnageReportData

	performances := a.extractPerformances(ctx, data, characterIDs)

	if data.ActivityDetails == nil || data.ActivityDetails.ReferenceId == nil || data.ActivityDetails.DirectorActivityHash == nil {
		return nil, fmt.Errorf("activity details missing")
	}

	activityDef, err := a.ManifestService.GetActivity(ctx, int64(*data.ActivityDetails.ReferenceId))
	if err != nil || activityDef == nil {
		return nil, fmt.Errorf("failed to load activity manifest: %v", err)
	}
	directoryDef, err := a.ManifestService.GetActivity(ctx, int64(*data.ActivityDetails.DirectorActivityHash))
	if err != nil || directoryDef == nil {
		return nil, fmt.Errorf("failed to load directory activity manifest: %v", err)
	}
	mode, err := a.ManifestService.GetActivityMode(ctx, int64(activityDef.DirectActivityModeHash))
	if err != nil || mode == nil {
		return nil, fmt.Errorf("failed to load activity mode manifest: %v", err)
	}

	details := TransformHistoricActivity(data.ActivityDetails, *activityDef, *directoryDef, *mode)
	if details != nil && data.Period != nil {
		details.Period = *data.Period
	}
	entries := make([]bungie.PostGameCarnageReportEntry, 0)
	if data.Entries != nil {
		entries = *data.Entries
	}
	result := EnrichedActivity{
		Period:          data.Period,
		Activity:        details,
		Performances:    performances,
		Teams:           TransformTeams(data.Teams),
		PostGameEntries: entries,
	}
	clog.WithGroupField(ctx, "destinyService.getEnrichedActivity", "entryCount", len(entries))
	return &result, nil
}

func buildItemsSet(ctx context.Context, data *bungie.PostGameCarnageReportData, characterSet *set.Set[string], a *service) map[string]ItemDefinition {
	if data == nil || data.Entries == nil {
		return make(map[string]ItemDefinition)
	}
	var itemIDs []int64
	for _, entry := range *data.Entries {
		if entry.CharacterId == nil || (characterSet != nil && !characterSet.Contains(*entry.CharacterId)) {
			continue
		}
		if entry.Extended != nil && entry.Extended.Weapons != nil {
			for _, stats := range *entry.Extended.Weapons {
				if stats.ReferenceId != nil {
					itemIDs = append(itemIDs, int64(*stats.ReferenceId))
				}
			}
		}
	}
	if len(itemIDs) == 0 {
		return make(map[string]ItemDefinition)
	}
	items, err := a.ManifestService.GetItemsByIDs(ctx, itemIDs)
	if err != nil {
		clog.WithGroupFields(ctx, "destinyService.buildItemsSet", clog.Fields{
			"warning":    "failed to fetch weapon item definitions",
			"fetchError": err.Error(),
		})
		return make(map[string]ItemDefinition)
	}
	return items
}

func (a *service) GetItemDetails(ctx context.Context, membershipID int64, membershipType int64, weaponInstanceID string) (*bungie.DestinyItem, error) {
	clog.WithGroupFields(ctx, "destinyService.getItemDetails", clog.Fields{
		"membershipId":     membershipID,
		"membershipType":   membershipType,
		"weaponInstanceId": weaponInstanceID,
	})
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
		return nil, err
	}
	if response.JSON200 == nil || response.JSON200.DestinyItem == nil {
		return nil, nil
	}

	return response.JSON200.DestinyItem, nil
}

func (a *service) GetLoadout(ctx context.Context, membershipID int64, membershipType int64, characterID string) (api.Loadout, map[string]api.ClassStat, []api.ArmorSetBonus, *time.Time, error) {
	var components []int32
	components = append(components, CharactersEquipment, CharactersCode)
	params := &bungie.Destiny2GetProfileParams{
		Components: &components,
	}
	test, err := a.Client.Destiny2GetProfileWithResponse(ctx, int32(membershipType), membershipID, params)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	// TODO: Migrate snapshot to include the guns information as it is now, since mods and perks could change on the same gun.

	if test.JSON200 == nil || test.JSON200.Response == nil {
		return nil, nil, nil, nil, fmt.Errorf("no response found")
	}

	timeStamp := test.JSON200.Response.ResponseMintedTimestamp

	results := make([]bungie.ItemComponent, 0)
	if test.JSON200.Response.CharacterEquipment != nil && test.JSON200.Response.CharacterEquipment.Data != nil {
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

	clog.WithGroupFields(ctx, "destinyService.getLoadout", clog.Fields{
		"membershipId":   membershipID,
		"membershipType": membershipType,
		"characterId":    characterID,
	})
	statDefinitions, err := a.ManifestService.GetStats(ctx)
	if err != nil {
		clog.WithGroupField(ctx, "destinyService.getLoadout", "warning", "failed to get stat definitions")
	}
	stats := make(map[string]api.ClassStat)
	if test.JSON200.Response.Characters != nil && test.JSON200.Response.Characters.Data != nil {
		characters := *test.JSON200.Response.Characters.Data
		for ID, character := range characters {
			if characterID == ID && character.Stats != nil {
				stats = generateClassStats(statDefinitions, *character.Stats)
			}
		}

	}
	loadout, armorSetBonuses, err := a.buildLoadout(ctx, membershipID, membershipType, results, statDefinitions)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	clog.WithGroupFields(ctx, "destinyService.getLoadout", clog.Fields{
		"itemCount":          len(loadout),
		"armorSetBonusCount": len(armorSetBonuses),
	})
	return loadout, stats, armorSetBonuses, timeStamp, nil
}

func (a *service) GetCharacters(ctx context.Context, primaryMembershipId int64, membershipType int64) ([]api.Character, error) {
	clog.WithGroupFields(ctx, "destinyService.getCharacters", clog.Fields{
		"primaryMembershipId": primaryMembershipId,
		"membershipType":      membershipType,
	})
	var components []int32
	components = append(components, CharactersCode)
	params := &bungie.Destiny2GetProfileParams{
		Components: &components,
	}
	resp, err := a.Client.Destiny2GetProfileWithResponse(ctx, int32(membershipType), primaryMembershipId, params)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		if resp.StatusCode() == http.StatusServiceUnavailable {
			return nil, ErrDestinyServerDown
		}
		clog.WithGroupField(ctx, "destinyService.getCharacters", "statusCode", resp.StatusCode())
		return nil, fmt.Errorf("failed to get characters")
	}

	if resp.JSON200 == nil || resp.JSON200.Response == nil || resp.JSON200.Response.Characters == nil || resp.JSON200.Response.Characters.Data == nil {
		clog.WithGroupField(ctx, "destinyService.getCharacters", "statusCode", resp.StatusCode())
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
			if err != nil || record == nil {
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
	clog.WithGroupField(ctx, "destinyService.getCharacters", "characterCount", len(results))
	return results, nil
}

func (a *service) GetPartyMembers(ctx context.Context, primaryMembershipId int64, membershipType int64) ([]bungie.PartyMember, error) {
	clog.WithGroupFields(ctx, "destinyService.getPartyMembers", clog.Fields{
		"primaryMembershipId": primaryMembershipId,
		"membershipType":      membershipType,
	})
	var components []int32
	components = append(components, TransitoryCode)
	params := &bungie.Destiny2GetProfileParams{
		Components: &components,
	}
	resp, err := a.Client.Destiny2GetProfileWithResponse(ctx, int32(membershipType), primaryMembershipId, params)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		if resp.StatusCode() == http.StatusServiceUnavailable {
			return nil, ErrDestinyServerDown
		}
		clog.WithGroupField(ctx, "destinyService.getPartyMembers", "statusCode", resp.StatusCode())
		return nil, fmt.Errorf("failed to get characters")
	}

	if resp.JSON200 == nil || resp.JSON200.Response == nil {
		return nil, fmt.Errorf("no response found")
	}

	if resp.JSON200.Response.ProfileTransitoryData == nil || resp.JSON200.Response.ProfileTransitoryData.Data == nil || resp.JSON200.Response.ProfileTransitoryData.Data.PartyMembers == nil {
		clog.WithGroupField(ctx, "destinyService.getPartyMembers", "partySize", 0)
		return nil, nil
	}
	members := *resp.JSON200.Response.ProfileTransitoryData.Data.PartyMembers
	clog.WithGroupField(ctx, "destinyService.getPartyMembers", "partySize", len(members))
	return members, nil
}

func (a *service) GetActivity(ctx context.Context, activityID string) (*bungie.PostGameCarnageReportData, []api.Team, error) {
	id, err := strconv.ParseInt(activityID, 10, 64)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid activity ID: %w", err)
	}

	clog.WithGroupField(ctx, "destinyService.getActivity", "activityId", activityID)

	resp, err := a.Client.Destiny2GetPostGameCarnageReportWithResponse(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if resp.JSON200 == nil || resp.JSON200.PostGameCarnageReportData == nil {
		return nil, nil, fmt.Errorf("nil data response")
	}
	data := resp.JSON200.PostGameCarnageReportData
	teams := TransformTeams(data.Teams)
	clog.WithGroupField(ctx, "destinyService.getActivity", "teamCount", len(teams))

	return data, teams, nil
}
