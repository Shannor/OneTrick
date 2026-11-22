package snapshot

import (
	"context"
	"errors"
	"fmt"
	"oneTrick/api"
	"oneTrick/services/aggregate"
	"oneTrick/services/destiny"
	"oneTrick/utils"
	"os"
	"slices"
	"strconv"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
)

// memSnapshotStore is an in-memory snapshotStore used to exercise Service methods directly in unit tests.
type memSnapshotStore struct {
	docs      map[string]api.CharacterSnapshot
	rawDocs   map[string]map[string]any
	histories map[string][]History
	nextSeq   int
}

func newMemSnapshotStore(initial ...api.CharacterSnapshot) *memSnapshotStore {
	m := &memSnapshotStore{
		docs:      make(map[string]api.CharacterSnapshot),
		rawDocs:   make(map[string]map[string]any),
		histories: make(map[string][]History),
	}
	for _, s := range initial {
		m.docs[s.ID] = cloneSnapshot(s)
	}
	return m
}

func cloneSnapshot(s api.CharacterSnapshot) api.CharacterSnapshot {
	out := s
	if s.MergedIntoID != nil {
		out.MergedIntoID = new(*s.MergedIntoID)
	}
	if s.MergedAt != nil {
		t := *s.MergedAt
		out.MergedAt = &t
	}
	if s.MergedFromIDs != nil {
		cp := slices.Clone(*s.MergedFromIDs)
		out.MergedFromIDs = &cp
	}
	return out
}

func (m *memSnapshotStore) createSnapshotDoc(_ context.Context, snap api.CharacterSnapshot) (api.CharacterSnapshot, error) {
	if snap.ID == "" {
		m.nextSeq++
		snap.ID = fmt.Sprintf("snap-gen-%d", m.nextSeq)
	}
	m.docs[snap.ID] = cloneSnapshot(snap)
	return snap, nil
}

func (m *memSnapshotStore) getByHash(_ context.Context, hash string) (*api.CharacterSnapshot, error) {
	for _, doc := range m.docs {
		if doc.Hash == hash {
			cp := cloneSnapshot(doc)
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *memSnapshotStore) getDoc(_ context.Context, snapshotID string) (*api.CharacterSnapshot, error) {
	doc, ok := m.docs[snapshotID]
	if !ok {
		return nil, NotFound
	}
	cp := cloneSnapshot(doc)
	return &cp, nil
}

func (m *memSnapshotStore) getRawDoc(_ context.Context, snapshotID string) (map[string]any, error) {
	if raw, ok := m.rawDocs[snapshotID]; ok {
		return raw, nil
	}
	if _, ok := m.docs[snapshotID]; !ok {
		return nil, NotFound
	}
	return map[string]any{}, nil
}

func (m *memSnapshotStore) setRawDoc(_ context.Context, snapshotID string, data map[string]any) error {
	m.rawDocs[snapshotID] = data
	return nil
}

func (m *memSnapshotStore) listByCharacter(_ context.Context, userID, characterID string) ([]api.CharacterSnapshot, error) {
	var res []api.CharacterSnapshot
	for _, doc := range m.docs {
		if doc.UserID == userID && doc.CharacterID == characterID {
			res = append(res, cloneSnapshot(doc))
		}
	}
	return res, nil
}

func (m *memSnapshotStore) listAll(_ context.Context) ([]api.CharacterSnapshot, error) {
	res := make([]api.CharacterSnapshot, 0, len(m.docs))
	for _, doc := range m.docs {
		res = append(res, cloneSnapshot(doc))
	}
	return res, nil
}

func (m *memSnapshotStore) listByIDs(_ context.Context, uniqueIDs []string) ([]api.CharacterSnapshot, error) {
	var res []api.CharacterSnapshot
	for _, id := range uniqueIDs {
		if doc, ok := m.docs[id]; ok {
			res = append(res, cloneSnapshot(doc))
		}
	}
	return res, nil
}

func (m *memSnapshotStore) createHistory(_ context.Context, history History, updatedAt time.Time) (string, error) {
	m.nextSeq++
	history.ID = fmt.Sprintf("hist-%d", m.nextSeq)
	m.histories[history.ParentID] = append(m.histories[history.ParentID], history)
	if doc, ok := m.docs[history.ParentID]; ok {
		doc.UpdatedAt = updatedAt
		m.docs[history.ParentID] = doc
	}
	return history.ID, nil
}

func (m *memSnapshotStore) archiveSource(_ context.Context, sourceID, targetID string, mergedAt time.Time) error {
	doc, ok := m.docs[sourceID]
	if !ok {
		return NotFound
	}
	doc.MergedIntoID = new(targetID)
	t := mergedAt
	doc.MergedAt = &t
	doc.UpdatedAt = mergedAt
	doc.MergedFromIDs = nil
	m.docs[sourceID] = doc
	return nil
}

func (m *memSnapshotStore) repointArchivedChild(_ context.Context, childID, targetID string, updatedAt time.Time) error {
	doc, ok := m.docs[childID]
	if !ok {
		return nil
	}
	doc.MergedIntoID = new(targetID)
	doc.UpdatedAt = updatedAt
	m.docs[childID] = doc
	return nil
}

func (m *memSnapshotStore) updateMergedFromIDs(_ context.Context, targetID string, mergedFromIDs []string, updatedAt time.Time) error {
	doc, ok := m.docs[targetID]
	if !ok {
		return NotFound
	}
	cp := slices.Clone(mergedFromIDs)
	doc.MergedFromIDs = &cp
	doc.UpdatedAt = updatedAt
	m.docs[targetID] = doc
	return nil
}

func (m *memSnapshotStore) clearMergeArchiveFields(_ context.Context, snapshotID string, updatedAt time.Time) error {
	doc, ok := m.docs[snapshotID]
	if !ok {
		return nil
	}
	doc.MergedIntoID = nil
	doc.MergedAt = nil
	doc.UpdatedAt = updatedAt
	m.docs[snapshotID] = doc
	return nil
}

func (m *memSnapshotStore) removeMergedFromID(_ context.Context, targetID, sourceID string) error {
	doc, ok := m.docs[targetID]
	if !ok || doc.MergedFromIDs == nil {
		return nil
	}
	filtered := slices.DeleteFunc(slices.Clone(*doc.MergedFromIDs), func(id string) bool {
		return id == sourceID
	})
	doc.MergedFromIDs = &filtered
	m.docs[targetID] = doc
	return nil
}

func (m *memSnapshotStore) deleteSnapshotAndHistory(_ context.Context, snapshotID string) error {
	delete(m.docs, snapshotID)
	delete(m.histories, snapshotID)
	return nil
}

type mockAggregateService struct {
	aggregate.Service
	aggs            map[string]api.Aggregate
	rawData         map[string]map[string]any
	removedSnapshot string
}

func newMockAggregateService() *mockAggregateService {
	return &mockAggregateService{
		aggs:    make(map[string]api.Aggregate),
		rawData: make(map[string]map[string]any),
	}
}

func (m *mockAggregateService) addAggregate(agg api.Aggregate) {
	m.aggs[agg.ID] = agg
	rawIDs := make([]any, 0, len(agg.SnapshotIds))
	for _, id := range agg.SnapshotIds {
		rawIDs = append(rawIDs, id)
	}
	links := make(map[string]any)
	for charID, link := range agg.SnapshotLinks {
		links[charID] = link
	}
	m.rawData[agg.ID] = map[string]any{
		"snapshotIds":   rawIDs,
		"snapshotLinks": links,
	}
}

func (m *mockAggregateService) BySnapshotID(_ context.Context, snapshotID string, _ *api.GameMode) ([]api.Aggregate, error) {
	var res []api.Aggregate
	for id, raw := range m.rawData {
		ids, _ := raw["snapshotIds"].([]any)
		for _, v := range ids {
			if s, ok := v.(string); ok && s == snapshotID {
				res = append(res, m.aggs[id])
				break
			}
		}
	}
	return res, nil
}

func (m *mockAggregateService) Update(_ context.Context, aggregateID string, updateFn func(data map[string]any) error, _ bool) error {
	data := m.rawData[aggregateID]
	if err := updateFn(data); err != nil {
		return err
	}
	m.rawData[aggregateID] = data

	// Keep typed Aggregate in sync for chained multi-merge tests.
	agg := m.aggs[aggregateID]
	if rawIDs, ok := data["snapshotIds"].([]any); ok {
		strIDs := make([]string, 0, len(rawIDs))
		for _, v := range rawIDs {
			if s, ok := v.(string); ok {
				strIDs = append(strIDs, s)
			}
		}
		agg.SnapshotIds = strIDs
	}
	if linksMap, ok := data["snapshotLinks"].(map[string]any); ok {
		if agg.SnapshotLinks == nil {
			agg.SnapshotLinks = make(map[string]api.SnapshotLink)
		}
		for charID, v := range linksMap {
			if link, ok := v.(api.SnapshotLink); ok {
				agg.SnapshotLinks[charID] = link
			}
		}
	}
	m.aggs[aggregateID] = agg
	return nil
}

func (m *mockAggregateService) RemoveSnapshot(_ context.Context, snapshotID string) error {
	m.removedSnapshot = snapshotID
	return nil
}

func makeLoadout(kinInst string, kinHash int64, enInst string, enHash int64, subHash int64, powInst string, powHash int64) api.Loadout {
	kinKey := strconv.Itoa(destiny.Kinetic)
	enKey := strconv.Itoa(destiny.Energy)
	subKey := strconv.Itoa(destiny.SubClass)
	powKey := strconv.Itoa(destiny.Power)

	loadout := make(api.Loadout)
	if kinInst != "" || kinHash != 0 {
		loadout[kinKey] = api.ItemSnapshot{
			InstanceID: kinInst,
			ItemHash:   kinHash,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{InstanceId: kinInst, ItemHash: kinHash, BucketHash: destiny.Kinetic},
				Perks:    []api.Perk{{Hash: 200}, {Hash: 100}},
			},
		}
	}
	if enInst != "" || enHash != 0 {
		loadout[enKey] = api.ItemSnapshot{
			InstanceID: enInst,
			ItemHash:   enHash,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{InstanceId: enInst, ItemHash: enHash, BucketHash: destiny.Energy},
				Perks:    []api.Perk{{Hash: 400}, {Hash: 300}},
			},
		}
	}
	if subHash != 0 {
		loadout[subKey] = api.ItemSnapshot{
			InstanceID: "sub-inst-1",
			ItemHash:   subHash,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{InstanceId: "sub-inst-1", ItemHash: subHash, BucketHash: destiny.SubClass},
			},
		}
	}
	if powInst != "" || powHash != 0 {
		loadout[powKey] = api.ItemSnapshot{
			InstanceID: powInst,
			ItemHash:   powHash,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{InstanceId: powInst, ItemHash: powHash, BucketHash: destiny.Power},
			},
		}
	}
	return loadout
}

func TestSnapshotServiceErrors(t *testing.T) {
	if !errors.Is(NotFound, NotFound) {
		t.Errorf("expected NotFound error to match")
	}
	if !errors.Is(Unauthorized, Unauthorized) {
		t.Errorf("expected Unauthorized error to match")
	}
	if !errors.Is(InvalidMerge, InvalidMerge) {
		t.Errorf("expected InvalidMerge error to match")
	}
}

func TestGenerateHashMatchesServerTickAndPreservesCallerPerks(t *testing.T) {
	l1 := makeLoadout("kin-1", 111, "en-1", 222, 333, "pow-1", 444)

	// Build an equivalent loadout with reversed perk slices and extra cosmetic fields
	// that server-tick ignores when hashing.
	l2 := makeLoadout("kin-1", 111, "en-1", 222, 333, "pow-1", 444)
	kinItem := l2[strconv.Itoa(destiny.Kinetic)]
	kinItem.Name = "Cosmetic Rename"
	kinItem.ItemProperties.Perks = []api.Perk{{Hash: 100}, {Hash: 200}}
	l2[strconv.Itoa(destiny.Kinetic)] = kinItem

	h1, err := GenerateHash(l1)
	if err != nil {
		t.Fatalf("GenerateHash(l1) unexpected error: %v", err)
	}
	h2, err := GenerateHash(l2)
	if err != nil {
		t.Fatalf("GenerateHash(l2) unexpected error: %v", err)
	}
	if h1 != h2 {
		t.Fatalf("expected identical hashes regardless of perk ordering or cosmetic name, got %s vs %s", h1, h2)
	}

	// Verify caller's Perks slice in l1 was NOT mutated in-place ({200, 100}).
	origPerks := l1[strconv.Itoa(destiny.Kinetic)].ItemProperties.Perks
	if origPerks[0].Hash != 200 || origPerks[1].Hash != 100 {
		t.Fatalf("expected GenerateHash not to mutate caller's Perks slice, got %+v", origPerks)
	}

	// Verify exact parity with server-tick's expected string slice order:
	// sorted keys: "1498876634" (Kinetic), "2465295065" (Energy), "3284755031" (SubClass), "953998645" (Power)
	expectedSlice := []string{
		"kin-1", "kin-1-100", "kin-1-200",
		"en-1", "en-1-300", "en-1-400",
		"sub-inst-1",
		"pow-1",
	}
	wantHash, err := utils.HashMap(expectedSlice)
	if err != nil {
		t.Fatalf("HashMap failed: %v", err)
	}
	if h1 != wantHash {
		t.Fatalf("GenerateHash mismatch with server-tick formula: got %s, want %s", h1, wantHash)
	}
}

func TestCanMergeStrictKineticEnergySubclassAndEmptyGuard(t *testing.T) {
	base := &api.CharacterSnapshot{
		Loadout: makeLoadout("kin-1", 111, "en-1", 222, 333, "pow-1", 444),
	}

	t.Run("allows merge when Kinetic, Energy, and Subclass match and Heavy differs", func(t *testing.T) {
		candidate := &api.CharacterSnapshot{
			Loadout: makeLoadout("kin-1", 111, "en-1", 222, 333, "pow-999", 999),
		}
		if !CanMerge(base, candidate) {
			t.Fatal("expected CanMerge=true when Kinetic, Energy, and Subclass match")
		}
	})

	t.Run("rejects merge when Subclass differs", func(t *testing.T) {
		diffSub := &api.CharacterSnapshot{
			Loadout: makeLoadout("kin-1", 111, "en-1", 222, 777, "pow-1", 444),
		}
		if CanMerge(base, diffSub) {
			t.Fatal("expected CanMerge=false when Subclass ItemHash differs")
		}
	})

	t.Run("rejects merge when Kinetic or Energy InstanceID differs", func(t *testing.T) {
		diffKin := &api.CharacterSnapshot{
			Loadout: makeLoadout("kin-2", 111, "en-1", 222, 333, "pow-1", 444),
		}
		diffEn := &api.CharacterSnapshot{
			Loadout: makeLoadout("kin-1", 111, "en-2", 222, 333, "pow-1", 444),
		}
		if CanMerge(base, diffKin) || CanMerge(base, diffEn) {
			t.Fatal("expected CanMerge=false when Kinetic or Energy InstanceID differs")
		}
	})

	t.Run("rejects empty Kinetic, empty Energy, zero Subclass, or nil snapshots", func(t *testing.T) {
		emptyA := &api.CharacterSnapshot{Loadout: api.Loadout{}}
		emptyB := &api.CharacterSnapshot{Loadout: api.Loadout{}}
		if CanMerge(emptyA, emptyB) {
			t.Fatal("expected CanMerge=false when both snapshots have empty loadouts ('' == '' bug)")
		}
		missingSubA := &api.CharacterSnapshot{Loadout: makeLoadout("kin-1", 111, "en-1", 222, 0, "pow-1", 444)}
		missingSubB := &api.CharacterSnapshot{Loadout: makeLoadout("kin-1", 111, "en-1", 222, 0, "pow-1", 444)}
		if CanMerge(missingSubA, missingSubB) {
			t.Fatal("expected CanMerge=false when Subclass ItemHash is 0 on both snapshots")
		}
		if CanMerge(nil, base) || CanMerge(base, nil) {
			t.Fatal("expected CanMerge=false for nil snapshots")
		}
	})
}

func TestServiceMergeSoftArchiveFlattenAndAggregateDeduplication(t *testing.T) {
	ctx := context.Background()
	snapA := api.CharacterSnapshot{
		ID:          "snap-a",
		UserID:      "user-1",
		CharacterID: "char-1",
		Loadout:     makeLoadout("kin-1", 111, "en-1", 222, 333, "pow-a", 401),
	}
	snapB := api.CharacterSnapshot{
		ID:          "snap-b",
		UserID:      "user-1",
		CharacterID: "char-1",
		Loadout:     makeLoadout("kin-1", 111, "en-1", 222, 333, "pow-b", 402),
	}
	snapC := api.CharacterSnapshot{
		ID:          "snap-c",
		UserID:      "user-1",
		CharacterID: "char-1",
		Loadout:     makeLoadout("kin-1", 111, "en-1", 222, 333, "pow-c", 403),
	}

	store := newMemSnapshotStore(snapA, snapB, snapC)
	aggMock := newMockAggregateService()
	aggMock.addAggregate(api.Aggregate{
		ID:          "agg-1",
		SnapshotIds: []string{"snap-a", "snap-b"}, // snap-b already in snapshotIds to test deduplication!
		SnapshotLinks: map[string]api.SnapshotLink{
			"char-1": {
				CharacterID: "char-1",
				SnapshotID:  new("snap-a"),
			},
		},
	})

	svc := &service{
		store:            store,
		aggregateService: aggMock,
	}

	// 1. Unauthorized user cannot merge
	if _, err := svc.Merge(ctx, "intruder", "snap-b", []string{"snap-a"}); !errors.Is(err, Unauthorized) {
		t.Fatalf("expected Unauthorized for intruder, got %v", err)
	}

	// 2. Merge A -> B
	mergedB, err := svc.Merge(ctx, "user-1", "snap-b", []string{"snap-a"})
	if err != nil {
		t.Fatalf("Merge(A -> B) unexpected error: %v", err)
	}
	if mergedB.MergedFromIDs == nil || !slices.Equal(*mergedB.MergedFromIDs, []string{"snap-a"}) {
		t.Fatalf("expected B.MergedFromIDs=[snap-a], got %+v", mergedB.MergedFromIDs)
	}

	storedA, _ := svc.Get(ctx, "snap-a")
	if storedA.MergedIntoID == nil || *storedA.MergedIntoID != "snap-b" || storedA.MergedAt == nil {
		t.Fatalf("expected snap-a to be archived into snap-b, got %+v", storedA)
	}

	// Verify agg-1 deduplicated snapshotIds to ["snap-b"] (not ["snap-b", "snap-b"]) and preserved OriginalSnapshotID="snap-a"
	agg1AfterFirst := aggMock.aggs["agg-1"]
	if !slices.Equal(agg1AfterFirst.SnapshotIds, []string{"snap-b"}) {
		t.Fatalf("expected deduplicated snapshotIds=[snap-b], got %v", agg1AfterFirst.SnapshotIds)
	}
	linkAfterFirst := agg1AfterFirst.SnapshotLinks["char-1"]
	if *linkAfterFirst.SnapshotID != "snap-b" || *linkAfterFirst.OriginalSnapshotID != "snap-a" || linkAfterFirst.ConfidenceSource != api.UserConfidenceSource {
		t.Fatalf("unexpected link after first merge: %+v", linkAfterFirst)
	}

	// 3. Chain merge B -> C: must flatten A -> C, archive B -> C, clear B.MergedFromIDs, and preserve OriginalSnapshotID="snap-a" on agg-1!
	mergedC, err := svc.Merge(ctx, "user-1", "snap-c", []string{"snap-b"})
	if err != nil {
		t.Fatalf("Merge(B -> C) unexpected error: %v", err)
	}
	if mergedC.MergedFromIDs == nil || !slices.Equal(*mergedC.MergedFromIDs, []string{"snap-b", "snap-a"}) {
		t.Fatalf("expected C.MergedFromIDs=[snap-b snap-a], got %+v", mergedC.MergedFromIDs)
	}

	storedA, _ = svc.Get(ctx, "snap-a")
	storedB, _ := svc.Get(ctx, "snap-b")
	if *storedA.MergedIntoID != "snap-c" {
		t.Fatalf("expected flattened child snap-a.MergedIntoID=snap-c, got %v", *storedA.MergedIntoID)
	}
	if *storedB.MergedIntoID != "snap-c" || storedB.MergedFromIDs != nil {
		t.Fatalf("expected snap-b archived into snap-c with cleared MergedFromIDs, got %+v", storedB)
	}

	agg1AfterChain := aggMock.aggs["agg-1"]
	linkAfterChain := agg1AfterChain.SnapshotLinks["char-1"]
	if *linkAfterChain.SnapshotID != "snap-c" || *linkAfterChain.OriginalSnapshotID != "snap-a" {
		t.Fatalf("expected agg-1 link SnapshotID=snap-c and preserved OriginalSnapshotID=snap-a, got %+v", linkAfterChain)
	}

	// 4. GetAllByCharacter must exclude archived A and B and return only active C!
	active, err := svc.GetAllByCharacter(ctx, "user-1", "char-1", 0, 0)
	if err != nil {
		t.Fatalf("GetAllByCharacter error: %v", err)
	}
	if len(active) != 1 || active[0].ID != "snap-c" {
		t.Fatalf("expected only [snap-c] from GetAllByCharacter, got %+v", active)
	}
}

func TestCreateAutoForwardsArchivedHashAndHandlesDeletedTargetFallback(t *testing.T) {
	ctx := context.Background()
	loadoutA := makeLoadout("kin-1", 111, "en-1", 222, 333, "pow-a", 401)
	hashA, err := GenerateHash(loadoutA)
	if err != nil {
		t.Fatalf("GenerateHash error: %v", err)
	}
	loadoutB := makeLoadout("kin-1", 111, "en-1", 222, 333, "pow-b", 402)
	hashB, _ := GenerateHash(loadoutB)
	now := time.Now()

	snapA := api.CharacterSnapshot{
		ID:           "snap-a",
		UserID:       "user-1",
		CharacterID:  "char-1",
		Hash:         hashA,
		Loadout:      loadoutA,
		MergedIntoID: new("snap-b"),
		MergedAt:     &now,
	}
	snapB := api.CharacterSnapshot{
		ID:            "snap-b",
		UserID:        "user-1",
		CharacterID:   "char-1",
		Hash:          hashB,
		Loadout:       loadoutB,
		MergedFromIDs: new([]string{"snap-a"}),
	}

	store := newMemSnapshotStore(snapA, snapB)
	svc := &service{
		store:            store,
		aggregateService: newMockAggregateService(),
	}

	// Auto-forwards creation of loadoutA to snap-b
	id, err := svc.create(ctx, "user-1", api.CharacterSnapshot{
		UserID:      "user-1",
		CharacterID: "char-1",
		Loadout:     loadoutA,
	})
	if err != nil {
		t.Fatalf("create auto-forward unexpected error: %v", err)
	}
	if id == nil || *id != "snap-b" {
		t.Fatalf("expected create to auto-forward to snap-b, got %v", id)
	}
	if len(store.histories["snap-b"]) != 1 || len(store.histories["snap-a"]) != 0 {
		t.Fatalf("expected history recorded under snap-b (1) and not snap-a (0), got %+v", store.histories)
	}

	// Simulate snap-b missing directly (e.g. legacy dangling pointer): fallback should un-archive snap-a and record history on snap-a
	delete(store.docs, "snap-b")
	idFallback, err := svc.create(ctx, "user-1", api.CharacterSnapshot{
		UserID:      "user-1",
		CharacterID: "char-1",
		Loadout:     loadoutA,
	})
	if err != nil {
		t.Fatalf("create fallback unexpected error: %v", err)
	}
	if idFallback == nil || *idFallback != "snap-a" {
		t.Fatalf("expected fallback to snap-a when target missing, got %v", idFallback)
	}
	restoredA, _ := svc.Get(ctx, "snap-a")
	if restoredA.MergedIntoID != nil || restoredA.MergedAt != nil {
		t.Fatalf("expected snap-a MergedIntoID/MergedAt cleared after fallback, got %+v", restoredA)
	}
}

func TestDeleteCleansUpMergeRelationships(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	snapA := api.CharacterSnapshot{
		ID:           "snap-a",
		UserID:       "user-1",
		CharacterID:  "char-1",
		MergedIntoID: new("snap-c"),
		MergedAt:     &now,
	}
	snapB := api.CharacterSnapshot{
		ID:           "snap-b",
		UserID:       "user-1",
		CharacterID:  "char-1",
		MergedIntoID: new("snap-c"),
		MergedAt:     &now,
	}
	snapC := api.CharacterSnapshot{
		ID:            "snap-c",
		UserID:        "user-1",
		CharacterID:   "char-1",
		MergedFromIDs: new([]string{"snap-a", "snap-b"}),
	}

	store := newMemSnapshotStore(snapA, snapB, snapC)
	aggMock := newMockAggregateService()
	svc := &service{
		store:            store,
		aggregateService: aggMock,
	}

	// Deleting archived source snap-a removes it from snap-c.MergedFromIDs
	if err := svc.Delete(ctx, "snap-a", "user-1"); err != nil {
		t.Fatalf("Delete(snap-a) error: %v", err)
	}
	afterSourceDel, _ := svc.Get(ctx, "snap-c")
	if !slices.Equal(*afterSourceDel.MergedFromIDs, []string{"snap-b"}) {
		t.Fatalf("expected snap-c.MergedFromIDs=[snap-b] after deleting snap-a, got %v", *afterSourceDel.MergedFromIDs)
	}

	// Deleting target snap-c clears MergedIntoID/MergedAt on remaining archived source snap-b
	if err := svc.Delete(ctx, "snap-c", "user-1"); err != nil {
		t.Fatalf("Delete(snap-c) error: %v", err)
	}
	restoredB, err := svc.Get(ctx, "snap-b")
	if err != nil {
		t.Fatalf("Get(snap-b) error: %v", err)
	}
	if restoredB.MergedIntoID != nil || restoredB.MergedAt != nil {
		t.Fatalf("expected snap-b archive fields cleared after deleting target snap-c, got %+v", restoredB)
	}
}

func TestFirestoreEmulatorSnapshotSoftMergeLifecycle(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("skipping Firestore emulator integration test; set FIRESTORE_EMULATOR_HOST to run")
	}

	ctx := context.Background()
	db, err := firestore.NewClient(ctx, "gruntt-destiny-test")
	if err != nil {
		t.Fatalf("failed to create Firestore emulator client: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	aggSvc := aggregate.NewService(db)
	snapSvc := NewService(db, nil, nil, aggSvc).(*service)

	runID := fmt.Sprintf("%d", time.Now().UnixNano())
	userID := "emu-user-" + runID
	charID := "emu-char-" + runID
	aggID := "emu-agg-" + runID

	loadoutA := makeLoadout("emu-kin-"+runID, 111, "emu-en-"+runID, 222, 333, "emu-pow-a-"+runID, 401)
	loadoutB := makeLoadout("emu-kin-"+runID, 111, "emu-en-"+runID, 222, 333, "emu-pow-b-"+runID, 402)
	loadoutC := makeLoadout("emu-kin-"+runID, 111, "emu-en-"+runID, 222, 333, "emu-pow-c-"+runID, 403)

	idA, err := snapSvc.create(ctx, userID, api.CharacterSnapshot{UserID: userID, CharacterID: charID, Name: "Loadout A", Loadout: loadoutA})
	if err != nil {
		t.Fatalf("create(A) failed: %v", err)
	}
	idB, err := snapSvc.create(ctx, userID, api.CharacterSnapshot{UserID: userID, CharacterID: charID, Name: "Loadout B", Loadout: loadoutB})
	if err != nil {
		t.Fatalf("create(B) failed: %v", err)
	}
	idC, err := snapSvc.create(ctx, userID, api.CharacterSnapshot{UserID: userID, CharacterID: charID, Name: "Loadout C", Loadout: loadoutC})
	if err != nil {
		t.Fatalf("create(C) failed: %v", err)
	}

	t.Cleanup(func() {
		_ = snapSvc.Delete(ctx, *idA, userID)
		_ = snapSvc.Delete(ctx, *idB, userID)
		_ = snapSvc.Delete(ctx, *idC, userID)
		_, _ = db.Collection("aggregates").Doc(aggID).Delete(ctx)
	})

	// Seed a real aggregate doc referencing *idA (and already containing *idB to verify deduplication).
	_, err = db.Collection("aggregates").Doc(aggID).Set(ctx, api.Aggregate{
		ID:           aggID,
		ActivityID:   "act-100",
		CreatedAt:    time.Now(),
		SnapshotIds:  []string{*idA, *idB},
		SessionIds:   []string{"ses-1"},
		CharacterIds: []string{charID},
		SnapshotLinks: map[string]api.SnapshotLink{
			charID: {
				CharacterID:      charID,
				ConfidenceLevel:  api.HighConfidenceLevel,
				ConfidenceSource: api.SystemConfidenceSource,
				CreatedAt:        time.Now(),
				SnapshotID:       idA,
			},
		},
	})
	if err != nil {
		t.Fatalf("failed to seed aggregate: %v", err)
	}

	// 1. Merge A -> B on real Firestore
	if _, err := snapSvc.Merge(ctx, userID, *idB, []string{*idA}); err != nil {
		t.Fatalf("Merge(A -> B) failed: %v", err)
	}

	// 2. Chain merge B -> C on real Firestore
	mergedC, err := snapSvc.Merge(ctx, userID, *idC, []string{*idB})
	if err != nil {
		t.Fatalf("Merge(B -> C) failed: %v", err)
	}
	if mergedC.MergedFromIDs == nil || len(*mergedC.MergedFromIDs) != 2 {
		t.Fatalf("expected C.MergedFromIDs to contain both B and A, got %+v", mergedC.MergedFromIDs)
	}

	// Verify A was flattened directly to C in Firestore
	gotA, err := snapSvc.Get(ctx, *idA)
	if err != nil {
		t.Fatalf("Get(A) failed: %v", err)
	}
	if gotA.MergedIntoID == nil || *gotA.MergedIntoID != *idC {
		t.Fatalf("expected flattened A.MergedIntoID=%s, got %+v", *idC, gotA.MergedIntoID)
	}

	// Verify GetAllByCharacter only returns C
	active, err := snapSvc.GetAllByCharacter(ctx, userID, charID, 0, 0)
	if err != nil {
		t.Fatalf("GetAllByCharacter failed: %v", err)
	}
	if len(active) != 1 || active[0].ID != *idC {
		t.Fatalf("expected only active snapshot C (%s), got %+v", *idC, active)
	}

	// Verify aggregate now matches BySnapshotID(C) with OriginalSnapshotID == A and 0 matches for A or B
	aggsForA, _ := aggSvc.BySnapshotID(ctx, *idA, nil)
	aggsForC, err := aggSvc.BySnapshotID(ctx, *idC, nil)
	if err != nil {
		t.Fatalf("BySnapshotID(C) failed: %v", err)
	}
	if len(aggsForA) != 0 || len(aggsForC) != 1 {
		t.Fatalf("expected 0 aggs for A and 1 agg for C, got A=%d C=%d", len(aggsForA), len(aggsForC))
	}
	link := aggsForC[0].SnapshotLinks[charID]
	if link.SnapshotID == nil || *link.SnapshotID != *idC || link.OriginalSnapshotID == nil || *link.OriginalSnapshotID != *idA {
		t.Fatalf("unexpected aggregate link after chained merge: %+v", link)
	}

	// 3. Re-creating Loadout A auto-forwards to C!
	forwardedID, err := snapSvc.create(ctx, userID, api.CharacterSnapshot{UserID: userID, CharacterID: charID, Loadout: loadoutA})
	if err != nil {
		t.Fatalf("auto-forwarded create(A) failed: %v", err)
	}
	if forwardedID == nil || *forwardedID != *idC {
		t.Fatalf("expected auto-forward to C (%s), got %v", *idC, forwardedID)
	}

	// 4. Deleting C clears mergedIntoId/mergedAt on A and B in Firestore!
	if err := snapSvc.Delete(ctx, *idC, userID); err != nil {
		t.Fatalf("Delete(C) failed: %v", err)
	}
	unarchivedA, err := snapSvc.Get(ctx, *idA)
	if err != nil {
		t.Fatalf("Get(A) after Delete(C) failed: %v", err)
	}
	if unarchivedA.MergedIntoID != nil || unarchivedA.MergedAt != nil {
		t.Fatalf("expected A unarchived in Firestore after deleting C, got %+v", unarchivedA)
	}
}

func TestCanMergeServerTickVsBackendEffectiveInstanceID(t *testing.T) {
	kineticKey := strconv.Itoa(destiny.Kinetic)
	energyKey := strconv.Itoa(destiny.Energy)
	subclassKey := strconv.Itoa(destiny.SubClass)

	// Backend snapshot sets both top-level InstanceID and BaseInfo.InstanceId to real 64-bit Bungie ID.
	backendSnap := &api.CharacterSnapshot{
		ID: "backend-snap",
		Loadout: api.Loadout{
			kineticKey: {
				ItemHash:   2907129556,
				InstanceID: "6917529895626386046",
				ItemProperties: api.ItemProperties{
					BaseInfo: api.BaseItemInfo{InstanceId: "6917529895626386046"},
				},
			},
			energyKey: {
				ItemHash:   738446555,
				InstanceID: "6917530145638094412",
				ItemProperties: api.ItemProperties{
					BaseInfo: api.BaseItemInfo{InstanceId: "6917530145638094412"},
				},
			},
			subclassKey: {ItemHash: 3284755031},
		},
	}

	// server-tick snapshot sets top-level InstanceID to itemHash string while BaseInfo.InstanceId holds the real Bungie ID.
	serverTickSameRoll := &api.CharacterSnapshot{
		ID: "tick-same",
		Loadout: api.Loadout{
			kineticKey: {
				ItemHash:   2907129556,
				InstanceID: "2907129556",
				ItemProperties: api.ItemProperties{
					BaseInfo: api.BaseItemInfo{InstanceId: "6917529895626386046"},
				},
			},
			energyKey: {
				ItemHash:   738446555,
				InstanceID: "738446555",
				ItemProperties: api.ItemProperties{
					BaseInfo: api.BaseItemInfo{InstanceId: "6917530145638094412"},
				},
			},
			subclassKey: {ItemHash: 3284755031},
		},
	}

	// Different roll of the same weapon in server-tick format must NOT match.
	serverTickDiffRoll := &api.CharacterSnapshot{
		ID: "tick-diff-roll",
		Loadout: api.Loadout{
			kineticKey: {
				ItemHash:   2907129556,
				InstanceID: "2907129556",
				ItemProperties: api.ItemProperties{
					BaseInfo: api.BaseItemInfo{InstanceId: "6917529999999999999"},
				},
			},
			energyKey: {
				ItemHash:   738446555,
				InstanceID: "738446555",
				ItemProperties: api.ItemProperties{
					BaseInfo: api.BaseItemInfo{InstanceId: "6917530145638094412"},
				},
			},
			subclassKey: {ItemHash: 3284755031},
		},
	}

	if !CanMerge(backendSnap, serverTickSameRoll) {
		t.Fatal("expected backend snapshot and server-tick snapshot with identical weapon rolls to be mergeable")
	}
	if CanMerge(serverTickSameRoll, serverTickDiffRoll) {
		t.Fatal("expected two server-tick snapshots with different weapon rolls of the same itemHash NOT to be mergeable")
	}
}
