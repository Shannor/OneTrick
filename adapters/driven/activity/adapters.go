// Package activity provides DRIVEN adapters (called by core/activity via its outbound ports).
//
// WARNING — TEMPORARY ANTI-CORRUPTION BRIDGE (INTENTIONAL TRANSITIONAL ANTI-PATTERN):
//
// In standard Hexagonal Architecture, driven adapters are organized by infrastructure
// (adapters/driven/firestore, adapters/driven/bungie, adapters/driven/cache) and talk
// directly to external systems rather than wrapping sibling application services.
//
// This package intentionally wraps legacy services/destiny, services/aggregate,
// services/snapshot, and services/user ONLY so core/activity can stay strictly isolated
// from api.*, clients/bungie.*, firestore, and legacy services/* while the rest of the
// codebase migrates incrementally.
//
// Migration & Removal Checklist (delete each adapter as its backing domain migrates):
//  1. NewCarnageReportGateway & NewManifestLookup: move into adapters/driven/bungie and
//     adapters/driven/cache once Bungie/Manifest gateways leave services/destiny.
//  2. NewAggregateReader: move into adapters/driven/firestore once services/aggregate migrates.
//  3. NewSnapshotReader: move into adapters/driven/firestore once services/snapshot migrates.
//  4. NewUserLookup: move into adapters/driven/firestore once services/user migrates.
//  5. Delete this entire package once all backing domains have migrated.
package activity

import (
	"context"
	"errors"
	"fmt"
	"oneTrick/api"
	"oneTrick/clients/bungie"
	coreactivity "oneTrick/core/activity"
	"oneTrick/ptr"
	"oneTrick/services/aggregate"
	"oneTrick/services/destiny"
	"oneTrick/services/snapshot"
	"oneTrick/services/user"
	"strconv"
	"strings"

	"github.com/fatih/structs"
)

const bungieBaseURL = "https://www.bungie.net"

func formatBungieURL(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return trimmed
	}
	if strings.HasPrefix(trimmed, "/") {
		return bungieBaseURL + trimmed
	}
	return bungieBaseURL + "/" + trimmed
}

type carnageReportGateway struct {
	svc destiny.Service
}

func NewCarnageReportGateway(svc destiny.Service) coreactivity.CarnageReportGateway {
	return carnageReportGateway{svc: svc}
}

func (g carnageReportGateway) GetPostGameCarnageReport(ctx context.Context, activityID string) (coreactivity.CarnageReport, error) {
	data, teams, err := g.svc.GetActivity(ctx, activityID)
	if err != nil {
		if errors.Is(err, destiny.ErrActivityNotFound) {
			return coreactivity.CarnageReport{}, fmt.Errorf("%w: %w", coreactivity.ErrActivityNotFound, err)
		}
		if errors.Is(err, destiny.ErrDestinyServerDown) {
			return coreactivity.CarnageReport{}, fmt.Errorf("%w: %w", coreactivity.ErrDestinyUnavailable, err)
		}
		return coreactivity.CarnageReport{}, err
	}
	if data == nil {
		return coreactivity.CarnageReport{}, coreactivity.ErrActivityNotFound
	}

	report := coreactivity.CarnageReport{
		Teams: toCoreTeams(teams),
	}
	if data.Period != nil {
		report.Period = *data.Period
	}
	if data.ActivityDetails != nil {
		d := data.ActivityDetails
		details := coreactivity.CarnageActivityDetails{
			InstanceID: ptr.Deref(d.InstanceId),
			ModeName:   destiny.ActivityModeTypeToString((*bungie.CurrentActivityModeType)(d.Mode)),
		}
		if d.ReferenceId != nil {
			details.ReferenceID = int64(*d.ReferenceId)
		}
		if d.DirectorActivityHash != nil {
			details.DirectorActivityHash = int64(*d.DirectorActivityHash)
		}
		if d.Mode != nil {
			details.Mode = int(*d.Mode)
		}
		if d.IsPrivate != nil {
			details.IsPrivate = *d.IsPrivate
			details.HasIsPrivate = true
		}
		report.ActivityDetails = details
	}

	if data.Entries != nil {
		src := *data.Entries
		report.Entries = make([]coreactivity.CarnageEntry, 0, len(src))
		report.RawEntries = make([]map[string]any, 0, len(src))
		for _, entry := range src {
			report.Entries = append(report.Entries, toCoreCarnageEntry(entry))
			report.RawEntries = append(report.RawEntries, structs.Map(entry))
		}
	} else {
		report.Entries = make([]coreactivity.CarnageEntry, 0)
		report.RawEntries = make([]map[string]any, 0)
	}

	return report, nil
}

func toCoreTeams(teams []api.Team) []coreactivity.Team {
	out := make([]coreactivity.Team, 0, len(teams))
	for _, t := range teams {
		out = append(out, coreactivity.Team{
			ID:       t.ID,
			Standing: t.Standing,
			Score:    t.Score,
			TeamName: ptr.Deref(t.TeamName),
		})
	}
	return out
}

func toCoreCarnageEntry(entry bungie.PostGameCarnageReportEntry) coreactivity.CarnageEntry {
	out := coreactivity.CarnageEntry{
		CharacterID: ptr.Deref(entry.CharacterId),
	}

	if entry.Player != nil {
		p := entry.Player
		out.CharacterClass = ptr.Deref(p.CharacterClass)
		if p.LightLevel != nil {
			out.LightLevel = *p.LightLevel
		}
		if p.EmblemHash != nil {
			out.EmblemHash = int64(*p.EmblemHash)
		}
		id := extractPlayerIdentity(p)
		out.MembershipID = id.membershipID
		out.BungieNetMembershipID = id.bungieNetMembershipID
		out.MembershipType = id.membershipType
		out.DisplayName = id.displayName
		out.DisplayNameCode = id.displayNameCode
	}

	if entry.Standing != nil {
		out.Standing = *entry.Standing
		out.HasStanding = true
	}

	if entry.Score != nil && entry.Score.Basic != nil {
		out.Score = toCoreBungieStatPair(entry.Score.Basic)
	}

	if entry.Values != nil {
		vals := *entry.Values
		out.PlayerStats = toCorePlayerStatsFromBungie(vals)
		if !out.Score.Present {
			if scoreVal, ok := vals["score"]; ok && scoreVal.Basic != nil {
				out.Score = toCoreBungieStatPair(scoreVal.Basic)
			}
		}
		if compVal, ok := vals["completed"]; ok && compVal.Basic != nil && compVal.Basic.Value != nil {
			out.Completed = *compVal.Basic.Value >= 1
		}
	}

	if entry.Extended != nil {
		if entry.Extended.Values != nil {
			out.ExtraStats = toCoreUniqueStatsFromBungie(*entry.Extended.Values)
		}
		if entry.Extended.Weapons != nil {
			rawWeapons := *entry.Extended.Weapons
			weapons := make([]coreactivity.CarnageWeapon, 0, len(rawWeapons))
			for _, w := range rawWeapons {
				if w.ReferenceId == nil || *w.ReferenceId == 0 {
					continue
				}
				cw := coreactivity.CarnageWeapon{
					ReferenceID: int64(*w.ReferenceId),
				}
				if w.Values != nil {
					cw.Stats = toCoreUniqueStatsFromBungie(*w.Values)
				}
				weapons = append(weapons, cw)
			}
			out.Weapons = weapons
		}
	}

	return out
}

type playerIdentity struct {
	membershipID          string
	bungieNetMembershipID string
	membershipType        int32
	displayName           string
	displayNameCode       string
}

func extractPlayerIdentity(p *bungie.DestinyPlayer) playerIdentity {
	var id playerIdentity
	if p == nil {
		return id
	}
	destinyInfo := p.DestinyUserInfo
	bnetInfo := p.BungieNetUserInfo

	if destinyInfo != nil {
		id.membershipID = strings.TrimSpace(ptr.Deref(destinyInfo.MembershipId))
		if destinyInfo.MembershipType != nil {
			id.membershipType = *destinyInfo.MembershipType
		}
	}
	if bnetInfo != nil {
		id.bungieNetMembershipID = strings.TrimSpace(ptr.Deref(bnetInfo.MembershipId))
		if id.membershipID == "" {
			id.membershipID = id.bungieNetMembershipID
		}
		if id.membershipType == 0 && bnetInfo.MembershipType != nil {
			id.membershipType = *bnetInfo.MembershipType
		}
	}

	if name, code := extractBungieGlobalName(destinyInfo); name != "" {
		id.displayName = name
		id.displayNameCode = code
		return id
	}
	if name, code := extractBungieGlobalName(bnetInfo); name != "" {
		id.displayName = name
		id.displayNameCode = code
		return id
	}
	if destinyInfo != nil {
		if name := strings.TrimSpace(ptr.Deref(destinyInfo.DisplayName)); name != "" {
			id.displayName = name
			return id
		}
	}
	if bnetInfo != nil {
		id.displayName = strings.TrimSpace(ptr.Deref(bnetInfo.DisplayName))
	}
	return id
}

func extractBungieGlobalName(info *bungie.UserUserInfoCard) (string, string) {
	if info == nil {
		return "", ""
	}
	name := strings.TrimSpace(ptr.Deref(info.BungieGlobalDisplayName))
	if name == "" {
		return "", ""
	}
	code := ""
	if info.BungieGlobalDisplayNameCode != nil {
		code = fmt.Sprintf("%04d", *info.BungieGlobalDisplayNameCode)
	}
	return name, code
}

func toCoreBungieStatPair(pair *bungie.DestinyHistoricalStatsDestinyHistoricalStatsValuePair) coreactivity.StatValuePair {
	if pair == nil {
		return coreactivity.StatValuePair{}
	}
	return coreactivity.StatValuePair{
		Value:        ptr.Deref(pair.Value),
		DisplayValue: ptr.Deref(pair.DisplayValue),
		Present:      true,
	}
}

func toCorePlayerStatsFromBungie(vals map[string]bungie.HistoricalStatsValue) coreactivity.PlayerStats {
	var ps coreactivity.PlayerStats
	for key, val := range vals {
		if val.Basic == nil {
			continue
		}
		pair := toCoreBungieStatPair(val.Basic)
		switch key {
		case "kills":
			ps.Kills = pair
		case "assists":
			ps.Assists = pair
		case "deaths":
			ps.Deaths = pair
		case "killsDeathsRatio":
			ps.KD = pair
		case "killsDeathsAssists":
			ps.KDA = pair
		case "standing":
			ps.Standing = pair
		case "team":
			ps.Team = pair
		case "fireteamId":
			ps.FireteamID = pair
		case "timePlayedSeconds":
			ps.TimePlayed = pair
		}
	}
	return ps
}

func toCoreUniqueStatsFromBungie(vals map[string]bungie.HistoricalStatsValue) map[string]coreactivity.UniqueStatValue {
	out := make(map[string]coreactivity.UniqueStatValue, len(vals))
	for key, val := range vals {
		if val.Basic == nil {
			continue
		}
		uv := coreactivity.UniqueStatValue{
			Name:       ptr.Deref(val.StatId),
			Basic:      toCoreBungieStatPair(val.Basic),
			PGA:        toCoreBungieStatPair(val.Pga),
			Weighted:   toCoreBungieStatPair(val.Weighted),
			ActivityID: parseOptionalInt64(val.ActivityId),
		}
		out[key] = uv
	}
	return out
}

func parseOptionalInt64(value *string) *int64 {
	if value == nil || *value == "" {
		return nil
	}
	parsed, err := strconv.ParseInt(*value, 10, 64)
	if err != nil {
		return nil
	}
	return &parsed
}

type manifestLookupAdapter struct {
	svc destiny.ManifestService
}

func NewManifestLookup(svc destiny.ManifestService) coreactivity.ManifestLookup {
	return manifestLookupAdapter{svc: svc}
}

func (m manifestLookupAdapter) GetItemsByIDs(ctx context.Context, ids []int64) (map[int64]coreactivity.ItemManifest, error) {
	items, err := m.svc.GetItemsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]coreactivity.ItemManifest, len(items))
	for key, def := range items {
		hash := def.Hash
		if hash == 0 {
			if parsed, parseErr := strconv.ParseInt(key, 10, 64); parseErr == nil {
				hash = parsed
			}
		}
		if hash == 0 {
			continue
		}
		bucketHash := def.Inventory.BucketTypeHash
		if bucketHash == 0 {
			bucketHash = def.EquippingBlock.EquipmentSlotTypeHash
		}
		out[hash] = coreactivity.ItemManifest{
			Hash:                       hash,
			Name:                       def.DisplayProperties.Name,
			Description:                def.DisplayProperties.Description,
			IconURL:                    formatBungieURL(def.DisplayProperties.Icon),
			HasIcon:                    def.DisplayProperties.HasIcon,
			ItemTypeDisplayName:        def.ItemTypeDisplayName,
			ItemTypeAndTierDisplayName: def.ItemTypeAndTierDisplayName,
			BucketHash:                 bucketHash,
		}
	}
	return out, nil
}

func (m manifestLookupAdapter) GetActivitiesByIDs(ctx context.Context, ids []int64) (map[int64]coreactivity.ActivityManifest, error) {
	acts, err := m.svc.GetActivitiesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]coreactivity.ActivityManifest, len(acts))
	for key, def := range acts {
		hash := int64(def.Hash)
		if hash == 0 {
			if parsed, parseErr := strconv.ParseInt(key, 10, 64); parseErr == nil {
				hash = parsed
			}
		}
		if hash == 0 {
			continue
		}
		out[hash] = coreactivity.ActivityManifest{
			Hash:                   hash,
			Name:                   def.DisplayProperties.Name,
			Description:            def.DisplayProperties.Description,
			PgcrImageURL:           formatBungieURL(def.PgcrImage),
			DirectActivityModeHash: int64(def.DirectActivityModeHash),
		}
	}
	return out, nil
}

func (m manifestLookupAdapter) GetActivityMode(ctx context.Context, hash int64, modeType int) (coreactivity.ActivityModeManifest, bool, error) {
	if hash != 0 {
		def, err := m.svc.GetActivityMode(ctx, hash)
		if err == nil && def != nil {
			return coreactivity.ActivityModeManifest{
				Hash:     def.Hash,
				ModeType: def.ModeType,
				IconURL:  formatBungieURL(def.DisplayProperties.Icon),
			}, true, nil
		}
	}
	if modeType == 0 {
		return coreactivity.ActivityModeManifest{}, false, nil
	}
	modes, err := m.svc.GetActivityModes(ctx)
	if err != nil {
		return coreactivity.ActivityModeManifest{}, false, err
	}
	for _, def := range modes {
		if def.ModeType == modeType {
			return coreactivity.ActivityModeManifest{
				Hash:     def.Hash,
				ModeType: def.ModeType,
				IconURL:  formatBungieURL(def.DisplayProperties.Icon),
			}, true, nil
		}
	}
	return coreactivity.ActivityModeManifest{}, false, nil
}

type aggregateReaderAdapter struct {
	svc aggregate.Service
}

func NewAggregateReader(svc aggregate.Service) coreactivity.AggregateReader {
	return aggregateReaderAdapter{svc: svc}
}

func (a aggregateReaderAdapter) GetByActivityID(ctx context.Context, activityID string) (coreactivity.Aggregate, bool, error) {
	agg, err := a.svc.GetAggregate(ctx, activityID)
	if err != nil {
		if errors.Is(err, aggregate.NotFound) {
			return coreactivity.Aggregate{}, false, nil
		}
		return coreactivity.Aggregate{}, false, err
	}
	if agg == nil {
		return coreactivity.Aggregate{}, false, nil
	}
	return toCoreAggregate(*agg), true, nil
}

func toCoreAggregate(agg api.Aggregate) coreactivity.Aggregate {
	links := make(map[string]coreactivity.SnapshotLink, len(agg.SnapshotLinks))
	for charID, l := range agg.SnapshotLinks {
		links[charID] = coreactivity.SnapshotLink{
			CharacterID:        l.CharacterID,
			SnapshotID:         ptr.Deref(l.SnapshotID),
			OriginalSnapshotID: ptr.Deref(l.OriginalSnapshotID),
			SessionID:          ptr.Deref(l.SessionID),
			ConfidenceLevel:    coreactivity.ConfidenceLevel(l.ConfidenceLevel),
			ConfidenceSource:   coreactivity.ConfidenceSource(l.ConfidenceSource),
			CreatedAt:          l.CreatedAt,
			UserID:             ptr.Deref(l.UserID),
			DisplayName:        ptr.Deref(l.DisplayName),
			CharacterClass:     ptr.Deref(l.CharacterClass),
			EmblemURL:          ptr.Deref(l.EmblemURL),
		}
	}

	perfs := make(map[string]coreactivity.Performance, len(agg.Performance))
	for charID, p := range agg.Performance {
		perfs[charID] = toCorePerformance(p)
	}

	return coreactivity.Aggregate{
		ID:              agg.ID,
		ActivityID:      agg.ActivityID,
		SessionIDs:      agg.SessionIds,
		CharacterIDs:    agg.CharacterIds,
		SnapshotIDs:     agg.SnapshotIds,
		ActivityDetails: toCoreActivityInfo(agg.ActivityDetails),
		SnapshotLinks:   links,
		Performance:     perfs,
		CreatedAt:       agg.CreatedAt,
	}
}

func toCoreActivityInfo(h api.ActivityHistory) coreactivity.ActivityInfo {
	info := coreactivity.ActivityInfo{
		ReferenceID:  h.ReferenceID,
		ActivityHash: h.ActivityHash,
		InstanceID:   h.InstanceID,
		Location:     h.Location,
		Description:  h.Description,
		Activity:     h.Activity,
		ImageURL:     h.ImageURL,
		ActivityIcon: h.ActivityIcon,
		Period:       h.Period,
		Mode:         ptr.Deref(h.Mode),
	}
	if h.IsPrivate != nil {
		info.IsPrivate = *h.IsPrivate
		info.HasIsPrivate = true
	}
	if h.EstimatedGameMode != nil {
		info.EstimatedGameMode = string(*h.EstimatedGameMode)
	}
	return info
}

func toCorePerformance(p api.InstancePerformance) coreactivity.Performance {
	weapons := make(map[string]coreactivity.WeaponMetrics, len(p.Weapons))
	for k, w := range p.Weapons {
		wm := coreactivity.WeaponMetrics{
			ReferenceID:         ptr.Deref(w.ReferenceID),
			BucketHash:          ptr.Deref(w.BucketHash),
			ItemTypeDisplayName: ptr.Deref(w.ItemTypeDisplayName),
		}
		if w.Display != nil {
			wm.HasDisplay = true
			wm.Display = coreactivity.WeaponDisplay{
				Name:        w.Display.Name,
				Description: w.Display.Description,
				Icon:        ptr.Deref(w.Display.Icon),
				HasIcon:     w.Display.HasIcon,
			}
		}
		if w.ItemProperties != nil {
			props := toCoreItemProperties(*w.ItemProperties)
			wm.Properties = &props
		}
		if w.Stats != nil {
			wm.Stats = toCoreUniqueStatsFromAPI(*w.Stats)
		}
		weapons[k] = wm
	}

	var extra map[string]coreactivity.UniqueStatValue
	if p.Extra != nil {
		extra = toCoreUniqueStatsFromAPI(*p.Extra)
	}

	return coreactivity.Performance{
		PlayerStats: toCorePlayerStatsFromAPI(p.PlayerStats),
		Weapons:     weapons,
		Extra:       extra,
	}
}

func toCoreStatPairFromAPI(p *api.StatsValuePair) coreactivity.StatValuePair {
	if p == nil {
		return coreactivity.StatValuePair{}
	}
	return coreactivity.StatValuePair{
		Value:        ptr.Deref(p.Value),
		DisplayValue: ptr.Deref(p.DisplayValue),
		Present:      true,
	}
}

func toCorePlayerStatsFromAPI(ps api.PlayerStats) coreactivity.PlayerStats {
	return coreactivity.PlayerStats{
		Kills:      toCoreStatPairFromAPI(ps.Kills),
		Assists:    toCoreStatPairFromAPI(ps.Assists),
		Deaths:     toCoreStatPairFromAPI(ps.Deaths),
		KD:         toCoreStatPairFromAPI(ps.Kd),
		KDA:        toCoreStatPairFromAPI(ps.Kda),
		Standing:   toCoreStatPairFromAPI(ps.Standing),
		Team:       toCoreStatPairFromAPI(ps.Team),
		FireteamID: toCoreStatPairFromAPI(ps.FireTeamID),
		TimePlayed: toCoreStatPairFromAPI(ps.TimePlayed),
	}
}

func toCoreUniqueStatsFromAPI(vals map[string]api.UniqueStatValue) map[string]coreactivity.UniqueStatValue {
	out := make(map[string]coreactivity.UniqueStatValue, len(vals))
	for k, v := range vals {
		out[k] = coreactivity.UniqueStatValue{
			Name:       ptr.Deref(v.Name),
			Basic:      toCoreStatPairFromAPI(&v.Basic),
			PGA:        toCoreStatPairFromAPI(v.Pga),
			Weighted:   toCoreStatPairFromAPI(v.Weighted),
			ActivityID: v.ActivityID,
		}
	}
	return out
}

type snapshotReaderAdapter struct {
	svc snapshot.Service
}

func NewSnapshotReader(svc snapshot.Service) coreactivity.SnapshotReader {
	return snapshotReaderAdapter{svc: svc}
}

func (s snapshotReaderAdapter) GetByIDs(ctx context.Context, snapshotIDs []string) ([]coreactivity.CharacterSnapshot, error) {
	if len(snapshotIDs) == 0 {
		return []coreactivity.CharacterSnapshot{}, nil
	}
	snaps, err := s.svc.GetByIDs(ctx, snapshotIDs)
	if err != nil {
		return nil, err
	}
	out := make([]coreactivity.CharacterSnapshot, 0, len(snaps))
	for _, snap := range snaps {
		out = append(out, toCoreSnapshot(snap))
	}
	return out, nil
}

func toCoreSnapshot(snap api.CharacterSnapshot) coreactivity.CharacterSnapshot {
	out := coreactivity.CharacterSnapshot{
		ID:           snap.ID,
		Name:         snap.Name,
		Description:  ptr.Deref(snap.Description),
		UserID:       snap.UserID,
		Hash:         snap.Hash,
		CharacterID:  snap.CharacterID,
		CreatedAt:    snap.CreatedAt,
		UpdatedAt:    snap.UpdatedAt,
		MergedIntoID: ptr.Deref(snap.MergedIntoID),
		MergedAt:     snap.MergedAt,
	}
	if snap.MergedFromIDs != nil {
		out.MergedFromIDs = *snap.MergedFromIDs
	}
	if snap.Stats != nil {
		stats := make(map[string]coreactivity.ClassStat, len(*snap.Stats))
		for k, st := range *snap.Stats {
			stats[k] = coreactivity.ClassStat{
				Name:            st.Name,
				Description:     st.Description,
				Icon:            st.Icon,
				HasIcon:         st.HasIcon,
				StatCategory:    st.StatCategory,
				AggregationType: st.AggregationType,
				Value:           st.Value,
			}
		}
		out.Stats = stats
	}
	if snap.Loadout != nil {
		loadout := make(map[string]coreactivity.ItemSnapshot, len(snap.Loadout))
		for k, item := range snap.Loadout {
			loadout[k] = coreactivity.ItemSnapshot{
				InstanceID: item.InstanceID,
				Name:       item.Name,
				ItemHash:   item.ItemHash,
				BucketHash: item.BucketHash,
				Details:    toCoreItemProperties(item.ItemProperties),
			}
		}
		out.Loadout = loadout
	}
	if snap.ArmorSetBonuses != nil {
		bonuses := make([]coreactivity.ArmorSetBonus, 0, len(*snap.ArmorSetBonuses))
		for _, b := range *snap.ArmorSetBonuses {
			perks := make([]coreactivity.ArmorSetBonusPerk, 0, len(b.ActivePerks))
			for _, p := range b.ActivePerks {
				perks = append(perks, coreactivity.ArmorSetBonusPerk{
					RequiredSetCount: p.RequiredSetCount,
					Hash:             p.Hash,
					Name:             p.Name,
					Description:      ptr.Deref(p.Description),
					IconPath:         ptr.Deref(p.IconPath),
				})
			}
			bonuses = append(bonuses, coreactivity.ArmorSetBonus{
				SetHash:     b.SetHash,
				SetName:     b.SetName,
				ActiveCount: b.ActiveCount,
				ActivePerks: perks,
			})
		}
		out.ArmorSetBonuses = bonuses
	}
	return out
}

func toCoreItemProperties(p api.ItemProperties) coreactivity.ItemProperties {
	perks := make([]coreactivity.Perk, 0, len(p.Perks))
	for _, pk := range p.Perks {
		perks = append(perks, coreactivity.Perk{
			Name:        pk.Name,
			Description: ptr.Deref(pk.Description),
			Hash:        pk.Hash,
			IconPath:    ptr.Deref(pk.IconPath),
		})
	}
	stats := make(map[string]coreactivity.GunStat, len(p.Stats))
	for k, st := range p.Stats {
		stats[k] = coreactivity.GunStat{
			Hash:        st.Hash,
			Value:       st.Value,
			Name:        st.Name,
			Description: st.Description,
		}
	}
	out := coreactivity.ItemProperties{
		CharacterID: ptr.Deref(p.CharacterId),
		BaseInfo:    toCoreBaseItemInfo(p.BaseInfo),
		Perks:       perks,
		Stats:       stats,
	}
	if p.Sockets != nil {
		out.HasSockets = true
		sockets := make([]coreactivity.Socket, 0, len(*p.Sockets))
		for _, sk := range *p.Sockets {
			sockets = append(sockets, coreactivity.Socket{
				PlugHash:                  sk.PlugHash,
				IsEnabled:                 sk.IsEnabled,
				IsVisible:                 sk.IsVisible,
				Name:                      sk.Name,
				Description:               sk.Description,
				Icon:                      ptr.Deref(sk.Icon),
				ItemTypeDisplayName:       ptr.Deref(sk.ItemTypeDisplayName),
				ItemTypeTieredDisplayName: ptr.Deref(sk.ItemTypeTieredDisplayName),
			})
		}
		out.Sockets = sockets
	}
	return out
}

func toCoreBaseItemInfo(b api.BaseItemInfo) coreactivity.BaseItemInfo {
	out := coreactivity.BaseItemInfo{
		Name:                       b.Name,
		Icon:                       ptr.Deref(b.Icon),
		ItemHash:                   b.ItemHash,
		InstanceID:                 b.InstanceId,
		BucketHash:                 b.BucketHash,
		ItemTypeAndTierDisplayName: b.ItemTypeAndTierDisplayName,
		ItemTypeDisplayName:        b.ItemTypeDisplayName,
		TierTypeName:               b.TierTypeName,
		TierType:                   b.TierType,
		EquipableItemSetHash:       b.EquipableItemSetHash,
	}
	if b.StyleBasicInfo != nil {
		style := toCoreBaseItemInfo(*b.StyleBasicInfo)
		out.StyleBasicInfo = &style
	}
	if b.Damage != nil {
		out.Damage = &coreactivity.DamageInfo{
			DamageType:      b.Damage.DamageType,
			DamageIcon:      b.Damage.DamageIcon,
			TransparentIcon: b.Damage.TransparentIcon,
			Color: coreactivity.Color{
				Red:   b.Damage.Color.Red,
				Green: b.Damage.Color.Green,
				Blue:  b.Damage.Color.Blue,
				Alpha: b.Damage.Color.Alpha,
			},
		}
	}
	return out
}

type userLookupAdapter struct {
	svc user.Service
}

func NewUserLookup(svc user.Service) coreactivity.UserLookup {
	return userLookupAdapter{svc: svc}
}

func (u userLookupAdapter) FindByPartyMembershipIDs(ctx context.Context, membershipIDs []string) ([]coreactivity.User, error) {
	if len(membershipIDs) == 0 {
		return []coreactivity.User{}, nil
	}
	users, err := u.svc.FindByPartyMembershipIDs(ctx, membershipIDs)
	if err != nil {
		return nil, err
	}
	return toCoreUsers(users), nil
}

func (u userLookupAdapter) GetByIDs(ctx context.Context, userIDs []string) ([]coreactivity.User, error) {
	if len(userIDs) == 0 {
		return []coreactivity.User{}, nil
	}
	users, err := u.svc.GetByIDs(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	return toCoreUsers(users), nil
}

func toCoreUsers(users []user.User) []coreactivity.User {
	out := make([]coreactivity.User, 0, len(users))
	for _, u := range users {
		memberships := make([]coreactivity.Membership, 0, len(u.Memberships))
		for _, m := range u.Memberships {
			memberships = append(memberships, coreactivity.Membership{
				ID:          m.ID,
				Type:        m.Type,
				DisplayName: m.DisplayName,
			})
		}
		out = append(out, coreactivity.User{
			ID:                  u.ID,
			MemberID:            u.MemberID,
			PrimaryMembershipID: u.PrimaryMembershipID,
			UniqueName:          u.UniqueName,
			DisplayName:         u.DisplayName,
			Memberships:         memberships,
			CreatedAt:           u.CreatedAt,
			CharacterIDs:        u.CharacterIDs,
		})
	}
	return out
}
