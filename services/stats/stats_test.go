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
