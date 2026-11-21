package snapshot

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"oneTrick/api"
	"oneTrick/generator"
	"oneTrick/services/aggregate"
	"oneTrick/services/destiny"
	"oneTrick/services/user"
	"oneTrick/utils"
	"slices"
	"strconv"
	"time"

	"cloud.google.com/go/firestore"
	clog "github.com/Shannor/canonical-logger"
)

// Service defines the interface for working with character snapshots and aggregates.
type Service interface {

	// Save saves a new snapshot for the specified character for a user. Returns the
	// snapshot data on success and an error if the generating or save to the DB fails
	Save(ctx context.Context, userID, membershipID, characterID string) (*api.CharacterSnapshot, error)

	// GetAllByCharacter retrieves all active (non-archived) snapshots for a given user and character with pagination.
	// Snapshots are returned in reverse chronological order based on their timestamp.
	GetAllByCharacter(ctx context.Context, userID string, characterID string, count int, offset int) ([]api.CharacterSnapshot, error)

	// Get retrieves a specific snapshot by ID (including soft-archived snapshots).
	Get(ctx context.Context, snapshotID string) (*api.CharacterSnapshot, error)

	GetAll(ctx context.Context) ([]api.CharacterSnapshot, error)

	// GetByIDs retrieves multiple snapshots for a given list of snapshot IDs.
	GetByIDs(ctx context.Context, snapshotIDs []string) ([]api.CharacterSnapshot, error)

	// Merge soft-archives one or more source snapshots into targetSnapshotID after validating ownership
	// and equipment compatibility, reassigning all linked aggregates to targetSnapshotID.
	Merge(ctx context.Context, userID string, targetSnapshotID string, sourceSnapshotIDs []string) (api.CharacterSnapshot, error)

	LookupLink(agg *api.Aggregate, characterID string) *api.SnapshotLink
	EnrichInstancePerformance(snapshot *api.CharacterSnapshot, performance api.InstancePerformance) (*api.InstancePerformance, error)

	// Update allows for updating a snapshot document's data.
	Update(ctx context.Context, snapshotID string, updateFn func(data map[string]any) error) error

	// Delete deletes a snapshot document, its subcollection history, and cleans up references in aggregates and merge links.
	Delete(ctx context.Context, snapshotID string, userID string) error
}

const (
	collection        = "snapshots"
	historyCollection = "histories"
)

type service struct {
	store            snapshotStore
	UserService      user.Service
	D2Service        destiny.Service
	aggregateService aggregate.Service
}

var _ Service = (*service)(nil)

func NewService(db *firestore.Client, userService user.Service, d2Service destiny.Service, aggregateService aggregate.Service) Service {
	return &service{
		store:            &firestoreStore{db: db},
		UserService:      userService,
		D2Service:        d2Service,
		aggregateService: aggregateService,
	}
}

// GenerateHash computes a deterministic SHA-256 hash string for a Loadout based on each item's
// BaseInfo.InstanceId and sorted Perk hashes, matching OneTrick-Scripts/server-tick's generateHash.
func GenerateHash(loadout api.Loadout) (string, error) {
	var hashing []string

	keys := make([]string, 0, len(loadout))
	for k := range loadout {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	for _, key := range keys {
		item := loadout[key]
		hashing = append(hashing, item.ItemProperties.BaseInfo.InstanceId)

		perks := slices.Clone(item.ItemProperties.Perks)
		slices.SortFunc(perks, func(a, b api.Perk) int {
			return cmp.Compare(a.Hash, b.Hash)
		})

		for _, perk := range perks {
			hashing = append(hashing, fmt.Sprintf("%s-%d", item.InstanceID, perk.Hash))
		}
	}
	return utils.HashMap(hashing)
}

// IsArchived reports whether a snapshot has been soft-merged into another snapshot.
func IsArchived(snap *api.CharacterSnapshot) bool {
	return snap != nil && snap.MergedIntoID != nil && *snap.MergedIntoID != ""
}

// EffectiveInstanceID returns the canonical Bungie item instance ID for a CharacterItem.
// Live session snapshots written by server-tick populate the true 64-bit instance ID on
// ItemProperties.BaseInfo.InstanceId while setting top-level InstanceID to the itemHash string,
// whereas direct backend snapshots populate both (or top-level InstanceID).
func EffectiveInstanceID(item api.ItemSnapshot) string {
	hashStr := ""
	if item.ItemHash != 0 {
		hashStr = strconv.FormatInt(item.ItemHash, 10)
	}
	baseID := item.ItemProperties.BaseInfo.InstanceId
	topID := item.InstanceID

	if baseID != "" && baseID != hashStr {
		return baseID
	}
	if topID != "" && topID != hashStr {
		return topID
	}
	if baseID != "" {
		return baseID
	}
	return topID
}

// BaseIdentityKey returns the canonical "{kineticInstanceId}:{energyInstanceId}:{subclassItemHash}"
// grouping key when a snapshot has valid non-empty Kinetic, Energy, and Subclass items.
func BaseIdentityKey(snap *api.CharacterSnapshot) (string, bool) {
	if snap == nil || snap.Loadout == nil {
		return "", false
	}
	kin, ok := snap.Loadout[strconv.Itoa(destiny.Kinetic)]
	if !ok {
		return "", false
	}
	kinID := EffectiveInstanceID(kin)
	if kinID == "" {
		return "", false
	}

	en, ok := snap.Loadout[strconv.Itoa(destiny.Energy)]
	if !ok {
		return "", false
	}
	enID := EffectiveInstanceID(en)
	if enID == "" {
		return "", false
	}

	sub, ok := snap.Loadout[strconv.Itoa(destiny.SubClass)]
	if !ok || sub.ItemHash == 0 {
		return "", false
	}

	return fmt.Sprintf("%s:%s:%d", kinID, enID, sub.ItemHash), true
}

// CanMerge reports whether two snapshots satisfy the minimum base identity required for merging:
// non-empty matching Kinetic InstanceID, non-empty matching Energy InstanceID, and non-zero matching Subclass ItemHash.
func CanMerge(a, b *api.CharacterSnapshot) bool {
	keyA, okA := BaseIdentityKey(a)
	if !okA {
		return false
	}
	keyB, okB := BaseIdentityKey(b)
	return okB && keyA == keyB
}

func snapshotCanMerge(a, b *api.CharacterSnapshot) bool {
	return CanMerge(a, b)
}

func (s *service) create(ctx context.Context, userID string, snapshot api.CharacterSnapshot) (*string, error) {
	if snapshot.Hash == "" {
		hash, err := GenerateHash(snapshot.Loadout)
		if err != nil {
			return nil, err
		}
		snapshot.Hash = hash
	}

	clog.WithGroupFields(ctx, "snapshotService.create", clog.Fields{
		"userId": userID,
		"hash":   snapshot.Hash,
	})

	existingSnapshot, err := s.store.getByHash(ctx, snapshot.Hash)
	if err != nil {
		return nil, err
	}
	if existingSnapshot != nil {
		if IsArchived(existingSnapshot) {
			targetSnap, err := s.resolveAutoForwardTarget(ctx, existingSnapshot)
			if err != nil {
				return nil, err
			}
			if targetSnap != nil {
				clog.WithGroupFields(ctx, "snapshotService.create", clog.Fields{
					"snapshotId":        targetSnap.ID,
					"autoForwardedFrom": existingSnapshot.ID,
					"isExisting":        true,
				})
				return s.createHistoryEntry(ctx, *targetSnap)
			}
		}

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
	created, err := s.store.createSnapshotDoc(ctx, snapshot)
	if err != nil {
		return nil, err
	}
	clog.WithGroupFields(ctx, "snapshotService.create", clog.Fields{
		"snapshotId": created.ID,
		"isExisting": false,
	})
	return s.createHistoryEntry(ctx, created)
}

func (s *service) resolveAutoForwardTarget(ctx context.Context, archived *api.CharacterSnapshot) (*api.CharacterSnapshot, error) {
	if !IsArchived(archived) {
		return nil, nil
	}
	targetID := *archived.MergedIntoID
	targetSnap, err := s.store.getDoc(ctx, targetID)
	if err == nil && targetSnap != nil && IsArchived(targetSnap) {
		// Defensive 1-hop follow in case a merge chain was not yet flattened.
		targetSnap, err = s.store.getDoc(ctx, *targetSnap.MergedIntoID)
	}
	if err != nil {
		if errors.Is(err, NotFound) {
			clog.WithGroupFields(ctx, "snapshotService.create", clog.Fields{
				"archivedSnapshotId": archived.ID,
				"missingTargetId":    targetID,
				"warning":            "auto-forward target missing; restoring archived snapshot",
			})
			_ = s.store.clearMergeArchiveFields(ctx, archived.ID, time.Now())
			archived.MergedIntoID = nil
			archived.MergedAt = nil
			return nil, nil
		}
		return nil, err
	}
	if targetSnap == nil || IsArchived(targetSnap) {
		return nil, nil
	}
	return targetSnap, nil
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
	historyID, err := s.store.createHistory(ctx, history, now)
	if err != nil {
		return nil, err
	}
	clog.WithGroupFields(ctx, "snapshotService.createHistoryEntry", clog.Fields{
		"snapshotId": og.ID,
		"historyId":  historyID,
	})
	return &og.ID, nil
}

func (s *service) GetAllByCharacter(ctx context.Context, userID string, characterID string, count int, offset int) ([]api.CharacterSnapshot, error) {
	clog.WithGroupFields(ctx, "snapshotService.getAllByCharacter", clog.Fields{
		"userId":      userID,
		"characterId": characterID,
		"count":       count,
		"offset":      offset,
	})
	allSnapshots, err := s.store.listByCharacter(ctx, userID, characterID)
	if err != nil {
		return nil, err
	}

	snapshots := make([]api.CharacterSnapshot, 0, len(allSnapshots))
	for _, snap := range allSnapshots {
		if !IsArchived(&snap) {
			snapshots = append(snapshots, snap)
		}
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

func (s *service) Get(ctx context.Context, snapshotID string) (*api.CharacterSnapshot, error) {
	clog.WithGroupField(ctx, "snapshotService.get", "snapshotId", snapshotID)
	return s.store.getDoc(ctx, snapshotID)
}

func (s *service) GetAll(ctx context.Context) ([]api.CharacterSnapshot, error) {
	results, err := s.store.listAll(ctx)
	if err != nil {
		return nil, err
	}
	clog.WithGroupField(ctx, "snapshotService.getAll", "returnedCount", len(results))
	return results, nil
}

func (s *service) GetByIDs(ctx context.Context, snapshotIDs []string) ([]api.CharacterSnapshot, error) {
	clog.WithGroupField(ctx, "snapshotService.getByIds", "requestedCount", len(snapshotIDs))
	uniqueIDs := deduplicateNonEmpty(snapshotIDs)
	clog.WithGroupField(ctx, "snapshotService.getByIds", "uniqueCount", len(uniqueIDs))
	if len(uniqueIDs) == 0 {
		clog.WithGroupField(ctx, "snapshotService.getByIds", "returnedCount", 0)
		return []api.CharacterSnapshot{}, nil
	}

	allResults, err := s.store.listByIDs(ctx, uniqueIDs)
	if err != nil {
		return nil, err
	}
	clog.WithGroupField(ctx, "snapshotService.getByIds", "returnedCount", len(allResults))
	return allResults, nil
}

func (s *service) Update(ctx context.Context, snapshotID string, updateFn func(data map[string]any) error) error {
	clog.WithGroupField(ctx, "snapshotService.update", "snapshotId", snapshotID)
	data, err := s.store.getRawDoc(ctx, snapshotID)
	if err != nil {
		return err
	}
	if err := updateFn(data); err != nil {
		return err
	}
	return s.store.setRawDoc(ctx, snapshotID, data)
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
		itemMetric := api.WeaponInstanceMetrics{}
		if metric.ReferenceID == nil {
			continue
		}
		itemMetric.ReferenceID = metric.ReferenceID
		itemMetric.Stats = metric.Stats

		properties, ok := mapping[*metric.ReferenceID]
		if ok {
			itemMetric.ItemProperties = &properties
		}
		results[strconv.FormatInt(*metric.ReferenceID, 10)] = itemMetric
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

	loadout, stats, armorSetBonuses, timestamp, err := s.D2Service.GetLoadout(ctx, memID, membershipType, characterID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch profile data: %w", err)
	}
	if timestamp == nil {
		return nil, fmt.Errorf("failed to fetch timestamp for profile data: %w", err)
	}

	var bonusesPtr *[]api.ArmorSetBonus
	if len(armorSetBonuses) > 0 {
		bonusesPtr = &armorSetBonuses
	}

	clog.WithGroupField(ctx, "snapshotService.generateSnapshot", "loadoutCount", len(loadout))
	return &api.CharacterSnapshot{
		UserID:          userID,
		CharacterID:     characterID,
		Stats:           new(stats),
		Loadout:         loadout,
		ArmorSetBonuses: bonusesPtr,
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
		saved, getErr := s.Get(ctx, *id)
		if getErr == nil && saved != nil {
			return saved, nil
		}
		data.ID = *id
	}
	return data, nil
}

func (s *service) Merge(ctx context.Context, userID string, targetSnapshotID string, sourceSnapshotIDs []string) (api.CharacterSnapshot, error) {
	if userID == "" {
		return api.CharacterSnapshot{}, Unauthorized
	}
	if targetSnapshotID == "" {
		return api.CharacterSnapshot{}, fmt.Errorf("%w: target snapshot ID cannot be empty", InvalidMerge)
	}
	uniqueSources := deduplicateNonEmpty(sourceSnapshotIDs)
	if len(uniqueSources) == 0 {
		return api.CharacterSnapshot{}, fmt.Errorf("%w: at least one source snapshot ID is required", InvalidMerge)
	}
	if slices.Contains(uniqueSources, targetSnapshotID) {
		return api.CharacterSnapshot{}, fmt.Errorf("%w: cannot merge snapshot with itself", InvalidMerge)
	}

	clog.WithGroupFields(ctx, "snapshotService.merge", clog.Fields{
		"userId":            userID,
		"targetSnapshotId":  targetSnapshotID,
		"sourceSnapshotIds": uniqueSources,
	})

	targetSnapshot, err := s.Get(ctx, targetSnapshotID)
	if err != nil {
		return api.CharacterSnapshot{}, err
	}
	if targetSnapshot == nil {
		return api.CharacterSnapshot{}, NotFound
	}
	if targetSnapshot.UserID != userID {
		return api.CharacterSnapshot{}, Unauthorized
	}
	if IsArchived(targetSnapshot) {
		return api.CharacterSnapshot{}, fmt.Errorf("%w: target snapshot is already merged", InvalidMerge)
	}

	// Validate every source snapshot up-front before writing any aggregate or snapshot changes.
	sourceSnapshots := make([]*api.CharacterSnapshot, 0, len(uniqueSources))
	for _, srcID := range uniqueSources {
		src, err := s.Get(ctx, srcID)
		if err != nil {
			return api.CharacterSnapshot{}, err
		}
		if src == nil {
			return api.CharacterSnapshot{}, NotFound
		}
		if src.UserID != userID {
			return api.CharacterSnapshot{}, Unauthorized
		}
		if src.CharacterID != targetSnapshot.CharacterID {
			return api.CharacterSnapshot{}, fmt.Errorf("%w: snapshots must belong to the same character", InvalidMerge)
		}
		if IsArchived(src) {
			return api.CharacterSnapshot{}, fmt.Errorf("%w: source snapshot %s is already merged", InvalidMerge, src.ID)
		}
		if !snapshotCanMerge(targetSnapshot, src) {
			return api.CharacterSnapshot{}, fmt.Errorf("%w: snapshots do not share matching Kinetic, Energy, and Subclass items", InvalidMerge)
		}
		sourceSnapshots = append(sourceSnapshots, src)
	}

	// Reassign aggregates from each source snapshot to targetSnapshotID.
	totalUpdatedAggregates := 0
	for _, src := range sourceSnapshots {
		srcID := src.ID
		aggs, err := s.aggregateService.BySnapshotID(ctx, srcID, nil)
		if err != nil {
			return api.CharacterSnapshot{}, err
		}
		for _, agg := range aggs {
			existingLink := agg.SnapshotLinks[targetSnapshot.CharacterID]
			err := s.aggregateService.Update(ctx, agg.ID, func(data map[string]any) error {
				replaceSnapshotIDInAggregateData(data, targetSnapshot.CharacterID, srcID, targetSnapshotID, existingLink)
				return nil
			}, true)
			if err != nil {
				return api.CharacterSnapshot{}, err
			}
			totalUpdatedAggregates++
		}
	}

	// Soft-archive each source, flatten any 1-hop child merges, and record mergedFromIds on target.
	now := time.Now()
	var mergedFromSeed []string
	if targetSnapshot.MergedFromIDs != nil {
		mergedFromSeed = append(mergedFromSeed, *targetSnapshot.MergedFromIDs...)
	}
	for _, src := range sourceSnapshots {
		mergedFromSeed = append(mergedFromSeed, src.ID)
		if err := s.store.archiveSource(ctx, src.ID, targetSnapshotID, now); err != nil {
			return api.CharacterSnapshot{}, err
		}
		if src.MergedFromIDs != nil {
			for _, childID := range *src.MergedFromIDs {
				if childID == "" || childID == targetSnapshotID {
					continue
				}
				mergedFromSeed = append(mergedFromSeed, childID)
				if err := s.store.repointArchivedChild(ctx, childID, targetSnapshotID, now); err != nil {
					return api.CharacterSnapshot{}, err
				}
			}
		}
	}

	mergedFromIDs := deduplicateNonEmpty(mergedFromSeed)
	if err := s.store.updateMergedFromIDs(ctx, targetSnapshotID, mergedFromIDs, now); err != nil {
		return api.CharacterSnapshot{}, err
	}

	clog.WithGroupFields(ctx, "snapshotService.merge", clog.Fields{
		"targetSnapshotId":  targetSnapshotID,
		"mergedSources":     len(sourceSnapshots),
		"updatedAggregates": totalUpdatedAggregates,
	})

	targetSnapshot.MergedFromIDs = &mergedFromIDs
	targetSnapshot.UpdatedAt = now
	return *targetSnapshot, nil
}

func replaceSnapshotIDInAggregateData(
	data map[string]any,
	characterID string,
	sourceSnapshotID string,
	targetSnapshotID string,
	existingLink api.SnapshotLink,
) {
	switch rawIDs := data["snapshotIds"].(type) {
	case []any:
		dedup := make([]any, 0, len(rawIDs)+1)
		seenTarget := false
		for _, raw := range rawIDs {
			s, ok := raw.(string)
			if !ok {
				dedup = append(dedup, raw)
				continue
			}
			if s == sourceSnapshotID {
				continue
			}
			if s == targetSnapshotID {
				if seenTarget {
					continue
				}
				seenTarget = true
			}
			dedup = append(dedup, s)
		}
		if !seenTarget {
			dedup = append(dedup, targetSnapshotID)
		}
		data["snapshotIds"] = dedup
	case []string:
		dedup := make([]string, 0, len(rawIDs)+1)
		seenTarget := false
		for _, s := range rawIDs {
			if s == sourceSnapshotID {
				continue
			}
			if s == targetSnapshotID {
				if seenTarget {
					continue
				}
				seenTarget = true
			}
			dedup = append(dedup, s)
		}
		if !seenTarget {
			dedup = append(dedup, targetSnapshotID)
		}
		data["snapshotIds"] = dedup
	default:
		data["snapshotIds"] = []any{targetSnapshotID}
	}

	if existingLink.CharacterID == "" {
		existingLink.CharacterID = characterID
	}
	existingLink.SnapshotID = &targetSnapshotID
	existingLink.ConfidenceSource = api.UserConfidenceSource
	if existingLink.OriginalSnapshotID == nil || *existingLink.OriginalSnapshotID == "" {
		existingLink.OriginalSnapshotID = &sourceSnapshotID
	}

	linksMap, ok := data["snapshotLinks"].(map[string]any)
	if !ok || linksMap == nil {
		linksMap = make(map[string]any)
	}
	linksMap[characterID] = existingLink
	data["snapshotLinks"] = linksMap
}

func deduplicateNonEmpty(ids []string) []string {
	unique := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	return unique
}

func (s *service) Delete(ctx context.Context, snapshotID string, userID string) error {
	clog.WithGroupFields(ctx, "snapshotService.delete", clog.Fields{
		"snapshotId": snapshotID,
		"userId":     userID,
	})
	snap, err := s.store.getDoc(ctx, snapshotID)
	if err != nil {
		if errors.Is(err, NotFound) {
			clog.WithGroupField(ctx, "snapshotService.delete", "warning", "snapshot not found for deletion")
			return NotFound
		}
		return fmt.Errorf("failed to get snapshot: %w", err)
	}
	if snap == nil {
		return NotFound
	}

	if snap.UserID != userID {
		clog.WithGroupFields(ctx, "snapshotService.delete", clog.Fields{
			"snapshotUserId": snap.UserID,
			"warning":        "unauthorized attempt to delete snapshot",
		})
		return Unauthorized
	}

	// If deleting a target snapshot that has archived sources, clear mergedIntoId/mergedAt on those sources
	// so no dangling auto-forward pointers remain.
	if snap.MergedFromIDs != nil {
		now := time.Now()
		for _, srcID := range *snap.MergedFromIDs {
			if srcID == "" {
				continue
			}
			_ = s.store.clearMergeArchiveFields(ctx, srcID, now)
		}
	}

	// If deleting an archived source snapshot directly, remove its ID from its parent target's mergedFromIds.
	if IsArchived(snap) {
		_ = s.store.removeMergedFromID(ctx, *snap.MergedIntoID, snapshotID)
	}

	// Remove snapshot references from aggregates
	if err := s.aggregateService.RemoveSnapshot(ctx, snapshotID); err != nil {
		return fmt.Errorf("failed to clean up snapshot aggregates: %w", err)
	}

	if err := s.store.deleteSnapshotAndHistory(ctx, snapshotID); err != nil {
		return err
	}

	clog.WithGroupField(ctx, "snapshotService.delete", "deleted", true)
	return nil
}
