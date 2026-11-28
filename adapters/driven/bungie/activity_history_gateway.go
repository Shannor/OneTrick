package bungie

import (
	"context"
	"fmt"
	"oneTrick/clients/bungie"
	"oneTrick/core/domain"
	"oneTrick/core/gamemode"
	"strconv"

	clog "github.com/Shannor/canonical-logger"
)

type ActivityHistoryManifestReader interface {
	GetActivitiesByIDs(ctx context.Context, ids []int64) (map[int64]domain.ActivityManifest, error)
	GetActivityModesByIDs(ctx context.Context, ids []int64) (map[int64]domain.ActivityModeManifest, error)
	GetActivityModes(ctx context.Context) (map[int64]domain.ActivityModeManifest, error)
}

type ActivityHistoryGateway struct {
	client   bungie.ClientWithResponsesInterface
	manifest ActivityHistoryManifestReader
}

func NewActivityHistoryGateway(
	client bungie.ClientWithResponsesInterface,
	manifest ActivityHistoryManifestReader,
) *ActivityHistoryGateway {
	return &ActivityHistoryGateway{
		client:   client,
		manifest: manifest,
	}
}

func (g *ActivityHistoryGateway) GetActivityHistory(
	ctx context.Context,
	membershipID string,
	membershipType int64,
	characterID string,
	mode domain.ActivityQueryMode,
	count int64,
	page int64,
) ([]domain.ActivityInfo, error) {
	cID, err := strconv.ParseInt(characterID, 10, 64)
	if err != nil {
		return nil, err
	}
	mID, err := strconv.ParseInt(membershipID, 10, 64)
	if err != nil {
		return nil, err
	}

	bungieMode := toBungieActivityQueryMode(mode)
	clog.WithGroupFields(ctx, "bungieActivityHistory.getActivityHistory", clog.Fields{
		"membershipId":   membershipID,
		"membershipType": membershipType,
		"characterId":    characterID,
		"count":          count,
		"mode":           int64(bungieMode),
		"page":           page,
	})

	resp, err := g.client.Destiny2GetActivityHistoryWithResponse(
		ctx,
		int32(membershipType),
		mID,
		cID,
		&bungie.Destiny2GetActivityHistoryParams{
			Count: new(int32(count)),
			Mode:  new(int32(bungieMode)),
			Page:  new(int32(page)),
		},
	)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("no response found")
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
	if err := checkBungieResponse(ctx, "bungieActivityHistory.getActivityHistory", resp.StatusCode(), resp.Body, env); err != nil {
		return nil, err
	}
	if resp.JSON200 == nil || resp.JSON200.Response == nil {
		return nil, fmt.Errorf("no response found")
	}
	if resp.JSON200.Response.Activities == nil {
		return nil, fmt.Errorf("no activities found")
	}

	periods := *resp.JSON200.Response.Activities
	activityIDs := make([]int64, 0, len(periods)*2)
	for _, p := range periods {
		if p.ActivityDetails == nil {
			continue
		}
		if p.ActivityDetails.ReferenceId != nil {
			activityIDs = append(activityIDs, int64(*p.ActivityDetails.ReferenceId))
		}
		if p.ActivityDetails.DirectorActivityHash != nil {
			activityIDs = append(activityIDs, int64(*p.ActivityDetails.DirectorActivityHash))
		}
	}

	activities, err := g.manifest.GetActivitiesByIDs(ctx, activityIDs)
	if err != nil {
		return nil, fmt.Errorf("cannot enhance activities: %w", err)
	}

	modeIDs := make([]int64, 0, len(activities))
	for _, act := range activities {
		if act.DirectActivityModeHash != 0 {
			modeIDs = append(modeIDs, act.DirectActivityModeHash)
		}
	}

	modes, err := g.manifest.GetActivityModesByIDs(ctx, modeIDs)
	if err != nil {
		return nil, fmt.Errorf("cannot enhance activities: %w", err)
	}

	needsModeTypeFallback := false
	for _, p := range periods {
		if p.ActivityDetails == nil || p.ActivityDetails.DirectorActivityHash == nil || p.ActivityDetails.Mode == nil || *p.ActivityDetails.Mode == 0 {
			continue
		}
		act, ok := activities[int64(*p.ActivityDetails.DirectorActivityHash)]
		if !ok {
			continue
		}
		if _, found := modes[act.DirectActivityModeHash]; !found {
			needsModeTypeFallback = true
			break
		}
	}
	if needsModeTypeFallback {
		if allModes, modeErr := g.manifest.GetActivityModes(ctx); modeErr == nil {
			modes = allModes
		}
	}

	history := transformPeriodGroups(periods, activities, modes)
	clog.WithGroupField(ctx, "bungieActivityHistory.getActivityHistory", "returnedCount", len(history))
	return history, nil
}

func toBungieActivityQueryMode(mode domain.ActivityQueryMode) bungie.CurrentActivityModeType {
	switch mode {
	case domain.ActivityQueryCompetitive:
		return bungie.CurrentActivityModeTypePvPCompetitive
	case domain.ActivityQueryQuickplay:
		return bungie.CurrentActivityModeTypePvPQuickplay
	case domain.ActivityQueryIronBanner:
		return bungie.CurrentActivityModeTypeIronBanner
	default:
		return bungie.CurrentActivityModeTypeAllPvP
	}
}

func transformPeriodGroups(
	periods []bungie.StatsPeriodGroup,
	activities map[int64]domain.ActivityManifest,
	modes map[int64]domain.ActivityModeManifest,
) []domain.ActivityInfo {
	if periods == nil {
		return nil
	}
	result := make([]domain.ActivityInfo, 0, len(periods))
	for _, group := range periods {
		if info, ok := transformPeriodGroup(group, activities, modes); ok {
			result = append(result, info)
		}
	}
	return result
}

func transformPeriodGroup(
	period bungie.StatsPeriodGroup,
	activities map[int64]domain.ActivityManifest,
	modes map[int64]domain.ActivityModeManifest,
) (domain.ActivityInfo, bool) {
	if period.ActivityDetails == nil {
		return domain.ActivityInfo{}, false
	}
	d := period.ActivityDetails
	if d.ReferenceId == nil || d.DirectorActivityHash == nil || d.InstanceId == nil {
		return domain.ActivityInfo{}, false
	}

	definition, ok := activities[int64(*d.ReferenceId)]
	if !ok {
		return domain.ActivityInfo{}, false
	}
	director, ok := activities[int64(*d.DirectorActivityHash)]
	if !ok {
		return domain.ActivityInfo{}, false
	}

	activityMode, ok := modes[director.DirectActivityModeHash]
	if !ok && d.Mode != nil {
		targetModeType := int(*d.Mode)
		for _, m := range modes {
			if m.ModeType == targetModeType {
				activityMode = m
				break
			}
		}
	}

	rawMode := activityModeTypeToString((*bungie.CurrentActivityModeType)(d.Mode))
	mode := gamemode.NormalizeModeName(rawMode, director.Name)

	info := domain.ActivityInfo{
		ReferenceID:       int64(*d.ReferenceId),
		ActivityHash:      int64(*d.DirectorActivityHash),
		InstanceID:        *d.InstanceId,
		Location:          definition.Name,
		Description:       definition.Description,
		Activity:          director.Name,
		ImageURL:          formatBungieURL(definition.PgcrImageURL),
		ActivityIcon:      formatBungieURL(activityMode.IconURL),
		Mode:              mode,
		EstimatedGameMode: gamemode.EstimateName(mode, director.Name),
	}
	if period.Period != nil {
		info.Period = *period.Period
	}
	if d.IsPrivate != nil {
		info.IsPrivate = *d.IsPrivate
		info.HasIsPrivate = true
	}
	return info, true
}
