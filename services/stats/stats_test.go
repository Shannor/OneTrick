package stats

import (
	"context"
	"fmt"
	"oneTrick/api"
	"oneTrick/ptr"
	"oneTrick/services/aggregate"
	"oneTrick/services/destiny"
	"oneTrick/services/snapshot"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
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
		Kills: ptr.Of(api.StatsValuePair{
			DisplayValue: ptr.Of("25"),
			Value:        ptr.Of(25.0),
		}),
		Deaths: ptr.Of(api.StatsValuePair{
			DisplayValue: ptr.Of("10"),
			Value:        ptr.Of(10.0),
		}),
		Kd: ptr.Of(api.StatsValuePair{
			DisplayValue: ptr.Of("2.50"),
			Value:        ptr.Of(2.50),
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
			Mode:     ptr.Of(tt.mode),
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
		filtered := filterSnapshotsByGameModeAndMinGames(allSnapshots, counts, ptr.Of(api.GameModeCompetitive), 0)
		if len(filtered) != 2 || filtered[0].ID != "snap-three" || filtered[1].ID != "snap-six" {
			t.Errorf("expected [snap-three, snap-six], got %+v", filtered)
		}
	})

	t.Run("gameMode set with minimumGames=5 excludes snapshots with <5 games", func(t *testing.T) {
		filtered := filterSnapshotsByGameModeAndMinGames(allSnapshots, counts, ptr.Of(api.GameModeCompetitive), 5)
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
		filtered := filterSnapshotsByGameModeAndMinGames(allSnapshots, counts, ptr.Of(api.GameModeAll), 0)
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

func TestComputeSimilarCountsSkipsArchivedAndIncomplete(t *testing.T) {
	snaps := []api.CharacterSnapshot{
		{ID: "a", Loadout: makeBaseTestLoadout("kin-1", "en-1", 3003, "pow-1", 4001)},
		{ID: "b", Loadout: makeBaseTestLoadout("kin-1", "en-1", 3003, "pow-2", 4002)},
		{ID: "archived", MergedIntoID: ptr.Of("a"), Loadout: makeBaseTestLoadout("kin-1", "en-1", 3003, "pow-3", 4003)},
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

func TestFirestoreEmulatorSimilarLoadoutsAndFilterSafety(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("FIRESTORE_EMULATOR_HOST not set; skipping Firestore emulator integration test")
	}

	ctx := context.Background()
	db, err := firestore.NewClient(ctx, "gruntt-destiny-test")
	if err != nil {
		t.Fatalf("failed to create emulator firestore client: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	runID := fmt.Sprintf("sim-%d", time.Now().UnixNano())
	userID := "user-" + runID
	charID := "char-" + runID
	now := time.Now().UTC().Truncate(time.Millisecond)
	helmetKey := strconv.FormatUint(uint64(destiny.HelmetArmor), 10)

	var snapDocIDs []string
	var aggDocIDs []string
	t.Cleanup(func() {
		for _, id := range snapDocIDs {
			_, _ = db.Collection("snapshots").Doc(id).Delete(context.Background())
		}
		for _, id := range aggDocIDs {
			_, _ = db.Collection("aggregates").Doc(id).Delete(context.Background())
		}
	})

	seedSnap := func(id string, createdOffset time.Duration, kin, en string, sub int64, exoticHelmHash int64, tierInDoc int32, mergedInto *string) {
		lo := makeBaseTestLoadout(kin, en, sub, "pow-"+id, 4001)
		if exoticHelmHash != 0 {
			lo[helmetKey] = api.ItemSnapshot{
				InstanceID: "helm-" + id,
				ItemHash:   exoticHelmHash,
				ItemProperties: api.ItemProperties{
					BaseInfo: api.BaseItemInfo{TierType: tierInDoc},
				},
			}
		}
		s := api.CharacterSnapshot{
			ID:           id,
			UserID:       userID,
			CharacterID:  charID,
			Name:         "Loadout " + id,
			Hash:         "hash-" + id,
			CreatedAt:    now.Add(createdOffset),
			UpdatedAt:    now.Add(createdOffset),
			Loadout:      lo,
			MergedIntoID: mergedInto,
		}
		if _, err := db.Collection("snapshots").Doc(id).Set(ctx, s); err != nil {
			t.Fatalf("failed seeding snapshot %s: %v", id, err)
		}
		snapDocIDs = append(snapDocIDs, id)
	}

	aggCounter := 0
	seedGames := func(snapID string, n int, mode api.GameMode, killsPerGame, deathsPerGame float64) {
		for i := 0; i < n; i++ {
			aggCounter++
			aggID := fmt.Sprintf("%s-agg-%d", runID, aggCounter)
			agg := api.Aggregate{
				ID:           aggID,
				ActivityID:   aggID,
				CharacterIds: []string{charID},
				SnapshotIds:  []string{snapID},
				ActivityDetails: api.ActivityHistory{
					Activity:          string(mode),
					EstimatedGameMode: ptr.Of(mode),
					InstanceID:        aggID,
				},
				SnapshotLinks: map[string]api.SnapshotLink{
					charID: {
						CharacterID:      charID,
						SnapshotID:       ptr.Of(snapID),
						ConfidenceLevel:  api.HighConfidenceLevel,
						ConfidenceSource: api.SystemConfidenceSource,
						CreatedAt:        now,
					},
				},
				Performance: map[string]api.InstancePerformance{
					charID: {
						PlayerStats: api.PlayerStats{
							Kills:    ptr.Of(api.StatsValuePair{Value: ptr.Of(killsPerGame)}),
							Deaths:   ptr.Of(api.StatsValuePair{Value: ptr.Of(deathsPerGame)}),
							Assists:  ptr.Of(api.StatsValuePair{Value: ptr.Of(2.0)}),
							Standing: ptr.Of(api.StatsValuePair{Value: ptr.Of(0.0)}),
						},
					},
				},
			}
			if _, err := db.Collection("aggregates").Doc(aggID).Set(ctx, agg); err != nil {
				t.Fatalf("failed seeding aggregate %s: %v", aggID, err)
			}
			aggDocIDs = append(aggDocIDs, aggID)
		}
	}

	snapA := runID + "-A" // 20 games, legacy tierType=0 on Exotic Helm 888001
	snapB := runID + "-B" // 1 game, legacy tierType=0 on SAME Exotic Helm 888001 -> HIGH
	snapC := runID + "-C" // 2 games, legacy tierType=0 on DIFF Exotic Helm 888002 -> BASE
	snapD := runID + "-D" // 3 games, twin group 2
	snapE := runID + "-E" // 3 games, twin group 2 (3 + 3 = 6 combined games!)
	snapU := runID + "-U" // 8 games, unique weapons (no similar loadouts)
	snapZ := runID + "-Z" // 10 games, soft-archived into A!

	seedSnap(snapA, -1*time.Minute, "kin-grp1", "en-grp1", 3003, 888001, 0, nil)
	seedSnap(snapB, -2*time.Minute, "kin-grp1", "en-grp1", 3003, 888001, 0, nil)
	seedSnap(snapC, -3*time.Minute, "kin-grp1", "en-grp1", 3003, 888002, 0, nil)
	seedSnap(snapD, -4*time.Minute, "kin-grp2", "en-grp2", 3003, 888001, 6, nil)
	seedSnap(snapE, -5*time.Minute, "kin-grp2", "en-grp2", 3003, 888001, 6, nil)
	seedSnap(snapU, -6*time.Minute, "kin-uniq", "en-uniq", 3003, 888001, 6, nil)
	seedSnap(snapZ, 0, "kin-grp1", "en-grp1", 3003, 888001, 6, ptr.Of(snapA))

	seedGames(snapA, 15, api.GameModeQuickPlay, 20, 10)
	seedGames(snapA, 5, api.GameModeTrials, 10, 5) // 20 total games across modes
	seedGames(snapB, 1, api.GameModeQuickPlay, 12, 6)
	seedGames(snapC, 2, api.GameModeTrials, 14, 7)
	seedGames(snapD, 3, api.GameModeQuickPlay, 15, 10)
	seedGames(snapE, 3, api.GameModeQuickPlay, 18, 9)
	seedGames(snapU, 8, api.GameModeQuickPlay, 16, 8)
	seedGames(snapZ, 10, api.GameModeQuickPlay, 30, 5)

	manifestMock := &mockManifestService{
		items: map[int64]*destiny.ItemDefinition{
			888001: {Hash: 888001, Inventory: destiny.Inventory{TierType: 6, TierTypeName: "Exotic"}},
			888002: {Hash: 888002, Inventory: destiny.Inventory{TierType: 6, TierTypeName: "Exotic"}},
		},
		callCount: make(map[int64]int),
	}

	aggSvc := aggregate.NewService(db)
	snapSvc := snapshot.NewService(db, nil, nil, aggSvc)
	statsSvc := NewService(db, snapSvc, nil, manifestMock)

	// 1. Critical Regression Test: minimumGames = 5, hasSimilar = false
	// Loadout B (1 game) and C (2 games) are filtered out by minimumGames=5, yet Loadout A MUST still report similarCounts[A] == 2!
	items, _, counts, simCounts, err := statsSvc.GetSnapshotsWithMetrics(ctx, userID, charID, nil, 5, "matches_played", true, false, 10, 0)
	if err != nil {
		t.Fatalf("GetSnapshotsWithMetrics(minGames=5, hasSimilar=false) failed: %v", err)
	}
	if len(items) != 2 || items[0].ID != snapA || items[1].ID != snapU {
		t.Fatalf("expected [snapA, snapU] under minGames=5, got %+v", items)
	}
	if counts == nil || (*counts)[snapA] != 20 {
		t.Fatalf("expected counts[snapA]=20, got %+v", counts)
	}
	if simCounts == nil || (*simCounts)[snapA] != 2 {
		t.Fatalf("expected filter-safe similarCounts[snapA]=2 despite B & C having <5 games, got %+v", simCounts)
	}
	if _, exists := (*simCounts)[snapU]; exists {
		t.Fatalf("expected snapU omitted from similarCounts, got %+v", simCounts)
	}

	// 2. hasSimilar = true with minimumGames = 5 surfaces twin 3-game loadouts D & E (combined 6 >= 5) and excludes U & archived Z!
	simFiltered, _, _, simCountsFiltered, err := statsSvc.GetSnapshotsWithMetrics(ctx, userID, charID, nil, 5, "created_at", true, true, 10, 0)
	if err != nil {
		t.Fatalf("GetSnapshotsWithMetrics(minGames=5, hasSimilar=true) failed: %v", err)
	}
	gotIDs := make([]string, 0, len(simFiltered))
	for _, it := range simFiltered {
		gotIDs = append(gotIDs, it.ID)
	}
	for _, wantID := range []string{snapA, snapB, snapC, snapD, snapE} {
		if !slices.Contains(gotIDs, wantID) {
			t.Fatalf("expected %s surfaced under hasSimilar=true & minGames=5, got %v", wantID, gotIDs)
		}
	}
	if slices.Contains(gotIDs, snapU) || slices.Contains(gotIDs, snapZ) {
		t.Fatalf("expected unique U and archived Z excluded under hasSimilar=true, got %v", gotIDs)
	}
	if (*simCountsFiltered)[snapD] != 1 || (*simCountsFiltered)[snapE] != 1 {
		t.Fatalf("expected similarCounts=1 for twin loadouts D & E, got %+v", simCountsFiltered)
	}

	// 3. Fast-path (minimumGames=0, sortBy="created_at", includeStats=false) still computes similarCounts and honors hasSimilar=true!
	fastItems, _, _, fastSimCounts, err := statsSvc.GetSnapshotsWithMetrics(ctx, userID, charID, nil, 0, "created_at", false, true, 10, 0)
	if err != nil {
		t.Fatalf("fast-path GetSnapshotsWithMetrics(hasSimilar=true) failed: %v", err)
	}
	if len(fastItems) != 5 || (*fastSimCounts)[snapA] != 2 {
		t.Fatalf("unexpected fast-path hasSimilar response: len=%d simCounts=%+v", len(fastItems), fastSimCounts)
	}

	// 4. GetSimilarSnapshots(snapA):
	//    - Never hides 1-game/2-game candidates
	//    - Ranks B (high, 1 game) ahead of C (base, 2 games) via memoized ManifestService fallback for tierType==0!
	//    - Excludes archived Z
	//    - Returns all-mode global match counts (A=20 across Quickplay+Trials, B=1, C=2)
	target, simItems, simStats, simAllCounts, err := statsSvc.GetSimilarSnapshots(ctx, snapA)
	if err != nil {
		t.Fatalf("GetSimilarSnapshots(snapA) failed: %v", err)
	}
	if target.ID != snapA || len(simItems) != 2 {
		t.Fatalf("expected 2 similar items [B, C] for snapA, got %d: %+v", len(simItems), simItems)
	}
	if simItems[0].Snapshot.ID != snapB || simItems[0].Tier != api.SimilarityTierHigh {
		t.Fatalf("expected first candidate to be snapB (high tier), got %+v", simItems[0])
	}
	if simItems[1].Snapshot.ID != snapC || simItems[1].Tier != api.SimilarityTierBase {
		t.Fatalf("expected second candidate to be snapC (base tier), got %+v", simItems[1])
	}
	if (*simAllCounts)[snapA] != 20 || (*simAllCounts)[snapB] != 1 || (*simAllCounts)[snapC] != 2 {
		t.Fatalf("expected global counts {A:20, B:1, C:2}, got %+v", simAllCounts)
	}
	if simStats == nil || *(*simStats)[snapA].Kd.Value != 2.0 {
		t.Fatalf("expected target KD 2.0 in GetSimilarSnapshots stats, got %+v", simStats)
	}
	if manifestMock.callCount[888001] != 1 {
		t.Fatalf("expected memoized manifest lookup to query hash 888001 exactly once, got %d", manifestMock.callCount[888001])
	}

	// 5. GetFeaturedLoadouts excludes soft-archived snapshot Z even though Z has 10 games and newest createdAt!
	featured, err := statsSvc.GetFeaturedLoadouts(ctx, 5, nil)
	if err != nil {
		t.Fatalf("GetFeaturedLoadouts failed: %v", err)
	}
	for _, f := range featured {
		if f.Snapshot.ID == snapZ {
			t.Fatalf("expected soft-archived snapshot %s excluded from GetFeaturedLoadouts, but it was returned!", snapZ)
		}
	}
}

func TestGetBestPerformingLoadoutsEmpty(t *testing.T) {
	s := &service{}
	loadouts, perfStats, counts, err := s.GetBestPerformingLoadouts(context.Background(), nil, "char-1", 3, 10)
	if err != nil {
		t.Fatalf("expected nil error when no loadouts found, got %v", err)
	}
	if len(loadouts) != 0 || len(perfStats) != 0 || len(counts) != 0 {
		t.Fatalf("expected empty loadouts/stats/counts, got loadouts=%v stats=%v counts=%v", loadouts, perfStats, counts)
	}
}

