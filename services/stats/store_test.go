package stats

import (
	"context"
	"fmt"
	"oneTrick/api"
	"oneTrick/services/aggregate"
	"oneTrick/services/snapshot"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
)

func TestFirestoreEmulatorStatsStoreProjectionAndNilPlayerStats(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("FIRESTORE_EMULATOR_HOST not set; skipping Firestore emulator integration test")
	}

	ctx := context.Background()
	db, err := firestore.NewClient(ctx, "gruntt-destiny-test")
	if err != nil {
		t.Fatalf("failed to create emulator firestore client: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	runID := fmt.Sprintf("stats-store-%d", time.Now().UnixNano())
	userID := "user-" + runID
	charID := "char-" + runID
	otherCharID := "other-char-" + runID
	snapGood := "snap-good-" + runID
	snapPartial := "snap-partial-" + runID
	now := time.Now().UTC().Truncate(time.Millisecond)

	var snapIDs []string
	var aggIDs []string
	t.Cleanup(func() {
		for _, id := range snapIDs {
			_, _ = db.Collection("snapshots").Doc(id).Delete(context.Background())
		}
		for _, id := range aggIDs {
			_, _ = db.Collection("aggregates").Doc(id).Delete(context.Background())
		}
	})

	seedSnap := func(id, name string) {
		s := api.CharacterSnapshot{
			ID:          id,
			UserID:      userID,
			CharacterID: charID,
			Name:        name,
			Hash:        "hash-" + id,
			CreatedAt:   now,
			UpdatedAt:   now,
			Loadout:     api.Loadout{},
		}
		if _, err := db.Collection("snapshots").Doc(id).Set(ctx, s); err != nil {
			t.Fatalf("failed seeding snapshot %s: %v", id, err)
		}
		snapIDs = append(snapIDs, id)
	}

	seedSnap(snapGood, "Good Loadout")
	seedSnap(snapPartial, "Partial Stats Loadout")

	seedAgg := func(agg api.Aggregate) {
		if _, err := db.Collection("aggregates").Doc(agg.ID).Set(ctx, agg); err != nil {
			t.Fatalf("failed seeding aggregate %s: %v", agg.ID, err)
		}
		aggIDs = append(aggIDs, agg.ID)
	}

	// 1. Full rich Trials match with multiple lobby players, weapons, and extra stats
	seedAgg(api.Aggregate{
		ID:           runID + "-agg-trials-win",
		ActivityID:   "act-1",
		CharacterIds: []string{charID, otherCharID},
		SnapshotIds:  []string{snapGood},
		SessionIds:   []string{"sess-1"},
		CreatedAt:    now,
		ActivityDetails: api.ActivityHistory{
			InstanceID:        "act-1",
			Period:            now,
			Location:          "Javelin-4",
			Activity:          "Trials of Osiris",
			Mode:              new("Trials of Osiris"),
			EstimatedGameMode: new(api.GameModeTrials),
		},
		SnapshotLinks: map[string]api.SnapshotLink{
			charID: {
				CharacterID:      charID,
				SnapshotID:       new(snapGood),
				SessionID:        new("sess-1"),
				ConfidenceLevel:  api.HighConfidenceLevel,
				ConfidenceSource: api.SystemConfidenceSource,
				CreatedAt:        now,
			},
			otherCharID: {
				CharacterID:      otherCharID,
				SnapshotID:       new("other-snap"),
				ConfidenceLevel:  api.HighConfidenceLevel,
				ConfidenceSource: api.SystemConfidenceSource,
				CreatedAt:        now,
			},
		},
		Performance: map[string]api.InstancePerformance{
			charID: {
				PlayerStats: api.PlayerStats{
					Kills:    new(api.StatsValuePair{Value: new(15.0)}),
					Deaths:   new(api.StatsValuePair{Value: new(5.0)}),
					Assists:  new(api.StatsValuePair{Value: new(4.0)}),
					Standing: new(api.StatsValuePair{Value: new(0.0)}),
				},
				Weapons: map[string]api.WeaponInstanceMetrics{
					"111": {ReferenceID: new(int64(111))},
				},
				Extra: &map[string]api.UniqueStatValue{
					"precisionKills": {Basic: api.StatsValuePair{Value: new(9.0)}},
				},
			},
			otherCharID: {
				PlayerStats: api.PlayerStats{
					Kills:    new(api.StatsValuePair{Value: new(99.0)}),
					Deaths:   new(api.StatsValuePair{Value: new(1.0)}),
					Standing: new(api.StatsValuePair{Value: new(1.0)}),
				},
			},
		},
	})

	// 2. Regression case for production 500 panic: QuickPlay match with nil Standing (and nil Deaths/Assists)
	seedAgg(api.Aggregate{
		ID:           runID + "-agg-nil-standing",
		ActivityID:   "act-2",
		CharacterIds: []string{charID},
		SnapshotIds:  []string{snapPartial},
		CreatedAt:    now,
		ActivityDetails: api.ActivityHistory{
			InstanceID:        "act-2",
			Period:            now,
			Activity:          "Control",
			EstimatedGameMode: new(api.GameModeQuickPlay),
		},
		SnapshotLinks: map[string]api.SnapshotLink{
			charID: {
				CharacterID:      charID,
				SnapshotID:       new(snapPartial),
				ConfidenceLevel:  api.HighConfidenceLevel,
				ConfidenceSource: api.SystemConfidenceSource,
				CreatedAt:        now,
			},
		},
		Performance: map[string]api.InstancePerformance{
			charID: {
				PlayerStats: api.PlayerStats{
					Kills:    new(api.StatsValuePair{Value: new(8.0)}),
					Deaths:   nil,
					Assists:  nil,
					Standing: nil, // previously panicked at *performance.PlayerStats.Standing.Value
				},
			},
		},
	})

	// 3. Partial PlayerStats where Kills/Deaths/Assists/Standing all exist with nil Value pointers
	seedAgg(api.Aggregate{
		ID:           runID + "-agg-nil-values",
		ActivityID:   "act-3",
		CharacterIds: []string{charID},
		SnapshotIds:  []string{snapPartial},
		CreatedAt:    now,
		ActivityDetails: api.ActivityHistory{
			InstanceID:        "act-3",
			Period:            now,
			Activity:          "Control",
			EstimatedGameMode: new(api.GameModeQuickPlay),
		},
		SnapshotLinks: map[string]api.SnapshotLink{
			charID: {
				CharacterID:      charID,
				SnapshotID:       new(snapPartial),
				ConfidenceLevel:  api.HighConfidenceLevel,
				ConfidenceSource: api.SystemConfidenceSource,
				CreatedAt:        now,
			},
		},
		Performance: map[string]api.InstancePerformance{
			charID: {
				PlayerStats: api.PlayerStats{
					Kills:    new(api.StatsValuePair{Value: nil}),
					Deaths:   new(api.StatsValuePair{Value: new(4.0)}),
					Assists:  new(api.StatsValuePair{Value: nil}),
					Standing: new(api.StatsValuePair{Value: nil}),
				},
			},
		},
	})

	// 4. Unlinked match (nil SnapshotID) -> must be skipped
	seedAgg(api.Aggregate{
		ID:           runID + "-agg-unlinked",
		ActivityID:   "act-4",
		CharacterIds: []string{charID},
		CreatedAt:    now,
		ActivityDetails: api.ActivityHistory{
			InstanceID:        "act-4",
			Period:            now,
			EstimatedGameMode: new(api.GameModeQuickPlay),
		},
		SnapshotLinks: map[string]api.SnapshotLink{
			charID: {
				CharacterID:      charID,
				SnapshotID:       nil,
				ConfidenceLevel:  api.NoMatchConfidenceLevel,
				ConfidenceSource: api.SystemConfidenceSource,
				CreatedAt:        now,
			},
		},
		Performance: map[string]api.InstancePerformance{
			charID: {
				PlayerStats: api.PlayerStats{
					Kills: new(api.StatsValuePair{Value: new(20.0)}),
				},
			},
		},
	})

	// 5. Missing performance entry for charID -> must be skipped
	seedAgg(api.Aggregate{
		ID:           runID + "-agg-missing-perf",
		ActivityID:   "act-5",
		CharacterIds: []string{charID},
		SnapshotIds:  []string{snapGood},
		CreatedAt:    now,
		ActivityDetails: api.ActivityHistory{
			InstanceID:        "act-5",
			Period:            now,
			EstimatedGameMode: new(api.GameModeTrials),
		},
		SnapshotLinks: map[string]api.SnapshotLink{
			charID: {
				CharacterID:      charID,
				SnapshotID:       new(snapGood),
				ConfidenceLevel:  api.HighConfidenceLevel,
				ConfidenceSource: api.SystemConfidenceSource,
				CreatedAt:        now,
			},
		},
		Performance: map[string]api.InstancePerformance{
			otherCharID: {
				PlayerStats: api.PlayerStats{Kills: new(api.StatsValuePair{Value: new(10.0)})},
			},
		},
	})

	// 6. Explicit performance.<charID>.playerStats: null -> must be treated as missing performance, not a 0/0/0 loss
	nullPlayerStatsDocID := runID + "-agg-null-player-stats"
	if _, err := db.Collection("aggregates").Doc(nullPlayerStatsDocID).Set(ctx, map[string]any{
		"id":           nullPlayerStatsDocID,
		"activityId":   "act-6",
		"characterIds": []string{charID},
		"snapshotIds":  []string{snapGood},
		"createdAt":    now,
		"activityHistory": map[string]any{
			"instanceId":        "act-6",
			"period":            now,
			"estimatedGameMode": string(api.GameModeTrials),
		},
		"snapshotLinks": map[string]any{
			charID: map[string]any{
				"characterId": charID,
				"snapshotId":  snapGood,
			},
		},
		"performance": map[string]any{
			charID: map[string]any{
				"playerStats": nil,
			},
		},
	}); err != nil {
		t.Fatalf("failed seeding null-playerStats aggregate: %v", err)
	}
	aggIDs = append(aggIDs, nullPlayerStatsDocID)

	store := NewFirestoreStore(db)

	// Verify SelectPaths actually omits unprojected fields from the wire DocumentSnapshot.
	projDocs, err := store.buildCharacterMatchStatsQuery(charID, string(api.GameModeTrials)).Documents(ctx).GetAll()
	if err != nil {
		t.Fatalf("projected query failed: %v", err)
	}
	foundRich := false
	for _, d := range projDocs {
		if d.Ref.ID != runID+"-agg-trials-win" {
			continue
		}
		foundRich = true
		raw := d.Data()
		if _, hasHistory := raw["activityHistory"]; hasHistory {
			t.Fatalf("expected activityHistory omitted by SelectPaths projection, got keys %v", raw)
		}
		if _, hasCharIDs := raw["characterIds"]; hasCharIDs {
			t.Fatalf("expected characterIds omitted by SelectPaths projection, got keys %v", raw)
		}
		perfMap, ok := raw["performance"].(map[string]any)
		if !ok {
			t.Fatalf("expected performance map in projected doc, got %T", raw["performance"])
		}
		if _, hasOther := perfMap[otherCharID]; hasOther {
			t.Fatalf("expected other character performance omitted by projection, got %+v", perfMap)
		}
		charPerf, ok := perfMap[charID].(map[string]any)
		if !ok {
			t.Fatalf("expected character performance map, got %T", perfMap[charID])
		}
		if _, hasWeapons := charPerf["weapons"]; hasWeapons {
			t.Fatalf("expected weapons omitted by SelectPaths projection, got %+v", charPerf)
		}
		if _, hasExtra := charPerf["extra"]; hasExtra {
			t.Fatalf("expected extra omitted by SelectPaths projection, got %+v", charPerf)
		}
		linksMap, ok := raw["snapshotLinks"].(map[string]any)
		if !ok {
			t.Fatalf("expected snapshotLinks map in projected doc, got %T", raw["snapshotLinks"])
		}
		if _, hasOtherLink := linksMap[otherCharID]; hasOtherLink {
			t.Fatalf("expected other character snapshotLink omitted by projection, got %+v", linksMap)
		}
		charLink, ok := linksMap[charID].(map[string]any)
		if !ok {
			t.Fatalf("expected character snapshotLink map, got %T", linksMap[charID])
		}
		if _, hasConf := charLink["confidenceLevel"]; hasConf {
			t.Fatalf("expected non-snapshotId link fields omitted by projection, got %+v", charLink)
		}
	}
	if !foundRich {
		t.Fatalf("expected to find seeded rich Trials doc in projected query")
	}

	// Verify ListCharacterMatchStats with empty mode returns 3 valid matches (skipping unlinked, missing perf, and explicit playerStats: null).
	allMatches, err := store.ListCharacterMatchStats(ctx, charID, "")
	if err != nil {
		t.Fatalf("ListCharacterMatchStats(\"\") failed: %v", err)
	}
	if len(allMatches) != 3 {
		t.Fatalf("expected 3 valid projected match stats, got %d: %+v", len(allMatches), allMatches)
	}

	// Verify Trials mode filter returns only the 1 valid Trials match (and skips explicit playerStats: null).
	trialsMatches, err := store.ListCharacterMatchStats(ctx, charID, string(api.GameModeTrials))
	if err != nil {
		t.Fatalf("ListCharacterMatchStats(trials) failed: %v", err)
	}
	if len(trialsMatches) != 1 || trialsMatches[0].SnapshotID != snapGood || trialsMatches[0].Kills != 15 || trialsMatches[0].Deaths != 5 || trialsMatches[0].Assists != 4 || !trialsMatches[0].Won {
		t.Fatalf("unexpected Trials match stats: %+v", trialsMatches)
	}

	// Verify end-to-end GetBestPerformingLoadouts handles nil PlayerStats without panicking and does NOT count nil Standing as a win.
	aggSvc := aggregate.NewService(db)
	snapSvc := snapshot.NewService(db, nil, nil, aggSvc)
	statsSvc := NewService(db, store, snapSvc, nil, nil)

	loadouts, perfStats, counts, err := statsSvc.GetBestPerformingLoadouts(ctx, charID, new(api.GameModeAll), 5, 1)
	if err != nil {
		t.Fatalf("GetBestPerformingLoadouts failed: %v", err)
	}
	if len(loadouts) != 2 || loadouts[0].ID != snapGood || loadouts[1].ID != snapPartial {
		t.Fatalf("expected [snapGood (3.0 KD), snapPartial (2.0 KD)], got %+v", loadouts)
	}
	if counts[snapGood] != 1 {
		t.Fatalf("expected 1 counted game for snapGood (excluding null playerStats doc), got %d", counts[snapGood])
	}
	if counts[snapPartial] != 2 {
		t.Fatalf("expected 2 counted games for snapPartial, got %d", counts[snapPartial])
	}
	if *perfStats[snapPartial].Kills.Value != 8 || *perfStats[snapPartial].Deaths.Value != 4 || *perfStats[snapPartial].Kd.Value != 2.0 {
		t.Fatalf("unexpected snapPartial stats: %+v", perfStats[snapPartial])
	}
	if *perfStats[snapPartial].Standing.Value != 0.0 {
		t.Fatalf("expected 0.0 win rate when Standing is nil, got %v", *perfStats[snapPartial].Standing.Value)
	}
	if *perfStats[snapGood].Standing.Value != 1.0 {
		t.Fatalf("expected 1.0 win rate for snapGood, got %v", *perfStats[snapGood].Standing.Value)
	}
}
