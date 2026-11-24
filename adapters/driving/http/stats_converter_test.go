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
