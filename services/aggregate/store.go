package aggregate

import (
	"context"
	"errors"
	"oneTrick/api"
	"slices"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type aggregateStore interface {
	getByActivityID(ctx context.Context, activityID string) (*api.Aggregate, error)
	listAll(ctx context.Context) ([]api.Aggregate, error)
	listBySessionID(ctx context.Context, sessionID string) ([]api.Aggregate, error)
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
	stampSnapshotLinkParticipants(ctx context.Context, aggregateID string, participants map[string]SnapshotLinkParticipant) error
	detachSession(ctx context.Context, aggregateID string, sessionID string, clearSessionCharIDs, removeCharIDs []string) error
	deleteAggregate(ctx context.Context, aggregateID string) error
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

func (f *firestoreAggregateStore) listBySessionID(ctx context.Context, sessionID string) ([]api.Aggregate, error) {
	docs, err := f.db.Collection(collection).
		Where("sessionIds", "array-contains", sessionID).
		Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	aggs := make([]api.Aggregate, 0, len(docs))
	for _, doc := range docs {
		var agg api.Aggregate
		if err := doc.DataTo(&agg); err != nil {
			return nil, err
		}
		if agg.ID == "" {
			agg.ID = doc.Ref.ID
		}
		aggs = append(aggs, agg)
	}
	return aggs, nil
}

func (f *firestoreAggregateStore) detachSession(ctx context.Context, aggregateID string, sessionID string, clearSessionCharIDs, removeCharIDs []string) error {
	nonEmptyRemove := make([]string, 0, len(removeCharIDs))
	for _, charID := range removeCharIDs {
		if charID != "" {
			nonEmptyRemove = append(nonEmptyRemove, charID)
		}
	}

	updates := make([]firestore.Update, 0, 2+len(clearSessionCharIDs)+2*len(nonEmptyRemove))
	updates = append(updates, firestore.Update{
		Path:  "sessionIds",
		Value: firestore.ArrayRemove(sessionID),
	})
	for _, charID := range clearSessionCharIDs {
		if charID == "" {
			continue
		}
		updates = append(updates, firestore.Update{
			FieldPath: firestore.FieldPath{"snapshotLinks", charID, "sessionId"},
			Value:     firestore.Delete,
		})
	}
	if len(nonEmptyRemove) > 0 {
		updates = append(updates, firestore.Update{
			Path:  "characterIds",
			Value: firestore.ArrayRemove(toInterfaceSlice(nonEmptyRemove)...),
		})
		for _, charID := range nonEmptyRemove {
			updates = append(updates,
				firestore.Update{
					FieldPath: firestore.FieldPath{"snapshotLinks", charID},
					Value:     firestore.Delete,
				},
				firestore.Update{
					FieldPath: firestore.FieldPath{"performance", charID},
					Value:     firestore.Delete,
				},
			)
		}
	}
	_, err := f.db.Collection(collection).Doc(aggregateID).Update(ctx, updates)
	return err
}

func (f *firestoreAggregateStore) listAll(ctx context.Context) ([]api.Aggregate, error) {
	docs, err := f.db.Collection(collection).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	aggs := make([]api.Aggregate, 0, len(docs))
	for _, doc := range docs {
		var agg api.Aggregate
		if err := doc.DataTo(&agg); err != nil {
			return nil, err
		}
		if agg.ID == "" {
			agg.ID = doc.Ref.ID
		}
		aggs = append(aggs, agg)
	}
	return aggs, nil
}

func (f *firestoreAggregateStore) stampSnapshotLinkParticipants(ctx context.Context, aggregateID string, participants map[string]SnapshotLinkParticipant) error {
	if aggregateID == "" || len(participants) == 0 {
		return nil
	}
	charIDs := make([]string, 0, len(participants))
	for charID := range participants {
		if charID != "" {
			charIDs = append(charIDs, charID)
		}
	}
	slices.Sort(charIDs)

	updates := make([]firestore.Update, 0, len(charIDs)*4)
	for _, charID := range charIDs {
		meta := participants[charID]
		if meta.UserID != "" {
			updates = append(updates, firestore.Update{
				FieldPath: firestore.FieldPath{"snapshotLinks", charID, "userId"},
				Value:     meta.UserID,
			})
		}
		if meta.DisplayName != "" {
			updates = append(updates, firestore.Update{
				FieldPath: firestore.FieldPath{"snapshotLinks", charID, "displayName"},
				Value:     meta.DisplayName,
			})
		}
		if meta.CharacterClass != "" {
			updates = append(updates, firestore.Update{
				FieldPath: firestore.FieldPath{"snapshotLinks", charID, "characterClass"},
				Value:     meta.CharacterClass,
			})
		}
		if meta.EmblemURL != "" {
			updates = append(updates, firestore.Update{
				FieldPath: firestore.FieldPath{"snapshotLinks", charID, "emblemUrl"},
				Value:     meta.EmblemURL,
			})
		}
	}
	if len(updates) == 0 {
		return nil
	}
	if _, err := f.db.Collection(collection).Doc(aggregateID).Update(ctx, updates); err != nil {
		if status.Code(err) == codes.NotFound {
			return NotFound
		}
		return err
	}
	return nil
}

func (f *firestoreAggregateStore) deleteAggregate(ctx context.Context, aggregateID string) error {
	_, err := f.db.Collection(collection).Doc(aggregateID).Delete(ctx)
	return err
}
