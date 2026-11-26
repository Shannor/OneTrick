package firestore

import (
	"context"
	"fmt"
	corefireteam "oneTrick/core/fireteam"
	"os"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
)

func TestFireteamSessionStoreDocRoundTrip(t *testing.T) {
	startedAt := time.Date(2026, 5, 2, 18, 0, 0, 0, time.UTC)
	recordedAt := startedAt.Add(time.Minute)
	step := 4
	delta := 120

	orig := corefireteam.Session{
		ID:                 "ses-1",
		UserID:             "u-1",
		CharacterID:        "char-1",
		Name:               "Flawless' Goes Brrr",
		Description:        "Trials run",
		Status:             corefireteam.SessionStatusComplete,
		StartedAt:          startedAt,
		StartedBy:          &corefireteam.AuditField{ID: "u-leader", Username: "Leader"},
		AggregateIDs:       []string{"agg-1"},
		LastSeenActivityID: "act-9",
		LastSeenTimestamp:  &recordedAt,
		CompTracking: &corefireteam.CompTracking{
			Enabled:    true,
			PointDelta: &delta,
			Start: &corefireteam.CompRankSnapshot{
				Points:       5800,
				StepIndex:    &step,
				DivisionName: "Platinum III",
				Icon:         "/img/plat.png",
				RecordedAt:   &recordedAt,
			},
		},
		Summary: &corefireteam.SessionSummary{
			TotalMatches: 3,
			Wins:         2,
			Losses:       1,
			Kills:        42,
			Deaths:       21,
			Assists:      10,
			WinRate:      0.66,
			KDRatio:      2.0,
			KDARatio:     2.24,
			ModesPlayed:  []string{"Competitive"},
			TopWeapons: []corefireteam.SessionWeaponSummary{
				{Name: "Rose", Icon: "/img/rose.png", Kills: 18},
			},
			Companions: []corefireteam.SessionCompanion{
				{
					UserID:        "u-2",
					DisplayName:   "Maverick",
					SharedMatches: 2,
					Wins:          2,
					Losses:        0,
					Sessions: []corefireteam.CompanionInstance{
						{
							SessionID:     "ses-2",
							CharacterID:   "char-2",
							Class:         "Titan",
							EmblemURL:     "https://www.bungie.net/emblem-2.jpg",
							SharedMatches: 2,
						},
					},
				},
			},
		},
	}

	doc := toSessionDoc(orig)
	if doc.Status == nil || *doc.Status != "complete" {
		t.Fatalf("expected Firestore status 'complete', got %v", doc.Status)
	}
	got := fromSessionDoc(doc)
	if got.ID != orig.ID || got.UserID != orig.UserID || got.CharacterID != orig.CharacterID {
		t.Fatalf("basic fields mismatch: %+v", got)
	}
	if got.Status != corefireteam.SessionStatusComplete || got.Name != orig.Name || got.Description != orig.Description {
		t.Fatalf("metadata mismatch: %+v", got)
	}
	if got.StartedBy == nil || got.StartedBy.ID != "u-leader" || got.StartedBy.Username != "Leader" {
		t.Fatalf("startedBy mismatch: %+v", got.StartedBy)
	}
	if got.CompTracking == nil || !got.CompTracking.Enabled || got.CompTracking.Start == nil || got.CompTracking.Start.Points != 5800 || *got.CompTracking.Start.StepIndex != 4 {
		t.Fatalf("compTracking mismatch: %+v", got.CompTracking)
	}
	if got.Summary == nil || got.Summary.TotalMatches != 3 || len(got.Summary.TopWeapons) != 1 || got.Summary.TopWeapons[0].Name != "Rose" {
		t.Fatalf("summary mismatch: %+v", got.Summary)
	}
	if len(got.Summary.Companions) != 1 || got.Summary.Companions[0].UserID != "u-2" || len(got.Summary.Companions[0].Sessions) != 1 || got.Summary.Companions[0].Sessions[0].Class != "Titan" {
		t.Fatalf("summary companions mismatch: %+v", got.Summary.Companions)
	}
}

func TestFireteamSessionStoreEmptyInputs(t *testing.T) {
	store := NewFireteamSessionStore(nil)
	active, err := store.GetActiveByUserIDs(context.Background(), nil)
	if err != nil || len(active) != 0 {
		t.Fatalf("expected empty active slice, got %+v, err=%v", active, err)
	}
	docs, err := store.GetActiveSessionsByUserIDs(context.Background(), []string{})
	if err != nil || len(docs) != 0 {
		t.Fatalf("expected empty docs slice, got %+v, err=%v", docs, err)
	}
	created, err := store.CreateSessionsIfInactive(context.Background(), nil)
	if err != nil || len(created) != 0 {
		t.Fatalf("expected empty created slice, got %+v, err=%v", created, err)
	}
}

func TestFirestoreEmulatorFireteamSessionStore(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("FIRESTORE_EMULATOR_HOST not set; skipping Firestore emulator integration test")
	}

	ctx := context.Background()
	db, err := firestore.NewClient(ctx, "gruntt-destiny-test")
	if err != nil {
		t.Fatalf("failed to create emulator firestore client: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store := NewFireteamSessionStore(db)
	runID := fmt.Sprintf("ft-%d", time.Now().UnixNano())
	userA := "u-a-" + runID
	userB := "u-b-" + runID
	userC := "u-c-" + runID
	base := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	var createdIDs []string
	var cleanupMu sync.Mutex
	trackCleanup := func(id string) {
		if id == "" {
			return
		}
		cleanupMu.Lock()
		createdIDs = append(createdIDs, id)
		cleanupMu.Unlock()
	}
	t.Cleanup(func() {
		cleanupMu.Lock()
		defer cleanupMu.Unlock()
		for _, id := range createdIDs {
			_, _ = db.Collection(sessionsCollection).Doc(id).Delete(context.Background())
		}
	})

	completeDocID := "ses-complete-" + runID
	trackCleanup(completeDocID)
	if _, err := db.Collection(sessionsCollection).Doc(completeDocID).Set(ctx, toSessionDoc(corefireteam.Session{
		ID:           completeDocID,
		UserID:       userA,
		CharacterID:  "char-1",
		Status:       corefireteam.SessionStatusComplete,
		StartedAt:    base.Add(-2 * time.Hour),
		AggregateIDs: []string{},
	})); err != nil {
		t.Fatalf("failed seeding completed session: %v", err)
	}

	firstBatch, err := store.CreateSessionsIfInactive(ctx, []corefireteam.Session{
		{
			UserID:       userA,
			CharacterID:  "char-1",
			Status:       corefireteam.SessionStatusPending,
			StartedAt:    base,
			StartedBy:    &corefireteam.AuditField{ID: userA, Username: "Leader"},
			AggregateIDs: []string{},
			CompTracking: &corefireteam.CompTracking{Enabled: true},
		},
		{
			UserID:       userB,
			CharacterID:  "char-2",
			Status:       corefireteam.SessionStatusPending,
			StartedAt:    base.Add(time.Minute),
			StartedBy:    &corefireteam.AuditField{ID: userA, Username: "Leader"},
			AggregateIDs: []string{},
		},
	})
	if err != nil {
		t.Fatalf("CreateSessionsIfInactive first batch failed: %v", err)
	}
	if len(firstBatch) != 2 || !firstBatch[0].Created || !firstBatch[1].Created {
		t.Fatalf("expected 2 created sessions, got %+v", firstBatch)
	}
	trackCleanup(firstBatch[0].Session.ID)
	trackCleanup(firstBatch[1].Session.ID)

	secondBatch, err := store.CreateSessionsIfInactive(ctx, []corefireteam.Session{
		{
			UserID:       userA,
			CharacterID:  "char-1",
			Status:       corefireteam.SessionStatusPending,
			StartedAt:    base.Add(2 * time.Minute),
			AggregateIDs: []string{},
		},
		{
			UserID:       userC,
			CharacterID:  "char-3",
			Status:       corefireteam.SessionStatusPending,
			StartedAt:    base.Add(2 * time.Minute),
			AggregateIDs: []string{},
		},
	})
	if err != nil {
		t.Fatalf("CreateSessionsIfInactive second batch failed: %v", err)
	}
	if len(secondBatch) != 2 || secondBatch[0].Created || secondBatch[0].Session.ID != firstBatch[0].Session.ID {
		t.Fatalf("expected userA/char-1 skipped with existing session %s, got %+v", firstBatch[0].Session.ID, secondBatch[0])
	}
	if !secondBatch[1].Created || secondBatch[1].Session.ID == "" {
		t.Fatalf("expected userC/char-3 created, got %+v", secondBatch[1])
	}
	trackCleanup(secondBatch[1].Session.ID)

	// Verify chunked >30 userId 'in' query and compTracking.enabled projection exclude completed sessions.
	queryUserIDs := make([]string, 0, 35)
	queryUserIDs = append(queryUserIDs, userA)
	for i := range 32 {
		queryUserIDs = append(queryUserIDs, fmt.Sprintf("u-pad-%s-%d", runID, i))
	}
	queryUserIDs = append(queryUserIDs, userB, userC)

	projected, err := store.GetActiveByUserIDs(ctx, queryUserIDs)
	if err != nil {
		t.Fatalf("GetActiveByUserIDs failed: %v", err)
	}
	if len(projected) != 3 {
		t.Fatalf("expected 3 active projected sessions across chunks, got %d: %+v", len(projected), projected)
	}
	byID := make(map[string]corefireteam.ActiveSession, len(projected))
	for _, item := range projected {
		byID[item.SessionID] = item
	}
	if !byID[firstBatch[0].Session.ID].TrackComp || byID[firstBatch[1].Session.ID].TrackComp {
		t.Fatalf("unexpected TrackComp projection values: %+v", byID)
	}

	activeDocs, err := store.GetActiveSessionsByUserIDs(ctx, queryUserIDs)
	if err != nil {
		t.Fatalf("GetActiveSessionsByUserIDs failed: %v", err)
	}
	if len(activeDocs) != 3 {
		t.Fatalf("expected 3 active full session docs, got %d", len(activeDocs))
	}

	// Concurrent double-click check: two simultaneous transactions targeting the same pair create only 1 session.
	raceUser := "u-race-" + runID
	var (
		wg          sync.WaitGroup
		raceResults = make([]corefireteam.CreateSessionResult, 2)
		raceErrs    = make([]error, 2)
	)
	for i := range 2 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			res, err := store.CreateSessionsIfInactive(ctx, []corefireteam.Session{
				{
					UserID:       raceUser,
					CharacterID:  "char-race",
					Status:       corefireteam.SessionStatusPending,
					StartedAt:    base.Add(3 * time.Minute),
					AggregateIDs: []string{},
				},
			})
			raceErrs[idx] = err
			if len(res) > 0 {
				raceResults[idx] = res[0]
			}
		}(i)
	}
	wg.Wait()

	createdCount := 0
	for i := range 2 {
		if raceErrs[i] != nil {
			t.Fatalf("concurrent CreateSessionsIfInactive[%d] failed: %v", i, raceErrs[i])
		}
		trackCleanup(raceResults[i].Session.ID)
		if raceResults[i].Created {
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("expected exactly 1 created session under concurrency, got %d: %+v", createdCount, raceResults)
	}
	if raceResults[0].Session.ID == "" || raceResults[0].Session.ID != raceResults[1].Session.ID {
		t.Fatalf("expected both concurrent callers to observe the same winning session ID, got %+v", raceResults)
	}
}
