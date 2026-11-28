package session

import (
	"oneTrick/api"
	"oneTrick/core/domain"
	"oneTrick/ptr"
)

func toAPICompRankSnapshot(snap domain.CompRankSnapshot) *api.CompRankSnapshot {
	return &api.CompRankSnapshot{
		Points:       snap.Points,
		StepIndex:    snap.StepIndex,
		DivisionName: ptr.NonEmpty(snap.DivisionName),
		Icon:         ptr.NonEmpty(snap.Icon),
		RecordedAt:   snap.RecordedAt,
	}
}

func toAPIActivityHistories(infos []domain.ActivityInfo) []api.ActivityHistory {
	out := make([]api.ActivityHistory, 0, len(infos))
	for _, info := range infos {
		item := api.ActivityHistory{
			ReferenceID:  info.ReferenceID,
			ActivityHash: info.ActivityHash,
			InstanceID:   info.InstanceID,
			Location:     info.Location,
			Description:  info.Description,
			Activity:     info.Activity,
			ImageURL:     info.ImageURL,
			ActivityIcon: info.ActivityIcon,
			Period:       info.Period,
			Mode:         ptr.NonEmpty(info.Mode),
		}
		if info.HasIsPrivate {
			item.IsPrivate = new(info.IsPrivate)
		}
		if info.EstimatedGameMode != "" {
			gm := api.GameMode(info.EstimatedGameMode)
			item.EstimatedGameMode = &gm
		}
		out = append(out, item)
	}
	return out
}

func toAPIInstancePerformances(perfs map[string]domain.Performance) map[string]api.InstancePerformance {
	out := make(map[string]api.InstancePerformance, len(perfs))
	for charID, perf := range perfs {
		out[charID] = toAPIInstancePerformance(perf)
	}
	return out
}

func toAPIInstancePerformance(perf domain.Performance) api.InstancePerformance {
	weapons := make(map[string]api.WeaponInstanceMetrics, len(perf.Weapons))
	for k, w := range perf.Weapons {
		wm := api.WeaponInstanceMetrics{
			ItemTypeDisplayName: ptr.NonEmpty(w.ItemTypeDisplayName),
			Stats:               toAPIUniqueStatsMap(w.Stats),
		}
		if w.ReferenceID != 0 {
			wm.ReferenceID = new(w.ReferenceID)
		}
		if w.BucketHash != 0 {
			wm.BucketHash = new(w.BucketHash)
		}
		if w.HasDisplay {
			wm.Display = &api.Display{
				Name:        w.Display.Name,
				Description: w.Display.Description,
				Icon:        ptr.NonEmpty(w.Display.Icon),
				HasIcon:     w.Display.HasIcon,
			}
		}
		if w.Properties != nil {
			props := toAPIItemProperties(*w.Properties)
			wm.ItemProperties = &props
		}
		weapons[k] = wm
	}

	return api.InstancePerformance{
		PlayerStats: api.PlayerStats{
			Kills:      toAPIStatPairPtr(perf.PlayerStats.Kills),
			Assists:    toAPIStatPairPtr(perf.PlayerStats.Assists),
			Deaths:     toAPIStatPairPtr(perf.PlayerStats.Deaths),
			Kd:         toAPIStatPairPtr(perf.PlayerStats.KD),
			Kda:        toAPIStatPairPtr(perf.PlayerStats.KDA),
			Standing:   toAPIStatPairPtr(perf.PlayerStats.Standing),
			Team:       toAPIStatPairPtr(perf.PlayerStats.Team),
			FireTeamID: toAPIStatPairPtr(perf.PlayerStats.FireteamID),
			TimePlayed: toAPIStatPairPtr(perf.PlayerStats.TimePlayed),
		},
		Weapons: weapons,
		Extra:   toAPIUniqueStatsMap(perf.Extra),
	}
}

func toAPIStatPairPtr(pair domain.StatValuePair) *api.StatsValuePair {
	if !pair.Present {
		return nil
	}
	return &api.StatsValuePair{
		Value:        new(pair.Value),
		DisplayValue: new(pair.DisplayValue),
	}
}

func toAPIStatPairValue(pair domain.StatValuePair) api.StatsValuePair {
	if !pair.Present {
		return api.StatsValuePair{}
	}
	return api.StatsValuePair{
		Value:        new(pair.Value),
		DisplayValue: new(pair.DisplayValue),
	}
}

func toAPIUniqueStatsMap(vals map[string]domain.UniqueStatValue) *map[string]api.UniqueStatValue {
	if vals == nil {
		return nil
	}
	out := make(map[string]api.UniqueStatValue, len(vals))
	for k, v := range vals {
		out[k] = api.UniqueStatValue{
			Name:       ptr.NonEmpty(v.Name),
			Basic:      toAPIStatPairValue(v.Basic),
			Pga:        toAPIStatPairPtr(v.PGA),
			Weighted:   toAPIStatPairPtr(v.Weighted),
			ActivityID: v.ActivityID,
		}
	}
	return &out
}

func toAPIItemProperties(p domain.ItemProperties) api.ItemProperties {
	perks := make([]api.Perk, 0, len(p.Perks))
	for _, pk := range p.Perks {
		perks = append(perks, api.Perk{
			Name:        pk.Name,
			Description: ptr.NonEmpty(pk.Description),
			Hash:        pk.Hash,
			IconPath:    ptr.NonEmpty(pk.IconPath),
		})
	}
	stats := make(api.Stats, len(p.Stats))
	for k, st := range p.Stats {
		stats[k] = api.GunStat{
			Hash:        st.Hash,
			Value:       st.Value,
			Name:        st.Name,
			Description: st.Description,
		}
	}
	out := api.ItemProperties{
		CharacterId: ptr.NonEmpty(p.CharacterID),
		BaseInfo:    toAPIBaseItemInfo(p.BaseInfo),
		Perks:       perks,
		Stats:       stats,
	}
	if p.HasSockets {
		sockets := make([]api.Socket, 0, len(p.Sockets))
		for _, sk := range p.Sockets {
			sockets = append(sockets, api.Socket{
				PlugHash:                  sk.PlugHash,
				IsEnabled:                 sk.IsEnabled,
				IsVisible:                 sk.IsVisible,
				Name:                      sk.Name,
				Description:               sk.Description,
				Icon:                      ptr.NonEmpty(sk.Icon),
				ItemTypeDisplayName:       ptr.NonEmpty(sk.ItemTypeDisplayName),
				ItemTypeTieredDisplayName: ptr.NonEmpty(sk.ItemTypeTieredDisplayName),
			})
		}
		out.Sockets = &sockets
	}
	return out
}

func toAPIBaseItemInfo(b domain.BaseItemInfo) api.BaseItemInfo {
	out := api.BaseItemInfo{
		Name:                       b.Name,
		Icon:                       ptr.NonEmpty(b.Icon),
		ItemHash:                   b.ItemHash,
		InstanceId:                 b.InstanceID,
		BucketHash:                 b.BucketHash,
		ItemTypeAndTierDisplayName: b.ItemTypeAndTierDisplayName,
		ItemTypeDisplayName:        b.ItemTypeDisplayName,
		TierTypeName:               b.TierTypeName,
		TierType:                   b.TierType,
		EquipableItemSetHash:       b.EquipableItemSetHash,
	}
	if b.StyleBasicInfo != nil {
		style := toAPIBaseItemInfo(*b.StyleBasicInfo)
		out.StyleBasicInfo = &style
	}
	if b.Damage != nil {
		out.Damage = &api.DamageInfo{
			DamageType:      b.Damage.DamageType,
			DamageIcon:      b.Damage.DamageIcon,
			TransparentIcon: b.Damage.TransparentIcon,
			Color: api.Color{
				Red:   b.Damage.Color.Red,
				Green: b.Damage.Color.Green,
				Blue:  b.Damage.Color.Blue,
				Alpha: b.Damage.Color.Alpha,
			},
		}
	}
	return out
}
