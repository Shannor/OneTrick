package snapshot

import (
	"cloud.google.com/go/firestore"
	"context"
	"fmt"
	"log/slog"
	"oneTrick/api"
	"oneTrick/generator"
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
	GetByIDs(ctx context.Context, snapshotIDs []string) ([]api.CharacterSnapshot, error)

	FindBestFit(ctx context.Context, userID string, characterID string, activityPeriod time.Time, weapons map[string]api.WeaponInstanceMetrics) (*api.CharacterSnapshot, *api.SnapshotLink, error)
	LookupLink(agg *api.Aggregate, characterID string) *api.SnapshotLink
	EnrichInstancePerformance(snapshot *api.CharacterSnapshot, performance api.InstancePerformance) (*api.InstancePerformance, error)
	GenerateSnapshot(ctx context.Context, userID, membershipID, characterID string) (*api.CharacterSnapshot, error)
}

const (
	collection        = "snapshots"
	historyCollection = "histories"
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
		slog.Debug("No provided snapshot to perform enrichment on")
		return result, nil
	}

	if len(performance.Weapons) == 0 {
		slog.Debug("No metrics provided to enrich")
		return result, nil
	}
	if snapshot.Loadout == nil {
		slog.Debug("No loadout provided to enrich")
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
	return &result, nil
}

func (s *service) FindBestFit(ctx context.Context, userID string, characterID string, activityPeriod time.Time, weapons map[string]api.WeaponInstanceMetrics) (*api.CharacterSnapshot, *api.SnapshotLink, error) {

	minTime := activityPeriod.Add(time.Duration(-12) * time.Hour)
	// A game can last about 8 minutes over the starting time
	maxTime := activityPeriod.Add(time.Duration(10) * time.Minute)
	l := slog.With(
		"activityPeriod", activityPeriod,
		"minTime", minTime,
		"maxTime", maxTime,
		"userId", userID,
		"characterId", characterID,
	)
	docs, err := s.DB.CollectionGroup(historyCollection).
		Where("userId", "==", userID).
		Where("characterId", "==", characterID).
		Where("timestamp", ">=", minTime).
		Where("timestamp", "<=", maxTime).
		OrderBy("timestamp", firestore.Desc).
		Documents(ctx).GetAll()
	if err != nil {
		l.Error("failed to get histories", "error", err.Error())
		return nil, nil, err
	}

	if docs == nil || len(docs) == 0 {
		link := api.SnapshotLink{
			CharacterID:      characterID,
			ConfidenceLevel:  api.NotFoundConfidenceLevel,
			ConfidenceSource: api.SystemConfidenceSource,
			CreatedAt:        time.Now(),
		}
		return nil, &link, nil
	}

	var (
		bestFit      *History
		bestFitScore = 0
	)
	histories, err := utils.GetAllToStructs[History](docs)
	if err != nil {
		l.Error("failed to get all histories", "error", err.Error())
		return nil, nil, err
	}

	weaponSet := make(map[string]bool)
	for _, weapon := range weapons {
		if weapon.ReferenceID != nil {
			weaponSet[strconv.FormatInt(*weapon.ReferenceID, 10)] = true
		}
	}
	for _, h := range histories {
		matches := 0
		if weaponSet[h.Meta.KineticID] {
			matches += 2
		}
		if weaponSet[h.Meta.EnergyID] {
			matches += 2
		}
		if weaponSet[h.Meta.PowerID] {
			matches++
		}

		if bestFit == nil && matches >= 1 {
			bestFit = &h
			bestFitScore = matches
			continue
		}
		if matches > bestFitScore {
			bestFit = &h
			bestFitScore = matches
		}
	}

	if bestFit == nil {
		link := api.SnapshotLink{
			CharacterID:      characterID,
			ConfidenceLevel:  api.NoMatchConfidenceLevel,
			ConfidenceSource: api.SystemConfidenceSource,
			CreatedAt:        time.Now(),
		}
		return nil, &link, nil
	}
	level := api.LowConfidenceLevel
	if bestFitScore >= 4 {
		level = api.HighConfidenceLevel
	} else if bestFitScore >= 2 {
		level = api.MediumConfidenceLevel
	}

	link := api.SnapshotLink{
		CharacterID:      characterID,
		ConfidenceLevel:  level,
		ConfidenceSource: api.SystemConfidenceSource,
		CreatedAt:        time.Now(),
		SnapshotID:       &bestFit.ParentID,
	}

	snap, err := s.Get(ctx, bestFit.ParentID)
	if err != nil {
		l.Error("failed to get snapshot", "error", err.Error())
		return nil, nil, err
	}
	return snap, &link, nil
}
