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
	"oneTrick/services/stats"
	"oneTrick/services/user"
	"strconv"
	"time"

	"github.com/fatih/structs"
	"github.com/rs/zerolog/log"
)

// ensure that we've conformed to the `ServerInterface` with a compile-time check
var _ api.StrictServerInterface = (*Server)(nil)

type Server struct {
	D2Service         destiny.Service
	D2AuthService     destiny.AuthService
	D2ManifestService destiny.ManifestService
	UserService       user.Service
	SnapshotService   snapshot.Service
	AggregateService  aggregate.Service
	SessionService    session.Service
	StatsService      stats.Service
}

func (s Server) BackfillSnapshotInfo(ctx context.Context, request api.BackfillSnapshotInfoRequestObject) (api.BackfillSnapshotInfoResponseObject, error) {
	snapshots, err := s.SnapshotService.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	updated := 0
	failed := 0
	for _, snap := range snapshots {
		if snap.Loadout == nil {
			continue
		}
		for key, item := range snap.Loadout { // item is a copy of the struct in the map
			d2Item, err := s.D2ManifestService.GetItem(ctx, item.ItemHash)
			if err != nil {
				log.Warn().Err(err).Msg("failed to get item from manifest")
				failed++
				continue
			}
			item.ItemProperties.BaseInfo.ItemTypeAndTierDisplayName = d2Item.ItemTypeAndTierDisplayName
			item.ItemProperties.BaseInfo.ItemTypeDisplayName = d2Item.ItemTypeDisplayName
			item.ItemProperties.BaseInfo.TierType = int32(d2Item.Inventory.TierType)
			item.ItemProperties.BaseInfo.TierTypeName = d2Item.Inventory.TierTypeName
			snap.Loadout[key] = item
		}
		err := s.SnapshotService.Update(ctx, snap.ID, func(data map[string]any) error {
			data["loadout"] = snap.Loadout
			return nil
		})
		if err != nil {
			log.Warn().Err(err).Msg("failed to update snapshot")
			failed++
		}
		updated++
	}

	return api.BackfillSnapshotInfo200JSONResponse{
		Updated: int32(updated),
		Failed:  int32(failed),
	}, nil
}

func (s Server) MergeSnapshots(ctx context.Context, request api.MergeSnapshotsRequestObject) (api.MergeSnapshotsResponseObject, error) {
	if request.Body == nil {
		return api.MergeSnapshots500JSONResponse{Message: "body cannot be empty"}, nil
	}
	if request.SnapshotID == "" {
		return api.MergeSnapshots500JSONResponse{Message: "snapshotID cannot be empty"}, nil
	}
	if request.SnapshotID == request.Body.SourceSnapshotID {
		return api.MergeSnapshots500JSONResponse{Message: "cannot merge snapshot with itself"}, nil
	}

	_, err := s.SnapshotService.Merge(ctx, request.SnapshotID, request.Body.SourceSnapshotID)
	if err != nil {
		return api.MergeSnapshots500JSONResponse{Message: err.Error()}, nil
	}
	return api.MergeSnapshots200JSONResponse(true), nil
}

func (s Server) StartUserSession(ctx context.Context, request api.StartUserSessionRequestObject) (api.StartUserSessionResponseObject, error) {
	if request.Params.XUserID != request.UserID {
		// TODO: Need to do a check to see if user requesting has the current user in their fireteam.
		// If not block the users from hitting it.
	}
	u, err := s.UserService.GetUser(ctx, request.Params.XUserID)
	if err != nil {
		return nil, err
	}
	createdBy := api.AuditField{
		ID:       u.ID,
		Username: u.DisplayName,
	}
	result, err := s.SessionService.Start(ctx, request.Body.UserID, request.Body.CharacterID, createdBy)
	if err != nil {
		return api.StartUserSession400JSONResponse{Message: err.Error()}, nil
	}
	return api.StartUserSession201JSONResponse(*result), nil
}

func (s Server) GetUserSessions(ctx context.Context, request api.GetUserSessionsRequestObject) (api.GetUserSessionsResponseObject, error) {
	offset := 0
	if request.Params.Page > 1 {
		offset = int(request.Params.Count) * int(request.Params.Page-1)
	}
	result, err := s.SessionService.GetAll(
		ctx,
		&request.UserID,
		&request.Params.CharacterID,
		(*api.SessionStatus)(request.Params.Status),
		int(request.Params.Count),
		offset,
	)
	if err != nil {
		return nil, err
	}
	return api.GetUserSessions200JSONResponse(result), nil
}

func (s Server) GetUser(ctx context.Context, request api.GetUserRequestObject) (api.GetUserResponseObject, error) {
	u, err := s.UserService.GetUser(ctx, request.UserID)
	if err != nil {
		return nil, err
	}
	go func() {
		// Perform update for characters if needed
		if u.LastUpdatedCharacters.Add(time.Hour).Before(time.Now()) {
			log.Info().Str("userId", u.ID).Msg("Updating characters for user")
			t := int64(0)
			for _, membership := range u.Memberships {
				if membership.ID == u.PrimaryMembershipID {
					t = membership.Type
					break
				}
			}
			pmId, err := strconv.ParseInt(u.PrimaryMembershipID, 10, 64)
			if err != nil {
				log.Error().Err(err).Msg("failed to parse primary membership id")
				return
			}

			characters, err := s.D2Service.GetCharacters(ctx, pmId, t)
			if len(characters) > 0 {
				err = s.UserService.UpdateCharacters(ctx, u.ID, characters)
				if err != nil {
					log.Error().Err(err).Msg("failed to update characters")
				}
			} else {
				log.Warn().Str("userId", u.ID).Msg("no characters found for user")
			}
		}
	}()

	result := api.GetUser200JSONResponse{
		DisplayName:  u.DisplayName,
		UniqueName:   u.UniqueName,
		Id:           u.ID,
		MembershipId: u.PrimaryMembershipID,
		Characters:   u.Characters,
	}

	if len(result.Characters) == 0 {
		t := int64(0)
		for _, membership := range u.Memberships {
			if membership.ID == u.PrimaryMembershipID {
				t = membership.Type
				break
			}
		}
		pmId, err := strconv.ParseInt(u.PrimaryMembershipID, 10, 64)
		if err != nil {
			log.Error().Err(err).Msg("failed to parse primary membership id")
			return result, err
		}
		characters, err := s.D2Service.GetCharacters(ctx, pmId, t)
		if len(characters) > 0 {
			result.Characters = u.Characters
		}
	}
	return result, nil
}

const (
	DefaultMinimumGames = 5
	DefaultLoadoutCount = 10
)

func (s Server) GetBestPerformingLoadouts(ctx context.Context, request api.GetBestPerformingLoadoutsRequestObject) (api.GetBestPerformingLoadoutsResponseObject, error) {
	characterID := request.Params.CharacterID
	count := DefaultLoadoutCount
	if request.Params.Count != nil {
		count = *request.Params.Count
	}
	minimumGames := DefaultMinimumGames
	if request.Params.MinimumGames != nil {
		minimumGames = *request.Params.MinimumGames
	}
	gameModeFilter, err := s.D2Service.GetActivityModesFromGameMode(request.Params.GameMode)
	if err != nil {
		return api.GetBestPerformingLoadouts200JSONResponse{}, err
	}
	aggs, err := s.StatsService.GetAggregatesByCharacterID(ctx, characterID, gameModeFilter)
	if err != nil {
		return api.GetBestPerformingLoadouts200JSONResponse{}, err
	}
	result, performanceStats, counts, err := s.StatsService.GetBestPerformingLoadouts(ctx, aggs, characterID, int8(count), minimumGames)
	if err != nil {
		return api.GetBestPerformingLoadouts200JSONResponse{}, err
	}
	return api.GetBestPerformingLoadouts200JSONResponse{
		Items: result,
		Stats: performanceStats,
		Count: counts,
	}, nil
}

func (s Server) GetFireteam(ctx context.Context, request api.GetFireteamRequestObject) (api.GetFireteamResponseObject, error) {
	members, err := s.UserService.GetFireteam(ctx, request.Params.XUserID)
	if err != nil {
		return nil, err
	}
	return api.GetFireteam200JSONResponse(members), nil
}

func (s Server) GetSession(ctx context.Context, request api.GetSessionRequestObject) (api.GetSessionResponseObject, error) {
	sessionID := request.SessionId
	l := log.With().Str("sessionID", sessionID).Logger()
	ses, err := s.SessionService.Get(ctx, sessionID)
	if err != nil {
		l.Error().Err(err).Msg("failed to fetch session")
		return nil, err
	}
	return api.GetSession200JSONResponse(*ses), nil
}

func (s Server) Search(ctx context.Context, request api.SearchRequestObject) (api.SearchResponseObject, error) {
	results, err := s.UserService.Search(ctx, request.Body.Prefix, int(request.Body.Page))
	if err != nil {
		return nil, err
	}
	return api.Search200JSONResponse{
		Results: results,
		HasMore: false,
	}, nil
}

func (s Server) GetSessions(ctx context.Context, request api.GetSessionsRequestObject) (api.GetSessionsResponseObject, error) {
	offset := 0
	if request.Params.Page > 1 {
		offset = int((request.Params.Page - 1) * request.Params.Count)
	}

	result, err := s.SessionService.GetAll(
		ctx,
		request.Params.UserID,
		request.Params.CharacterID,
		(*api.SessionStatus)(request.Params.Status),
		int(request.Params.Count),
		offset,
	)
	if err != nil {
		// Return a 500 error
		return nil, err
	}

	return api.GetSessions200JSONResponse(result), nil
}

func (s Server) StartSession(ctx context.Context, request api.StartSessionRequestObject) (api.StartSessionResponseObject, error) {
	if request.Params.XUserID != request.Body.UserID {
		// TODO: Need to do a check to see if user requesting has the current user in their fireteam.
	}
	u, err := s.UserService.GetUser(ctx, request.Params.XUserID)
	if err != nil {
		return nil, err
	}
	createdBy := api.AuditField{
		ID:       u.ID,
		Username: u.DisplayName,
	}
	result, err := s.SessionService.Start(ctx, request.Body.UserID, request.Body.CharacterID, createdBy)
	if err != nil {
		return api.StartSession400JSONResponse{Message: err.Error()}, nil
	}
	return api.StartSession201JSONResponse(*result), nil
}

func (s Server) UpdateSession(ctx context.Context, request api.UpdateSessionRequestObject) (api.UpdateSessionResponseObject, error) {
	if request.Body.Name != nil {
		// Migrate name
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
	l := slog.With("sessionID", request.SessionId)
	ses, err := s.SessionService.Get(ctx, request.SessionId)
	if err != nil {
		l.With("error", err.Error()).Error("Failed to fetch session")
		return nil, err
	}
	if len(ses.AggregateIDs) == 0 {
		l.Error("No aggregate IDs found")
		return nil, fmt.Errorf("no aggregate found")
	}
	aggregates, err := s.AggregateService.GetAggregates(ctx, ses.AggregateIDs)
	if err != nil {
		l.With("error", err.Error()).Error("Failed to fetch aggregates")
		return nil, err
	}
	uniqueIDS := make([]string, 0)
	for _, a := range aggregates {
		link, ok := a.SnapshotLinks[ses.CharacterID]
		if !ok {
			continue
		}
		if link.SnapshotID == nil {
			continue
		}
		uniqueIDS = append(uniqueIDS, *link.SnapshotID)
	}
	snapshots, err := s.SnapshotService.GetByIDs(ctx, uniqueIDS)
	if err != nil {
		l.With("error", err.Error()).Error("Failed to fetch snapshots")
		return nil, err
	}
	snapshotByID := make(map[string]api.CharacterSnapshot)
	for _, snap := range snapshots {
		snapshotByID[snap.ID] = snap
	}
	return api.GetSessionAggregates200JSONResponse{
		Aggregates: aggregates,
		Snapshots:  snapshotByID,
	}, nil
}

func (s Server) GetSnapshot(ctx context.Context, request api.GetSnapshotRequestObject) (api.GetSnapshotResponseObject, error) {

	result, err := s.SnapshotService.Get(ctx, request.SnapshotID)
	if err != nil {
		slog.With("error", err.Error()).Error("Failed to fetch snapshot")
		return nil, fmt.Errorf("failed to fetch snapshot: %w", err)
	}

	return api.GetSnapshot200JSONResponse(*result), nil
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
	membershipType := int64(0)
	for i, mem := range *bUser.DestinyMemberships {
		if i == 0 && bUser.PrimaryMembershipId == nil {
			u.PrimaryMembershipID = *mem.MembershipId
			membershipType = int64(int(*mem.MembershipType))
		}
		m = append(m, user.Membership{
			ID:          *mem.MembershipId,
			Type:        int64(*mem.MembershipType),
			DisplayName: *mem.DisplayName,
		})
	}
	id, _ := strconv.ParseInt(u.PrimaryMembershipID, 10, 64)
	chars, err := s.D2Service.GetCharacters(ctx, id, membershipType)
	if err != nil {
		return nil, err
	}
	charIDs := make([]string, 0)
	for _, char := range chars {
		charIDs = append(charIDs, char.Id)
	}
	u.Memberships = m
	u.CharacterIDs = charIDs
	u.Characters = chars
	u.LastUpdatedCharacters = time.Now()

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
	manifestService destiny.ManifestService,
	statsService stats.Service,
) Server {
	return Server{
		D2Service:         service,
		D2AuthService:     authService,
		UserService:       userService,
		SnapshotService:   snapshotService,
		AggregateService:  aggregateService,
		SessionService:    sessionService,
		D2ManifestService: manifestService,
		StatsService:      statsService,
	}
}
func (s Server) GetSnapshotAggregates(ctx context.Context, request api.GetSnapshotAggregatesRequestObject) (api.GetSnapshotAggregatesResponseObject, error) {
	snap, err := s.SnapshotService.Get(ctx, request.SnapshotID)
	if err != nil {
		return nil, err
	}

	gameModeFilter, err := s.D2Service.GetActivityModesFromGameMode(request.Params.GameMode)
	if err != nil {
		return nil, err
	}
	aggs, err := s.StatsService.GetAggregatesForSnapshot(ctx, snap.ID, gameModeFilter)
	if err != nil {
		return nil, err
	}

	return api.GetSnapshotAggregates200JSONResponse(aggs), nil
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

	l := log.With().Str("userID", userID).
		Str("membershipID", membershipID).
		Str("characterID", characterID).Logger()

	data, err := s.SnapshotService.Save(ctx, userID, membershipID, characterID)
	if err != nil {
		l.Error().Err(err).Msg("couldn't save the users snapshot data")
		return nil, fmt.Errorf("failed to save snapshot: %w", err)
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

	l := log.With().
		Str("activityID", activityID).Logger()
	activityDetails, teams, err := s.D2Service.GetActivity(ctx, activityID)
	if err != nil {
		l.Error().Err(err).Msg("Failed to fetch weapon data for activity")
		return nil, fmt.Errorf("failed to fetch weapon data for activity: %w", err)
	}
	if activityDetails == nil {
		return nil, fmt.Errorf("no activity details found for activity ID: %s", activityID)
	}

	// TODO: Come to fix this when no aggregate has been made for an activity
	agg, err := s.AggregateService.GetAggregate(ctx, activityID)
	if err != nil {
		if errors.Is(err, aggregate.NotFound) {
			l.Debug().Msg("No aggregation found for activity")
		} else {
			l.Error().Err(err).Msg("unexpected error fetching aggregation")
			return nil, err
		}
	}

	entries := make([]map[string]any, 0)
	for _, entry := range *activityDetails.Entries {
		entries = append(entries, structs.Map(entry))
	}
	var IDs []string
	for _, link := range agg.SnapshotLinks {
		if link.SnapshotID == nil {
			continue
		}
		IDs = append(IDs, *link.SnapshotID)
	}
	snapshots := make(map[string]api.CharacterSnapshot)
	snaps, err := s.SnapshotService.GetByIDs(ctx, IDs)
	if err != nil {
		return nil, err
	}
	for _, snap := range snaps {
		snapshots[snap.CharacterID] = snap
	}
	// Build users map keyed by characterId when available
	users := make(map[string]api.User)
	for characterID, snap := range snapshots {
		u, err := s.UserService.GetUser(ctx, snap.UserID)
		if err != nil {
			l.Error().Err(err).Str("characterId", characterID).Msg("failed to fetch user by character id")
			continue
		}
		if u == nil {
			continue
		}
		// Map service user to API user
		apiUser := api.User{
			ID:                  u.ID,
			MemberID:            u.MemberID,
			PrimaryMembershipID: u.PrimaryMembershipID,
			UniqueName:          u.UniqueName,
			DisplayName:         u.DisplayName,
			CreatedAt:           u.CreatedAt,
			CharacterIDs:        u.CharacterIDs,
		}
		// memberships
		if len(u.Memberships) > 0 {
			ms := make([]api.Membership, 0, len(u.Memberships))
			for _, m := range u.Memberships {
				ms = append(ms, api.Membership{ID: m.ID, Type: m.Type, DisplayName: m.DisplayName})
			}
			apiUser.Memberships = ms
		}
		users[characterID] = apiUser
	}
	return api.GetActivity200JSONResponse{
		Activity:        agg.ActivityDetails,
		Teams:           teams,
		Aggregate:       agg,
		PostGameEntries: &entries,
		Snapshots:       snapshots,
		Users:           users,
	}, nil

}

// Admin endpoint to backfill character IDs for all users
func (s Server) BackfillAllUsersCharacterIds(ctx context.Context, request api.BackfillAllUsersCharacterIdsRequestObject) (api.BackfillAllUsersCharacterIdsResponseObject, error) {
	users, err := s.UserService.GetAll(ctx)
	if err != nil {
		return nil, err
	}
	var updated int32
	var failed int32
	for _, u := range users {
		if err := s.UserService.BackfillCharacters(ctx, u.ID); err != nil {
			slog.With("userId", u.ID, "error", err.Error()).Warn("failed to backfill character ids")
			failed++
			continue
		}
		updated++
	}
	return api.BackfillAllUsersCharacterIds200JSONResponse{
		Updated: updated,
		Failed:  failed,
	}, nil
}

func (s Server) BackfillAggregateData(ctx context.Context, request api.BackfillAggregateDataRequestObject) (api.BackfillAggregateDataResponseObject, error) {
	count, err := s.AggregateService.UpdateAllAggregates(ctx)
	if err != nil {
		return api.BackfillAggregateData200JSONResponse{}, err
	}

	return api.BackfillAggregateData200JSONResponse{
		Updated: int32(count),
		Failed:  0,
	}, nil
}
