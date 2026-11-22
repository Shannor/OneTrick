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
	"time"

	"cloud.google.com/go/firestore"
	clog "github.com/Shannor/canonical-logger"
)

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
	emptyCleanupLookback     = 60 * time.Minute
	activityStartGracePeriod = 15 * time.Minute
	snapshotHeartbeatPeriod  = 30 * time.Minute
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
