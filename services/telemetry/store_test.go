package telemetry

import (
	"context"
	"fmt"
	"oneTrick/api"
	"oneTrick/services/aggregate"
	"oneTrick/services/snapshot"
	"oneTrick/services/user"
	"os"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
)

func TestFirestoreEmulatorTelemetryStoreAndService(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("FIRESTORE_EMULATOR_HOST not set; skipping Firestore emulator integration test")
	}

	ctx := context.Background()
	db, err := firestore.NewClient(ctx, "gruntt-destiny-test")
	if err != nil {
		t.Fatalf("failed to create emulator firestore client: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	runID := fmt.Sprintf("tel-%d", time.Now().UnixNano())
	userID := "user-" + runID
	charID := "char-" + runID
	otherCharID := "other-char-" + runID
	snapActiveID := "snap-active-" + runID
	snapArchivedID := "snap-archived-" + runID
	snapTargetID := "snap-target-" + runID
	base := time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

	var aggIDs []string
	var snapIDs []string
	t.Cleanup(func() {
		_, _ = db.Collection("users").Doc(userID).Delete(context.Background())
		for _, id := range snapIDs {
			_, _ = db.Collection("snapshots").Doc(id).Delete(context.Background())
		}
		for _, id := range aggIDs {
			_, _ = db.Collection("aggregates").Doc(id).Delete(context.Background())
		}
	})

	// Seed user document.
	_, err = db.Collection("users").Doc(userID).Set(ctx, user.User{
		ID:                  userID,
		MemberID:            "member-" + runID,
		PrimaryMembershipID: "primary-" + runID,
		DisplayName:         "TelemetryGuardian",
		CharacterIDs:        []string{charID},
		CreatedAt:           base,
	})
	if err != nil {
		t.Fatalf("failed seeding user: %v", err)
	}

	// Seed active, archived, and merge-target snapshots.
	seedSnap := func(s api.CharacterSnapshot) {
		if _, err := db.Collection("snapshots").Doc(s.ID).Set(ctx, s); err != nil {
			t.Fatalf("failed seeding snapshot %s: %v", s.ID, err)
		}
		snapIDs = append(snapIDs, s.ID)
	}
	seedSnap(api.CharacterSnapshot{
		ID:          snapActiveID,
		UserID:      userID,
		CharacterID: charID,
		Name:        "Solar Hand Cannon",
		Hash:        "hash-active-" + runID,
		CreatedAt:   base,
		UpdatedAt:   base,
		Loadout:     api.Loadout{},
	})
	seedSnap(api.CharacterSnapshot{
		ID:          snapTargetID,
		UserID:      userID,
		CharacterID: charID,
		Name:        "Void Pulse Target",
		Hash:        "hash-target-" + runID,
		CreatedAt:   base,
		UpdatedAt:   base,
		Loadout:     api.Loadout{},
	})
	seedSnap(api.CharacterSnapshot{
		ID:           snapArchivedID,
		UserID:       userID,
		CharacterID:  charID,
		Name:         "Archived Void Pulse",
		Hash:         "hash-archived-" + runID,
		CreatedAt:    base,
		UpdatedAt:    base,
		Loadout:      api.Loadout{},
		MergedIntoID: new(snapTargetID),
	})

	seedAgg := func(agg api.Aggregate) {
		if _, err := db.Collection("aggregates").Doc(agg.ID).Set(ctx, agg); err != nil {
			t.Fatalf("failed seeding aggregate %s: %v", agg.ID, err)
		}
		aggIDs = append(aggIDs, agg.ID)
	}

	// 1. Modern Trials match with heavy weapons/extra and another lobby character (verifies SelectPaths).
	seedAgg(api.Aggregate{
		ID:           runID + "-agg-1",
		ActivityID:   "act-1",
		CharacterIds: []string{charID, otherCharID},
		SnapshotIds:  []string{snapActiveID},
		SessionIds:   []string{"sess-1"},
		CreatedAt:    base.Add(40 * time.Minute),
		ActivityDetails: api.ActivityHistory{
			InstanceID:        "act-1",
			Period:            base.Add(40 * time.Minute),
			Location:          "Javelin-4",
			Activity:          "Trials of Osiris",
			Mode:              new("Trials of Osiris"),
			EstimatedGameMode: new(api.GameModeTrials),
		},
		Performance: map[string]api.InstancePerformance{
			charID: {
				PlayerStats: api.PlayerStats{
					Kills:      new(api.StatsValuePair{Value: new(14.0)}),
					Deaths:     new(api.StatsValuePair{Value: new(5.0)}),
					Assists:    new(api.StatsValuePair{Value: new(6.0)}),
					Standing:   new(api.StatsValuePair{Value: new(0.0)}),
					TimePlayed: new(api.StatsValuePair{Value: new(512.0)}),
				},
				Weapons: map[string]api.WeaponInstanceMetrics{
					"12345": {ReferenceID: new(int64(12345))},
				},
				Extra: &map[string]api.UniqueStatValue{
					"precisionKills": {Basic: api.StatsValuePair{Value: new(9.0)}},
				},
			},
			otherCharID: {
				PlayerStats: api.PlayerStats{
					Kills:    new(api.StatsValuePair{Value: new(1.0)}),
					Deaths:   new(api.StatsValuePair{Value: new(99.0)}),
					Standing: new(api.StatsValuePair{Value: new(1.0)}),
				},
			},
		},
		SnapshotLinks: map[string]api.SnapshotLink{
			charID: {
				CharacterID:      charID,
				SnapshotID:       new(snapActiveID),
				SessionID:        new("sess-1"),
				ConfidenceLevel:  api.HighConfidenceLevel,
				ConfidenceSource: api.SystemConfidenceSource,
				CreatedAt:        base.Add(40 * time.Minute),
			},
			otherCharID: {
				CharacterID:      otherCharID,
				SnapshotID:       new("other-snap"),
				ConfidenceLevel:  api.HighConfidenceLevel,
				ConfidenceSource: api.SystemConfidenceSource,
				CreatedAt:        base.Add(40 * time.Minute),
			},
		},
	})

	// 2. Legacy Trials match lacking stored EstimatedGameMode, nil TimePlayed, referencing archived snapshot.
	seedAgg(api.Aggregate{
		ID:           runID + "-agg-2",
		ActivityID:   "act-2",
		CharacterIds: []string{charID},
		SnapshotIds:  []string{snapArchivedID},
		CreatedAt:    base.Add(30 * time.Minute),
		ActivityDetails: api.ActivityHistory{
			InstanceID: "act-2",
			Period:     base.Add(30 * time.Minute),
			Location:   "Burnout",
			Activity:   "Trials of Osiris",
			Mode:       new("Trials of Osiris"),
		},
		Performance: map[string]api.InstancePerformance{
			charID: {
				PlayerStats: api.PlayerStats{
					Kills:    new(api.StatsValuePair{Value: new(8.0)}),
					Deaths:   new(api.StatsValuePair{Value: new(7.0)}),
					Assists:  new(api.StatsValuePair{Value: new(3.0)}),
					Standing: new(api.StatsValuePair{Value: new(1.0)}),
				},
			},
		},
		SnapshotLinks: map[string]api.SnapshotLink{
			charID: {
				CharacterID:      charID,
				SnapshotID:       new(snapArchivedID),
				ConfidenceLevel:  api.MediumConfidenceLevel,
				ConfidenceSource: api.SystemConfidenceSource,
				CreatedAt:        base.Add(30 * time.Minute),
			},
		},
	})

	// 3. Quickplay match with noMatch link (nil SnapshotID & SessionID in Firestore).
	seedAgg(api.Aggregate{
		ID:           runID + "-agg-3",
		ActivityID:   "act-3",
		CharacterIds: []string{charID},
		CreatedAt:    base.Add(20 * time.Minute),
		ActivityDetails: api.ActivityHistory{
			InstanceID:        "act-3",
			Period:            base.Add(20 * time.Minute),
			Location:          "Endless Vale",
			Activity:          "Control",
			Mode:              nil,
			EstimatedGameMode: new(api.GameModeQuickPlay),
		},
		Performance: map[string]api.InstancePerformance{
			charID: {
				PlayerStats: api.PlayerStats{
					Kills:    new(api.StatsValuePair{Value: new(22.0)}),
					Deaths:   new(api.StatsValuePair{Value: new(11.0)}),
					Assists:  new(api.StatsValuePair{Value: new(5.0)}),
					Standing: new(api.StatsValuePair{Value: new(0.0)}),
				},
			},
		},
		SnapshotLinks: map[string]api.SnapshotLink{
			charID: {
				CharacterID:      charID,
				ConfidenceLevel:  api.NoMatchConfidenceLevel,
				ConfidenceSource: api.SystemConfidenceSource,
				CreatedAt:        base.Add(20 * time.Minute),
			},
		},
	})

	// 4. Corrupt/partial aggregate missing performance[charID] — must be skipped cleanly.
	seedAgg(api.Aggregate{
		ID:           runID + "-agg-corrupt",
		ActivityID:   "act-corrupt",
		CharacterIds: []string{charID},
		CreatedAt:    base.Add(10 * time.Minute),
		ActivityDetails: api.ActivityHistory{
			InstanceID:        "act-corrupt",
			Period:            base.Add(10 * time.Minute),
			Location:          "Dead Cliffs",
			Activity:          "Control",
			EstimatedGameMode: new(api.GameModeQuickPlay),
		},
		Performance: map[string]api.InstancePerformance{
			otherCharID: {
				PlayerStats: api.PlayerStats{Kills: new(api.StatsValuePair{Value: new(5.0)})},
			},
		},
		SnapshotLinks: map[string]api.SnapshotLink{},
	})

	userSvc := user.NewUserService(db, nil)
	aggSvc := aggregate.NewService(db)
	snapSvc := snapshot.NewService(db, userSvc, nil, aggSvc)
	store := NewFirestoreMatchStore(db)
	svc := NewService(NewUserReaderAdapter(userSvc), NewSnapshotReaderAdapter(snapSvc), store)

	t.Run("character-wide fast path non-truncated skips corrupt doc and normalizes archived snapshot", func(t *testing.T) {
		res, err := svc.GetCharacterTelemetry(ctx, Query{
			UserID:      "primary-" + runID,
			CharacterID: charID,
			Limit:       10,
		})
		if err != nil {
			t.Fatalf("GetCharacterTelemetry failed: %v", err)
		}
		if res.TotalCount != 3 || res.Truncated {
			t.Fatalf("expected totalCount=3 truncated=false, got total=%d truncated=%v", res.TotalCount, res.Truncated)
		}
		if len(res.Matches) != 3 {
			t.Fatalf("expected 3 matches, got %d", len(res.Matches))
		}
		// Ascending chronological order: act-3 -> act-2 -> act-1
		if res.Matches[0].ActivityID != "act-3" || res.Matches[1].ActivityID != "act-2" || res.Matches[2].ActivityID != "act-1" {
			t.Fatalf("unexpected ordering: %+v", res.Matches)
		}
		// act-3: noMatch link
		if res.Matches[0].SnapshotID != "" || res.Matches[0].SnapshotConfidence != ConfidenceNoMatch || !res.Matches[0].Won {
			t.Fatalf("unexpected act-3: %+v", res.Matches[0])
		}
		// act-2: legacy Trials + archived snapshot normalized to target
		if res.Matches[1].EstimatedGameMode != GameModeTrials || res.Matches[1].SnapshotID != snapTargetID || res.Matches[1].HasTimePlayedSeconds || res.Matches[1].Won {
			t.Fatalf("unexpected act-2: %+v", res.Matches[1])
		}
		// act-1: active snapshot + timePlayedSeconds
		if res.Matches[2].SnapshotID != snapActiveID || res.Matches[2].TimePlayedSeconds != 512 || !res.Matches[2].HasTimePlayedSeconds || res.Matches[2].SessionID != "sess-1" || !res.Matches[2].Won {
			t.Fatalf("unexpected act-1: %+v", res.Matches[2])
		}
		if len(res.Snapshots) != 2 || res.Snapshots[snapActiveID].Name != "Solar Hand Cannon" || res.Snapshots[snapTargetID].Name != "Void Pulse Target" {
			t.Fatalf("unexpected snapshots map: %+v", res.Snapshots)
		}
	})

	t.Run("character-wide fast path truncated triggers Firestore WithCount aggregation", func(t *testing.T) {
		res, err := svc.GetCharacterTelemetry(ctx, Query{
			UserID:      userID,
			CharacterID: charID,
			Limit:       2,
		})
		if err != nil {
			t.Fatalf("GetCharacterTelemetry failed: %v", err)
		}
		if !res.Truncated || len(res.Matches) != 2 {
			t.Fatalf("expected truncated=true len=2, got truncated=%v len=%d", res.Truncated, len(res.Matches))
		}
		// 4 docs exist in Firestore for charID (corrupt doc is at offset 3 so not seen in Limit(3)), count >= 3.
		if res.TotalCount < 3 {
			t.Fatalf("expected totalCount >= 3, got %d", res.TotalCount)
		}
		if res.Matches[0].ActivityID != "act-2" || res.Matches[1].ActivityID != "act-1" {
			t.Fatalf("expected most recent 2 matches [act-2 act-1], got [%s %s]", res.Matches[0].ActivityID, res.Matches[1].ActivityID)
		}
	})

	t.Run("gameMode filter includes legacy aggregates and respects from/to inclusive bounds", func(t *testing.T) {
		res, err := svc.GetCharacterTelemetry(ctx, Query{
			UserID:      userID,
			CharacterID: charID,
			GameMode:    GameModeTrials,
			From:        base.Add(30 * time.Minute),
			To:          base.Add(40 * time.Minute),
		})
		if err != nil {
			t.Fatalf("GetCharacterTelemetry failed: %v", err)
		}
		if res.TotalCount != 2 || res.Truncated || len(res.Matches) != 2 {
			t.Fatalf("expected 2 Trials matches, got total=%d truncated=%v len=%d", res.TotalCount, res.Truncated, len(res.Matches))
		}
		if res.Matches[0].ActivityID != "act-2" || res.Matches[1].ActivityID != "act-1" {
			t.Fatalf("unexpected Trials matches: %+v", res.Matches)
		}
	})

	t.Run("single-loadout snapshotId query uses snapshotIds branch", func(t *testing.T) {
		res, err := svc.GetCharacterTelemetry(ctx, Query{
			UserID:      userID,
			CharacterID: charID,
			SnapshotID:  snapActiveID,
		})
		if err != nil {
			t.Fatalf("GetCharacterTelemetry failed: %v", err)
		}
		if res.TotalCount != 1 || res.Truncated || len(res.Matches) != 1 || res.Matches[0].ActivityID != "act-1" {
			t.Fatalf("expected single match act-1, got total=%d truncated=%v matches=%+v", res.TotalCount, res.Truncated, res.Matches)
		}
		if len(res.Snapshots) != 1 || res.Snapshots[snapActiveID].Name != "Solar Hand Cannon" {
			t.Fatalf("unexpected snapshots map: %+v", res.Snapshots)
		}
	})
}
