package main

import (
	"context"
	"fmt"
	"log/slog"
	"oneTrick/api"
	"oneTrick/services/destiny"
	"strconv"
)

// ensure that we've conformed to the `ServerInterface` with a compile-time check
var _ api.StrictServerInterface = (*Server)(nil)

type Server struct {
	DestinyService destiny.Service
}

func (s Server) GetPing(ctx context.Context, request api.GetPingRequestObject) (api.GetPingResponseObject, error) {
	return api.GetPing200JSONResponse{
		Ping: "pong",
	}, nil
}

func NewServer(service destiny.Service) Server {
	return Server{
		DestinyService: service,
	}
}

const characterID = "2305843009261519028"

func (s Server) GetSnapshots(ctx context.Context, request api.GetSnapshotsRequestObject) (api.GetSnapshotsResponseObject, error) {
	snapshots, err := s.DestinyService.GetAllCharacterSnapshots()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch snapshots: %w", err)
	}
	return api.GetSnapshots200JSONResponse(snapshots), nil
}

func (s Server) CreateSnapshot(ctx context.Context, request api.CreateSnapshotRequestObject) (api.CreateSnapshotResponseObject, error) {
	items, timestamp, err := s.DestinyService.GetCurrentInventory(primaryMembershipId)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch profile data: %w", err)
	}
	if timestamp == nil {
		return nil, fmt.Errorf("failed to fetch timestamp for profile data: %w", err)
	}
	result := api.CharacterSnapshot{
		Timestamp: *timestamp,
	}
	itemSnapshots := make([]api.ItemSnapshot, 0)
	for _, item := range items {
		if item.ItemInstanceId == nil {
			return nil, fmt.Errorf("missing instance id for item hash: %d", item.ItemHash)
		}
		snap := api.ItemSnapshot{
			InstanceId: *item.ItemInstanceId,
			Timestamp:  *timestamp,
		}
		details, err := s.DestinyService.GetWeaponDetails(ctx, strconv.Itoa(primaryMembershipId), *item.ItemInstanceId)
		if err != nil {
			return nil, fmt.Errorf("couldn't find an item with item hash %d", item.ItemHash)
		}
		snap.Name = details.BaseInfo.Name
		snap.ItemHash = details.BaseInfo.ItemHash
		snap.ItemDetails = *details
		itemSnapshots = append(itemSnapshots, snap)
	}

	result.Items = itemSnapshots
	err = s.DestinyService.SaveCharacterSnapshot(result)
	if err != nil {
		return nil, fmt.Errorf("failed to save character snapshot: %w", err)
	}
	return api.CreateSnapshot201JSONResponse(result), nil
}

func (s Server) GetActivities(ctx context.Context, request api.GetActivitiesRequestObject) (api.GetActivitiesResponseObject, error) {
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

	return api.GetActivities200JSONResponse(resp), nil
}
func (s Server) GetActivity(ctx context.Context, request api.GetActivityRequestObject) (api.GetActivityResponseObject, error) {
	activityId := request.ActivityId
	id, err := strconv.ParseInt(activityId, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid activity ID: %w", err)
	}
	activityDetails, weaponStats, period, err := s.DestinyService.GetActivity(ctx, characterID, id)
	if err != nil {
		slog.With("error", err.Error()).Error("Failed to fetch weapon data for activity")
		return nil, fmt.Errorf("failed to fetch weapon data for activity: %w", err)
	}
	if activityDetails == nil {
		return nil, fmt.Errorf("no activity details found for activity ID: %s", activityId)
	}
	// 1. Get the closet snapshot(s)
	// TODO: Maybe add a warning when the snapshot is more than 30 minutes away since that may not be accurate anymore
	characterSnapshot, err := s.DestinyService.GetClosestSnapshot(primaryMembershipId, period)
	if err != nil {
		slog.With("error", err.Error()).Error("Failed to fetch snapshot")
		return nil, fmt.Errorf("failed to fetch snapshot: %w", err)
	}

	stats, err := s.DestinyService.EnrichWeaponStats(ctx, strconv.Itoa(primaryMembershipId), characterSnapshot.Items, weaponStats)
	if err != nil {
		slog.With("error", err.Error()).Error("failed enriching")
		return nil, fmt.Errorf("failed to enrich weapon stats: %w", err)
	}
	return api.GetActivity200JSONResponse{
		Activity: *activityDetails,
		Stats:    stats,
	}, nil
}
