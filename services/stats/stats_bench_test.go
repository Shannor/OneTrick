package stats

import (
	"context"
	"encoding/json"
	"fmt"
	"oneTrick/api"
	"oneTrick/services/aggregate"
	"oneTrick/services/snapshot"
	"os"
	"strconv"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
)

func buildRealisticAggregate(aggID, targetCharID, snapID string, idx int, now time.Time) api.Aggregate {
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
		for eIdx, name := range extraStatNames {
			extra[name] = api.UniqueStatValue{
				Name:  new(name),
				Basic: *makePair(float64((idx + eIdx + p) % 25)),
			}
		}

		standing := float64(idx % 2)
		kills := float64(10 + (idx+p)%18)
		deaths := float64(4 + (idx+p)%10)
		assists := float64(2 + (idx+p)%8)

		perf[cID] = api.InstancePerformance{
			PlayerStats: api.PlayerStats{
				Kills:      makePair(kills),
				Deaths:     makePair(deaths),
				Assists:    makePair(assists),
				Kd:         makePair(kills / deaths),
				Kda:        makePair((kills + assists) / deaths),
				Standing:   makePair(standing),
				Team:       makePair(float64(p % 2)),
				FireTeamID: makePair(float64(100000 + p/3)),
				TimePlayed: makePair(480),
			},
			Weapons: weapons,
			Extra:   &extra,
		}
	}

	modes := []api.GameMode{api.GameModeQuickPlay, api.GameModeTrials, api.GameModeCompetitive}
	mode := modes[idx%len(modes)]

	return api.Aggregate{
		ID:           aggID,
		ActivityID:   aggID,
		CharacterIds: charIDs,
		SnapshotIds:  []string{snapID},
		SessionIds:   []string{fmt.Sprintf("sess-%d", idx/10)},
		CreatedAt:    now.Add(-time.Duration(idx) * time.Minute),
		ActivityDetails: api.ActivityHistory{
			Activity:          "Control: Matchmade",
			ActivityHash:      2259621230,
			ActivityIcon:      "/common/destiny2_content/icons/crucible_icon.png",
			Description:       "Capture and defend zones to increase points per defeat.",
			EstimatedGameMode: new(mode),
			ImageURL:          "/img/destiny_content/pgcr/crucible_javelin_4.jpg",
			InstanceID:        aggID,
			IsPrivate:         new(false),
			Location:          "Javelin-4",
			Mode:              new("Control"),
			Period:            now.Add(-time.Duration(idx) * time.Minute),
			ReferenceID:       3345198229,
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

func legacySummarizeAggregates(aggs []api.Aggregate, characterID string) (map[string]aggStatSummary, map[string]int) {
	stats := make(map[string]aggStatSummary)
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
		st := stats[*link.SnapshotID]
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
		stats[*link.SnapshotID] = st
		counts[*link.SnapshotID]++
	}
	return stats, counts
}

func BenchmarkSummarize600Matches(b *testing.B) {
	const numMatches = 600
	const numSnaps = 40
	const charID = "bench-char"
	now := time.Now().UTC()

	aggs := make([]api.Aggregate, numMatches)
	matches := make([]characterMatchStat, numMatches)
	for i := 0; i < numMatches; i++ {
		snapID := fmt.Sprintf("snap-%02d", i%numSnaps)
		aggs[i] = buildRealisticAggregate(fmt.Sprintf("agg-%04d", i), charID, snapID, i, now)
		ps := aggs[i].Performance[charID].PlayerStats
		matches[i] = characterMatchStat{
			snapshotID: snapID,
			kills:      int(*ps.Kills.Value),
			deaths:     int(*ps.Deaths.Value),
			assists:    int(*ps.Assists.Value),
			won:        *ps.Standing.Value == 0,
		}
	}

	b.Run("legacy_api_Aggregate", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			st, cnt := legacySummarizeAggregates(aggs, charID)
			if len(st) == 0 || len(cnt) == 0 {
				b.Fatal("empty result")
			}
		}
	})

	b.Run("projected_characterMatchStat", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			st, cnt := summarizeCharacterMatchStats(matches)
			if len(st) == 0 || len(cnt) == 0 {
				b.Fatal("empty result")
			}
		}
	})
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
			Loadout:     makeBaseTestLoadout(fmt.Sprintf("kin-%d", i%50), fmt.Sprintf("en-%d", i%50), int64(3000+i%5), fmt.Sprintf("pow-%d", i), int64(4000+i)),
		}
		if _, err := bw.Set(db.Collection("snapshots").Doc(id), s); err != nil {
			b.Fatalf("bulk seed snapshot %s: %v", id, err)
		}
	}
	for i := 0; i < numAggs; i++ {
		id := fmt.Sprintf("%s-agg-%04d", runID, i)
		aggIDs[i] = id
		snapID := snapIDs[i%50]
		agg := buildRealisticAggregate(id, charID, snapID, i, now)
		if _, err := bw.Set(db.Collection("aggregates").Doc(id), agg); err != nil {
			b.Fatalf("bulk seed aggregate %s: %v", id, err)
		}
	}
	bw.End()

	aggSvc := aggregate.NewService(db)
	snapSvc := snapshot.NewService(db, nil, nil, aggSvc)
	statsSvc := NewService(db, snapSvc, nil, nil).(*service)
	store := newFirestoreStatsStore(db)

	fullSampleDocs, err := db.Collection(aggregatesCollection).Where("characterIds", "array-contains", charID).Limit(1).Documents(ctx).GetAll()
	if err != nil || len(fullSampleDocs) == 0 {
		b.Fatalf("failed reading sample full doc: %v", err)
	}
	fullJSON, _ := json.Marshal(fullSampleDocs[0].Data())

	projSampleDocs, err := store.buildCharacterMatchStatsQuery(charID, nil).Limit(1).Documents(ctx).GetAll()
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
			aggs, err := statsSvc.GetAggregatesByCharacterID(ctx, charID, nil)
			if err != nil || len(aggs) != numAggs {
				b.Fatalf("expected %d aggs, got %d err=%v", numAggs, len(aggs), err)
			}
			_, _ = legacySummarizeAggregates(aggs, charID)
		}
		b.ReportMetric(float64(len(fullJSON)), "payloadBytes/doc")
	})

	b.Run("query_600_after_projected", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			ms, err := store.listCharacterMatchStats(ctx, charID, nil)
			if err != nil || len(ms) != numAggs {
				b.Fatalf("expected %d matchStats, got %d err=%v", numAggs, len(ms), err)
			}
			_, _ = summarizeCharacterMatchStats(ms)
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
			_, _ = ComputeSimilarCounts(allSnaps)
			aggs, err := statsSvc.GetAggregatesByCharacterID(ctx, charID, nil)
			if err != nil {
				b.Fatal(err)
			}
			_, _ = legacySummarizeAggregates(aggs, charID)
		}
	})

	b.Run("GetSnapshotsWithMetrics_after_concurrent_projected", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			items, _, _, _, err := statsSvc.GetSnapshotsWithMetrics(ctx, userID, charID, nil, 5, "kd_ratio", true, false, 10, 0)
			if err != nil || len(items) == 0 {
				b.Fatalf("expected non-empty items, got %d err=%v", len(items), err)
			}
		}
	})

	b.Run("GetBestPerformingLoadouts_before_unprojected", func(b *testing.B) {
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			aggs, err := statsSvc.GetAggregatesByCharacterID(ctx, charID, nil)
			if err != nil {
				b.Fatal(err)
			}
			st, cnt := legacySummarizeAggregates(aggs, charID)
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
