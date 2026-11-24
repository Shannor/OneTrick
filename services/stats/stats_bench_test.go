package stats

import (
	"fmt"
	"oneTrick/api"
	"strconv"
	"testing"
	"time"
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
	matches := make([]CharacterMatchStat, numMatches)
	for i := 0; i < numMatches; i++ {
		snapID := fmt.Sprintf("snap-%02d", i%numSnaps)
		aggs[i] = buildRealisticAggregate(fmt.Sprintf("agg-%04d", i), charID, snapID, i, now)
		ps := aggs[i].Performance[charID].PlayerStats
		matches[i] = CharacterMatchStat{
			SnapshotID: snapID,
			Kills:      int(*ps.Kills.Value),
			Deaths:     int(*ps.Deaths.Value),
			Assists:    int(*ps.Assists.Value),
			Won:        *ps.Standing.Value == 0,
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

	b.Run("projected_CharacterMatchStat", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			st, cnt := summarizeCharacterMatchStats(matches)
			if len(st) == 0 || len(cnt) == 0 {
				b.Fatal("empty result")
			}
		}
	})
}
