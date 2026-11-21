package destiny

import (
	"context"
	"fmt"
	"net/http"
	"oneTrick/clients/bungie"
	"oneTrick/ptr"
	"testing"
)

const (
	testIconUntested  = "/icons/untested.png"
	testIconCopper    = "/icons/copper.png"
	testIconBronze    = "/icons/bronze.png"
	testIconSilver    = "/icons/silver.png"
	testIconGold      = "/icons/gold.png"
	testIconPlatinum  = "/icons/platinum.png"
	testIconAdept     = "/icons/adept.png"
	testIconAscendant = "/icons/ascendant.png"
)

func testCompProgressionFixture() *ProgressionDefinition {
	return &ProgressionDefinition{
		Hash: CompProgressionHashInt64,
		DisplayProperties: ModeDisplayProperties{
			Name:    "Competitive Division",
			Icon:    "/icons/comp.png",
			HasIcon: true,
		},
		Steps: []ProgressionStepDefinition{
			{StepName: "Untested", ProgressTotal: 1, Icon: testIconUntested},
			{StepName: "Copper III", ProgressTotal: 374, Icon: testIconCopper},
			{StepName: "Copper II", ProgressTotal: 375, Icon: testIconCopper},
			{StepName: "Copper I", ProgressTotal: 500, Icon: testIconCopper},
			{StepName: "PROMOTION", ProgressTotal: 0, Icon: testIconCopper},
			{StepName: "RELEGATION", ProgressTotal: 0, Icon: testIconBronze},
			{StepName: "Bronze III", ProgressTotal: 500, Icon: testIconBronze},
			{StepName: "Bronze II", ProgressTotal: 500, Icon: testIconBronze},
			{StepName: "Bronze I", ProgressTotal: 500, Icon: testIconBronze},
			{StepName: "PROMOTION", ProgressTotal: 0, Icon: testIconBronze},
			{StepName: "RELEGATION", ProgressTotal: 0, Icon: testIconSilver},
			{StepName: "Silver III", ProgressTotal: 500, Icon: testIconSilver},
			{StepName: "Silver II", ProgressTotal: 500, Icon: testIconSilver},
			{StepName: "Silver I", ProgressTotal: 500, Icon: testIconSilver},
			{StepName: "PROMOTION", ProgressTotal: 0, Icon: testIconSilver},
			{StepName: "RELEGATION", ProgressTotal: 0, Icon: testIconGold},
			{StepName: "Gold III", ProgressTotal: 500, Icon: testIconGold},
			{StepName: "Gold II", ProgressTotal: 500, Icon: testIconGold},
			{StepName: "Gold I", ProgressTotal: 500, Icon: testIconGold},
			{StepName: "PROMOTION", ProgressTotal: 0, Icon: testIconGold},
			{StepName: "RELEGATION", ProgressTotal: 0, Icon: testIconPlatinum},
			{StepName: "Platinum III", ProgressTotal: 500, Icon: testIconPlatinum},
			{StepName: "Platinum II", ProgressTotal: 500, Icon: testIconPlatinum},
			{StepName: "Platinum I", ProgressTotal: 499, Icon: testIconPlatinum},
			{StepName: "PROMOTION", ProgressTotal: 1, Icon: testIconPlatinum},
			{StepName: "RELEGATION", ProgressTotal: 1, Icon: testIconAdept},
			{StepName: "Adept III", ProgressTotal: 499, Icon: testIconAdept},
			{StepName: "Adept II", ProgressTotal: 500, Icon: testIconAdept},
			{StepName: "Adept I", ProgressTotal: 499, Icon: testIconAdept},
			{StepName: "PROMOTION", ProgressTotal: 1, Icon: testIconAdept},
			{StepName: "RELEGATION", ProgressTotal: 1, Icon: testIconAscendant},
			{StepName: "Ascendant III", ProgressTotal: 499, Icon: testIconAscendant},
			{StepName: "Ascendant II", ProgressTotal: 375, Icon: testIconAscendant},
			{StepName: "Ascendant I", ProgressTotal: 375, Icon: testIconAscendant},
			{StepName: "Ascendant I", ProgressTotal: 1, Icon: testIconAscendant},
			{StepName: "Ascendant 0", ProgressTotal: 4999, Icon: testIconAscendant},
		},
	}
}

type fakeCompManifestService struct {
	ManifestService
	prog *ProgressionDefinition
	err  error
}

func (f *fakeCompManifestService) GetProgression(_ context.Context, hash int64) (*ProgressionDefinition, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.prog != nil && f.prog.Hash == hash {
		return f.prog, nil
	}
	return nil, fmt.Errorf("progression %d not found", hash)
}

type fakeBungieClient struct {
	bungie.ClientWithResponsesInterface
	profileResp        *bungie.Destiny2GetProfileResponse
	profileErr         error
	capturedMemType    int32
	capturedMemID      int64
	capturedComponents []int32
}

func (f *fakeBungieClient) Destiny2GetProfileWithResponse(
	_ context.Context,
	membershipType int32,
	destinyMembershipId int64,
	params *bungie.Destiny2GetProfileParams,
	_ ...bungie.RequestEditorFn,
) (*bungie.Destiny2GetProfileResponse, error) {
	f.capturedMemType = membershipType
	f.capturedMemID = destinyMembershipId
	if params != nil && params.Components != nil {
		f.capturedComponents = append([]int32(nil), (*params.Components)...)
	}
	return f.profileResp, f.profileErr
}

func newOKProfileResponse(resp *bungie.DestinyResponsesDestinyProfileResponse) *bungie.Destiny2GetProfileResponse {
	return &bungie.Destiny2GetProfileResponse{
		HTTPResponse: &http.Response{StatusCode: http.StatusOK},
		JSON200: &bungie.DestinyProfileResponse{
			Response: resp,
		},
	}
}

func TestGetCompetitiveRank_ProgressionPrimaryPath(t *testing.T) {
	charID := "2305843009261519028"
	points := int32(5820)
	stepIdx := int32(21) // Platinum III

	fakeClient := &fakeBungieClient{
		profileResp: newOKProfileResponse(&bungie.DestinyResponsesDestinyProfileResponse{
			CharacterProgressions: &bungie.DictionaryComponentResponseOfint64AndDestinyCharacterProgressionComponent{
				Data: &map[string]bungie.DestinyEntitiesCharactersDestinyCharacterProgressionComponent{
					charID: {
						Progressions: &map[string]bungie.DestinyDestinyProgression{
							CompProgressionHash: {
								CurrentProgress: &points,
								StepIndex:       &stepIdx,
							},
						},
					},
				},
			},
		}),
	}

	svc := &service{
		Client:          fakeClient,
		ManifestService: &fakeCompManifestService{prog: testCompProgressionFixture()},
	}

	snap, err := svc.GetCompetitiveRank(context.Background(), 4611686018467484863, 3, charID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(fakeClient.capturedComponents) != 2 || fakeClient.capturedComponents[0] != CharacterProgressionsCode || fakeClient.capturedComponents[1] != MetricsCode {
		t.Fatalf("expected components [202, 1100], got %v", fakeClient.capturedComponents)
	}
	if snap.Points != 5820 {
		t.Fatalf("expected Points=5820, got %d", snap.Points)
	}
	if snap.StepIndex == nil || *snap.StepIndex != 21 {
		t.Fatalf("expected StepIndex=21, got %v", snap.StepIndex)
	}
	if snap.DivisionName == nil || *snap.DivisionName != "Platinum III" {
		t.Fatalf("expected DivisionName='Platinum III', got %v", snap.DivisionName)
	}
	if snap.Icon == nil || *snap.Icon != testIconPlatinum {
		t.Fatalf("expected Icon=%q, got %v", testIconPlatinum, snap.Icon)
	}
	if snap.RecordedAt == nil || snap.RecordedAt.IsZero() {
		t.Fatal("expected non-zero RecordedAt")
	}
}

func TestGetCompetitiveRank_PrivacyRestrictedMetricFallback(t *testing.T) {
	charID := "2305843009261519028"
	privacy := int32(2)
	metricPoints := int32(7800) // Adept II (7750..8249)

	fakeClient := &fakeBungieClient{
		profileResp: newOKProfileResponse(&bungie.DestinyResponsesDestinyProfileResponse{
			CharacterProgressions: &bungie.DictionaryComponentResponseOfint64AndDestinyCharacterProgressionComponent{
				Data:    nil,
				Privacy: &privacy,
			},
			Metrics: &bungie.SingleComponentResponseOfDestinyMetricsComponent{
				Data: &bungie.DestinyComponentsMetricsDestinyMetricsComponent{
					Metrics: &map[string]bungie.DestinyComponentsMetricsDestinyMetricComponent{
						CompMetricHash: {
							ObjectiveProgress: &bungie.DestinyQuestsDestinyObjectiveProgress{
								Progress: &metricPoints,
							},
						},
					},
				},
			},
		}),
	}

	svc := &service{
		Client:          fakeClient,
		ManifestService: &fakeCompManifestService{prog: testCompProgressionFixture()},
	}

	snap, err := svc.GetCompetitiveRank(context.Background(), 4611686018467484863, 3, charID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if snap.Points != 7800 {
		t.Fatalf("expected Points=7800, got %d", snap.Points)
	}
	if snap.StepIndex != nil {
		t.Fatalf("expected nil StepIndex on Metrics fallback, got %v", *snap.StepIndex)
	}
	if snap.DivisionName == nil || *snap.DivisionName != "Adept II" {
		t.Fatalf("expected DivisionName='Adept II', got %v", snap.DivisionName)
	}
	if snap.Icon == nil || *snap.Icon != testIconAdept {
		t.Fatalf("expected Icon=%q, got %v", testIconAdept, snap.Icon)
	}
}

func TestGetCompetitiveRank_MissingManifestStillCapturesPoints(t *testing.T) {
	charID := "2305843009261519028"
	points := int32(6100)
	stepIdx := int32(21)

	fakeClient := &fakeBungieClient{
		profileResp: newOKProfileResponse(&bungie.DestinyResponsesDestinyProfileResponse{
			CharacterProgressions: &bungie.DictionaryComponentResponseOfint64AndDestinyCharacterProgressionComponent{
				Data: &map[string]bungie.DestinyEntitiesCharactersDestinyCharacterProgressionComponent{
					charID: {
						Progressions: &map[string]bungie.DestinyDestinyProgression{
							CompProgressionHash: {
								CurrentProgress: &points,
								StepIndex:       &stepIdx,
							},
						},
					},
				},
			},
		}),
	}

	svc := &service{
		Client:          fakeClient,
		ManifestService: &fakeCompManifestService{err: fmt.Errorf("manifest unavailable")},
	}

	snap, err := svc.GetCompetitiveRank(context.Background(), 4611686018467484863, 3, charID)
	if err != nil {
		t.Fatalf("expected rank points to succeed even when manifest is missing, got: %v", err)
	}
	if snap.Points != 6100 || snap.StepIndex == nil || *snap.StepIndex != 21 {
		t.Fatalf("unexpected snapshot: %+v", snap)
	}
	if snap.DivisionName != nil || snap.Icon != nil {
		t.Fatalf("expected nil DivisionName and Icon when manifest is missing, got %+v", snap)
	}
}

func TestProgressionStepForPoints_CompAndGenericProgressions(t *testing.T) {
	compDef := testCompProgressionFixture()

	compCases := []struct {
		points   int
		wantName string
		wantIcon string
	}{
		{points: 0, wantName: "Untested", wantIcon: testIconUntested},
		{points: 1, wantName: "Copper III", wantIcon: testIconCopper},
		{points: 374, wantName: "Copper III", wantIcon: testIconCopper},
		{points: 375, wantName: "Copper II", wantIcon: testIconCopper},
		{points: 5820, wantName: "Platinum III", wantIcon: testIconPlatinum},
		{points: 7249, wantName: "Platinum I", wantIcon: testIconPlatinum}, // 1-point PROMOTION boundary maps to Platinum I
		{points: 7250, wantName: "Adept III", wantIcon: testIconAdept},     // 1-point RELEGATION boundary maps to Adept III
		{points: 10000, wantName: "Ascendant I", wantIcon: testIconAscendant},
		{points: 10001, wantName: "Ascendant 0", wantIcon: testIconAscendant},
		{points: 16000, wantName: "Ascendant 0", wantIcon: testIconAscendant},
	}

	for _, tc := range compCases {
		step := compDef.StepForPoints(tc.points)
		if step == nil || step.StepName != tc.wantName || step.Icon != tc.wantIcon {
			t.Fatalf("points=%d: expected (%q, %q), got %+v", tc.points, tc.wantName, tc.wantIcon, step)
		}
	}

	// Verify StepForPoints also works generically for non-Competitive progressions (e.g., Crucible / Trials Rank)
	crucibleDef := &ProgressionDefinition{
		Hash: 2083746873,
		Steps: []ProgressionStepDefinition{
			{StepName: "GUARDIAN I", ProgressTotal: 50, Icon: "/icons/g1.png"},
			{StepName: "GUARDIAN II", ProgressTotal: 75, Icon: "/icons/g2.png"},
			{StepName: "LEGEND", ProgressTotal: 1000, Icon: "/icons/legend.png"},
		},
	}
	if s := crucibleDef.StepForPoints(25); s == nil || s.StepName != "GUARDIAN I" {
		t.Fatalf("expected GUARDIAN I at 25 points, got %+v", s)
	}
	if s := crucibleDef.StepForPoints(60); s == nil || s.StepName != "GUARDIAN II" {
		t.Fatalf("expected GUARDIAN II at 60 points, got %+v", s)
	}
	if s := crucibleDef.StepForPoints(5000); s == nil || s.StepName != "LEGEND" {
		t.Fatalf("expected LEGEND at overflow points, got %+v", s)
	}
}

func TestGetCompetitiveRank_UntestedAndPromotionAndAscendantZeroSteps(t *testing.T) {
	charID := "2305843009261519028"

	tests := []struct {
		name         string
		points       int32
		stepIndex    int32
		wantDivision string
		wantIcon     string
	}{
		{
			name:         "Untested placement step",
			points:       0,
			stepIndex:    0,
			wantDivision: "Untested",
			wantIcon:     testIconUntested,
		},
		{
			name:         "PROMOTION series between Platinum and Adept",
			points:       7249,
			stepIndex:    24,
			wantDivision: "PROMOTION",
			wantIcon:     testIconPlatinum,
		},
		{
			name:         "RELEGATION series between Adept and Platinum",
			points:       7250,
			stepIndex:    25,
			wantDivision: "RELEGATION",
			wantIcon:     testIconAdept,
		},
		{
			name:         "Ascendant 0 high rating",
			points:       12450,
			stepIndex:    35,
			wantDivision: "Ascendant 0",
			wantIcon:     testIconAscendant,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fakeClient := &fakeBungieClient{
				profileResp: newOKProfileResponse(&bungie.DestinyResponsesDestinyProfileResponse{
					CharacterProgressions: &bungie.DictionaryComponentResponseOfint64AndDestinyCharacterProgressionComponent{
						Data: &map[string]bungie.DestinyEntitiesCharactersDestinyCharacterProgressionComponent{
							charID: {
								Progressions: &map[string]bungie.DestinyDestinyProgression{
									CompProgressionHash: {
										CurrentProgress: ptr.Of(tc.points),
										StepIndex:       ptr.Of(tc.stepIndex),
									},
								},
							},
						},
					},
				}),
			}

			svc := &service{
				Client:          fakeClient,
				ManifestService: &fakeCompManifestService{prog: testCompProgressionFixture()},
			}

			snap, err := svc.GetCompetitiveRank(context.Background(), 12345, 3, charID)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if snap.Points != int(tc.points) {
				t.Fatalf("expected Points=%d, got %d", tc.points, snap.Points)
			}
			if snap.StepIndex == nil || *snap.StepIndex != int(tc.stepIndex) {
				t.Fatalf("expected StepIndex=%d, got %v", tc.stepIndex, snap.StepIndex)
			}
			if snap.DivisionName == nil || *snap.DivisionName != tc.wantDivision {
				t.Fatalf("expected DivisionName=%q, got %v", tc.wantDivision, snap.DivisionName)
			}
			if snap.Icon == nil || *snap.Icon != tc.wantIcon {
				t.Fatalf("expected Icon=%q, got %v", tc.wantIcon, snap.Icon)
			}
		})
	}
}
