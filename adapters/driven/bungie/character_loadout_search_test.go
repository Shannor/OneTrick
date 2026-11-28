package bungie

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"oneTrick/clients/bungie"
	"oneTrick/core/domain"
	"oneTrick/core/inventory"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeCharManifest struct {
	classes map[int64]domain.ClassManifest
	races   map[int64]domain.RaceManifest
	records map[int64]domain.RecordManifest
}

func (f fakeCharManifest) GetClasses(_ context.Context) (map[int64]domain.ClassManifest, error) {
	return f.classes, nil
}

func (f fakeCharManifest) GetRaces(_ context.Context) (map[int64]domain.RaceManifest, error) {
	return f.races, nil
}

func (f fakeCharManifest) GetRecord(_ context.Context, hash int64) (domain.RecordManifest, bool, error) {
	rec, ok := f.records[hash]
	return rec, ok, nil
}

func TestCharacterGateway_HydrationAndSorting(t *testing.T) {
	cli := &fakeProfileBungieClient{
		resp: &bungie.Destiny2GetProfileResponse{
			HTTPResponse: &http.Response{StatusCode: http.StatusOK},
			JSON200: &bungie.DestinyProfileResponse{
				Response: &bungie.DestinyResponsesDestinyProfileResponse{
					Characters: &bungie.DictionaryComponentResponseOfint64AndDestinyCharacterComponent{
						Data: &map[string]bungie.CharacterComponent{
							"c1": {
								CharacterId:          new("c1"),
								Light:                new(int32(2005)),
								ClassHash:            new(uint32(10)),
								RaceHash:             new(uint32(20)),
								TitleRecordHash:      new(uint32(30)),
								EmblemPath:           new("/emblem/1.png"),
								EmblemBackgroundPath: new("/bg/1.png"),
							},
							"c2": {
								CharacterId: new("c2"),
								Light:       new(int32(2010)),
								ClassHash:   new(uint32(11)),
								RaceHash:    new(uint32(20)),
							},
						},
					},
				},
			},
		},
	}

	manifest := fakeCharManifest{
		classes: map[int64]domain.ClassManifest{
			10: {Hash: 10, Name: "Warlock"},
			11: {Hash: 11, Name: "Hunter"},
		},
		races: map[int64]domain.RaceManifest{
			20: {Hash: 20, Name: "Awoken"},
		},
		records: map[int64]domain.RecordManifest{
			30: {Hash: 30, MaleTitle: "Flawless"},
		},
	}

	chars, err := NewCharacterGateway(cli, manifest).GetCharacters(context.Background(), 123, 3)
	if err != nil {
		t.Fatalf("GetCharacters failed: %v", err)
	}
	if len(chars) != 2 || chars[0].ID != "c2" || chars[1].ID != "c1" {
		t.Fatalf("expected characters sorted by Light desc [c2, c1], got %+v", chars)
	}
	if chars[1].CurrentTitle != "Flawless" || chars[1].EmblemURL != "https://www.bungie.net/emblem/1.png" {
		t.Fatalf("unexpected hydrated character: %+v", chars[1])
	}
}

type fakeLoadoutClient struct {
	bungie.ClientWithResponsesInterface
	profileResp *bungie.Destiny2GetProfileResponse
	itemsByInst map[int64]*bungie.Destiny2GetItemResponse
}

func (f *fakeLoadoutClient) Destiny2GetProfileWithResponse(
	_ context.Context,
	_ int32,
	_ int64,
	_ *bungie.Destiny2GetProfileParams,
	_ ...bungie.RequestEditorFn,
) (*bungie.Destiny2GetProfileResponse, error) {
	return f.profileResp, nil
}

func (f *fakeLoadoutClient) Destiny2GetItemWithResponse(
	_ context.Context,
	_ int32,
	_ int64,
	itemInstanceID int64,
	_ *bungie.Destiny2GetItemParams,
	_ ...bungie.RequestEditorFn,
) (*bungie.Destiny2GetItemResponse, error) {
	return f.itemsByInst[itemInstanceID], nil
}

type fakeLoadoutManifest struct {
	stats   map[int64]domain.StatManifest
	damages map[int64]domain.DamageTypeManifest
	sets    map[int64]domain.EquipableItemSetManifest
	items   map[int64]domain.ItemManifest
	perks   map[int64]domain.PerkManifest
}

func (f fakeLoadoutManifest) GetStats(_ context.Context) (map[int64]domain.StatManifest, error) {
	return f.stats, nil
}
func (f fakeLoadoutManifest) GetDamageTypes(_ context.Context) (map[int64]domain.DamageTypeManifest, error) {
	return f.damages, nil
}
func (f fakeLoadoutManifest) GetEquipableItemSets(_ context.Context) (map[int64]domain.EquipableItemSetManifest, error) {
	return f.sets, nil
}
func (f fakeLoadoutManifest) GetItemsByIDs(_ context.Context, ids []int64) (map[int64]domain.ItemManifest, error) {
	out := make(map[int64]domain.ItemManifest, len(ids))
	for _, id := range ids {
		if def, ok := f.items[id]; ok {
			out[id] = def
		}
	}
	return out, nil
}
func (f fakeLoadoutManifest) GetPerksByIDs(_ context.Context, ids []int64) (map[int64]domain.PerkManifest, error) {
	out := make(map[int64]domain.PerkManifest, len(ids))
	for _, id := range ids {
		if def, ok := f.perks[id]; ok {
			out[id] = def
		}
	}
	return out, nil
}

func TestLiveLoadoutGateway_NonMutatingStyleFallbackConcurrencyAndArmorSets(t *testing.T) {
	t.Run("hydrates live loadout, armor set bonuses, and ornament fallback without mutating shared pointers", func(t *testing.T) {
		helmBucket := uint32(inventory.HelmetArmor)
		chestBucket := uint32(inventory.ChestArmor)
		helmHash := uint32(1001)
		chestHash := uint32(1002)
		ornamentHash := uint32(9001)
		plugHash := uint32(3001)
		perkHash := uint32(4001)
		statHash := uint32(2996146975)
		dmgHash := uint32(3373582085)

		sharedHelmDetail := &bungie.DestinyItem{
			CharacterId: new("char-1"),
			Item: &bungie.SingleComponentResponseOfDestinyItemComponent{
				ItemComponent: &bungie.ItemComponent{
					ItemHash:              &helmHash,
					BucketHash:            &helmBucket,
					ItemInstanceId:        new("111"),
					OverrideStyleItemHash: nil,
				},
			},
			Instance: &bungie.SingleComponentResponseOfDestinyItemInstanceComponent{
				ItemInstanceComponent: &bungie.ItemInstanceComponent{
					DamageTypeHash: &dmgHash,
				},
			},
			Perks: &bungie.SingleComponentResponseOfDestinyItemPerksComponent{
				Data: &bungie.DestinyEntitiesItemsDestinyItemPerksComponent{
					Perks: &[]bungie.DestinyPerksDestinyPerkReference{
						{PerkHash: &perkHash, IconPath: new("/icons/perk.png")},
					},
				},
			},
			Sockets: &bungie.SingleComponentResponseOfDestinyItemSocketsComponent{
				Data: &bungie.DestinyEntitiesItemsDestinyItemSocketsComponent{
					Sockets: &[]bungie.DestinyEntitiesItemsDestinyItemSocketState{
						{PlugHash: &plugHash, IsEnabled: new(true), IsVisible: new(true)},
					},
				},
			},
			Stats: &bungie.SingleComponentResponseOfDestinyItemStatsComponent{
				Data: &bungie.DestinyEntitiesItemsDestinyItemStatsComponent{
					Stats: &map[string]bungie.DestinyDestinyStat{
						"2996146975": {StatHash: &statHash, Value: new(int32(22))},
					},
				},
			},
		}

		chestDetail := &bungie.DestinyItem{
			CharacterId: new("char-1"),
			Item: &bungie.SingleComponentResponseOfDestinyItemComponent{
				ItemComponent: &bungie.ItemComponent{
					ItemHash:       &chestHash,
					BucketHash:     &chestBucket,
					ItemInstanceId: new("222"),
				},
			},
		}

		minted := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
		cli := &fakeLoadoutClient{
			profileResp: &bungie.Destiny2GetProfileResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200: &bungie.DestinyProfileResponse{
					Response: &bungie.DestinyResponsesDestinyProfileResponse{
						ResponseMintedTimestamp: &minted,
						Characters: &bungie.DictionaryComponentResponseOfint64AndDestinyCharacterComponent{
							Data: &map[string]bungie.CharacterComponent{
								"char-1": {
									Stats: &map[string]int32{"2996146975": 100},
								},
							},
						},
						CharacterEquipment: &bungie.DictionaryComponentResponseOfint64AndDestinyInventoryComponent{
							Data: &map[string]bungie.DestinyEntitiesInventoryDestinyInventoryComponent{
								"char-1": {
									Items: &[]bungie.ItemComponent{
										{
											ItemHash:              &helmHash,
											BucketHash:            &helmBucket,
											ItemInstanceId:        new("111"),
											OverrideStyleItemHash: &ornamentHash,
										},
										{
											ItemHash:       &chestHash,
											BucketHash:     &chestBucket,
											ItemInstanceId: new("222"),
										},
									},
								},
							},
						},
					},
				},
			},
			itemsByInst: map[int64]*bungie.Destiny2GetItemResponse{
				111: {JSON200: &bungie.DestinyItemResponse{DestinyItem: sharedHelmDetail}},
				222: {JSON200: &bungie.DestinyItemResponse{DestinyItem: chestDetail}},
			},
		}

		manifest := fakeLoadoutManifest{
			stats: map[int64]domain.StatManifest{
				2996146975: {Hash: 2996146975, Name: "Mobility", IconURL: "https://www.bungie.net/icons/mob.png", HasIcon: true},
			},
			damages: map[int64]domain.DamageTypeManifest{
				3373582085: {Hash: 3373582085, Name: "Kinetic", Icon: "/icons/kin.png"},
			},
			sets: map[int64]domain.EquipableItemSetManifest{
				7001: {
					Hash:     7001,
					Name:     "Techsec",
					SetItems: []int64{1001, 1002},
					SetPerks: []domain.ItemSetPerk{{RequiredSetCount: 2, SandboxPerkHash: 8001}},
				},
			},
			items: map[int64]domain.ItemManifest{
				1001: {Hash: 1001, Name: "Techsec Hood", IconURL: "https://www.bungie.net/icons/hood.png", TierType: 5},
				1002: {Hash: 1002, Name: "Techsec Robes", IconURL: "https://www.bungie.net/icons/robes.png", TierType: 5},
				9001: {Hash: 9001, Name: "Trials Ornament", IconURL: "https://www.bungie.net/icons/ornament.png", TierType: 6},
				3001: {Hash: 3001, Name: "Targeting Mod", IconURL: "https://www.bungie.net/icons/mod.png"},
			},
			perks: map[int64]domain.PerkManifest{
				4001: {Hash: 4001, Name: "Ashes to Assets", IsDisplayable: true},
				8001: {Hash: 8001, Name: "2-Piece Bonus", Description: "+15 Handling", IconURL: "https://www.bungie.net/icons/set2.png"},
			},
		}

		live, err := NewLiveLoadoutGateway(cli, manifest).GetLiveLoadout(context.Background(), 123, 3, "char-1")
		if err != nil {
			t.Fatalf("GetLiveLoadout failed: %v", err)
		}
		if sharedHelmDetail.Item.ItemComponent.OverrideStyleItemHash != nil {
			t.Fatal("expected shared DestinyItem.ItemComponent.OverrideStyleItemHash pointer NOT to be mutated")
		}
		helmSnap := live.Loadout[inventory.BucketKey(int64(helmBucket))]
		if helmSnap.Details.BaseInfo.StyleBasicInfo == nil || helmSnap.Details.BaseInfo.StyleBasicInfo.ItemHash != 9001 {
			t.Fatalf("expected fallback StyleBasicInfo 9001 on helmet, got %+v", helmSnap.Details.BaseInfo.StyleBasicInfo)
		}
		if len(live.ArmorSetBonuses) != 1 || live.ArmorSetBonuses[0].SetName != "Techsec" || live.ArmorSetBonuses[0].ActiveCount != 2 {
			t.Fatalf("unexpected ArmorSetBonuses: %+v", live.ArmorSetBonuses)
		}
		if live.Stats["2996146975"].Value != 100 {
			t.Fatalf("unexpected character stats: %+v", live.Stats)
		}
	})

	t.Run("bounds concurrent getItemDetails fetches to maxConcurrentItemFetches", func(t *testing.T) {
		var inFlight, maxSeen atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cur := inFlight.Add(1)
			for {
				prev := maxSeen.Load()
				if cur <= prev || maxSeen.CompareAndSwap(prev, cur) {
					break
				}
			}
			defer inFlight.Add(-1)
			time.Sleep(10 * time.Millisecond)

			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			instanceID := parts[len(parts)-1]
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"Response":{"item":{"data":{"itemHash":1001,"itemInstanceId":"%s"}}}}`, instanceID)
		}))
		defer srv.Close()

		cli, err := bungie.NewClientWithResponses(srv.URL)
		if err != nil {
			t.Fatalf("NewClientWithResponses: %v", err)
		}
		gw := NewLiveLoadoutGateway(cli, fakeLoadoutManifest{})

		items := make([]bungie.ItemComponent, 0, 8)
		for i := 1; i <= 8; i++ {
			id := fmt.Sprintf("%d", 1000+i)
			items = append(items, bungie.ItemComponent{ItemInstanceId: &id})
		}
		fetched, err := gw.fetchEquippedItems(context.Background(), 123, 3, items)
		if err != nil || len(fetched) != 8 {
			t.Fatalf("fetchEquippedItems failed: len=%d err=%v", len(fetched), err)
		}
		if maxSeen.Load() > maxConcurrentItemFetches {
			t.Fatalf("expected concurrency <= %d, got %d", maxConcurrentItemFetches, maxSeen.Load())
		}
	})
}

type fakeSearchBungieClient struct {
	bungie.ClientWithResponsesInterface
	globalResp  *bungie.UserSearchByGlobalNamePostResponse
	exactResp   *bungie.Destiny2SearchDestinyPlayerByBungieNameResponse
	globalCalls int
	exactCalls  int
}

func (f *fakeSearchBungieClient) UserSearchByGlobalNamePostWithResponse(
	_ context.Context,
	_ int32,
	_ bungie.UserSearchByGlobalNamePostJSONRequestBody,
	_ ...bungie.RequestEditorFn,
) (*bungie.UserSearchByGlobalNamePostResponse, error) {
	f.globalCalls++
	return f.globalResp, nil
}

func (f *fakeSearchBungieClient) Destiny2SearchDestinyPlayerByBungieNameWithResponse(
	_ context.Context,
	_ int32,
	_ bungie.Destiny2SearchDestinyPlayerByBungieNameJSONRequestBody,
	_ ...bungie.RequestEditorFn,
) (*bungie.Destiny2SearchDestinyPlayerByBungieNameResponse, error) {
	f.exactCalls++
	return f.exactResp, nil
}

func TestUserSearchGateway(t *testing.T) {
	t.Run("prefix search resolves cross-save primary membership and filters empty memberships", func(t *testing.T) {
		cli := &fakeSearchBungieClient{
			globalResp: &bungie.UserSearchByGlobalNamePostResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200: &bungie.UserSearchResponse{
					SearchResponse: &bungie.SearchResponse{
						HasMore: new(true),
						SearchResults: &[]bungie.UserSearchDetail{
							{
								BungieNetMembershipId:       new("999001"),
								BungieGlobalDisplayName:     new("Weegee"),
								BungieGlobalDisplayNameCode: new(int16(42)),
								DestinyMemberships: &[]bungie.UserUserInfoCard{
									{
										MembershipId:              new("4611686018467484863"),
										MembershipType:            new(int32(3)),
										DisplayName:               new("WeegeeSteam"),
										IconPath:                  new("/img/steam.png"),
										CrossSaveOverride:         new(int32(0)),
										ApplicableMembershipTypes: &[]int32{3},
									},
									{
										MembershipId:      new("4611686018400000001"),
										MembershipType:    new(int32(1)),
										DisplayName:       new("WeegeeXbox"),
										CrossSaveOverride: new(int32(3)),
									},
								},
							},
							{
								BungieNetMembershipId: new("999002"),
								DestinyMemberships:    &[]bungie.UserUserInfoCard{},
							},
						},
					},
				},
			},
		}

		gw := NewUserSearchGateway(cli)
		got, hasMore, err := gw.SearchUsers(context.Background(), " Weegee ", 0)
		if err != nil || !hasMore || len(got) != 1 {
			t.Fatalf("unexpected search result: len=%d hasMore=%v err=%v", len(got), hasMore, err)
		}
		if got[0].UniqueName != "Weegee#0042" || got[0].PrimaryMembershipID != "4611686018467484863" || got[0].PrimaryMembershipType != 3 {
			t.Fatalf("unexpected mapped user: %+v", got[0])
		}
		if !reflect.DeepEqual(got[0].AlternateNames, []string{"WeegeeSteam", "WeegeeXbox"}) {
			t.Fatalf("unexpected alternate names: %+v", got[0].AlternateNames)
		}
	})

	t.Run("exact #1234 discriminator dispatches exact Bungie name endpoint and maps ErrorCode 5", func(t *testing.T) {
		cli := &fakeSearchBungieClient{
			exactResp: &bungie.Destiny2SearchDestinyPlayerByBungieNameResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200: &bungie.IEnumerableOfUserInfoCard{
					ErrorCode: new(int32(1)),
					Response: &[]bungie.UserUserInfoCard{
						{
							MembershipId:                new("4611686018467484863"),
							MembershipType:              new(int32(3)),
							BungieGlobalDisplayName:     new("Weegee"),
							BungieGlobalDisplayNameCode: new(int16(42)),
						},
					},
				},
			},
		}
		gw := NewUserSearchGateway(cli)
		got, hasMore, err := gw.SearchUsers(context.Background(), "Weegee#0042", 0)
		if err != nil || hasMore || cli.exactCalls != 1 || len(got) != 1 {
			t.Fatalf("unexpected exact search: len=%d calls=%d err=%v", len(got), cli.exactCalls, err)
		}

		downCli := &fakeSearchBungieClient{
			globalResp: &bungie.UserSearchByGlobalNamePostResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200:      &bungie.UserSearchResponse{ErrorCode: new(int32(5))},
			},
		}
		if _, _, err := NewUserSearchGateway(downCli).SearchUsers(context.Background(), "Weegee", 0); !errors.Is(err, domain.ErrDestinyUnavailable) {
			t.Fatalf("expected ErrDestinyUnavailable, got %v", err)
		}
	})
}
