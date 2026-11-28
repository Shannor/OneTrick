// Package activity provides DRIVEN adapters (called by core/activity via its outbound ports).
//
// WARNING — TEMPORARY ANTI-CORRUPTION BRIDGE (INTENTIONAL TRANSITIONAL ANTI-PATTERN):
//
// In standard Hexagonal Architecture, driven adapters are organized by infrastructure
// (adapters/driven/firestore, adapters/driven/bungie, adapters/driven/cache) and talk
// directly to external systems rather than wrapping sibling application services.
//
// This package intentionally wraps legacy services/aggregate, services/snapshot, and
// services/user ONLY so core/activity can stay strictly isolated from api.*,
// clients/bungie.*, firestore, and legacy services/* while the rest of the codebase
// migrates incrementally.
//
// Migration & Removal Checklist (delete each adapter as its backing domain migrates):
//  1. [DONE in OT-24] CarnageReportGateway & ManifestLookup moved permanently into
//     adapters/driven/bungie and adapters/driven/cache.
//  2. NewAggregateReader: move into adapters/driven/firestore once services/aggregate migrates.
//  3. NewSnapshotReader: move into adapters/driven/firestore once services/snapshot migrates.
//  4. NewUserLookup: move into adapters/driven/firestore once services/user migrates.
//  5. Delete this entire package once all backing domains have migrated.
package activity

import (
	"context"
	"errors"
	"oneTrick/api"
	coreactivity "oneTrick/core/activity"
	"oneTrick/ptr"
	"oneTrick/services/aggregate"
	"oneTrick/services/snapshot"
	"oneTrick/services/user"
)

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
