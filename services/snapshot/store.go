package snapshot

import (
	"context"
	"fmt"
	"oneTrick/adapters/driven/firestore/fsutil"
	"oneTrick/api"
	"time"

	"cloud.google.com/go/firestore"
	clog "github.com/Shannor/canonical-logger"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// snapshotStore abstracts persistence operations on the snapshots collection so domain logic
// (hash deduplication, auto-forwarding, soft-archive merging, and delete cleanup) can be unit-tested
// without requiring a live Firestore emulator.
type snapshotStore interface {
	createSnapshotDoc(ctx context.Context, snap api.CharacterSnapshot) (api.CharacterSnapshot, error)
	getByHashes(ctx context.Context, hashes []string) (*api.CharacterSnapshot, error)
	getDoc(ctx context.Context, snapshotID string) (*api.CharacterSnapshot, error)
	getRawDoc(ctx context.Context, snapshotID string) (map[string]any, error)
	setRawDoc(ctx context.Context, snapshotID string, data map[string]any) error
	listByCharacter(ctx context.Context, userID, characterID string) ([]api.CharacterSnapshot, error)
	listAll(ctx context.Context) ([]api.CharacterSnapshot, error)
	listByIDs(ctx context.Context, uniqueIDs []string) ([]api.CharacterSnapshot, error)
	createHistory(ctx context.Context, history History, updatedAt time.Time) (string, error)
	listHistoriesInWindow(ctx context.Context, userID, characterID string, minTime, maxTime time.Time) ([]History, error)
	archiveSource(ctx context.Context, sourceID, targetID string, mergedAt time.Time) error
	repointArchivedChild(ctx context.Context, childID, targetID string, updatedAt time.Time) error
	updateMergedFromIDs(ctx context.Context, targetID string, mergedFromIDs []string, updatedAt time.Time) error
	clearMergeArchiveFields(ctx context.Context, snapshotID string, updatedAt time.Time) error
	removeMergedFromID(ctx context.Context, targetID, sourceID string) error
	updateArmorSetBonuses(ctx context.Context, snapshotID string, bonuses []api.ArmorSetBonus, updatedAt time.Time) error
	deleteSnapshotAndHistory(ctx context.Context, snapshotID string) error
}

type firestoreStore struct {
	db *firestore.Client
}

var _ snapshotStore = (*firestoreStore)(nil)

func (f *firestoreStore) createSnapshotDoc(ctx context.Context, snap api.CharacterSnapshot) (api.CharacterSnapshot, error) {
	ref := f.db.Collection(collection).NewDoc()
	if snap.ID == "" {
		snap.ID = ref.ID
	} else {
		ref = f.db.Collection(collection).Doc(snap.ID)
	}
	if _, err := ref.Set(ctx, snap); err != nil {
		return api.CharacterSnapshot{}, err
	}
	return snap, nil
}

func (f *firestoreStore) getByHashes(ctx context.Context, hashes []string) (*api.CharacterSnapshot, error) {
	if len(hashes) == 0 {
		return nil, nil
	}
	clog.WithGroupField(ctx, "snapshotService.getByHash", "hash", hashes[0])

	var (
		docs []*firestore.DocumentSnapshot
		err  error
	)
	if len(hashes) == 1 {
		docs, err = f.db.Collection(collection).
			Where("hash", "==", hashes[0]).
			Limit(1).
			Documents(ctx).GetAll()
	} else {
		docs, err = f.db.Collection(collection).
			Where("hash", "in", hashes).
			Documents(ctx).GetAll()
	}
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		clog.WithGroupField(ctx, "snapshotService.getByHash", "found", false)
		return nil, nil
	}

	byHash := make(map[string]api.CharacterSnapshot, len(docs))
	for _, doc := range docs {
		var snap api.CharacterSnapshot
		if err := doc.DataTo(&snap); err != nil {
			return nil, err
		}
		if _, exists := byHash[snap.Hash]; !exists {
			byHash[snap.Hash] = snap
		}
	}

	for _, h := range hashes {
		if snap, ok := byHash[h]; ok {
			clog.WithGroupFields(ctx, "snapshotService.getByHash", clog.Fields{
				"found":      true,
				"snapshotId": snap.ID,
			})
			return &snap, nil
		}
	}

	clog.WithGroupField(ctx, "snapshotService.getByHash", "found", false)
	return nil, nil
}

func (f *firestoreStore) getDoc(ctx context.Context, snapshotID string) (*api.CharacterSnapshot, error) {
	if snapshotID == "" {
		return nil, NotFound
	}
	data, err := f.db.Collection(collection).Doc(snapshotID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, NotFound
		}
		return nil, err
	}
	var result api.CharacterSnapshot
	if err := data.DataTo(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (f *firestoreStore) getRawDoc(ctx context.Context, snapshotID string) (map[string]any, error) {
	if snapshotID == "" {
		return nil, NotFound
	}
	doc, err := f.db.Collection(collection).Doc(snapshotID).Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, NotFound
		}
		return nil, err
	}
	return doc.Data(), nil
}

func (f *firestoreStore) setRawDoc(ctx context.Context, snapshotID string, data map[string]any) error {
	_, err := f.db.Collection(collection).Doc(snapshotID).Set(ctx, data)
	return err
}

func (f *firestoreStore) listByCharacter(ctx context.Context, userID, characterID string) ([]api.CharacterSnapshot, error) {
	query := f.db.Collection(collection).
		Where("userId", "==", userID).
		Where("characterId", "==", characterID)

	docs, err := query.Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("failed to query snapshots: %w", err)
	}
	snapshots, err := fsutil.GetAllToStructs[api.CharacterSnapshot](docs)
	if err != nil {
		return nil, fmt.Errorf("failed to parse snapshots: %w", err)
	}
	return snapshots, nil
}

func (f *firestoreStore) listAll(ctx context.Context) ([]api.CharacterSnapshot, error) {
	data, err := f.db.Collection(collection).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	return fsutil.GetAllToStructs[api.CharacterSnapshot](data)
}

func (f *firestoreStore) listByIDs(ctx context.Context, uniqueIDs []string) ([]api.CharacterSnapshot, error) {
	var allResults []api.CharacterSnapshot
	const batchSize = 30
	for i := 0; i < len(uniqueIDs); i += batchSize {
		end := i + batchSize
		if end > len(uniqueIDs) {
			end = len(uniqueIDs)
		}
		batch := uniqueIDs[i:end]

		data, err := f.db.Collection(collection).Where("id", "in", batch).Documents(ctx).GetAll()
		if err != nil {
			return nil, err
		}
		results, err := fsutil.GetAllToStructs[api.CharacterSnapshot](data)
		if err != nil {
			return nil, err
		}
		allResults = append(allResults, results...)
	}
	return allResults, nil
}

func (f *firestoreStore) createHistory(ctx context.Context, history History, updatedAt time.Time) (string, error) {
	ref := f.db.Collection(collection).Doc(history.ParentID).Collection(historyCollection).NewDoc()
	history.ID = ref.ID
	if _, err := ref.Set(ctx, history); err != nil {
		return "", err
	}
	_, err := f.db.Collection(collection).Doc(history.ParentID).Set(ctx, map[string]any{
		"updatedAt": updatedAt,
	}, firestore.MergeAll)
	if err != nil {
		return "", err
	}
	return history.ID, nil
}

func (f *firestoreStore) listHistoriesInWindow(ctx context.Context, userID, characterID string, minTime, maxTime time.Time) ([]History, error) {
	docs, err := f.db.CollectionGroup(historyCollection).
		Where("userId", "==", userID).
		Where("characterId", "==", characterID).
		Where("timestamp", ">=", minTime).
		Where("timestamp", "<=", maxTime).
		OrderBy("timestamp", firestore.Desc).
		Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, nil
	}
	return fsutil.GetAllToStructs[History](docs)
}

func (f *firestoreStore) archiveSource(ctx context.Context, sourceID, targetID string, mergedAt time.Time) error {
	_, err := f.db.Collection(collection).Doc(sourceID).Update(ctx, []firestore.Update{
		{Path: "mergedIntoId", Value: targetID},
		{Path: "mergedAt", Value: mergedAt},
		{Path: "updatedAt", Value: mergedAt},
		{Path: "mergedFromIds", Value: firestore.Delete},
	})
	return err
}

func (f *firestoreStore) repointArchivedChild(ctx context.Context, childID, targetID string, updatedAt time.Time) error {
	_, err := f.db.Collection(collection).Doc(childID).Update(ctx, []firestore.Update{
		{Path: "mergedIntoId", Value: targetID},
		{Path: "updatedAt", Value: updatedAt},
	})
	if status.Code(err) == codes.NotFound {
		return nil
	}
	return err
}

func (f *firestoreStore) updateMergedFromIDs(ctx context.Context, targetID string, mergedFromIDs []string, updatedAt time.Time) error {
	_, err := f.db.Collection(collection).Doc(targetID).Update(ctx, []firestore.Update{
		{Path: "mergedFromIds", Value: mergedFromIDs},
		{Path: "updatedAt", Value: updatedAt},
	})
	return err
}

func (f *firestoreStore) clearMergeArchiveFields(ctx context.Context, snapshotID string, updatedAt time.Time) error {
	_, err := f.db.Collection(collection).Doc(snapshotID).Update(ctx, []firestore.Update{
		{Path: "mergedIntoId", Value: firestore.Delete},
		{Path: "mergedAt", Value: firestore.Delete},
		{Path: "updatedAt", Value: updatedAt},
	})
	if status.Code(err) == codes.NotFound {
		return nil
	}
	return err
}

func (f *firestoreStore) removeMergedFromID(ctx context.Context, targetID, sourceID string) error {
	_, err := f.db.Collection(collection).Doc(targetID).Update(ctx, []firestore.Update{
		{Path: "mergedFromIds", Value: firestore.ArrayRemove(sourceID)},
	})
	if status.Code(err) == codes.NotFound {
		return nil
	}
	return err
}

func (f *firestoreStore) updateArmorSetBonuses(ctx context.Context, snapshotID string, bonuses []api.ArmorSetBonus, updatedAt time.Time) error {
	_, err := f.db.Collection(collection).Doc(snapshotID).Update(ctx, []firestore.Update{
		{Path: "armorSetBonuses", Value: bonuses},
		{Path: "updatedAt", Value: updatedAt},
	})
	return err
}

func (f *firestoreStore) deleteSnapshotAndHistory(ctx context.Context, snapshotID string) error {
	docRef := f.db.Collection(collection).Doc(snapshotID)
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

	if _, err := docRef.Delete(ctx); err != nil {
		return fmt.Errorf("failed to delete snapshot: %w", err)
	}
	return nil
}
