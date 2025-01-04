package api

import (
	"context"
	"fmt"
	"log/slog"
	"oneTrick/services"
	"strconv"
)

// ensure that we've conformed to the `ServerInterface` with a compile-time check
var _ StrictServerInterface = (*Server)(nil)

type Server struct {
	DestinyService services.DestinyService
}

func (s Server) GetPing(ctx context.Context, request GetPingRequestObject) (GetPingResponseObject, error) {
	return GetPing200JSONResponse{
		Ping: "pong",
	}, nil
}

func NewServer(service services.DestinyService) Server {
	return Server{
		DestinyService: service,
	}
}

const characterID = "2305843009261519028"
const primaryMembershipId = 4611686018434106050

func (s Server) GetActivities(ctx context.Context, request GetActivitiesRequestObject) (GetActivitiesResponseObject, error) {
	id, err := strconv.ParseInt(characterID, 10, 64)
	if err != nil {
		return nil, err
	}
	params := request.Params
	resp, err := s.DestinyService.GetAllPVPActivity(primaryMembershipId, id, params.Count, params.Page)
	if err != nil {
		slog.With("error", err.Error()).Error("Failed to fetch activity data")
		return nil, err
	}

	return GetActivities200JSONResponse(TransformPeriodGroups(resp)), nil
}
func (s Server) GetActivity(ctx context.Context, request GetActivityRequestObject) (GetActivityResponseObject, error) {
	activityId := request.ActivityId
	id, err := strconv.ParseInt(activityId, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid activity ID: %w", err)
	}
	activityDetails, resp, period, err := s.DestinyService.GetActivity(ctx, characterID, id)
	if err != nil {
		slog.With("error", err.Error()).Error("Failed to fetch weapon data for activity")
		return nil, fmt.Errorf("failed to fetch weapon data for activity: %w", err)
	}
	if activityDetails == nil {
		return nil, fmt.Errorf("no activity details found for activity ID: %s", activityId)
	}
	// 1. Get the closet snapshot(s)
	components, err := s.DestinyService.GetClosestSnapshot(primaryMembershipId, period)
	if err != nil {
		slog.With("error", err.Error()).Error("Failed to fetch snapshot")
		return nil, fmt.Errorf("failed to fetch snapshot: %w", err)
	}

	// 2 Compare refs with itemHash.
	mapping := map[int64]string{}
	for _, component := range components {
		mapping[int64(*component.ItemHash)] = *component.ItemInstanceId
	}
	// 3. Get weapon data
	var results []WeaponStats
	for _, stats := range resp {
		result := WeaponStats{}
		id, ok := mapping[int64(*stats.ReferenceId)]
		if !ok {
			slog.Warn("No instance id found for reference id: ", *stats.ReferenceId)
			continue
		}
		result.ReferenceId = stats.ReferenceId
		result.Stats = TransformD2HistoricalStatValues(stats.Values)
		details, err := s.DestinyService.GetWeaponDetails(ctx, strconv.Itoa(primaryMembershipId), id)
		if err != nil {
			slog.With(
				"error",
				err.Error(),
				"weapon instance id",
				id,
			).Error("failed to get details for weapon")
		}
		result.ItemDetails = TransformItemToDetails(details)
		results = append(results, result)
	}

	return GetActivity200JSONResponse{
		Activity: TransformHistoricActivity(*activityDetails),
		Stats:    results,
	}, nil
}
