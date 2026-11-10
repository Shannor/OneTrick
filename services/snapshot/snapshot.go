package snapshot

import (
	"context"
	"fmt"
	"oneTrick/api"
	"oneTrick/generator"
	"oneTrick/ptr"
	"oneTrick/services/aggregate"
	"oneTrick/services/destiny"
	"oneTrick/services/user"
	"oneTrick/utils"
	"slices"
	"strconv"
	"time"

	"cloud.google.com/go/firestore"
	clog "github.com/Shannor/canonical-logger"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Service defines the interface for working with character snapshots and aggregates.
type Service interface {

	// Save saves a new snapshot for the specified character for a user. Returns the
	// snapshot data on success and an error if the generating or save to the DB fails
	Save(ctx context.Context, userID, membershipID, characterID string) (*api.CharacterSnapshot, error)

	// GetAllByCharacter retrieves all snapshots for a given user and character with pagination.
	// Snapshots are returned in reverse chronological order based on their timestamp.
	GetAllByCharacter(ctx context.Context, userID string, characterID string, count int, offset int) ([]api.CharacterSnapshot, error)

	// Get retrieves a specific snapshot for a given user, character, and snapshot ID.
	// Takes a context, user ID, character ID, and snapshot ID as input.
	// Returns the requested CharacterSnapshot or an error if the snapshot is not found or cannot be retrieved.
	Get(ctx context.Context, snapshotID string) (*api.CharacterSnapshot, error)

	GetAll(ctx context.Context) ([]api.CharacterSnapshot, error)

	// GetByIDs retrieves multiple snapshots for a given list of snapshot IDs.
	GetByIDs(ctx context.Context, snapshotIDs []string) ([]api.CharacterSnapshot, error)

	// Merge merges two character snapshots identified by snapshotID and targetSnapshotID, storing the result in a new snapshot.
	Merge(ctx context.Context, targetSnapshotID, sourceSnapshotID string) (api.CharacterSnapshot, error)

	LookupLink(agg *api.Aggregate, characterID string) *api.SnapshotLink
	EnrichInstancePerformance(snapshot *api.CharacterSnapshot, performance api.InstancePerformance) (*api.InstancePerformance, error)

	// Update allows for updating a snapshot document's data.
	Update(ctx context.Context, snapshotID string, updateFn func(data map[string]any) error) error

	// Delete deletes a snapshot document, its subcollection history, and cleans up references in aggregates.
	Delete(ctx context.Context, snapshotID string, userID string) error
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

	clog.WithGroupFields(ctx, "snapshotService.create", clog.Fields{
		"userId": userID,
		"hash":   snapshot.Hash,
	})

	existingSnapshot, err := optionalGetByHash(s.DB, ctx, snapshot.Hash)
	if err != nil {
		return nil, err
	}
	if existingSnapshot != nil {
		clog.WithGroupFields(ctx, "snapshotService.create", clog.Fields{
			"snapshotId": existingSnapshot.ID,
			"isExisting": true,
		})
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
	clog.WithGroupFields(ctx, "snapshotService.create", clog.Fields{
		"snapshotId": snapshot.ID,
		"isExisting": false,
	})
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
	clog.WithGroupFields(ctx, "snapshotService.createHistoryEntry", clog.Fields{
		"snapshotId": og.ID,
		"historyId":  history.ID,
	})
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

func (s *service) GetAllByCharacter(ctx context.Context, userID string, characterID string, count int, offset int) ([]api.CharacterSnapshot, error) {
	clog.WithGroupFields(ctx, "snapshotService.getAllByCharacter", clog.Fields{
		"userId":      userID,
		"characterId": characterID,
		"count":       count,
		"offset":      offset,
	})
	query := s.DB.Collection(collection).
		Where("userId", "==", userID).
		Where("characterId", "==", characterID)

	docs, err := query.Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("failed to query snapshots: %w", err)
	}

	snapshots, err := utils.GetAllToStructs[api.CharacterSnapshot](docs)
	if err != nil {
		return nil, fmt.Errorf("failed to parse snapshots: %w", err)
	}

	// Sort by CreatedAt descending (newest first)
	slices.SortFunc(snapshots, func(a, b api.CharacterSnapshot) int {
		return b.CreatedAt.Compare(a.CreatedAt)
	})

	// Apply offset
	if offset > 0 {
		if offset >= len(snapshots) {
			clog.WithGroupField(ctx, "snapshotService.getAllByCharacter", "returnedCount", 0)
			return []api.CharacterSnapshot{}, nil
		}
		snapshots = snapshots[offset:]
	}

	// Apply count limit if specified (count > 0 means limit; count <= 0 means return all)
	if count > 0 && count < len(snapshots) {
		snapshots = snapshots[:count]
	}

	clog.WithGroupField(ctx, "snapshotService.getAllByCharacter", "returnedCount", len(snapshots))
	return snapshots, nil
}

func optionalGetByHash(db *firestore.Client, ctx context.Context, hash string) (*api.CharacterSnapshot, error) {
	clog.WithGroupField(ctx, "snapshotService.getByHash", "hash", hash)
	og := api.CharacterSnapshot{}
	docs, err := db.Collection(collection).
		Where("hash", "==", hash).
		Limit(1).
		Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		clog.WithGroupField(ctx, "snapshotService.getByHash", "found", false)
		return nil, nil
	}
	err = docs[0].DataTo(&og)
	if err != nil {
		return nil, err
	}
	clog.WithGroupFields(ctx, "snapshotService.getByHash", clog.Fields{
		"found":      true,
		"snapshotId": og.ID,
	})
	return &og, nil
}

func (s *service) Get(ctx context.Context, snapshotID string) (*api.CharacterSnapshot, error) {
	clog.WithGroupField(ctx, "snapshotService.get", "snapshotId", snapshotID)
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

func (s *service) GetAll(ctx context.Context) ([]api.CharacterSnapshot, error) {
	data, err := s.DB.Collection(collection).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	results, err := utils.GetAllToStructs[api.CharacterSnapshot](data)
	if err != nil {
		return nil, err
	}
	clog.WithGroupField(ctx, "snapshotService.getAll", "returnedCount", len(results))
	return results, nil
}

func (s *service) GetByIDs(ctx context.Context, snapshotIDs []string) ([]api.CharacterSnapshot, error) {
	clog.WithGroupField(ctx, "snapshotService.getByIds", "requestedCount", len(snapshotIDs))
	uniqueIDs := make([]string, 0, len(snapshotIDs))
	seen := make(map[string]struct{}, len(snapshotIDs))
	for _, id := range snapshotIDs {
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	clog.WithGroupField(ctx, "snapshotService.getByIds", "uniqueCount", len(uniqueIDs))
	if len(uniqueIDs) == 0 {
		clog.WithGroupField(ctx, "snapshotService.getByIds", "returnedCount", 0)
		return []api.CharacterSnapshot{}, nil
	}

	var allResults []api.CharacterSnapshot
	const batchSize = 30
	for i := 0; i < len(uniqueIDs); i += batchSize {
		end := i + batchSize
		if end > len(uniqueIDs) {
			end = len(uniqueIDs)
		}
		batch := uniqueIDs[i:end]

		data, err := s.DB.Collection(collection).Where("id", "in", batch).Documents(ctx).GetAll()
		if err != nil {
			return nil, err
		}
		results, err := utils.GetAllToStructs[api.CharacterSnapshot](data)
		if err != nil {
			return nil, err
		}
		allResults = append(allResults, results...)
	}
	clog.WithGroupField(ctx, "snapshotService.getByIds", "returnedCount", len(allResults))
	return allResults, nil
}

func (s *service) Update(ctx context.Context, snapshotID string, updateFn func(data map[string]any) error) error {
	clog.WithGroupField(ctx, "snapshotService.update", "snapshotId", snapshotID)
	docRef := s.DB.Collection(collection).Doc(snapshotID)
	doc, err := docRef.Get(ctx)
	if err != nil {
		return err
	}

	data := doc.Data()
	if err := updateFn(data); err != nil {
		return err
	}

	_, err = docRef.Set(ctx, data)
	return err
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
	if snapshot == nil || len(performance.Weapons) == 0 || snapshot.Loadout == nil {
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
	clog.WithGroupFields(ctx, "snapshotService.generateSnapshot", clog.Fields{
		"userId":       userID,
		"membershipId": membershipID,
		"characterId":  characterID,
	})

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

	clog.WithGroupField(ctx, "snapshotService.generateSnapshot", "loadoutCount", len(loadout))
	return &api.CharacterSnapshot{
		UserID:      userID,
		CharacterID: characterID,
		Stats:       ptr.Of(stats),
		Loadout:     loadout,
	}, nil
}

func (s *service) Save(ctx context.Context, userID, membershipID, characterID string) (*api.CharacterSnapshot, error) {
	clog.WithGroupFields(ctx, "snapshotService.save", clog.Fields{
		"userId":       userID,
		"membershipId": membershipID,
		"characterId":  characterID,
	})
	data, err := s.generateSnapshot(ctx, userID, membershipID, characterID)
	if err != nil {
		return nil, fmt.Errorf("failed to build data: %w", err)
	}
	if data == nil {
		return nil, fmt.Errorf("failed to generate snapshot")
	}
	id, err := s.create(ctx, userID, *data)
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshot: %w", err)
	}
	if id != nil {
		clog.WithGroupField(ctx, "snapshotService.save", "snapshotId", *id)
	}
	return data, nil
}

func (s *service) Merge(ctx context.Context, targetSnapshotID, sourceSnapshotID string) (api.CharacterSnapshot, error) {
	clog.WithGroupFields(ctx, "snapshotService.merge", clog.Fields{
		"targetSnapshotId": targetSnapshotID,
		"sourceSnapshotId": sourceSnapshotID,
	})
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
	clog.WithGroupFields(ctx, "snapshotService.merge", clog.Fields{
		"targetSnapshotId":  targetSnapshotID,
		"sourceSnapshotId":  sourceSnapshotID,
		"updatedAggregates": len(aggs),
	})
	for _, agg := range aggs {
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

func (s *service) Delete(ctx context.Context, snapshotID string, userID string) error {
	clog.WithGroupFields(ctx, "snapshotService.delete", clog.Fields{
		"snapshotId": snapshotID,
		"userId":     userID,
	})
	docRef := s.DB.Collection(collection).Doc(snapshotID)
	doc, err := docRef.Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			clog.WithGroupField(ctx, "snapshotService.delete", "warning", "snapshot not found for deletion")
			return NotFound
		}
		return fmt.Errorf("failed to get snapshot: %w", err)
	}

	var snap api.CharacterSnapshot
	if err := doc.DataTo(&snap); err != nil {
		return fmt.Errorf("failed to parse snapshot: %w", err)
	}

	if snap.UserID != userID {
		clog.WithGroupFields(ctx, "snapshotService.delete", clog.Fields{
			"snapshotUserId": snap.UserID,
			"warning":        "unauthorized attempt to delete snapshot",
		})
		return Unauthorized
	}

	// Delete subcollection history documents
	historyDocs, err := docRef.Collection(historyCollection).Documents(ctx).GetAll()
	if err != nil {
		clog.WithGroupField(ctx, "snapshotService.delete", "warning", "failed to fetch snapshot history subcollection")
	} else {
		deletedHistoryCount := 0
		failedHistoryCount := 0
		for _, hDoc := range historyDocs {
			if _, err := hDoc.Ref.Delete(ctx); err != nil {
				failedHistoryCount++
			} else {
				deletedHistoryCount++
			}
		}
		clog.WithGroupFields(ctx, "snapshotService.delete", clog.Fields{
			"historyCount":        len(historyDocs),
			"deletedHistoryCount": deletedHistoryCount,
			"failedHistoryCount":  failedHistoryCount,
		})
	}

	// Remove snapshot references from aggregates
	if err := s.aggregateService.RemoveSnapshot(ctx, snapshotID); err != nil {
		return fmt.Errorf("failed to clean up snapshot aggregates: %w", err)
	}

	// Delete snapshot document
	if _, err := docRef.Delete(ctx); err != nil {
		return fmt.Errorf("failed to delete snapshot: %w", err)
	}

	clog.WithGroupField(ctx, "snapshotService.delete", "deleted", true)
	return nil
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
