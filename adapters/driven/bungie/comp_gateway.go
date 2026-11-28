package bungie

import (
	"context"
	"fmt"
	"oneTrick/clients/bungie"
	"oneTrick/core/domain"
	"oneTrick/core/fireteam"
	"time"

	clog "github.com/Shannor/canonical-logger"
)

const (
	compProgressionHash      = "3696598664"
	compProgressionHashInt64 = int64(3696598664)
	compMetricHash           = "268448617"
)

type ProgressionManifestReader interface {
	GetProgression(ctx context.Context, hash int64) (domain.ProgressionManifest, bool, error)
}

var _ fireteam.CompRankReader = (*CompRankGateway)(nil)

type CompRankGateway struct {
	client   bungie.ClientWithResponsesInterface
	manifest ProgressionManifestReader
}

func NewCompRankGateway(
	client bungie.ClientWithResponsesInterface,
	manifest ProgressionManifestReader,
) *CompRankGateway {
	return &CompRankGateway{
		client:   client,
		manifest: manifest,
	}
}

func (g *CompRankGateway) GetCompetitiveRank(
	ctx context.Context,
	primaryMembershipID int64,
	membershipType int64,
	characterID string,
) (domain.CompRankSnapshot, bool, error) {
	clog.WithGroupFields(ctx, "bungieComp.getCompetitiveRank", clog.Fields{
		"membershipId":   primaryMembershipID,
		"membershipType": membershipType,
		"characterId":    characterID,
	})

	components := []int32{componentCharacterProgressions, componentMetrics}
	params := &bungie.Destiny2GetProfileParams{
		Components: &components,
	}
	resp, err := g.client.Destiny2GetProfileWithResponse(ctx, int32(membershipType), primaryMembershipID, params)
	if err != nil {
		return domain.CompRankSnapshot{}, false, err
	}
	if resp == nil {
		return domain.CompRankSnapshot{}, false, fmt.Errorf("no response found")
	}

	var env bungieEnvelope
	if resp.JSON200 != nil {
		env = bungieEnvelope{
			ErrorCode:       resp.JSON200.ErrorCode,
			ThrottleSeconds: resp.JSON200.ThrottleSeconds,
			ErrorStatus:     resp.JSON200.ErrorStatus,
			Message:         resp.JSON200.Message,
		}
	}
	if err := checkBungieResponse(ctx, "bungieComp.getCompetitiveRank", resp.StatusCode(), resp.Body, env); err != nil {
		return domain.CompRankSnapshot{}, false, err
	}
	if resp.JSON200 == nil || resp.JSON200.Response == nil {
		return domain.CompRankSnapshot{}, false, fmt.Errorf("no response found")
	}

	var progDef *domain.ProgressionManifest
	if g.manifest != nil {
		def, found, manifestErr := g.manifest.GetProgression(ctx, compProgressionHashInt64)
		if manifestErr != nil {
			clog.WithGroupField(ctx, "bungieComp.getCompetitiveRank", "manifestWarning", manifestErr.Error())
		} else if found {
			progDef = &def
		}
	}

	snapshot, ok := extractCompetitiveRank(ctx, resp.JSON200.Response, characterID, progDef, time.Now().UTC())
	if !ok {
		return domain.CompRankSnapshot{}, false, nil
	}
	clog.WithGroupField(ctx, "bungieComp.getCompetitiveRank", "points", snapshot.Points)
	return snapshot, true, nil
}

// extractCompetitiveRank prefers CharacterProgressions["3696598664"] (Component 202) and
// falls back to Metrics["268448617"] (Component 1100) when player privacy settings omit
// CharacterProgressions.Data.
func extractCompetitiveRank(
	ctx context.Context,
	profile *bungie.DestinyResponsesDestinyProfileResponse,
	characterID string,
	progDef *domain.ProgressionManifest,
	recordedAt time.Time,
) (domain.CompRankSnapshot, bool) {
	if profile == nil {
		return domain.CompRankSnapshot{}, false
	}

	if profile.CharacterProgressions != nil && profile.CharacterProgressions.Data != nil {
		charMap := *profile.CharacterProgressions.Data
		var matchedProg *bungie.DestinyDestinyProgression

		if characterID != "" {
			if charComp, ok := charMap[characterID]; ok && charComp.Progressions != nil {
				if p, ok := (*charComp.Progressions)[compProgressionHash]; ok {
					matchedProg = &p
				}
			}
		}
		if matchedProg == nil {
			for _, charComp := range charMap {
				if charComp.Progressions != nil {
					if p, ok := (*charComp.Progressions)[compProgressionHash]; ok {
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
				if progDef != nil && idx >= 0 && idx < len(progDef.Steps) {
					divisionName = progDef.Steps[idx].StepName
					icon = progDef.Steps[idx].Icon
				}
			}
			if divisionName == "" && progDef != nil {
				if step, ok := progDef.StepForPoints(points); ok {
					divisionName = step.StepName
					icon = step.Icon
				}
			}

			clog.WithGroupField(ctx, "bungieComp.getCompetitiveRank", "source", "characterProgressions")
			return domain.CompRankSnapshot{
				Points:       points,
				StepIndex:    stepIndexPtr,
				DivisionName: divisionName,
				Icon:         icon,
				RecordedAt:   new(recordedAt),
			}, true
		}
	}

	if profile.Metrics != nil && profile.Metrics.Data != nil && profile.Metrics.Data.Metrics != nil {
		if metric, ok := (*profile.Metrics.Data.Metrics)[compMetricHash]; ok {
			if metric.ObjectiveProgress != nil && metric.ObjectiveProgress.Progress != nil {
				points := int(*metric.ObjectiveProgress.Progress)
				var divisionName, icon string
				if progDef != nil {
					if step, found := progDef.StepForPoints(points); found {
						divisionName = step.StepName
						icon = step.Icon
					}
				}
				clog.WithGroupField(ctx, "bungieComp.getCompetitiveRank", "source", "metrics")
				return domain.CompRankSnapshot{
					Points:       points,
					DivisionName: divisionName,
					Icon:         icon,
					RecordedAt:   new(recordedAt),
				}, true
			}
		}
	}

	return domain.CompRankSnapshot{}, false
}
