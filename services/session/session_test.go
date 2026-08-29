package session

import (
	"oneTrick/api"
	"oneTrick/ptr"
	"slices"
	"testing"
	"time"
)

func TestSessionSorting(t *testing.T) {
	now := time.Now()
	sessions := []api.Session{
		{
			ID:        "1",
			StartedAt: now.Add(-2 * time.Hour),
			Status:    ptr.Of(api.SessionComplete),
		},
		{
			ID:        "2",
			StartedAt: now,
			Status:    ptr.Of(api.SessionPending),
		},
		{
			ID:        "3",
			StartedAt: now.Add(-1 * time.Hour),
			Status:    ptr.Of(api.SessionComplete),
		},
	}

	slices.SortFunc(sessions, func(a, b api.Session) int {
		return b.StartedAt.Compare(a.StartedAt)
	})

	if sessions[0].ID != "2" {
		t.Errorf("expected newest session first ('2'), got %s", sessions[0].ID)
	}
	if sessions[1].ID != "3" {
		t.Errorf("expected second session '3', got %s", sessions[1].ID)
	}
	if sessions[2].ID != "1" {
		t.Errorf("expected oldest session last ('1'), got %s", sessions[2].ID)
	}
}

func TestSessionWithSummary(t *testing.T) {
	summary := api.SessionSummary{
		TotalMatches: ptr.Of(5),
		Wins:         ptr.Of(3),
		Losses:       ptr.Of(2),
		WinRate:      ptr.Of(0.60),
		Kills:        ptr.Of(45),
		Deaths:       ptr.Of(25),
		Assists:      ptr.Of(15),
		KDRatio:      ptr.Of(1.80),
		KDARatio:     ptr.Of(2.10),
		ModesPlayed:  ptr.Of([]string{"Control", "Trials of Osiris"}),
		TopWeapons: ptr.Of([]api.SessionWeaponSummary{
			{
				Name:  ptr.Of("Igneous Hammer"),
				Icon:  ptr.Of("/img/igneous.png"),
				Kills: ptr.Of(20),
			},
		}),
	}

	session := api.Session{
		ID:           "test-session-1",
		StartedAt:    time.Now(),
		UserID:       "user-1",
		CharacterID:  "char-1",
		AggregateIDs: []string{"agg-1", "agg-2"},
		Summary:      &summary,
	}

	if session.ID != "test-session-1" || session.UserID != "user-1" || session.CharacterID != "char-1" || len(session.AggregateIDs) != 2 {
		t.Errorf("session fields mismatch: %+v", session)
	}
	if session.Summary == nil {
		t.Fatalf("expected session summary to be present")
	}
	if *session.Summary.TotalMatches != 5 {
		t.Errorf("expected total matches 5, got %d", *session.Summary.TotalMatches)
	}
	if *session.Summary.WinRate != 0.60 {
		t.Errorf("expected win rate 0.60, got %f", *session.Summary.WinRate)
	}
	if len(*session.Summary.TopWeapons) != 1 || *(*session.Summary.TopWeapons)[0].Name != "Igneous Hammer" {
		t.Errorf("expected top weapon 'Igneous Hammer'")
	}
}
