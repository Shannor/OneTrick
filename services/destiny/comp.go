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
			var divisionNamePtr, iconPtr *string
			if matchedProg.StepIndex != nil {
				idx := int(*matchedProg.StepIndex)
				stepIndexPtr = &idx
				if progDef != nil && idx >= 0 && idx < len(progDef.Steps) {
					if progDef.Steps[idx].StepName != "" {
						divisionNamePtr = ptr.Of(progDef.Steps[idx].StepName)
					}
					if progDef.Steps[idx].Icon != "" {
						iconPtr = ptr.Of(progDef.Steps[idx].Icon)
					}
				}
			}
			if divisionNamePtr == nil {
				if step := progDef.StepForPoints(points); step != nil {
					if step.StepName != "" {
						divisionNamePtr = ptr.Of(step.StepName)
					}
					if step.Icon != "" {
						iconPtr = ptr.Of(step.Icon)
					}
				}
			}

			return &api.CompRankSnapshot{
				Points:       points,
				StepIndex:    stepIndexPtr,
				DivisionName: divisionNamePtr,
				Icon:         iconPtr,
				RecordedAt:   ptr.Of(recordedAt),
			}, "characterProgressions", nil
		}
	}

	// 2. Privacy-Resilient Fallback Source: Metrics (Component 1100) -> Metric Hash 268448617
	if profile.Metrics != nil && profile.Metrics.Data != nil && profile.Metrics.Data.Metrics != nil {
		if metric, ok := (*profile.Metrics.Data.Metrics)[CompMetricHash]; ok {
			if metric.ObjectiveProgress != nil && metric.ObjectiveProgress.Progress != nil {
				points := int(*metric.ObjectiveProgress.Progress)
				var divisionNamePtr, iconPtr *string
				if step := progDef.StepForPoints(points); step != nil {
					if step.StepName != "" {
						divisionNamePtr = ptr.Of(step.StepName)
					}
					if step.Icon != "" {
						iconPtr = ptr.Of(step.Icon)
					}
				}
				return &api.CompRankSnapshot{
					Points:       points,
					DivisionName: divisionNamePtr,
					Icon:         iconPtr,
					RecordedAt:   ptr.Of(recordedAt),
				}, "metrics", nil
			}
		}
	}

	return nil, "", fmt.Errorf("competitive rank data not available")
}

func (a *service) GetCompetitiveRank(ctx context.Context, membershipID int64, membershipType int64, characterID string) (*api.CompRankSnapshot, error) {
	clog.WithGroupFields(ctx, "destinyService.getCompetitiveRank", clog.Fields{
		"membershipId":   membershipID,
		"membershipType": membershipType,
		"characterId":    characterID,
	})

	components := []int32{CharacterProgressionsCode, MetricsCode}
	params := &bungie.Destiny2GetProfileParams{
		Components: &components,
	}

	resp, err := a.Client.Destiny2GetProfileWithResponse(ctx, int32(membershipType), membershipID, params)
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
		if err != nil {
			clog.WithGroupField(ctx, "destinyService.getCompetitiveRank", "manifestWarning", err.Error())
		} else {
			progDef = def
		}
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
