package snapshot

import (
	"cloud.google.com/go/firestore"
	"context"
	"errors"
	"google.golang.org/api/iterator"
	"oneTrick/api"
	"time"
)

type Service interface {
	Create(ctx context.Context, userID string, snapshot api.CharacterSnapshot) (*string, error)
	GetAllByCharacter(ctx context.Context, userID string, characterID string) ([]api.CharacterSnapshot, error)
	GetClosestSnapshot(ctx context.Context, userID string, characterID string, activityPeriod time.Time) (*api.CharacterSnapshot, error)
}

const (
	snapShotCollection = "snapshots"
)

type Snapshot = map[string]api.CharacterSnapshot
type UserCollection struct {
	Characters map[string]Snapshot `json:"characters" firestore:"characters"`
}

type service struct {
	DB *firestore.Client
}

func (s *service) GetClosestSnapshot(ctx context.Context, userID string, characterID string, activityPeriod time.Time) (*api.CharacterSnapshot, error) {
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
		return nil, errors.New("couldn't find a snapshot")
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
	ref := s.DB.Collection(snapShotCollection).Doc(userID).Collection(snapshot.CharacterId).NewDoc()
	id := ref.ID
	_, err := ref.Set(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func (s *service) GetAllByCharacter(ctx context.Context, userID string, characterID string) ([]api.CharacterSnapshot, error) {
	iter := s.DB.Collection(snapShotCollection).Doc(userID).Collection(characterID).Documents(ctx)
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
