package firestore

import (
	"context"
	"fmt"
	corefireteam "oneTrick/core/fireteam"
	"oneTrick/ptr"
	"oneTrick/set"
	"oneTrick/utils"
	"time"

	"cloud.google.com/go/firestore"
)

const (
	sessionsCollection    = "sessions"
	firestoreInBatchLimit = 30
)

type FireteamSessionStore struct {
	db *firestore.Client
}

var _ corefireteam.SessionStore = (*FireteamSessionStore)(nil)

func NewFireteamSessionStore(db *firestore.Client) *FireteamSessionStore {
	return &FireteamSessionStore{db: db}
}

type activeSessionProjectionDoc struct {
	ID           string    `firestore:"id"`
	UserID       string    `firestore:"userId"`
	CharacterID  string    `firestore:"characterId"`
	StartedAt    time.Time `firestore:"startedAt"`
	CompTracking *struct {
		Enabled bool `firestore:"enabled"`
	} `firestore:"compTracking"`
}

type auditFieldDoc struct {
	ID       string `firestore:"id"`
	Username string `firestore:"username"`
}

type compRankSnapshotDoc struct {
	DivisionName *string    `firestore:"divisionName"`
	Icon         *string    `firestore:"icon"`
	Points       int        `firestore:"points"`
	RecordedAt   *time.Time `firestore:"recordedAt"`
	StepIndex    *int       `firestore:"stepIndex"`
}

type compTrackingDoc struct {
	Enabled    bool                 `firestore:"enabled"`
	End        *compRankSnapshotDoc `firestore:"end"`
	PointDelta *int                 `firestore:"pointDelta"`
	Start      *compRankSnapshotDoc `firestore:"start"`
}

type sessionWeaponSummaryDoc struct {
	Icon  *string `firestore:"icon"`
	Kills *int    `firestore:"kills"`
	Name  *string `firestore:"name"`
}

type companionInstanceDoc struct {
	CharacterID   string  `firestore:"characterId"`
	Class         *string `firestore:"class,omitempty"`
	EmblemURL     *string `firestore:"emblemUrl,omitempty"`
	SessionID     string  `firestore:"sessionId"`
	SharedMatches int     `firestore:"sharedMatches"`
}

type sessionCompanionDoc struct {
	DisplayName   string                 `firestore:"displayName"`
	Losses        int                    `firestore:"losses"`
	Sessions      []companionInstanceDoc `firestore:"sessions"`
	SharedMatches int                    `firestore:"sharedMatches"`
	UserID        string                 `firestore:"userId"`
	Wins          int                    `firestore:"wins"`
}

type sessionSummaryDoc struct {
	Assists      *int                       `firestore:"assists"`
	Companions   *[]sessionCompanionDoc     `firestore:"companions"`
	Deaths       *int                       `firestore:"deaths"`
	KDRatio      *float64                   `firestore:"kdRatio"`
	KDARatio     *float64                   `firestore:"kdaRatio"`
	Kills        *int                       `firestore:"kills"`
	Losses       *int                       `firestore:"losses"`
	ModesPlayed  *[]string                  `firestore:"modesPlayed"`
	TopWeapons   *[]sessionWeaponSummaryDoc `firestore:"topWeapons"`
	TotalMatches *int                       `firestore:"totalMatches"`
	WinRate      *float64                   `firestore:"winRate"`
	Wins         *int                       `firestore:"wins"`
}

type sessionDoc struct {
	AggregateIDs       []string           `firestore:"aggregateIds"`
	CharacterID        string             `firestore:"characterId"`
	CompTracking       *compTrackingDoc   `firestore:"compTracking"`
	CompletedAt        *time.Time         `firestore:"completedAt"`
	CompletedBy        *auditFieldDoc     `firestore:"completedBy"`
	Description        *string            `firestore:"description"`
	ID                 string             `firestore:"id"`
	LastSeenActivityID *string            `firestore:"lastSeenActivityId"`
	LastSeenTimestamp  *time.Time         `firestore:"lastSeenTimestamp"`
	Name               *string            `firestore:"name"`
	StartedAt          time.Time          `firestore:"startedAt"`
	StartedBy          *auditFieldDoc     `firestore:"startedBy"`
	Status             *string            `firestore:"status"`
	Summary            *sessionSummaryDoc `firestore:"sessionSummary"`
	UserID             string             `firestore:"userId"`
}

func (s *FireteamSessionStore) GetActiveByUserIDs(ctx context.Context, userIDs []string) ([]corefireteam.ActiveSession, error) {
	if s.db == nil || len(userIDs) == 0 {
		return []corefireteam.ActiveSession{}, nil
	}

	results := make([]corefireteam.ActiveSession, 0, len(userIDs))
	for _, chunk := range utils.Chunk(userIDs, firestoreInBatchLimit) {
		docs, err := s.db.Collection(sessionsCollection).
			Where("userId", "in", chunk).
			Where("status", "==", string(corefireteam.SessionStatusPending)).
			Select("id", "userId", "characterId", "startedAt", "compTracking.enabled").
			Documents(ctx).
			GetAll()
		if err != nil {
			return nil, fmt.Errorf("failed to query active sessions for users: %w", err)
		}
		for _, doc := range docs {
			var projected activeSessionProjectionDoc
			if err := doc.DataTo(&projected); err != nil {
				return nil, fmt.Errorf("failed to parse active session %s: %w", doc.Ref.ID, err)
			}
			sessionID := projected.ID
			if sessionID == "" {
				sessionID = doc.Ref.ID
			}
			results = append(results, corefireteam.ActiveSession{
				SessionID:   sessionID,
				UserID:      projected.UserID,
				CharacterID: projected.CharacterID,
				StartedAt:   projected.StartedAt,
				TrackComp:   projected.CompTracking != nil && projected.CompTracking.Enabled,
			})
		}
	}
	return results, nil
}

func (s *FireteamSessionStore) GetActiveSessionsByUserIDs(ctx context.Context, userIDs []string) ([]corefireteam.Session, error) {
	if s.db == nil || len(userIDs) == 0 {
		return []corefireteam.Session{}, nil
	}

	results := make([]corefireteam.Session, 0, len(userIDs))
	for _, chunk := range utils.Chunk(userIDs, firestoreInBatchLimit) {
		docs, err := s.db.Collection(sessionsCollection).
			Where("userId", "in", chunk).
			Where("status", "==", string(corefireteam.SessionStatusPending)).
			Documents(ctx).
			GetAll()
		if err != nil {
			return nil, fmt.Errorf("failed to query active session docs for users: %w", err)
		}
		for _, doc := range docs {
			ses, err := decodeSessionSnapshot(doc)
			if err != nil {
				return nil, err
			}
			results = append(results, ses)
		}
	}
	return results, nil
}

func (s *FireteamSessionStore) CreateSessionsIfInactive(
	ctx context.Context,
	sessions []corefireteam.Session,
) ([]corefireteam.CreateSessionResult, error) {
	if len(sessions) == 0 {
		return []corefireteam.CreateSessionResult{}, nil
	}
	if s.db == nil {
		return nil, fmt.Errorf("firestore client not configured")
	}

	userIDSet := set.NewOrdered[string]()
	for _, ses := range sessions {
		if ses.UserID != "" {
			userIDSet.Add(ses.UserID)
		}
	}
	userIDs := userIDSet.ToSlice()

	var results []corefireteam.CreateSessionResult
	err := s.db.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		activeByPair := make(map[corefireteam.SessionKey]corefireteam.Session, len(sessions))
		for _, chunk := range utils.Chunk(userIDs, firestoreInBatchLimit) {
			q := s.db.Collection(sessionsCollection).
				Where("userId", "in", chunk).
				Where("status", "==", string(corefireteam.SessionStatusPending))
			docs, err := tx.Documents(q).GetAll()
			if err != nil {
				return fmt.Errorf("failed to query active sessions in transaction: %w", err)
			}
			for _, doc := range docs {
				ses, err := decodeSessionSnapshot(doc)
				if err != nil {
					return err
				}
				key := ses.Key()
				if prev, exists := activeByPair[key]; exists && !ses.IsNewerThan(prev) {
					continue
				}
				activeByPair[key] = ses
			}
		}

		txResults := make([]corefireteam.CreateSessionResult, len(sessions))
		createdInTx := make(map[corefireteam.SessionKey]corefireteam.Session, len(sessions))
		for i, ses := range sessions {
			key := ses.Key()
			if existing, active := activeByPair[key]; active {
				txResults[i] = corefireteam.CreateSessionResult{Session: existing, Created: false}
				continue
			}
			if existing, dup := createdInTx[key]; dup {
				txResults[i] = corefireteam.CreateSessionResult{Session: existing, Created: false}
				continue
			}
			ref := s.db.Collection(sessionsCollection).NewDoc()
			ses.ID = ref.ID
			if ses.AggregateIDs == nil {
				ses.AggregateIDs = make([]string, 0)
			}
			if err := tx.Set(ref, toSessionDoc(ses)); err != nil {
				return err
			}
			createdInTx[key] = ses
			txResults[i] = corefireteam.CreateSessionResult{Session: ses, Created: true}
		}

		results = txResults
		return nil
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

func decodeSessionSnapshot(doc *firestore.DocumentSnapshot) (corefireteam.Session, error) {
	var raw sessionDoc
	if err := doc.DataTo(&raw); err != nil {
		return corefireteam.Session{}, fmt.Errorf("failed to parse active session %s: %w", doc.Ref.ID, err)
	}
	if raw.ID == "" {
		raw.ID = doc.Ref.ID
	}
	return fromSessionDoc(raw), nil
}

func toSessionDoc(ses corefireteam.Session) sessionDoc {
	aggs := ses.AggregateIDs
	if aggs == nil {
		aggs = make([]string, 0)
	}
	doc := sessionDoc{
		AggregateIDs:       aggs,
		CharacterID:        ses.CharacterID,
		CompTracking:       toCompTrackingDoc(ses.CompTracking),
		CompletedAt:        ses.CompletedAt,
		CompletedBy:        toAuditFieldDoc(ses.CompletedBy),
		Description:        ptr.NonEmpty(ses.Description),
		ID:                 ses.ID,
		LastSeenActivityID: ptr.NonEmpty(ses.LastSeenActivityID),
		LastSeenTimestamp:  ses.LastSeenTimestamp,
		Name:               ptr.NonEmpty(ses.Name),
		StartedAt:          ses.StartedAt,
		StartedBy:          toAuditFieldDoc(ses.StartedBy),
		Summary:            toSessionSummaryDoc(ses.Summary),
		UserID:             ses.UserID,
	}
	if ses.Status != "" {
		status := string(ses.Status)
		doc.Status = &status
	}
	return doc
}

func fromSessionDoc(doc sessionDoc) corefireteam.Session {
	aggs := doc.AggregateIDs
	if aggs == nil {
		aggs = make([]string, 0)
	}
	var status corefireteam.SessionStatus
	if doc.Status != nil {
		status = corefireteam.SessionStatus(*doc.Status)
	}
	return corefireteam.Session{
		ID:                 doc.ID,
		UserID:             doc.UserID,
		CharacterID:        doc.CharacterID,
		Name:               ptr.Deref(doc.Name),
		Description:        ptr.Deref(doc.Description),
		Status:             status,
		StartedAt:          doc.StartedAt,
		CompletedAt:        doc.CompletedAt,
		StartedBy:          fromAuditFieldDoc(doc.StartedBy),
		CompletedBy:        fromAuditFieldDoc(doc.CompletedBy),
		AggregateIDs:       aggs,
		LastSeenActivityID: ptr.Deref(doc.LastSeenActivityID),
		LastSeenTimestamp:  doc.LastSeenTimestamp,
		CompTracking:       fromCompTrackingDoc(doc.CompTracking),
		Summary:            fromSessionSummaryDoc(doc.Summary),
	}
}

func toAuditFieldDoc(a *corefireteam.AuditField) *auditFieldDoc {
	if a == nil {
		return nil
	}
	return &auditFieldDoc{
		ID:       a.ID,
		Username: a.Username,
	}
}

func fromAuditFieldDoc(a *auditFieldDoc) *corefireteam.AuditField {
	if a == nil {
		return nil
	}
	return &corefireteam.AuditField{
		ID:       a.ID,
		Username: a.Username,
	}
}

func toCompTrackingDoc(ct *corefireteam.CompTracking) *compTrackingDoc {
	if ct == nil {
		return nil
	}
	return &compTrackingDoc{
		Enabled:    ct.Enabled,
		Start:      toCompRankSnapshotDoc(ct.Start),
		End:        toCompRankSnapshotDoc(ct.End),
		PointDelta: ct.PointDelta,
	}
}

func fromCompTrackingDoc(ct *compTrackingDoc) *corefireteam.CompTracking {
	if ct == nil {
		return nil
	}
	return &corefireteam.CompTracking{
		Enabled:    ct.Enabled,
		Start:      fromCompRankSnapshotDoc(ct.Start),
		End:        fromCompRankSnapshotDoc(ct.End),
		PointDelta: ct.PointDelta,
	}
}

func toCompRankSnapshotDoc(snap *corefireteam.CompRankSnapshot) *compRankSnapshotDoc {
	if snap == nil {
		return nil
	}
	return &compRankSnapshotDoc{
		Points:       snap.Points,
		StepIndex:    snap.StepIndex,
		DivisionName: ptr.NonEmpty(snap.DivisionName),
		Icon:         ptr.NonEmpty(snap.Icon),
		RecordedAt:   snap.RecordedAt,
	}
}

func fromCompRankSnapshotDoc(snap *compRankSnapshotDoc) *corefireteam.CompRankSnapshot {
	if snap == nil {
		return nil
	}
	return &corefireteam.CompRankSnapshot{
		Points:       snap.Points,
		StepIndex:    snap.StepIndex,
		DivisionName: ptr.Deref(snap.DivisionName),
		Icon:         ptr.Deref(snap.Icon),
		RecordedAt:   snap.RecordedAt,
	}
}

func toSessionSummaryDoc(sum *corefireteam.SessionSummary) *sessionSummaryDoc {
	if sum == nil {
		return nil
	}
	modes := sum.ModesPlayed
	if modes == nil {
		modes = make([]string, 0)
	}
	weapons := make([]sessionWeaponSummaryDoc, 0, len(sum.TopWeapons))
	for _, w := range sum.TopWeapons {
		weapons = append(weapons, sessionWeaponSummaryDoc{
			Name:  ptr.NonEmpty(w.Name),
			Icon:  ptr.NonEmpty(w.Icon),
			Kills: new(w.Kills),
		})
	}
	companions := make([]sessionCompanionDoc, 0, len(sum.Companions))
	for _, c := range sum.Companions {
		sessions := make([]companionInstanceDoc, 0, len(c.Sessions))
		for _, cs := range c.Sessions {
			sessions = append(sessions, companionInstanceDoc{
				SessionID:     cs.SessionID,
				CharacterID:   cs.CharacterID,
				Class:         ptr.NonEmpty(cs.Class),
				EmblemURL:     ptr.NonEmpty(cs.EmblemURL),
				SharedMatches: cs.SharedMatches,
			})
		}
		companions = append(companions, sessionCompanionDoc{
			UserID:        c.UserID,
			DisplayName:   c.DisplayName,
			SharedMatches: c.SharedMatches,
			Wins:          c.Wins,
			Losses:        c.Losses,
			Sessions:      sessions,
		})
	}
	return &sessionSummaryDoc{
		TotalMatches: new(sum.TotalMatches),
		Wins:         new(sum.Wins),
		Losses:       new(sum.Losses),
		Kills:        new(sum.Kills),
		Deaths:       new(sum.Deaths),
		Assists:      new(sum.Assists),
		WinRate:      new(sum.WinRate),
		KDRatio:      new(sum.KDRatio),
		KDARatio:     new(sum.KDARatio),
		ModesPlayed:  &modes,
		TopWeapons:   &weapons,
		Companions:   &companions,
	}
}

func fromSessionSummaryDoc(sum *sessionSummaryDoc) *corefireteam.SessionSummary {
	if sum == nil {
		return nil
	}
	out := &corefireteam.SessionSummary{
		TotalMatches: ptr.Deref(sum.TotalMatches),
		Wins:         ptr.Deref(sum.Wins),
		Losses:       ptr.Deref(sum.Losses),
		Kills:        ptr.Deref(sum.Kills),
		Deaths:       ptr.Deref(sum.Deaths),
		Assists:      ptr.Deref(sum.Assists),
		WinRate:      ptr.Deref(sum.WinRate),
		KDRatio:      ptr.Deref(sum.KDRatio),
		KDARatio:     ptr.Deref(sum.KDARatio),
		ModesPlayed:  make([]string, 0),
		TopWeapons:   make([]corefireteam.SessionWeaponSummary, 0),
		Companions:   make([]corefireteam.SessionCompanion, 0),
	}
	if sum.ModesPlayed != nil {
		out.ModesPlayed = append(out.ModesPlayed, (*sum.ModesPlayed)...)
	}
	if sum.TopWeapons != nil {
		out.TopWeapons = make([]corefireteam.SessionWeaponSummary, 0, len(*sum.TopWeapons))
		for _, w := range *sum.TopWeapons {
			out.TopWeapons = append(out.TopWeapons, corefireteam.SessionWeaponSummary{
				Name:  ptr.Deref(w.Name),
				Icon:  ptr.Deref(w.Icon),
				Kills: ptr.Deref(w.Kills),
			})
		}
	}
	if sum.Companions != nil {
		out.Companions = make([]corefireteam.SessionCompanion, 0, len(*sum.Companions))
		for _, c := range *sum.Companions {
			sessions := make([]corefireteam.CompanionInstance, 0, len(c.Sessions))
			for _, cs := range c.Sessions {
				sessions = append(sessions, corefireteam.CompanionInstance{
					SessionID:     cs.SessionID,
					CharacterID:   cs.CharacterID,
					Class:         ptr.Deref(cs.Class),
					EmblemURL:     ptr.Deref(cs.EmblemURL),
					SharedMatches: cs.SharedMatches,
				})
			}
			out.Companions = append(out.Companions, corefireteam.SessionCompanion{
				UserID:        c.UserID,
				DisplayName:   c.DisplayName,
				SharedMatches: c.SharedMatches,
				Wins:          c.Wins,
				Losses:        c.Losses,
				Sessions:      sessions,
			})
		}
	}
	return out
}
