package session

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"oneTrick/api"
	"oneTrick/services/snapshot"
	"oneTrick/services/user"
	"oneTrick/set"
	"oneTrick/utils"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	clog "github.com/Shannor/canonical-logger"
	"golang.org/x/sync/singleflight"
)

const defaultTickConcurrency = 8

type TickOptions struct {
	DryRun         bool
	SkipSave       bool
	MaxConcurrency int
}

func (o TickOptions) concurrency() int {
	if o.MaxConcurrency > 0 {
		return o.MaxConcurrency
	}
	return defaultTickConcurrency
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

type sessionTickOutcome struct {
	queueCompSessionID  string
	deletedSessionID    string
	updatedSession      *api.Session
	persistSessionMap   bool
	dirtySessionIDs     []string
	sessionsProcessed   int
	sessionsSkipped     int
	staleEnded          int
	staleDeleted        int
	inactiveDeleted     int
	sessionsErrored     int
	loadoutsSaved       int
	loadoutsThrottled   int
	loadoutsFailed      int
	activitiesFound     int
	aggregatesAdded     int
	aggregatesRefreshed int
	errorsTotal         int
}

type tickRunner struct {
	svc                   service
	opts                  TickOptions
	now                   time.Time
	resp                  *api.SessionTickResponse
	compQueue             *set.OrderedSet[string]
	dirtySessionIDs       *set.OrderedSet[string]
	deletedSessionIDs     *set.Set[string]
	sessionsByID          map[string]api.Session
	userMu                sync.RWMutex
	userFlight            singleflight.Group
	usersByID             map[string]*user.User
	pgcrMu                sync.RWMutex
	pgcrFlight            singleflight.Group
	pgcrCache             map[string]map[string]api.InstancePerformance
	charLocks             [64]sync.Mutex
	candidateCharacterIDs []string
	loadoutsThrottled     int
	aggregatesRefreshed   int
	pgcrCacheHits         atomic.Int64
	summariesUpdated      int
}

func newTickRunner(svc service, opts TickOptions) *tickRunner {
	return &tickRunner{
		svc:               svc,
		opts:              opts,
		now:               svc.now(),
		resp:              &api.SessionTickResponse{DryRun: opts.DryRun},
		compQueue:         set.NewOrdered[string](),
		dirtySessionIDs:   set.NewOrdered[string](),
		deletedSessionIDs: set.New[string](),
		sessionsByID:      make(map[string]api.Session),
		usersByID:         make(map[string]*user.User),
		pgcrCache:         make(map[string]map[string]api.InstancePerformance),
	}
}

func lockStripe(locks *[64]sync.Mutex, key string) *sync.Mutex {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	mu := &locks[h.Sum32()%uint32(len(locks))]
	mu.Lock()
	return mu
}

func (s service) RunTick(ctx context.Context, opts TickOptions) (*api.SessionTickResponse, error) {
	tickStart := time.Now()
	runner := newTickRunner(s, opts)

	clog.WithGroupFields(ctx, "sessionService.runTick", clog.Fields{
		"dryRun":      opts.DryRun,
		"skipSave":    opts.SkipSave,
		"concurrency": opts.concurrency(),
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
		"pgcrCacheHits":           int(r.pgcrCacheHits.Load()),
		"summariesUpdated":        r.summariesUpdated,
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
	for _, cand := range candidates {
		if cand.Session.ID != "" {
			ses := cand.Session
			ses.AggregateIDs = slices.Clone(ses.AggregateIDs)
			r.sessionsByID[ses.ID] = ses
		}
	}

	r.processCandidates(ctx, candidates)
	r.recomputeDirtySummaries(ctx)
	r.finalizeCompSessions(ctx)
	r.cleanupEmptyCompleted(ctx)

	return r.resp, nil
}

func (r *tickRunner) processCandidates(tickCtx context.Context, candidates []TickCandidate) {
	if len(candidates) == 0 {
		return
	}
	outcomes := make([]sessionTickOutcome, len(candidates))

	_ = clog.LoopRollupWithOptions(tickCtx, "sessionService.processSession", candidates, clog.LoopOptions{
		Concurrency: r.opts.concurrency(),
	}, func(iterCtx context.Context, idx int, cand TickCandidate) error {
		out, err := r.processSession(tickCtx, iterCtx, cand)
		outcomes[idx] = out
		return err
	})

	r.applyCandidateOutcomes(outcomes)
}

func (r *tickRunner) applyCandidateOutcomes(outcomes []sessionTickOutcome) {
	for _, out := range outcomes {
		r.resp.SessionsProcessed += out.sessionsProcessed
		r.resp.SessionsSkipped += out.sessionsSkipped
		r.resp.SessionsStaleEnded += out.staleEnded
		r.resp.SessionsStaleDeleted += out.staleDeleted
		r.resp.SessionsInactiveDeleted += out.inactiveDeleted
		r.resp.SessionsErrored += out.sessionsErrored
		r.resp.LoadoutsSaved += out.loadoutsSaved
		r.loadoutsThrottled += out.loadoutsThrottled
		r.resp.LoadoutsFailed += out.loadoutsFailed
		r.resp.ActivitiesFoundTotal += out.activitiesFound
		r.resp.AggregatesAddedTotal += out.aggregatesAdded
		r.aggregatesRefreshed += out.aggregatesRefreshed
		r.resp.ErrorsTotal += out.errorsTotal

		if out.deletedSessionID != "" {
			delete(r.sessionsByID, out.deletedSessionID)
			r.deletedSessionIDs.Add(out.deletedSessionID)
		}
		if out.persistSessionMap && out.updatedSession != nil && out.updatedSession.ID != "" {
			r.sessionsByID[out.updatedSession.ID] = *out.updatedSession
		}
		for _, dirtyID := range out.dirtySessionIDs {
			if dirtyID != "" {
				r.dirtySessionIDs.Add(dirtyID)
			}
		}
		if out.queueCompSessionID != "" {
			r.compQueue.Add(out.queueCompSessionID)
		}
	}
}

func (r *tickRunner) processSession(tickCtx, iterCtx context.Context, cand TickCandidate) (out sessionTickOutcome, err error) {
	// Serialize rare overlapping pending + recently-completed grace sessions on the same character.
	if cand.Session.CharacterID != "" {
		mu := lockStripe(&r.charLocks, cand.Session.CharacterID)
		defer mu.Unlock()
	}

	clog.WithGroupFields(iterCtx, "session", clog.Fields{
		"sessionId":   cand.Session.ID,
		"userId":      cand.Session.UserID,
		"characterId": cand.Session.CharacterID,
	})

	if cand.isCompleted() {
		defer func() {
			ses := cand.Session
			if out.updatedSession != nil {
				ses = *out.updatedSession
			}
			if ShouldFinalizeComp(ses) {
				out.queueCompSessionID = ses.ID
			}
		}()
	}

	if handled, expOut, expErr := r.expireIfNeeded(iterCtx, cand); handled || expErr != nil {
		return expOut, expErr
	}

	membership, err := r.resolveMembership(tickCtx, cand.Session)
	if err != nil {
		out.sessionsErrored++
		out.errorsTotal++
		return out, err
	}

	updatedCand, loadoutOut, err := r.saveLoadoutIfNeeded(iterCtx, cand, membership)
	out.loadoutsSaved += loadoutOut.loadoutsSaved
	out.loadoutsThrottled += loadoutOut.loadoutsThrottled
	out.loadoutsFailed += loadoutOut.loadoutsFailed
	out.errorsTotal += loadoutOut.errorsTotal
	if err != nil {
		return out, err
	}

	latestID, histories, err := r.fetchNewActivities(iterCtx, updatedCand, membership)
	if err != nil {
		out.sessionsErrored++
		out.errorsTotal++
		return out, err
	}
	if len(histories) == 0 {
		out.sessionsSkipped++
		return out, nil
	}

	out.activitiesFound += len(histories)

	linkRes, fatalLinkErr, err := r.linkActivities(tickCtx, iterCtx, updatedCand.Session, membership.participant, histories)
	out.errorsTotal += linkRes.activityErrors
	if err != nil {
		if fatalLinkErr {
			out.sessionsErrored++
			out.errorsTotal++
		}
		return out, err
	}
	if len(linkRes.aggregateIDs) == 0 {
		if !linkRes.hasPendingRetry && !updatedCand.isCompleted() && !r.opts.DryRun && latestID != "" && !updatedCand.hasSeenActivity(latestID) {
			if err := r.svc.store.SetLastActivity(iterCtx, updatedCand.Session.ID, latestID, r.svc.now()); err != nil {
				out.errorsTotal++
				return out, fmt.Errorf("failed to save last activity for session %s: %w", updatedCand.Session.ID, err)
			}
		}
		out.sessionsSkipped++
		return out, nil
	}

	progressOut, err := r.persistSessionProgress(iterCtx, updatedCand, latestID, linkRes)
	out.updatedSession = progressOut.updatedSession
	out.persistSessionMap = progressOut.persistSessionMap
	out.dirtySessionIDs = progressOut.dirtySessionIDs
	out.sessionsProcessed += progressOut.sessionsProcessed
	out.sessionsErrored += progressOut.sessionsErrored
	out.aggregatesAdded += progressOut.aggregatesAdded
	out.aggregatesRefreshed += progressOut.aggregatesRefreshed
	out.errorsTotal += progressOut.errorsTotal
	return out, err
}

func (r *tickRunner) expireIfNeeded(ctx context.Context, cand TickCandidate) (bool, sessionTickOutcome, error) {
	var (
		reason  string
		isStale bool
	)
	switch {
	case cand.isStale(r.now):
		reason = "stale"
		isStale = true
	case cand.isInactive(r.now):
		reason = "inactive"
	default:
		return false, sessionTickOutcome{}, nil
	}

	if !r.opts.DryRun {
		if err := r.svc.endOrDeleteSession(ctx, cand.Session); err != nil {
			return true, sessionTickOutcome{
				sessionsErrored: 1,
				errorsTotal:     1,
			}, fmt.Errorf("failed to process %s session %s: %w", reason, cand.Session.ID, err)
		}
	}

	var out sessionTickOutcome
	if !cand.hasAggregates() {
		if isStale {
			out.staleDeleted = 1
		} else {
			out.inactiveDeleted = 1
		}
		if !r.opts.DryRun && cand.Session.ID != "" {
			out.deletedSessionID = cand.Session.ID
		}
	} else {
		out.staleEnded = 1
		if ShouldFinalizeComp(cand.Session) {
			out.queueCompSessionID = cand.Session.ID
		}
	}
	return true, out, nil
}

func (r *tickRunner) getUser(tickCtx context.Context, userID string) (*user.User, error) {
	if userID == "" {
		return nil, user.NotFound
	}
	r.userMu.RLock()
	cached, ok := r.usersByID[userID]
	r.userMu.RUnlock()
	if ok {
		return cached, nil
	}

	val, err, _ := r.userFlight.Do(userID, func() (any, error) {
		r.userMu.RLock()
		cached, ok := r.usersByID[userID]
		r.userMu.RUnlock()
		if ok {
			return cached, nil
		}

		u, err := r.svc.userService.GetUser(clog.NewContext(tickCtx), userID)
		if err != nil {
			return nil, err
		}
		if u == nil {
			return nil, user.NotFound
		}

		r.userMu.Lock()
		r.usersByID[userID] = u
		if u.ID != "" {
			r.usersByID[u.ID] = u
		}
		r.userMu.Unlock()
		return u, nil
	})
	if err != nil {
		return nil, err
	}
	return val.(*user.User), nil
}

func (r *tickRunner) resolveMembership(tickCtx context.Context, ses api.Session) (tickMembership, error) {
	if r.svc.userService == nil || r.svc.d2Service == nil {
		return tickMembership{}, fmt.Errorf("session tick dependencies not configured")
	}

	u, err := r.getUser(tickCtx, ses.UserID)
	if err != nil {
		return tickMembership{}, fmt.Errorf("failed to fetch user for session %s: %w", ses.ID, err)
	}
	pmID, membershipType, err := u.PrimaryMembership()
	if err != nil {
		return tickMembership{}, fmt.Errorf("failed to fetch membership type for session %s: %w", ses.ID, err)
	}
	return tickMembership{
		id:             strconv.FormatInt(pmID, 10),
		membershipType: membershipType,
		participant:    buildTickParticipant(ses.UserID, ses.CharacterID, u),
	}, nil
}

func (r *tickRunner) saveLoadoutIfNeeded(ctx context.Context, cand TickCandidate, membership tickMembership) (TickCandidate, sessionTickOutcome, error) {
	if cand.isCompleted() || r.opts.SkipSave || r.opts.DryRun {
		return cand, sessionTickOutcome{}, nil
	}
	if r.svc.snapshotService == nil {
		return cand, sessionTickOutcome{
			loadoutsFailed: 1,
			errorsTotal:    1,
		}, fmt.Errorf("snapshot service not configured for session %s", cand.Session.ID)
	}
	res, err := r.svc.snapshotService.SaveSessionCheckIn(ctx, cand.Session.UserID, membership.id, cand.Session.CharacterID, snapshot.SessionCheckInOptions{
		MembershipType:         membership.membershipType,
		LastSnapshotID:         cand.LastSnapshotID,
		LastSnapshotHash:       cand.LastSnapshotHash,
		LastSnapshotRecordedAt: cand.LastSnapshotRecordedAt,
		HeartbeatInterval:      snapshotHeartbeatPeriod,
	})
	if err != nil {
		return cand, sessionTickOutcome{
			loadoutsFailed: 1,
			errorsTotal:    1,
		}, fmt.Errorf("failed to save loadout for session %s: %w", cand.Session.ID, err)
	}
	if !res.HistoryRecorded {
		return cand, sessionTickOutcome{loadoutsThrottled: 1}, nil
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
	return cand, sessionTickOutcome{loadoutsSaved: 1}, nil
}

func (r *tickRunner) fetchNewActivities(ctx context.Context, cand TickCandidate, membership tickMembership) (string, []api.ActivityHistory, error) {
	rawActivities, err := r.svc.d2Service.GetAllPVPActivity(ctx, membership.id, membership.membershipType, cand.Session.CharacterID, 5, 0)
	if err != nil {
		return "", nil, fmt.Errorf("failed to get activities for session %s: %w", cand.Session.ID, err)
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
	dirtySessionIDs []string
	refreshedCount  int
	activityErrors  int
	hasPendingRetry bool
}

type activityLinkOutcome struct {
	aggregateID     string
	dirtySessionIDs []string
	linked          bool
	refreshed       bool
	pendingRetry    bool
}

func (r *tickRunner) linkActivities(tickCtx, iterCtx context.Context, ses api.Session, participant tickParticipant, histories []api.ActivityHistory) (linkActivitiesResult, bool, error) {
	if r.svc.aggregateService == nil {
		return linkActivitiesResult{}, true, fmt.Errorf("aggregate service not configured for session %s", ses.ID)
	}

	ids := make([]string, len(histories))
	for i, h := range histories {
		ids[i] = h.InstanceID
	}

	existingAggs, err := r.svc.aggregateService.GetAggregatesByActivity(iterCtx, ids)
	if err != nil {
		return linkActivitiesResult{}, true, fmt.Errorf("failed to fetch aggregates for session %s: %w", ses.ID, err)
	}

	existingByActivity := make(map[string]*api.Aggregate, len(existingAggs))
	for i := range existingAggs {
		existingByActivity[existingAggs[i].ActivityID] = &existingAggs[i]
	}

	res := linkActivitiesResult{
		aggregateIDs: make([]string, 0, len(histories)),
	}
	dirty := set.NewOrdered[string]()
	_ = clog.LoopRollup(iterCtx, "aggregateService.processActivity", histories, func(actCtx context.Context, _ int, history api.ActivityHistory) error {
		outcome, err := r.linkSingleActivity(tickCtx, actCtx, ses, participant, history, existingByActivity[history.InstanceID])
		if err != nil {
			res.activityErrors++
			res.hasPendingRetry = true
			return err
		}
		if outcome.pendingRetry {
			res.hasPendingRetry = true
		}
		if outcome.linked {
			res.aggregateIDs = append(res.aggregateIDs, outcome.aggregateID)
			for _, dirtyID := range outcome.dirtySessionIDs {
				dirty.Add(dirtyID)
			}
			if outcome.refreshed {
				res.refreshedCount++
			}
		}
		return nil
	})
	res.dirtySessionIDs = dirty.ToSlice()

	return res, false, nil
}

func (r *tickRunner) getPerformances(tickCtx context.Context, activityID string) (map[string]api.InstancePerformance, error) {
	r.pgcrMu.RLock()
	cached, ok := r.pgcrCache[activityID]
	r.pgcrMu.RUnlock()
	if ok {
		r.pgcrCacheHits.Add(1)
		return cached, nil
	}

	fetchedByCaller := false
	val, err, _ := r.pgcrFlight.Do(activityID, func() (any, error) {
		r.pgcrMu.RLock()
		cached, ok := r.pgcrCache[activityID]
		r.pgcrMu.RUnlock()
		if ok {
			return cached, nil
		}

		performances, err := r.svc.d2Service.GetPerformances(clog.NewContext(tickCtx), activityID, r.candidateCharacterIDs)
		if err != nil {
			return nil, err
		}
		fetchedByCaller = true

		r.pgcrMu.Lock()
		r.pgcrCache[activityID] = performances
		r.pgcrMu.Unlock()
		return performances, nil
	})
	if err != nil {
		return nil, err
	}
	if !fetchedByCaller {
		r.pgcrCacheHits.Add(1)
	}
	return val.(map[string]api.InstancePerformance), nil
}

func (r *tickRunner) linkSingleActivity(tickCtx, actCtx context.Context, ses api.Session, participant tickParticipant, history api.ActivityHistory, existing *api.Aggregate) (activityLinkOutcome, error) {
	clog.WithGroupField(actCtx, "activity", "activityId", history.InstanceID)

	refreshing := false
	if hasSessionLink(existing, ses.CharacterID) {
		if !needsPerformanceRefresh(existing, ses.CharacterID, r.now) {
			return activityLinkOutcome{}, nil
		}
		refreshing = true
	}

	performances, err := r.getPerformances(tickCtx, history.InstanceID)
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
		aggregateID:     agg.ID,
		dirtySessionIDs: collectAggregateSessionIDs(agg),
		linked:          true,
		refreshed:       refreshing,
		pendingRetry:    incomplete,
	}, nil
}

func collectAggregateSessionIDs(agg *api.Aggregate) []string {
	if agg == nil {
		return nil
	}
	ids := set.NewOrdered[string]()
	for _, sid := range agg.SessionIds {
		if sid != "" {
			ids.Add(sid)
		}
	}
	if len(agg.SnapshotLinks) > 0 {
		extra := make([]string, 0, len(agg.SnapshotLinks))
		for _, link := range agg.SnapshotLinks {
			if link.SessionID != nil && *link.SessionID != "" {
				extra = append(extra, *link.SessionID)
			}
		}
		slices.Sort(extra)
		for _, sid := range extra {
			ids.Add(sid)
		}
	}
	return ids.ToSlice()
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

func (r *tickRunner) persistSessionProgress(iterCtx context.Context, cand TickCandidate, latestID string, linkRes linkActivitiesResult) (sessionTickOutcome, error) {
	ses := cand.Session
	seen := set.FromSlice(ses.AggregateIDs)
	newAggIDs := make([]string, 0, len(linkRes.aggregateIDs))
	for _, id := range linkRes.aggregateIDs {
		if id == "" || seen.Contains(id) {
			continue
		}
		seen.Add(id)
		newAggIDs = append(newAggIDs, id)
	}

	var out sessionTickOutcome
	if r.opts.DryRun {
		updatedSes := ses
		updatedSes.AggregateIDs = utils.UnionNonZero(ses.AggregateIDs, newAggIDs)
		out.updatedSession = &updatedSes
		out.sessionsProcessed = 1
		out.aggregatesAdded = len(newAggIDs)
		out.aggregatesRefreshed = linkRes.refreshedCount
		return out, nil
	}

	var postErrs []error
	if !cand.isCompleted() {
		if !linkRes.hasPendingRetry {
			if err := r.svc.store.SetLastActivity(iterCtx, ses.ID, latestID, r.svc.now()); err != nil {
				out.errorsTotal++
				postErrs = append(postErrs, fmt.Errorf("failed to save last activity for session %s: %w", ses.ID, err))
			}
		} else if len(newAggIDs) > 0 {
			if err := r.svc.store.TouchLastSeen(iterCtx, ses.ID, r.svc.now()); err != nil {
				out.errorsTotal++
				postErrs = append(postErrs, fmt.Errorf("failed to update last seen timestamp for session %s: %w", ses.ID, err))
			}
		}
	}

	if len(newAggIDs) > 0 {
		if err := r.svc.AddAggregateIDs(iterCtx, ses.ID, newAggIDs); err != nil {
			dirty := make([]string, 0, len(linkRes.dirtySessionIDs))
			for _, dirtyID := range linkRes.dirtySessionIDs {
				if dirtyID != "" && dirtyID != ses.ID {
					dirty = append(dirty, dirtyID)
				}
			}
			out.dirtySessionIDs = dirty
			out.sessionsErrored++
			out.errorsTotal++
			return out, errors.Join(append(postErrs, fmt.Errorf("failed to add aggregate IDs to session %s: %w", ses.ID, err))...)
		}
	}

	updatedSes := ses
	updatedSes.AggregateIDs = slices.Clone(utils.UnionNonZero(ses.AggregateIDs, newAggIDs))
	out.updatedSession = &updatedSes
	out.persistSessionMap = true

	dirty := make([]string, 0, 1+len(linkRes.dirtySessionIDs))
	if ses.ID != "" {
		dirty = append(dirty, ses.ID)
	}
	for _, dirtyID := range linkRes.dirtySessionIDs {
		if dirtyID != "" {
			dirty = append(dirty, dirtyID)
		}
	}
	out.dirtySessionIDs = dirty
	out.sessionsProcessed = 1
	out.aggregatesAdded = len(newAggIDs)
	out.aggregatesRefreshed = linkRes.refreshedCount
	return out, errors.Join(postErrs...)
}

func (r *tickRunner) recomputeDirtySummaries(ctx context.Context) {
	sessionIDs := r.dirtySessionIDs.ToSlice()
	if len(sessionIDs) == 0 || r.opts.DryRun {
		return
	}

	_ = clog.LoopRollup(ctx, "sessionService.recomputeSummary", sessionIDs, func(iterCtx context.Context, _ int, sessionID string) error {
		clog.WithGroupField(iterCtx, "session", "sessionId", sessionID)
		if sessionID == "" || r.deletedSessionIDs.Contains(sessionID) {
			return nil
		}

		ses, ok := r.sessionsByID[sessionID]
		if !ok {
			fetched, err := r.svc.store.GetDoc(iterCtx, sessionID)
			if err != nil {
				if errors.Is(err, ErrNotFound) {
					return nil
				}
				r.resp.ErrorsTotal++
				return fmt.Errorf("failed to fetch dirty session %s: %w", sessionID, err)
			}
			ses = *fetched
		}

		summary, err := r.svc.ComputeSessionSummary(iterCtx, ses)
		if err != nil {
			r.resp.ErrorsTotal++
			return fmt.Errorf("failed to compute session summary for session %s: %w", ses.ID, err)
		}
		if err := r.svc.UpdateSessionSummary(iterCtx, ses.ID, summary); err != nil {
			r.resp.ErrorsTotal++
			return fmt.Errorf("failed to update session summary for session %s: %w", ses.ID, err)
		}
		r.summariesUpdated++
		return nil
	})
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
