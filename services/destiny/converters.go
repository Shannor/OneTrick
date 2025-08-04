package destiny

import (
	"fmt"
	"log/slog"
	"oneTrick/api"
	"oneTrick/clients/bungie"
	"oneTrick/ptr"
	"strconv"

	"github.com/rs/zerolog/log"
)

func setBaseBungieURL(value *string) string {
	if value == nil {
		return ""
	}

	return fmt.Sprintf("%s%s", "https://www.bungie.net", *value)
}
func TransformItemToDetails(
	item *bungie.DestinyItem,
	items map[string]ItemDefinition,
	damages map[string]DamageType,
	perks map[string]PerkDefinition,
	stats map[string]StatDefinition,
) *api.ItemProperties {
	if item == nil {
		return nil
	}
	result := api.ItemProperties{CharacterId: item.CharacterId}

	// Generate Base Info
	if item.Item != nil {
		result.BaseInfo = generateBaseInfo(item, items, damages)
	}

	// Generate Perks
	if item.Perks != nil && item.Perks.Data != nil {
		result.Perks = generatePerks(item, perks)
	}

	// Generate Sockets
	if item.Sockets != nil && item.Sockets.Data != nil {
		result.Sockets = generateSockets(item, items)
	}

	// Generate Stats
	if item.Stats != nil && item.Stats.Data != nil {
		result.Stats = generateStats(item, stats)
	}

	return &result
}

func TransformCharacter(item *bungie.CharacterComponent, classes map[string]ClassDefinition, races map[string]RaceDefinition, records map[string]RecordDefinition) api.Character {
	class := classes[strconv.Itoa(int(*item.ClassHash))]
	race := races[strconv.Itoa(int(*item.RaceHash))]
	title := records[strconv.Itoa(int(*item.TitleRecordHash))]
	return api.Character{
		Class:               class.DisplayProperties.Name,
		EmblemBackgroundURL: setBaseBungieURL(item.EmblemBackgroundPath),
		EmblemURL:           setBaseBungieURL(item.EmblemPath),
		Id:                  *item.CharacterId,
		Light:               int64(*item.Light),
		Race:                race.DisplayProperties.Name,
		CurrentTitle:        title.TitleInfo.TitlesByGender.Male,
	}

}
func generateBaseInfo(item *bungie.DestinyItem, items map[string]ItemDefinition, damages map[string]DamageType) api.BaseItemInfo {
	c := *item.Item.ItemComponent
	hash := strconv.Itoa(int(*c.ItemHash))
	name := items[hash].DisplayProperties.Name

	base := api.BaseItemInfo{
		BucketHash: int64(*c.BucketHash),
		InstanceId: *c.ItemInstanceId,
		ItemHash:   int64(*c.ItemHash),
		Name:       name,
	}

	if item.Instance != nil {
		instance := *item.Instance.ItemInstanceComponent
		if instance.DamageTypeHash != nil {
			hash := strconv.Itoa(int(*instance.DamageTypeHash))
			def := damages[hash]
			dc := def.Color

			base.Damage = &api.DamageInfo{
				Color: api.Color{
					Alpha: dc.Alpha,
					Blue:  dc.Blue,
					Green: dc.Green,
					Red:   dc.Red,
				},
				DamageIcon:      def.DisplayProperties.Icon,
				DamageType:      def.DisplayProperties.Name,
				TransparentIcon: def.TransparentIconPath,
			}
		}
	}
	return base
}

func generatePerks(item *bungie.DestinyItem, perks map[string]PerkDefinition) []api.Perk {
	var results []api.Perk
	for _, p := range *item.Perks.Data.Perks {
		perk, ok := perks[strconv.Itoa(int(*p.PerkHash))]
		if !ok {
			slog.Warn("Perk not found in manifest", "perkHash", strconv.Itoa(int(*p.PerkHash)))
			continue
		}
		if !perk.IsDisplayable {
			continue
		}
		results = append(results, api.Perk{
			Hash:        int64(*p.PerkHash),
			IconPath:    ptr.Of(setBaseBungieURL(p.IconPath)),
			Name:        perk.DisplayProperties.Name,
			Description: &perk.DisplayProperties.Description,
		})
	}
	return results
}

func generateSockets(item *bungie.DestinyItem, items map[string]ItemDefinition) *[]api.Socket {
	var sockets []api.Socket
	for _, s := range *item.Sockets.Data.Sockets {
		if s.PlugHash == nil {
			log.Warn().Msg("Socket has no plug hash")
			continue
		}
		socket, ok := items[strconv.Itoa(int(*s.PlugHash))]
		if !ok {
			log.Warn().Uint32("socketHash", *s.PlugHash).Msg("Socket not found in manifest")
			continue
		}

		hash := int(*s.PlugHash)
		sockets = append(sockets, api.Socket{
			IsEnabled:                 s.IsEnabled,
			IsVisible:                 s.IsVisible,
			PlugHash:                  hash,
			Name:                      socket.DisplayProperties.Name,
			Description:               socket.DisplayProperties.Description,
			ItemTypeDisplayName:       ptr.Of(socket.ItemTypeDisplayName),
			ItemTypeTieredDisplayName: ptr.Of(socket.ItemTypeAndTierDisplayName),
			Icon:                      ptr.Of(setBaseBungieURL(&socket.DisplayProperties.Icon)),
		})
	}
	return &sockets
}

func generateStats(item *bungie.DestinyItem, statDefinitions map[string]StatDefinition) api.Stats {
	stats := make(api.Stats)
	for key, s := range *item.Stats.Data.Stats {
		if s.StatHash == nil || s.Value == nil {
			slog.Warn("Missing stat hash or value for stat: ", key)
			continue
		}
		stat, ok := statDefinitions[strconv.Itoa(int(*s.StatHash))]
		if !ok {
			slog.Warn("Stat not found in manifest: ", strconv.Itoa(int(*s.StatHash)))
			continue
		}
		value := int64(*s.Value)
		stats[key] = api.GunStat{
			Description: stat.DisplayProperties.Description,
			Hash:        stat.Hash,
			Name:        stat.DisplayProperties.Name,
			Value:       value,
		}
	}
	return stats
}

func TransformD2HistoricalStatValues(stats *map[string]bungie.HistoricalStatsValue) *map[string]api.UniqueStatValue {
	if stats == nil {
		return nil
	}

	result := make(map[string]api.UniqueStatValue)
	for key, value := range *stats {
		values := transformD2StatValue(&value)
		if values == nil {
			continue
		}
		result[key] = *values
	}

	return &result
}

func transformD2StatValue(item *bungie.HistoricalStatsValue) *api.UniqueStatValue {
	if item == nil {
		return nil
	}
	if item.Basic == nil {
		slog.Warn("Missing basic value for stat")
		return nil
	}
	result := &api.UniqueStatValue{
		ActivityID: item.ActivityId,
	}
	if item.Basic != nil {
		result.Basic = api.StatsValuePair{
			DisplayValue: item.Basic.DisplayValue,
			Value:        item.Basic.Value,
		}
	}
	if item.Pga != nil {
		result.Pga = &api.StatsValuePair{
			DisplayValue: item.Pga.DisplayValue,
			Value:        item.Pga.Value,
		}
	}
	if item.Weighted != nil {
		result.Weighted = &api.StatsValuePair{
			DisplayValue: item.Weighted.DisplayValue,
			Value:        item.Weighted.Value,
		}
	}
	return result
}

func uintToInt64[T ~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64](item *T) *int64 {
	if item == nil {
		return nil
	}
	return ptr.Of(int64(*item))
}

func TransformHistoricActivity(history *bungie.HistoricalStatsActivity, activityDefinition, directorDef ActivityDefinition, modeDefinition ActivityModeDefinition) *api.ActivityHistory {
	if history == nil {
		return nil
	}
	mode := ActivityModeTypeToString((*bungie.CurrentActivityModeType)(history.Mode))
	return &api.ActivityHistory{
		ActivityHash: *uintToInt64(history.DirectorActivityHash),
		InstanceID:   *history.InstanceId,
		IsPrivate:    history.IsPrivate,
		Mode:         &mode,
		ReferenceID:  *uintToInt64(history.ReferenceId),
		Location:     activityDefinition.DisplayProperties.Name,
		Description:  activityDefinition.DisplayProperties.Description,
		Activity:     directorDef.DisplayProperties.Name,
		ImageURL:     setBaseBungieURL(&activityDefinition.PgcrImage),
		ActivityIcon: setBaseBungieURL(&modeDefinition.DisplayProperties.Icon),
	}
}

func TransformTeams(teams *[]bungie.TeamEntry) []api.Team {
	if teams == nil {
		return nil
	}
	if *teams == nil {
		return nil
	}
	var result []api.Team
	for _, team := range *teams {
		if team.TeamID == nil {
			continue
		}

		t := api.Team{
			ID:       strconv.Itoa(int(*team.TeamID)),
			TeamName: team.TeamName,
		}
		if team.Score != nil {
			t.Score = *team.Score.Basic.DisplayValue
		}
		if team.Standing != nil {
			t.Standing = *team.Standing.Basic.DisplayValue
		}
		result = append(result, t)
	}
	return result
}

func TransformPeriodGroups(period []bungie.StatsPeriodGroup, activities map[string]ActivityDefinition, modes map[string]ActivityModeDefinition) []api.ActivityHistory {
	if period == nil {
		return nil
	}
	var result []api.ActivityHistory
	for _, group := range period {
		r := TransformPeriodGroup(&group, activities, modes)
		if r == nil {
			log.Warn().Msg("period group returned nil")
			continue
		}
		result = append(result, *r)
	}
	return result
}

func TransformPeriodGroup(period *bungie.StatsPeriodGroup, activities map[string]ActivityDefinition, modes map[string]ActivityModeDefinition) *api.ActivityHistory {
	if period == nil {
		return nil
	}

	definition, ok := activities[strconv.Itoa(int(*period.ActivityDetails.ReferenceId))]
	if !ok {
		log.Warn().Msgf("Activity locale not found in manifest: %d ", period.ActivityDetails.ReferenceId)
		return nil
	}
	activity, ok := activities[strconv.Itoa(int(*period.ActivityDetails.DirectorActivityHash))]
	if !ok {
		log.Warn().Msgf("Activity Directory not found in manifest: %d", period.ActivityDetails.DirectorActivityHash)
		return nil
	}
	activityMode := modes[strconv.Itoa(activity.DirectActivityModeHash)]
	mode := ActivityModeTypeToString((*bungie.CurrentActivityModeType)(period.ActivityDetails.Mode))
	return &api.ActivityHistory{
		ActivityHash:   *uintToInt64(period.ActivityDetails.DirectorActivityHash),
		InstanceID:     *period.ActivityDetails.InstanceId,
		IsPrivate:      period.ActivityDetails.IsPrivate,
		Mode:           &mode,
		ReferenceID:    *uintToInt64(period.ActivityDetails.ReferenceId),
		Location:       definition.DisplayProperties.Name,
		Description:    definition.DisplayProperties.Description,
		Activity:       activity.DisplayProperties.Name,
		ImageURL:       setBaseBungieURL(&definition.PgcrImage),
		ActivityIcon:   setBaseBungieURL(&activityMode.DisplayProperties.Icon),
		PersonalValues: ToPlayerStats(period.Values),
		Period:         *period.Period,
	}
}

func ToPlayerStats(values *map[string]bungie.HistoricalStatsValue) *api.PlayerStats {
	if values == nil {
		return nil
	}
	personalValues := &api.PlayerStats{}
	for key, value := range *values {
		switch key {
		case "kills":
			personalValues.Kills = (*api.StatsValuePair)(value.Basic)
		case "assists":
			personalValues.Assists = (*api.StatsValuePair)(value.Basic)
		case "deaths":
			personalValues.Deaths = (*api.StatsValuePair)(value.Basic)
		case "killsDeathsRatio":
			personalValues.Kd = (*api.StatsValuePair)(value.Basic)
		case "killsDeathsAssists":
			personalValues.Kda = (*api.StatsValuePair)(value.Basic)
		case "standing":
			personalValues.Standing = (*api.StatsValuePair)(value.Basic)
		case "fireteamId":
			personalValues.FireTeamID = (*api.StatsValuePair)(value.Basic)
		case "timePlayedSeconds":
			personalValues.TimePlayed = (*api.StatsValuePair)(value.Basic)
		}
	}
	return personalValues
}

func CarnageEntryToInstancePerformance(entry *bungie.PostGameCarnageReportEntry, items map[string]ItemDefinition) *api.InstancePerformance {
	if entry == nil {
		return nil
	}
	result := &api.InstancePerformance{}

	result.Extra = BungieStatValueToUniqueStatValue(entry.Extended.Values)
	result.PlayerStats = *ToPlayerStats(entry.Values)
	result.Weapons = WeaponsToInstanceWeapons(entry.Extended.Weapons, items)
	return result
}

func BungieStatValueToUniqueStatValue(values *map[string]bungie.HistoricalStatsValue) *map[string]api.UniqueStatValue {
	if values == nil {
		return nil
	}
	result := make(map[string]api.UniqueStatValue)
	for key, value := range *values {
		result[key] = api.UniqueStatValue{
			ActivityID: value.ActivityId,
			Basic: api.StatsValuePair{
				DisplayValue: value.Basic.DisplayValue,
				Value:        value.Basic.Value,
			},
			Name: value.StatId,
		}
	}
	return &result
}

func WeaponsToInstanceWeapons(values *[]bungie.HistoricalWeaponStats, items map[string]ItemDefinition) map[string]api.WeaponInstanceMetrics {
	if values == nil {
		return nil
	}
	result := make(map[string]api.WeaponInstanceMetrics)
	for _, v := range *values {
		if v.ReferenceId == nil {
			continue
		}
		ref := int64(*v.ReferenceId)
		if ref == 0 {
			continue
		}
		r := api.WeaponInstanceMetrics{
			ReferenceID: &ref,
			Stats:       BungieStatValueToUniqueStatValue(v.Values),
		}
		def, ok := items[strconv.Itoa(int(*v.ReferenceId))]
		if ok {
			r.Display = &api.Display{
				Description: def.ItemTypeAndTierDisplayName,
				HasIcon:     def.DisplayProperties.HasIcon,
				Icon:        ptr.Of(setBaseBungieURL(&def.DisplayProperties.Icon)),
				Name:        def.DisplayProperties.Name,
			}
		}

		result[strconv.Itoa(int(*v.ReferenceId))] = r
	}
	return result
}

func ActivityModeTypeToString(modeType *bungie.CurrentActivityModeType) string {
	if modeType == nil {
		slog.Warn("Activity Mode type is nil")
		return "Unknown"
	}
	switch *modeType {
	case bungie.CurrentActivityModeTypeControl:
		return "Control"
	case bungie.CurrentActivityModeTypeIronBannerZoneControl:
		return "Iron Banner Zone Control"
	case bungie.CurrentActivityModeTypeIronBannerControl:
		return "Iron Banner Control"
	case bungie.CurrentActivityModeTypeZoneControl:
		return "Zone Control"
	case bungie.CurrentActivityModeTypeControlCompetitive:
		return "Control Competitive"
	case bungie.CurrentActivityModeTypeControlQuickplay:
		return "Control Quickplay"
	case bungie.CurrentActivityModeTypePrivateMatchesControl:
		return "Private Matches Control"
	case bungie.CurrentActivityModeTypeAllDoubles:
		return "Doubles"
	case bungie.CurrentActivityModeTypeAllPvE:
		return "PvE"
	case bungie.CurrentActivityModeTypeAllPvP:
		return "PvP"
	case bungie.CurrentActivityModeTypeClash:
		return "Clash"
	case bungie.CurrentActivityModeTypeClashQuickplay:
		return "Clash Quickplay"
	case bungie.CurrentActivityModeTypeClashCompetitive:
		return "Clash Competitive"
	case bungie.CurrentActivityModeTypeIronBannerRift:
		return "Iron Banner Rift"
	case bungie.CurrentActivityModeTypeRift:
		return "Rift"
	case bungie.CurrentActivityModeTypeIronBannerClash:
		return "Iron Banner Clash"
	case bungie.CurrentActivityModeTypeIronBannerSupremacy:
		return "Iron Banner Supremacy"
	case bungie.CurrentActivityModeTypePrivateMatchesSurvival:
		return "Private Matches Survival"
	case bungie.CurrentActivityModeTypeTrialsSurvival:
		return "Trials Survival"
	case bungie.CurrentActivityModeTypeTrialsCountdown:
		return "Trials Countdown"
	case bungie.CurrentActivityModeTypeRaid:
		return "Raid"
	case bungie.CurrentActivityModeTypeNightfall:
		return "Nightfall"
	case bungie.CurrentActivityModeTypeGambit:
		return "Gambit"
	case bungie.CurrentActivityModeTypeIronBanner:
		return "Iron Banner"
	case bungie.CurrentActivityModeTypeTrialsOfOsiris:
		return "Trials of Osiris"
	case bungie.CurrentActivityModeTypeSurvival:
		return "Survival"
	default:
		return "Unknown"
	}
}

func TransformUserSearchDetail(detail bungie.UserSearchDetail) *api.SearchUserResult {
	if detail.BungieNetMembershipId == nil {
		return nil
	}
	return &api.SearchUserResult{
		BungieMembershipID: *detail.BungieNetMembershipId,
		NameCode:           strconv.Itoa(int(*detail.BungieGlobalDisplayNameCode)),
		DisplayName:        *detail.BungieGlobalDisplayName,
		Memberships:        TransformDestinyMemberships(detail.DestinyMemberships),
	}
}

func TransformDestinyMemberships(memberships *[]bungie.UserUserInfoCard) []api.DestinyMembership {
	if memberships == nil {
		return nil
	}
	results := make([]api.DestinyMembership, 0)
	for _, info := range *memberships {
		i := api.DestinyMembership{
			DisplayName:    *info.DisplayName,
			MembershipID:   *info.MembershipId,
			MembershipType: generateSourceSystem(info.MembershipType),
			IconPath:       ptr.Of(setBaseBungieURL(info.IconPath)),
		}
		results = append(results, i)
	}
	return results
}

func generateSourceSystem(membershipType *int32) api.SourceSystem {
	if membershipType == nil {
		return api.SystemUnknown
	}
	switch *membershipType {
	case 2:
		return api.SystemPlayStation
	case 3:
		return api.SystemSteam
	case 4:
		return api.SystemXbox
	case 5:
		return api.SystemStadia
	default:
		return api.SystemUnknown

	}
}
func generateClassStats(statDefinitions map[string]StatDefinition, stats map[string]int32) map[string]api.ClassStat {
	if statDefinitions == nil {
		return nil
	}
	results := make(map[string]api.ClassStat)
	for key, value := range stats {
		info, ok := statDefinitions[key]
		if !ok {
			slog.Warn("Missing stat", "statKey", key)
			continue
		}
		i := api.ClassStat{
			Name:            info.DisplayProperties.Name,
			Icon:            info.DisplayProperties.Name,
			HasIcon:         info.DisplayProperties.HasIcon,
			Description:     info.DisplayProperties.Description,
			StatCategory:    info.StatCategory,
			AggregationType: info.AggregationType,
			Value:           value,
		}
		results[key] = i
	}
	return results
}
