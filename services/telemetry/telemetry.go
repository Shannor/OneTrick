package telemetry

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"oneTrick/services/destiny"
	"oneTrick/set"
	"slices"
	"time"

	clog "github.com/Shannor/canonical-logger"
)

type UserReader interface {
	GetUser(ctx context.Context, lookupID string) (User, error)
}

type SnapshotReader interface {
	GetSnapshot(ctx context.Context, snapshotID string) (Snapshot, error)
	GetSnapshotsByIDs(ctx context.Context, snapshotIDs []string) ([]Snapshot, error)
}

type MatchStore interface {
	ListRecentUnfiltered(ctx context.Context, characterID string, from, to time.Time, limit int) (matches []RawMatch, totalCount int, truncated bool, err error)
	StreamMatches(ctx context.Context, characterID, snapshotID string, from, to time.Time, fn func(RawMatch) error) error
}

type Service interface {
	GetCharacterTelemetry(ctx context.Context, query Query) (Result, error)
}

type service struct {
	users     UserReader
	snapshots SnapshotReader
	matches   MatchStore
}

func NewService(users UserReader, snapshots SnapshotReader, matches MatchStore) Service {
	return &service{
		users:     users,
		snapshots: snapshots,
		matches:   matches,
	}
}

func (s *service) GetCharacterTelemetry(ctx context.Context, query Query) (Result, error) {
	q, err := query.NormalizeAndValidate()
	if err != nil {
		return Result{}, err
	}

	logFields := clog.Fields{
		"userId":      q.UserID,
		"characterId": q.CharacterID,
		"gameMode":    string(q.GameMode),
		"limit":       q.Limit,
	}
	if q.SnapshotID != "" {
		logFields["snapshotId"] = q.SnapshotID
	}
	if !q.From.IsZero() {
		logFields["from"] = q.From.Format(time.RFC3339)
	}
	if !q.To.IsZero() {
		logFields["to"] = q.To.Format(time.RFC3339)
	}
	clog.WithGroupFields(ctx, "telemetryService.getCharacterTelemetry", logFields)

	u, err := s.users.GetUser(ctx, q.UserID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Result{}, fmt.Errorf("user %s: %w", q.UserID, ErrNotFound)
		}
		return Result{}, fmt.Errorf("fetch user %s: %w", q.UserID, err)
	}
	if !u.OwnsCharacter(q.CharacterID) {
		return Result{}, fmt.Errorf("character %s not found for user %s: %w", q.CharacterID, u.ID, ErrNotFound)
	}

	effectiveSnapshotID := q.SnapshotID
	preloadedSnapshots := make(map[string]Snapshot)
	if q.SnapshotID != "" {
		snap, err := s.snapshots.GetSnapshot(ctx, q.SnapshotID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return Result{}, fmt.Errorf("snapshot %s: %w", q.SnapshotID, ErrNotFound)
			}
			return Result{}, fmt.Errorf("fetch snapshot %s: %w", q.SnapshotID, err)
		}
		if !snap.BelongsTo(u.ID, q.CharacterID) {
			return Result{}, fmt.Errorf("snapshot %s does not belong to character %s: %w", q.SnapshotID, q.CharacterID, ErrNotFound)
		}
		preloadedSnapshots[snap.ID] = snap
		if snap.IsArchived() {
			target, err := s.snapshots.GetSnapshot(ctx, snap.MergedIntoID)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					return Result{}, fmt.Errorf("merged target snapshot %s: %w", snap.MergedIntoID, ErrNotFound)
				}
				return Result{}, fmt.Errorf("fetch merged target snapshot %s: %w", snap.MergedIntoID, err)
			}
			if !target.IsActiveFor(u.ID, q.CharacterID) {
				return Result{}, fmt.Errorf("merged target snapshot %s unavailable: %w", snap.MergedIntoID, ErrNotFound)
			}
			effectiveSnapshotID = target.ID
			preloadedSnapshots[target.ID] = target
		}
	}

	var (
		selected     []Match
		totalCount   int
		truncated    bool
		usedFastPath bool
	)

	if q.GameMode == GameModeAll && effectiveSnapshotID == "" {
		usedFastPath = true
		rawMatches, total, isTruncated, err := s.matches.ListRecentUnfiltered(ctx, q.CharacterID, q.From, q.To, q.Limit)
		if err != nil {
			return Result{}, fmt.Errorf("list character telemetry: %w", err)
		}
		selected = make([]Match, 0, len(rawMatches))
		for _, raw := range rawMatches {
			selected = append(selected, toDomainMatch(raw, resolveEstimatedGameMode(raw)))
		}
		totalCount = total
		truncated = isTruncated
	} else {
		selected = make([]Match, 0, min(q.Limit, 64))
		err := s.matches.StreamMatches(ctx, q.CharacterID, effectiveSnapshotID, q.From, q.To, func(raw RawMatch) error {
			estMode := resolveEstimatedGameMode(raw)
			if q.GameMode != GameModeAll && estMode != q.GameMode {
				return nil
			}
			if effectiveSnapshotID != "" {
				if !raw.SnapshotConfidence.IsValidSnapshotLink() || raw.RawSnapshotID != effectiveSnapshotID {
					return nil
				}
			}
			totalCount++
			if len(selected) < q.Limit {
				selected = append(selected, toDomainMatch(raw, estMode))
			}
			return nil
		})
		if err != nil {
			return Result{}, fmt.Errorf("stream character telemetry: %w", err)
		}
		truncated = totalCount > q.Limit
	}

	slices.SortFunc(selected, func(a, b Match) int {
		if c := a.Period.Compare(b.Period); c != 0 {
			return c
		}
		return cmp.Compare(a.ActivityID, b.ActivityID)
	})

	snapshotsMap, err := s.resolveSnapshots(ctx, selected, preloadedSnapshots)
	if err != nil {
		return Result{}, err
	}

	clog.WithGroupFields(ctx, "telemetryService.getCharacterTelemetry", clog.Fields{
		"resolvedUserId": u.ID,
		"matchCount":     len(selected),
		"totalCount":     totalCount,
		"truncated":      truncated,
		"snapshotCount":  len(snapshotsMap),
		"usedFastPath":   usedFastPath,
	})

	return Result{
		Matches:    selected,
		Snapshots:  snapshotsMap,
		TotalCount: totalCount,
		Truncated:  truncated,
	}, nil
}

func (s *service) resolveSnapshots(ctx context.Context, matches []Match, preloaded map[string]Snapshot) (map[string]SnapshotSummary, error) {
	if len(matches) == 0 {
		return make(map[string]SnapshotSummary), nil
	}

	fetched := make(map[string]Snapshot, len(preloaded))
	for id, snap := range preloaded {
		fetched[id] = snap
	}

	neededSet := set.NewOrdered[string]()
	var missingIDs []string
	for _, m := range matches {
		if m.SnapshotID == "" || !neededSet.Add(m.SnapshotID) {
			continue
		}
		if _, alreadyLoaded := fetched[m.SnapshotID]; !alreadyLoaded {
			missingIDs = append(missingIDs, m.SnapshotID)
		}
	}

	if len(missingIDs) > 0 {
		snaps, err := s.snapshots.GetSnapshotsByIDs(ctx, missingIDs)
		if err != nil {
			return nil, fmt.Errorf("fetch referenced snapshots: %w", err)
		}
		for _, snap := range snaps {
			fetched[snap.ID] = snap
		}
	}

	// Follow 1-hop soft-archive merges so matches normalize to their active target snapshot.
	neededIDs := neededSet.ToSlice()
	missingTargets := set.NewOrdered[string]()
	for _, id := range neededIDs {
		snap, ok := fetched[id]
		if !ok || !snap.IsArchived() {
			continue
		}
		if _, exists := fetched[snap.MergedIntoID]; exists {
			continue
		}
		missingTargets.Add(snap.MergedIntoID)
	}

	if missingTargets.Size() > 0 {
		targets, err := s.snapshots.GetSnapshotsByIDs(ctx, missingTargets.ToSlice())
		if err != nil {
			return nil, fmt.Errorf("fetch merged target snapshots: %w", err)
		}
		for _, snap := range targets {
			fetched[snap.ID] = snap
		}
	}

	resolvedByOrigID := make(map[string]SnapshotSummary, len(neededIDs))
	for _, id := range neededIDs {
		snap, ok := fetched[id]
		if !ok {
			continue
		}
		if snap.IsArchived() {
			target, ok := fetched[snap.MergedIntoID]
			if !ok || target.IsArchived() {
				continue
			}
			resolvedByOrigID[id] = SnapshotSummary{ID: target.ID, Name: target.Name}
			continue
		}
		resolvedByOrigID[id] = SnapshotSummary{ID: snap.ID, Name: snap.Name}
	}

	snapshotsMap := make(map[string]SnapshotSummary, len(resolvedByOrigID))
	for i := range matches {
		if matches[i].SnapshotID == "" {
			continue
		}
		summary, ok := resolvedByOrigID[matches[i].SnapshotID]
		if !ok {
			matches[i].SnapshotID = ""
			continue
		}
		matches[i].SnapshotID = summary.ID
		snapshotsMap[summary.ID] = summary
	}

	return snapshotsMap, nil
}

func resolveEstimatedGameMode(raw RawMatch) GameMode {
	if raw.StoredEstimatedGameMode != "" && raw.StoredEstimatedGameMode != GameModeAll && raw.StoredEstimatedGameMode.IsValid() {
		return raw.StoredEstimatedGameMode
	}
	return GameMode(destiny.EstimateGameModeName(raw.Mode, raw.Activity))
}

func toDomainMatch(raw RawMatch, estMode GameMode) Match {
	snapshotID := ""
	if raw.SnapshotConfidence.IsValidSnapshotLink() && raw.RawSnapshotID != "" {
		snapshotID = raw.RawSnapshotID
	}
	return Match{
		ActivityID:           raw.ActivityID,
		Period:               raw.Period,
		Location:             raw.Location,
		Activity:             raw.Activity,
		EstimatedGameMode:    estMode,
		Kills:                raw.Kills,
		Deaths:               raw.Deaths,
		Assists:              raw.Assists,
		Won:                  raw.Won,
		TimePlayedSeconds:    raw.TimePlayedSeconds,
		HasTimePlayedSeconds: raw.HasTimePlayedSeconds,
		SessionID:            raw.SessionID,
		SnapshotID:           snapshotID,
		SnapshotConfidence:   raw.SnapshotConfidence,
	}
}
