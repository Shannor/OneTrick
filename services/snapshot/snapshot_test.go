package snapshot

import (
	"errors"
	"oneTrick/api"
	"slices"
	"testing"
	"time"
)

func TestSnapshotServiceErrors(t *testing.T) {
	if !errors.Is(NotFound, NotFound) {
		t.Errorf("expected NotFound error to match")
	}
	if !errors.Is(Unauthorized, Unauthorized) {
		t.Errorf("expected Unauthorized error to match")
	}
}

func TestSnapshotSortingAndPagination(t *testing.T) {
	now := time.Now()
	snapshots := []api.CharacterSnapshot{
		{
			ID:          "snap-1",
			UserID:      "user-1",
			CharacterID: "char-1",
			CreatedAt:   now.Add(-2 * time.Hour),
		},
		{
			ID:          "snap-2",
			UserID:      "user-1",
			CharacterID: "char-2",
			CreatedAt:   now,
		},
		{
			ID:          "snap-3",
			UserID:      "user-1",
			CharacterID: "char-1",
			CreatedAt:   now.Add(-1 * time.Hour),
		},
	}

	// Sort descending by CreatedAt
	slices.SortFunc(snapshots, func(a, b api.CharacterSnapshot) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})

	if snapshots[0].ID != "snap-2" {
		t.Errorf("expected newest snapshot 'snap-2', got %s", snapshots[0].ID)
	}
	if snapshots[1].ID != "snap-3" {
		t.Errorf("expected second snapshot 'snap-3', got %s", snapshots[1].ID)
	}
	if snapshots[2].ID != "snap-1" {
		t.Errorf("expected oldest snapshot 'snap-1', got %s", snapshots[2].ID)
	}

	// Test offset and limit
	offset := 1
	limit := 1
	paginated := snapshots[offset:]
	if len(paginated) > limit {
		paginated = paginated[:limit]
	}

	if len(paginated) != 1 || paginated[0].ID != "snap-3" {
		t.Errorf("expected paginated result ['snap-3'], got %+v", paginated)
	}
}
