package cache

import (
	"context"
	"oneTrick/core/domain"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
)

type spyManifestReader struct {
	ManifestReader
	classesCalls           atomic.Int32
	racesCalls             atomic.Int32
	modesCalls             atomic.Int32
	perksCalls             atomic.Int32
	equipableItemSetsCalls atomic.Int32
	recordCalls            atomic.Int32
	itemCalls              atomic.Int32
	activityCalls          atomic.Int32
	activityModeCalls      atomic.Int32
	progressionsCalls      atomic.Int32
	progressionCalls       atomic.Int32

	mu                      sync.Mutex
	itemsByIDsRequests      [][]int64
	perksByIDsRequests      [][]int64
	activitiesByIDsRequests [][]int64
	activityModesByIDsReqs  [][]int64
	itemsStore              map[int64]domain.ItemManifest
	perksStore              map[int64]domain.PerkManifest
	activitiesStore         map[int64]domain.ActivityManifest
	activityModesStore      map[int64]domain.ActivityModeManifest
}

func (s *spyManifestReader) GetClasses(_ context.Context) (map[int64]domain.ClassManifest, error) {
	s.classesCalls.Add(1)
	return map[int64]domain.ClassManifest{671679327: {Hash: 671679327, Name: "Hunter"}}, nil
}

func (s *spyManifestReader) GetRaces(_ context.Context) (map[int64]domain.RaceManifest, error) {
	s.racesCalls.Add(1)
	return map[int64]domain.RaceManifest{3887404748: {Hash: 3887404748, Name: "Exo"}}, nil
}

func (s *spyManifestReader) GetActivityModes(_ context.Context) (map[int64]domain.ActivityModeManifest, error) {
	s.modesCalls.Add(1)
	if s.activityModesStore != nil {
		return s.activityModesStore, nil
	}
	return map[int64]domain.ActivityModeManifest{84: {Hash: 84, ModeType: 84, Name: "Trials of Osiris", IconURL: "https://www.bungie.net/icons/trials.png"}}, nil
}

func (s *spyManifestReader) GetPerks(_ context.Context) (map[int64]domain.PerkManifest, error) {
	s.perksCalls.Add(1)
	return map[int64]domain.PerkManifest{900001: {Hash: 900001, Name: "Set Bonus Perk"}}, nil
}

func (s *spyManifestReader) GetEquipableItemSets(_ context.Context) (map[int64]domain.EquipableItemSetManifest, error) {
	s.equipableItemSetsCalls.Add(1)
	return map[int64]domain.EquipableItemSetManifest{500001: {Hash: 500001, Name: "Techsec Armor"}}, nil
}

func (s *spyManifestReader) GetProgressions(_ context.Context) (map[int64]domain.ProgressionManifest, error) {
	s.progressionsCalls.Add(1)
	return map[int64]domain.ProgressionManifest{
		2083746873: {Hash: 2083746873, Steps: []domain.ProgressionStep{{StepName: "GUARDIAN I", ProgressTotal: 50}}},
	}, nil
}

func (s *spyManifestReader) GetRecord(_ context.Context, hash int64) (domain.RecordManifest, bool, error) {
	s.recordCalls.Add(1)
	return domain.RecordManifest{Hash: hash}, true, nil
}

func (s *spyManifestReader) GetItem(_ context.Context, hash int64) (domain.ItemManifest, bool, error) {
	s.itemCalls.Add(1)
	if s.itemsStore != nil {
		def, ok := s.itemsStore[hash]
		return def, ok, nil
	}
	return domain.ItemManifest{Hash: hash}, true, nil
}

func (s *spyManifestReader) GetActivity(_ context.Context, hash int64) (domain.ActivityManifest, bool, error) {
	s.activityCalls.Add(1)
	if s.activitiesStore != nil {
		def, ok := s.activitiesStore[hash]
		return def, ok, nil
	}
	return domain.ActivityManifest{Hash: hash}, true, nil
}

func (s *spyManifestReader) GetActivityModeByHash(_ context.Context, hash int64) (domain.ActivityModeManifest, bool, error) {
	s.activityModeCalls.Add(1)
	if s.activityModesStore != nil {
		def, ok := s.activityModesStore[hash]
		return def, ok, nil
	}
	return domain.ActivityModeManifest{Hash: hash}, true, nil
}

func (s *spyManifestReader) GetProgression(_ context.Context, hash int64) (domain.ProgressionManifest, bool, error) {
	s.progressionCalls.Add(1)
	return domain.ProgressionManifest{Hash: hash, Steps: []domain.ProgressionStep{{StepName: "Copper III", ProgressTotal: 374}}}, true, nil
}

func (s *spyManifestReader) GetItemsByIDs(_ context.Context, ids []int64) (map[int64]domain.ItemManifest, error) {
	s.mu.Lock()
	s.itemsByIDsRequests = append(s.itemsByIDsRequests, slices.Clone(ids))
	s.mu.Unlock()
	res := make(map[int64]domain.ItemManifest, len(ids))
	for _, id := range ids {
		if s.itemsStore != nil {
			if def, ok := s.itemsStore[id]; ok {
				res[id] = def
			}
			continue
		}
		res[id] = domain.ItemManifest{Hash: id}
	}
	return res, nil
}

func (s *spyManifestReader) GetPerksByIDs(_ context.Context, ids []int64) (map[int64]domain.PerkManifest, error) {
	s.mu.Lock()
	s.perksByIDsRequests = append(s.perksByIDsRequests, slices.Clone(ids))
	s.mu.Unlock()
	res := make(map[int64]domain.PerkManifest, len(ids))
	for _, id := range ids {
		if s.perksStore != nil {
			if def, ok := s.perksStore[id]; ok {
				res[id] = def
			}
			continue
		}
		res[id] = domain.PerkManifest{Hash: id}
	}
	return res, nil
}

func (s *spyManifestReader) GetActivitiesByIDs(_ context.Context, ids []int64) (map[int64]domain.ActivityManifest, error) {
	s.mu.Lock()
	s.activitiesByIDsRequests = append(s.activitiesByIDsRequests, slices.Clone(ids))
	s.mu.Unlock()
	res := make(map[int64]domain.ActivityManifest, len(ids))
	for _, id := range ids {
		if s.activitiesStore != nil {
			if def, ok := s.activitiesStore[id]; ok {
				res[id] = def
			}
			continue
		}
		res[id] = domain.ActivityManifest{Hash: id}
	}
	return res, nil
}

func (s *spyManifestReader) GetActivityModesByIDs(_ context.Context, ids []int64) (map[int64]domain.ActivityModeManifest, error) {
	s.mu.Lock()
	s.activityModesByIDsReqs = append(s.activityModesByIDsReqs, slices.Clone(ids))
	s.mu.Unlock()
	res := make(map[int64]domain.ActivityModeManifest, len(ids))
	for _, id := range ids {
		if s.activityModesStore != nil {
			if def, ok := s.activityModesStore[id]; ok {
				res[id] = def
			}
			continue
		}
		res[id] = domain.ActivityModeManifest{Hash: id}
	}
	return res, nil
}

func TestManifestCache_CollectionAndSingleHashCaching(t *testing.T) {
	ctx := context.Background()
	spy := &spyManifestReader{}
	c := NewManifestCache(spy)

	for range 3 {
		if classes, err := c.GetClasses(ctx); err != nil || len(classes) != 1 {
			t.Fatalf("expected 1 cached class, got %d err=%v", len(classes), err)
		}
		if races, err := c.GetRaces(ctx); err != nil || len(races) != 1 {
			t.Fatalf("expected 1 cached race, got %d err=%v", len(races), err)
		}
		if perks, err := c.GetPerks(ctx); err != nil || len(perks) != 1 {
			t.Fatalf("expected 1 cached perk, got %d err=%v", len(perks), err)
		}
		if itemSets, err := c.GetEquipableItemSets(ctx); err != nil || len(itemSets) != 1 {
			t.Fatalf("expected 1 cached item set, got %d err=%v", len(itemSets), err)
		}
	}
	if spy.classesCalls.Load() != 1 || spy.racesCalls.Load() != 1 || spy.perksCalls.Load() != 1 || spy.equipableItemSetsCalls.Load() != 1 {
		t.Fatalf("expected each collection loaded once")
	}

	for range 3 {
		if rec, ok, err := c.GetRecord(ctx, 3464275895); err != nil || !ok || rec.Hash != 3464275895 {
			t.Fatalf("expected record 3464275895, got %+v ok=%v err=%v", rec, ok, err)
		}
		if item, ok, err := c.GetItem(ctx, 1001); err != nil || !ok || item.Hash != 1001 {
			t.Fatalf("expected item 1001, got %+v ok=%v err=%v", item, ok, err)
		}
		if act, ok, err := c.GetActivity(ctx, 2002); err != nil || !ok || act.Hash != 2002 {
			t.Fatalf("expected activity 2002, got %+v ok=%v err=%v", act, ok, err)
		}
		if prog, ok, err := c.GetProgression(ctx, 3696598664); err != nil || !ok || prog.Hash != 3696598664 {
			t.Fatalf("expected progression, got %+v ok=%v err=%v", prog, ok, err)
		}
	}
	if spy.recordCalls.Load() != 1 || spy.itemCalls.Load() != 1 || spy.activityCalls.Load() != 1 || spy.progressionCalls.Load() != 1 {
		t.Fatalf("expected each single-hash lookup called once")
	}

	// Collection cache fast-path for GetActivityModeByHash and GetProgression
	if _, err := c.GetActivityModes(ctx); err != nil {
		t.Fatalf("GetActivityModes failed: %v", err)
	}
	mode, ok, err := c.GetActivityModeByHash(ctx, 84)
	if err != nil || !ok || mode.Hash != 84 {
		t.Fatalf("expected mode 84, got %+v ok=%v err=%v", mode, ok, err)
	}
	if spy.activityModeCalls.Load() != 0 {
		t.Fatalf("expected GetActivityModeByHash(84) resolved from collection cache, got %d calls", spy.activityModeCalls.Load())
	}

	if _, err := c.GetProgressions(ctx); err != nil {
		t.Fatalf("GetProgressions failed: %v", err)
	}
	if prog, ok, err := c.GetProgression(ctx, 2083746873); err != nil || !ok || prog.Hash != 2083746873 {
		t.Fatalf("expected progression 2083746873, got %+v ok=%v err=%v", prog, ok, err)
	}
	if spy.progressionCalls.Load() != 1 {
		t.Fatalf("expected no extra inner GetProgression call, got %d", spy.progressionCalls.Load())
	}
}

func TestManifestCache_BatchedCachingAndModeTypeFallback(t *testing.T) {
	ctx := context.Background()
	spy := &spyManifestReader{
		activityModesStore: map[int64]domain.ActivityModeManifest{
			1164760504: {Hash: 1164760504, ModeType: 84, Name: "Trials of Osiris", IconURL: "https://www.bungie.net/icons/trials.png"},
		},
	}
	c := NewManifestCache(spy)

	empty, err := c.GetItemsByIDs(ctx, []int64{0, 0})
	if err != nil || len(empty) != 0 || len(spy.itemsByIDsRequests) != 0 {
		t.Fatalf("expected 0 inner calls for zero IDs")
	}

	items, err := c.GetItemsByIDs(ctx, []int64{101, 102, 101, 0})
	if err != nil || len(items) != 2 {
		t.Fatalf("expected 2 items, got %d err=%v", len(items), err)
	}
	if len(spy.itemsByIDsRequests) != 1 || !slices.Equal(spy.itemsByIDsRequests[0], []int64{101, 102}) {
		t.Fatalf("unexpected requests: %+v", spy.itemsByIDsRequests)
	}

	// Partial hit fetches only [103]
	items3, err := c.GetItemsByIDs(ctx, []int64{101, 103})
	if err != nil || len(items3) != 2 || items3[103].Hash != 103 {
		t.Fatalf("expected 101 & 103, got %+v err=%v", items3, err)
	}
	if len(spy.itemsByIDsRequests) != 2 || !slices.Equal(spy.itemsByIDsRequests[1], []int64{103}) {
		t.Fatalf("expected [103], got %+v", spy.itemsByIDsRequests)
	}

	// Single GetItem reuses cache populated by GetItemsByIDs
	if single, ok, err := c.GetItem(ctx, 103); err != nil || !ok || single.Hash != 103 || spy.itemCalls.Load() != 0 {
		t.Fatalf("expected cached item 103 with 0 inner GetItem calls")
	}

	// ManifestLookup.GetActivityMode falls back to modeType when hash misses
	mode, ok, err := c.GetActivityMode(ctx, 9999999, 84)
	if err != nil || !ok || mode.Name != "Trials of Osiris" {
		t.Fatalf("expected fallback to modeType=84, got %+v ok=%v err=%v", mode, ok, err)
	}
}
