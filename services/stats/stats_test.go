package stats

import (
	"context"
	"errors"
	"fmt"
	"oneTrick/api"
	"oneTrick/services/destiny"
	"oneTrick/services/snapshot"
	"slices"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestSnapshotMetricsSorting(t *testing.T) {
	now := time.Now()
	snaps := []api.CharacterSnapshot{
		{
			ID:        "snap-1",
			CreatedAt: now.Add(-1 * time.Hour),
		},
		{
			ID:        "snap-2",
			CreatedAt: now,
		},
	}

	counts := map[string]int{
		"snap-1": 10,
		"snap-2": 5,
	}

	// Test sort by matches_played desc
	slices.SortFunc(snaps, func(a, b api.CharacterSnapshot) int {
		ca, cb := counts[a.ID], counts[b.ID]
		return cb - ca
	})

	if snaps[0].ID != "snap-1" {
		t.Errorf("expected snap-1 first by matches played, got %s", snaps[0].ID)
	}

	// Test sort by created_at desc
	slices.SortFunc(snaps, func(a, b api.CharacterSnapshot) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})

	if snaps[0].ID != "snap-2" {
		t.Errorf("expected snap-2 first by created_at, got %s", snaps[0].ID)
	}
}

func TestPlayerStatsMapping(t *testing.T) {
	ps := api.PlayerStats{
		Kills: new(api.StatsValuePair{
			DisplayValue: new("25"),
			Value:        new(25.0),
		}),
		Deaths: new(api.StatsValuePair{
			DisplayValue: new("10"),
			Value:        new(10.0),
		}),
		Kd: new(api.StatsValuePair{
			DisplayValue: new("2.50"),
			Value:        new(2.50),
		}),
	}

	if *ps.Kills.Value != 25.0 || *ps.Kd.Value != 2.50 {
		t.Errorf("stats value mismatch: %+v", ps)
	}
}

func TestStatsMatchesGameMode(t *testing.T) {
	tests := []struct {
		mode     string
		activity string
		gameMode api.GameMode
		want     bool
	}{
		{"Clash Quickplay", "Competitive: Matchmade", api.GameModeCompetitive, true},
		{"Clash Quickplay", "Competitive: Matchmade", api.GameModeQuickPlay, false},
		{"Zone Control", "Competitive: Matchmade", api.GameModeCompetitive, true},
		{"Zone Control", "Competitive: Matchmade", api.GameModeQuickPlay, false},
		{"Iron Banner Control", "Control: Matchmade", api.GameModeIronBanner, true},
		{"Iron Banner Control", "Control: Matchmade", api.GameModeQuickPlay, false},
		{"Control Quickplay", "Control: Matchmade", api.GameModeQuickPlay, true},
		{"Trials of Osiris", "Trials of Osiris: Matchmade", api.GameModeTrials, true},
	}

	for _, tt := range tests {
		h := api.ActivityHistory{
			Mode:     new(tt.mode),
			Activity: tt.activity,
		}
		if got := MatchesGameMode(h, tt.gameMode); got != tt.want {
			t.Errorf("MatchesGameMode((%q, %q), %q) = %v, want %v", tt.mode, tt.activity, tt.gameMode, got, tt.want)
		}
	}
}

func TestFilterSnapshotsByGameModeAndMinGames(t *testing.T) {
	allSnapshots := []api.CharacterSnapshot{
		{ID: "snap-zero"},
		{ID: "snap-three"},
		{ID: "snap-six"},
	}
	counts := map[string]int{
		"snap-zero":  0,
		"snap-three": 3,
		"snap-six":   6,
	}

	t.Run("gameMode set with minimumGames=0 excludes snapshots with 0 games", func(t *testing.T) {
		filtered := filterSnapshotsByGameModeAndMinGames(allSnapshots, counts, new(api.GameModeCompetitive), 0)
		if len(filtered) != 2 || filtered[0].ID != "snap-three" || filtered[1].ID != "snap-six" {
			t.Errorf("expected [snap-three, snap-six], got %+v", filtered)
		}
	})

	t.Run("gameMode set with minimumGames=5 excludes snapshots with <5 games", func(t *testing.T) {
		filtered := filterSnapshotsByGameModeAndMinGames(allSnapshots, counts, new(api.GameModeCompetitive), 5)
		if len(filtered) != 1 || filtered[0].ID != "snap-six" {
			t.Errorf("expected [snap-six], got %+v", filtered)
		}
	})

	t.Run("gameMode nil with minimumGames=0 includes snapshots with 0 games", func(t *testing.T) {
		filtered := filterSnapshotsByGameModeAndMinGames(allSnapshots, counts, nil, 0)
		if len(filtered) != 3 {
			t.Errorf("expected all 3 snapshots, got %+v", filtered)
		}
	})

	t.Run("gameMode allGameModes with minimumGames=0 includes snapshots with 0 games", func(t *testing.T) {
		filtered := filterSnapshotsByGameModeAndMinGames(allSnapshots, counts, new(api.GameModeAll), 0)
		if len(filtered) != 3 {
			t.Errorf("expected all 3 snapshots, got %+v", filtered)
		}
	})
}

type mockManifestService struct {
	destiny.ManifestService
	items     map[int64]*destiny.ItemDefinition
	callCount map[int64]int
}

func (m *mockManifestService) GetItem(_ context.Context, hash int64) (*destiny.ItemDefinition, error) {
	if m.callCount != nil {
		m.callCount[hash]++
	}
	if m.items != nil {
		if def, ok := m.items[hash]; ok {
			return def, nil
		}
	}
	return nil, fmt.Errorf("item %d not found", hash)
}

func makeBaseTestLoadout(kinID string, enID string, subclassHash int64, powerID string, powerHash int64) api.Loadout {
	return api.Loadout{
		strconv.Itoa(destiny.Kinetic): {
			InstanceID: kinID,
			ItemHash:   1001,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{InstanceId: kinID, TierType: 6},
			},
		},
		strconv.Itoa(destiny.Energy): {
			InstanceID: enID,
			ItemHash:   2002,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{InstanceId: enID, TierType: 5},
			},
		},
		strconv.Itoa(destiny.SubClass): {
			InstanceID: "sub-inst",
			ItemHash:   subclassHash,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{Name: "Gunslinger"},
			},
		},
		strconv.Itoa(destiny.Power): {
			InstanceID: powerID,
			ItemHash:   powerHash,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{InstanceId: powerID, TierType: 5},
			},
		},
	}
}

func TestCompareSimilarLoadoutTiersAndDiffs(t *testing.T) {
	helmetKey := strconv.FormatUint(uint64(destiny.HelmetArmor), 10)
	chestKey := strconv.FormatUint(uint64(destiny.ChestArmor), 10)
	classKey := strconv.FormatUint(uint64(destiny.ClassArmor), 10)

	t.Run("matching Exotic Helmet marks high similarity and ignores Legendary armor swaps", func(t *testing.T) {
		targetLoadout := makeBaseTestLoadout("kin-1", "en-1", 3003, "pow-1", 4001)
		targetLoadout[helmetKey] = api.ItemSnapshot{
			InstanceID: "helm-inst-a",
			ItemHash:   999111, // St0mp-EE5 / Knucklehead
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{TierType: 6, TierTypeName: "Exotic"},
			},
		}
		targetLoadout[chestKey] = api.ItemSnapshot{
			InstanceID: "chest-leg-a",
			ItemHash:   555001,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{TierType: 5, TierTypeName: "Legendary"},
			},
		}

		candLoadout := makeBaseTestLoadout("kin-1", "en-1", 3003, "pow-2", 4002)
		candLoadout[helmetKey] = api.ItemSnapshot{
			InstanceID: "helm-inst-b", // Different copy of the same standard Exotic armor still matches by ItemHash!
			ItemHash:   999111,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{TierType: 6, TierTypeName: "Exotic"},
			},
		}
		candLoadout[chestKey] = api.ItemSnapshot{
			InstanceID: "chest-leg-b", // Swapped Legendary chest piece ignored!
			ItemHash:   555999,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{TierType: 5, TierTypeName: "Legendary"},
			},
		}

		target := &api.CharacterSnapshot{
			ID:      "target",
			Loadout: targetLoadout,
			Stats: &map[string]api.ClassStat{
				"1943323491": {Name: "Recovery", Value: 90},
				"392767087":  {Name: "Resilience", Value: 70},
				"2996146975": {Name: "Mobility", Value: 100},
				"1935470627": {Name: "Power", Value: 2000},
			},
		}
		cand := &api.CharacterSnapshot{
			ID:      "cand",
			Loadout: candLoadout,
			Stats: &map[string]api.ClassStat{
				"1943323491": {Name: "Recovery", Value: 100},
				"392767087":  {Name: "Resilience", Value: 60},
				"2996146975": {Name: "Mobility", Value: 100},
				"1935470627": {Name: "Power", Value: 2010}, // Power level infusion delta must be ignored!
			},
		}

		res := CompareSimilarLoadout(target, cand, nil)
		if res.Tier != api.SimilarityTierHigh || !res.ExoticArmorMatch {
			t.Fatalf("expected high tier & exoticArmorMatch=true, got %+v", res)
		}
		if res.PowerWeaponMatch {
			t.Fatalf("expected powerWeaponMatch=false when Heavy swapped, got true")
		}
		if res.StatDiffs == nil {
			t.Fatal("expected non-nil statDiffs")
		}
		diffs := *res.StatDiffs
		if len(diffs) != 2 || diffs["Recovery"] != 10 || diffs["Resilience"] != -10 {
			t.Fatalf("expected {Recovery: 10, Resilience: -10} (omitting 0-delta Mobility and Power level), got %+v", diffs)
		}
	})

	t.Run("swapped Exotic armor marks base similarity", func(t *testing.T) {
		targetLoadout := makeBaseTestLoadout("kin-1", "en-1", 3003, "pow-1", 4001)
		targetLoadout[helmetKey] = api.ItemSnapshot{
			InstanceID: "helm-1",
			ItemHash:   999111,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{TierType: 6},
			},
		}
		candLoadout := makeBaseTestLoadout("kin-1", "en-1", 3003, "pow-1", 4001)
		candLoadout[helmetKey] = api.ItemSnapshot{
			InstanceID: "helm-2",
			ItemHash:   999222,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{TierType: 6},
			},
		}

		res := CompareSimilarLoadout(&api.CharacterSnapshot{Loadout: targetLoadout}, &api.CharacterSnapshot{Loadout: candLoadout}, nil)
		if res.Tier != api.SimilarityTierBase || res.ExoticArmorMatch {
			t.Fatalf("expected base tier & exoticArmorMatch=false, got %+v", res)
		}
		if !res.PowerWeaponMatch {
			t.Fatalf("expected powerWeaponMatch=true, got false")
		}
		if res.StatDiffs != nil {
			t.Fatalf("expected nil statDiffs when identical, got %+v", res.StatDiffs)
		}
	})

	t.Run("neither loadout equipping Exotic armor marks high similarity", func(t *testing.T) {
		targetLoadout := makeBaseTestLoadout("kin-1", "en-1", 3003, "pow-1", 4001)
		candLoadout := makeBaseTestLoadout("kin-1", "en-1", 3003, "pow-1", 4001)

		res := CompareSimilarLoadout(&api.CharacterSnapshot{Loadout: targetLoadout}, &api.CharacterSnapshot{Loadout: candLoadout}, nil)
		if res.Tier != api.SimilarityTierHigh || !res.ExoticArmorMatch {
			t.Fatalf("expected high tier when neither loadout has Exotic armor, got %+v", res)
		}
	})

	t.Run("Prismatic Exotic Class Items compare real InstanceID (including server-tick format) and perk fallback", func(t *testing.T) {
		const relativismHash int64 = 2809120022

		targetLoadout := makeBaseTestLoadout("kin-1", "en-1", 3003, "pow-1", 4001)
		// server-tick style: top-level InstanceID == itemHash string, real ID on BaseInfo.InstanceId
		targetLoadout[classKey] = api.ItemSnapshot{
			InstanceID: strconv.FormatInt(relativismHash, 10),
			ItemHash:   relativismHash,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{InstanceId: "6917530110359965980", TierType: 6},
				Perks:    []api.Perk{{Hash: 101}, {Hash: 202}},
			},
		}

		diffRollLoadout := makeBaseTestLoadout("kin-1", "en-1", 3003, "pow-1", 4001)
		diffRollLoadout[classKey] = api.ItemSnapshot{
			InstanceID: strconv.FormatInt(relativismHash, 10),
			ItemHash:   relativismHash,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{InstanceId: "6917530999999999999", TierType: 6},
				Perks:    []api.Perk{{Hash: 101}, {Hash: 303}},
			},
		}

		sameRollBackendLoadout := makeBaseTestLoadout("kin-1", "en-1", 3003, "pow-1", 4001)
		sameRollBackendLoadout[classKey] = api.ItemSnapshot{
			InstanceID: "6917530110359965980",
			ItemHash:   relativismHash,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{InstanceId: "6917530110359965980", TierType: 6},
				Perks:    []api.Perk{{Hash: 202}, {Hash: 101}},
			},
		}

		resDiff := CompareSimilarLoadout(&api.CharacterSnapshot{Loadout: targetLoadout}, &api.CharacterSnapshot{Loadout: diffRollLoadout}, nil)
		if resDiff.Tier != api.SimilarityTierBase || resDiff.ExoticArmorMatch {
			t.Fatalf("expected different Relativism rolls to be base similarity, got %+v", resDiff)
		}

		resSame := CompareSimilarLoadout(&api.CharacterSnapshot{Loadout: targetLoadout}, &api.CharacterSnapshot{Loadout: sameRollBackendLoadout}, nil)
		if resSame.Tier != api.SimilarityTierHigh || !resSame.ExoticArmorMatch {
			t.Fatalf("expected identical Relativism roll across server-tick/backend to be high similarity, got %+v", resSame)
		}

		// Fallback to sorted perk hashes when InstanceID is missing
		noInstA := makeBaseTestLoadout("kin-1", "en-1", 3003, "pow-1", 4001)
		noInstA[classKey] = api.ItemSnapshot{
			ItemHash: relativismHash,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{TierType: 6},
				Perks:    []api.Perk{{Hash: 202}, {Hash: 101}},
			},
		}
		noInstB := makeBaseTestLoadout("kin-1", "en-1", 3003, "pow-1", 4001)
		noInstB[classKey] = api.ItemSnapshot{
			ItemHash: relativismHash,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{TierType: 6},
				Perks:    []api.Perk{{Hash: 101}, {Hash: 999}},
			},
		}
		if r := CompareSimilarLoadout(&api.CharacterSnapshot{Loadout: noInstA}, &api.CharacterSnapshot{Loadout: noInstB}, nil); r.Tier != api.SimilarityTierBase {
			t.Fatalf("expected perk-hash fallback to mark differing rolls as base, got %+v", r)
		}
	})

	t.Run("legacy tierType == 0 falls back to tierLookup", func(t *testing.T) {
		targetLoadout := makeBaseTestLoadout("kin-1", "en-1", 3003, "pow-1", 4001)
		targetLoadout[helmetKey] = api.ItemSnapshot{
			InstanceID: "legacy-helm-1",
			ItemHash:   777001, // TierType omitted (0)
		}
		candLoadout := makeBaseTestLoadout("kin-1", "en-1", 3003, "pow-1", 4001)
		candLoadout[helmetKey] = api.ItemSnapshot{
			InstanceID: "legacy-helm-2",
			ItemHash:   777002, // Different Exotic helmet, TierType omitted (0)
		}

		lookup := func(hash int64) int32 {
			if hash == 777001 || hash == 777002 {
				return 6
			}
			return 5
		}

		res := CompareSimilarLoadout(&api.CharacterSnapshot{Loadout: targetLoadout}, &api.CharacterSnapshot{Loadout: candLoadout}, lookup)
		if res.Tier != api.SimilarityTierBase || res.ExoticArmorMatch {
			t.Fatalf("expected legacy tierType==0 Exotic mismatch to resolve to base via tierLookup, got %+v", res)
		}
	})
}

func TestCompareSimilarLoadoutArmorSetBonuses(t *testing.T) {
	helmetKey := strconv.FormatUint(uint64(destiny.HelmetArmor), 10)
	makeSnap := func(exoticHelmHash int64, bonuses []api.ArmorSetBonus) *api.CharacterSnapshot {
		lo := makeBaseTestLoadout("kin-1", "en-1", 3003, "pow-1", 4001)
		if exoticHelmHash != 0 {
			lo[helmetKey] = api.ItemSnapshot{
				InstanceID: "helm",
				ItemHash:   exoticHelmHash,
				ItemProperties: api.ItemProperties{
					BaseInfo: api.BaseItemInfo{TierType: 6},
				},
			}
		}
		var bonusesPtr *[]api.ArmorSetBonus
		if bonuses != nil {
			bonusesPtr = &bonuses
		}
		return &api.CharacterSnapshot{
			Loadout:         lo,
			ArmorSetBonuses: bonusesPtr,
		}
	}

	setA2pc := api.ArmorSetBonus{
		SetHash:     1000,
		SetName:     "Set A",
		ActiveCount: 2,
		ActivePerks: []api.ArmorSetBonusPerk{{RequiredSetCount: 2, Hash: 1002, Name: "A 2pc"}},
	}
	setA3pc := api.ArmorSetBonus{
		SetHash:     1000,
		SetName:     "Set A",
		ActiveCount: 3,
		ActivePerks: []api.ArmorSetBonusPerk{{RequiredSetCount: 2, Hash: 1002, Name: "A 2pc"}},
	}
	setA4pc := api.ArmorSetBonus{
		SetHash:     1000,
		SetName:     "Set A",
		ActiveCount: 4,
		ActivePerks: []api.ArmorSetBonusPerk{
			{RequiredSetCount: 2, Hash: 1002, Name: "A 2pc"},
			{RequiredSetCount: 4, Hash: 1004, Name: "A 4pc"},
		},
	}
	setA5pc := api.ArmorSetBonus{
		SetHash:     1000,
		SetName:     "Set A",
		ActiveCount: 5,
		ActivePerks: []api.ArmorSetBonusPerk{
			{RequiredSetCount: 2, Hash: 1002, Name: "A 2pc"},
			{RequiredSetCount: 4, Hash: 1004, Name: "A 4pc"},
		},
	}
	setB2pc := api.ArmorSetBonus{
		SetHash:     2000,
		SetName:     "Set B",
		ActiveCount: 2,
		ActivePerks: []api.ArmorSetBonusPerk{{RequiredSetCount: 2, Hash: 2002, Name: "B 2pc"}},
	}
	setB3pc := api.ArmorSetBonus{
		SetHash:     2000,
		SetName:     "Set B",
		ActiveCount: 3,
		ActivePerks: []api.ArmorSetBonusPerk{{RequiredSetCount: 2, Hash: 2002, Name: "B 2pc"}},
	}
	setC2pc := api.ArmorSetBonus{
		SetHash:     3000,
		SetName:     "Set C",
		ActiveCount: 2,
		ActivePerks: []api.ArmorSetBonusPerk{{RequiredSetCount: 2, Hash: 3002, Name: "C 2pc"}},
	}

	tests := []struct {
		name      string
		target    *api.CharacterSnapshot
		candidate *api.CharacterSnapshot
		wantMatch bool
		wantTier  api.SimilarityTier
	}{
		{
			name:      "none vs none matches",
			target:    makeSnap(555, nil),
			candidate: makeSnap(555, nil),
			wantMatch: true,
			wantTier:  api.SimilarityTierHigh,
		},
		{
			name:      "none vs empty slice matches",
			target:    makeSnap(555, nil),
			candidate: makeSnap(555, []api.ArmorSetBonus{}),
			wantMatch: true,
			wantTier:  api.SimilarityTierHigh,
		},
		{
			name:      "2pc vs 3pc same set matches",
			target:    makeSnap(555, []api.ArmorSetBonus{setA2pc}),
			candidate: makeSnap(555, []api.ArmorSetBonus{setA3pc}),
			wantMatch: true,
			wantTier:  api.SimilarityTierHigh,
		},
		{
			name:      "4pc vs 5pc same set matches",
			target:    makeSnap(555, []api.ArmorSetBonus{setA4pc}),
			candidate: makeSnap(555, []api.ArmorSetBonus{setA5pc}),
			wantMatch: true,
			wantTier:  api.SimilarityTierHigh,
		},
		{
			name:      "2pc vs 4pc same set differs without altering SimilarityTier",
			target:    makeSnap(555, []api.ArmorSetBonus{setA2pc}),
			candidate: makeSnap(555, []api.ArmorSetBonus{setA4pc}),
			wantMatch: false,
			wantTier:  api.SimilarityTierHigh,
		},
		{
			name:      "dual 2pc Set A + 2pc Set B matches 3pc Set B + 2pc Set A regardless of slice order",
			target:    makeSnap(555, []api.ArmorSetBonus{setA2pc, setB2pc}),
			candidate: makeSnap(555, []api.ArmorSetBonus{setB3pc, setA2pc}),
			wantMatch: true,
			wantTier:  api.SimilarityTierHigh,
		},
		{
			name:      "dual 2pc Set A + 2pc Set B differs from single 2pc Set A",
			target:    makeSnap(555, []api.ArmorSetBonus{setA2pc, setB2pc}),
			candidate: makeSnap(555, []api.ArmorSetBonus{setA2pc}),
			wantMatch: false,
			wantTier:  api.SimilarityTierHigh,
		},
		{
			name:      "dual 2pc Set A + 2pc Set B differs from 2pc Set A + 2pc Set C",
			target:    makeSnap(555, []api.ArmorSetBonus{setA2pc, setB2pc}),
			candidate: makeSnap(555, []api.ArmorSetBonus{setA2pc, setC2pc}),
			wantMatch: false,
			wantTier:  api.SimilarityTierHigh,
		},
		{
			name:      "dual 2pc Set A + 2pc Set B differs from 4pc Set A",
			target:    makeSnap(555, []api.ArmorSetBonus{setA2pc, setB2pc}),
			candidate: makeSnap(555, []api.ArmorSetBonus{setA4pc}),
			wantMatch: false,
			wantTier:  api.SimilarityTierHigh,
		},
		{
			name:      "matching set bonuses do not upgrade base tier when Exotic armor differs",
			target:    makeSnap(555, []api.ArmorSetBonus{setA2pc, setB2pc}),
			candidate: makeSnap(666, []api.ArmorSetBonus{setA2pc, setB2pc}),
			wantMatch: true,
			wantTier:  api.SimilarityTierBase,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompareSimilarLoadout(tt.target, tt.candidate, nil)
			if got.ArmorSetBonusMatch != tt.wantMatch {
				t.Fatalf("ArmorSetBonusMatch = %v, want %v", got.ArmorSetBonusMatch, tt.wantMatch)
			}
			if got.Tier != tt.wantTier {
				t.Fatalf("Tier = %q, want %q", got.Tier, tt.wantTier)
			}
		})
	}
}

func TestComputeSimilarCountsSkipsArchivedAndIncomplete(t *testing.T) {
	snaps := []api.CharacterSnapshot{
		{ID: "a", Loadout: makeBaseTestLoadout("kin-1", "en-1", 3003, "pow-1", 4001)},
		{ID: "b", Loadout: makeBaseTestLoadout("kin-1", "en-1", 3003, "pow-2", 4002)},
		{ID: "archived", MergedIntoID: new("a"), Loadout: makeBaseTestLoadout("kin-1", "en-1", 3003, "pow-3", 4003)},
		{ID: "empty-kin", Loadout: makeBaseTestLoadout("", "en-1", 3003, "pow-1", 4001)},
		{ID: "empty-kin-2", Loadout: makeBaseTestLoadout("", "en-1", 3003, "pow-1", 4001)},
	}

	simCounts, groups := ComputeSimilarCounts(snaps)
	if simCounts["a"] != 1 || simCounts["b"] != 1 {
		t.Fatalf("expected simCounts[a]=1 and simCounts[b]=1 (excluding archived), got %+v", simCounts)
	}
	if _, ok := simCounts["archived"]; ok {
		t.Fatalf("expected archived snapshot excluded from similarCounts, got %+v", simCounts)
	}
	if _, ok := simCounts["empty-kin"]; ok {
		t.Fatalf("expected incomplete empty-instanceId loadouts not to match ''=='', got %+v", simCounts)
	}
	if len(groups["a"]) != 2 {
		t.Fatalf("expected peerGroup size 2 for 'a', got %+v", groups["a"])
	}
}

type fakeStatsStore struct {
	matches          []CharacterMatchStat
	err              error
	fn               func(ctx context.Context, characterID string, gameMode string) ([]CharacterMatchStat, error)
	calls            atomic.Int32
	capturedCharID   string
	capturedGameMode string
}

var _ Store = (*fakeStatsStore)(nil)

func (f *fakeStatsStore) ListCharacterMatchStats(ctx context.Context, characterID string, gameMode string) ([]CharacterMatchStat, error) {
	f.calls.Add(1)
	f.capturedCharID = characterID
	f.capturedGameMode = gameMode
	if f.fn != nil {
		return f.fn(ctx, characterID, gameMode)
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.matches, nil
}

type fakeSnapshotService struct {
	snapshot.Service
	byID       map[string]*api.CharacterSnapshot
	all        []api.CharacterSnapshot
	getErr     error
	getAllErr  error
	getByIDsFn func(ctx context.Context, ids []string) ([]api.CharacterSnapshot, error)
	getAllFn   func(ctx context.Context, userID, characterID string, count, offset int) ([]api.CharacterSnapshot, error)
	getAllHits atomic.Int32
}

func (f *fakeSnapshotService) Get(_ context.Context, snapshotID string) (*api.CharacterSnapshot, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if s, ok := f.byID[snapshotID]; ok {
		return s, nil
	}
	return nil, snapshot.NotFound
}

func (f *fakeSnapshotService) GetAllByCharacter(ctx context.Context, userID, characterID string, count, offset int) ([]api.CharacterSnapshot, error) {
	f.getAllHits.Add(1)
	if f.getAllFn != nil {
		return f.getAllFn(ctx, userID, characterID, count, offset)
	}
	if f.getAllErr != nil {
		return nil, f.getAllErr
	}
	return f.all, nil
}

func (f *fakeSnapshotService) GetByIDs(ctx context.Context, ids []string) ([]api.CharacterSnapshot, error) {
	if f.getByIDsFn != nil {
		return f.getByIDsFn(ctx, ids)
	}
	out := make([]api.CharacterSnapshot, 0, len(ids))
	for _, id := range ids {
		if s, ok := f.byID[id]; ok {
			out = append(out, *s)
		}
	}
	return out, nil
}

func TestGetBestPerformingLoadoutsEmpty(t *testing.T) {
	s := NewService(nil, &fakeStatsStore{}, nil, nil, nil)
	loadouts, perfStats, counts, err := s.GetBestPerformingLoadouts(context.Background(), "char-1", nil, 3, 10)
	if err != nil {
		t.Fatalf("expected nil error when no loadouts found, got %v", err)
	}
	if len(loadouts) != 0 || len(perfStats) != 0 || len(counts) != 0 {
		t.Fatalf("expected empty loadouts/stats/counts, got loadouts=%v stats=%v counts=%v", loadouts, perfStats, counts)
	}
}

func TestGetBestPerformingLoadoutsWithFakeStore(t *testing.T) {
	t.Run("filters min games, archived, deleted snapshots, and backfills to limit without orphaned map keys", func(t *testing.T) {
		mode := api.GameModeTrials
		store := &fakeStatsStore{
			matches: []CharacterMatchStat{
				// snap-archived: 2 games, 100 KD -> ranked #1, archived
				{SnapshotID: "snap-archived", Kills: 50, Deaths: 1, Assists: 0, Won: true},
				{SnapshotID: "snap-archived", Kills: 50, Deaths: 0, Assists: 0, Won: true},
				// snap-deleted: 2 games, 20 KD -> ranked #2, missing from snapshot store (omitted by GetByIDs)
				{SnapshotID: "snap-deleted", Kills: 20, Deaths: 1, Assists: 0, Won: true},
				{SnapshotID: "snap-deleted", Kills: 20, Deaths: 1, Assists: 0, Won: true},
				// snap-top: 2 games, 30 kills, 10 deaths (3.0 KD), 1 win -> ranked #3 (1st active)
				{SnapshotID: "snap-top", Kills: 20, Deaths: 5, Assists: 4, Won: true},
				{SnapshotID: "snap-top", Kills: 10, Deaths: 5, Assists: 2, Won: false},
				// snap-zero-partial: 2 games, 4 kills, 2 deaths (2.0 KD) -> ranked #4 (2nd active, outside initial limit=2 slice)
				{SnapshotID: "snap-zero-partial", Kills: 0, Deaths: 0, Assists: 0, Won: false},
				{SnapshotID: "snap-zero-partial", Kills: 4, Deaths: 2, Assists: 0, Won: false},
				// snap-third-active: 2 games, 2 kills, 2 deaths (1.0 KD) -> ranked #5 (should not be returned when limit=2)
				{SnapshotID: "snap-third-active", Kills: 1, Deaths: 1, Assists: 0, Won: false},
				{SnapshotID: "snap-third-active", Kills: 1, Deaths: 1, Assists: 0, Won: false},
				// snap-below-min: 1 game -> filtered out by minimumGames=2
				{SnapshotID: "snap-below-min", Kills: 99, Deaths: 1, Assists: 0, Won: true},
				// blank snapshotID ignored
				{SnapshotID: "", Kills: 50, Deaths: 1, Assists: 0, Won: true},
			},
		}
		snaps := &fakeSnapshotService{
			byID: map[string]*api.CharacterSnapshot{
				"snap-top":          {ID: "snap-top", Name: "Top"},
				"snap-zero-partial": {ID: "snap-zero-partial", Name: "Partial"},
				"snap-third-active": {ID: "snap-third-active", Name: "Third"},
				"snap-archived":     {ID: "snap-archived", Name: "Archived", MergedIntoID: new("snap-top")},
				"snap-below-min":    {ID: "snap-below-min", Name: "BelowMin"},
			},
		}
		s := NewService(nil, store, snaps, nil, nil)

		loadouts, perfStats, counts, err := s.GetBestPerformingLoadouts(context.Background(), "char-1", &mode, 2, 2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if store.capturedCharID != "char-1" || store.capturedGameMode != string(api.GameModeTrials) {
			t.Fatalf("expected store called with char-1 and trials mode, got char=%q mode=%q", store.capturedCharID, store.capturedGameMode)
		}
		if len(loadouts) != 2 || loadouts[0].ID != "snap-top" || loadouts[1].ID != "snap-zero-partial" {
			t.Fatalf("expected [snap-top, snap-zero-partial], got %+v", loadouts)
		}
		if len(perfStats) != 2 || len(counts) != 2 {
			t.Fatalf("expected stats/counts maps to contain only surviving loadouts, got len(stats)=%d len(counts)=%d", len(perfStats), len(counts))
		}
		for _, excluded := range []string{"snap-archived", "snap-deleted", "snap-third-active", "snap-below-min"} {
			if _, ok := perfStats[excluded]; ok {
				t.Fatalf("expected %s excluded from perfStats", excluded)
			}
			if _, ok := counts[excluded]; ok {
				t.Fatalf("expected %s excluded from counts", excluded)
			}
		}
		if counts["snap-top"] != 2 || *perfStats["snap-top"].Kd.Value != 3.0 || *perfStats["snap-top"].Standing.Value != 0.5 {
			t.Fatalf("unexpected snap-top stats: count=%d stats=%+v", counts["snap-top"], perfStats["snap-top"])
		}
		if counts["snap-zero-partial"] != 2 || *perfStats["snap-zero-partial"].Kd.Value != 2.0 || *perfStats["snap-zero-partial"].Standing.Value != 0.0 {
			t.Fatalf("unexpected snap-zero-partial stats: count=%d stats=%+v", counts["snap-zero-partial"], perfStats["snap-zero-partial"])
		}
	})

	t.Run("walks multiple GetByIDs batches when more than 10 leading candidates are archived or deleted", func(t *testing.T) {
		matches := make([]CharacterMatchStat, 0, 14)
		byID := make(map[string]*api.CharacterSnapshot, 14)

		for i := 0; i < 11; i++ {
			id := fmt.Sprintf("snap-skip-%02d", i)
			matches = append(matches, CharacterMatchStat{SnapshotID: id, Kills: 100 - i, Deaths: 1, Won: true})
			if i%2 == 0 {
				byID[id] = &api.CharacterSnapshot{ID: id, MergedIntoID: new("snap-active-1")}
			}
		}
		matches = append(matches,
			CharacterMatchStat{SnapshotID: "snap-active-1", Kills: 10, Deaths: 2, Won: true},
			CharacterMatchStat{SnapshotID: "snap-active-2", Kills: 8, Deaths: 2, Won: false},
			CharacterMatchStat{SnapshotID: "snap-active-3", Kills: 6, Deaths: 2, Won: false},
		)
		byID["snap-active-1"] = &api.CharacterSnapshot{ID: "snap-active-1", Name: "Active 1"}
		byID["snap-active-2"] = &api.CharacterSnapshot{ID: "snap-active-2", Name: "Active 2"}
		byID["snap-active-3"] = &api.CharacterSnapshot{ID: "snap-active-3", Name: "Active 3"}

		var batchCalls int
		snaps := &fakeSnapshotService{
			byID: byID,
			getByIDsFn: func(_ context.Context, ids []string) ([]api.CharacterSnapshot, error) {
				batchCalls++
				out := make([]api.CharacterSnapshot, 0, len(ids))
				for i := len(ids) - 1; i >= 0; i-- {
					if s, ok := byID[ids[i]]; ok {
						out = append(out, *s)
					}
				}
				return out, nil
			},
		}
		store := &fakeStatsStore{matches: matches}
		s := NewService(nil, store, snaps, nil, nil)

		loadouts, perfStats, counts, err := s.GetBestPerformingLoadouts(context.Background(), "char-1", new(api.GameModeAll), 2, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if store.capturedGameMode != "" {
			t.Fatalf("expected allGameModes normalized to empty string, got %q", store.capturedGameMode)
		}
		if batchCalls != 2 {
			t.Fatalf("expected 2 GetByIDs batch calls, got %d", batchCalls)
		}
		if len(loadouts) != 2 || loadouts[0].ID != "snap-active-1" || loadouts[1].ID != "snap-active-2" {
			t.Fatalf("expected [snap-active-1, snap-active-2] in ranked order, got %+v", loadouts)
		}
		if len(perfStats) != 2 || len(counts) != 2 || counts["snap-active-1"] != 1 || counts["snap-active-2"] != 1 {
			t.Fatalf("unexpected stats/counts maps: stats=%+v counts=%+v", perfStats, counts)
		}
	})
}

func TestGetSnapshotsWithMetricsConcurrentFetchAndFastPath(t *testing.T) {
	now := time.Now().UTC()
	baseSnaps := []api.CharacterSnapshot{
		{ID: "snap-1", CharacterID: "char-1", CreatedAt: now.Add(-time.Hour), Loadout: makeBaseTestLoadout("k1", "e1", 1, "p1", 10)},
		{ID: "snap-2", CharacterID: "char-1", CreatedAt: now, Loadout: makeBaseTestLoadout("k2", "e2", 1, "p2", 10)},
	}

	t.Run("runs snapshot and projected match stats queries concurrently when needsAggs is true", func(t *testing.T) {
		snapStarted := make(chan struct{})
		storeStarted := make(chan struct{})

		snaps := &fakeSnapshotService{
			getAllFn: func(ctx context.Context, _, _ string, _, _ int) ([]api.CharacterSnapshot, error) {
				close(snapStarted)
				select {
				case <-storeStarted:
					return baseSnaps, nil
				case <-time.After(2 * time.Second):
					return nil, errors.New("timed out waiting for store query to run concurrently")
				}
			},
		}
		store := &fakeStatsStore{
			fn: func(ctx context.Context, _ string, _ string) ([]CharacterMatchStat, error) {
				close(storeStarted)
				select {
				case <-snapStarted:
					return []CharacterMatchStat{
						{SnapshotID: "snap-1", Kills: 20, Deaths: 5, Assists: 4, Won: true},
						{SnapshotID: "snap-2", Kills: 5, Deaths: 10, Assists: 1, Won: false},
					}, nil
				case <-time.After(2 * time.Second):
					return nil, errors.New("timed out waiting for snapshot query to run concurrently")
				}
			},
		}

		svc := NewService(nil, store, snaps, nil, nil)
		items, statsMap, countsMap, _, err := svc.GetSnapshotsWithMetrics(context.Background(), "user-1", "char-1", nil, 0, "kd_ratio", true, false, 10, 0)
		if err != nil {
			t.Fatalf("GetSnapshotsWithMetrics failed: %v", err)
		}
		if len(items) != 2 || items[0].ID != "snap-1" || items[1].ID != "snap-2" {
			t.Fatalf("expected [snap-1, snap-2] sorted by kd_ratio, got %+v", items)
		}
		if statsMap == nil || countsMap == nil || (*countsMap)["snap-1"] != 1 || *(*statsMap)["snap-1"].Kd.Value != 4.0 {
			t.Fatalf("unexpected stats/counts: stats=%+v counts=%+v", statsMap, countsMap)
		}
	})

	t.Run("fast path skips statsStore when needsAggs is false and sortBy is created_at", func(t *testing.T) {
		store := &fakeStatsStore{}
		snaps := &fakeSnapshotService{all: baseSnaps}
		svc := NewService(nil, store, snaps, nil, nil)

		items, statsMap, countsMap, _, err := svc.GetSnapshotsWithMetrics(context.Background(), "user-1", "char-1", nil, 0, "created_at", false, false, 10, 0)
		if err != nil {
			t.Fatalf("fast-path GetSnapshotsWithMetrics failed: %v", err)
		}
		if store.calls.Load() != 0 {
			t.Fatalf("expected statsStore not to be called on fast path, got %d calls", store.calls.Load())
		}
		if len(items) != 2 || statsMap != nil || countsMap != nil {
			t.Fatalf("unexpected fast-path response: items=%d stats=%v counts=%v", len(items), statsMap, countsMap)
		}
	})

	t.Run("cancels peer goroutine and returns error when statsStore fails", func(t *testing.T) {
		wantErr := errors.New("projected query failed")
		snaps := &fakeSnapshotService{
			getAllFn: func(ctx context.Context, _, _ string, _, _ int) ([]api.CharacterSnapshot, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		}
		store := &fakeStatsStore{err: wantErr}
		svc := NewService(nil, store, snaps, nil, nil)

		_, _, _, _, err := svc.GetSnapshotsWithMetrics(context.Background(), "user-1", "char-1", nil, 0, "kd_ratio", true, false, 10, 0)
		if !errors.Is(err, wantErr) {
			t.Fatalf("expected error %v, got %v", wantErr, err)
		}
	})
}

func TestGetSimilarSnapshotsUsesProjectedStoreAndConcurrentFetch(t *testing.T) {
	now := time.Now().UTC()
	target := &api.CharacterSnapshot{
		ID:          "snap-target",
		UserID:      "user-1",
		CharacterID: "char-1",
		CreatedAt:   now,
		Loadout:     makeBaseTestLoadout("kin-1", "en-1", 3003, "pow-1", 4001),
	}
	cand := api.CharacterSnapshot{
		ID:          "snap-cand",
		UserID:      "user-1",
		CharacterID: "char-1",
		CreatedAt:   now.Add(-time.Minute),
		Loadout:     makeBaseTestLoadout("kin-1", "en-1", 3003, "pow-2", 4002),
	}

	t.Run("active target fetches projected match stats and character snapshots concurrently", func(t *testing.T) {
		snapStarted := make(chan struct{})
		storeStarted := make(chan struct{})

		snaps := &fakeSnapshotService{
			byID: map[string]*api.CharacterSnapshot{target.ID: target},
			getAllFn: func(ctx context.Context, _, _ string, _, _ int) ([]api.CharacterSnapshot, error) {
				close(snapStarted)
				select {
				case <-storeStarted:
					return []api.CharacterSnapshot{*target, cand}, nil
				case <-time.After(2 * time.Second):
					return nil, errors.New("timed out waiting for store query in GetSimilarSnapshots")
				}
			},
		}
		store := &fakeStatsStore{
			fn: func(ctx context.Context, _ string, _ string) ([]CharacterMatchStat, error) {
				close(storeStarted)
				select {
				case <-snapStarted:
					return []CharacterMatchStat{
						{SnapshotID: target.ID, Kills: 12, Deaths: 6, Assists: 3, Won: true},
						{SnapshotID: cand.ID, Kills: 10, Deaths: 5, Assists: 2, Won: false},
					}, nil
				case <-time.After(2 * time.Second):
					return nil, errors.New("timed out waiting for GetAllByCharacter in GetSimilarSnapshots")
				}
			},
		}

		svc := NewService(nil, store, snaps, nil, nil)
		gotTarget, items, statsMap, countsMap, err := svc.GetSimilarSnapshots(context.Background(), target.ID)
		if err != nil {
			t.Fatalf("GetSimilarSnapshots failed: %v", err)
		}
		if gotTarget.ID != target.ID || len(items) != 1 || items[0].Snapshot.ID != cand.ID {
			t.Fatalf("unexpected GetSimilarSnapshots output: target=%v items=%+v", gotTarget, items)
		}
		if (*countsMap)[target.ID] != 1 || (*countsMap)[cand.ID] != 1 || *(*statsMap)[target.ID].Kd.Value != 2.0 {
			t.Fatalf("unexpected stats/counts: stats=%+v counts=%+v", statsMap, countsMap)
		}
	})

	t.Run("archived target skips GetAllByCharacter and still returns projected stats", func(t *testing.T) {
		archived := &api.CharacterSnapshot{
			ID:           "snap-arch",
			UserID:       "user-1",
			CharacterID:  "char-1",
			MergedIntoID: new("snap-target"),
		}
		snaps := &fakeSnapshotService{
			byID: map[string]*api.CharacterSnapshot{archived.ID: archived},
		}
		store := &fakeStatsStore{
			matches: []CharacterMatchStat{
				{SnapshotID: archived.ID, Kills: 8, Deaths: 4, Assists: 2, Won: true},
			},
		}
		svc := NewService(nil, store, snaps, nil, nil)
		gotTarget, items, statsMap, countsMap, err := svc.GetSimilarSnapshots(context.Background(), archived.ID)
		if err != nil {
			t.Fatalf("GetSimilarSnapshots(archived) failed: %v", err)
		}
		if snaps.getAllHits.Load() != 0 {
			t.Fatalf("expected archived target not to call GetAllByCharacter, got %d", snaps.getAllHits.Load())
		}
		if gotTarget.ID != archived.ID || len(items) != 0 || (*countsMap)[archived.ID] != 1 || *(*statsMap)[archived.ID].Kd.Value != 2.0 {
			t.Fatalf("unexpected archived target result: items=%+v stats=%+v counts=%+v", items, statsMap, countsMap)
		}
	})
}
