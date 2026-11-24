package firestore

import (
	"context"
	corefireteam "oneTrick/core/fireteam"
	"testing"
	"time"
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
		Status:             corefireteam.SessionStatusPending,
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
		},
	}

	got := fromSessionDoc(toSessionDoc(orig))
	if got.ID != orig.ID || got.UserID != orig.UserID || got.CharacterID != orig.CharacterID {
		t.Fatalf("basic fields mismatch: %+v", got)
	}
	if got.Status != corefireteam.SessionStatusPending || got.Name != orig.Name || got.Description != orig.Description {
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
	created, err := store.CreateSessions(context.Background(), nil)
	if err != nil || len(created) != 0 {
		t.Fatalf("expected empty created slice, got %+v, err=%v", created, err)
	}
}
