package snapshot

import (
	"cloud.google.com/go/firestore"
	"context"
	"errors"
	"fmt"
	"google.golang.org/api/iterator"
	"log/slog"
	"oneTrick/api"
	"oneTrick/services/destiny"
	"oneTrick/services/user"
	"oneTrick/utils"
	"strconv"
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

	// FindBestFit TODO: Update the logic around how we find the best fit and the link creation
	FindBestFit(ctx context.Context, userID string, characterID string, activityPeriod time.Time) (*api.CharacterSnapshot, *api.SnapshotLink, error)

	OptionalSnapshotAndLink(ctx context.Context, agg *api.Aggregate, characterID string) (*api.CharacterSnapshot, *api.SnapshotLink, error)
	EnrichWeaponInstances(snapshot *api.CharacterSnapshot, metrics []api.WeaponInstanceMetrics) ([]api.WeaponInstanceMetrics, error)
	GenerateSnapshot(ctx context.Context, userID, membershipID, characterID string) (*api.CharacterSnapshot, error)
}

const (
	collection = "snapshots"
)

type service struct {
	DB          *firestore.Client
	UserService user.Service
	D2Service   destiny.Service
}

var _ Service = (*service)(nil)

func NewService(db *firestore.Client, userService user.Service, d2Service destiny.Service) Service {
	return &service{
		DB:          db,
		UserService: userService,
		D2Service:   d2Service,
	}
}

func (s *service) Create(ctx context.Context, userID string, snapshot api.CharacterSnapshot) (*string, error) {
	snapshot.UserID = userID
	snapshot.Timestamp = time.Now()
	ref := s.DB.Collection(collection).NewDoc()
	id := ref.ID
	snapshot.ID = ref.ID

	// TODO: Logic could move to the GetSnapshotDataFunction
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

func hasLink(agg *api.Aggregate, characterID string) bool {
	if agg == nil {
		return false
	}
	link, ok := agg.SnapshotLinks[characterID]
	if !ok {
		return false
	}
	return link.CharacterID != ""
}

func isConfidenceLevelAcceptable(level api.ConfidenceLevel) bool {
	return level != api.NotFoundConfidenceLevel && level != api.NoMatchConfidenceLevel
}

func (s *service) OptionalSnapshotAndLink(ctx context.Context, agg *api.Aggregate, characterID string) (*api.CharacterSnapshot, *api.SnapshotLink, error) {
	if agg == nil {
		return nil, nil, nil
	}

	// If agg has a link already
	if hasLink(agg, characterID) {
		link := agg.SnapshotLinks[characterID]
		// Return the link and applicable snapshot
		if isConfidenceLevelAcceptable(link.ConfidenceLevel) {
			if link.SnapshotID == nil {
				slog.Error("link has no snapshot id but expected one: %v", link)
				return nil, nil, fmt.Errorf("link has no snapshot id but expected one: %v", link)
			}
			snapshot, err := s.Get(ctx, *link.SnapshotID)
			if err != nil {
				return nil, nil, err
			}
			return snapshot, &link, nil
		}
		// We do have a link but, we didn't find a snapshot in the past.
		return nil, &link, nil
	}

	return nil, nil, nil
}

func (s *service) EnrichWeaponInstances(snapshot *api.CharacterSnapshot, metrics []api.WeaponInstanceMetrics) ([]api.WeaponInstanceMetrics, error) {
	if snapshot == nil {
		slog.Info("No provided snapshot to perform enrichment on")
		return metrics, nil
	}

	if len(metrics) == 0 {
		slog.Info("No metrics provided to enrich")
		return nil, nil
	}
	if snapshot.Loadout == nil {
		slog.Info("No loadout provided to enrich")
		return metrics, nil
	}

	mapping := map[int64]api.ItemProperties{}
	for _, component := range snapshot.Loadout {
		mapping[component.ItemHash] = component.ItemProperties
	}

	results := make([]api.WeaponInstanceMetrics, 0)
	for _, metric := range metrics {
		result := api.WeaponInstanceMetrics{}
		if metric.ReferenceID == nil {
			continue
		}
		result.ReferenceID = metric.ReferenceID
		result.Stats = metric.Stats

		properties, ok := mapping[*metric.ReferenceID]
		if ok {
			result.ItemProperties = &properties
		}
		results = append(results, result)
	}

	return results, nil
}

func (s *service) GenerateSnapshot(ctx context.Context, userID, membershipID, characterID string) (*api.CharacterSnapshot, error) {

	membershipType, err := s.UserService.GetMembershipType(ctx, userID, membershipID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch membership type: %w", err)
	}

	memID, err := strconv.ParseInt(membershipID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid membership id: %w", err)
	}

	items, timestamp, err := s.D2Service.GetCurrentInventory(ctx, memID, membershipType, characterID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch profile data: %w", err)
	}
	if timestamp == nil {
		return nil, fmt.Errorf("failed to fetch timestamp for profile data: %w", err)
	}

	result := api.CharacterSnapshot{
		Timestamp:   *timestamp,
		UserID:      userID,
		CharacterID: characterID,
	}

	itemSnapshots := make(api.Loadout)
	for _, item := range items {
		if item.ItemInstanceId == nil {
			return nil, fmt.Errorf("missing instance id for item hash: %d", item.ItemHash)
		}
		snap := api.ItemSnapshot{
			InstanceID: *item.ItemInstanceId,
		}
		details, err := s.D2Service.GetWeaponDetails(ctx, membershipID, membershipType, *item.ItemInstanceId)
		if err != nil {
			return nil, fmt.Errorf("couldn't find an item with item hash %d", item.ItemHash)
		}
		snap.Name = details.BaseInfo.Name
		snap.ItemHash = details.BaseInfo.ItemHash
		snap.ItemProperties = *details
		snap.BucketHash = &details.BaseInfo.BucketHash
		itemSnapshots[strconv.FormatInt(snap.ItemProperties.BaseInfo.BucketHash, 10)] = snap
	}

	result.Loadout = itemSnapshots

	hash, err := utils.HashMap(result.Loadout)
	if err != nil {
		return nil, err
	}
	result.Hash = hash

	og, err := getOptionalOriginal(s.DB, ctx, hash)
	if err != nil {
		return nil, err
	}
	if og == nil {
		result.IsOriginal = true
		result.ParentID = nil
	} else {
		// Clear Loadout because only the original snapshot will hold it
		result.ParentID = &og.ID
		result.Loadout = nil
	}

	return &result, nil
}

func (s *service) FindBestFit(ctx context.Context, userID string, characterID string, activityPeriod time.Time) (*api.CharacterSnapshot, *api.SnapshotLink, error) {
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
			return nil, nil, err
		}
		err = doc.DataTo(&s)
		if err != nil {
			return nil, nil, err
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
		link := api.SnapshotLink{
			CharacterID:      characterID,
			ConfidenceLevel:  api.NotFoundConfidenceLevel,
			ConfidenceSource: api.SystemConfidenceSource,
			CreatedAt:        time.Now(),
		}
		return nil, &link, nil
	}

	if !snapshot.IsOriginal {
		if snapshot.ParentID != nil {
			og, err := s.Get(ctx, *snapshot.ParentID)
			if err != nil {
				return nil, nil, err
			}
			snapshot.Loadout = og.Loadout
		} else {
			return nil, nil, errors.New("snapshot has no parent but is not an original")
		}
	}

	// TODO: Think about this and add features to actually decide on how to generate the link
	link := api.SnapshotLink{
		CharacterID:      characterID,
		ConfidenceLevel:  api.MediumConfidenceLevel,
		ConfidenceSource: api.SystemConfidenceSource,
		CreatedAt:        time.Now(),
		SnapshotID:       &snapshot.ID,
	}

	return snapshot, &link, nil
}
