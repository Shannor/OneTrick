package activity

import (
	"context"
	"errors"
	"oneTrick/api"
	"oneTrick/clients/bungie"
	coreactivity "oneTrick/core/activity"
	"oneTrick/services/aggregate"
	"oneTrick/services/destiny"
	"oneTrick/services/user"
	"testing"
	"time"
)

type stubDestinyService struct {
	destiny.Service
	data  *bungie.PostGameCarnageReportData
	teams []api.Team
	err   error
}

func (s stubDestinyService) GetActivity(_ context.Context, _ string) (*bungie.PostGameCarnageReportData, []api.Team, error) {
	return s.data, s.teams, s.err
}

type stubManifestService struct {
	destiny.ManifestService
	items       map[string]destiny.ItemDefinition
	activities  map[string]destiny.ActivityDefinition
	modeByHash  *destiny.ActivityModeDefinition
	modeErr     error
	allModes    map[string]destiny.ActivityModeDefinition
	allModesErr error
}

func (s stubManifestService) GetItemsByIDs(_ context.Context, _ []int64) (map[string]destiny.ItemDefinition, error) {
	return s.items, nil
}

func (s stubManifestService) GetActivitiesByIDs(_ context.Context, _ []int64) (map[string]destiny.ActivityDefinition, error) {
	return s.activities, nil
}

func (s stubManifestService) GetActivityMode(_ context.Context, _ int64) (*destiny.ActivityModeDefinition, error) {
	return s.modeByHash, s.modeErr
}

func (s stubManifestService) GetActivityModes(_ context.Context) (map[string]destiny.ActivityModeDefinition, error) {
	return s.allModes, s.allModesErr
}

type stubAggregateService struct {
	aggregate.Service
	agg *api.Aggregate
	err error
}

func (s stubAggregateService) GetAggregate(_ context.Context, _ string) (*api.Aggregate, error) {
	return s.agg, s.err
}

type stubUserService struct {
	user.Service
	partyUsers []user.User
	byIDUsers  []user.User
}

func (s stubUserService) FindByPartyMembershipIDs(_ context.Context, _ []string) ([]user.User, error) {
	return s.partyUsers, nil
}

func (s stubUserService) GetByIDs(_ context.Context, _ []string) ([]user.User, error) {
	return s.byIDUsers, nil
}

func TestCarnageReportGateway_ConversionAndErrors(t *testing.T) {
	now := time.Date(2026, 11, 25, 19, 0, 0, 0, time.UTC)
	mode := int32(bungie.CurrentActivityModeTypeControl)
	refID := uint32(111)
	dirHash := uint32(222)
	emblemHash := uint32(999)
	weaponHash := uint32(444)
	code := int16(42)

	vals := map[string]bungie.HistoricalStatsValue{
		"kills":     {Basic: &bungie.DestinyHistoricalStatsDestinyHistoricalStatsValuePair{Value: new(12.0), DisplayValue: new("12")}},
		"team":      {Basic: &bungie.DestinyHistoricalStatsDestinyHistoricalStatsValuePair{Value: new(16.0), DisplayValue: new("16")}},
		"completed": {Basic: &bungie.DestinyHistoricalStatsDestinyHistoricalStatsValuePair{Value: new(1.0), DisplayValue: new("Yes")}},
		"score":     {Basic: &bungie.DestinyHistoricalStatsDestinyHistoricalStatsValuePair{Value: new(25.0), DisplayValue: new("25")}},
	}
	weapons := []bungie.HistoricalWeaponStats{
		{
			ReferenceId: &weaponHash,
			Values: &map[string]bungie.HistoricalStatsValue{
				"uniqueWeaponKills": {Basic: &bungie.DestinyHistoricalStatsDestinyHistoricalStatsValuePair{Value: new(8.0), DisplayValue: new("8")}},
				"nilBasicStat":      {StatId: new("nilBasicStat"), Basic: nil},
			},
		},
	}
	extVals := map[string]bungie.HistoricalStatsValue{
		"weaponKillsSuper": {Basic: &bungie.DestinyHistoricalStatsDestinyHistoricalStatsValuePair{Value: new(2.0), DisplayValue: new("2")}},
		"nilExtendedBasic": {StatId: new("nilExtendedBasic"), Basic: nil},
	}

	entries := []bungie.PostGameCarnageReportEntry{
		{
			CharacterId: new("char-1"),
			Standing:    new(int32(0)),
			Player: &bungie.DestinyPlayer{
				CharacterClass: new("Hunter"),
				LightLevel:     new(int32(2020)),
				EmblemHash:     &emblemHash,
				DestinyUserInfo: &bungie.UserUserInfoCard{
					MembershipId:                new("mem-1"),
					MembershipType:              new(int32(3)),
					DisplayName:                 new("SteamFallback"),
					BungieGlobalDisplayName:     new("Guardian"),
					BungieGlobalDisplayNameCode: &code,
				},
			},
			Values:   &vals,
			Extended: &bungie.PostGameCarnageReportExtendedData{Weapons: &weapons, Values: &extVals},
		},
	}

	gw := NewCarnageReportGateway(stubDestinyService{
		data: &bungie.PostGameCarnageReportData{
			Period: &now,
			ActivityDetails: &bungie.HistoricalStatsActivity{
				ReferenceId:          &refID,
				DirectorActivityHash: &dirHash,
				InstanceId:           new("555"),
				Mode:                 &mode,
				IsPrivate:            new(false),
			},
			Entries: &entries,
		},
		teams: []api.Team{{ID: "16", Standing: "Victory", Score: "150", TeamName: new("Alpha")}},
	})

	report, err := gw.GetPostGameCarnageReport(context.Background(), "555")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Entries) != 1 || len(report.RawEntries) != 1 {
		t.Fatalf("expected 1 entry and 1 raw entry, got %d / %d", len(report.Entries), len(report.RawEntries))
	}
	e := report.Entries[0]
	if e.DisplayName != "Guardian" || e.DisplayNameCode != "0042" {
		t.Fatalf("expected Guardian#0042, got %q#%q", e.DisplayName, e.DisplayNameCode)
	}
	if !e.Completed || !e.Score.Present || e.Score.Value != 25 {
		t.Fatalf("expected completed=true and fallback score=25, got completed=%v score=%+v", e.Completed, e.Score)
	}
	if len(e.Weapons) != 1 || e.Weapons[0].ReferenceID != 444 {
		t.Fatalf("expected weapon 444, got %+v", e.Weapons)
	}
	if _, exists := e.Weapons[0].Stats["nilBasicStat"]; exists {
		t.Fatal("expected weapon stat with nil Basic to be skipped")
	}
	if _, exists := e.ExtraStats["nilExtendedBasic"]; exists {
		t.Fatal("expected extended stat with nil Basic to be skipped")
	}
	if _, exists := e.ExtraStats["weaponKillsSuper"]; !exists {
		t.Fatal("expected extended stat with non-nil Basic to be preserved")
	}

	t.Run("not found error mapped", func(t *testing.T) {
		gwNotFound := NewCarnageReportGateway(stubDestinyService{err: destiny.ErrActivityNotFound})
		_, err := gwNotFound.GetPostGameCarnageReport(context.Background(), "555")
		if !errors.Is(err, coreactivity.ErrActivityNotFound) {
			t.Fatalf("expected ErrActivityNotFound, got %v", err)
		}
	})

	t.Run("server down error mapped", func(t *testing.T) {
		gwDown := NewCarnageReportGateway(stubDestinyService{err: destiny.ErrDestinyServerDown})
		_, err := gwDown.GetPostGameCarnageReport(context.Background(), "555")
		if !errors.Is(err, coreactivity.ErrDestinyUnavailable) {
			t.Fatalf("expected ErrDestinyUnavailable, got %v", err)
		}
	})
}

func TestManifestLookup_ItemsAndModeFallback(t *testing.T) {
	ml := NewManifestLookup(stubManifestService{
		items: map[string]destiny.ItemDefinition{
			"1001": {
				Hash:                       1001,
				DisplayProperties:          destiny.ItemDisplayProperties{Name: "Rose", Icon: "/img/rose.png", HasIcon: true},
				ItemTypeDisplayName:        "Hand Cannon",
				ItemTypeAndTierDisplayName: "Legendary Hand Cannon",
				EquippingBlock:             destiny.EquippingBlock{EquipmentSlotTypeHash: 1498876634},
			},
			"9001": {
				Hash:              9001,
				DisplayProperties: destiny.ItemDisplayProperties{Icon: "", HasIcon: false},
			},
		},
		modeErr: errors.New("not found by hash"),
		allModes: map[string]destiny.ActivityModeDefinition{
			"55": {
				Hash:              55,
				ModeType:          10,
				DisplayProperties: destiny.ModeDisplayProperties{Icon: "/img/control.png"},
			},
		},
	})

	items, err := ml.GetItemsByIDs(context.Background(), []int64{1001, 9001})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if items[1001].BucketHash != 1498876634 || items[1001].IconURL != "https://www.bungie.net/img/rose.png" {
		t.Fatalf("unexpected item 1001: %+v", items[1001])
	}
	if items[9001].IconURL != "" {
		t.Fatalf("expected empty IconURL for empty icon path, got %q", items[9001].IconURL)
	}

	mode, ok, err := ml.GetActivityMode(context.Background(), 99999, 10)
	if err != nil || !ok {
		t.Fatalf("expected fallback mode lookup by ModeType=10 to succeed, ok=%v err=%v", ok, err)
	}
	if mode.IconURL != "https://www.bungie.net/img/control.png" {
		t.Fatalf("unexpected mode icon: %+v", mode)
	}
}

func TestAggregateReader_NotFoundTranslatesToFalse(t *testing.T) {
	reader := NewAggregateReader(stubAggregateService{err: aggregate.NotFound})
	_, found, err := reader.GetByActivityID(context.Background(), "123")
	if err != nil || found {
		t.Fatalf("expected (zero, false, nil) for aggregate.NotFound, got found=%v err=%v", found, err)
	}
}

func TestUserLookup_ConvertsUsers(t *testing.T) {
	lookup := NewUserLookup(stubUserService{
		partyUsers: []user.User{{ID: "u1", DisplayName: "Alpha", Memberships: []user.Membership{{ID: "m1", Type: 3}}}},
		byIDUsers:  []user.User{{ID: "u2", DisplayName: "Bravo"}},
	})
	party, err := lookup.FindByPartyMembershipIDs(context.Background(), []string{"m1"})
	if err != nil || len(party) != 1 || party[0].ID != "u1" {
		t.Fatalf("unexpected party users: %+v, err=%v", party, err)
	}
	byID, err := lookup.GetByIDs(context.Background(), []string{"u2"})
	if err != nil || len(byID) != 1 || byID[0].ID != "u2" {
		t.Fatalf("unexpected byID users: %+v, err=%v", byID, err)
	}
}
