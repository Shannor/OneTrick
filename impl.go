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
	D2Service     destiny.Service
	D2AuthService destiny.AuthService
}

func (s Server) Login(ctx context.Context, request api.LoginRequestObject) (api.LoginResponseObject, error) {
	code := request.Body.Code
	resp, err := s.D2AuthService.GetAccessToken(ctx, code)
	if err != nil {
		slog.With("error", err.Error()).Error("Failed to fetch access token")
		return nil, err
	}

	// TODO: Create account in DB based on Membership ID, in a separate service maybe
	result := api.AuthResponse{
		AccessToken:      resp.AccessToken,
		ExpiresIn:        resp.ExpiresIn,
		MembershipId:     resp.MembershipID,
		RefreshExpiresIn: resp.RefreshExpiresIn,
		RefreshToken:     resp.RefreshToken,
		TokenType:        resp.TokenType,
	}
	return api.Login200JSONResponse(result), nil
}

func (s Server) RefreshToken(ctx context.Context, request api.RefreshTokenRequestObject) (api.RefreshTokenResponseObject, error) {
	//TODO implement me
	panic("implement me")
}

func (s Server) GetPing(ctx context.Context, request api.GetPingRequestObject) (api.GetPingResponseObject, error) {
	return api.GetPing200JSONResponse{
		Ping: "pong",
	}, nil
}

func NewServer(service destiny.Service, authService destiny.AuthService) Server {
	return Server{
		D2Service:     service,
		D2AuthService: authService,
	}
}

const characterID = "2305843009261519028"

func (s Server) GetSnapshots(ctx context.Context, request api.GetSnapshotsRequestObject) (api.GetSnapshotsResponseObject, error) {
	snapshots, err := s.D2Service.GetAllCharacterSnapshots()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch snapshots: %w", err)
	}
	return api.GetSnapshots200JSONResponse(snapshots), nil
}

func (s Server) CreateSnapshot(ctx context.Context, request api.CreateSnapshotRequestObject) (api.CreateSnapshotResponseObject, error) {
	items, timestamp, err := s.D2Service.GetCurrentInventory(primaryMembershipId)
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
		details, err := s.D2Service.GetWeaponDetails(ctx, strconv.Itoa(primaryMembershipId), *item.ItemInstanceId)
		if err != nil {
			return nil, fmt.Errorf("couldn't find an item with item hash %d", item.ItemHash)
		}
		snap.Name = details.BaseInfo.Name
		snap.ItemHash = details.BaseInfo.ItemHash
		snap.ItemDetails = *details
		itemSnapshots = append(itemSnapshots, snap)
	}

	result.Items = itemSnapshots
	err = s.D2Service.SaveCharacterSnapshot(result)
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
	resp, err := s.D2Service.GetAllPVPActivity(primaryMembershipId, id, params.Count, params.Page)
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
	activityDetails, weaponStats, period, err := s.D2Service.GetActivity(ctx, characterID, id)
	if err != nil {
		slog.With("error", err.Error()).Error("Failed to fetch weapon data for activity")
		return nil, fmt.Errorf("failed to fetch weapon data for activity: %w", err)
	}
	if activityDetails == nil {
		return nil, fmt.Errorf("no activity details found for activity ID: %s", activityId)
	}
	// 1. Get the closet snapshot(s)
	// TODO: Maybe add a warning when the snapshot is more than 30 minutes away since that may not be accurate anymore
	characterSnapshot, err := s.D2Service.GetClosestSnapshot(primaryMembershipId, period)
	if err != nil {
		slog.With("error", err.Error()).Error("Failed to fetch snapshot")
		return nil, fmt.Errorf("failed to fetch snapshot: %w", err)
	}

	stats, err := s.D2Service.EnrichWeaponStats(ctx, strconv.Itoa(primaryMembershipId), characterSnapshot.Items, weaponStats)
	if err != nil {
		slog.With("error", err.Error()).Error("failed enriching")
		return nil, fmt.Errorf("failed to enrich weapon stats: %w", err)
	}
	return api.GetActivity200JSONResponse{
		Activity: *activityDetails,
		Stats:    stats,
	}, nil
}
