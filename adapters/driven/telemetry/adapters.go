// Package telemetry provides DRIVEN adapters (called by core/telemetry via its outbound ports).
//
// WARNING — TEMPORARY ANTI-CORRUPTION BRIDGE (INTENTIONAL TRANSITIONAL ANTI-PATTERN):
//
// In standard Hexagonal Architecture, driven adapters are organized by infrastructure
// (adapters/driven/firestore, adapters/driven/bungie, adapters/driven/cache) and talk
// directly to external systems rather than wrapping sibling application services.
//
// This package intentionally wraps legacy services/user and services/snapshot ONLY so
// core/telemetry can stay strictly isolated from api.*, firestore, and legacy services/*
// while the rest of the codebase migrates incrementally.
//
// Migration & Removal Checklist (delete each adapter as its backing domain migrates):
//   1. NewUserReader: remove once services/user moves into core/user + adapters/driven/firestore.
//   2. NewSnapshotReader: remove once services/snapshot moves into core/snapshot + adapters/driven/firestore.
//   3. Delete this entire package once both backing domains have migrated.
package telemetry

import (
	"context"
	"errors"
	"oneTrick/api"
	coretelemetry "oneTrick/core/telemetry"
	"oneTrick/services/snapshot"
	"oneTrick/services/user"
	"oneTrick/set"
)

type userReaderAdapter struct {
	svc user.Service
}

func NewUserReader(svc user.Service) coretelemetry.UserReader {
	return userReaderAdapter{svc: svc}
}

func (a userReaderAdapter) GetUser(ctx context.Context, lookupID string) (coretelemetry.User, error) {
	u, err := a.svc.GetUser(ctx, lookupID)
	if err != nil {
		if errors.Is(err, user.NotFound) {
			return coretelemetry.User{}, coretelemetry.ErrNotFound
		}
		return coretelemetry.User{}, err
	}
	if u == nil {
		return coretelemetry.User{}, coretelemetry.ErrNotFound
	}

	charIDs := set.NewOrdered[string]()
	for _, id := range u.CharacterIDs {
		if id != "" {
			charIDs.Add(id)
		}
	}
	for _, c := range u.Characters {
		if c.Id != "" {
			charIDs.Add(c.Id)
		}
	}

	return coretelemetry.User{
		ID:           u.ID,
		CharacterIDs: charIDs.ToSlice(),
	}, nil
}

type snapshotReaderAdapter struct {
	svc snapshot.Service
}

func NewSnapshotReader(svc snapshot.Service) coretelemetry.SnapshotReader {
	return snapshotReaderAdapter{svc: svc}
}

func (a snapshotReaderAdapter) GetSnapshot(ctx context.Context, snapshotID string) (coretelemetry.Snapshot, error) {
	snap, err := a.svc.Get(ctx, snapshotID)
	if err != nil {
		if errors.Is(err, snapshot.NotFound) {
			return coretelemetry.Snapshot{}, coretelemetry.ErrNotFound
		}
		return coretelemetry.Snapshot{}, err
	}
	if snap == nil {
		return coretelemetry.Snapshot{}, coretelemetry.ErrNotFound
	}
	return toCoreSnapshot(*snap), nil
}

func (a snapshotReaderAdapter) GetSnapshotsByIDs(ctx context.Context, snapshotIDs []string) ([]coretelemetry.Snapshot, error) {
	if len(snapshotIDs) == 0 {
		return nil, nil
	}
	snaps, err := a.svc.GetByIDs(ctx, snapshotIDs)
	if err != nil {
		return nil, err
	}
	out := make([]coretelemetry.Snapshot, 0, len(snaps))
	for _, snap := range snaps {
		out = append(out, toCoreSnapshot(snap))
	}
	return out, nil
}

func toCoreSnapshot(snap api.CharacterSnapshot) coretelemetry.Snapshot {
	mergedInto := ""
	if snap.MergedIntoID != nil {
		mergedInto = *snap.MergedIntoID
	}
	return coretelemetry.Snapshot{
		ID:           snap.ID,
		UserID:       snap.UserID,
		CharacterID:  snap.CharacterID,
		Name:         snap.Name,
		MergedIntoID: mergedInto,
	}
}
