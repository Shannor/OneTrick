package stats

import (
	"context"
	"fmt"
	"oneTrick/api"
	"oneTrick/utils"

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
}

type service struct {
	DB *firestore.Client
}

// NewService creates a new Stats service instance.
func NewService(db *firestore.Client) Service {
	return &service{DB: db}
}

const (
	aggregatesCollection = "aggregates"
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
