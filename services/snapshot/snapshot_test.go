package snapshot

import (
	"context"
	"errors"
	"fmt"
	"oneTrick/api"
	"oneTrick/core/domain"
	"oneTrick/core/inventory"
	"oneTrick/ptr"
	"oneTrick/services/aggregate"
	"oneTrick/services/user"
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
	if s.ArmorSetBonuses != nil {
		bonuses := make([]api.ArmorSetBonus, len(*s.ArmorSetBonuses))
		for i, b := range *s.ArmorSetBonuses {
			bCopy := b
			if b.ActivePerks != nil {
				perks := make([]api.ArmorSetBonusPerk, len(b.ActivePerks))
				for j, p := range b.ActivePerks {
					pCopy := p
					if p.Description != nil {
						pCopy.Description = new(*p.Description)
					}
					if p.IconPath != nil {
						pCopy.IconPath = new(*p.IconPath)
					}
					perks[j] = pCopy
				}
				bCopy.ActivePerks = perks
			}
			bonuses[i] = bCopy
		}
		out.ArmorSetBonuses = &bonuses
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

func (m *memSnapshotStore) getByHashes(_ context.Context, hashes []string) (*api.CharacterSnapshot, error) {
	for _, hash := range hashes {
		for _, doc := range m.docs {
			if doc.Hash == hash {
				cp := cloneSnapshot(doc)
				return &cp, nil
			}
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

func (m *memSnapshotStore) listHistoriesInWindow(_ context.Context, userID, characterID string, minTime, maxTime time.Time) ([]History, error) {
	var res []History
	for _, list := range m.histories {
		for _, h := range list {
			if h.UserID != userID || h.CharacterID != characterID {
				continue
			}
			if h.Timestamp.Before(minTime) || h.Timestamp.After(maxTime) {
				continue
			}
			res = append(res, h)
		}
	}
	slices.SortFunc(res, func(a, b History) int {
		return b.Timestamp.Compare(a.Timestamp)
	})
	return res, nil
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

func (m *memSnapshotStore) updateArmorSetBonuses(_ context.Context, snapshotID string, bonuses []api.ArmorSetBonus, updatedAt time.Time) error {
	doc, ok := m.docs[snapshotID]
	if !ok {
		return NotFound
	}
	cloned := cloneSnapshot(api.CharacterSnapshot{ArmorSetBonuses: &bonuses})
	doc.ArmorSetBonuses = cloned.ArmorSetBonuses
	doc.UpdatedAt = updatedAt
	m.docs[snapshotID] = doc
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
	kinKey := inventory.KineticKey
	enKey := inventory.EnergyKey
	subKey := inventory.SubClassKey
	powKey := inventory.PowerKey

	loadout := make(api.Loadout)
	if kinInst != "" || kinHash != 0 {
		loadout[kinKey] = api.ItemSnapshot{
			InstanceID: kinInst,
			ItemHash:   kinHash,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{InstanceId: kinInst, ItemHash: kinHash, BucketHash: inventory.Kinetic},
				Perks:    []api.Perk{{Hash: 200}, {Hash: 100}},
			},
		}
	}
	if enInst != "" || enHash != 0 {
		loadout[enKey] = api.ItemSnapshot{
			InstanceID: enInst,
			ItemHash:   enHash,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{InstanceId: enInst, ItemHash: enHash, BucketHash: inventory.Energy},
				Perks:    []api.Perk{{Hash: 400}, {Hash: 300}},
			},
		}
	}
	if subHash != 0 {
		loadout[subKey] = api.ItemSnapshot{
			InstanceID: "sub-inst-1",
			ItemHash:   subHash,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{InstanceId: "sub-inst-1", ItemHash: subHash, BucketHash: inventory.SubClass},
			},
		}
	}
	if powInst != "" || powHash != 0 {
		loadout[powKey] = api.ItemSnapshot{
			InstanceID: powInst,
			ItemHash:   powHash,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{InstanceId: powInst, ItemHash: powHash, BucketHash: inventory.Power},
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
	kinItem := l2[inventory.KineticKey]
	kinItem.Name = "Cosmetic Rename"
	kinItem.ItemProperties.Perks = []api.Perk{{Hash: 100}, {Hash: 200}}
	l2[inventory.KineticKey] = kinItem

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
	origPerks := l1[inventory.KineticKey].ItemProperties.Perks
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
	kineticKey := inventory.KineticKey
	energyKey := inventory.EnergyKey
	subclassKey := inventory.SubClassKey

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

type fakeSnapshotUserService struct {
	user.Service
	membershipType int64
	calls          int
}

func (f *fakeSnapshotUserService) GetMembershipType(_ context.Context, _, _ string) (int64, error) {
	f.calls++
	return f.membershipType, nil
}

type fakeSnapshotD2Service struct {
	loadout            api.Loadout
	stats              map[string]api.ClassStat
	bonuses            []api.ArmorSetBonus
	ts                 *time.Time
	lastMembershipType int64
}

func (f *fakeSnapshotD2Service) GetLiveLoadout(_ context.Context, _ int64, membershipType int64, _ string) (domain.LiveLoadout, error) {
	f.lastMembershipType = membershipType
	outLoadout := make(domain.Loadout, len(f.loadout))
	for k, item := range f.loadout {
		perks := make([]domain.Perk, 0, len(item.ItemProperties.Perks))
		for _, pk := range item.ItemProperties.Perks {
			perks = append(perks, domain.Perk{
				Name:        pk.Name,
				Description: ptr.Deref(pk.Description),
				Hash:        pk.Hash,
				IconPath:    ptr.Deref(pk.IconPath),
			})
		}
		outLoadout[k] = domain.ItemSnapshot{
			InstanceID: item.InstanceID,
			Name:       item.Name,
			ItemHash:   item.ItemHash,
			BucketHash: item.BucketHash,
			Details: domain.ItemProperties{
				CharacterID: ptr.Deref(item.ItemProperties.CharacterId),
				BaseInfo: domain.BaseItemInfo{
					Name:                       item.ItemProperties.BaseInfo.Name,
					Icon:                       ptr.Deref(item.ItemProperties.BaseInfo.Icon),
					ItemHash:                   item.ItemProperties.BaseInfo.ItemHash,
					InstanceID:                 item.ItemProperties.BaseInfo.InstanceId,
					BucketHash:                 item.ItemProperties.BaseInfo.BucketHash,
					ItemTypeAndTierDisplayName: item.ItemProperties.BaseInfo.ItemTypeAndTierDisplayName,
					ItemTypeDisplayName:        item.ItemProperties.BaseInfo.ItemTypeDisplayName,
					TierTypeName:               item.ItemProperties.BaseInfo.TierTypeName,
					TierType:                   item.ItemProperties.BaseInfo.TierType,
					EquipableItemSetHash:       item.ItemProperties.BaseInfo.EquipableItemSetHash,
				},
				Perks: perks,
			},
		}
	}
	outStats := make(map[string]domain.ClassStat, len(f.stats))
	for k, st := range f.stats {
		outStats[k] = domain.ClassStat{
			Name:            st.Name,
			Description:     st.Description,
			Icon:            st.Icon,
			HasIcon:         st.HasIcon,
			StatCategory:    st.StatCategory,
			AggregationType: st.AggregationType,
			Value:           st.Value,
		}
	}
	var outBonuses []domain.ArmorSetBonus
	if len(f.bonuses) > 0 {
		outBonuses = make([]domain.ArmorSetBonus, 0, len(f.bonuses))
		for _, b := range f.bonuses {
			perks := make([]domain.ArmorSetBonusPerk, 0, len(b.ActivePerks))
			for _, pk := range b.ActivePerks {
				perks = append(perks, domain.ArmorSetBonusPerk{
					RequiredSetCount: pk.RequiredSetCount,
					Hash:             pk.Hash,
					Name:             pk.Name,
					Description:      ptr.Deref(pk.Description),
					IconPath:         ptr.Deref(pk.IconPath),
				})
			}
			outBonuses = append(outBonuses, domain.ArmorSetBonus{
				SetHash:     b.SetHash,
				SetName:     b.SetName,
				ActiveCount: b.ActiveCount,
				ActivePerks: perks,
			})
		}
	}
	return domain.LiveLoadout{
		Loadout:         outLoadout,
		Stats:           outStats,
		ArmorSetBonuses: outBonuses,
		MintedAt:        f.ts,
	}, nil
}

func TestCloneSnapshotAndSaveRoundTripArmorSetBonuses(t *testing.T) {
	kineticKey := inventory.KineticKey
	energyKey := inventory.EnergyKey
	powerKey := inventory.PowerKey

	loadout := api.Loadout{
		kineticKey: {
			ItemHash:   111,
			InstanceID: "kin-inst",
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{InstanceId: "kin-inst", ItemHash: 111},
			},
		},
		energyKey: {
			ItemHash:   222,
			InstanceID: "en-inst",
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{InstanceId: "en-inst", ItemHash: 222},
			},
		},
		powerKey: {
			ItemHash:   333,
			InstanceID: "pow-inst",
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{InstanceId: "pow-inst", ItemHash: 333},
			},
		},
	}

	bonuses := []api.ArmorSetBonus{
		{
			SetHash:     1000,
			SetName:     "Techsec Armor",
			ActiveCount: 2,
			ActivePerks: []api.ArmorSetBonusPerk{
				{
					RequiredSetCount: 2,
					Hash:             9002,
					Name:             "Techsec 2pc",
					Description:      new("Kinetic shield bonus"),
					IconPath:         new("https://www.bungie.net/icons/techsec2.png"),
				},
			},
		},
		{
			SetHash:     2000,
			SetName:     "Bushido Armor",
			ActiveCount: 2,
			ActivePerks: []api.ArmorSetBonusPerk{
				{
					RequiredSetCount: 2,
					Hash:             9102,
					Name:             "Bushido 2pc",
					Description:      new("Heal on final blow"),
				},
			},
		},
	}

	t.Run("cloneSnapshot deep-copies ArmorSetBonuses and ActivePerks", func(t *testing.T) {
		orig := api.CharacterSnapshot{
			ID:              "snap-1",
			ArmorSetBonuses: &bonuses,
		}
		cloned := cloneSnapshot(orig)
		if cloned.ArmorSetBonuses == nil || len(*cloned.ArmorSetBonuses) != 2 {
			t.Fatalf("expected 2 cloned bonuses, got %+v", cloned.ArmorSetBonuses)
		}
		(*cloned.ArmorSetBonuses)[0].SetName = "Mutated"
		(*cloned.ArmorSetBonuses)[0].ActivePerks[0].Name = "Mutated Perk"
		*(*cloned.ArmorSetBonuses)[0].ActivePerks[0].Description = "Mutated Desc"

		if (*orig.ArmorSetBonuses)[0].SetName != "Techsec Armor" ||
			(*orig.ArmorSetBonuses)[0].ActivePerks[0].Name != "Techsec 2pc" ||
			*(*orig.ArmorSetBonuses)[0].ActivePerks[0].Description != "Kinetic shield bonus" {
			t.Fatalf("mutating cloned snapshot altered original ArmorSetBonuses: %+v", *orig.ArmorSetBonuses)
		}
	})

	t.Run("Save and Get round-trip active ArmorSetBonuses without changing GenerateHash", func(t *testing.T) {
		now := time.Now()
		store := newMemSnapshotStore()
		svc := &service{
			store:       store,
			UserService: &fakeSnapshotUserService{membershipType: 3},
			D2Service: &fakeSnapshotD2Service{
				loadout: loadout,
				stats:   map[string]api.ClassStat{},
				bonuses: bonuses,
				ts:      &now,
			},
		}

		expectedHash, err := GenerateHash(loadout)
		if err != nil {
			t.Fatalf("GenerateHash failed: %v", err)
		}

		saved, err := svc.Save(context.Background(), "user-1", "4611686018467484863", "char-1")
		if err != nil {
			t.Fatalf("Save failed: %v", err)
		}
		if saved.Hash != expectedHash {
			t.Fatalf("expected hash %q to remain unchanged, got %q", expectedHash, saved.Hash)
		}
		if saved.ArmorSetBonuses == nil || len(*saved.ArmorSetBonuses) != 2 {
			t.Fatalf("expected 2 saved ArmorSetBonuses, got %+v", saved.ArmorSetBonuses)
		}

		fetched, err := svc.Get(context.Background(), saved.ID)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if fetched.ArmorSetBonuses == nil || len(*fetched.ArmorSetBonuses) != 2 {
			t.Fatalf("expected 2 fetched ArmorSetBonuses, got %+v", fetched.ArmorSetBonuses)
		}
		if (*fetched.ArmorSetBonuses)[0].SetHash != 1000 || (*fetched.ArmorSetBonuses)[1].SetHash != 2000 {
			t.Fatalf("unexpected fetched ArmorSetBonuses: %+v", *fetched.ArmorSetBonuses)
		}
	})

	t.Run("Save omits empty ArmorSetBonuses as nil", func(t *testing.T) {
		now := time.Now()
		store := newMemSnapshotStore()
		svc := &service{
			store:       store,
			UserService: &fakeSnapshotUserService{membershipType: 3},
			D2Service: &fakeSnapshotD2Service{
				loadout: loadout,
				stats:   map[string]api.ClassStat{},
				bonuses: []api.ArmorSetBonus{},
				ts:      &now,
			},
		}

		saved, err := svc.Save(context.Background(), "user-1", "4611686018467484863", "char-1")
		if err != nil {
			t.Fatalf("Save failed: %v", err)
		}
		if saved.ArmorSetBonuses != nil {
			t.Fatalf("expected nil ArmorSetBonuses when empty, got %+v", saved.ArmorSetBonuses)
		}
	})

	t.Run("Save self-heals missing ArmorSetBonuses on existing snapshot hash match", func(t *testing.T) {
		now := time.Now()
		store := newMemSnapshotStore()
		d2Fake := &fakeSnapshotD2Service{
			loadout: loadout,
			stats:   map[string]api.ClassStat{},
			bonuses: nil,
			ts:      &now,
		}
		svc := &service{
			store:       store,
			UserService: &fakeSnapshotUserService{membershipType: 3},
			D2Service:   d2Fake,
		}

		initial, err := svc.Save(context.Background(), "user-1", "4611686018467484863", "char-1")
		if err != nil {
			t.Fatalf("initial Save failed: %v", err)
		}
		if initial.ArmorSetBonuses != nil {
			t.Fatalf("expected nil ArmorSetBonuses on initial degraded save, got %+v", initial.ArmorSetBonuses)
		}

		d2Fake.bonuses = bonuses
		healed, err := svc.Save(context.Background(), "user-1", "4611686018467484863", "char-1")
		if err != nil {
			t.Fatalf("second Save failed: %v", err)
		}
		if healed.ID != initial.ID {
			t.Fatalf("expected hash match to reuse snapshot %q, got %q", initial.ID, healed.ID)
		}
		if healed.ArmorSetBonuses == nil || len(*healed.ArmorSetBonuses) != 2 {
			t.Fatalf("expected self-healed ArmorSetBonuses on reused snapshot, got %+v", healed.ArmorSetBonuses)
		}
	})
}

func makeWeaponsMap(refs ...int64) map[string]api.WeaponInstanceMetrics {
	m := make(map[string]api.WeaponInstanceMetrics, len(refs))
	for _, r := range refs {
		ref := r
		m[strconv.FormatInt(r, 10)] = api.WeaponInstanceMetrics{ReferenceID: &ref}
	}
	return m
}

func TestFindBestFit(t *testing.T) {
	ctx := context.Background()
	basePeriod := time.Date(2026, 4, 10, 18, 0, 0, 0, time.UTC)

	t.Run("notFound when no histories in [-1h, +15m] window", func(t *testing.T) {
		store := newMemSnapshotStore(api.CharacterSnapshot{ID: "snap-1", UserID: "u1", CharacterID: "c1"})
		store.histories["snap-1"] = []History{
			{ID: "h-early", ParentID: "snap-1", UserID: "u1", CharacterID: "c1", Timestamp: basePeriod.Add(-61 * time.Minute), Meta: MetaData{KineticID: "100"}},
			{ID: "h-late", ParentID: "snap-1", UserID: "u1", CharacterID: "c1", Timestamp: basePeriod.Add(16 * time.Minute), Meta: MetaData{KineticID: "100"}},
		}
		svc := &service{store: store}
		snap, link, err := svc.FindBestFit(ctx, "u1", "c1", basePeriod, makeWeaponsMap(100))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if snap != nil {
			t.Fatalf("expected nil snap, got %+v", snap)
		}
		if link == nil || link.ConfidenceLevel != api.NotFoundConfidenceLevel || link.ConfidenceSource != api.SystemConfidenceSource || link.SnapshotID != nil {
			t.Fatalf("unexpected link: %+v", link)
		}
	})

	t.Run("noMatch when histories exist in window but zero weapons match", func(t *testing.T) {
		store := newMemSnapshotStore(api.CharacterSnapshot{ID: "snap-1", UserID: "u1", CharacterID: "c1"})
		store.histories["snap-1"] = []History{
			{ID: "h-1", ParentID: "snap-1", UserID: "u1", CharacterID: "c1", Timestamp: basePeriod.Add(-5 * time.Minute), Meta: MetaData{KineticID: "100", EnergyID: "200", PowerID: "300"}},
		}
		svc := &service{store: store}
		snap, link, err := svc.FindBestFit(ctx, "u1", "c1", basePeriod, makeWeaponsMap(999))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if snap != nil {
			t.Fatalf("expected nil snap, got %+v", snap)
		}
		if link == nil || link.ConfidenceLevel != api.NoMatchConfidenceLevel || link.SnapshotID != nil {
			t.Fatalf("unexpected link: %+v", link)
		}
	})

	t.Run("confidence levels low (power=1), medium (kinetic=2 or kinetic+power=3), high (kinetic+energy=4)", func(t *testing.T) {
		store := newMemSnapshotStore(api.CharacterSnapshot{ID: "snap-1", UserID: "u1", CharacterID: "c1"})
		store.histories["snap-1"] = []History{
			{ID: "h-1", ParentID: "snap-1", UserID: "u1", CharacterID: "c1", Timestamp: basePeriod, Meta: MetaData{KineticID: "100", EnergyID: "200", PowerID: "300"}},
		}
		svc := &service{store: store}

		cases := []struct {
			name    string
			weapons []int64
			want    api.ConfidenceLevel
		}{
			{"power only -> low", []int64{300}, api.LowConfidenceLevel},
			{"kinetic only -> medium", []int64{100}, api.MediumConfidenceLevel},
			{"energy + power -> medium", []int64{200, 300}, api.MediumConfidenceLevel},
			{"kinetic + energy -> high", []int64{100, 200}, api.HighConfidenceLevel},
			{"all three -> high", []int64{100, 200, 300}, api.HighConfidenceLevel},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				snap, link, err := svc.FindBestFit(ctx, "u1", "c1", basePeriod, makeWeaponsMap(tc.weapons...))
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if snap == nil || snap.ID != "snap-1" {
					t.Fatalf("expected snap-1, got %+v", snap)
				}
				if link == nil || link.ConfidenceLevel != tc.want || link.SnapshotID == nil || *link.SnapshotID != "snap-1" || link.OriginalSnapshotID != nil {
					t.Fatalf("expected confidence %s snap-1 with nil OriginalSnapshotID, got %+v", tc.want, link)
				}
			})
		}
	})

	t.Run("tie-breaks equal scores by closest absolute timestamp distance to activityPeriod", func(t *testing.T) {
		store := newMemSnapshotStore(
			api.CharacterSnapshot{ID: "snap-far", UserID: "u1", CharacterID: "c1"},
			api.CharacterSnapshot{ID: "snap-close", UserID: "u1", CharacterID: "c1"},
		)
		store.histories["snap-far"] = []History{
			{ID: "h-far", ParentID: "snap-far", UserID: "u1", CharacterID: "c1", Timestamp: basePeriod.Add(10 * time.Minute), Meta: MetaData{KineticID: "100", EnergyID: "200"}},
		}
		store.histories["snap-close"] = []History{
			{ID: "h-close", ParentID: "snap-close", UserID: "u1", CharacterID: "c1", Timestamp: basePeriod.Add(-2 * time.Minute), Meta: MetaData{KineticID: "100", EnergyID: "200"}},
		}
		svc := &service{store: store}
		snap, link, err := svc.FindBestFit(ctx, "u1", "c1", basePeriod, makeWeaponsMap(100, 200))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if snap == nil || snap.ID != "snap-close" || link.SnapshotID == nil || *link.SnapshotID != "snap-close" {
			t.Fatalf("expected snap-close on timestamp tie-break, got snap=%+v link=%+v", snap, link)
		}
	})

	t.Run("auto-forwards archived matched snapshot to merged target and preserves OriginalSnapshotID", func(t *testing.T) {
		targetID := "snap-target"
		store := newMemSnapshotStore(
			api.CharacterSnapshot{ID: "snap-archived", UserID: "u1", CharacterID: "c1", MergedIntoID: &targetID},
			api.CharacterSnapshot{ID: targetID, UserID: "u1", CharacterID: "c1"},
		)
		store.histories["snap-archived"] = []History{
			{ID: "h-1", ParentID: "snap-archived", UserID: "u1", CharacterID: "c1", Timestamp: basePeriod, Meta: MetaData{KineticID: "100", EnergyID: "200"}},
		}
		svc := &service{store: store}
		snap, link, err := svc.FindBestFit(ctx, "u1", "c1", basePeriod, makeWeaponsMap(100, 200))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if snap == nil || snap.ID != targetID {
			t.Fatalf("expected auto-forwarded snap %q, got %+v", targetID, snap)
		}
		if link == nil || link.SnapshotID == nil || *link.SnapshotID != targetID || link.ConfidenceLevel != api.HighConfidenceLevel {
			t.Fatalf("expected link pointing to %q with high confidence, got %+v", targetID, link)
		}
		if link.OriginalSnapshotID == nil || *link.OriginalSnapshotID != "snap-archived" {
			t.Fatalf("expected OriginalSnapshotID='snap-archived', got %v", link.OriginalSnapshotID)
		}
	})

	t.Run("restores archived snapshot when auto-forward target is missing", func(t *testing.T) {
		missingTarget := "snap-gone"
		store := newMemSnapshotStore(
			api.CharacterSnapshot{ID: "snap-archived", UserID: "u1", CharacterID: "c1", MergedIntoID: &missingTarget},
		)
		store.histories["snap-archived"] = []History{
			{ID: "h-1", ParentID: "snap-archived", UserID: "u1", CharacterID: "c1", Timestamp: basePeriod, Meta: MetaData{KineticID: "100", EnergyID: "200"}},
		}
		svc := &service{store: store}
		snap, link, err := svc.FindBestFit(ctx, "u1", "c1", basePeriod, makeWeaponsMap(100, 200))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if snap == nil || snap.ID != "snap-archived" || IsArchived(snap) {
			t.Fatalf("expected restored snap-archived, got %+v", snap)
		}
		if link == nil || link.SnapshotID == nil || *link.SnapshotID != "snap-archived" || link.OriginalSnapshotID != nil {
			t.Fatalf("expected link to snap-archived with nil OriginalSnapshotID, got %+v", link)
		}
	})
}

func TestCreate_LegacyServerTickHashFallback(t *testing.T) {
	ctx := context.Background()

	loadout := api.Loadout{
		inventory.KineticKey: {
			InstanceID: "6917529027641081234",
			ItemHash:   123456789,
			ItemProperties: api.ItemProperties{
				BaseInfo: api.BaseItemInfo{
					InstanceId: "6917529027641081234",
					ItemHash:   123456789,
				},
				Perks: []api.Perk{{Hash: 999111, Name: "Opening Shot"}},
			},
		},
	}

	canonicalHash, err := GenerateHash(loadout)
	if err != nil {
		t.Fatalf("GenerateHash failed: %v", err)
	}
	legacyHash, err := GenerateLegacyHash(loadout)
	if err != nil {
		t.Fatalf("GenerateLegacyHash failed: %v", err)
	}
	if canonicalHash == legacyHash {
		t.Fatalf("expected canonical and legacy hashes to differ for perked loadout, both=%s", canonicalHash)
	}

	t.Run("reuses existing server-tick legacy snapshot and backfills armorSetBonuses without duplicating", func(t *testing.T) {
		store := newMemSnapshotStore(api.CharacterSnapshot{
			ID:          "snap-legacy",
			UserID:      "u1",
			CharacterID: "c1",
			Hash:        legacyHash,
			Loadout:     loadout,
		})
		svc := &service{store: store}

		bonuses := []api.ArmorSetBonus{{SetHash: 500001, SetName: "Techsec", ActiveCount: 2}}
		id, err := svc.create(ctx, "u1", api.CharacterSnapshot{
			UserID:          "u1",
			CharacterID:     "c1",
			Loadout:         loadout,
			ArmorSetBonuses: &bonuses,
		})
		if err != nil {
			t.Fatalf("create failed: %v", err)
		}
		if id == nil || *id != "snap-legacy" {
			t.Fatalf("expected existing legacy snapshot ID 'snap-legacy', got %v", id)
		}
		if len(store.docs) != 1 {
			t.Fatalf("expected no duplicate snapshot created, got %d docs", len(store.docs))
		}
		if got := store.docs["snap-legacy"]; got.ArmorSetBonuses == nil || len(*got.ArmorSetBonuses) != 1 {
			t.Fatalf("expected armorSetBonuses backfilled on legacy snapshot, got %+v", got.ArmorSetBonuses)
		}
		if len(store.histories["snap-legacy"]) != 1 {
			t.Fatalf("expected 1 history entry recorded on snap-legacy, got %d", len(store.histories["snap-legacy"]))
		}
	})

	t.Run("auto-forwards archived server-tick legacy snapshot to merged target", func(t *testing.T) {
		targetID := "snap-target"
		store := newMemSnapshotStore(
			api.CharacterSnapshot{
				ID:           "snap-legacy-archived",
				UserID:       "u1",
				CharacterID:  "c1",
				Hash:         legacyHash,
				MergedIntoID: &targetID,
				Loadout:      loadout,
			},
			api.CharacterSnapshot{
				ID:          targetID,
				UserID:      "u1",
				CharacterID: "c1",
				Hash:        "some-other-hash",
				Loadout:     loadout,
			},
		)
		svc := &service{store: store}

		id, err := svc.create(ctx, "u1", api.CharacterSnapshot{
			UserID:      "u1",
			CharacterID: "c1",
			Loadout:     loadout,
		})
		if err != nil {
			t.Fatalf("create failed: %v", err)
		}
		if id == nil || *id != targetID {
			t.Fatalf("expected auto-forwarded target %q, got %v", targetID, id)
		}
		if len(store.docs) != 2 {
			t.Fatalf("expected no duplicate snapshot created, got %d docs", len(store.docs))
		}
		if len(store.histories[targetID]) != 1 {
			t.Fatalf("expected 1 history entry on target %q, got %d", targetID, len(store.histories[targetID]))
		}
	})

	t.Run("prefers canonical hash snapshot when both canonical and legacy snapshots exist", func(t *testing.T) {
		store := newMemSnapshotStore(
			api.CharacterSnapshot{ID: "snap-legacy", UserID: "u1", CharacterID: "c1", Hash: legacyHash, Loadout: loadout},
			api.CharacterSnapshot{ID: "snap-canonical", UserID: "u1", CharacterID: "c1", Hash: canonicalHash, Loadout: loadout},
		)
		svc := &service{store: store}

		id, err := svc.create(ctx, "u1", api.CharacterSnapshot{
			UserID:      "u1",
			CharacterID: "c1",
			Loadout:     loadout,
		})
		if err != nil {
			t.Fatalf("create failed: %v", err)
		}
		if id == nil || *id != "snap-canonical" {
			t.Fatalf("expected canonical snapshot 'snap-canonical', got %v", id)
		}
	})
}

func TestSaveSessionCheckIn_ThrottlingAndMultiHourFindBestFit(t *testing.T) {
	makeLoadoutWithKinetic := func(kineticInstanceID string, kineticItemHash int64, perkHash int64) api.Loadout {
		return api.Loadout{
			inventory.KineticKey: {
				ItemHash:   kineticItemHash,
				InstanceID: kineticInstanceID,
				BucketHash: new(int64(inventory.Kinetic)),
				ItemProperties: api.ItemProperties{
					BaseInfo: api.BaseItemInfo{
						InstanceId: kineticInstanceID,
						ItemHash:   kineticItemHash,
					},
					Perks: []api.Perk{{Hash: perkHash, Name: "Perk"}},
				},
			},
			inventory.EnergyKey: {
				ItemHash:   2002,
				InstanceID: "en-inst-1",
				BucketHash: new(int64(inventory.Energy)),
				ItemProperties: api.ItemProperties{
					BaseInfo: api.BaseItemInfo{
						InstanceId: "en-inst-1",
						ItemHash:   2002,
					},
				},
			},
			inventory.PowerKey: {
				ItemHash:   3003,
				InstanceID: "pw-inst-1",
				BucketHash: new(int64(inventory.Power)),
				ItemProperties: api.ItemProperties{
					BaseInfo: api.BaseItemInfo{
						InstanceId: "pw-inst-1",
						ItemHash:   3003,
					},
				},
			},
		}
	}

	t.Run("throttles unchanged loadout within 30m, emits 30m heartbeat, and preserves FindBestFit >1h into session", func(t *testing.T) {
		start := time.Date(2026, 5, 1, 14, 0, 0, 0, time.UTC)
		now := start
		loadoutA := makeLoadoutWithKinetic("kin-inst-1", 1001, 501)

		store := newMemSnapshotStore()
		d2Fake := &fakeSnapshotD2Service{
			loadout: loadoutA,
			stats:   map[string]api.ClassStat{},
			ts:      &now,
		}
		svc := &service{
			store:       store,
			UserService: &fakeSnapshotUserService{membershipType: 3},
			D2Service:   d2Fake,
			nowFn:       func() time.Time { return now },
		}

		ctx := context.Background()
		opts := SessionCheckInOptions{HeartbeatInterval: 30 * time.Minute}
		recordedTicks := 0

		// Simulate 5-minute ticks over 90 minutes with unchanged loadout (19 ticks total).
		for step := 0; step <= 90; step += 5 {
			now = start.Add(time.Duration(step) * time.Minute)
			res, err := svc.SaveSessionCheckIn(ctx, "user-1", "4611686018467484863", "char-1", opts)
			if err != nil {
				t.Fatalf("step %dm SaveSessionCheckIn failed: %v", step, err)
			}
			expectWrite := step%30 == 0
			if res.HistoryRecorded != expectWrite {
				t.Fatalf("step %dm: expected HistoryRecorded=%v, got %v", step, expectWrite, res.HistoryRecorded)
			}
			if res.HistoryRecorded {
				recordedTicks++
				opts.LastSnapshotID = res.SnapshotID
				opts.LastSnapshotHash = res.SnapshotHash
				opts.LastSnapshotRecordedAt = res.RecordedAt
			}
		}

		// Expect writes only at 0m, 30m, 60m, 90m (4 writes instead of 19).
		if recordedTicks != 4 {
			t.Fatalf("expected 4 heartbeat history writes across 90m, got %d", recordedTicks)
		}
		if len(store.histories[opts.LastSnapshotID]) != 4 {
			t.Fatalf("expected 4 stored history entries, got %d", len(store.histories[opts.LastSnapshotID]))
		}
		if got := store.docs[opts.LastSnapshotID].UpdatedAt; !got.Equal(start.Add(90 * time.Minute)) {
			t.Fatalf("expected UpdatedAt to match last heartbeat (90m), got %v", got)
		}

		// Match played 85 minutes into the session (>1h after start) must still match with High confidence.
		matchPeriod := start.Add(85 * time.Minute)
		bestSnap, link, err := svc.FindBestFit(ctx, "user-1", "char-1", matchPeriod, makeWeaponsMap(1001, 2002))
		if err != nil {
			t.Fatalf("FindBestFit failed: %v", err)
		}
		if bestSnap == nil || bestSnap.ID != opts.LastSnapshotID {
			t.Fatalf("expected FindBestFit to match %q, got %+v", opts.LastSnapshotID, bestSnap)
		}
		if link == nil || link.ConfidenceLevel != api.HighConfidenceLevel {
			t.Fatalf("expected High confidence match >1h into session, got %+v", link)
		}
	})

	t.Run("writes history immediately when loadout hash changes within <30m", func(t *testing.T) {
		start := time.Date(2026, 5, 1, 14, 0, 0, 0, time.UTC)
		now := start
		loadoutA := makeLoadoutWithKinetic("kin-inst-1", 1001, 501)
		loadoutB := makeLoadoutWithKinetic("kin-inst-1", 1001, 999) // perk swap changes hash

		store := newMemSnapshotStore()
		d2Fake := &fakeSnapshotD2Service{
			loadout: loadoutA,
			stats:   map[string]api.ClassStat{},
			ts:      &now,
		}
		svc := &service{
			store:       store,
			UserService: &fakeSnapshotUserService{membershipType: 3},
			D2Service:   d2Fake,
			nowFn:       func() time.Time { return now },
		}

		ctx := context.Background()
		res1, err := svc.SaveSessionCheckIn(ctx, "user-1", "4611686018467484863", "char-1", SessionCheckInOptions{
			HeartbeatInterval: 30 * time.Minute,
		})
		if err != nil || !res1.HistoryRecorded {
			t.Fatalf("initial check-in failed: res=%+v err=%v", res1, err)
		}

		// 5m later, unchanged loadout -> skipped and snapshot UpdatedAt untouched.
		now = start.Add(5 * time.Minute)
		res2, err := svc.SaveSessionCheckIn(ctx, "user-1", "4611686018467484863", "char-1", SessionCheckInOptions{
			LastSnapshotID:         res1.SnapshotID,
			LastSnapshotHash:       res1.SnapshotHash,
			LastSnapshotRecordedAt: res1.RecordedAt,
			HeartbeatInterval:      30 * time.Minute,
		})
		if err != nil {
			t.Fatalf("second check-in failed: %v", err)
		}
		if res2.HistoryRecorded {
			t.Fatal("expected unchanged check-in at +5m to skip history write")
		}
		if !store.docs[res1.SnapshotID].UpdatedAt.Equal(start) {
			t.Fatalf("expected snapshot UpdatedAt to remain %v when skipped, got %v", start, store.docs[res1.SnapshotID].UpdatedAt)
		}

		// 10m after start, player swaps a perk -> must immediately record new snapshot + history.
		now = start.Add(10 * time.Minute)
		d2Fake.loadout = loadoutB
		res3, err := svc.SaveSessionCheckIn(ctx, "user-1", "4611686018467484863", "char-1", SessionCheckInOptions{
			LastSnapshotID:         res1.SnapshotID,
			LastSnapshotHash:       res1.SnapshotHash,
			LastSnapshotRecordedAt: res1.RecordedAt,
			HeartbeatInterval:      30 * time.Minute,
		})
		if err != nil {
			t.Fatalf("perk-swap check-in failed: %v", err)
		}
		if !res3.HistoryRecorded {
			t.Fatal("expected immediate history write upon perk/hash change")
		}
		if res3.SnapshotID == res1.SnapshotID || res3.SnapshotHash == res1.SnapshotHash {
			t.Fatalf("expected distinct snapshot after perk change, got old=(%s,%s) new=(%s,%s)", res1.SnapshotID, res1.SnapshotHash, res3.SnapshotID, res3.SnapshotHash)
		}
		if len(store.histories[res3.SnapshotID]) != 1 {
			t.Fatalf("expected 1 history entry for new snapshot %q, got %d", res3.SnapshotID, len(store.histories[res3.SnapshotID]))
		}
	})

	t.Run("reuses pre-write timestamp for RecordedAt so create latency does not drift heartbeat boundary", func(t *testing.T) {
		start := time.Date(2026, 5, 1, 14, 0, 0, 0, time.UTC)
		loadoutA := makeLoadoutWithKinetic("kin-inst-1", 1001, 501)

		store := newMemSnapshotStore()
		d2Fake := &fakeSnapshotD2Service{
			loadout: loadoutA,
			stats:   map[string]api.ClassStat{},
			ts:      &start,
		}
		calls := 0
		svc := &service{
			store:       store,
			UserService: &fakeSnapshotUserService{membershipType: 3},
			D2Service:   d2Fake,
			nowFn: func() time.Time {
				// Simulate 200ms elapsing on each clock read during s.create / createHistoryEntry.
				ts := start.Add(time.Duration(calls) * 200 * time.Millisecond)
				calls++
				return ts
			},
		}

		ctx := context.Background()
		res1, err := svc.SaveSessionCheckIn(ctx, "user-1", "4611686018467484863", "char-1", SessionCheckInOptions{
			HeartbeatInterval: 30 * time.Minute,
		})
		if err != nil || !res1.HistoryRecorded {
			t.Fatalf("initial check-in failed: res=%+v err=%v", res1, err)
		}
		if !res1.RecordedAt.Equal(start) {
			t.Fatalf("expected RecordedAt to use pre-write timestamp %v, got %v", start, res1.RecordedAt)
		}

		// Exactly 30m later must trigger heartbeat rather than skipping due to write latency.
		svc.nowFn = func() time.Time { return start.Add(30 * time.Minute) }
		res2, err := svc.SaveSessionCheckIn(ctx, "user-1", "4611686018467484863", "char-1", SessionCheckInOptions{
			LastSnapshotID:         res1.SnapshotID,
			LastSnapshotHash:       res1.SnapshotHash,
			LastSnapshotRecordedAt: res1.RecordedAt,
			HeartbeatInterval:      30 * time.Minute,
		})
		if err != nil {
			t.Fatalf("heartbeat check-in failed: %v", err)
		}
		if !res2.HistoryRecorded {
			t.Fatal("expected heartbeat check-in at exactly +30m to record history")
		}
	})

	t.Run("skips GetMembershipType when MembershipType is provided in options", func(t *testing.T) {
		start := time.Date(2026, 5, 1, 14, 0, 0, 0, time.UTC)
		loadoutA := makeLoadoutWithKinetic("kin-inst-1", 1001, 501)
		store := newMemSnapshotStore()
		userFake := &fakeSnapshotUserService{membershipType: 1}
		d2Fake := &fakeSnapshotD2Service{
			loadout: loadoutA,
			stats:   map[string]api.ClassStat{},
			ts:      &start,
		}
		svc := &service{
			store:       store,
			UserService: userFake,
			D2Service:   d2Fake,
			nowFn:       func() time.Time { return start },
		}

		res, err := svc.SaveSessionCheckIn(context.Background(), "user-1", "4611686018467484863", "char-1", SessionCheckInOptions{
			MembershipType:    3,
			HeartbeatInterval: 30 * time.Minute,
		})
		if err != nil {
			t.Fatalf("SaveSessionCheckIn with MembershipType failed: %v", err)
		}
		if !res.HistoryRecorded {
			t.Fatal("expected initial check-in to record history")
		}
		if userFake.calls != 0 {
			t.Fatalf("expected 0 GetMembershipType calls when MembershipType is provided, got %d", userFake.calls)
		}
		if d2Fake.lastMembershipType != 3 {
			t.Fatalf("expected D2Service.GetLoadout to receive membershipType=3, got %d", d2Fake.lastMembershipType)
		}

		// Zero MembershipType still falls back to UserService.GetMembershipType.
		if _, err := svc.SaveSessionCheckIn(context.Background(), "user-1", "4611686018467484863", "char-1", SessionCheckInOptions{
			HeartbeatInterval: 30 * time.Minute,
		}); err != nil {
			t.Fatalf("SaveSessionCheckIn without MembershipType failed: %v", err)
		}
		if userFake.calls != 1 {
			t.Fatalf("expected 1 GetMembershipType fallback call when MembershipType is 0, got %d", userFake.calls)
		}
		if d2Fake.lastMembershipType != 1 {
			t.Fatalf("expected D2Service.GetLoadout to receive fallback membershipType=1, got %d", d2Fake.lastMembershipType)
		}
	})
}
