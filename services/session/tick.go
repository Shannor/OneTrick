package session

import (
	"context"
	"errors"
	"fmt"
	"oneTrick/api"
	"oneTrick/set"
	"strconv"
	"strings"
	"time"

	clog "github.com/Shannor/canonical-logger"
)

type TickOptions struct {
	DryRun   bool
	SkipSave bool
}

func (c tickCandidate) isCompleted() bool {
	return c.session.Status != nil && *c.session.Status == api.SessionComplete
}

func (c tickCandidate) isStale(now time.Time) bool {
	return !c.isCompleted() && IsStaleSession(c.session, c.updatedAt, now)
}

func (c tickCandidate) isInactive(now time.Time) bool {
	return !c.isCompleted() && c.session.LastSeenTimestamp == nil && IsInactiveSession(c.session, now)
}

func (c tickCandidate) hasAggregates() bool {
	return len(c.session.AggregateIDs) > 0
}

func (c tickCandidate) hasSeenActivity(activityID string) bool {
	return c.session.LastSeenActivityID != nil && *c.session.LastSeenActivityID == activityID
}

func (c tickCandidate) activityGraceCutoff() time.Time {
	return c.session.StartedAt.Add(-activityStartGracePeriod)
}

type tickMembership struct {
	id             string
	membershipType int64
}

type tickRunner struct {
	svc       service
	opts      TickOptions
	now       time.Time
	resp      *api.SessionTickResponse
	compQueue *set.OrderedSet[string]
}

func newTickRunner(svc service, opts TickOptions) *tickRunner {
	return &tickRunner{
		svc:       svc,
		opts:      opts,
		now:       svc.now(),
		resp:      &api.SessionTickResponse{DryRun: opts.DryRun},
		compQueue: set.NewOrdered[string](),
	}
}

func (s service) RunTick(ctx context.Context, opts TickOptions) (*api.SessionTickResponse, error) {
	tickStart := time.Now()
	runner := newTickRunner(s, opts)

	clog.WithGroupFields(ctx, "sessionService.runTick", clog.Fields{
		"dryRun":   opts.DryRun,
		"skipSave": opts.SkipSave,
	})
	defer runner.recordSummary(ctx, tickStart)

	return runner.run(ctx)
}

func (r *tickRunner) recordSummary(ctx context.Context, tickStart time.Time) {
	r.resp.DurationMs = time.Since(tickStart).Milliseconds()
	clog.WithGroupFields(ctx, "sessionService.runTick", clog.Fields{
		"totalSessions":           r.resp.TotalSessions,
		"sessionsProcessed":       r.resp.SessionsProcessed,
		"sessionsSkipped":         r.resp.SessionsSkipped,
		"sessionsStaleEnded":      r.resp.SessionsStaleEnded,
		"sessionsStaleDeleted":    r.resp.SessionsStaleDeleted,
		"sessionsInactiveDeleted": r.resp.SessionsInactiveDeleted,
		"sessionsErrored":         r.resp.SessionsErrored,
		"loadoutsSaved":           r.resp.LoadoutsSaved,
		"loadoutsFailed":          r.resp.LoadoutsFailed,
		"activitiesFoundTotal":    r.resp.ActivitiesFoundTotal,
		"aggregatesAddedTotal":    r.resp.AggregatesAddedTotal,
		"compFinalizedCount":      r.resp.CompFinalizedCount,
		"emptySessionsCleaned":    r.resp.EmptySessionsCleaned,
		"errorsTotal":             r.resp.ErrorsTotal,
	})
}

func (r *tickRunner) run(ctx context.Context) (*api.SessionTickResponse, error) {
	candidates, err := r.svc.store.getTickCandidates(ctx, r.now.Add(-completedGracePeriod))
	if err != nil {
		r.resp.ErrorsTotal++
		return nil, fmt.Errorf("failed to get sessions: %w", err)
	}
	r.resp.TotalSessions = len(candidates)

	r.processCandidates(ctx, candidates)
	r.finalizeCompSessions(ctx)
	r.cleanupEmptyCompleted(ctx)

	return r.resp, nil
}

func (r *tickRunner) queueCompFinalize(ses api.Session) {
	if ShouldFinalizeComp(ses) {
		r.compQueue.Add(ses.ID)
	}
}

func (r *tickRunner) failSession(err error) error {
	r.resp.SessionsErrored++
	r.resp.ErrorsTotal++
	return err
}

func (r *tickRunner) processCandidates(ctx context.Context, candidates []tickCandidate) {
	_ = clog.LoopRollup(ctx, "sessionService.processSession", candidates, func(iterCtx context.Context, _ int, cand tickCandidate) error {
		return r.processSession(iterCtx, ctx, cand)
	})
}

func (r *tickRunner) processSession(iterCtx, parentCtx context.Context, cand tickCandidate) error {
	defer func() {
		if bag := clog.BagFromContext(iterCtx); bag != nil && bag.HasWarning() {
			clog.WithGroupField(parentCtx, "sessionService.processSession", "warning", "one or more sessions recorded a warning")
		}
	}()

	clog.WithGroupFields(iterCtx, "session", clog.Fields{
		"sessionId":   cand.session.ID,
		"userId":      cand.session.UserID,
		"characterId": cand.session.CharacterID,
	})

	if cand.isCompleted() {
		defer func() {
			r.queueCompFinalize(cand.session)
		}()
	}

	if handled, err := r.expireIfNeeded(iterCtx, cand); handled || err != nil {
		return err
	}

	membership, err := r.resolveMembership(iterCtx, cand.session)
	if err != nil {
		return err
	}

	if err := r.saveLoadoutIfNeeded(iterCtx, cand, membership); err != nil {
		return err
	}

	latestID, histories, err := r.fetchNewActivities(iterCtx, cand, membership)
	if err != nil {
		return err
	}
	if len(histories) == 0 {
		r.resp.SessionsSkipped++
		return nil
	}

	r.resp.ActivitiesFoundTotal += len(histories)

	aggIDs, err := r.linkActivities(iterCtx, cand.session, histories)
	if err != nil {
		return err
	}
	if len(aggIDs) == 0 {
		r.resp.SessionsSkipped++
		return nil
	}

	return r.persistSessionProgress(iterCtx, &cand, latestID, aggIDs)
}

func (r *tickRunner) expireIfNeeded(ctx context.Context, cand tickCandidate) (bool, error) {
	var (
		reason       string
		emptyCounter *int
	)
	switch {
	case cand.isStale(r.now):
		reason = "stale"
		emptyCounter = &r.resp.SessionsStaleDeleted
	case cand.isInactive(r.now):
		reason = "inactive"
		emptyCounter = &r.resp.SessionsInactiveDeleted
	default:
		return false, nil
	}

	if !r.opts.DryRun {
		if err := r.svc.endOrDeleteSession(ctx, cand.session); err != nil {
			return true, r.failSession(fmt.Errorf("failed to process %s session %s: %w", reason, cand.session.ID, err))
		}
	}

	if !cand.hasAggregates() {
		*emptyCounter++
	} else {
		r.resp.SessionsStaleEnded++
		r.queueCompFinalize(cand.session)
	}
	return true, nil
}

func (r *tickRunner) resolveMembership(ctx context.Context, ses api.Session) (tickMembership, error) {
	if r.svc.userService == nil || r.svc.d2Service == nil {
		return tickMembership{}, r.failSession(fmt.Errorf("session tick dependencies not configured"))
	}

	pmID, membershipType, err := r.svc.userService.GetPrimaryMembership(ctx, ses.UserID)
	if err != nil {
		return tickMembership{}, r.failSession(fmt.Errorf("failed to fetch membership type for session %s: %w", ses.ID, err))
	}
	return tickMembership{
		id:             strconv.FormatInt(pmID, 10),
		membershipType: membershipType,
	}, nil
}

func (r *tickRunner) saveLoadoutIfNeeded(ctx context.Context, cand tickCandidate, membership tickMembership) error {
	if cand.isCompleted() || r.opts.SkipSave || r.opts.DryRun {
		return nil
	}
	if r.svc.snapshotService == nil {
		r.resp.LoadoutsFailed++
		r.resp.ErrorsTotal++
		return fmt.Errorf("snapshot service not configured for session %s", cand.session.ID)
	}
	if _, err := r.svc.snapshotService.Save(ctx, cand.session.UserID, membership.id, cand.session.CharacterID); err != nil {
		r.resp.LoadoutsFailed++
		r.resp.ErrorsTotal++
		return fmt.Errorf("failed to save loadout for session %s: %w", cand.session.ID, err)
	}
	r.resp.LoadoutsSaved++
	return nil
}

func (r *tickRunner) fetchNewActivities(ctx context.Context, cand tickCandidate, membership tickMembership) (string, []api.ActivityHistory, error) {
	rawActivities, err := r.svc.d2Service.GetAllPVPActivity(ctx, membership.id, membership.membershipType, cand.session.CharacterID, 5, 0)
	if err != nil {
		return "", nil, r.failSession(fmt.Errorf("failed to get activities for session %s: %w", cand.session.ID, err))
	}

	activityHistories := filterTickActivities(rawActivities)
	if len(activityHistories) == 0 {
		return "", nil, nil
	}

	latestID := activityHistories[0].InstanceID
	if cand.hasSeenActivity(latestID) {
		return "", nil, nil
	}

	graceCutoff := cand.activityGraceCutoff()
	histories := make([]api.ActivityHistory, 0, len(activityHistories))
	for _, activity := range activityHistories {
		if activity.Period.After(graceCutoff) {
			histories = append(histories, activity)
		}
	}
	return latestID, histories, nil
}

func (r *tickRunner) linkActivities(iterCtx context.Context, ses api.Session, histories []api.ActivityHistory) ([]string, error) {
	if r.svc.aggregateService == nil {
		return nil, r.failSession(fmt.Errorf("aggregate service not configured for session %s", ses.ID))
	}

	ids := make([]string, len(histories))
	for i, h := range histories {
		ids[i] = h.InstanceID
	}

	existingAggs, err := r.svc.aggregateService.GetAggregatesByActivity(iterCtx, ids)
	if err != nil {
		return nil, r.failSession(fmt.Errorf("failed to fetch aggregates for session %s: %w", ses.ID, err))
	}

	existingByActivity := make(map[string]*api.Aggregate, len(existingAggs))
	for i := range existingAggs {
		existingByActivity[existingAggs[i].ActivityID] = &existingAggs[i]
	}

	aggIDs := make([]string, 0, len(histories))
	_ = clog.LoopRollup(iterCtx, "aggregateService.processActivity", histories, func(actCtx context.Context, _ int, history api.ActivityHistory) error {
		defer func() {
			if bag := clog.BagFromContext(actCtx); bag != nil && bag.HasWarning() {
				clog.WithGroupField(iterCtx, "aggregateService.processActivity", "warning", "one or more activities recorded a warning")
			}
		}()

		aggID, linked, err := r.linkSingleActivity(actCtx, ses, history, existingByActivity[history.InstanceID])
		if err != nil {
			r.resp.ErrorsTotal++
			return err
		}
		if linked {
			aggIDs = append(aggIDs, aggID)
		}
		return nil
	})

	return aggIDs, nil
}

func (r *tickRunner) linkSingleActivity(actCtx context.Context, ses api.Session, history api.ActivityHistory, existing *api.Aggregate) (string, bool, error) {
	clog.WithGroupField(actCtx, "activity", "activityId", history.InstanceID)

	if hasSessionLink(existing, ses.CharacterID) {
		return "", false, nil
	}

	performances, err := r.svc.d2Service.GetPerformances(actCtx, history.InstanceID, []string{ses.CharacterID})
	if err != nil {
		return "", false, fmt.Errorf("failed to fetch performances for activity %s: %w", history.InstanceID, err)
	}

	performance, ok := performances[ses.CharacterID]
	if !ok {
		clog.WithGroupFields(actCtx, "activity", clog.Fields{
			"warning":     "missing performance entry for character",
			"warningType": "missing_performance",
			"userId":      ses.UserID,
			"activityId":  history.InstanceID,
		})
		return "", false, nil
	}

	if r.opts.DryRun {
		return history.InstanceID, true, nil
	}

	if r.svc.snapshotService == nil {
		return "", false, fmt.Errorf("snapshot service not configured for activity %s", history.InstanceID)
	}

	snap, link, err := r.svc.snapshotService.FindBestFit(actCtx, ses.UserID, ses.CharacterID, history.Period, performance.Weapons)
	if err != nil {
		return "", false, fmt.Errorf("failed to find best-fit snapshot for activity %s: %w", history.InstanceID, err)
	}

	enrichedPerformance, err := r.svc.snapshotService.EnrichInstancePerformance(snap, performance)
	if err != nil {
		return "", false, fmt.Errorf("failed to enrich performance instance for activity %s: %w", history.InstanceID, err)
	}

	sessionID := ses.ID
	link.SessionID = &sessionID

	agg, err := r.svc.aggregateService.UpsertSessionActivity(actCtx, ses.CharacterID, history, *link, *enrichedPerformance)
	if err != nil {
		return "", false, fmt.Errorf("failed to add data to aggregate for activity %s: %w", history.InstanceID, err)
	}
	return agg.ID, true, nil
}

func (r *tickRunner) persistSessionProgress(iterCtx context.Context, cand *tickCandidate, latestID string, aggIDs []string) error {
	ses := &cand.session
	if r.opts.DryRun {
		ses.AggregateIDs = append(ses.AggregateIDs, aggIDs...)
		r.resp.SessionsProcessed++
		r.resp.AggregatesAddedTotal += len(aggIDs)
		return nil
	}

	var postErrs []error
	if !cand.isCompleted() {
		if err := r.svc.store.setLastActivity(iterCtx, ses.ID, latestID, r.svc.now()); err != nil {
			r.resp.ErrorsTotal++
			postErrs = append(postErrs, fmt.Errorf("failed to save last activity for session %s: %w", ses.ID, err))
		}
	}

	if err := r.svc.AddAggregateIDs(iterCtx, ses.ID, aggIDs); err != nil {
		r.resp.SessionsErrored++
		r.resp.ErrorsTotal++
		return errors.Join(append(postErrs, fmt.Errorf("failed to add aggregate IDs to session %s: %w", ses.ID, err))...)
	}

	ses.AggregateIDs = append(ses.AggregateIDs, aggIDs...)
	summary, err := r.svc.ComputeSessionSummary(iterCtx, *ses)
	if err != nil {
		r.resp.ErrorsTotal++
		postErrs = append(postErrs, fmt.Errorf("failed to compute session summary for session %s: %w", ses.ID, err))
	} else if err := r.svc.UpdateSessionSummary(iterCtx, ses.ID, summary); err != nil {
		r.resp.ErrorsTotal++
		postErrs = append(postErrs, fmt.Errorf("failed to update session summary for session %s: %w", ses.ID, err))
	}

	r.resp.SessionsProcessed++
	r.resp.AggregatesAddedTotal += len(aggIDs)
	return errors.Join(postErrs...)
}

func (r *tickRunner) finalizeCompSessions(ctx context.Context) {
	sessionIDs := r.compQueue.ToSlice()
	if len(sessionIDs) == 0 {
		return
	}
	if r.opts.DryRun {
		clog.WithGroupFields(ctx, "sessionService.finalizeCompBatch", clog.Fields{
			"sessionCount": len(sessionIDs),
			"dryRun":       true,
		})
		return
	}

	_ = clog.LoopRollup(ctx, "sessionService.finalizeCompItem", sessionIDs, func(iterCtx context.Context, _ int, sessionID string) error {
		if err := r.svc.FinalizeCompTracking(iterCtx, sessionID); err != nil {
			r.resp.ErrorsTotal++
			return err
		}
		r.resp.CompFinalizedCount++
		return nil
	})
}

func (r *tickRunner) cleanupEmptyCompleted(ctx context.Context) {
	cleaned, err := r.svc.CleanupEmptyCompleted(ctx, r.opts.DryRun)
	r.resp.EmptySessionsCleaned = cleaned
	if err != nil {
		r.resp.ErrorsTotal++
	}
}

func hasSessionLink(agg *api.Aggregate, characterID string) bool {
	if agg == nil {
		return false
	}
	link, ok := agg.SnapshotLinks[characterID]
	return ok && link.SessionID != nil
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

	graceCutoff := s.now().Add(-completedGracePeriod)
	deletedCount := 0
	rollupErr := clog.LoopRollup(ctx, "sessionService.cleanupEmptyItem", sessions, func(iterCtx context.Context, _ int, ses api.Session) error {
		clog.WithGroupField(iterCtx, "session", "sessionId", ses.ID)
		if len(ses.AggregateIDs) == 0 {
			if ses.CompletedAt != nil && ses.CompletedAt.After(graceCutoff) {
				return nil
			}
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
