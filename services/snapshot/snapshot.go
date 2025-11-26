package snapshot

import (
	"oneTrick/services/aggregate"

	"cloud.google.com/go/firestore"
	"github.com/rs/zerolog/log"

	"context"
	"fmt"
	"oneTrick/api"
	"oneTrick/generator"
	"oneTrick/ptr"
	"oneTrick/services/destiny"
	"oneTrick/services/user"
	"oneTrick/utils"
	"strconv"
	"time"
)

// Service defines the interface for working with character snapshots and aggregates.
type Service interface {

	// Save saves a new snapshot for the specified character for a user. Returns the
	// snapshot data on success and an error if the generating or save to the DB fails
	Save(ctx context.Context, userID, membershipID, characterID string) (*api.CharacterSnapshot, error)

	// GetAllByCharacter retrieves all snapshots for a given user and character.
	// Snapshots are returned in reverse chronological order based on their timestamp.
	// Takes a context, user ID, and character ID as input.
	// Returns a slice of snapshots or an error if the operation fails.
	GetAllByCharacter(ctx context.Context, userID string, characterID string) ([]api.CharacterSnapshot, error)

	// Get retrieves a specific snapshot for a given user, character, and snapshot ID.
	// Takes a context, user ID, character ID, and snapshot ID as input.
	// Returns the requested CharacterSnapshot or an error if the snapshot is not found or cannot be retrieved.
	Get(ctx context.Context, snapshotID string) (*api.CharacterSnapshot, error)

	// GetByIDs retrieves multiple snapshots for a given list of snapshot IDs.
	GetByIDs(ctx context.Context, snapshotIDs []string) ([]api.CharacterSnapshot, error)

	// Merge merges two character snapshots identified by snapshotID and targetSnapshotID, storing the result in a new snapshot.
	Merge(ctx context.Context, targetSnapshotID, sourceSnapshotID string) (api.CharacterSnapshot, error)

	LookupLink(agg *api.Aggregate, characterID string) *api.SnapshotLink
	EnrichInstancePerformance(snapshot *api.CharacterSnapshot, performance api.InstancePerformance) (*api.InstancePerformance, error)
}

const (
	collection        = "snapshots"
	historyCollection = "histories"
)

type service struct {
	DB               *firestore.Client
	UserService      user.Service
	D2Service        destiny.Service
	aggregateService aggregate.Service
}

var _ Service = (*service)(nil)

func NewService(db *firestore.Client, userService user.Service, d2Service destiny.Service, aggregateService aggregate.Service) Service {
	return &service{
		DB:               db,
		UserService:      userService,
		D2Service:        d2Service,
		aggregateService: aggregateService,
	}
}

func (s *service) create(ctx context.Context, userID string, snapshot api.CharacterSnapshot) (*string, error) {

	if snapshot.Hash == "" {
		hash, err := utils.HashMap(snapshot.Loadout)
		if err != nil {
			return nil, err
		}
		snapshot.Hash = hash
	}

	existingSnapshot, err := optionalGetByHash(s.DB, ctx, snapshot.Hash)
	if err != nil {
		return nil, err
	}
	if existingSnapshot != nil {
		return s.createHistoryEntry(ctx, *existingSnapshot)
	}

	snapshot.UserID = userID
	now := time.Now()
	snapshot.CreatedAt = now
	snapshot.UpdatedAt = now
	if snapshot.Name == "" {
		snapshot.Name = generator.PVPName()
	}
	ref := s.DB.Collection(collection).NewDoc()
	snapshot.ID = ref.ID
	_, err = ref.Set(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	return s.createHistoryEntry(ctx, snapshot)
}

func (s *service) createHistoryEntry(ctx context.Context, og api.CharacterSnapshot) (*string, error) {
	now := time.Now()
	history := History{
		ParentID:    og.ID,
		UserID:      og.UserID,
		CharacterID: og.CharacterID,
		Timestamp:   now,
		Meta: MetaData{
			KineticID: strconv.FormatInt(og.Loadout[strconv.Itoa(destiny.Kinetic)].ItemHash, 10),
			EnergyID:  strconv.FormatInt(og.Loadout[strconv.Itoa(destiny.Energy)].ItemHash, 10),
			PowerID:   strconv.FormatInt(og.Loadout[strconv.Itoa(destiny.Power)].ItemHash, 10),
		},
	}
	ref := s.DB.Collection(collection).Doc(og.ID).Collection(historyCollection).NewDoc()
	history.ID = ref.ID
	_, err := ref.Set(ctx, history)
	if err != nil {
		return nil, err
	}

	_, err = s.DB.Collection(collection).Doc(og.ID).Set(ctx, map[string]interface{}{
		"updatedAt": now,
	}, firestore.MergeAll)
	if err != nil {
		return nil, err
	}
	return &og.ID, nil
}

func (s *service) GetAllByCharacter(ctx context.Context, userID string, characterID string) ([]api.CharacterSnapshot, error) {
	docs, err := s.DB.Collection(collection).
		Where("userId", "==", userID).
		Where("characterId", "==", characterID).
		OrderBy("createdAt", firestore.Desc).
		Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	snapshots, err := utils.GetAllToStructs[api.CharacterSnapshot](docs)
	if err != nil {
		return nil, err
	}
	return snapshots, nil
}

func optionalGetByHash(db *firestore.Client, ctx context.Context, hash string) (*api.CharacterSnapshot, error) {
	og := api.CharacterSnapshot{}
	docs, err := db.Collection(collection).
		Where("hash", "==", hash).
		Limit(1).
		Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, nil
	}
	err = docs[0].DataTo(&og)
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
	return result, nil
}

func (s *service) GetByIDs(ctx context.Context, snapshotIDs []string) ([]api.CharacterSnapshot, error) {
	data, err := s.DB.Collection(collection).Where("id", "in", snapshotIDs).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[api.CharacterSnapshot](data)
	if err != nil {
		return nil, err
	}
	return results, nil
}
func (s *service) LookupLink(agg *api.Aggregate, characterID string) *api.SnapshotLink {
	if agg == nil {
		return nil
	}
	link, ok := agg.SnapshotLinks[characterID]
	if !ok {
		return nil
	}
	return &link
}

func (s *service) EnrichInstancePerformance(snapshot *api.CharacterSnapshot, performance api.InstancePerformance) (*api.InstancePerformance, error) {
	result := &api.InstancePerformance{
		Extra:       performance.Extra,
		PlayerStats: performance.PlayerStats,
		Weapons:     performance.Weapons,
	}
	if snapshot == nil {
		log.Debug().Msg("No provided snapshot to perform enrichment on")
		return result, nil
	}

	if len(performance.Weapons) == 0 {
		log.Debug().Msg("No metrics provided to enrich")
		return result, nil
	}
	if snapshot.Loadout == nil {
		log.Debug().Msg("No loadout provided to enrich")
		return result, nil
	}

	mapping := map[int64]api.ItemProperties{}
	for _, component := range snapshot.Loadout {
		mapping[component.ItemHash] = component.ItemProperties
	}

	results := make(map[string]api.WeaponInstanceMetrics)
	for _, metric := range performance.Weapons {
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
		results[strconv.FormatInt(*metric.ReferenceID, 10)] = result
	}
	result.Weapons = results
	return result, nil
}

func (s *service) generateSnapshot(ctx context.Context, userID, membershipID, characterID string) (*api.CharacterSnapshot, error) {

	membershipType, err := s.UserService.GetMembershipType(ctx, userID, membershipID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch membership type: %w", err)
	}

	memID, err := strconv.ParseInt(membershipID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid membership id: %w", err)
	}

	loadout, stats, timestamp, err := s.D2Service.GetLoadout(ctx, memID, membershipType, characterID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch profile data: %w", err)
	}
	if timestamp == nil {
		return nil, fmt.Errorf("failed to fetch timestamp for profile data: %w", err)
	}

	return &api.CharacterSnapshot{
		UserID:      userID,
		CharacterID: characterID,
		Stats:       ptr.Of(stats),
		Loadout:     loadout,
	}, nil
}

func (s *service) Save(ctx context.Context, userID, membershipID, characterID string) (*api.CharacterSnapshot, error) {
	data, err := s.generateSnapshot(ctx, userID, membershipID, characterID)
	if err != nil {
		return nil, fmt.Errorf("failed to build data: %w", err)
	}
	if data == nil {
		return nil, fmt.Errorf("failed to generate snapshot")
	}
	_, err = s.create(ctx, userID, *data)
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshot: %w", err)
	}
	return data, nil
}

func (s *service) Merge(ctx context.Context, targetSnapshotID, sourceSnapshotID string) (api.CharacterSnapshot, error) {
	resultSnapshot, err := s.Get(ctx, targetSnapshotID)
	if err != nil {
		return api.CharacterSnapshot{}, err
	}
	sourceSnapshot, err := s.Get(ctx, sourceSnapshotID)
	if err != nil {
		return api.CharacterSnapshot{}, err
	}

	if resultSnapshot == nil || sourceSnapshot == nil {
		return api.CharacterSnapshot{}, fmt.Errorf("snapshot not found")
	}
	if resultSnapshot.CharacterID != sourceSnapshot.CharacterID {
		return api.CharacterSnapshot{}, fmt.Errorf("snapshots must belong to the same character")
	}
	if resultSnapshot.UserID != sourceSnapshot.UserID {
		return api.CharacterSnapshot{}, fmt.Errorf("snapshots must belong to the same user")
	}
	if !snapshotCanMerge(resultSnapshot, sourceSnapshot) {
		return api.CharacterSnapshot{}, fmt.Errorf("snapshots cannot be merged")
	}

	aggs, err := s.aggregateService.BySnapshotID(ctx, sourceSnapshotID, nil)
	if err != nil {
		return api.CharacterSnapshot{}, err
	}
	for _, agg := range aggs {
		log.Info().Msgf("Agg ID: %s\n", agg.ID)
		err := s.aggregateService.Update(ctx, agg.ID, func(data map[string]any) error {
			// Atomically "replace" an element in an array by modifying the slice directly
			// within the update transaction.
			if snapshotIDs, ok := data["snapshotIds"].([]interface{}); ok {
				newSnapshotIDs := make([]interface{}, 0, len(snapshotIDs))
				for _, id := range snapshotIDs {
					if id != sourceSnapshotID {
						newSnapshotIDs = append(newSnapshotIDs, id)
					}
				}
				newSnapshotIDs = append(newSnapshotIDs, targetSnapshotID)
				data["snapshotIds"] = newSnapshotIDs
			}
			snapshotLink := agg.SnapshotLinks[resultSnapshot.CharacterID]
			snapshotLink.SnapshotID = &targetSnapshotID
			snapshotLink.ConfidenceSource = api.UserConfidenceSource
			if snapshotLink.OriginalSnapshotID == nil {
				snapshotLink.OriginalSnapshotID = &sourceSnapshotID
			}
			data["snapshotLinks"].(map[string]any)[resultSnapshot.CharacterID] = snapshotLink
			return nil
		}, true)
		if err != nil {
			return api.CharacterSnapshot{}, err
		}
	}

	return api.CharacterSnapshot{}, nil
}

// snapshotCanMerge will grow to cover a more complex answer on if a snapshot can be merged.
func snapshotCanMerge(a, b *api.CharacterSnapshot) bool {
	kinetic := strconv.Itoa(destiny.Kinetic)
	energy := strconv.Itoa(destiny.Energy)
	if a.Loadout[kinetic].InstanceID != b.Loadout[kinetic].InstanceID {
		return false
	}
	if a.Loadout[energy].InstanceID != b.Loadout[energy].InstanceID {
		return false
	}
	return true
}
