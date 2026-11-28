package session

import (
	"context"
	"errors"
	"fmt"
	"oneTrick/api"
	"oneTrick/services/user"
	"testing"
	"time"

	clog "github.com/Shannor/canonical-logger"
)

func TestGetSessionAggregates(t *testing.T) {
	now := time.Date(2026, 4, 20, 20, 0, 0, 0, time.UTC)

	t.Run("hydrates companion snapshots and participants with zero userService calls when SnapshotLink metadata is stamped", func(t *testing.T) {
		store := newMemorySessionStore()
		store.docs["ses-A"] = &api.Session{
			ID:           "ses-A",
			UserID:       "user-A",
			CharacterID:  "char-A",
			AggregateIDs: []string{"agg-1", "agg-2", "agg-3", "agg-4"},
		}

		aggSvc := newFakeSessionAggregateService()
		// Match 1: Fireteam match with B (Hunter) + opposing player D + random teammate E + detached link G
		aggSvc.seed(api.Aggregate{
			ID:         "agg-1",
			ActivityID: "act-1",
			CreatedAt:  now.Add(-40 * time.Minute),
			ActivityDetails: api.ActivityHistory{
				InstanceID: "act-1",
				IsPrivate:  new(false),
			},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-A": {
					CharacterID: "char-A",
					SessionID:   new("ses-A"),
					SnapshotID:  new("snap-A1"),
					UserID:      new("user-A"),
					DisplayName: new("Alpha#1111"),
				},
				"char-B-hunter": {
					CharacterID:    "char-B-hunter",
					SessionID:      new("ses-B1"),
					SnapshotID:     new("snap-B1"),
					UserID:         new("user-B"),
					DisplayName:    new("Bravo#2222"),
					CharacterClass: new("Hunter"),
					EmblemURL:      new("https://www.bungie.net/emblem-b1.jpg"),
				},
				"char-D-opp": {
					CharacterID: "char-D-opp",
					SessionID:   new("ses-D"),
					SnapshotID:  new("snap-D"),
					UserID:      new("user-D"),
					DisplayName: new("Delta#4444"),
				},
				"char-E-rand": {
					CharacterID: "char-E-rand",
					SessionID:   new("ses-E"),
					SnapshotID:  new("snap-E"),
					UserID:      new("user-E"),
					DisplayName: new("Echo#5555"),
				},
				"char-G-detached": {
					CharacterID: "char-G-detached",
					SessionID:   nil,
					SnapshotID:  new("snap-G"),
					UserID:      new("user-G"),
					DisplayName: new("Golf#7777"),
				},
			},
			Performance: map[string]api.InstancePerformance{
				"char-A":          makeCompanionPerf(0, 17, 99001, "99001"),
				"char-B-hunter":   makeCompanionPerf(0, 17, 99001, "99001"),
				"char-D-opp":      makeCompanionPerf(1, 18, 99001, "99001"),
				"char-E-rand":     makeCompanionPerf(0, 17, 88888, "88888"),
				"char-G-detached": makeCompanionPerf(0, 17, 99001, "99001"),
			},
		})
		// Match 2: Solo match for A (no companions present)
		aggSvc.seed(api.Aggregate{
			ID:         "agg-2",
			ActivityID: "act-2",
			CreatedAt:  now.Add(-30 * time.Minute),
			ActivityDetails: api.ActivityHistory{
				InstanceID: "act-2",
				IsPrivate:  new(false),
			},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-A": {
					CharacterID: "char-A",
					SessionID:   new("ses-A"),
					SnapshotID:  new("snap-A2"),
					UserID:      new("user-A"),
					DisplayName: new("Alpha#1111"),
				},
			},
			Performance: map[string]api.InstancePerformance{
				"char-A": makeCompanionPerf(0, 17, 99002, "99002"),
			},
		})
		// Match 3: Companion B swapped to Warlock (char-B-warlock) with a different snapshot
		aggSvc.seed(api.Aggregate{
			ID:         "agg-3",
			ActivityID: "act-3",
			CreatedAt:  now.Add(-20 * time.Minute),
			ActivityDetails: api.ActivityHistory{
				InstanceID: "act-3",
				IsPrivate:  new(false),
			},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-A": {
					CharacterID: "char-A",
					SessionID:   new("ses-A"),
					SnapshotID:  new("snap-A1"),
					UserID:      new("user-A"),
					DisplayName: new("Alpha#1111"),
				},
				"char-B-warlock": {
					CharacterID:    "char-B-warlock",
					SessionID:      new("ses-B2"),
					SnapshotID:     new("snap-B2"),
					UserID:         new("user-B"),
					DisplayName:    new("Bravo#2222"),
					CharacterClass: new("Warlock"),
					EmblemURL:      new("https://www.bungie.net/emblem-b2.jpg"),
				},
			},
			Performance: map[string]api.InstancePerformance{
				"char-A":         makeCompanionPerf(1, 17, 99003, "99003"),
				"char-B-warlock": makeCompanionPerf(1, 17, 99003, "99003"),
			},
		})
		// Match 4: Private match with C -> owner snapshot hydrated, private companion excluded
		aggSvc.seed(api.Aggregate{
			ID:         "agg-4",
			ActivityID: "act-4",
			CreatedAt:  now.Add(-10 * time.Minute),
			ActivityDetails: api.ActivityHistory{
				InstanceID: "act-4",
				IsPrivate:  new(true),
			},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-A": {
					CharacterID: "char-A",
					SessionID:   new("ses-A"),
					SnapshotID:  new("snap-A1"),
					UserID:      new("user-A"),
				},
				"char-C-private": {
					CharacterID: "char-C-private",
					SessionID:   new("ses-C"),
					SnapshotID:  new("snap-C"),
					UserID:      new("user-C"),
					DisplayName: new("Charlie#3333"),
				},
			},
			Performance: map[string]api.InstancePerformance{
				"char-A":         makeCompanionPerf(0, 17, 99004, "99004"),
				"char-C-private": makeCompanionPerf(0, 17, 99004, "99004"),
			},
		})

		uSvc := &fakeSessionUserService{}
		snapSvc := &fakeSessionSnapshotService{}
		svc := NewService(store, aggSvc, uSvc, nil, nil, nil, snapSvc, nil)

		res, err := svc.GetSessionAggregates(clog.NewContext(context.Background()), "ses-A")
		if err != nil {
			t.Fatalf("GetSessionAggregates failed: %v", err)
		}

		if store.getDocCalls.Load() != 1 {
			t.Fatalf("expected 1 session read, got %d", store.getDocCalls.Load())
		}
		if aggSvc.getCalls.Load() != 1 {
			t.Fatalf("expected 1 aggregate batch read, got %d", aggSvc.getCalls.Load())
		}
		if snapSvc.getByIDsCalls.Load() != 1 {
			t.Fatalf("expected 1 snapshot batch read, got %d", snapSvc.getByIDsCalls.Load())
		}
		if uSvc.findByCharCalls.Load() != 0 || uSvc.userCalls.Load() != 0 || uSvc.membershipCalls.Load() != 0 {
			t.Fatalf("expected ZERO userService calls when SnapshotLink metadata is stamped, got findByChar=%d user=%d membership=%d",
				uSvc.findByCharCalls.Load(), uSvc.userCalls.Load(), uSvc.membershipCalls.Load())
		}

		if len(res.Aggregates) != 4 {
			t.Fatalf("expected 4 aggregates, got %d", len(res.Aggregates))
		}

		// Snapshots must include owner (snap-A1, snap-A2) + verified companions (snap-B1, snap-B2) only.
		wantSnaps := []string{"snap-A1", "snap-A2", "snap-B1", "snap-B2"}
		if len(res.Snapshots) != len(wantSnaps) {
			t.Fatalf("expected %d snapshots (%v), got %d (%v)", len(wantSnaps), wantSnaps, len(res.Snapshots), res.Snapshots)
		}
		for _, id := range wantSnaps {
			if _, ok := res.Snapshots[id]; !ok {
				t.Fatalf("expected snapshot %s to be hydrated", id)
			}
		}
		for _, excluded := range []string{"snap-D", "snap-E", "snap-G", "snap-C"} {
			if _, ok := res.Snapshots[excluded]; ok {
				t.Fatalf("did not expect non-companion snapshot %s to be hydrated", excluded)
			}
		}

		// Participants must include verified companions char-B-hunter and char-B-warlock only.
		if len(res.Participants) != 2 {
			t.Fatalf("expected 2 participants, got %d (%+v)", len(res.Participants), res.Participants)
		}
		hunter, ok := res.Participants["char-B-hunter"]
		if !ok || hunter.UserID != "user-B" || hunter.DisplayName != "Bravo#2222" ||
			hunter.CharacterID != "char-B-hunter" || hunter.CharacterClass != "Hunter" ||
			hunter.EmblemURL != "https://www.bungie.net/emblem-b1.jpg" {
			t.Fatalf("unexpected char-B-hunter participant: %+v", hunter)
		}
		warlock, ok := res.Participants["char-B-warlock"]
		if !ok || warlock.UserID != "user-B" || warlock.CharacterClass != "Warlock" ||
			warlock.EmblemURL != "https://www.bungie.net/emblem-b2.jpg" {
			t.Fatalf("unexpected char-B-warlock participant: %+v", warlock)
		}
	})

	t.Run("falls back to batched userService lookup for legacy links missing participant metadata", func(t *testing.T) {
		store := newMemorySessionStore()
		store.docs["ses-A"] = &api.Session{
			ID:           "ses-A",
			UserID:       "user-A",
			CharacterID:  "char-A",
			AggregateIDs: []string{"agg-legacy"},
		}

		aggSvc := newFakeSessionAggregateService()
		aggSvc.seed(api.Aggregate{
			ID:         "agg-legacy",
			ActivityID: "act-legacy",
			ActivityDetails: api.ActivityHistory{
				InstanceID: "act-legacy",
				IsPrivate:  new(false),
			},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-A": {
					CharacterID: "char-A",
					SessionID:   new("ses-A"),
					SnapshotID:  new("snap-A"),
				},
				"char-B": {
					CharacterID: "char-B",
					SessionID:   new("ses-B"),
					SnapshotID:  new("snap-B"),
				},
			},
			Performance: map[string]api.InstancePerformance{
				"char-A": makeCompanionPerf(0, 17, 55555, "55555"),
				"char-B": makeCompanionPerf(0, 17, 55555, "55555"),
			},
		})

		uSvc := &fakeSessionUserService{
			users: map[string]*user.User{
				"user-B": {
					ID:           "user-B",
					DisplayName:  "LegacyBravo#2222",
					CharacterIDs: []string{"char-B"},
					Characters: []api.Character{
						{Id: "char-B", Class: "Titan", EmblemURL: "https://www.bungie.net/titan.jpg"},
					},
				},
			},
		}
		snapSvc := &fakeSessionSnapshotService{}
		svc := NewService(store, aggSvc, uSvc, nil, nil, nil, snapSvc, nil)

		res, err := svc.GetSessionAggregates(clog.NewContext(context.Background()), "ses-A")
		if err != nil {
			t.Fatalf("GetSessionAggregates failed: %v", err)
		}
		if uSvc.findByCharCalls.Load() != 1 {
			t.Fatalf("expected 1 fallback FindByCharacterIDs call, got %d", uSvc.findByCharCalls.Load())
		}
		p, ok := res.Participants["char-B"]
		if !ok || p.UserID != "user-B" || p.DisplayName != "LegacyBravo#2222" || p.CharacterClass != "Titan" {
			t.Fatalf("unexpected fallback participant: %+v", p)
		}
		if _, ok := res.Snapshots["snap-B"]; !ok {
			t.Fatalf("expected companion snapshot snap-B hydrated after fallback resolution")
		}
	})

	t.Run("deduplicates snapshot IDs across 35 solo matches into one batch call", func(t *testing.T) {
		const charID = "char-1"
		aggIDs := make([]string, 35)
		aggSvc := newFakeSessionAggregateService()
		for i := range 35 {
			aggIDs[i] = fmt.Sprintf("agg-%d", i)
			snapID := "snap-a"
			if i%2 == 1 {
				snapID = "snap-b"
			}
			aggSvc.seed(api.Aggregate{
				ID:         aggIDs[i],
				ActivityID: fmt.Sprintf("act-%d", i),
				SnapshotLinks: map[string]api.SnapshotLink{
					charID: {
						CharacterID: charID,
						SnapshotID:  new(snapID),
					},
				},
			})
		}

		store := newMemorySessionStore()
		store.docs["ses-1"] = &api.Session{
			ID:           "ses-1",
			UserID:       "user-1",
			CharacterID:  charID,
			AggregateIDs: aggIDs,
		}
		snapSvc := &fakeSessionSnapshotService{}
		uSvc := &fakeSessionUserService{}
		svc := NewService(store, aggSvc, uSvc, nil, nil, nil, snapSvc, nil)

		res, err := svc.GetSessionAggregates(clog.NewContext(context.Background()), "ses-1")
		if err != nil {
			t.Fatalf("GetSessionAggregates failed: %v", err)
		}
		if snapSvc.getByIDsCalls.Load() != 1 || len(snapSvc.capturedIDs) != 2 {
			t.Fatalf("expected 1 GetByIDs call with 2 deduplicated IDs, got calls=%d ids=%v", snapSvc.getByIDsCalls.Load(), snapSvc.capturedIDs)
		}
		if len(res.Snapshots) != 2 || len(res.Participants) != 0 {
			t.Fatalf("expected 2 snapshots and 0 participants, got snaps=%d participants=%d", len(res.Snapshots), len(res.Participants))
		}
		if uSvc.findByCharCalls.Load() != 0 {
			t.Fatalf("expected 0 userService calls on solo session, got %d", uSvc.findByCharCalls.Load())
		}
	})

	t.Run("empty session returns non-nil empty collections with zero aggregate or snapshot reads", func(t *testing.T) {
		store := newMemorySessionStore()
		store.docs["ses-empty"] = &api.Session{
			ID:           "ses-empty",
			UserID:       "user-1",
			CharacterID:  "char-1",
			AggregateIDs: nil,
		}
		aggSvc := newFakeSessionAggregateService()
		snapSvc := &fakeSessionSnapshotService{}
		svc := NewService(store, aggSvc, &fakeSessionUserService{}, nil, nil, nil, snapSvc, nil)

		res, err := svc.GetSessionAggregates(clog.NewContext(context.Background()), "ses-empty")
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if res.Aggregates == nil || res.Snapshots == nil || res.Participants == nil {
			t.Fatalf("expected non-nil empty collections, got %+v", res)
		}
		if aggSvc.getCalls.Load() != 0 || snapSvc.getByIDsCalls.Load() != 0 {
			t.Fatalf("expected 0 downstream reads for empty session")
		}
	})

	t.Run("handles legacy session doc missing embedded id field", func(t *testing.T) {
		store := newMemorySessionStore()
		store.docs["ses-legacy"] = &api.Session{
			ID:           "",
			UserID:       "user-A",
			CharacterID:  "char-A",
			AggregateIDs: []string{"agg-1"},
		}
		aggSvc := newFakeSessionAggregateService()
		aggSvc.seed(api.Aggregate{
			ID:         "agg-1",
			ActivityID: "act-1",
			ActivityDetails: api.ActivityHistory{
				InstanceID: "act-1",
				IsPrivate:  new(false),
			},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-A": {
					CharacterID: "char-A",
					SessionID:   new("ses-legacy"),
					SnapshotID:  new("snap-A"),
					UserID:      new("user-A"),
				},
				"char-same-session": {
					CharacterID: "char-same-session",
					SessionID:   new("ses-legacy"),
					SnapshotID:  new("snap-same"),
					UserID:      new("user-X"),
					DisplayName: new("SameSession#0000"),
				},
				"char-B": {
					CharacterID: "char-B",
					SessionID:   new("ses-B"),
					SnapshotID:  new("snap-B"),
					UserID:      new("user-B"),
					DisplayName: new("Bravo#2222"),
				},
			},
			Performance: map[string]api.InstancePerformance{
				"char-A":            makeCompanionPerf(0, 17, 12345, "12345"),
				"char-same-session": makeCompanionPerf(0, 17, 12345, "12345"),
				"char-B":            makeCompanionPerf(0, 17, 12345, "12345"),
			},
		})
		svc := NewService(store, aggSvc, &fakeSessionUserService{}, nil, nil, nil, &fakeSessionSnapshotService{}, nil)

		res, err := svc.GetSessionAggregates(clog.NewContext(context.Background()), "ses-legacy")
		if err != nil {
			t.Fatalf("GetSessionAggregates failed: %v", err)
		}
		if len(res.Participants) != 1 {
			t.Fatalf("expected only char-B participant (same-session link excluded via fallback ses.ID), got %+v", res.Participants)
		}
		if _, ok := res.Participants["char-B"]; !ok {
			t.Fatalf("expected char-B in participants, got %+v", res.Participants)
		}
	})

	t.Run("missing session returns ErrNotFound", func(t *testing.T) {
		store := newMemorySessionStore()
		svc := NewService(store, newFakeSessionAggregateService(), &fakeSessionUserService{}, nil, nil, nil, &fakeSessionSnapshotService{}, nil)

		_, err := svc.GetSessionAggregates(clog.NewContext(context.Background()), "missing-id")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})
}
