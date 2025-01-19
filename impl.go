package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"oneTrick/api"
	"oneTrick/services/destiny"
	"oneTrick/services/snapshot"
	"oneTrick/services/user"
	"oneTrick/validator"
	"strconv"
	"time"
)

// ensure that we've conformed to the `ServerInterface` with a compile-time check
var _ api.StrictServerInterface = (*Server)(nil)

type Server struct {
	D2Service       destiny.Service
	D2AuthService   destiny.AuthService
	UserService     user.Service
	SnapshotService snapshot.Service
}

func (s Server) Profile(ctx context.Context, request api.ProfileRequestObject) (api.ProfileResponseObject, error) {
	access, ok := validator.FromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("missing access info")
	}
	if ok, err := s.D2AuthService.HasAccess(ctx, request.Params.XMembershipID, access.AccessToken); !ok || err != nil {
		return nil, fmt.Errorf("invalid access token")
	}

	u, err := s.UserService.GetUser(ctx, request.Params.XUserID)
	if err != nil {
		return nil, err
	}
	t := int64(0)
	for _, membership := range u.Memberships {
		if membership.ID == u.PrimaryMembershipID {
			t = membership.Type
			break
		}
	}
	pmId, err := strconv.ParseInt(u.PrimaryMembershipID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse primary membership id")
	}
	characters, err := s.D2Service.GetCharacters(pmId, t)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch characters: %w", err)
	}

	c := make([]api.Character, 0)
	for _, character := range characters {
		c = append(c, api.Character{
			ClassId:              int(*character.ClassHash),
			EmblemBackgroundPath: *character.EmblemBackgroundPath,
			EmblemPath:           *character.EmblemPath,
			Id:                   *character.CharacterId,
			Light:                int64(*character.Light),
			RaceId:               int(*character.RaceHash),
			TitleId:              int64(*character.TitleRecordHash),
		})
	}
	return api.Profile200JSONResponse{
		DisplayName: u.DisplayName,
		UniqueName:  u.UniqueName,
		Id:          u.ID,
		Characters:  c,
	}, nil
}

func (s Server) Login(ctx context.Context, request api.LoginRequestObject) (api.LoginResponseObject, error) {
	code := request.Body.Code
	resp, err := s.D2AuthService.GetAccessToken(ctx, code)
	if err != nil {
		slog.With("error", err.Error()).Error("Failed to fetch access token")
		return nil, err
	}

	existingUser, err := s.UserService.GetUser(ctx, resp.MembershipID)
	if err != nil && !errors.Is(err, user.NotFound) {
		return nil, err
	}
	if existingUser != nil {
		now := time.Now()
		result := api.AuthResponse{
			AccessToken:         resp.AccessToken,
			ExpiresIn:           resp.ExpiresIn,
			MembershipId:        resp.MembershipID,
			RefreshExpiresIn:    resp.RefreshExpiresIn,
			RefreshToken:        resp.RefreshToken,
			TokenType:           resp.TokenType,
			Id:                  existingUser.ID,
			PrimaryMembershipId: existingUser.PrimaryMembershipID,
			Timestamp:           now,
		}
		return api.Login200JSONResponse(result), nil
	}

	// TODO: Split into it's own function, when no account exists
	bUser, err := s.D2AuthService.GetCurrentUser(ctx, resp.AccessToken)
	if err != nil {
		return nil, err
	}
	if bUser.BungieNetUser == nil && bUser.DestinyMemberships == nil {
		return nil, fmt.Errorf("failed to fetch user data")
	}
	m := make([]user.Membership, 0)
	u := user.User{
		MemberID:    *bUser.BungieNetUser.MembershipId,
		DisplayName: *bUser.BungieNetUser.DisplayName,
		UniqueName:  *bUser.BungieNetUser.UniqueName,
	}
	if bUser.PrimaryMembershipId != nil {
		u.PrimaryMembershipID = *bUser.PrimaryMembershipId
	}
	for i, mem := range *bUser.DestinyMemberships {
		if i == 0 && bUser.PrimaryMembershipId == nil {
			u.PrimaryMembershipID = *mem.MembershipId
		}
		m = append(m, user.Membership{
			ID:          *mem.MembershipId,
			Type:        int64(*mem.MembershipType),
			DisplayName: *mem.DisplayName,
		})
	}
	u.Memberships = m

	newUser, err := s.UserService.CreateUser(ctx, &u)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	result := api.AuthResponse{
		AccessToken:         resp.AccessToken,
		ExpiresIn:           resp.ExpiresIn,
		MembershipId:        resp.MembershipID,
		RefreshExpiresIn:    resp.RefreshExpiresIn,
		RefreshToken:        resp.RefreshToken,
		TokenType:           resp.TokenType,
		Id:                  newUser.ID,
		PrimaryMembershipId: newUser.PrimaryMembershipID,
		Timestamp:           now,
	}
	return api.Login200JSONResponse(result), nil
}

func (s Server) RefreshToken(ctx context.Context, request api.RefreshTokenRequestObject) (api.RefreshTokenResponseObject, error) {
	resp, err := s.D2AuthService.RefreshAccessToken(request.Body.Code)
	if err != nil {
		return nil, err
	}
	existingUser, err := s.UserService.GetUser(ctx, resp.MembershipID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	result := api.AuthResponse{
		AccessToken:         resp.AccessToken,
		ExpiresIn:           resp.ExpiresIn,
		MembershipId:        resp.MembershipID,
		RefreshExpiresIn:    resp.RefreshExpiresIn,
		RefreshToken:        resp.RefreshToken,
		TokenType:           resp.TokenType,
		Id:                  existingUser.ID,
		PrimaryMembershipId: existingUser.PrimaryMembershipID,
		Timestamp:           now,
	}
	return api.RefreshToken200JSONResponse(result), nil
}

func (s Server) GetPing(ctx context.Context, request api.GetPingRequestObject) (api.GetPingResponseObject, error) {
	return api.GetPing200JSONResponse{
		Ping: "pong",
	}, nil
}

func NewServer(
	service destiny.Service,
	authService destiny.AuthService,
	userService user.Service,
	snapshotService snapshot.Service,
) Server {
	return Server{
		D2Service:       service,
		D2AuthService:   authService,
		UserService:     userService,
		SnapshotService: snapshotService,
	}
}

const characterID = "2305843009261519028"

func (s Server) GetSnapshots(ctx context.Context, request api.GetSnapshotsRequestObject) (api.GetSnapshotsResponseObject, error) {
	snapshots, err := s.SnapshotService.GetAllByCharacter(ctx, request.Params.XUserID, request.Params.CharacterId)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch snapshots: %w", err)
	}
	return api.GetSnapshots200JSONResponse(snapshots), nil
}

func (s Server) CreateSnapshot(ctx context.Context, request api.CreateSnapshotRequestObject) (api.CreateSnapshotResponseObject, error) {
	memID, err := strconv.ParseInt(request.Params.XMembershipID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid membership id: %w", err)
	}
	items, timestamp, err := s.D2Service.GetCurrentInventory(memID)
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
		details, err := s.D2Service.GetWeaponDetails(ctx, request.Params.XMembershipID, *item.ItemInstanceId)
		if err != nil {
			return nil, fmt.Errorf("couldn't find an item with item hash %d", item.ItemHash)
		}
		snap.Name = details.BaseInfo.Name
		snap.ItemHash = details.BaseInfo.ItemHash
		snap.ItemDetails = *details
		itemSnapshots = append(itemSnapshots, snap)
	}

	result.Items = itemSnapshots
	result.CharacterId = request.Body.CharacterId
	_, err = s.SnapshotService.Create(ctx, request.Params.XUserID, result)
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshot: %w", err)
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
