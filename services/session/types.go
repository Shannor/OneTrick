package session

import (
	"oneTrick/api"
	"slices"
	"strings"
	"time"
)

type ActiveSession struct {
	SessionID   string
	UserID      string
	CharacterID string
	StartedAt   time.Time
	TrackComp   bool
}

type TickCandidate struct {
	Session                api.Session
	UpdatedAt              *time.Time
	LastSnapshotID         string
	LastSnapshotHash       string
	LastSnapshotRecordedAt time.Time
}

func sortActiveSessions(sessions []ActiveSession) {
	slices.SortFunc(sessions, func(a, b ActiveSession) int {
		if cmp := b.StartedAt.Compare(a.StartedAt); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.SessionID, b.SessionID)
	})
}
