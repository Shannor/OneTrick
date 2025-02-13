package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"oneTrick/api"
	"oneTrick/services/aggregate"
	"oneTrick/services/destiny"
	"oneTrick/services/session"
	"oneTrick/services/snapshot"
	"oneTrick/services/user"
	"oneTrick/validator"
	"strconv"
	"time"
)

// ensure that we've conformed to the `ServerInterface` with a compile-time check
var _ api.StrictServerInterface = (*Server)(nil)

type Server struct {
	D2Service        destiny.Service
	D2AuthService    destiny.AuthService
	UserService      user.Service
	SnapshotService  snapshot.Service
	AggregateService aggregate.Service
	SessionService   session.Service
}

func (s Server) GetSnapshot(ctx context.Context, request api.GetSnapshotRequestObject) (api.GetSnapshotResponseObject, error) {

	result, err := s.SnapshotService.Get(ctx, request.SnapshotId)
	if err != nil {
		slog.With("error", err.Error()).Error("Failed to fetch snapshot")
		return nil, fmt.Errorf("failed to fetch snapshot: %w", err)
	}

	return api.GetSnapshot200JSONResponse(*result), nil
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
	characters, err := s.D2Service.GetCharacters(ctx, pmId, t)
	if err != nil {
		if errors.Is(err, destiny.ErrDestinyServerDown) {
			return api.Profile503JSONResponse{
				Message: "Destiny Server is down. Please wait while they get it back up and running",
				Status:  api.ErrDestinyServerDown,
			}, nil
		}
		return nil, fmt.Errorf("failed to fetch characters: %w", err)
	}

	return api.Profile200JSONResponse{
		DisplayName:  u.DisplayName,
		UniqueName:   u.UniqueName,
		Id:           u.ID,
		MembershipId: u.PrimaryMembershipID,
		Characters:   characters,
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

func (s Server) GetPing(context.Context, api.GetPingRequestObject) (api.GetPingResponseObject, error) {
	return api.GetPing200JSONResponse{
		Ping: "pong",
	}, nil
}

func NewServer(
	service destiny.Service,
	authService destiny.AuthService,
	userService user.Service,
	snapshotService snapshot.Service,
	aggregateService aggregate.Service,
	sessionService session.Service,
) Server {
	return Server{
		D2Service:        service,
		D2AuthService:    authService,
		UserService:      userService,
		SnapshotService:  snapshotService,
		AggregateService: aggregateService,
		SessionService:   sessionService,
	}
}

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

	membershipType, err := s.UserService.GetMembershipType(ctx, request.Params.XUserID, request.Params.XMembershipID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch membership type: %w", err)
	}

	items, timestamp, err := s.D2Service.GetCurrentInventory(ctx, memID, membershipType, request.Body.CharacterId)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch profile data: %w", err)
	}
	if timestamp == nil {
		return nil, fmt.Errorf("failed to fetch timestamp for profile data: %w", err)
	}
	result := api.CharacterSnapshot{
		Timestamp: *timestamp,
	}
	itemSnapshots := make(api.Loadout)
	for _, item := range items {
		if item.ItemInstanceId == nil {
			return nil, fmt.Errorf("missing instance id for item hash: %d", item.ItemHash)
		}
		snap := api.ItemSnapshot{
			InstanceID: *item.ItemInstanceId,
		}
		details, err := s.D2Service.GetWeaponDetails(ctx, request.Params.XMembershipID, membershipType, *item.ItemInstanceId)
		if err != nil {
			return nil, fmt.Errorf("couldn't find an item with item hash %d", item.ItemHash)
		}
		snap.Name = details.BaseInfo.Name
		snap.ItemHash = details.BaseInfo.ItemHash
		snap.ItemDetails = *details
		itemSnapshots[strconv.FormatInt(snap.ItemDetails.BaseInfo.BucketHash, 10)] = snap
	}

	result.Loadout = itemSnapshots
	result.CharacterID = request.Body.CharacterId
	_, err = s.SnapshotService.Create(ctx, request.Params.XUserID, result)
	if err != nil {
		return nil, fmt.Errorf("failed to create snapshot: %w", err)
	}
	return api.CreateSnapshot201JSONResponse(result), nil
}

func (s Server) GetActivities(ctx context.Context, request api.GetActivitiesRequestObject) (api.GetActivitiesResponseObject, error) {
	params := request.Params

	membershipType, err := s.UserService.GetMembershipType(ctx, params.XUserID, params.XMembershipID)
	if err != nil {
		return nil, err
	}

	mode := api.AllPvP
	if request.Params.Mode != nil {
		mode = *request.Params.Mode
	}

	history := make([]api.ActivityHistory, 0)
	switch mode {
	case api.AllPvP:
		history, err = s.D2Service.GetAllPVPActivity(ctx, params.XMembershipID, membershipType, params.CharacterID, params.Count, params.Page)
		if err != nil {
			slog.With("error", err.Error()).Error("Failed to fetch activity data")
			return nil, err
		}
	case api.Competitive:
		history, err = s.D2Service.GetCompetitiveActivity(ctx, params.XMembershipID, membershipType, params.CharacterID, params.Count, params.Page)
		if err != nil {
			slog.With("error", err.Error()).Error("Failed to fetch activity data")
			return nil, err
		}

	case api.Quickplay:
		history, err = s.D2Service.GetQuickPlayActivity(ctx, params.XMembershipID, membershipType, params.CharacterID, params.Count, params.Page)
		if err != nil {
			slog.With("error", err.Error()).Error("Failed to fetch activity data")
			return nil, err
		}
	case api.IronBanner:
		history, err = s.D2Service.GetIronBannerActivity(ctx, params.XMembershipID, membershipType, params.CharacterID, params.Count, params.Page)
		if err != nil {
			slog.With("error", err.Error()).Error("Failed to fetch activity data")
			return nil, err
		}
	default:
		history, err = s.D2Service.GetAllPVPActivity(ctx, params.XMembershipID, membershipType, params.CharacterID, params.Count, params.Page)
		if err != nil {
			slog.With("error", err.Error()).Error("Failed to fetch activity data")
			return nil, err
		}
	}
	activityIDs := make([]string, 0)
	for _, activityHistory := range history {
		activityIDs = append(activityIDs, activityHistory.InstanceID)
	}
	aggregates, err := s.AggregateService.GetAggregates(ctx, activityIDs)
	if err != nil {
		return nil, err
	}
	aggMap := make(map[string]api.Aggregate)
	for _, agg := range aggregates {
		aggMap[agg.ActivityID] = agg
	}

	result := make([]api.DetailActivity, 0)
	for _, h := range history {
		a := api.DetailActivity{
			Activity: h,
		}
		agg, ok := aggMap[h.InstanceID]
		if ok {
			a.Aggregate = &agg
		}
		result = append(result, a)
	}
	return api.GetActivities200JSONResponse(result), nil
}
func (s Server) GetActivity(ctx context.Context, request api.GetActivityRequestObject) (api.GetActivityResponseObject, error) {
	activityID := request.ActivityId
	userID := request.Params.XUserID
	characterID := request.Params.CharacterId

	l := slog.With("activityID", activityID).With("userID", userID).With("characterID", characterID)
	activityDetails, err := s.D2Service.GetActivity(ctx, request.Params.CharacterId, activityID)
	if err != nil {
		l.With("error", err.Error()).Error("Failed to fetch weapon data for activity")
		return nil, fmt.Errorf("failed to fetch weapon data for activity: %w", err)
	}
	if activityDetails == nil {
		return nil, fmt.Errorf("no activity details found for activity ID: %s", activityID)
	}

	agg, err := s.AggregateService.GetAggregate(ctx, activityID)
	if err != nil {
		if errors.Is(err, snapshot.NotFound) {
			l.Info("No aggregation found for activity")
		} else {
			l.With("error", err.Error()).Error("unexpected error fetching aggregation")
			return nil, err
		}
	}

	characterInGame := false
	for _, entry := range activityDetails.PostGameEntries {
		if entry.CharacterId != nil && *entry.CharacterId == characterID {
			characterInGame = true
			break
		}
	}
	if !characterInGame {
		return api.GetActivity200JSONResponse{
			Activity:  *activityDetails.Activity,
			Teams:     activityDetails.Teams,
			Aggregate: *agg,
		}, nil
	}

	// TODO: Start splitting out this logic, it's getting out of hand

	var snap *api.CharacterSnapshot
	skipAgg := false
	if agg != nil {
		snapshotMapping, ok := agg.Mapping[characterID]
		if ok {
			if snapshotMapping.ConfidenceLevel == api.NotFoundConfidenceLevel || snapshotMapping.ConfidenceLevel == api.NoMatchConfidenceLevel {
				skipAgg = true
			} else {
				snap, err = s.SnapshotService.Get(ctx, snapshotMapping.SnapshotData.SnapshotID)
				if err != nil {
					return nil, err
				}
			}
		}
	}

	if snap == nil && skipAgg == false {
		snap, err = s.SnapshotService.FindClosest(
			ctx,
			request.Params.XUserID,
			request.Params.CharacterId,
			*activityDetails.Period,
		)
		if err != nil && !errors.Is(err, snapshot.NotFound) {
			l.With("error", err.Error()).Error("Failed to fetch snapshot")
			return nil, fmt.Errorf("failed to fetch snapshot: %w", err)
		}
	}
	// TODO: Move this logic to it's own function for a future easy button that will try and match things up for you.
	items := make(api.Loadout)
	if snap != nil {
		items = snap.Loadout
	}
	characterWeaponStats, err := s.D2Service.EnrichWeaponStats(items, activityDetails.WeaponStats)
	if err != nil {
		slog.With("error", err.Error()).Error("failed enriching")
		return nil, fmt.Errorf("failed to enrich weapon characterWeaponStats: %w", err)
	}

	if snap != nil && len(characterWeaponStats) > 0 {
		agg, err = s.AggregateService.AddAggregate(ctx, characterID, activityID, &snap.ID, api.MediumConfidenceLevel, api.SystemConfidenceSource)
		if err != nil {
			l.With("error", err.Error()).Error("Failed to add aggregate")
			return nil, err
		}
	} else if snap != nil {
		agg, err = s.AggregateService.AddAggregate(ctx, characterID, activityID, nil, api.NoMatchConfidenceLevel, api.SystemConfidenceSource)
		if err != nil {
			l.With("error", err.Error()).Error("Failed to add aggregate")
			return nil, err
		}
	} else {
		agg, err = s.AggregateService.AddAggregate(ctx, characterID, activityID, nil, api.NotFoundConfidenceLevel, api.SystemConfidenceSource)
		if err != nil {
			l.With("error", err.Error()).Error("Failed to add aggregate")
			return nil, err
		}
	}

	return api.GetActivity200JSONResponse{
		Activity:       *activityDetails.Activity,
		CharacterStats: &characterWeaponStats,
		Teams:          activityDetails.Teams,
		Aggregate:      *agg,
	}, nil
}
