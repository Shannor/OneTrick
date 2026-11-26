package fireteam

import (
	"context"
	"errors"
	"fmt"
	"oneTrick/generator"
	"oneTrick/set"
	"strconv"
	"strings"
	"time"
)

var (
	ErrUserNotFound       = errors.New("user not found")
	ErrDestinyUnavailable = errors.New("destiny 2 servers unavailable")
	ErrNotInFireteam      = errors.New("target user is not in caller's fireteam")
	ErrInvalidRequest     = errors.New("invalid request")
	ErrAlreadyActive      = errors.New("session already active")
	ErrUnauthorized       = errors.New("unauthorized")
)

type Color struct {
	Red   int
	Green int
	Blue  int
	Alpha int
}

type ClassStat struct {
	Name            string
	Description     string
	Icon            string
	HasIcon         bool
	StatCategory    int
	AggregationType int
	Value           int32
}

type Character struct {
	ID                  string
	Class               string
	Race                string
	CurrentTitle        string
	Light               int64
	EmblemURL           string
	EmblemBackgroundURL string
	EmblemColor         Color
	Stats               map[string]ClassStat
}

type Membership struct {
	ID          string
	Type        int64
	DisplayName string
}

type Player struct {
	ID                  string
	MemberID            string
	PrimaryMembershipID string
	DisplayName         string
	Memberships         []Membership
	Characters          []Character
}

func (p Player) PrimaryMembership() (int64, int64, error) {
	if p.PrimaryMembershipID == "" {
		return 0, 0, errors.New("user missing primary membership id")
	}
	membershipType := int64(0)
	for _, membership := range p.Memberships {
		if membership.ID == p.PrimaryMembershipID {
			membershipType = membership.Type
			break
		}
	}
	if membershipType == 0 && len(p.Memberships) > 0 {
		membershipType = p.Memberships[0].Type
	}
	if membershipType == 0 {
		return 0, 0, errors.New("user missing valid membership type")
	}
	pmID, err := strconv.ParseInt(p.PrimaryMembershipID, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to parse primary membership id: %w", err)
	}
	return pmID, membershipType, nil
}

func (p Player) AuditField(fallbackID string) AuditField {
	id := p.ID
	if id == "" {
		id = fallbackID
	}
	return AuditField{
		ID:       id,
		Username: p.DisplayName,
	}
}

func (p Player) MatchesAnyMembership(ids *set.Set[string]) bool {
	if id := strings.TrimSpace(p.PrimaryMembershipID); id != "" && ids.Contains(id) {
		return true
	}
	if id := strings.TrimSpace(p.MemberID); id != "" && ids.Contains(id) {
		return true
	}
	for _, m := range p.Memberships {
		if id := strings.TrimSpace(m.ID); id != "" && ids.Contains(id) {
			return true
		}
	}
	return false
}

type ActiveSession struct {
	SessionID   string
	UserID      string
	CharacterID string
	StartedAt   time.Time
	TrackComp   bool
}

type SessionStatus string

const (
	SessionStatusPending  SessionStatus = "pending"
	SessionStatusComplete SessionStatus = "complete"
)

type AuditField struct {
	ID       string
	Username string
}

type CompRankSnapshot struct {
	Points       int
	StepIndex    *int
	DivisionName string
	Icon         string
	RecordedAt   *time.Time
}

type CompTracking struct {
	Enabled    bool
	Start      *CompRankSnapshot
	End        *CompRankSnapshot
	PointDelta *int
}

type SessionWeaponSummary struct {
	Name  string
	Icon  string
	Kills int
}

type CompanionInstance struct {
	SessionID     string
	CharacterID   string
	Class         string
	EmblemURL     string
	SharedMatches int
}

type SessionCompanion struct {
	UserID        string
	DisplayName   string
	SharedMatches int
	Wins          int
	Losses        int
	Sessions      []CompanionInstance
}

type SessionSummary struct {
	TotalMatches int
	Wins         int
	Losses       int
	Kills        int
	Deaths       int
	Assists      int
	WinRate      float64
	KDRatio      float64
	KDARatio     float64
	ModesPlayed  []string
	TopWeapons   []SessionWeaponSummary
	Companions   []SessionCompanion
}

type SessionKey struct {
	UserID      string
	CharacterID string
}

type Session struct {
	ID                 string
	UserID             string
	CharacterID        string
	Name               string
	Description        string
	Status             SessionStatus
	StartedAt          time.Time
	CompletedAt        *time.Time
	StartedBy          *AuditField
	CompletedBy        *AuditField
	AggregateIDs       []string
	LastSeenActivityID string
	LastSeenTimestamp  *time.Time
	CompTracking       *CompTracking
	Summary            *SessionSummary
}

func newPendingSession(userID, characterID string, startedBy AuditField, startedAt time.Time) Session {
	return Session{
		UserID:       userID,
		CharacterID:  characterID,
		Name:         generator.SessionName(),
		Status:       SessionStatusPending,
		StartedAt:    startedAt,
		StartedBy:    new(startedBy),
		AggregateIDs: make([]string, 0),
	}
}

func (s Session) Key() SessionKey {
	return SessionKey{UserID: s.UserID, CharacterID: s.CharacterID}
}

func (s Session) IsNewerThan(other Session) bool {
	if s.StartedAt.After(other.StartedAt) {
		return true
	}
	return s.StartedAt.Equal(other.StartedAt) && s.ID < other.ID
}

func (s Session) WithCompTracking(start *CompRankSnapshot) Session {
	s.CompTracking = &CompTracking{
		Enabled: true,
		Start:   start,
	}
	return s
}

type StartSessionTarget struct {
	UserID      string
	CharacterID string
}

func (t StartSessionTarget) Normalized() (StartSessionTarget, error) {
	uID := strings.TrimSpace(t.UserID)
	cID := strings.TrimSpace(t.CharacterID)
	if uID == "" || cID == "" {
		return StartSessionTarget{}, fmt.Errorf("%w: each member must include userId and characterId", ErrInvalidRequest)
	}
	return StartSessionTarget{UserID: uID, CharacterID: cID}, nil
}

func (t StartSessionTarget) Key() SessionKey {
	return SessionKey{UserID: t.UserID, CharacterID: t.CharacterID}
}

type StartSessionOutcome string

const (
	StartSessionOutcomeStarted       StartSessionOutcome = "started"
	StartSessionOutcomeSkippedActive StartSessionOutcome = "skipped_active"
)

type StartSessionResult struct {
	UserID      string
	CharacterID string
	Outcome     StartSessionOutcome
	Session     Session
}

type Member struct {
	ID             string
	MembershipID   string
	DisplayName    string
	Characters     []Character
	ActiveSessions []ActiveSession
}

type UserReader interface {
	GetPlayer(ctx context.Context, userID string) (Player, error)
	FindPlayersByPartyMembershipIDs(ctx context.Context, membershipIDs []string) ([]Player, error)
}

type PartyReader interface {
	GetPartyMembershipIDs(ctx context.Context, primaryMembershipID int64, membershipType int64) ([]string, error)
}

type CreateSessionResult struct {
	Session Session
	Created bool
}

type SessionStore interface {
	GetActiveByUserIDs(ctx context.Context, userIDs []string) ([]ActiveSession, error)
	GetActiveSessionsByUserIDs(ctx context.Context, userIDs []string) ([]Session, error)
	CreateSessionsIfInactive(ctx context.Context, sessions []Session) ([]CreateSessionResult, error)
}

type CompRankReader interface {
	GetCompetitiveRank(ctx context.Context, primaryMembershipID int64, membershipType int64, characterID string) (CompRankSnapshot, bool, error)
}
