package session

import (
	"context"
	"errors"
	"fmt"
	"oneTrick/api"
	"oneTrick/generator"
	"oneTrick/services/aggregate"
	"oneTrick/services/destiny"
	"oneTrick/services/snapshot"
	"oneTrick/services/user"
	"slices"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	clog "github.com/Shannor/canonical-logger"
)

type TickOptions struct {
	DryRun   bool
	SkipSave bool
}

type Service interface {
	Start(ctx context.Context, userID string, characterID string, startedBy api.AuditField, trackComp bool) (*api.Session, error)
	Update(ctx context.Context, sessionID string, name, description string) error
	AddAggregateIDs(ctx context.Context, sessionID string, aggregateIDs []string) error
	Get(ctx context.Context, ID string) (*api.Session, error)
	GetActive(ctx context.Context, userID string, characterID string) (*api.Session, error)
	GetAll(ctx context.Context, userID *string, characterID *string, status *api.SessionStatus, count int, offset int) ([]api.Session, error)
	Complete(ctx context.Context, ID string) error
	FinalizeCompTracking(ctx context.Context, sessionID string) error
	SetLastActivity(ctx context.Context, ID, activityID string) error
	Delete(ctx context.Context, sessionID string, userID string) error
	ComputeSessionSummary(ctx context.Context, ses api.Session) (*api.SessionSummary, error)
	UpdateSessionSummary(ctx context.Context, sessionID string, summary *api.SessionSummary) error
	CleanupEmptyCompleted(ctx context.Context, dryRun bool) (int, error)
	RunTick(ctx context.Context, opts TickOptions) (*api.SessionTickResponse, error)
}

var (
	ErrNotFound     = fmt.Errorf("session not found")
	ErrUnauthorized = fmt.Errorf("unauthorized")
)

type service struct {
	store            sessionStore
	aggregateService aggregate.Service
	userService      user.Service
	d2Service        destiny.Service
	snapshotService  snapshot.Service
	manifestService  destiny.ManifestService
	nowFn            func() time.Time
}

var _ Service = (*service)(nil)

func NewService(
	db *firestore.Client,
	aggregateService aggregate.Service,
	userService user.Service,
	d2Service destiny.Service,
	snapshotService snapshot.Service,
	manifestService destiny.ManifestService,
) Service {
	return &service{
		store:            &firestoreStore{db: db},
		aggregateService: aggregateService,
		userService:      userService,
		d2Service:        d2Service,
		snapshotService:  snapshotService,
		manifestService:  manifestService,
	}
}

const (
	collection = "sessions"
	// CutOffDuration is when an inactive or stale pending session is automatically ended or deleted.
	CutOffDuration           = 2 * time.Hour
	completedGracePeriod     = 15 * time.Minute
	activityStartGracePeriod = 15 * time.Minute
)

func (s service) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

func (s service) Start(ctx context.Context, userID string, characterID string, startedBy api.AuditField, trackComp bool) (*api.Session, error) {
	clog.WithGroupFields(ctx, "sessionService.start", clog.Fields{
		"userId":      userID,
		"characterId": characterID,
		"startedById": startedBy.ID,
		"trackComp":   trackComp,
	})
	if ok, err := s.HasActive(ctx, userID, characterID); ok || err != nil {
		return nil, fmt.Errorf("session already active")
	}
	result := &api.Session{
		UserID:       userID,
		StartedAt:    time.Now(),
		CharacterID:  characterID,
		Name:         new(generator.SessionName()),
		AggregateIDs: make([]string, 0),
		Status:       new(api.SessionPending),
		StartedBy:    &startedBy,
	}

	if trackComp {
		compTracking := &api.CompTracking{
			Enabled: true,
		}
		if s.userService != nil && s.d2Service != nil {
			pmID, membershipType, err := s.userService.GetPrimaryMembership(ctx, userID)
			if err != nil {
				clog.WithGroupFields(ctx, "sessionService.start", clog.Fields{
					"warning":       "failed to resolve primary membership for competitive tracking",
					"compRankError": err.Error(),
				})
			} else {
				snap, err := s.d2Service.GetCompetitiveRank(ctx, pmID, membershipType, characterID)
				if err != nil {
					clog.WithGroupFields(ctx, "sessionService.start", clog.Fields{
						"warning":       "failed to fetch starting competitive rank",
						"compRankError": err.Error(),
					})
				} else {
					compTracking.Start = snap
				}
			}
		} else {
			clog.WithGroupField(ctx, "sessionService.start", "warning", "competitive rank services not configured")
		}
		result.CompTracking = compTracking
	}

	created, err := s.store.createDoc(ctx, result)
	if err != nil {
		return nil, err
	}

	clog.WithGroupField(ctx, "sessionService.start", "sessionId", created.ID)
	return created, nil
}

func (s service) HasActive(ctx context.Context, userID string, characterID string) (bool, error) {
	clog.WithGroupFields(ctx, "sessionService.hasActive", clog.Fields{
		"userId":      userID,
		"characterId": characterID,
	})
	hasActive, err := s.store.hasActive(ctx, userID, characterID)
	if err != nil {
		return false, err
	}
	clog.WithGroupField(ctx, "sessionService.hasActive", "hasActive", hasActive)
	return hasActive, nil
}

func (s service) GetActive(ctx context.Context, userID string, characterID string) (*api.Session, error) {
	clog.WithGroupFields(ctx, "sessionService.getActive", clog.Fields{
		"userId":      userID,
		"characterId": characterID,
	})
	result, err := s.store.getActiveDoc(ctx, userID, characterID)
	if err != nil {
		return nil, err
	}
	if result.AggregateIDs == nil {
		result.AggregateIDs = make([]string, 0)
	}
	clog.WithGroupField(ctx, "sessionService.getActive", "sessionId", result.ID)
	return result, nil
}

func (s service) Get(ctx context.Context, ID string) (*api.Session, error) {
	clog.WithGroupField(ctx, "sessionService.get", "sessionId", ID)
	result, err := s.store.getDoc(ctx, ID)
	if err != nil {
		return nil, err
	}
	if result.AggregateIDs == nil {
		result.AggregateIDs = make([]string, 0)
	}
	return result, nil
}

func (s service) GetAll(ctx context.Context, userID *string, characterID *string, status *api.SessionStatus, count int, offset int) ([]api.Session, error) {
	fields := clog.Fields{
		"count":  count,
		"offset": offset,
	}
	if userID != nil && *userID != "" {
		fields["userId"] = *userID
	}
	if characterID != nil && *characterID != "" {
		fields["characterId"] = *characterID
	}
	if status != nil && *status != "" {
		fields["status"] = string(*status)
	}
	clog.WithGroupFields(ctx, "sessionService.getAll", fields)

	result, err := s.store.listDocs(ctx, userID, characterID, status)
	if err != nil {
		return nil, err
	}

	// Sort by StartedAt descending (newest first)
	slices.SortFunc(result, func(a, b api.Session) int {
		return b.StartedAt.Compare(a.StartedAt)
	})

	for i := range result {
		if result[i].AggregateIDs == nil {
			result[i].AggregateIDs = make([]string, 0)
		}
	}

	// Apply offset
	if offset > 0 {
		if offset >= len(result) {
			clog.WithGroupField(ctx, "sessionService.getAll", "returnedCount", 0)
			return []api.Session{}, nil
		}
		result = result[offset:]
	}

	// Apply limit
	limit := 10
	if count > 0 {
		limit = count
	}
	if limit < len(result) {
		result = result[:limit]
	}

	clog.WithGroupField(ctx, "sessionService.getAll", "returnedCount", len(result))
	return result, nil
}

func (s service) Update(ctx context.Context, sessionID string, name, description string) error {
	updates := make([]firestore.Update, 0)
	if name != "" {
		updates = append(updates, firestore.Update{
			Path:  "name",
			Value: name,
		})
	}
	if description != "" {
		updates = append(updates, firestore.Update{
			Path:  "description",
			Value: description,
		})
	}
	clog.WithGroupField(ctx, "sessionService.update", "sessionId", sessionID)
	if len(updates) == 0 {
		clog.WithGroupField(ctx, "sessionService.update", "warning", "no updates to session")
		return nil
	}
	if err := s.store.updateFields(ctx, sessionID, updates); err != nil {
		return fmt.Errorf("failed to update session: %w", err)
	}

	return nil
}

func (s service) Complete(ctx context.Context, ID string) error {
	clog.WithGroupField(ctx, "sessionService.complete", "sessionId", ID)
	session, err := s.store.getDoc(ctx, ID)
	if err != nil {
		return fmt.Errorf("failed to get session: %w", err)
	}
	if session.CompletedAt == nil {
		completedAt := time.Now()
		if err := s.store.completeDoc(ctx, ID, completedAt); err != nil {
			return fmt.Errorf("failed to complete session: %w", err)
		}
		session.CompletedAt = &completedAt
		session.Status = new(api.SessionComplete)
	} else {
		clog.WithGroupField(ctx, "sessionService.complete", "warning", "session already completed")
	}

	if session.CompTracking != nil && session.CompTracking.Enabled && session.CompTracking.End == nil {
		if err := s.finalizeCompTrackingForSession(ctx, session); err != nil {
			clog.WithGroupFields(ctx, "sessionService.complete", clog.Fields{
				"warning":       "failed to finalize competitive rank",
				"compRankError": err.Error(),
			})
		}
	}

	return nil
}

func (s service) FinalizeCompTracking(ctx context.Context, sessionID string) error {
	clog.WithGroupField(ctx, "sessionService.finalizeCompTracking", "sessionId", sessionID)
	ses, err := s.store.getDoc(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to get session: %w", err)
	}
	return s.finalizeCompTrackingForSession(ctx, ses)
}

func (s service) finalizeCompTrackingForSession(ctx context.Context, ses *api.Session) error {
	clog.WithGroupFields(ctx, "sessionService.finalizeCompTracking", clog.Fields{
		"sessionId": ses.ID,
		"userId":    ses.UserID,
	})

	if ses.CompTracking == nil || !ses.CompTracking.Enabled || ses.CompTracking.End != nil {
		clog.WithGroupField(ctx, "sessionService.finalizeCompTracking", "skipped", true)
		return nil
	}

	if ses.CompletedAt == nil && (ses.Status == nil || *ses.Status != api.SessionComplete) {
		return fmt.Errorf("session %s is not completed", ses.ID)
	}

	if s.userService == nil || s.d2Service == nil {
		return fmt.Errorf("competitive rank services not configured")
	}

	pmID, membershipType, err := s.userService.GetPrimaryMembership(ctx, ses.UserID)
	if err != nil {
		return err
	}

	endSnap, err := s.d2Service.GetCompetitiveRank(ctx, pmID, membershipType, ses.CharacterID)
	if err != nil {
		return fmt.Errorf("failed to fetch ending competitive rank: %w", err)
	}

	var pointDelta *int
	if ses.CompTracking.Start != nil {
		delta := endSnap.Points - ses.CompTracking.Start.Points
		pointDelta = &delta
	}

	if err := s.store.updateCompTrackingEnd(ctx, ses.ID, endSnap, pointDelta); err != nil {
		return fmt.Errorf("failed to persist competitive rank end: %w", err)
	}

	fields := clog.Fields{
		"updated":   true,
		"endPoints": endSnap.Points,
	}
	if pointDelta != nil {
		fields["pointDelta"] = *pointDelta
	}
	clog.WithGroupFields(ctx, "sessionService.finalizeCompTracking", fields)
	return nil
}

func (s service) SetLastActivity(ctx context.Context, ID, activityID string) error {
	clog.WithGroupFields(ctx, "sessionService.setLastActivity", clog.Fields{
		"sessionId":  ID,
		"activityId": activityID,
	})
	if err := s.store.setLastActivity(ctx, ID, activityID, time.Now()); err != nil {
		return fmt.Errorf("failed to update session: %v", err)
	}
	return nil
}

func (s service) Delete(ctx context.Context, sessionID string, userID string) error {
	clog.WithGroupFields(ctx, "sessionService.delete", clog.Fields{
		"sessionId": sessionID,
		"userId":    userID,
	})
	ses, err := s.store.getDoc(ctx, sessionID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			clog.WithGroupField(ctx, "sessionService.delete", "warning", "session not found for deletion")
			return ErrNotFound
		}
		return fmt.Errorf("failed to get session: %w", err)
	}

	if ses.UserID != userID {
		clog.WithGroupFields(ctx, "sessionService.delete", clog.Fields{
			"sessionUserId": ses.UserID,
			"warning":       "unauthorized attempt to delete session",
		})
		return ErrUnauthorized
	}

	// Remove session references from aggregates
	if s.aggregateService != nil {
		if err := s.aggregateService.RemoveSession(ctx, sessionID); err != nil {
			return fmt.Errorf("failed to clean up session aggregates: %w", err)
		}
	}

	// Delete session document
	if err := s.store.deleteDoc(ctx, sessionID); err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	clog.WithGroupField(ctx, "sessionService.delete", "deleted", true)
	return nil
}

func (s service) AddAggregateIDs(ctx context.Context, sessionID string, aggregateIDs []string) error {
	clog.WithGroupFields(ctx, "sessionService.addAggregateIds", clog.Fields{
		"sessionId":      sessionID,
		"aggregateCount": len(aggregateIDs),
	})
	return s.store.addAggregateIDs(ctx, sessionID, aggregateIDs)
}

func ShouldFinalizeComp(ses api.Session) bool {
	return len(ses.AggregateIDs) > 0 &&
		ses.CompTracking != nil &&
		ses.CompTracking.Enabled &&
		ses.CompTracking.End == nil &&
		ses.ID != ""
}

func IsStaleSession(ses api.Session, updatedAt *time.Time, now time.Time) bool {
	if ses.LastSeenTimestamp != nil {
		return now.Sub(*ses.LastSeenTimestamp) >= CutOffDuration
	}
	if updatedAt != nil {
		return now.Sub(*updatedAt) >= CutOffDuration
	}
	return false
}

func IsInactiveSession(ses api.Session, now time.Time) bool {
	return now.Sub(ses.StartedAt) >= CutOffDuration
}

// filterTickActivities mirrors server-tick's private match filter: exclude generic private matches
// unless the activity or map name indicates Iron Banner or Tribute.
func filterTickActivities(activities []api.ActivityHistory) []api.ActivityHistory {
	filtered := make([]api.ActivityHistory, 0, len(activities))
	for _, a := range activities {
		if a.IsPrivate != nil && *a.IsPrivate {
			actLower := strings.ToLower(a.Activity)
			locLower := strings.ToLower(a.Location)
			isTributeOrIB := strings.Contains(actLower, "tribute") ||
				strings.Contains(actLower, "iron banner") ||
				strings.Contains(locLower, "tribute") ||
				strings.Contains(locLower, "iron banner")
			if !isTributeOrIB {
				continue
			}
		}
		filtered = append(filtered, a)
	}
	return filtered
}

func (s service) endOrDeleteSession(ctx context.Context, ses api.Session) error {
	if len(ses.AggregateIDs) == 0 {
		return s.store.deleteDoc(ctx, ses.ID)
	}
	return s.endSession(ctx, ses)
}

func (s service) endSession(ctx context.Context, ses api.Session) error {
	completedBy := api.AuditField{
		ID:       "system",
		Username: "system",
	}
	summary, err := s.ComputeSessionSummary(ctx, ses)
	if err != nil {
		clog.WithGroupFields(ctx, "sessionService.endSession", clog.Fields{
			"warning":      "failed to compute session summary while ending session",
			"sessionId":    ses.ID,
			"summaryError": err.Error(),
		})
		summary = nil
	}
	if err := s.store.endSessionDoc(ctx, ses.ID, completedBy, s.now(), summary); err != nil {
		return fmt.Errorf("failed to end session: %w", err)
	}
	return nil
}

func (s service) CleanupEmptyCompleted(ctx context.Context, dryRun bool) (int, error) {
	completedStatus := api.SessionComplete
	sessions, err := s.store.listDocs(ctx, nil, nil, &completedStatus)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch completed sessions for cleanup: %w", err)
	}

	deletedCount := 0
	rollupErr := clog.LoopRollup(ctx, "sessionService.cleanupEmptyItem", sessions, func(iterCtx context.Context, _ int, ses api.Session) error {
		clog.WithGroupField(iterCtx, "session", "sessionId", ses.ID)
		if len(ses.AggregateIDs) == 0 {
			if !dryRun {
				if err := s.store.deleteDoc(iterCtx, ses.ID); err != nil {
					return fmt.Errorf("failed to delete empty completed session %s: %w", ses.ID, err)
				}
			}
			deletedCount++
		}
		return nil
	})

	clog.WithGroupFields(ctx, "sessionService.cleanupEmpty", clog.Fields{
		"rowsChecked":  len(sessions),
		"deletedCount": deletedCount,
		"dryRun":       dryRun,
	})
	return deletedCount, rollupErr
}

func (s service) RunTick(ctx context.Context, opts TickOptions) (*api.SessionTickResponse, error) {
	tickStart := time.Now()
	now := s.now()

	resp := &api.SessionTickResponse{
		DryRun: opts.DryRun,
	}

	clog.WithGroupFields(ctx, "sessionService.runTick", clog.Fields{
		"dryRun":   opts.DryRun,
		"skipSave": opts.SkipSave,
	})
	defer func() {
		resp.DurationMs = time.Since(tickStart).Milliseconds()
		clog.WithGroupFields(ctx, "sessionService.runTick", clog.Fields{
			"totalSessions":           resp.TotalSessions,
			"sessionsProcessed":       resp.SessionsProcessed,
			"sessionsSkipped":         resp.SessionsSkipped,
			"sessionsStaleEnded":      resp.SessionsStaleEnded,
			"sessionsStaleDeleted":    resp.SessionsStaleDeleted,
			"sessionsInactiveDeleted": resp.SessionsInactiveDeleted,
			"sessionsErrored":         resp.SessionsErrored,
			"loadoutsSaved":           resp.LoadoutsSaved,
			"loadoutsFailed":          resp.LoadoutsFailed,
			"activitiesFoundTotal":    resp.ActivitiesFoundTotal,
			"aggregatesAddedTotal":    resp.AggregatesAddedTotal,
			"compFinalizedCount":      resp.CompFinalizedCount,
			"emptySessionsCleaned":    resp.EmptySessionsCleaned,
			"errorsTotal":             resp.ErrorsTotal,
		})
	}()

	candidates, err := s.store.getTickCandidates(ctx, now.Add(-completedGracePeriod))
	if err != nil {
		resp.ErrorsTotal++
		return nil, fmt.Errorf("failed to get sessions: %w", err)
	}
	resp.TotalSessions = len(candidates)

	var compFinalizeSessionIDs []string
	compFinalizeSeen := make(map[string]struct{})
	queueCompFinalize := func(ses api.Session) {
		if !ShouldFinalizeComp(ses) {
			return
		}
		if _, exists := compFinalizeSeen[ses.ID]; exists {
			return
		}
		compFinalizeSeen[ses.ID] = struct{}{}
		compFinalizeSessionIDs = append(compFinalizeSessionIDs, ses.ID)
	}

	_ = clog.LoopRollup(ctx, "sessionService.processSession", candidates, func(iterCtx context.Context, _ int, cand tickCandidate) error {
		defer func() {
			if bag := clog.BagFromContext(iterCtx); bag != nil && bag.HasWarning() {
				clog.WithGroupField(ctx, "sessionService.processSession", "warning", "one or more sessions recorded a warning")
			}
		}()

		ses := cand.session
		clog.WithGroupFields(iterCtx, "session", clog.Fields{
			"sessionId":   ses.ID,
			"userId":      ses.UserID,
			"characterId": ses.CharacterID,
		})

		isCompleted := ses.Status != nil && *ses.Status == api.SessionComplete
		if isCompleted {
			defer func() {
				queueCompFinalize(ses)
			}()
		}

		if s.userService == nil || s.d2Service == nil {
			resp.SessionsErrored++
			resp.ErrorsTotal++
			return fmt.Errorf("session tick dependencies not configured")
		}

		pmID, membershipType, err := s.userService.GetPrimaryMembership(iterCtx, ses.UserID)
		if err != nil {
			resp.SessionsErrored++
			resp.ErrorsTotal++
			return fmt.Errorf("failed to fetch membership type for session %s: %w", ses.ID, err)
		}
		membershipID := strconv.FormatInt(pmID, 10)

		if !opts.SkipSave && !opts.DryRun {
			if s.snapshotService == nil {
				resp.LoadoutsFailed++
				resp.ErrorsTotal++
				return fmt.Errorf("snapshot service not configured for session %s", ses.ID)
			}
			if _, err := s.snapshotService.Save(iterCtx, ses.UserID, membershipID, ses.CharacterID); err != nil {
				resp.LoadoutsFailed++
				resp.ErrorsTotal++
				return fmt.Errorf("failed to save loadout for session %s: %w", ses.ID, err)
			}
			resp.LoadoutsSaved++
		}

		rawActivities, err := s.d2Service.GetAllPVPActivity(iterCtx, membershipID, membershipType, ses.CharacterID, 5, 0)
		if err != nil {
			resp.SessionsErrored++
			resp.ErrorsTotal++
			return fmt.Errorf("failed to get activities for session %s: %w", ses.ID, err)
		}
		activityHistories := filterTickActivities(rawActivities)

		if !isCompleted && IsStaleSession(ses, cand.updatedAt, now) {
			if !opts.DryRun {
				if err := s.endOrDeleteSession(iterCtx, ses); err != nil {
					resp.SessionsErrored++
					resp.ErrorsTotal++
					return fmt.Errorf("failed to process stale session %s: %w", ses.ID, err)
				}
			}
			if len(ses.AggregateIDs) == 0 {
				resp.SessionsStaleDeleted++
			} else {
				resp.SessionsStaleEnded++
				queueCompFinalize(ses)
			}
			return nil
		}

		if !isCompleted && ses.LastSeenTimestamp == nil && IsInactiveSession(ses, now) {
			if !opts.DryRun {
				if err := s.endOrDeleteSession(iterCtx, ses); err != nil {
					resp.SessionsErrored++
					resp.ErrorsTotal++
					return fmt.Errorf("failed to process inactive session %s: %w", ses.ID, err)
				}
			}
			resp.SessionsInactiveDeleted++
			return nil
		}

		if len(activityHistories) == 0 {
			resp.SessionsSkipped++
			return nil
		}

		latest := activityHistories[0]
		if ses.LastSeenActivityID != nil && *ses.LastSeenActivityID == latest.InstanceID {
			resp.SessionsSkipped++
			return nil
		}

		gracePeriod := ses.StartedAt.Add(-activityStartGracePeriod)
		ids := make([]string, 0, len(activityHistories))
		histories := make([]api.ActivityHistory, 0, len(activityHistories))
		for _, activity := range activityHistories {
			if activity.Period.After(gracePeriod) {
				ids = append(ids, activity.InstanceID)
				histories = append(histories, activity)
			}
		}

		if len(ids) == 0 {
			resp.SessionsSkipped++
			return nil
		}

		resp.ActivitiesFoundTotal += len(ids)

		if s.aggregateService == nil {
			resp.SessionsErrored++
			resp.ErrorsTotal++
			return fmt.Errorf("aggregate service not configured for session %s", ses.ID)
		}

		existingAggs, err := s.aggregateService.GetAggregatesByActivity(iterCtx, ids)
		if err != nil {
			resp.SessionsErrored++
			resp.ErrorsTotal++
			return fmt.Errorf("failed to fetch aggregates for session %s: %w", ses.ID, err)
		}

		existingAggMap := make(map[string]*api.Aggregate, len(existingAggs))
		for i := range existingAggs {
			existingAggMap[existingAggs[i].ActivityID] = &existingAggs[i]
		}

		aggIDs := make([]string, 0, len(histories))
		_ = clog.LoopRollup(iterCtx, "aggregateService.processActivity", histories, func(actCtx context.Context, _ int, history api.ActivityHistory) error {
			defer func() {
				if bag := clog.BagFromContext(actCtx); bag != nil && bag.HasWarning() {
					clog.WithGroupField(iterCtx, "aggregateService.processActivity", "warning", "one or more activities recorded a warning")
				}
			}()
			clog.WithGroupField(actCtx, "activity", "activityId", history.InstanceID)

			existing := existingAggMap[history.InstanceID]
			if existing != nil {
				if link, ok := existing.SnapshotLinks[ses.CharacterID]; ok && link.SessionID != nil {
					return nil
				}
			}

			performances, err := s.d2Service.GetPerformances(actCtx, history.InstanceID, []string{ses.CharacterID})
			if err != nil {
				resp.ErrorsTotal++
				return fmt.Errorf("failed to fetch performances for activity %s: %w", history.InstanceID, err)
			}

			performance, ok := performances[ses.CharacterID]
			if !ok {
				clog.WithGroupFields(actCtx, "activity", clog.Fields{
					"warning":     "missing performance entry for character",
					"warningType": "missing_performance",
					"userId":      ses.UserID,
					"activityId":  history.InstanceID,
				})
				return nil
			}

			if opts.DryRun {
				aggIDs = append(aggIDs, history.InstanceID)
				return nil
			}

			if s.snapshotService == nil {
				resp.ErrorsTotal++
				return fmt.Errorf("snapshot service not configured for activity %s", history.InstanceID)
			}

			snap, link, err := s.snapshotService.FindBestFit(actCtx, ses.UserID, ses.CharacterID, history.Period, performance.Weapons)
			if err != nil {
				resp.ErrorsTotal++
				return fmt.Errorf("failed to find best-fit snapshot for activity %s: %w", history.InstanceID, err)
			}

			enrichedPerformance, err := s.snapshotService.EnrichInstancePerformance(snap, performance)
			if err != nil {
				resp.ErrorsTotal++
				return fmt.Errorf("failed to enrich performance instance for activity %s: %w", history.InstanceID, err)
			}

			sessionID := ses.ID
			link.SessionID = &sessionID

			agg, err := s.aggregateService.UpsertSessionActivity(actCtx, ses.CharacterID, history, *link, *enrichedPerformance)
			if err != nil {
				resp.ErrorsTotal++
				return fmt.Errorf("failed to add data to aggregate for activity %s: %w", history.InstanceID, err)
			}
			aggIDs = append(aggIDs, agg.ID)
			return nil
		})

		if len(aggIDs) == 0 {
			resp.SessionsSkipped++
			return nil
		}

		if opts.DryRun {
			ses.AggregateIDs = append(ses.AggregateIDs, aggIDs...)
			resp.SessionsProcessed++
			resp.AggregatesAddedTotal += len(aggIDs)
			return nil
		}

		var postErrs []error
		if !isCompleted {
			if err := s.store.setLastActivity(iterCtx, ses.ID, latest.InstanceID, s.now()); err != nil {
				resp.ErrorsTotal++
				postErrs = append(postErrs, fmt.Errorf("failed to save last activity for session %s: %w", ses.ID, err))
			}
		}

		if err := s.AddAggregateIDs(iterCtx, ses.ID, aggIDs); err != nil {
			resp.SessionsErrored++
			resp.ErrorsTotal++
			return errors.Join(append(postErrs, fmt.Errorf("failed to add aggregate IDs to session %s: %w", ses.ID, err))...)
		}

		ses.AggregateIDs = append(ses.AggregateIDs, aggIDs...)
		summary, err := s.ComputeSessionSummary(iterCtx, ses)
		if err != nil {
			resp.ErrorsTotal++
			postErrs = append(postErrs, fmt.Errorf("failed to compute session summary for session %s: %w", ses.ID, err))
		} else if err := s.UpdateSessionSummary(iterCtx, ses.ID, summary); err != nil {
			resp.ErrorsTotal++
			postErrs = append(postErrs, fmt.Errorf("failed to update session summary for session %s: %w", ses.ID, err))
		}

		resp.SessionsProcessed++
		resp.AggregatesAddedTotal += len(aggIDs)
		return errors.Join(postErrs...)
	})

	if len(compFinalizeSessionIDs) > 0 {
		if opts.DryRun {
			clog.WithGroupFields(ctx, "sessionService.finalizeCompBatch", clog.Fields{
				"sessionCount": len(compFinalizeSessionIDs),
				"dryRun":       true,
			})
		} else {
			_ = clog.LoopRollup(ctx, "sessionService.finalizeCompItem", compFinalizeSessionIDs, func(iterCtx context.Context, _ int, sessionID string) error {
				if err := s.FinalizeCompTracking(iterCtx, sessionID); err != nil {
					resp.ErrorsTotal++
					return err
				}
				resp.CompFinalizedCount++
				return nil
			})
		}
	}

	cleaned, err := s.CleanupEmptyCompleted(ctx, opts.DryRun)
	resp.EmptySessionsCleaned = cleaned
	if err != nil {
		resp.ErrorsTotal++
	}

	return resp, nil
}
