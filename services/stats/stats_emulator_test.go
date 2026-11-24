package stats_test

import (
	"context"
	"fmt"
	firestoredriven "oneTrick/adapters/driven/firestore"
	"oneTrick/api"
	"oneTrick/services/aggregate"
	"oneTrick/services/destiny"
	"oneTrick/services/snapshot"
	"oneTrick/services/stats"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
)

type emulatorMockManifestService struct {
	destiny.ManifestService
	items     map[int64]*destiny.ItemDefinition
	callCount map[int64]int
}

func (m *emulatorMockManifestService) GetItem(_ context.Context, hash int64) (*destiny.ItemDefinition, error) {
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

func makeEmulatorBaseLoadout(kinID string, enID string, subclassHash int64, powerID string, powerHash int64) api.Loadout {
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
		lo := makeEmulatorBaseLoadout(kin, en, sub, "pow-"+id, 4001)
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
					EstimatedGameMode: new(mode),
					InstanceID:        aggID,
				},
				SnapshotLinks: map[string]api.SnapshotLink{
					charID: {
						CharacterID:      charID,
						SnapshotID:       new(snapID),
						ConfidenceLevel:  api.HighConfidenceLevel,
						ConfidenceSource: api.SystemConfidenceSource,
						CreatedAt:        now,
					},
				},
				Performance: map[string]api.InstancePerformance{
					charID: {
						PlayerStats: api.PlayerStats{
							Kills:    new(api.StatsValuePair{Value: new(killsPerGame)}),
							Deaths:   new(api.StatsValuePair{Value: new(deathsPerGame)}),
							Assists:  new(api.StatsValuePair{Value: new(2.0)}),
							Standing: new(api.StatsValuePair{Value: new(0.0)}),
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
	seedSnap(snapZ, 0, "kin-grp1", "en-grp1", 3003, 888001, 6, new(snapA))

	seedGames(snapA, 15, api.GameModeQuickPlay, 20, 10)
	seedGames(snapA, 5, api.GameModeTrials, 10, 5) // 20 total games across modes
	seedGames(snapB, 1, api.GameModeQuickPlay, 12, 6)
	seedGames(snapC, 2, api.GameModeTrials, 14, 7)
	seedGames(snapD, 3, api.GameModeQuickPlay, 15, 10)
	seedGames(snapE, 3, api.GameModeQuickPlay, 18, 9)
	seedGames(snapU, 8, api.GameModeQuickPlay, 16, 8)
	seedGames(snapZ, 10, api.GameModeQuickPlay, 30, 5)

	manifestMock := &emulatorMockManifestService{
		items: map[int64]*destiny.ItemDefinition{
			888001: {Hash: 888001, Inventory: destiny.Inventory{TierType: 6, TierTypeName: "Exotic"}},
			888002: {Hash: 888002, Inventory: destiny.Inventory{TierType: 6, TierTypeName: "Exotic"}},
		},
		callCount: make(map[int64]int),
	}

	aggSvc := aggregate.NewService(db)
	snapSvc := snapshot.NewService(db, nil, nil, aggSvc)
	statsSvc := stats.NewService(db, firestoredriven.NewStatsStore(db), snapSvc, nil, manifestMock)

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
