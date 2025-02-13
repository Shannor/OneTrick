package aggregate

import (
	"cloud.google.com/go/firestore"
	"context"
	"errors"
	"google.golang.org/api/iterator"
	"oneTrick/api"
	"time"
)

var NotFound = errors.New("not found")

// Service defines the interface for working with character snapshots and aggregates.
type Service interface {
	// GetAggregate retrieves an existing aggregate for a given activity ID.
	// Takes a context and activity ID as input.
	// Returns the Aggregate or an error if no aggregate is found for the given activity ID.
	GetAggregate(ctx context.Context, activityID string) (*api.Aggregate, error)

	// AddAggregate creates or updates an aggregate for the specified parameters.
	// If the aggregate already exists, it performs a partial update with the new data.
	// Takes context, user ID, snapshot ID, activity ID, character ID, confidence level, and confidence source as input.
	// Returns the updated or newly created Aggregate or an error if the operation fails.
	AddAggregate(ctx context.Context, characterID string, activityID string, snapshotID *string, level api.ConfidenceLevel, source api.ConfidenceSource) (*api.Aggregate, error)

	// GetAggregates retrieves a list of aggregates for the given activity IDs.
	// Takes a context and a slice of activity IDs as input.
	// Returns a slice of Aggregates or an error if the operation fails.
	GetAggregates(ctx context.Context, activityIDs []string) ([]api.Aggregate, error)
}

const (
	collection = "aggregates"
)

type service struct {
	DB *firestore.Client
}

var _ Service = (*service)(nil)

func NewService(db *firestore.Client) Service {
	return &service{
		DB: db,
	}
}

func (s *service) AddAggregate(ctx context.Context, characterID string, activityID string, snapshotID *string, level api.ConfidenceLevel, source api.ConfidenceSource) (*api.Aggregate, error) {
	now := time.Now()
	mapping := api.CharacterMapping{
		CharacterID:      characterID,
		ConfidenceLevel:  level,
		ConfidenceSource: source,
		CreatedAt:        now,
	}
	if level != api.NotFoundConfidenceLevel && snapshotID != nil {
		// TODO: Add logic here to get whatever a "Snapshot Snippet" is
		mapping.SnapshotData = &api.SnapshotData{
			SnapshotID: *snapshotID,
			Snippet:    api.SnapshotSnippet{PrimaryWeapon: "Test Weapon"},
		}
	}
	aggregate := api.Aggregate{
		ActivityID: activityID,
		Mapping: map[string]api.CharacterMapping{
			characterID: mapping,
		},
		CreatedAt: now,
	}

	iter := s.DB.Collection(collection).Where("activityId", "==", activityID).Documents(ctx)
	var (
		existingAggregate *api.Aggregate
	)
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		err = doc.DataTo(&existingAggregate)
		if err != nil {
			return nil, err
		}
	}
	if existingAggregate != nil {
		// Partial update, adding the new data
		_, err := s.DB.Collection(collection).Doc(existingAggregate.ID).Set(ctx, map[string]any{
			"mapping": map[string]any{
				characterID: mapping,
			},
		}, firestore.MergeAll)
		if err != nil {
			return nil, err
		}
		existingAggregate.Mapping[characterID] = mapping
		return existingAggregate, nil
	} else {
		// Create new Doc and return object
		ref := s.DB.Collection(collection).NewDoc()
		aggregate.ID = ref.ID
		_, err := ref.Set(ctx, aggregate)
		if err != nil {
			return nil, err
		}

		return &aggregate, nil
	}
}

func (s *service) GetAggregate(ctx context.Context, activityID string) (*api.Aggregate, error) {
	iter := s.DB.
		Collection(collection).
		Where("activityId", "==", activityID).
		Limit(1).
		Documents(ctx)
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		agg := api.Aggregate{}
		err = doc.DataTo(&agg)
		if err != nil {
			return nil, err
		}
		return &agg, nil
	}
	return nil, NotFound
}

func (s *service) GetAggregates(ctx context.Context, activityIDs []string) ([]api.Aggregate, error) {
	results := make([]api.Aggregate, 0)
	iter := s.DB.
		Collection(collection).
		Where("activityId", "in", activityIDs).
		Documents(ctx)
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		agg := api.Aggregate{}
		err = doc.DataTo(&agg)
		if err != nil {
			return nil, err
		}
		results = append(results, agg)
	}
	return results, nil
}
