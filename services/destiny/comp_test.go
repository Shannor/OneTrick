package destiny

import (
	"context"
	"net/http"
	"oneTrick/clients/bungie"
	"oneTrick/ptr"
	"testing"
)

type fakeBungieClient struct {
	bungie.ClientWithResponsesInterface
	profileResp        *bungie.Destiny2GetProfileResponse
	profileErr         error
	capturedMemType    int32
	capturedMemID      int64
	capturedComponents []int32
	capturedAuthHeader string
}

func (f *fakeBungieClient) Destiny2GetProfileWithResponse(
	ctx context.Context,
	membershipType int32,
	destinyMembershipId int64,
	params *bungie.Destiny2GetProfileParams,
	reqEditors ...bungie.RequestEditorFn,
) (*bungie.Destiny2GetProfileResponse, error) {
	f.capturedMemType = membershipType
	f.capturedMemID = destinyMembershipId
	if params != nil && params.Components != nil {
		f.capturedComponents = append([]int32(nil), (*params.Components)...)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.bungie.net/Platform/Destiny2/Profile", nil)
	for _, ed := range reqEditors {
		_ = ed(ctx, req)
	}
	f.capturedAuthHeader = req.Header.Get("Authorization")
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
		ManifestService: NewManifestService(nil, "dev"),
	}

	token := "user-oauth-token-123"
	snap, err := svc.GetCompetitiveRank(context.Background(), 4611686018467484863, 3, charID, &token)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fakeClient.capturedAuthHeader != "Bearer user-oauth-token-123" {
		t.Fatalf("expected Authorization header 'Bearer user-oauth-token-123', got %q", fakeClient.capturedAuthHeader)
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
	if snap.Icon == nil || *snap.Icon != compIconPlatinum {
		t.Fatalf("expected Icon=%q, got %v", compIconPlatinum, snap.Icon)
	}
	if snap.RecordedAt == nil || snap.RecordedAt.IsZero() {
		t.Fatal("expected non-zero RecordedAt")
	}
}

func TestGetCompetitiveRank_PrivacyRestrictedMetricFallback(t *testing.T) {
	charID := "2305843009261519028"
	privacy := int32(2)
	metricPoints := int32(7800) // Adept II (threshold 7750)

	fakeClient := &fakeBungieClient{
		profileResp: newOKProfileResponse(&bungie.DestinyResponsesDestinyProfileResponse{
			// CharacterProgressions.Data is nil when user hides progressions in privacy settings
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
		ManifestService: NewManifestService(nil, "dev"),
	}

	snap, err := svc.GetCompetitiveRank(context.Background(), 4611686018467484863, 3, charID, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fakeClient.capturedAuthHeader != "" {
		t.Fatalf("expected empty Authorization header when token is nil, got %q", fakeClient.capturedAuthHeader)
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
	if snap.Icon == nil || *snap.Icon != compIconAdept {
		t.Fatalf("expected Icon=%q, got %v", compIconAdept, snap.Icon)
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
			wantIcon:     compIconUntested,
		},
		{
			name:         "PROMOTION series between Platinum and Adept",
			points:       7249,
			stepIndex:    24,
			wantDivision: "PROMOTION",
			wantIcon:     compIconPlatinum,
		},
		{
			name:         "RELEGATION series between Adept and Platinum",
			points:       7250,
			stepIndex:    25,
			wantDivision: "RELEGATION",
			wantIcon:     compIconAdept,
		},
		{
			name:         "Ascendant 0 high rating",
			points:       12450,
			stepIndex:    35,
			wantDivision: "Ascendant 0",
			wantIcon:     compIconAscendant,
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
				ManifestService: NewManifestService(nil, "dev"),
			}

			snap, err := svc.GetCompetitiveRank(context.Background(), 12345, 3, charID, nil)
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
