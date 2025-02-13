package snapshot

import (
	"cloud.google.com/go/firestore"
	"context"
	"errors"
	"google.golang.org/api/iterator"
	"oneTrick/api"
	"oneTrick/utils"
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
	Get(ctx context.Context, snapshotID string) (*api.CharacterSnapshot, error)

	// FindClosest retrieves the closest snapshot to a given activity period timestamp for a specified user and character.
	// Takes a context, user ID, character ID, and activity period (timestamp) as input.
	// Returns the closest CharacterSnapshot or an error if no snapshot is found.
	FindClosest(ctx context.Context, userID string, characterID string, activityPeriod time.Time) (*api.CharacterSnapshot, error)
}

const (
	collection = "snapshots"
)

type service struct {
	DB *firestore.Client
}

func (s *service) FindClosest(ctx context.Context, userID string, characterID string, activityPeriod time.Time) (*api.CharacterSnapshot, error) {
	iter := s.DB.Collection(collection).
		Where("userId", "==", userID).
		Where("characterId", "==", characterID).
		Documents(ctx)
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
	if !snapshot.IsOriginal {
		if snapshot.ParentID != nil {
			og, err := s.Get(ctx, *snapshot.ParentID)
			if err != nil {
				return nil, err
			}
			snapshot.Loadout = og.Loadout
		} else {
			return nil, errors.New("snapshot has no parent but is not an original")
		}
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
	snapshot.UserID = userID
	snapshot.Timestamp = time.Now()
	ref := s.DB.Collection(collection).NewDoc()
	id := ref.ID
	snapshot.ID = ref.ID

	hash, err := utils.HashMap(snapshot.Loadout)
	if err != nil {
		return nil, err
	}
	snapshot.Hash = hash

	og, err := getOptionalOriginal(s.DB, ctx, hash)
	if err != nil {
		return nil, err
	}
	if og == nil {
		snapshot.IsOriginal = true
		snapshot.ParentID = nil
	} else {
		// Clear Loadout because only the original snapshot will hold it
		snapshot.ParentID = &og.ID
		snapshot.Loadout = nil
	}

	_, err = ref.Set(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func (s *service) GetAllByCharacter(ctx context.Context, userID string, characterID string) ([]api.CharacterSnapshot, error) {
	iter := s.DB.Collection(collection).
		Where("userId", "==", userID).
		Where("characterId", "==", characterID).
		Where("isOriginal", "==", true).
		OrderBy("timestamp", firestore.Desc).
		Documents(ctx)
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

func getOptionalOriginal(db *firestore.Client, ctx context.Context, hash string) (*api.CharacterSnapshot, error) {
	og := api.CharacterSnapshot{}
	iter := db.Collection(collection).
		Where("hash", "==", hash).
		Where("isOriginal", "==", true).
		Limit(1).
		Documents(ctx)
	defer iter.Stop()

	for {
		itr, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		err = itr.DataTo(&og)
		if err != nil {
			return nil, err
		}
	}
	if og.ID == "" {
		return nil, nil
	}
	return &og, nil
}
func (s *service) Get(ctx context.Context, snapshotID string) (*api.CharacterSnapshot, error) {
	var result *api.CharacterSnapshot
	data, err := s.DB.Collection(collection).Doc(snapshotID).Get(ctx)
	if err != nil {
		return nil, err
	}
	err = data.DataTo(&result)
	if err != nil {
		return nil, err
	}

	if !result.IsOriginal {
		if result.ParentID != nil {
			og, err := s.Get(ctx, *result.ParentID)
			if err != nil {
				return nil, err
			}
			result.Loadout = og.Loadout
		} else {
			return nil, errors.New("snapshot has no parent but is not an original")
		}
	}

	return result, nil
}
