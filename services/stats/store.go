package stats

import (
	"context"
	"errors"
	"fmt"

	"cloud.google.com/go/firestore"
	clog "github.com/Shannor/canonical-logger"
	"google.golang.org/api/iterator"
)

type CharacterMatchStat struct {
	SnapshotID string
	Kills      int
	Deaths     int
	Assists    int
	Won        bool
}

type Store interface {
	ListCharacterMatchStats(ctx context.Context, characterID string, gameMode string) ([]CharacterMatchStat, error)
}

type FirestoreStore struct {
	db *firestore.Client
}

var _ Store = (*FirestoreStore)(nil)

func NewFirestoreStore(db *firestore.Client) *FirestoreStore {
	return &FirestoreStore{db: db}
}

type projectedStatsAggregateDoc struct {
	SnapshotLinks map[string]projectedSnapshotLinkDoc `firestore:"snapshotLinks"`
	Performance   map[string]projectedPerformanceDoc  `firestore:"performance"`
}

type projectedSnapshotLinkDoc struct {
	SnapshotID string `firestore:"snapshotId"`
}

type projectedPerformanceDoc struct {
	PlayerStats *projectedPlayerStatsDoc `firestore:"playerStats"`
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

func (f *FirestoreStore) buildCharacterMatchStatsQuery(characterID string, gameMode string) firestore.Query {
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

func (f *FirestoreStore) ListCharacterMatchStats(ctx context.Context, characterID string, gameMode string) ([]CharacterMatchStat, error) {
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

	results := make([]CharacterMatchStat, 0, 64)
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

func decodeProjectedCharacterMatchStat(doc *firestore.DocumentSnapshot, characterID string) (CharacterMatchStat, decodeStatus, error) {
	var p projectedStatsAggregateDoc
	if err := doc.DataTo(&p); err != nil {
		return CharacterMatchStat{}, decodeStatusSkip, fmt.Errorf("decode projected aggregate %s: %w", doc.Ref.ID, err)
	}

	link, hasLink := p.SnapshotLinks[characterID]
	if !hasLink || link.SnapshotID == "" {
		return CharacterMatchStat{}, decodeStatusSkip, nil
	}

	perf, hasPerf := p.Performance[characterID]
	if !hasPerf || perf.PlayerStats == nil {
		return CharacterMatchStat{}, decodeStatusMissingPerformance, nil
	}

	ps := perf.PlayerStats
	// Standing == 0 denotes a win in Destiny 2; missing/nil standing must not count as a win.
	won := ps.Standing.Value != nil && *ps.Standing.Value == 0

	return CharacterMatchStat{
		SnapshotID: link.SnapshotID,
		Kills:      int(ps.Kills.Value),
		Deaths:     int(ps.Deaths.Value),
		Assists:    int(ps.Assists.Value),
		Won:        won,
	}, decodeStatusOK, nil
}
