package aggregate

import (
	"context"
	"errors"
	"fmt"
	"oneTrick/api"
	"oneTrick/utils"
	"sort"

	"cloud.google.com/go/firestore"
	"github.com/rs/zerolog/log"
	"google.golang.org/api/iterator"
)

var NotFound = errors.New("not found")

// Service defines the interface for working with character snapshots and aggregates.
type Service interface {
	// GetAggregate retrieves an existing aggregate for a given activity ID.
	GetAggregate(ctx context.Context, activityID string) (*api.Aggregate, error)

	// GetAggregates retrieves a list of aggregates for the given session IDs.
	// Limited to max of 30 documents at once
	GetAggregates(ctx context.Context, IDs []string) ([]api.Aggregate, error)

	BySnapshotID(ctx context.Context, snapshotID string, gameModeFilter []string) ([]api.Aggregate, error)

	UpdateAllAggregates(ctx context.Context) (int, error)

	// GetAggregatesByActivity retrieves a list of aggregates for the given activity IDs.
	// Limited to a max of 30 documents at once
	GetAggregatesByActivity(ctx context.Context, activityIDs []string) ([]api.Aggregate, error)

	// Update allows for updating an aggregate document's data.
	Update(ctx context.Context, aggregateID string, updateFn func(data map[string]interface{}) error, shouldMerge bool) error
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

func (s *service) Update(ctx context.Context, aggregateID string, updateFn func(data map[string]any) error, shouldMerge bool) error {
	docRef := s.DB.Collection(collection).Doc(aggregateID)
	doc, err := docRef.Get(ctx)
	if err != nil {
		return err
	}

	data := doc.Data()
	if err := updateFn(data); err != nil {
		return err
	}

	if shouldMerge {
		_, err = docRef.Set(ctx, data, firestore.MergeAll)
		if err != nil {
			log.Error().Err(err).Msg("failed to merge aggregate")
			return err
		}
		return nil
	}
	_, err = docRef.Set(ctx, data)
	if err != nil {
		log.Error().Err(err).Msg("failed to merge aggregate")
		return err
	}
	return err
}

func (s *service) BySnapshotID(ctx context.Context, snapshotID string, gameModeFilter []string) ([]api.Aggregate, error) {
	if snapshotID == "" {
		return nil, fmt.Errorf("snapshotID is required")
	}

	q := s.DB.Collection(collection).
		Where("snapshotIds", "array-contains", snapshotID)

	if len(gameModeFilter) > 0 {
		q = q.Where("activityHistory.mode", "in", gameModeFilter)
	}

	docs, err := q.Documents(ctx).GetAll()

	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[api.Aggregate](docs)
	if err != nil {
		return nil, err
	}
	return results, nil
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

func (s *service) GetAggregatesByActivity(ctx context.Context, activityIDs []string) ([]api.Aggregate, error) {
	docs, err := s.DB.
		Collection(collection).
		Where("activityId", "in", activityIDs).
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

func (s *service) GetAggregates(ctx context.Context, IDs []string) ([]api.Aggregate, error) {
	if len(IDs) == 0 {
		return []api.Aggregate{}, nil
	}

	var allResults []api.Aggregate
	batchSize := 30

	// Process IDs in batches of 30
	for i := 0; i < len(IDs); i += batchSize {
		end := i + batchSize
		if end > len(IDs) {
			end = len(IDs)
		}
		batch := IDs[i:end]

		docs, err := s.DB.
			Collection(collection).
			Where("id", "in", batch).
			Documents(ctx).GetAll()
		if err != nil {
			return nil, err
		}

		results, err := utils.GetAllToStructs[api.Aggregate](docs)
		if err != nil {
			return nil, err
		}

		allResults = append(allResults, results...)
	}
	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].CreatedAt.Before(allResults[j].CreatedAt)
	})
	return allResults, nil
}

func (s *service) GetAllAggregates(ctx context.Context) ([]api.Aggregate, error) {
	docs, err := s.DB.
		Collection(collection).
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

func (s *service) UpdateAllAggregates(ctx context.Context) (int, error) {
	aggregates, err := s.GetAllAggregates(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, agg := range aggregates {
		snapshotIDs := make([]string, 0)
		sessionIDs := make([]string, 0)
		characterIDs := make([]string, 0)
		if len(agg.SnapshotLinks) > 0 {
			for _, link := range agg.SnapshotLinks {
				if link.SnapshotID != nil {
					snapshotIDs = append(snapshotIDs, *link.SnapshotID)
				}
				if link.SessionID != nil {
					sessionIDs = append(sessionIDs, *link.SessionID)
				}
				characterIDs = append(characterIDs, link.CharacterID)
			}
			_, err := s.DB.Collection(collection).Doc(agg.ID).Set(ctx, map[string]any{
				"sessionIds":   firestore.ArrayUnion(toInterfaceSlice(sessionIDs)...),
				"snapshotIds":  firestore.ArrayUnion(toInterfaceSlice(snapshotIDs)...),
				"characterIds": firestore.ArrayUnion(toInterfaceSlice(characterIDs)...),
			}, firestore.MergeAll)
			if err != nil {
				log.Warn().Str("id", agg.ID).Err(err).Msg("failed to update aggregate")
			}
			count++
		}
	}
	return count, nil
}

// Helper function to convert any slice to []interface{}
func toInterfaceSlice[T any](slice []T) []interface{} {
	result := make([]interface{}, len(slice))
	for i, v := range slice {
		result[i] = v
	}
	return result
}
