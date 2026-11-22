package destiny

import (
	"context"
	"net/http"
	"oneTrick/clients/bungie"
	"oneTrick/set"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type spyManifestService struct {
	ManifestService
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
	damageTypesCalls       atomic.Int32

	mu                        sync.Mutex
	itemsByIDsRequests        [][]int64
	perksByIDsRequests        [][]int64
	activitiesByIDsRequests   [][]int64
	activityModesByIDsReqs    [][]int64
	itemsStore                map[int64]ItemDefinition
	perksStore                map[int64]PerkDefinition
	activitiesStore           map[int64]ActivityDefinition
	activityModesStore        map[int64]ActivityModeDefinition
	equipableItemSetsStore    map[string]EquipableItemSetDefinition
}

func (s *spyManifestService) GetClasses(_ context.Context) (map[string]ClassDefinition, error) {
	s.classesCalls.Add(1)
	return map[string]ClassDefinition{"671679327": {Hash: 671679327}}, nil
}

func (s *spyManifestService) GetRaces(_ context.Context) (map[string]RaceDefinition, error) {
	s.racesCalls.Add(1)
	return map[string]RaceDefinition{"3887404748": {Hash: 3887404748}}, nil
}

func (s *spyManifestService) GetDamageTypes(_ context.Context) (map[string]DamageType, error) {
	s.damageTypesCalls.Add(1)
	return map[string]DamageType{}, nil
}

func (s *spyManifestService) GetActivityModes(_ context.Context) (map[string]ActivityModeDefinition, error) {
	s.modesCalls.Add(1)
	return map[string]ActivityModeDefinition{"84": {Hash: 84}}, nil
}

func (s *spyManifestService) GetPerks(_ context.Context) (map[string]PerkDefinition, error) {
	s.perksCalls.Add(1)
	return map[string]PerkDefinition{
		"900001": {Hash: 900001, DisplayProperties: DamageTypeDisplayProperties{Name: "Set Bonus Perk"}},
	}, nil
}

func (s *spyManifestService) GetEquipableItemSets(_ context.Context) (map[string]EquipableItemSetDefinition, error) {
	s.equipableItemSetsCalls.Add(1)
	if s.equipableItemSetsStore != nil {
		return s.equipableItemSetsStore, nil
	}
	return map[string]EquipableItemSetDefinition{
		"500001": {Hash: 500001, DisplayProperties: ItemDisplayProperties{Name: "Techsec Armor"}},
	}, nil
}

func (s *spyManifestService) GetProgressions(_ context.Context) (map[string]ProgressionDefinition, error) {
	s.progressionsCalls.Add(1)
	return map[string]ProgressionDefinition{
		"2083746873": {Hash: 2083746873, Steps: []ProgressionStepDefinition{{StepName: "GUARDIAN I", ProgressTotal: 50}}},
	}, nil
}

func (s *spyManifestService) GetRecord(_ context.Context, hash int64) (*RecordDefinition, error) {
	s.recordCalls.Add(1)
	return &RecordDefinition{Hash: int(hash)}, nil
}

func (s *spyManifestService) GetItem(_ context.Context, hash int64) (*ItemDefinition, error) {
	s.itemCalls.Add(1)
	if s.itemsStore != nil {
		if def, ok := s.itemsStore[hash]; ok {
			return &def, nil
		}
	}
	return &ItemDefinition{Hash: hash}, nil
}

func (s *spyManifestService) GetActivity(_ context.Context, hash int64) (*ActivityDefinition, error) {
	s.activityCalls.Add(1)
	if s.activitiesStore != nil {
		if def, ok := s.activitiesStore[hash]; ok {
			return &def, nil
		}
	}
	return &ActivityDefinition{Hash: int(hash)}, nil
}

func (s *spyManifestService) GetActivityMode(_ context.Context, hash int64) (*ActivityModeDefinition, error) {
	s.activityModeCalls.Add(1)
	if s.activityModesStore != nil {
		if def, ok := s.activityModesStore[hash]; ok {
			return &def, nil
		}
	}
	return &ActivityModeDefinition{Hash: hash}, nil
}

func (s *spyManifestService) GetProgression(_ context.Context, hash int64) (*ProgressionDefinition, error) {
	s.progressionCalls.Add(1)
	return &ProgressionDefinition{Hash: hash, Steps: []ProgressionStepDefinition{{StepName: "Copper III", ProgressTotal: 374}}}, nil
}

func (s *spyManifestService) GetItemsByIDs(_ context.Context, ids []int64) (map[string]ItemDefinition, error) {
	s.mu.Lock()
	s.itemsByIDsRequests = append(s.itemsByIDsRequests, slices.Clone(ids))
	s.mu.Unlock()
	res := make(map[string]ItemDefinition, len(ids))
	for _, id := range ids {
		if s.itemsStore != nil {
			if def, ok := s.itemsStore[id]; ok {
				res[strconv.FormatInt(id, 10)] = def
			}
			continue
		}
		res[strconv.FormatInt(id, 10)] = ItemDefinition{Hash: id}
	}
	return res, nil
}

func (s *spyManifestService) GetPerksByIDs(_ context.Context, ids []int64) (map[string]PerkDefinition, error) {
	s.mu.Lock()
	s.perksByIDsRequests = append(s.perksByIDsRequests, slices.Clone(ids))
	s.mu.Unlock()
	res := make(map[string]PerkDefinition, len(ids))
	for _, id := range ids {
		if s.perksStore != nil {
			if def, ok := s.perksStore[id]; ok {
				res[strconv.FormatInt(id, 10)] = def
			}
			continue
		}
		res[strconv.FormatInt(id, 10)] = PerkDefinition{Hash: id}
	}
	return res, nil
}

func (s *spyManifestService) GetActivitiesByIDs(_ context.Context, ids []int64) (map[string]ActivityDefinition, error) {
	s.mu.Lock()
	s.activitiesByIDsRequests = append(s.activitiesByIDsRequests, slices.Clone(ids))
	s.mu.Unlock()
	res := make(map[string]ActivityDefinition, len(ids))
	for _, id := range ids {
		if s.activitiesStore != nil {
			if def, ok := s.activitiesStore[id]; ok {
				res[strconv.FormatInt(id, 10)] = def
			}
			continue
		}
		res[strconv.FormatInt(id, 10)] = ActivityDefinition{Hash: int(id)}
	}
	return res, nil
}

func (s *spyManifestService) GetActivityModesByIDs(_ context.Context, ids []int64) (map[string]ActivityModeDefinition, error) {
	s.mu.Lock()
	s.activityModesByIDsReqs = append(s.activityModesByIDsReqs, slices.Clone(ids))
	s.mu.Unlock()
	res := make(map[string]ActivityModeDefinition, len(ids))
	for _, id := range ids {
		if s.activityModesStore != nil {
			if def, ok := s.activityModesStore[id]; ok {
				res[strconv.FormatInt(id, 10)] = def
			}
			continue
		}
		res[strconv.FormatInt(id, 10)] = ActivityModeDefinition{Hash: id}
	}
	return res, nil
}

func TestCachedManifestServiceWrapper(t *testing.T) {
	ctx := context.Background()
	spy := &spyManifestService{}
	svc := NewCachedManifestService(spy)

	// 1. Collection methods should call the inner service on first miss and serve from cache on subsequent calls
	for i := 0; i < 3; i++ {
		classes, err := svc.GetClasses(ctx)
		if err != nil || len(classes) != 1 {
			t.Fatalf("expected 1 cached class, got len=%d err=%v", len(classes), err)
		}
		races, err := svc.GetRaces(ctx)
		if err != nil || len(races) != 1 {
			t.Fatalf("expected 1 cached race, got len=%d err=%v", len(races), err)
		}
		perks, err := svc.GetPerks(ctx)
		if err != nil || len(perks) != 1 {
			t.Fatalf("expected 1 cached perk, got len=%d err=%v", len(perks), err)
		}
		itemSets, err := svc.GetEquipableItemSets(ctx)
		if err != nil || len(itemSets) != 1 {
			t.Fatalf("expected 1 cached equipable item set, got len=%d err=%v", len(itemSets), err)
		}
	}
	if spy.classesCalls.Load() != 1 {
		t.Fatalf("expected inner GetClasses called once, got %d", spy.classesCalls.Load())
	}
	if spy.racesCalls.Load() != 1 {
		t.Fatalf("expected inner GetRaces called once, got %d", spy.racesCalls.Load())
	}
	if spy.perksCalls.Load() != 1 {
		t.Fatalf("expected inner GetPerks called once, got %d", spy.perksCalls.Load())
	}
	if spy.equipableItemSetsCalls.Load() != 1 {
		t.Fatalf("expected inner GetEquipableItemSets called once, got %d", spy.equipableItemSetsCalls.Load())
	}

	// 2. Hash lookup methods should cache per hash and only call the inner service once per unique hash
	for i := 0; i < 3; i++ {
		rec, err := svc.GetRecord(ctx, 3464275895)
		if err != nil || rec == nil || rec.Hash != 3464275895 {
			t.Fatalf("expected record 3464275895, got %+v err=%v", rec, err)
		}
		item, err := svc.GetItem(ctx, 1001)
		if err != nil || item == nil || item.Hash != 1001 {
			t.Fatalf("expected item 1001, got %+v err=%v", item, err)
		}
		act, err := svc.GetActivity(ctx, 2002)
		if err != nil || act == nil || act.Hash != 2002 {
			t.Fatalf("expected activity 2002, got %+v err=%v", act, err)
		}
		prog, err := svc.GetProgression(ctx, CompProgressionHashInt64)
		if err != nil || prog == nil || prog.Hash != CompProgressionHashInt64 {
			t.Fatalf("expected progression %d, got %+v err=%v", CompProgressionHashInt64, prog, err)
		}
	}
	if spy.recordCalls.Load() != 1 {
		t.Fatalf("expected inner GetRecord called once for hash 3464275895, got %d", spy.recordCalls.Load())
	}
	if spy.itemCalls.Load() != 1 {
		t.Fatalf("expected inner GetItem called once for hash 1001, got %d", spy.itemCalls.Load())
	}
	if spy.activityCalls.Load() != 1 {
		t.Fatalf("expected inner GetActivity called once for hash 2002, got %d", spy.activityCalls.Load())
	}
	if spy.progressionCalls.Load() != 1 {
		t.Fatalf("expected inner GetProgression called once for hash %d, got %d", CompProgressionHashInt64, spy.progressionCalls.Load())
	}

	// 3. Once GetActivityModes / GetProgressions are cached, hash lookups resolve from the collection cache without calling inner single-doc methods
	if _, err := svc.GetActivityModes(ctx); err != nil {
		t.Fatalf("GetActivityModes failed: %v", err)
	}
	mode, err := svc.GetActivityMode(ctx, 84)
	if err != nil || mode == nil || mode.Hash != 84 {
		t.Fatalf("expected activity mode 84, got %+v err=%v", mode, err)
	}
	if spy.activityModeCalls.Load() != 0 {
		t.Fatalf("expected GetActivityMode(84) to resolve from cached activityModes without calling inner GetActivityMode, got %d calls", spy.activityModeCalls.Load())
	}

	if _, err := svc.GetProgressions(ctx); err != nil {
		t.Fatalf("GetProgressions failed: %v", err)
	}
	crucibleProg, err := svc.GetProgression(ctx, 2083746873)
	if err != nil || crucibleProg == nil || crucibleProg.Hash != 2083746873 {
		t.Fatalf("expected progression 2083746873, got %+v err=%v", crucibleProg, err)
	}
	if spy.progressionCalls.Load() != 1 {
		t.Fatalf("expected GetProgression(2083746873) to resolve from cached progressions without additional inner GetProgression call, got %d calls", spy.progressionCalls.Load())
	}
}

func TestNormalizeAndChunkIDs(t *testing.T) {
	got := normalizeUniqueIDs([]int64{0, 10, 20, 10, 0, 30})
	if !slices.Equal(got, []int64{10, 20, 30}) {
		t.Fatalf("unexpected normalized IDs: %v", got)
	}
	if len(normalizeUniqueIDs([]int64{0, 0})) != 0 {
		t.Fatalf("expected empty slice for all-zero IDs")
	}

	ids := make([]int64, 65)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	chunks := chunkIDs(ids, manifestBatchSize)
	if len(chunks) != 3 || len(chunks[0]) != 30 || len(chunks[1]) != 30 || len(chunks[2]) != 5 {
		t.Fatalf("expected chunks of [30, 30, 5], got lens [%d, %d, %d]", len(chunks[0]), len(chunks[1]), len(chunks[2]))
	}
}

func TestCachedManifestService_BatchedCachingAndSharing(t *testing.T) {
	ctx := context.Background()
	spy := &spyManifestService{}
	svc := NewCachedManifestService(spy)

	// Empty and zero-only IDs never call inner service
	empty, err := svc.GetItemsByIDs(ctx, []int64{0, 0})
	if err != nil || len(empty) != 0 {
		t.Fatalf("expected empty map for zero IDs, got %+v err=%v", empty, err)
	}
	if len(spy.itemsByIDsRequests) != 0 {
		t.Fatalf("expected 0 inner GetItemsByIDs calls for zero IDs, got %d", len(spy.itemsByIDsRequests))
	}

	// Cold batch call deduplicates and caches
	items, err := svc.GetItemsByIDs(ctx, []int64{101, 102, 101, 0})
	if err != nil || len(items) != 2 {
		t.Fatalf("expected 2 items, got %d err=%v", len(items), err)
	}
	if len(spy.itemsByIDsRequests) != 1 || !slices.Equal(spy.itemsByIDsRequests[0], []int64{101, 102}) {
		t.Fatalf("unexpected inner GetItemsByIDs requests: %+v", spy.itemsByIDsRequests)
	}

	// Fully warm batch call hits cache
	items2, err := svc.GetItemsByIDs(ctx, []int64{102, 101})
	if err != nil || len(items2) != 2 {
		t.Fatalf("expected 2 cached items, got %d err=%v", len(items2), err)
	}
	if len(spy.itemsByIDsRequests) != 1 {
		t.Fatalf("expected 0 additional inner calls on full cache hit, got %d", len(spy.itemsByIDsRequests))
	}

	// Partial hit fetches only missing ID
	items3, err := svc.GetItemsByIDs(ctx, []int64{101, 103})
	if err != nil || len(items3) != 2 || items3["103"].Hash != 103 {
		t.Fatalf("expected items 101 & 103, got %+v err=%v", items3, err)
	}
	if len(spy.itemsByIDsRequests) != 2 || !slices.Equal(spy.itemsByIDsRequests[1], []int64{103}) {
		t.Fatalf("expected inner call with only missing [103], got %+v", spy.itemsByIDsRequests)
	}

	// Single GetItem reuses cache populated by GetItemsByIDs
	single, err := svc.GetItem(ctx, 103)
	if err != nil || single == nil || single.Hash != 103 {
		t.Fatalf("expected cached item 103, got %+v err=%v", single, err)
	}
	if spy.itemCalls.Load() != 0 {
		t.Fatalf("expected GetItem(103) to hit itemsByHash cache, got %d inner calls", spy.itemCalls.Load())
	}

	// Single GetActivity warms GetActivitiesByIDs
	if _, err := svc.GetActivity(ctx, 500); err != nil {
		t.Fatalf("GetActivity failed: %v", err)
	}
	acts, err := svc.GetActivitiesByIDs(ctx, []int64{500, 501})
	if err != nil || len(acts) != 2 {
		t.Fatalf("expected 2 activities, got %+v err=%v", acts, err)
	}
	if len(spy.activitiesByIDsRequests) != 1 || !slices.Equal(spy.activitiesByIDsRequests[0], []int64{501}) {
		t.Fatalf("expected GetActivitiesByIDs to fetch only uncached [501], got %+v", spy.activitiesByIDsRequests)
	}

	// Collection cache fast-path for perks and activityModes
	if _, err := svc.GetPerks(ctx); err != nil {
		t.Fatalf("GetPerks failed: %v", err)
	}
	perks, err := svc.GetPerksByIDs(ctx, []int64{900001})
	if err != nil || perks["900001"].Hash != 900001 {
		t.Fatalf("expected perk 900001 from collection cache, got %+v err=%v", perks, err)
	}
	if len(spy.perksByIDsRequests) != 0 {
		t.Fatalf("expected 0 inner GetPerksByIDs calls when resolved from collection cache, got %d", len(spy.perksByIDsRequests))
	}

	if _, err := svc.GetActivityModes(ctx); err != nil {
		t.Fatalf("GetActivityModes failed: %v", err)
	}
	modes, err := svc.GetActivityModesByIDs(ctx, []int64{84})
	if err != nil || modes["84"].Hash != 84 {
		t.Fatalf("expected mode 84 from collection cache, got %+v err=%v", modes, err)
	}
	if len(spy.activityModesByIDsReqs) != 0 {
		t.Fatalf("expected 0 inner GetActivityModesByIDs calls when resolved from collection cache, got %d", len(spy.activityModesByIDsReqs))
	}
}

type stubBungieClient struct {
	bungie.ClientWithResponsesInterface
	activityResp *bungie.Destiny2GetActivityHistoryResponse
	itemResps    map[int64]*bungie.Destiny2GetItemResponse
}

func (c *stubBungieClient) Destiny2GetActivityHistoryWithResponse(
	_ context.Context,
	_ int32,
	_ int64,
	_ int64,
	_ *bungie.Destiny2GetActivityHistoryParams,
	_ ...bungie.RequestEditorFn,
) (*bungie.Destiny2GetActivityHistoryResponse, error) {
	return c.activityResp, nil
}

func (c *stubBungieClient) Destiny2GetItemWithResponse(
	_ context.Context,
	_ int32,
	_ int64,
	itemInstanceId int64,
	_ *bungie.Destiny2GetItemParams,
	_ ...bungie.RequestEditorFn,
) (*bungie.Destiny2GetItemResponse, error) {
	return c.itemResps[itemInstanceId], nil
}

func TestDestinyService_UsesBatchedManifestLookups(t *testing.T) {
	ctx := context.Background()

	t.Run("getActivity uses GetActivitiesByIDs and GetActivityModesByIDs with mode fallback", func(t *testing.T) {
		spy := &spyManifestService{
			activitiesStore: map[int64]ActivityDefinition{
				1000: {Hash: 1000, DisplayProperties: ActivityDisplayProperties{Name: "Burnout"}, PgcrImage: "/pgcr.jpg"},
				2000: {Hash: 2000, DisplayProperties: ActivityDisplayProperties{Name: "Control: Matchmade"}, DirectActivityModeHash: 0},
			},
			activityModesStore: map[int64]ActivityModeDefinition{
				10: {Hash: 10, DisplayProperties: ModeDisplayProperties{Name: "Control", Icon: "/control.png"}},
			},
		}
		refID := uint32(1000)
		dirHash := uint32(2000)
		instID := "99999"
		mode := int32(bungie.CurrentActivityModeTypeControl)
		period := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
		activities := []bungie.StatsPeriodGroup{
			{
				Period: &period,
				ActivityDetails: &bungie.HistoricalStatsActivity{
					ReferenceId:          &refID,
					DirectorActivityHash: &dirHash,
					InstanceId:           &instID,
					Mode:                 &mode,
				},
			},
		}
		svc := &service{
			ManifestService: spy,
			Client: &stubBungieClient{
				activityResp: &bungie.Destiny2GetActivityHistoryResponse{
					HTTPResponse: &http.Response{StatusCode: http.StatusOK},
					JSON200: &bungie.DestinyActivityHistoryResults{
						Response: &bungie.DestinyHistoricalStatsDestinyActivityHistoryResults{Activities: &activities},
					},
				},
			},
		}

		got, err := svc.GetAllPVPActivity(ctx, "123", 3, "456", 10, 0)
		if err != nil {
			t.Fatalf("GetAllPVPActivity failed: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 activity, got %d", len(got))
		}
		if got[0].Location != "Burnout" || got[0].ActivityIcon != "https://www.bungie.net/control.png" {
			t.Fatalf("unexpected activity: %+v", got[0])
		}
		if len(spy.activitiesByIDsRequests) != 1 || len(spy.activityModesByIDsReqs) != 1 {
			t.Fatalf("expected 1 batched call each, got activities=%d modes=%d", len(spy.activitiesByIDsRequests), len(spy.activityModesByIDsReqs))
		}
		if spy.modesCalls.Load() != 0 {
			t.Fatalf("expected full-collection GetActivityModes never called, got %d", spy.modesCalls.Load())
		}
	})

	t.Run("buildLoadout uses GetItemsByIDs and GetPerksByIDs including ornament, socket, and set-bonus perks", func(t *testing.T) {
		setHash := int64(500001)
		spy := &spyManifestService{
			itemsStore: map[int64]ItemDefinition{
				3001: {Hash: 3001, DisplayProperties: ItemDisplayProperties{Name: "Helmet", Icon: "/helm.png"}, EquippingBlock: EquippingBlock{EquipableItemSetHash: &setHash}},
				3002: {Hash: 3002, DisplayProperties: ItemDisplayProperties{Name: "Ornament", Icon: "/orn.png"}},
				3003: {Hash: 3003, DisplayProperties: ItemDisplayProperties{Name: "Mod Plug", Icon: "/mod.png"}},
				3004: {Hash: 3004, DisplayProperties: ItemDisplayProperties{Name: "Arms", Icon: "/arms.png"}, EquippingBlock: EquippingBlock{EquipableItemSetHash: &setHash}},
			},
			perksStore: map[int64]PerkDefinition{
				4001: {Hash: 4001, IsDisplayable: true, DisplayProperties: DamageTypeDisplayProperties{Name: "Armor Perk"}},
				4002: {Hash: 4002, IsDisplayable: true, DisplayProperties: DamageTypeDisplayProperties{Name: "Set Bonus 2pc"}},
			},
			equipableItemSetsStore: map[string]EquipableItemSetDefinition{
				"500001": {
					Hash:              500001,
					DisplayProperties: ItemDisplayProperties{Name: "Test Set"},
					SetItems:          []int64{3001, 3004},
					SetPerks:          []ItemSetPerkDefinition{{RequiredSetCount: 2, SandboxPerkHash: 4002}},
				},
			},
		}

		helmInst := "111"
		armsInst := "222"
		helmBucket := uint32(HelmetArmor)
		armsBucket := uint32(GauntletsArmor)
		helmHash := uint32(3001)
		ornHash := uint32(3002)
		plugHash := uint32(3003)
		armsHash := uint32(3004)
		perkHash := uint32(4001)

		svc := &service{
			ManifestService: spy,
			Client: &stubBungieClient{
				itemResps: map[int64]*bungie.Destiny2GetItemResponse{
					111: {
						JSON200: &bungie.DestinyItemResponse{
							DestinyItem: &bungie.DestinyItem{
								Item: &bungie.SingleComponentResponseOfDestinyItemComponent{
									ItemComponent: &bungie.ItemComponent{
										ItemHash:       &helmHash,
										BucketHash:     &helmBucket,
										ItemInstanceId: &helmInst,
									},
								},
								Sockets: &bungie.SingleComponentResponseOfDestinyItemSocketsComponent{
									Data: &bungie.DestinyEntitiesItemsDestinyItemSocketsComponent{
										Sockets: &[]bungie.DestinyEntitiesItemsDestinyItemSocketState{{PlugHash: &plugHash}},
									},
								},
								Perks: &bungie.SingleComponentResponseOfDestinyItemPerksComponent{
									Data: &bungie.DestinyEntitiesItemsDestinyItemPerksComponent{
										Perks: &[]bungie.DestinyPerksDestinyPerkReference{{PerkHash: &perkHash}},
									},
								},
							},
						},
					},
					222: {
						JSON200: &bungie.DestinyItemResponse{
							DestinyItem: &bungie.DestinyItem{
								Item: &bungie.SingleComponentResponseOfDestinyItemComponent{
									ItemComponent: &bungie.ItemComponent{
										ItemHash:       &armsHash,
										BucketHash:     &armsBucket,
										ItemInstanceId: &armsInst,
									},
								},
							},
						},
					},
				},
			},
		}

		components := []bungie.ItemComponent{
			{ItemInstanceId: &helmInst, OverrideStyleItemHash: &ornHash},
			{ItemInstanceId: &armsInst},
		}
		loadout, bonuses, err := svc.buildLoadout(ctx, 1, 3, components, map[string]StatDefinition{})
		if err != nil {
			t.Fatalf("buildLoadout failed: %v", err)
		}
		if len(loadout) != 2 {
			t.Fatalf("expected 2 loadout items, got %d", len(loadout))
		}
		helmSnap := loadout[strconv.FormatUint(uint64(HelmetArmor), 10)]
		if helmSnap.ItemProperties.BaseInfo.StyleBasicInfo == nil || helmSnap.ItemProperties.BaseInfo.StyleBasicInfo.Name != "Ornament" {
			t.Fatalf("expected OverrideStyleItemHash propagated and resolved, got %+v", helmSnap.ItemProperties.BaseInfo.StyleBasicInfo)
		}
		if len(bonuses) != 1 || len(bonuses[0].ActivePerks) != 1 || bonuses[0].ActivePerks[0].Name != "Set Bonus 2pc" {
			t.Fatalf("expected set bonus perk resolved via GetPerksByIDs, got %+v", bonuses)
		}
		if spy.perksCalls.Load() != 0 {
			t.Fatalf("expected full-collection GetPerks never called, got %d", spy.perksCalls.Load())
		}
		if len(spy.itemsByIDsRequests) != 1 || len(spy.perksByIDsRequests) != 1 {
			t.Fatalf("expected 1 batched GetItemsByIDs & GetPerksByIDs call, got items=%d perks=%d", len(spy.itemsByIDsRequests), len(spy.perksByIDsRequests))
		}
	})

	t.Run("buildItemsSet makes single batched GetItemsByIDs call", func(t *testing.T) {
		spy := &spyManifestService{}
		svc := &service{ManifestService: spy}
		charID := "char-1"
		w1 := uint32(100)
		w2 := uint32(200)
		data := &bungie.PostGameCarnageReportData{
			Entries: &[]bungie.PostGameCarnageReportEntry{
				{
					CharacterId: &charID,
					Extended: &bungie.PostGameCarnageReportExtendedData{
						Weapons: &[]bungie.HistoricalWeaponStats{
							{ReferenceId: &w1},
							{ReferenceId: &w2},
						},
					},
				},
			},
		}
		items := buildItemsSet(ctx, data, set.FromSlice([]string{charID}), svc)
		if len(items) != 2 {
			t.Fatalf("expected 2 weapon items, got %d", len(items))
		}
		if len(spy.itemsByIDsRequests) != 1 {
			t.Fatalf("expected 1 GetItemsByIDs call, got %d", len(spy.itemsByIDsRequests))
		}
		if spy.itemCalls.Load() != 0 {
			t.Fatalf("expected 0 single GetItem calls, got %d", spy.itemCalls.Load())
		}
	})
}
