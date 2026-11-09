package session

import (
	"context"
	"fmt"
	"oneTrick/api"
	"oneTrick/generator"
	"oneTrick/ptr"
	"oneTrick/services/aggregate"
	"oneTrick/utils"
	"slices"
	"time"

	"cloud.google.com/go/firestore"
	clog "github.com/Shannor/canonical-logger"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Service interface {
	Start(ctx context.Context, userID string, characterID string, startedBy api.AuditField) (*api.Session, error)
	Update(ctx context.Context, sessionID string, name, description string) error
	AddAggregateIDs(ctx context.Context, sessionID string, aggregateIDs []string) error
	Get(ctx context.Context, ID string) (*api.Session, error)
	GetActive(ctx context.Context, userID string, characterID string) (*api.Session, error)
	GetAll(ctx context.Context, userID *string, characterID *string, status *api.SessionStatus, count int, offset int) ([]api.Session, error)
	Complete(ctx context.Context, ID string) error
	SetLastActivity(ctx context.Context, ID, activityID string) error
	Delete(ctx context.Context, sessionID string, userID string) error
}

var (
	ErrNotFound     = fmt.Errorf("session not found")
	ErrUnauthorized = fmt.Errorf("unauthorized")
)

type service struct {
	db               *firestore.Client
	aggregateService aggregate.Service
}

var _ Service = (*service)(nil)

func NewService(db *firestore.Client, aggregateService aggregate.Service) Service {
	return &service{
		db:               db,
		aggregateService: aggregateService,
	}
}

const (
	collection = "sessions"
)

func (s service) Start(ctx context.Context, userID string, characterID string, startedBy api.AuditField) (*api.Session, error) {
	clog.WithGroupFields(ctx, "sessionService.start", clog.Fields{
		"userId":      userID,
		"characterId": characterID,
		"startedById": startedBy.ID,
	})
	if ok, err := s.HasActive(ctx, userID, characterID); ok || err != nil {
		return nil, fmt.Errorf("session already active")
	}
	result := &api.Session{
		UserID:       userID,
		StartedAt:    time.Now(),
		CharacterID:  characterID,
		Name:         ptr.Of(generator.SessionName()),
		AggregateIDs: make([]string, 0),
		Status:       ptr.Of(api.SessionPending),
		StartedBy:    &startedBy,
	}
	ref := s.db.Collection(collection).NewDoc()
	result.ID = ref.ID
	_, err := ref.Set(ctx, result)
	if err != nil {
		return nil, err
	}

	clog.WithGroupField(ctx, "sessionService.start", "sessionId", result.ID)
	return result, nil
}

func (s service) HasActive(ctx context.Context, userID string, characterID string) (bool, error) {
	clog.WithGroupFields(ctx, "sessionService.hasActive", clog.Fields{
		"userId":      userID,
		"characterId": characterID,
	})
	query := s.db.Collection(collection).
		Where("userId", "==", userID).
		Where("characterId", "==", characterID).
		Where("status", "==", api.SessionPending).
		Limit(1)

	docs, err := query.Documents(ctx).GetAll()
	if err != nil {
		return false, err
	}
	hasActive := len(docs) > 0
	clog.WithGroupField(ctx, "sessionService.hasActive", "hasActive", hasActive)
	return hasActive, nil
}

func (s service) GetActive(ctx context.Context, userID string, characterID string) (*api.Session, error) {
	clog.WithGroupFields(ctx, "sessionService.getActive", clog.Fields{
		"userId":      userID,
		"characterId": characterID,
	})
	query := s.db.Collection(collection).
		Where("userId", "==", userID).
		Where("characterId", "==", characterID).
		Where("status", "==", api.SessionPending).
		Limit(1)

	docs, err := query.Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, fmt.Errorf("no active session found")
	}
	result := &api.Session{}
	err = docs[0].DataTo(&result)
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
	doc, err := s.db.Collection(collection).Doc(ID).Get(ctx)
	if err != nil {
		return nil, err
	}
	result := &api.Session{}
	err = doc.DataTo(&result)
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
	query := s.db.Collection(collection).Query

	if userID != nil && *userID != "" {
		query = query.Where("userId", "==", *userID)
		fields["userId"] = *userID
	}
	if characterID != nil && *characterID != "" {
		query = query.Where("characterId", "==", *characterID)
		fields["characterId"] = *characterID
	}
	if status != nil && *status != "" {
		query = query.Where("status", "==", *status)
		fields["status"] = string(*status)
	}
	clog.WithGroupFields(ctx, "sessionService.getAll", fields)

	docs, err := query.Documents(ctx).GetAll()
	if err != nil {
		return nil, fmt.Errorf("failed to query sessions: %w", err)
	}

	result, err := utils.GetAllToStructs[api.Session](docs)
	if err != nil {
		return nil, fmt.Errorf("failed to parse sessions: %w", err)
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
	ref := s.db.Collection(collection).Doc(sessionID)

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
	_, err := ref.Update(ctx, updates)
	if err != nil {
		return fmt.Errorf("failed to update session: %w", err)
	}

	return nil
}
func (s service) Complete(ctx context.Context, ID string) error {
	clog.WithGroupField(ctx, "sessionService.complete", "sessionId", ID)
	ref := s.db.Collection(collection).Doc(ID)

	data, err := ref.Get(ctx)
	if err != nil {
		return fmt.Errorf("failed to get session: %w", err)
	}
	session := api.Session{}
	err = data.DataTo(&session)
	if err != nil {
		return fmt.Errorf("failed to get session: %w", err)
	}
	if session.CompletedAt == nil {
		_, err := ref.Update(ctx, []firestore.Update{
			{
				Path:  "completedAt",
				Value: time.Now(),
			},
			{
				Path:  "status",
				Value: api.SessionComplete,
			},
		})
		if err != nil {
			return fmt.Errorf("failed to complete session: %w", err)
		}
	} else {
		clog.WithGroupField(ctx, "sessionService.complete", "warning", "session already completed")
	}

	return nil
}

func (s service) SetLastActivity(ctx context.Context, ID, activityID string) error {
	clog.WithGroupFields(ctx, "sessionService.setLastActivity", clog.Fields{
		"sessionId":  ID,
		"activityId": activityID,
	})
	_, err := s.db.Collection(collection).Doc(ID).Update(ctx, []firestore.Update{
		{
			Path:  "lastSeenActivityId",
			Value: activityID,
		},
		{
			Path:  "lastSeenTimestamp",
			Value: time.Now(),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to update session: %v", err)
	}
	return nil
}

func (s service) Delete(ctx context.Context, sessionID string, userID string) error {
	clog.WithGroupFields(ctx, "sessionService.delete", clog.Fields{
		"sessionId": sessionID,
		"userId":    userID,
	})
	docRef := s.db.Collection(collection).Doc(sessionID)
	doc, err := docRef.Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			clog.WithGroupField(ctx, "sessionService.delete", "warning", "session not found for deletion")
			return ErrNotFound
		}
		return fmt.Errorf("failed to get session: %w", err)
	}

	var ses api.Session
	if err := doc.DataTo(&ses); err != nil {
		return fmt.Errorf("failed to parse session: %w", err)
	}

	if ses.UserID != userID {
		clog.WithGroupFields(ctx, "sessionService.delete", clog.Fields{
			"sessionUserId": ses.UserID,
			"warning":       "unauthorized attempt to delete session",
		})
		return ErrUnauthorized
	}

	// Remove session references from aggregates
	if err := s.aggregateService.RemoveSession(ctx, sessionID); err != nil {
		return fmt.Errorf("failed to clean up session aggregates: %w", err)
	}

	// Delete session document
	if _, err := docRef.Delete(ctx); err != nil {
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
	ids := make([]any, 0)
	for _, d := range aggregateIDs {
		ids = append(ids, d)
	}
	_, err := s.db.Collection(collection).Doc(sessionID).Update(ctx, []firestore.Update{
		{
			Path:  "aggregateIds",
			Value: firestore.ArrayUnion(ids...),
		},
	})
	if err != nil {
		return err
	}
	return nil
}
