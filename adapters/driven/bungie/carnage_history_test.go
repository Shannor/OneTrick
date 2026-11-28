package bungie

import (
	"context"
	"errors"
	"net/http"
	"oneTrick/clients/bungie"
	"oneTrick/core/domain"
	"testing"
	"time"
)

type fakeCarnageBungieClient struct {
	bungie.ClientWithResponsesInterface
	pgcrResp     *bungie.Destiny2GetPostGameCarnageReportResponse
	pgcrErr      error
	lastID       int64
	activityResp *bungie.Destiny2GetActivityHistoryResponse
	capturedMode int32
}

func (f *fakeCarnageBungieClient) Destiny2GetPostGameCarnageReportWithResponse(
	_ context.Context,
	activityID int64,
	_ ...bungie.RequestEditorFn,
) (*bungie.Destiny2GetPostGameCarnageReportResponse, error) {
	f.lastID = activityID
	return f.pgcrResp, f.pgcrErr
}

func (f *fakeCarnageBungieClient) Destiny2GetActivityHistoryWithResponse(
	_ context.Context,
	_ int32,
	_ int64,
	_ int64,
	params *bungie.Destiny2GetActivityHistoryParams,
	_ ...bungie.RequestEditorFn,
) (*bungie.Destiny2GetActivityHistoryResponse, error) {
	if params != nil && params.Mode != nil {
		f.capturedMode = *params.Mode
	}
	return f.activityResp, nil
}

type fakeCarnageAndHistoryManifest struct {
	items        map[int64]domain.ItemManifest
	activities   map[int64]domain.ActivityManifest
	modesByID    map[int64]domain.ActivityModeManifest
	allModes     map[int64]domain.ActivityModeManifest
	allModesCall int
}

func (m *fakeCarnageAndHistoryManifest) GetItemsByIDs(_ context.Context, ids []int64) (map[int64]domain.ItemManifest, error) {
	out := make(map[int64]domain.ItemManifest, len(ids))
	for _, id := range ids {
		if def, ok := m.items[id]; ok {
			out[id] = def
		}
	}
	return out, nil
}

func (m *fakeCarnageAndHistoryManifest) GetActivitiesByIDs(_ context.Context, ids []int64) (map[int64]domain.ActivityManifest, error) {
	out := make(map[int64]domain.ActivityManifest, len(ids))
	for _, id := range ids {
		if def, ok := m.activities[id]; ok {
			out[id] = def
		}
	}
	return out, nil
}

func (m *fakeCarnageAndHistoryManifest) GetActivityModesByIDs(_ context.Context, ids []int64) (map[int64]domain.ActivityModeManifest, error) {
	out := make(map[int64]domain.ActivityModeManifest, len(ids))
	for _, id := range ids {
		if def, ok := m.modesByID[id]; ok {
			out[id] = def
		}
	}
	return out, nil
}

func (m *fakeCarnageAndHistoryManifest) GetActivityModes(_ context.Context) (map[int64]domain.ActivityModeManifest, error) {
	m.allModesCall++
	return m.allModes, nil
}

func TestCarnageGateway_RawEntriesConversionPerformancesAndErrors(t *testing.T) {
	t.Run("decodes JSON-faithful camelCase RawEntries from response body and maps typed entries", func(t *testing.T) {
		now := time.Date(2026, 11, 25, 19, 0, 0, 0, time.UTC)
		mode := int32(bungie.CurrentActivityModeTypeControl)
		refID := uint32(111)
		dirHash := uint32(222)
		weaponHash := uint32(444)
		code := int16(42)

		rawJSON := []byte(`{
			"ErrorCode": 1,
			"Response": {
				"entries": [
					{
						"characterId": "char-1",
						"standing": 0,
						"customVendorField": {"preserved": true}
					}
				]
			}
		}`)

		entries := []bungie.PostGameCarnageReportEntry{
			{
				CharacterId: new("char-1"),
				Standing:    new(int32(0)),
				Player: &bungie.DestinyPlayer{
					CharacterClass: new("Hunter"),
					LightLevel:     new(int32(2020)),
					DestinyUserInfo: &bungie.UserUserInfoCard{
						MembershipId:                new("mem-1"),
						MembershipType:              new(int32(3)),
						BungieGlobalDisplayName:     new("Guardian"),
						BungieGlobalDisplayNameCode: &code,
					},
				},
				Values: &map[string]bungie.HistoricalStatsValue{
					"kills":     {Basic: &bungie.DestinyHistoricalStatsDestinyHistoricalStatsValuePair{Value: new(14.0), DisplayValue: new("14")}},
					"completed": {Basic: &bungie.DestinyHistoricalStatsDestinyHistoricalStatsValuePair{Value: new(1.0), DisplayValue: new("Yes")}},
					"score":     {Basic: &bungie.DestinyHistoricalStatsDestinyHistoricalStatsValuePair{Value: new(28.0), DisplayValue: new("28")}},
				},
				Extended: &bungie.PostGameCarnageReportExtendedData{
					Weapons: &[]bungie.HistoricalWeaponStats{
						{
							ReferenceId: &weaponHash,
							Values: &map[string]bungie.HistoricalStatsValue{
								"uniqueWeaponKills": {Basic: &bungie.DestinyHistoricalStatsDestinyHistoricalStatsValuePair{Value: new(9.0), DisplayValue: new("9")}},
								"nilBasic":          {StatId: new("nilBasic"), Basic: nil},
							},
						},
					},
				},
			},
		}

		manifest := &fakeCarnageAndHistoryManifest{
			items: map[int64]domain.ItemManifest{
				444: {
					Hash:                       444,
					Name:                       "Rose",
					IconURL:                    "https://www.bungie.net/img/rose.png",
					HasIcon:                    true,
					BucketHash:                 1498876634,
					ItemTypeDisplayName:        "Hand Cannon",
					ItemTypeAndTierDisplayName: "Legendary Hand Cannon",
				},
			},
		}

		cli := &fakeCarnageBungieClient{
			pgcrResp: &bungie.Destiny2GetPostGameCarnageReportResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				Body:         rawJSON,
				JSON200: &bungie.DestinyHistoricalStatsDestinyPostGameCarnageReportData{
					ErrorCode: new(int32(1)),
					PostGameCarnageReportData: &bungie.PostGameCarnageReportData{
						Period: &now,
						ActivityDetails: &bungie.HistoricalStatsActivity{
							ReferenceId:          &refID,
							DirectorActivityHash: &dirHash,
							InstanceId:           new("555"),
							Mode:                 &mode,
							IsPrivate:            new(false),
						},
						Entries: &entries,
						Teams: &[]bungie.TeamEntry{
							{
								TeamID:   new(int32(16)),
								TeamName: new("Alpha"),
								Standing: &bungie.HistoricalStatsValue{Basic: &bungie.DestinyHistoricalStatsDestinyHistoricalStatsValuePair{DisplayValue: new("Victory")}},
								Score:    &bungie.HistoricalStatsValue{Basic: &bungie.DestinyHistoricalStatsDestinyHistoricalStatsValuePair{DisplayValue: new("150")}},
							},
						},
					},
				},
			},
		}

		gw := NewCarnageGateway(cli, manifest)
		report, err := gw.GetPostGameCarnageReport(context.Background(), "555")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(report.Entries) != 1 || len(report.RawEntries) != 1 {
			t.Fatalf("expected 1 entry and 1 raw entry, got %d / %d", len(report.Entries), len(report.RawEntries))
		}
		if report.RawEntries[0]["characterId"] != "char-1" {
			t.Fatalf("expected camelCase characterId in RawEntries, got %+v", report.RawEntries[0])
		}
		custom, ok := report.RawEntries[0]["customVendorField"].(map[string]any)
		if !ok || custom["preserved"] != true {
			t.Fatalf("expected verbatim wire fields preserved in RawEntries, got %+v", report.RawEntries[0])
		}
		if report.Entries[0].DisplayName != "Guardian" || report.Entries[0].DisplayNameCode != "0042" || !report.Entries[0].Completed {
			t.Fatalf("unexpected mapped entry: %+v", report.Entries[0])
		}

		perfs, err := gw.GetPerformances(context.Background(), "555", []string{"char-1"})
		if err != nil {
			t.Fatalf("GetPerformances failed: %v", err)
		}
		perf, ok := perfs["char-1"]
		if !ok || perf.PlayerStats.Kills.Value != 14 || !perf.Weapons["444"].HasDisplay || perf.Weapons["444"].Display.Name != "Rose" {
			t.Fatalf("unexpected performance: %+v", perf)
		}
	})

	t.Run("maps invalid ID, 404, 1653, 503, and ErrorCode 5 to domain sentinels", func(t *testing.T) {
		gw := NewCarnageGateway(&fakeCarnageBungieClient{}, nil)
		if _, err := gw.GetPostGameCarnageReport(context.Background(), "bad-id"); !errors.Is(err, domain.ErrInvalidActivityID) {
			t.Fatalf("expected ErrInvalidActivityID, got %v", err)
		}

		for _, tc := range []struct {
			name string
			resp *bungie.Destiny2GetPostGameCarnageReportResponse
			want error
		}{
			{
				name: "HTTP 404",
				resp: &bungie.Destiny2GetPostGameCarnageReportResponse{HTTPResponse: &http.Response{StatusCode: http.StatusNotFound}},
				want: domain.ErrActivityNotFound,
			},
			{
				name: "ErrorCode 1653",
				resp: &bungie.Destiny2GetPostGameCarnageReportResponse{
					HTTPResponse: &http.Response{StatusCode: http.StatusOK},
					JSON200:      &bungie.DestinyHistoricalStatsDestinyPostGameCarnageReportData{ErrorCode: new(int32(1653))},
				},
				want: domain.ErrActivityNotFound,
			},
			{
				name: "HTTP 503",
				resp: &bungie.Destiny2GetPostGameCarnageReportResponse{HTTPResponse: &http.Response{StatusCode: http.StatusServiceUnavailable}},
				want: domain.ErrDestinyUnavailable,
			},
			{
				name: "ErrorCode 5",
				resp: &bungie.Destiny2GetPostGameCarnageReportResponse{
					HTTPResponse: &http.Response{StatusCode: http.StatusOK},
					JSON200:      &bungie.DestinyHistoricalStatsDestinyPostGameCarnageReportData{ErrorCode: new(int32(5))},
				},
				want: domain.ErrDestinyUnavailable,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, err := NewCarnageGateway(&fakeCarnageBungieClient{pgcrResp: tc.resp}, nil).GetPostGameCarnageReport(context.Background(), "12345")
				if !errors.Is(err, tc.want) {
					t.Fatalf("expected %v, got %v", tc.want, err)
				}
			})
		}
	})
}

func TestActivityHistoryGateway(t *testing.T) {
	refID := uint32(10)
	dirHash := uint32(20)
	modeInt := int32(bungie.CurrentActivityModeTypeIronBannerControl)
	now := time.Date(2026, 11, 26, 20, 0, 0, 0, time.UTC)

	manifest := &fakeCarnageAndHistoryManifest{
		activities: map[int64]domain.ActivityManifest{
			10: {Hash: 10, Name: "Burnout", Description: "Mercury", PgcrImageURL: "https://www.bungie.net/pgcr/burnout.jpg"},
			20: {Hash: 20, Name: "Iron Banner: Tribute", DirectActivityModeHash: 9999},
		},
		modesByID: map[int64]domain.ActivityModeManifest{},
		allModes: map[int64]domain.ActivityModeManifest{
			55: {Hash: 55, ModeType: int(modeInt), IconURL: "https://www.bungie.net/icons/ib.png"},
		},
	}

	periods := []bungie.StatsPeriodGroup{
		{
			Period: &now,
			ActivityDetails: &bungie.HistoricalStatsActivity{
				ReferenceId:          &refID,
				DirectorActivityHash: &dirHash,
				InstanceId:           new("777"),
				Mode:                 &modeInt,
				IsPrivate:            new(false),
			},
		},
	}

	cli := &fakeCarnageBungieClient{
		activityResp: &bungie.Destiny2GetActivityHistoryResponse{
			HTTPResponse: &http.Response{StatusCode: http.StatusOK},
			JSON200: &bungie.DestinyActivityHistoryResults{
				Response: &bungie.DestinyHistoricalStatsDestinyActivityHistoryResults{
					Activities: &periods,
				},
			},
		},
	}

	gw := NewActivityHistoryGateway(cli, manifest)
	got, err := gw.GetActivityHistory(context.Background(), "12345", 3, "67890", domain.ActivityQueryIronBanner, 25, 0)
	if err != nil {
		t.Fatalf("GetActivityHistory failed: %v", err)
	}
	if cli.capturedMode != int32(bungie.CurrentActivityModeTypeIronBanner) {
		t.Fatalf("expected IronBanner mode %d, got %d", bungie.CurrentActivityModeTypeIronBanner, cli.capturedMode)
	}
	if manifest.allModesCall != 1 {
		t.Fatalf("expected mode fallback GetActivityModes call, got %d", manifest.allModesCall)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 activity info, got %d", len(got))
	}
	if got[0].Mode != "Iron Banner Tribute" || got[0].EstimatedGameMode != "ironBanner" || got[0].ActivityIcon != "https://www.bungie.net/icons/ib.png" {
		t.Fatalf("unexpected ActivityInfo: %+v", got[0])
	}
}
