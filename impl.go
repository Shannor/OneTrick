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

func (s Server) GetSession(ctx context.Context, request api.GetSessionRequestObject) (api.GetSessionResponseObject, error) {
	sessionID := request.SessionId
	l := slog.With("sessionID", sessionID, "function", "GetSession")
	ses, err := s.SessionService.Get(ctx, sessionID)
	if err != nil {
		l.With("error", err.Error()).Error("Failed to fetch session")
		return nil, err
	}
	return api.GetSession200JSONResponse(*ses), nil
}

func (s Server) SessionCheckIn(ctx context.Context, request api.SessionCheckInRequestObject) (api.SessionCheckInResponseObject, error) {
	sessionID := request.Body.SessionID
	membershipID := request.Params.XMembershipID
	l := slog.With(
		"sessionID",
		sessionID,
		"membershipID",
		membershipID,
		"function",
		"SessionCheckIn",
	)
	// 1. Get Session
	currentSession, err := s.SessionService.Get(ctx, sessionID)
	if err != nil {
		l.Error("Failed to fetch session")
		return nil, err
	}

	characterID := currentSession.CharacterID
	userID := currentSession.UserID
	l = l.
		With("characterID", characterID).
		With("userID", userID)

	// 2. Take a snapshot at this current time
	_, err = s.CreateSnapshot(ctx, api.CreateSnapshotRequestObject{
		Params: api.CreateSnapshotParams{
			XUserID:       userID,
			XMembershipID: membershipID,
		},
		Body: &api.CreateSnapshotJSONRequestBody{
			CharacterID: characterID,
		},
	})
	if err != nil {
		l.With("error", err.Error()).Error("Failed to create snapshot")
		return nil, err
	}

	membershipType, err := s.UserService.GetMembershipType(ctx, userID, membershipID)
	if err != nil {
		l.With("error", err.Error()).Error("Failed to fetch membership type")
		return nil, err
	}

	// 2. Get 3 latest activity history
	activityHistories, err := s.D2Service.GetAllPVPActivity(
		ctx,
		membershipID,
		membershipType,
		characterID,
		3,
		0,
	)
	if err != nil {
		l.With("error", err.Error()).Error("Failed to fetch history data")
		return nil, err
	}
	// TODO: Save to currentSession the last seen history ID and timestamp, If it's been the same for a long time we should kill the currentSession

	// 3. Check 3 activity history to see if we have aggregates for them
	IDs := make([]string, 0)
	for _, activity := range activityHistories {
		IDs = append(IDs, activity.InstanceID)
	}
	aggregates, err := s.AggregateService.GetAggregatesByActivity(ctx, IDs)
	if err != nil {
		l.With("error", err.Error()).Error("Failed to fetch aggregate data")
		return nil, err
	}

	activityToAgg := make(map[string]*api.Aggregate)
	aggIDs := make([]string, 0)
	for _, agg := range aggregates {
		activityToAgg[agg.ActivityID] = &agg
		aggIDs = append(aggIDs, agg.ID)
	}

	updatedAgg := false
	for _, history := range activityHistories {
		agg := activityToAgg[history.InstanceID]
		link := s.SnapshotService.LookupLink(agg, characterID)

		// Already attempted to link this character to this activity so we can skip it
		if link != nil && link.SessionID != nil {
			l.With("activityID", history.InstanceID).Debug("Already linked to this activity")
			continue
		} else if link != nil {
			// TODO: Figure out if we want to add this Session ID to this link
			// Probably need to check the times to see if they're close
		}

		updatedAgg = true

		activity, err := s.D2Service.GetEnrichedActivity(ctx, characterID, history.InstanceID)
		if err != nil {
			l.With("error", err.Error()).Error("Failed to fetch activity data")
			return nil, err
		}
		_, err = SetAggregate(ctx, s, userID, characterID, &history, history.Period, *activity.Performance, &sessionID)
		if err != nil {
			return nil, err
		}
	}

	err = s.SessionService.AddAggregateIDs(ctx, sessionID, aggIDs)
	if err != nil {
		l.With("error", err.Error()).Error("Failed to add aggregate IDs to session")
		return nil, err
	}
	l.Info("Session check in complete")
	return api.SessionCheckIn200JSONResponse(updatedAgg), nil
}

// SetAggregate will find the best fitting snapshot and link for a character.
// Will enrich their data if a snapshot is found. And will upsert an aggregate with the characters data.
func SetAggregate(ctx context.Context, s Server, userID string, characterID string, activity *api.ActivityHistory, period time.Time, performance api.InstancePerformance, sessionID *string) (*api.Aggregate, error) {
	snap, link, err := s.SnapshotService.FindBestFit(ctx, userID, characterID, period, performance.Weapons)
	if err != nil {
		return nil, err
	}

	enrichedPerformance, err := s.SnapshotService.EnrichInstancePerformance(snap, performance)
	if err != nil {
		return nil, fmt.Errorf("failed to enrich performance instance: %w", err)
	}

	if sessionID != nil {
		link.SessionID = sessionID
	}

	agg, err := s.AggregateService.AddAggregate(ctx, characterID, *activity, *link, *enrichedPerformance)
	if err != nil {
		return nil, err
	}
	return agg, nil
}

func (s Server) GetSessions(ctx context.Context, request api.GetSessionsRequestObject) (api.GetSessionsResponseObject, error) {
	result, err := s.SessionService.GetAll(ctx, request.Params.XUserID, request.Params.CharacterID, (*api.SessionStatus)(request.Params.Status))
	if err != nil {
		return nil, err
	}
	return api.GetSessions200JSONResponse(result), nil
}

func (s Server) StartSession(ctx context.Context, request api.StartSessionRequestObject) (api.StartSessionResponseObject, error) {
	result, err := s.SessionService.Start(ctx, request.Params.XUserID, request.Body.CharacterID)
	if err != nil {
		return api.StartSession400JSONResponse{Message: err.Error()}, nil
	}
	return api.StartSession201JSONResponse(*result), nil
}

func (s Server) UpdateSession(ctx context.Context, request api.UpdateSessionRequestObject) (api.UpdateSessionResponseObject, error) {
	if request.Body.Name != nil {
		// Update name
	}
	if request.Body.CompletedAt != nil {
		err := s.SessionService.Complete(ctx, request.SessionId)
		if err != nil {
			return nil, err
		}
	}

	ses, err := s.SessionService.Get(ctx, request.SessionId)
	if err != nil {
		return nil, err
	}
	return api.UpdateSession201JSONResponse(*ses), nil
}

func (s Server) GetSessionAggregates(ctx context.Context, request api.GetSessionAggregatesRequestObject) (api.GetSessionAggregatesResponseObject, error) {
	ses, err := s.SessionService.Get(ctx, request.SessionId)
	if err != nil {
		return nil, err
	}
	aggregates, err := s.AggregateService.GetAggregates(ctx, ses.AggregateIDs)
	if err != nil {
		return nil, err
	}
	return api.GetSessionAggregates200JSONResponse(aggregates), nil
}

func (s Server) GetSnapshot(ctx context.Context, request api.GetSnapshotRequestObject) (api.GetSnapshotResponseObject, error) {

	result, err := s.SnapshotService.Get(ctx, request.SnapshotID)
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
	snapshots, err := s.SnapshotService.GetAllByCharacter(ctx, request.Params.XUserID, request.Params.CharacterID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch snapshots: %w", err)
	}
	return api.GetSnapshots200JSONResponse(snapshots), nil
}

func (s Server) CreateSnapshot(ctx context.Context, request api.CreateSnapshotRequestObject) (api.CreateSnapshotResponseObject, error) {
	userID := request.Params.XUserID
	membershipID := request.Params.XMembershipID
	characterID := request.Body.CharacterID

	l := slog.With("userID", userID).
		With("membershipID", membershipID).
		With("characterID", characterID)

	data, err := s.SnapshotService.GenerateSnapshot(ctx, userID, membershipID, characterID)
	if err != nil {
		l.With("error", err.Error()).Error("Failed to generate snapshot")
		return nil, fmt.Errorf("failed to build data: %w", err)
	}
	if data == nil {
		l.Error("Failed to generate snapshot")
		return nil, fmt.Errorf("failed to generate snapshot")
	}
	_, err = s.SnapshotService.Create(ctx, userID, *data)
	if err != nil {
		l.With("error", err.Error()).Error("Failed to create snapshot")
		return nil, fmt.Errorf("failed to create snapshot: %w", err)
	}
	return api.CreateSnapshot201JSONResponse(*data), nil
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
	aggregates, err := s.AggregateService.GetAggregatesByActivity(ctx, activityIDs)
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
	activityID := request.ActivityID
	userID := request.Params.XUserID
	characterID := request.Params.CharacterID

	l := slog.
		With("activityID", activityID).
		With("userID", userID).
		With("characterID", characterID)

	activityDetails, err := s.D2Service.GetEnrichedActivity(ctx, request.Params.CharacterID, activityID)
	if err != nil {
		l.With("error", err.Error()).Error("Failed to fetch weapon data for activity")
		return nil, fmt.Errorf("failed to fetch weapon data for activity: %w", err)
	}
	if activityDetails == nil {
		return nil, fmt.Errorf("no activity details found for activity ID: %s", activityID)
	}

	agg, err := s.AggregateService.GetAggregate(ctx, activityID)
	if err != nil {
		if errors.Is(err, aggregate.NotFound) {
			l.Debug("No aggregation found for activity")
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
			Aggregate: agg,
		}, nil
	}

	if s.SnapshotService.LookupLink(agg, characterID) != nil {
		return api.GetActivity200JSONResponse{
			Activity:  *activityDetails.Activity,
			Teams:     activityDetails.Teams,
			Aggregate: agg,
		}, nil
	}

	// Backfill an aggregate on lookup when looking at an activity
	a, err := SetAggregate(ctx, s, userID, characterID, activityDetails.Activity, *activityDetails.Period, *activityDetails.Performance, nil)
	if err != nil {
		l.With("error", err.Error()).Error("Failed to set aggregate")
		return nil, err
	}

	return api.GetActivity200JSONResponse{
		Activity:  *activityDetails.Activity,
		Teams:     activityDetails.Teams,
		Aggregate: a,
	}, nil
}
