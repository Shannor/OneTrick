package httpadapter

import (
	"errors"
	"oneTrick/api"
	"oneTrick/core/activity"
	"oneTrick/ptr"
)

func ToAPIActivityResponse(report activity.MatchReport) api.GetActivity200JSONResponse {
	teams := make([]api.Team, 0, len(report.Teams))
	for _, t := range report.Teams {
		teams = append(teams, api.Team{
			ID:       t.ID,
			Standing: t.Standing,
			Score:    t.Score,
			TeamName: ptr.NonEmpty(t.TeamName),
		})
	}

	participants := make([]api.ActivityParticipant, 0, len(report.Participants))
	for _, p := range report.Participants {
		ap := api.ActivityParticipant{
			CharacterID:     p.CharacterID,
			MembershipID:    p.MembershipID,
			MembershipType:  p.MembershipType,
			DisplayName:     p.DisplayName,
			DisplayNameCode: ptr.NonEmpty(p.DisplayNameCode),
			CharacterClass:  p.CharacterClass,
			LightLevel:      p.LightLevel,
			EmblemIconURL:   ptr.NonEmpty(p.EmblemIconURL),
			TeamID:          ptr.NonEmpty(p.TeamID),
			Standing:        p.Standing,
			Score:           toAPIStatPairPtr(p.Score),
			Completed:       p.Completed,
			FireteamID:      ptr.NonEmpty(p.FireteamID),
			Performance:     toAPIInstancePerformance(p.Performance),
			UserID:          ptr.NonEmpty(p.UserID),
			SessionID:       ptr.NonEmpty(p.SessionID),
			SnapshotID:      ptr.NonEmpty(p.SnapshotID),
		}
		if p.ConfidenceLevel != "" {
			conf := api.ConfidenceLevel(p.ConfidenceLevel)
			ap.ConfidenceLevel = &conf
		}
		participants = append(participants, ap)
	}

	snapshots := make(map[string]api.CharacterSnapshot, len(report.Snapshots))
	for charID, snap := range report.Snapshots {
		snapshots[charID] = toAPICharacterSnapshot(snap)
	}

	users := make(map[string]api.User, len(report.Users))
	for charID, u := range report.Users {
		users[charID] = toAPIUser(u)
	}

	var aggPtr *api.Aggregate
	if report.Aggregate != nil {
		agg := toAPIAggregate(*report.Aggregate)
		aggPtr = &agg
	}

	var rawEntries *[]map[string]any
	if report.PostGameEntries != nil {
		entries := report.PostGameEntries
		rawEntries = &entries
	}

	return api.GetActivity200JSONResponse{
		Activity:        toAPIActivityHistory(report.Activity),
		Teams:           teams,
		Participants:    participants,
		Aggregate:       aggPtr,
		Snapshots:       snapshots,
		Users:           users,
		PostGameEntries: rawEntries,
	}
}

func ToAPIActivityError(err error) api.GetActivityResponseObject {
	switch {
	case errors.Is(err, activity.ErrInvalidActivityID):
		return api.GetActivity400JSONResponse{
			Message: err.Error(),
			Status:  api.ErrUnknownError,
		}
	case errors.Is(err, activity.ErrActivityNotFound):
		return api.GetActivity404JSONResponse{
			Message: "activity not found",
			Status:  api.ErrUnknownError,
		}
	case errors.Is(err, activity.ErrDestinyUnavailable):
		return api.GetActivity500JSONResponse{
			Message: "Destiny 2 servers are currently unavailable",
			Status:  api.ErrDestinyServerDown,
		}
	default:
		return api.GetActivity500JSONResponse{
			Message: err.Error(),
			Status:  api.ErrInternalSystemDown,
		}
	}
}

func toAPIActivityHistory(info activity.ActivityInfo) api.ActivityHistory {
	out := api.ActivityHistory{
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
		out.IsPrivate = new(info.IsPrivate)
	}
	if info.EstimatedGameMode != "" {
		gm := api.GameMode(info.EstimatedGameMode)
		out.EstimatedGameMode = &gm
	}
	return out
}

func toAPIStatPairPtr(pair activity.StatValuePair) *api.StatsValuePair {
	if !pair.Present {
		return nil
	}
	return &api.StatsValuePair{
		Value:        new(pair.Value),
		DisplayValue: new(pair.DisplayValue),
	}
}

func toAPIStatPairValue(pair activity.StatValuePair) api.StatsValuePair {
	if !pair.Present {
		return api.StatsValuePair{}
	}
	return api.StatsValuePair{
		Value:        new(pair.Value),
		DisplayValue: new(pair.DisplayValue),
	}
}

func toAPIUniqueStatsMap(vals map[string]activity.UniqueStatValue) *map[string]api.UniqueStatValue {
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

func toAPIPlayerStats(ps activity.PlayerStats) api.PlayerStats {
	return api.PlayerStats{
		Kills:      toAPIStatPairPtr(ps.Kills),
		Assists:    toAPIStatPairPtr(ps.Assists),
		Deaths:     toAPIStatPairPtr(ps.Deaths),
		Kd:         toAPIStatPairPtr(ps.KD),
		Kda:        toAPIStatPairPtr(ps.KDA),
		Standing:   toAPIStatPairPtr(ps.Standing),
		Team:       toAPIStatPairPtr(ps.Team),
		FireTeamID: toAPIStatPairPtr(ps.FireteamID),
		TimePlayed: toAPIStatPairPtr(ps.TimePlayed),
	}
}

func toAPIInstancePerformance(perf activity.Performance) api.InstancePerformance {
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
		PlayerStats: toAPIPlayerStats(perf.PlayerStats),
		Weapons:     weapons,
		Extra:       toAPIUniqueStatsMap(perf.Extra),
	}
}

func toAPIAggregate(agg activity.Aggregate) api.Aggregate {
	links := make(map[string]api.SnapshotLink, len(agg.SnapshotLinks))
	for charID, l := range agg.SnapshotLinks {
		links[charID] = api.SnapshotLink{
			CharacterID:        l.CharacterID,
			SnapshotID:         ptr.NonEmpty(l.SnapshotID),
			OriginalSnapshotID: ptr.NonEmpty(l.OriginalSnapshotID),
			SessionID:          ptr.NonEmpty(l.SessionID),
			ConfidenceLevel:    api.ConfidenceLevel(l.ConfidenceLevel),
			ConfidenceSource:   api.ConfidenceSource(l.ConfidenceSource),
			CreatedAt:          l.CreatedAt,
			UserID:             ptr.NonEmpty(l.UserID),
			DisplayName:        ptr.NonEmpty(l.DisplayName),
			CharacterClass:     ptr.NonEmpty(l.CharacterClass),
			EmblemURL:          ptr.NonEmpty(l.EmblemURL),
		}
	}

	perfs := make(map[string]api.InstancePerformance, len(agg.Performance))
	for charID, p := range agg.Performance {
		perfs[charID] = toAPIInstancePerformance(p)
	}

	sessionIDs := agg.SessionIDs
	if sessionIDs == nil {
		sessionIDs = make([]string, 0)
	}
	charIDs := agg.CharacterIDs
	if charIDs == nil {
		charIDs = make([]string, 0)
	}
	snapIDs := agg.SnapshotIDs
	if snapIDs == nil {
		snapIDs = make([]string, 0)
	}

	return api.Aggregate{
		ID:              agg.ID,
		ActivityID:      agg.ActivityID,
		SessionIds:      sessionIDs,
		CharacterIds:    charIDs,
		SnapshotIds:     snapIDs,
		ActivityDetails: toAPIActivityHistory(agg.ActivityDetails),
		SnapshotLinks:   links,
		Performance:     perfs,
		CreatedAt:       agg.CreatedAt,
	}
}

func toAPICharacterSnapshot(snap activity.CharacterSnapshot) api.CharacterSnapshot {
	out := api.CharacterSnapshot{
		ID:           snap.ID,
		Name:         snap.Name,
		Description:  ptr.NonEmpty(snap.Description),
		UserID:       snap.UserID,
		Hash:         snap.Hash,
		CharacterID:  snap.CharacterID,
		CreatedAt:    snap.CreatedAt,
		UpdatedAt:    snap.UpdatedAt,
		MergedIntoID: ptr.NonEmpty(snap.MergedIntoID),
		MergedAt:     snap.MergedAt,
	}
	if len(snap.MergedFromIDs) > 0 {
		ids := snap.MergedFromIDs
		out.MergedFromIDs = &ids
	}
	if snap.Stats != nil {
		stats := make(map[string]api.ClassStat, len(snap.Stats))
		for k, st := range snap.Stats {
			stats[k] = api.ClassStat{
				Name:            st.Name,
				Description:     st.Description,
				Icon:            st.Icon,
				HasIcon:         st.HasIcon,
				StatCategory:    st.StatCategory,
				AggregationType: st.AggregationType,
				Value:           st.Value,
			}
		}
		out.Stats = &stats
	}

	loadout := make(api.Loadout, len(snap.Loadout))
	for k, item := range snap.Loadout {
		loadout[k] = api.ItemSnapshot{
			InstanceID:     item.InstanceID,
			Name:           item.Name,
			ItemHash:       item.ItemHash,
			BucketHash:     item.BucketHash,
			ItemProperties: toAPIItemProperties(item.Details),
		}
	}
	out.Loadout = loadout

	if len(snap.ArmorSetBonuses) > 0 {
		bonuses := make([]api.ArmorSetBonus, 0, len(snap.ArmorSetBonuses))
		for _, b := range snap.ArmorSetBonuses {
			perks := make([]api.ArmorSetBonusPerk, 0, len(b.ActivePerks))
			for _, p := range b.ActivePerks {
				perks = append(perks, api.ArmorSetBonusPerk{
					RequiredSetCount: p.RequiredSetCount,
					Hash:             p.Hash,
					Name:             p.Name,
					Description:      ptr.NonEmpty(p.Description),
					IconPath:         ptr.NonEmpty(p.IconPath),
				})
			}
			bonuses = append(bonuses, api.ArmorSetBonus{
				SetHash:     b.SetHash,
				SetName:     b.SetName,
				ActiveCount: b.ActiveCount,
				ActivePerks: perks,
			})
		}
		out.ArmorSetBonuses = &bonuses
	}

	return out
}

func toAPIItemProperties(p activity.ItemProperties) api.ItemProperties {
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

func toAPIBaseItemInfo(b activity.BaseItemInfo) api.BaseItemInfo {
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

func toAPIUser(u activity.User) api.User {
	memberships := make([]api.Membership, 0, len(u.Memberships))
	for _, m := range u.Memberships {
		memberships = append(memberships, api.Membership{
			ID:          m.ID,
			Type:        m.Type,
			DisplayName: m.DisplayName,
		})
	}
	charIDs := u.CharacterIDs
	if charIDs == nil {
		charIDs = make([]string, 0)
	}
	return api.User{
		ID:                  u.ID,
		MemberID:            u.MemberID,
		PrimaryMembershipID: u.PrimaryMembershipID,
		UniqueName:          u.UniqueName,
		DisplayName:         u.DisplayName,
		Memberships:         memberships,
		CreatedAt:           u.CreatedAt,
		CharacterIDs:        charIDs,
	}
}
