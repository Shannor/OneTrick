package session

import (
	"context"
	"fmt"
	"oneTrick/api"
	"oneTrick/utils"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type sessionStore interface {
	hasActive(ctx context.Context, userID, characterID string) (bool, error)
	createDoc(ctx context.Context, ses *api.Session) (*api.Session, error)
	getActiveDoc(ctx context.Context, userID, characterID string) (*api.Session, error)
	getDoc(ctx context.Context, sessionID string) (*api.Session, error)
	listDocs(ctx context.Context, userID, characterID *string, st *api.SessionStatus) ([]api.Session, error)
	updateFields(ctx context.Context, sessionID string, updates []firestore.Update) error
	completeDoc(ctx context.Context, sessionID string, completedAt time.Time) error
	updateCompTrackingEnd(ctx context.Context, sessionID string, end *api.CompRankSnapshot, pointDelta *int) error
	setLastActivity(ctx context.Context, sessionID, activityID string, seenAt time.Time) error
	deleteDoc(ctx context.Context, sessionID string) error
	addAggregateIDs(ctx context.Context, sessionID string, aggregateIDs []string) error
}

type firestoreStore struct {
	db *firestore.Client
}

var _ sessionStore = (*firestoreStore)(nil)

func (f *firestoreStore) hasActive(ctx context.Context, userID, characterID string) (bool, error) {
	query := f.db.Collection(collection).
		Where("userId", "==", userID).
		Where("characterId", "==", characterID).
		Where("status", "==", api.SessionPending).
		Limit(1)

	docs, err := query.Documents(ctx).GetAll()
	if err != nil {
		return false, err
	}
	return len(docs) > 0, nil
}

func (f *firestoreStore) createDoc(ctx context.Context, ses *api.Session) (*api.Session, error) {
	ref := f.db.Collection(collection).NewDoc()
	ses.ID = ref.ID
	if _, err := ref.Set(ctx, ses); err != nil {
		return nil, err
	}
	return ses, nil
}

func (f *firestoreStore) getActiveDoc(ctx context.Context, userID, characterID string) (*api.Session, error) {
	query := f.db.Collection(collection).
		Where("userId", "==", userID).
		Where("characterId", "==", characterID).
		Where("status", "==", api.SessionPending).
		Limit(1)

	docs, err := query.Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("no active session found")
	}
	result := &api.Session{}
	if err := docs[0].DataTo(&result); err != nil {
		return nil, err
	}
	return result, nil
}

func (f *firestoreStore) getDoc(ctx context.Context, sessionID string) (*api.Session, error) {
	doc, err := f.db.Collection(collection).Doc(sessionID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	result := &api.Session{}
	if err := doc.DataTo(&result); err != nil {
		return nil, err
	}
	return result, nil
}

func (f *firestoreStore) listDocs(ctx context.Context, userID, characterID *string, st *api.SessionStatus) ([]api.Session, error) {
	query := f.db.Collection(collection).Query
	if userID != nil && *userID != "" {
		query = query.Where("userId", "==", *userID)
	}
	if characterID != nil && *characterID != "" {
		query = query.Where("characterId", "==", *characterID)
	}
	if st != nil && *st != "" {
		query = query.Where("status", "==", *st)
	}
	docs, err := query.Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("failed to query sessions: %w", err)
	}
	result, err := utils.GetAllToStructs[api.Session](docs)
	if err != nil {
		return nil, fmt.Errorf("failed to parse sessions: %w", err)
	}
	return result, nil
}

func (f *firestoreStore) updateFields(ctx context.Context, sessionID string, updates []firestore.Update) error {
	_, err := f.db.Collection(collection).Doc(sessionID).Update(ctx, updates)
	return err
}

func (f *firestoreStore) completeDoc(ctx context.Context, sessionID string, completedAt time.Time) error {
	_, err := f.db.Collection(collection).Doc(sessionID).Update(ctx, []firestore.Update{
		{
			Path:  "completedAt",
			Value: completedAt,
		},
		{
			Path:  "status",
			Value: api.SessionComplete,
		},
	})
	return err
}

func (f *firestoreStore) updateCompTrackingEnd(ctx context.Context, sessionID string, end *api.CompRankSnapshot, pointDelta *int) error {
	updates := []firestore.Update{
		{
			Path:  "compTracking.end",
			Value: end,
		},
	}
	if pointDelta != nil {
		updates = append(updates, firestore.Update{
			Path:  "compTracking.pointDelta",
			Value: *pointDelta,
		})
	}
	_, err := f.db.Collection(collection).Doc(sessionID).Update(ctx, updates)
	return err
}

func (f *firestoreStore) setLastActivity(ctx context.Context, sessionID, activityID string, seenAt time.Time) error {
	_, err := f.db.Collection(collection).Doc(sessionID).Update(ctx, []firestore.Update{
		{
			Path:  "lastSeenActivityId",
			Value: activityID,
		},
		{
			Path:  "lastSeenTimestamp",
			Value: seenAt,
		},
	})
	return err
}

func (f *firestoreStore) deleteDoc(ctx context.Context, sessionID string) error {
	_, err := f.db.Collection(collection).Doc(sessionID).Delete(ctx)
	return err
}

func (f *firestoreStore) addAggregateIDs(ctx context.Context, sessionID string, aggregateIDs []string) error {
	ids := make([]any, 0, len(aggregateIDs))
	for _, d := range aggregateIDs {
		ids = append(ids, d)
	}
	_, err := f.db.Collection(collection).Doc(sessionID).Update(ctx, []firestore.Update{
		{
			Path:  "aggregateIds",
			Value: firestore.ArrayUnion(ids...),
		},
	})
	return err
}
