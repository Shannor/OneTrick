package session

import (
	"context"
	"errors"
	"fmt"
	"oneTrick/api"
	"oneTrick/services/snapshot"
	"oneTrick/services/user"
	"oneTrick/set"
	"oneTrick/utils"
	"slices"
	"strconv"
	"strings"
	"time"

	clog "github.com/Shannor/canonical-logger"
)

type TickOptions struct {
	DryRun   bool
	SkipSave bool
}

func (c TickCandidate) isCompleted() bool {
	return c.Session.Status != nil && *c.Session.Status == api.SessionComplete
}

func (c TickCandidate) isStale(now time.Time) bool {
	return !c.isCompleted() && IsStaleSession(c.Session, c.UpdatedAt, now)
}

func (c TickCandidate) isInactive(now time.Time) bool {
	return !c.isCompleted() && c.Session.LastSeenTimestamp == nil && IsInactiveSession(c.Session, now)
}

func (c TickCandidate) hasAggregates() bool {
	return len(c.Session.AggregateIDs) > 0
}

func (c TickCandidate) hasSeenActivity(activityID string) bool {
	return c.Session.LastSeenActivityID != nil && *c.Session.LastSeenActivityID == activityID
}

func (c TickCandidate) activityGraceCutoff() time.Time {
	return c.Session.StartedAt.Add(-activityStartGracePeriod)
}

type tickParticipant struct {
	userID         string
	displayName    string
	characterClass string
	emblemURL      string
}

type tickMembership struct {
	id             string
	membershipType int64
	participant    tickParticipant
}

type tickRunner struct {
	svc                   service
	opts                  TickOptions
	now                   time.Time
	resp                  *api.SessionTickResponse
	compQueue             *set.OrderedSet[string]
	usersByID             map[string]*user.User
	pgcrCache             map[string]map[string]api.InstancePerformance
	candidateCharacterIDs []string
	loadoutsThrottled     int
	aggregatesRefreshed   int
	pgcrCacheHits         int
}

func newTickRunner(svc service, opts TickOptions) *tickRunner {
	return &tickRunner{
		svc:       svc,
		opts:      opts,
		now:       svc.now(),
		resp:      &api.SessionTickResponse{DryRun: opts.DryRun},
		compQueue: set.NewOrdered[string](),
		usersByID: make(map[string]*user.User),
		pgcrCache: make(map[string]map[string]api.InstancePerformance),
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
		"loadoutsThrottled":       r.loadoutsThrottled,
		"loadoutsFailed":          r.resp.LoadoutsFailed,
		"activitiesFoundTotal":    r.resp.ActivitiesFoundTotal,
		"aggregatesAddedTotal":    r.resp.AggregatesAddedTotal,
		"aggregatesRefreshed":     r.aggregatesRefreshed,
		"pgcrCacheHits":           r.pgcrCacheHits,
		"compFinalizedCount":      r.resp.CompFinalizedCount,
		"emptySessionsCleaned":    r.resp.EmptySessionsCleaned,
		"errorsTotal":             r.resp.ErrorsTotal,
	})
}

func (r *tickRunner) run(ctx context.Context) (*api.SessionTickResponse, error) {
	candidates, err := r.svc.store.GetTickCandidates(ctx, r.now.Add(-completedGracePeriod))
	if err != nil {
		r.resp.ErrorsTotal++
		return nil, fmt.Errorf("failed to get sessions: %w", err)
	}
	r.resp.TotalSessions = len(candidates)
	r.candidateCharacterIDs = collectCandidateCharacterIDs(candidates, r.now)

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

func (r *tickRunner) processCandidates(ctx context.Context, candidates []TickCandidate) {
	_ = clog.LoopRollup(ctx, "sessionService.processSession", candidates, func(iterCtx context.Context, _ int, cand TickCandidate) error {
		return r.processSession(iterCtx, ctx, cand)
	})
}

func (r *tickRunner) processSession(iterCtx, parentCtx context.Context, cand TickCandidate) error {
	defer func() {
		if bag := clog.BagFromContext(iterCtx); bag != nil && bag.HasWarning() {
			clog.WithGroupField(parentCtx, "sessionService.processSession", "warning", "one or more sessions recorded a warning")
		}
	}()

	clog.WithGroupFields(iterCtx, "session", clog.Fields{
		"sessionId":   cand.Session.ID,
		"userId":      cand.Session.UserID,
		"characterId": cand.Session.CharacterID,
	})

	if cand.isCompleted() {
		defer func() {
			r.queueCompFinalize(cand.Session)
		}()
	}

	if handled, err := r.expireIfNeeded(iterCtx, cand); handled || err != nil {
		return err
	}

	membership, err := r.resolveMembership(iterCtx, cand.Session)
	if err != nil {
		return err
	}

	if err := r.saveLoadoutIfNeeded(iterCtx, &cand, membership); err != nil {
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

	linkRes, err := r.linkActivities(iterCtx, cand.Session, membership.participant, histories)
	if err != nil {
		return err
	}
	if len(linkRes.aggregateIDs) == 0 {
		if !linkRes.hasPendingRetry && !cand.isCompleted() && !r.opts.DryRun && latestID != "" && !cand.hasSeenActivity(latestID) {
			if err := r.svc.store.SetLastActivity(iterCtx, cand.Session.ID, latestID, r.svc.now()); err != nil {
				r.resp.ErrorsTotal++
				return fmt.Errorf("failed to save last activity for session %s: %w", cand.Session.ID, err)
			}
		}
		r.resp.SessionsSkipped++
		return nil
	}

	return r.persistSessionProgress(iterCtx, &cand, latestID, linkRes)
}

func (r *tickRunner) expireIfNeeded(ctx context.Context, cand TickCandidate) (bool, error) {
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
		if err := r.svc.endOrDeleteSession(ctx, cand.Session); err != nil {
			return true, r.failSession(fmt.Errorf("failed to process %s session %s: %w", reason, cand.Session.ID, err))
		}
	}

	if !cand.hasAggregates() {
		*emptyCounter++
	} else {
		r.resp.SessionsStaleEnded++
		r.queueCompFinalize(cand.Session)
	}
	return true, nil
}

func (r *tickRunner) getUser(ctx context.Context, userID string) (*user.User, error) {
	if userID == "" {
		return nil, user.NotFound
	}
	if cached, ok := r.usersByID[userID]; ok {
		return cached, nil
	}
	u, err := r.svc.userService.GetUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, user.NotFound
	}
	r.usersByID[userID] = u
	if u.ID != "" {
		r.usersByID[u.ID] = u
	}
	return u, nil
}

func (r *tickRunner) resolveMembership(ctx context.Context, ses api.Session) (tickMembership, error) {
	if r.svc.userService == nil || r.svc.d2Service == nil {
		return tickMembership{}, r.failSession(fmt.Errorf("session tick dependencies not configured"))
	}

	u, err := r.getUser(ctx, ses.UserID)
	if err != nil {
		return tickMembership{}, r.failSession(fmt.Errorf("failed to fetch user for session %s: %w", ses.ID, err))
	}
	pmID, membershipType, err := u.PrimaryMembership()
	if err != nil {
		return tickMembership{}, r.failSession(fmt.Errorf("failed to fetch membership type for session %s: %w", ses.ID, err))
	}
	return tickMembership{
		id:             strconv.FormatInt(pmID, 10),
		membershipType: membershipType,
		participant:    buildTickParticipant(ses.UserID, ses.CharacterID, u),
	}, nil
}

func (r *tickRunner) saveLoadoutIfNeeded(ctx context.Context, cand *TickCandidate, membership tickMembership) error {
	if cand.isCompleted() || r.opts.SkipSave || r.opts.DryRun {
		return nil
	}
	if r.svc.snapshotService == nil {
		r.resp.LoadoutsFailed++
		r.resp.ErrorsTotal++
		return fmt.Errorf("snapshot service not configured for session %s", cand.Session.ID)
	}
	res, err := r.svc.snapshotService.SaveSessionCheckIn(ctx, cand.Session.UserID, membership.id, cand.Session.CharacterID, snapshot.SessionCheckInOptions{
		MembershipType:         membership.membershipType,
		LastSnapshotID:         cand.LastSnapshotID,
		LastSnapshotHash:       cand.LastSnapshotHash,
		LastSnapshotRecordedAt: cand.LastSnapshotRecordedAt,
		HeartbeatInterval:      snapshotHeartbeatPeriod,
	})
	if err != nil {
		r.resp.LoadoutsFailed++
		r.resp.ErrorsTotal++
		return fmt.Errorf("failed to save loadout for session %s: %w", cand.Session.ID, err)
	}
	if !res.HistoryRecorded {
		r.loadoutsThrottled++
		return nil
	}
	if err := r.svc.store.SetLastSnapshot(ctx, cand.Session.ID, res.SnapshotID, res.SnapshotHash, res.RecordedAt); err != nil {
		clog.WithGroupFields(ctx, "sessionService.saveLoadout", clog.Fields{
			"warning":           "failed to persist last snapshot metadata on session",
			"sessionId":         cand.Session.ID,
			"lastSnapshotError": err.Error(),
		})
	} else {
		cand.LastSnapshotID = res.SnapshotID
		cand.LastSnapshotHash = res.SnapshotHash
		cand.LastSnapshotRecordedAt = res.RecordedAt
	}
	r.resp.LoadoutsSaved++
	return nil
}

func (r *tickRunner) fetchNewActivities(ctx context.Context, cand TickCandidate, membership tickMembership) (string, []api.ActivityHistory, error) {
	rawActivities, err := r.svc.d2Service.GetAllPVPActivity(ctx, membership.id, membership.membershipType, cand.Session.CharacterID, 5, 0)
	if err != nil {
		return "", nil, r.failSession(fmt.Errorf("failed to get activities for session %s: %w", cand.Session.ID, err))
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

type linkActivitiesResult struct {
	aggregateIDs    []string
	refreshedCount  int
	hasPendingRetry bool
}

type activityLinkOutcome struct {
	aggregateID  string
	linked       bool
	refreshed    bool
	pendingRetry bool
}

func (r *tickRunner) linkActivities(iterCtx context.Context, ses api.Session, participant tickParticipant, histories []api.ActivityHistory) (linkActivitiesResult, error) {
	if r.svc.aggregateService == nil {
		return linkActivitiesResult{}, r.failSession(fmt.Errorf("aggregate service not configured for session %s", ses.ID))
	}

	ids := make([]string, len(histories))
	for i, h := range histories {
		ids[i] = h.InstanceID
	}

	existingAggs, err := r.svc.aggregateService.GetAggregatesByActivity(iterCtx, ids)
	if err != nil {
		return linkActivitiesResult{}, r.failSession(fmt.Errorf("failed to fetch aggregates for session %s: %w", ses.ID, err))
	}

	existingByActivity := make(map[string]*api.Aggregate, len(existingAggs))
	for i := range existingAggs {
		existingByActivity[existingAggs[i].ActivityID] = &existingAggs[i]
	}

	res := linkActivitiesResult{
		aggregateIDs: make([]string, 0, len(histories)),
	}
	_ = clog.LoopRollup(iterCtx, "aggregateService.processActivity", histories, func(actCtx context.Context, _ int, history api.ActivityHistory) error {
		defer func() {
			if bag := clog.BagFromContext(actCtx); bag != nil && bag.HasWarning() {
				clog.WithGroupField(iterCtx, "aggregateService.processActivity", "warning", "one or more activities recorded a warning")
			}
		}()

		outcome, err := r.linkSingleActivity(actCtx, ses, participant, history, existingByActivity[history.InstanceID])
		if err != nil {
			r.resp.ErrorsTotal++
			res.hasPendingRetry = true
			return err
		}
		if outcome.pendingRetry {
			res.hasPendingRetry = true
		}
		if outcome.linked {
			res.aggregateIDs = append(res.aggregateIDs, outcome.aggregateID)
			if outcome.refreshed {
				res.refreshedCount++
			}
		}
		return nil
	})

	return res, nil
}

func (r *tickRunner) getPerformances(ctx context.Context, activityID string) (map[string]api.InstancePerformance, error) {
	if cached, ok := r.pgcrCache[activityID]; ok {
		r.pgcrCacheHits++
		return cached, nil
	}
	performances, err := r.svc.d2Service.GetPerformances(ctx, activityID, r.candidateCharacterIDs)
	if err != nil {
		return nil, err
	}
	r.pgcrCache[activityID] = performances
	return performances, nil
}

func (r *tickRunner) linkSingleActivity(actCtx context.Context, ses api.Session, participant tickParticipant, history api.ActivityHistory, existing *api.Aggregate) (activityLinkOutcome, error) {
	clog.WithGroupField(actCtx, "activity", "activityId", history.InstanceID)

	refreshing := false
	if hasSessionLink(existing, ses.CharacterID) {
		if !needsPerformanceRefresh(existing, ses.CharacterID, r.now) {
			return activityLinkOutcome{}, nil
		}
		refreshing = true
	}

	performances, err := r.getPerformances(actCtx, history.InstanceID)
	if err != nil {
		return activityLinkOutcome{}, fmt.Errorf("failed to fetch performances for activity %s: %w", history.InstanceID, err)
	}

	performance, ok := performances[ses.CharacterID]
	if !ok {
		clog.WithGroupFields(actCtx, "activity", clog.Fields{
			"warning":     "missing performance entry for character",
			"warningType": "missing_performance",
			"userId":      ses.UserID,
			"activityId":  history.InstanceID,
		})
		return activityLinkOutcome{pendingRetry: true}, nil
	}

	// Bungie occasionally serves PGCRs with entry.Values populated before entry.Extended
	// (weapons/medals) is ready. Persist on first sight so matches are not dropped, then
	// retry on subsequent ticks until extended data arrives or the retry window expires.
	incomplete := isIncompletePerformance(performance)
	if incomplete {
		clog.WithGroupFields(actCtx, "activity", clog.Fields{
			"warning":     "activity performance missing extended stats, will retry on next tick",
			"warningType": "incomplete_pgcr_extended",
			"userId":      ses.UserID,
			"activityId":  history.InstanceID,
			"refreshing":  refreshing,
		})
		if refreshing {
			return activityLinkOutcome{pendingRetry: true}, nil
		}
	}

	if r.opts.DryRun {
		if refreshing {
			return activityLinkOutcome{aggregateID: existing.ID, linked: true, refreshed: true}, nil
		}
		return activityLinkOutcome{aggregateID: history.InstanceID, linked: true, pendingRetry: incomplete}, nil
	}

	if r.svc.snapshotService == nil {
		return activityLinkOutcome{}, fmt.Errorf("snapshot service not configured for activity %s", history.InstanceID)
	}

	snap, link, err := r.svc.snapshotService.FindBestFit(actCtx, ses.UserID, ses.CharacterID, history.Period, performance.Weapons)
	if err != nil {
		return activityLinkOutcome{}, fmt.Errorf("failed to find best-fit snapshot for activity %s: %w", history.InstanceID, err)
	}

	enrichedPerformance, err := r.svc.snapshotService.EnrichInstancePerformance(snap, performance)
	if err != nil {
		return activityLinkOutcome{}, fmt.Errorf("failed to enrich performance instance for activity %s: %w", history.InstanceID, err)
	}

	stampedLink := stampParticipantMetadata(*link, ses.ID, participant)

	agg, err := r.svc.aggregateService.UpsertSessionActivity(actCtx, ses.CharacterID, history, stampedLink, *enrichedPerformance)
	if err != nil {
		return activityLinkOutcome{}, fmt.Errorf("failed to add data to aggregate for activity %s: %w", history.InstanceID, err)
	}
	if refreshing {
		clog.WithGroupFields(actCtx, "activity", clog.Fields{
			"refreshed":       true,
			"aggregateId":     agg.ID,
			"confidenceLevel": string(stampedLink.ConfidenceLevel),
		})
	}
	return activityLinkOutcome{
		aggregateID:  agg.ID,
		linked:       true,
		refreshed:    refreshing,
		pendingRetry: incomplete,
	}, nil
}

func collectCandidateCharacterIDs(candidates []TickCandidate, now time.Time) []string {
	charIDs := set.NewOrdered[string]()
	for _, cand := range candidates {
		if cand.isStale(now) || cand.isInactive(now) {
			continue
		}
		if cand.Session.CharacterID != "" {
			charIDs.Add(cand.Session.CharacterID)
		}
	}
	return charIDs.ToSlice()
}

func buildTickParticipant(sessionUserID, characterID string, u *user.User) tickParticipant {
	p := tickParticipant{
		userID: sessionUserID,
	}
	if u == nil {
		return p
	}
	if u.ID != "" {
		p.userID = u.ID
	}
	p.displayName = u.DisplayName
	for _, ch := range u.Characters {
		if ch.Id == characterID {
			p.characterClass = ch.Class
			p.emblemURL = ch.EmblemURL
			break
		}
	}
	return p
}

func stampParticipantMetadata(link api.SnapshotLink, sessionID string, p tickParticipant) api.SnapshotLink {
	if sessionID != "" {
		link.SessionID = new(sessionID)
	}
	if p.userID != "" {
		link.UserID = new(p.userID)
	}
	if p.displayName != "" {
		link.DisplayName = new(p.displayName)
	}
	if p.characterClass != "" {
		link.CharacterClass = new(p.characterClass)
	}
	if p.emblemURL != "" {
		link.EmblemURL = new(p.emblemURL)
	}
	return link
}

func (r *tickRunner) persistSessionProgress(iterCtx context.Context, cand *TickCandidate, latestID string, linkRes linkActivitiesResult) error {
	ses := &cand.Session
	newAggIDs := make([]string, 0, len(linkRes.aggregateIDs))
	for _, id := range linkRes.aggregateIDs {
		if id != "" && !slices.Contains(ses.AggregateIDs, id) && !slices.Contains(newAggIDs, id) {
			newAggIDs = append(newAggIDs, id)
		}
	}

	if r.opts.DryRun {
		ses.AggregateIDs = utils.UnionNonZero(ses.AggregateIDs, newAggIDs)
		r.resp.SessionsProcessed++
		r.resp.AggregatesAddedTotal += len(newAggIDs)
		r.aggregatesRefreshed += linkRes.refreshedCount
		return nil
	}

	var postErrs []error
	if !cand.isCompleted() {
		if !linkRes.hasPendingRetry {
			if err := r.svc.store.SetLastActivity(iterCtx, ses.ID, latestID, r.svc.now()); err != nil {
				r.resp.ErrorsTotal++
				postErrs = append(postErrs, fmt.Errorf("failed to save last activity for session %s: %w", ses.ID, err))
			}
		} else if len(newAggIDs) > 0 {
			if err := r.svc.store.TouchLastSeen(iterCtx, ses.ID, r.svc.now()); err != nil {
				r.resp.ErrorsTotal++
				postErrs = append(postErrs, fmt.Errorf("failed to update last seen timestamp for session %s: %w", ses.ID, err))
			}
		}
	}

	if len(newAggIDs) > 0 {
		if err := r.svc.AddAggregateIDs(iterCtx, ses.ID, newAggIDs); err != nil {
			r.resp.SessionsErrored++
			r.resp.ErrorsTotal++
			return errors.Join(append(postErrs, fmt.Errorf("failed to add aggregate IDs to session %s: %w", ses.ID, err))...)
		}
	}

	ses.AggregateIDs = utils.UnionNonZero(ses.AggregateIDs, newAggIDs)
	summary, err := r.svc.ComputeSessionSummary(iterCtx, *ses)
	if err != nil {
		r.resp.ErrorsTotal++
		postErrs = append(postErrs, fmt.Errorf("failed to compute session summary for session %s: %w", ses.ID, err))
	} else if err := r.svc.UpdateSessionSummary(iterCtx, ses.ID, summary); err != nil {
		r.resp.ErrorsTotal++
		postErrs = append(postErrs, fmt.Errorf("failed to update session summary for session %s: %w", ses.ID, err))
	}

	r.resp.SessionsProcessed++
	r.resp.AggregatesAddedTotal += len(newAggIDs)
	r.aggregatesRefreshed += linkRes.refreshedCount
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

func isIncompletePerformance(perf api.InstancePerformance) bool {
	return perf.Extra == nil && len(perf.Weapons) == 0
}

func needsPerformanceRefresh(agg *api.Aggregate, characterID string, now time.Time) bool {
	if agg == nil {
		return false
	}
	link, ok := agg.SnapshotLinks[characterID]
	if !ok || link.SessionID == nil || link.SnapshotID != nil {
		return false
	}
	if link.ConfidenceSource != api.SystemConfidenceSource {
		return false
	}
	refTime := link.CreatedAt
	if refTime.IsZero() {
		refTime = agg.CreatedAt
	}
	if refTime.IsZero() || now.Sub(refTime) >= incompletePerformanceRetryWindow {
		return false
	}
	perf, ok := agg.Performance[characterID]
	return !ok || isIncompletePerformance(perf)
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
		return s.store.DeleteDoc(ctx, ses.ID)
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
	if err := s.store.EndSessionDoc(ctx, ses.ID, completedBy, s.now(), summary); err != nil {
		return fmt.Errorf("failed to end session: %w", err)
	}
	return nil
}

func (s service) CleanupEmptyCompleted(ctx context.Context, dryRun bool) (int, error) {
	now := s.now()
	windowEnd := now.Add(-completedGracePeriod)
	windowStart := now.Add(-emptyCleanupLookback)
	sessions, err := s.store.ListCompletedInWindow(ctx, windowStart, windowEnd)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch completed sessions for cleanup: %w", err)
	}

	deletedCount := 0
	rollupErr := clog.LoopRollup(ctx, "sessionService.cleanupEmptyItem", sessions, func(iterCtx context.Context, _ int, ses api.Session) error {
		clog.WithGroupField(iterCtx, "session", "sessionId", ses.ID)
		if len(ses.AggregateIDs) == 0 {
			if ses.CompletedAt == nil || ses.CompletedAt.Before(windowStart) || ses.CompletedAt.After(windowEnd) {
				return nil
			}
			if !dryRun {
				if err := s.store.DeleteDoc(iterCtx, ses.ID); err != nil {
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
