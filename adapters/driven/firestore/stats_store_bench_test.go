package firestore

import (
	"context"
	"encoding/json"
	"fmt"
	"oneTrick/api"
	"oneTrick/services/aggregate"
	"oneTrick/services/destiny"
	"oneTrick/services/snapshot"
	"oneTrick/services/stats"
	"oneTrick/utils"
	"os"
	"strconv"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
)

type benchStatSummary struct {
	Kills   int
	Deaths  int
	Assists int
	Wins    int
}

func buildBenchAggregate(aggID, targetCharID, snapID string, idx int, now time.Time) api.Aggregate {
	const lobbyPlayers = 12
	charIDs := make([]string, 0, lobbyPlayers)
	perf := make(map[string]api.InstancePerformance, lobbyPlayers)

	extraStatNames := []string{
		"opponentsDefeated", "efficiency", "combatRating", "precisionKills",
		"weaponKillsHandCannon", "weaponKillsShotgun", "weaponKillsSuper",
		"weaponKillsGrenade", "weaponKillsMelee", "longestKillSpree",
		"averageScorePerKill", "averageScorePerLife", "averageLifespan",
		"zonesCaptured", "zonesNeutralized",
	}

	makePair := func(v float64) *api.StatsValuePair {
		return &api.StatsValuePair{
			DisplayValue: new(fmt.Sprintf("%.2f", v)),
			Value:        new(v),
		}
	}

	for p := 0; p < lobbyPlayers; p++ {
		cID := targetCharID
		if p > 0 {
			cID = fmt.Sprintf("lobby-char-%02d", p)
		}
		charIDs = append(charIDs, cID)

		weapons := make(map[string]api.WeaponInstanceMetrics, 4)
		for w := 0; w < 4; w++ {
			refID := int64(3000000000 + w*100 + p)
			wStats := map[string]api.UniqueStatValue{
				"uniqueWeaponKills":          {Name: new("uniqueWeaponKills"), Basic: *makePair(float64((idx+w)%12 + 1))},
				"uniqueWeaponPrecisionKills": {Name: new("uniqueWeaponPrecisionKills"), Basic: *makePair(float64((idx + w) % 6))},
				"uniqueWeaponKillsPrecisionKills": {
					Name:  new("uniqueWeaponKillsPrecisionKills"),
					Basic: *makePair(0.58),
				},
			}
			weapons[strconv.FormatInt(refID, 10)] = api.WeaponInstanceMetrics{
				ReferenceID: new(refID),
				Stats:       &wStats,
			}
		}

		extra := make(map[string]api.UniqueStatValue, len(extraStatNames))
		for ei, name := range extraStatNames {
			extra[name] = api.UniqueStatValue{
				Name:  new(name),
				Basic: *makePair(float64((idx + ei + p) % 25)),
			}
		}

		standingVal := 1.0
		if (idx+p)%2 == 0 {
			standingVal = 0.0
		}
		perf[cID] = api.InstancePerformance{
			PlayerStats: api.PlayerStats{
				Kills:    makePair(float64(8 + (idx+p)%18)),
				Deaths:   makePair(float64(4 + (idx+p)%10)),
				Assists:  makePair(float64(2 + (idx+p)%8)),
				Kd:       makePair(1.75),
				Kda:      makePair(2.15),
				Standing: makePair(standingVal),
			},
			Weapons: weapons,
			Extra:   &extra,
		}
	}

	return api.Aggregate{
		ID:           aggID,
		ActivityID:   "act-" + aggID,
		CharacterIds: charIDs,
		SnapshotIds:  []string{snapID},
		SessionIds:   []string{fmt.Sprintf("sess-%d", idx/10)},
		CreatedAt:    now.Add(-time.Duration(idx) * time.Minute),
		ActivityDetails: api.ActivityHistory{
			InstanceID:        "act-" + aggID,
			Period:            now.Add(-time.Duration(idx) * time.Minute),
			Location:          "Javelin-4",
			Activity:          "Control",
			Mode:              new("Control"),
			EstimatedGameMode: new(api.GameModeQuickPlay),
		},
		SnapshotLinks: map[string]api.SnapshotLink{
			targetCharID: {
				CharacterID:      targetCharID,
				SnapshotID:       new(snapID),
				SessionID:        new(fmt.Sprintf("sess-%d", idx/10)),
				ConfidenceLevel:  api.HighConfidenceLevel,
				ConfidenceSource: api.SystemConfidenceSource,
				CreatedAt:        now.Add(-time.Duration(idx) * time.Minute),
			},
			"lobby-char-01": {
				CharacterID:      "lobby-char-01",
				SnapshotID:       new("other-snap-1"),
				ConfidenceLevel:  api.MediumConfidenceLevel,
				ConfidenceSource: api.SystemConfidenceSource,
				CreatedAt:        now.Add(-time.Duration(idx) * time.Minute),
			},
		},
		Performance: perf,
	}
}

func makeBenchLoadout(kinID string, enID string, subclassHash int64, powerID string, powerHash int64) api.Loadout {
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

func legacySummarizeBenchAggregates(aggs []api.Aggregate, characterID string) (map[string]benchStatSummary, map[string]int) {
	summaries := make(map[string]benchStatSummary)
	counts := make(map[string]int)
	for _, agg := range aggs {
		link, ok := agg.SnapshotLinks[characterID]
		if !ok || link.SnapshotID == nil || *link.SnapshotID == "" {
			continue
		}
		performance, ok := agg.Performance[characterID]
		if !ok {
			continue
		}
		st := summaries[*link.SnapshotID]
		if performance.PlayerStats.Kills != nil && performance.PlayerStats.Kills.Value != nil {
			st.Kills += int(*performance.PlayerStats.Kills.Value)
		}
		if performance.PlayerStats.Deaths != nil && performance.PlayerStats.Deaths.Value != nil {
			st.Deaths += int(*performance.PlayerStats.Deaths.Value)
		}
		if performance.PlayerStats.Assists != nil && performance.PlayerStats.Assists.Value != nil {
			st.Assists += int(*performance.PlayerStats.Assists.Value)
		}
		if performance.PlayerStats.Standing != nil && performance.PlayerStats.Standing.Value != nil && *performance.PlayerStats.Standing.Value == 0 {
			st.Wins++
		}
		summaries[*link.SnapshotID] = st
		counts[*link.SnapshotID]++
	}
	return summaries, counts
}

func summarizeBenchMatchStats(matches []stats.CharacterMatchStat) (map[string]benchStatSummary, map[string]int) {
	summaries := make(map[string]benchStatSummary)
	counts := make(map[string]int)
	for _, match := range matches {
		if match.SnapshotID == "" {
			continue
		}
		st := summaries[match.SnapshotID]
		st.Kills += match.Kills
		st.Deaths += match.Deaths
		st.Assists += match.Assists
		if match.Won {
			st.Wins++
		}
		summaries[match.SnapshotID] = st
		counts[match.SnapshotID]++
	}
	return summaries, counts
}

func legacyGetAggregatesByCharacterID(ctx context.Context, db *firestore.Client, characterID string) ([]api.Aggregate, error) {
	aggDocs, err := db.Collection(aggregatesCollection).
		Where("characterIds", "array-contains", characterID).
		Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	return utils.GetAllToStructs[api.Aggregate](aggDocs)
}

func BenchmarkFirestoreEmulator600AggregatesAnd200Snapshots(b *testing.B) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		b.Skip("FIRESTORE_EMULATOR_HOST not set; skipping Firestore emulator benchmark")
	}

	ctx := context.Background()
	db, err := firestore.NewClient(ctx, "gruntt-destiny-test")
	if err != nil {
		b.Fatalf("failed to create emulator firestore client: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })

	runID := fmt.Sprintf("bench-%d", time.Now().UnixNano())
	userID := "user-" + runID
	charID := "char-" + runID
	now := time.Now().UTC().Truncate(time.Millisecond)

	const numSnaps = 200
	const numAggs = 600

	snapIDs := make([]string, numSnaps)
	aggIDs := make([]string, numAggs)
	b.Cleanup(func() {
		bw := db.BulkWriter(context.Background())
		for _, id := range snapIDs {
			_, _ = bw.Delete(db.Collection("snapshots").Doc(id))
		}
		for _, id := range aggIDs {
			_, _ = bw.Delete(db.Collection("aggregates").Doc(id))
		}
		bw.End()
	})

	bw := db.BulkWriter(ctx)
	for i := 0; i < numSnaps; i++ {
		id := fmt.Sprintf("%s-snap-%03d", runID, i)
		snapIDs[i] = id
		s := api.CharacterSnapshot{
			ID:          id,
			UserID:      userID,
			CharacterID: charID,
			Name:        fmt.Sprintf("Loadout %d", i),
			Hash:        fmt.Sprintf("hash-%s-%d", runID, i),
			CreatedAt:   now.Add(-time.Duration(i) * time.Hour),
			UpdatedAt:   now.Add(-time.Duration(i) * time.Hour),
			Loadout:     makeBenchLoadout(fmt.Sprintf("kin-%d", i%50), fmt.Sprintf("en-%d", i%50), int64(3000+i%5), fmt.Sprintf("pow-%d", i), int64(4000+i)),
		}
		if _, err := bw.Set(db.Collection("snapshots").Doc(id), s); err != nil {
			b.Fatalf("bulk seed snapshot %s: %v", id, err)
		}
	}
	for i := 0; i < numAggs; i++ {
		id := fmt.Sprintf("%s-agg-%04d", runID, i)
		aggIDs[i] = id
		snapID := snapIDs[i%50]
		agg := buildBenchAggregate(id, charID, snapID, i, now)
		if _, err := bw.Set(db.Collection("aggregates").Doc(id), agg); err != nil {
			b.Fatalf("bulk seed aggregate %s: %v", id, err)
		}
	}
	bw.End()

	aggSvc := aggregate.NewService(db)
	snapSvc := snapshot.NewService(db, nil, nil, aggSvc)
	store := NewStatsStore(db)
	statsSvc := stats.NewService(db, store, snapSvc, nil, nil)

	fullSampleDocs, err := db.Collection(aggregatesCollection).Where("characterIds", "array-contains", charID).Limit(1).Documents(ctx).GetAll()
	if err != nil || len(fullSampleDocs) == 0 {
		b.Fatalf("failed reading sample full doc: %v", err)
	}
	fullJSON, _ := json.Marshal(fullSampleDocs[0].Data())

	projSampleDocs, err := store.buildCharacterMatchStatsQuery(charID, "").Limit(1).Documents(ctx).GetAll()
	if err != nil || len(projSampleDocs) == 0 {
		b.Fatalf("failed reading sample projected doc: %v", err)
	}
	projJSON, _ := json.Marshal(projSampleDocs[0].Data())
	b.Logf("Single aggregate doc JSON payload: full=%d bytes, projected=%d bytes (%.1fx reduction)",
		len(fullJSON), len(projJSON), float64(len(fullJSON))/float64(len(projJSON)))

	b.Run("query_600_before_unprojected", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			aggs, err := legacyGetAggregatesByCharacterID(ctx, db, charID)
			if err != nil || len(aggs) != numAggs {
				b.Fatalf("expected %d aggs, got %d err=%v", numAggs, len(aggs), err)
			}
			_, _ = legacySummarizeBenchAggregates(aggs, charID)
		}
		b.ReportMetric(float64(len(fullJSON)), "payloadBytes/doc")
	})

	b.Run("query_600_after_projected", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			ms, err := store.ListCharacterMatchStats(ctx, charID, "")
			if err != nil || len(ms) != numAggs {
				b.Fatalf("expected %d matchStats, got %d err=%v", numAggs, len(ms), err)
			}
			_, _ = summarizeBenchMatchStats(ms)
		}
		b.ReportMetric(float64(len(projJSON)), "payloadBytes/doc")
	})

	b.Run("GetSnapshotsWithMetrics_before_sequential_unprojected", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			allSnaps, err := snapSvc.GetAllByCharacter(ctx, userID, charID, 0, 0)
			if err != nil {
				b.Fatal(err)
			}
			_ = stats.ComputeSimilarCounts(allSnaps)
			aggs, err := legacyGetAggregatesByCharacterID(ctx, db, charID)
			if err != nil {
				b.Fatal(err)
			}
			_, _ = legacySummarizeBenchAggregates(aggs, charID)
		}
	})

	b.Run("GetSnapshotsWithMetrics_after_concurrent_projected", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			items, _, _, _, err := statsSvc.GetSnapshotsWithMetrics(ctx, userID, charID, nil, 5, "kd_ratio", true, 10, 0)
			if err != nil || len(items) == 0 {
				b.Fatalf("expected non-empty items, got %d err=%v", len(items), err)
			}
		}
	})

	b.Run("GetBestPerformingLoadouts_before_unprojected", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			aggs, err := legacyGetAggregatesByCharacterID(ctx, db, charID)
			if err != nil {
				b.Fatal(err)
			}
			st, cnt := legacySummarizeBenchAggregates(aggs, charID)
			if len(st) == 0 || len(cnt) == 0 {
				b.Fatal("empty summary")
			}
			if _, err := snapSvc.GetByIDs(ctx, snapIDs[:3]); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("GetBestPerformingLoadouts_after_projected", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			items, _, _, err := statsSvc.GetBestPerformingLoadouts(ctx, charID, nil, 3, 10)
			if err != nil || len(items) != 3 {
				b.Fatalf("expected 3 best loadouts, got %d err=%v", len(items), err)
			}
		}
	})
}
