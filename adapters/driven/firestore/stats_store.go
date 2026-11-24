package firestore

import (
	"context"
	"errors"
	"fmt"
	"oneTrick/services/stats"

	"cloud.google.com/go/firestore"
	clog "github.com/Shannor/canonical-logger"
	"google.golang.org/api/iterator"
)

type StatsStore struct {
	db *firestore.Client
}

var _ stats.Store = (*StatsStore)(nil)

func NewStatsStore(db *firestore.Client) *StatsStore {
	return &StatsStore{db: db}
}

type projectedStatsAggregateDoc struct {
	SnapshotLinks map[string]projectedStatsLinkDoc `firestore:"snapshotLinks"`
	Performance   map[string]projectedStatsPerfDoc `firestore:"performance"`
}

type projectedStatsLinkDoc struct {
	SnapshotID string `firestore:"snapshotId"`
}

type projectedStatsPerfDoc struct {
	PlayerStats *projectedStatsPlayerStatsDoc `firestore:"playerStats"`
}

type projectedStatsPlayerStatsDoc struct {
	Kills    projectedStatValueDoc         `firestore:"kills"`
	Deaths   projectedStatValueDoc         `firestore:"deaths"`
	Assists  projectedStatValueDoc         `firestore:"assists"`
	Standing projectedOptionalStatValueDoc `firestore:"standing"`
}

func (f *StatsStore) buildCharacterMatchStatsQuery(characterID string, gameMode string) firestore.Query {
	q := f.db.Collection(aggregatesCollection).
		Where("characterIds", "array-contains", characterID)

	if gameMode != "" {
		q = q.Where("activityHistory.estimatedGameMode", "==", gameMode)
	}

	return q.SelectPaths(
		firestore.FieldPath{"snapshotLinks", characterID, "snapshotId"},
		firestore.FieldPath{"performance", characterID, "playerStats"},
	)
}

func (f *StatsStore) ListCharacterMatchStats(ctx context.Context, characterID string, gameMode string) ([]stats.CharacterMatchStat, error) {
	if characterID == "" {
		return nil, fmt.Errorf("characterID is required")
	}
	if f == nil || f.db == nil {
		return nil, fmt.Errorf("stats store is not configured")
	}

	clog.WithGroupField(ctx, "statsStore.listCharacterMatchStats", "characterId", characterID)
	if gameMode != "" {
		clog.WithGroupField(ctx, "statsStore.listCharacterMatchStats", "gameMode", gameMode)
	}

	iter := f.buildCharacterMatchStatsQuery(characterID, gameMode).Documents(ctx)
	defer iter.Stop()

	results := make([]stats.CharacterMatchStat, 0, 64)
	scanned := 0
	missingPerf := 0

	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("iterate projected character match stats: %w", err)
		}
		scanned++

		stat, status, err := decodeProjectedCharacterMatchStat(doc, characterID)
		if err != nil {
			return nil, err
		}
		switch status {
		case statsDecodeStatusOK:
			results = append(results, stat)
		case statsDecodeStatusMissingPerformance:
			missingPerf++
		}
	}

	clog.WithGroupFields(ctx, "statsStore.listCharacterMatchStats", clog.Fields{
		"scannedCount":            scanned,
		"returnedCount":           len(results),
		"missingPerformanceCount": missingPerf,
	})
	return results, nil
}

type statsDecodeStatus uint8

const (
	statsDecodeStatusSkip statsDecodeStatus = iota
	statsDecodeStatusMissingPerformance
	statsDecodeStatusOK
)

func decodeProjectedCharacterMatchStat(doc *firestore.DocumentSnapshot, characterID string) (stats.CharacterMatchStat, statsDecodeStatus, error) {
	var p projectedStatsAggregateDoc
	if err := doc.DataTo(&p); err != nil {
		return stats.CharacterMatchStat{}, statsDecodeStatusSkip, fmt.Errorf("decode projected aggregate %s: %w", doc.Ref.ID, err)
	}

	link, hasLink := p.SnapshotLinks[characterID]
	if !hasLink || link.SnapshotID == "" {
		return stats.CharacterMatchStat{}, statsDecodeStatusSkip, nil
	}

	perf, hasPerf := p.Performance[characterID]
	if !hasPerf || perf.PlayerStats == nil {
		return stats.CharacterMatchStat{}, statsDecodeStatusMissingPerformance, nil
	}

	ps := perf.PlayerStats
	// Standing == 0 denotes a win in Destiny 2; missing/nil standing must not count as a win.
	won := ps.Standing.Value != nil && *ps.Standing.Value == 0

	return stats.CharacterMatchStat{
		SnapshotID: link.SnapshotID,
		Kills:      int(ps.Kills.Value),
		Deaths:     int(ps.Deaths.Value),
		Assists:    int(ps.Assists.Value),
		Won:        won,
	}, statsDecodeStatusOK, nil
}
