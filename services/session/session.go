package session

import (
	"context"
	"fmt"
	"log/slog"
	"oneTrick/api"
	"oneTrick/generator"
	"oneTrick/ptr"
	"oneTrick/services/aggregate"
	"oneTrick/utils"
	"slices"
	"time"

	"cloud.google.com/go/firestore"
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

	return result, nil
}

func (s service) HasActive(ctx context.Context, userID string, characterID string) (bool, error) {
	query := s.db.Collection(collection).
		Where("userId", "==", userID).
		Where("characterId", "==", characterID).
		Where("status", "==", api.SessionPending).
		Limit(1)

	docs, err := query.Documents(ctx).GetAll()
	if err != nil {
		return false, err
	}
	return len(docs) > 0, nil
}

func (s service) GetActive(ctx context.Context, userID string, characterID string) (*api.Session, error) {
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
	return result, nil
}

func (s service) Get(ctx context.Context, ID string) (*api.Session, error) {
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
	query := s.db.Collection(collection).Query

	if userID != nil && *userID != "" {
		query = query.Where("userId", "==", *userID)
	}
	if characterID != nil && *characterID != "" {
		query = query.Where("characterId", "==", *characterID)
	}
	if status != nil && *status != "" {
		query = query.Where("status", "==", *status)
	}

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
	if len(updates) == 0 {
		slog.With("sessionID", sessionID).Warn("no updates to session")
		return nil
	}
	_, err := ref.Update(ctx, updates)
	if err != nil {
		return fmt.Errorf("failed to update session: %w", err)
	}

	return nil
}
func (s service) Complete(ctx context.Context, ID string) error {
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
		slog.With("sessionID", ID).Warn("session already completed")
	}

	return nil
}

func (s service) SetLastActivity(ctx context.Context, ID, activityID string) error {
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
	slog.Info("Initiating session deletion", "sessionID", sessionID, "userID", userID)
	docRef := s.db.Collection(collection).Doc(sessionID)
	doc, err := docRef.Get(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			slog.Warn("Session not found for deletion", "sessionID", sessionID, "userID", userID)
			return ErrNotFound
		}
		slog.Error("Failed to fetch session document for deletion", "sessionID", sessionID, "userID", userID, "error", err)
		return fmt.Errorf("failed to get session: %w", err)
	}

	var ses api.Session
	if err := doc.DataTo(&ses); err != nil {
		slog.Error("Failed to parse session data for deletion", "sessionID", sessionID, "error", err)
		return fmt.Errorf("failed to parse session: %w", err)
	}

	if ses.UserID != userID {
		slog.Warn("Unauthorized attempt to delete session", "sessionID", sessionID, "requestUserID", userID, "sessionUserID", ses.UserID)
		return ErrUnauthorized
	}

	// Remove session references from aggregates
	if err := s.aggregateService.RemoveSession(ctx, sessionID); err != nil {
		slog.Error("Failed to remove session references from aggregates", "sessionID", sessionID, "error", err)
		return fmt.Errorf("failed to clean up session aggregates: %w", err)
	}
	slog.Info("Successfully cleaned up aggregate references for session", "sessionID", sessionID)

	// Delete session document
	if _, err := docRef.Delete(ctx); err != nil {
		slog.Error("Failed to delete session document", "sessionID", sessionID, "userID", userID, "error", err)
		return fmt.Errorf("failed to delete session: %w", err)
	}

	slog.Info("Successfully deleted session document", "sessionID", sessionID, "userID", userID)
	return nil
}

func (s service) AddAggregateIDs(ctx context.Context, sessionID string, aggregateIDs []string) error {
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
