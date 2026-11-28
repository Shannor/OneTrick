package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	httpadapter "oneTrick/adapters/driving/http"
	"oneTrick/api"
	"oneTrick/core/activity"
	"oneTrick/core/domain"
	"oneTrick/core/fireteam"
	"oneTrick/core/telemetry"
	"oneTrick/services/aggregate"
	"oneTrick/services/session"
	"oneTrick/services/snapshot"
	"oneTrick/services/stats"
	"oneTrick/services/user"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	clog "github.com/Shannor/canonical-logger"
)

type CharacterReader interface {
	GetCharacters(ctx context.Context, primaryMembershipID int64, membershipType int64) ([]domain.Character, error)
}

type ActivityHistoryReader interface {
	GetActivityHistory(ctx context.Context, membershipID string, membershipType int64, characterID string, mode domain.ActivityQueryMode, count int64, page int64) ([]domain.ActivityInfo, error)
}

type OAuthClient interface {
	ExchangeCode(ctx context.Context, code string) (domain.OAuthToken, error)
	RefreshAccessToken(ctx context.Context, refreshToken string) (domain.OAuthToken, error)
	GetCurrentAccount(ctx context.Context, accessToken string) (domain.BungieAccount, error)
}

type ItemManifestLookup interface {
	GetItem(ctx context.Context, id int64) (domain.ItemManifest, bool, error)
}

// ensure that we've conformed to the `ServerInterface` with a compile-time check
var _ api.StrictServerInterface = (*Server)(nil)

type Server struct {
	CharacterReader   CharacterReader
	ActivityHistory   ActivityHistoryReader
	D2AuthService     OAuthClient
	D2ManifestService ItemManifestLookup
	UserService       user.Service
	SnapshotService   snapshot.Service
	AggregateService  aggregate.Service
	SessionService    session.Service
	FireteamService   fireteam.Service
	StatsService      stats.Service
	TelemetryService  telemetry.Service
	ActivityService   activity.Service
	InternalAPIKey    string
}

func NewServer(
	characterReader CharacterReader,
	activityHistory ActivityHistoryReader,
	authService OAuthClient,
	userService user.Service,
	snapshotService snapshot.Service,
	aggregateService aggregate.Service,
	sessionService session.Service,
	fireteamService fireteam.Service,
	manifestService ItemManifestLookup,
	statsService stats.Service,
	telemetrySvc telemetry.Service,
	activitySvc activity.Service,
	internalAPIKey string,
) Server {
	return Server{
		CharacterReader:   characterReader,
		ActivityHistory:   activityHistory,
		D2AuthService:     authService,
		UserService:       userService,
		SnapshotService:   snapshotService,
		AggregateService:  aggregateService,
		SessionService:    sessionService,
		FireteamService:   fireteamService,
		D2ManifestService: manifestService,
		StatsService:      statsService,
		TelemetryService:  telemetrySvc,
		ActivityService:   activitySvc,
		InternalAPIKey:    internalAPIKey,
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
			d2Item, found, err := s.D2ManifestService.GetItem(ctx, item.ItemHash)
			if err != nil || !found {
				failed++
				continue
			}
			item.ItemProperties.BaseInfo.ItemTypeAndTierDisplayName = d2Item.ItemTypeAndTierDisplayName
			item.ItemProperties.BaseInfo.ItemTypeDisplayName = d2Item.ItemTypeDisplayName
			item.ItemProperties.BaseInfo.TierType = d2Item.TierType
			item.ItemProperties.BaseInfo.TierTypeName = d2Item.TierTypeName
			item.ItemProperties.BaseInfo.EquipableItemSetHash = d2Item.EquipableItemSetHash
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
	clog.WithGroupField(ctx, "actor", "userId", request.Params.XUserID)
	clog.WithGroupField(ctx, "request", "snapshotId", request.SnapshotID)
	if request.Body == nil {
		return api.MergeSnapshots400JSONResponse{Message: "body cannot be empty"}, nil
	}
	if request.SnapshotID == "" {
		return api.MergeSnapshots400JSONResponse{Message: "snapshotID cannot be empty"}, nil
	}

	sourceIDs := make([]string, 0)
	seen := make(map[string]struct{})
	addSourceID := func(id string) {
		if id == "" {
			return
		}
		if _, exists := seen[id]; exists {
			return
		}
		seen[id] = struct{}{}
		sourceIDs = append(sourceIDs, id)
	}
	if request.Body.SourceSnapshotID != nil {
		addSourceID(*request.Body.SourceSnapshotID)
	}
	if request.Body.SourceSnapshotIDs != nil {
		for _, id := range *request.Body.SourceSnapshotIDs {
			addSourceID(id)
		}
	}

	if len(sourceIDs) == 0 {
		return api.MergeSnapshots400JSONResponse{Message: "at least one source snapshot ID is required"}, nil
	}
	if slices.Contains(sourceIDs, request.SnapshotID) {
		return api.MergeSnapshots400JSONResponse{Message: "cannot merge snapshot with itself"}, nil
	}
	clog.WithGroupField(ctx, "request", "sourceSnapshotIds", sourceIDs)

	if _, err := s.SnapshotService.Merge(ctx, request.Params.XUserID, request.SnapshotID, sourceIDs); err != nil {
		if errors.Is(err, snapshot.InvalidMerge) {
			return api.MergeSnapshots400JSONResponse{Message: err.Error()}, nil
		}
		if errors.Is(err, snapshot.Unauthorized) {
			return api.MergeSnapshots401JSONResponse{Message: "unauthorized"}, nil
		}
		if errors.Is(err, snapshot.NotFound) {
			return api.MergeSnapshots404JSONResponse{Message: "snapshot not found"}, nil
		}
		return api.MergeSnapshots500JSONResponse{Message: err.Error()}, nil
	}
	return api.MergeSnapshots200JSONResponse(true), nil
}

func (s Server) GetSimilarSnapshots(ctx context.Context, request api.GetSimilarSnapshotsRequestObject) (api.GetSimilarSnapshotsResponseObject, error) {
	params := httpadapter.ToSimilarSnapshotsParams(request)
	clog.WithGroupFields(ctx, "request", clog.Fields{
		"snapshotId":   params.SnapshotID,
		"includeStats": params.IncludeStats,
		"tier":         string(params.Tier),
	})
	if params.GameMode != "" {
		clog.WithGroupField(ctx, "request", "gameMode", params.GameMode)
	}
	if params.SnapshotID == "" {
		return api.GetSimilarSnapshots404JSONResponse{Message: "snapshot not found"}, nil
	}

	target, items, stats, counts, err := s.StatsService.GetSimilarSnapshots(ctx, params)
	if err != nil {
		if errors.Is(err, snapshot.NotFound) {
			return api.GetSimilarSnapshots404JSONResponse{Message: "snapshot not found"}, nil
		}
		return api.GetSimilarSnapshots500JSONResponse{Message: err.Error()}, nil
	}
	if target == nil {
		return api.GetSimilarSnapshots404JSONResponse{Message: "snapshot not found"}, nil
	}

	return httpadapter.ToAPISimilarSnapshotsResponse(*target, items, stats, counts), nil
}

func (s Server) StartUserSession(ctx context.Context, request api.StartUserSessionRequestObject) (api.StartUserSessionResponseObject, error) {
	clog.WithGroupField(ctx, "actor", "userId", request.Params.XUserID)
	if request.Body == nil {
		return api.StartUserSession400JSONResponse{Message: "body cannot be empty"}, nil
	}
	targetUserID := strings.TrimSpace(request.UserID)
	bodyUserID := strings.TrimSpace(request.Body.UserID)
	if bodyUserID != "" && bodyUserID != targetUserID {
		return api.StartUserSession400JSONResponse{Message: "body userId must match path userId"}, nil
	}
	if targetUserID == "" {
		targetUserID = bodyUserID
	}
	trackComp := request.Body.TrackComp != nil && *request.Body.TrackComp
	clog.WithGroupFields(ctx, "request", clog.Fields{
		"userId":      targetUserID,
		"characterId": request.Body.CharacterID,
		"trackComp":   trackComp,
	})

	result, err := s.FireteamService.StartSession(
		ctx,
		request.Params.XUserID,
		fireteam.StartSessionTarget{
			UserID:      targetUserID,
			CharacterID: request.Body.CharacterID,
		},
		trackComp,
	)
	if err != nil {
		return httpadapter.ToAPIStartUserSessionError(err), nil
	}
	return api.StartUserSession201JSONResponse(httpadapter.ToAPISession(result)), nil
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

const CharacterRefreshTTL = 6 * time.Hour

var inFlightCharacterUpdates sync.Map

func (s Server) GetUser(ctx context.Context, request api.GetUserRequestObject) (api.GetUserResponseObject, error) {
	clog.WithGroupField(ctx, "request", "userId", request.UserID)
	u, err := s.UserService.GetUser(ctx, request.UserID)
	if err != nil {
		return nil, err
	}

	result := api.GetUser200JSONResponse{
		DisplayName:  u.DisplayName,
		UniqueName:   u.UniqueName,
		Id:           u.ID,
		MembershipId: u.PrimaryMembershipID,
		Characters:   u.Characters,
	}

	if len(result.Characters) == 0 {
		pmId, t, err := u.PrimaryMembership()
		if err != nil {
			return result, err
		}
		domainChars, err := s.CharacterReader.GetCharacters(ctx, pmId, t)
		if err != nil {
			return result, err
		}
		if len(domainChars) > 0 {
			characters := httpadapter.ToAPICharacters(domainChars)
			result.Characters = characters
			_ = s.UserService.UpdateCharacters(ctx, u.ID, characters)
		}
		return result, nil
	}

	if u.LastUpdatedCharacters.Add(CharacterRefreshTTL).Before(time.Now()) {
		if _, loaded := inFlightCharacterUpdates.LoadOrStore(u.ID, struct{}{}); !loaded {
			clog.Go(ctx, "update-user-characters-bg", func(bgCtx context.Context) error {
				defer inFlightCharacterUpdates.Delete(u.ID)
				clog.WithGroupFields(bgCtx, "request", clog.Fields{
					"userId":              u.ID,
					"primaryMembershipId": u.PrimaryMembershipID,
				})
				pmId, t, err := u.PrimaryMembership()
				if err != nil {
					return err
				}

				domainChars, err := s.CharacterReader.GetCharacters(bgCtx, pmId, t)
				if err != nil {
					return err
				}
				if len(domainChars) == 0 {
					clog.WithGroupField(bgCtx, "request", "warning", "no characters found for user")
					return nil
				}
				return s.UserService.UpdateCharacters(bgCtx, u.ID, httpadapter.ToAPICharacters(domainChars))
			})
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
	if request.Params.GameMode != nil {
		clog.WithGroupField(ctx, "request", "gameMode", string(*request.Params.GameMode))
	}
	result, performanceStats, counts, err := s.StatsService.GetBestPerformingLoadouts(ctx, characterID, request.Params.GameMode, count, minimumGames)
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
	members, err := s.FireteamService.GetFireteam(ctx, request.Params.XUserID)
	if err != nil {
		if errors.Is(err, fireteam.ErrUserNotFound) {
			return api.GetFireteam404JSONResponse{Message: "user not found"}, nil
		}
		if errors.Is(err, fireteam.ErrDestinyUnavailable) {
			return api.GetFireteam503JSONResponse{
				Message: "Destiny 2 servers are currently unavailable",
				Status:  api.ErrDestinyServerDown,
			}, nil
		}
		return nil, err
	}
	return api.GetFireteam200JSONResponse(httpadapter.ToAPIFireteamMembers(members)), nil
}

func (s Server) StartFireteamSessions(ctx context.Context, request api.StartFireteamSessionsRequestObject) (api.StartFireteamSessionsResponseObject, error) {
	clog.WithGroupField(ctx, "actor", "userId", request.Params.XUserID)
	if request.Body == nil || len(request.Body.Members) == 0 {
		return api.StartFireteamSessions400JSONResponse{
			Message: "members must contain at least one target",
			Status:  api.ErrUnknownError,
		}, nil
	}
	trackComp := request.Body.TrackComp != nil && *request.Body.TrackComp
	clog.WithGroupFields(ctx, "request", clog.Fields{
		"memberCount": len(request.Body.Members),
		"trackComp":   trackComp,
	})

	results, err := s.FireteamService.StartSessions(
		ctx,
		request.Params.XUserID,
		httpadapter.ToStartSessionTargets(request.Body.Members),
		trackComp,
	)
	if err != nil {
		return httpadapter.ToAPIStartFireteamSessionsError(err), nil
	}

	return httpadapter.ToAPIStartFireteamSessionsResponse(results), nil
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
	if request.Body == nil {
		return api.Search400JSONResponse{Message: "prefix must be non-empty and page must be >= 0"}, nil
	}
	prefix := strings.TrimSpace(request.Body.Prefix)
	clog.WithGroupFields(ctx, "request", clog.Fields{
		"queryLen": len(prefix),
		"page":     request.Body.Page,
	})
	if prefix == "" || request.Body.Page < 0 {
		return api.Search400JSONResponse{Message: "prefix must be non-empty and page must be >= 0"}, nil
	}

	results, hasMore, err := s.UserService.Search(ctx, prefix, int(request.Body.Page))
	if err != nil {
		return api.Search500JSONResponse{
			Message: err.Error(),
			Status:  api.ErrInternalSystemDown,
		}, nil
	}
	if results == nil {
		results = []api.SearchUserResult{}
	}
	return api.Search200JSONResponse{
		Results: results,
		HasMore: hasMore,
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
	if request.Body == nil {
		return api.StartSession400JSONResponse{Message: "body cannot be empty"}, nil
	}
	trackComp := request.Body.TrackComp != nil && *request.Body.TrackComp
	clog.WithGroupFields(ctx, "request", clog.Fields{
		"userId":      request.Body.UserID,
		"characterId": request.Body.CharacterID,
		"trackComp":   trackComp,
	})

	result, err := s.FireteamService.StartSession(
		ctx,
		request.Params.XUserID,
		fireteam.StartSessionTarget{
			UserID:      request.Body.UserID,
			CharacterID: request.Body.CharacterID,
		},
		trackComp,
	)
	if err != nil {
		return httpadapter.ToAPIStartSessionError(err), nil
	}
	return api.StartSession201JSONResponse(httpadapter.ToAPISession(result)), nil
}

func (s Server) CompleteSession(ctx context.Context, request api.CompleteSessionRequestObject) (api.CompleteSessionResponseObject, error) {
	if request.Params.XUserID != "" {
		clog.WithGroupField(ctx, "actor", "userId", request.Params.XUserID)
	}
	clog.WithGroupField(ctx, "request", "sessionId", request.SessionId)
	if request.Body != nil && request.Body.CompletedAt != nil {
		ses, err := s.SessionService.Complete(ctx, request.SessionId)
		if err != nil {
			return nil, err
		}
		return api.CompleteSession200JSONResponse(*ses), nil
	}

	ses, err := s.SessionService.Get(ctx, request.SessionId)
	if err != nil {
		return nil, err
	}
	return api.CompleteSession200JSONResponse(*ses), nil
}

func (s Server) FinalizeSessionsComp(ctx context.Context, request api.FinalizeSessionsCompRequestObject) (api.FinalizeSessionsCompResponseObject, error) {
	if request.Params.XInternalAPIKey == nil || *request.Params.XInternalAPIKey == "" || s.InternalAPIKey == "" ||
		subtle.ConstantTimeCompare([]byte(*request.Params.XInternalAPIKey), []byte(s.InternalAPIKey)) != 1 {
		return api.FinalizeSessionsComp401JSONResponse{Message: "unauthorized"}, nil
	}
	if request.Body == nil {
		return api.FinalizeSessionsComp400JSONResponse{Message: "body cannot be empty"}, nil
	}

	if err := clog.LoopRollup(ctx, "sessionService.finalizeCompBatch", request.Body.SessionIDs, func(iterCtx context.Context, _ int, sessionID string) error {
		if sessionID == "" {
			return nil
		}
		return s.SessionService.FinalizeCompTracking(iterCtx, sessionID)
	}); err != nil {
		return api.FinalizeSessionsComp500JSONResponse{Message: "failed to finalize one or more sessions"}, nil
	}

	return api.FinalizeSessionsComp204Response{}, nil
}

func (s Server) RunSessionTick(ctx context.Context, request api.RunSessionTickRequestObject) (api.RunSessionTickResponseObject, error) {
	if request.Params.XInternalAPIKey == nil || *request.Params.XInternalAPIKey == "" || s.InternalAPIKey == "" ||
		subtle.ConstantTimeCompare([]byte(*request.Params.XInternalAPIKey), []byte(s.InternalAPIKey)) != 1 {
		return api.RunSessionTick401JSONResponse{Message: "unauthorized"}, nil
	}

	opts := session.TickOptions{}
	if request.Body != nil {
		if request.Body.DryRun != nil {
			opts.DryRun = *request.Body.DryRun
		}
		if request.Body.SkipSave != nil {
			opts.SkipSave = *request.Body.SkipSave
		}
	}

	clog.WithGroupFields(ctx, "request", clog.Fields{
		"dryRun":   opts.DryRun,
		"skipSave": opts.SkipSave,
	})

	result, err := s.SessionService.RunTick(ctx, opts)
	if err != nil {
		return api.RunSessionTick500JSONResponse{Message: err.Error()}, nil
	}
	if result == nil {
		return api.RunSessionTick200JSONResponse{DryRun: opts.DryRun}, nil
	}
	return api.RunSessionTick200JSONResponse(*result), nil
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
	result, err := s.SessionService.GetSessionAggregates(ctx, request.SessionId)
	if err != nil {
		return httpadapter.ToAPISessionAggregatesError(err), nil
	}
	return httpadapter.ToAPISessionAggregatesResponse(result), nil
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
		"codeSnippet": httpadapter.MaskOAuthCode(code),
	})

	resp, err := s.D2AuthService.ExchangeCode(ctx, code)
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
		needsDisplayLower := existingUser.DisplayName != "" && existingUser.DisplayNameLower == ""
		needsUniqueLower := existingUser.UniqueName != "" && existingUser.UniqueNameLower == ""
		if needsDisplayLower || needsUniqueLower {
			uID := existingUser.ID
			dName := existingUser.DisplayName
			uName := existingUser.UniqueName
			clog.Go(ctx, "backfill-user-search-fields-bg", func(bgCtx context.Context) error {
				clog.WithGroupField(bgCtx, "request", "userId", uID)
				return s.UserService.UpdateSearchFields(bgCtx, uID, dName, uName)
			})
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
		return api.Login200JSONResponse(result), nil
	}

	bUser, err := s.D2AuthService.GetCurrentAccount(ctx, resp.AccessToken)
	if err != nil {
		return nil, err
	}
	if bUser.BungieNetMembershipID == "" && len(bUser.DestinyMemberships) == 0 {
		return nil, fmt.Errorf("failed to fetch user data")
	}

	m := make([]user.Membership, 0, len(bUser.DestinyMemberships))
	u := user.User{
		MemberID:            bUser.BungieNetMembershipID,
		DisplayName:         bUser.DisplayName,
		UniqueName:          bUser.UniqueName,
		PrimaryMembershipID: bUser.PrimaryMembershipID,
	}
	if u.MemberID == "" {
		u.MemberID = resp.MembershipID
	}

	membershipType := int64(0)
	for _, mem := range bUser.DestinyMemberships {
		if mem.ID == "" || mem.Type == 0 {
			continue
		}
		m = append(m, user.Membership{
			ID:          mem.ID,
			Type:        mem.Type,
			DisplayName: mem.DisplayName,
		})

		if u.PrimaryMembershipID != "" && mem.ID == u.PrimaryMembershipID {
			membershipType = mem.Type
		}

		if u.PrimaryMembershipID == "" {
			u.PrimaryMembershipID = mem.ID
			membershipType = mem.Type
		}
	}

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

	domainChars, err := s.CharacterReader.GetCharacters(ctx, id, membershipType)
	if err != nil {
		return nil, err
	}
	chars := httpadapter.ToAPICharacters(domainChars)

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

	items, stats, counts, similarCounts, err := s.StatsService.GetSnapshotsWithMetrics(
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
		Items:         items,
		Stats:         stats,
		Count:         counts,
		SimilarCounts: similarCounts,
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

	domainHistory, err := s.ActivityHistory.GetActivityHistory(
		ctx,
		params.XMembershipID,
		membershipType,
		params.CharacterID,
		httpadapter.ToActivityQueryMode(mode),
		params.Count,
		params.Page,
	)
	if err != nil {
		return nil, err
	}
	history := httpadapter.ToAPIActivityHistories(domainHistory)
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
	reqFields := clog.Fields{"activityId": request.ActivityID}
	if request.Params.CharacterID != nil && *request.Params.CharacterID != "" {
		reqFields["characterId"] = *request.Params.CharacterID
	}
	clog.WithGroupFields(ctx, "request", reqFields)

	report, err := s.ActivityService.GetActivity(ctx, request.ActivityID)
	if err != nil {
		return httpadapter.ToAPIActivityError(err), nil
	}
	return httpadapter.ToAPIActivityResponse(report), nil
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

func (s Server) BackfillSessionSummaries(ctx context.Context, request api.BackfillSessionSummariesRequestObject) (api.BackfillSessionSummariesResponseObject, error) {
	if request.Params.XInternalAPIKey == nil || *request.Params.XInternalAPIKey == "" || s.InternalAPIKey == "" ||
		subtle.ConstantTimeCompare([]byte(*request.Params.XInternalAPIKey), []byte(s.InternalAPIKey)) != 1 {
		return httpadapter.ToAPIBackfillSessionSummariesUnauthorized(), nil
	}

	dryRun := request.Params.DryRun != nil && *request.Params.DryRun
	clog.WithGroupField(ctx, "request", "dryRun", dryRun)

	res, err := s.SessionService.BackfillSessionSummaries(ctx, dryRun)
	if err != nil {
		return httpadapter.ToAPIBackfillSessionSummariesError(err), nil
	}
	return httpadapter.ToAPIBackfillSessionSummariesResponse(res), nil
}

func (s Server) GetCharacterTelemetry(ctx context.Context, request api.GetCharacterTelemetryRequestObject) (api.GetCharacterTelemetryResponseObject, error) {
	reqFields := clog.Fields{
		"userId":      request.UserID,
		"characterId": request.CharacterID,
	}
	if request.Params.SnapshotID != nil && *request.Params.SnapshotID != "" {
		reqFields["snapshotId"] = *request.Params.SnapshotID
	}
	if request.Params.GameMode != nil && *request.Params.GameMode != "" {
		reqFields["gameMode"] = string(*request.Params.GameMode)
	}
	if request.Params.Limit != nil {
		reqFields["limit"] = *request.Params.Limit
	}
	if request.Params.From != nil {
		reqFields["from"] = request.Params.From.Format(time.RFC3339)
	}
	if request.Params.To != nil {
		reqFields["to"] = request.Params.To.Format(time.RFC3339)
	}
	clog.WithGroupFields(ctx, "request", reqFields)

	if request.Params.Limit != nil && (*request.Params.Limit < 1 || *request.Params.Limit > telemetry.MaxLimit) {
		return api.GetCharacterTelemetry400JSONResponse{
			Message: fmt.Sprintf("limit must be between 1 and %d", telemetry.MaxLimit),
			Status:  api.ErrUnknownError,
		}, nil
	}

	result, err := s.TelemetryService.GetCharacterTelemetry(ctx, httpadapter.ToTelemetryQuery(request))
	if err != nil {
		if errors.Is(err, telemetry.ErrInvalidQuery) {
			return api.GetCharacterTelemetry400JSONResponse{
				Message: err.Error(),
				Status:  api.ErrUnknownError,
			}, nil
		}
		if errors.Is(err, telemetry.ErrNotFound) {
			return api.GetCharacterTelemetry404JSONResponse{
				Message: err.Error(),
				Status:  api.ErrUnknownError,
			}, nil
		}
		return api.GetCharacterTelemetry500JSONResponse{
			Message: err.Error(),
			Status:  api.ErrInternalSystemDown,
		}, nil
	}

	return httpadapter.ToAPITelemetryResponse(result), nil
}
