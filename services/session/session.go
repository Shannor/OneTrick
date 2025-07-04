package session

import (
	"cloud.google.com/go/firestore"
	"context"
	"fmt"
	"log/slog"
	"oneTrick/api"
	"oneTrick/generator"
	"oneTrick/ptr"
	"oneTrick/utils"
	"slices"
	"time"
)

type Service interface {
	Start(ctx context.Context, userID string, characterID string) (*api.Session, error)
	AddAggregateIDs(ctx context.Context, sessionID string, aggregateIDs []string) error
	Get(ctx context.Context, ID string) (*api.Session, error)
	GetActive(ctx context.Context, userID string, characterID string) (*api.Session, error)
	GetAll(ctx context.Context, userID *string, characterID *string, status *api.SessionStatus) ([]api.Session, error)
	Complete(ctx context.Context, ID string) error
}
type service struct {
	db *firestore.Client
}

var _ Service = (*service)(nil)

func NewService(db *firestore.Client) Service {
	return &service{
		db: db,
	}
}

const (
	collection = "sessions"
)

func (s service) Start(ctx context.Context, userID string, characterID string) (*api.Session, error) {
	if ok, err := s.HasActive(ctx, userID, characterID); ok || err != nil {
		return nil, fmt.Errorf("session already active")
	}
	result := &api.Session{
		UserID:       userID,
		StartedAt:    time.Now(),
		CharacterID:  characterID,
		Name:         ptr.Of(generator.SessionName()),
		AggregateIDs: make([]string, 0),
		Status:       ptr.Of(api.SessionPending),
	}
	ref := s.db.Collection(collection).NewDoc()
	result.ID = ref.ID
	_, err := ref.Set(ctx, result)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (s service) HasActive(ctx context.Context, userID string, characterID string) (bool, error) {
	query := s.db.Collection(collection).
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

func (s service) GetActive(ctx context.Context, userID string, characterID string) (*api.Session, error) {
	query := s.db.Collection(collection).
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
	err = docs[0].DataTo(&result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s service) Get(ctx context.Context, ID string) (*api.Session, error) {
	doc, err := s.db.Collection(collection).Doc(ID).Get(ctx)
	if err != nil {
		return nil, err
	}
	result := &api.Session{}
	err = doc.DataTo(&result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s service) GetAll(ctx context.Context, userID *string, characterID *string, status *api.SessionStatus) ([]api.Session, error) {
	query := s.db.Collection(collection).Query

	if userID != nil {
		query = query.Where("userId", "==", *userID)
	}
	if characterID != nil {
		query = query.Where("characterId", "==", *characterID)
	}

	if status != nil {
		query = query.Where("status", "==", *status)
		switch *status {
		case api.SessionPending:
			query = query.Limit(1)
		}
	}
	docs, err := query.Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}

	result, err := utils.GetAllToStructs[api.Session](docs)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(result, func(a, b api.Session) int {
		return b.StartedAt.Compare(a.StartedAt)
	})
	return result, nil
}

func (s service) Complete(ctx context.Context, ID string) error {
	ref := s.db.Collection(collection).Doc(ID)

	data, err := ref.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to get session: %w", err)
	}
	session := api.Session{}
	err = data.DataTo(&s)
	if err != nil {
		return fmt.Errorf("failed to get session: %w", err)
	}
	if session.CompletedAt == nil {
		_, err := ref.Update(ctx, []firestore.Update{
			{
				Path:  "completedAt",
				Value: time.Now(),
			},
			{
				Path:  "status",
				Value: api.SessionComplete,
			},
		})
		if err != nil {
			return fmt.Errorf("failed to complete session: %w", err)
		}
	} else {
		slog.With("sessionID", ID).Warn("session already completed")
	}

	return nil
}

func (s service) AddAggregateIDs(ctx context.Context, sessionID string, aggregateIDs []string) error {
	ids := make([]any, 0)
	for _, d := range aggregateIDs {
		ids = append(ids, d)
	}
	_, err := s.db.Collection(collection).Doc(sessionID).Update(ctx, []firestore.Update{
		{
			Path:  "aggregateIds",
			Value: firestore.ArrayUnion(ids...),
		},
	})
	if err != nil {
		return err
	}
	return nil
}
