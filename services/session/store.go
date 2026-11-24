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

const firestoreInBatchLimit = 30

type activeSessionDoc struct {
	ID           string    `firestore:"id"`
	UserID       string    `firestore:"userId"`
	CharacterID  string    `firestore:"characterId"`
	StartedAt    time.Time `firestore:"startedAt"`
	CompTracking *struct {
		Enabled bool `firestore:"enabled"`
	} `firestore:"compTracking"`
}

type Store interface {
	// GetActiveByUserIDs expects pre-trimmed, deduplicated userIDs; returned order is unspecified.
	GetActiveByUserIDs(ctx context.Context, userIDs []string) ([]ActiveSession, error)
	HasActive(ctx context.Context, userID, characterID string) (bool, error)
	CreateDoc(ctx context.Context, ses *api.Session) (*api.Session, error)
	GetActiveDoc(ctx context.Context, userID, characterID string) (*api.Session, error)
	GetDoc(ctx context.Context, sessionID string) (*api.Session, error)
	GetDocWithMeta(ctx context.Context, sessionID string) (*TickCandidate, error)
	ListDocs(ctx context.Context, userID, characterID *string, st *api.SessionStatus) ([]api.Session, error)
	ListCompletedInWindow(ctx context.Context, since, until time.Time) ([]api.Session, error)
	GetTickCandidates(ctx context.Context, completedSince time.Time) ([]TickCandidate, error)
	UpdateFields(ctx context.Context, sessionID string, updates []firestore.Update) error
	CompleteDoc(ctx context.Context, sessionID string, completedAt time.Time) error
	EndSessionDoc(ctx context.Context, sessionID string, completedBy api.AuditField, completedAt time.Time, summary *api.SessionSummary) error
	UpdateSummary(ctx context.Context, sessionID string, summary *api.SessionSummary, updatedAt time.Time) error
	UpdateCompTrackingEnd(ctx context.Context, sessionID string, end *api.CompRankSnapshot, pointDelta *int) error
	SetLastActivity(ctx context.Context, sessionID, activityID string, seenAt time.Time) error
	TouchLastSeen(ctx context.Context, sessionID string, seenAt time.Time) error
	SetLastSnapshot(ctx context.Context, sessionID, snapshotID, snapshotHash string, recordedAt time.Time) error
	DeleteDoc(ctx context.Context, sessionID string) error
	AddAggregateIDs(ctx context.Context, sessionID string, aggregateIDs []string) error
}

type FirestoreStore struct {
	db *firestore.Client
}

func NewFirestoreStore(db *firestore.Client) *FirestoreStore {
	return &FirestoreStore{db: db}
}

var _ Store = (*FirestoreStore)(nil)

func (f *FirestoreStore) HasActive(ctx context.Context, userID, characterID string) (bool, error) {
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

func (f *FirestoreStore) CreateDoc(ctx context.Context, ses *api.Session) (*api.Session, error) {
	ref := f.db.Collection(collection).NewDoc()
	ses.ID = ref.ID
	if _, err := ref.Set(ctx, ses); err != nil {
		return nil, err
	}
	return ses, nil
}

func (f *FirestoreStore) GetActiveDoc(ctx context.Context, userID, characterID string) (*api.Session, error) {
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

func (f *FirestoreStore) GetActiveByUserIDs(ctx context.Context, userIDs []string) ([]ActiveSession, error) {
	if f.db == nil || len(userIDs) == 0 {
		return []ActiveSession{}, nil
	}

	results := make([]ActiveSession, 0, len(userIDs))
	for _, chunk := range utils.Chunk(userIDs, firestoreInBatchLimit) {
		docs, err := f.db.Collection(collection).
			Where("userId", "in", chunk).
			Where("status", "==", api.SessionPending).
			Select("id", "userId", "characterId", "startedAt", "compTracking").
			Documents(ctx).
			GetAll()
		if err != nil {
			return nil, fmt.Errorf("failed to query active sessions for users: %w", err)
		}
		for _, doc := range docs {
			var d activeSessionDoc
			if err := doc.DataTo(&d); err != nil {
				return nil, fmt.Errorf("failed to parse active session %s: %w", doc.Ref.ID, err)
			}
			sessionID := d.ID
			if sessionID == "" {
				sessionID = doc.Ref.ID
			}
			results = append(results, ActiveSession{
				SessionID:   sessionID,
				UserID:      d.UserID,
				CharacterID: d.CharacterID,
				StartedAt:   d.StartedAt,
				TrackComp:   d.CompTracking != nil && d.CompTracking.Enabled,
			})
		}
	}

	return results, nil
}

func (f *FirestoreStore) GetDoc(ctx context.Context, sessionID string) (*api.Session, error) {
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

func (f *FirestoreStore) GetDocWithMeta(ctx context.Context, sessionID string) (*TickCandidate, error) {
	doc, err := f.db.Collection(collection).Doc(sessionID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	cand, err := parseTickCandidate(doc)
	if err != nil {
		return nil, err
	}
	return &cand, nil
}

func parseTickCandidate(doc *firestore.DocumentSnapshot) (TickCandidate, error) {
	var s api.Session
	if err := doc.DataTo(&s); err != nil {
		return TickCandidate{}, err
	}
	var meta struct {
		UpdatedAt              *time.Time `firestore:"updatedAt"`
		LastSnapshotID         string     `firestore:"lastSnapshotId"`
		LastSnapshotHash       string     `firestore:"lastSnapshotHash"`
		LastSnapshotRecordedAt time.Time  `firestore:"lastSnapshotRecordedAt"`
	}
	if err := doc.DataTo(&meta); err != nil {
		return TickCandidate{}, fmt.Errorf("failed to parse session tick metadata for %s: %w", s.ID, err)
	}
	return TickCandidate{
		Session:                s,
		UpdatedAt:              meta.UpdatedAt,
		LastSnapshotID:         meta.LastSnapshotID,
		LastSnapshotHash:       meta.LastSnapshotHash,
		LastSnapshotRecordedAt: meta.LastSnapshotRecordedAt,
	}, nil
}

func (f *FirestoreStore) ListDocs(ctx context.Context, userID, characterID *string, st *api.SessionStatus) ([]api.Session, error) {
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

func (f *FirestoreStore) ListCompletedInWindow(ctx context.Context, since, until time.Time) ([]api.Session, error) {
	docs, err := f.db.Collection(collection).
		Where("status", "==", api.SessionComplete).
		Where("completedAt", ">=", since).
		Where("completedAt", "<=", until).
		Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("failed to query completed sessions in cleanup window: %w", err)
	}
	result, err := utils.GetAllToStructs[api.Session](docs)
	if err != nil {
		return nil, fmt.Errorf("failed to parse completed sessions: %w", err)
	}
	return result, nil
}

func (f *FirestoreStore) GetTickCandidates(ctx context.Context, completedSince time.Time) ([]TickCandidate, error) {
	orFilter := firestore.OrFilter{
		Filters: []firestore.EntityFilter{
			firestore.PropertyFilter{
				Path:     "status",
				Operator: "==",
				Value:    api.SessionPending,
			},
			firestore.PropertyFilter{
				Path:     "completedAt",
				Operator: ">=",
				Value:    completedSince,
			},
		},
	}

	docs, err := f.db.Collection(collection).
		WhereEntity(orFilter).
		Documents(ctx).
		GetAll()
	if err != nil {
		return nil, err
	}

	candidates := make([]TickCandidate, 0, len(docs))
	for _, doc := range docs {
		cand, err := parseTickCandidate(doc)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, cand)
	}
	return candidates, nil
}

func (f *FirestoreStore) UpdateFields(ctx context.Context, sessionID string, updates []firestore.Update) error {
	_, err := f.db.Collection(collection).Doc(sessionID).Update(ctx, updates)
	return err
}

func (f *FirestoreStore) CompleteDoc(ctx context.Context, sessionID string, completedAt time.Time) error {
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

func (f *FirestoreStore) EndSessionDoc(ctx context.Context, sessionID string, completedBy api.AuditField, completedAt time.Time, summary *api.SessionSummary) error {
	updates := []firestore.Update{
		{
			Path:  "completedBy",
			Value: completedBy,
		},
		{
			Path:  "status",
			Value: api.SessionComplete,
		},
		{
			Path:  "completedAt",
			Value: completedAt,
		},
		{
			Path:  "updatedAt",
			Value: completedAt,
		},
	}
	if summary != nil {
		updates = append(updates, firestore.Update{
			Path:  "sessionSummary",
			Value: summary,
		})
	}
	_, err := f.db.Collection(collection).Doc(sessionID).Update(ctx, updates)
	return err
}

func (f *FirestoreStore) UpdateSummary(ctx context.Context, sessionID string, summary *api.SessionSummary, updatedAt time.Time) error {
	_, err := f.db.Collection(collection).Doc(sessionID).Update(ctx, []firestore.Update{
		{
			Path:  "sessionSummary",
			Value: summary,
		},
		{
			Path:  "updatedAt",
			Value: updatedAt,
		},
	})
	return err
}

func (f *FirestoreStore) UpdateCompTrackingEnd(ctx context.Context, sessionID string, end *api.CompRankSnapshot, pointDelta *int) error {
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

func (f *FirestoreStore) SetLastActivity(ctx context.Context, sessionID, activityID string, seenAt time.Time) error {
	_, err := f.db.Collection(collection).Doc(sessionID).Update(ctx, []firestore.Update{
		{
			Path:  "lastSeenActivityId",
			Value: activityID,
		},
		{
			Path:  "lastSeenTimestamp",
			Value: seenAt,
		},
		{
			Path:  "updatedAt",
			Value: seenAt,
		},
	})
	return err
}

func (f *FirestoreStore) TouchLastSeen(ctx context.Context, sessionID string, seenAt time.Time) error {
	_, err := f.db.Collection(collection).Doc(sessionID).Update(ctx, []firestore.Update{
		{
			Path:  "lastSeenTimestamp",
			Value: seenAt,
		},
		{
			Path:  "updatedAt",
			Value: seenAt,
		},
	})
	return err
}

func (f *FirestoreStore) SetLastSnapshot(ctx context.Context, sessionID, snapshotID, snapshotHash string, recordedAt time.Time) error {
	_, err := f.db.Collection(collection).Doc(sessionID).Update(ctx, []firestore.Update{
		{
			Path:  "lastSnapshotId",
			Value: snapshotID,
		},
		{
			Path:  "lastSnapshotHash",
			Value: snapshotHash,
		},
		{
			Path:  "lastSnapshotRecordedAt",
			Value: recordedAt,
		},
	})
	return err
}

func (f *FirestoreStore) DeleteDoc(ctx context.Context, sessionID string) error {
	_, err := f.db.Collection(collection).Doc(sessionID).Delete(ctx)
	return err
}

func (f *FirestoreStore) AddAggregateIDs(ctx context.Context, sessionID string, aggregateIDs []string) error {
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
