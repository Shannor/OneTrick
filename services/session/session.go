package session

import (
	"context"
	"errors"
	"fmt"
	"oneTrick/api"
	"oneTrick/core/domain"
	"oneTrick/generator"
	"oneTrick/services/aggregate"
	"oneTrick/services/snapshot"
	"oneTrick/services/user"
	"oneTrick/utils"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/firestore"
	clog "github.com/Shannor/canonical-logger"
)

type CompRankReader interface {
	GetCompetitiveRank(ctx context.Context, primaryMembershipID int64, membershipType int64, characterID string) (domain.CompRankSnapshot, bool, error)
}

type ActivityHistoryReader interface {
	GetActivityHistory(ctx context.Context, membershipID string, membershipType int64, characterID string, mode domain.ActivityQueryMode, count int64, page int64) ([]domain.ActivityInfo, error)
}

type PerformanceReader interface {
	GetPerformances(ctx context.Context, activityID string, characterIDs []string) (map[string]domain.Performance, error)
}

type ManifestReader interface {
	GetItemsByIDs(ctx context.Context, ids []int64) (map[int64]domain.ItemManifest, error)
	GetActivitiesByIDs(ctx context.Context, ids []int64) (map[int64]domain.ActivityManifest, error)
	GetActivityModesByIDs(ctx context.Context, ids []int64) (map[int64]domain.ActivityModeManifest, error)
}

type Service interface {
	Start(ctx context.Context, userID string, characterID string, startedBy api.AuditField, trackComp bool) (*api.Session, error)
	Update(ctx context.Context, sessionID string, name, description string) error
	AddAggregateIDs(ctx context.Context, sessionID string, aggregateIDs []string) error
	Get(ctx context.Context, ID string) (*api.Session, error)
	GetActive(ctx context.Context, userID string, characterID string) (*api.Session, error)
	GetActiveByUserIDs(ctx context.Context, userIDs []string) ([]ActiveSession, error)
	GetAll(ctx context.Context, userID *string, characterID *string, status *api.SessionStatus, count int, offset int) ([]api.Session, error)
	Complete(ctx context.Context, ID string) (*api.Session, error)
	FinalizeCompTracking(ctx context.Context, sessionID string) error
	SetLastActivity(ctx context.Context, ID, activityID string) error
	Delete(ctx context.Context, sessionID string, userID string) error
	ComputeSessionSummary(ctx context.Context, ses api.Session) (*api.SessionSummary, error)
	UpdateSessionSummary(ctx context.Context, sessionID string, summary *api.SessionSummary) error
	GetSessionAggregates(ctx context.Context, sessionID string) (SessionAggregatesResult, error)
	BackfillSessionSummaries(ctx context.Context, dryRun bool) (BackfillSessionSummariesResult, error)
	CleanupEmptyCompleted(ctx context.Context, dryRun bool) (int, error)
	RunTick(ctx context.Context, opts TickOptions) (*api.SessionTickResponse, error)
}

var (
	ErrNotFound     = fmt.Errorf("session not found")
	ErrUnauthorized = fmt.Errorf("unauthorized")
)

type service struct {
	store                 Store
	aggregateService      aggregate.Service
	userService           user.Service
	compRanks             CompRankReader
	activityHistory       ActivityHistoryReader
	performances          PerformanceReader
	snapshotService       snapshot.Service
	manifestService       ManifestReader
	inFlightFinalizations *sync.Map
	nowFn                 func() time.Time
	finalCheckInTimeout   time.Duration
	finalCompTimeout      time.Duration
	finalWriteTimeout     time.Duration
	onFinalizeDone        func(ctx context.Context, err error)
}

var _ Service = (*service)(nil)

func NewService(
	store Store,
	aggregateService aggregate.Service,
	userService user.Service,
	compRanks CompRankReader,
	activityHistory ActivityHistoryReader,
	performances PerformanceReader,
	snapshotService snapshot.Service,
	manifestService ManifestReader,
) Service {
	return &service{
		store:                 store,
		aggregateService:      aggregateService,
		userService:           userService,
		compRanks:             compRanks,
		activityHistory:       activityHistory,
		performances:          performances,
		snapshotService:       snapshotService,
		manifestService:       manifestService,
		inFlightFinalizations: &sync.Map{},
	}
}

const (
	collection = "sessions"
	// CutOffDuration is when an inactive or stale pending session is automatically ended or deleted.
	CutOffDuration           = 2 * time.Hour
	completedGracePeriod     = 15 * time.Minute
	emptyCleanupLookback     = 60 * time.Minute
	activityStartGracePeriod = 15 * time.Minute
	// 28m (rather than 30m) absorbs 5m cron dispatch jitter so Tick 6 reliably emits the 30m heartbeat.
	snapshotHeartbeatPeriod = 28 * time.Minute
	// Active sessions retry incomplete PGCRs up to 30m; completed sessions are additionally bounded by completedGracePeriod (15m) to cap post-completion reads.
	incompletePerformanceRetryWindow = 30 * time.Minute
	finalLoadoutCheckInTimeout       = 5 * time.Second
	finalCompRankTimeout             = 5 * time.Second
	finalStoreWriteTimeout           = 5 * time.Second
)

func (s service) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

func (s service) checkInTimeout() time.Duration {
	if s.finalCheckInTimeout > 0 {
		return s.finalCheckInTimeout
	}
	return finalLoadoutCheckInTimeout
}

func (s service) compFinalizeTimeout() time.Duration {
	if s.finalCompTimeout > 0 {
		return s.finalCompTimeout
	}
	return finalCompRankTimeout
}

func (s service) storeWriteTimeout() time.Duration {
	if s.finalWriteTimeout > 0 {
		return s.finalWriteTimeout
	}
	return finalStoreWriteTimeout
}

func (s service) membershipLookupTimeout(task completeFinalizeTask) time.Duration {
	switch {
	case task.runLoadoutCheckIn && task.runCompFinalize:
		return max(s.checkInTimeout(), s.compFinalizeTimeout())
	case task.runCompFinalize:
		return s.compFinalizeTimeout()
	default:
		return s.checkInTimeout()
	}
}

func (s service) tryAcquireFinalize(sessionID string) (release func(), acquired bool) {
	if s.inFlightFinalizations == nil {
		return func() {}, true
	}
	if _, loaded := s.inFlightFinalizations.LoadOrStore(sessionID, struct{}{}); loaded {
		return nil, false
	}
	return func() { s.inFlightFinalizations.Delete(sessionID) }, true
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
		if s.userService != nil && s.compRanks != nil {
			pmID, membershipType, err := s.userService.GetPrimaryMembership(ctx, userID)
			if err != nil {
				clog.WithGroupFields(ctx, "sessionService.start", clog.Fields{
					"warning":       "failed to resolve primary membership for competitive tracking",
					"compRankError": err.Error(),
				})
			} else {
				snap, found, err := s.compRanks.GetCompetitiveRank(ctx, pmID, membershipType, characterID)
				if err != nil {
					clog.WithGroupFields(ctx, "sessionService.start", clog.Fields{
						"warning":       "failed to fetch starting competitive rank",
						"compRankError": err.Error(),
					})
				} else if found {
					compTracking.Start = toAPICompRankSnapshot(snap)
				}
			}
		} else {
			clog.WithGroupField(ctx, "sessionService.start", "warning", "competitive rank services not configured")
		}
		result.CompTracking = compTracking
	}

	created, err := s.store.CreateDoc(ctx, result)
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
	hasActive, err := s.store.HasActive(ctx, userID, characterID)
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
	result, err := s.store.GetActiveDoc(ctx, userID, characterID)
	if err != nil {
		return nil, err
	}
	if result.AggregateIDs == nil {
		result.AggregateIDs = make([]string, 0)
	}
	clog.WithGroupField(ctx, "sessionService.getActive", "sessionId", result.ID)
	return result, nil
}

func (s service) GetActiveByUserIDs(ctx context.Context, userIDs []string) ([]ActiveSession, error) {
	uniqueIDs := utils.UniqueTrimmedStrings(userIDs)
	clog.WithGroupField(ctx, "sessionService.getActiveByUserIds", "requestedUserCount", len(uniqueIDs))
	if len(uniqueIDs) == 0 || s.store == nil {
		clog.WithGroupField(ctx, "sessionService.getActiveByUserIds", "returnedCount", 0)
		return []ActiveSession{}, nil
	}
	sessions, err := s.store.GetActiveByUserIDs(ctx, uniqueIDs)
	if err != nil {
		return nil, err
	}
	sortActiveSessions(sessions)
	clog.WithGroupField(ctx, "sessionService.getActiveByUserIds", "returnedCount", len(sessions))
	return sessions, nil
}

func (s service) Get(ctx context.Context, ID string) (*api.Session, error) {
	clog.WithGroupField(ctx, "sessionService.get", "sessionId", ID)
	result, err := s.store.GetDoc(ctx, ID)
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

	result, err := s.store.ListDocs(ctx, userID, characterID, status)
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
	if err := s.store.UpdateFields(ctx, sessionID, updates); err != nil {
		return fmt.Errorf("failed to update session: %w", err)
	}

	return nil
}

type completeFinalizeTask struct {
	sessionID              string
	userID                 string
	characterID            string
	lastSnapshotID         string
	lastSnapshotHash       string
	lastSnapshotRecordedAt time.Time
	runLoadoutCheckIn      bool
	runCompFinalize        bool
	hasCompStart           bool
	compStartPoints        int
}

type loadoutCheckInOutcome struct {
	status     string
	snapshotID string
	err        error
	metaErr    error
}

type compFinalizeOutcome struct {
	status     string
	endPoints  int
	pointDelta *int
	err        error
}

func buildCompleteFinalizeTask(sessionID string, cand TickCandidate, runLoadoutCheckIn, runCompFinalize bool) completeFinalizeTask {
	resolvedID := cand.Session.ID
	if resolvedID == "" {
		resolvedID = sessionID
	}
	task := completeFinalizeTask{
		sessionID:              resolvedID,
		userID:                 cand.Session.UserID,
		characterID:            cand.Session.CharacterID,
		lastSnapshotID:         cand.LastSnapshotID,
		lastSnapshotHash:       cand.LastSnapshotHash,
		lastSnapshotRecordedAt: cand.LastSnapshotRecordedAt,
		runLoadoutCheckIn:      runLoadoutCheckIn,
		runCompFinalize:        runCompFinalize,
	}
	if cand.Session.CompTracking != nil && cand.Session.CompTracking.Start != nil {
		task.hasCompStart = true
		task.compStartPoints = cand.Session.CompTracking.Start.Points
	}
	return task
}

func (s service) Complete(ctx context.Context, ID string) (*api.Session, error) {
	clog.WithGroupField(ctx, "sessionService.complete", "sessionId", ID)
	cand, err := s.store.GetDocWithMeta(ctx, ID)
	if err != nil {
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	session := &cand.Session
	if session.AggregateIDs == nil {
		session.AggregateIDs = make([]string, 0)
	}

	justCompleted := session.CompletedAt == nil
	if justCompleted {
		completedAt := s.now()
		if err := s.store.CompleteDoc(ctx, ID, completedAt); err != nil {
			return nil, fmt.Errorf("failed to complete session: %w", err)
		}
		session.CompletedAt = &completedAt
		session.Status = new(api.SessionComplete)
	} else {
		clog.WithGroupFields(ctx, "sessionService.complete", clog.Fields{
			"alreadyCompleted": true,
			"warning":          "session already completed",
		})
	}

	needsLoadout := justCompleted && s.snapshotService != nil && s.userService != nil
	wantsCompFinalize := session.CompTracking != nil && session.CompTracking.Enabled && session.CompTracking.End == nil
	needsCompFinalize := wantsCompFinalize && s.userService != nil && s.compRanks != nil
	if wantsCompFinalize && !needsCompFinalize {
		clog.WithGroupFields(ctx, "sessionService.complete", clog.Fields{
			"warning":            "competitive rank services not configured",
			"compFinalizeStatus": "fallback_run_tick",
		})
	}

	if needsLoadout || needsCompFinalize {
		// Detach live Bungie workflows after marking the session complete so End Session returns
		// immediately while UI real-time listeners and RunTick's 15m grace window handle updates/retries.
		task := buildCompleteFinalizeTask(ID, *cand, needsLoadout, needsCompFinalize)
		release, acquired := s.tryAcquireFinalize(task.sessionID)
		if !acquired {
			clog.WithGroupFields(ctx, "sessionService.complete", clog.Fields{
				"finalizationAsync":        false,
				"finalizationDeduplicated": true,
			})
		} else {
			clog.WithGroupFields(ctx, "sessionService.complete", clog.Fields{
				"finalizationAsync":     true,
				"finalLoadoutScheduled": needsLoadout,
				"compFinalizeScheduled": needsCompFinalize,
			})
			clog.Go(ctx, "complete-session-finalize-bg", func(bgCtx context.Context) error {
				err := func() error {
					defer release()
					return s.runCompleteFinalization(bgCtx, task)
				}()
				if s.onFinalizeDone != nil {
					s.onFinalizeDone(bgCtx, err)
				}
				return err
			})
		}
	} else {
		clog.WithGroupField(ctx, "sessionService.complete", "finalizationAsync", false)
	}

	return session, nil
}

func (s service) runCompleteFinalization(ctx context.Context, task completeFinalizeTask) error {
	fields := clog.Fields{
		"sessionId":   task.sessionID,
		"userId":      task.userID,
		"characterId": task.characterID,
	}

	memberCtx, memberCancel := context.WithTimeout(ctx, s.membershipLookupTimeout(task))
	pmID, membershipType, err := s.userService.GetPrimaryMembership(memberCtx, task.userID)
	memberCancel()
	if err != nil {
		fields["warning"] = "failed to resolve primary membership for session finalization"
		if task.runLoadoutCheckIn {
			fields["finalLoadoutStatus"] = "failed"
			fields["loadoutError"] = err.Error()
		}
		if task.runCompFinalize {
			fields["compFinalizeStatus"] = "fallback_run_tick"
			fields["compRankError"] = err.Error()
		}
		clog.WithGroupFields(ctx, "sessionService.completeFinalize", fields)
		return nil
	}

	var (
		loadoutOutcome loadoutCheckInOutcome
		compOutcome    compFinalizeOutcome
	)
	if task.runLoadoutCheckIn && task.runCompFinalize {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			loadoutOutcome = s.recordFinalLoadoutCheckIn(ctx, task, pmID, membershipType)
		}()
		go func() {
			defer wg.Done()
			compOutcome = s.finalizeCompForTask(ctx, task, pmID, membershipType)
		}()
		wg.Wait()
	} else if task.runLoadoutCheckIn {
		loadoutOutcome = s.recordFinalLoadoutCheckIn(ctx, task, pmID, membershipType)
	} else if task.runCompFinalize {
		compOutcome = s.finalizeCompForTask(ctx, task, pmID, membershipType)
	}

	var warnings []string
	if task.runLoadoutCheckIn {
		fields["finalLoadoutStatus"] = loadoutOutcome.status
		switch loadoutOutcome.status {
		case "saved":
			fields["finalLoadoutSaved"] = true
			fields["snapshotId"] = loadoutOutcome.snapshotID
			if loadoutOutcome.metaErr != nil {
				fields["lastSnapshotError"] = loadoutOutcome.metaErr.Error()
				warnings = append(warnings, "failed to persist final snapshot metadata on session")
			}
		case "throttled":
			fields["finalLoadoutThrottled"] = true
		case "failed":
			fields["loadoutError"] = loadoutOutcome.err.Error()
			warnings = append(warnings, "failed to save final loadout check-in")
		}
	}

	if task.runCompFinalize {
		fields["compFinalizeStatus"] = compOutcome.status
		switch compOutcome.status {
		case "succeeded":
			fields["compEndPoints"] = compOutcome.endPoints
			if compOutcome.pointDelta != nil {
				fields["compPointDelta"] = *compOutcome.pointDelta
			}
		case "fallback_run_tick":
			fields["compRankError"] = compOutcome.err.Error()
			warnings = append(warnings, "failed to finalize competitive rank; will fall back to runTick")
		}
	}

	if len(warnings) > 0 {
		fields["warning"] = strings.Join(warnings, "; ")
	}
	clog.WithGroupFields(ctx, "sessionService.completeFinalize", fields)
	return nil
}

func (s service) recordFinalLoadoutCheckIn(ctx context.Context, task completeFinalizeTask, pmID, membershipType int64) loadoutCheckInOutcome {
	checkInCtx, cancel := context.WithTimeout(ctx, s.checkInTimeout())
	defer cancel()

	res, err := s.snapshotService.SaveSessionCheckIn(checkInCtx, task.userID, strconv.FormatInt(pmID, 10), task.characterID, snapshot.SessionCheckInOptions{
		MembershipType:         membershipType,
		LastSnapshotID:         task.lastSnapshotID,
		LastSnapshotHash:       task.lastSnapshotHash,
		LastSnapshotRecordedAt: task.lastSnapshotRecordedAt,
		HeartbeatInterval:      snapshotHeartbeatPeriod,
	})
	if err != nil {
		return loadoutCheckInOutcome{
			status: "failed",
			err:    err,
		}
	}
	if !res.HistoryRecorded {
		return loadoutCheckInOutcome{
			status: "throttled",
		}
	}
	writeCtx, writeCancel := context.WithTimeout(ctx, s.storeWriteTimeout())
	metaErr := s.store.SetLastSnapshot(writeCtx, task.sessionID, res.SnapshotID, res.SnapshotHash, res.RecordedAt)
	writeCancel()
	return loadoutCheckInOutcome{
		status:     "saved",
		snapshotID: res.SnapshotID,
		metaErr:    metaErr,
	}
}

func (s service) finalizeCompForTask(ctx context.Context, task completeFinalizeTask, pmID, membershipType int64) compFinalizeOutcome {
	compCtx, cancel := context.WithTimeout(ctx, s.compFinalizeTimeout())
	defer cancel()

	endSnap, pointDelta, err := s.fetchAndPersistCompEnd(compCtx, ctx, s.storeWriteTimeout(), task.sessionID, task.characterID, pmID, membershipType, task.hasCompStart, task.compStartPoints)
	if err != nil {
		return compFinalizeOutcome{
			status: "fallback_run_tick",
			err:    err,
		}
	}
	return compFinalizeOutcome{
		status:     "succeeded",
		endPoints:  endSnap.Points,
		pointDelta: pointDelta,
	}
}

func (s service) FinalizeCompTracking(ctx context.Context, sessionID string) error {
	clog.WithGroupField(ctx, "sessionService.finalizeCompTracking", "sessionId", sessionID)
	ses, err := s.store.GetDoc(ctx, sessionID)
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

	if s.userService == nil || s.compRanks == nil {
		return fmt.Errorf("competitive rank services not configured")
	}

	pmID, membershipType, err := s.userService.GetPrimaryMembership(ctx, ses.UserID)
	if err != nil {
		return err
	}

	hasStart := ses.CompTracking.Start != nil
	startPoints := 0
	if hasStart {
		startPoints = ses.CompTracking.Start.Points
	}

	endSnap, pointDelta, err := s.fetchAndPersistCompEnd(ctx, ctx, 0, ses.ID, ses.CharacterID, pmID, membershipType, hasStart, startPoints)
	if err != nil {
		return err
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

func (s service) fetchAndPersistCompEnd(
	fetchCtx context.Context,
	storeCtx context.Context,
	writeTimeout time.Duration,
	sessionID string,
	characterID string,
	pmID int64,
	membershipType int64,
	hasStart bool,
	startPoints int,
) (*api.CompRankSnapshot, *int, error) {
	domainEnd, found, err := s.compRanks.GetCompetitiveRank(fetchCtx, pmID, membershipType, characterID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to fetch ending competitive rank: %w", err)
	}
	if !found {
		return nil, nil, fmt.Errorf("empty ending competitive rank snapshot")
	}
	endSnap := toAPICompRankSnapshot(domainEnd)

	var pointDelta *int
	if hasStart {
		delta := endSnap.Points - startPoints
		pointDelta = &delta
	}

	if writeTimeout > 0 {
		var cancel context.CancelFunc
		storeCtx, cancel = context.WithTimeout(storeCtx, writeTimeout)
		defer cancel()
	}

	if err := s.store.UpdateCompTrackingEnd(storeCtx, sessionID, endSnap, pointDelta); err != nil {
		return nil, nil, fmt.Errorf("failed to persist competitive rank end: %w", err)
	}

	return endSnap, pointDelta, nil
}

func (s service) SetLastActivity(ctx context.Context, ID, activityID string) error {
	clog.WithGroupFields(ctx, "sessionService.setLastActivity", clog.Fields{
		"sessionId":  ID,
		"activityId": activityID,
	})
	if err := s.store.SetLastActivity(ctx, ID, activityID, time.Now()); err != nil {
		return fmt.Errorf("failed to update session: %v", err)
	}
	return nil
}

func (s service) Delete(ctx context.Context, sessionID string, userID string) error {
	clog.WithGroupFields(ctx, "sessionService.delete", clog.Fields{
		"sessionId": sessionID,
		"userId":    userID,
	})
	ses, err := s.store.GetDoc(ctx, sessionID)
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
	var siblingSessionIDs []string
	if s.aggregateService != nil {
		siblingSessionIDs, err = s.aggregateService.RemoveSession(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("failed to clean up session aggregates: %w", err)
		}
	}

	// Delete session document
	if err := s.store.DeleteDoc(ctx, sessionID); err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	refreshedSiblings := 0
	if len(siblingSessionIDs) > 0 {
		if rollupErr := clog.LoopRollup(ctx, "sessionService.refreshSiblingSummary", siblingSessionIDs, func(iterCtx context.Context, _ int, siblingID string) error {
			clog.WithGroupField(iterCtx, "session", "sessionId", siblingID)
			if siblingID == "" || siblingID == sessionID {
				return nil
			}
			updated, err := s.recomputeSessionSummaryByID(iterCtx, siblingID)
			if err != nil {
				return err
			}
			if updated {
				refreshedSiblings++
			}
			return nil
		}); rollupErr != nil {
			clog.WithGroupFields(ctx, "sessionService.delete", clog.Fields{
				"warning":             "failed to refresh one or more sibling session summaries",
				"siblingRefreshError": rollupErr.Error(),
			})
		}
	}

	clog.WithGroupFields(ctx, "sessionService.delete", clog.Fields{
		"deleted":                  true,
		"siblingSessionsFound":     len(siblingSessionIDs),
		"siblingSessionsRefreshed": refreshedSiblings,
	})
	return nil
}

func (s service) recomputeSessionSummaryByID(ctx context.Context, sessionID string) (bool, error) {
	ses, err := s.store.GetDoc(ctx, sessionID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("failed to fetch sibling session %s: %w", sessionID, err)
	}
	summary, err := s.ComputeSessionSummary(ctx, *ses)
	if err != nil {
		return false, fmt.Errorf("failed to compute summary for sibling session %s: %w", ses.ID, err)
	}
	if err := s.UpdateSessionSummary(ctx, ses.ID, summary); err != nil {
		return false, fmt.Errorf("failed to update summary for sibling session %s: %w", ses.ID, err)
	}
	return true, nil
}

func (s service) AddAggregateIDs(ctx context.Context, sessionID string, aggregateIDs []string) error {
	clog.WithGroupFields(ctx, "sessionService.addAggregateIds", clog.Fields{
		"sessionId":      sessionID,
		"aggregateCount": len(aggregateIDs),
	})
	return s.store.AddAggregateIDs(ctx, sessionID, aggregateIDs)
}
