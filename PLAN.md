# Implementation Plan: Session & Loadout/Snapshot Deletion

## Overview

Implement support for deleting **Sessions** and **Loadouts/Snapshots** across the OpenAPI spec, Service layer, and Firestore database. Deleting sessions and snapshots requires cascading reference cleanups in linked `aggregates` and subcollections (`snapshots/{snapshotId}/histories`), as well as auto-deleting orphaned aggregates that are no longer referenced by any session, snapshot, or user.

---

## Technical Cleanup Rules & Logic

### 1. Session Deletion (`DeleteSession`)

- **Verification**: Check if `sessions/{sessionId}` exists (404) and belongs to `userID` (401).
- **Aggregates Cleanup**:
  - Query `aggregates` where `sessionIds` array contains `sessionId`.
  - For each aggregate:
    - Remove `sessionId` from `sessionIds` array.
    - If `snapshotLinks[characterId].sessionId == sessionId`, clear/remove `sessionId` from the link.
    - **Orphan Check**: If `sessionIds` is empty AND `snapshotIds` is empty AND no active `snapshotId`/`sessionId` remain in `snapshotLinks`, **delete the aggregate document**. Otherwise, update the aggregate.
- **Session Removal**: Delete the document `sessions/{sessionId}`.

### 2. Loadout / Snapshot Deletion (`DeleteSnapshot`)

- **Verification**: Check if `snapshots/{snapshotId}` exists (404) and belongs to `userID` (401).
- **Subcollection History Cleanup**:
  - Query and delete all documents in `snapshots/{snapshotId}/histories`.
- **Aggregates Cleanup**:
  - Query `aggregates` where `snapshotIds` array contains `snapshotId`.
  - For each aggregate:
    - Remove `snapshotId` from `snapshotIds` array.
    - In `snapshotLinks[characterId]`:
      - If `snapshotId == target`, set `snapshotId = nil`.
      - If `originalSnapshotId == target`, set `originalSnapshotId = nil`.
    - **Orphan Check**: If `sessionIds` is empty AND `snapshotIds` is empty AND no active `snapshotId`/`sessionId` remain in `snapshotLinks`, **delete the aggregate document**. Otherwise, update the aggregate.
- **Snapshot Removal**: Delete the document `snapshots/{snapshotId}`.

---

## Tasks & Progress Tracking

- [x] **Step 1: OpenAPI Schema & Code Generation**
  - [x] Add `DELETE /sessions/{sessionId}` to `openapi/paths/sessions_{sessionId}.yaml`.
  - [x] Add `DELETE /snapshots/{snapshotId}` to `openapi/paths/snapshots_{snapshotId}.yaml`.
  - [x] Run `make generate` to update `openapi.yaml` and `api/api.gen.go`.
  - [x] Add temporary handler stubs in `impl.go` for build pass.

- [x] **Step 2: Aggregate Service Extensions (`services/aggregate/aggregate.go`)**
  - [x] Add `RemoveSession(ctx context.Context, sessionID string) error` to `aggregate.Service`.
  - [x] Add `RemoveSnapshot(ctx context.Context, snapshotID string) error` to `aggregate.Service`.
  - [x] Implement orphan detection and conditional document deletion in `services/aggregate`.

- [x] **Step 3: Session Service Implementation (`services/session/session.go`)**
  - [x] Add `Delete(ctx context.Context, sessionID string, userID string) error` to `session.Service`.
  - [x] Integrate aggregate reference cleanup before session document deletion.

- [x] **Step 4: Snapshot Service Implementation (`services/snapshot/snapshot.go`)**
  - [x] Add `Delete(ctx context.Context, snapshotID string, userID string) error` to `snapshot.Service`.
  - [x] Implement subcollection `histories` deletion for the target snapshot.
  - [x] Integrate aggregate reference cleanup before snapshot document deletion.

- [x] **Step 5: API Handlers Integration (`impl.go`)**
  - [x] Connect `DeleteSession` handler to `SessionService.Delete`.
  - [x] Connect `DeleteSnapshot` handler to `SnapshotService.Delete`.
  - [x] Map error cases to standard OpenAPI HTTP responses (204, 401, 404, 500).

- [x] **Step 6: Unit & Integration Testing**
  - [x] Write unit tests for `services/aggregate` (`isAggregateOrphaned`).
  - [x] Write unit tests for `services/snapshot` error mappings.
  - [x] Run `go test ./...` to verify all tests pass.
