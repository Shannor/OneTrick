package stats

import (
	"context"
	"errors"
	"fmt"
	"oneTrick/api"

	"cloud.google.com/go/firestore"
	clog "github.com/Shannor/canonical-logger"
	"google.golang.org/api/iterator"
)

type characterMatchStat struct {
	snapshotID string
	kills      int
	deaths     int
	assists    int
	won        bool
}

type statsStore interface {
	listCharacterMatchStats(ctx context.Context, characterID string, gameMode *api.GameMode) ([]characterMatchStat, error)
}

type firestoreStatsStore struct {
	db *firestore.Client
}

var _ statsStore = (*firestoreStatsStore)(nil)

func newFirestoreStatsStore(db *firestore.Client) *firestoreStatsStore {
	if db == nil {
		return nil
	}
	return &firestoreStatsStore{db: db}
}

type projectedStatsAggregateDoc struct {
	SnapshotLinks map[string]projectedSnapshotLinkDoc `firestore:"snapshotLinks"`
	Performance   map[string]projectedPerformanceDoc  `firestore:"performance"`
}

type projectedSnapshotLinkDoc struct {
	SnapshotID string `firestore:"snapshotId"`
}

type projectedPerformanceDoc struct {
	PlayerStats projectedPlayerStatsDoc `firestore:"playerStats"`
}

type projectedPlayerStatsDoc struct {
	Kills    projectedStatValueDoc         `firestore:"kills"`
	Deaths   projectedStatValueDoc         `firestore:"deaths"`
	Assists  projectedStatValueDoc         `firestore:"assists"`
	Standing projectedOptionalStatValueDoc `firestore:"standing"`
}

type projectedStatValueDoc struct {
	Value float64 `firestore:"value"`
}

type projectedOptionalStatValueDoc struct {
	Value *float64 `firestore:"value"`
}

func (f *firestoreStatsStore) buildCharacterMatchStatsQuery(characterID string, gameMode *api.GameMode) firestore.Query {
	q := f.db.Collection(aggregatesCollection).
		Where("characterIds", "array-contains", characterID)

	if hasGameModeFilter(gameMode) {
		q = q.Where("activityHistory.estimatedGameMode", "==", string(*gameMode))
	}

	return q.SelectPaths(
		firestore.FieldPath{"snapshotLinks", characterID, "snapshotId"},
		firestore.FieldPath{"performance", characterID, "playerStats"},
	)
}

func (f *firestoreStatsStore) listCharacterMatchStats(ctx context.Context, characterID string, gameMode *api.GameMode) ([]characterMatchStat, error) {
	if characterID == "" {
		return nil, fmt.Errorf("characterID is required")
	}
	if f == nil || f.db == nil {
		return nil, fmt.Errorf("stats store is not configured")
	}

	clog.WithGroupField(ctx, "statsStore.listCharacterMatchStats", "characterId", characterID)
	if hasGameModeFilter(gameMode) {
		clog.WithGroupField(ctx, "statsStore.listCharacterMatchStats", "gameMode", string(*gameMode))
	}

	iter := f.buildCharacterMatchStatsQuery(characterID, gameMode).Documents(ctx)
	defer iter.Stop()

	results := make([]characterMatchStat, 0, 64)
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
		case decodeStatusOK:
			results = append(results, stat)
		case decodeStatusMissingPerformance:
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

type decodeStatus uint8

const (
	decodeStatusSkip decodeStatus = iota
	decodeStatusMissingPerformance
	decodeStatusOK
)

func decodeProjectedCharacterMatchStat(doc *firestore.DocumentSnapshot, characterID string) (characterMatchStat, decodeStatus, error) {
	var p projectedStatsAggregateDoc
	if err := doc.DataTo(&p); err != nil {
		return characterMatchStat{}, decodeStatusSkip, fmt.Errorf("decode projected aggregate %s: %w", doc.Ref.ID, err)
	}

	link, hasLink := p.SnapshotLinks[characterID]
	if !hasLink || link.SnapshotID == "" {
		return characterMatchStat{}, decodeStatusSkip, nil
	}

	perf, hasPerf := p.Performance[characterID]
	if !hasPerf {
		return characterMatchStat{}, decodeStatusMissingPerformance, nil
	}

	ps := perf.PlayerStats
	// Standing == 0 denotes a win in Destiny 2; missing/nil standing must not count as a win.
	won := ps.Standing.Value != nil && *ps.Standing.Value == 0

	return characterMatchStat{
		snapshotID: link.SnapshotID,
		kills:      int(ps.Kills.Value),
		deaths:     int(ps.Deaths.Value),
		assists:    int(ps.Assists.Value),
		won:        won,
	}, decodeStatusOK, nil
}
