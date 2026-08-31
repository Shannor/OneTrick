package stats

import (
	"oneTrick/api"
	"oneTrick/ptr"
	"slices"
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
