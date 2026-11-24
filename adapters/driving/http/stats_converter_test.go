package httpadapter

import (
	"oneTrick/api"
	"oneTrick/services/stats"
	"testing"
)

func TestToSimilarSnapshotsParams(t *testing.T) {
	t.Run("defaults includeStats to true and tier to all when omitted", func(t *testing.T) {
		got := ToSimilarSnapshotsParams(api.GetSimilarSnapshotsRequestObject{
			SnapshotID: "snap-1",
		})

		if got.SnapshotID != "snap-1" {
			t.Fatalf("SnapshotID = %q, want snap-1", got.SnapshotID)
		}
		if !got.IncludeStats {
			t.Fatalf("IncludeStats = false, want true")
		}
		if got.Tier != stats.SimilarTierAll {
			t.Fatalf("Tier = %q, want %q", got.Tier, stats.SimilarTierAll)
		}
		if got.GameMode != "" {
			t.Fatalf("GameMode = %q, want empty", got.GameMode)
		}
	})

	t.Run("maps explicit includeStats=false, tier=high, and gameMode", func(t *testing.T) {
		includeStats := false
		tier := api.SimilarTierFilterHigh
		gameMode := api.GameModeTrials

		got := ToSimilarSnapshotsParams(api.GetSimilarSnapshotsRequestObject{
			SnapshotID: "snap-2",
			Params: api.GetSimilarSnapshotsParams{
				IncludeStats: &includeStats,
				Tier:         &tier,
				GameMode:     &gameMode,
			},
		})

		if got.SnapshotID != "snap-2" {
			t.Fatalf("SnapshotID = %q, want snap-2", got.SnapshotID)
		}
		if got.IncludeStats {
			t.Fatalf("IncludeStats = true, want false")
		}
		if got.Tier != stats.SimilarTierHigh {
			t.Fatalf("Tier = %q, want %q", got.Tier, stats.SimilarTierHigh)
		}
		if got.GameMode != string(api.GameModeTrials) {
			t.Fatalf("GameMode = %q, want %q", got.GameMode, api.GameModeTrials)
		}
	})
}

func TestToAPISimilarSnapshotsResponse(t *testing.T) {
	target := api.CharacterSnapshot{ID: "snap-1", Name: "Trials Hawkmoon"}

	t.Run("normalizes nil items to empty slice and leaves nil stats/counts as nil pointers", func(t *testing.T) {
		got := ToAPISimilarSnapshotsResponse(target, nil, nil, nil)
		if got.Target.ID != "snap-1" {
			t.Fatalf("Target.ID = %q, want snap-1", got.Target.ID)
		}
		if got.Items == nil || len(got.Items) != 0 {
			t.Fatalf("expected non-nil empty Items slice, got %v", got.Items)
		}
		if got.Stats != nil || got.Count != nil {
			t.Fatalf("expected nil Stats and Count pointers, got stats=%v count=%v", got.Stats, got.Count)
		}
	})

	t.Run("wraps non-nil stats and counts maps for OpenAPI response", func(t *testing.T) {
		statsMap := map[string]api.PlayerStats{
			"snap-1": {Kd: new(api.StatsValuePair{Value: new(1.75)})},
		}
		countsMap := map[string]int{"snap-1": 4}

		got := ToAPISimilarSnapshotsResponse(target, []api.SimilarSnapshotItem{}, statsMap, countsMap)
		if got.Stats == nil || *(*got.Stats)["snap-1"].Kd.Value != 1.75 {
			t.Fatalf("unexpected Stats pointer: %+v", got.Stats)
		}
		if got.Count == nil || (*got.Count)["snap-1"] != 4 {
			t.Fatalf("unexpected Count pointer: %+v", got.Count)
		}
	})
}
