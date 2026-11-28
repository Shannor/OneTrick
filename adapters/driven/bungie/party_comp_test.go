package bungie

import (
	"context"
	"errors"
	"net/http"
	"oneTrick/clients/bungie"
	"oneTrick/core/domain"
	"slices"
	"testing"
)

type fakeProfileBungieClient struct {
	bungie.ClientWithResponsesInterface
	resp               *bungie.Destiny2GetProfileResponse
	err                error
	capturedMemType    int32
	capturedMemID      int64
	capturedComponents []int32
}

func (f *fakeProfileBungieClient) Destiny2GetProfileWithResponse(
	_ context.Context,
	membershipType int32,
	destinyMembershipID int64,
	params *bungie.Destiny2GetProfileParams,
	_ ...bungie.RequestEditorFn,
) (*bungie.Destiny2GetProfileResponse, error) {
	f.capturedMemType = membershipType
	f.capturedMemID = destinyMembershipID
	if params != nil && params.Components != nil {
		f.capturedComponents = slices.Clone(*params.Components)
	}
	return f.resp, f.err
}

type fakeProgressionManifest struct {
	def   domain.ProgressionManifest
	found bool
	err   error
}

func (f fakeProgressionManifest) GetProgression(_ context.Context, _ int64) (domain.ProgressionManifest, bool, error) {
	return f.def, f.found, f.err
}

func testCompProgressionManifest() domain.ProgressionManifest {
	return domain.ProgressionManifest{
		Hash: compProgressionHashInt64,
		Name: "Competitive Division",
		Steps: []domain.ProgressionStep{
			{StepName: "Unranked", ProgressTotal: 1, Icon: "/icons/unranked.png"},
			{StepName: "Copper III", ProgressTotal: 399, Icon: "/icons/copper3.png"},
			{StepName: "Copper II", ProgressTotal: 300, Icon: "/icons/copper2.png"},
			{StepName: "Copper I", ProgressTotal: 300, Icon: "/icons/copper1.png"},
			{StepName: "PROMOTION", ProgressTotal: 1, Icon: ""},
			{StepName: "RELEGATION", ProgressTotal: 1, Icon: ""},
			{StepName: "Bronze III", ProgressTotal: 498, Icon: "/icons/bronze3.png"},
			{StepName: "Bronze II", ProgressTotal: 500, Icon: "/icons/bronze2.png"},
			{StepName: "Bronze I", ProgressTotal: 500, Icon: "/icons/bronze1.png"},
			{StepName: "PROMOTION", ProgressTotal: 1, Icon: ""},
			{StepName: "RELEGATION", ProgressTotal: 1, Icon: ""},
			{StepName: "Silver III", ProgressTotal: 498, Icon: "/icons/silver3.png"},
			{StepName: "Silver II", ProgressTotal: 500, Icon: "/icons/silver2.png"},
			{StepName: "Silver I", ProgressTotal: 500, Icon: "/icons/silver1.png"},
			{StepName: "PROMOTION", ProgressTotal: 1, Icon: ""},
			{StepName: "RELEGATION", ProgressTotal: 1, Icon: ""},
			{StepName: "Gold III", ProgressTotal: 498, Icon: "/icons/gold3.png"},
			{StepName: "Gold II", ProgressTotal: 500, Icon: "/icons/gold2.png"},
			{StepName: "Gold I", ProgressTotal: 500, Icon: "/icons/gold1.png"},
			{StepName: "PROMOTION", ProgressTotal: 1, Icon: ""},
			{StepName: "RELEGATION", ProgressTotal: 1, Icon: ""},
			{StepName: "Platinum III", ProgressTotal: 498, Icon: "/icons/plat3.png"},
			{StepName: "Platinum II", ProgressTotal: 500, Icon: "/icons/plat2.png"},
			{StepName: "Platinum I", ProgressTotal: 500, Icon: "/icons/plat1.png"},
		},
	}
}

func TestPartyGateway(t *testing.T) {
	t.Run("extracts party membership IDs and requests component 1000", func(t *testing.T) {
		cli := &fakeProfileBungieClient{
			resp: &bungie.Destiny2GetProfileResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200: &bungie.DestinyProfileResponse{
					ErrorCode: new(int32(1)),
					Response: &bungie.DestinyResponsesDestinyProfileResponse{
						ProfileTransitoryData: &bungie.SingleComponentResponseOfDestinyProfileTransitoryComponent{
							Data: &bungie.DestinyComponentsProfilesDestinyProfileTransitoryComponent{
								PartyMembers: &[]bungie.PartyMember{
									{MembershipId: new("m-1")},
									{MembershipId: new("m-2")},
								},
							},
						},
					},
				},
			},
		}

		gw := NewPartyGateway(cli)
		ids, err := gw.GetPartyMembershipIDs(context.Background(), 9001, 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !slices.Equal(cli.capturedComponents, []int32{componentTransitory}) {
			t.Fatalf("expected component [1000], got %v", cli.capturedComponents)
		}
		if !slices.Equal(ids, []string{"m-1", "m-2"}) {
			t.Fatalf("unexpected party IDs: %+v", ids)
		}
	})

	t.Run("maps 503 and ErrorCode 1665 to domain sentinels", func(t *testing.T) {
		downGW := NewPartyGateway(&fakeProfileBungieClient{
			resp: &bungie.Destiny2GetProfileResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusServiceUnavailable},
			},
		})
		if _, err := downGW.GetPartyMembershipIDs(context.Background(), 9001, 3); !errors.Is(err, domain.ErrDestinyUnavailable) {
			t.Fatalf("expected ErrDestinyUnavailable, got %v", err)
		}

		privGW := NewPartyGateway(&fakeProfileBungieClient{
			resp: &bungie.Destiny2GetProfileResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200: &bungie.DestinyProfileResponse{
					ErrorCode: new(int32(bungieErrorCodePrivacyRestriction)),
				},
			},
		})
		if _, err := privGW.GetPartyMembershipIDs(context.Background(), 9001, 3); !errors.Is(err, domain.ErrPrivateProfile) {
			t.Fatalf("expected ErrPrivateProfile, got %v", err)
		}
	})
}

func TestCompRankGateway(t *testing.T) {
	manifest := fakeProgressionManifest{def: testCompProgressionManifest(), found: true}

	t.Run("uses CharacterProgressions primary source when available", func(t *testing.T) {
		cli := &fakeProfileBungieClient{
			resp: &bungie.Destiny2GetProfileResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200: &bungie.DestinyProfileResponse{
					Response: &bungie.DestinyResponsesDestinyProfileResponse{
						CharacterProgressions: &bungie.DictionaryComponentResponseOfint64AndDestinyCharacterProgressionComponent{
							Data: &map[string]bungie.DestinyEntitiesCharactersDestinyCharacterProgressionComponent{
								"char-1": {
									Progressions: &map[string]bungie.DestinyDestinyProgression{
										compProgressionHash: {
											CurrentProgress: new(int32(5800)),
											StepIndex:       new(int32(21)),
										},
									},
								},
							},
						},
					},
				},
			},
		}

		gw := NewCompRankGateway(cli, manifest)
		snap, ok, err := gw.GetCompetitiveRank(context.Background(), 12345, 3, "char-1")
		if err != nil || !ok {
			t.Fatalf("expected ok=true err=nil, got ok=%v err=%v", ok, err)
		}
		if !slices.Equal(cli.capturedComponents, []int32{componentCharacterProgressions, componentMetrics}) {
			t.Fatalf("expected components [202, 1100], got %v", cli.capturedComponents)
		}
		if snap.Points != 5800 || snap.DivisionName != "Platinum III" || snap.Icon != "/icons/plat3.png" || snap.StepIndex == nil || *snap.StepIndex != 21 {
			t.Fatalf("unexpected snapshot: %+v", snap)
		}
	})

	t.Run("falls back to Metrics component 1100 when CharacterProgressions omitted for privacy", func(t *testing.T) {
		cli := &fakeProfileBungieClient{
			resp: &bungie.Destiny2GetProfileResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200: &bungie.DestinyProfileResponse{
					Response: &bungie.DestinyResponsesDestinyProfileResponse{
						CharacterProgressions: &bungie.DictionaryComponentResponseOfint64AndDestinyCharacterProgressionComponent{
							Privacy: new(int32(2)),
							Data:    nil,
						},
						Metrics: &bungie.SingleComponentResponseOfDestinyMetricsComponent{
							Data: &bungie.DestinyComponentsMetricsDestinyMetricsComponent{
								Metrics: &map[string]bungie.DestinyComponentsMetricsDestinyMetricComponent{
									compMetricHash: {
										ObjectiveProgress: &bungie.DestinyQuestsDestinyObjectiveProgress{
											Progress: new(int32(4500)),
										},
									},
								},
							},
						},
					},
				},
			},
		}

		gw := NewCompRankGateway(cli, manifest)
		snap, ok, err := gw.GetCompetitiveRank(context.Background(), 12345, 3, "char-1")
		if err != nil || !ok {
			t.Fatalf("expected metrics fallback ok=true, got ok=%v err=%v", ok, err)
		}
		if snap.Points != 4500 || snap.DivisionName != "Gold II" || snap.StepIndex != nil {
			t.Fatalf("unexpected metrics fallback snapshot: %+v", snap)
		}
	})

	t.Run("returns ok=false without error when neither progression nor metric exists", func(t *testing.T) {
		cli := &fakeProfileBungieClient{
			resp: &bungie.Destiny2GetProfileResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200: &bungie.DestinyProfileResponse{
					Response: &bungie.DestinyResponsesDestinyProfileResponse{},
				},
			},
		}
		gw := NewCompRankGateway(cli, manifest)
		_, ok, err := gw.GetCompetitiveRank(context.Background(), 12345, 3, "char-1")
		if err != nil || ok {
			t.Fatalf("expected (zero, false, nil), got ok=%v err=%v", ok, err)
		}
	})

	t.Run("maps 503 and ErrorCode 1665 to domain sentinels", func(t *testing.T) {
		downGW := NewCompRankGateway(&fakeProfileBungieClient{
			resp: &bungie.Destiny2GetProfileResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusServiceUnavailable},
			},
		}, manifest)
		if _, _, err := downGW.GetCompetitiveRank(context.Background(), 123, 3, "char-1"); !errors.Is(err, domain.ErrDestinyUnavailable) {
			t.Fatalf("expected ErrDestinyUnavailable, got %v", err)
		}

		privGW := NewCompRankGateway(&fakeProfileBungieClient{
			resp: &bungie.Destiny2GetProfileResponse{
				HTTPResponse: &http.Response{StatusCode: http.StatusOK},
				JSON200: &bungie.DestinyProfileResponse{
					ErrorCode: new(int32(bungieErrorCodePrivacyRestriction)),
				},
			},
		}, manifest)
		if _, _, err := privGW.GetCompetitiveRank(context.Background(), 123, 3, "char-1"); !errors.Is(err, domain.ErrPrivateProfile) {
			t.Fatalf("expected ErrPrivateProfile, got %v", err)
		}
	})
}
