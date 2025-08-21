package stats

import (
	"context"
	"fmt"
	"oneTrick/api"
	"oneTrick/services/snapshot"
	"oneTrick/utils"
	"sort"

	"cloud.google.com/go/firestore"
)

// Service defines operations for retrieving stats-related data.
// Note: "Loadout" in product language corresponds to a Snapshot in code.
// This service focuses on aggregating data to support stats views for a user's loadouts.
type Service interface {
	// GetAggregatesForSnapshot returns all aggregates where the provided characterID
	// is present in snapshotLinks and the linked snapshotId matches the provided snapshotID.
	// This is the foundational data needed to compute overall stats for a loadout.
	GetAggregatesForSnapshot(ctx context.Context, characterID string, snapshotID string) ([]api.Aggregate, error)

	GetTopLoadouts(ctx context.Context, characterID string, userID string) ([]api.CharacterSnapshot, map[string]int, error)
}

type service struct {
	DB              *firestore.Client
	snapshotService snapshot.Service
}

// NewService creates a new Stats service instance.
func NewService(db *firestore.Client, snapshotService snapshot.Service) Service {
	return &service{DB: db, snapshotService: snapshotService}
}

const (
	aggregatesCollection = "aggregates"
	snapshotsCollection  = "snapshots"
)

// GetAggregatesForSnapshot finds all aggregates that link the given character to the given snapshot.
// Implementation details:
// - We leverage Firestore map-field querying on: snapshotLinks.<characterID>.snapshotId == snapshotID
// - This yields all activity aggregates where this character was linked to the specified snapshot (loadout).
func (s *service) GetAggregatesForSnapshot(ctx context.Context, characterID string, snapshotID string) ([]api.Aggregate, error) {
	if characterID == "" || snapshotID == "" {
		return nil, fmt.Errorf("characterID and snapshotID are required")
	}
	field := fmt.Sprintf("snapshotLinks.%s.snapshotId", characterID)
	docs, err := s.DB.Collection(aggregatesCollection).
		Where(field, "==", snapshotID).
		Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[api.Aggregate](docs)
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (s *service) GetTopLoadouts(ctx context.Context, characterID string, userID string) ([]api.CharacterSnapshot, map[string]int, error) {
	if characterID == "" || userID == "" {
		return nil, nil, fmt.Errorf("characterID and userID are required")
	}
	// 1) Get all snapshot IDs for this user and character
	snapshotDocs, err := s.DB.Collection(snapshotsCollection).
		Where("userId", "==", userID).
		Where("characterId", "==", characterID).
		Documents(ctx).GetAll()
	if err != nil {
		return nil, nil, err
	}
	if len(snapshotDocs) == 0 {
		return nil, nil, nil
	}
	type MinimalSnapshot struct {
		ID string `firestore:"id"`
	}
	snapshotIDs := make([]string, 0, len(snapshotDocs))
	for _, d := range snapshotDocs {
		var ms MinimalSnapshot
		if err := d.DataTo(&ms); err != nil {
			return nil, nil, err
		}
		if ms.ID != "" {
			snapshotIDs = append(snapshotIDs, ms.ID)
		}
	}
	if len(snapshotIDs) == 0 {
		return nil, nil, nil
	}

	// 2) Query aggregates in batches of 30 using IN filter and count by snapshotId
	const inLimit = 30
	counts := map[string]int{}
	field := fmt.Sprintf("snapshotLinks.%s.snapshotId", characterID)
	for i := 0; i < len(snapshotIDs); i += inLimit {
		end := i + inLimit
		if end > len(snapshotIDs) {
			end = len(snapshotIDs)
		}
		batch := snapshotIDs[i:end]
		aggDocs, err := s.DB.Collection(aggregatesCollection).
			Where(field, "in", batch).
			Documents(ctx).GetAll()
		if err != nil {
			return nil, nil, err
		}
		aggs, err := utils.GetAllToStructs[api.Aggregate](aggDocs)
		if err != nil {
			return nil, nil, err
		}
		for _, agg := range aggs {
			link, ok := agg.SnapshotLinks[characterID]
			if !ok || link.SnapshotID == nil || *link.SnapshotID == "" {
				continue
			}
			counts[*link.SnapshotID]++
		}
	}

	// 3) Sort snapshot IDs by count desc and return top 10
	type pair struct {
		id    string
		count int
	}
	pairs := make([]pair, 0, len(counts))
	for id, c := range counts {
		pairs = append(pairs, pair{id: id, count: c})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count == pairs[j].count {
			return pairs[i].id < pairs[j].id
		}
		return pairs[i].count > pairs[j].count
	})

	limit := 10
	if len(pairs) < limit {
		limit = len(pairs)
	}

	ids := make([]string, 0, limit)
	finalCount := make(map[string]int)
	for idx := 0; idx < limit; idx++ {
		ids = append(ids, pairs[idx].id)
		finalCount[pairs[idx].id] = pairs[idx].count
	}

	loadouts, err := s.snapshotService.GetByIDs(ctx, ids)
	if err != nil {
		return nil, nil, err
	}
	return loadouts, finalCount, nil
}
