package aggregate

import (
	"context"
	"errors"
	"oneTrick/api"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

type aggregateStore interface {
	getByActivityID(ctx context.Context, activityID string) (*api.Aggregate, error)
	mergeSessionActivity(
		ctx context.Context,
		aggregateID string,
		characterID string,
		snapshotLink api.SnapshotLink,
		performance api.InstancePerformance,
		sessionIDs, snapshotIDs, characterIDs []string,
		estimatedGameMode *api.GameMode,
	) error
	createAggregate(ctx context.Context, agg api.Aggregate) (api.Aggregate, error)
}

type firestoreAggregateStore struct {
	db *firestore.Client
}

var _ aggregateStore = (*firestoreAggregateStore)(nil)

func (f *firestoreAggregateStore) getByActivityID(ctx context.Context, activityID string) (*api.Aggregate, error) {
	iter := f.db.Collection(collection).
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
		var agg api.Aggregate
		if err := doc.DataTo(&agg); err != nil {
			return nil, err
		}
		return &agg, nil
	}
	return nil, NotFound
}

func (f *firestoreAggregateStore) mergeSessionActivity(
	ctx context.Context,
	aggregateID string,
	characterID string,
	snapshotLink api.SnapshotLink,
	performance api.InstancePerformance,
	sessionIDs, snapshotIDs, characterIDs []string,
	estimatedGameMode *api.GameMode,
) error {
	updates := map[string]any{
		"snapshotLinks": map[string]any{
			characterID: snapshotLink,
		},
		"performance": map[string]any{
			characterID: performance,
		},
	}
	if len(sessionIDs) > 0 {
		updates["sessionIds"] = firestore.ArrayUnion(toInterfaceSlice(sessionIDs)...)
	}
	if len(snapshotIDs) > 0 {
		updates["snapshotIds"] = firestore.ArrayUnion(toInterfaceSlice(snapshotIDs)...)
	}
	if len(characterIDs) > 0 {
		updates["characterIds"] = firestore.ArrayUnion(toInterfaceSlice(characterIDs)...)
	}
	if estimatedGameMode != nil {
		updates["activityHistory"] = map[string]any{
			"estimatedGameMode": *estimatedGameMode,
		}
	}
	_, err := f.db.Collection(collection).Doc(aggregateID).Set(ctx, updates, firestore.MergeAll)
	return err
}

func (f *firestoreAggregateStore) createAggregate(ctx context.Context, agg api.Aggregate) (api.Aggregate, error) {
	ref := f.db.Collection(collection).NewDoc()
	agg.ID = ref.ID
	if _, err := ref.Set(ctx, agg); err != nil {
		return api.Aggregate{}, err
	}
	return agg, nil
}
