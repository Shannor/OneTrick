package telemetry

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	DefaultLimit = 500
	MaxLimit     = 2000
)

var (
	ErrNotFound     = errors.New("not found")
	ErrInvalidQuery = errors.New("invalid query")
)

type GameMode string

const (
	GameModeAll         GameMode = "allGameModes"
	GameModeQuickPlay   GameMode = "quickplay"
	GameModeCompetitive GameMode = "competitive"
	GameModeTrials      GameMode = "trials"
	GameModeIronBanner  GameMode = "ironBanner"
)

func (g GameMode) IsValid() bool {
	switch g {
	case GameModeAll, GameModeQuickPlay, GameModeCompetitive, GameModeTrials, GameModeIronBanner:
		return true
	default:
		return false
	}
}

type ConfidenceLevel string

const (
	ConfidenceNotFound ConfidenceLevel = "notFound"
	ConfidenceNoMatch  ConfidenceLevel = "noMatch"
	ConfidenceLow      ConfidenceLevel = "low"
	ConfidenceMedium   ConfidenceLevel = "medium"
	ConfidenceHigh     ConfidenceLevel = "high"
)

// IsValidSnapshotLink reports whether a link's confidence represents an actual loadout match.
// Empty confidenceLevel is allowed because legacy aggregates and merge-reassigned links can
// store a valid snapshotId without populating confidenceLevel.
func (c ConfidenceLevel) IsValidSnapshotLink() bool {
	switch c {
	case "", ConfidenceLow, ConfidenceMedium, ConfidenceHigh:
		return true
	default:
		return false
	}
}

type Query struct {
	UserID      string
	CharacterID string
	From        time.Time
	To          time.Time
	GameMode    GameMode
	SnapshotID  string
	Limit       int
}

func (q Query) NormalizeAndValidate() (Query, error) {
	q.UserID = strings.TrimSpace(q.UserID)
	q.CharacterID = strings.TrimSpace(q.CharacterID)
	q.SnapshotID = strings.TrimSpace(q.SnapshotID)

	if q.UserID == "" {
		return Query{}, fmt.Errorf("%w: userId is required", ErrInvalidQuery)
	}
	if q.CharacterID == "" {
		return Query{}, fmt.Errorf("%w: characterId is required", ErrInvalidQuery)
	}
	if !q.From.IsZero() && !q.To.IsZero() && q.From.After(q.To) {
		return Query{}, fmt.Errorf("%w: from must not be after to", ErrInvalidQuery)
	}
	if q.GameMode == "" {
		q.GameMode = GameModeAll
	}
	if !q.GameMode.IsValid() {
		return Query{}, fmt.Errorf("%w: unsupported gameMode %q", ErrInvalidQuery, q.GameMode)
	}
	if q.Limit == 0 {
		q.Limit = DefaultLimit
	}
	if q.Limit < 1 || q.Limit > MaxLimit {
		return Query{}, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidQuery, MaxLimit)
	}
	return q, nil
}

type User struct {
	ID           string
	CharacterIDs []string
}

// OwnsCharacter reports whether characterID belongs to the user.
// Legacy user docs without cached character IDs allow lookups rather than returning a false 404.
func (u User) OwnsCharacter(characterID string) bool {
	if len(u.CharacterIDs) == 0 {
		return true
	}
	return slices.Contains(u.CharacterIDs, characterID)
}

type Snapshot struct {
	ID           string
	UserID       string
	CharacterID  string
	Name         string
	MergedIntoID string
}

func (s Snapshot) IsArchived() bool {
	return s.MergedIntoID != ""
}

type SnapshotSummary struct {
	ID   string
	Name string
}

type RawMatch struct {
	ActivityID              string
	Period                  time.Time
	Location                string
	Activity                string
	Mode                    string
	StoredEstimatedGameMode GameMode
	Kills                   int
	Deaths                  int
	Assists                 int
	Won                     bool
	TimePlayedSeconds       int
	HasTimePlayedSeconds    bool
	SessionID               string
	RawSnapshotID           string
	SnapshotConfidence      ConfidenceLevel
}

type Match struct {
	ActivityID           string
	Period               time.Time
	Location             string
	Activity             string
	EstimatedGameMode    GameMode
	Kills                int
	Deaths               int
	Assists              int
	Won                  bool
	TimePlayedSeconds    int
	HasTimePlayedSeconds bool
	SessionID            string
	SnapshotID           string
	SnapshotConfidence   ConfidenceLevel
}

type Result struct {
	Matches    []Match
	Snapshots  map[string]SnapshotSummary
	TotalCount int
	Truncated  bool
}
