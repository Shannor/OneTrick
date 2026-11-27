package session

import (
	"context"
	"errors"
	"oneTrick/api"
	"oneTrick/services/user"
	"testing"
	"time"

	clog "github.com/Shannor/canonical-logger"
)

func TestBackfillSessionSummaries(t *testing.T) {
	now := time.Date(2026, 4, 20, 21, 0, 0, 0, time.UTC)

	seedHistoricalFireteam := func() (*memorySessionStore, *fakeSessionAggregateService, *fakeSessionUserService) {
		store := newMemorySessionStore()
		store.docs["ses-A"] = &api.Session{
			ID:           "ses-A",
			UserID:       "user-A",
			CharacterID:  "char-A",
			AggregateIDs: []string{"agg-shared", "agg-solo-A"},
			Summary: &api.SessionSummary{
				TotalMatches: new(2),
				Wins:         new(2),
				Losses:       new(0),
			},
		}
		store.docs["ses-B"] = &api.Session{
			ID:           "ses-B",
			UserID:       "user-B",
			CharacterID:  "char-B",
			AggregateIDs: []string{"agg-shared"},
		}
		store.docs["ses-solo-only"] = &api.Session{
			ID:           "ses-solo-only",
			UserID:       "user-S",
			CharacterID:  "char-S",
			AggregateIDs: []string{"agg-solo-S"},
		}

		aggSvc := newFakeSessionAggregateService()
		// Multi-character historical aggregate missing participant metadata on both links
		aggSvc.seed(api.Aggregate{
			ID:         "agg-shared",
			ActivityID: "act-shared",
			CreatedAt:  now.Add(-20 * time.Minute),
			ActivityDetails: api.ActivityHistory{
				InstanceID: "act-shared",
				Mode:       new("Control"),
				IsPrivate:  new(false),
			},
			SessionIds: []string{"ses-A", "ses-B"},
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
				"char-A": makeCompanionPerf(0, 17, 777001, "777001"),
				"char-B": makeCompanionPerf(0, 17, 777001, "777001"),
			},
		})
		// Solo match for A (no companions)
		aggSvc.seed(api.Aggregate{
			ID:         "agg-solo-A",
			ActivityID: "act-solo-A",
			CreatedAt:  now.Add(-10 * time.Minute),
			ActivityDetails: api.ActivityHistory{
				InstanceID: "act-solo-A",
				Mode:       new("Control"),
				IsPrivate:  new(false),
			},
			SessionIds: []string{"ses-A"},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-A": {
					CharacterID: "char-A",
					SessionID:   new("ses-A"),
					SnapshotID:  new("snap-A"),
				},
			},
			Performance: map[string]api.InstancePerformance{
				"char-A": makeCompanionPerf(0, 17, 777002, "777002"),
			},
		})
		// Completely solo session S -> must not be scanned or touched
		aggSvc.seed(api.Aggregate{
			ID:         "agg-solo-S",
			ActivityID: "act-solo-S",
			CreatedAt:  now.Add(-5 * time.Minute),
			ActivityDetails: api.ActivityHistory{
				InstanceID: "act-solo-S",
				Mode:       new("Control"),
				IsPrivate:  new(false),
			},
			SessionIds: []string{"ses-solo-only"},
			SnapshotLinks: map[string]api.SnapshotLink{
				"char-S": {
					CharacterID: "char-S",
					SessionID:   new("ses-solo-only"),
				},
			},
			Performance: map[string]api.InstancePerformance{
				"char-S": makeCompanionPerf(0, 17, 777003, "777003"),
			},
		})

		uSvc := &fakeSessionUserService{
			users: map[string]*user.User{
				"user-A": {
					ID:           "user-A",
					DisplayName:  "Alpha#1111",
					CharacterIDs: []string{"char-A"},
					Characters: []api.Character{
						{Id: "char-A", Class: "Hunter", EmblemURL: "https://www.bungie.net/a.jpg"},
					},
				},
				"user-B": {
					ID:           "user-B",
					DisplayName:  "Bravo#2222",
					CharacterIDs: []string{"char-B"},
					Characters: []api.Character{
						{Id: "char-B", Class: "Titan", EmblemURL: ""}, // intentionally empty emblem to test partial-metadata idempotency
					},
				},
			},
		}
		return store, aggSvc, uSvc
	}

	t.Run("backfills SnapshotLinks and SessionSummary companions idempotently while leaving solo sessions untouched", func(t *testing.T) {
		store, aggSvc, uSvc := seedHistoricalFireteam()
		svc := NewService(store, aggSvc, uSvc, nil, nil, nil)
		ctx := clog.NewContext(context.Background())

		// Run 1: populates multi-character aggregate SnapshotLinks and both sessions' companions.
		res1, err := svc.BackfillSessionSummaries(ctx, false)
		if err != nil {
			t.Fatalf("BackfillSessionSummaries run 1 failed: %v", err)
		}
		if res1.DryRun || res1.AggregatesScanned != 1 || res1.AggregatesUpdated != 1 || res1.AggregatesFailed != 0 ||
			res1.SessionsScanned != 2 || res1.SessionsUpdated != 2 || res1.SessionsFailed != 0 {
			t.Fatalf("unexpected run 1 result: %+v", res1)
		}
		if aggSvc.getCalls.Load() != 0 {
			t.Fatalf("expected 0 extra GetAggregates re-reads during backfill, got %d", aggSvc.getCalls.Load())
		}

		// Verify SnapshotLinks on agg-shared were stamped.
		shared := aggSvc.byID["agg-shared"]
		linkA := shared.SnapshotLinks["char-A"]
		linkB := shared.SnapshotLinks["char-B"]
		if linkA.UserID == nil || *linkA.UserID != "user-A" || linkA.DisplayName == nil || *linkA.DisplayName != "Alpha#1111" ||
			linkA.CharacterClass == nil || *linkA.CharacterClass != "Hunter" || linkA.EmblemURL == nil || *linkA.EmblemURL != "https://www.bungie.net/a.jpg" {
			t.Fatalf("unexpected stamped linkA: %+v", linkA)
		}
		if linkB.UserID == nil || *linkB.UserID != "user-B" || linkB.DisplayName == nil || *linkB.DisplayName != "Bravo#2222" ||
			linkB.CharacterClass == nil || *linkB.CharacterClass != "Titan" {
			t.Fatalf("unexpected stamped linkB: %+v", linkB)
		}

		// Verify companions populated on both ses-A and ses-B, and ses-solo-only untouched.
		sesA := store.docs["ses-A"]
		if sesA.Summary == nil || sesA.Summary.Companions == nil || len(*sesA.Summary.Companions) != 1 {
			t.Fatalf("expected 1 companion on ses-A, got %+v", sesA.Summary)
		}
		if (*sesA.Summary.Companions)[0].UserID != "user-B" || (*sesA.Summary.Companions)[0].SharedMatches != 1 {
			t.Fatalf("unexpected companion on ses-A: %+v", (*sesA.Summary.Companions)[0])
		}
		if *sesA.Summary.TotalMatches != 2 {
			t.Fatalf("expected ses-A TotalMatches=2 (including solo match), got %d", *sesA.Summary.TotalMatches)
		}

		sesB := store.docs["ses-B"]
		if sesB.Summary == nil || sesB.Summary.Companions == nil || len(*sesB.Summary.Companions) != 1 {
			t.Fatalf("expected 1 companion on ses-B, got %+v", sesB.Summary)
		}
		if (*sesB.Summary.Companions)[0].UserID != "user-A" {
			t.Fatalf("unexpected companion on ses-B: %+v", (*sesB.Summary.Companions)[0])
		}

		if store.docs["ses-solo-only"].Summary != nil {
			t.Fatalf("expected solo-only session to remain untouched")
		}

		// Run 2: completely idempotent — zero writes on both aggregates and sessions even with empty emblemURL on char-B.
		writesBefore := store.writes.Load()
		aggWritesBefore := aggSvc.writes.Load()
		res2, err := svc.BackfillSessionSummaries(ctx, false)
		if err != nil {
			t.Fatalf("BackfillSessionSummaries run 2 failed: %v", err)
		}
		if res2.AggregatesScanned != 1 || res2.AggregatesUpdated != 0 || res2.AggregatesFailed != 0 ||
			res2.SessionsScanned != 2 || res2.SessionsUpdated != 0 || res2.SessionsFailed != 0 {
			t.Fatalf("expected 0 updates on idempotent run 2, got %+v", res2)
		}
		if store.writes.Load() != writesBefore || aggSvc.writes.Load() != aggWritesBefore {
			t.Fatalf("expected 0 Firestore writes on idempotent run 2")
		}
	})

	t.Run("dryRun computes accurate update counts with zero writes", func(t *testing.T) {
		store, aggSvc, uSvc := seedHistoricalFireteam()
		svc := NewService(store, aggSvc, uSvc, nil, nil, nil)

		res, err := svc.BackfillSessionSummaries(clog.NewContext(context.Background()), true)
		if err != nil {
			t.Fatalf("BackfillSessionSummaries dryRun failed: %v", err)
		}
		if !res.DryRun || res.AggregatesScanned != 1 || res.AggregatesUpdated != 1 || res.SessionsScanned != 2 || res.SessionsUpdated != 2 {
			t.Fatalf("unexpected dryRun counts: %+v", res)
		}
		if aggSvc.writes.Load() != 0 || aggSvc.stampCalls.Load() != 0 || store.writes.Load() != 0 {
			t.Fatalf("expected 0 writes in dryRun mode, got aggWrites=%d stampCalls=%d sessionWrites=%d",
				aggSvc.writes.Load(), aggSvc.stampCalls.Load(), store.writes.Load())
		}
		if aggSvc.byID["agg-shared"].SnapshotLinks["char-A"].UserID != nil {
			t.Fatalf("dryRun must not mutate stored aggregate SnapshotLinks")
		}
	})

	t.Run("skips missing sessions and records aggregate write errors without aborting", func(t *testing.T) {
		store, aggSvc, uSvc := seedHistoricalFireteam()
		delete(store.docs, "ses-B")
		aggSvc.stampErrByAgg = map[string]error{"agg-shared": errors.New("firestore unavailable")}

		svc := NewService(store, aggSvc, uSvc, nil, nil, nil)
		res, err := svc.BackfillSessionSummaries(clog.NewContext(context.Background()), false)
		if err != nil {
			t.Fatalf("expected backfill to continue past per-item errors, got %v", err)
		}
		if res.AggregatesFailed != 1 || res.AggregatesUpdated != 0 {
			t.Fatalf("expected 1 failed aggregate and 0 updated, got %+v", res)
		}
		if res.SessionsScanned != 1 || res.SessionsUpdated != 1 || res.SessionsFailed != 0 {
			t.Fatalf("expected ses-A scanned/updated and deleted ses-B skipped, got %+v", res)
		}
	})

	t.Run("fails fast when user lookup errors instead of persisting partial state", func(t *testing.T) {
		store, aggSvc, uSvc := seedHistoricalFireteam()
		uSvc.findByCharErr = errors.New("firestore users unavailable")
		svc := NewService(store, aggSvc, uSvc, nil, nil, nil)

		_, err := svc.BackfillSessionSummaries(clog.NewContext(context.Background()), false)
		if err == nil {
			t.Fatalf("expected error when FindByCharacterIDs fails")
		}
		if aggSvc.writes.Load() != 0 || store.writes.Load() != 0 {
			t.Fatalf("expected 0 writes on user lookup failure, got aggWrites=%d sessionWrites=%d", aggSvc.writes.Load(), store.writes.Load())
		}
	})
}
