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
