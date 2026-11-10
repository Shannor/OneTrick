package main

import (
	"context"
	"errors"
	"fmt"
	"oneTrick/api"
	"oneTrick/services/aggregate"
	"oneTrick/services/destiny"
	"oneTrick/services/session"
	"oneTrick/services/snapshot"
	"oneTrick/services/stats"
	"oneTrick/services/user"
	"strconv"
	"time"

	clog "github.com/Shannor/canonical-logger"
	"github.com/fatih/structs"
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

func (s Server) DeleteSession(ctx context.Context, request api.DeleteSessionRequestObject) (api.DeleteSessionResponseObject, error) {
	clog.WithGroupField(ctx, "actor", "userId", request.Params.XUserID)
	clog.WithGroupField(ctx, "request", "sessionId", request.SessionID)

	err := s.SessionService.Delete(ctx, request.SessionID, request.Params.XUserID)
	if err != nil {
		if errors.Is(err, session.ErrNotFound) {
			return api.DeleteSession404JSONResponse{Message: "session not found"}, nil
		}
		if errors.Is(err, session.ErrUnauthorized) {
			return api.DeleteSession401JSONResponse{Message: "unauthorized"}, nil
		}
		return api.DeleteSession500JSONResponse{Message: err.Error()}, nil
	}
	return api.DeleteSession204Response{}, nil
}

func (s Server) DeleteSnapshot(ctx context.Context, request api.DeleteSnapshotRequestObject) (api.DeleteSnapshotResponseObject, error) {
	clog.WithGroupField(ctx, "actor", "userId", request.Params.XUserID)
	clog.WithGroupField(ctx, "request", "snapshotId", request.SnapshotID)

	err := s.SnapshotService.Delete(ctx, request.SnapshotID, request.Params.XUserID)
	if err != nil {
		if errors.Is(err, snapshot.NotFound) {
			return api.DeleteSnapshot404JSONResponse{Message: "snapshot not found"}, nil
		}
		if errors.Is(err, snapshot.Unauthorized) {
			return api.DeleteSnapshot401JSONResponse{Message: "unauthorized"}, nil
		}
		return api.DeleteSnapshot500JSONResponse{Message: err.Error()}, nil
	}
	return api.DeleteSnapshot204Response{}, nil
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
			failed++
		}
		updated++
	}

	clog.WithGroupFields(ctx, "request", clog.Fields{
		"totalSnapshots": len(snapshots),
		"updated":        updated,
		"failed":         failed,
	})
	if failed > 0 {
		clog.WithGroupField(ctx, "request", "warning", "some snapshot items failed to backfill")
	}

	return api.BackfillSnapshotInfo200JSONResponse{
		Updated: int32(updated),
		Failed:  int32(failed),
	}, nil
}

func (s Server) MergeSnapshots(ctx context.Context, request api.MergeSnapshotsRequestObject) (api.MergeSnapshotsResponseObject, error) {
	clog.WithGroupField(ctx, "request", "snapshotId", request.SnapshotID)
	if request.Body == nil {
		return api.MergeSnapshots500JSONResponse{Message: "body cannot be empty"}, nil
	}
	clog.WithGroupField(ctx, "request", "sourceSnapshotId", request.Body.SourceSnapshotID)
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
	clog.WithGroupField(ctx, "actor", "userId", request.Params.XUserID)
	clog.WithGroupFields(ctx, "request", clog.Fields{
		"userId":      request.UserID,
		"characterId": request.Body.CharacterID,
	})
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
	clog.WithGroupFields(ctx, "request", clog.Fields{
		"userId":      request.UserID,
		"characterId": request.Params.CharacterID,
		"count":       request.Params.Count,
		"page":        request.Params.Page,
		"offset":      offset,
	})
	var charID *string
	if request.Params.CharacterID != "" {
		charID = &request.Params.CharacterID
	}
	result, err := s.SessionService.GetAll(
		ctx,
		&request.UserID,
		charID,
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
	clog.WithGroupField(ctx, "request", "userId", request.UserID)
	u, err := s.UserService.GetUser(ctx, request.UserID)
	if err != nil {
		return nil, err
	}
	if u.LastUpdatedCharacters.Add(time.Hour).Before(time.Now()) {
		clog.Go(ctx, "update-user-characters-bg", func(bgCtx context.Context) error {
			clog.WithGroupFields(bgCtx, "request", clog.Fields{
				"userId":              u.ID,
				"primaryMembershipId": u.PrimaryMembershipID,
			})
			t := int64(0)
			for _, membership := range u.Memberships {
				if membership.ID == u.PrimaryMembershipID {
					t = membership.Type
					break
				}
			}
			if t == 0 && len(u.Memberships) > 0 {
				t = u.Memberships[0].Type
			}
			pmId, err := strconv.ParseInt(u.PrimaryMembershipID, 10, 64)
			if err != nil {
				return fmt.Errorf("failed to parse primary membership id: %w", err)
			}

			characters, err := s.D2Service.GetCharacters(bgCtx, pmId, t)
			if err != nil {
				return err
			}
			if len(characters) == 0 {
				clog.WithGroupField(bgCtx, "request", "warning", "no characters found for user")
				return nil
			}
			return s.UserService.UpdateCharacters(bgCtx, u.ID, characters)
		})
	}

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
		if t == 0 && len(u.Memberships) > 0 {
			t = u.Memberships[0].Type
		}
		pmId, err := strconv.ParseInt(u.PrimaryMembershipID, 10, 64)
		if err != nil {
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
	clog.WithGroupFields(ctx, "request", clog.Fields{
		"characterId":  characterID,
		"count":        count,
		"minimumGames": minimumGames,
	})
	aggs, err := s.StatsService.GetAggregatesByCharacterID(ctx, characterID, request.Params.GameMode)
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

func (s Server) GetFeaturedLoadouts(ctx context.Context, request api.GetFeaturedLoadoutsRequestObject) (api.GetFeaturedLoadoutsResponseObject, error) {
	count := 5
	if request.Params.Count != nil && *request.Params.Count > 0 {
		count = *request.Params.Count
	}
	clog.WithGroupField(ctx, "request", "count", count)

	featured, err := s.StatsService.GetFeaturedLoadouts(ctx, count, request.Params.GameMode)
	if err != nil {
		return api.GetFeaturedLoadouts500JSONResponse(api.OneTrickError{
			Message: err.Error(),
		}), nil
	}

	return api.GetFeaturedLoadouts200JSONResponse{
		Items: featured,
	}, nil
}

func (s Server) GetFireteam(ctx context.Context, request api.GetFireteamRequestObject) (api.GetFireteamResponseObject, error) {
	clog.WithGroupField(ctx, "actor", "userId", request.Params.XUserID)
	members, err := s.UserService.GetFireteam(ctx, request.Params.XUserID)
	if err != nil {
		return nil, err
	}
	return api.GetFireteam200JSONResponse(members), nil
}

func (s Server) GetSession(ctx context.Context, request api.GetSessionRequestObject) (api.GetSessionResponseObject, error) {
	sessionID := request.SessionId
	clog.WithGroupField(ctx, "request", "sessionId", sessionID)
	ses, err := s.SessionService.Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	return api.GetSession200JSONResponse(*ses), nil
}

func (s Server) Search(ctx context.Context, request api.SearchRequestObject) (api.SearchResponseObject, error) {
	clog.WithGroupFields(ctx, "request", clog.Fields{
		"prefix": request.Body.Prefix,
		"page":   request.Body.Page,
	})
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
	clog.WithGroupFields(ctx, "request", clog.Fields{
		"count":  request.Params.Count,
		"page":   request.Params.Page,
		"offset": offset,
	})

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
	clog.WithGroupField(ctx, "actor", "userId", request.Params.XUserID)
	clog.WithGroupFields(ctx, "request", clog.Fields{
		"userId":      request.Body.UserID,
		"characterId": request.Body.CharacterID,
	})
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

func (s Server) CompleteSession(ctx context.Context, request api.CompleteSessionRequestObject) (api.CompleteSessionResponseObject, error) {
	clog.WithGroupField(ctx, "request", "sessionId", request.SessionId)
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
	return api.CompleteSession200JSONResponse(*ses), nil
}

func (s Server) UpdateSession(ctx context.Context, request api.UpdateSessionRequestObject) (api.UpdateSessionResponseObject, error) {
	clog.WithGroupField(ctx, "request", "sessionId", request.SessionID)
	description := ""
	if request.Body.Description != nil {
		description = *request.Body.Description
	}

	err := s.SessionService.Update(ctx, request.SessionID, request.Body.Name, description)
	if err != nil {
		return nil, err
	}

	ses, err := s.SessionService.Get(ctx, request.SessionID)
	if err != nil {
		return nil, err
	}
	return api.UpdateSession200JSONResponse(*ses), nil
}

func (s Server) GetSessionAggregates(ctx context.Context, request api.GetSessionAggregatesRequestObject) (api.GetSessionAggregatesResponseObject, error) {
	clog.WithGroupField(ctx, "request", "sessionId", request.SessionId)
	ses, err := s.SessionService.Get(ctx, request.SessionId)
	if err != nil {
		return nil, err
	}
	if len(ses.AggregateIDs) == 0 {
		return nil, fmt.Errorf("no aggregate found")
	}
	aggregates, err := s.AggregateService.GetAggregates(ctx, ses.AggregateIDs)
	if err != nil {
		return nil, err
	}
	uniqueIDS := make([]string, 0)
	seenSnapshotIDs := make(map[string]struct{})
	for _, a := range aggregates {
		link, ok := a.SnapshotLinks[ses.CharacterID]
		if !ok {
			continue
		}
		if link.SnapshotID == nil || *link.SnapshotID == "" {
			continue
		}
		if _, exists := seenSnapshotIDs[*link.SnapshotID]; exists {
			continue
		}
		seenSnapshotIDs[*link.SnapshotID] = struct{}{}
		uniqueIDS = append(uniqueIDS, *link.SnapshotID)
	}
	snapshots, err := s.SnapshotService.GetByIDs(ctx, uniqueIDS)
	if err != nil {
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
	clog.WithGroupField(ctx, "request", "snapshotId", request.SnapshotID)
	result, err := s.SnapshotService.Get(ctx, request.SnapshotID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch snapshot: %w", err)
	}

	return api.GetSnapshot200JSONResponse(*result), nil
}

func (s Server) Login(ctx context.Context, request api.LoginRequestObject) (api.LoginResponseObject, error) {
	code := request.Body.Code
	clog.WithGroupFields(ctx, "request", clog.Fields{
		"codeLen":     len(code),
		"codeSnippet": destiny.CodeSnippet(code),
	})

	resp, err := s.D2AuthService.GetAccessToken(ctx, code)
	if err != nil {
		return nil, err
	}

	existingUser, err := s.UserService.GetUser(ctx, resp.MembershipID)
	if err != nil && !errors.Is(err, user.NotFound) {
		return nil, err
	}
	if existingUser != nil {
		clog.WithGroupFields(ctx, "actor", clog.Fields{
			"userId":       existingUser.ID,
			"membershipId": resp.MembershipID,
			"isNewUser":    false,
		})
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
	if bUser == nil || (bUser.BungieNetUser == nil && bUser.DestinyMemberships == nil) {
		return nil, fmt.Errorf("failed to fetch user data")
	}

	m := make([]user.Membership, 0)
	u := user.User{}

	if bUser.BungieNetUser != nil {
		if bUser.BungieNetUser.MembershipId != nil {
			u.MemberID = *bUser.BungieNetUser.MembershipId
		}
		if bUser.BungieNetUser.DisplayName != nil {
			u.DisplayName = *bUser.BungieNetUser.DisplayName
		}
		if bUser.BungieNetUser.UniqueName != nil {
			u.UniqueName = *bUser.BungieNetUser.UniqueName
		}
	}
	if u.MemberID == "" {
		u.MemberID = resp.MembershipID
	}

	if bUser.PrimaryMembershipId != nil {
		u.PrimaryMembershipID = *bUser.PrimaryMembershipId
	}

	membershipType := int64(0)
	if bUser.DestinyMemberships != nil {
		for _, mem := range *bUser.DestinyMemberships {
			if mem.MembershipId == nil || mem.MembershipType == nil {
				continue
			}
			memID := *mem.MembershipId
			memType := int64(*mem.MembershipType)
			displayName := ""
			if mem.DisplayName != nil {
				displayName = *mem.DisplayName
			}

			m = append(m, user.Membership{
				ID:          memID,
				Type:        memType,
				DisplayName: displayName,
			})

			// Match primary membership ID to determine membershipType
			if u.PrimaryMembershipID != "" && memID == u.PrimaryMembershipID {
				membershipType = memType
			}

			// If PrimaryMembershipID was not set by Bungie, fallback to first membership
			if u.PrimaryMembershipID == "" {
				u.PrimaryMembershipID = memID
				membershipType = memType
			}
		}
	}

	// Fallback if membershipType is still 0 but memberships exist
	if membershipType == 0 && len(m) > 0 {
		membershipType = m[0].Type
		if u.PrimaryMembershipID == "" {
			u.PrimaryMembershipID = m[0].ID
		}
	}

	if u.DisplayName == "" && len(m) > 0 {
		u.DisplayName = m[0].DisplayName
	}

	clog.WithGroupFields(ctx, "actor", clog.Fields{
		"membershipId":        resp.MembershipID,
		"primaryMembershipId": u.PrimaryMembershipID,
		"membershipType":      membershipType,
		"numMemberships":      len(m),
		"isNewUser":           true,
	})

	if u.PrimaryMembershipID == "" {
		return nil, fmt.Errorf("user has no destiny memberships linked")
	}

	id, err := strconv.ParseInt(u.PrimaryMembershipID, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid primary membership ID: %w", err)
	}

	chars, err := s.D2Service.GetCharacters(ctx, id, membershipType)
	if err != nil {
		return nil, err
	}

	charIDs := make([]string, 0, len(chars))
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

	clog.WithGroupField(ctx, "actor", "userId", newUser.ID)
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
	resp, err := s.D2AuthService.RefreshAccessToken(ctx, request.Body.Code)
	if err != nil {
		return nil, err
	}
	existingUser, err := s.UserService.GetUser(ctx, resp.MembershipID)
	if err != nil {
		return nil, err
	}
	clog.WithGroupFields(ctx, "actor", clog.Fields{
		"userId":       existingUser.ID,
		"membershipId": resp.MembershipID,
	})
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

func (s Server) UpdateSnapshot(ctx context.Context, request api.UpdateSnapshotRequestObject) (api.UpdateSnapshotResponseObject, error) {
	snapshotID := request.SnapshotID
	clog.WithGroupField(ctx, "actor", "userId", request.Params.XUserID)
	clog.WithGroupField(ctx, "request", "snapshotId", snapshotID)
	snap, err := s.SnapshotService.Get(ctx, snapshotID)
	if err != nil {
		return api.UpdateSnapshot404JSONResponse{Message: "snapshot not found"}, nil
	}
	if snap.UserID != request.Params.XUserID {
		return api.UpdateSnapshot401JSONResponse{Message: "unauthorized"}, nil
	}

	err = s.SnapshotService.Update(ctx, snapshotID, func(data map[string]any) error {
		if request.Body.Name != "" {
			data["name"] = request.Body.Name
		}
		if request.Body.Description != nil && *request.Body.Description != "" {
			data["description"] = request.Body.Description
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	snap, err = s.SnapshotService.Get(ctx, snapshotID)
	if err != nil {
		return nil, err
	}
	return api.UpdateSnapshot200JSONResponse(*snap), nil
}

func (s Server) GetSnapshotAggregates(ctx context.Context, request api.GetSnapshotAggregatesRequestObject) (api.GetSnapshotAggregatesResponseObject, error) {
	clog.WithGroupField(ctx, "request", "snapshotId", request.SnapshotID)
	snap, err := s.SnapshotService.Get(ctx, request.SnapshotID)
	if err != nil {
		return nil, err
	}

	aggs, err := s.StatsService.GetAggregatesForSnapshot(ctx, snap.ID, request.Params.GameMode)
	if err != nil {
		return nil, err
	}

	return api.GetSnapshotAggregates200JSONResponse(aggs), nil
}

func (s Server) GetSnapshots(ctx context.Context, request api.GetSnapshotsRequestObject) (api.GetSnapshotsResponseObject, error) {
	userID := request.Params.UserID
	characterID := request.Params.CharacterID

	count := 10
	if request.Params.Count != nil && *request.Params.Count > 0 {
		count = *request.Params.Count
	}
	offset := 0
	if request.Params.Page != nil && *request.Params.Page > 0 {
		offset = *request.Params.Page * count
	}

	minimumGames := 0
	if request.Params.MinimumGames != nil {
		minimumGames = *request.Params.MinimumGames
	}
	includeStats := false
	if request.Params.IncludeStats != nil {
		includeStats = *request.Params.IncludeStats
	}
	sortBy := "created_at"
	if request.Params.SortBy != nil {
		sortBy = string(*request.Params.SortBy)
	}

	clog.WithGroupFields(ctx, "request", clog.Fields{
		"userId":       userID,
		"characterId":  characterID,
		"count":        count,
		"offset":       offset,
		"minimumGames": minimumGames,
		"includeStats": includeStats,
		"sortBy":       sortBy,
	})
	if request.Params.GameMode != nil {
		clog.WithGroupField(ctx, "request", "gameMode", string(*request.Params.GameMode))
	}

	items, stats, counts, err := s.StatsService.GetSnapshotsWithMetrics(
		ctx,
		userID,
		characterID,
		request.Params.GameMode,
		minimumGames,
		sortBy,
		includeStats,
		count,
		offset,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch snapshots: %w", err)
	}

	return api.GetSnapshots200JSONResponse{
		Items: items,
		Stats: stats,
		Count: counts,
	}, nil
}

func (s Server) CreateSnapshot(ctx context.Context, request api.CreateSnapshotRequestObject) (api.CreateSnapshotResponseObject, error) {
	userID := request.Params.XUserID
	membershipID := request.Params.XMembershipID
	characterID := request.Body.CharacterID

	clog.WithGroupFields(ctx, "actor", clog.Fields{
		"userId":       userID,
		"membershipId": membershipID,
	})
	clog.WithGroupField(ctx, "request", "characterId", characterID)

	data, err := s.SnapshotService.Save(ctx, userID, membershipID, characterID)
	if err != nil {
		return nil, fmt.Errorf("failed to save snapshot: %w", err)
	}
	return api.CreateSnapshot201JSONResponse(*data), nil
}

func (s Server) GetActivities(ctx context.Context, request api.GetActivitiesRequestObject) (api.GetActivitiesResponseObject, error) {
	params := request.Params

	clog.WithGroupFields(ctx, "actor", clog.Fields{
		"userId":       params.XUserID,
		"membershipId": params.XMembershipID,
	})

	membershipType, err := s.UserService.GetMembershipType(ctx, params.XUserID, params.XMembershipID)
	if err != nil {
		return nil, err
	}

	mode := api.AllPvP
	if request.Params.Mode != nil {
		mode = *request.Params.Mode
	}

	clog.WithGroupFields(ctx, "request", clog.Fields{
		"characterId": params.CharacterID,
		"mode":        string(mode),
		"count":       params.Count,
		"page":        params.Page,
	})

	history := make([]api.ActivityHistory, 0)
	switch mode {
	case api.AllPvP:
		history, err = s.D2Service.GetAllPVPActivity(ctx, params.XMembershipID, membershipType, params.CharacterID, params.Count, params.Page)
		if err != nil {
			return nil, err
		}
	case api.Competitive:
		history, err = s.D2Service.GetCompetitiveActivity(ctx, params.XMembershipID, membershipType, params.CharacterID, params.Count, params.Page)
		if err != nil {
			return nil, err
		}

	case api.Quickplay:
		history, err = s.D2Service.GetQuickPlayActivity(ctx, params.XMembershipID, membershipType, params.CharacterID, params.Count, params.Page)
		if err != nil {
			return nil, err
		}
	case api.IronBanner:
		history, err = s.D2Service.GetIronBannerActivity(ctx, params.XMembershipID, membershipType, params.CharacterID, params.Count, params.Page)
		if err != nil {
			return nil, err
		}
	default:
		history, err = s.D2Service.GetAllPVPActivity(ctx, params.XMembershipID, membershipType, params.CharacterID, params.Count, params.Page)
		if err != nil {
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

	clog.WithGroupField(ctx, "request", "activityId", activityID)
	activityDetails, teams, err := s.D2Service.GetActivity(ctx, activityID)
	if err != nil {
		return api.GetActivity500JSONResponse{Message: "failed to fetch weapon data for activity"}, nil
	}
	if activityDetails == nil {
		return api.GetActivity500JSONResponse{Message: "no activity data"}, nil
	}

	// TODO: Come to fix this when no aggregate has been made for an activity
	agg, err := s.AggregateService.GetAggregate(ctx, activityID)
	if err != nil && !errors.Is(err, aggregate.NotFound) {
		return api.GetActivity500JSONResponse{Message: err.Error()}, nil
	}

	entries := make([]map[string]any, 0)
	for _, entry := range *activityDetails.Entries {
		entries = append(entries, structs.Map(entry))
	}

	var snapshotIDS []string
	for _, link := range agg.SnapshotLinks {
		if link.SnapshotID == nil {
			continue
		}
		snapshotIDS = append(snapshotIDS, *link.SnapshotID)
	}

	if len(snapshotIDS) == 0 {
		// TODO: Technically, could still grab the users but not snapshots.
		return api.GetActivity200JSONResponse{
			Activity:        agg.ActivityDetails,
			Teams:           teams,
			Aggregate:       agg,
			PostGameEntries: &entries,
		}, nil
	}

	snapshots := make(map[string]api.CharacterSnapshot)
	snaps, err := s.SnapshotService.GetByIDs(ctx, snapshotIDS)
	if err != nil {
		return api.GetActivity500JSONResponse{Message: err.Error()}, nil
	}
	for _, snap := range snaps {
		snapshots[snap.CharacterID] = snap
	}
	// Build users map keyed by characterId when available
	users := make(map[string]api.User)
	for characterID, snap := range snapshots {
		u, err := s.UserService.GetUser(ctx, snap.UserID)
		if err != nil {
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
	_ = clog.LoopRollup(ctx, "userService.backfillCharacters", users, func(iterCtx context.Context, _ int, u user.User) error {
		clog.WithGroupField(iterCtx, "user", "userId", u.ID)
		if err := s.UserService.BackfillCharacters(iterCtx, u.ID); err != nil {
			failed++
			return err
		}
		updated++
		return nil
	})
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
