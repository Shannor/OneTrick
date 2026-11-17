package destiny

import (
	"context"
	"fmt"
	"net/http"
	"oneTrick/api"
	"oneTrick/clients/bungie"
	"oneTrick/ptr"
	"time"

	clog "github.com/Shannor/canonical-logger"
)

const (
	compIconUntested  = "/common/destiny2_content/icons/646cc5780b406e6565008dde5f4fd60e.png"
	compIconCopper    = "/common/destiny2_content/icons/3cb3f1c32d988eb2728cea3129827baa.png"
	compIconBronze    = "/common/destiny2_content/icons/a0fa7eb347a499409ef7b41f4e95faeb.png"
	compIconSilver    = "/common/destiny2_content/icons/a2fda602ff4b68973fb433d0368af7a3.png"
	compIconGold      = "/common/destiny2_content/icons/e654eadefa60635fa2e1c741675866cd.png"
	compIconPlatinum  = "/common/destiny2_content/icons/2756e440c14fc72ef870df75c0d9d5d9.png"
	compIconAdept     = "/common/destiny2_content/icons/e86307d8ae6e32b7cd9ea2cffc4d9de1.png"
	compIconAscendant = "/common/destiny2_content/icons/2341581763d68250bc2e3b95e4cd2b02.png"
)

type compThreshold struct {
	minPoints    int
	stepIndex    int
	divisionName string
	icon         string
}

var compPointThresholds = []compThreshold{
	{minPoints: 10000, stepIndex: 35, divisionName: "Ascendant 0", icon: compIconAscendant},
	{minPoints: 9750, stepIndex: 33, divisionName: "Ascendant I", icon: compIconAscendant},
	{minPoints: 9250, stepIndex: 32, divisionName: "Ascendant II", icon: compIconAscendant},
	{minPoints: 8750, stepIndex: 31, divisionName: "Ascendant III", icon: compIconAscendant},
	{minPoints: 8250, stepIndex: 28, divisionName: "Adept I", icon: compIconAdept},
	{minPoints: 7750, stepIndex: 27, divisionName: "Adept II", icon: compIconAdept},
	{minPoints: 7250, stepIndex: 26, divisionName: "Adept III", icon: compIconAdept},
	{minPoints: 6750, stepIndex: 23, divisionName: "Platinum I", icon: compIconPlatinum},
	{minPoints: 6250, stepIndex: 22, divisionName: "Platinum II", icon: compIconPlatinum},
	{minPoints: 5750, stepIndex: 21, divisionName: "Platinum III", icon: compIconPlatinum},
	{minPoints: 5250, stepIndex: 18, divisionName: "Gold I", icon: compIconGold},
	{minPoints: 4750, stepIndex: 17, divisionName: "Gold II", icon: compIconGold},
	{minPoints: 4250, stepIndex: 16, divisionName: "Gold III", icon: compIconGold},
	{minPoints: 3750, stepIndex: 13, divisionName: "Silver I", icon: compIconSilver},
	{minPoints: 3250, stepIndex: 12, divisionName: "Silver II", icon: compIconSilver},
	{minPoints: 2750, stepIndex: 11, divisionName: "Silver III", icon: compIconSilver},
	{minPoints: 2250, stepIndex: 8, divisionName: "Bronze I", icon: compIconBronze},
	{minPoints: 1750, stepIndex: 7, divisionName: "Bronze II", icon: compIconBronze},
	{minPoints: 1250, stepIndex: 6, divisionName: "Bronze III", icon: compIconBronze},
	{minPoints: 750, stepIndex: 3, divisionName: "Copper I", icon: compIconCopper},
	{minPoints: 250, stepIndex: 2, divisionName: "Copper II", icon: compIconCopper},
	{minPoints: 0, stepIndex: 1, divisionName: "Copper III", icon: compIconCopper},
}

// DefaultCompProgressionDefinition returns the DestinyProgressionDefinition (3696598664)
// Competitive Division steps so Competitive rank lookups always have step names and badge icons.
func DefaultCompProgressionDefinition() *ProgressionDefinition {
	return &ProgressionDefinition{
		Hash: CompProgressionHashInt64,
		DisplayProperties: ModeDisplayProperties{
			Name:    "Competitive Division",
			Icon:    compIconUntested,
			HasIcon: true,
		},
		Steps: []ProgressionStepDefinition{
			{StepName: "Untested", ProgressTotal: 1, Icon: compIconUntested},
			{StepName: "Copper III", ProgressTotal: 374, Icon: compIconCopper},
			{StepName: "Copper II", ProgressTotal: 375, Icon: compIconCopper},
			{StepName: "Copper I", ProgressTotal: 500, Icon: compIconCopper},
			{StepName: "PROMOTION", ProgressTotal: 0, Icon: compIconCopper},
			{StepName: "RELEGATION", ProgressTotal: 0, Icon: compIconBronze},
			{StepName: "Bronze III", ProgressTotal: 500, Icon: compIconBronze},
			{StepName: "Bronze II", ProgressTotal: 500, Icon: compIconBronze},
			{StepName: "Bronze I", ProgressTotal: 500, Icon: compIconBronze},
			{StepName: "PROMOTION", ProgressTotal: 0, Icon: compIconBronze},
			{StepName: "RELEGATION", ProgressTotal: 0, Icon: compIconSilver},
			{StepName: "Silver III", ProgressTotal: 500, Icon: compIconSilver},
			{StepName: "Silver II", ProgressTotal: 500, Icon: compIconSilver},
			{StepName: "Silver I", ProgressTotal: 500, Icon: compIconSilver},
			{StepName: "PROMOTION", ProgressTotal: 0, Icon: compIconSilver},
			{StepName: "RELEGATION", ProgressTotal: 0, Icon: compIconGold},
			{StepName: "Gold III", ProgressTotal: 500, Icon: compIconGold},
			{StepName: "Gold II", ProgressTotal: 500, Icon: compIconGold},
			{StepName: "Gold I", ProgressTotal: 500, Icon: compIconGold},
			{StepName: "PROMOTION", ProgressTotal: 0, Icon: compIconGold},
			{StepName: "RELEGATION", ProgressTotal: 0, Icon: compIconPlatinum},
			{StepName: "Platinum III", ProgressTotal: 500, Icon: compIconPlatinum},
			{StepName: "Platinum II", ProgressTotal: 500, Icon: compIconPlatinum},
			{StepName: "Platinum I", ProgressTotal: 499, Icon: compIconPlatinum},
			{StepName: "PROMOTION", ProgressTotal: 1, Icon: compIconPlatinum},
			{StepName: "RELEGATION", ProgressTotal: 1, Icon: compIconAdept},
			{StepName: "Adept III", ProgressTotal: 499, Icon: compIconAdept},
			{StepName: "Adept II", ProgressTotal: 500, Icon: compIconAdept},
			{StepName: "Adept I", ProgressTotal: 499, Icon: compIconAdept},
			{StepName: "PROMOTION", ProgressTotal: 1, Icon: compIconAdept},
			{StepName: "RELEGATION", ProgressTotal: 1, Icon: compIconAscendant},
			{StepName: "Ascendant III", ProgressTotal: 499, Icon: compIconAscendant},
			{StepName: "Ascendant II", ProgressTotal: 375, Icon: compIconAscendant},
			{StepName: "Ascendant I", ProgressTotal: 375, Icon: compIconAscendant},
			{StepName: "Ascendant I", ProgressTotal: 1, Icon: compIconAscendant},
			{StepName: "Ascendant 0", ProgressTotal: 4999, Icon: compIconAscendant},
		},
	}
}

// CompDivisionForPoints resolves a Competitive Division name and badge icon from numerical points.
func CompDivisionForPoints(points int, progDef *ProgressionDefinition) (string, string) {
	for _, t := range compPointThresholds {
		if points >= t.minPoints {
			if progDef != nil && t.stepIndex >= 0 && t.stepIndex < len(progDef.Steps) {
				step := progDef.Steps[t.stepIndex]
				if step.StepName != "" {
					icon := step.Icon
					if icon == "" {
						icon = t.icon
					}
					return step.StepName, icon
				}
			}
			return t.divisionName, t.icon
		}
	}
	return "Copper III", compIconCopper
}

// ExtractCompetitiveRank extracts a CompRankSnapshot from a Bungie profile response,
// preferring CharacterProgressions["3696598664"] and falling back to Metrics["268448617"]
// when CharacterProgressions.Data is omitted due to player privacy settings.
func ExtractCompetitiveRank(
	profile *bungie.DestinyResponsesDestinyProfileResponse,
	characterID string,
	progDef *ProgressionDefinition,
	recordedAt time.Time,
) (*api.CompRankSnapshot, string, error) {
	if profile == nil {
		return nil, "", fmt.Errorf("profile response is nil")
	}
	if progDef == nil {
		progDef = DefaultCompProgressionDefinition()
	}

	// 1. Primary Rich Source: CharacterProgressions (Component 202) -> Progression Hash 3696598664
	if profile.CharacterProgressions != nil && profile.CharacterProgressions.Data != nil {
		charMap := *profile.CharacterProgressions.Data
		var matchedProg *bungie.DestinyDestinyProgression

		if characterID != "" {
			if charComp, ok := charMap[characterID]; ok && charComp.Progressions != nil {
				if p, ok := (*charComp.Progressions)[CompProgressionHash]; ok {
					matchedProg = &p
				}
			}
		}
		if matchedProg == nil {
			for _, charComp := range charMap {
				if charComp.Progressions != nil {
					if p, ok := (*charComp.Progressions)[CompProgressionHash]; ok {
						progCopy := p
						matchedProg = &progCopy
						break
					}
				}
			}
		}

		if matchedProg != nil {
			points := 0
			if matchedProg.CurrentProgress != nil {
				points = int(*matchedProg.CurrentProgress)
			}

			var stepIndexPtr *int
			var divisionName, icon string
			if matchedProg.StepIndex != nil {
				idx := int(*matchedProg.StepIndex)
				stepIndexPtr = &idx
				if idx >= 0 && idx < len(progDef.Steps) {
					divisionName = progDef.Steps[idx].StepName
					icon = progDef.Steps[idx].Icon
				}
			}
			if divisionName == "" {
				divisionName, icon = CompDivisionForPoints(points, progDef)
			}

			return &api.CompRankSnapshot{
				Points:       points,
				StepIndex:    stepIndexPtr,
				DivisionName: ptr.Of(divisionName),
				Icon:         ptr.Of(icon),
				RecordedAt:   ptr.Of(recordedAt),
			}, "characterProgressions", nil
		}
	}

	// 2. Privacy-Resilient Fallback Source: Metrics (Component 1100) -> Metric Hash 268448617
	if profile.Metrics != nil && profile.Metrics.Data != nil && profile.Metrics.Data.Metrics != nil {
		if metric, ok := (*profile.Metrics.Data.Metrics)[CompMetricHash]; ok {
			if metric.ObjectiveProgress != nil && metric.ObjectiveProgress.Progress != nil {
				points := int(*metric.ObjectiveProgress.Progress)
				divisionName, icon := CompDivisionForPoints(points, progDef)
				return &api.CompRankSnapshot{
					Points:       points,
					DivisionName: ptr.Of(divisionName),
					Icon:         ptr.Of(icon),
					RecordedAt:   ptr.Of(recordedAt),
				}, "metrics", nil
			}
		}
	}

	return nil, "", fmt.Errorf("competitive rank data not available")
}

func (a *service) GetCompetitiveRank(ctx context.Context, membershipID int64, membershipType int64, characterID string, accessToken *string) (*api.CompRankSnapshot, error) {
	clog.WithGroupFields(ctx, "destinyService.getCompetitiveRank", clog.Fields{
		"membershipId":   membershipID,
		"membershipType": membershipType,
		"characterId":    characterID,
	})

	components := []int32{CharacterProgressionsCode, MetricsCode}
	params := &bungie.Destiny2GetProfileParams{
		Components: &components,
	}

	var reqEditors []bungie.RequestEditorFn
	if accessToken != nil && *accessToken != "" {
		token := *accessToken
		reqEditors = append(reqEditors, func(_ context.Context, req *http.Request) error {
			req.Header.Set("Authorization", "Bearer "+token)
			return nil
		})
	}

	resp, err := a.Client.Destiny2GetProfileWithResponse(ctx, int32(membershipType), membershipID, params, reqEditors...)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode() != http.StatusOK {
		if resp.StatusCode() == http.StatusServiceUnavailable {
			return nil, ErrDestinyServerDown
		}
		clog.WithGroupField(ctx, "destinyService.getCompetitiveRank", "statusCode", resp.StatusCode())
		return nil, fmt.Errorf("failed to get competitive rank")
	}
	if resp.JSON200 == nil || resp.JSON200.Response == nil {
		return nil, fmt.Errorf("no response found")
	}

	var progDef *ProgressionDefinition
	if a.ManifestService != nil {
		def, err := a.ManifestService.GetProgression(ctx, CompProgressionHashInt64)
		if err == nil && def != nil {
			progDef = def
		}
	}
	if progDef == nil {
		progDef = DefaultCompProgressionDefinition()
	}

	snapshot, source, err := ExtractCompetitiveRank(resp.JSON200.Response, characterID, progDef, time.Now().UTC())
	if err != nil {
		return nil, err
	}

	clog.WithGroupFields(ctx, "destinyService.getCompetitiveRank", clog.Fields{
		"source": source,
		"points": snapshot.Points,
	})
	return snapshot, nil
}
