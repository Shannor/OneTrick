package snapshot

import (
	"cloud.google.com/go/firestore"
	"context"
	"errors"
	"fmt"
	"google.golang.org/api/iterator"
	"oneTrick/api"
	"time"
)

// Service defines the interface for working with character snapshots and aggregates.
type Service interface {

	// Create creates a new snapshot for a given user and character.
	// Takes a context, user ID, and CharacterSnapshot as input.
	// Returns the ID of the created snapshot or an error if the operation fails.
	Create(ctx context.Context, userID string, snapshot api.CharacterSnapshot) (*string, error)

	// GetAllByCharacter retrieves all snapshots for a given user and character.
	// Snapshots are returned in reverse chronological order based on their timestamp.
	// Takes a context, user ID, and character ID as input.
	// Returns a slice of snapshots or an error if the operation fails.
	GetAllByCharacter(ctx context.Context, userID string, characterID string) ([]api.CharacterSnapshot, error)

	// Get retrieves a specific snapshot for a given user, character, and snapshot ID.
	// Takes a context, user ID, character ID, and snapshot ID as input.
	// Returns the requested CharacterSnapshot or an error if the snapshot is not found or cannot be retrieved.
	Get(ctx context.Context, userID string, characterID string, snapshotID string) (*api.CharacterSnapshot, error)

	// FindClosest retrieves the closest snapshot to a given activity period timestamp for a specified user and character.
	// Takes a context, user ID, character ID, and activity period (timestamp) as input.
	// Returns the closest CharacterSnapshot or an error if no snapshot is found.
	FindClosest(ctx context.Context, userID string, characterID string, activityPeriod time.Time) (*api.CharacterSnapshot, error)

	// GetAggregate retrieves an existing aggregate for a given activity ID.
	// Takes a context and activity ID as input.
	// Returns the Aggregate or an error if no aggregate is found for the given activity ID.
	GetAggregate(ctx context.Context, activityID string) (*api.Aggregate, error)

	// AddAggregate creates or updates an aggregate for the specified parameters.
	// If the aggregate already exists, it performs a partial update with the new data.
	// Takes context, user ID, snapshot ID, activity ID, character ID, confidence level, and confidence source as input.
	// Returns the updated or newly created Aggregate or an error if the operation fails.
	AddAggregate(ctx context.Context, userID string, snapshotID string, activityID string, characterID string, level api.ConfidenceLevel, source api.ConfidenceSource) (*api.Aggregate, error)
}

const (
	snapShotCollection  = "snapshots"
	aggregateCollection = "aggregates"
)

type service struct {
	DB *firestore.Client
}

func (s *service) FindClosest(ctx context.Context, userID string, characterID string, activityPeriod time.Time) (*api.CharacterSnapshot, error) {
	iter := s.DB.Collection(snapShotCollection).Doc(userID).Collection(characterID).Documents(ctx)
	var snapshot *api.CharacterSnapshot
	minDuration := time.Duration(1<<63 - 1) // Max duration value

	defer iter.Stop()
	for {
		s := api.CharacterSnapshot{}
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		err = doc.DataTo(&s)
		if err != nil {
			return nil, err
		}

		duration := s.Timestamp.Sub(activityPeriod)
		if duration < 0 {
			duration = -duration
		}

		if duration < minDuration {
			minDuration = duration
			snapshot = &s
		}
	}
	if snapshot == nil {
		return nil, NotFound
	}
	return snapshot, nil
}

var _ Service = (*service)(nil)

func NewService(db *firestore.Client) Service {
	return &service{
		DB: db,
	}
}

func (s *service) Create(ctx context.Context, userID string, snapshot api.CharacterSnapshot) (*string, error) {
	ref := s.DB.Collection(snapShotCollection).Doc(userID).Collection(snapshot.CharacterID).NewDoc()
	id := ref.ID
	snapshot.ID = ref.ID
	_, err := ref.Set(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func (s *service) GetAllByCharacter(ctx context.Context, userID string, characterID string) ([]api.CharacterSnapshot, error) {
	iter := s.DB.Collection(snapShotCollection).Doc(userID).Collection(characterID).OrderBy("timestamp", firestore.Desc).Documents(ctx)
	snapshots := make([]api.CharacterSnapshot, 0)
	defer iter.Stop()
	for {
		s := api.CharacterSnapshot{}
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		err = doc.DataTo(&s)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, s)
	}
	return snapshots, nil
}

func (s *service) Get(ctx context.Context, userID string, characterID string, snapshotID string) (*api.CharacterSnapshot, error) {
	var result *api.CharacterSnapshot
	data, err := s.DB.Collection(snapShotCollection).Doc(userID).Collection(characterID).Doc(snapshotID).Get(ctx)
	if err != nil {
		return nil, err
	}
	err = data.DataTo(&result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *service) AddAggregate(ctx context.Context, userID string, snapshotID string, activityID string, characterID string, level api.ConfidenceLevel, source api.ConfidenceSource) (*api.Aggregate, error) {
	now := time.Now()
	mapping := api.CharacterMapping{
		CharacterID:      characterID,
		SnapshotID:       snapshotID,
		ConfidenceLevel:  level,
		ConfidenceSource: source,
		Snippet: api.SnapshotSnippet{
			PrimaryWeapon: "Test Weapon",
		},
		CreatedAt: now,
	}
	aggregate := api.Aggregate{
		ActivityID: activityID,
		Mapping: map[string]api.CharacterMapping{
			characterID: mapping,
		},
		CreatedAt: now,
	}

	iter := s.DB.Collection(aggregateCollection).Where("activityId", "==", activityID).Documents(ctx)
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
		_, err := s.DB.Collection(aggregateCollection).Doc(existingAggregate.ID).Set(ctx, map[string]any{
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
		ref := s.DB.Collection(aggregateCollection).NewDoc()
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
		Collection(aggregateCollection).
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
