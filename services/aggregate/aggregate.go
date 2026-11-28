package aggregate

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"oneTrick/adapters/driven/firestore/fsutil"
	"oneTrick/api"
	"oneTrick/set"
	"oneTrick/utils"
	"slices"
	"sort"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	clog "github.com/Shannor/canonical-logger"
	"google.golang.org/api/iterator"
)

var NotFound = errors.New("not found")

// Service defines the interface for working with character snapshots and aggregates.
type Service interface {
	// GetAggregate retrieves an existing aggregate for a given activity ID.
	GetAggregate(ctx context.Context, activityID string) (*api.Aggregate, error)

	// GetAggregates retrieves a list of aggregates for the given session IDs.
	// Limited to max of 30 documents at once
	GetAggregates(ctx context.Context, IDs []string) ([]api.Aggregate, error)

	BySnapshotID(ctx context.Context, snapshotID string, gameMode *api.GameMode) ([]api.Aggregate, error)

	UpdateAllAggregates(ctx context.Context) (int, error)

	// GetAggregatesByActivity retrieves a list of aggregates for the given activity IDs.
	// Limited to a max of 30 documents at once
	GetAggregatesByActivity(ctx context.Context, activityIDs []string) ([]api.Aggregate, error)

	// Update allows for updating an aggregate document's data.
	Update(ctx context.Context, aggregateID string, updateFn func(data map[string]interface{}) error, shouldMerge bool) error

	// UpsertSessionActivity creates a new aggregate document for an activity or merges a character's
	// snapshot link, performance, and array memberships into an existing aggregate for the same activityId.
	UpsertSessionActivity(
		ctx context.Context,
		characterID string,
		history api.ActivityHistory,
		snapshotLink api.SnapshotLink,
		performance api.InstancePerformance,
	) (*api.Aggregate, error)

	// RemoveSession removes all references to sessionID from aggregates, deletes orphaned aggregates,
	// and returns deduplicated surviving sibling session IDs from modified aggregates.
	RemoveSession(ctx context.Context, sessionID string) ([]string, error)

	// GetAllAggregates returns all aggregate documents. Used for admin backfills.
	GetAllAggregates(ctx context.Context) ([]api.Aggregate, error)

	// StampSnapshotLinkParticipants patches participant metadata fields on existing SnapshotLinks
	// without overwriting unrelated snapshot or performance fields.
	StampSnapshotLinkParticipants(ctx context.Context, aggregateID string, participants map[string]SnapshotLinkParticipant) error

	// RemoveSnapshot removes all references to snapshotID from aggregates and deletes orphaned aggregates.
	RemoveSnapshot(ctx context.Context, snapshotID string) error
}

const (
	collection = "aggregates"
)

type service struct {
	DB            *firestore.Client
	store         aggregateStore
	activityLocks [64]sync.Mutex
}

var _ Service = (*service)(nil)

func NewService(db *firestore.Client) Service {
	return &service{
		DB:    db,
		store: &firestoreAggregateStore{db: db},
	}
}

func (s *service) lockActivity(activityID string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(activityID))
	mu := &s.activityLocks[h.Sum32()%uint32(len(s.activityLocks))]
	mu.Lock()
	return mu
}

func (s *service) UpsertSessionActivity(
	ctx context.Context,
	characterID string,
	history api.ActivityHistory,
	snapshotLink api.SnapshotLink,
	performance api.InstancePerformance,
) (*api.Aggregate, error) {
	if history.InstanceID != "" {
		mu := s.lockActivity(history.InstanceID)
		defer mu.Unlock()
	}

	now := time.Now()
	sessionIDs := make([]string, 0, 1)
	snapshotIDs := make([]string, 0, 1)
	characterIDs := make([]string, 0, 1)

	if snapshotLink.SessionID != nil && *snapshotLink.SessionID != "" {
		sessionIDs = append(sessionIDs, *snapshotLink.SessionID)
	}
	if snapshotLink.SnapshotID != nil && *snapshotLink.SnapshotID != "" {
		snapshotIDs = append(snapshotIDs, *snapshotLink.SnapshotID)
	}
	if characterID != "" {
		characterIDs = append(characterIDs, characterID)
	}

	existing, err := s.store.getByActivityID(ctx, history.InstanceID)
	if err != nil && !errors.Is(err, NotFound) {
		return nil, err
	}

	if existing != nil {
		var backfillMode *api.GameMode
		if history.EstimatedGameMode != nil && existing.ActivityDetails.EstimatedGameMode == nil {
			backfillMode = history.EstimatedGameMode
		}

		if err := s.store.mergeSessionActivity(
			ctx,
			existing.ID,
			characterID,
			snapshotLink,
			performance,
			sessionIDs,
			snapshotIDs,
			characterIDs,
			backfillMode,
		); err != nil {
			return nil, err
		}

		if existing.SnapshotLinks == nil {
			existing.SnapshotLinks = make(map[string]api.SnapshotLink)
		}
		if existing.Performance == nil {
			existing.Performance = make(map[string]api.InstancePerformance)
		}
		existing.SnapshotLinks[characterID] = snapshotLink
		existing.Performance[characterID] = performance
		existing.SessionIds = utils.UnionNonZero(existing.SessionIds, sessionIDs)
		existing.SnapshotIds = utils.UnionNonZero(existing.SnapshotIds, snapshotIDs)
		existing.CharacterIds = utils.UnionNonZero(existing.CharacterIds, characterIDs)
		if backfillMode != nil {
			existing.ActivityDetails.EstimatedGameMode = backfillMode
		}

		clog.WithGroupFields(ctx, "aggregateService.upsertSessionActivity", clog.Fields{
			"aggregateId":     existing.ID,
			"activityId":      history.InstanceID,
			"updatedExisting": true,
		})
		return existing, nil
	}

	agg := api.Aggregate{
		ActivityID:      history.InstanceID,
		ActivityDetails: history,
		SnapshotLinks: map[string]api.SnapshotLink{
			characterID: snapshotLink,
		},
		Performance: map[string]api.InstancePerformance{
			characterID: performance,
		},
		SessionIds:   sessionIDs,
		SnapshotIds:  snapshotIDs,
		CharacterIds: characterIDs,
		CreatedAt:    now,
	}

	created, err := s.store.createAggregate(ctx, agg)
	if err != nil {
		return nil, err
	}

	clog.WithGroupFields(ctx, "aggregateService.upsertSessionActivity", clog.Fields{
		"aggregateId":     created.ID,
		"activityId":      history.InstanceID,
		"updatedExisting": false,
	})
	return &created, nil
}

func (s *service) Update(ctx context.Context, aggregateID string, updateFn func(data map[string]any) error, shouldMerge bool) error {
	docRef := s.DB.Collection(collection).Doc(aggregateID)
	doc, err := docRef.Get(ctx)
	if err != nil {
		return err
	}

	data := doc.Data()
	if err := updateFn(data); err != nil {
		return err
	}

	clog.WithGroupFields(ctx, "aggregateService.update", clog.Fields{
		"aggregateId": aggregateID,
		"shouldMerge": shouldMerge,
	})
	if shouldMerge {
		_, err = docRef.Set(ctx, data, firestore.MergeAll)
		if err != nil {
			return fmt.Errorf("failed to merge aggregate: %w", err)
		}
		return nil
	}
	_, err = docRef.Set(ctx, data)
	if err != nil {
		return fmt.Errorf("failed to save aggregate: %w", err)
	}
	return nil
}

func (s *service) BySnapshotID(ctx context.Context, snapshotID string, gameMode *api.GameMode) ([]api.Aggregate, error) {
	if snapshotID == "" {
		return nil, fmt.Errorf("snapshotID is required")
	}
	clog.WithGroupField(ctx, "aggregateService.bySnapshotId", "snapshotId", snapshotID)

	q := s.DB.Collection(collection).
		Where("snapshotIds", "array-contains", snapshotID)

	if gameMode != nil && *gameMode != "" && *gameMode != api.GameModeAll {
		q = q.Where("activityHistory.estimatedGameMode", "==", string(*gameMode))
		clog.WithGroupField(ctx, "aggregateService.bySnapshotId", "gameMode", string(*gameMode))
	}

	docs, err := q.Documents(ctx).GetAll()

	if err != nil {
		return nil, err
	}
	results, err := fsutil.GetAllToStructs[api.Aggregate](docs)
	if err != nil {
		return nil, err
	}
	clog.WithGroupField(ctx, "aggregateService.bySnapshotId", "returnedCount", len(results))
	return results, nil
}

func (s *service) GetAggregate(ctx context.Context, activityID string) (*api.Aggregate, error) {
	clog.WithGroupField(ctx, "aggregateService.getAggregate", "activityId", activityID)
	iter := s.DB.
		Collection(collection).
		Where("activityId", "==", activityID).
		Limit(1).
		Documents(ctx)
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		agg := api.Aggregate{}
		err = doc.DataTo(&agg)
		if err != nil {
			return nil, err
		}
		clog.WithGroupField(ctx, "aggregateService.getAggregate", "aggregateId", agg.ID)
		return &agg, nil
	}
	return nil, NotFound
}

func (s *service) GetAggregatesByActivity(ctx context.Context, activityIDs []string) ([]api.Aggregate, error) {
	clog.WithGroupField(ctx, "aggregateService.getAggregatesByActivity", "requestedCount", len(activityIDs))
	if len(activityIDs) == 0 {
		clog.WithGroupField(ctx, "aggregateService.getAggregatesByActivity", "returnedCount", 0)
		return []api.Aggregate{}, nil
	}

	var allResults []api.Aggregate
	const batchSize = 30
	for i := 0; i < len(activityIDs); i += batchSize {
		end := i + batchSize
		if end > len(activityIDs) {
			end = len(activityIDs)
		}
		batch := activityIDs[i:end]

		docs, err := s.DB.
			Collection(collection).
			Where("activityId", "in", batch).
			Documents(ctx).GetAll()
		if err != nil {
			return nil, err
		}
		results, err := fsutil.GetAllToStructs[api.Aggregate](docs)
		if err != nil {
			return nil, err
		}
		allResults = append(allResults, results...)
	}
	clog.WithGroupField(ctx, "aggregateService.getAggregatesByActivity", "returnedCount", len(allResults))
	return allResults, nil
}

func (s *service) GetAggregates(ctx context.Context, IDs []string) ([]api.Aggregate, error) {
	clog.WithGroupField(ctx, "aggregateService.getAggregates", "requestedCount", len(IDs))
	if len(IDs) == 0 {
		clog.WithGroupField(ctx, "aggregateService.getAggregates", "returnedCount", 0)
		return []api.Aggregate{}, nil
	}

	var allResults []api.Aggregate
	batchSize := 30

	// Process IDs in batches of 30
	for i := 0; i < len(IDs); i += batchSize {
		end := i + batchSize
		if end > len(IDs) {
			end = len(IDs)
		}
		batch := IDs[i:end]

		docs, err := s.DB.
			Collection(collection).
			Where("id", "in", batch).
			Documents(ctx).GetAll()
		if err != nil {
			return nil, err
		}

		results, err := fsutil.GetAllToStructs[api.Aggregate](docs)
		if err != nil {
			return nil, err
		}

		allResults = append(allResults, results...)
	}
	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].CreatedAt.Before(allResults[j].CreatedAt)
	})
	clog.WithGroupField(ctx, "aggregateService.getAggregates", "returnedCount", len(allResults))
	return allResults, nil
}

func (s *service) GetAllAggregates(ctx context.Context) ([]api.Aggregate, error) {
	results, err := s.store.listAll(ctx)
	if err != nil {
		return nil, err
	}
	clog.WithGroupField(ctx, "aggregateService.getAllAggregates", "returnedCount", len(results))
	return results, nil
}

func (s *service) StampSnapshotLinkParticipants(ctx context.Context, aggregateID string, participants map[string]SnapshotLinkParticipant) error {
	if aggregateID == "" || len(participants) == 0 {
		return nil
	}
	if err := s.store.stampSnapshotLinkParticipants(ctx, aggregateID, participants); err != nil {
		return fmt.Errorf("failed to stamp snapshot link participants on aggregate %s: %w", aggregateID, err)
	}
	clog.WithGroupFields(ctx, "aggregateService.stampSnapshotLinkParticipants", clog.Fields{
		"aggregateId":      aggregateID,
		"participantCount": len(participants),
	})
	return nil
}

func (s *service) UpdateAllAggregates(ctx context.Context) (int, error) {
	aggregates, err := s.GetAllAggregates(ctx)
	if err != nil {
		return 0, err
	}
	count := 0
	failedCount := 0
	for _, agg := range aggregates {
		snapshotIDs := make([]string, 0)
		sessionIDs := make([]string, 0)
		characterIDs := make([]string, 0)
		if len(agg.SnapshotLinks) > 0 {
			for _, link := range agg.SnapshotLinks {
				if link.SnapshotID != nil {
					snapshotIDs = append(snapshotIDs, *link.SnapshotID)
				}
				if link.SessionID != nil {
					sessionIDs = append(sessionIDs, *link.SessionID)
				}
				characterIDs = append(characterIDs, link.CharacterID)
			}
			_, err := s.DB.Collection(collection).Doc(agg.ID).Set(ctx, map[string]any{
				"sessionIds":   firestore.ArrayUnion(toInterfaceSlice(sessionIDs)...),
				"snapshotIds":  firestore.ArrayUnion(toInterfaceSlice(snapshotIDs)...),
				"characterIds": firestore.ArrayUnion(toInterfaceSlice(characterIDs)...),
			}, firestore.MergeAll)
			if err != nil {
				failedCount++
			}
			count++
		}
	}
	clog.WithGroupFields(ctx, "aggregateService.updateAllAggregates", clog.Fields{
		"totalAggregates":   len(aggregates),
		"updatedAggregates": count,
		"failedAggregates":  failedCount,
	})
	if failedCount > 0 {
		clog.WithGroupField(ctx, "aggregateService.updateAllAggregates", "warning", "some aggregates failed to update")
	}
	return count, nil
}

func (s *service) RemoveSession(ctx context.Context, sessionID string) ([]string, error) {
	if sessionID == "" {
		return nil, nil
	}

	clog.WithGroupField(ctx, "aggregateService.removeSession", "sessionId", sessionID)
	aggs, err := s.store.listBySessionID(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to query aggregates for session %s: %w", sessionID, err)
	}

	deletedOrphans := 0
	updatedCount := 0
	survivingSiblings := set.NewOrdered[string]()
	for _, agg := range aggs {
		plan := stripSessionFromAggregate(agg, sessionID)
		if isTypedAggregateOrphaned(plan.cleaned) {
			if err := s.store.deleteAggregate(ctx, agg.ID); err != nil {
				return nil, fmt.Errorf("failed to delete orphaned aggregate %s: %w", agg.ID, err)
			}
			deletedOrphans++
			continue
		}
		if err := s.store.detachSession(ctx, agg.ID, sessionID, plan.clearSessionCharIDs, plan.removeCharIDs); err != nil {
			return nil, fmt.Errorf("failed to update aggregate %s: %w", agg.ID, err)
		}
		updatedCount++
		for _, siblingID := range plan.survivingSessionIDs {
			survivingSiblings.Add(siblingID)
		}
	}

	siblings := survivingSiblings.ToSlice()
	clog.WithGroupFields(ctx, "aggregateService.removeSession", clog.Fields{
		"affectedAggregates":       len(aggs),
		"deletedOrphans":           deletedOrphans,
		"updatedAggregates":        updatedCount,
		"survivingSiblingSessions": len(siblings),
	})
	return siblings, nil
}

type sessionDetachPlan struct {
	cleaned             api.Aggregate
	clearSessionCharIDs []string
	removeCharIDs       []string
	survivingSessionIDs []string
}

func stripSessionFromAggregate(agg api.Aggregate, sessionID string) sessionDetachPlan {
	cleaned := agg
	remainingSessionIDs := make([]string, 0, len(agg.SessionIds))
	surviving := set.NewOrdered[string]()
	for _, id := range agg.SessionIds {
		if id == "" || id == sessionID {
			continue
		}
		remainingSessionIDs = append(remainingSessionIDs, id)
		surviving.Add(id)
	}
	cleaned.SessionIds = remainingSessionIDs

	var clearSessionCharIDs []string
	var removeCharIDs []string
	removedSet := set.New[string]()
	if len(agg.SnapshotLinks) > 0 {
		cleanedLinks := make(map[string]api.SnapshotLink, len(agg.SnapshotLinks))
		extraLinkSessions := make([]string, 0)
		for charID, link := range agg.SnapshotLinks {
			if link.SessionID != nil && *link.SessionID == sessionID {
				if link.SnapshotID != nil && *link.SnapshotID != "" {
					link.SessionID = nil
					cleanedLinks[charID] = link
					clearSessionCharIDs = append(clearSessionCharIDs, charID)
				} else {
					removeCharIDs = append(removeCharIDs, charID)
					removedSet.Add(charID)
				}
				continue
			}
			if link.SessionID != nil && *link.SessionID != "" {
				extraLinkSessions = append(extraLinkSessions, *link.SessionID)
			}
			cleanedLinks[charID] = link
		}
		cleaned.SnapshotLinks = cleanedLinks
		slices.Sort(clearSessionCharIDs)
		slices.Sort(removeCharIDs)
		slices.Sort(extraLinkSessions)
		for _, sid := range extraLinkSessions {
			surviving.Add(sid)
		}
	}

	if removedSet.Size() > 0 {
		if len(agg.CharacterIds) > 0 {
			remainingCharIDs := make([]string, 0, len(agg.CharacterIds))
			for _, cid := range agg.CharacterIds {
				if !removedSet.Contains(cid) {
					remainingCharIDs = append(remainingCharIDs, cid)
				}
			}
			cleaned.CharacterIds = remainingCharIDs
		}
		if len(agg.Performance) > 0 {
			cleanedPerf := make(map[string]api.InstancePerformance, len(agg.Performance))
			for cid, perf := range agg.Performance {
				if !removedSet.Contains(cid) {
					cleanedPerf[cid] = perf
				}
			}
			cleaned.Performance = cleanedPerf
		}
	}

	return sessionDetachPlan{
		cleaned:             cleaned,
		clearSessionCharIDs: clearSessionCharIDs,
		removeCharIDs:       removeCharIDs,
		survivingSessionIDs: surviving.ToSlice(),
	}
}

func isTypedAggregateOrphaned(agg api.Aggregate) bool {
	if len(agg.SessionIds) > 0 || len(agg.SnapshotIds) > 0 {
		return false
	}
	for _, link := range agg.SnapshotLinks {
		if (link.SnapshotID != nil && *link.SnapshotID != "") ||
			(link.SessionID != nil && *link.SessionID != "") {
			return false
		}
	}
	return true
}

func (s *service) RemoveSnapshot(ctx context.Context, snapshotID string) error {
	if snapshotID == "" {
		return nil
	}

	clog.WithGroupField(ctx, "aggregateService.removeSnapshot", "snapshotId", snapshotID)
	docs, err := s.DB.Collection(collection).
		Where("snapshotIds", "array-contains", snapshotID).
		Documents(ctx).GetAll()
	if err != nil {
		return fmt.Errorf("failed to query aggregates for snapshot %s: %w", snapshotID, err)
	}

	deletedOrphans := 0
	updatedCount := 0
	for _, doc := range docs {
		docRef := doc.Ref
		data := doc.Data()

		// Remove snapshotID from snapshotIds array
		if snapshotIDs, ok := data["snapshotIds"].([]any); ok {
			newSnapshotIDs := make([]any, 0, len(snapshotIDs))
			for _, id := range snapshotIDs {
				if idStr, ok := id.(string); ok && idStr == snapshotID {
					continue
				}
				newSnapshotIDs = append(newSnapshotIDs, id)
			}
			data["snapshotIds"] = newSnapshotIDs
		}

		// Remove snapshotId and originalSnapshotId from snapshotLinks
		if linksMap, ok := data["snapshotLinks"].(map[string]any); ok {
			for charID, linkVal := range linksMap {
				if linkObj, ok := linkVal.(map[string]any); ok {
					modified := false
					if snapID, ok := linkObj["snapshotId"].(string); ok && snapID == snapshotID {
						delete(linkObj, "snapshotId")
						modified = true
					}
					if origSnapID, ok := linkObj["originalSnapshotId"].(string); ok && origSnapID == snapshotID {
						delete(linkObj, "originalSnapshotId")
						modified = true
					}
					if modified {
						linksMap[charID] = linkObj
					}
				}
			}
			data["snapshotLinks"] = linksMap
		}

		if isAggregateOrphaned(data) {
			if _, err := docRef.Delete(ctx); err != nil {
				return fmt.Errorf("failed to delete orphaned aggregate %s: %w", docRef.ID, err)
			}
			deletedOrphans++
		} else {
			if _, err := docRef.Set(ctx, data); err != nil {
				return fmt.Errorf("failed to update aggregate %s: %w", docRef.ID, err)
			}
			updatedCount++
		}
	}
	clog.WithGroupFields(ctx, "aggregateService.removeSnapshot", clog.Fields{
		"affectedAggregates": len(docs),
		"deletedOrphans":     deletedOrphans,
		"updatedAggregates":  updatedCount,
	})
	return nil
}

func isAggregateOrphaned(data map[string]any) bool {
	// Check sessionIds
	if sessionIDs, ok := data["sessionIds"].([]any); ok && len(sessionIDs) > 0 {
		return false
	}

	// Check snapshotIds
	if snapshotIDs, ok := data["snapshotIds"].([]any); ok && len(snapshotIDs) > 0 {
		return false
	}

	// Check snapshotLinks map
	if linksMap, ok := data["snapshotLinks"].(map[string]any); ok && len(linksMap) > 0 {
		for _, linkVal := range linksMap {
			if linkObj, ok := linkVal.(map[string]any); ok {
				if sid, ok := linkObj["snapshotId"].(string); ok && sid != "" {
					return false
				}
				if sessID, ok := linkObj["sessionId"].(string); ok && sessID != "" {
					return false
				}
			}
		}
	}

	return true
}

// Helper function to convert any slice to []interface{}
func toInterfaceSlice[T any](slice []T) []interface{} {
	result := make([]interface{}, len(slice))
	for i, v := range slice {
		result[i] = v
	}
	return result
}
