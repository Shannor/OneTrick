package telemetry

import (
	"context"
	"errors"
	"oneTrick/api"
	"oneTrick/services/snapshot"
	"oneTrick/services/user"
)

type userServiceGetter interface {
	GetUser(ctx context.Context, id string) (*user.User, error)
}

type UserReaderAdapter struct {
	users userServiceGetter
}

var _ UserReader = (*UserReaderAdapter)(nil)

func NewUserReaderAdapter(users userServiceGetter) *UserReaderAdapter {
	return &UserReaderAdapter{users: users}
}

func (a *UserReaderAdapter) GetUser(ctx context.Context, lookupID string) (User, error) {
	u, err := a.users.GetUser(ctx, lookupID)
	if err != nil {
		if errors.Is(err, user.NotFound) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	if u == nil {
		return User{}, ErrNotFound
	}

	seen := make(map[string]struct{}, len(u.CharacterIDs)+len(u.Characters))
	charIDs := make([]string, 0, len(u.CharacterIDs)+len(u.Characters))
	for _, id := range u.CharacterIDs {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		charIDs = append(charIDs, id)
	}
	for _, c := range u.Characters {
		if c.Id == "" {
			continue
		}
		if _, ok := seen[c.Id]; ok {
			continue
		}
		seen[c.Id] = struct{}{}
		charIDs = append(charIDs, c.Id)
	}

	return User{
		ID:           u.ID,
		CharacterIDs: charIDs,
	}, nil
}

type snapshotServiceGetter interface {
	Get(ctx context.Context, snapshotID string) (*api.CharacterSnapshot, error)
	GetByIDs(ctx context.Context, snapshotIDs []string) ([]api.CharacterSnapshot, error)
}

type SnapshotReaderAdapter struct {
	snapshots snapshotServiceGetter
}

var _ SnapshotReader = (*SnapshotReaderAdapter)(nil)

func NewSnapshotReaderAdapter(snapshots snapshotServiceGetter) *SnapshotReaderAdapter {
	return &SnapshotReaderAdapter{snapshots: snapshots}
}

func (a *SnapshotReaderAdapter) GetSnapshot(ctx context.Context, snapshotID string) (Snapshot, error) {
	snap, err := a.snapshots.Get(ctx, snapshotID)
	if err != nil {
		if errors.Is(err, snapshot.NotFound) {
			return Snapshot{}, ErrNotFound
		}
		return Snapshot{}, err
	}
	if snap == nil {
		return Snapshot{}, ErrNotFound
	}
	return toDomainSnapshot(*snap), nil
}

func (a *SnapshotReaderAdapter) GetSnapshotsByIDs(ctx context.Context, snapshotIDs []string) ([]Snapshot, error) {
	if len(snapshotIDs) == 0 {
		return nil, nil
	}
	snaps, err := a.snapshots.GetByIDs(ctx, snapshotIDs)
	if err != nil {
		return nil, err
	}
	out := make([]Snapshot, 0, len(snaps))
	for _, snap := range snaps {
		out = append(out, toDomainSnapshot(snap))
	}
	return out, nil
}

func toDomainSnapshot(snap api.CharacterSnapshot) Snapshot {
	mergedInto := ""
	if snap.MergedIntoID != nil {
		mergedInto = *snap.MergedIntoID
	}
	return Snapshot{
		ID:           snap.ID,
		CharacterID:  snap.CharacterID,
		Name:         snap.Name,
		MergedIntoID: mergedInto,
	}
}
