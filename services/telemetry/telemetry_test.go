package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"oneTrick/api"
	"oneTrick/services/snapshot"
	"oneTrick/services/user"
	"slices"
	"testing"
	"time"

	clog "github.com/Shannor/canonical-logger"
)

type fakeUserReader struct {
	users map[string]User
	err   error
}

func (f *fakeUserReader) GetUser(_ context.Context, lookupID string) (User, error) {
	if f.err != nil {
		return User{}, f.err
	}
	u, ok := f.users[lookupID]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

type fakeSnapshotReader struct {
	snaps         map[string]Snapshot
	err           error
	getByIDsCalls [][]string
}

func (f *fakeSnapshotReader) GetSnapshot(_ context.Context, snapshotID string) (Snapshot, error) {
	if f.err != nil {
		return Snapshot{}, f.err
	}
	s, ok := f.snaps[snapshotID]
	if !ok {
		return Snapshot{}, ErrNotFound
	}
	return s, nil
}

func (f *fakeSnapshotReader) GetSnapshotsByIDs(_ context.Context, snapshotIDs []string) ([]Snapshot, error) {
	if f.err != nil {
		return nil, f.err
	}
	f.getByIDsCalls = append(f.getByIDsCalls, slices.Clone(snapshotIDs))
	out := make([]Snapshot, 0, len(snapshotIDs))
	for _, id := range snapshotIDs {
		if s, ok := f.snaps[id]; ok {
			out = append(out, s)
		}
	}
	return out, nil
}

type fakeMatchStore struct {
	// Stored newest-first (period DESC), mirroring Firestore ordering.
	matches               []RawMatch
	snapshotMatches       map[string][]RawMatch
	err                   error
	fastPathCalls         int
	streamCalls           int
	capturedSnapshotID    string
	capturedFrom          time.Time
	capturedTo            time.Time
	capturedLimit         int
}

func (f *fakeMatchStore) filterByTime(src []RawMatch, from, to time.Time) []RawMatch {
	out := make([]RawMatch, 0, len(src))
	for _, m := range src {
		if !from.IsZero() && m.Period.Before(from) {
			continue
		}
		if !to.IsZero() && m.Period.After(to) {
			continue
		}
		out = append(out, m)
	}
	return out
}

func (f *fakeMatchStore) ListRecentUnfiltered(_ context.Context, _ string, from, to time.Time, limit int) ([]RawMatch, int, bool, error) {
	f.fastPathCalls++
	f.capturedFrom = from
	f.capturedTo = to
	f.capturedLimit = limit
	if f.err != nil {
		return nil, 0, false, f.err
	}
	filtered := f.filterByTime(f.matches, from, to)
	total := len(filtered)
	if total <= limit {
		return slices.Clone(filtered), total, false, nil
	}
	return slices.Clone(filtered[:limit]), total, true, nil
}

func (f *fakeMatchStore) StreamMatches(_ context.Context, _, snapshotID string, from, to time.Time, fn func(RawMatch) error) error {
	f.streamCalls++
	f.capturedSnapshotID = snapshotID
	f.capturedFrom = from
	f.capturedTo = to
	if f.err != nil {
		return f.err
	}
	src := f.matches
	if snapshotID != "" && f.snapshotMatches != nil {
		src = f.snapshotMatches[snapshotID]
	}
	for _, m := range f.filterByTime(src, from, to) {
		if err := fn(m); err != nil {
			return err
		}
	}
	return nil
}

func TestGetCharacterTelemetryValidationErrors(t *testing.T) {
	t0 := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	svc := NewService(&fakeUserReader{}, &fakeSnapshotReader{}, &fakeMatchStore{})

	cases := []struct {
		name  string
		query Query
	}{
		{
			name:  "empty userId",
			query: Query{CharacterID: "char-1"},
		},
		{
			name:  "empty characterId",
			query: Query{UserID: "user-1"},
		},
		{
			name:  "from after to",
			query: Query{UserID: "user-1", CharacterID: "char-1", From: t0.Add(time.Minute), To: t0},
		},
		{
			name:  "negative limit",
			query: Query{UserID: "user-1", CharacterID: "char-1", Limit: -1},
		},
		{
			name:  "limit above max",
			query: Query{UserID: "user-1", CharacterID: "char-1", Limit: MaxLimit + 1},
		},
		{
			name:  "invalid gameMode",
			query: Query{UserID: "user-1", CharacterID: "char-1", GameMode: GameMode("gambit")},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.GetCharacterTelemetry(context.Background(), tc.query)
			if !errors.Is(err, ErrInvalidQuery) {
				t.Fatalf("expected ErrInvalidQuery, got %v", err)
			}
		})
	}
}

func TestGetCharacterTelemetryNotFoundCases(t *testing.T) {
	users := &fakeUserReader{
		users: map[string]User{
			"user-1":      {ID: "user-1", CharacterIDs: []string{"char-1"}},
			"legacy-user": {ID: "legacy-user"},
		},
	}
	snaps := &fakeSnapshotReader{
		snaps: map[string]Snapshot{
			"snap-other-char":       {ID: "snap-other-char", UserID: "user-1", CharacterID: "char-2", Name: "Titan Build"},
			"snap-other-user":       {ID: "snap-other-user", UserID: "user-other", CharacterID: "char-1", Name: "Other User Build"},
			"snap-archived-orphan":  {ID: "snap-archived-orphan", UserID: "user-1", CharacterID: "char-1", Name: "Orphaned", MergedIntoID: "snap-deleted"},
			"snap-archived-chain":   {ID: "snap-archived-chain", UserID: "user-1", CharacterID: "char-1", Name: "Chained", MergedIntoID: "snap-archived-mid"},
			"snap-archived-mid":     {ID: "snap-archived-mid", UserID: "user-1", CharacterID: "char-1", Name: "Mid", MergedIntoID: "snap-final"},
			"snap-archived-foreign": {ID: "snap-archived-foreign", UserID: "user-1", CharacterID: "char-1", Name: "Foreign", MergedIntoID: "snap-other-user"},
		},
	}
	svc := NewService(users, snaps, &fakeMatchStore{})

	t.Run("missing user returns ErrNotFound", func(t *testing.T) {
		_, err := svc.GetCharacterTelemetry(context.Background(), Query{
			UserID:      "unknown-user",
			CharacterID: "char-1",
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("character not owned by user returns ErrNotFound", func(t *testing.T) {
		_, err := svc.GetCharacterTelemetry(context.Background(), Query{
			UserID:      "user-1",
			CharacterID: "char-999",
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("missing snapshotId returns ErrNotFound", func(t *testing.T) {
		_, err := svc.GetCharacterTelemetry(context.Background(), Query{
			UserID:      "user-1",
			CharacterID: "char-1",
			SnapshotID:  "snap-missing",
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("snapshotId belonging to different character returns ErrNotFound", func(t *testing.T) {
		_, err := svc.GetCharacterTelemetry(context.Background(), Query{
			UserID:      "user-1",
			CharacterID: "char-1",
			SnapshotID:  "snap-other-char",
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("snapshotId belonging to different user for legacy user without CharacterIDs returns ErrNotFound", func(t *testing.T) {
		_, err := svc.GetCharacterTelemetry(context.Background(), Query{
			UserID:      "legacy-user",
			CharacterID: "char-1",
			SnapshotID:  "snap-other-user",
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("archived snapshotId with missing merge target returns ErrNotFound", func(t *testing.T) {
		_, err := svc.GetCharacterTelemetry(context.Background(), Query{
			UserID:      "user-1",
			CharacterID: "char-1",
			SnapshotID:  "snap-archived-orphan",
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("archived snapshotId whose merge target is still archived returns ErrNotFound", func(t *testing.T) {
		_, err := svc.GetCharacterTelemetry(context.Background(), Query{
			UserID:      "user-1",
			CharacterID: "char-1",
			SnapshotID:  "snap-archived-chain",
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("archived snapshotId whose merge target belongs to another user returns ErrNotFound", func(t *testing.T) {
		_, err := svc.GetCharacterTelemetry(context.Background(), Query{
			UserID:      "user-1",
			CharacterID: "char-1",
			SnapshotID:  "snap-archived-foreign",
		})
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})
}

func TestGetCharacterTelemetryEmptyHistoryAndLegacyUser(t *testing.T) {
	users := &fakeUserReader{
		users: map[string]User{
			// Legacy user without cached CharacterIDs should not 404.
			"legacy-user": {ID: "legacy-user"},
		},
	}
	store := &fakeMatchStore{}
	svc := NewService(users, &fakeSnapshotReader{}, store)

	res, err := svc.GetCharacterTelemetry(context.Background(), Query{
		UserID:      "legacy-user",
		CharacterID: "char-legacy",
	})
	if err != nil {
		t.Fatalf("expected empty history to succeed, got %v", err)
	}
	if res.Matches == nil || len(res.Matches) != 0 {
		t.Fatalf("expected non-nil empty Matches, got %#v", res.Matches)
	}
	if res.Snapshots == nil || len(res.Snapshots) != 0 {
		t.Fatalf("expected non-nil empty Snapshots, got %#v", res.Snapshots)
	}
	if res.TotalCount != 0 || res.Truncated {
		t.Fatalf("expected totalCount=0 truncated=false, got total=%d truncated=%v", res.TotalCount, res.Truncated)
	}
	if store.fastPathCalls != 1 || store.capturedLimit != DefaultLimit {
		t.Fatalf("expected 1 fast-path call with default limit %d, got calls=%d limit=%d", DefaultLimit, store.fastPathCalls, store.capturedLimit)
	}
}

func TestGetCharacterTelemetryFastPathOrderingAndSnapshotHygiene(t *testing.T) {
	base := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

	users := &fakeUserReader{
		users: map[string]User{
			"u1": {ID: "u1", CharacterIDs: []string{"c1"}},
		},
	}
	snaps := &fakeSnapshotReader{
		snaps: map[string]Snapshot{
			"snap-active":   {ID: "snap-active", CharacterID: "c1", Name: "Solar Hand Cannon"},
			"snap-archived": {ID: "snap-archived", CharacterID: "c1", Name: "Old Solar", MergedIntoID: "snap-target"},
			"snap-target":   {ID: "snap-target", CharacterID: "c1", Name: "Merged Solar Target"},
			// snap-deleted intentionally omitted
		},
	}
	store := &fakeMatchStore{
		// Newest first, with an exact timestamp tie on act-b and act-a.
		matches: []RawMatch{
			{
				ActivityID:              "act-newest",
				Period:                  base.Add(30 * time.Minute),
				Location:                "Javelin-4",
				Activity:                "Trials of Osiris",
				StoredEstimatedGameMode: GameModeTrials,
				Kills:                   14,
				Deaths:                  5,
				Assists:                 6,
				Won:                     true,
				TimePlayedSeconds:       512,
				HasTimePlayedSeconds:    true,
				SessionID:               "sess-1",
				RawSnapshotID:           "snap-active",
				SnapshotConfidence:      ConfidenceHigh,
			},
			{
				ActivityID:         "act-tie-b",
				Period:             base.Add(20 * time.Minute),
				Location:           "Burnout",
				Activity:           "Control",
				Mode:               "Control Quickplay",
				Kills:              20,
				Deaths:             10,
				Assists:            4,
				Won:                true,
				RawSnapshotID:      "snap-archived",
				SnapshotConfidence: ConfidenceMedium,
			},
			{
				ActivityID:         "act-tie-a",
				Period:             base.Add(20 * time.Minute),
				Location:           "Endless Vale",
				Activity:           "Iron Banner: Tribute",
				Mode:               "Iron Banner Tribute",
				Kills:              12,
				Deaths:             8,
				Assists:            3,
				Won:                false,
				SessionID:          "sess-2",
				RawSnapshotID:      "snap-stale-nomatch",
				SnapshotConfidence: ConfidenceNoMatch,
			},
			{
				ActivityID:         "act-deleted-snap",
				Period:             base.Add(10 * time.Minute),
				Location:           "Dead Cliffs",
				Activity:           "Competitive: Matchmade",
				Kills:              9,
				Deaths:             9,
				Assists:            2,
				Won:                false,
				RawSnapshotID:      "snap-deleted",
				SnapshotConfidence: ConfidenceLow,
			},
			{
				ActivityID:         "act-notfound-link",
				Period:             base,
				Location:           "Unknown Map",
				Activity:           "Private Custom",
				Kills:              5,
				Deaths:             5,
				Assists:            1,
				Won:                false,
				SnapshotConfidence: ConfidenceNotFound,
			},
		},
	}

	svc := NewService(users, snaps, store)
	res, err := svc.GetCharacterTelemetry(context.Background(), Query{
		UserID:      "u1",
		CharacterID: "c1",
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.fastPathCalls != 1 || store.streamCalls != 0 {
		t.Fatalf("expected fast path only, got fast=%d stream=%d", store.fastPathCalls, store.streamCalls)
	}
	if res.TotalCount != 5 || res.Truncated {
		t.Fatalf("expected totalCount=5 truncated=false, got total=%d truncated=%v", res.TotalCount, res.Truncated)
	}

	// Chronological ASC with activityId ASC tie-breaking:
	// act-notfound-link -> act-deleted-snap -> act-tie-a -> act-tie-b -> act-newest
	gotIDs := make([]string, len(res.Matches))
	for i, m := range res.Matches {
		gotIDs[i] = m.ActivityID
	}
	wantIDs := []string{"act-notfound-link", "act-deleted-snap", "act-tie-a", "act-tie-b", "act-newest"}
	if !slices.Equal(gotIDs, wantIDs) {
		t.Fatalf("unexpected match ordering: got %v, want %v", gotIDs, wantIDs)
	}

	// 0: notFound link, unclassifiable mode
	if res.Matches[0].SnapshotID != "" || res.Matches[0].SnapshotConfidence != ConfidenceNotFound || res.Matches[0].EstimatedGameMode != "" {
		t.Fatalf("unexpected match[0]: %+v", res.Matches[0])
	}
	// 1: deleted snapshot cleared while keeping confidence + legacy competitive classification
	if res.Matches[1].SnapshotID != "" || res.Matches[1].SnapshotConfidence != ConfidenceLow || res.Matches[1].EstimatedGameMode != GameModeCompetitive {
		t.Fatalf("unexpected match[1]: %+v", res.Matches[1])
	}
	// 2: noMatch link cleared while keeping sessionId + confidence + legacy ironBanner classification
	if res.Matches[2].SnapshotID != "" || res.Matches[2].SessionID != "sess-2" || res.Matches[2].SnapshotConfidence != ConfidenceNoMatch || res.Matches[2].EstimatedGameMode != GameModeIronBanner {
		t.Fatalf("unexpected match[2]: %+v", res.Matches[2])
	}
	// 3: soft-archived snapshot normalized to target + legacy quickplay classification
	if res.Matches[3].SnapshotID != "snap-target" || res.Matches[3].EstimatedGameMode != GameModeQuickPlay {
		t.Fatalf("unexpected match[3]: %+v", res.Matches[3])
	}
	// 4: active snapshot
	if res.Matches[4].SnapshotID != "snap-active" || res.Matches[4].TimePlayedSeconds != 512 || !res.Matches[4].HasTimePlayedSeconds {
		t.Fatalf("unexpected match[4]: %+v", res.Matches[4])
	}

	// Snapshots map must only contain active resolved targets actually referenced.
	if len(res.Snapshots) != 2 {
		t.Fatalf("expected 2 resolved snapshots, got %+v", res.Snapshots)
	}
	if res.Snapshots["snap-active"].Name != "Solar Hand Cannon" {
		t.Fatalf("unexpected snap-active summary: %+v", res.Snapshots["snap-active"])
	}
	if res.Snapshots["snap-target"].Name != "Merged Solar Target" {
		t.Fatalf("unexpected snap-target summary: %+v", res.Snapshots["snap-target"])
	}
	if _, hasArchived := res.Snapshots["snap-archived"]; hasArchived {
		t.Fatalf("archived snapshot should not appear in Snapshots map")
	}
}

func TestGetCharacterTelemetryFilteredGameModeAndTruncation(t *testing.T) {
	base := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

	users := &fakeUserReader{
		users: map[string]User{"u1": {ID: "u1", CharacterIDs: []string{"c1"}}},
	}
	snaps := &fakeSnapshotReader{
		snaps: map[string]Snapshot{
			"snap-1": {ID: "snap-1", CharacterID: "c1", Name: "Trials Loadout"},
		},
	}
	store := &fakeMatchStore{
		matches: []RawMatch{
			{
				ActivityID:              "trials-3",
				Period:                  base.Add(40 * time.Minute),
				Activity:                "Trials of Osiris",
				StoredEstimatedGameMode: GameModeTrials,
				RawSnapshotID:           "snap-1",
				SnapshotConfidence:      ConfidenceHigh,
			},
			{
				ActivityID:              "qp-1",
				Period:                  base.Add(30 * time.Minute),
				Activity:                "Control",
				StoredEstimatedGameMode: GameModeQuickPlay,
				RawSnapshotID:           "snap-1",
				SnapshotConfidence:      ConfidenceHigh,
			},
			{
				// Legacy aggregate missing stored EstimatedGameMode — must be included via fallback!
				ActivityID:         "trials-2-legacy",
				Period:             base.Add(20 * time.Minute),
				Activity:           "Trials of Osiris",
				Mode:               "Trials of Osiris",
				RawSnapshotID:      "snap-1",
				SnapshotConfidence: ConfidenceHigh,
			},
			{
				ActivityID:              "trials-1-oldest",
				Period:                  base.Add(10 * time.Minute),
				Activity:                "Trials of Osiris",
				StoredEstimatedGameMode: GameModeTrials,
				RawSnapshotID:           "snap-1",
				SnapshotConfidence:      ConfidenceHigh,
			},
		},
	}

	svc := NewService(users, snaps, store)
	res, err := svc.GetCharacterTelemetry(context.Background(), Query{
		UserID:      "u1",
		CharacterID: "c1",
		GameMode:    GameModeTrials,
		Limit:       2,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.streamCalls != 1 || store.fastPathCalls != 0 {
		t.Fatalf("expected filtered stream path, got stream=%d fast=%d", store.streamCalls, store.fastPathCalls)
	}
	if res.TotalCount != 3 || !res.Truncated {
		t.Fatalf("expected totalCount=3 truncated=true, got total=%d truncated=%v", res.TotalCount, res.Truncated)
	}
	if len(res.Matches) != 2 {
		t.Fatalf("expected 2 most recent trials matches, got %d", len(res.Matches))
	}
	// Returned chronologically ascending: trials-2-legacy, then trials-3
	if res.Matches[0].ActivityID != "trials-2-legacy" || res.Matches[0].EstimatedGameMode != GameModeTrials {
		t.Fatalf("expected trials-2-legacy first with fallback GameModeTrials, got %+v", res.Matches[0])
	}
	if res.Matches[1].ActivityID != "trials-3" {
		t.Fatalf("expected trials-3 second, got %+v", res.Matches[1])
	}
}

func TestGetCharacterTelemetrySingleSnapshotFilterAndAutoForward(t *testing.T) {
	base := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

	users := &fakeUserReader{
		users: map[string]User{"u1": {ID: "u1", CharacterIDs: []string{"c1"}}},
	}
	snaps := &fakeSnapshotReader{
		snaps: map[string]Snapshot{
			"snap-old":    {ID: "snap-old", CharacterID: "c1", Name: "Old", MergedIntoID: "snap-target"},
			"snap-target": {ID: "snap-target", CharacterID: "c1", Name: "Canonical Build"},
		},
	}
	store := &fakeMatchStore{
		snapshotMatches: map[string][]RawMatch{
			"snap-target": {
				{
					ActivityID:              "valid-match",
					Period:                  base.Add(20 * time.Minute),
					Activity:                "Control",
					StoredEstimatedGameMode: GameModeQuickPlay,
					RawSnapshotID:           "snap-target",
					SnapshotConfidence:      ConfidenceHigh,
				},
				{
					// Corrupt/noMatch confidence should be filtered out in Go even if present in snapshotIds array.
					ActivityID:              "invalid-confidence",
					Period:                  base.Add(15 * time.Minute),
					Activity:                "Control",
					StoredEstimatedGameMode: GameModeQuickPlay,
					RawSnapshotID:           "snap-target",
					SnapshotConfidence:      ConfidenceNoMatch,
				},
				{
					// Different snapshot link for this character (e.g. lobby peer used snap-target).
					ActivityID:              "different-snap-link",
					Period:                  base.Add(10 * time.Minute),
					Activity:                "Control",
					StoredEstimatedGameMode: GameModeQuickPlay,
					RawSnapshotID:           "snap-other",
					SnapshotConfidence:      ConfidenceHigh,
				},
			},
		},
	}

	svc := NewService(users, snaps, store)
	res, err := svc.GetCharacterTelemetry(context.Background(), Query{
		UserID:      "u1",
		CharacterID: "c1",
		SnapshotID:  "snap-old",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if store.capturedSnapshotID != "snap-target" {
		t.Fatalf("expected archived snapshot filter to auto-forward to snap-target, got %q", store.capturedSnapshotID)
	}
	if len(res.Matches) != 1 || res.Matches[0].ActivityID != "valid-match" {
		t.Fatalf("expected only valid-match, got %+v", res.Matches)
	}
	if res.TotalCount != 1 || res.Truncated {
		t.Fatalf("expected totalCount=1 truncated=false, got total=%d truncated=%v", res.TotalCount, res.Truncated)
	}
	if res.Snapshots["snap-target"].Name != "Canonical Build" {
		t.Fatalf("expected snap-target summary, got %+v", res.Snapshots)
	}
	// Preloaded snapshot eliminates redundant GetSnapshotsByIDs call.
	if len(snaps.getByIDsCalls) != 0 {
		t.Fatalf("expected 0 GetSnapshotsByIDs calls when snapshot was preloaded, got %d", len(snaps.getByIDsCalls))
	}
}

func TestGetCharacterTelemetryEmptyConfidenceLevelPreservedAndLogsDateBounds(t *testing.T) {
	base := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

	users := &fakeUserReader{
		users: map[string]User{"u1": {ID: "u1", CharacterIDs: []string{"c1"}}},
	}
	snaps := &fakeSnapshotReader{
		snaps: map[string]Snapshot{
			"snap-legacy": {ID: "snap-legacy", UserID: "u1", CharacterID: "c1", Name: "Legacy Reassigned Build"},
		},
	}
	legacyLinked := RawMatch{
		ActivityID:              "act-empty-confidence-linked",
		Period:                  base.Add(20 * time.Minute),
		Activity:                "Control",
		StoredEstimatedGameMode: GameModeQuickPlay,
		RawSnapshotID:           "snap-legacy",
		SnapshotConfidence:      "",
	}
	unlinked := RawMatch{
		ActivityID:              "act-empty-confidence-unlinked",
		Period:                  base.Add(10 * time.Minute),
		Activity:                "Control",
		StoredEstimatedGameMode: GameModeQuickPlay,
		RawSnapshotID:           "",
		SnapshotConfidence:      "",
	}
	store := &fakeMatchStore{
		matches: []RawMatch{legacyLinked, unlinked},
		snapshotMatches: map[string][]RawMatch{
			"snap-legacy": {legacyLinked, unlinked},
		},
	}

	svc := NewService(users, snaps, store)

	t.Run("character-wide query preserves snapshotId when confidenceLevel is empty and logs from/to", func(t *testing.T) {
		from := base
		to := base.Add(time.Hour)
		ctx := clog.NewContext(context.Background())

		res, err := svc.GetCharacterTelemetry(ctx, Query{
			UserID:      "u1",
			CharacterID: "c1",
			From:        from,
			To:          to,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res.Matches) != 2 {
			t.Fatalf("expected 2 matches, got %d", len(res.Matches))
		}
		if res.Matches[0].ActivityID != "act-empty-confidence-unlinked" || res.Matches[0].SnapshotID != "" {
			t.Fatalf("expected unlinked match to remain unlinked, got %+v", res.Matches[0])
		}
		if res.Matches[1].ActivityID != "act-empty-confidence-linked" || res.Matches[1].SnapshotID != "snap-legacy" {
			t.Fatalf("expected legacy empty-confidence link to preserve snapshotId, got %+v", res.Matches[1])
		}
		if res.Snapshots["snap-legacy"].Name != "Legacy Reassigned Build" {
			t.Fatalf("expected snap-legacy in snapshots map, got %+v", res.Snapshots)
		}

		svcAttrs := make(map[string]string)
		var collect func(prefix string, attrs []slog.Attr)
		collect = func(prefix string, attrs []slog.Attr) {
			for _, attr := range attrs {
				key := attr.Key
				if prefix != "" {
					key = prefix + "." + key
				}
				if attr.Value.Kind() == slog.KindGroup {
					collect(key, attr.Value.Group())
					continue
				}
				svcAttrs[key] = attr.Value.String()
			}
		}
		collect("", clog.BagFromContext(ctx).Attrs())
		if svcAttrs["telemetryService.getCharacterTelemetry.from"] != from.Format(time.RFC3339) ||
			svcAttrs["telemetryService.getCharacterTelemetry.to"] != to.Format(time.RFC3339) {
			t.Fatalf("expected service log from=%q to=%q, got %+v", from.Format(time.RFC3339), to.Format(time.RFC3339), svcAttrs)
		}
	})

	t.Run("snapshotId filter includes legacy empty-confidence match and excludes unlinked match", func(t *testing.T) {
		res, err := svc.GetCharacterTelemetry(context.Background(), Query{
			UserID:      "u1",
			CharacterID: "c1",
			SnapshotID:  "snap-legacy",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(res.Matches) != 1 || res.Matches[0].ActivityID != "act-empty-confidence-linked" || res.Matches[0].SnapshotID != "snap-legacy" {
			t.Fatalf("expected only act-empty-confidence-linked, got %+v", res.Matches)
		}
	})
}

type stubUserService struct {
	u   *user.User
	err error
}

func (s *stubUserService) GetUser(_ context.Context, _ string) (*user.User, error) {
	return s.u, s.err
}

type stubSnapshotService struct {
	snap  *api.CharacterSnapshot
	snaps []api.CharacterSnapshot
	err   error
}

func (s *stubSnapshotService) Get(_ context.Context, _ string) (*api.CharacterSnapshot, error) {
	return s.snap, s.err
}

func (s *stubSnapshotService) GetByIDs(_ context.Context, _ []string) ([]api.CharacterSnapshot, error) {
	return s.snaps, s.err
}

func TestAdaptersTranslateNotFoundAndDomainTypes(t *testing.T) {
	t.Run("UserReaderAdapter translates user.NotFound and merges CharacterIDs + Characters", func(t *testing.T) {
		missing := NewUserReaderAdapter(&stubUserService{err: user.NotFound})
		if _, err := missing.GetUser(context.Background(), "u1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}

		okAdapter := NewUserReaderAdapter(&stubUserService{
			u: &user.User{
				ID:           "u1",
				CharacterIDs: []string{"c1", "c2"},
				Characters:   []api.Character{{Id: "c2"}, {Id: "c3"}},
			},
		})
		u, err := okAdapter.GetUser(context.Background(), "u1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !slices.Equal(u.CharacterIDs, []string{"c1", "c2", "c3"}) {
			t.Fatalf("expected deduplicated character IDs [c1 c2 c3], got %v", u.CharacterIDs)
		}
	})

	t.Run("SnapshotReaderAdapter translates snapshot.NotFound, UserID, and MergedIntoID", func(t *testing.T) {
		missing := NewSnapshotReaderAdapter(&stubSnapshotService{err: snapshot.NotFound})
		if _, err := missing.GetSnapshot(context.Background(), "s1"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}

		mergedTarget := "s2"
		okAdapter := NewSnapshotReaderAdapter(&stubSnapshotService{
			snap: &api.CharacterSnapshot{
				ID:           "s1",
				UserID:       "u1",
				CharacterID:  "c1",
				Name:         "Void Hunter",
				MergedIntoID: &mergedTarget,
			},
		})
		snap, err := okAdapter.GetSnapshot(context.Background(), "s1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if snap.ID != "s1" || snap.UserID != "u1" || snap.CharacterID != "c1" || snap.Name != "Void Hunter" || snap.MergedIntoID != "s2" {
			t.Fatalf("unexpected snapshot: %+v", snap)
		}
	})
}

