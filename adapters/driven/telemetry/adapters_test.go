package telemetry

import (
	"context"
	"errors"
	"oneTrick/api"
	coretelemetry "oneTrick/core/telemetry"
	"oneTrick/services/snapshot"
	"oneTrick/services/user"
	"slices"
	"testing"
)

type stubUserService struct {
	user.Service
	u   *user.User
	err error
}

func (s *stubUserService) GetUser(_ context.Context, _ string) (*user.User, error) {
	return s.u, s.err
}

type stubSnapshotService struct {
	snapshot.Service
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

func TestUserReaderAdapter(t *testing.T) {
	t.Run("translates user.NotFound to coretelemetry.ErrNotFound", func(t *testing.T) {
		missing := NewUserReader(&stubUserService{err: user.NotFound})
		if _, err := missing.GetUser(context.Background(), "u1"); !errors.Is(err, coretelemetry.ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("merges and deduplicates CharacterIDs and Characters preserving order", func(t *testing.T) {
		okAdapter := NewUserReader(&stubUserService{
			u: &user.User{
				ID:           "u1",
				CharacterIDs: []string{"c1", "c2", ""},
				Characters:   []api.Character{{Id: "c2"}, {Id: "c3"}, {Id: ""}},
			},
		})
		u, err := okAdapter.GetUser(context.Background(), "u1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if u.ID != "u1" || !slices.Equal(u.CharacterIDs, []string{"c1", "c2", "c3"}) {
			t.Fatalf("expected ID=u1 and CharacterIDs=[c1 c2 c3], got %+v", u)
		}
	})
}

func TestSnapshotReaderAdapter(t *testing.T) {
	t.Run("translates snapshot.NotFound to coretelemetry.ErrNotFound", func(t *testing.T) {
		missing := NewSnapshotReader(&stubSnapshotService{err: snapshot.NotFound})
		if _, err := missing.GetSnapshot(context.Background(), "s1"); !errors.Is(err, coretelemetry.ErrNotFound) {
			t.Fatalf("expected ErrNotFound, got %v", err)
		}
	})

	t.Run("maps UserID, CharacterID, Name, and MergedIntoID", func(t *testing.T) {
		mergedTarget := "s2"
		okAdapter := NewSnapshotReader(&stubSnapshotService{
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
